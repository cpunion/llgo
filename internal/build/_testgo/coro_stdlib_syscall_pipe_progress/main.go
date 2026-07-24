package main

import (
	"os"
	"syscall"
	"time"
)

func fail(code int) {
	os.Exit(code)
}

func writeAfterDelay(fd int) {
	time.Sleep(50 * time.Millisecond)
	payload := [1]byte{'p'}
	if n, err := syscall.Write(fd, payload[:]); err != nil || n != len(payload) {
		fail(20)
	}
}

func main() {
	var pipe [2]int
	if err := syscall.Pipe(pipe[:]); err != nil {
		fail(10)
	}

	go writeAfterDelay(pipe[1])

	started := time.Now()
	var payload [1]byte
	n, err := syscall.Read(pipe[0], payload[:])
	if err != nil || n != len(payload) || payload[0] != 'p' {
		fail(11)
	}
	if time.Since(started) < 25*time.Millisecond {
		fail(12)
	}
	if err := syscall.Close(pipe[0]); err != nil {
		fail(13)
	}
	if err := syscall.Close(pipe[1]); err != nil {
		fail(14)
	}
}
