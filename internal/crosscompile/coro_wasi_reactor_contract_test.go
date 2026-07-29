//go:build !llgo

package crosscompile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWASICommandReactorUsesOneBoundedHostPullCore(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	read := func(path string) string {
		t.Helper()
		content, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		return string(content)
	}

	reactor := read("runtime/internal/runtime/_wrap/coro_wasi_command.c")
	for _, required := range []string{
		"__llgo_coro_host_profile_v1",
		"__llgo_coro_host_next_action_v1",
		"__llgo_coro_host_next_operation_v1",
		"__llgo_coro_host_publish_time_v1",
		"__llgo_coro_host_publish_wall_time_v1",
		"__llgo_coro_host_ack_cancel_v1",
		"__llgo_coro_host_continue_slice_v1",
		"__llgo_coro_host_complete_operation_v1",
		"__wasi_clock_time_get",
		"__wasi_poll_oneoff",
		"__WASI_EVENTTYPE_CLOCK",
		"__WASI_EVENTTYPE_FD_READ",
		"__WASI_EVENTTYPE_FD_WRITE",
		"__WASI_SUBCLOCKFLAGS_SUBSCRIPTION_CLOCK_ABSTIME",
		"llgo_host_operation_capacity_v1 = 64",
		"llgo_run_budget_v2 = 1024",
		"return llgo_fail_operation_v1(operation, ENOSYS)",
	} {
		if !strings.Contains(reactor, required) {
			t.Fatalf("WASI command reactor lacks contract %q", required)
		}
	}
	for _, forbidden := range []string{
		"pthread_", "uv_", "GC_", "ASYNCIFY", "asyncify",
		"malloc(", "calloc(", "realloc(", "free(",
	} {
		if strings.Contains(reactor, forbidden) {
			t.Fatalf("WASI command reactor contains forbidden second-runtime mechanism %q", forbidden)
		}
	}

	selector := read("runtime/internal/runtime/coro_wasi_command_c_llgo.go")
	if !strings.Contains(selector, "llgo && llgo_coro && wasip1") ||
		!strings.Contains(selector, `LLGoFiles = "_wrap/coro_wasi_command.c"`) {
		t.Fatal("WASI command reactor is not selected by its exact LLGo target contract")
	}

	toolchain := read("internal/crosscompile/crosscompile.go")
	for _, required := range []string{
		`"-nostartfiles"`,
		`"-Wl,--import-memory"`,
		`"-Wl,--export-memory"`,
	} {
		if !strings.Contains(toolchain, required) {
			t.Fatalf("WASI command link contract lacks %q", required)
		}
	}
	if strings.Contains(toolchain, `"-Wl,--import-memory,"`) {
		t.Fatal("WASI memory import retains the invalid trailing-comma spelling")
	}

	fixture := read("internal/build/_testgo/coro_wasi_command_reactor/main.go")
	for _, required := range []string{
		"go func()", "time.Sleep(20 * time.Millisecond)",
		"time.Sleep(250 * time.Millisecond)",
		"syscall.Open(", "syscall.Write(", "syscall.Seek(",
		"syscall.Read(", "syscall.Close(", "syscall.Unlink(",
	} {
		if !strings.Contains(fixture, required) {
			t.Fatalf("WASI command integration fixture lacks %q", required)
		}
	}
}
