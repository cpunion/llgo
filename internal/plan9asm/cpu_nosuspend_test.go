//go:build !llgo

package plan9asm

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/goplus/llgo/internal/packages"
)

func TestStdlibInternalCPUX86NoSuspendProof(t *testing.T) {
	goroot := runtime.GOROOT()
	if goroot == "" {
		t.Skip("GOROOT not available")
	}
	sfile := filepath.Join(goroot, "src", "internal", "cpu", "cpu_x86.s")
	source, err := os.ReadFile(sfile)
	if os.IsNotExist(err) {
		t.Skip("GOROOT has no internal/cpu x86 assembly")
	}
	if err != nil {
		t.Fatal(err)
	}

	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesSizes | packages.NeedTypesInfo |
			packages.NeedImports,
		Env: append(os.Environ(), "GOOS=linux", "GOARCH=amd64"),
	}
	pkgs, err := packages.LoadEx(nil, nil, cfg, "internal/cpu")
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 || pkgs[0].Types == nil {
		t.Fatalf("load internal/cpu: got %d packages", len(pkgs))
	}

	translation, err := TranslateSourceModuleForPkg(pkgs[0], sfile, source, "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	defer translation.Module.Dispose()
	for _, symbol := range []string{"internal/cpu.cpuid", "internal/cpu.xgetbv"} {
		if _, err := ProveNoSuspendLeaf(translation, symbol); err != nil {
			t.Fatalf("prove translated %s no-suspend leaf: %v", symbol, err)
		}
	}
}
