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

func TestCoroLLVMHandleControlIsSchedulerOwnerRawHostStackOnly(t *testing.T) {
	const path = "internal/runtime/coro_run_slice.go"
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, symbol := range []string{"coroHandleDone", "coroHandleResume", "coroHandleDestroy"} {
		if !strings.Contains(text, "//go:linkname "+symbol+" ") {
			t.Errorf("%s lacks exact compiler-owned handle declaration for %s", path, symbol)
		}
		for _, capability := range []string{"noblock", "sync", "schedulerwait", "worker"} {
			wrong := "//llgo:coro " + capability + "\n//go:linkname " + symbol + " "
			if strings.Contains(text, wrong) {
				t.Errorf("%s gives inferred raw-host operation %s the obsolete or incorrect %s declaration capability", path, symbol, capability)
			}
		}
	}
	for _, required := range []string{
		"direct execution is inferred only for the compiler-owned raw",
		"host-stack island",
		"the scheduler owner",
		"Resume may execute the",
		"coroutine until its next suspend and therefore is neither a bounded foreign",
		"leaf nor an ordinary synchronous runtime call",
		"Managed coroutine plans",
		"retain WaitForeign",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("%s lacks scheduler-stack audit marker %q", path, required)
		}
	}
}

func TestCoroBoundedRunSliceAuditsImmutableDriverOncePerHostEntry(t *testing.T) {
	read := func(path string) string {
		t.Helper()
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	corePath := "internal/coro/run_slice.go"
	core := read(corePath)
	for _, marker := range []string{
		"func nextExecutorRunStepAtValidated(",
		"type ExecutorRunSliceCapability struct",
		"func BeginExecutorRunSlice(",
		"func (capability *ExecutorRunSliceCapability) owner(",
		"func (capability *ExecutorRunSliceCapability) Next(",
		"func (capability *ExecutorRunSliceCapability) NextBeforeTime(",
		"func (capability *ExecutorRunSliceCapability) NextAt(",
		"return nextExecutorRunStepAtValidated(driver, now, withDeadline)",
	} {
		if !strings.Contains(core, marker) {
			t.Errorf("%s lacks bounded run-slice capability marker %q", corePath, marker)
		}
	}

	programPath := "internal/runtime/coro_sched.go"
	program := read(programPath)
	for _, marker := range []string{
		"run, runOK := coro.BeginExecutorRunSlice(driver)",
		"coroProgramNextRunStepV1(driver, &run)",
	} {
		if !strings.Contains(program, marker) {
			t.Errorf("%s lacks bounded program-run marker %q", programPath, marker)
		}
	}

	fleetPath := "internal/runtime/coro_run_slice.go"
	fleet := read(fleetPath)
	for _, marker := range []string{
		"run, runOK := coro.BeginExecutorRunSlice(driver)",
		"step, nextOK := run.NextAt(now)",
	} {
		if !strings.Contains(fleet, marker) {
			t.Errorf("%s lacks bounded fleet-run marker %q", fleetPath, marker)
		}
	}
	if strings.Contains(fleet, "coro.NextExecutorRunStepAt(driver, now)") {
		t.Errorf("%s repeats the full immutable driver audit inside its bounded loop", fleetPath)
	}
}
