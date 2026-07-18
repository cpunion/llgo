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
	"go/token"
	"go/types"
	"strconv"
	"strings"

	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

const (
	coroPlainDispatchVersion          = llssa.CoroPlainDispatchVersionV1
	coroPlainDispatchFlags            = llssa.CoroPlainDispatchFlagsV1
	coroPlainDispatchDescriptorPrefix = "__llgo_coro_func_descriptor_v1."
	coroPlainDispatchThunkPrefix      = "__llgo_coro_func_plain_v1."
)

// coroPlainDispatchABI is deliberately target independent of the selected
// function body. Every function with the same canonical callable ABI receives
// the same hash, while its FunctionID digest is used only to make the descriptor
// and thunk symbols target-specific.
type coroPlainDispatchABI struct {
	hash           [16]byte
	signature      *types.Signature
	resultSlotType types.Type
}

func validateCoroPlainDispatchTarget(fn *ssa.Function, plan coro.FunctionPlan) error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("coroutine plain dispatch ABI: function %q: %s", plan.ID, fmt.Sprintf(format, args...))
	}
	if fn == nil || plan.External != coro.Defined || len(fn.Blocks) == 0 {
		return fail("requires one defined SSA body")
	}
	if plan.Emission != coro.EmitPlain || plan.Primary != coro.PrimaryPlain || plan.FuncRep != coro.Dispatch {
		return fail("requires plain descriptor emission, got emission=%s primary=%s representation=%s", plan.Emission, plan.Primary, plan.FuncRep)
	}
	if plan.Effect != coro.NoSuspend || plan.Effect.IsOpaque() {
		return fail("requires an exact non-suspending effect, got %s", plan.Effect)
	}
	if plan.Exec.Contains(coro.NeedsPreempt) || plan.Exec.IsOpaque() {
		return fail("execution flags %s require coroutine or open dispatch lowering", plan.Exec)
	}
	if len(fn.FreeVars) != 0 {
		return fail("captured closures require an environment descriptor")
	}
	if fn.Signature == nil || fn.Signature.Recv() != nil {
		return fail("methods require receiver-aware dispatch lowering")
	}
	if fn.Signature.Variadic() {
		return fail("variadic dispatch is not implemented")
	}
	if directive := coroLeafABIDirective(fn); directive != "" {
		return fail("ABI directive %q requires an explicit boundary adapter", directive)
	}
	if isCgoExternSymbol(fn) {
		return fail("cgo entry requires a foreign adapter")
	}
	if fn.Synthetic != "" {
		return fail("synthetic function %q is outside the plain dispatch ABI", fn.Synthetic)
	}
	if params := fn.TypeParams(); params != nil && params.Len() != 0 {
		return fail("generic declarations are not materialized dispatch bodies")
	}
	if len(fn.TypeArgs()) != 0 || fn.Origin() != nil {
		return fail("generic instances require a frozen instantiated dispatch ABI")
	}
	if path, ok := nestedFunctionTypePath(fn.Signature); ok {
		return fail("nested function type at %s requires recursive function-representation lowering", path)
	}
	if err := validateCoroPlainDispatchSignatureShape(fn.Signature); err != nil {
		return fail("signature: %v", err)
	}
	return nil
}

func validateCoroPlainDispatchSignatureShape(sig *types.Signature) error {
	if sig == nil {
		return fmt.Errorf("missing signature")
	}
	if sig.Results().Len() > 1 {
		return fmt.Errorf("multiple results are not implemented")
	}
	for _, item := range []struct {
		role  string
		tuple *types.Tuple
	}{
		{"parameter", sig.Params()},
		{"result", sig.Results()},
	} {
		for i := 0; i < item.tuple.Len(); i++ {
			if !coroPlainDispatchSourceScalar(item.tuple.At(i).Type()) {
				return fmt.Errorf("%s %d type %s is not a supported scalar", item.role, i, item.tuple.At(i).Type())
			}
		}
	}
	return nil
}

func coroPlainDispatchSourceScalar(typ types.Type) bool {
	typ = types.Unalias(typ)
	if named, ok := typ.(*types.Named); ok {
		return coroPlainDispatchSourceScalar(named.Underlying())
	}
	switch value := typ.Underlying().(type) {
	case *types.Basic:
		info := value.Info()
		return value.Kind() == types.UnsafePointer || info&(types.IsBoolean|types.IsInteger|types.IsFloat) != 0
	case *types.Pointer, *types.Map, *types.Chan:
		return true
	default:
		return false
	}
}

func validateCoroPlainDispatchConsumers(plan *coro.SSAPlan, interfacePlain *coroClosedInterfacePlainPlan) error {
	if plan == nil {
		return fmt.Errorf("coroutine plain dispatch ABI requires a compilation plan")
	}
	for _, function := range plan.Functions() {
		if function.Plan.Emission != coro.EmitPlain && function.Plan.Emission != coro.EmitCoroutine {
			continue
		}
		fn := function.Function
		for _, param := range fn.Params {
			if err := validateCoroPlainDispatchValue(plan, fn, param); err != nil {
				return err
			}
		}
		for _, free := range fn.FreeVars {
			if err := validateCoroPlainDispatchValue(plan, fn, free); err != nil {
				return err
			}
		}
		for _, block := range fn.Blocks {
			for _, instr := range block.Instrs {
				if value, ok := instr.(ssa.Value); ok {
					if err := validateCoroPlainDispatchValue(plan, fn, value); err != nil {
						return err
					}
				}
				for _, operand := range instr.Operands(nil) {
					if operand != nil && *operand != nil {
						if err := validateCoroPlainDispatchValue(plan, fn, *operand); err != nil {
							return err
						}
					}
				}
				if boxed, ok := instr.(*ssa.MakeInterface); ok {
					if valuePlan, found := plan.ValuePlan(boxed.X); found && funcRepMapContains(valuePlan.Funcs, coro.Dispatch) {
						return coroPlainDispatchInstructionError(fn, instr, "interface boxing of a descriptor-backed function value is not implemented")
					}
				}
				call, ok := instr.(ssa.CallInstruction)
				if !ok || plan.ElidesCall(call) {
					continue
				}
				common := call.Common()
				if common != nil {
					if _, builtin := common.Value.(*ssa.Builtin); builtin {
						continue
					}
				}
				callPlan, found := plan.CallPlan(call)
				if !found {
					return coroPlainDispatchInstructionError(fn, instr, "call has no compilation CallPlan")
				}
				if callPlan.Rep != coro.Dispatch {
					continue
				}
				if interfacePlain.acceptsCall(call) {
					continue
				}
				if err := validateCoroPlainDispatchCall(plan, fn, call, callPlan); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validateCoroPlainDispatchValue(plan *coro.SSAPlan, owner *ssa.Function, value ssa.Value) error {
	valuePlan, found := plan.ValuePlan(value)
	if !found || !funcRepMapContains(valuePlan.Funcs, coro.Dispatch) {
		return nil
	}
	if len(valuePlan.Funcs) != 1 || len(valuePlan.Funcs[0].Path) != 0 {
		// Aggregate storage does not change the physical width of a function
		// leaf: both direct and descriptor-backed values remain two pointers.
		// Every aggregate leaf is canonical Dispatch, while exact scalar
		// producers and consumers are validated separately. Interface boxing is
		// still rejected at its instruction boundary below.
		for _, leaf := range valuePlan.Funcs {
			if leaf.Rep != coro.Dispatch {
				return fmt.Errorf("coroutine plain dispatch ABI: function %q: aggregate value %q has non-Dispatch function leaf", owner.Name(), value.Name())
			}
		}
		return nil
	}
	leaf := valuePlan.Funcs[0]
	if leaf.Rep != coro.Dispatch {
		return fmt.Errorf("coroutine plain dispatch ABI: function %q: value %q has a mixed function representation", owner.Name(), value.Name())
	}
	if _, ok := types.Unalias(value.Type()).Underlying().(*types.Signature); !ok {
		return fmt.Errorf("coroutine plain dispatch ABI: function %q: value %q is not a scalar function value", owner.Name(), value.Name())
	}
	if len(leaf.Targets) > 1 {
		return fmt.Errorf("coroutine plain dispatch ABI: function %q: value %q has %d targets; multi-target dispatch is not implemented", owner.Name(), value.Name(), len(leaf.Targets))
	}
	if len(leaf.Targets) == 0 {
		if !leaf.MayBeNil {
			return fmt.Errorf("coroutine plain dispatch ABI: function %q: value %q has no target and is not nil", owner.Name(), value.Name())
		}
		return nil
	}
	target, targetPlan, err := coroPlainDispatchPlanTarget(plan, leaf.Targets[0])
	if err != nil {
		return fmt.Errorf("coroutine plain dispatch ABI: function %q: value %q: %w", owner.Name(), value.Name(), err)
	}
	return validateCoroPlainDispatchTarget(target, targetPlan)
}

func validateCoroPlainDispatchCall(plan *coro.SSAPlan, owner *ssa.Function, call ssa.CallInstruction, callPlan coro.SSACallPlan) error {
	fail := func(format string, args ...any) error {
		return coroPlainDispatchInstructionError(owner, call, fmt.Sprintf(format, args...))
	}
	direct, ordinary := call.(*ssa.Call)
	if !ordinary || direct == nil || callPlan.Kind != coro.CallDirect {
		return fail("descriptor dispatch is supported only for an ordinary direct call instruction")
	}
	common := direct.Common()
	if common == nil || common.StaticCallee() != nil || common.IsInvoke() || common.Method != nil {
		return fail("descriptor dispatch requires an ordinary dynamic function call")
	}
	if callPlan.Open || callPlan.Unresolved == coro.UnknownForeign {
		return fail("open or foreign descriptor dispatch is not implemented")
	}
	if len(callPlan.Targets) > 1 {
		return fail("multi-target descriptor dispatch is not implemented")
	}
	if len(callPlan.Targets) == 0 {
		if !callPlan.MayBeNil {
			return fail("closed descriptor call has no target and is not nil")
		}
	} else {
		targetFn, targetPlan, err := coroPlainDispatchPlanTarget(plan, callPlan.Targets[0])
		if err != nil {
			return fail("%v", err)
		}
		if err := validateCoroPlainDispatchTarget(targetFn, targetPlan); err != nil {
			return fail("%v", err)
		}
		if !types.Identical(common.Signature(), targetFn.Signature) {
			return fail("call signature %s does not match target %q signature %s", common.Signature(), targetPlan.ID, targetFn.Signature)
		}
	}
	valuePlan, found := plan.ValuePlan(common.Value)
	if !found || len(valuePlan.Funcs) != 1 || len(valuePlan.Funcs[0].Path) != 0 || valuePlan.Funcs[0].Rep != coro.Dispatch {
		return fail("callee has no exact scalar Dispatch ValuePlan")
	}
	leaf := valuePlan.Funcs[0]
	if len(leaf.Targets) != len(callPlan.Targets) {
		return fail("callee target count %d conflicts with CallPlan target count %d", len(leaf.Targets), len(callPlan.Targets))
	}
	for i := range leaf.Targets {
		if leaf.Targets[i] != callPlan.Targets[i] {
			return fail("callee target %q conflicts with CallPlan target %q", leaf.Targets[i], callPlan.Targets[i])
		}
	}
	if leaf.MayBeNil != callPlan.MayBeNil {
		return fail("callee nilability %t conflicts with CallPlan nilability %t", leaf.MayBeNil, callPlan.MayBeNil)
	}
	return nil
}

func funcRepMapContains(reps coro.FuncRepMap, want coro.FuncRep) bool {
	for _, leaf := range reps {
		if leaf.Rep == want {
			return true
		}
	}
	return false
}

func coroPlainDispatchPlanTarget(plan *coro.SSAPlan, id coro.FunctionID) (*ssa.Function, coro.FunctionPlan, error) {
	target, found := plan.Function(id)
	if !found || target == nil {
		return nil, coro.FunctionPlan{}, fmt.Errorf("target %q is absent from the compilation plan", id)
	}
	targetPlan, found := plan.FunctionPlan(target)
	if !found || targetPlan.ID != id {
		return nil, coro.FunctionPlan{}, fmt.Errorf("target %q has no canonical function plan", id)
	}
	return target, targetPlan, nil
}

func coroPlainDispatchInstructionError(fn *ssa.Function, instr ssa.Instruction, reason string) error {
	position := token.Position{}
	if fn != nil && fn.Prog != nil && fn.Prog.Fset != nil && instr != nil {
		position = fn.Prog.Fset.Position(instr.Pos())
	}
	return fmt.Errorf("coroutine plain dispatch ABI: function %q at %s: %s", fn.Name(), position, reason)
}

func nestedFunctionTypePath(typ types.Type) (string, bool) {
	seen := make(map[types.Type]bool)
	var visit func(types.Type, string, bool) (string, bool)
	visit = func(typ types.Type, path string, root bool) (string, bool) {
		if typ == nil {
			return "", false
		}
		typ = types.Unalias(typ)
		if seen[typ] {
			return "", false
		}
		seen[typ] = true
		switch value := typ.(type) {
		case *types.Signature:
			if !root {
				return path, true
			}
			for i := 0; i < value.Params().Len(); i++ {
				if found, ok := visit(value.Params().At(i).Type(), fmt.Sprintf("param[%d]", i), false); ok {
					return found, true
				}
			}
			for i := 0; i < value.Results().Len(); i++ {
				if found, ok := visit(value.Results().At(i).Type(), fmt.Sprintf("result[%d]", i), false); ok {
					return found, true
				}
			}
		case *types.Named:
			return visit(value.Underlying(), path+".underlying", false)
		case *types.Pointer:
			// Pointer identity is part of the canonical logical signature, while
			// its physical layout terminates at one opaque pointer.
			return "", false
		case *types.Array:
			return visit(value.Elem(), path+".elem", false)
		case *types.Slice:
			return visit(value.Elem(), path+".elem", false)
		case *types.Map:
			if found, ok := visit(value.Key(), path+".key", false); ok {
				return found, true
			}
			return visit(value.Elem(), path+".elem", false)
		case *types.Chan:
			return visit(value.Elem(), path+".elem", false)
		case *types.Struct:
			for i := 0; i < value.NumFields(); i++ {
				if found, ok := visit(value.Field(i).Type(), fmt.Sprintf("%s.field[%d]", path, i), false); ok {
					return found, true
				}
			}
		case *types.Interface:
			for i := 0; i < value.NumExplicitMethods(); i++ {
				if found, ok := visit(value.ExplicitMethod(i).Type(), fmt.Sprintf("%s.method[%d]", path, i), false); ok {
					return found, true
				}
			}
		}
		return "", false
	}
	return visit(typ, "signature", true)
}

func newCoroPlainDispatchABI(p *context, signature *types.Signature) (coroPlainDispatchABI, error) {
	if p == nil || p.prog == nil || signature == nil {
		return coroPlainDispatchABI{}, fmt.Errorf("coroutine plain dispatch ABI requires a program and signature")
	}
	if path, ok := nestedFunctionTypePath(signature); ok {
		return coroPlainDispatchABI{}, fmt.Errorf("nested function type at %s is unsupported", path)
	}
	patched, ok := p.patchType(signature).(*types.Signature)
	if !ok {
		return coroPlainDispatchABI{}, fmt.Errorf("patched dispatch signature is %T", p.patchType(signature))
	}
	patched = canonicalCoroPlainDispatchSignature(patched)
	physical := p.prog.PhysicalFuncDecl(patched, llssa.InGo)
	resultFields := make([]*types.Var, physical.Results().Len())
	for i := range resultFields {
		resultFields[i] = types.NewField(token.NoPos, nil, fmt.Sprintf("r%d", i), physical.Results().At(i).Type(), false)
	}
	resultSlot := types.NewStruct(resultFields, nil)

	qualified := func(pkg *types.Package) string {
		if pkg == nil {
			return ""
		}
		return llssa.PathOf(pkg)
	}
	var key strings.Builder
	writeDispatchHashField(&key, "domain", "llgo.coro.func-dispatch.v1")
	writeDispatchHashField(&key, "version", strconv.FormatUint(uint64(coroPlainDispatchVersion), 10))
	writeDispatchHashField(&key, "flags", strconv.FormatUint(uint64(coroPlainDispatchFlags), 10))
	writeDispatchHashField(&key, "closure", "two-pointer:descriptor,env;entry=(env,args)->results;env=nil")
	writeDispatchHashField(&key, "panic", activeCompilationABI(p.compilation, func(c *Compilation) string { return c.PanicABI }, coro.PanicLegacyABIV0))
	writeDispatchHashField(&key, "func-rep", activeCompilationABI(p.compilation, func(c *Compilation) string { return c.FuncRepABI }, coro.FuncRepABIV1))
	target := p.prog.TargetSpec()
	writeDispatchHashField(&key, "triple", target.Triple)
	writeDispatchHashField(&key, "cpu", target.CPU)
	writeDispatchHashField(&key, "features", target.Features)
	writeDispatchHashField(&key, "target-abi", target.TargetABI)
	writeDispatchHashField(&key, "data-layout", p.prog.DataLayout())
	writeDispatchHashField(&key, "pointer-bytes", strconv.Itoa(p.prog.PointerSize()))
	writeDispatchHashField(&key, "byte-order", strconv.Itoa(int(p.prog.TargetData().ByteOrder())))
	writeDispatchHashField(&key, "logical-signature", types.TypeString(patched, qualified))
	writeDispatchHashField(&key, "physical-signature", types.TypeString(physical, qualified))
	if err := appendCoroPlainDispatchTupleLayout(&key, p.prog, "params", physical.Params(), qualified); err != nil {
		return coroPlainDispatchABI{}, err
	}
	if err := appendCoroPlainDispatchTupleLayout(&key, p.prog, "results", physical.Results(), qualified); err != nil {
		return coroPlainDispatchABI{}, err
	}
	if err := appendCoroPlainDispatchTypeLayout(&key, p.prog, "result-slot", resultSlot, qualified, make(map[types.Type]bool)); err != nil {
		return coroPlainDispatchABI{}, err
	}
	sum := sha256.Sum256([]byte(key.String()))
	var hash [16]byte
	copy(hash[:], sum[:len(hash)])
	return coroPlainDispatchABI{hash: hash, signature: patched, resultSlotType: resultSlot}, nil
}

// canonicalCoroPlainDispatchSignature removes source parameter/result names.
// go/types identity ignores those names, and a target declaration commonly has
// them while a function-typed parameter at the exact dynamic call does not.
// Letting names enter the descriptor hash would make two ABI-identical sites
// disagree at runtime.
func canonicalCoroPlainDispatchSignature(sig *types.Signature) *types.Signature {
	params := make([]*types.Var, sig.Params().Len())
	for i := range params {
		params[i] = types.NewParam(token.NoPos, nil, "", sig.Params().At(i).Type())
	}
	results := make([]*types.Var, sig.Results().Len())
	for i := range results {
		results[i] = types.NewParam(token.NoPos, nil, "", sig.Results().At(i).Type())
	}
	return types.NewSignatureType(nil, nil, nil, types.NewTuple(params...), types.NewTuple(results...), false)
}

func activeCompilationABI(c *Compilation, value func(*Compilation) string, fallback string) string {
	if c != nil {
		if current := value(c); current != "" {
			return current
		}
	}
	return fallback
}

func writeDispatchHashField(builder *strings.Builder, name, value string) {
	builder.WriteString(strconv.Itoa(len(name)))
	builder.WriteByte(':')
	builder.WriteString(name)
	builder.WriteByte('=')
	builder.WriteString(strconv.Itoa(len(value)))
	builder.WriteByte(':')
	builder.WriteString(value)
	builder.WriteByte('\n')
}

func appendCoroPlainDispatchTupleLayout(builder *strings.Builder, prog llssa.Program, path string, tuple *types.Tuple, qualified types.Qualifier) error {
	writeDispatchHashField(builder, path+".count", strconv.Itoa(tuple.Len()))
	for i := 0; i < tuple.Len(); i++ {
		if err := appendCoroPlainDispatchTypeLayout(builder, prog, fmt.Sprintf("%s[%d]", path, i), tuple.At(i).Type(), qualified, make(map[types.Type]bool)); err != nil {
			return err
		}
	}
	return nil
}

func appendCoroPlainDispatchTypeLayout(builder *strings.Builder, prog llssa.Program, path string, typ types.Type, qualified types.Qualifier, visiting map[types.Type]bool) error {
	if typ == nil {
		return fmt.Errorf("coroutine plain dispatch ABI: nil type at %s", path)
	}
	typ = types.Unalias(typ)
	writeDispatchHashField(builder, path+".type", types.TypeString(typ, qualified))
	physical := prog.Type(typ, llssa.InC)
	writeDispatchHashField(builder, path+".size", strconv.FormatUint(prog.SizeOf(physical), 10))
	writeDispatchHashField(builder, path+".align", strconv.FormatUint(prog.AlignOf(physical), 10))
	if visiting[typ] {
		writeDispatchHashField(builder, path+".cycle", "true")
		return nil
	}
	visiting[typ] = true
	defer delete(visiting, typ)
	switch value := typ.(type) {
	case *types.Named:
		return appendCoroPlainDispatchTypeLayout(builder, prog, path+".underlying", value.Underlying(), qualified, visiting)
	case *types.Pointer:
		writeDispatchHashField(builder, path+".pointer", "opaque")
	case *types.Struct:
		writeDispatchHashField(builder, path+".fields", strconv.Itoa(value.NumFields()))
		for i := 0; i < value.NumFields(); i++ {
			writeDispatchHashField(builder, fmt.Sprintf("%s.field[%d].offset", path, i), strconv.FormatUint(prog.OffsetOf(physical, i), 10))
			if err := appendCoroPlainDispatchTypeLayout(builder, prog, fmt.Sprintf("%s.field[%d]", path, i), value.Field(i).Type(), qualified, visiting); err != nil {
				return err
			}
		}
	case *types.Array:
		writeDispatchHashField(builder, path+".length", strconv.FormatInt(value.Len(), 10))
		return appendCoroPlainDispatchTypeLayout(builder, prog, path+".element", value.Elem(), qualified, visiting)
	case *types.Signature:
		return fmt.Errorf("coroutine plain dispatch ABI: nested signature at %s", path)
	}
	return nil
}

func (p *context) tryCompileCoroPlainDispatchFunctionValue(b llssa.Builder, value *ssa.Function) (llssa.Expr, bool) {
	if p.compilation == nil || !p.compilation.EnableCoroPlainDispatch || p.compilation.CoroPlan == nil {
		return llssa.Expr{}, false
	}
	valuePlan, found := p.compilation.CoroPlan.ValuePlan(value)
	if !found || len(valuePlan.Funcs) != 1 || len(valuePlan.Funcs[0].Path) != 0 || valuePlan.Funcs[0].Rep != coro.Dispatch {
		return llssa.Expr{}, false
	}
	return p.emitCoroPlainDispatchValue(b, value, valuePlan.Funcs[0]), true
}

func (p *context) tryCompileCoroPlainDispatchClosure(b llssa.Builder, closure *ssa.MakeClosure) (llssa.Expr, bool) {
	if p.compilation == nil || !p.compilation.EnableCoroPlainDispatch || p.compilation.CoroPlan == nil {
		return llssa.Expr{}, false
	}
	valuePlan, found := p.compilation.CoroPlan.ValuePlan(closure)
	if !found || len(valuePlan.Funcs) != 1 || len(valuePlan.Funcs[0].Path) != 0 || valuePlan.Funcs[0].Rep != coro.Dispatch {
		return llssa.Expr{}, false
	}
	target, ok := closure.Fn.(*ssa.Function)
	if !ok || len(closure.Bindings) != 0 || len(target.FreeVars) != 0 {
		panic(fmt.Errorf("coroutine plain dispatch ABI: closure %q requires an unsupported captured or non-function producer", closure.Name()))
	}
	return p.emitCoroPlainDispatchValue(b, target, valuePlan.Funcs[0]), true
}

func (p *context) emitCoroPlainDispatchValue(b llssa.Builder, target *ssa.Function, leaf coro.FuncRepLeaf) llssa.Expr {
	if len(leaf.Targets) != 1 {
		panic(fmt.Errorf("coroutine plain dispatch ABI: producer %q requires one target, got %d", target.Name(), len(leaf.Targets)))
	}
	entry := p.mustFunctionSymbol(target)
	if entry.plan.ID != leaf.Targets[0] {
		panic(fmt.Errorf("coroutine plain dispatch ABI: producer %q target %q conflicts with plan %q", target.Name(), leaf.Targets[0], entry.plan.ID))
	}
	if err := validateCoroPlainDispatchTarget(entry.function, entry.plan); err != nil {
		panic(err)
	}
	abi, err := newCoroPlainDispatchABI(p, entry.function.Signature)
	if err != nil {
		panic(err)
	}
	plain, py, ftype := p.compileFunction(entry.function)
	if ftype != goFunc || plain == nil || py != nil {
		panic(fmt.Errorf("coroutine plain dispatch ABI: target %q did not compile as one Go function", entry.plan.ID))
	}
	targetHash := sha256.Sum256([]byte(entry.plan.ID))
	targetKey := hex.EncodeToString(targetHash[:8]) + "." + hex.EncodeToString(abi.hash[:])
	result := p.prog.Type(abi.resultSlotType, llssa.InC)
	descriptorName := coroPlainDispatchDescriptorPrefix + targetKey
	descriptor, found := p.coroPlainDescriptors[descriptorName]
	if !found {
		descriptor = p.pkg.NewCoroPlainDispatchDescriptor(
			descriptorName,
			llssa.CoroPlainDispatchDescriptorOptions{
				Version:     coroPlainDispatchVersion,
				Flags:       coroPlainDispatchFlags,
				ABIHash:     abi.hash,
				PlainTarget: plain.Expr,
				Signature:   abi.signature,
				ThunkName:   coroPlainDispatchThunkPrefix + targetKey,
				Result:      result,
			},
		)
		if p.coroPlainDescriptors == nil {
			p.coroPlainDescriptors = make(map[string]llssa.Expr)
		}
		p.coroPlainDescriptors[descriptorName] = descriptor
	}
	return b.MakeCoroPlainDispatchValue(abi.signature, descriptor)
}

func (p *context) tryCompileCoroPlainDispatchCall(b llssa.Builder, call *ssa.Call) (llssa.Expr, bool) {
	if p.compilation == nil || !p.compilation.EnableCoroPlainDispatch || p.compilation.CoroPlan == nil || call == nil {
		return llssa.Expr{}, false
	}
	callPlan, found := p.compilation.CoroPlan.CallPlan(call)
	if !found || callPlan.Rep != coro.Dispatch {
		return llssa.Expr{}, false
	}
	if p.compilation.coroClosedInterfacePlain.acceptsCall(call) {
		// Preserve the ordinary LLGo itab invoke. The closed candidate proof is
		// a scheduling constraint, not a second function-value representation.
		return llssa.Expr{}, false
	}
	if err := validateCoroPlainDispatchCall(p.compilation.CoroPlan, call.Parent(), call, callPlan); err != nil {
		panic(err)
	}
	p.recordCallerLocationForCall(b, &call.Call)
	p.emitPCLineLabel(b, call.Pos())
	fn := p.compileValue(b, call.Call.Value)
	args := p.compileValues(b, call.Call.Args, fnNormal)
	abi, err := newCoroPlainDispatchABI(p, call.Call.Signature())
	if err != nil {
		panic(err)
	}
	result := p.prog.Type(abi.resultSlotType, llssa.InC)
	return b.CallCoroPlainDispatch(fn, args, llssa.CoroPlainDispatchCallOptions{
		Version: coroPlainDispatchVersion,
		Flags:   coroPlainDispatchFlags,
		ABIHash: abi.hash,
		Result:  result,
	}), true
}
