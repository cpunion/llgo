package dcepass

import "github.com/xgo-dev/llvm"

// CaptureMethodTypes copies only type initializers and their constant local
// dependencies. Function bodies and source-context values never escape. A
// skipped name is a tombstone: a later package must not override a definition
// whose first linked copy could not be captured safely.
func CaptureMethodTypes(dst, src llvm.Module) (skipped []string) {
	e := newOverrideEmitter(dst)
	for g := src.FirstGlobal(); !g.IsNil(); g = llvm.NextGlobal(g) {
		if !g.IsGlobalConstant() {
			continue
		}
		methods, elem, ok := methodArray(g.Initializer())
		if !ok {
			continue
		}
		if !capturableConstant(g.Initializer(), make(map[llvm.Value]bool)) {
			skipped = append(skipped, g.Name())
			continue
		}
		keep := make(map[int]bool)
		for i := 0; i < methods.OperandsCount(); i++ {
			keep[i] = true
		}
		e.emitTypeOverride(g, methods, elem, keep, false)
	}
	return skipped
}

func capturableConstant(v llvm.Value, seen map[llvm.Value]bool) bool {
	if seen[v] {
		return true
	}
	seen[v] = true
	if fn := v.IsAFunction(); !fn.IsNil() {
		// A declaration in the entry object cannot resolve a package-local body.
		return fn.Name() != "" && !isLocalLinkage(fn.Linkage())
	}
	if g := v.IsAGlobalVariable(); !g.IsNil() {
		if g.Name() != "" && !isLocalLinkage(g.Linkage()) {
			return true
		}
		return g.IsGlobalConstant() && !g.Initializer().IsNil() && capturableConstant(g.Initializer(), seen)
	}
	if !v.IsAGlobalValue().IsNil() {
		return false
	}
	if v.IsNull() || !v.IsAConstantAggregateZero().IsNil() || !v.IsAUndefValue().IsNil() ||
		!v.IsAConstantInt().IsNil() || !v.IsAConstantFP().IsNil() || v.IsConstantString() {
		return true
	}
	if !v.IsAConstantExpr().IsNil() {
		switch v.Opcode() {
		case llvm.GetElementPtr, llvm.BitCast, llvm.IntToPtr, llvm.PtrToInt, llvm.Trunc, llvm.Add, llvm.Sub, llvm.Xor:
		default:
			return false
		}
	} else if v.IsAConstantStruct().IsNil() && v.IsAConstantArray().IsNil() && v.IsAConstantVector().IsNil() {
		return false
	}
	for i := 0; i < v.OperandsCount(); i++ {
		if !capturableConstant(v.Operand(i), seen) {
			return false
		}
	}
	return true
}

// EmitMethodTypeFragment consumes one independently owned fragment. Only names
// survive between calls, so disposing a source context cannot leave LLVM value
// keys which accidentally match allocations from a later context.
func EmitMethodTypeFragment(dst, src llvm.Module, liveSlots map[string][]int, seen map[string]bool, verbose bool) {
	e := newOverrideEmitter(dst)
	for g := src.FirstGlobal(); !g.IsNil(); g = llvm.NextGlobal(g) {
		if seen[g.Name()] || !g.IsGlobalConstant() {
			continue
		}
		methods, elem, ok := methodArray(g.Initializer())
		if ok {
			e.emitTypeOverride(g, methods, elem, liveSlotSet(liveSlots[g.Name()]), verbose)
			seen[g.Name()] = true
		}
	}
}
