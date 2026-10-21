package build

import (
	"crypto/sha256"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/env"
)

func TestSourcePatchRequirementPreservesOriginalOnMismatch(t *testing.T) {
	const original = "package demo\ntype WaitGroup struct{}\nfunc (*WaitGroup) Wait() {}\n"
	for _, test := range []struct {
		name, disk, overlay string
		want                bool
	}{
		{"exact", original, "", true},
		{"changed", original + "// updated SDK\n", "", false},
		{"missing", "", "", false},
		{"changed-overlay", original, original + "// user overlay\n", false},
		{"exact-overlay", original + "// changed disk\n", original, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			goroot, runtimeDir := t.TempDir(), t.TempDir()
			srcDir := filepath.Join(goroot, "src", "demo")
			if err := os.MkdirAll(srcDir, 0755); err != nil {
				t.Fatal(err)
			}
			filename := filepath.Join(srcDir, "waitgroup.go")
			if test.disk != "" {
				mustWriteFile(t, filename, test.disk)
			}
			patch := fmt.Sprintf("//llgo:require-source waitgroup.go %x\npackage demo\nfunc (*WaitGroup) Wait() {}\n", sha256.Sum256([]byte(original)))
			mustWriteFile(t, filepath.Join(runtimeDir, "_patch", "demo", "wait.go"), patch)
			var base map[string][]byte
			if test.overlay != "" {
				base = map[string][]byte{filename: []byte(test.overlay)}
			}
			changed, overlay, files, err := applySourcePatchForPkg(base, nil, runtimeDir, goroot, "demo", sourcePatchBuildContext{goos: "linux", goarch: "amd64"})
			if err != nil {
				t.Fatal(err)
			}
			if changed != test.want || (len(files) == 1) != test.want {
				t.Fatalf("changed=%t files=%v, want replacement=%t", changed, files, test.want)
			}
			if !test.want {
				if len(overlay) != 0 {
					t.Fatal("rejected patch published an override or skip")
				}
				return
			}
			file, err := parser.ParseFile(token.NewFileSet(), filename, overlay[filename], 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, decl := range file.Decls {
				if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "Wait" {
					t.Fatal("admitted patch kept the original method too")
				}
			}
			injected := string(overlay[filepath.Join(srcDir, "z_llgo_patch_wait.go")])
			if strings.Contains(injected, "//llgo:require-source") || !strings.Contains(injected, "//llgo_require-source") {
				t.Fatal("loader requirement reached the frontend as a compiler directive")
			}
		})
	}
}

func TestSourcePatchRequirementRejectsMalformedDirective(t *testing.T) {
	const digest = "274e43a7cbc76eb0da4635a2680785200dfaf94d85e2d55cdd5122c76a4791d9"
	for _, directive := range []string{
		"require-source", "require-source waitgroup.go", "require-source waitgroup.go 00",
		"require-source waitgroup.go " + strings.Repeat("z", 64),
		"require-source ../waitgroup.go " + digest, "require-source /waitgroup.go " + digest,
		"require-source ..\\waitgroup.go " + digest, "require-source waitgroup.s " + digest,
		"require-source C:waitgroup.go " + digest,
		"require-source waitgroup.go " + digest + " extra",
	} {
		for _, prefix := range []string{"//llgo:", "// llgo:"} {
			if _, err := collectSourcePatchDirectives([]byte(prefix + directive + "\npackage demo\n")); err == nil {
				t.Fatalf("accepted malformed directive %q", prefix+directive)
			}
		}
	}
}

func TestCoroWaitGroupPrewaitPatchTargets(t *testing.T) {
	const native = "llgo,llgo_coro,llgo_coro_native_pipe,llgo_coro_native_timer"
	for _, test := range []struct {
		name, goos, arch, version, tags string
		want                            bool
	}{
		{"darwin", "darwin", "arm64", "go1.27.0", native, true},
		{"linux", "linux", "amd64", "go1.27.0", native, true},
		{"changed-release", "linux", "amd64", "go1.28.0", native, false},
		{"older-release", "linux", "amd64", "go1.26.0", native, false},
		{"adapter", "linux", "amd64", "go1.27.0", native + ",coro_runtime_adapter_test", false},
		{"missing-timer", "linux", "amd64", "go1.27.0", "llgo,llgo_coro,llgo_coro_native_pipe", false},
		{"ordinary", "linux", "amd64", "go1.27.0", "llgo", false},
		{"baremetal", "linux", "arm", "go1.27.0", native + ",baremetal", false},
		{"wasm", "js", "wasm", "go1.27.0", "llgo,llgo_coro", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, err := newSourcePatchMatchContext("", sourcePatchBuildContext{
				goos: test.goos, goarch: test.arch, goversion: test.version,
				buildFlags: []string{"-tags=" + test.tags},
			})
			if err != nil {
				t.Fatal(err)
			}
			got, err := ctx.MatchFile(filepath.Join(env.LLGoRuntimeDir(), "_patch", "sync"), "waitgroup_coro_go127.go")
			if err != nil || got != test.want {
				t.Fatalf("patch selected=%t error=%v, want %t", got, err, test.want)
			}
		})
	}
}
