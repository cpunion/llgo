//go:build !llgo

package build

import (
	stdcontext "context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func coroResourceAcceptanceImage(t *testing.T, fixture string) string {
	t.Helper()
	dir := t.TempDir()
	if output := os.Getenv("LLGO_CORO_RESOURCE_OUTPUT"); output != "" {
		dir = filepath.Join(output, fixture)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "resource")
}

func TestCoroResourceAcceptanceImageRetention(t *testing.T) {
	t.Setenv("LLGO_CORO_RESOURCE_OUTPUT", "")
	if image := coroResourceAcceptanceImage(t, "ordinary"); filepath.Base(image) != "resource" {
		t.Fatal("unexpected temporary image:", image)
	}
	output := t.TempDir()
	t.Setenv("LLGO_CORO_RESOURCE_OUTPUT", output)
	for _, fixture := range []string{"coro_resource_borrowed_fd", "coro_resource_delivery_slot", "coro_resource_delivery_forward"} {
		image := coroResourceAcceptanceImage(t, fixture)
		if image != filepath.Join(output, fixture, "resource") {
			t.Fatal("unexpected retained image:", image)
		}
		if info, err := os.Stat(filepath.Dir(image)); err != nil || !info.IsDir() {
			t.Fatalf("retained image parent: %v, %v", info, err)
		}
	}
}

// This is a physical borrowed-output/defer gate, not a claim that arbitrary
// syscall.Open results already have cancellation-safe child-to-parent delivery.
// Shared-machine callers must bound the process tree's RSS. The dedicated
// Linux CI step additionally caps address space, including LLVM allocations.
func TestCoroBorrowedResourceCleanupAcceptance(t *testing.T) {
	if os.Getenv("LLGO_CORO_RESOURCE_ACCEPTANCE") != "1" {
		t.Skip("set LLGO_CORO_RESOURCE_ACCEPTANCE=1 for linked borrowed-resource cleanup acceptance")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("native borrowed-resource fixture requires POSIX descriptors")
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
	image := coroResourceAcceptanceImage(t, "coro_resource_borrowed_fd")
	ctx, cancel := stdcontext.WithTimeout(stdcontext.Background(), 10*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, compiler, "build", "-p=1", "-o", image, "./_testgo/coro_resource_borrowed_fd")
	build.Dir = filepath.Join(root, "runtime")
	build.Env = append(os.Environ(), "LLGO_ROOT="+root, "LLGO_BUILD_CACHE=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("borrowed resource build: %v\n%s", err, output)
	}
	for i := 0; i < 10; i++ {
		runCtx, runCancel := stdcontext.WithTimeout(stdcontext.Background(), 30*time.Second)
		output, err := exec.CommandContext(runCtx, image).CombinedOutput()
		runCancel()
		if err != nil || strings.TrimSpace(string(output)) != "borrowed fd cancellation and asynchronous cleanup ok" {
			t.Fatalf("borrowed resource cleanup run %d: %v\n%s", i, err, output)
		}
	}
}

// The producer writes an actual fd into the caller-owned result slot before
// its foreign operation completes. These modes distinguish Go's normal
// discarded-result semantics from cancellation/panic custody, including a stop
// arriving between two asynchronous steps of the internal drop.
func TestCoroResourceDeliverySlotAcceptance(t *testing.T) {
	testCoroResourceDeliveryAcceptance(t, "coro_resource_delivery_slot", []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15})
}

func TestCoroResourceDeliveryForwardAcceptance(t *testing.T) {
	testCoroResourceDeliveryAcceptance(t, "coro_resource_delivery_forward", []int{1, 2, 3, 4, 6, 7, 9, 12, 13, 14, 15, 16, 17})
}

func testCoroResourceDeliveryAcceptance(t *testing.T, fixture string, modes []int) {
	t.Helper()
	if os.Getenv("LLGO_CORO_RESOURCE_ACCEPTANCE") != "1" {
		t.Skip("set LLGO_CORO_RESOURCE_ACCEPTANCE=1 for linked typed resource delivery acceptance")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("native resource delivery fixture requires POSIX descriptors")
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
	image := coroResourceAcceptanceImage(t, fixture)
	ctx, cancel := stdcontext.WithTimeout(stdcontext.Background(), 10*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, compiler, "build", "-p=1", "-o", image, "./_testgo/"+fixture+"/main.go")
	build.Dir = filepath.Join(root, "runtime")
	build.Env = append(os.Environ(), "LLGO_ROOT="+root, "LLGO_BUILD_CACHE=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("resource delivery build: %v\n%s", err, output)
	}
	expectedByMode := []string{
		"typed resource delivery normal, discarded and canceled passed",
		"typed resource drop survives late cancellation",
		"typed resource delivery normal, discarded and canceled passed",
		"typed resource delivery normal, discarded and canceled passed", // Source defer.
		"typed resource delivery normal, discarded and canceled passed", // Range-yield owner.
		"typed resource delivery normal, discarded and canceled passed", // Second static site.
		"typed resource delivery normal, discarded and canceled passed", // Captured dynamic call.
		"typed resource delivery normal, discarded and canceled passed", // Captured dynamic looped defer.
		"typed resource delivery normal, discarded and canceled passed", // Captured discarded entry.
		"typed resource delivery normal, discarded and canceled passed", // Normal captured result, then cancellation.
		"typed resource delivery normal, discarded and canceled passed", // External range-owner static resource defer.
		"typed resource delivery normal, discarded and canceled passed", // External range-owner captured resource defer.
		"typed resource delivery normal, discarded and canceled passed", // Large result with managed pointer, canceled call.
		"typed resource delivery normal, discarded and canceled passed", // Large independent fallback.
		"typed resource delivery normal, discarded and canceled passed", // Large static resource defer.
		"typed resource delivery normal, discarded and canceled passed", // Large captured dynamic resource defer.
		"cross-P shutdown topology ready\ntyped resource delivery normal, discarded and canceled passed",
		"cross-P shutdown topology ready\ntyped resource delivery normal, discarded and canceled passed", // Resize during shutdown cleanup.
	}
	for _, mode := range modes {
		expected := expectedByMode[mode]
		t.Run(strconv.Itoa(mode), func(t *testing.T) {
			repetitions := 10
			if fixture == "coro_resource_delivery_forward" && mode == 3 {
				// This source-defer shutdown route has timed out intermittently
				// on P=2 CI. Keep the same image, assertions and per-run deadline.
				repetitions = 128
			}
			ownerCounts := []string{"1", "2"}
			if mode == 16 || mode == 17 {
				ownerCounts = []string{"2"} // The fixture proves P1 -> worker -> P2 cleanup.
			}
			for _, owners := range ownerCounts {
				for attempt := 0; attempt < repetitions; attempt++ {
					runCtx, runCancel := stdcontext.WithTimeout(stdcontext.Background(), 30*time.Second)
					run := exec.CommandContext(runCtx, image)
					run.Env = append(os.Environ(), "GOMAXPROCS="+owners, "LLGO_DELIVERY_FIXTURE_MODE="+strconv.Itoa(mode))
					output, err := run.CombinedOutput()
					deadline := runCtx.Err()
					runCancel()
					if err != nil || strings.TrimSpace(string(output)) != expected {
						t.Fatalf("resource delivery P=%s run %d: %v, deadline: %v, image: %s\n%s", owners, attempt, err, deadline, image, output)
					}
				}
			}
		})
	}
	if fixture != "coro_resource_delivery_slot" {
		return
	}
	t.Run("opaque-nonresource-callable", func(t *testing.T) {
		image := filepath.Join(t.TempDir(), "opaque-callable")
		build := exec.CommandContext(ctx, compiler, "build", "-p=1", "-o", image, "./_testgo/coro_dynamic_delivery_predicate/main.go")
		build.Dir = filepath.Join(root, "runtime")
		build.Env = append(os.Environ(), "LLGO_ROOT="+root, "LLGO_BUILD_CACHE=off")
		if output, err := build.CombinedOutput(); err != nil {
			t.Fatalf("opaque callable predicate build: %v\n%s", err, output)
		}
		for _, owners := range []string{"1", "2"} {
			for attempt := 0; attempt < 3; attempt++ {
				runCtx, runCancel := stdcontext.WithTimeout(stdcontext.Background(), 30*time.Second)
				run := exec.CommandContext(runCtx, image)
				run.Env = append(os.Environ(), "GOMAXPROCS="+owners)
				output, err := run.CombinedOutput()
				runCancel()
				if err != nil || strings.TrimSpace(string(output)) != "opaque callable predicate closure ok" {
					t.Fatalf("opaque callable P=%s run %d: %v\n%s", owners, attempt, err, output)
				}
			}
		}
	})
}

// The Node gate consumes the real private borrowed-output primitive. Public
// syscall.Open/os.File return delivery is deliberately outside this contract.
func TestCoroJSBorrowedResourceCleanupAcceptance(t *testing.T) {
	if os.Getenv("LLGO_CORO_JS_RESOURCE_ACCEPTANCE") != "1" {
		t.Skip("set LLGO_CORO_JS_RESOURCE_ACCEPTANCE=1 for real Node borrowed-resource acceptance")
	}
	compiler := os.Getenv("LLGO_TEST_LLGO")
	if compiler == "" {
		t.Fatal("LLGO_TEST_LLGO must identify the compiler being accepted")
	}
	compiler, err := filepath.Abs(compiler)
	if err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	image := filepath.Join(t.TempDir(), "borrowed-resource.wasm")
	ctx, cancel := stdcontext.WithTimeout(stdcontext.Background(), 10*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, compiler, "build", "-p=1", "-target=wasm", "-o", image, "./_testgo/coro_js_resource_borrowed_fd")
	build.Dir = filepath.Join(root, "runtime")
	build.Env = append(os.Environ(), "LLGO_ROOT="+root, "LLGO_BUILD_CACHE=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("Node borrowed resource build: %v\n%s", err, output)
	}
	for i := 0; i < 3; i++ {
		runCtx, runCancel := stdcontext.WithTimeout(stdcontext.Background(), 30*time.Second)
		output, err := exec.CommandContext(runCtx, node, "--max-old-space-size=256", "--unhandled-rejections=strict",
			filepath.Join(root, "internal/crosscompile/testdata/coro_js_fs/borrowed_file.cjs"),
			filepath.Join(root, "targets/wasm_exec.js"), image).CombinedOutput()
		runCancel()
		if err != nil || strings.TrimSpace(string(output)) != "Node borrowed fd cancellation and asynchronous cleanup ok" {
			t.Fatalf("Node borrowed resource cleanup run %d: %v\n%s", i, err, output)
		}
	}
}
