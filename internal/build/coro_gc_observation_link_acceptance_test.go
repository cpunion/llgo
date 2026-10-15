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

// The timer gate proves the enabled predicate. These real link/run probes prove
// its disabled side both without the timer package and with that package loaded.
func TestCoroGCObservationLinkAcceptance(t *testing.T) {
	if os.Getenv("LLGO_CORO_TIMER_GC_ACCEPTANCE") != "1" {
		t.Skip("set LLGO_CORO_TIMER_GC_ACCEPTANCE=1 to run the linked GC predicate gate")
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
	var nm string
	for _, candidate := range []string{"llvm-nm", "llvm-nm-22", "nm"} {
		if nm, err = exec.LookPath(candidate); err == nil {
			break
		}
	}
	if nm == "" {
		t.Fatal("linked code-size gate requires llvm-nm or nm")
	}
	for _, fixture := range []struct{ name, source string }{
		{"no-import", `package main; func main() { println("predicate link ok") }`},
		{"time-now", `package main; import "time"; func main() {
			if time.Now().Unix() <= 0 { panic("invalid clock") }; println("predicate link ok")
		}`},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			dir := t.TempDir()
			source, image := filepath.Join(dir, "main.go"), filepath.Join(dir, "main")
			if err := os.WriteFile(source, []byte(fixture.source), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := stdcontext.WithTimeout(stdcontext.Background(), 5*time.Minute)
			defer cancel()
			build := exec.CommandContext(ctx, compiler, "build", "-o", image, source)
			build.Dir = root
			build.Env = append(os.Environ(), "LLGO_ROOT="+root, "LLGO_BUILD_CACHE=off")
			if output, err := build.CombinedOutput(); err != nil {
				t.Fatalf("GC predicate link: %v\n%s", err, output)
			}
			runCtx, runCancel := stdcontext.WithTimeout(stdcontext.Background(), 30*time.Second)
			defer runCancel()
			if output, err := exec.CommandContext(runCtx, image).CombinedOutput(); err != nil || !strings.Contains(string(output), "predicate link ok") {
				t.Fatalf("GC predicate run: %v\n%s", err, output)
			}
			symbols, err := exec.CommandContext(runCtx, nm, image).CombinedOutput()
			if err != nil {
				t.Fatalf("inspect linked GC predicate symbols: %v\n%s", err, symbols)
			}
			for _, name := range []string{"coroTimerGC", "coroObserveGCV1", "coroGCObservation", "coroGCCompletedV1", "__llgo_coro_gc_notification", "__llgo_coro_gc_observe", "__llgo_coro_gc_completed"} {
				if strings.Contains(string(symbols), name) {
					t.Errorf("disabled GC service retained %q in final executable", name)
				}
			}
			if fixture.name == "time-now" {
				for _, name := range []string{"time.Now$coro", "time.runtimeNow$coro", "time.now$coro", "runtime.nanotime1$coro", "runtime.SynctestNow$coro"} {
					if strings.Contains(string(symbols), name) {
						t.Errorf("clock-only program retained unnecessary coroutine frame %q", name)
					}
				}
			}
		})
	}
}
