//go:build !coro_runtime_adapter_test && !coro_native_fleet_test

package runtime

import (
	"unsafe"

	"github.com/xgo-dev/llgo/runtime/internal/coro"
)

func coroInheritExecutionDomainTask(task, parent *coro.G) bool {
	entry := coro.InheritableExecutionDomainTask(parent)
	if entry == nil {
		return true
	}
	child := (*coro.ExecutionDomainTask)(AllocZ(unsafe.Sizeof(coro.ExecutionDomainTask{})))
	if child == nil {
		return false
	}
	child.Context, child.Activity = entry.Context, entry.Activity
	return coro.AttachExecutionDomainTask(task, child, false)
}

// GoOutsideExecutionDomain publishes one ordinary, unbubbled child. Only
// inheritance is suppressed during spawn; the parent retains its identity and
// activity unit. In particular, the child may start a fresh synctest.Run.
func GoOutsideExecutionDomain(f func()) {
	if !coro.ExecutionDomainEnabled() || CurrentExecutionDomainContext() == nil {
		go f()
		return
	}
	gp := getgIfPresent()
	task := (*coro.G)(gp.startarg)
	if !coro.SuppressExecutionDomainInheritance(task) {
		panic("cannot suppress execution-domain inheritance")
	}
	defer func() {
		if !coro.RestoreExecutionDomainInheritance(task) {
			panic("cannot restore execution-domain inheritance")
		}
	}()
	go f()
}

// CurrentExecutionDomainContext does not allocate. Ordinary sync primitives may
// ask this question without activating the whole-program domain capability.
func CurrentExecutionDomainContext() unsafe.Pointer {
	if !coro.ExecutionDomainEnabled() {
		return nil
	}
	gp := getgIfPresent()
	if gp == nil || gp.startfn != nil || gp.startarg == nil {
		return nil
	}
	entry := coro.LookupExecutionDomainTask((*coro.G)(gp.startarg))
	if entry == nil {
		return nil
	}
	return entry.Context
}

// EnterExecutionDomain makes the current logical task the controller. A child
// spawned by it inherits the context and is counted before becoming runnable.
func EnterExecutionDomain(context unsafe.Pointer, activity *coro.ExecutionDomainActivity) bool {
	if !coro.ExecutionDomainEnabled() || context == nil || activity == nil {
		return false
	}
	gp := getgIfPresent()
	if gp == nil || gp.startfn != nil || gp.startarg == nil {
		return false
	}
	entry := new(coro.ExecutionDomainTask)
	entry.Context, entry.Activity = context, activity
	return coro.AttachExecutionDomainTask((*coro.G)(gp.startarg), entry, true)
}

func LeaveExecutionDomain() bool {
	gp := getgIfPresent()
	if gp == nil || gp.startfn != nil || gp.startarg == nil {
		return false
	}
	post, ok := coro.DetachExecutionDomainTask((*coro.G)(gp.startarg))
	return ok && !post.Valid()
}
