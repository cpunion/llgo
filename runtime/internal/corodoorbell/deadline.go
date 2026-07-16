//go:build (darwin || linux) && !baremetal

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

package corodoorbell

const deadlineNanosPerMilli int64 = 1_000_000

// deadlinePollTimeout converts one absolute monotonic deadline into a bounded
// poll timeout. Rounding is upward, so an ordinary timeout can never be
// mistaken for proof that a sub-millisecond deadline has already elapsed.
// The caller samples the clock again after every timeout.
func deadlinePollTimeout(now, deadline int64) (timeoutMS int32, reached, ok bool) {
	if now < 0 || deadline < 0 {
		return 0, false, false
	}
	if deadline <= now {
		return 0, true, true
	}
	delta := deadline - now
	milliseconds := delta / deadlineNanosPerMilli
	if delta%deadlineNanosPerMilli != 0 {
		milliseconds++
	}
	if milliseconds > int64(physicalPollMaxMS) {
		milliseconds = int64(physicalPollMaxMS)
	}
	return int32(milliseconds), false, true
}

// waitDeadlinePass performs one retained deadline-wait pass for a clock sample
// supplied by the owner. A timeout and EINTR both return a successful non-wake;
// the caller must take a fresh monotonic sample before the next pass.
func (pipe *Pipe) waitDeadlinePass(now, deadline int64) (woke, reached, ok bool) {
	timeoutMS, due, timeoutOK := deadlinePollTimeout(now, deadline)
	if !timeoutOK {
		return false, false, false
	}
	if due {
		return false, true, true
	}
	woke, waitOK := pipe.waitBoundedInterruptible(timeoutMS)
	if !waitOK {
		return false, false, false
	}
	return woke, false, true
}
