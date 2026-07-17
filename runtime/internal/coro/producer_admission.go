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

// producerAdmission is the common stable-ingress join word used by executor,
// registration, route, operation, and task-control slots. The high bit closes
// admission; the low bits count producer calls which entered before close.
//
// A source still owns its generation, mailbox, and physical quiescence rules.
// This primitive only closes the otherwise-identical pre-lease race: an
// acquire CAS either increments the open word before Seal, or loses to the
// closed bit and cannot enter. Owner storage may be cleared or reused only
// after Quiesced observes the exact closed-with-zero-count word.
//
// Quiesced joins admitted shim calls only. A backend must still prove that no
// callback can reach Acquire in the future before it releases target storage.
const (
	producerAdmissionClosed    = uint32(1 << 31)
	producerAdmissionCountMask = producerAdmissionClosed - 1
)

func producerAdmissionAcquire(word *uint32) bool {
	if word == nil {
		return false
	}
	for {
		state := preemptLoad(word)
		if state&producerAdmissionClosed != 0 || state&producerAdmissionCountMask == producerAdmissionCountMask {
			return false
		}
		if preemptCompareAndSwap(word, state, state+1) {
			return true
		}
	}
}

// producerAdmissionReleaseChecked removes one outstanding admission and
// rejects nil or an already empty aggregate count. The count does not identify
// an individual lease: ownership-carrying transactions must additionally use
// their own linear token/state certificate to reject copied or duplicate
// releases while another producer is still admitted.
func producerAdmissionReleaseChecked(word *uint32) bool {
	if word == nil {
		return false
	}
	for {
		state := preemptLoad(word)
		if state&producerAdmissionCountMask == 0 {
			return false
		}
		if preemptCompareAndSwap(word, state, state-1) {
			return true
		}
	}
}

// producerAdmissionRelease preserves the callback-leaf compatibility surface.
// Source shims which already prove their own linear lease use the checked form
// directly when an invalid aggregate release must fail closed.
func producerAdmissionRelease(word *uint32) {
	_ = producerAdmissionReleaseChecked(word)
}

func producerAdmissionSeal(word *uint32) bool {
	if word == nil {
		return false
	}
	for {
		state := preemptLoad(word)
		if state&producerAdmissionClosed != 0 {
			return true
		}
		if preemptCompareAndSwap(word, state, state|producerAdmissionClosed) {
			return true
		}
	}
}

func producerAdmissionQuiesced(word *uint32) bool {
	return word != nil && preemptLoad(word) == producerAdmissionClosed
}

func producerAdmissionReopen(word *uint32) bool {
	return word != nil && preemptCompareAndSwap(word, producerAdmissionClosed, 0)
}
