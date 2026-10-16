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

package coro

import "testing"

func TestProgramCapabilitiesKeepWorkerAndFaultDemandsOrthogonal(t *testing.T) {
	for _, test := range []struct {
		worker, dynamicPanicOnFault, nativeDefaultFaultBoundary bool
	}{
		{},
		{worker: true},
		{dynamicPanicOnFault: true},
		{nativeDefaultFaultBoundary: true},
		{worker: true, dynamicPanicOnFault: true},
		{worker: true, nativeDefaultFaultBoundary: true},
		{dynamicPanicOnFault: true, nativeDefaultFaultBoundary: true},
		{worker: true, dynamicPanicOnFault: true, nativeDefaultFaultBoundary: true},
	} {
		capabilities := NewProgramCapabilities(test.worker, test.dynamicPanicOnFault)
		if test.nativeDefaultFaultBoundary {
			capabilities |= NativeDefaultFaultBoundaryProgramCapability()
		}
		if !capabilities.Valid() || capabilities.Worker() != test.worker ||
			capabilities.DynamicPanicOnFault() != test.dynamicPanicOnFault ||
			capabilities.NativeDefaultFaultBoundary() != test.nativeDefaultFaultBoundary ||
			capabilities.PanicOnFault() != (test.dynamicPanicOnFault || test.nativeDefaultFaultBoundary) {
			t.Fatalf("capabilities(%t, %t, %t) = %#x", test.worker, test.dynamicPanicOnFault, test.nativeDefaultFaultBoundary, capabilities)
		}
	}
	if invalid := ProgramCapabilities(1 << 7); invalid.Valid() {
		t.Fatalf("unknown capability %#x was accepted", invalid)
	}
}

func TestExecutionDomainCapabilityIsOrthogonal(t *testing.T) {
	capabilities := ExecutionDomainProgramCapability()
	if !capabilities.Valid() || !capabilities.ExecutionDomain() || capabilities.Worker() || capabilities.PanicOnFault() {
		t.Fatalf("execution-domain capability includes unrelated services: %#x", capabilities)
	}
	if NewProgramCapabilities(true, true).ExecutionDomain() {
		t.Fatal("ordinary worker/fault support enabled execution-domain accounting")
	}
}

func TestGCObservationCapabilityIsOrthogonal(t *testing.T) {
	capabilities := GCObservationProgramCapability()
	if !capabilities.Valid() || !capabilities.GCObservation() || capabilities.ExecutionDomain() || capabilities.Worker() || capabilities.PanicOnFault() {
		t.Fatalf("GC observation includes unrelated services: %#x", capabilities)
	}
	if (ExecutionDomainProgramCapability() | NewProgramCapabilities(true, true)).GCObservation() {
		t.Fatal("unrelated services enabled post-GC observations")
	}
	if ProgramPredicateMask() != ExecutionDomainProgramCapability()|GCObservationProgramCapability() {
		t.Fatal("program predicate mask does not exactly cover its compile-time queries")
	}
}
