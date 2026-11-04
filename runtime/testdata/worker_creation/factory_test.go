package workercreationprobe

import (
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/xgo-dev/llgo/runtime/internal/coro"
	"github.com/xgo-dev/llgo/runtime/workercreationprobe/worker"
)

func coroRuntimeAbort(message string) { panic(message) }

// These tests enter the factory after delivery admission. Scheduler delivery
// itself is covered by the program/fleet and LLGo native acceptance tests.
func coroNativeWorkerDeliveryReadyV1(_ coroNativeWorkerDeliveryV1, _ coro.ExecutorHandle, _ coro.RouteID) bool {
	return true
}

func waitFor(t *testing.T, wanted uint32, value func() uint32) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for value() != wanted {
		if time.Now().After(deadline) {
			t.Fatalf("native count=%d, want %d", value(), wanted)
		}
		runtime.Gosched()
	}
}

func usage(t *testing.T, wanted uint32) {
	t.Helper()
	_, live, ok := coroNativePhysicalThreadCapacityV1State.Usage()
	if !ok || live != wanted {
		t.Fatalf("physical thread ledger=%d (%v), want %d", live, ok, wanted)
	}
}

func start(t *testing.T) {
	t.Helper()
	if !coroTargetStartPhysicalThreadCapacityV1() ||
		!coroNativeWorkerPoolStartDeliveryV1(coroNativeWorkerDeliveryProgramV1, coro.ExecutorHandle{}, 0) {
		t.Fatal("start")
	}
	usage(t, 2)
}

func submitBlockedAndCanceled(t *testing.T) {
	t.Helper()
	for generation := uint32(1); generation <= 4; generation++ {
		if !worker.Submit(generation) {
			t.Fatal("submit", generation)
		}
	}
	// Keep a real reservation live, prove stop cannot seal it, then publish a
	// tombstone behind the four blocked/queued jobs for the join drain to retire.
	token := worker.QueueReserve()
	if token == 0 || worker.QueueStop(1) || !worker.QueueCancel(token) {
		t.Fatal("reservation admission")
	}
	waitFor(t, 1, worker.Entered)
	if worker.QueueDemand() != 5 {
		t.Fatal("lost reserved/canceled/blocked/queued demand", worker.QueueDemand())
	}
}

func TestInitialFailure(t *testing.T) {
	if !coroTargetStartPhysicalThreadCapacityV1() {
		t.Fatal("start ledger")
	}
	worker.FailAt(1)
	if coroNativeWorkerPoolStartDeliveryV1(coroNativeWorkerDeliveryProgramV1, coro.ExecutorHandle{}, 0) ||
		!coroNativeWorkerPoolCanReleaseV1() || worker.Attempts() != 1 || worker.Successes() != 0 || worker.Joins() != 0 {
		t.Fatal("initial failure did not reset the empty pool")
	}
	usage(t, 1)
}

func partialFailure(t *testing.T, failure uint32) {
	start(t)
	submitBlockedAndCanceled(t)
	worker.FailAt(failure)
	var fatal any
	func() {
		defer func() { fatal = recover() }()
		coroTargetEnsureWorkerCapacityV1()
	}()
	if fatal != "native coroutine worker demand growth failed" {
		t.Fatalf("fatal=%v", fatal)
	}
	created := failure - 1
	waitFor(t, created, worker.Entered)
	state := &coroNativeWorkerPoolV1State
	if worker.Attempts() != failure || worker.Successes() != created || worker.Joins() != 0 ||
		state.joined != 0 || state.stopping || state.threads[created] != nil {
		t.Fatal("failed create published an ID or joined blocked work")
	}
	for index := uint32(0); index < created; index++ {
		if state.threads[index] == nil {
			t.Fatal("lost live native thread identity", index)
		}
	}
	usage(t, 1+created)
	if _, _, status := state.growth.TryAcquire(); status != coro.WorkerGrowthClosed {
		t.Fatal("failed factory accepted another creator")
	}
	if _, status := state.growth.TrySeal(); status != coro.WorkerGrowthClosed || worker.QueueDestroyAfterJoin() || worker.QueueCanRelease() {
		t.Fatal("failed live pool was treated as releasable")
	}
	// Production abort exits the process. The panic surrogate only lets this
	// control inspect the failed state; the following explicit drain is fixture
	// cleanup, not a recovery path or a promise to recover from creation failure.
	if !worker.QueueStop(created) {
		t.Fatal("fixture stop")
	}
	worker.ReleaseCalls()
	if !coroNativeWorkerPoolJoinCreatedV1(state, created) || !worker.QueueDestroyAfterJoin() || !worker.QueueCanRelease() ||
		worker.Completed() != 4 || worker.BadCompletions() != 0 || worker.Joins() != created || worker.QueueDemand() != 0 {
		t.Fatal("fixture drain did not join exactly the successful prefix")
	}
	usage(t, 1)
	fmt.Println("partial failure: rolled back failed reservation, preserved live IDs, fatal before join, drained 4 jobs and tombstone")
}

func TestPartialFailure2(t *testing.T) { partialFailure(t, 2) }
func TestPartialFailure3(t *testing.T) { partialFailure(t, 3) }
func TestPartialFailure4(t *testing.T) { partialFailure(t, 4) }

func stop(t *testing.T) {
	t.Helper()
	state := &coroNativeWorkerPoolV1State
	count, status := state.growth.TrySeal()
	if status != coro.WorkerGrowthAcquired || count != 4 {
		t.Fatal("seal", count, status)
	}
	worker.ReleaseCalls()
	if !coroNativeWorkerPoolStopCreatedV1(state, count) || !coroNativeWorkerPoolCanReleaseV1() ||
		worker.Completed() != 4 || worker.BadCompletions() != 0 || worker.Joins() != 4 || worker.QueueDemand() != 0 {
		t.Fatal("strong join and reset")
	}
	usage(t, 1)
}

func TestSuccessfulGrowthAndJoin(t *testing.T) {
	start(t)
	submitBlockedAndCanceled(t)
	coroTargetEnsureWorkerCapacityV1()
	waitFor(t, 4, worker.Entered)
	if worker.Successes() != 4 {
		t.Fatal("growth did not satisfy demand")
	}
	usage(t, 5)
	stop(t)
}

func TestSealWaitsForAdmittedNativeCreate(t *testing.T) {
	start(t)
	submitBlockedAndCanceled(t)
	worker.HoldAt(2)
	done := make(chan struct{})
	go func() { coroTargetEnsureWorkerCapacityV1(); close(done) }()
	waitFor(t, 2, worker.Attempts)
	if _, status := coroNativeWorkerPoolV1State.growth.TrySeal(); status != coro.WorkerGrowthBusy {
		t.Fatal("seal admitted a pending native creation")
	}
	worker.ReleaseCreate()
	<-done
	waitFor(t, 4, worker.Entered)
	usage(t, 5)
	stop(t)
}
