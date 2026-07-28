//go:build wasm || tinygo.wasm

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package syscall

import (
	stdsyscall "syscall"
	"unsafe"
)

const LLGoPackage = true

// The host operation opcode is a stable class/verb pair. The runtime treats
// it as opaque; an embedding chooses the actual filesystem or network
// implementation. Class 1 is the synchronous Go syscall-compatible file
// surface; class 2 is the socket surface.
const (
	coroHostFileOpenV1   = uintptr(1<<16 | 1)
	coroHostFileReadV1   = uintptr(1<<16 | 2)
	coroHostFileWriteV1  = uintptr(1<<16 | 3)
	coroHostFileCloseV1  = uintptr(1<<16 | 4)
	coroHostFileSeekV1   = uintptr(1<<16 | 5)
	coroHostFileUnlinkV1 = uintptr(1<<16 | 6)

	coroHostNetworkSocketV1      = uintptr(2<<16 | 1)
	coroHostNetworkBindV1        = uintptr(2<<16 | 2)
	coroHostNetworkListenV1      = uintptr(2<<16 | 3)
	coroHostNetworkAcceptV1      = uintptr(2<<16 | 4)
	coroHostNetworkConnectV1     = uintptr(2<<16 | 5)
	coroHostNetworkGetSockNameV1 = uintptr(2<<16 | 6)
	coroHostNetworkGetPeerNameV1 = uintptr(2<<16 | 7)
	coroHostNetworkSetSockOptV1  = uintptr(2<<16 | 8)
	coroHostNetworkGetSockOptV1  = uintptr(2<<16 | 9)
	coroHostNetworkShutdownV1    = uintptr(2<<16 | 10)
	coroHostNetworkRecvFromV1    = uintptr(2<<16 | 11)
	coroHostNetworkSendToV1      = uintptr(2<<16 | 12)
	coroHostNetworkRecvMsgV1     = uintptr(2<<16 | 13)
	coroHostNetworkSendMsgV1     = uintptr(2<<16 | 14)
)

const (
	coroHostSocketFamilyInet4V1 = uintptr(1)
	coroHostSocketFamilyInet6V1 = uintptr(2)

	coroHostSocketTypeStreamV1    = uintptr(1)
	coroHostSocketTypeDatagramV1  = uintptr(2)
	coroHostSocketTypeSeqPacketV1 = uintptr(3)

	coroHostSocketProtocolDefaultV1 = uintptr(0)
	coroHostSocketProtocolTCPV1     = uintptr(1)
	coroHostSocketProtocolUDPV1     = uintptr(2)
)

//go:linkname coroHostOperation1 llgo.coroHostOperation
func coroHostOperation1(opcode, a0 uintptr) (r1, r2, errno uintptr)

//go:linkname coroHostOperationScalars2 llgo.coroHostOperation
func coroHostOperationScalars2(opcode, a0, a1 uintptr) (r1, r2, errno uintptr)

//go:linkname coroHostOperationScalars3 llgo.coroHostOperation
func coroHostOperationScalars3(opcode, a0, a1, a2 uintptr) (r1, r2, errno uintptr)

//go:linkname coroHostOperationScalars4 llgo.coroHostOperation
func coroHostOperationScalars4(opcode, a0, a1, a2, a3 uintptr) (r1, r2, errno uintptr)

//go:linkname coroHostOperation2 llgo.coroHostOperation
func coroHostOperation2(opcode uintptr, a0 unsafe.Pointer, a1 uintptr) (r1, r2, errno uintptr)

//go:linkname coroHostOperation3 llgo.coroHostOperation
func coroHostOperation3(opcode, a0 uintptr, a1 unsafe.Pointer, a2 uintptr) (r1, r2, errno uintptr)

//go:linkname coroHostOperation4 llgo.coroHostOperation
func coroHostOperation4(opcode uintptr, a0 unsafe.Pointer, a1, a2, a3 uintptr) (r1, r2, errno uintptr)

//go:linkname coroHostOperationDatagram llgo.coroHostOperation
func coroHostOperationDatagram(
	opcode, fd uintptr,
	buffer unsafe.Pointer,
	bufferSize, flags uintptr,
	address unsafe.Pointer,
	addressSize uintptr,
) (r1, r2, errno uintptr)

//go:linkname coroHostOperationMessage llgo.coroHostOperation
func coroHostOperationMessage(
	opcode, fd uintptr,
	buffer unsafe.Pointer,
	bufferSize uintptr,
	oob unsafe.Pointer,
	oobSize, flags uintptr,
	address unsafe.Pointer,
	addressSize uintptr,
) (r1, r2, errno uintptr)

//go:linkname coroHostOperationSeek llgo.coroHostOperation
func coroHostOperationSeek(opcode, fd, offsetLo, offsetHi, whence uintptr) (r1, r2, errno uintptr)

// coroHostSockaddrV1 is the target-neutral borrowed address record passed to
// class-2 host operations. It deliberately does not expose a Linux, WASI, JS,
// or libc sockaddr layout. Version/family/port/zone are little-endian u32
// fields and Address contains exactly four or sixteen significant bytes.
type coroHostSockaddrV1 struct {
	Version uint32
	Family  uint32
	Port    uint32
	Zone    uint32
	Address [16]byte
}

var (
	_ [32 - unsafe.Sizeof(coroHostSockaddrV1{})]byte
	_ [unsafe.Sizeof(coroHostSockaddrV1{}) - 32]byte
	_ [4 - unsafe.Alignof(coroHostSockaddrV1{})]byte
	_ [unsafe.Alignof(coroHostSockaddrV1{}) - 4]byte
)

type coroHostRecvMsgResultV1 struct {
	Address  coroHostSockaddrV1
	Sysflags uint32
}

var (
	_ [36 - unsafe.Sizeof(coroHostRecvMsgResultV1{})]byte
	_ [unsafe.Sizeof(coroHostRecvMsgResultV1{}) - 36]byte
)

func hostOperationError(r1, errno uintptr) error {
	if r1 == ^uintptr(0) {
		return stdsyscall.Errno(errno)
	}
	return nil
}

func unsupportedWebAssemblySyscall() (r1, r2 uintptr, err stdsyscall.Errno) {
	return ^uintptr(0), 0, stdsyscall.ENOSYS
}

func rawSyscallNoError(trap, a1, a2, a3 uintptr) (r1, r2 uintptr) {
	return ^uintptr(0), 0
}

func Syscall(trap, a1, a2, a3 uintptr) (r1, r2 uintptr, err stdsyscall.Errno) {
	return unsupportedWebAssemblySyscall()
}

func Syscall6(trap, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err stdsyscall.Errno) {
	return unsupportedWebAssemblySyscall()
}

func RawSyscall(trap, a1, a2, a3 uintptr) (r1, r2 uintptr, err stdsyscall.Errno) {
	return unsupportedWebAssemblySyscall()
}

func RawSyscall6(trap, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err stdsyscall.Errno) {
	return unsupportedWebAssemblySyscall()
}

// Named freestanding WebAssembly files are complete host operations rather
// than Linux traps. The ordinary synchronous Go signatures remain unchanged;
// llgo.coroHostOperation is the single compiler-owned stack cut, and its
// pointer-shaped operands keep string/slice storage live until exact host
// completion.
func Open(path string, mode int, perm uint32) (fd int, err error) {
	pointer := unsafe.StringData(path)
	r1, _, errno := coroHostOperation4(
		coroHostFileOpenV1,
		unsafe.Pointer(pointer),
		uintptr(len(path)),
		uintptr(mode),
		uintptr(perm),
	)
	if operationErr := hostOperationError(r1, errno); operationErr != nil {
		return -1, operationErr
	}
	return int(r1), nil
}

func Read(fd int, p []byte) (n int, err error) {
	r1, _, errno := coroHostOperation3(
		coroHostFileReadV1,
		uintptr(fd),
		unsafe.Pointer(unsafe.SliceData(p)),
		uintptr(len(p)),
	)
	if operationErr := hostOperationError(r1, errno); operationErr != nil {
		return 0, operationErr
	}
	return int(r1), nil
}

func Write(fd int, p []byte) (n int, err error) {
	r1, _, errno := coroHostOperation3(
		coroHostFileWriteV1,
		uintptr(fd),
		unsafe.Pointer(unsafe.SliceData(p)),
		uintptr(len(p)),
	)
	if operationErr := hostOperationError(r1, errno); operationErr != nil {
		return 0, operationErr
	}
	return int(r1), nil
}

func Close(fd int) error {
	r1, _, errno := coroHostOperation1(coroHostFileCloseV1, uintptr(fd))
	return hostOperationError(r1, errno)
}

func Seek(fd int, offset int64, whence int) (newoffset int64, err error) {
	word := uint64(offset)
	r1, r2, errno := coroHostOperationSeek(
		coroHostFileSeekV1,
		uintptr(fd),
		uintptr(uint32(word)),
		uintptr(uint32(word>>32)),
		uintptr(whence),
	)
	if operationErr := hostOperationError(r1, errno); operationErr != nil {
		return 0, operationErr
	}
	return int64(uint64(uint32(r1)) | uint64(uint32(r2))<<32), nil
}

func Unlink(path string) error {
	pointer := unsafe.StringData(path)
	r1, _, errno := coroHostOperation2(
		coroHostFileUnlinkV1,
		unsafe.Pointer(pointer),
		uintptr(len(path)),
	)
	return hostOperationError(r1, errno)
}

func hostSocketFamily(domain int) (uintptr, error) {
	switch domain {
	case stdsyscall.AF_INET:
		return coroHostSocketFamilyInet4V1, nil
	case stdsyscall.AF_INET6:
		return coroHostSocketFamilyInet6V1, nil
	default:
		return 0, stdsyscall.EAFNOSUPPORT
	}
}

func hostSocketType(socketType int) (uintptr, error) {
	socketType &^= hostSocketCreationFlags
	switch socketType {
	case stdsyscall.SOCK_STREAM:
		return coroHostSocketTypeStreamV1, nil
	case stdsyscall.SOCK_DGRAM:
		return coroHostSocketTypeDatagramV1, nil
	case stdsyscall.SOCK_SEQPACKET:
		return coroHostSocketTypeSeqPacketV1, nil
	default:
		return 0, stdsyscall.EPROTOTYPE
	}
}

func hostSocketProtocol(protocol int) (uintptr, error) {
	switch protocol {
	case 0:
		return coroHostSocketProtocolDefaultV1, nil
	case stdsyscall.IPPROTO_TCP:
		return coroHostSocketProtocolTCPV1, nil
	case stdsyscall.IPPROTO_UDP:
		return coroHostSocketProtocolUDPV1, nil
	default:
		return 0, stdsyscall.EPROTONOSUPPORT
	}
}

func hostSockaddr(sa stdsyscall.Sockaddr) (coroHostSockaddrV1, error) {
	switch address := sa.(type) {
	case *stdsyscall.SockaddrInet4:
		if address == nil || address.Port < 0 || address.Port > 1<<16-1 {
			return coroHostSockaddrV1{}, stdsyscall.EINVAL
		}
		result := coroHostSockaddrV1{
			Version: 1,
			Family:  uint32(coroHostSocketFamilyInet4V1),
			Port:    uint32(address.Port),
		}
		copy(result.Address[:4], address.Addr[:])
		return result, nil
	case *stdsyscall.SockaddrInet6:
		if address == nil || address.Port < 0 || address.Port > 1<<16-1 {
			return coroHostSockaddrV1{}, stdsyscall.EINVAL
		}
		result := coroHostSockaddrV1{
			Version: 1,
			Family:  uint32(coroHostSocketFamilyInet6V1),
			Port:    uint32(address.Port),
			Zone:    address.ZoneId,
		}
		copy(result.Address[:], address.Addr[:])
		return result, nil
	default:
		return coroHostSockaddrV1{}, stdsyscall.EAFNOSUPPORT
	}
}

func sockaddrFromHost(address *coroHostSockaddrV1) (stdsyscall.Sockaddr, error) {
	if address == nil || address.Version != 1 || address.Port > 1<<16-1 {
		return nil, stdsyscall.EINVAL
	}
	switch uintptr(address.Family) {
	case coroHostSocketFamilyInet4V1:
		result := &stdsyscall.SockaddrInet4{Port: int(address.Port)}
		copy(result.Addr[:], address.Address[:4])
		return result, nil
	case coroHostSocketFamilyInet6V1:
		result := &stdsyscall.SockaddrInet6{
			Port:   int(address.Port),
			ZoneId: address.Zone,
		}
		copy(result.Addr[:], address.Address[:])
		return result, nil
	default:
		return nil, stdsyscall.EAFNOSUPPORT
	}
}

func optionalSockaddrFromHost(address *coroHostSockaddrV1) (stdsyscall.Sockaddr, error) {
	if address == nil || address.Version == 0 {
		return nil, nil
	}
	return sockaddrFromHost(address)
}

// Socket and the operations below preserve the ordinary synchronous syscall
// API while using one target-neutral HostOp recipe. A blocking Accept,
// Connect, Read, or Write therefore parks only its logical G; the embedding
// retains a pointer-free operation ID and copied words until completion.
func Socket(domain, socketType, protocol int) (fd int, err error) {
	family, err := hostSocketFamily(domain)
	if err != nil {
		return -1, err
	}
	kind, err := hostSocketType(socketType)
	if err != nil {
		return -1, err
	}
	proto, err := hostSocketProtocol(protocol)
	if err != nil {
		return -1, err
	}
	r1, _, errno := coroHostOperationScalars3(
		coroHostNetworkSocketV1,
		family,
		kind,
		proto,
	)
	if operationErr := hostOperationError(r1, errno); operationErr != nil {
		return -1, operationErr
	}
	return int(r1), nil
}

func Bind(fd int, sa stdsyscall.Sockaddr) error {
	address, err := hostSockaddr(sa)
	if err != nil {
		return err
	}
	r1, _, errno := coroHostOperation3(
		coroHostNetworkBindV1,
		uintptr(fd),
		unsafe.Pointer(&address),
		unsafe.Sizeof(address),
	)
	return hostOperationError(r1, errno)
}

func Listen(fd, backlog int) error {
	r1, _, errno := coroHostOperationScalars2(
		coroHostNetworkListenV1,
		uintptr(fd),
		uintptr(backlog),
	)
	return hostOperationError(r1, errno)
}

func Accept(fd int) (newfd int, sa stdsyscall.Sockaddr, err error) {
	address := coroHostSockaddrV1{Version: 1}
	r1, _, errno := coroHostOperation3(
		coroHostNetworkAcceptV1,
		uintptr(fd),
		unsafe.Pointer(&address),
		unsafe.Sizeof(address),
	)
	if err := hostOperationError(r1, errno); err != nil {
		return -1, nil, err
	}
	sa, err = sockaddrFromHost(&address)
	if err != nil {
		_ = Close(int(r1))
		return -1, nil, err
	}
	return int(r1), sa, nil
}

func Accept4(fd, flags int) (newfd int, sa stdsyscall.Sockaddr, err error) {
	_ = flags
	return Accept(fd)
}

func Connect(fd int, sa stdsyscall.Sockaddr) error {
	address, err := hostSockaddr(sa)
	if err != nil {
		return err
	}
	r1, _, errno := coroHostOperation3(
		coroHostNetworkConnectV1,
		uintptr(fd),
		unsafe.Pointer(&address),
		unsafe.Sizeof(address),
	)
	return hostOperationError(r1, errno)
}

func Getsockname(fd int) (stdsyscall.Sockaddr, error) {
	address := coroHostSockaddrV1{Version: 1}
	r1, _, errno := coroHostOperation3(
		coroHostNetworkGetSockNameV1,
		uintptr(fd),
		unsafe.Pointer(&address),
		unsafe.Sizeof(address),
	)
	if err := hostOperationError(r1, errno); err != nil {
		return nil, err
	}
	return sockaddrFromHost(&address)
}

func Getpeername(fd int) (stdsyscall.Sockaddr, error) {
	address := coroHostSockaddrV1{Version: 1}
	r1, _, errno := coroHostOperation3(
		coroHostNetworkGetPeerNameV1,
		uintptr(fd),
		unsafe.Pointer(&address),
		unsafe.Sizeof(address),
	)
	if err := hostOperationError(r1, errno); err != nil {
		return nil, err
	}
	return sockaddrFromHost(&address)
}

func SetsockoptInt(fd, level, option, value int) error {
	r1, _, errno := coroHostOperationScalars4(
		coroHostNetworkSetSockOptV1,
		uintptr(fd),
		uintptr(level),
		uintptr(option),
		uintptr(value),
	)
	return hostOperationError(r1, errno)
}

func GetsockoptInt(fd, level, option int) (int, error) {
	r1, _, errno := coroHostOperationScalars3(
		coroHostNetworkGetSockOptV1,
		uintptr(fd),
		uintptr(level),
		uintptr(option),
	)
	if operationErr := hostOperationError(r1, errno); operationErr != nil {
		return 0, operationErr
	}
	return int(r1), nil
}

func Shutdown(fd, how int) error {
	r1, _, errno := coroHostOperationScalars2(
		coroHostNetworkShutdownV1,
		uintptr(fd),
		uintptr(how),
	)
	return hostOperationError(r1, errno)
}

func Recvfrom(fd int, p []byte, flags int) (n int, from stdsyscall.Sockaddr, err error) {
	address := coroHostSockaddrV1{Version: 1}
	r1, _, errno := coroHostOperationDatagram(
		coroHostNetworkRecvFromV1,
		uintptr(fd),
		unsafe.Pointer(unsafe.SliceData(p)),
		uintptr(len(p)),
		uintptr(flags),
		unsafe.Pointer(&address),
		unsafe.Sizeof(address),
	)
	if operationErr := hostOperationError(r1, errno); operationErr != nil {
		return 0, nil, operationErr
	}
	from, err = sockaddrFromHost(&address)
	if err != nil {
		return 0, nil, err
	}
	return int(r1), from, nil
}

func Sendto(fd int, p []byte, flags int, to stdsyscall.Sockaddr) error {
	address, err := hostSockaddr(to)
	if err != nil {
		return err
	}
	r1, _, errno := coroHostOperationDatagram(
		coroHostNetworkSendToV1,
		uintptr(fd),
		unsafe.Pointer(unsafe.SliceData(p)),
		uintptr(len(p)),
		uintptr(flags),
		unsafe.Pointer(&address),
		unsafe.Sizeof(address),
	)
	return hostOperationError(r1, errno)
}

func Recvmsg(
	fd int,
	p, oob []byte,
	flags int,
) (n, oobn int, recvflags int, from stdsyscall.Sockaddr, err error) {
	result := coroHostRecvMsgResultV1{
		Address: coroHostSockaddrV1{Version: 1},
	}
	r1, r2, errno := coroHostOperationMessage(
		coroHostNetworkRecvMsgV1,
		uintptr(fd),
		unsafe.Pointer(unsafe.SliceData(p)),
		uintptr(len(p)),
		unsafe.Pointer(unsafe.SliceData(oob)),
		uintptr(len(oob)),
		uintptr(flags),
		unsafe.Pointer(&result),
		unsafe.Sizeof(result),
	)
	if operationErr := hostOperationError(r1, errno); operationErr != nil {
		return 0, 0, 0, nil, operationErr
	}
	from, err = optionalSockaddrFromHost(&result.Address)
	if err != nil {
		return 0, 0, 0, nil, err
	}
	return int(r1), int(r2), int(result.Sysflags), from, nil
}

func SendmsgN(fd int, p, oob []byte, to stdsyscall.Sockaddr, flags int) (n int, err error) {
	var address coroHostSockaddrV1
	var addressPointer unsafe.Pointer
	var addressSize uintptr
	if to != nil {
		address, err = hostSockaddr(to)
		if err != nil {
			return 0, err
		}
		addressPointer = unsafe.Pointer(&address)
		addressSize = unsafe.Sizeof(address)
	}
	r1, _, errno := coroHostOperationMessage(
		coroHostNetworkSendMsgV1,
		uintptr(fd),
		unsafe.Pointer(unsafe.SliceData(p)),
		uintptr(len(p)),
		unsafe.Pointer(unsafe.SliceData(oob)),
		uintptr(len(oob)),
		uintptr(flags),
		addressPointer,
		addressSize,
	)
	if operationErr := hostOperationError(r1, errno); operationErr != nil {
		return 0, operationErr
	}
	return int(r1), nil
}

// Host operations already provide logical blocking and completion. These
// compatibility calls intentionally do not expose an embedding descriptor as
// a POSIX nonblocking fd or inherit it across exec.
func SetNonblock(fd int, nonblocking bool) error {
	_, _ = fd, nonblocking
	return nil
}

func CloseOnExec(fd int) {
	_ = fd
}
