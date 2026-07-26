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
