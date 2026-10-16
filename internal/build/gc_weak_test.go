package build

import (
	stdcontext "context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Exercise the actual C collector boundary: a live interior pointer, a static
// object, collection without a finalizer drain, then finalizer ordering. Go
// reachability tests alone cannot prove the collector's read/reveal protocol.
func TestBDWGCWeakClearsBeforeFinalization(t *testing.T) {
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang is unavailable")
	}
	flags, err := exec.Command("pkg-config", "--cflags", "--libs", "bdw-gc").Output()
	if err != nil {
		t.Skip("BDWGC development files are unavailable")
	}
	ctx, cancel := stdcontext.WithTimeout(stdcontext.Background(), 30*time.Second)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "weak")
	args := []string{"-O2", "testdata/gcweak/main.c", "../../runtime/internal/clite/bdwgc/_wrap/coro_allocator.c", "-o", binary}
	args = append(args, strings.Fields(string(flags))...)
	if output, err := exec.CommandContext(ctx, clang, args...).CombinedOutput(); err != nil {
		t.Fatalf("compile weak collector test: %v\n%s", err, output)
	}
	if output, err := exec.CommandContext(ctx, binary).CombinedOutput(); err != nil {
		t.Fatalf("weak collector lifecycle: %v\n%s", err, output)
	}
}
