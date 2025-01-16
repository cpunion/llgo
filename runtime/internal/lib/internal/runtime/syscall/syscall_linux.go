// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package syscall provides the syscall primitives required for the runtime.
package syscall

/*
#include <sys/epoll.h>
#include <sys/eventfd.h>
#include <errno.h>

static int llgo_errno(void) {
	return errno;
}
*/
import "C"

import (
	"syscall"
	"unsafe"
)

// TODO(https://go.dev/issue/51087): This package is incomplete and currently
// only contains very minimal support for Linux.

func EpollCreate1(flags int32) (fd int32, errno uintptr) {
	fd = int32(C.epoll_create1(C.int(flags)))
	return fd, uintptr(C.llgo_errno())
}

var _zero uintptr

func EpollWait(epfd int32, events []syscall.EpollEvent, maxev, waitms int32) (n int32, errno uintptr) {
	var ev unsafe.Pointer
	if len(events) > 0 {
		ev = unsafe.Pointer(&events[0])
	} else {
		ev = unsafe.Pointer(&_zero)
	}
	n = int32(C.epoll_pwait(C.int(epfd), (*C.struct_epoll_event)(ev), C.int(maxev), C.int(waitms), nil))
	return n, uintptr(C.llgo_errno())
}

func EpollCtl(epfd, op, fd int32, event *syscall.EpollEvent) (errno uintptr) {
	if C.epoll_ctl(C.int(epfd), C.int(op), C.int(fd), (*C.struct_epoll_event)(unsafe.Pointer(event))) < 0 {
		return uintptr(C.llgo_errno())
	}
	return 0
}

func Eventfd(initval, flags int32) (fd int32, errno uintptr) {
	r1 := int32(C.eventfd(C.uint(initval), C.int(flags)))
	return r1, uintptr(C.llgo_errno())
}
