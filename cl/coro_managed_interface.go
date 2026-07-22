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
	"go/types"

	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

const coroManagedInterfaceRawTrapPrefix = "__llgo_coro_method_raw_trap_v1."

// coroManagedInterfaceDispatchPlan freezes the exact method families whose
// ABI Method.Ifn_ word uses the universal {descriptor, receiver-environment}
// transport. A family is introduced only by an open CallPlan explicitly
// classified as UnknownManagedInterfaceDispatch. Closed invokes of the same
// method family must use the same transport because ABI type data has one Ifn_
// word per concrete method, independent of the source call site.
type coroManagedInterfaceDispatchPlan struct {
	calls   map[ssa.CallInstruction]struct{}
	methods map[string]struct{}
	targets map[coro.FunctionID]*ssa.Function
}

func (p *coroManagedInterfaceDispatchPlan) acceptsCall(call ssa.CallInstruction) bool {
	if p == nil || call == nil {
		return false
	}
	_, ok := p.calls[call]
	return ok
}

func (p *coroManagedInterfaceDispatchPlan) acceptsMethod(method *types.Func, signature *types.Signature) bool {
	if p == nil {
		return false
	}
	_, ok := p.methods[coroManagedInterfaceMethodKey(method, signature)]
	return ok
}

func (p *coroManagedInterfaceDispatchPlan) acceptsTarget(fn *ssa.Function, plan coro.FunctionPlan) bool {
	if p == nil || fn == nil {
		return false
	}
	target, ok := p.targets[plan.ID]
	return ok && target == fn
}

func coroManagedInterfaceMethodKey(method *types.Func, signature *types.Signature) string {
	if method == nil || signature == nil {
		return ""
	}
	callable := coroInterfaceDispatchCanonicalSignature(coroInterfaceDispatchCallableSignature(signature))
	if callable == nil {
		return ""
	}
	return method.Id() + "\x00" + structuralEmissionABITypeKey(callable)
}

func coroManagedInterfaceInvokeMethodKey(
	universe *EmissionUniverse, owner *ssa.Function, call ssa.CallInstruction,
) (string, error) {
	if owner == nil || call == nil || call.Common() == nil {
		return "", fmt.Errorf("managed interface descriptor requires an exact owner and call")
	}
	common := call.Common()
	if common.StaticCallee() != nil || !common.IsInvoke() || common.Method == nil {
		return "", fmt.Errorf("managed interface descriptor requires an ordinary interface invoke")
	}
	signature, err := coroInterfaceDispatchEffectiveCallableSignature(universe, owner, common.Signature())
	if err != nil {
		return "", err
	}
	key := coroManagedInterfaceMethodKey(common.Method, signature)
	if key == "" {
		return "", fmt.Errorf("managed interface descriptor has no exact method/signature key")
	}
	return key, nil
}

func analyzeCoroManagedInterfaceDispatchPlan(
	plan *coro.SSAPlan, universe *EmissionUniverse, enabled bool,
) (*coroManagedInterfaceDispatchPlan, error) {
	result := &coroManagedInterfaceDispatchPlan{
		calls:   make(map[ssa.CallInstruction]struct{}),
		methods: make(map[string]struct{}),
		targets: make(map[coro.FunctionID]*ssa.Function),
	}
	if plan == nil {
		return nil, fmt.Errorf("managed interface descriptor requires a compilation plan")
	}
	// First freeze only explicitly certified open method families.
	for _, owner := range plan.Functions() {
		if owner.Function == nil || (owner.Plan.Emission != coro.EmitPlain && owner.Plan.Emission != coro.EmitCoroutine) {
			continue
		}
		for _, block := range owner.Function.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok || plan.ElidesCall(call) {
					continue
				}
				callPlan, found := plan.CallPlan(call)
				if !found || !callPlan.Open || callPlan.Unresolved != coro.UnknownManagedInterfaceDispatch {
					continue
				}
				if !enabled {
					return nil, coroLeafInstructionError(owner.Function, owner.Plan, instruction,
						"managed interface descriptor transport is disabled")
				}
				if err := validateCoroManagedInterfaceDispatchCall(plan, universe, owner.Function, call, callPlan); err != nil {
					return nil, err
				}
				common := call.Common()
				key, err := coroManagedInterfaceInvokeMethodKey(universe, owner.Function, call)
				if err != nil {
					return nil, coroLeafInstructionError(owner.Function, owner.Plan, instruction, err.Error())
				}
				result.methods[key] = struct{}{}

				// An open managed invoke can retain a bounded set of exact CHA
				// candidates in addition to its UnknownManagedInterfaceDispatch
				// tail. Some candidates are not otherwise materialized in ABI type
				// data (for example, a dead promoted wrapper), but the planner still
				// demands their bodies conservatively. Freeze those exact receiver
				// targets here so entry validation uses the existing method
				// descriptor/receiver-environment ABI rather than misrouting them
				// through the receiver-free function-value descriptor validator.
				iface, ok := types.Unalias(common.Value.Type()).Underlying().(*types.Interface)
				if !ok {
					return nil, coroLeafInstructionError(owner.Function, owner.Plan, instruction,
						fmt.Sprintf("managed interface receiver %s is not an interface", common.Value.Type()))
				}
				iface.Complete()
				sourceSignature, err := coroInterfaceDispatchSourceSignature(common)
				if err != nil {
					return nil, coroLeafInstructionError(owner.Function, owner.Plan, instruction, err.Error())
				}
				for _, targetID := range callPlan.Targets {
					target, found := plan.Function(targetID)
					if !found || target == nil {
						return nil, coroLeafInstructionError(owner.Function, owner.Plan, instruction,
							fmt.Sprintf("managed interface target %q is absent from the compilation plan", targetID))
					}
					targetPlan, found := plan.FunctionPlan(target)
					if !found || targetPlan.ID != targetID {
						return nil, coroLeafInstructionError(owner.Function, owner.Plan, instruction,
							fmt.Sprintf("managed interface target %q has no exact function plan", targetID))
					}
					if _, _, _, err := validateCoroInterfaceDispatchCandidate(
						common, iface, sourceSignature, universe, owner.Function,
						targetID, target, targetPlan,
					); err != nil {
						return nil, coroLeafInstructionError(owner.Function, owner.Plan, instruction, err.Error())
					}
					if previous := result.targets[targetID]; previous != nil && previous != target {
						return nil, coroLeafInstructionError(owner.Function, owner.Plan, instruction,
							fmt.Sprintf("managed interface target %q resolves to both %q and %q", targetID, previous.Name(), target.Name()))
					}
					result.targets[targetID] = target
				}
			}
		}
	}

	if len(result.methods) == 0 {
		return result, nil
	}
	// Then bind every source invoke of those method families to the one physical
	// transport. An open call in another execution domain cannot safely share an
	// Ifn_ word and therefore fails before LLVM emission.
	for _, owner := range plan.Functions() {
		if owner.Function == nil || (owner.Plan.Emission != coro.EmitPlain && owner.Plan.Emission != coro.EmitCoroutine) {
			continue
		}
		for _, block := range owner.Function.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok || plan.ElidesCall(call) || call.Common() == nil || !call.Common().IsInvoke() {
					continue
				}
				key, err := coroManagedInterfaceInvokeMethodKey(universe, owner.Function, call)
				if err != nil {
					return nil, coroLeafInstructionError(owner.Function, owner.Plan, instruction, err.Error())
				}
				if _, required := result.methods[key]; !required {
					continue
				}
				callPlan, found := plan.CallPlan(call)
				if !found || callPlan.Rep != coro.Dispatch {
					return nil, coroLeafInstructionError(owner.Function, owner.Plan, instruction,
						"managed interface method family has no Dispatch CallPlan")
				}
				if callPlan.Open && callPlan.Unresolved != coro.UnknownManagedInterfaceDispatch {
					return nil, coroLeafInstructionError(owner.Function, owner.Plan, instruction,
						fmt.Sprintf("managed interface method family has conflicting open domain %v", callPlan.Unresolved))
				}
				result.calls[call] = struct{}{}
			}
		}
	}
	return result, nil
}

func validateCoroManagedInterfaceDispatchCall(
	plan *coro.SSAPlan,
	universe *EmissionUniverse,
	owner *ssa.Function,
	call ssa.CallInstruction,
	callPlan coro.SSACallPlan,
) error {
	fail := func(format string, args ...any) error {
		return coroPlainDispatchInstructionError(owner, call, "managed interface descriptor: "+fmt.Sprintf(format, args...))
	}
	direct, ordinary := call.(*ssa.Call)
	if plan == nil || owner == nil || !ordinary || direct == nil || direct.Parent() != owner || direct.Common() == nil {
		return fail("requires one exact ordinary call in the compilation plan")
	}
	common := direct.Common()
	if callPlan.Call != call || callPlan.Kind != coro.CallDirect || callPlan.Rep != coro.Dispatch ||
		callPlan.SyncDispatch || !callPlan.Open || callPlan.Unresolved != coro.UnknownManagedInterfaceDispatch ||
		common.StaticCallee() != nil || !common.IsInvoke() || common.Method == nil {
		return fail("requires an open UnknownManagedInterfaceDispatch CallPlan")
	}
	ownerPlan, ok := plan.FunctionPlan(owner)
	if !ok || ownerPlan.Emission != coro.EmitCoroutine || ownerPlan.Primary != coro.PrimaryCoroutine {
		return fail("owner plan present=%t emission=%s primary=%s demand=%s effect=%s exec=%s is not one coroutine primary",
			ok, ownerPlan.Emission, ownerPlan.Primary, ownerPlan.Demand, ownerPlan.Effect, ownerPlan.Exec)
	}
	if !callPlan.MayBeNil {
		return fail("open interface invoke lost its nil-interface check")
	}
	signature, err := coroInterfaceDispatchSourceSignature(common)
	if err != nil {
		return fail("signature: %v", err)
	}
	if err := validateCoroManagedDispatchSignatureShape(signature); err != nil {
		return fail("signature: %v", err)
	}
	if _, err := coroManagedInterfaceInvokeMethodKey(universe, owner, call); err != nil {
		return fail("signature: %v", err)
	}
	return nil
}

func (p *context) tryCompileCoroManagedInterfaceDispatch(
	b llssa.Builder, call *ssa.Call,
) (llssa.Expr, bool) {
	if p.compilation == nil || p.compilation.CoroPlan == nil ||
		!p.compilation.EnableCoroPlainDispatch || call == nil || call.Common() == nil ||
		!p.compilation.coroManagedInterface.acceptsCall(call) {
		return llssa.Nil, false
	}
	callPlan, found := p.compilation.CoroPlan.CallPlan(call)
	if !found || callPlan.Rep != coro.Dispatch {
		panic("managed interface descriptor call lost its frozen Dispatch CallPlan")
	}
	common := call.Common()
	if callPlan.Open {
		if err := validateCoroManagedInterfaceDispatchCall(
			p.compilation.CoroPlan, p.compilation.EmissionUniverse, p.goFn, call, callPlan,
		); err != nil {
			panic(err)
		}
	}
	signature, err := coroInterfaceDispatchSourceSignature(common)
	if err != nil {
		panic(err)
	}
	p.recordCallerLocationForCall(b, &call.Call)
	p.emitPCLineLabel(b, call.Pos())
	// Evaluate the interface receiver before arguments, exactly as the ordinary
	// LLGo invoke path does. Imethod preserves the nil-interface panic and pairs
	// the descriptor Ifn_ word with IfacePtrData as its receiver environment.
	intf := p.compileValue(b, common.Value)
	method := b.Imethod(intf, common.Method)
	args := p.compileValues(b, call.Call.Args, fnNormal)
	if p.coroBody() != nil {
		keepaliveSlots := p.compileCoroCallKeepaliveSlots(b, call)
		return p.compileCoroManagedDispatchAwaitValue(b, method, args, signature, keepaliveSlots), true
	}
	if callPlan.Open || coroDispatchCallHasCoroutineTarget(p.compilation.CoroPlan, callPlan) {
		panic("managed interface descriptor requires a coroutine owner for an open or coroutine-capable target")
	}
	abi, err := newCoroPlainDispatchABI(p, signature)
	if err != nil {
		panic(fmt.Errorf("managed interface plain dispatch: %w", err))
	}
	return b.CallCoroDispatchPlain(method, args, llssa.CoroDispatchCallOptions{
		Version: coroPlainDispatchVersion,
		ABIHash: abi.hash,
		Result:  p.prog.Type(abi.resultSlotType, llssa.InC),
	}), true
}

func (p *context) resolveInterfaceMethodSSA(method *types.Func, signature *types.Signature) *ssa.Function {
	if method == nil || signature == nil || signature.Recv() == nil {
		panic("coroutine interface method resolution requires a method and receiver signature")
	}
	selection := p.goProg.MethodSets.MethodSet(signature.Recv().Type()).Lookup(method.Pkg(), method.Name())
	if selection == nil {
		panic(fmt.Errorf("coroutine interface method resolution: method %q is absent from receiver %s", method.Name(), signature.Recv().Type()))
	}
	fn := p.methodValue(selection)
	if fn == nil {
		panic(fmt.Errorf("coroutine interface method resolution: method %q has no SSA implementation", method.Name()))
	}
	return fn
}

// resolveInterfaceMethodDescriptor is installed only for active coroutine
// compilation. It replaces an Ifn_ word iff preflight froze that exact method
// family as universal descriptor transport. Returning false preserves the
// legacy callable method word for every unrelated raw/foreign family.
func (p *context) resolveInterfaceMethodDescriptor(
	_ string, method *types.Func, signature *types.Signature,
) (llssa.Expr, bool) {
	if p.compilation == nil || p.compilation.coroManagedInterface == nil ||
		!p.compilation.EnableCoroPlainDispatch || signature == nil {
		return llssa.Nil, false
	}
	patched, ok := p.patchType(signature).(*types.Signature)
	if !ok || !p.compilation.coroManagedInterface.acceptsMethod(method, patched) {
		return llssa.Nil, false
	}
	target := p.resolveInterfaceMethodSSA(method, signature)
	descriptor, err := p.emitCoroManagedInterfaceMethodDescriptor(target, patched)
	if err != nil {
		panic(err)
	}
	return descriptor, true
}

// resolveManagedInterfaceRawMethodSymbol preserves the independent raw-method
// address domain while a method family's Ifn_ uses universal descriptor
// transport. A real RawPlainEntry selects its separately planned legacy body.
// Without that capability, Tfn_ receives a signature-correct trap stub rather
// than an invalid call to the coroutine primary.
func (p *context) resolveManagedInterfaceRawMethodSymbol(
	method *types.Func, signature *types.Signature,
) (string, bool) {
	if p.compilation == nil || p.compilation.coroManagedInterface == nil || signature == nil {
		return "", false
	}
	patched, ok := p.patchType(signature).(*types.Signature)
	if !ok || !p.compilation.coroManagedInterface.acceptsMethod(method, patched) {
		return "", false
	}
	target := p.resolveInterfaceMethodSSA(method, signature)
	entry := p.mustFunctionSymbol(target)
	if entry.plan.Emission != coro.EmitCoroutine {
		return entry.name, true
	}
	if entry.plan.RawPlainEntry {
		if err := validatePlannedRawPlainEntry(entry.function, entry.plan); err != nil {
			panic(err)
		}
		return p.mustRawPlainFunctionSymbol(target).name, true
	}
	key := sha256.Sum256([]byte(string(entry.plan.ID) + "\x00" + structuralEmissionABITypeKey(patched)))
	name := coroManagedInterfaceRawTrapPrefix + hex.EncodeToString(key[:16])
	stub := p.pkg.FuncOf(name)
	if stub == nil {
		stub = p.pkg.NewFunc(name, patched, llssa.InGo)
	}
	if !stub.HasBody() {
		body := stub.MakeBody(1)
		trap := p.pkg.NewFunc(
			"llvm.trap", types.NewSignatureType(nil, nil, nil, nil, nil, false), llssa.InC,
		)
		body.Call(trap.Expr)
		body.Unreachable()
		body.EndBuild()
		body.Dispose()
	}
	return name, true
}

func (p *context) emitCoroManagedInterfaceMethodDescriptor(
	target *ssa.Function, interfaceEntrySignature *types.Signature,
) (llssa.Expr, error) {
	if p == nil || p.compilation == nil || p.compilation.CoroPlan == nil || target == nil {
		return llssa.Nil, fmt.Errorf("managed interface descriptor requires an exact target and compilation plan")
	}
	entry := p.mustFunctionSymbol(target)
	logicalSignature := coroInterfaceDispatchCanonicalSignature(coroInterfaceDispatchCallableSignature(interfaceEntrySignature))
	if err := validateCoroManagedInterfaceDescriptorTarget(
		entry.function, entry.plan, p.compilation.EmissionUniverse, logicalSignature,
	); err != nil {
		return llssa.Nil, err
	}
	abi, err := newCoroPlainDispatchABI(p, logicalSignature)
	if err != nil {
		return llssa.Nil, fmt.Errorf("managed interface descriptor target %q: %w", entry.plan.ID, err)
	}
	physical, py, kind := p.compileFunction(entry.function)
	if kind != goFunc || physical == nil || py != nil {
		return llssa.Nil, fmt.Errorf("managed interface descriptor target %q did not compile as one Go function", entry.plan.ID)
	}
	patchedTarget, ok := p.patchType(entry.function.Signature).(*types.Signature)
	if !ok || patchedTarget.Recv() == nil {
		return llssa.Nil, fmt.Errorf("managed interface descriptor target %q lost its receiver signature", entry.plan.ID)
	}
	receiver := patchedTarget.Recv().Type()
	targetHash := sha256.Sum256([]byte(entry.plan.ID))
	targetKey := "method." + hex.EncodeToString(targetHash[:8]) + "." + hex.EncodeToString(abi.hash[:])
	descriptorName := coroPlainDispatchDescriptorPrefix + targetKey
	if descriptor, found := p.coroPlainDescriptors[descriptorName]; found {
		return descriptor, nil
	}
	flags := uint32(0)
	var plainEntry, coroEntry llssa.Expr
	switch entry.plan.Emission {
	case coro.EmitPlain:
		flags |= llssa.CoroDispatchFlagHasPlain
		plainEntry = p.newCoroDynamicDispatchEntryThunk(
			coroPlainDispatchThunkPrefix+targetKey, physical.Expr, abi, entry.plan.Emission, receiver,
		)
	case coro.EmitCoroutine:
		flags |= llssa.CoroDispatchFlagHasCoro
		coroEntry = p.newCoroDynamicDispatchEntryThunk(
			coroCoroDispatchThunkPrefix+targetKey, physical.Expr, abi, entry.plan.Emission, receiver,
		)
	default:
		return llssa.Nil, fmt.Errorf("managed interface descriptor target %q has unsupported emission %s", entry.plan.ID, entry.plan.Emission)
	}
	// The descriptor environment is the dynamic receiver supplied by
	// IfacePtrData, so NoCapture must remain clear even for a top-level method.
	descriptor := p.pkg.NewCoroDispatchDescriptor(descriptorName, llssa.CoroDispatchDescriptorOptions{
		Version:    coroPlainDispatchVersion,
		Flags:      flags,
		ABIHash:    abi.hash,
		Signature:  abi.signature,
		PlainEntry: plainEntry,
		CoroEntry:  coroEntry,
		Result:     p.prog.Type(abi.resultSlotType, llssa.InC),
	})
	if p.coroPlainDescriptors == nil {
		p.coroPlainDescriptors = make(map[string]llssa.Expr)
	}
	p.coroPlainDescriptors[descriptorName] = descriptor
	return descriptor, nil
}

func validateCoroManagedInterfaceDescriptorTarget(
	target *ssa.Function,
	functionPlan coro.FunctionPlan,
	universe *EmissionUniverse,
	logicalSignature *types.Signature,
) error {
	fail := func(format string, args ...any) error {
		name := "<nil>"
		if target != nil {
			name = target.String()
		}
		return fmt.Errorf("managed interface descriptor target %q (%s): %s", name, functionPlan.ID, fmt.Sprintf(format, args...))
	}
	if target == nil || target.Signature == nil || target.Signature.Recv() == nil || len(target.Blocks) == 0 || len(target.FreeVars) != 0 {
		return fail("requires one defined non-capturing receiver body")
	}
	if functionPlan.External != coro.Defined || functionPlan.FuncRep != coro.Dispatch || functionPlan.Demand == coro.NoDemand {
		return fail("requires a demanded defined Dispatch body, got external=%s representation=%s demand=%s",
			functionPlan.External, functionPlan.FuncRep, functionPlan.Demand)
	}
	if functionPlan.Effect.IsOpaque() || functionPlan.Exec.IsOpaque() ||
		functionPlan.Exec.Contains(coro.BlockForeign|coro.ThreadAffine) {
		return fail("opaque/foreign/thread-affine policy cannot publish a managed capability, got effect=%s exec=%s",
			functionPlan.Effect, functionPlan.Exec)
	}
	switch functionPlan.Emission {
	case coro.EmitPlain:
		if functionPlan.Primary != coro.PrimaryPlain || functionPlan.Effect != coro.NoSuspend ||
			functionPlan.Exec.Contains(coro.NeedsPreempt) {
			return fail("plain capability is not exact bounded no-suspend, got primary=%s effect=%s exec=%s",
				functionPlan.Primary, functionPlan.Effect, functionPlan.Exec)
		}
	case coro.EmitCoroutine:
		// BothDemand/RawPlainEntry still publishes only the managed coroutine
		// primary. The raw alternate remains reachable solely through its exact
		// legacy address consumers.
		if functionPlan.Primary != coro.PrimaryCoroutine || !functionPlan.Demand.Contains(coro.AsyncDemand) ||
			!functionPlan.Effect.MaySuspend() {
			return fail("coroutine capability has primary=%s demand=%s effect=%s",
				functionPlan.Primary, functionPlan.Demand, functionPlan.Effect)
		}
	default:
		return fail("unsupported emission %s", functionPlan.Emission)
	}
	if target.Signature.Variadic() || typeParamCount(target.Signature.TypeParams()) != 0 ||
		typeParamCount(target.Signature.RecvTypeParams()) != 0 || len(target.TypeArgs()) != 0 || target.Origin() != nil {
		return fail("variadic or generic method ABI is not implemented")
	}
	directive, err := coroRawABIDirective(target, universe)
	if err != nil {
		return fail("classify ABI directive: %v", err)
	}
	if directive != "" {
		return fail("ABI directive %q requires an explicit boundary adapter", directive)
	}
	if logicalSignature == nil || logicalSignature.Recv() != nil {
		return fail("missing receiver-free logical signature")
	}
	if universe == nil {
		return fail("requires a prepared emission universe")
	}
	effective, err := universe.coroPhysicalSourceSignature(target)
	if err != nil {
		return fail("derive effective target signature: %v", err)
	}
	if effective == nil || effective.Params().Len() == 0 {
		return fail("effective target signature has no receiver parameter")
	}
	params := make([]*types.Var, effective.Params().Len()-1)
	for i := range params {
		params[i] = effective.Params().At(i + 1)
	}
	targetLogical := coroInterfaceDispatchCanonicalSignature(types.NewSignatureType(
		nil, nil, nil, types.NewTuple(params...), effective.Results(), effective.Variadic(),
	))
	if !coroInterfaceDispatchSignaturesIdentical(logicalSignature, targetLogical) {
		return fail("logical signature %s does not match effective target signature %s", logicalSignature, targetLogical)
	}
	return nil
}
