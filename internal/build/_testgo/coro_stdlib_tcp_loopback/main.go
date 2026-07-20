package main

import (
	"errors"
	"net"
	"os"
	"time"
)

var listener *net.TCPListener

func serve() {
	conn, err := listener.AcceptTCP()
	if err != nil {
		return
	}

	var request [1]byte
	if n, err := conn.Read(request[:]); err != nil || n != len(request) || request[0] != 'p' {
		_ = conn.Close()
		return
	}

	// The client's first Read must park until its 20 ms deadline expires. The
	// same Read succeeds after the deadline is cleared and this reply is sent.
	time.Sleep(60 * time.Millisecond)
	reply := [1]byte{'q'}
	if n, err := conn.Write(reply[:]); err != nil || n != len(reply) {
		_ = conn.Close()
		return
	}
	_ = conn.Close()
}

func main() {
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
	if err := conn.Close(); err != nil {
		os.Exit(38)
	}
	if err := ln.Close(); err != nil {
		os.Exit(39)
	}
}
