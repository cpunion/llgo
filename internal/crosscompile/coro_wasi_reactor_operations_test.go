//go:build !llgo

package crosscompile

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// This gate compiles the actual C reactor into a small WASI command, then
// verifies cancellation, cursor preservation, short I/O and close retirement.
// The standard Go-file fixture separately verifies compiler/runtime integration.
func TestWASIReactorOperationLifecycle(t *testing.T) {
	sysroot := os.Getenv("LLGO_WASI_REACTOR_SYSROOT")
	if sysroot == "" {
		t.Skip("set LLGO_WASI_REACTOR_SYSROOT to run the actual WASI reactor lifecycle gate")
	}
	cc := os.Getenv("CLANG")
	if cc == "" {
		cc = "clang"
	}
	dir := t.TempDir()
	image := filepath.Join(dir, "operations.wasm")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, cc, "--target=wasm32-wasip1", "--sysroot="+sysroot,
		"-O2", "-ffunction-sections", "-fdata-sections", "-nodefaultlibs", "-lc",
		"-std=c11", "-Wall", "-Wextra", "-Werror", "-o", image,
		filepath.Join("testdata", "coro_wasi_reactor", "operations.c"))
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("compile actual WASI reactor: %v\n%s", err, output)
	}
	runCtx, runCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer runCancel()
	run := exec.CommandContext(runCtx, "wasmtime", "run", "--dir="+dir+"::/tmp", image)
	if output, err := run.CombinedOutput(); err != nil {
		t.Fatalf("run actual WASI reactor lifecycle: %v\n%s", err, output)
	} else if string(output) != "reactor stdout ok\n" {
		t.Fatalf("inherited stdout did not complete: %q", output)
	}
}
