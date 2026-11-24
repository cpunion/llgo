//go:build llgo && llgo_coro && llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux) && !baremetal && !tinygo.wasm && !coro_runtime_adapter_test

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

package net

import (
	"internal/poll"
	"runtime"
)

func (fd *netFD) Read(p []byte) (n int, err error) {
	var status uint8
	n, err, status = fd.pfd.LLGoTryRead(p)
	switch status {
	case poll.LLGoTryDone:
	case poll.LLGoTryDoneSlowUnlock:
		fd.pfd.LLGoSlowReadUnlock()
	case poll.LLGoTryWaitLocked:
		n, err = fd.pfd.LLGoReadWaitLocked(p)
	case poll.LLGoTryFallbackSlowUnlock:
		fd.pfd.LLGoSlowReadUnlock()
		n, err = fd.pfd.Read(p)
	default:
		n, err = fd.pfd.Read(p)
	}
	runtime.KeepAlive(fd)
	if err != nil {
		return n, wrapSyscallError(readSyscallName, err)
	}
	return n, nil
}

func (fd *netFD) Write(p []byte) (nn int, err error) {
	var status uint8
	nn, err, status = fd.pfd.LLGoTryWrite(p)
	switch status {
	case poll.LLGoTryDone:
	case poll.LLGoTryDoneSlowUnlock:
		fd.pfd.LLGoSlowWriteUnlock()
	case poll.LLGoTryWaitLocked:
		nn, err = fd.pfd.LLGoWriteWaitLocked(p, nn)
	case poll.LLGoTryPartialWrite:
		var m int
		m, err = fd.pfd.Write(p[nn:])
		nn += m
	case poll.LLGoTryPartialWriteSlowUnlock:
		fd.pfd.LLGoSlowWriteUnlock()
		var m int
		m, err = fd.pfd.Write(p[nn:])
		nn += m
	case poll.LLGoTryFallbackSlowUnlock:
		fd.pfd.LLGoSlowWriteUnlock()
		nn, err = fd.pfd.Write(p)
	default:
		nn, err = fd.pfd.Write(p)
	}
	runtime.KeepAlive(fd)
	if err != nil {
		return nn, wrapSyscallError(writeSyscallName, err)
	}
	return nn, nil
}
