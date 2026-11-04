package worker

/*
#cgo CFLAGS: -std=c11 -Wall -Wextra -Werror -Wno-unused-parameter
#cgo LDFLAGS: -pthread
#include "worker.h"
int probe_create(uintptr_t *thread);
void probe_fail_at(uint32_t attempt);
void probe_hold_at(uint32_t attempt);
void probe_release_create(void);
void probe_release_calls(void);
uint32_t probe_attempts(void);
uint32_t probe_successes(void);
uint32_t probe_joins(void);
uint32_t probe_entered(void);
uint32_t probe_completed(void);
uint32_t probe_bad_completions(void);
bool probe_submit(uint32_t generation);
*/
import "C"

import (
	"unsafe"

	"github.com/xgo-dev/llgo/runtime/workercreationprobe/thread"
)

const QueueCapacity = 1024

func Create(native *thread.Thread) int {
	var raw C.uintptr_t
	result := C.probe_create(&raw)
	*native = unsafe.Pointer(uintptr(raw))
	return int(result)
}

func QueueInit() bool             { return bool(C.__llgo_coro_worker_queue_init_v1()) }
func QueueCanRelease() bool       { return bool(C.__llgo_coro_worker_queue_can_release_v1()) }
func QueueDemand() uint32         { return uint32(C.__llgo_coro_worker_queue_demand_v1()) }
func QueueStop(count uint32) bool { return bool(C.__llgo_coro_worker_queue_stop_v1(C.uint32_t(count))) }
func QueueDestroyAfterJoin() bool { return bool(C.__llgo_coro_worker_queue_destroy_after_join_v1()) }
func QueueReserve() uintptr       { return uintptr(C.__llgo_coro_worker_queue_reserve_v2()) }
func QueueCancel(token uintptr) bool {
	return bool(C.__llgo_coro_worker_queue_cancel_reservation_v2(C.size_t(token)))
}
func Submit(generation uint32) bool { return bool(C.probe_submit(C.uint32_t(generation))) }
func FailAt(attempt uint32)         { C.probe_fail_at(C.uint32_t(attempt)) }
func HoldAt(attempt uint32)         { C.probe_hold_at(C.uint32_t(attempt)) }
func ReleaseCreate()                { C.probe_release_create() }
func ReleaseCalls()                 { C.probe_release_calls() }
func Attempts() uint32              { return uint32(C.probe_attempts()) }
func Successes() uint32             { return uint32(C.probe_successes()) }
func Joins() uint32                 { return uint32(C.probe_joins()) }
func Entered() uint32               { return uint32(C.probe_entered()) }
func Completed() uint32             { return uint32(C.probe_completed()) }
func BadCompletions() uint32        { return uint32(C.probe_bad_completions()) }
