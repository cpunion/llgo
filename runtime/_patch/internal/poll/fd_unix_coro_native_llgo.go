//go:build llgo && llgo_coro && llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux) && !baremetal && !coro_runtime_adapter_test

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

package poll

import (
	"internal/strconv"
	"io"
	"syscall"
	"unsafe"
)

func runtime_pollReadAttempt(ctx uintptr, fd int, address unsafe.Pointer, size int) (result int, errno int, attempted bool)
func runtime_pollWriteAttempt(ctx uintptr, fd int, address unsafe.Pointer, size int) (result int, errno int, attempted bool)

func llgoCoroPollBufferAddress(p []byte) unsafe.Pointer {
	if len(p) == 0 {
		return nil
	}
	return unsafe.Pointer(&p[0])
}

// llgoCoroPollReadOnce asks the runtime for one bounded stream-socket
// recv(MSG_DONTWAIT) attempt. EINTR is handled by FD.Read's outer loop. A
// non-stream socket or non-pollable descriptor keeps the standard syscall path,
// which the coroutine worker can lower without changing the synchronous
// internal/poll API. The C leaf itself clamps a large stream buffer to one
// 64 KiB episode; Read may return short and Write's standard loop continues.
func llgoCoroPollReadOnce(fd *FD, p []byte) (int, error) {
	if fd.pd.runtimeCtx != 0 {
		result, errno, attempted := runtime_pollReadAttempt(
			fd.pd.runtimeCtx,
			fd.Sysfd,
			llgoCoroPollBufferAddress(p),
			len(p),
		)
		if attempted {
			if errno != 0 {
				return result, syscall.Errno(errno)
			}
			return result, nil
		}
	}
	return syscall.Read(fd.Sysfd, p)
}

func llgoCoroPollWriteOnce(fd *FD, p []byte) (int, error) {
	if fd.pd.runtimeCtx != 0 {
		result, errno, attempted := runtime_pollWriteAttempt(
			fd.pd.runtimeCtx,
			fd.Sysfd,
			llgoCoroPollBufferAddress(p),
			len(p),
		)
		if attempted {
			if errno != 0 {
				return result, syscall.Errno(errno)
			}
			return result, nil
		}
	}
	return syscall.Write(fd.Sysfd, p)
}

// Read implements io.Reader while preserving the standard blocking call
// style. Only the bounded non-blocking syscall attempt stays on the executor;
// readiness waiting uses the coroutine poll source and every fallback remains
// eligible for the syscall worker.
func (fd *FD) Read(p []byte) (int, error) {
	if err := fd.readLock(); err != nil {
		return 0, err
	}
	defer fd.readUnlock()
	if len(p) == 0 {
		return 0, nil
	}
	if err := fd.pd.prepareRead(fd.isFile); err != nil {
		return 0, err
	}
	if fd.IsStream && len(p) > maxRW {
		p = p[:maxRW]
	}
	for {
		n, err := llgoCoroPollReadOnce(fd, p)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			n = 0
			if err == syscall.EAGAIN && fd.pd.pollable() {
				if err = fd.pd.waitRead(fd.isFile); err == nil {
					continue
				}
			}
		}
		err = fd.eofError(n, err)
		return n, err
	}
}

// Write implements io.Writer with the same partial-write and readiness rules
// as the Go standard library implementation.
func (fd *FD) Write(p []byte) (int, error) {
	if err := fd.writeLock(); err != nil {
		return 0, err
	}
	defer fd.writeUnlock()
	if err := fd.pd.prepareWrite(fd.isFile); err != nil {
		return 0, err
	}
	var nn int
	for {
		max := len(p)
		if fd.IsStream && max-nn > maxRW {
			max = nn + maxRW
		}
		n, err := llgoCoroPollWriteOnce(fd, p[nn:max])
		if err == syscall.EINTR {
			continue
		}
		if n > 0 {
			if n > max-nn {
				panic("invalid return from write: got " + strconv.Itoa(n) + " from a write of " + strconv.Itoa(max-nn))
			}
			nn += n
		}
		if nn == len(p) {
			return nn, err
		}
		if err == syscall.EAGAIN && fd.pd.pollable() {
			if err = fd.pd.waitWrite(fd.isFile); err == nil {
				continue
			}
		}
		if err != nil {
			return nn, err
		}
		if n == 0 {
			return nn, io.ErrUnexpectedEOF
		}
	}
}
