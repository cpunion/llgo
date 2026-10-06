//go:build windows && !nogc

package gotest

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"testing"
	"time"
)

var windowsExitGCObjects [8][]byte

func TestWindowsExitDuringGC(t *testing.T) {
	const helperEnv = "LLGO_TEST_WINDOWS_EXIT_GC"
	if value := os.Getenv(helperEnv); value != "" {
		iteration, err := strconv.Atoi(value)
		if err != nil {
			t.Fatal(err)
		}
		ready := make(chan struct{}, len(windowsExitGCObjects))
		for i := range windowsExitGCObjects {
			go func(i int) {
				windowsExitGCObjects[i] = make([]byte, 4096)
				ready <- struct{}{}
				for {
					windowsExitGCObjects[i] = make([]byte, 4096)
				}
			}(i)
		}
		for range windowsExitGCObjects {
			<-ready
		}
		started := make(chan struct{})
		go func() {
			close(started)
			for {
				runtime.GC()
			}
		}()
		<-started
		// Vary the overlap between explicit collection, allocation, and exit.
		time.Sleep(time.Duration(iteration%4) * time.Millisecond)
		os.Exit(iteration % 2 * 23)
	}

	for iteration := range 32 {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWindowsExitDuringGC$")
		cmd.Env = append(os.Environ(), helperEnv+"="+strconv.Itoa(iteration))
		output, err := cmd.CombinedOutput()
		timedOut := ctx.Err() != nil
		cancel()
		want := iteration % 2 * 23
		if timedOut || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != want {
			t.Fatalf("iteration %d: child err=%v, state=%v, timeout=%v, want exit %d\n%s",
				iteration, err, cmd.ProcessState, timedOut, want, output)
		}
	}
}
