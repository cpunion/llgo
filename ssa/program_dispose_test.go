package ssa

import (
	"go/types"
	"runtime"
	"testing"
	"time"
)

type disposedProgramOwner struct {
	payload [1024]byte
}

//go:noinline
func newDisposedProgramWithCapturedOwner(done chan<- struct{}) Program {
	prog := NewProgram(nil)
	owner := new(disposedProgramOwner)
	runtime.SetFinalizer(owner, func(*disposedProgramOwner) { done <- struct{}{} })
	prog.SetCompileMethods(func(Package, types.Type) { runtime.KeepAlive(owner) })
	prog.Dispose()
	return prog
}

// Staged code generation retains the disposed Program's identity in its
// coordinator. Its callbacks must no longer retain the source/SSA universe.
func TestCoroBackendDisposedProgramReleasesCapturedOwner(t *testing.T) {
	Initialize(InitAll)
	done := make(chan struct{}, 1)
	prog := newDisposedProgramWithCapturedOwner(done)
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	defer runtime.KeepAlive(prog)
	for {
		runtime.GC()
		select {
		case <-done:
			// Dispose remains idempotent after clearing its Go-owned state.
			prog.Dispose()
			return
		case <-deadline.C:
			t.Fatal("disposed Program still retains its captured compiler owner")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}
