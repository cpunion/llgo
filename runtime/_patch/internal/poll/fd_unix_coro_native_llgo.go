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
	"sync/atomic"
	"syscall"
	"unsafe"
)

// LLGoTry* status codes returned by the non-parking EmitOutcomePlain fast-try
// helpers below. Keeping uncontended regular-file and ready-socket I/O out of
// FD's full coroutine frame avoids allocating/destroying a poll.FD frame when
// neither fdMutex nor the netpoller needs to park.
const (
	LLGoTryDone uint8 = iota
	LLGoTryDoneSlowUnlock
	LLGoTryFallback
	LLGoTryFallbackSlowUnlock
	LLGoTryWaitLocked
	LLGoTryPartialWrite
	LLGoTryPartialWriteSlowUnlock
)

func (fd *FD) llgoTryLock(mutexBit uint64) bool {
	old := atomic.LoadUint64(&fd.fdmu.state)
	if old&(mutexClosed|mutexBit) != 0 {
		return false
	}
	next := (old | mutexBit) + mutexRef
	if next&mutexRefMask == 0 {
		return false
	}
	return atomic.CompareAndSwapUint64(&fd.fdmu.state, old, next)
}

func (fd *FD) llgoTryUnlock(mutexBit, mutexMask uint64) bool {
	old := atomic.LoadUint64(&fd.fdmu.state)
	if old&mutexBit == 0 || old&mutexRefMask == 0 || old&mutexMask != 0 {
		return false
	}
	next := (old &^ mutexBit) - mutexRef
	if next&(mutexClosed|mutexRefMask) == mutexClosed {
		return false
	}
	return atomic.CompareAndSwapUint64(&fd.fdmu.state, old, next)
}

func (fd *FD) llgoTryIncref() bool {
	old := atomic.LoadUint64(&fd.fdmu.state)
	if old&mutexClosed != 0 {
		return false
	}
	next := old + mutexRef
	if next&mutexRefMask == 0 {
		return false
	}
	return atomic.CompareAndSwapUint64(&fd.fdmu.state, old, next)
}

func (fd *FD) llgoTryDecref() bool {
	old := atomic.LoadUint64(&fd.fdmu.state)
	if old&mutexRefMask == 0 {
		return false
	}
	next := old - mutexRef
	if next&(mutexClosed|mutexRefMask) == mutexClosed {
		return false
	}
	return atomic.CompareAndSwapUint64(&fd.fdmu.state, old, next)
}

func (fd *FD) LLGoSlowReadUnlock()  { fd.readUnlock() }
func (fd *FD) LLGoSlowWriteUnlock() { fd.writeUnlock() }
func (fd *FD) LLGoSlowDecref()      { _ = fd.decref() }

// LLGoTryRead attempts one uncontended read without allocating an FD coroutine
// frame. On EAGAIN for a pollable descriptor it retains the read lock and
// returns LLGoTryWaitLocked so LLGoReadWaitLocked can wait immediately without
// repeating the non-blocking recv syscall.
func (fd *FD) LLGoTryRead(p []byte) (int, error, uint8) {
	if !fd.llgoTryLock(mutexRLock) {
		return 0, nil, LLGoTryFallback
	}
	if len(p) == 0 {
		if fd.llgoTryUnlock(mutexRLock, mutexRMask) {
			return 0, nil, LLGoTryDone
		}
		return 0, nil, LLGoTryDoneSlowUnlock
	}
	ctx := fd.pd.runtimeCtx
	if ctx != 0 {
		if runtime_pollReset(ctx, 'r') != 0 {
			if fd.llgoTryUnlock(mutexRLock, mutexRMask) {
				return 0, nil, LLGoTryFallback
			}
			return 0, nil, LLGoTryFallbackSlowUnlock
		}
	}
	if fd.IsStream && len(p) > maxRW {
		p = p[:maxRW]
	}
	if ctx != 0 {
		n, errno, attempted := runtime_pollReadAttempt(
			ctx,
			fd.Sysfd,
			llgoCoroPollBufferAddress(p),
			len(p),
		)
		if attempted {
			if errno == 0 {
				err := fd.eofError(n, nil)
				if fd.llgoTryUnlock(mutexRLock, mutexRMask) {
					return n, err, LLGoTryDone
				}
				return n, err, LLGoTryDoneSlowUnlock
			}
			if errno == int(syscall.EAGAIN) && fd.pd.pollable() {
				return 0, nil, LLGoTryWaitLocked
			}
			if errno != int(syscall.EINTR) {
				err := syscall.Errno(errno)
				if fd.llgoTryUnlock(mutexRLock, mutexRMask) {
					return 0, err, LLGoTryDone
				}
				return 0, err, LLGoTryDoneSlowUnlock
			}
		}
		if fd.llgoTryUnlock(mutexRLock, mutexRMask) {
			return 0, nil, LLGoTryFallback
		}
		return 0, nil, LLGoTryFallbackSlowUnlock
	}
	n, err := syscall.Read(fd.Sysfd, p)
	if err != nil {
		if errno, ok := err.(syscall.Errno); ok {
			if errno == syscall.EINTR {
				if fd.llgoTryUnlock(mutexRLock, mutexRMask) {
					return 0, nil, LLGoTryFallback
				}
				return 0, nil, LLGoTryFallbackSlowUnlock
			}
			if errno == syscall.EAGAIN && fd.pd.pollable() {
				return 0, nil, LLGoTryWaitLocked
			}
		}
		n = 0
	}
	err = fd.eofError(n, err)
	if fd.llgoTryUnlock(mutexRLock, mutexRMask) {
		return n, err, LLGoTryDone
	}
	return n, err, LLGoTryDoneSlowUnlock
}

// LLGoReadWaitLocked completes a read after LLGoTryRead observed EAGAIN while
// holding fd's read lock and having already prepared/reset the poll descriptor.
func (fd *FD) LLGoReadWaitLocked(p []byte) (int, error) {
	defer fd.readUnlock()
	if fd.IsStream && len(p) > maxRW {
		p = p[:maxRW]
	}
	ctx := fd.pd.runtimeCtx
	if ctx != 0 {
		if res := runtime_pollWait(ctx, 'r'); res != 0 {
			return 0, convertErr(res, fd.isFile)
		}
	} else if err := fd.pd.waitRead(fd.isFile); err != nil {
		return 0, err
	}
	for {
		if ctx != 0 {
			n, errno, attempted := runtime_pollReadAttempt(
				ctx,
				fd.Sysfd,
				llgoCoroPollBufferAddress(p),
				len(p),
			)
			if attempted {
				if errno == 0 {
					return n, fd.eofError(n, nil)
				}
				if errno == int(syscall.EINTR) {
					continue
				}
				if errno == int(syscall.EAGAIN) && fd.pd.pollable() {
					if res := runtime_pollWait(ctx, 'r'); res == 0 {
						continue
					} else {
						return 0, convertErr(res, fd.isFile)
					}
				}
				return 0, syscall.Errno(errno)
			}
		}
		n, err := llgoCoroPollReadFallback(fd, p)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			n = 0
			if err == syscall.EAGAIN && fd.pd.pollable() {
				if ctx != 0 {
					if res := runtime_pollWait(ctx, 'r'); res == 0 {
						continue
					} else {
						err = convertErr(res, fd.isFile)
					}
				} else if err = fd.pd.waitRead(fd.isFile); err == nil {
					continue
				}
			}
		}
		err = fd.eofError(n, err)
		return n, err
	}
}

// llgoTryWriteStream runs one bounded non-blocking write attempt on a pollable
// stream socket while the caller holds fd's write lock.
func (fd *FD) llgoTryWriteStream(ctx uintptr, p []byte) (int, error, uint8) {
	if runtime_pollReset(ctx, 'w') != 0 {
		return 0, nil, LLGoTryFallback
	}
	n, errno, attempted := runtime_pollWriteAttempt(
		ctx,
		fd.Sysfd,
		llgoCoroPollBufferAddress(p),
		len(p),
	)
	if !attempted {
		return 0, nil, LLGoTryFallback
	}
	if errno == 0 && n == len(p) {
		return n, nil, LLGoTryDone
	}
	if errno == int(syscall.EAGAIN) && fd.pd.pollable() && n >= 0 && n <= len(p) {
		return n, nil, LLGoTryWaitLocked
	}
	if errno != 0 && errno != int(syscall.EINTR) && errno != int(syscall.EAGAIN) && n >= 0 && n <= len(p) {
		return n, syscall.Errno(errno), LLGoTryDone
	}
	if n > 0 && n < len(p) && (errno == 0 || errno == int(syscall.EINTR)) {
		return n, nil, LLGoTryPartialWrite
	}
	return 0, nil, LLGoTryFallback
}

// llgoTryWriteFile runs one direct write syscall on a non-polled file
// descriptor while the caller holds fd's write lock.
func (fd *FD) llgoTryWriteFile(p []byte) (int, error, uint8) {
	n, err := syscall.Write(fd.Sysfd, p)
	if err == nil && n == len(p) {
		return n, nil, LLGoTryDone
	}
	if err != nil {
		if errno, ok := err.(syscall.Errno); ok {
			if errno == syscall.EAGAIN && fd.pd.pollable() && n >= 0 && n <= len(p) {
				return n, nil, LLGoTryWaitLocked
			}
			if errno == syscall.EINTR {
				if n > 0 && n < len(p) {
					return n, nil, LLGoTryPartialWrite
				}
				return 0, nil, LLGoTryFallback
			}
		}
		if n >= 0 && n <= len(p) {
			return n, err, LLGoTryDone
		}
	} else if n > 0 && n < len(p) {
		return n, nil, LLGoTryPartialWrite
	}
	return 0, nil, LLGoTryFallback
}

// LLGoTryWrite attempts one uncontended write without allocating an FD
// coroutine frame.
func (fd *FD) LLGoTryWrite(p []byte) (int, error, uint8) {
	if !fd.llgoTryLock(mutexWLock) {
		return 0, nil, LLGoTryFallback
	}
	var n int
	var err error
	var status uint8
	if fd.IsStream && len(p) > maxRW {
		status = LLGoTryFallback
	} else if ctx := fd.pd.runtimeCtx; ctx != 0 {
		n, err, status = fd.llgoTryWriteStream(ctx, p)
	} else {
		n, err, status = fd.llgoTryWriteFile(p)
	}
	if status == LLGoTryWaitLocked {
		return n, nil, LLGoTryWaitLocked
	}
	if !fd.llgoTryUnlock(mutexWLock, mutexWMask) {
		switch status {
		case LLGoTryDone:
			status = LLGoTryDoneSlowUnlock
		case LLGoTryPartialWrite:
			status = LLGoTryPartialWriteSlowUnlock
		default:
			status = LLGoTryFallbackSlowUnlock
		}
	}
	return n, err, status
}

// LLGoWriteWaitLocked completes a write after LLGoTryWrite observed EAGAIN
// while holding fd's write lock and having already written nn bytes.
func (fd *FD) LLGoWriteWaitLocked(p []byte, nn int) (int, error) {
	defer fd.writeUnlock()
	ctx := fd.pd.runtimeCtx
	if ctx != 0 {
		if res := runtime_pollWait(ctx, 'w'); res != 0 {
			return nn, convertErr(res, fd.isFile)
		}
	} else if err := fd.pd.waitWrite(fd.isFile); err != nil {
		return nn, err
	}
	for {
		max := len(p)
		if fd.IsStream && max-nn > maxRW {
			max = nn + maxRW
		}
		var n int
		var err error
		if ctx != 0 {
			var errno int
			var attempted bool
			n, errno, attempted = runtime_pollWriteAttempt(
				ctx,
				fd.Sysfd,
				llgoCoroPollBufferAddress(p[nn:max]),
				max-nn,
			)
			if attempted {
				if errno == int(syscall.EINTR) {
					continue
				}
				if n > 0 {
					if n > max-nn {
						panic("invalid return from write: got " + strconv.Itoa(n) + " from a write of " + strconv.Itoa(max-nn))
					}
					nn += n
				}
				if errno == 0 {
					if nn == len(p) {
						return nn, nil
					}
					if n == 0 {
						return nn, io.ErrUnexpectedEOF
					}
					continue
				}
				if errno == int(syscall.EAGAIN) && fd.pd.pollable() {
					if res := runtime_pollWait(ctx, 'w'); res == 0 {
						continue
					} else {
						return nn, convertErr(res, fd.isFile)
					}
				}
				return nn, syscall.Errno(errno)
			}
		}
		n, err = llgoCoroPollWriteFallback(fd, p[nn:max])
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
			if ctx != 0 {
				if res := runtime_pollWait(ctx, 'w'); res == 0 {
					continue
				} else {
					err = convertErr(res, fd.isFile)
				}
			} else if err = fd.pd.waitWrite(fd.isFile); err == nil {
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

// LLGoTrySeek attempts one uncontended seek without allocating an FD coroutine
// frame.
func (fd *FD) LLGoTrySeek(offset int64, whence int) (int64, error, uint8) {
	if !fd.llgoTryIncref() {
		return 0, nil, LLGoTryFallback
	}
	ret, err := syscall.Seek(fd.Sysfd, offset, whence)
	if fd.llgoTryDecref() {
		return ret, err, LLGoTryDone
	}
	return ret, err, LLGoTryDoneSlowUnlock
}

func runtime_pollReadAttempt(ctx uintptr, fd int, address unsafe.Pointer, size int) (result int, errno int, attempted bool)
func runtime_pollWriteAttempt(ctx uintptr, fd int, address unsafe.Pointer, size int) (result int, errno int, attempted bool)

func llgoCoroPollBufferAddress(p []byte) unsafe.Pointer {
	if len(p) == 0 {
		return nil
	}
	return unsafe.Pointer(&p[0])
}

func llgoCoroPollReadFallback(fd *FD, p []byte) (int, error) {
	return syscall.Read(fd.Sysfd, p)
}

func llgoCoroPollWriteFallback(fd *FD, p []byte) (int, error) {
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
	// A descriptor without poll state has nothing to reset before the syscall.
	// Call runtime_pollReset directly to avoid the separate pollDesc.prepare
	// coroutine frame on every Read.
	ctx := fd.pd.runtimeCtx
	if ctx != 0 {
		if res := runtime_pollReset(ctx, 'r'); res != 0 {
			return 0, convertErr(res, fd.isFile)
		}
	}
	if fd.IsStream && len(p) > maxRW {
		p = p[:maxRW]
	}
	for {
		if ctx != 0 {
			n, errno, attempted := runtime_pollReadAttempt(
				ctx,
				fd.Sysfd,
				llgoCoroPollBufferAddress(p),
				len(p),
			)
			if attempted {
				if errno == 0 {
					return n, fd.eofError(n, nil)
				}
				if errno == int(syscall.EINTR) {
					continue
				}
				if errno == int(syscall.EAGAIN) && fd.pd.pollable() {
					if err := fd.pd.waitRead(fd.isFile); err == nil {
						continue
					} else {
						return 0, err
					}
				}
				return 0, syscall.Errno(errno)
			}
		}
		n, err := llgoCoroPollReadFallback(fd, p)
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
	ctx := fd.pd.runtimeCtx
	if ctx != 0 {
		if res := runtime_pollReset(ctx, 'w'); res != 0 {
			return 0, convertErr(res, fd.isFile)
		}
	}
	var nn int
	for {
		max := len(p)
		if fd.IsStream && max-nn > maxRW {
			max = nn + maxRW
		}
		var n int
		var err error
		if ctx != 0 {
			var errno int
			var attempted bool
			n, errno, attempted = runtime_pollWriteAttempt(
				ctx,
				fd.Sysfd,
				llgoCoroPollBufferAddress(p[nn:max]),
				max-nn,
			)
			if attempted {
				if errno == int(syscall.EINTR) {
					continue
				}
				if n > 0 {
					if n > max-nn {
						panic("invalid return from write: got " + strconv.Itoa(n) + " from a write of " + strconv.Itoa(max-nn))
					}
					nn += n
				}
				if errno == 0 {
					if nn == len(p) {
						return nn, nil
					}
					if n == 0 {
						return nn, io.ErrUnexpectedEOF
					}
					continue
				}
				if errno == int(syscall.EAGAIN) && fd.pd.pollable() {
					if err = fd.pd.waitWrite(fd.isFile); err == nil {
						continue
					}
					return nn, err
				}
				return nn, syscall.Errno(errno)
			}
		}
		n, err = llgoCoroPollWriteFallback(fd, p[nn:max])
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
