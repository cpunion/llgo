//go:build !llgo

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

package runtime

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

const (
	runtimePollGoSource                      = "internal/lib/runtime/poll_linkname_llgo.go"
	runtimeCoroPollGoSource                  = "internal/lib/runtime/poll_linkname_coro_llgo.go"
	runtimePollCSource                       = "internal/lib/runtime/_wrap/poll.c"
	runtimeCoroChannelSource                 = "internal/coro/channel_operation_source.go"
	runtimeCoroOperationDirectorySource      = "internal/coro/operation_page_directory.go"
	runtimeCoroWorkerSource                  = "internal/coro/worker_operation_source.go"
	runtimeCoroNativeWorkerSource            = "internal/runtime/coro_worker_native_llgo.go"
	runtimeCoroWorkerProgramCompletionSource = "internal/runtime/coro_worker_completion_program_llgo.go"
	runtimeCoroWorkerFleetCompletionSource   = "internal/runtime/coro_worker_completion_fleet_llgo.go"
	runtimeCoroWorkerOwnerSource             = "internal/runtime/coro_worker_owner_llgo.go"
	runtimeCoroNativeDriverSource            = "internal/runtime/coro_executor_driver_timer_llgo.go"
	runtimeCoroNativeFleetSource             = "internal/runtime/coro_native_fleet.go"
	runtimeCoroNativeFleetTargetSource       = "internal/runtime/coro_target_native_fleet_llgo.go"
	runtimeCoroWorkerCallSource              = "internal/coroworker/call_llgo.go"
	runtimeCoroWorkerCSource                 = "internal/coroworker/_worker/worker.c"
	runtimeCoroWorkerHeaderSource            = "internal/coroworker/_worker/worker.h"
	runtimeCoroOSThreadForeignSource         = "internal/runtime/coro_os_thread_foreign_llgo.go"
	runtimePthreadSyncSource                 = "internal/clite/pthread/sync/sync.go"
	runtimePthreadGCSource                   = "internal/clite/pthread/pthread_gc.go"
	runtimePthreadNoGCSource                 = "internal/clite/pthread/pthread_nogc.go"
)

func requireRuntimeAnnotationFreeCDeclarations(t *testing.T, path string, names ...string) {
	t.Helper()
	source := readRuntimePollFile(t, path)
	file, err := parser.ParseFile(token.NewFileSet(), path, source, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	found := make(map[string]bool, len(names))
	for _, name := range names {
		found[name] = false
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if _, wanted := found[function.Name.Name]; !wanted {
			continue
		}
		found[function.Name.Name] = true
		if function.Body != nil || function.Doc == nil {
			t.Errorf("%s declaration %s is not an external declaration", path, function.Name.Name)
			continue
		}
		link := false
		for _, comment := range function.Doc.List {
			if strings.HasPrefix(comment.Text, "//llgo:coro") {
				t.Errorf("%s declaration %s acquired explicit coroutine policy %q", path, function.Name.Name, comment.Text)
			}
			if strings.HasPrefix(comment.Text, "//go:linkname "+function.Name.Name+" C.") {
				link = true
			}
		}
		if !link {
			t.Errorf("%s declaration %s lacks an exact C link", path, function.Name.Name)
		}
	}
	for name, present := range found {
		if !present {
			t.Errorf("%s lacks annotation-free C declaration %s", path, name)
		}
	}
}

func TestRuntimeTerminalStdioUsesExactPrivateSyncBoundary(t *testing.T) {
	const abortPath = "internal/runtime/coro_abort_libc.go"
	const workerPath = "internal/runtime/coro_worker_owner_llgo.go"
	const clitePath = "internal/clite/c.go"

	requireRuntimeAnnotationFreeCDeclarations(t, clitePath, "Fputs", "Fputc")

	abort := readRuntimePollFile(t, abortPath)
	for _, declaration := range []string{
		"//llgo:coro sync\n//go:linkname coroTerminalFputs C.fputs",
		"//llgo:coro sync\n//go:linkname coroTerminalFputc C.fputc",
	} {
		if !strings.Contains(abort, declaration) {
			t.Errorf("%s lacks private terminal boundary %q", abortPath, declaration)
		}
	}
	if !strings.Contains(abort, "c.Exit(2)") {
		t.Errorf("%s lost its terminal exit", abortPath)
	}
	paths, err := filepath.Glob("internal/runtime/*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if path == abortPath || path == workerPath || strings.HasSuffix(path, "_test.go") {
			continue
		}
		source := readRuntimePollFile(t, path)
		if strings.Contains(source, "coroTerminalFputs") ||
			strings.Contains(source, "coroTerminalFputc") {
			t.Errorf("%s reaches private terminal stdio outside the audited abort adapters", path)
		}
	}
}

func TestRuntimeCoroChannelCapacityUsesPagedLogicalSource(t *testing.T) {
	directory := readRuntimePollFile(t, runtimeCoroOperationDirectorySource)
	normalizedDirectory := strings.Join(strings.Fields(directory), " ")
	for _, required := range []string{
		"operationPageDirectoryBlockCapacity = uint32(64)",
		"type OperationPageDirectoryBlock struct",
		"blocks [operationPageDirectoryBlockCount]*OperationPageDirectoryBlock",
		"newBlock *OperationPageDirectoryBlock",
	} {
		if !strings.Contains(normalizedDirectory, required) {
			t.Errorf("%s lacks sparse directory marker %q", runtimeCoroOperationDirectorySource, required)
		}
	}
	if strings.Contains(normalizedDirectory, "pages [operationDynamicPageCapacity]unsafe.Pointer") {
		t.Errorf("%s restored a full per-source pointer catalog", runtimeCoroOperationDirectorySource)
	}

	core := readRuntimePollFile(t, runtimeCoroChannelSource)
	normalizedCore := strings.Join(strings.Fields(core), " ")
	for _, required := range []string{
		"const ChannelOperationPageCapacity = 64",
		"const ChannelOperationSourceCapacity = ChannelOperationPageCapacity",
		"extraPages []ChannelOperationPage",
		"dynamicPages operationDynamicPageDirectory",
		"readyPages operationReadyPageIndex",
		"func ConfigureChannelOperationPages(",
		"func AttachChannelOperationPage(",
		"func ChannelOperationConfiguredCapacity(",
		"func nextChannelOperationReady(",
		"ChannelOperationConfiguredCapacity(source)",
	} {
		if !strings.Contains(normalizedCore, required) {
			t.Errorf("%s lacks paged channel marker %q", runtimeCoroChannelSource, required)
		}
	}

	driver := readRuntimePollFile(t, runtimeCoroNativeDriverSource)
	for _, required := range []string{
		"coro.ChannelOperationConfiguredCapacity(&coroProgramChannelSourceV1State) != coro.ChannelOperationPageCapacity",
	} {
		if !strings.Contains(driver, required) {
			t.Errorf("%s lacks native channel capacity marker %q", runtimeCoroNativeDriverSource, required)
		}
	}
	fleet := readRuntimePollFile(t, runtimeCoroNativeFleetSource)
	for _, required := range []string{
		"coro.ChannelOperationConfiguredCapacity(&domain.channel) == coro.ChannelOperationPageCapacity",
	} {
		if !strings.Contains(fleet, required) {
			t.Errorf("%s lacks owned-P channel capacity marker %q", runtimeCoroNativeFleetSource, required)
		}
	}
	for path, source := range map[string]string{
		runtimeCoroNativeDriverSource: driver,
		runtimeCoroNativeFleetSource:  fleet,
	} {
		for _, forbidden := range []string{"coroProgramChannelExtraPagesV1State", "channelPages [", "ConfigureChannelOperationPages(&domain.channel"} {
			if strings.Contains(source, forbidden) {
				t.Errorf("%s eagerly reserves channel pages through %q", path, forbidden)
			}
		}
	}
}

func TestRuntimeCoroWorkerCapacityUsesPagedLogicalSourceAndBoundedNativePool(t *testing.T) {
	core := readRuntimePollFile(t, runtimeCoroWorkerSource)
	normalizedCore := strings.Join(strings.Fields(core), " ")
	for _, required := range []string{
		"const WorkerOperationPageCapacity = 64",
		"const WorkerOperationSourceCapacity = WorkerOperationPageCapacity",
		"extraPages []WorkerOperationPage",
		"dynamicPages operationDynamicPageDirectory",
		"func ConfigureWorkerOperationPages(",
		"func AttachWorkerOperationPage(",
		"func CanReserveWorkerOperation(",
		"func WorkerOperationConfiguredCapacity(",
		"WorkerOperationConfiguredCapacity(source)",
	} {
		if !strings.Contains(normalizedCore, required) {
			t.Errorf("%s lacks paged worker marker %q", runtimeCoroWorkerSource, required)
		}
	}

	native := readRuntimePollFile(t, runtimeCoroNativeWorkerSource)
	for _, required := range []string{
		"coroNativeWorkerThreadCountV1 = 4",
		"coroNativeWorkerPageCountV1 = 16",
		"coroNativeWorkerCapacityV1  = coroNativeWorkerPageCountV1 * coro.WorkerOperationPageCapacity",
		"coroNativeWorkerQueueSizeV1 = coroworker.QueueCapacity",
		"bounded C11 sequence ring",
		"coroworker.QueueReserve(&reservation)",
		"coroworker.QueueSubmitReserved(reservation, &job)",
		"coroNativeWorkerDeliveryFleetV1",
		"func coroNativeWorkerPoolStartFleetV1() bool",
	} {
		if !strings.Contains(native, required) {
			t.Errorf("%s lacks bounded native worker marker %q", runtimeCoroNativeWorkerSource, required)
		}
	}
	programCompletion := readRuntimePollFile(t, runtimeCoroWorkerProgramCompletionSource)
	for _, required := range []string{
		"!llgo_coro_native_timer",
		"//export __llgo_coro_native_worker_complete_v1",
		"state.delivery != coroNativeWorkerDeliveryProgramV1",
		"coroProgramWorkerSourceV1State.Post(id, payload)",
		"coroTargetRequestExecutorV1(state.handle)",
	} {
		if !strings.Contains(programCompletion, required) {
			t.Errorf("%s lacks static program worker completion marker %q", runtimeCoroWorkerProgramCompletionSource, required)
		}
	}
	fleetCompletion := readRuntimePollFile(t, runtimeCoroWorkerFleetCompletionSource)
	for _, required := range []string{
		"llgo_coro_native_timer",
		"//export __llgo_coro_native_worker_complete_v1",
		"state.delivery != coroNativeWorkerDeliveryFleetV1",
		"coroNativeFleetPostWorkerV1(id, payload)",
		"result.Route != coro.OperationRoutePosted || !accepted",
	} {
		if !strings.Contains(fleetCompletion, required) {
			t.Errorf("%s lacks static fleet worker completion marker %q", runtimeCoroWorkerFleetCompletionSource, required)
		}
	}
	for _, forbidden := range []string{"llgo_coro_native_fleet", "unsafe.Pointer", "reflect", "runtimeDarwinFuncPCABI0", "map[uintptr]"} {
		if strings.Contains(fleetCompletion, forbidden) {
			t.Errorf("%s retained reverse worker routing marker %q", runtimeCoroWorkerFleetCompletionSource, forbidden)
		}
	}
	reserveStart := strings.Index(native, "func coroNativeWorkerPoolReserveV1(")
	if reserveStart < 0 {
		t.Fatal("native worker source lacks reservation function")
	}
	reserveEnd := strings.Index(native[reserveStart:], "func coroNativeWorkerPoolCancelReservationV1(")
	if reserveEnd < 0 {
		t.Fatal("native worker source lacks exact reservation functions")
	}
	reserve := native[reserveStart : reserveStart+reserveEnd]
	for _, forbidden := range []string{"mutex", "Mutex", "TryLock"} {
		if strings.Contains(reserve, forbidden) {
			t.Fatalf("native worker reservation contains managed blocking edge %q:\n%s", forbidden, reserve)
		}
	}
	if !strings.Contains(reserve, "coroworker.QueueReserve(&reservation)") {
		t.Fatalf("native worker reservation bypasses the C11 capacity preflight:\n%s", reserve)
	}
	for _, forbidden := range []string{"psync", "state.mutex", "state.work", "pthread.Mutex", "pthread.Cond"} {
		if strings.Contains(native, forbidden) {
			t.Errorf("%s retains worker ingress pthread synchronization %q", runtimeCoroNativeWorkerSource, forbidden)
		}
	}
	for _, required := range []string{
		"func coroReserveNativeWorkerSubmissionV1(",
		"coroNativeWorkerSubmissionOwnerV1(handle, route)",
		"coro.CommitCurrentExecutorWorkerSubmission(driver, g, id)",
		"id.Route() != route",
	} {
		if !strings.Contains(native, required) {
			t.Errorf("%s lacks current-owner worker boundary %q", runtimeCoroNativeWorkerSource, required)
		}
	}

	owner := readRuntimePollFile(t, runtimeCoroWorkerOwnerSource)
	for _, required := range []string{
		"coro.CurrentExecutorWorkerDriver(task)",
		"coro.PrepareCurrentExecutorWorkerPark(",
		"coro.BindSingleWaitSetResumePacket(",
		"coro.TakeResumePacket(",
		"coroReserveNativeWorkerSubmissionV1(executor, route)",
		"packet    coro.ResumePacket",
	} {
		if !strings.Contains(owner, required) {
			t.Errorf("%s lacks current-owner worker marker %q", runtimeCoroWorkerOwnerSource, required)
		}
	}
	for _, forbidden := range []string{
		"&coroProgramWorkerSourceV1State",
		"coroProgramReserveNativeWorkerSubmissionV1",
		"coroProgramCancelNativeWorkerSubmissionV1",
		"coroProgramCommitNativeWorkerSubmissionV1",
		"coro.TakeRunDecision(",
		"coro.FinishCurrentExecutorWorkerPark(",
	} {
		if strings.Contains(owner, forbidden) {
			t.Errorf("%s retained singleton owner selection %q", runtimeCoroWorkerOwnerSource, forbidden)
		}
	}

	declaration := readRuntimePollFile(t, runtimeCoroWorkerCallSource)
	requireRuntimeAnnotationFreeCDeclarations(
		t,
		runtimeCoroWorkerCallSource,
		"QueueInit",
		"QueueCanRelease",
		"QueueReserve",
		"QueueCancelReservation",
		"QueueSubmitReserved",
		"QueueStop",
	)
	for _, required := range []string{
		"//go:linkname QueueInit C.__llgo_coro_worker_queue_init_v1",
		"//go:linkname QueueCanRelease C.__llgo_coro_worker_queue_can_release_v1",
		"//go:linkname QueueReserve C.__llgo_coro_worker_queue_reserve_v1",
		"//go:linkname QueueCancelReservation C.__llgo_coro_worker_queue_cancel_reservation_v1",
		"//go:linkname QueueSubmitReserved C.__llgo_coro_worker_queue_submit_reserved_v1",
		"//go:linkname QueueStop C.__llgo_coro_worker_queue_stop_v1",
		"lock-free by QueueInit",
		"semaphore_signal never wait for worker",
		"C adapter owns the fixed routine",
	} {
		if !strings.Contains(declaration, required) {
			t.Errorf("%s lacks lock-free worker transport contract %q", runtimeCoroWorkerCallSource, required)
		}
	}
	for _, name := range []string{"QueueReserve", "QueueCancelReservation", "QueueSubmitReserved"} {
		for _, capability := range []string{"sync", "worker"} {
			if strings.Contains(declaration, "//llgo:coro "+capability+"\n//go:linkname "+name+" ") {
				t.Errorf("managed worker ingress %s acquired incorrect %s capability", name, capability)
			}
		}
	}

	cSource := readRuntimePollFile(t, runtimeCoroWorkerCSource)
	for _, required := range []string{
		"_Atomic size_t sequence;",
		"_Atomic uint32_t producer_state;",
		"_Atomic size_t enqueue_position;",
		"atomic_is_lock_free(&queue->enqueue_position)",
		"atomic_store_explicit(&slot->sequence, reservation + 1, memory_order_release);",
		"llgo_coro_worker_job_canceled_v1",
		"sem_post(wake)",
		"semaphore_signal(*wake)",
		"__llgo_coro_worker_queue_wait_take_v1",
		"static void *llgo_coro_worker_main_v1(void *unused)",
		"__llgo_coro_native_worker_complete_v1(",
	} {
		if !strings.Contains(cSource, required) {
			t.Errorf("%s lacks C11 worker transport marker %q", runtimeCoroWorkerCSource, required)
		}
	}
	for _, forbidden := range []string{"pthread_mutex_lock", "pthread_cond_wait", "pthread_cond_signal"} {
		if strings.Contains(cSource, forbidden) {
			t.Errorf("%s retains pthread queue synchronization %q", runtimeCoroWorkerCSource, forbidden)
		}
	}
	header := readRuntimePollFile(t, runtimeCoroWorkerHeaderSource)
	for _, required := range []string{
		"LLGO_CORO_WORKER_THREAD_COUNT_V1 = 4",
		"LLGO_CORO_WORKER_QUEUE_CAPACITY_V1 = 1024",
		"struct llgo_coro_worker_job_v1",
		"uint32_t source_slot;",
		"uintptr_t args[LLGO_CORO_WORKER_MAX_ARGS_V1];",
	} {
		if !strings.Contains(header, required) {
			t.Errorf("%s lacks POD queue ABI marker %q", runtimeCoroWorkerHeaderSource, required)
		}
	}

	driver := readRuntimePollFile(t, runtimeCoroNativeDriverSource)
	for _, required := range []string{
		"coro.WorkerOperationConfiguredCapacity(&coroProgramWorkerSourceV1State) != coro.WorkerOperationPageCapacity",
		"coroNativeWorkerCapacityV1 != coroRuntimeWorkerCapacityV1",
		"coroNativeWorkerQueueSizeV1 != coroNativeWorkerCapacityV1",
	} {
		if !strings.Contains(driver, required) {
			t.Errorf("%s lacks native worker capacity marker %q", runtimeCoroNativeDriverSource, required)
		}
	}
	fleet := readRuntimePollFile(t, runtimeCoroNativeFleetSource)
	for _, required := range []string{
		"coro.WorkerOperationConfiguredCapacity(&domain.worker) == coro.WorkerOperationPageCapacity",
	} {
		if !strings.Contains(fleet, required) {
			t.Errorf("%s lacks owned-P worker capacity marker %q", runtimeCoroNativeFleetSource, required)
		}
	}
	if !strings.Contains(owner, "ensureCoroWorkerOperationCapacityV1(driver, task, coroRuntimeWorkerCapacityV1)") {
		t.Errorf("%s does not demand-grow the current worker catalog", runtimeCoroWorkerOwnerSource)
	}
	for path, source := range map[string]string{
		runtimeCoroNativeWorkerSource: native,
		runtimeCoroNativeDriverSource: driver,
		runtimeCoroNativeFleetSource:  fleet,
	} {
		for _, forbidden := range []string{"coroProgramWorkerExtraPagesV1State", "workerPages  [", "ConfigureWorkerOperationPages(&domain.worker"} {
			if strings.Contains(source, forbidden) {
				t.Errorf("%s eagerly reserves worker pages through %q", path, forbidden)
			}
		}
	}
}

func TestRuntimeCoroWorkerKeepsPthreadCreationCertificateOwnerScoped(t *testing.T) {
	native := readRuntimePollFile(t, runtimeCoroNativeWorkerSource)
	if !strings.Contains(native, "coroworker.Create(&state.threads[index])") {
		t.Fatalf("%s does not use the scheduler-owned worker creation leaf", runtimeCoroNativeWorkerSource)
	}
	if strings.Contains(native, "pthread.Create(") {
		t.Fatalf("%s bypasses the scheduler-owned worker creation leaf", runtimeCoroNativeWorkerSource)
	}

	declaration := readRuntimePollFile(t, runtimeCoroWorkerCallSource)
	requireRuntimeAnnotationFreeCDeclarations(t, runtimeCoroWorkerCallSource, "Create")
	for _, required := range []string{
		"//go:linkname Create C.__llgo_coro_worker_create_v1",
		"func Create(thread *pthread.Thread) c.Int",
		"No arbitrary Go callback address is accepted",
		"compiler-owned raw-host occurrence executes that conservative may-block",
		"ordinary managed occurrence would",
		"retain its foreign-wait policy",
	} {
		if !strings.Contains(declaration, required) {
			t.Errorf("%s lacks exact worker creation contract %q", runtimeCoroWorkerCallSource, required)
		}
	}
	wrapper := readRuntimePollFile(t, runtimeCoroWorkerCSource)
	for _, required := range []string{
		"int __llgo_coro_worker_create_v1(",
		"#if defined(LLGO_CORO_WORKER_BDWGC)",
		"return GC_pthread_create(thread, NULL, routine, NULL);",
		"return pthread_create(thread, NULL, routine, NULL);",
		"return llgo_coro_worker_thread_create_v1(thread, llgo_coro_worker_main_v1);",
	} {
		if !strings.Contains(wrapper, required) {
			t.Errorf("%s lacks exact worker creation implementation %q", runtimeCoroWorkerCSource, required)
		}
	}

	// The general API accepts arbitrary callbacks and therefore never inherits
	// the scheduler-owned declaration's exact certificate.
	for _, path := range []string{runtimePthreadGCSource, runtimePthreadNoGCSource} {
		text := readRuntimePollFile(t, path)
		if strings.Contains(text, "//llgo:coro noblock\n//go:linkname Create ") {
			t.Fatalf("general pthread.Create in %s acquired a coroutine noblock certificate", path)
		}
		if !strings.Contains(text, "//go:linkname Join ") ||
			!strings.Contains(text, "direct execution is inferred from the raw-host") {
			t.Fatalf("blocking pthread.Join in %s lacks its inferred raw-host operation contract", path)
		}
		for _, capability := range []string{"sync", "noblock", "worker", "schedulerwait"} {
			if strings.Contains(text, "//llgo:coro "+capability+"\n//go:linkname Create ") {
				t.Fatalf("general pthread.Create in %s acquired a coroutine %s capability", path, capability)
			}
			if strings.Contains(text, "//llgo:coro "+capability+"\n//go:linkname Join ") {
				t.Fatalf("blocking pthread.Join in %s acquired obsolete or incorrect %s declaration capability", path, capability)
			}
		}
	}
	if strings.Contains(declaration, "//llgo:coro noblock\n//go:linkname Create ") ||
		strings.Contains(declaration, "//llgo:coro worker\n//go:linkname Create ") {
		t.Fatal("scheduler-owned worker creation has a stronger or scheduler-only capability")
	}
}

func TestRuntimeCoroWorkerBlockingCallHasOnlyGuardedSameMEntrance(t *testing.T) {
	declaration := readRuntimePollFile(t, runtimeCoroWorkerCallSource)
	requireRuntimeAnnotationFreeCDeclarations(t, runtimeCoroWorkerCallSource, "Call")
	if strings.Contains(declaration, "func QueueWaitTake(") {
		t.Errorf("%s exposes the blocking worker consumer loop to managed Go", runtimeCoroWorkerCallSource)
	}
	for _, required := range []string{
		"reserved for the runtime's dynamically proved",
		"LockOSThread path",
		"//go:linkname Call C.__llgo_coro_worker_call_v1",
		"func Call(function uintptr, argc uint32, args *[MaxArgs]uintptr, result *Result) bool",
	} {
		if !strings.Contains(declaration, required) {
			t.Errorf("%s lacks guarded same-M call contract %q", runtimeCoroWorkerCallSource, required)
		}
	}

	entrance := readRuntimePollFile(t, runtimeCoroOSThreadForeignSource)
	for _, required := range []string{
		"sole same-M blocking foreign",
		"!coro.CurrentOSThreadLocked(task)",
		"type coroNativeForeignBoundaryV1 struct",
		"coroNativeForeignBoundaryTLSV1      tls.Handle[*coroNativeForeignBoundaryV1]",
		"func coroNativeForeignBoundaryTLSStartV1() bool",
		"tls.Alloc[*coroNativeForeignBoundaryV1](nil)",
		"!coroNativeForeignBoundaryTLSReadyV1",
		"coro.CurrentExecutorDriver(task)",
		"coro.DetachExecutorResume(",
		"boundary.parent.handoff.Begin(boundary.ownerEpoch)",
		"coroNativeMAllocateReplacementV1(",
		"coroTargetReleaseManagedExecutionV1(boundary.driver)",
		"coroNativeMStartPhysicalOwnerV1(replacement, slot)",
		"boundary.beginV1(task, coro.ExecutorResumeHandoffLockedForeign)",
		"callOK := coroworker.Call(function, argc, &args, &result)",
		"boundary.parent.handoff.RequestReturn(boundary.baton)",
		"coroNativeMReplacementLineageOwnerV1(",
		"coroNativeMRecycleReplacementV1(returnedSlot)",
		"coroTargetReenterManagedExecutionV1(boundary.driver)",
		"coro.RestoreExecutorResume(&boundary.resume)",
		"boundary.finishV1()",
		"//export __llgo_coro_foreign_reentry_acquire_v1",
		"coro.ExecutorResumeHandoffContext(&boundary.resume)",
		"coro.BeginForeignReentry(&record, &boundary.resume, child)",
		"coro.ConsumeForeignReentryCompletion(&record)",
		"//export __llgo_coro_foreign_reentry_run_v1",
		"//export __llgo_coro_foreign_reentry_failure_v1",
		"//export __llgo_coro_same_m_foreign_call_v1",
		"boundary.beginV1(task, coro.ExecutorResumeHandoffSameMForeign)",
		"callOK := coroworker.Call(thunk, 1, &args, &result)",
	} {
		if !strings.Contains(entrance, required) {
			t.Errorf("%s lacks locked-thread call guard %q", runtimeCoroOSThreadForeignSource, required)
		}
	}
	target := readRuntimePollFile(t, runtimeCoroNativeFleetTargetSource)
	if !strings.Contains(target, "!coroNativeForeignBoundaryTLSStartV1()") {
		t.Errorf(
			"%s does not initialize managed foreign-reentry TLS during serialized fleet startup",
			runtimeCoroNativeFleetTargetSource,
		)
	}
	for _, forbidden := range []string{
		"map[uintptr]",
		"reflect.ValueOf",
		"runtime.FuncForPC",
		"workeraddr",
	} {
		if strings.Contains(entrance, forbidden) {
			t.Errorf(
				"%s retained callback address recovery marker %q",
				runtimeCoroOSThreadForeignSource, forbidden,
			)
		}
	}
	detach := strings.Index(entrance, "coro.DetachExecutorResume(")
	start := strings.Index(entrance, "if boundary.startReplacementV1(true)")
	leave := strings.Index(entrance, "coroTargetReleaseManagedExecutionV1(boundary.driver)")
	create := strings.Index(entrance, "coroNativeMStartPhysicalOwnerV1(replacement, slot)")
	begin := strings.LastIndex(entrance, "boundary.beginV1(task, coro.ExecutorResumeHandoffLockedForeign)")
	call := strings.Index(entrance, "callOK := coroworker.Call(function, argc, &args, &result)")
	finish := strings.LastIndex(entrance, "boundary.finishV1()")
	request := strings.LastIndex(entrance, "boundary.parent.handoff.RequestReturn(boundary.baton)")
	recycle := strings.Index(entrance, "coroNativeMRecycleReplacementV1(returnedSlot)")
	reenter := strings.LastIndex(entrance, "coroTargetReenterManagedExecutionV1(boundary.driver)")
	restore := strings.LastIndex(entrance, "coro.RestoreExecutorResume(&boundary.resume)")
	if detach < 0 || start <= detach || create <= leave ||
		request < 0 || recycle <= request || reenter <= recycle || restore <= reenter ||
		begin < 0 || call <= begin || finish <= call {
		t.Errorf("%s does not bracket same-M C with detach/release/create/return/recycle/restore", runtimeCoroOSThreadForeignSource)
	}
	quota := readRuntimePollFile(t, "internal/runtime/coro_execution_quota_native_llgo.go")
	for _, required := range []string{
		"func coroTargetReleaseManagedExecutionV1(driver *coro.ExecutorDriver) bool",
		"func coroTargetReenterManagedExecutionV1(driver *coro.ExecutorDriver) bool",
		"acquired, ok := coroTargetAcquireManagedExecutionV1(driver)",
		"if !coroTargetWaitManagedExecutionV1(driver)",
	} {
		if !strings.Contains(quota, required) {
			t.Errorf("native execution quota lacks same-M compensation marker %q", required)
		}
	}
	cSource := readRuntimePollFile(t, runtimeCoroWorkerCSource)
	for _, required := range []string{
		"This is the complete blocking worker island",
		"__llgo_coro_worker_queue_wait_take_v1(&job)",
		"__llgo_coro_worker_call_v1(",
		"__llgo_coro_native_worker_complete_v1(",
	} {
		if !strings.Contains(cSource, required) {
			t.Errorf("%s lacks fixed native-stack worker step %q", runtimeCoroWorkerCSource, required)
		}
	}
}

func TestRuntimePthreadPrimitivesKeepPhysicalWaitSemantics(t *testing.T) {
	text := readRuntimePollFile(t, runtimePthreadSyncSource)
	for _, symbol := range []string{
		"c_pthread_mutex_lock",
		"c_pthread_rwlock_rdlock",
		"c_pthread_rwlock_wrlock",
		"c_pthread_cond_wait",
		"c_pthread_cond_timedwait",
	} {
		if !strings.Contains(text, "//go:linkname "+symbol+" ") {
			t.Errorf("blocking pthread primitive %s lacks its exact declaration", symbol)
		}
		for _, capability := range []string{"noblock", "sync", "worker", "schedulerwait"} {
			marker := "//llgo:coro " + capability + "\n//go:linkname " + symbol + " "
			if strings.Contains(text, marker) {
				t.Errorf("blocking pthread primitive %s acquired obsolete or incorrect %s declaration capability", symbol, capability)
			}
		}
	}
	for _, required := range []string{
		"legal only in an audited raw",
		"managed callers retain BlockForeign/WaitForeign",
		"Direct raw-host execution",
		"is inferred at each compiler-owned invocation",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("pthread wait declarations lack inferred raw-host audit marker %q", required)
		}
	}
	defaultSymbols := []string{
		"c_pthread_mutex_destroy",
		"c_pthread_rwlock_init",
		"c_pthread_rwlock_destroy",
		"c_pthread_cond_init",
		"c_pthread_cond_destroy",
		"c_pthread_cond_signal",
		"c_pthread_cond_broadcast",
	}
	requireRuntimeAnnotationFreeCDeclarations(t, runtimePthreadSyncSource, defaultSymbols...)
	for _, symbol := range defaultSymbols {
		if !strings.Contains(text, "//go:linkname "+symbol+" ") {
			t.Errorf("pthread lifecycle/notification primitive %s lacks its exact declaration", symbol)
		}
	}
	for _, required := range []string{
		"ordinary foreign default",
		"invoke a callback, or retain either argument",
		"Condition lifecycle and notification operations",
		"rather than a declaration-wide sync capability",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("pthread default-contract migration lacks producer-semantics marker %q", required)
		}
	}
}

func TestRuntimeCoroWorkerDestroyCapabilityIsAfterJoinScoped(t *testing.T) {
	native := readRuntimePollFile(t, runtimeCoroNativeWorkerSource)
	for _, required := range []string{
		"func coroNativeWorkerPoolResetAfterJoinV1(state *coroNativeWorkerPoolV1) bool",
		"state.created != 0 || !state.stopping",
		"coroworker.QueueDestroyAfterJoin()",
		"joined := coroNativeWorkerPoolJoinCreatedV1(state)",
	} {
		if !strings.Contains(native, required) {
			t.Errorf("%s lacks after-join destroy invariant %q", runtimeCoroNativeWorkerSource, required)
		}
	}
	if got := strings.Count(native, "coroworker.QueueDestroyAfterJoin()"); got != 1 {
		t.Fatalf("private worker queue destroy uses = %d, want exactly 1", got)
	}
	for _, forbidden := range []string{"state.work.Destroy()", "state.mutex.Destroy()", "pthread_cond_destroy", "pthread_mutex_destroy"} {
		if strings.Contains(native, forbidden) {
			t.Fatalf("worker pool bypasses C queue after-join lifecycle with %q", forbidden)
		}
	}

	declaration := readRuntimePollFile(t, runtimeCoroWorkerCallSource)
	requireRuntimeAnnotationFreeCDeclarations(t, runtimeCoroWorkerCallSource, "QueueDestroyAfterJoin")
	for _, required := range []string{
		"//go:linkname QueueDestroyAfterJoin C.__llgo_coro_worker_queue_destroy_after_join_v1",
		"func QueueDestroyAfterJoin() bool",
		"The caller must have joined every worker",
		"verifies that every published position was consumed",
		"performs no wait",
	} {
		if !strings.Contains(declaration, required) {
			t.Errorf("%s lacks after-join destroy contract %q", runtimeCoroWorkerCallSource, required)
		}
	}
	wrapper := readRuntimePollFile(t, runtimeCoroWorkerCSource)
	for _, required := range []string{
		"bool __llgo_coro_worker_queue_destroy_after_join_v1(void)",
		"queue->enqueue_position",
		"queue->dequeue_position",
		"llgo_coro_worker_wake_destroy_v1(&queue->wake)",
		"atomic_store_explicit(&queue->initialized, false, memory_order_release)",
	} {
		if !strings.Contains(wrapper, required) {
			t.Errorf("%s lacks after-join destroy implementation %q", runtimeCoroWorkerCSource, required)
		}
	}
}

func TestRuntimePollWaitSeparatesLegacyWorkerAndCoroutineOwnerABI(t *testing.T) {
	goSource := readRuntimePollFile(t, runtimePollGoSource)
	for _, required := range []string{
		"coro_runtime_adapter_test || !(llgo && llgo_coro && llgo_coro_native_pipe && llgo_coro_native_timer)",
		"//go:linkname pollFuncPCABI0 llgo.funcPCABI0",
		"//go:linkname pollSyscall llgo.syscall",
		"//go:linkname pollWaitFixedV1 C.__llgo_runtime_poll_wait_v1",
		"pollFuncPCABI0(pollWaitFixedV1)",
		"uintptr(uint32(timeout))",
		"n, errno := runtimePollWaitFixedV1(&fds[0], 2, timeout)",
		"if int(errno) == int(csyscall.EINTR)",
		"With the worker capability disabled, llgo.syscall keeps its legacy direct",
	} {
		if !strings.Contains(goSource, required) {
			t.Errorf("%s lacks fixed worker ABI marker %q", runtimePollGoSource, required)
		}
	}
	for _, forbidden := range []string{
		"//go:linkname c_poll C.poll",
		"cliteos.Errno()",
	} {
		if strings.Contains(goSource, forbidden) {
			t.Errorf("%s retains executor-thread poll/TLS errno path %q", runtimePollGoSource, forbidden)
		}
	}

	coroSource := readRuntimePollFile(t, runtimeCoroPollGoSource)
	for _, required := range []string{
		"llgo && llgo_coro && llgo_coro_native_pipe && llgo_coro_native_timer",
		"//go:linkname llgoCoroPollWaitV2 llgo.coroPollWait",
		"func llgoCoroPollWaitV2(ctx uintptr, fd int32, interest uint32, deadline int64) uint32",
		"C.__llgo_coro_poll_update_deadline_or_abort_v1",
		"C.__llgo_coro_poll_post_closing_or_abort_v1",
		"return llgoCoroPollWaitV2(ctx, fd, interest, deadline)",
		"func pollCoroPrepareWaitV2(ctx uintptr, mode int)",
		"func pollCoroFinishWaitV2(ctx uintptr, mode int, result uint32)",
		"pollCoroWaitOneV2(ctx, fd, interest, deadline)",
		"llgoCoroPollUpdateDeadlineOrAbortV1(ctx, coroPollInterestReadV2, deadline)",
		"llgoCoroPollPostClosingOrAbortV1(ctx, coroPollInterestReadV2)",
		"reset-after-expiry rule",
		"result, statErrno := coroPollFstat(fd, &info)",
		"uint32(result) == ^uint32(0)",
		"case uint32(csyscall.S_IFSOCK):",
		"return true, pollCoroFDStreamLeafV1(fd), 0",
		"case uint32(csyscall.S_IFIFO), uint32(csyscall.S_IFCHR):",
		"return true, false, 0",
		"return false, false, int(csyscall.EOPNOTSUPP)",
		"llrt.CoroNativePollServerDescriptorV1(fd)",
		"keep their Go blocking style without a Future/Await API or one worker thread",
	} {
		if !strings.Contains(coroSource, required) {
			t.Errorf("%s lacks coroutine poll marker %q", runtimeCoroPollGoSource, required)
		}
	}
	for path, trampoline := range map[string]string{
		"internal/lib/runtime/poll_fstat_linux_coro_llgo.go":  "//go:linkname libc_fstat_trampoline C.fstat",
		"internal/lib/runtime/poll_fstat_darwin_coro_llgo.go": "//go:linkname libc_fstat64_trampoline C.fstat64",
	} {
		statSource := readRuntimePollFile(t, path)
		syscallMarker := "//go:linkname coroPollFstatSyscall3 llgo.syscall32"
		if strings.Contains(path, "darwin") {
			syscallMarker = "runtimeDarwinSyscall3Int32("
		}
		for _, marker := range []string{
			trampoline,
			syscallMarker,
			"func coroPollFstat(fd int32, info *cliteos.StatT) (result, errno uintptr)",
		} {
			if !strings.Contains(statSource, marker) {
				t.Errorf("%s lacks fixed coroutine Fstat errno transport %q", path, marker)
			}
		}
		if strings.Contains(statSource, "//llgo:coro") {
			t.Errorf("%s retains a derivable worker-address directive", path)
		}
	}
	for _, forbidden := range []string{
		"pollSyscall",
		"runtimePollWaitFixedV1",
		"pthread_create",
		"coroworker",
		"type Future",
		"func Await",
		"llgoCoroPollWaitTokenV1",
		"llgoCoroPollPrepareOrAbortV1",
		"llgoCoroPollRetireCompletedOrAbortV1",
		"llgoCoroPollParkV1",
		"llgo.coroPark",
		"cliteos.Errno()",
	} {
		if strings.Contains(coroSource, forbidden) {
			t.Errorf("%s retains per-wait worker/public async path %q", runtimeCoroPollGoSource, forbidden)
		}
	}
	assertRuntimePollTypedParkRecipe(t, runtimeCoroPollGoSource, coroSource)
	assertRuntimePollTimeoutPrefersClosing(t, coroSource)

	cSource := readRuntimePollFile(t, runtimePollCSource)
	for _, required := range []string{
		"uintptr_t __llgo_runtime_poll_wait_v1(",
		"nfds_t nfds = (nfds_t)nfds_word;",
		"int timeout = (int)(int32_t)(uint32_t)timeout_word;",
		"int result = poll(fds, nfds, timeout);",
		"return UINTPTR_MAX;",
	} {
		if !strings.Contains(cSource, required) {
			t.Errorf("%s lacks fixed poll wrapper marker %q", runtimePollCSource, required)
		}
	}
	// These two exact expressions freeze the -1 contract without executing an
	// unbounded wait: Go publishes 0xffffffff and C restores int32(-1) before
	// widening to the platform's int.
	if !strings.Contains(goSource, "uintptr(uint32(timeout))") ||
		!strings.Contains(cSource, "(int)(int32_t)(uint32_t)timeout_word") {
		t.Fatal("poll timeout -1 low-32-bit round trip is not explicit at both ABI ends")
	}

	manifest := readRuntimePollFile(t, "internal/runtime/coro_poll_c_llgo.go")
	if !strings.Contains(manifest, "_wrap/poll.c") {
		t.Fatal("compiler runtime C manifest does not include the fixed poll wrapper")
	}
	legacyManifest := readRuntimePollFile(t, "internal/lib/runtime/runtime_default.go")
	if strings.Contains(legacyManifest, "_wrap/poll.c") {
		t.Fatal("standard-library runtime patch still owns the compiler poll C object")
	}
}

func TestRuntimePollSourceSelectionUsesCompleteNativeCapability(t *testing.T) {
	native := []string{"llgo", "llgo_coro", "llgo_coro_native_pipe", "llgo_coro_native_timer"}
	tests := []struct {
		name      string
		goos      string
		goarch    string
		buildTags []string
		legacy    bool
		coro      bool
	}{
		{name: "ordinary linux", goos: "linux", legacy: true},
		{name: "ordinary darwin", goos: "darwin", legacy: true},
		{name: "coro tag alone falls back", goos: "linux", buildTags: []string{"llgo", "llgo_coro"}, legacy: true},
		{name: "missing pipe falls back", goos: "linux", buildTags: []string{"llgo", "llgo_coro", "llgo_coro_native_timer"}, legacy: true},
		{name: "missing timer falls back", goos: "linux", buildTags: []string{"llgo", "llgo_coro", "llgo_coro_native_pipe"}, legacy: true},
		{name: "complete linux capability", goos: "linux", buildTags: native, coro: true},
		{name: "complete linux arm64 capability", goos: "linux", goarch: "arm64", buildTags: native, coro: true},
		{name: "complete darwin capability", goos: "darwin", buildTags: native, coro: true},
		{name: "complete darwin arm64 capability", goos: "darwin", goarch: "arm64", buildTags: native, coro: true},
		{name: "adapter selects legacy", goos: "linux", buildTags: append(slices.Clone(native), "coro_runtime_adapter_test"), legacy: true},
		{name: "baremetal owns poll elsewhere", goos: "linux", buildTags: append(slices.Clone(native), "baremetal")},
		{name: "unsupported windows", goos: "windows", buildTags: native},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := build.Default
			ctx.GOOS = test.goos
			ctx.GOARCH = test.goarch
			if ctx.GOARCH == "" {
				ctx.GOARCH = "amd64"
			}
			ctx.BuildTags = slices.Clone(test.buildTags)
			for file, want := range map[string]bool{
				filepath.Base(runtimePollGoSource):     test.legacy,
				filepath.Base(runtimeCoroPollGoSource): test.coro,
			} {
				got, err := ctx.MatchFile(filepath.Dir(runtimePollGoSource), file)
				if err != nil {
					t.Fatalf("MatchFile(%q): %v", file, err)
				}
				if got != want {
					t.Errorf("MatchFile(%q) = %t, want %t", file, got, want)
				}
			}
		})
	}
}

func assertRuntimePollTimeoutPrefersClosing(t *testing.T, source string) {
	t.Helper()
	start := strings.Index(source, "case coroPollResultTimeoutV2:")
	if start < 0 {
		t.Fatal("coroutine poll finish lacks timeout branch")
	}
	end := strings.Index(source[start:], "default:")
	if end < 0 {
		t.Fatal("coroutine poll timeout branch lacks following default")
	}
	branch := source[start : start+end]
	closing := strings.Index(branch, "pollDescClosing(state)")
	deadline := strings.Index(branch, "current := pollDeadline(ctx, mode)")
	if closing < 0 || deadline < 0 || closing > deadline {
		t.Fatalf("timeout completion does not give closing priority:\n%s", branch)
	}
}

func assertRuntimePollTypedParkRecipe(t *testing.T, path, source string) {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
	if err != nil {
		t.Fatal(err)
	}
	var function *ast.FuncDecl
	for _, declaration := range file.Decls {
		candidate, ok := declaration.(*ast.FuncDecl)
		if ok && candidate.Name.Name == "pollCoroWaitOneV2" {
			function = candidate
			break
		}
	}
	if function == nil || function.Body == nil || len(function.Body.List) != 1 {
		t.Fatalf("%s pollCoroWaitOneV2 is not one exact typed intrinsic return", path)
	}
	ret, ok := function.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		t.Fatal("poll typed park recipe is not one exact return")
	}
	call, ok := ret.Results[0].(*ast.CallExpr)
	if !ok || len(call.Args) != 4 {
		t.Fatal("poll typed park recipe does not call the exact four-argument intrinsic")
	}
	identifier, ok := call.Fun.(*ast.Ident)
	if !ok || identifier.Name != "llgoCoroPollWaitV2" {
		t.Fatal("poll typed park recipe does not call llgoCoroPollWaitV2")
	}
	for index, want := range []string{"ctx", "fd", "interest", "deadline"} {
		argument, ok := call.Args[index].(*ast.Ident)
		if !ok || argument.Name != want {
			t.Fatalf("poll typed park argument %d is not exact scalar %s", index, want)
		}
	}
	functionText := coroTimerOwnerNodeText(t, function)
	forbidden := map[string]bool{
		"llgoPollDesc": false,
		"token":        false,
		"ticket":       false,
		"slot":         false,
		"generation":   false,
	}
	ast.Inspect(function, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if ok {
			if _, exists := forbidden[identifier.Name]; exists {
				forbidden[identifier.Name] = true
			}
		}
		return true
	})
	for name, found := range forbidden {
		if found {
			t.Fatalf("poll typed park recipe retains forbidden %s provenance:\n%s", name, functionText)
		}
	}
}

func TestRuntimePollFixedCWrapper(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skipf("poll wrapper is POSIX-only on %s", runtime.GOOS)
	}
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("host C compiler is unavailable")
	}
	dir := t.TempDir()
	testSource := filepath.Join(dir, "poll_wrapper_test.c")
	program := `
#include <errno.h>
#include <poll.h>
#include <stdint.h>
#include <unistd.h>

uintptr_t __llgo_runtime_poll_wait_v1(uintptr_t, uintptr_t, uintptr_t);
uintptr_t __llgo_runtime_poll_desc_alloc_v1(int32_t, uint32_t);
void __llgo_runtime_poll_desc_free_v1(uintptr_t);
uint64_t __llgo_runtime_poll_desc_state_v1(uintptr_t);
int64_t __llgo_runtime_poll_desc_deadline_v1(uintptr_t, int32_t);
void __llgo_runtime_poll_desc_set_deadline_v1(uintptr_t, int32_t, int64_t);
uint64_t __llgo_runtime_poll_desc_mark_closing_v1(uintptr_t);
uint64_t __llgo_runtime_poll_desc_load_operation_v1(uintptr_t, uint32_t);
uint32_t __llgo_runtime_poll_desc_publish_operation_v1(
    uintptr_t, uint32_t, uint32_t, uint32_t);
uint32_t __llgo_runtime_poll_desc_clear_operation_v1(
    uintptr_t, uint32_t, uint32_t, uint32_t);

int main(void) {
    int pipefd[2];
    if (pipe(pipefd) != 0) {
        return 10;
    }
    struct pollfd fd = { .fd = pipefd[0], .events = POLLIN, .revents = 0 };
    if (__llgo_runtime_poll_wait_v1((uintptr_t)&fd, 1, 0) != 0) {
        return 11;
    }
    const char byte = 'x';
    if (write(pipefd[1], &byte, 1) != 1) {
        return 12;
    }
    fd.revents = 0;
    if (__llgo_runtime_poll_wait_v1((uintptr_t)&fd, 1, UINT32_MAX) != 1 ||
        (fd.revents & POLLIN) == 0) {
        return 13;
    }
    errno = 0;
    if (__llgo_runtime_poll_wait_v1(0, 1, 0) != UINTPTR_MAX || errno == 0) {
        return 14;
    }
    if (close(pipefd[0]) != 0 || close(pipefd[1]) != 0) {
        return 15;
    }
    uintptr_t context = __llgo_runtime_poll_desc_alloc_v1(23, 1);
    if (context == 0 || (uint32_t)__llgo_runtime_poll_desc_state_v1(context) != 23) {
        return 16;
    }
    __llgo_runtime_poll_desc_set_deadline_v1(context, 'r', 1234);
    if (__llgo_runtime_poll_desc_deadline_v1(context, 'r') != 1234) {
        return 17;
    }
    const uint32_t source_slot = 0x01010001;
    const uint32_t generation = 9;
    const uint64_t operation =
        ((uint64_t)generation << 32) | (uint64_t)source_slot;
    if (__llgo_runtime_poll_desc_publish_operation_v1(
            context, 1, source_slot, generation) != 1 ||
        __llgo_runtime_poll_desc_load_operation_v1(context, 1) != operation ||
        __llgo_runtime_poll_desc_publish_operation_v1(
            context, 1, source_slot, generation) != 0) {
        return 18;
    }
    if (__llgo_runtime_poll_desc_clear_operation_v1(
            context, 1, source_slot, generation + 1) != 0 ||
        __llgo_runtime_poll_desc_clear_operation_v1(
            context, 1, source_slot, generation) != 1 ||
        __llgo_runtime_poll_desc_load_operation_v1(context, 1) != 0) {
        return 19;
    }
    if ((__llgo_runtime_poll_desc_mark_closing_v1(context) &
            (UINT64_C(1) << 32)) != 0 ||
        __llgo_runtime_poll_desc_publish_operation_v1(
            context, 2, source_slot, generation) != 3) {
        return 20;
    }
    if ((__llgo_runtime_poll_desc_mark_closing_v1(context) &
            (UINT64_C(1) << 32)) == 0 ||
        __llgo_runtime_poll_desc_clear_operation_v1(
            context, 2, source_slot, generation) != 1) {
        return 21;
    }
    __llgo_runtime_poll_desc_free_v1(context);
    return 0;
}
`
	if err := os.WriteFile(testSource, []byte(program), 0o600); err != nil {
		t.Fatal(err)
	}
	wrapper, err := filepath.Abs(runtimePollCSource)
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(dir, "poll_wrapper_test")
	compile := exec.Command(cc, "-std=c11", "-Wall", "-Wextra", "-Werror", wrapper, testSource, "-o", executable)
	if output, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile fixed poll wrapper: %v\n%s", err, output)
	}
	run := exec.Command(executable)
	if output, err := run.CombinedOutput(); err != nil {
		t.Fatalf("run fixed poll wrapper: %v\n%s", err, output)
	}
}

func readRuntimePollFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
