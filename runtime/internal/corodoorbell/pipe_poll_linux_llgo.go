//go:build llgo && linux && !baremetal

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

import (
	_ "unsafe"

	c "github.com/goplus/llgo/runtime/internal/clite"
)

// Linux defines nfds_t as unsigned long, whose width follows the target word.
//
// The link target is the doorbell-only wrapper, not generic poll(2). It accepts
// exactly one descriptor and a timeout in [0, physicalPollMaxMS]. Even though
// the wait is bounded, it is legal only in the scheduler-owner case of the
// compiler-owned raw host-stack island; schedulerwait records that ownership
// without claiming that poll is noblock.
//
//llgo:coro schedulerwait
//go:linkname nativeCPoll C.__llgo_coro_doorbell_poll_one_v1
func nativeCPoll(fds *nativePollFD, nfds uintptr, timeout c.Int) uint64

func nativePipePoll(fd int32, timeoutMS int32) (int, int16, int32) {
	pollFD := nativePollFD{fd: fd, events: physicalPollIn}
	result, errno := unpackNativeDoorbellResult(nativeCPoll(&pollFD, uintptr(1), c.Int(timeoutMS)))
	return result, pollFD.revents, errno
}
