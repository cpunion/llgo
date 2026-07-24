/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package cl

import (
	"fmt"
	"go/ast"
	"go/types"
	"strings"

	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

const coroWorkerCgoThunkPrefixV1 = "__llgo_coro_worker_cgo_thunk_v1_"

// CoroCgoWorkerCallCertificate binds one managed source call to the exact
// compiler-generated synchronous cgo adapter that may execute on a bounded
// worker. The certificate authorizes only this call-site replacement. It does
// not classify arbitrary C code as callback-free, cancellable, or safe to run
// on an executor.
type CoroCgoWorkerCallCertificate struct {
	ID             string
	TargetIdentity string
	ABISignature   string
}

type coroWorkerCgoCallShape struct {
	target      *ssa.Function
	signature   *types.Signature
	record      *types.Struct
	argc        int
	result      types.Type
	resultField int
	certificate CoroCgoWorkerCallCertificate
}

func isGeneratedCgoWorkerAdapterName(name string) bool {
	return isCgoCfunc(name) || isCgoCmacro(name) || isCgoCMalloc(name)
}

func exactCgoUnsafeArgsDirective(fn *ssa.Function) (bool, error) {
	if fn == nil {
		return false, nil
	}
	decl, _ := fn.Syntax().(*ast.FuncDecl)
	if decl == nil || decl.Doc == nil {
		return false, nil
	}
	present := false
	for _, comment := range decl.Doc.List {
		if comment == nil {
			continue
		}
		text := strings.TrimSpace(comment.Text)
		if text != "//go:cgo_unsafe_args" && !strings.HasPrefix(text, "//go:cgo_unsafe_args ") {
			continue
		}
		if text != "//go:cgo_unsafe_args" {
			return false, fmt.Errorf("//go:cgo_unsafe_args accepts no arguments")
		}
		if present {
			return false, fmt.Errorf("duplicate //go:cgo_unsafe_args directive")
		}
		present = true
	}
	return present, nil
}

// verifyCoroCMallocWrapperShape recognizes the one Go 1.26 cgo wrapper which
// intentionally lacks //go:cgo_unsafe_args. It is still safe to use as a
// bounded-worker raw adapter because its complete evaluated body is a scalar
// ChangeType followed by one call to the separately verified
// //go:cgo_unsafe_args _cgo_cmalloc adapter and one return.
func (ctx *context) verifyCoroCMallocWrapperShape(fn *ssa.Function) error {
	if ctx == nil || ctx.emissionUniverse == nil || fn == nil || fn.Name() != "_Cfunc__CMalloc" ||
		fn.Parent() != nil || len(fn.FreeVars) != 0 || len(fn.Blocks) != 1 ||
		fn.Signature == nil || fn.Signature.Recv() != nil || fn.Signature.Variadic() ||
		fn.Signature.Params().Len() != 1 || fn.Signature.Results().Len() != 1 {
		return fmt.Errorf("_Cfunc__CMalloc is not the exact Go 1.26 scalar wrapper")
	}
	paramBasic, paramOK := types.Unalias(fn.Signature.Params().At(0).Type()).Underlying().(*types.Basic)
	resultBasic, resultOK := types.Unalias(fn.Signature.Results().At(0).Type()).Underlying().(*types.Basic)
	if !paramOK || paramBasic.Kind() != types.Uint64 ||
		!resultOK || resultBasic.Kind() != types.UnsafePointer {
		return fmt.Errorf("_Cfunc__CMalloc requires exact func(uint64-like) unsafe.Pointer ABI")
	}

	var conversion *ssa.ChangeType
	var call *ssa.Call
	var ret *ssa.Return
	for _, instruction := range fn.Blocks[0].Instrs {
		switch instruction := instruction.(type) {
		case *ssa.DebugRef:
			continue
		case *ssa.ChangeType:
			if conversion != nil {
				return fmt.Errorf("_Cfunc__CMalloc has multiple scalar conversions")
			}
			conversion = instruction
		case *ssa.Call:
			if call != nil {
				return fmt.Errorf("_Cfunc__CMalloc has multiple calls")
			}
			call = instruction
		case *ssa.Return:
			if ret != nil {
				return fmt.Errorf("_Cfunc__CMalloc has multiple returns")
			}
			ret = instruction
		default:
			return fmt.Errorf("_Cfunc__CMalloc has unsupported SSA instruction %T", instruction)
		}
	}
	if conversion == nil || call == nil || ret == nil ||
		conversion.X != fn.Params[0] || len(call.Call.Args) != 1 ||
		call.Call.Args[0] != conversion || len(ret.Results) != 1 ||
		ret.Results[0] != call {
		return fmt.Errorf("_Cfunc__CMalloc does not preserve the exact convert-call-return dataflow")
	}
	converted, ok := types.Unalias(conversion.Type()).Underlying().(*types.Basic)
	if !ok || converted.Kind() != types.Uint64 {
		return fmt.Errorf("_Cfunc__CMalloc conversion result is not uint64")
	}
	target := call.Call.StaticCallee()
	if target == nil || target.Name() != "_cgo_cmalloc" ||
		!types.Identical(call.Type(), fn.Signature.Results().At(0).Type()) {
		return fmt.Errorf("_Cfunc__CMalloc does not call the exact _cgo_cmalloc adapter")
	}
	marker, err := exactCgoUnsafeArgsDirective(target)
	if err != nil {
		return fmt.Errorf("_cgo_cmalloc directive: %w", err)
	}
	if !marker {
		return fmt.Errorf("_cgo_cmalloc lacks //go:cgo_unsafe_args")
	}
	lowering, err := ctx.emissionUniverse.cgoLoweringPlan(ctx, fn)
	if err != nil {
		return err
	}
	if len(lowering.calls) != 1 || lowering.calls[0].call != call ||
		!lowering.calls[0].compiled {
		return fmt.Errorf("_Cfunc__CMalloc is outside the exact dedicated cgo lowering")
	}
	return nil
}

// verifyCoroRawCgocallShape proves the narrow synchronous half of the
// generated-cgo protocol. Call-site classification remains centralized in
// classifyCoroIntrinsicCallSite; this helper verifies only the generated
// adapter shape. It never licenses a managed caller to invoke C on an
// executor: only the dedicated raw _Cfunc_* lowering may inline this compiler
// intrinsic, while freezeCoroCgoWorkerCallCertificate replaces the outer
// managed call with a worker transaction.
func (u *EmissionUniverse) verifyCoroRawCgocallShape(
	ctx *context,
	call *ssa.Call,
) error {
	const shape = "func(unsafe.Pointer, uintptr) int32"
	if u == nil || ctx == nil || call == nil || call.Common() == nil ||
		call.Common().IsInvoke() || call.Common().Method != nil {
		return fmt.Errorf("_cgo_runtime_cgocall requires an exact direct call in a generated cgo adapter")
	}
	parent := call.Parent()
	if parent == nil || ctx.goFn != parent || parent.Parent() != nil ||
		!isGeneratedCgoWorkerAdapterName(parent.Name()) || isCgoC2func(parent.Name()) {
		return fmt.Errorf("_cgo_runtime_cgocall is synchronous only inside one exact generated cgo adapter")
	}
	marker, err := exactCgoUnsafeArgsDirective(parent)
	if err != nil {
		return fmt.Errorf("generated cgo adapter %q directive: %w", parent.Name(), err)
	}
	if !marker {
		return fmt.Errorf("generated cgo adapter %q lacks //go:cgo_unsafe_args", parent.Name())
	}
	directive, err := coroRawABIDirective(parent, u)
	if err != nil {
		return err
	}
	if directive != "//go:cgo_unsafe_args" {
		return fmt.Errorf("generated cgo adapter %q has raw ABI directive %q", parent.Name(), directive)
	}
	background, classified, err := u.FunctionBackground(parent)
	if err != nil {
		return err
	}
	if !classified || background != llssa.InGo {
		return fmt.Errorf("generated cgo adapter %q is not one exact Go body", parent.Name())
	}
	common := call.Common()
	signature := common.Signature()
	if signature == nil || signature.Recv() != nil || signature.Variadic() ||
		signature.Params() == nil || signature.Params().Len() != 2 ||
		signature.Results() == nil || signature.Results().Len() != 1 ||
		len(common.Args) != 2 {
		return fmt.Errorf("_cgo_runtime_cgocall in %q requires the exact %s shape", parent.Name(), shape)
	}
	basicKind := func(typ types.Type, kind types.BasicKind) bool {
		basic, ok := types.Unalias(typ).Underlying().(*types.Basic)
		return ok && basic.Kind() == kind
	}
	if !basicKind(signature.Params().At(0).Type(), types.UnsafePointer) ||
		!basicKind(signature.Params().At(1).Type(), types.Uintptr) ||
		!basicKind(signature.Results().At(0).Type(), types.Int32) ||
		!types.Identical(common.Args[0].Type(), signature.Params().At(0).Type()) ||
		!types.Identical(common.Args[1].Type(), signature.Params().At(1).Type()) {
		return fmt.Errorf("_cgo_runtime_cgocall in %q requires the exact %s shape", parent.Name(), shape)
	}
	lowering, err := u.cgoLoweringPlan(ctx, parent)
	if err != nil {
		return err
	}
	found := false
	compiled := !isCgoCmacro(parent.Name())
	for _, candidate := range lowering.calls {
		if candidate.call != call {
			continue
		}
		if candidate.compiled != compiled {
			return fmt.Errorf(
				"_cgo_runtime_cgocall in %q has dedicated cgo compiled=%t, want %t",
				parent.Name(), candidate.compiled, compiled,
			)
		}
		if found {
			return fmt.Errorf("_cgo_runtime_cgocall in %q has duplicate dedicated lowering records", parent.Name())
		}
		found = true
	}
	if !found {
		return fmt.Errorf("_cgo_runtime_cgocall in %q is outside the dedicated cgo lowering block", parent.Name())
	}
	return nil
}

func (u *EmissionUniverse) freezeCoroCgoWorkerCallCertificate(
	ctx *context,
	call ssa.CallInstruction,
) (certificate CoroCgoWorkerCallCertificate, target *ssa.Function, certified bool, err error) {
	if u == nil || ctx == nil || call == nil || call.Common() == nil || !u.CoroWorkerSupported() {
		return certificate, nil, false, nil
	}
	switch call.(type) {
	case *ssa.Call, *ssa.Defer:
	default:
		// A go statement already transfers execution to a new logical task.
		// It must not also park its source owner on a bounded worker.
		return certificate, nil, false, nil
	}
	// A generated adapter is already the synchronous transaction body executed
	// by the bounded worker. Calls inside that body stay ordinary raw calls;
	// recursively replacing them with another worker transaction would both
	// park a native worker and incorrectly color the adapter as managed.
	if owner := call.Parent(); owner != nil && isGeneratedCgoWorkerAdapterName(owner.Name()) {
		return certificate, nil, false, nil
	}
	common := call.Common()
	raw := common.StaticCallee()
	if raw == nil || common.IsInvoke() || common.Method != nil {
		return certificate, nil, false, nil
	}
	target, resolved := u.Resolve(raw)
	if !resolved || target == nil || target != raw || !isGeneratedCgoWorkerAdapterName(target.Name()) ||
		isCgoC2func(target.Name()) {
		return certificate, nil, false, nil
	}
	marker, markerErr := exactCgoUnsafeArgsDirective(target)
	if markerErr != nil {
		return certificate, nil, false, markerErr
	}
	cmallocWrapper := target.Name() == "_Cfunc__CMalloc"
	if !marker && !cmallocWrapper {
		return certificate, nil, false, nil
	}
	if cmallocWrapper {
		if marker {
			return certificate, nil, false, fmt.Errorf("_Cfunc__CMalloc unexpectedly carries //go:cgo_unsafe_args")
		}
		if shapeErr := ctx.verifyCoroCMallocWrapperShape(target); shapeErr != nil {
			return certificate, nil, false, shapeErr
		}
	}
	if target.Parent() != nil || len(target.FreeVars) != 0 || len(target.Blocks) == 0 ||
		target.Signature == nil || target.Signature.Recv() != nil || target.Signature.Variadic() ||
		coroWorkerTypeParamLen(target.Signature.TypeParams()) != 0 ||
		coroWorkerTypeParamLen(target.Signature.RecvTypeParams()) != 0 ||
		target.Origin() != nil || len(target.TypeArgs()) != 0 {
		return certificate, nil, false, fmt.Errorf("generated cgo worker target %q has an unsupported function shape", target.Name())
	}
	background, classified, backgroundErr := u.FunctionBackground(target)
	if backgroundErr != nil {
		return certificate, nil, false, backgroundErr
	}
	if !classified || background != llssa.InGo {
		return certificate, nil, false, fmt.Errorf("generated cgo worker target %q is not one exact Go adapter body", target.Name())
	}
	directive, directiveErr := coroRawABIDirective(target, u)
	if directiveErr != nil {
		return certificate, nil, false, directiveErr
	}
	expectedDirective := "//go:cgo_unsafe_args"
	if cmallocWrapper {
		expectedDirective = ""
	}
	if directive != expectedDirective {
		return certificate, nil, false, fmt.Errorf(
			"generated cgo worker target %q has raw ABI directive %q, want %q",
			target.Name(), directive, expectedDirective,
		)
	}

	signature, signatureErr := u.coroPhysicalSourceSignature(target)
	if signatureErr != nil {
		return certificate, nil, false, signatureErr
	}
	if signature == nil || signature.Recv() != nil || signature.Variadic() ||
		signature.Params().Len() != len(common.Args) || signature.Results().Len() > 1 {
		return certificate, nil, false, fmt.Errorf("generated cgo worker target %q has an unsupported effective signature", target.Name())
	}
	callSignature, ok := ctx.patchType(common.Signature()).(*types.Signature)
	if !ok || !types.Identical(coroPhysicalNormalizeSourceSignature(callSignature), signature) {
		return certificate, nil, false, fmt.Errorf("generated cgo worker call and target %q have different effective signatures", target.Name())
	}
	for index, argument := range common.Args {
		if argument == nil {
			return certificate, nil, false, fmt.Errorf("generated cgo worker call argument %d is nil", index)
		}
		parameter := signature.Params().At(index).Type()
		if !types.Identical(ctx.patchType(argument.Type()), parameter) ||
			!coroWorkerForeignRecordValueType(u, parameter, true, make(map[types.Type]bool)) {
			return certificate, nil, false, fmt.Errorf(
				"generated cgo worker call argument %d type %s has no typed worker-record representation",
				index, parameter,
			)
		}
	}
	if signature.Results().Len() == 1 {
		result := signature.Results().At(0).Type()
		// A cgo result pointer denotes foreign storage. Unlike an ordinary
		// unannotated C declaration, the generated adapter is the provenance
		// boundary, so the typed frame record may carry that pointer back after
		// completion.
		if !coroWorkerForeignRecordValueType(u, result, true, make(map[types.Type]bool)) {
			return certificate, nil, false, fmt.Errorf(
				"generated cgo worker result type %s has no typed worker-record representation",
				result,
			)
		}
	}
	linkIdentity := u.linkIdentities[target]
	targetIdentity := u.finalIdentity(target)
	if linkIdentity == "" || targetIdentity == "" || targetIdentity == "<nil>" || targetIdentity == "<cyclic-alias>" {
		return certificate, nil, false, fmt.Errorf("generated cgo worker target %q has no frozen identity", target.Name())
	}
	abi := structuralEmissionABITypeKey(signature)
	targetSpec := u.prog.TargetSpec()
	certificate = CoroCgoWorkerCallCertificate{
		ID: emissionDigest(framedEmissionKey(
			"llgo-coro-cgo-worker-call-v1",
			targetIdentity,
			linkIdentity,
			abi,
			targetSpec.Triple,
			targetSpec.CPU,
			targetSpec.Features,
			targetSpec.TargetABI,
			u.prog.DataLayout(),
		)),
		TargetIdentity: targetIdentity,
		ABISignature:   abi,
	}
	return certificate, target, true, nil
}

func validateCoroWorkerCgoCall(
	plan *coro.SSAPlan,
	universe *EmissionUniverse,
	call ssa.CallInstruction,
) (shape coroWorkerCgoCallShape, recognized bool, err error) {
	shape.resultField = -1
	if plan == nil || universe == nil || call == nil || call.Common() == nil ||
		universe.coroProgramIR == nil {
		return shape, false, nil
	}
	frozen, found, frozenErr := universe.coroProgramIR.callSitePlan(call)
	if frozenErr != nil {
		return shape, false, frozenErr
	}
	if !found || frozen.plan.Elision != CoroCallElidedCgoWorker {
		return shape, false, nil
	}
	recognized = true
	if frozen.failure != "" {
		return shape, true, fmt.Errorf("%s", frozen.failure)
	}
	if frozen.cgoWorker.ID == "" || frozen.plan.CgoWorkerTarget == nil ||
		frozen.plan.ElisionCertificate != frozen.cgoWorker.ID {
		return shape, true, fmt.Errorf("frozen cgo worker call has no exact certificate and target")
	}
	target := frozen.plan.CgoWorkerTarget
	targetPlan, planned := plan.FunctionPlan(target)
	if !planned || targetPlan.External != coro.Defined || !targetPlan.RawPlainOnly ||
		targetPlan.Emission != coro.EmitRawPlain || targetPlan.Primary != coro.PrimaryPlain ||
		!targetPlan.RawPlainDemand || targetPlan.ManagedDemand != coro.NoDemand ||
		!plan.HasRawPlainVariant(target) {
		return shape, true, fmt.Errorf(
			"generated cgo target %q is not one raw-only synchronous adapter: %+v",
			target.Name(), targetPlan,
		)
	}
	signature, signatureErr := universe.coroPhysicalSourceSignature(target)
	if signatureErr != nil {
		return shape, true, signatureErr
	}
	if structuralEmissionABITypeKey(signature) != frozen.cgoWorker.ABISignature {
		return shape, true, fmt.Errorf("generated cgo worker ABI differs from its frozen certificate")
	}
	result := types.Type(nil)
	if signature.Results().Len() == 1 {
		result = signature.Results().At(0).Type()
	}
	record, resultField := coroWorkerForeignRecordType(signature, result)
	shape = coroWorkerCgoCallShape{
		target:      target,
		signature:   signature,
		record:      record,
		argc:        signature.Params().Len(),
		result:      result,
		resultField: resultField,
		certificate: frozen.cgoWorker,
	}
	return shape, true, nil
}

func (p *context) coroWorkerCgoThunk(shape coroWorkerCgoCallShape, target llssa.Function) llssa.Function {
	if p == nil || shape.target == nil || shape.signature == nil || shape.record == nil || target == nil {
		panic("coroutine cgo worker thunk requires an exact target, signature, and call record")
	}
	name := coroWorkerCgoThunkPrefixV1 + emissionDigest(framedEmissionKey(
		"cl-coro-worker-cgo-thunk-v1",
		shape.certificate.ID,
		target.Name(),
		structuralEmissionABITypeKey(shape.signature),
	))
	thunk := p.pkg.NewFuncEx(name, coroWorkerForeignThunkSignature(), llssa.InC, false, true)
	if thunk.HasBody() {
		return thunk
	}
	b := thunk.MakeBody(1)
	recordPointer := b.Convert(
		p.type_(types.NewPointer(shape.record), llssa.InGo),
		thunk.Param(0),
	)
	args := make([]llssa.Expr, shape.argc)
	for index := range args {
		args[index] = b.LoadKnownNonNil(b.FieldAddr(recordPointer, index))
	}
	ret := b.Call(target.Expr, args...)
	if shape.result != nil {
		if shape.resultField < 0 {
			panic("coroutine cgo worker thunk lost its result field")
		}
		b.Store(b.FieldAddr(recordPointer, shape.resultField), ret)
	}
	b.Return(p.prog.Zero(p.prog.Uintptr()))
	b.EndBuild()
	b.Dispose()
	return thunk
}

func (p *context) compileCoroWorkerCgoCall(
	b llssa.Builder,
	call *ssa.Call,
	shape coroWorkerCgoCallShape,
) llssa.Expr {
	if p == nil || !p.hasCoroPhysicalBody() || call == nil || shape.target == nil ||
		shape.signature == nil || shape.record == nil {
		panic("coroutine cgo worker lowering escaped its frozen physical operation recipe")
	}
	compiled := p.compileValues(b, call.Common().Args, fnNormal)
	keepaliveSlots := p.compileCoroCallKeepaliveSlots(b, call)
	return p.compileCoroWorkerCgoTransaction(b, shape, compiled, keepaliveSlots)
}

// compileCoroWorkerCgoTransaction is the single physical operation shared by
// an ordinary generated-cgo call and a deferred generated-cgo cleanup. The
// source-specific lowering owns evaluation/capture; this helper owns only the
// typed worker record, exact raw adapter thunk, and common park/resume
// transaction.
func (p *context) compileCoroWorkerCgoTransaction(
	b llssa.Builder,
	shape coroWorkerCgoCallShape,
	compiled []llssa.Expr,
	keepaliveSlots []llssa.Expr,
) llssa.Expr {
	if p == nil || !p.hasCoroPhysicalBody() || shape.target == nil ||
		shape.signature == nil || shape.record == nil || len(compiled) != shape.argc {
		panic("coroutine cgo worker transaction escaped its frozen typed recipe")
	}
	target, py, kind := p.compileRawPlainFunction(shape.target)
	if kind != goFunc || target == nil || py != nil {
		panic("coroutine cgo worker lowering lost its exact raw Go adapter")
	}
	record := p.coroFrameAlloc(p.type_(shape.record, llssa.InGo))
	for index, argument := range compiled {
		b.Store(b.FieldAddr(record, index), argument)
	}
	thunk := p.coroWorkerCgoThunk(shape, target)
	p.compileCoroWorkerWordCall(
		b,
		b.Convert(p.prog.Uintptr(), thunk.Expr),
		[]llssa.Expr{b.Convert(p.prog.Uintptr(), record)},
		keepaliveSlots,
	)
	b.KeepAlive(record)
	if shape.result == nil {
		return llssa.Expr{}
	}
	if shape.resultField < 0 {
		panic("coroutine cgo worker lowering lost its result field")
	}
	return b.LoadKnownNonNil(b.FieldAddr(record, shape.resultField))
}

func generatedCgoWorkerCallRecipeID(certificate CoroCgoWorkerCallCertificate) coro.RecipeID {
	if certificate.ID == "" {
		return ""
	}
	return coro.RecipeID("cl.cgo.worker-call.v1")
}
