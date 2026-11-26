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
	"io"
	"runtime"
	"syscall"
)

func (c *conn) Read(b []byte) (int, error) {
	if c == nil || c.fd == nil {
		return 0, syscall.EINVAL
	}
	n, err, status := c.fd.pfd.LLGoTryRead(b)
	if status == poll.LLGoTryDone && err == nil {
		runtime.KeepAlive(c.fd)
		return n, nil
	}
	if status == poll.LLGoTryWaitLockedNoDeadline {
		return c.llgoReadWaitFast(b)
	}
	return c.llgoReadSlow(b, n, err, status)
}

// llgoReadWaitFast handles the common zero-deadline EAGAIN -> poll-wait -> read
// path in a minimal coroutine frame that stays inside the lock-free TLS frame
// cache instead of spilling all cold fallback awaits into a >1 KiB frame.
func (c *conn) llgoReadWaitFast(b []byte) (int, error) {
	fd := c.fd
	ctx, pfd, interest := fd.pfd.LLGoReadWaitFastSnapshot()
	waitResult := poll.LLGoPollWaitOne(ctx, pfd, interest, 0)
	n, err, pst, pollSt := fd.pfd.LLGoReadWaitFinishAndRead(ctx, b, waitResult)
	if pst == poll.LLGoTryDone && err == nil && pollSt == 0 {
		runtime.KeepAlive(fd)
		return n, nil
	}
	return c.llgoReadWaitFallback(b, n, err, pst, pollSt)
}

func (c *conn) llgoReadWaitFallback(b []byte, n int, err error, pst uint8, pollSt int) (int, error) {
	fd := c.fd
	switch pst {
	case poll.LLGoTryDone:
		if pollSt != 0 {
			err = fd.pfd.LLGoPollError(pollSt)
		}
	case poll.LLGoTryDoneSlowUnlock:
		fd.pfd.LLGoSlowReadUnlock()
		if pollSt != 0 {
			err = fd.pfd.LLGoPollError(pollSt)
		}
	case poll.LLGoTryWaitLocked, poll.LLGoTryWaitLockedNoDeadline:
		return c.llgoReadSlow(b, 0, nil, pst)
	default:
		n, err = fd.pfd.LLGoReadWaitLocked(b)
	}
	runtime.KeepAlive(fd)
	if err != nil {
		err = wrapSyscallError(readSyscallName, err)
		if err != io.EOF {
			err = &OpError{Op: "read", Net: fd.net, Source: fd.laddr, Addr: fd.raddr, Err: err}
		}
		return n, err
	}
	return n, nil
}

func (c *conn) llgoReadSlow(b []byte, n int, err error, status uint8) (int, error) {
	fd := c.fd
	switch status {
	case poll.LLGoTryDone:
	case poll.LLGoTryDoneSlowUnlock:
		fd.pfd.LLGoSlowReadUnlock()
	case poll.LLGoTryWaitLocked, poll.LLGoTryWaitLockedNoDeadline:
		fastNoDeadline := status == poll.LLGoTryWaitLockedNoDeadline
		for {
			var ctx uintptr
			var pfd int32
			var interest uint32
			var deadline int64
			var pst uint8
			var pollSt int
			if fastNoDeadline {
				fastNoDeadline = false
				ctx, pfd, interest = fd.pfd.LLGoReadWaitFastSnapshot()
				pst = poll.LLGoTryWaitLocked
			} else {
				ctx, pfd, interest, deadline, pst, pollSt = fd.pfd.LLGoReadWaitPrepare()
			}
			if pst == poll.LLGoTryWaitLocked {
				waitResult := poll.LLGoPollWaitOne(ctx, pfd, interest, deadline)
				n, err, pst, pollSt = fd.pfd.LLGoReadWaitFinishAndRead(ctx, b, waitResult)
				if pst == poll.LLGoTryWaitLocked {
					continue
				}
				if pst == poll.LLGoTryDoneSlowUnlock {
					fd.pfd.LLGoSlowReadUnlock()
				} else if pst == poll.LLGoTryFallback {
					n, err = fd.pfd.LLGoReadWaitLocked(b)
					break
				}
				if pollSt != 0 {
					err = fd.pfd.LLGoPollError(pollSt)
				}
				break
			}
			if pst == poll.LLGoTryDoneSlowUnlock {
				fd.pfd.LLGoSlowReadUnlock()
			}
			if pst == poll.LLGoTryDone || pst == poll.LLGoTryDoneSlowUnlock {
				n, err = 0, fd.pfd.LLGoPollError(pollSt)
			} else {
				n, err = fd.pfd.LLGoReadWaitLocked(b)
			}
			break
		}
	case poll.LLGoTryFallbackSlowUnlock:
		fd.pfd.LLGoSlowReadUnlock()
		n, err = fd.pfd.Read(b)
	default:
		n, err = fd.pfd.Read(b)
	}
	runtime.KeepAlive(fd)
	if err != nil {
		err = wrapSyscallError(readSyscallName, err)
		if err != io.EOF {
			err = &OpError{Op: "read", Net: fd.net, Source: fd.laddr, Addr: fd.raddr, Err: err}
		}
		return n, err
	}
	return n, nil
}

func (c *conn) Write(b []byte) (int, error) {
	if c == nil || c.fd == nil {
		return 0, syscall.EINVAL
	}
	nn, err, status := c.fd.pfd.LLGoTryWrite(b)
	if status == poll.LLGoTryDone && err == nil {
		runtime.KeepAlive(c.fd)
		return nn, nil
	}
	return c.llgoWriteSlow(b, nn, err, status)
}

func (c *conn) llgoWriteSlow(b []byte, nn int, err error, status uint8) (int, error) {
	fd := c.fd
	switch status {
	case poll.LLGoTryDone:
	case poll.LLGoTryDoneSlowUnlock:
		fd.pfd.LLGoSlowWriteUnlock()
	case poll.LLGoTryWaitLocked:
		nn, err = fd.pfd.LLGoWriteWaitLocked(b, nn)
	case poll.LLGoTryPartialWrite:
		var m int
		m, err = fd.pfd.Write(b[nn:])
		nn += m
	case poll.LLGoTryPartialWriteSlowUnlock:
		fd.pfd.LLGoSlowWriteUnlock()
		var m int
		m, err = fd.pfd.Write(b[nn:])
		nn += m
	case poll.LLGoTryFallbackSlowUnlock:
		fd.pfd.LLGoSlowWriteUnlock()
		nn, err = fd.pfd.Write(b)
	default:
		nn, err = fd.pfd.Write(b)
	}
	runtime.KeepAlive(fd)
	if err != nil {
		err = wrapSyscallError(writeSyscallName, err)
		return nn, &OpError{Op: "write", Net: fd.net, Source: fd.laddr, Addr: fd.raddr, Err: err}
	}
	return nn, nil
}

func (fd *netFD) Read(p []byte) (n int, err error) {
	var status uint8
	n, err, status = fd.pfd.LLGoTryRead(p)
	if status == poll.LLGoTryDone && err == nil {
		runtime.KeepAlive(fd)
		return n, nil
	}
	return fd.llgoReadSlow(p, n, err, status)
}

func (fd *netFD) llgoReadSlow(p []byte, n int, err error, status uint8) (int, error) {
	switch status {
	case poll.LLGoTryDone:
	case poll.LLGoTryDoneSlowUnlock:
		fd.pfd.LLGoSlowReadUnlock()
	case poll.LLGoTryWaitLocked, poll.LLGoTryWaitLockedNoDeadline:
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
	if status == poll.LLGoTryDone && err == nil {
		runtime.KeepAlive(fd)
		return nn, nil
	}
	return fd.llgoWriteSlow(p, nn, err, status)
}

func (fd *netFD) llgoWriteSlow(p []byte, nn int, err error, status uint8) (int, error) {
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
