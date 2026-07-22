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

// RuntimeProfile is the only user-visible coroutine architecture selection.
// Stackless is a complete language/runtime contract: entry resolution,
// physical LLVM coroutines, synchronous-style await, explicit outcomes,
// channels/select, spawn, preemption, and the program executor always move as
// one unit. Target-dependent transports are described separately by
// TargetCapabilities; they are not independently selectable language stages.
type RuntimeProfile uint8

const (
	RuntimeProfileNone RuntimeProfile = iota
	RuntimeProfileStackless
)

func (profile RuntimeProfile) Active() bool {
	return profile == RuntimeProfileStackless
}

func (profile RuntimeProfile) Valid() bool {
	return profile == RuntimeProfileNone || profile == RuntimeProfileStackless
}

// TargetCapabilities is compiler-derived from the selected target runtime.
// It deliberately contains only transports which cannot exist on every
// environment. Portable scheduler semantics never appear here: they are part
// of RuntimeProfileStackless and therefore cannot be disabled piecemeal.
type TargetCapabilities uint8

const (
	targetCapabilityWorker TargetCapabilities = 1 << iota
	targetCapabilityNativeFleet
)

func NewTargetCapabilities(worker, nativeFleet bool) TargetCapabilities {
	var capabilities TargetCapabilities
	if worker {
		capabilities |= targetCapabilityWorker
	}
	if nativeFleet {
		capabilities |= targetCapabilityNativeFleet
	}
	return capabilities
}

func (capabilities TargetCapabilities) Worker() bool {
	return capabilities&targetCapabilityWorker != 0
}

func (capabilities TargetCapabilities) NativeFleet() bool {
	return capabilities&targetCapabilityNativeFleet != 0
}

func (capabilities TargetCapabilities) Valid() bool {
	const known = targetCapabilityWorker | targetCapabilityNativeFleet
	return capabilities&^known == 0 && (!capabilities.NativeFleet() || capabilities.Worker())
}
