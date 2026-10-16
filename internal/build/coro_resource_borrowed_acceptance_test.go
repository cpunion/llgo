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
	image := filepath.Join(t.TempDir(), "borrowed-resource")
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
	image := filepath.Join(t.TempDir(), "resource-delivery")
	ctx, cancel := stdcontext.WithTimeout(stdcontext.Background(), 10*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, compiler, "build", "-p=1", "-o", image, "./_testgo/coro_resource_delivery_slot/main.go")
	build.Dir = filepath.Join(root, "runtime")
	build.Env = append(os.Environ(), "LLGO_ROOT="+root, "LLGO_BUILD_CACHE=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("resource delivery build: %v\n%s", err, output)
	}
	for mode, expected := range []string{
		"typed resource delivery normal, discarded and canceled passed",
		"typed resource drop survives late cancellation",
		"typed resource delivery normal, discarded and canceled passed",
		"typed resource delivery normal, discarded and canceled passed", // Source defer.
		"typed resource delivery normal, discarded and canceled passed", // Range-yield owner.
		"typed resource delivery normal, discarded and canceled passed", // Second static site.
	} {
		t.Run(strconv.Itoa(mode), func(t *testing.T) {
			for attempt := 0; attempt < 10; attempt++ {
				runCtx, runCancel := stdcontext.WithTimeout(stdcontext.Background(), 30*time.Second)
				run := exec.CommandContext(runCtx, image)
				run.Env = append(os.Environ(), "LLGO_DELIVERY_FIXTURE_MODE="+strconv.Itoa(mode))
				output, err := run.CombinedOutput()
				runCancel()
				if err != nil || strings.TrimSpace(string(output)) != expected {
					t.Fatalf("resource delivery run %d: %v\n%s", attempt, err, output)
				}
			}
		})
	}
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
