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

import "unsafe"

// PollSetCapacity is the maximum physical pollfd span accepted by this native
// profile: one executor doorbell plus 1024 logical fd-direction operations.
// It is deliberately independent of the target-neutral 64-slot page size.
// The owner supplies stable contiguous storage and no Go pointer survives the
// poll call.
const PollSetCapacity = 1025

const (
	PollRead   int16 = 0x0001
	PollWrite  int16 = 0x0004
	PollError  int16 = 0x0008
	PollHangup int16 = 0x0010
	PollBadFD  int16 = 0x0020
)

// PollFD deliberately matches the POSIX struct pollfd layout on supported
// native targets. Revents is output-only and must be cleared before each wait.
type PollFD struct {
	FD      int32
	Events  int16
	Revents int16
}

var (
	_ [8 - unsafe.Sizeof(PollFD{})]byte
	_ [unsafe.Sizeof(PollFD{}) - 8]byte
	_ [4 - unsafe.Offsetof(PollFD{}.Events)]byte
	_ [unsafe.Offsetof(PollFD{}.Events) - 4]byte
	_ [6 - unsafe.Offsetof(PollFD{}.Revents)]byte
	_ [unsafe.Offsetof(PollFD{}.Revents) - 6]byte
)

// WaitPollSet performs one owner-thread poll pass. EINTR is returned to the
// absolute-deadline owner so it can resample its monotonic clock. timeoutMS is
// finite and fault-containment bounded; the scheduler resamples monotonic time
// and re-enters instead of exposing an unbounded poll(-1) foreign edge.
func WaitPollSet(first *PollFD, count uint32, timeoutMS int32) (ready int, errno int32) {
	if first == nil || count == 0 || count > PollSetCapacity ||
		timeoutMS < 0 || timeoutMS > physicalPollMaxMS {
		return -1, -1
	}
	return nativePollSet(first, count, timeoutMS)
}

// PollInterrupted classifies the only retryable physical wait error. The
// caller owns absolute-deadline resampling between interrupted passes.
func PollInterrupted(errno int32) bool {
	return nativeErrInterrupted(errno)
}
