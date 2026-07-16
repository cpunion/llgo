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

const (
	targetIngressCountMask = uint32(1<<30) - 1
	targetIngressOpen      = uint32(1 << 30)
	targetIngressSealed    = uint32(1 << 31)
	targetIngressRetired   = targetIngressOpen | targetIngressSealed
)

// TargetIngress is the allocation-free admission barrier around a complete
// platform callback shim. It deliberately sits outside ExecutorRegistry:
// ExecutorRegistry.Request cannot account for a callback paused before it
// takes that registry's producer lease, or for its Request-to-doorbell tail.
//
// Enter must be the callback's first access to target-owned state. A close
// owner calls Seal, waits for Quiesced, and only then closes its physical
// doorbell and calls Retire. A callback whose increment loses the seal CAS is
// rejected without touching the registry, registration table, or doorbell.
// The word remains in retired static storage so even a callback delayed before
// Enter can safely observe a permanent rejection after strong join.
//
// All methods are lock-free uint32 atomics. Start, Seal, Quiesced, and Retire
// are scheduler-owner-only; Enter and Leave are producer-concurrent.
type TargetIngress struct {
	state uint32
}

func (ingress *TargetIngress) Start() bool {
	return ingress != nil && preemptCompareAndSwap(&ingress.state, 0, targetIngressOpen)
}

func (ingress *TargetIngress) Enter() bool {
	if ingress == nil {
		return false
	}
	for {
		state := preemptLoad(&ingress.state)
		if state&^targetIngressCountMask != targetIngressOpen || state&targetIngressCountMask == targetIngressCountMask {
			return false
		}
		if preemptCompareAndSwap(&ingress.state, state, state+1) {
			return true
		}
	}
}

// Leave returns whether Seal has already linearized and whether this call
// released a valid producer lease. Leave must be the shim's absolute final
// operation: after the decrement a close owner may observe Quiesced, close the
// pipe, and allow its descriptor number to be reused. In particular, a
// producer must perform every required doorbell write before Leave.
func (ingress *TargetIngress) Leave() (sealed, ok bool) {
	if ingress == nil {
		return false, false
	}
	for {
		state := preemptLoad(&ingress.state)
		count := state & targetIngressCountMask
		lifecycle := state &^ targetIngressCountMask
		if count == 0 || (lifecycle != targetIngressOpen && lifecycle != targetIngressSealed) {
			return false, false
		}
		if preemptCompareAndSwap(&ingress.state, state, state-1) {
			return lifecycle == targetIngressSealed, true
		}
	}
}

func (ingress *TargetIngress) Seal() bool {
	if ingress == nil {
		return false
	}
	for {
		state := preemptLoad(&ingress.state)
		if state&^targetIngressCountMask != targetIngressOpen {
			return false
		}
		closed := state&targetIngressCountMask | targetIngressSealed
		if preemptCompareAndSwap(&ingress.state, state, closed) {
			return true
		}
	}
}

func (ingress *TargetIngress) Quiesced() bool {
	return ingress != nil && preemptLoad(&ingress.state) == targetIngressSealed
}

func (ingress *TargetIngress) Retire() bool {
	return ingress != nil && preemptCompareAndSwap(&ingress.state, targetIngressSealed, targetIngressRetired)
}

// CanReleaseResources is true for pristine storage and after the only
// generation is permanently retired. It permits releasing external resources
// protected by the barrier; it never permits freeing, resetting, or reusing
// the TargetIngress word itself. That word is a permanent static tombstone for
// callbacks delayed before Enter. The first production runner is single-start.
func (ingress *TargetIngress) CanReleaseResources() bool {
	if ingress == nil {
		return false
	}
	state := preemptLoad(&ingress.state)
	return state == 0 || state == targetIngressRetired
}
