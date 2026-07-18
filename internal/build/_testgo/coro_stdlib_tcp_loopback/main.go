package main

import (
	"net"
	"os"
	"time"
)

var (
	listener *net.TCPListener
	ready    = make(chan bool)
	done     = make(chan int)
)

func readExact(conn *net.TCPConn, buf []byte) bool {
	offset := 0
	for offset < len(buf) {
		n, err := conn.Read(buf[offset:])
		if n > 0 {
			offset += n
		}
		if err != nil || n == 0 {
			return false
		}
	}
	return true
}

func writeExact(conn *net.TCPConn, buf []byte) bool {
	offset := 0
	for offset < len(buf) {
		n, err := conn.Write(buf[offset:])
		if n > 0 {
			offset += n
		}
		if err != nil || n == 0 {
			return false
		}
	}
	return true
}

func serve() {
	// The rendezvous and the delay in main make AcceptTCP reach its readiness
	// wait before DialTCP. This is an acceptance gate for non-blocking scheduler
	// progress, not merely a socket call that happened to be ready already.
	ready <- true
	conn, err := listener.AcceptTCP()
	if err != nil {
		done <- 20
		return
	}
	payload := make([]byte, 4)
	if !readExact(conn, payload) || string(payload) != "ping" {
		done <- 21
		return
	}
	if !writeExact(conn, []byte("pong")) {
		done <- 22
		return
	}
	if err := conn.Close(); err != nil {
		done <- 23
		return
	}
	done <- 0
}

func main() {
	addr := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)}
	ln, err := net.ListenTCP("tcp4", addr)
	if err != nil {
		os.Exit(30)
	}
	listener = ln
	go serve()
	<-ready
	time.Sleep(25 * time.Millisecond)

	local, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		os.Exit(31)
	}
	conn, err := net.DialTCP("tcp4", nil, local)
	if err != nil {
		os.Exit(32)
	}
	if !writeExact(conn, []byte("ping")) {
		os.Exit(33)
	}
	payload := make([]byte, 4)
	if !readExact(conn, payload) || string(payload) != "pong" {
		os.Exit(34)
	}
	if err := conn.Close(); err != nil {
		os.Exit(35)
	}
	if code := <-done; code != 0 {
		os.Exit(code)
	}
	if err := ln.Close(); err != nil {
		os.Exit(36)
	}
}
