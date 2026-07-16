//go:build llgo && darwin && !baremetal

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

// Darwin defines nfds_t as unsigned int even on 64-bit targets. A uintptr
// declaration would therefore produce an incompatible fixed C ABI prototype.
//
//go:linkname nativeCPoll C.poll
func nativeCPoll(fds *nativePollFD, nfds c.Uint, timeout c.Int) c.Int

func nativePipePoll(fd int32, timeoutMS int32) (int, int16, int32) {
	pollFD := nativePollFD{fd: c.Int(fd), events: physicalPollIn}
	result := nativeCPoll(&pollFD, c.Uint(1), c.Int(timeoutMS))
	if result < 0 {
		return int(result), pollFD.revents, nativeErrno()
	}
	return int(result), pollFD.revents, 0
}
