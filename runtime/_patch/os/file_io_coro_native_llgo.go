// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build llgo && llgo_coro && llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux) && go1.26 && !baremetal && !tinygo.wasm && !coro_runtime_adapter_test

package os

import (
	"internal/poll"
	"io"
	"runtime"
	"syscall"
)

// Read reads up to len(b) bytes from the File and stores them in b.
// It returns the number of bytes read and any error encountered.
// At end of file, Read returns 0, io.EOF.
func (f *File) Read(b []byte) (n int, err error) {
	if err := f.checkValid("read"); err != nil {
		return 0, err
	}
	var status uint8
	n, err, status = f.pfd.LLGoTryRead(b)
	if status == poll.LLGoTryDone && err == nil {
		runtime.KeepAlive(f)
		return n, nil
	}
	return f.llgoReadSlow(b, n, err, status)
}

func (f *File) llgoReadSlow(b []byte, n int, err error, status uint8) (int, error) {
	switch status {
	case poll.LLGoTryDone:
	case poll.LLGoTryDoneSlowUnlock:
		f.pfd.LLGoSlowReadUnlock()
	case poll.LLGoTryWaitLocked, poll.LLGoTryWaitLockedNoDeadline:
		n, err = f.pfd.LLGoReadWaitLocked(b)
	case poll.LLGoTryFallbackSlowUnlock:
		f.pfd.LLGoSlowReadUnlock()
		n, err = f.pfd.Read(b)
	default:
		n, err = f.pfd.Read(b)
	}
	runtime.KeepAlive(f)
	if err != nil {
		err = f.wrapErr("read", err)
	}
	return n, err
}

// Write writes len(b) bytes from b to the File.
// It returns the number of bytes written and an error, if any.
// Write returns a non-nil error when n != len(b).
func (f *File) Write(b []byte) (n int, err error) {
	if err := f.checkValid("write"); err != nil {
		return 0, err
	}
	var e error
	var status uint8
	n, e, status = f.pfd.LLGoTryWrite(b)
	if status == poll.LLGoTryDone && e == nil && n == len(b) {
		runtime.KeepAlive(f)
		return n, nil
	}
	return f.llgoWriteSlow(b, n, e, status)
}

func (f *File) llgoWriteSlow(b []byte, n int, e error, status uint8) (int, error) {
	switch status {
	case poll.LLGoTryDone:
	case poll.LLGoTryDoneSlowUnlock:
		f.pfd.LLGoSlowWriteUnlock()
	case poll.LLGoTryWaitLocked:
		n, e = f.pfd.LLGoWriteWaitLocked(b, n)
	case poll.LLGoTryPartialWrite:
		var m int
		m, e = f.pfd.Write(b[n:])
		n += m
	case poll.LLGoTryPartialWriteSlowUnlock:
		f.pfd.LLGoSlowWriteUnlock()
		var m int
		m, e = f.pfd.Write(b[n:])
		n += m
	case poll.LLGoTryFallbackSlowUnlock:
		f.pfd.LLGoSlowWriteUnlock()
		n, e = f.pfd.Write(b)
	default:
		n, e = f.pfd.Write(b)
	}
	runtime.KeepAlive(f)
	var err error
	if n < 0 {
		n = 0
	}
	if n != len(b) {
		err = io.ErrShortWrite
	}
	if e != nil {
		epipecheck(f, e)
		err = f.wrapErr("write", e)
	}
	return n, err
}

// Seek sets the offset for the next Read or Write on file, according to
// whence, and returns the new offset.
func (f *File) Seek(offset int64, whence int) (int64, error) {
	if err := f.checkValid("seek"); err != nil {
		return 0, err
	}
	if f.dirinfo.Load() != nil {
		return f.llgoSeekSlow(offset, whence, 0, nil, poll.LLGoTryFallback, true)
	}
	ret, err, status := f.pfd.LLGoTrySeek(offset, whence)
	if status == poll.LLGoTryDone && err == nil && (ret == 0 || f.dirinfo.Load() == nil) {
		runtime.KeepAlive(f)
		return ret, nil
	}
	return f.llgoSeekSlow(offset, whence, ret, err, status, false)
}

func (f *File) llgoSeekSlow(offset int64, whence int, ret int64, err error, status uint8, clearDir bool) (int64, error) {
	if clearDir {
		if info := f.dirinfo.Swap(nil); info != nil {
			// Discard the directory buffer so the next read starts at the new offset.
			info.close()
		}
	}
	switch status {
	case poll.LLGoTryDone:
	case poll.LLGoTryDoneSlowUnlock:
		f.pfd.LLGoSlowDecref()
	default:
		ret, err = f.pfd.Seek(offset, whence)
	}
	runtime.KeepAlive(f)
	if err == nil && f.dirinfo.Load() != nil && ret != 0 {
		err = syscall.EISDIR
	}
	if err != nil {
		return 0, f.wrapErr("seek", err)
	}
	return ret, nil
}
