//go:build !llgo

package runtime

import (
	"go/build"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestBaremetalRuntimeExcludesHostedBoundaryImplementations(t *testing.T) {
	ctx := build.Default
	ctx.GOOS, ctx.GOARCH, ctx.CgoEnabled = "linux", "arm", false
	ctx.BuildTags = []string{"llgo", "llgo_coro", "baremetal"}
	for _, test := range []struct {
		dir                 string
		required, forbidden []string
	}{
		{"internal/runtime", []string{"z_freestanding.go", "random_freestanding.go", "coro_abort_freestanding_webassembly.go"},
			[]string{"z_baremetal.go", "z_default.go", "random_libc.go", "coro_abort_libc.go"}},
		{"internal/clite/ffi", []string{"ffi_link_freestanding.go"}, []string{"ffi_link.go"}},
		{"internal/clite", []string{"stdio_baremetal.go"}, []string{"stdio_default.go", "stdio_darwin.go"}},
		{"internal/runtime/tinygogc", []string{"gc_link.go"}, []string{"gc_wasm.go"}},
	} {
		pkg, err := ctx.ImportDir(test.dir, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range test.required {
			if !slices.Contains(pkg.GoFiles, file) {
				t.Errorf("%s missing %s: %v", test.dir, file, pkg.GoFiles)
			}
		}
		for _, file := range test.forbidden {
			if slices.Contains(pkg.GoFiles, file) {
				t.Errorf("%s selected hosted file %s", test.dir, file)
			}
		}
	}
}

func TestBaremetalMemoryAndStdioStayWithinTargetBoundary(t *testing.T) {
	read := func(path string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.FromSlash(path))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	gc := read("internal/runtime/tinygogc/gc_link.go")
	for _, operation := range []string{"malloc", "free", "realloc", "calloc"} {
		if !strings.Contains(gc, "--wrap="+operation) || !strings.Contains(gc, "func __wrap_"+operation+"(") {
			t.Errorf("GC allocation domain is missing %s redirection", operation)
		}
	}
	stdio := read("internal/clite/stdio_baremetal.go")
	if strings.Contains(stdio, "Fopen(") || strings.Contains(stdio, "func init(") {
		t.Fatal("baremetal startup assumes hosted filesystem/stdio initialization")
	}
	for _, path := range []string{"internal/runtime/tinygogc/gc_tinygo.go", "internal/lib/runtime/runtime_gc_tinygogc.go"} {
		source := read(path)
		if strings.Contains(source, "c.Exit(") || !strings.Contains(source, "c.Trap()") {
			t.Errorf("%s bootstrap failure must not require a process exit ABI", path)
		}
	}
}
