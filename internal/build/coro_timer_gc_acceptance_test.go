//go:build !llgo

package build

import (
	stdcontext "context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The raw observer's host race tests cannot prove collector reachability,
// owner wakeup, manager retirement, or the standard library's channel contract.
func TestCoroTimerGCAcceptance(t *testing.T) {
	testCoroTimerGCAcceptance(t, false)
}

func TestCoroWASITimerGCAcceptance(t *testing.T) {
	testCoroTimerGCAcceptance(t, true)
}

func testCoroTimerGCAcceptance(t *testing.T, wasi bool) {
	t.Helper()
	optIn := "LLGO_CORO_TIMER_GC_ACCEPTANCE"
	if wasi {
		optIn = "LLGO_CORO_WASI_TIMER_GC_ACCEPTANCE"
	}
	if os.Getenv(optIn) != "1" {
		t.Skipf("set %s=1 to run the linked Timer/GC gate", optIn)
	}
	var wasmtime string
	if wasi {
		var err error
		wasmtime, err = exec.LookPath("wasmtime")
		if err != nil {
			t.Fatal("WASI Timer/GC acceptance requires wasmtime:", err)
		}
	}
	compiler := os.Getenv("LLGO_TEST_LLGO")
	if compiler == "" {
		t.Fatal("LLGO_TEST_LLGO must identify the compiler being accepted")
	}
	compiler, err := filepath.Abs(compiler)
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct{ name, output string }{
		{"coro_timer_gc_go127", "timer/ticker collection and manager retirement ok"},
		{"coro_synctest_timer_gc_go127", "synctest timer collection and retained ownership ok"},
		{"coro_stdlib_timer_go126", "standard Timer/Ticker semantics ok"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			imageDir := t.TempDir()
			// Opt-in CI/debug retention preserves the exact failed linked image,
			// including when the test process times out. A local test's ordinary
			// temporary directory is still reclaimed by testing.T.
			if output := os.Getenv("LLGO_CORO_TIMER_GC_OUTPUT"); output != "" {
				imageDir = filepath.Join(output, fixture.name)
				if err := os.MkdirAll(imageDir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			image := filepath.Join(imageDir, "timer")
			if wasi {
				image += ".wasm"
			}
			ctx, cancel := stdcontext.WithTimeout(stdcontext.Background(), 10*time.Minute)
			defer cancel()
			args := []string{"build", "-p=1", "-o", image}
			if wasi {
				args = append(args, "-target=wasip1-gc")
			}
			args = append(args, "./_testgo/"+fixture.name)
			build := exec.CommandContext(ctx, compiler, args...)
			build.Dir = filepath.Join(root, "runtime")
			build.Env = append(os.Environ(), "LLGO_ROOT="+root, "LLGO_BUILD_CACHE=off")
			if output, err := build.CombinedOutput(); err != nil {
				t.Fatalf("LLGo Timer/GC build: %v\n%s", err, output)
			}
			topologies, repetitions := []string{""}, 1
			if !wasi && fixture.name == "coro_timer_gc_go127" {
				// Collection and physical manager exit are separate owner turns.
				// Exercise both local and cross-P retirement using the same image.
				topologies, repetitions = []string{"1", "2"}, 8
			}
			for _, procs := range topologies {
				for iteration := 0; iteration < repetitions; iteration++ {
					runCtx, runCancel := stdcontext.WithTimeout(stdcontext.Background(), 30*time.Second)
					run := exec.CommandContext(runCtx, image)
					if wasi {
						run = exec.CommandContext(runCtx, wasmtime, "run", image)
					}
					run.Env = append(os.Environ(), "LLGO_TIMER_GC_CONTROL=")
					if procs != "" {
						run.Env = append(run.Env, "GOMAXPROCS="+procs)
					}
					output, err := run.CombinedOutput()
					deadline := runCtx.Err()
					runCancel()
					if err != nil || strings.TrimSpace(string(output)) != fixture.output {
						t.Fatalf("LLGo Timer/GC run (P=%s, iteration=%d): %v, deadline: %v\n%s", procs, iteration, err, deadline, output)
					}
				}
			}
			if !wasi && fixture.name == "coro_timer_gc_go127" {
				// The fixture must reject an actual strong Timer owner and an
				// extra parked G independently. Reuse the accepted image, limits
				// and assertions; neither control may pass through a timeout.
				for _, control := range []struct{ mode, failure string }{
					{"keep-timer", "unreachable future Timer/Ticker or channel remained strongly alive"},
					{"extra-g", "collected future timer retained parked manager until deadline"},
				} {
					t.Run("negative/"+control.mode, func(t *testing.T) {
						ctx, cancel := stdcontext.WithTimeout(stdcontext.Background(), 30*time.Second)
						defer cancel()
						run := exec.CommandContext(ctx, image)
						run.Env = append(os.Environ(), "GOMAXPROCS=2", "LLGO_TIMER_GC_CONTROL="+control.mode)
						output, err := run.CombinedOutput()
						if ctx.Err() != nil || err == nil || !strings.Contains(string(output), control.failure) {
							t.Fatalf("Timer/GC negative control %s: %v, deadline: %v\n%s", control.mode, err, ctx.Err(), output)
						}
					})
				}
			}
		})
	}
}
