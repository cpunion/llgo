package runtime

import (
	"go/build"
	"os"
	"strings"
	"testing"
)

func TestWebAssemblyExitHasExactProcessCapability(t *testing.T) {
	const dir = "internal/lib/runtime"
	for _, profile := range []struct {
		goos string
		tags []string
		wasi bool
	}{
		{"wasip1", []string{"llgo", "llgo_coro"}, true},
		{"js", []string{"llgo", "llgo_coro"}, false},
		{"linux", []string{"llgo", "llgo_coro", "tinygo.wasm", "wasip2"}, false},
		{"linux", []string{"llgo", "llgo_coro", "tinygo.wasm", "wasm_unknown"}, false},
	} {
		ctx := build.Default
		ctx.GOOS, ctx.GOARCH, ctx.BuildTags = profile.goos, "wasm", profile.tags
		for _, file := range []string{"exit_wasip1_llgo.go", "exit_webassembly_llgo.go"} {
			selected, err := ctx.MatchFile(dir, file)
			if err != nil {
				t.Fatal(err)
			}
			if selected != (profile.wasi == (file == "exit_wasip1_llgo.go")) {
				t.Errorf("%s/%v selected %s=%v", profile.goos, profile.tags, file, selected)
			}
		}
	}
	source, err := os.ReadFile(dir + "/exit_wasip1_llgo.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, required := range []string{
		"//go:wasmimport wasi_snapshot_preview1 proc_exit",
		"//llgo:coro noblock\nfunc syscallProcExit(code int32)",
		"//go:linkname syscall_Exit syscall.Exit",
		"syscallProcExit(int32(code))", "c_debugtrap()", "for {",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("WASI exit lacks %q", required)
		}
	}
	if strings.Contains(text, "coroHostOperation") {
		t.Error("terminal process exit must not create a pending asynchronous operation")
	}
}

func TestWebAssemblyLogicalArchitectureDoesNotSelectARMFacts(t *testing.T) {
	for _, profile := range []struct {
		name string
		tags []string
		arm  bool
	}{
		{"native-arm", nil, true},
		{"baremetal-arm", []string{"baremetal"}, true},
		{"wasm-unknown", []string{"tinygo.wasm", "wasm_unknown"}, false},
		{"wasip2", []string{"tinygo.wasm", "wasip2"}, false},
	} {
		ctx := build.Default
		ctx.GOOS, ctx.GOARCH = "linux", "arm"
		ctx.BuildTags = append([]string{"llgo", "llgo_coro"}, profile.tags...)
		for _, file := range []string{"zgoarch_arm.go", "zgoarch_webassembly.go"} {
			selected, err := ctx.MatchFile("internal/lib/runtime", file)
			if err != nil {
				t.Fatal(err)
			}
			if selected != (profile.arm == (file == "zgoarch_arm.go")) {
				t.Errorf("%s selected %s=%v", profile.name, file, selected)
			}
		}
	}
}
