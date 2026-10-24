package cl

import (
	"fmt"

	"github.com/xgo-dev/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

// A static MakeClosure call selects its body independently of the producer's
// representation. Escaping uses can require {descriptor, environment} even
// though this exact call still has a closed direct-plain target. Only the
// environment word belongs to the existing value's invocation capability.
func (p *context) coroStaticDescriptorClosureTarget(source ssa.CallInstruction) (*ssa.Function, bool) {
	if source == nil || p.compilation == nil || p.immutablePlan() == nil {
		return nil, false
	}
	call, ordinary := source.(*ssa.Call)
	if !ordinary || call.Common() == nil || call.Common().IsInvoke() {
		return nil, false
	}
	closure, exact := call.Common().Value.(*ssa.MakeClosure)
	if !exact {
		return nil, false
	}
	plan := p.immutablePlan()
	value, found := plan.ValuePlan(closure)
	if !found || len(value.Funcs) != 1 || len(value.Funcs[0].Path) != 0 || value.Funcs[0].Rep != coro.Dispatch {
		return nil, false
	}
	fail := func() {
		panic(fmt.Errorf("static descriptor closure call %q has no exact direct-plain body capability", call.String()))
	}
	cp, found := plan.CallPlan(call)
	if !found || cp.Kind != coro.CallDirect || cp.Rep != coro.DirectPlain || cp.Transport != coro.ManagedTransport ||
		cp.Open || cp.MayBeNil || cp.RawPlain || len(cp.Targets) != 1 || p.emissionUniverse == nil {
		fail()
	}
	target, exact := closure.Fn.(*ssa.Function)
	if !exact || target == nil || call.Common().StaticCallee() != target {
		fail()
	}
	target, found = p.emissionUniverse.Resolve(target)
	if !found || target == nil {
		fail()
	}
	tp, found := plan.FunctionPlan(target)
	leaf := value.Funcs[0]
	if !found || tp.Emission != coro.EmitPlain || tp.Effect != coro.NoSuspend || tp.Exec.Contains(coro.MayUnwind) ||
		cp.Targets[0] != tp.ID || leaf.Transport != coro.ManagedTransport || leaf.MayBeNil ||
		len(leaf.Targets) != 1 || leaf.Targets[0] != tp.ID {
		fail()
	}
	return target, true
}
