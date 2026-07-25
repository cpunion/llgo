package main

import (
	"context"
	"errors"
	"net"
	"os"
	"time"
)

var listener *net.TCPListener

type writeResult struct {
	n   int
	err error
}

func serve() {
	conn, err := listener.AcceptTCP()
	if err != nil {
		return
	}
	defer conn.Close()

	var request [1]byte
	if n, err := conn.Read(request[:]); err != nil || n != len(request) || request[0] != 'p' {
		return
	}

	// The client's first Read must park until its 20 ms deadline expires. The
	// same Read succeeds after the deadline is cleared and this reply is sent.
	time.Sleep(60 * time.Millisecond)
	reply := [1]byte{'q'}
	if n, err := conn.Write(reply[:]); err != nil || n != len(reply) {
		return
	}

	if n, err := conn.Read(request[:]); err != nil || n != len(request) || request[0] != 'd' {
		return
	}
	// This reply is later than the deadline installed by another goroutine
	// after the client's second Read has already parked.
	time.Sleep(80 * time.Millisecond)
	reply[0] = 'e'
	if n, err := conn.Write(reply[:]); err != nil || n != len(reply) {
		return
	}

	// A canceled write must not reach the peer. Only the retry after the
	// client clears its dynamically installed write deadline is observable.
	if n, err := conn.Read(request[:]); err != nil || n != len(request) || request[0] != 'x' {
		return
	}
	reply[0] = 'y'
	if n, err := conn.Write(reply[:]); err != nil || n != len(reply) {
		return
	}

	// Keep the peer alive while the client verifies that Close cancels a
	// different already-pending Read and waits for its physical retirement.
	_, _ = conn.Read(request[:])
}

func main() {
	nativeMode := len(os.Args) == 2 && os.Args[1] == "native"
	if len(os.Args) > 2 || len(os.Args) == 2 && !nativeMode {
		os.Exit(29)
	}

	addr := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)}
	ln, err := net.ListenTCP("tcp4", addr)
	if err != nil {
		os.Exit(30)
	}
	listener = ln
	go serve()

	local, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		os.Exit(31)
	}
	conn, err := net.DialTCP("tcp4", nil, local)
	if err != nil {
		os.Exit(32)
	}

	request := [1]byte{'p'}
	if n, err := conn.Write(request[:]); err != nil || n != len(request) {
		os.Exit(33)
	}

	if err := conn.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		os.Exit(34)
	}
	var reply [1]byte
	n, err := conn.Read(reply[:])
	if n != 0 || !errors.Is(err, os.ErrDeadlineExceeded) {
		os.Exit(35)
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		os.Exit(36)
	}
	n, err = conn.Read(reply[:])
	if err != nil || n != len(reply) || reply[0] != 'q' {
		os.Exit(37)
	}

	request[0] = 'd'
	if n, err := conn.Write(request[:]); err != nil || n != len(request) {
		os.Exit(40)
	}
	deadlineSet := make(chan error, 1)
	go func() {
		time.Sleep(20 * time.Millisecond)
		deadlineSet <- conn.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
	}()
	n, err = conn.Read(reply[:])
	if setErr := <-deadlineSet; setErr != nil {
		os.Exit(41)
	}
	if n != 0 || !errors.Is(err, os.ErrDeadlineExceeded) {
		os.Exit(42)
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		os.Exit(43)
	}
	n, err = conn.Read(reply[:])
	if err != nil || n != len(reply) || reply[0] != 'e' {
		os.Exit(44)
	}

	if nativeMode {
		// A one-byte write to a real loopback socket normally completes before
		// another G can install a deadline. Use an already-expired deadline for
		// the deterministic native integration gate; the event-source race test
		// covers active write-deadline replacement independently. The host
		// runner below can deliberately retain 'w', so it keeps the stronger
		// already-pending integration path.
		if err := conn.SetWriteDeadline(time.Now().Add(-time.Millisecond)); err != nil {
			os.Exit(46)
		}
		request[0] = 'w'
		n, err = conn.Write(request[:])
		if n != 0 || !errors.Is(err, os.ErrDeadlineExceeded) {
			os.Exit(47)
		}
	} else {
		writeDeadlineSet := make(chan error, 1)
		go func() {
			time.Sleep(20 * time.Millisecond)
			writeDeadlineSet <- conn.SetWriteDeadline(time.Now().Add(20 * time.Millisecond))
		}()
		request[0] = 'w'
		n, err = conn.Write(request[:])
		if setErr := <-writeDeadlineSet; setErr != nil {
			os.Exit(46)
		}
		if n != 0 || !errors.Is(err, os.ErrDeadlineExceeded) {
			os.Exit(47)
		}
	}
	if err := conn.SetWriteDeadline(time.Time{}); err != nil {
		os.Exit(48)
	}
	request[0] = 'x'
	if n, err := conn.Write(request[:]); err != nil || n != len(request) {
		os.Exit(49)
	}
	n, err = conn.Read(reply[:])
	if err != nil || n != len(reply) || reply[0] != 'y' {
		os.Exit(50)
	}

	var writeDone chan writeResult
	if !nativeMode {
		writeStarted := make(chan struct{})
		writeDone = make(chan writeResult, 1)
		go func() {
			close(writeStarted)
			blocked := [1]byte{'z'}
			n, err := conn.Write(blocked[:])
			writeDone <- writeResult{n: n, err: err}
		}()
		<-writeStarted
	}
	closeDone := make(chan error, 1)
	go func() {
		time.Sleep(20 * time.Millisecond)
		closeDone <- conn.Close()
	}()
	n, err = conn.Read(reply[:])
	if n != 0 || !errors.Is(err, net.ErrClosed) {
		os.Exit(45)
	}
	if writeDone != nil {
		if result := <-writeDone; result.n != 0 || !errors.Is(result.err, net.ErrClosed) {
			os.Exit(51)
		}
	}
	if closeErr := <-closeDone; closeErr != nil {
		os.Exit(38)
	}

	if err := ln.SetDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		os.Exit(52)
	}
	accepted, err := ln.AcceptTCP()
	if accepted != nil || !errors.Is(err, os.ErrDeadlineExceeded) {
		os.Exit(53)
	}
	if err := ln.SetDeadline(time.Time{}); err != nil {
		os.Exit(54)
	}

	acceptDeadlineSet := make(chan error, 1)
	go func() {
		time.Sleep(20 * time.Millisecond)
		acceptDeadlineSet <- ln.SetDeadline(time.Now().Add(20 * time.Millisecond))
	}()
	accepted, err = ln.AcceptTCP()
	if setErr := <-acceptDeadlineSet; setErr != nil {
		os.Exit(55)
	}
	if accepted != nil || !errors.Is(err, os.ErrDeadlineExceeded) {
		os.Exit(56)
	}
	if err := ln.SetDeadline(time.Time{}); err != nil {
		os.Exit(57)
	}

	listenerCloseDone := make(chan error, 1)
	go func() {
		time.Sleep(20 * time.Millisecond)
		listenerCloseDone <- ln.Close()
	}()
	accepted, err = ln.AcceptTCP()
	if accepted != nil || !errors.Is(err, net.ErrClosed) {
		os.Exit(58)
	}
	if closeErr := <-listenerCloseDone; closeErr != nil {
		os.Exit(39)
	}

	// A real native connect to an unused loopback port normally fails
	// immediately with ECONNREFUSED, so it cannot deterministically exercise a
	// pending connect deadline. Use an already-expired deadline and a
	// pre-canceled context for the native standard-library contract. The host
	// runner deliberately retains this address and therefore keeps the stronger
	// connect-in-flight timer and cancellation paths.
	dialer := &net.Dialer{Timeout: 40 * time.Millisecond}
	if nativeMode {
		dialer = &net.Dialer{Deadline: time.Now().Add(-time.Millisecond)}
	}
	pendingConn, err := dialer.Dial("tcp4", "127.0.0.1:49999")
	timeoutErr, timeoutOK := err.(net.Error)
	if pendingConn != nil || !timeoutOK || !timeoutErr.Timeout() {
		os.Exit(59)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancelStarted := make(chan struct{})
	if nativeMode {
		cancel()
		close(cancelStarted)
	} else {
		go func() {
			close(cancelStarted)
			time.Sleep(20 * time.Millisecond)
			cancel()
		}()
	}
	<-cancelStarted
	pendingConn, err = (&net.Dialer{}).DialContext(ctx, "tcp4", "127.0.0.1:49999")
	if pendingConn != nil || !errors.Is(err, context.Canceled) {
		os.Exit(60)
	}

	udpServer, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		os.Exit(61)
	}
	udpClient, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		os.Exit(62)
	}
	if err := udpServer.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		os.Exit(63)
	}
	var packet [1]byte
	n, peer, err := udpServer.ReadFromUDP(packet[:])
	if n != 0 || peer != nil || !errors.Is(err, os.ErrDeadlineExceeded) {
		os.Exit(64)
	}
	if err := udpServer.SetReadDeadline(time.Time{}); err != nil {
		os.Exit(65)
	}

	serverAddress, ok := udpServer.LocalAddr().(*net.UDPAddr)
	if !ok {
		os.Exit(66)
	}
	packet[0] = 'u'
	if n, err := udpClient.WriteToUDP(packet[:], serverAddress); err != nil || n != len(packet) {
		os.Exit(67)
	}
	n, peer, err = udpServer.ReadFromUDP(packet[:])
	if err != nil || n != len(packet) || packet[0] != 'u' || peer == nil {
		os.Exit(68)
	}
	packet[0] = 'v'
	if n, err := udpServer.WriteToUDP(packet[:], peer); err != nil || n != len(packet) {
		os.Exit(69)
	}
	n, peer, err = udpClient.ReadFromUDP(packet[:])
	if err != nil || n != len(packet) || packet[0] != 'v' || peer == nil {
		os.Exit(70)
	}

	if err := udpServer.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		os.Exit(73)
	}
	n, oobn, flags, msgPeer, err := udpServer.ReadMsgUDP(packet[:], nil)
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		os.Exit(74)
	}
	if err := udpServer.SetReadDeadline(time.Time{}); err != nil {
		os.Exit(75)
	}

	packet[0] = 'm'
	n, oobn, err = udpClient.WriteMsgUDP(packet[:], nil, serverAddress)
	if err != nil || n != len(packet) || oobn != 0 {
		os.Exit(76)
	}
	n, oobn, flags, msgPeer, err = udpServer.ReadMsgUDP(packet[:], nil)
	if err != nil || n != len(packet) || oobn != 0 || flags != 0 ||
		packet[0] != 'm' || msgPeer == nil {
		os.Exit(77)
	}
	packet[0] = 'n'
	n, oobn, err = udpServer.WriteMsgUDP(packet[:], nil, msgPeer)
	if err != nil || n != len(packet) || oobn != 0 {
		os.Exit(78)
	}
	n, oobn, flags, msgPeer, err = udpClient.ReadMsgUDP(packet[:], nil)
	if err != nil || n != len(packet) || oobn != 0 || flags != 0 ||
		packet[0] != 'n' || msgPeer == nil {
		os.Exit(79)
	}
	if err := udpClient.Close(); err != nil {
		os.Exit(71)
	}
	if err := udpServer.Close(); err != nil {
		os.Exit(72)
	}

	// The host runtime currently uses a fixed 64-entry poll descriptor page.
	// Cycle beyond one whole page so every Close must reach its post-unbind
	// retirement gate; a silent leak or stale runtimeCtx eventually makes
	// ListenTCP fail here.
	for cycle := 0; cycle < 80; cycle++ {
		reused, err := net.ListenTCP("tcp4", addr)
		if err != nil {
			os.Exit(84)
		}
		if err := reused.Close(); err != nil {
			os.Exit(85)
		}
	}

	resolver := &net.Resolver{PreferGo: true}
	hosts, err := resolver.LookupHost(context.Background(), "localhost")
	if err != nil {
		os.Exit(80)
	}
	foundLoopback := false
	for _, host := range hosts {
		if host == "127.0.0.1" || host == "::1" {
			foundLoopback = true
			break
		}
	}
	if !foundLoopback {
		os.Exit(81)
	}
	if !nativeMode {
		hosts, err = resolver.LookupHost(context.Background(), "llgo.test")
		if err != nil {
			os.Exit(82)
		}
		foundTestAddress := false
		for _, host := range hosts {
			if host == "192.0.2.42" || host == "2001:db8::42" {
				foundTestAddress = true
				break
			}
		}
		if !foundTestAddress {
			os.Exit(83)
		}
	}
}
