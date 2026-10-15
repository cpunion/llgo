//go:build !llgo

package build

import (
	stdcontext "context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// This gate runs the actual standard-library timer image, not a host-Go
// adapter. Enable it with LLGO_CORO_BAREMETAL_ACCEPTANCE=1 and an explicitly
// built LLGO_TEST_LLGO. An outer process-tree resource guard may wrap go test.
func TestCoroBaremetalTimerAcceptance(t *testing.T) {
	if os.Getenv("LLGO_CORO_BAREMETAL_ACCEPTANCE") != "1" {
		t.Skip("set LLGO_CORO_BAREMETAL_ACCEPTANCE=1 for the LLVM22/QEMU target gate")
	}
	compiler := os.Getenv("LLGO_TEST_LLGO")
	if compiler == "" {
		t.Fatal("LLGO_TEST_LLGO must name the compiler under acceptance")
	}
	compiler, err := filepath.Abs(compiler)
	if err != nil {
		t.Fatal(err)
	}
	qemu, err := exec.LookPath("qemu-system-arm")
	if err != nil {
		t.Fatal("the explicit baremetal gate requires qemu-system-arm:", err)
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	image := filepath.Join(t.TempDir(), "timer.elf")
	ctx, cancel := stdcontext.WithTimeout(stdcontext.Background(), 10*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, compiler, "build", "-target=mps2-an385-qemu", "-o", image, "./internal/build/_testgo/coro_baremetal_timer")
	build.Dir = root
	build.Env = append(os.Environ(), "LLGO_ROOT="+root, "LLGO_BUILD_CACHE=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("baremetal timer build: %v\n%s", err, output)
	}
	// A retained cache uses the baremetal policy, not the desktop 16 KiB
	// wasm32/ARM table. Complete DCE of an unused cache is also valid.
	symbols, err := exec.CommandContext(ctx, "llvm-nm", "-S", "--defined-only", image).CombinedOutput()
	if err != nil {
		t.Fatalf("inspect baremetal cache size: %v\n%s", err, symbols)
	}
	for _, line := range strings.Split(string(symbols), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 4 || fields[3] != "runtime.funcForPCCache" {
			continue
		}
		size, err := strconv.ParseUint(fields[1], 16, 64)
		if err != nil || size > 32*4*4 {
			t.Fatalf("baremetal FuncForPC cache occupies %s bytes, want <=512: %v", fields[1], err)
		}
	}
	runCtx, runCancel := stdcontext.WithTimeout(stdcontext.Background(), 20*time.Second)
	defer runCancel()
	run := exec.CommandContext(runCtx, qemu, "-machine", "mps2-an385", "-semihosting", "-nographic", "-kernel", image)
	if output, err := run.CombinedOutput(); err != nil {
		t.Fatalf("baremetal timer host protocol: %v (deadline: %v)\n%s", err, runCtx.Err(), output)
	}
	// Exit zero is emitted only by the C embedding after it observes main
	// returning before completion and >=4 real deadline/continue transactions.
	// A HardFault exits nonzero; absent/default console output is never a pass.
}
