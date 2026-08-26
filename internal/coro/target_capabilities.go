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

// TargetCapabilities is compiler-derived from the selected target runtime.
// It deliberately contains only transports which cannot exist on every
// environment. Portable stackless scheduler semantics never appear here: they
// are the sole LLGo execution architecture and cannot be disabled piecemeal.
type TargetCapabilities uint8

const (
	targetCapabilityWorker TargetCapabilities = 1 << iota
	targetCapabilityNativeFleet
	targetCapabilityHostOperation
)

func NewTargetCapabilities(worker, nativeFleet, hostOperation bool) TargetCapabilities {
	var capabilities TargetCapabilities
	if worker {
		capabilities |= targetCapabilityWorker
	}
	if nativeFleet {
		capabilities |= targetCapabilityNativeFleet
	}
	if hostOperation {
		capabilities |= targetCapabilityHostOperation
	}
	return capabilities
}

func (capabilities TargetCapabilities) Worker() bool {
	return capabilities&targetCapabilityWorker != 0
}

func (capabilities TargetCapabilities) NativeFleet() bool {
	return capabilities&targetCapabilityNativeFleet != 0
}

func (capabilities TargetCapabilities) HostOperation() bool {
	return capabilities&targetCapabilityHostOperation != 0
}

func (capabilities TargetCapabilities) Valid() bool {
	const known = targetCapabilityWorker | targetCapabilityNativeFleet | targetCapabilityHostOperation
	return capabilities&^known == 0 && (!capabilities.NativeFleet() || capabilities.Worker())
}

// ProgramCapabilities is the closed-world demand projection of physical
// operations which need an optional target service. TargetCapabilities says
// what the selected environment can provide; ProgramCapabilities says what
// the final emitted program will actually use. Keeping the two lattices
// separate prevents a capable native target from eagerly starting services
// which have no reachable physical transaction.
type ProgramCapabilities uint8

const (
	programCapabilityWorker ProgramCapabilities = 1 << iota
	programCapabilityDynamicPanicOnFault
	programCapabilityNativeDefaultFaultBoundary
)

// NewProgramCapabilities constructs the worker and dynamic-fault service
// demands. The exact static native-fault demand has a separate constructor so
// callers cannot accidentally broaden it to every coroutine in the program.
// dynamicPanicOnFault means runtime/debug.SetPanicOnFault is reachable and may
// change the current G's policy before any later native resume. Exact
// low-address accesses use NativeDefaultFaultBoundaryProgramCapability
// instead, so their landing can remain function-local after call propagation.
func NewProgramCapabilities(worker, dynamicPanicOnFault bool) ProgramCapabilities {
	var capabilities ProgramCapabilities
	if worker {
		capabilities |= programCapabilityWorker
	}
	if dynamicPanicOnFault {
		capabilities |= programCapabilityDynamicPanicOnFault
	}
	return capabilities
}

// NativeDefaultFaultBoundaryProgramCapability is the exact static demand of
// an operation which relies on Go's default low-address signal conversion.
// It is deliberately separate from dynamic SetPanicOnFault policy: reverse
// call propagation can prove the complete set of physical resumes which need
// this landing without adding hooks to unrelated coroutines.
func NativeDefaultFaultBoundaryProgramCapability() ProgramCapabilities {
	return programCapabilityNativeDefaultFaultBoundary
}

func (capabilities ProgramCapabilities) Worker() bool {
	return capabilities&programCapabilityWorker != 0
}

// DynamicPanicOnFault reports whether reachable runtime code can change the
// current G's fault policy. Native stackless resumes then need a landing from
// their first instruction because the policy call may enable recovery and
// fault again before another suspension boundary is crossed.
func (capabilities ProgramCapabilities) DynamicPanicOnFault() bool {
	return capabilities&programCapabilityDynamicPanicOnFault != 0
}

// NativeDefaultFaultBoundary reports exact statically propagated demand for
// Go's default low-address signal conversion.
func (capabilities ProgramCapabilities) NativeDefaultFaultBoundary() bool {
	return capabilities&programCapabilityNativeDefaultFaultBoundary != 0
}

// PanicOnFault reports whether the final reachable program needs any native
// hardware-fault landing support. Bootstrap/runtime service selection consumes
// this union; physical code emission retains the distinction above.
func (capabilities ProgramCapabilities) PanicOnFault() bool {
	return capabilities.DynamicPanicOnFault() || capabilities.NativeDefaultFaultBoundary()
}

func (capabilities ProgramCapabilities) Valid() bool {
	const known = programCapabilityWorker |
		programCapabilityDynamicPanicOnFault |
		programCapabilityNativeDefaultFaultBoundary
	return capabilities&^known == 0
}
