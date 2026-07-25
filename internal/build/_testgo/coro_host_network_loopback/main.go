package main

import (
	"runtime"
	"syscall"
	_ "unsafe"
)

const loopbackPort = 29090
const (
	cancelProbeOpcode  = uintptr(0x7fff0001)
	releaseProbeOpcode = uintptr(0x7fff0002)
	staleEpochOpcode   = uintptr(1<<31 | 0x7fff0003)
	probeToken         = uintptr(0x4c4c474f)
)

//go:linkname cancelProbeHostOperation llgo.coroHostOperation
func cancelProbeHostOperation(opcode, token uintptr) (r1, r2, errno uintptr)

//go:linkname releaseProbeHostOperation llgo.coroHostOperation
func releaseProbeHostOperation(opcode, token uintptr) (r1, r2, errno uintptr)

//go:linkname staleEpochProbeHostOperation llgo.coroHostOperation
func staleEpochProbeHostOperation(
	opcode, deadlineLo, deadlineHi, timeoutErrno,
	controlKey, controlLane, controlEpoch, token uintptr,
) (r1, r2, errno uintptr)

var loopback = &syscall.SockaddrInet4{
	Port: loopbackPort,
	Addr: [4]byte{127, 0, 0, 1},
}

func serve(listener int, done chan<- int) {
	connection, peer, err := syscall.Accept(listener)
	if err != nil {
		done <- 10
		return
	}
	address, ok := peer.(*syscall.SockaddrInet4)
	if !ok || address.Addr != loopback.Addr {
		done <- 11
		return
	}
	var request [1]byte
	if n, err := syscall.Read(connection, request[:]); err != nil || n != 1 || request[0] != 'p' {
		done <- 12
		return
	}
	reply := [1]byte{'q'}
	if n, err := syscall.Write(connection, reply[:]); err != nil || n != 1 {
		done <- 13
		return
	}
	if err := syscall.Close(connection); err != nil {
		done <- 14
		return
	}
	done <- 0
}

func awaitHostCancellation(started chan<- struct{}) {
	close(started)
	_, _, _ = cancelProbeHostOperation(cancelProbeOpcode, probeToken)
	panic("canceled host operation resumed normally")
}

func main() {
	listener, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, syscall.IPPROTO_TCP)
	if err != nil {
		panic("host socket failed")
	}
	if err := syscall.Bind(listener, loopback); err != nil {
		panic("host bind failed")
	}
	if err := syscall.Listen(listener, 4); err != nil {
		panic("host listen failed")
	}

	done := make(chan int, 1)
	go serve(listener, done)

	client, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, syscall.IPPROTO_TCP)
	if err != nil {
		panic("host client socket failed")
	}
	if err := syscall.Connect(client, loopback); err != nil {
		panic("host connect failed")
	}
	request := [1]byte{'p'}
	if n, err := syscall.Write(client, request[:]); err != nil || n != 1 {
		panic("host client write failed")
	}
	var reply [1]byte
	if n, err := syscall.Read(client, reply[:]); err != nil || n != 1 || reply[0] != 'q' {
		panic("host client read failed")
	}
	if err := syscall.Close(client); err != nil {
		panic("host client close failed")
	}
	if status := <-done; status != 0 {
		panic("host server failed")
	}
	if err := syscall.Close(listener); err != nil {
		panic("host listener close failed")
	}

	// Control key 1 is idle at epoch zero in this raw-syscall fixture. Carrying
	// expected epoch one deterministically models SetDeadline/Close landing
	// after the caller's snapshot but before its park hook binds. The hook must
	// submit the exact operation, request physical cancellation, and resume the
	// synchronous caller with its configured timeout errno.
	r1, _, errno := staleEpochProbeHostOperation(
		staleEpochOpcode,
		0,
		0,
		uintptr(syscall.ETIMEDOUT),
		1,
		1,
		1,
		probeToken,
	)
	if r1 != ^uintptr(0) || errno != uintptr(syscall.ETIMEDOUT) {
		panic("stale control epoch did not reconcile through exact cancellation")
	}

	// The child first reaches a physically pending HostOp. Main then parks in
	// a second host turn, which makes the embedding observe the child's Submit
	// before main returns and command shutdown requests its exact cancellation.
	started := make(chan struct{})
	go awaitHostCancellation(started)
	<-started
	runtime.Gosched()
	r1, _, errno = releaseProbeHostOperation(releaseProbeOpcode, probeToken)
	if r1 != probeToken || errno != 0 {
		panic("host cancellation release probe failed")
	}
}
