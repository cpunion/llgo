package build

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/env"
)

func TestJSFilesystemKeepsStandardGoBookkeeping(t *testing.T) {
	ctx := sourcePatchBuildContext{goos: "js", goarch: "wasm", goversion: runtime.Version(),
		buildFlags: []string{"-tags=llgo,llgo_coro,tinygo.wasm,wasm_unknown"}}
	changed, overlay, files, err := applySourcePatchForPkg(nil, nil, env.LLGoRuntimeDir(), runtime.GOROOT(), "syscall", ctx)
	if err != nil || !changed {
		t.Fatalf("apply JS syscall patch: changed=%v, error=%v", changed, err)
	}
	path := filepath.Join(runtime.GOROOT(), "src", "syscall", "fs_js.go")
	parsed, err := parser.ParseFile(token.NewFileSet(), path, overlay[path], 0)
	if err != nil {
		t.Fatal(err)
	}
	retained := make(map[string]bool)
	for _, decl := range parsed.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			retained[fn.Name.Name] = true
		}
	}
	for _, name := range []string{"Open", "Close", "Read", "Write", "Pread", "Pwrite", "Seek", "ReadDirent", "Getcwd", "fdToFile"} {
		if !retained[name] {
			t.Errorf("JS patch removed Go bookkeeping in %s", name)
		}
	}
	if retained["fsCall"] {
		t.Error("JS patch retained FuncOf/channel fsCall bridge")
	}
	if !strings.Contains(string(overlay[path]), `jsFD, err := fsCall("open", path, flags, perm)`) {
		t.Fatal("public Open must retain its standard path until full result delivery is implemented")
	}
	for _, file := range files {
		if strings.Contains(file, "file_webassembly.go") {
			t.Error("JS selected generic descriptor bypass wrappers")
		}
	}
	bridge := string(overlay[filepath.Join(runtime.GOROOT(), "src", "syscall", "z_llgo_patch_fs_js_coro.go")])
	if !strings.Contains(bridge, "syscall/js.llgoFSCall") || !strings.Contains(bridge, "mapJSError(failure)") {
		t.Fatal("JS filesystem bridge lost the typed target boundary or standard error mapping")
	}
	if strings.Contains(bridge, "llgoFSOpenInto") {
		t.Fatal("borrowed primitive alone must not silently replace public Open ownership")
	}
}

func TestJSFilesystemDoesNotSelectPOSIXHostPoll(t *testing.T) {
	for _, profile := range []struct{ goos, goarch string }{{"js", "wasm"}, {"wasip1", "wasm"}, {"linux", "arm"}} {
		ctx := sourcePatchBuildContext{goos: profile.goos, goarch: profile.goarch, goversion: runtime.Version(),
			buildFlags: []string{"-tags=llgo,llgo_coro,tinygo.wasm"}}
		for _, pkg := range []string{"internal/poll", "net"} {
			_, _, files, err := applySourcePatchForPkg(nil, nil, env.LLGoRuntimeDir(), runtime.GOROOT(), pkg, ctx)
			if err != nil {
				t.Fatal(err)
			}
			selected := false
			for _, file := range files {
				selected = selected || strings.Contains(file, "fd_unix_coro_host_llgo.go")
			}
			if selected != (profile.goos != "js") {
				t.Errorf("%s/%s selected %s POSIX host adapter = %v", profile.goos, profile.goarch, pkg, selected)
			}
		}
	}
}
