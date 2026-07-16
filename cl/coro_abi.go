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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

const (
	// Version zero is intentionally experimental: the complete CoroHeader and
	// FrameDescriptor ABI is not frozen until scheduler/root lowering lands.
	coroPhysicalABIVersion uint32 = 0
	coroFrameAllocHook            = "__llgo_coro_frame_alloc_v0"
	coroFrameFreeHook             = "__llgo_coro_frame_free_v0"
	coroDescriptorPrefix          = "__llgo_coro_frame_descriptor_v0."
)

type coroPhysicalABI struct {
	hash           [16]byte
	descriptorName string
	physicalSig    *types.Signature
	resultSlotType types.Type
	resultCount    int
}

func newCoroPhysicalABI(p *context, entry plannedFunctionSymbol, sourceSig *types.Signature) coroPhysicalABI {
	resultFields := make([]*types.Var, sourceSig.Results().Len())
	for i := range resultFields {
		resultFields[i] = types.NewField(token.NoPos, nil, fmt.Sprintf("r%d", i), sourceSig.Results().At(i).Type(), false)
	}
	resultSlotType := types.NewStruct(resultFields, nil)
	physicalParams := make([]*types.Var, 0, sourceSig.Params().Len()+2)
	physicalParams = append(physicalParams,
		types.NewParam(token.NoPos, nil, "__llgo_g", types.Typ[types.UnsafePointer]),
		types.NewParam(token.NoPos, nil, "__llgo_out", types.Typ[types.UnsafePointer]),
	)
	for i := 0; i < sourceSig.Params().Len(); i++ {
		physicalParams = append(physicalParams, sourceSig.Params().At(i))
	}
	physicalResults := types.NewTuple(types.NewParam(token.NoPos, nil, "__llgo_handle", types.Typ[types.UnsafePointer]))
	physicalSig := types.NewSignatureType(nil, nil, nil, types.NewTuple(physicalParams...), physicalResults, false)

	qualified := func(pkg *types.Package) string {
		if pkg == nil {
			return ""
		}
		return llssa.PathOf(pkg)
	}
	target := p.prog.TargetSpec()
	key := fmt.Sprintf(
		"llgo-coro-physical-v%d\x00%s\x00triple=%s\x00target-abi=%s\x00data-layout=%s\x00ptr=%d\x00sig=%s\x00result=%s\x00panic=legacy",
		coroPhysicalABIVersion,
		entry.plan.ID,
		target.Triple,
		target.TargetABI,
		p.prog.DataLayout(),
		p.prog.PointerSize(),
		types.TypeString(sourceSig, qualified),
		types.TypeString(resultSlotType, qualified),
	)
	sum := sha256.Sum256([]byte(key))
	var hash [16]byte
	copy(hash[:], sum[:len(hash)])
	return coroPhysicalABI{
		hash:           hash,
		descriptorName: coroDescriptorPrefix + hex.EncodeToString(hash[:]),
		physicalSig:    physicalSig,
		resultSlotType: resultSlotType,
		resultCount:    sourceSig.Results().Len(),
	}
}

func (p *context) beginCoroLeaf(b llssa.Builder, abi coroPhysicalABI) (*llssa.CoroBuilder, llssa.Expr) {
	prog := p.prog
	resultType := prog.Type(abi.resultSlotType, llssa.InGo)
	descriptor := p.pkg.NewCoroFrameDescriptor(abi.descriptorName, llssa.CoroFrameDescriptorOptions{
		Version: coroPhysicalABIVersion,
		ABIHash: abi.hash,
		Result:  resultType,
	})
	descriptorPtr := b.Convert(prog.VoidPtr(), descriptor)
	task := p.fn.PhysicalParam(0)
	resultSlot := p.fn.PhysicalParam(1)
	null := prog.Nil(prog.VoidPtr())
	headerType := prog.Struct(
		prog.VoidPtr(), // g
		prog.VoidPtr(), // parent
		prog.VoidPtr(), // descriptor
		prog.VoidPtr(), // allocation base (published by the future runtime)
		prog.VoidPtr(), // result slot
		prog.Uint16(),  // suspend reason
		prog.Uint16(),  // lifecycle state
		prog.Uint32(),  // state ID
		prog.Uint32(),  // flags
	)
	header := b.AllocaT(headerType)
	headerValues := []llssa.Expr{
		task,
		null,
		descriptorPtr,
		null,
		resultSlot,
		prog.IntVal(0, prog.Uint16()),
		prog.IntVal(0, prog.Uint16()),
		prog.IntVal(0, prog.Uint32()),
		prog.IntVal(0, prog.Uint32()),
	}
	allocSig := coroFrameAllocSignature()
	freeSig := coroFrameFreeSignature()
	alloc := p.pkg.NewFunc(coroFrameAllocHook, allocSig, llssa.InC)
	free := p.pkg.NewFunc(coroFrameFreeHook, freeSig, llssa.InC)
	frame := llssa.CoroFrameOps{
		Alloc: func(b llssa.Builder, size, align llssa.Expr) llssa.Expr {
			return b.Call(alloc.Expr, size, align, descriptorPtr)
		},
		Free: func(b llssa.Builder, storage, size, align llssa.Expr) {
			b.Call(free.Expr, storage, size, align, descriptorPtr)
		},
	}
	return b.BeginCoro(llssa.CoroOptions{
		Promise: header,
		Frame:   frame,
		BeforeInitialSuspend: func(b llssa.Builder, _ llssa.Expr) {
			for i, value := range headerValues {
				b.Store(b.FieldAddr(header, i), value)
			}
		},
	}), resultSlot
}

func coroFrameAllocSignature() *types.Signature {
	params := types.NewTuple(
		types.NewParam(token.NoPos, nil, "size", types.Typ[types.Uintptr]),
		types.NewParam(token.NoPos, nil, "align", types.Typ[types.Uintptr]),
		types.NewParam(token.NoPos, nil, "descriptor", types.Typ[types.UnsafePointer]),
	)
	results := types.NewTuple(types.NewParam(token.NoPos, nil, "frame", types.Typ[types.UnsafePointer]))
	return types.NewSignatureType(nil, nil, nil, params, results, false)
}

func coroFrameFreeSignature() *types.Signature {
	params := types.NewTuple(
		types.NewParam(token.NoPos, nil, "frame", types.Typ[types.UnsafePointer]),
		types.NewParam(token.NoPos, nil, "size", types.Typ[types.Uintptr]),
		types.NewParam(token.NoPos, nil, "align", types.Typ[types.Uintptr]),
		types.NewParam(token.NoPos, nil, "descriptor", types.Typ[types.UnsafePointer]),
	)
	return types.NewSignatureType(nil, nil, nil, params, nil, false)
}

func (p *context) storeCoroLeafResult(b llssa.Builder, abi coroPhysicalABI, resultSlot llssa.Expr, results []llssa.Expr) {
	if len(results) != abi.resultCount {
		panic(fmt.Sprintf("coroutine result count %d does not match ABI count %d", len(results), abi.resultCount))
	}
	if len(results) == 0 {
		return
	}
	resultType := p.prog.Type(abi.resultSlotType, llssa.InGo)
	typedSlot := b.Convert(p.prog.Pointer(resultType), resultSlot)
	b.Store(b.FieldAddr(typedSlot, 0), results[0])
}

func (p *context) compileCoroLeafBody(b llssa.Builder, fn *ssa.Function, abi coroPhysicalABI) {
	if len(fn.Blocks) != 1 {
		panic("coroutine leaf body reached codegen without one-block preflight")
	}
	oldBase := p.sourceParamBase
	p.sourceParamBase = 2
	defer func() { p.sourceParamBase = oldBase }()

	b.SetBlock(p.fn.Block(0))
	if enableDbgSyms && fn.Origin() == nil {
		p.debugParams(b, fn)
	}
	leaf, resultSlot := p.beginCoroLeaf(b, abi)
	body := leaf.InitialResumeBlock()
	completion := p.fn.MakeBlock()
	b.SetBlock(body)

	for _, instr := range fn.Blocks[0].Instrs {
		if _, debug := instr.(*ssa.DebugRef); debug {
			// Source block 0 is not physical ramp block 0. Until the general
			// source-to-resume block map lands, omit local debug intrinsics
			// instead of emitting a non-dominating use into the ramp.
			continue
		}
		if ret, ok := instr.(*ssa.Return); ok {
			results := make([]llssa.Expr, len(ret.Results))
			for i, result := range ret.Results {
				results[i] = p.compileValue(b, result)
			}
			p.storeCoroLeafResult(b, abi, resultSlot, results)
			b.Jump(completion)
			continue
		}
		p.compileInstr(b, instr)
	}
	b.SetBlock(completion)
	leaf.Finish()
}

func validateCoroLeafPhysicalABI(fn *ssa.Function, plan coro.FunctionPlan) error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("coroutine physical ABI: function %q: %s", plan.ID, fmt.Sprintf(format, args...))
	}
	if fn == nil || plan.External != coro.Defined || len(fn.Blocks) == 0 {
		return fail("requires one defined SSA body")
	}
	if plan.Primary != coro.PrimaryCoroutine || plan.FuncRep != coro.DirectCoro {
		return fail("requires a direct coroutine primary, got primary=%s representation=%s", plan.Primary, plan.FuncRep)
	}
	if plan.Demand != coro.AsyncDemand {
		return fail("requires async-only demand until root and hard-sync adapters exist, got %s", plan.Demand)
	}
	if plan.Recursive {
		return fail("recursive coroutine lowering requires child frames and preemption polls")
	}
	if plan.DeclaredEffect != coro.YieldOnly || plan.LocalEffect != coro.YieldOnly || plan.Effect != coro.YieldOnly {
		return fail("requires an explicit, isolated yield-only effect, got declared=%s local=%s final=%s", plan.DeclaredEffect, plan.LocalEffect, plan.Effect)
	}
	if unsupported := plan.Exec &^ coro.MayUnwind; unsupported != 0 {
		return fail("execution flags %s require lowering outside the leaf ABI", unsupported)
	}
	if fn.Parent() != nil || len(fn.FreeVars) != 0 {
		return fail("closures require the coroutine context ABI")
	}
	if len(fn.AnonFuncs) != 0 {
		return fail("nested function literals require closure body lowering")
	}
	if fn.Signature.Recv() != nil {
		return fail("methods require descriptor and receiver ABI lowering")
	}
	if fn.Signature.Variadic() {
		return fail("variadic coroutine ABI is not implemented")
	}
	if directive := coroLeafABIDirective(fn); directive != "" {
		return fail("ABI directive %q requires a root or foreign adapter", directive)
	}
	if isCgoExternSymbol(fn) {
		return fail("cgo entry requires a foreign adapter")
	}
	if fn.Synthetic != "" {
		return fail("synthetic function %q is outside the leaf ABI", fn.Synthetic)
	}
	if list := fn.TypeParams(); list != nil && list.Len() != 0 {
		return fail("generic declarations are not materialized coroutine bodies")
	}
	if list := fn.TypeArgs(); len(list) != 0 {
		return fail("generic instances require a frozen instantiated ABI")
	}
	if fn.Name() == "main" || strings.HasPrefix(fn.Name(), "init") {
		return fail("program roots require scheduler bootstrap lowering")
	}
	if len(fn.Blocks) != 1 {
		return fail("requires exactly one basic block, got %d", len(fn.Blocks))
	}
	if err := validateCoroLeafPhysicalSignature(plan, fn.Signature); err != nil {
		return err
	}

	returns := 0
	for _, instr := range fn.Blocks[0].Instrs {
		switch instr := instr.(type) {
		case *ssa.DebugRef:
		case *ssa.Return:
			returns++
		case *ssa.BinOp:
			if instr.Op == token.QUO || instr.Op == token.REM || instr.Op == token.SHL || instr.Op == token.SHR ||
				!coroLeafScalar(instr.Type()) ||
				!coroLeafScalar(instr.X.Type()) || !coroLeafScalar(instr.Y.Type()) {
				return coroLeafInstructionError(fn, plan, instr, "potentially panicking or non-scalar binary operation")
			}
		case *ssa.UnOp:
			if (instr.Op != token.SUB && instr.Op != token.XOR && instr.Op != token.NOT) || !coroLeafScalar(instr.Type()) {
				return coroLeafInstructionError(fn, plan, instr, "unsupported unary operation")
			}
		case *ssa.Convert, *ssa.ChangeType:
			value, ok := instr.(ssa.Value)
			if !ok || !coroLeafScalar(value.Type()) {
				return coroLeafInstructionError(fn, plan, instr, "non-scalar conversion")
			}
		default:
			return coroLeafInstructionError(fn, plan, instr, "instruction is outside the ABI-only leaf allowlist")
		}
	}
	if returns != 1 {
		return fail("requires exactly one return instruction, got %d", returns)
	}
	return nil
}

func (u *EmissionUniverse) coroPhysicalSourceSignature(fn *ssa.Function) (*types.Signature, error) {
	owner := u.ownerOf(fn)
	ctx, err := u.functionABIContext(fn, owner)
	if err != nil {
		return nil, fmt.Errorf("coroutine physical ABI: function %q: derive effective signature: %w", fn.Name(), err)
	}
	sig, ok := ctx.patchType(fn.Signature).(*types.Signature)
	if !ok {
		return nil, fmt.Errorf("coroutine physical ABI: function %q: effective type is not a signature", fn.Name())
	}
	return sig, nil
}

func validateCoroLeafPhysicalSignature(plan coro.FunctionPlan, sig *types.Signature) error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("coroutine physical ABI: function %q: %s", plan.ID, fmt.Sprintf(format, args...))
	}
	if sig == nil {
		return fail("requires a physical source signature")
	}
	if sig.Recv() != nil {
		return fail("effective method receiver requires descriptor lowering")
	}
	if sig.Variadic() {
		return fail("effective variadic coroutine ABI is not implemented")
	}
	for i := 0; i < sig.Params().Len(); i++ {
		if !coroLeafScalar(sig.Params().At(i).Type()) {
			return fail("parameter %d has unsupported type %s", i, sig.Params().At(i).Type())
		}
	}
	if sig.Results().Len() > 1 {
		return fail("supports at most one result, got %d", sig.Results().Len())
	}
	if sig.Results().Len() == 1 && !coroLeafScalar(sig.Results().At(0).Type()) {
		return fail("result has unsupported type %s", sig.Results().At(0).Type())
	}
	return nil
}

func coroLeafABIDirective(fn *ssa.Function) string {
	decl, _ := fn.Syntax().(*ast.FuncDecl)
	if decl == nil || decl.Doc == nil {
		return ""
	}
	for _, comment := range decl.Doc.List {
		text := strings.TrimSpace(comment.Text)
		for _, prefix := range []string{
			"//go:linkname", "//llgo:link", "// llgo:link", "//export", "//go:wasmexport", "//go:wasmimport",
		} {
			if text == prefix || strings.HasPrefix(text, prefix+" ") {
				return text
			}
		}
		if strings.HasPrefix(text, "//go:cgo_") {
			return text
		}
	}
	return ""
}

func validateCoroPhysicalConsumers(plan *coro.SSAPlan) error {
	coroutineIDs := make(map[coro.FunctionID]struct{})
	for _, function := range plan.Functions() {
		if function.Plan.Primary == coro.PrimaryCoroutine {
			coroutineIDs[function.Plan.ID] = struct{}{}
		}
	}
	for _, function := range plan.Functions() {
		fn := function.Function
		for _, block := range fn.Blocks {
			for _, instr := range block.Instrs {
				if _, spawn := instr.(*ssa.Go); spawn {
					return coroLeafInstructionError(fn, function.Plan, instr, "goroutine spawn requires scheduler root lowering")
				}
				if call, ok := instr.(ssa.CallInstruction); ok {
					callPlan, found := plan.CallPlan(call)
					if !found {
						return coroLeafInstructionError(fn, function.Plan, instr, "call has no compilation CallPlan")
					}
					for _, target := range callPlan.Targets {
						if _, isCoroutine := coroutineIDs[target]; isCoroutine {
							return coroLeafInstructionError(fn, function.Plan, instr, "coroutine target requires direct await or root lowering")
						}
					}
				}
				for _, operand := range instr.Operands(nil) {
					if operand == nil || *operand == nil {
						continue
					}
					target, ok := (*operand).(*ssa.Function)
					if !ok {
						continue
					}
					targetPlan, planned := plan.FunctionPlan(target)
					if planned && targetPlan.Primary == coro.PrimaryCoroutine {
						return coroLeafInstructionError(fn, function.Plan, instr, "coroutine function value requires physical representation conversion")
					}
				}
			}
		}
	}
	return nil
}

func coroLeafScalar(typ types.Type) bool {
	typ = types.Unalias(typ)
	if named, ok := typ.(*types.Named); ok {
		typ = named.Underlying()
	}
	basic, ok := typ.Underlying().(*types.Basic)
	if !ok || basic.Kind() == types.Uintptr {
		return false
	}
	info := basic.Info()
	return info&(types.IsBoolean|types.IsInteger|types.IsFloat) != 0
}

func coroLeafInstructionError(fn *ssa.Function, plan coro.FunctionPlan, instr ssa.Instruction, reason string) error {
	pos := fn.Prog.Fset.Position(instr.Pos())
	where := "unknown position"
	if pos.IsValid() {
		where = fmt.Sprintf("%s:%d:%d", pos.Filename, pos.Line, pos.Column)
	}
	return fmt.Errorf("coroutine physical ABI: function %q: %T at %s: %s", plan.ID, instr, where, reason)
}
