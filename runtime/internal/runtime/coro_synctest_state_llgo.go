//go:build llgo && llgo_coro && !coro_runtime_adapter_test && ((llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux) && !baremetal) || wasm || tinygo.wasm || baremetal || llgo_coro_host)

package runtime

import (
	"unsafe"

	"github.com/xgo-dev/llgo/runtime/internal/coro"
	catomic "github.com/xgo-dev/llgo/runtime/internal/sync/atomic"
)

// Domain identity and parked-event cleanup are shared by ordinary channel and
// scheduler adapters. They do not depend on the managed bubble controller.
type coroSynctestBubble struct {
	activity coro.ExecutionDomainActivity
	lock     uint32
	mainDone uint32
	waiter   *coroDomainEventV1
	sleepers *coroDomainEventV1
	timers   *SynctestTimer
	now      int64
}

type coroDomainParkV1 struct {
	state      CoroKeyedParkV2
	bubble     *coroSynctestBubble
	operation  coro.OperationID
	controller bool
	event      *coroDomainEventV1
}

// The controller owns only an independently allocated scalar event identity,
// never a pointer into a cancellable coroutine frame or retired G allocation.
type coroDomainEventV1 struct {
	operation coro.OperationID
	when      int64
	next      *coroDomainEventV1
	canceled  bool
}

func (b *coroSynctestBubble) acquireLock() {
	for {
		if _, ok := catomic.CompareAndExchange(&b.lock, 0, 1); ok {
			return
		}
	}
}

func (b *coroSynctestBubble) releaseLock() { catomic.Store(&b.lock, 0) }

func currentSynctestBubble() *coroSynctestBubble {
	return (*coroSynctestBubble)(CurrentExecutionDomainContext())
}

func coroCancelDomainParkV1(storage unsafe.Pointer) bool {
	state := (*CoroKeyedParkV2)(storage)
	p := (*coroDomainParkV1)(storage)
	if p == nil || state.key != uintptr(storage) || p.bubble == nil || p.operation != state.operation {
		return false
	}
	b := p.bubble
	if p.controller {
		return b.activity.DisarmWake(p.operation)
	}
	event := p.event
	if event == nil {
		return false
	}
	b.acquireLock()
	event.canceled = true
	if b.waiter == event {
		b.waiter = nil
	}
	link := &b.sleepers
	for *link != nil && *link != event {
		link = &(*link).next
	}
	if *link == event {
		*link = event.next
		event.next = nil
	}
	b.releaseLock()
	return true
}
