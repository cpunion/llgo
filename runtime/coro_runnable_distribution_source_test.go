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
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestCoroRunnableDistributionUsesDemandAndExactMailbox(t *testing.T) {
	core := readRuntimePollFile(t, "internal/coro/executor_fleet.go")
	for _, required := range []string{
		"runnableDemandRequested",
		"runnableDemandClaimed",
		"func (fleet *ExecutorFleet) RequestPNeutralRunnable(",
		"func (fleet *ExecutorFleet) CancelPNeutralRunnableRequest(",
		"func (fleet *ExecutorFleet) DistributePNeutralRunnable(",
		"fleet.publishPreparedPNeutralRunnableBatchAndRequest(",
		"func (fleet *ExecutorFleet) PublishPNeutralRunnableAndRequest(",
		"Count    uint32",
		"preemptLoad(&slot.runnableDemand) != uint32(runnableDemandIdle)",
	} {
		if !strings.Contains(core, required) {
			t.Errorf("executor fleet lacks demand-driven distribution marker %q", required)
		}
	}

	transfer := readRuntimePollFile(t, "internal/coro/runnable_transfer.go")
	for _, required := range []string{
		"runnableTransferGImported",
		"g.transferState = runnableTransferGImported",
		"func PublishPNeutralRunnableBatch(",
		"source.readyCount / 2",
	} {
		if !strings.Contains(transfer, required) {
			t.Errorf("runnable transfer lacks imported ownership marker %q", required)
		}
	}

	scheduler := readRuntimePollFile(t, "internal/coro/scheduler.go")
	for _, required := range []string{
		"g.transferState == runnableTransferGImported",
		"g.transferState = runnableTransferGIdle",
		"g.transferState != runnableTransferGImported",
		"func BindRunnableOwner(g *G) bool",
		"runnableAffinity runnableOwnerAffinity",
	} {
		if !strings.Contains(scheduler, required) {
			t.Errorf("scheduler lacks imported dequeue gate %q", required)
		}
	}
}

func TestCoroDirectChannelPublisherDoesNotReadOwnerCursor(t *testing.T) {
	const name = "PublishExecutorDirectChannelCompletion"
	source := readRuntimePollFile(t, "internal/coro/direct_channel_completion.go")
	file, err := parser.ParseFile(token.NewFileSet(), "direct_channel_completion.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	var publisher *ast.FuncDecl
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == name {
			publisher = function
			break
		}
	}
	if publisher == nil {
		t.Fatalf("runtime core lacks %s", name)
	}
	ast.Inspect(publisher.Body, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "directChannelTail" {
			return true
		}
		if receiver, ok := selector.X.(*ast.Ident); ok && receiver.Name == "driver" {
			t.Error("direct-channel producer reads the owner-only inbox cursor")
		}
		return true
	})
	if !strings.Contains(source, "preemptLoadPointer(&driver.directChannelHead)") {
		t.Error("direct-channel producer lacks its atomic publication cursor gate")
	}
}

func TestCoroRunnableDistributionHasOneGenericTargetPath(t *testing.T) {
	paths := []string{
		"internal/runtime/coro_native_fleet.go",
		"internal/runtime/coro_native_fleet_owner_llgo.go",
		"internal/runtime/coro_ready_distribution_default.go",
		"internal/runtime/coro_ready_distribution_fleet_llgo.go",
		"internal/runtime/coro_program.go",
		"internal/runtime/coro_sched.go",
		"internal/runtime/coro_spawn.go",
	}
	var source strings.Builder
	for _, path := range paths {
		source.WriteString(readRuntimePollFile(t, path))
	}
	text := source.String()
	for _, required := range []string{
		"DistributePNeutralRunnable(",
		"RequestPNeutralRunnable(",
		"CancelPNeutralRunnableRequest(",
		"coroTargetRequestProgramRunnableV1(",
		"coro.BindRunnableOwner(&coroProgramGV1State)",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("native fleet distribution lacks generic marker %q", required)
		}
	}
	for _, forbidden := range []string{
		"readySpawn",
		"PublishInitialReadyHeadAndRequest",
		"coroTargetCanRecordReadySpawnV1",
		"coroTargetRecordReadySpawnV1",
	} {
		if strings.Contains(text, forbidden) {
			t.Errorf("native fleet distribution retained spawn-specific path %q", forbidden)
		}
	}
}

func TestCoroNativeFleetUsesFixedTopologyLogicalQuotaAndScalarPeerABI(t *testing.T) {
	fleet := readRuntimePollFile(t, "internal/runtime/coro_native_fleet.go")
	for _, required := range []string{
		"coroNativeFleetDomainCapacityV1 = coro.ExecutorFleetCapacity",
		"execution   coro.ExecutionQuota",
		"domainCount uint32",
		"count == 0 || count > coroNativeFleetDomainCapacityV1",
		"state.domainCount = count",
		"for index := uint32(0); index < count; index++",
	} {
		if !strings.Contains(fleet, required) {
			t.Errorf("native fleet lacks bounded topology marker %q", required)
		}
	}

	owner := readRuntimePollFile(t, "internal/runtime/coro_native_fleet_owner_llgo.go")
	for _, required := range []string{
		"[coroNativeFleetDomainCapacityV1 - 1]coroNativeFleetPhysicalOwnerV1",
		"policyEpoch uint32",
		"func coroNativeFleetPhysicalOwnerDesiredPeersV1(limit, capacity uint32)",
		"func coroNativeFleetPhysicalOwnersEnsureLockedV1(",
		"state.started < desired",
		"func coroNativeFleetSetExecutionLimitV1(limit uint32)",
		"coroNativeAtomicStoreV1(&state.policyEpoch, epoch+1)",
		"coroNativeMStartPhysicalOwnerV1(owner, slot)",
		"func __llgo_coro_native_fleet_owner_v2(slot uint32) uint32",
		"coroNativeMRunReplacementOwnerV1(slot)",
		"owner.baton.Valid() {\n\t\treturn 2",
		"state.stop.Quiesced()",
	} {
		if !strings.Contains(owner, required) {
			t.Errorf("native fleet physical owner lacks fixed-topology marker %q", required)
		}
	}
	for _, forbidden := range []string{
		"coroNativeFleetPeerIndexV1",
		"state.lifecycle == coroNativeFleetPhysicalStoppingV1",
		"__llgo_coro_native_fleet_owner_v1",
	} {
		if strings.Contains(owner, forbidden) {
			t.Errorf("native fleet physical owner retained fixed/non-atomic path %q", forbidden)
		}
	}

	directory := readRuntimePollFile(t, "internal/runtime/coro_native_m_owner_llgo.go")
	for _, required := range []string{
		"coroNativeMDirectoryCapacityV1 uint32 = 10_000",
		"coroNativeMPageCapacityV1      uint32 = 64",
		"handoff coro.ExecutionDomainHandoff",
		"resume  coro.ExecutorResumeHandoff",
		"token  uint32",
		"owners [coroNativeFleetDomainCapacityV1]coroNativeMOwnerV1",
		"pages  [coroNativeMPageCountV1]unsafe.Pointer",
		"active [coroNativeFleetDomainCapacityV1]uint32",
		"func coroNativeMEnsureOwnerForSlotV1(slot uint32)",
		"coroNativeAtomicCASPointerV1(pageAddress, nil, publishing)",
		"page = unsafe.Pointer(new(coroNativeMOwnerPageV1))",
		"coroNativeAtomicStorePointerV1(pageAddress, page)",
		"for page == publishing",
		"corofleet.TryReuseOwner(&owner.thread, &owner.token, slot)",
		"corofleet.ReleaseOwner(",
		"coroNativeMAllocateReplacementV1(",
		"coroNativeMClaimReplacementV1(",
		"coroNativeMFinishReplacementReturnV1(",
	} {
		if !strings.Contains(directory, required) {
			t.Errorf("native M directory lacks replacement-owner marker %q", required)
		}
	}
	if strings.Contains(directory, "owners [coroNativeMDirectoryCapacityV1]coroNativeMOwnerV1") {
		t.Error("native M directory still reserves every logical owner in BSS")
	}
	if strings.Index(directory, "new(coroNativeMOwnerPageV1)") <
		strings.Index(directory, "coroNativeAtomicCASPointerV1(pageAddress, nil, publishing)") {
		t.Error("native M page allocation occurs before exclusive publication claim")
	}

	quota := readRuntimePollFile(t, "internal/runtime/coro_execution_quota_native_llgo.go")
	for _, required := range []string{
		"corofleet.OwnerCount(coroNativeMaximumLogicalProcsV1)",
		"coroNativeFleetV1State.execution.TryAcquire(route)",
		"coroNativeFleetV1State.execution.Release(route)",
		"func CoroGOMAXPROCS(n int) int",
		"coroNativeFleetSetExecutionLimitV1(next)",
		"coroNativeFleetRingExecutionWaitersV1(waiters uint32)",
		"coroNativeFleetV1State.execution.WaiterMask()",
	} {
		if !strings.Contains(quota, required) {
			t.Errorf("native fleet execution quota lacks logical-limit marker %q", required)
		}
	}

	distribution := readRuntimePollFile(t, "internal/runtime/coro_ready_distribution_fleet_llgo.go")
	for _, required := range []string{
		"func coroTargetBeginRunSliceV1(",
		"target.policyEpoch = epoch",
		"func coroTargetRefreshRunSliceV1(target coroRunTargetCapabilityV1)",
		"epoch != target.policyEpoch",
	} {
		if !strings.Contains(distribution, required) {
			t.Errorf("native fleet distribution lacks stable-slice policy marker %q", required)
		}
	}

	runSlice := readRuntimePollFile(t, "internal/runtime/coro_run_slice.go")
	for _, required := range []string{
		"if next.Kind == coro.ActionYield {",
		"coroTargetRefreshRunSliceV1(target)",
		"coroTargetReadyDistributionV1(target)",
	} {
		if !strings.Contains(runSlice, required) {
			t.Errorf("runtime run slice lacks yield-synchronized policy marker %q", required)
		}
	}
	if strings.Count(runSlice, "coroTargetRefreshRunSliceV1(target)") != 1 {
		t.Error("runtime run slice must refresh mutable placement policy only at the explicit yield boundary")
	}

	gomaxprocs := readRuntimePollFile(t, "internal/lib/runtime/gomaxprocs_coro_llgo.go")
	for _, required := range []string{
		"previous := llruntime.CoroGOMAXPROCS(n)",
		"if n > 0 && previous != n {",
		"coroSchedulerYield()",
	} {
		if !strings.Contains(gomaxprocs, required) {
			t.Errorf("public coroutine GOMAXPROCS wrapper lacks policy handoff marker %q", required)
		}
	}

	target := readRuntimePollFile(t, "internal/runtime/coro_target_native_fleet_llgo.go")
	for _, required := range []string{
		"coroNativeInitialExecutionLimitV1()",
		"coroTargetStartPhysicalThreadCapacityV1()",
		"coroNativeFleetStartProgramV1(coroNativeFleetDomainCapacityV1)",
		"coroNativeFleetV1State.execution.Start(limit)",
		"coroNativeFleetPhysicalOwnersStartV1(limit)",
		"coroNativeFleetV1State.execution.Seal()",
		"coroNativeFleetV1State.execution.Retire()",
		"coroNativeMStartCleanFactoryV1()",
		"coroNativeMStopStandbyV1()",
		"coroNativeMStopCleanFactoryV1()",
	} {
		if !strings.Contains(target, required) {
			t.Errorf("native fleet target lacks fixed-topology quota lifecycle marker %q", required)
		}
	}

	keyed := readRuntimePollFile(t, "internal/runtime/coro_keyed_post_native_llgo.go")
	for _, required := range []string{
		"coroNativeFleetActiveDomainForRouteV1(id.Route())",
		"coroNativeFleetV1State.fleet.PostManualAndRequest(id)",
		"coroNativeFleetRequestNeedsRingV1(domain, result.Executor)",
	} {
		if !strings.Contains(keyed, required) {
			t.Errorf("managed keyed completion lacks owner-joined post marker %q", required)
		}
	}
	for _, forbidden := range []string{
		"coroNativeFleetPostManualV1(id)",
		"domain.ingress.Enter()",
	} {
		if strings.Contains(keyed, forbidden) {
			t.Errorf("managed keyed completion retained external ingress path %q", forbidden)
		}
	}

	leaf := readRuntimePollFile(t, "internal/corofleet/_owner/owner.c")
	for _, required := range []string{
		"getenv(\"GOMAXPROCS\")",
		"sysconf(_SC_NPROCESSORS_ONLN)",
		"__llgo_coro_native_fleet_owner_v2(slot)",
		"llgo_coro_fleet_factory_main_v1",
		"llgo_coro_fleet_owner_main_v3",
		"LLGO_CORO_FLEET_FACTORY_REQUESTED_V1",
		"__llgo_coro_fleet_owner_create_v3(",
		"__llgo_coro_fleet_owner_try_reuse_v1(",
		"__llgo_coro_fleet_owner_release_v1(",
		"__llgo_coro_fleet_owner_stop_standby_v1(",
		"LLGO_CORO_FLEET_OWNER_STANDBY_CAPACITY_V1 = 8",
		"__llgo_coro_fleet_factory_stop_v2(uint32_t terminal_owner_token)",
		"pthread_cond_wait(&factory->changed, &factory->mutex)",
		"struct llgo_coro_fleet_owner_record_v1 *records;",
		"struct llgo_coro_fleet_owner_record_v1 *standby_head;",
		"struct llgo_coro_fleet_owner_record_v1 *all_next;",
		"struct llgo_coro_fleet_owner_record_v1 *standby_next;",
		"calloc(1, sizeof(*record))",
		"free(record)",
		"factory->next_token == UINT32_MAX",
	} {
		if !strings.Contains(leaf, required) {
			t.Errorf("native fleet C leaf lacks scalar startup-policy marker %q", required)
		}
	}
	for _, forbidden := range []string{
		"__llgo_coro_native_fleet_owner_v1",
		"__llgo_coro_fleet_owner_create_v1",
		"__llgo_coro_fleet_owner_create_v2",
		"records[LLGO_CORO_FLEET_OWNER_SLOT_CAPACITY_V1]",
		"slot_records[LLGO_CORO_FLEET_OWNER_SLOT_CAPACITY_V1]",
		"LLGO_CORO_FLEET_OWNER_TOKEN_INDEX_BITS_V1",
		"LLGO_CORO_FLEET_OWNER_TOKEN_GENERATION_MASK_V1",
		"malloc(",
		"void (*callback",
	} {
		if strings.Contains(leaf, forbidden) {
			t.Errorf("native fleet C leaf retained callback/address-owned path %q", forbidden)
		}
	}
}

func TestCoroNativePhysicalThreadCapacityCoversEveryRuntimeThreadCreator(t *testing.T) {
	core := readRuntimePollFile(t, "internal/coro/physical_thread_capacity.go")
	for _, required := range []string{
		"PhysicalThreadDefaultLimit uint32 = 10_000",
		"PhysicalThreadMaximumLimit uint32 = 1<<31 - 1",
		"type PhysicalThreadCapacity struct",
		"func (capacity *PhysicalThreadCapacity) Reserve()",
		"func (capacity *PhysicalThreadCapacity) Release() bool",
		"func (capacity *PhysicalThreadCapacity) SetLimit(",
	} {
		if !strings.Contains(core, required) {
			t.Errorf("physical thread capacity core lacks %q", required)
		}
	}

	native := readRuntimePollFile(t, "internal/runtime/coro_physical_thread_capacity_native_llgo.go")
	for _, required := range []string{
		"coroNativePhysicalThreadCapacityV1State.Start(",
		"func coroTargetReservePhysicalThreadV1() bool",
		"func coroTargetReleasePhysicalThreadV1() bool",
		"func CoroSetMaxThreads(n int) int",
		"coroNativePhysicalThreadCapacityV1State.SetLimit(next)",
	} {
		if !strings.Contains(native, required) {
			t.Errorf("native physical thread capacity adapter lacks %q", required)
		}
	}

	worker := readRuntimePollFile(t, "internal/runtime/coro_worker_native_llgo.go")
	reserveWorker := strings.Index(worker, "if !coroTargetReservePhysicalThreadV1()")
	createWorker := strings.Index(worker, "if coroworker.Create(&state.threads[index])")
	joinWorker := strings.Index(worker, "pthread.Join(thread, nil)")
	releaseWorker := strings.Index(worker, "!coroTargetReleasePhysicalThreadV1()")
	if reserveWorker < 0 || createWorker <= reserveWorker ||
		joinWorker < 0 || releaseWorker <= joinWorker {
		t.Error("native worker threads are not reserved before create and released after join")
	}

	directory := readRuntimePollFile(t, "internal/runtime/coro_native_m_owner_llgo.go")
	reuseOwner := strings.Index(directory, "corofleet.TryReuseOwner(&owner.thread, &owner.token, slot)")
	reserveOwner := strings.Index(directory, "if !coroTargetReservePhysicalThreadV1()")
	createOwner := strings.Index(directory, "corofleet.CreateOwner(&owner.thread, &owner.token, slot)")
	if reuseOwner < 0 || reserveOwner <= reuseOwner || createOwner <= reserveOwner {
		t.Error("native owner acquisition does not reuse standby before reserving and creating")
	}
	for _, required := range []string{
		"corofleet.ReleaseOwner(",
		"corofleet.StopStandby(&joined)",
		"coroTargetReservePhysicalThreadV1()",
		"corofleet.StartFactory()",
		"corofleet.StopFactory(terminalToken)",
		"coroTargetReleasePhysicalThreadV1()",
	} {
		if !strings.Contains(directory, required) {
			t.Errorf("native M lifecycle lacks physical capacity marker %q", required)
		}
	}

	target := readRuntimePollFile(t, "internal/runtime/coro_target_native_fleet_llgo.go")
	stopWorker := strings.Index(target, "!coroNativeWorkerPoolStopFleetV1()")
	stopStandby := strings.Index(target, "!coroNativeMStopStandbyV1()")
	stopFactory := strings.Index(target, "!coroNativeMStopCleanFactoryV1()")
	if stopWorker < 0 || stopStandby <= stopWorker || stopFactory <= stopStandby {
		t.Error("native target does not stop workers, standby Ms, and clean factory in ownership order")
	}

	stdlib := readRuntimePollFile(t, "internal/lib/runtime/setmaxthreads_coro_llgo.go")
	if !strings.Contains(stdlib, "return llruntime.CoroSetMaxThreads(in)") {
		t.Error("runtime/debug.SetMaxThreads is not delegated to the physical capacity ledger")
	}
}
