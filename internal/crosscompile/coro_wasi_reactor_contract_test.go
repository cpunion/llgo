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
		"llgo_host_wasi_fd_pread_v1 = (3u << 16) | 3u",
		"llgo_host_wasi_fd_pwrite_v1 = (3u << 16) | 4u",
		"error = __wasi_fd_pread(descriptor,",
		"error = __wasi_fd_pwrite(descriptor,",
		"LLGO_WASI_IMPORT(path_open)",
		"llgo_wasi_path_open_v1(ARG(0), ARG(1),",
		"uint64_t capabilities[2]",
		"buffer_operation ? 3 : argument_counts[verb]",
		"operation->arg_count != arg_count",
		"llgo_host_file_read_v1 = (1u << 16) | 2u",
		"llgo_host_file_write_v1 = (1u << 16) | 3u",
		"return llgo_fail_operation_v1(operation, ENOSYS)",
	} {
		if !strings.Contains(reactor, required) {
			t.Fatalf("WASI command reactor lacks contract %q", required)
		}
	}
	for _, forbidden := range []string{
		"pthread_", "uv_", "GC_", "ASYNCIFY", "asyncify",
		"malloc(", "calloc(", "realloc(", "free(",
		"llgo_copy_path", "llgo_translate_open_flags", "llgo_host_file_open_", "llgo_host_file_unlink_",
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
	positioned := read("internal/build/_testgo/coro_host_file_positional/main.go")
	for _, required := range []string{
		"syscall.Pread(", "syscall.Pwrite(", "1<<32|3", "syscall.EINVAL",
		"syscall.EBADF", "syscall.Seek(fd, 0, 1)", "positional short read",
	} {
		if !strings.Contains(positioned, required) {
			t.Fatalf("positional I/O fixture lacks %q", required)
		}
	}
}
