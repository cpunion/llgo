//go:build !llgo && linux && !baremetal

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

import "syscall"

func nativePipePoll(fd int32, timeoutMS int32) (int, int16, int32) {
	readSet, ok := nativePipeReadSet(fd)
	if !ok {
		return -1, 0, int32(syscall.EINVAL)
	}
	timeout := syscall.NsecToTimeval(int64(timeoutMS) * int64(1e6))
	result, err := syscall.Select(int(fd)+1, &readSet, nil, nil, &timeout)
	if err != nil {
		return -1, 0, int32(err.(syscall.Errno))
	}
	if result == 0 {
		return 0, 0, 0
	}
	return result, physicalPollIn, 0
}
