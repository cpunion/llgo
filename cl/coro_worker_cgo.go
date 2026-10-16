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
	"go/token"
	"go/types"
	"strconv"
	"strings"

	"github.com/xgo-dev/llgo/internal/coro"
	llssa "github.com/xgo-dev/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

const coroGeneratedCgoThunkPrefixV2 = "__llgo_coro_generated_cgo_thunk_v2_"

// CoroGeneratedCgoCallCertificate binds one managed source call to the exact
// compiler-generated synchronous cgo adapter and its frozen execution class.
// The certificate may select either a bounded worker or a same-M foreign
// episode. It authorizes only this call-site replacement and never classifies
// arbitrary C code as callback-free.
type CoroGeneratedCgoCallCertificate struct {
	ID             string
	TargetIdentity string
	ABISignature   string
	mode           coroForeignCallMode
	reentry        coro.ReentryClass
}

type coroGeneratedCgoCallShape struct {
	target      *ssa.Function
	signature   *types.Signature
	record      *types.Struct
	argc        int
	result      types.Type
	resultField int
	mode        coroForeignCallMode
	reentry     coro.ReentryClass
	certificate CoroGeneratedCgoCallCertificate
}

// coroGeneratedCgoErrnoCallShape is the managed half of Go's generated
// _C2func_* protocol. The source wrapper stays a coroutine so construction of
// its (result, error) pair happens on the logical G. Only the exact C function
// pointer, typed arguments, primary result, and same-thread errno snapshot
// cross the frozen worker or same-M transaction.
type coroGeneratedCgoErrnoCallShape struct {
	function     ssa.Value
	signature    *types.Signature
	record       *types.Struct
	calleeField  int
	argumentBase int
	argc         int
	result       types.Type
	resultField  int
	errnoField   int
	mode         coroForeignCallMode
	reentry      coro.ReentryClass
	certificate  CoroGeneratedCgoCallCertificate
}

func validCoroGeneratedCgoExecution(mode coroForeignCallMode, reentry coro.ReentryClass) bool {
	switch mode {
	case coroForeignCallModeWorker:
		return reentry == coro.ReentryNone
	case coroForeignCallModeSameM:
		return reentry == coro.ReentryNone || reentry == coro.ReentryManagedIngress
	default:
		return false
	}
}

func isGeneratedCgoAdapterName(name string) bool {
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

// coroGeneratedCgoErrnoCallShape freezes the exact generated _C2func_* boundary.
// It is intentionally stricter than name recognition: the compiler directive,
// intrinsic ABI, first-block lowering occurrence, C function cell, source ABI,
// and target configuration all participate in the certificate. No function
// pointer is reverse-classified at runtime.
func (ctx *context) coroGeneratedCgoErrnoCallShape(
	call *ssa.Call,
) (shape coroGeneratedCgoErrnoCallShape, recognized bool, err error) {
	shape.resultField = -1
	shape.errnoField = -1
	if ctx == nil || ctx.emissionUniverse == nil || call == nil || call.Common() == nil ||
		ctx.goFn != call.Parent() || !ctx.emissionUniverse.coroCapabilities.NativeFleet() {
		return shape, false, nil
	}
	u := ctx.emissionUniverse
	common := call.Common()
	callee := common.StaticCallee()
	if callee == nil || common.IsInvoke() || common.Method != nil {
		return shape, false, nil
	}
	parent := call.Parent()
	if parent == nil || ctx.goFn != parent || parent.Parent() != nil ||
		!isCgoC2func(parent.Name()) {
		return shape, false, nil
	}
	recognized = true
	if len(parent.FreeVars) != 0 || parent.Signature == nil ||
		parent.Signature.Recv() != nil || parent.Signature.Variadic() ||
		coroWorkerTypeParamLen(parent.Signature.TypeParams()) != 0 ||
		coroWorkerTypeParamLen(parent.Signature.RecvTypeParams()) != 0 ||
		parent.Origin() != nil || len(parent.TypeArgs()) != 0 {
		return shape, true, fmt.Errorf("generated C2 adapter %q has an unsupported function shape", parent.Name())
	}
	marker, markerErr := exactCgoUnsafeArgsDirective(parent)
	if markerErr != nil {
		return shape, true, fmt.Errorf("generated C2 adapter %q directive: %w", parent.Name(), markerErr)
	}
	if !marker {
		return shape, true, fmt.Errorf("generated C2 adapter %q lacks //go:cgo_unsafe_args", parent.Name())
	}
	directive, directiveErr := coroRawABIDirective(parent, u)
	if directiveErr != nil {
		return shape, true, directiveErr
	}
	if directive != "//go:cgo_unsafe_args" {
		return shape, true, fmt.Errorf("generated C2 adapter %q has raw ABI directive %q", parent.Name(), directive)
	}
	background, classified, backgroundErr := u.FunctionBackground(parent)
	if backgroundErr != nil {
		return shape, true, backgroundErr
	}
	if !classified || background != llssa.InGo {
		return shape, true, fmt.Errorf("generated C2 adapter %q is not one exact Go body", parent.Name())
	}

	const intrinsicShape = "func(unsafe.Pointer, uintptr) int32"
	signature := common.Signature()
	basicKind := func(typ types.Type, kind types.BasicKind) bool {
		basic, ok := types.Unalias(typ).Underlying().(*types.Basic)
		return ok && basic.Kind() == kind
	}
	if signature == nil || signature.Recv() != nil || signature.Variadic() ||
		signature.Params() == nil || signature.Params().Len() != 2 ||
		signature.Results() == nil || signature.Results().Len() != 1 ||
		len(common.Args) != 2 ||
		!basicKind(signature.Params().At(0).Type(), types.UnsafePointer) ||
		!basicKind(signature.Params().At(1).Type(), types.Uintptr) ||
		!basicKind(signature.Results().At(0).Type(), types.Int32) ||
		!types.Identical(common.Args[0].Type(), signature.Params().At(0).Type()) ||
		!types.Identical(common.Args[1].Type(), signature.Params().At(1).Type()) {
		return shape, true, fmt.Errorf(
			"_cgo_runtime_cgocall in %q requires the exact %s shape",
			parent.Name(), intrinsicShape,
		)
	}
	functionLoad, ok := common.Args[0].(*ssa.UnOp)
	if !ok || functionLoad.Op != token.MUL {
		return shape, true, fmt.Errorf("generated C2 adapter %q has no exact C function-cell load", parent.Name())
	}
	functionCell, ok := functionLoad.X.(*ssa.Global)
	if !ok || !strings.HasPrefix(functionCell.Name(), "_cgo_") ||
		!strings.HasSuffix(functionCell.Name(), parent.Name()) ||
		!basicKind(functionLoad.Type(), types.UnsafePointer) {
		return shape, true, fmt.Errorf(
			"generated C2 adapter %q has an incompatible C function cell %q",
			parent.Name(), functionLoad.String(),
		)
	}
	lowering, loweringErr := u.cgoLoweringPlan(ctx, parent)
	if loweringErr != nil {
		return shape, true, loweringErr
	}
	if len(lowering.calls) != 1 || lowering.calls[0].call != call ||
		!lowering.calls[0].compiled {
		return shape, true, fmt.Errorf(
			"generated C2 adapter %q does not have one exact compiled cgo call",
			parent.Name(),
		)
	}

	sourceSignature, sourceErr := u.coroPhysicalSourceSignature(parent)
	if sourceErr != nil {
		return shape, true, sourceErr
	}
	if sourceSignature == nil || sourceSignature.Results() == nil ||
		sourceSignature.Results().Len() != 2 ||
		!types.Identical(
			types.Unalias(sourceSignature.Results().At(1).Type()),
			types.Unalias(types.Universe.Lookup("error").Type()),
		) {
		return shape, true, fmt.Errorf("generated C2 adapter %q must return exactly (result, error)", parent.Name())
	}
	result := sourceSignature.Results().At(0).Type()
	if !coroWorkerForeignRecordValueType(u, result, true, make(map[types.Type]bool)) {
		return shape, true, fmt.Errorf(
			"generated C2 adapter %q result type %s has no typed record representation",
			parent.Name(), result,
		)
	}
	params := sourceSignature.Params()
	for index := 0; index < params.Len(); index++ {
		parameter := params.At(index).Type()
		if !coroWorkerForeignRecordValueType(u, parameter, true, make(map[types.Type]bool)) {
			return shape, true, fmt.Errorf(
				"generated C2 adapter %q argument %d type %s has no typed record representation",
				parent.Name(), index, parameter,
			)
		}
	}
	directSignature := types.NewSignatureType(
		nil, nil, nil, params,
		types.NewTuple(types.NewVar(token.NoPos, nil, "", result)),
		false,
	)
	fields := make([]*types.Var, 0, params.Len()+3)
	calleeField := len(fields)
	fields = append(fields, types.NewField(token.NoPos, nil, "callee", directSignature, false))
	argumentBase := len(fields)
	for index := 0; index < params.Len(); index++ {
		fields = append(fields, types.NewField(
			token.NoPos, nil, fmt.Sprintf("a%d", index), params.At(index).Type(), false,
		))
	}
	resultField := len(fields)
	fields = append(fields, types.NewField(token.NoPos, nil, "result", result, false))
	errnoField := len(fields)
	fields = append(fields, types.NewField(token.NoPos, nil, "errno", types.Typ[types.Int32], false))

	ownerIdentity := u.linkIdentities[parent]
	if ownerIdentity == "" {
		return shape, true, fmt.Errorf("generated C2 adapter %q has no frozen linkage identity", parent.Name())
	}
	semantic, semanticErr := coro.SemanticInstructionOrdinal(call)
	if semanticErr != nil {
		return shape, true, semanticErr
	}
	block := call.Block()
	if block == nil {
		return shape, true, fmt.Errorf("generated C2 adapter %q has a detached cgo call", parent.Name())
	}
	targetIdentity := framedEmissionKey(
		"cl-coro-cgo-errno-target-v1",
		ownerIdentity,
		functionCell.String(),
		strconv.Itoa(block.Index),
		strconv.Itoa(semantic),
	)
	abi := u.emissionTypeKeys.cFunctionABI(directSignature)
	targetSpec := u.prog.TargetSpec()
	mode, reentry := coroForeignCallModeSameM, coro.ReentryManagedIngress
	certificate := CoroGeneratedCgoCallCertificate{
		ID: emissionDigest(framedEmissionKey(
			"llgo-coro-cgo-errno-same-m-reentry-call-v2",
			targetIdentity,
			abi,
			fmt.Sprintf("%d", mode),
			string(reentry),
			targetSpec.Triple,
			targetSpec.CPU,
			targetSpec.Features,
			targetSpec.TargetABI,
			u.prog.DataLayout(),
		)),
		TargetIdentity: targetIdentity,
		ABISignature:   abi,
		mode:           mode,
		reentry:        reentry,
	}
	shape = coroGeneratedCgoErrnoCallShape{
		function:     functionLoad,
		signature:    directSignature,
		record:       types.NewStruct(fields, nil),
		calleeField:  calleeField,
		argumentBase: argumentBase,
		argc:         params.Len(),
		result:       result,
		resultField:  resultField,
		errnoField:   errnoField,
		mode:         mode,
		reentry:      reentry,
		certificate:  certificate,
	}
	return shape, true, nil
}

func (ctx *context) freezeCoroGeneratedCgoErrnoCallCertificate(
	call ssa.CallInstruction,
) (CoroGeneratedCgoCallCertificate, bool, error) {
	direct, ok := call.(*ssa.Call)
	if !ok {
		return CoroGeneratedCgoCallCertificate{}, false, nil
	}
	shape, recognized, err := ctx.coroGeneratedCgoErrnoCallShape(direct)
	if err != nil || !recognized {
		return CoroGeneratedCgoCallCertificate{}, recognized, err
	}
	return shape.certificate, true, nil
}

// verifyCoroRawCgocallShape proves the narrow synchronous half of the
// generated-cgo protocol. Call-site classification remains centralized in
// classifyCoroIntrinsicCallSite; this helper verifies only the generated
// adapter shape. It never licenses a managed caller to invoke C on an
// executor: only the dedicated raw _Cfunc_* lowering may inline this compiler
// intrinsic, while freezeCoroGeneratedCgoCallCertificate replaces the outer
// managed call with its frozen worker or same-M foreign transaction.
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
		!isGeneratedCgoAdapterName(parent.Name()) || isCgoC2func(parent.Name()) {
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

func (u *EmissionUniverse) freezeCoroGeneratedCgoCallCertificate(
	ctx *context,
	call ssa.CallInstruction,
) (certificate CoroGeneratedCgoCallCertificate, target *ssa.Function, certified bool, err error) {
	if u == nil || ctx == nil || call == nil || call.Common() == nil ||
		!u.coroCapabilities.NativeFleet() {
		return certificate, nil, false, nil
	}
	switch call.(type) {
	case *ssa.Call, *ssa.Defer:
	default:
		// A go statement already transfers execution to a new logical task.
		// It must not also park its source owner on a bounded worker.
		return certificate, nil, false, nil
	}
	// A generated adapter is already the raw synchronous transaction body.
	// Calls inside that body stay ordinary raw calls; recursively replacing them
	// with another foreign transaction would nest the scheduler boundary and
	// incorrectly color the adapter as managed.
	if owner := call.Parent(); owner != nil && isGeneratedCgoAdapterName(owner.Name()) {
		return certificate, nil, false, nil
	}
	common := call.Common()
	raw := common.StaticCallee()
	if raw == nil || common.IsInvoke() || common.Method != nil {
		return certificate, nil, false, nil
	}
	target, resolved := u.Resolve(raw)
	if !resolved || target == nil || target != raw || !isGeneratedCgoAdapterName(target.Name()) ||
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
		return certificate, nil, false, fmt.Errorf("generated-cgo target %q has an unsupported function shape", target.Name())
	}
	background, classified, backgroundErr := u.FunctionBackground(target)
	if backgroundErr != nil {
		return certificate, nil, false, backgroundErr
	}
	if !classified || background != llssa.InGo {
		return certificate, nil, false, fmt.Errorf("generated-cgo target %q is not one exact Go adapter body", target.Name())
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
			"generated-cgo target %q has raw ABI directive %q, want %q",
			target.Name(), directive, expectedDirective,
		)
	}

	signature, signatureErr := u.coroPhysicalSourceSignature(target)
	if signatureErr != nil {
		return certificate, nil, false, signatureErr
	}
	if signature == nil || signature.Recv() != nil || signature.Variadic() ||
		signature.Params().Len() != len(common.Args) || signature.Results().Len() > 1 {
		return certificate, nil, false, fmt.Errorf("generated-cgo target %q has an unsupported effective signature", target.Name())
	}
	callSignature, ok := ctx.patchType(common.Signature()).(*types.Signature)
	if !ok || !types.Identical(coroPhysicalNormalizeSourceSignature(callSignature), signature) {
		return certificate, nil, false, fmt.Errorf("generated-cgo call and target %q have different effective signatures", target.Name())
	}
	for index, argument := range common.Args {
		if argument == nil {
			return certificate, nil, false, fmt.Errorf("generated-cgo call argument %d is nil", index)
		}
		parameter := signature.Params().At(index).Type()
		if !types.Identical(ctx.patchType(argument.Type()), parameter) ||
			!coroWorkerForeignRecordValueType(u, parameter, true, make(map[types.Type]bool)) {
			return certificate, nil, false, fmt.Errorf(
				"generated-cgo call argument %d type %s has no typed record representation",
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
				"generated-cgo result type %s has no typed record representation",
				result,
			)
		}
	}
	linkIdentity := u.linkIdentities[target]
	targetIdentity := u.finalIdentity(target)
	if linkIdentity == "" || targetIdentity == "" || targetIdentity == "<nil>" || targetIdentity == "<cyclic-alias>" {
		return certificate, nil, false, fmt.Errorf("generated-cgo target %q has no frozen identity", target.Name())
	}
	abi := u.emissionTypeKeys.strictABI(signature)
	targetSpec := u.prog.TargetSpec()
	mode, reentry := coroForeignCallModeSameM, coro.ReentryManagedIngress
	certificate = CoroGeneratedCgoCallCertificate{
		ID: emissionDigest(framedEmissionKey(
			"llgo-coro-cgo-same-m-reentry-call-v2",
			targetIdentity,
			linkIdentity,
			abi,
			fmt.Sprintf("%d", mode),
			string(reentry),
			targetSpec.Triple,
			targetSpec.CPU,
			targetSpec.Features,
			targetSpec.TargetABI,
			u.prog.DataLayout(),
		)),
		TargetIdentity: targetIdentity,
		ABISignature:   abi,
		mode:           mode,
		reentry:        reentry,
	}
	return certificate, target, true, nil
}

func validateCoroGeneratedCgoCall(
	plan *coro.SSAPlan,
	universe *EmissionUniverse,
	call ssa.CallInstruction,
) (shape coroGeneratedCgoCallShape, recognized bool, err error) {
	shape.resultField = -1
	if plan == nil || universe == nil || call == nil || call.Common() == nil ||
		universe.coroProgramIR == nil {
		return shape, false, nil
	}
	frozen, found, frozenErr := universe.coroProgramIR.callSitePlan(call)
	if frozenErr != nil {
		return shape, false, frozenErr
	}
	if !found || frozen.plan.Elision != CoroCallElidedGeneratedCgo {
		return shape, false, nil
	}
	recognized = true
	if frozen.failure != "" {
		return shape, true, fmt.Errorf("%s", frozen.failure)
	}
	if frozen.generatedCgo.ID == "" || frozen.plan.GeneratedCgoTarget == nil ||
		frozen.plan.ElisionCertificate != frozen.generatedCgo.ID {
		return shape, true, fmt.Errorf("frozen generated-cgo call has no exact certificate and target")
	}
	target := frozen.plan.GeneratedCgoTarget
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
	if universe.emissionTypeKeys.strictABI(signature) != frozen.generatedCgo.ABISignature {
		return shape, true, fmt.Errorf("generated-cgo ABI differs from its frozen certificate")
	}
	if !validCoroGeneratedCgoExecution(frozen.generatedCgo.mode, frozen.generatedCgo.reentry) {
		return shape, true, fmt.Errorf("generated cgo call has an invalid frozen execution class")
	}
	result := types.Type(nil)
	if signature.Results().Len() == 1 {
		result = signature.Results().At(0).Type()
	}
	record, resultField := coroWorkerForeignRecordType(signature, result)
	shape = coroGeneratedCgoCallShape{
		target:      target,
		signature:   signature,
		record:      record,
		argc:        signature.Params().Len(),
		result:      result,
		resultField: resultField,
		mode:        frozen.generatedCgo.mode,
		reentry:     frozen.generatedCgo.reentry,
		certificate: frozen.generatedCgo,
	}
	return shape, true, nil
}

func validateCoroGeneratedCgoErrnoCall(
	plan *coro.SSAPlan,
	ctx *context,
	call *ssa.Call,
) (shape coroGeneratedCgoErrnoCallShape, recognized bool, err error) {
	shape.resultField = -1
	shape.errnoField = -1
	if plan == nil || ctx == nil || ctx.emissionUniverse == nil ||
		call == nil || call.Common() == nil ||
		ctx.emissionUniverse.coroProgramIR == nil {
		return shape, false, nil
	}
	universe := ctx.emissionUniverse
	frozen, found, frozenErr := universe.coroProgramIR.callSitePlan(call)
	if frozenErr != nil {
		return shape, false, frozenErr
	}
	if !found || !frozen.plan.Intrinsic || frozen.opcode != llgoCgoCgocall ||
		frozen.plan.IntrinsicSemantics != CoroIntrinsicCallInlineForeignTransaction {
		return shape, false, nil
	}
	recognized = true
	if frozen.failure != "" {
		return shape, true, fmt.Errorf("%s", frozen.failure)
	}
	if frozen.plan.Elision != CoroCallElidedIntrinsic || !plan.ElidesCall(call) ||
		frozen.generatedCgo.ID == "" || frozen.generatedCgo.TargetIdentity == "" ||
		frozen.generatedCgo.ABISignature == "" {
		return shape, true, fmt.Errorf("frozen C2 cgo call has no exact intrinsic foreign certificate")
	}
	parent := call.Parent()
	parentPlan, planned := plan.FunctionPlan(parent)
	if !planned || parentPlan.External != coro.Defined ||
		parentPlan.Emission != coro.EmitCoroutine ||
		parentPlan.Primary != coro.PrimaryCoroutine ||
		!parentPlan.Effect.Contains(coro.WaitForeign) ||
		parentPlan.RawPlainOnly {
		return shape, true, fmt.Errorf(
			"generated C2 owner %q is not one managed wait-foreign coroutine: %+v",
			parent.Name(), parentPlan,
		)
	}
	derived, exact, shapeErr := ctx.coroGeneratedCgoErrnoCallShape(call)
	if shapeErr != nil {
		return shape, true, shapeErr
	}
	if !exact {
		return shape, true, fmt.Errorf("frozen C2 cgo call no longer has its generated foreign shape")
	}
	if derived.certificate != frozen.generatedCgo ||
		universe.emissionTypeKeys.cFunctionABI(derived.signature) != frozen.generatedCgo.ABISignature {
		return shape, true, fmt.Errorf("generated C2 cgo call differs from its frozen foreign certificate")
	}
	if !validCoroGeneratedCgoExecution(derived.mode, derived.reentry) {
		return shape, true, fmt.Errorf("generated C2 cgo call has an invalid frozen execution class")
	}
	return derived, true, nil
}

func validateCoroGeneratedCgoErrnoOwner(
	plan *coro.SSAPlan,
	ctx *context,
	fn *ssa.Function,
) error {
	if plan == nil || ctx == nil || ctx.emissionUniverse == nil ||
		fn == nil || ctx.goFn != fn || !isCgoC2func(fn.Name()) {
		return fmt.Errorf("C2 foreign owner validation requires one exact generated C2 function")
	}
	count := 0
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok {
				continue
			}
			_, recognized, err := validateCoroGeneratedCgoErrnoCall(plan, ctx, call)
			if err != nil {
				return err
			}
			if recognized {
				count++
			}
		}
	}
	if count != 1 {
		return fmt.Errorf("generated C2 function %q has %d typed errno foreign operations, want 1", fn.Name(), count)
	}
	return nil
}

func (p *context) coroGeneratedCgoThunk(shape coroGeneratedCgoCallShape, target llssa.Function) llssa.Function {
	if p == nil || shape.target == nil || shape.signature == nil || shape.record == nil || target == nil {
		panic("coroutine generated-cgo thunk requires an exact target, signature, and call record")
	}
	name := coroGeneratedCgoThunkPrefixV2 + emissionDigest(framedEmissionKey(
		"cl-coro-generated-cgo-thunk-v2",
		shape.certificate.ID,
		target.Name(),
		p.cachedStrictEmissionABITypeKey(shape.signature),
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
			panic("coroutine generated-cgo thunk lost its result field")
		}
		b.Store(b.FieldAddr(recordPointer, shape.resultField), ret)
	}
	b.Return(p.prog.Zero(p.prog.Uintptr()))
	b.EndBuild()
	b.Dispose()
	return thunk
}

func (p *context) coroGeneratedCgoErrnoThunk(shape coroGeneratedCgoErrnoCallShape) llssa.Function {
	if p == nil || shape.function == nil || shape.signature == nil || shape.record == nil ||
		shape.result == nil || shape.calleeField < 0 || shape.argumentBase < 0 ||
		shape.resultField < 0 || shape.errnoField < 0 {
		panic("coroutine generated C2 thunk requires one exact typed errno record")
	}
	name := coroGeneratedCgoThunkPrefixV2 + emissionDigest(framedEmissionKey(
		"cl-coro-generated-cgo-errno-thunk-v2",
		shape.certificate.ID,
		p.cachedCFunctionABITypeKey(shape.signature),
	))
	thunk := p.pkg.NewFuncEx(name, coroWorkerForeignThunkSignature(), llssa.InC, false, true)
	if thunk.HasBody() {
		return thunk
	}
	b := thunk.MakeBody(1)
	recordPointer := b.Convert(
		p.type_(types.NewPointer(shape.record), llssa.InC),
		thunk.Param(0),
	)
	target := b.LoadKnownNonNil(b.FieldAddr(recordPointer, shape.calleeField))
	args := make([]llssa.Expr, shape.argc)
	for index := range args {
		args[index] = b.LoadKnownNonNil(b.FieldAddr(recordPointer, shape.argumentBase+index))
	}
	result := b.Call(target, args...)
	b.Store(b.FieldAddr(recordPointer, shape.resultField), result)
	errnoSignature := types.NewSignatureType(
		nil, nil, nil, nil,
		types.NewTuple(types.NewVar(token.NoPos, nil, "", types.Typ[types.Int32])),
		false,
	)
	errnoFunction := b.Pkg.NewFunc("cliteErrno", errnoSignature, llssa.InC)
	errno := b.Call(errnoFunction.Expr)
	b.Store(b.FieldAddr(recordPointer, shape.errnoField), errno)
	b.Return(p.prog.Zero(p.prog.Uintptr()))
	b.EndBuild()
	b.Dispose()
	return thunk
}

// compileCoroGeneratedCgoBoundary is the common scheduler transaction for all
// compiler-owned cgo records. Record construction and result interpretation
// stay typed at their source sites; only the frozen execution class, lifetime
// fence, and outcome transfer are shared here.
func (p *context) compileCoroGeneratedCgoBoundary(
	b llssa.Builder,
	mode coroForeignCallMode,
	reentry coro.ReentryClass,
	thunkWord llssa.Expr,
	workerIdentity llssa.Expr,
	recordWord llssa.Expr,
	record llssa.Expr,
	keepaliveSlots []llssa.Expr,
) {
	if p == nil || !p.hasCoroPhysicalBody() || b == nil || b.Func != p.fn ||
		thunkWord.IsNil() || recordWord.IsNil() || record.IsNil() ||
		!validCoroGeneratedCgoExecution(mode, reentry) {
		panic("coroutine generated-cgo boundary escaped its frozen execution class")
	}
	switch mode {
	case coroForeignCallModeWorker:
		if workerIdentity.IsNil() {
			panic("coroutine generated-cgo worker boundary lost its exact identity")
		}
		p.compileCoroWorkerWordCall(
			b,
			thunkWord,
			workerIdentity,
			[]llssa.Expr{recordWord},
			keepaliveSlots,
			nil,
		)
	case coroForeignCallModeSameM:
		status := p.emitCoroSameMForeignBoundary(b, thunkWord, recordWord, workerIdentity, reentry)
		p.emitCoroKeepaliveSlots(b, keepaliveSlots)
		b.KeepAlive(record)
		if !status.IsNil() {
			p.dispatchCoroSameMForeignReentryCompletion(b, status)
		}
		return
	default:
		panic("coroutine generated-cgo boundary selected an unknown execution class")
	}
	b.KeepAlive(record)
}

func (p *context) compileCoroGeneratedCgoErrnoCall(
	b llssa.Builder,
	call *ssa.Call,
	shape coroGeneratedCgoErrnoCallShape,
) llssa.Expr {
	if p == nil || !p.hasCoroPhysicalBody() || call == nil ||
		p.goFn == nil || p.goFn != call.Parent() ||
		shape.function == nil || shape.signature == nil || shape.record == nil ||
		shape.argc != len(p.goFn.Params) || shape.resultField < 0 ||
		shape.errnoField < 0 || !validCoroGeneratedCgoExecution(shape.mode, shape.reentry) {
		panic("coroutine generated C2 lowering escaped its frozen typed errno recipe")
	}
	functionCell := p.compileValue(b, shape.function)
	functionCell.Type = p.prog.Pointer(p.type_(shape.signature, llssa.InC))
	callee := b.Load(functionCell)
	arguments := make([]llssa.Expr, shape.argc)
	for index, parameter := range p.goFn.Params {
		arguments[index] = p.compileValue(b, parameter)
	}
	record := p.coroFrameAlloc(p.type_(shape.record, llssa.InC))
	b.Store(b.FieldAddr(record, shape.calleeField), callee)
	for index, argument := range arguments {
		b.Store(b.FieldAddr(record, shape.argumentBase+index), argument)
	}
	thunk := p.coroGeneratedCgoErrnoThunk(shape)
	thunkWord := b.Convert(p.prog.Uintptr(), thunk.Expr)
	recordWord := b.Convert(p.prog.Uintptr(), record)
	p.compileCoroGeneratedCgoBoundary(
		b,
		shape.mode,
		shape.reentry,
		thunkWord,
		b.Convert(p.prog.Uintptr(), callee),
		recordWord,
		record,
		nil,
	)
	p.cgoRet = b.LoadKnownNonNil(b.FieldAddr(record, shape.resultField))
	p.cgoErrno = b.LoadKnownNonNil(b.FieldAddr(record, shape.errnoField))
	return p.cgoErrno
}

func (p *context) compileCoroGeneratedCgoCall(
	b llssa.Builder,
	call *ssa.Call,
	shape coroGeneratedCgoCallShape,
) llssa.Expr {
	if p == nil || !p.hasCoroPhysicalBody() || call == nil || shape.target == nil ||
		shape.signature == nil || shape.record == nil ||
		!validCoroGeneratedCgoExecution(shape.mode, shape.reentry) {
		panic("coroutine generated-cgo lowering escaped its frozen physical operation recipe")
	}
	compiled := p.compileValues(b, call.Common().Args, fnNormal)
	keepaliveSlots := p.compileCoroCallKeepaliveSlots(b, call)
	return p.compileCoroGeneratedCgoTransaction(b, shape, compiled, keepaliveSlots)
}

// compileCoroGeneratedCgoTransaction is the single physical operation shared by
// an ordinary generated-cgo call and a deferred generated-cgo cleanup. The
// source-specific lowering owns evaluation/capture; this helper owns only the
// typed record, exact raw adapter thunk, and frozen worker/same-M transaction.
func (p *context) compileCoroGeneratedCgoTransaction(
	b llssa.Builder,
	shape coroGeneratedCgoCallShape,
	compiled []llssa.Expr,
	keepaliveSlots []llssa.Expr,
) llssa.Expr {
	if p == nil || !p.hasCoroPhysicalBody() || shape.target == nil ||
		shape.signature == nil || shape.record == nil || len(compiled) != shape.argc {
		panic("coroutine generated-cgo transaction escaped its frozen typed recipe")
	}
	target, py, kind := p.compileRawPlainFunction(shape.target)
	if kind != goFunc || target == nil || py != nil {
		panic("coroutine generated-cgo lowering lost its exact raw Go adapter")
	}
	record := p.coroFrameAlloc(p.type_(shape.record, llssa.InGo))
	for index, argument := range compiled {
		b.Store(b.FieldAddr(record, index), argument)
	}
	thunk := p.coroGeneratedCgoThunk(shape, target)
	thunkWord := b.Convert(p.prog.Uintptr(), thunk.Expr)
	recordWord := b.Convert(p.prog.Uintptr(), record)
	p.compileCoroGeneratedCgoBoundary(
		b,
		shape.mode,
		shape.reentry,
		thunkWord,
		b.Convert(p.prog.Uintptr(), target.Expr),
		recordWord,
		record,
		keepaliveSlots,
	)
	if shape.result == nil {
		return llssa.Expr{}
	}
	if shape.resultField < 0 {
		panic("coroutine generated-cgo lowering lost its result field")
	}
	return b.LoadKnownNonNil(b.FieldAddr(record, shape.resultField))
}

func generatedCgoCallRecipeID(certificate CoroGeneratedCgoCallCertificate) coro.RecipeID {
	if certificate.ID == "" {
		return ""
	}
	switch {
	case certificate.mode == coroForeignCallModeWorker && certificate.reentry == coro.ReentryNone:
		return coro.RecipeID("cl.cgo.worker-call.v1")
	case certificate.mode == coroForeignCallModeSameM && certificate.reentry == coro.ReentryNone:
		return coro.RecipeID("cl.cgo.same-m-call.v1")
	case certificate.mode == coroForeignCallModeSameM && certificate.reentry == coro.ReentryManagedIngress:
		return coro.RecipeID("cl.cgo.same-m-reentry-call.v2")
	default:
		return ""
	}
}

func generatedCgoCallContractID(certificate CoroGeneratedCgoCallCertificate) coro.ContractID {
	switch {
	case certificate.mode == coroForeignCallModeWorker && certificate.reentry == coro.ReentryNone:
		return coro.ContractID("llgo.coro.cgo-worker-call.v1")
	case certificate.mode == coroForeignCallModeSameM && certificate.reentry == coro.ReentryNone:
		return coro.ContractID("llgo.coro.cgo-same-m-call.v1")
	case certificate.mode == coroForeignCallModeSameM && certificate.reentry == coro.ReentryManagedIngress:
		return coro.ContractID("llgo.coro.cgo-same-m-reentry-call.v2")
	default:
		return ""
	}
}
