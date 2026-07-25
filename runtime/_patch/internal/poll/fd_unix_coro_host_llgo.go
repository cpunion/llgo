//go:build llgo && llgo_coro && (wasm || tinygo.wasm) && !baremetal && !coro_runtime_adapter_test

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package poll

import (
	"internal/strconv"
	"io"
	"syscall"
	"unsafe"
)

const (
	llgoCoroHostDeadlineFlagV1    = uintptr(1 << 31)
	llgoCoroHostFileReadV1        = uintptr(1<<16 | 2)
	llgoCoroHostFileWriteV1       = uintptr(1<<16 | 3)
	llgoCoroHostNetworkAcceptV1   = uintptr(2<<16 | 4)
	llgoCoroHostNetworkConnectV1  = uintptr(2<<16 | 5)
	llgoCoroHostNetworkRecvFromV1 = uintptr(2<<16 | 11)
	llgoCoroHostNetworkSendToV1   = uintptr(2<<16 | 12)
	llgoCoroHostNetworkRecvMsgV1  = uintptr(2<<16 | 13)
	llgoCoroHostNetworkSendMsgV1  = uintptr(2<<16 | 14)
	llgoCoroHostControlReadV1     = uintptr(1)
	llgoCoroHostControlWriteV1    = uintptr(2)
)

type llgoCoroHostSockaddrV1 struct {
	version uint32
	family  uint32
	port    uint32
	zone    uint32
	address [16]byte
}

var (
	_ [32 - unsafe.Sizeof(llgoCoroHostSockaddrV1{})]byte
	_ [unsafe.Sizeof(llgoCoroHostSockaddrV1{}) - 32]byte
)

type llgoCoroHostRecvMsgResultV1 struct {
	address  llgoCoroHostSockaddrV1
	sysflags uint32
}

var (
	_ [36 - unsafe.Sizeof(llgoCoroHostRecvMsgResultV1{})]byte
	_ [unsafe.Sizeof(llgoCoroHostRecvMsgResultV1{}) - 36]byte
)

func runtime_pollDeadlineEpoch(ctx uintptr, mode int) (deadline int64, controlEpoch uintptr)

//go:linkname llgoCoroHostReadUntilV1 llgo.coroHostOperation
func llgoCoroHostReadUntilV1(
	opcode, deadlineLo, deadlineHi, timeoutErrno, controlKey, controlLane, controlEpoch, fd uintptr,
	address unsafe.Pointer,
	size uintptr,
) (r1, r2, errno uintptr)

//go:linkname llgoCoroHostWriteUntilV1 llgo.coroHostOperation
func llgoCoroHostWriteUntilV1(
	opcode, deadlineLo, deadlineHi, timeoutErrno, controlKey, controlLane, controlEpoch, fd uintptr,
	address unsafe.Pointer,
	size uintptr,
) (r1, r2, errno uintptr)

//go:linkname llgoCoroHostAcceptUntilV1 llgo.coroHostOperation
func llgoCoroHostAcceptUntilV1(
	opcode, deadlineLo, deadlineHi, timeoutErrno, controlKey, controlLane, controlEpoch, fd uintptr,
	address unsafe.Pointer,
	size uintptr,
) (r1, r2, errno uintptr)

//go:linkname llgoCoroHostConnectUntilV1 llgo.coroHostOperation
func llgoCoroHostConnectUntilV1(
	opcode, deadlineLo, deadlineHi, timeoutErrno, controlKey, controlLane, controlEpoch, fd uintptr,
	address unsafe.Pointer,
	size uintptr,
) (r1, r2, errno uintptr)

//go:linkname llgoCoroHostRecvFromUntilV1 llgo.coroHostOperation
func llgoCoroHostRecvFromUntilV1(
	opcode, deadlineLo, deadlineHi, timeoutErrno, controlKey, controlLane, controlEpoch, fd uintptr,
	buffer unsafe.Pointer,
	bufferSize uintptr,
	flags uintptr,
	address unsafe.Pointer,
	addressSize uintptr,
) (r1, r2, errno uintptr)

//go:linkname llgoCoroHostSendToUntilV1 llgo.coroHostOperation
func llgoCoroHostSendToUntilV1(
	opcode, deadlineLo, deadlineHi, timeoutErrno, controlKey, controlLane, controlEpoch, fd uintptr,
	buffer unsafe.Pointer,
	bufferSize uintptr,
	flags uintptr,
	address unsafe.Pointer,
	addressSize uintptr,
) (r1, r2, errno uintptr)

//go:linkname llgoCoroHostRecvMsgUntilV1 llgo.coroHostOperation
func llgoCoroHostRecvMsgUntilV1(
	opcode, deadlineLo, deadlineHi, timeoutErrno, controlKey, controlLane, controlEpoch, fd uintptr,
	buffer unsafe.Pointer,
	bufferSize uintptr,
	oob unsafe.Pointer,
	oobSize, flags uintptr,
	result unsafe.Pointer,
	resultSize uintptr,
) (r1, r2, errno uintptr)

//go:linkname llgoCoroHostSendMsgUntilV1 llgo.coroHostOperation
func llgoCoroHostSendMsgUntilV1(
	opcode, deadlineLo, deadlineHi, timeoutErrno, controlKey, controlLane, controlEpoch, fd uintptr,
	buffer unsafe.Pointer,
	bufferSize uintptr,
	oob unsafe.Pointer,
	oobSize, flags uintptr,
	address unsafe.Pointer,
	addressSize uintptr,
) (r1, r2, errno uintptr)

func llgoCoroHostBufferAddressV1(p []byte) unsafe.Pointer {
	if len(p) == 0 {
		return nil
	}
	return unsafe.Pointer(&p[0])
}

func llgoCoroHostDeadlineWordsV1(deadline int64) (uintptr, uintptr) {
	word := uint64(deadline)
	return uintptr(uint32(word)), uintptr(uint32(word >> 32))
}

func llgoCoroHostReadOnceV1(fd *FD, p []byte) (int, error) {
	if fd.pd.runtimeCtx == 0 {
		return syscall.Read(fd.Sysfd, p)
	}
	deadline, controlEpoch := runtime_pollDeadlineEpoch(fd.pd.runtimeCtx, 'r')
	lo, hi := llgoCoroHostDeadlineWordsV1(deadline)
	r1, _, errno := llgoCoroHostReadUntilV1(
		llgoCoroHostDeadlineFlagV1|llgoCoroHostFileReadV1,
		lo,
		hi,
		uintptr(syscall.ETIMEDOUT),
		fd.pd.runtimeCtx,
		llgoCoroHostControlReadV1,
		controlEpoch,
		uintptr(fd.Sysfd),
		llgoCoroHostBufferAddressV1(p),
		uintptr(len(p)),
	)
	if r1 == ^uintptr(0) {
		return 0, syscall.Errno(errno)
	}
	return int(r1), nil
}

func llgoCoroHostWriteOnceV1(fd *FD, p []byte) (int, error) {
	if fd.pd.runtimeCtx == 0 {
		return syscall.Write(fd.Sysfd, p)
	}
	deadline, controlEpoch := runtime_pollDeadlineEpoch(fd.pd.runtimeCtx, 'w')
	lo, hi := llgoCoroHostDeadlineWordsV1(deadline)
	r1, _, errno := llgoCoroHostWriteUntilV1(
		llgoCoroHostDeadlineFlagV1|llgoCoroHostFileWriteV1,
		lo,
		hi,
		uintptr(syscall.ETIMEDOUT),
		fd.pd.runtimeCtx,
		llgoCoroHostControlWriteV1,
		controlEpoch,
		uintptr(fd.Sysfd),
		llgoCoroHostBufferAddressV1(p),
		uintptr(len(p)),
	)
	if r1 == ^uintptr(0) {
		return 0, syscall.Errno(errno)
	}
	return int(r1), nil
}

func llgoCoroHostAcceptOnceV1(fd *FD) (int, syscall.Sockaddr, error) {
	if fd.pd.runtimeCtx == 0 {
		return syscall.Accept(fd.Sysfd)
	}
	deadline, controlEpoch := runtime_pollDeadlineEpoch(fd.pd.runtimeCtx, 'r')
	lo, hi := llgoCoroHostDeadlineWordsV1(deadline)
	address := llgoCoroHostSockaddrV1{version: 1}
	r1, _, errno := llgoCoroHostAcceptUntilV1(
		llgoCoroHostDeadlineFlagV1|llgoCoroHostNetworkAcceptV1,
		lo,
		hi,
		uintptr(syscall.ETIMEDOUT),
		fd.pd.runtimeCtx,
		llgoCoroHostControlReadV1,
		controlEpoch,
		uintptr(fd.Sysfd),
		unsafe.Pointer(&address),
		unsafe.Sizeof(address),
	)
	if r1 == ^uintptr(0) {
		return -1, nil, syscall.Errno(errno)
	}
	peer, err := llgoCoroHostSockaddrFromV1(address)
	if err != nil {
		_ = syscall.Close(int(r1))
		return -1, nil, err
	}
	return int(r1), peer, nil
}

func llgoCoroHostSockaddrFromV1(address llgoCoroHostSockaddrV1) (syscall.Sockaddr, error) {
	if address.version != 1 || address.port > 1<<16-1 {
		return nil, syscall.EINVAL
	}
	switch address.family {
	case 1:
		value := &syscall.SockaddrInet4{Port: int(address.port)}
		copy(value.Addr[:], address.address[:4])
		return value, nil
	case 2:
		value := &syscall.SockaddrInet6{Port: int(address.port), ZoneId: address.zone}
		copy(value.Addr[:], address.address[:])
		return value, nil
	default:
		return nil, syscall.EAFNOSUPPORT
	}
}

func llgoCoroHostOptionalSockaddrFromV1(address llgoCoroHostSockaddrV1) (syscall.Sockaddr, error) {
	if address.version == 0 {
		return nil, nil
	}
	return llgoCoroHostSockaddrFromV1(address)
}

func llgoCoroHostSockaddrForV1(address syscall.Sockaddr) (llgoCoroHostSockaddrV1, error) {
	switch value := address.(type) {
	case *syscall.SockaddrInet4:
		if value == nil || value.Port < 0 || value.Port > 1<<16-1 {
			return llgoCoroHostSockaddrV1{}, syscall.EINVAL
		}
		result := llgoCoroHostSockaddrV1{
			version: 1,
			family:  1,
			port:    uint32(value.Port),
		}
		copy(result.address[:4], value.Addr[:])
		return result, nil
	case *syscall.SockaddrInet6:
		if value == nil || value.Port < 0 || value.Port > 1<<16-1 {
			return llgoCoroHostSockaddrV1{}, syscall.EINVAL
		}
		result := llgoCoroHostSockaddrV1{
			version: 1,
			family:  2,
			port:    uint32(value.Port),
			zone:    value.ZoneId,
		}
		copy(result.address[:], value.Addr[:])
		return result, nil
	default:
		return llgoCoroHostSockaddrV1{}, syscall.EAFNOSUPPORT
	}
}

func llgoCoroHostConnectOnceV1(fd *FD, peer syscall.Sockaddr) error {
	if fd.pd.runtimeCtx == 0 {
		return syscall.Connect(fd.Sysfd, peer)
	}
	address, err := llgoCoroHostSockaddrForV1(peer)
	if err != nil {
		return err
	}
	deadline, controlEpoch := runtime_pollDeadlineEpoch(fd.pd.runtimeCtx, 'w')
	lo, hi := llgoCoroHostDeadlineWordsV1(deadline)
	r1, _, errno := llgoCoroHostConnectUntilV1(
		llgoCoroHostDeadlineFlagV1|llgoCoroHostNetworkConnectV1,
		lo,
		hi,
		uintptr(syscall.ETIMEDOUT),
		fd.pd.runtimeCtx,
		llgoCoroHostControlWriteV1,
		controlEpoch,
		uintptr(fd.Sysfd),
		unsafe.Pointer(&address),
		unsafe.Sizeof(address),
	)
	if r1 == ^uintptr(0) {
		return syscall.Errno(errno)
	}
	return nil
}

func llgoCoroHostRecvFromOnceV1(
	fd *FD,
	p []byte,
) (int, llgoCoroHostSockaddrV1, error) {
	if fd.pd.runtimeCtx == 0 {
		n, peer, err := syscall.Recvfrom(fd.Sysfd, p, 0)
		if err != nil {
			return 0, llgoCoroHostSockaddrV1{}, err
		}
		address, err := llgoCoroHostSockaddrForV1(peer)
		return n, address, err
	}
	deadline, controlEpoch := runtime_pollDeadlineEpoch(fd.pd.runtimeCtx, 'r')
	lo, hi := llgoCoroHostDeadlineWordsV1(deadline)
	address := llgoCoroHostSockaddrV1{version: 1}
	r1, _, errno := llgoCoroHostRecvFromUntilV1(
		llgoCoroHostDeadlineFlagV1|llgoCoroHostNetworkRecvFromV1,
		lo,
		hi,
		uintptr(syscall.ETIMEDOUT),
		fd.pd.runtimeCtx,
		llgoCoroHostControlReadV1,
		controlEpoch,
		uintptr(fd.Sysfd),
		llgoCoroHostBufferAddressV1(p),
		uintptr(len(p)),
		0,
		unsafe.Pointer(&address),
		unsafe.Sizeof(address),
	)
	if r1 == ^uintptr(0) {
		return 0, llgoCoroHostSockaddrV1{}, syscall.Errno(errno)
	}
	if _, err := llgoCoroHostSockaddrFromV1(address); err != nil {
		return 0, llgoCoroHostSockaddrV1{}, err
	}
	return int(r1), address, nil
}

func llgoCoroHostSendToOnceV1(fd *FD, p []byte, peer syscall.Sockaddr) (int, error) {
	if fd.pd.runtimeCtx == 0 {
		if err := syscall.Sendto(fd.Sysfd, p, 0, peer); err != nil {
			return 0, err
		}
		return len(p), nil
	}
	address, err := llgoCoroHostSockaddrForV1(peer)
	if err != nil {
		return 0, err
	}
	deadline, controlEpoch := runtime_pollDeadlineEpoch(fd.pd.runtimeCtx, 'w')
	lo, hi := llgoCoroHostDeadlineWordsV1(deadline)
	r1, _, errno := llgoCoroHostSendToUntilV1(
		llgoCoroHostDeadlineFlagV1|llgoCoroHostNetworkSendToV1,
		lo,
		hi,
		uintptr(syscall.ETIMEDOUT),
		fd.pd.runtimeCtx,
		llgoCoroHostControlWriteV1,
		controlEpoch,
		uintptr(fd.Sysfd),
		llgoCoroHostBufferAddressV1(p),
		uintptr(len(p)),
		0,
		unsafe.Pointer(&address),
		unsafe.Sizeof(address),
	)
	if r1 == ^uintptr(0) {
		return 0, syscall.Errno(errno)
	}
	return int(r1), nil
}

func llgoCoroHostRecvMsgOnceV1(
	fd *FD,
	p, oob []byte,
	flags int,
) (int, int, int, llgoCoroHostSockaddrV1, error) {
	if fd.pd.runtimeCtx == 0 {
		n, oobn, sysflags, peer, err := syscall.Recvmsg(fd.Sysfd, p, oob, flags)
		if err != nil {
			return n, oobn, sysflags, llgoCoroHostSockaddrV1{}, err
		}
		if peer == nil {
			return n, oobn, sysflags, llgoCoroHostSockaddrV1{}, nil
		}
		address, err := llgoCoroHostSockaddrForV1(peer)
		return n, oobn, sysflags, address, err
	}
	deadline, controlEpoch := runtime_pollDeadlineEpoch(fd.pd.runtimeCtx, 'r')
	lo, hi := llgoCoroHostDeadlineWordsV1(deadline)
	result := llgoCoroHostRecvMsgResultV1{
		address: llgoCoroHostSockaddrV1{version: 1},
	}
	r1, r2, errno := llgoCoroHostRecvMsgUntilV1(
		llgoCoroHostDeadlineFlagV1|llgoCoroHostNetworkRecvMsgV1,
		lo,
		hi,
		uintptr(syscall.ETIMEDOUT),
		fd.pd.runtimeCtx,
		llgoCoroHostControlReadV1,
		controlEpoch,
		uintptr(fd.Sysfd),
		llgoCoroHostBufferAddressV1(p),
		uintptr(len(p)),
		llgoCoroHostBufferAddressV1(oob),
		uintptr(len(oob)),
		uintptr(flags),
		unsafe.Pointer(&result),
		unsafe.Sizeof(result),
	)
	if r1 == ^uintptr(0) {
		return 0, 0, 0, llgoCoroHostSockaddrV1{}, syscall.Errno(errno)
	}
	if _, err := llgoCoroHostOptionalSockaddrFromV1(result.address); err != nil {
		return 0, 0, 0, llgoCoroHostSockaddrV1{}, err
	}
	return int(r1), int(r2), int(result.sysflags), result.address, nil
}

func llgoCoroHostSendMsgOnceV1(
	fd *FD,
	p, oob []byte,
	peer syscall.Sockaddr,
	flags int,
) (int, int, error) {
	if fd.pd.runtimeCtx == 0 {
		n, err := syscall.SendmsgN(fd.Sysfd, p, oob, peer, flags)
		if err != nil {
			return n, 0, err
		}
		return n, len(oob), nil
	}
	var address llgoCoroHostSockaddrV1
	var addressPointer unsafe.Pointer
	var addressSize uintptr
	if peer != nil {
		var err error
		address, err = llgoCoroHostSockaddrForV1(peer)
		if err != nil {
			return 0, 0, err
		}
		addressPointer = unsafe.Pointer(&address)
		addressSize = unsafe.Sizeof(address)
	}
	deadline, controlEpoch := runtime_pollDeadlineEpoch(fd.pd.runtimeCtx, 'w')
	lo, hi := llgoCoroHostDeadlineWordsV1(deadline)
	r1, r2, errno := llgoCoroHostSendMsgUntilV1(
		llgoCoroHostDeadlineFlagV1|llgoCoroHostNetworkSendMsgV1,
		lo,
		hi,
		uintptr(syscall.ETIMEDOUT),
		fd.pd.runtimeCtx,
		llgoCoroHostControlWriteV1,
		controlEpoch,
		uintptr(fd.Sysfd),
		llgoCoroHostBufferAddressV1(p),
		uintptr(len(p)),
		llgoCoroHostBufferAddressV1(oob),
		uintptr(len(oob)),
		uintptr(flags),
		addressPointer,
		addressSize,
	)
	if r1 == ^uintptr(0) {
		return 0, 0, syscall.Errno(errno)
	}
	return int(r1), int(r2), nil
}

func (fd *FD) Read(p []byte) (int, error) {
	if err := fd.readLock(); err != nil {
		return 0, err
	}
	defer fd.readUnlock()
	if len(p) == 0 {
		return 0, nil
	}
	if fd.IsStream && len(p) > maxRW {
		p = p[:maxRW]
	}
	for {
		if err := fd.pd.prepareRead(fd.isFile); err != nil {
			return 0, err
		}
		n, err := llgoCoroHostReadOnceV1(fd, p)
		if err == syscall.EINTR {
			continue
		}
		if err == syscall.ETIMEDOUT {
			// A dynamic deadline or Close cancels the exact pending HostOp.
			// Re-enter prepareRead to observe the latest synchronous policy;
			// it returns timeout/closing or permits a reconfigured resubmit.
			continue
		}
		err = fd.eofError(n, err)
		return n, err
	}
}

func llgoCoroHostReadFromV1(
	fd *FD,
	p []byte,
) (int, llgoCoroHostSockaddrV1, error) {
	for {
		if err := fd.pd.prepareRead(fd.isFile); err != nil {
			return 0, llgoCoroHostSockaddrV1{}, err
		}
		n, peer, err := llgoCoroHostRecvFromOnceV1(fd, p)
		if err == syscall.EINTR || err == syscall.ETIMEDOUT {
			continue
		}
		err = fd.eofError(n, err)
		return n, peer, err
	}
}

func (fd *FD) ReadFrom(p []byte) (int, syscall.Sockaddr, error) {
	if err := fd.readLock(); err != nil {
		return 0, nil, err
	}
	defer fd.readUnlock()
	n, address, err := llgoCoroHostReadFromV1(fd, p)
	if err != nil {
		return n, nil, err
	}
	peer, err := llgoCoroHostSockaddrFromV1(address)
	return n, peer, err
}

func (fd *FD) ReadFromInet4(p []byte, from *syscall.SockaddrInet4) (int, error) {
	if from == nil {
		return 0, syscall.EINVAL
	}
	if err := fd.readLock(); err != nil {
		return 0, err
	}
	defer fd.readUnlock()
	n, address, err := llgoCoroHostReadFromV1(fd, p)
	if err != nil {
		return n, err
	}
	if address.family != 1 {
		return 0, syscall.EAFNOSUPPORT
	}
	*from = syscall.SockaddrInet4{Port: int(address.port)}
	copy(from.Addr[:], address.address[:4])
	return n, nil
}

func (fd *FD) ReadFromInet6(p []byte, from *syscall.SockaddrInet6) (int, error) {
	if from == nil {
		return 0, syscall.EINVAL
	}
	if err := fd.readLock(); err != nil {
		return 0, err
	}
	defer fd.readUnlock()
	n, address, err := llgoCoroHostReadFromV1(fd, p)
	if err != nil {
		return n, err
	}
	if address.family != 2 {
		return 0, syscall.EAFNOSUPPORT
	}
	*from = syscall.SockaddrInet6{Port: int(address.port), ZoneId: address.zone}
	copy(from.Addr[:], address.address[:])
	return n, nil
}

func llgoCoroHostReadMsgV1(
	fd *FD,
	p, oob []byte,
	flags int,
) (int, int, int, llgoCoroHostSockaddrV1, error) {
	for {
		if err := fd.pd.prepareRead(fd.isFile); err != nil {
			return 0, 0, 0, llgoCoroHostSockaddrV1{}, err
		}
		n, oobn, sysflags, peer, err := llgoCoroHostRecvMsgOnceV1(fd, p, oob, flags)
		if err == syscall.EINTR || err == syscall.ETIMEDOUT {
			continue
		}
		err = fd.eofError(n, err)
		return n, oobn, sysflags, peer, err
	}
}

func (fd *FD) ReadMsg(p []byte, oob []byte, flags int) (int, int, int, syscall.Sockaddr, error) {
	if err := fd.readLock(); err != nil {
		return 0, 0, 0, nil, err
	}
	defer fd.readUnlock()
	n, oobn, sysflags, address, err := llgoCoroHostReadMsgV1(fd, p, oob, flags)
	if err != nil {
		return n, oobn, sysflags, nil, err
	}
	peer, err := llgoCoroHostOptionalSockaddrFromV1(address)
	return n, oobn, sysflags, peer, err
}

func (fd *FD) ReadMsgInet4(
	p []byte,
	oob []byte,
	flags int,
	from *syscall.SockaddrInet4,
) (int, int, int, error) {
	if from == nil {
		return 0, 0, 0, syscall.EINVAL
	}
	if err := fd.readLock(); err != nil {
		return 0, 0, 0, err
	}
	defer fd.readUnlock()
	n, oobn, sysflags, address, err := llgoCoroHostReadMsgV1(fd, p, oob, flags)
	if err != nil {
		return n, oobn, sysflags, err
	}
	if address.family != 1 {
		return 0, 0, 0, syscall.EAFNOSUPPORT
	}
	*from = syscall.SockaddrInet4{Port: int(address.port)}
	copy(from.Addr[:], address.address[:4])
	return n, oobn, sysflags, nil
}

func (fd *FD) ReadMsgInet6(
	p []byte,
	oob []byte,
	flags int,
	from *syscall.SockaddrInet6,
) (int, int, int, error) {
	if from == nil {
		return 0, 0, 0, syscall.EINVAL
	}
	if err := fd.readLock(); err != nil {
		return 0, 0, 0, err
	}
	defer fd.readUnlock()
	n, oobn, sysflags, address, err := llgoCoroHostReadMsgV1(fd, p, oob, flags)
	if err != nil {
		return n, oobn, sysflags, err
	}
	if address.family != 2 {
		return 0, 0, 0, syscall.EAFNOSUPPORT
	}
	*from = syscall.SockaddrInet6{Port: int(address.port), ZoneId: address.zone}
	copy(from.Addr[:], address.address[:])
	return n, oobn, sysflags, nil
}

func (fd *FD) Write(p []byte) (int, error) {
	if err := fd.writeLock(); err != nil {
		return 0, err
	}
	defer fd.writeUnlock()
	var nn int
	for {
		if err := fd.pd.prepareWrite(fd.isFile); err != nil {
			return nn, err
		}
		max := len(p)
		if fd.IsStream && max-nn > maxRW {
			max = nn + maxRW
		}
		n, err := llgoCoroHostWriteOnceV1(fd, p[nn:max])
		if err == syscall.EINTR {
			continue
		}
		if err == syscall.ETIMEDOUT {
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
		if err != nil {
			return nn, err
		}
		if n == 0 {
			return nn, io.ErrUnexpectedEOF
		}
	}
}

func llgoCoroHostWriteToV1(fd *FD, p []byte, peer syscall.Sockaddr) (int, error) {
	for {
		if err := fd.pd.prepareWrite(fd.isFile); err != nil {
			return 0, err
		}
		n, err := llgoCoroHostSendToOnceV1(fd, p, peer)
		if err == syscall.EINTR || err == syscall.ETIMEDOUT {
			continue
		}
		if err != nil {
			return n, err
		}
		if n != len(p) {
			return n, io.ErrShortWrite
		}
		return n, nil
	}
}

func (fd *FD) WriteTo(p []byte, peer syscall.Sockaddr) (int, error) {
	if err := fd.writeLock(); err != nil {
		return 0, err
	}
	defer fd.writeUnlock()
	return llgoCoroHostWriteToV1(fd, p, peer)
}

func (fd *FD) WriteToInet4(p []byte, peer *syscall.SockaddrInet4) (int, error) {
	if peer == nil {
		return 0, syscall.EINVAL
	}
	if err := fd.writeLock(); err != nil {
		return 0, err
	}
	defer fd.writeUnlock()
	return llgoCoroHostWriteToV1(fd, p, peer)
}

func (fd *FD) WriteToInet6(p []byte, peer *syscall.SockaddrInet6) (int, error) {
	if peer == nil {
		return 0, syscall.EINVAL
	}
	if err := fd.writeLock(); err != nil {
		return 0, err
	}
	defer fd.writeUnlock()
	return llgoCoroHostWriteToV1(fd, p, peer)
}

func llgoCoroHostWriteMsgV1(
	fd *FD,
	p, oob []byte,
	peer syscall.Sockaddr,
) (int, int, error) {
	for {
		if err := fd.pd.prepareWrite(fd.isFile); err != nil {
			return 0, 0, err
		}
		n, oobn, err := llgoCoroHostSendMsgOnceV1(fd, p, oob, peer, 0)
		if err == syscall.EINTR || err == syscall.ETIMEDOUT {
			continue
		}
		if err != nil {
			return n, 0, err
		}
		return n, oobn, nil
	}
}

func (fd *FD) WriteMsg(p []byte, oob []byte, peer syscall.Sockaddr) (int, int, error) {
	if err := fd.writeLock(); err != nil {
		return 0, 0, err
	}
	defer fd.writeUnlock()
	return llgoCoroHostWriteMsgV1(fd, p, oob, peer)
}

func (fd *FD) WriteMsgInet4(
	p []byte,
	oob []byte,
	peer *syscall.SockaddrInet4,
) (int, int, error) {
	if err := fd.writeLock(); err != nil {
		return 0, 0, err
	}
	defer fd.writeUnlock()
	var address syscall.Sockaddr
	if peer != nil {
		address = peer
	}
	return llgoCoroHostWriteMsgV1(fd, p, oob, address)
}

func (fd *FD) WriteMsgInet6(
	p []byte,
	oob []byte,
	peer *syscall.SockaddrInet6,
) (int, int, error) {
	if err := fd.writeLock(); err != nil {
		return 0, 0, err
	}
	defer fd.writeUnlock()
	var address syscall.Sockaddr
	if peer != nil {
		address = peer
	}
	return llgoCoroHostWriteMsgV1(fd, p, oob, address)
}

// Accept uses the same read control lane as stream reads. FD.readLock keeps
// them mutually exclusive, while SetDeadline and Close can cancel an already
// submitted host accept and wait for its physical retirement.
func (fd *FD) Accept() (int, syscall.Sockaddr, string, error) {
	if err := fd.readLock(); err != nil {
		return -1, nil, "", err
	}
	defer fd.readUnlock()
	for {
		if err := fd.pd.prepareRead(fd.isFile); err != nil {
			return -1, nil, "", err
		}
		accepted, peer, err := llgoCoroHostAcceptOnceV1(fd)
		if err == syscall.EINTR || err == syscall.ETIMEDOUT {
			continue
		}
		if err != nil {
			return -1, nil, "accept", err
		}
		return accepted, peer, "", nil
	}
}

// Connect is selected only by the host-specific net source patch. Initializing
// the poll descriptor before this call gives context deadline/cancellation an
// exact write-lane OperationID to cancel while preserving net's synchronous
// connect API.
func (fd *FD) Connect(peer syscall.Sockaddr) error {
	if err := fd.writeLock(); err != nil {
		return err
	}
	defer fd.writeUnlock()
	for {
		if err := fd.pd.prepareWrite(fd.isFile); err != nil {
			return err
		}
		err := llgoCoroHostConnectOnceV1(fd, peer)
		if err == syscall.EINTR || err == syscall.ETIMEDOUT {
			continue
		}
		return err
	}
}
