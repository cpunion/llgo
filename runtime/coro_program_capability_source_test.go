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

// TestRuntimeCoroProgramWorkerCapabilityIsEndToEndOptional is an architecture
// gate, not a spelling inventory. It keeps the compiler-emitted closed-program
// fact authoritative from bootstrap validation through native source binding
// and physical worker startup. A partial implementation at any one of those
// layers would retain the worker subsystem or create four idle pthreads for a
// program which has no physical worker operation.
func TestRuntimeCoroProgramWorkerCapabilityIsEndToEndOptional(t *testing.T) {
	bootstrapPath := "internal/coro/bootstrap.go"
	bootstrap := readRuntimePollFile(t, bootstrapPath)
	for _, required := range []string{
		"ProgramCapabilityWorkerV2 ProgramCapabilitiesV2 = 1 << iota",
		"const known = ProgramCapabilityWorkerV2",
		"lo, hi = mixProgramDigestV2(lo, hi, uint64(bootstrap.Flags))",
		"func ResolveProgramCapabilitiesV2(",
		"capabilities := ProgramCapabilitiesV2(current.bootstrap.Flags)",
	} {
		if !strings.Contains(bootstrap, required) {
			t.Errorf("%s lacks program capability contract %q", bootstrapPath, required)
		}
	}

	programPath := "internal/runtime/coro_program.go"
	program := readRuntimePollFile(t, programPath)
	resolve := strings.Index(program, "capabilities, capabilityCode := coro.ResolveProgramCapabilitiesV2(program)")
	publish := strings.Index(program, "coroProgramCapabilitiesV2State = capabilities")
	bind := strings.Index(program, "!coroProgramBindExecutorV1()")
	if resolve < 0 || publish <= resolve || bind <= publish {
		t.Errorf("%s does not resolve and publish capabilities before source binding", programPath)
	}
	if !strings.Contains(program, "return coroProgramCapabilitiesV2State.Valid() && coroProgramCapabilitiesV2State.Worker()") {
		t.Errorf("%s lacks the validated worker capability query", programPath)
	}

	for _, path := range []string{
		"internal/runtime/coro_executor_driver_timer_llgo.go",
		"internal/runtime/coro_executor_driver_worker_llgo.go",
	} {
		driver := readRuntimePollFile(t, path)
		normalized := strings.Join(strings.Fields(driver), " ")
		for _, required := range []string{
			"var worker *coro.WorkerOperationSource",
			"coroProgramWorkerCapabilityV2()",
			"worker = &coroProgramWorkerSourceV1State",
			"Worker: worker",
		} {
			if !strings.Contains(normalized, required) {
				t.Errorf("%s lacks optional worker binding marker %q", path, required)
			}
		}
		if strings.Contains(normalized, "Worker: &coroProgramWorkerSourceV1State") {
			t.Errorf("%s restored unconditional program worker binding", path)
		}
	}

	fleetProgramPath := "internal/runtime/coro_native_fleet_program_llgo.go"
	fleetProgram := strings.Join(strings.Fields(readRuntimePollFile(t, fleetProgramPath)), " ")
	for _, required := range []string{
		"if coroProgramWorkerCapabilityV2() { worker = &coroProgramWorkerSourceV1State }",
		"Worker: worker",
	} {
		if !strings.Contains(fleetProgram, required) {
			t.Errorf("%s lacks optional adopted-worker marker %q", fleetProgramPath, required)
		}
	}

	fleetCorePath := "internal/runtime/coro_native_fleet.go"
	fleetCore := strings.Join(strings.Fields(readRuntimePollFile(t, fleetCorePath)), " ")
	for _, required := range []string{
		"func coroNativeFleetBindDomainV1(state *coroNativeFleetStateV1, index uint32, workerEnabled bool) bool",
		"if workerEnabled { worker = &domain.worker }",
		"workerEnabled := program == nil || program.sources.Worker != nil",
		"coroNativeFleetBindDomainV1(state, index, workerEnabled)",
	} {
		if !strings.Contains(fleetCore, required) {
			t.Errorf("%s lacks fleet worker optionality marker %q", fleetCorePath, required)
		}
	}

	workerPath := "internal/runtime/coro_worker_native_llgo.go"
	worker := strings.Join(strings.Fields(readRuntimePollFile(t, workerPath)), " ")
	for _, required := range []string{
		"func coroNativeWorkerPoolStartFleetV1() bool",
		"if !coroProgramWorkerCapabilityV2() { return coroNativeWorkerPoolCanReleaseV1() }",
	} {
		if !strings.Contains(worker, required) {
			t.Errorf("%s lacks no-worker physical-start marker %q", workerPath, required)
		}
	}

	// TLS setup happens before the scheduler can service a worker operation.
	// Its terminal caller owns diagnostics, so this leaf must remain free of
	// stdio which effect analysis would correctly classify as may-block.
	pthreadPath := "internal/runtime/g_pthread.go"
	pthread := readRuntimePollFile(t, pthreadPath)
	if strings.Contains(pthread, "c.Fprintf(") {
		t.Errorf("%s restored pre-scheduler stdio and false worker demand", pthreadPath)
	}
	if !strings.Contains(pthread, "The caller owns the terminal diagnostic") {
		t.Errorf("%s lost the audited terminal-diagnostic ownership", pthreadPath)
	}
}
