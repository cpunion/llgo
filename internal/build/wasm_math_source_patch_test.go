package build

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/env"
	llruntime "github.com/xgo-dev/llgo/runtime"
)

func TestWASMMathGenericSourcePatch(t *testing.T) {
	for _, arch := range []string{"wasm", "arm64"} {
		changed, overlay, files, err := applySourcePatchForPkg(nil, nil, env.LLGoRuntimeDir(), runtime.GOROOT(), "math", sourcePatchBuildContext{
			goos: "linux", goarch: arch, goversion: runtime.Version(), buildFlags: []string{"-tags=llgo,llgo_coro"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if arch != "wasm" {
			if changed || len(files) != 0 || llruntime.SourcePatchReplacesAsmForGOARCH("math", arch) {
				t.Fatal("WASM math adapter changed native source/assembly selection")
			}
			continue
		}
		if !changed || len(files) != 1 || !llruntime.SourcePatchReplacesAsmForGOARCH("math", arch) {
			t.Fatal("WASM math adapter did not select its exact source replacement")
		}
		source := string(overlay[filepath.Join(runtime.GOROOT(), "src", "math", "z_llgo_patch_floor_wasm.go")])
		for _, name := range []string{"Floor", "Ceil", "Trunc"} {
			if !strings.Contains(source, "const haveArch"+name+" = false") ||
				!strings.Contains(source, "return "+strings.ToLower(name)+"(x)") {
				t.Errorf("WASM math %s does not reuse the standard generic body", name)
			}
		}
		if original := string(overlay[filepath.Join(runtime.GOROOT(), "src", "math", "floor_wasm.s")]); original != "// replaced by LLGo source patch\n" {
			t.Fatalf("WASM assembly was not explicitly replaced: %q", original)
		}
	}
}
