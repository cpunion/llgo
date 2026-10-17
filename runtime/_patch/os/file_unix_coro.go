// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build (darwin || linux) && go1.26 && !tinygo.wasm && !baremetal

package os

import (
	"internal/poll"
	"internal/syscall/unix"
	"runtime"
	"syscall"
	"unsafe"
)

// File initialization keeps its descriptor in a protected result until every
// potentially blocking native operation has finished. Use the worker protocol
// here too, so shutdown confirms the stat operation before dropping the File.
//
//llgo:coro worker
//go:linkname llgoFileStatInto C.__llgo_runtime_posix_fstat_into_v1
func llgoFileStatInto(int32, unsafe.Pointer) uintptr

func llgoFileStat(fd int, st *syscall.Stat_t) error {
	if errno := llgoFileStatInto(int32(fd), unsafe.Pointer(st)); errno != 0 {
		return syscall.Errno(errno)
	}
	return nil
}

// Allocate the final File before acquisition. Its result guard retains this
// same object through acquisition and poll/finalizer setup, including stops
// between fd delivery and completion of initialization.
func llgoFile(fd int, name string) *File {
	f := &File{&file{
		pfd: poll.FD{
			Sysfd:         fd,
			IsStream:      true,
			ZeroReadIsEOF: true,
		},
		name:        name,
		stdoutOrErr: fd == 1 || fd == 2,
	}}

	return f
}

func newFile(fd int, name string, kind newFileKind, nonBlocking bool) *File {
	return llgoFinishFile(llgoFile(fd, name), kind, nonBlocking)
}

// Preserve the target's standard File initialization after ownership has been
// installed by openFileNolog. Ordinary NewFile/Pipe callers use the same body.
func llgoFinishFile(f *File, kind newFileKind, nonBlocking bool) *File {
	fd := f.pfd.Sysfd

	pollable := kind == kindOpenFile || kind == kindPipe || kind == kindSock || nonBlocking

	// Things like regular files and FIFOs in kqueue on *BSD/Darwin
	// may not work properly (or accurately according to its manual).
	// As a result, we should avoid adding those to the kqueue-based
	// netpoller. Check out #19093, #24164, and #66239 for more contexts.
	//
	// If the fd was passed to us via any path other than OpenFile,
	// we assume those callers know what they were doing, so we won't
	// perform this check and allow it to be added to the kqueue.
	if kind == kindOpenFile {
		switch runtime.GOOS {
		case "darwin", "ios", "dragonfly", "freebsd", "netbsd", "openbsd":
			var st syscall.Stat_t
			err := ignoringEINTR(func() error {
				return llgoFileStat(fd, &st)
			})
			typ := st.Mode & syscall.S_IFMT
			// Don't try to use kqueue with regular files on *BSDs.
			// On FreeBSD a regular file is always
			// reported as ready for writing.
			// On Dragonfly, NetBSD and OpenBSD the fd is signaled
			// only once as ready (both read and write).
			// Issue 19093.
			// Also don't add directories to the netpoller.
			if err == nil && (typ == syscall.S_IFREG || typ == syscall.S_IFDIR) {
				pollable = false
			}

			// In addition to the behavior described above for regular files,
			// on Darwin, kqueue does not work properly with fifos:
			// closing the last writer does not cause a kqueue event
			// for any readers. See issue #24164.
			if (runtime.GOOS == "darwin" || runtime.GOOS == "ios") && typ == syscall.S_IFIFO {
				pollable = false
			}
		}
	}

	clearNonBlock := false
	if pollable {
		// The descriptor is already in non-blocking mode.
		// We only set f.nonblock if we put the file into
		// non-blocking mode.
		if nonBlocking {
			// See the comments on net_newUnixFile.
			if kind == kindSock {
				f.nonblock = true // tell Fd to return blocking descriptor
			}
		} else if err := syscall.SetNonblock(fd, true); err == nil {
			f.nonblock = true
			clearNonBlock = true
		} else {
			pollable = false
		}
	}

	// An error here indicates a failure to register
	// with the netpoll system. That can happen for
	// a file descriptor that is not supported by
	// epoll/kqueue; for example, disk files on
	// Linux systems. We assume that any real error
	// will show up in later I/O.
	// We do restore the blocking behavior if it was set by us.
	if pollErr := f.pfd.Init("file", pollable); pollErr != nil && clearNonBlock {
		if err := syscall.SetNonblock(fd, false); err == nil {
			f.nonblock = false
		}
	}

	runtime.SetFinalizer(f.file, (*file).close)
	return f
}

func openFileNolog(name string, flag int, perm FileMode) (*File, error) {
	out := llgoBorrowOpenFile(llgoInitializeOpenFile, llgoDropOpenFile)
	setSticky := false
	if !supportsCreateWithStickyBit && flag&O_CREATE != 0 && perm&ModeSticky != 0 {
		if _, err := Stat(name); IsNotExist(err) {
			setSticky = true
		}
	}
	f := llgoFile(-1, name)
	out.R0 = f
	var e error
	ignoringEINTR(func() error {
		var fd int
		var system poll.SysFile
		fd, system, e = open(name, flag|syscall.O_CLOEXEC, syscallMode(perm))
		if e == nil {
			f.pfd.Sysfd = fd
			f.pfd.SysFile = system
		}
		return e
	})
	if e != nil {
		out.R0 = nil
		out.R1 = &PathError{Op: "open", Path: name, Err: e}
		return out.R0, out.R1
	}
	if setSticky {
		setStickyBit(name)
	}
	if !supportsCloseOnExec {
		syscall.CloseOnExec(f.pfd.Sysfd)
	}
	f.stdoutOrErr = f.pfd.Sysfd == 1 || f.pfd.Sysfd == 2
	llgoFinishFile(f, kindOpenFile, unix.HasNonblockFlag(flag))
	return out.R0, out.R1
}

//go:linkname llgoBorrowOpenDir llgo.coroBorrowResult
func llgoBorrowOpenDir(func(*llgoOpenFileResult), func(*llgoOpenFileResult, unsafe.Pointer, string)) *llgoOpenFileResult

func llgoDropOpenDir(out *llgoOpenFileResult, _ unsafe.Pointer, _ string) {
	llgoDropFile(out)
}

// The standard directory helper is the other consumer of os.open. Retain the
// same File guard while its kindNoPoll initialization accepts the descriptor.
func openDirNolog(name string) (*File, error) {
	out := llgoBorrowOpenDir(llgoInitializeOpenFile, llgoDropOpenDir)
	f := llgoFile(-1, name)
	out.R0 = f
	var e error
	ignoringEINTR(func() error {
		var fd int
		var system poll.SysFile
		fd, system, e = open(name, O_RDONLY|syscall.O_CLOEXEC|syscall.O_DIRECTORY, 0)
		if e == nil {
			f.pfd.Sysfd = fd
			f.pfd.SysFile = system
		}
		return e
	})
	if e != nil {
		out.R0 = nil
		out.R1 = &PathError{Op: "open", Path: name, Err: e}
		return out.R0, out.R1
	}
	if !supportsCloseOnExec {
		syscall.CloseOnExec(f.pfd.Sysfd)
	}
	f.stdoutOrErr = f.pfd.Sysfd == 1 || f.pfd.Sysfd == 2
	llgoFinishFile(f, kindNoPoll, false)
	return out.R0, out.R1
}
