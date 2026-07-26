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
	"os"
	"strings"
	"testing"
)

func TestCoroNativePollReactorSharesExecutorWait(t *testing.T) {
	const source = "internal/runtime/coro_target_wait_timer_llgo.go"
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	for _, required := range []string{
		"coroNativePollCapacityV1 + 1",
		"coro.PollOperationConfiguredCapacity(",
		"coro.SnapshotExecutorPollOperation(",
		"corodoorbell.WaitPollSet(",
		"coroNativePollIDsV2",
		"coro.PostExecutorPollEvent(",
		"coro.PollOperationReady",
		"pipe.ConsumeRetainedWake()",
		"corodoorbell.DeadlinePollTimeout(",
	} {
		if !strings.Contains(body, required) {
			t.Errorf("%s lacks shared-reactor marker %q", source, required)
		}
	}
	for _, forbidden := range []string{
		"coroworker",
		"pthread",
		"libuv",
		"go func(",
		"pipe.Wait()",
		"pipe.WaitDeadline(",
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf("%s retains per-wait/legacy path %q", source, forbidden)
		}
	}
}

func TestCoroNativeFleetPollReactorKeepsOneExactPhysicalWaitPrimitive(t *testing.T) {
	const source = "internal/runtime/coro_native_fleet_reactor.go"
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	for _, required := range []string{
		"[coroNativeFleetDomainCapacityV1]coroNativeFleetPollSetV1",
		"poll, driver := domain.pollOwnerV1(), domain.driverOwnerV1()",
		"coro.SnapshotExecutorPollOperation(driver, index)",
		"domain.nextOwnerEpoch != wait.Epoch",
		"func coroNativeFleetBuildPhysicalWaitV1(",
		"func coroNativeFleetWaitPhysicalPassAtV1(",
		"coroNativeFleetPhysicalWaitIdleV1",
		"coroNativeFleetPhysicalWaitActiveV1",
		"domain.doorbell.ConsumeRetainedWake()",
		"corodoorbell.DeadlinePollTimeout(now, wait.deadline)",
		"corodoorbell.WaitPollSet(&storage.entries[0], wait.count, timeoutMS)",
		"coroNativeFleetPostPollV1(",
		"storage.operations[entry-1]",
		"coroNativeFleetWaitPassRetryV1",
		"coroNativeFleetWaitPassWakeV1",
	} {
		if !strings.Contains(body, required) {
			t.Errorf("%s lacks fixed fleet-reactor marker %q", source, required)
		}
	}
	for _, forbidden := range []string{
		"llgo_coro_native_fleet",
		"pthread",
		"coroworker",
		"libuv",
		"go func(",
		"make(",
		"map[",
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf("%s contains dynamic/per-wait mechanism %q", source, forbidden)
		}
	}

	const replacement = "internal/runtime/coro_native_replacement_reactor_llgo.go"
	data, err = os.ReadFile(replacement)
	if err != nil {
		t.Fatal(err)
	}
	body = string(data)
	for _, required := range []string{
		"coroNativeFleetBuildPhysicalWaitV1(",
		"coroNativeFleetWaitPhysicalPassAtV1(",
		"coroNativeFleetPhysicalWaitActiveV1",
	} {
		if !strings.Contains(body, required) {
			t.Errorf("%s lacks shared physical-wait marker %q", replacement, required)
		}
	}
	for _, forbidden := range []string{
		"corodoorbell.WaitPollSet(",
		"coro.SnapshotExecutorPollOperation(",
		"corodoorbell.DeadlinePollTimeout(",
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf("%s duplicates physical-wait mechanism %q", replacement, forbidden)
		}
	}
	owner, err := os.ReadFile("internal/runtime/coro_native_replacement_owner_llgo.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(owner), "coro.RequestExecutorSourceService(") {
		t.Error("replacement deadline does not request common reducer source service")
	}
}
