/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
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

// This program deliberately uses ordinary os, io, and net APIs. Go gc and
// LLGo compile the exact same source so the result includes the standard-
// library adapters needed to reach each runtime's file and network backend.
package main

import (
	"io"
	"net"
	"os"
	"syscall"
	"time"
)

const (
	maximumWork = 100_000_000
	payloadSize = 4 * 1024
)

func parsePositive(text string) (int, bool) {
	if text == "" {
		return 0, false
	}
	value := 0
	for _, char := range text {
		if char < '0' || char > '9' || value > (maximumWork-int(char-'0'))/10 {
			return 0, false
		}
		value = value*10 + int(char-'0')
	}
	return value, value != 0
}

func fillPayload(payload []byte) {
	for index := range payload {
		payload[index] = byte(index*31 + 7)
	}
}

func fileRoundTrip(count, rounds int) int {
	file, err := os.CreateTemp("", "llgo-coro-benchmark-*")
	if err != nil {
		panic(err)
	}
	path := file.Name()
	defer os.Remove(path)
	defer file.Close()

	payload := make([]byte, payloadSize)
	readback := make([]byte, payloadSize)
	fillPayload(payload)
	checksum := 0
	for round := range rounds {
		for index := range count {
			if _, err := file.Seek(0, 0); err != nil {
				panic(err)
			}
			if n, err := file.Write(payload); err != nil || n != len(payload) {
				panic("short file write")
			}
			if _, err := file.Seek(0, 0); err != nil {
				panic(err)
			}
			if _, err := io.ReadFull(file, readback); err != nil {
				panic(err)
			}
			checksum += int(readback[(round*count+index)%len(readback)])
		}
	}
	return checksum
}

// fileSyscallRoundTrip performs the same persistent-file transaction as
// fileRoundTrip but deliberately bypasses os.File and internal/poll inside the
// measured loop. Keeping both modes in one source fixture separates the
// compiler/runtime worker boundary from coroutine frames introduced by the
// standard-library wrapper chain without changing the physical syscalls.
func fileSyscallRoundTrip(count, rounds int) int {
	file, err := os.CreateTemp("", "llgo-coro-benchmark-syscall-*")
	if err != nil {
		panic(err)
	}
	path := file.Name()
	defer os.Remove(path)
	defer file.Close()

	fd := int(file.Fd())
	payload := make([]byte, payloadSize)
	readback := make([]byte, payloadSize)
	fillPayload(payload)
	checksum := 0
	for round := range rounds {
		for index := range count {
			if _, err := syscall.Seek(fd, 0, 0); err != nil {
				panic(err)
			}
			if n, err := syscall.Write(fd, payload); err != nil || n != len(payload) {
				panic("short syscall file write")
			}
			if _, err := syscall.Seek(fd, 0, 0); err != nil {
				panic(err)
			}
			if n, err := syscall.Read(fd, readback); err != nil || n != len(readback) {
				panic("short syscall file read")
			}
			checksum += int(readback[(round*count+index)%len(readback)])
		}
	}
	return checksum
}

func writeFull(conn *net.TCPConn, payload []byte) {
	for len(payload) != 0 {
		n, err := conn.Write(payload)
		if err != nil {
			panic(err)
		}
		payload = payload[n:]
	}
}

func tcpRoundTrip(count, rounds int) int {
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		panic(err)
	}
	defer listener.Close()

	total := count * rounds
	serverDone := make(chan struct{})
	go func() {
		conn, err := listener.AcceptTCP()
		if err != nil {
			panic(err)
		}
		defer close(serverDone)
		defer conn.Close()
		payload := make([]byte, payloadSize)
		for range total {
			if _, err := io.ReadFull(conn, payload); err != nil {
				panic(err)
			}
			writeFull(conn, payload)
		}
	}()

	address := listener.Addr().(*net.TCPAddr)
	conn, err := net.DialTCP("tcp4", nil, address)
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	payload := make([]byte, payloadSize)
	readback := make([]byte, payloadSize)
	fillPayload(payload)
	checksum := 0
	for round := range rounds {
		for index := range count {
			payload[0] = byte(round*count + index)
			writeFull(conn, payload)
			if _, err := io.ReadFull(conn, readback); err != nil {
				panic(err)
			}
			checksum += int(readback[0])
		}
	}
	<-serverDone
	return checksum
}

func main() {
	if len(os.Args) != 4 {
		panic("usage: io_workload <file|file-syscall|tcp> <count> <rounds>")
	}
	mode := os.Args[1]
	count, ok := parsePositive(os.Args[2])
	if !ok {
		panic("invalid count")
	}
	rounds, ok := parsePositive(os.Args[3])
	if !ok || rounds > maximumWork/count {
		panic("invalid rounds")
	}

	started := time.Now()
	result := 0
	switch mode {
	case "file":
		result = fileRoundTrip(count, rounds)
	case "file-syscall":
		result = fileSyscallRoundTrip(count, rounds)
	case "tcp":
		result = tcpRoundTrip(count, rounds)
	default:
		panic("unknown mode")
	}
	elapsed := time.Since(started)
	println("ok", mode, count, rounds, result, elapsed.Nanoseconds())
}
