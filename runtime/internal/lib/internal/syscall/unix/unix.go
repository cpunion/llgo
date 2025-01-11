/*
 * Copyright (c) 2024 The GoPlus Authors (goplus.org). All rights reserved.
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

package unix

/*
#include <unistd.h>
#include <errno.h>
#include <sys/socket.h>

static int llgo_errno(void) {
	return errno;
}
*/
import "C"
import (
	"syscall"
	"unsafe"
	_ "unsafe"

	psyscall "github.com/goplus/llgo/runtime/internal/lib/syscall"
)

var _zero uintptr

func faccessat(dirfd int, path string, mode uint32, flags int) error {
	p, err := psyscall.CharPtrFromString(path)
	if err != nil {
		return err
	}
	if ret := C.faccessat(C.int(dirfd), (*C.char)(p), C.int(mode), C.int(flags)); ret != 0 {
		return syscall.Errno(ret)
	}
	return nil
}

func recvfrom(fd int, p []byte, flags int, rsa *syscall.RawSockaddrAny, addrlen *uint32) (n int, err error) {
	var _p0 unsafe.Pointer
	if len(p) > 0 {
		_p0 = unsafe.Pointer(&p[0])
	} else {
		_p0 = unsafe.Pointer(&_zero)
	}
	ret := C.recvfrom(C.int(fd), _p0, C.size_t(len(p)), C.int(flags), (*C.struct_sockaddr)(unsafe.Pointer(rsa)), (*C.socklen_t)(unsafe.Pointer(addrlen)))
	if ret < 0 {
		return 0, syscall.Errno(C.llgo_errno())
	}
	n = int(ret)
	return
}

func recvmsg(fd int, msg *syscall.Msghdr, flags int) (n int, err error) {
	ret := C.recvmsg(C.int(fd), (*C.struct_msghdr)(unsafe.Pointer(msg)), C.int(flags))
	if ret < 0 {
		return 0, syscall.Errno(C.llgo_errno())
	}
	n = int(ret)
	return
}

func sendmsg(fd int, msg *syscall.Msghdr, flags int) (n int, err error) {
	ret := C.sendmsg(C.int(fd), (*C.struct_msghdr)(unsafe.Pointer(msg)), C.int(flags))
	if ret < 0 {
		return 0, syscall.Errno(C.llgo_errno())
	}
	n = int(ret)
	return
}

func SendtoInet4(fd int, p []byte, flags int, to *syscall.SockaddrInet4) (err error)

func SendtoInet6(fd int, p []byte, flags int, to *syscall.SockaddrInet6) (err error)

func SendmsgNInet4(fd int, p, oob []byte, to *syscall.SockaddrInet4, flags int) (n int, err error)

func SendmsgNInet6(fd int, p, oob []byte, to *syscall.SockaddrInet6, flags int) (n int, err error)

func RecvfromInet4(fd int, p []byte, flags int, from *syscall.SockaddrInet4) (n int, err error) {
	var rsa syscall.RawSockaddrAny
	var socklen uint32 = syscall.SizeofSockaddrAny
	if n, err = recvfrom(fd, p, flags, &rsa, &socklen); err != nil {
		return
	}
	pp := (*syscall.RawSockaddrInet4)(unsafe.Pointer(&rsa))
	port := (*[2]byte)(unsafe.Pointer(&pp.Port))
	from.Port = int(port[0])<<8 + int(port[1])
	from.Addr = pp.Addr
	return
}

func RecvfromInet6(fd int, p []byte, flags int, from *syscall.SockaddrInet6) (n int, err error) {
	var rsa syscall.RawSockaddrAny
	var socklen uint32 = syscall.SizeofSockaddrAny
	if n, err = recvfrom(fd, p, flags, &rsa, &socklen); err != nil {
		return
	}
	pp := (*syscall.RawSockaddrInet6)(unsafe.Pointer(&rsa))
	port := (*[2]byte)(unsafe.Pointer(&pp.Port))
	from.Port = int(port[0])<<8 + int(port[1])
	from.ZoneId = pp.Scope_id
	from.Addr = pp.Addr
	return
}

func RecvmsgInet4(fd int, p, oob []byte, flags int, from *syscall.SockaddrInet4) (n, oobn int, recvflags int, err error) {
	var rsa syscall.RawSockaddrAny
	n, oobn, recvflags, err = recvmsgRaw(fd, p, oob, flags, &rsa)
	if err != nil {
		return
	}
	pp := (*syscall.RawSockaddrInet4)(unsafe.Pointer(&rsa))
	port := (*[2]byte)(unsafe.Pointer(&pp.Port))
	from.Port = int(port[0])<<8 + int(port[1])
	from.Addr = pp.Addr
	return
}

func RecvmsgInet6(fd int, p, oob []byte, flags int, from *syscall.SockaddrInet6) (n, oobn int, recvflags int, err error) {
	var rsa syscall.RawSockaddrAny
	n, oobn, recvflags, err = recvmsgRaw(fd, p, oob, flags, &rsa)
	if err != nil {
		return
	}
	pp := (*syscall.RawSockaddrInet6)(unsafe.Pointer(&rsa))
	port := (*[2]byte)(unsafe.Pointer(&pp.Port))
	from.Port = int(port[0])<<8 + int(port[1])
	from.ZoneId = pp.Scope_id
	from.Addr = pp.Addr
	return
}

func recvmsgRaw(fd int, p, oob []byte, flags int, rsa *syscall.RawSockaddrAny) (n, oobn int, recvflags int, err error) {
	var msg syscall.Msghdr
	msg.Name = (*byte)(unsafe.Pointer(rsa))
	msg.Namelen = uint32(syscall.SizeofSockaddrAny)
	var iov syscall.Iovec
	if len(p) > 0 {
		iov.Base = &p[0]
		iov.SetLen(len(p))
	}
	var dummy byte
	if len(oob) > 0 {
		if len(p) == 0 {
			var sockType int
			sockType, err = syscall.GetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_TYPE)
			if err != nil {
				return
			}
			// receive at least one normal byte
			if sockType != syscall.SOCK_DGRAM {
				iov.Base = &dummy
				iov.SetLen(1)
			}
		}
		msg.Control = &oob[0]
		msg.SetControllen(len(oob))
	}
	msg.Iov = &iov
	msg.Iovlen = 1
	if n, err = recvmsg(fd, &msg, flags); err != nil {
		return
	}
	oobn = int(msg.Controllen)
	recvflags = int(msg.Flags)
	return
}
