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
		"fleet.PublishPNeutralRunnableAndRequest(",
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
		"corofleet.CreateOwner(&owner.thread, slot)",
		"func __llgo_coro_native_fleet_owner_v2(slot uint32) uint32",
		"coroNativeMRunReplacementOwnerV1(slot)",
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
		"handoff coro.ExecutionDomainHandoff",
		"resume  coro.ExecutorResumeHandoff",
		"active [coroNativeFleetDomainCapacityV1]uint32",
		"coroNativeMAllocateReplacementV1(",
		"coroNativeMClaimReplacementV1(",
		"coroNativeMFinishReplacementReturnV1(",
	} {
		if !strings.Contains(directory, required) {
			t.Errorf("native M directory lacks replacement-owner marker %q", required)
		}
	}

	quota := readRuntimePollFile(t, "internal/runtime/coro_execution_quota_native_llgo.go")
	for _, required := range []string{
		"corofleet.OwnerCount(coroNativeMaximumLogicalProcsV1)",
		"coroNativeFleetV1State.execution.TryAcquire(route)",
		"coroNativeFleetV1State.execution.Release(route)",
		"func CoroGOMAXPROCS(n int) int",
		"coroNativeFleetRingExecutionWaitersV1()",
	} {
		if !strings.Contains(quota, required) {
			t.Errorf("native fleet execution quota lacks logical-limit marker %q", required)
		}
	}

	target := readRuntimePollFile(t, "internal/runtime/coro_target_native_fleet_llgo.go")
	for _, required := range []string{
		"coroNativeInitialExecutionLimitV1()",
		"coroNativeFleetStartProgramV1(coroNativeFleetDomainCapacityV1)",
		"coroNativeFleetV1State.execution.Start(limit)",
		"coroNativeFleetV1State.execution.Seal()",
		"coroNativeFleetV1State.execution.Retire()",
	} {
		if !strings.Contains(target, required) {
			t.Errorf("native fleet target lacks fixed-topology quota lifecycle marker %q", required)
		}
	}

	leaf := readRuntimePollFile(t, "internal/corofleet/_owner/owner.c")
	for _, required := range []string{
		"getenv(\"GOMAXPROCS\")",
		"sysconf(_SC_NPROCESSORS_ONLN)",
		"__llgo_coro_native_fleet_owner_v2((uint32_t)slot)",
		"__llgo_coro_fleet_owner_create_v2(pthread_t *thread, uint32_t slot)",
		"(void *)(uintptr_t)slot",
	} {
		if !strings.Contains(leaf, required) {
			t.Errorf("native fleet C leaf lacks scalar startup-policy marker %q", required)
		}
	}
	for _, forbidden := range []string{
		"__llgo_coro_native_fleet_owner_v1",
		"__llgo_coro_fleet_owner_create_v1",
		"malloc(",
		"void (*",
	} {
		if strings.Contains(leaf, forbidden) {
			t.Errorf("native fleet C leaf retained callback/address-owned path %q", forbidden)
		}
	}
}
