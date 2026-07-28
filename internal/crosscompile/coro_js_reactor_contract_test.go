//go:build !llgo

package crosscompile

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/targets"
)

func TestJSWasmCommandUsesOnlyHostPullCoroutineReactor(t *testing.T) {
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

	runner := read("targets/wasm_exec.js")
	for _, required := range []string{
		"__llgo_coro_host_profile_v1",
		"__llgo_coro_host_next_action_v1",
		"__llgo_coro_host_publish_time_v1",
		"__llgo_coro_host_publish_wall_time_v1",
		"__llgo_coro_host_ack_cancel_v1",
		"__llgo_coro_host_continue_slice_v1",
		"__llgo_coro_host_next_operation_v1",
		"__llgo_coro_host_complete_operation_v1",
		"queueMicrotask",
		"setTimeout",
		"unsupported LLGo JavaScript host operation",
	} {
		if !strings.Contains(runner, required) {
			t.Fatalf("WebAssembly command runner lacks host-pull reactor contract %q", required)
		}
	}
	if got, want := strings.Fields(read("targets/wasm-undefined.txt")), []string{
		"syscall/js.emvalHostInvokeV1",
		"time.timezoneOffsetMinutes",
	}; !slices.Equal(got, want) {
		t.Fatalf("WebAssembly command import allowlist = %v, want exact host ABI %v", got, want)
	}

	for _, file := range []string{
		"internal/crosscompile/crosscompile.go",
		"targets/wasm.json",
		"targets/wasip1.json",
		"targets/wasip2.json",
		"targets/wasm_exec.js",
	} {
		content := read(file)
		for _, forbidden := range []string{"ASYNCIFY", "\"asyncify\"", "go_scheduler", "runtime.sleepTicks"} {
			if strings.Contains(content, forbidden) {
				t.Fatalf("%s retains obsolete stackful scheduler mechanism %q", file, forbidden)
			}
		}
	}

	config, err := targets.NewLoader(filepath.Join(root, "targets")).Load("wasm")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"--export=_start",
		"--export=main",
		"--export-memory",
		"--export=malloc",
		"--export=free",
		"--export=__llgo_coro_host_next_action_v1",
		"--export=__llgo_coro_host_profile_v1",
		"--export=__llgo_coro_host_next_deadline_v1",
		"--export=__llgo_coro_host_publish_time_v1",
		"--export=__llgo_coro_host_publish_wall_time_v1",
		"--export=__llgo_coro_host_ack_cancel_v1",
		"--export=__llgo_coro_host_continue_slice_v1",
		"--export=__llgo_coro_host_next_operation_v1",
		"--export=__llgo_coro_host_complete_operation_v1",
	} {
		if !slices.Contains(config.LDFlags, required) {
			t.Fatalf("wasm target lacks command-reactor link flag %q: %v", required, config.LDFlags)
		}
	}
}
