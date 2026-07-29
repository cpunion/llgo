package main

import (
	"syscall"
	"time"
)

const probePath = "/tmp/llgo-coro-wasi-command-reactor"

type fileResult struct {
	code     int
	finished time.Duration
}

func fileRoundTrip() int {
	_ = syscall.Unlink(probePath)
	fd, err := syscall.Open(
		probePath,
		syscall.O_RDWR|syscall.O_CREAT|syscall.O_TRUNC,
		0o600,
	)
	if err != nil {
		return 1
	}

	want := [3]byte{'w', 'a', 's'}
	if n, writeErr := syscall.Write(fd, want[:]); writeErr != nil || n != len(want) {
		_ = syscall.Close(fd)
		return 2
	}
	if position, seekErr := syscall.Seek(fd, 0, 0); seekErr != nil || position != 0 {
		_ = syscall.Close(fd)
		return 3
	}
	var got [3]byte
	if n, readErr := syscall.Read(fd, got[:]); readErr != nil ||
		n != len(got) || got != want {
		_ = syscall.Close(fd)
		return 4
	}
	if err = syscall.Close(fd); err != nil {
		return 5
	}
	if err = syscall.Unlink(probePath); err != nil {
		return 6
	}
	return 0
}

func main() {
	start := time.Now()
	done := make(chan fileResult, 1)
	go func() {
		time.Sleep(20 * time.Millisecond)
		done <- fileResult{
			code:     fileRoundTrip(),
			finished: time.Since(start),
		}
	}()

	// The child timer and all six file operations must be serviced while this
	// independent, later alarm remains parked. A reactor that serializes the
	// whole program behind the 250 ms alarm fails the child timestamp check.
	time.Sleep(250 * time.Millisecond)
	if elapsed := time.Since(start); elapsed < 200*time.Millisecond {
		panic("main timer fired early")
	}
	result := <-done
	if result.code != 0 {
		panic("WASI file round trip failed")
	}
	if result.finished >= 200*time.Millisecond {
		panic("WASI reactor did not make concurrent timer and file progress")
	}
}
