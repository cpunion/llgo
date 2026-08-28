//go:build !llgo
// +build !llgo

package build

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/xgo-dev/llgo/internal/packages"
)

func TestSelectedSFilesSkipsTestAsm(t *testing.T) {
	dir := "/tmp/pkg"
	got := selectedSFiles([]string{
		filepath.Join(dir, "abi_test.s"),
		filepath.Join(dir, "stub.s"),
		filepath.Join(dir, "helper.S"),
		filepath.Join(dir, "compare_test.S"),
		filepath.Join(dir, "helper.c"),
	})
	want := []string{
		filepath.Join(dir, "stub.s"),
		filepath.Join(dir, "helper.S"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("selectedSFiles() = %#v, want %#v", got, want)
	}
}

func TestPlan9AsmSFilesExcludesCPreprocessedAssembly(t *testing.T) {
	dir := "/tmp/pkg"
	got := plan9AsmSFiles([]string{
		filepath.Join(dir, "asm_arm64.s"),
		filepath.Join(dir, "gcc_arm64.S"),
	})
	want := []string{filepath.Join(dir, "asm_arm64.s")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("plan9AsmSFiles() = %#v, want %#v", got, want)
	}
}

func TestSelectedSFilesHandlesEmptyInput(t *testing.T) {
	if got := selectedSFiles(nil); got != nil {
		t.Fatalf("selectedSFiles(nil files) = %#v, want nil", got)
	}
	if got := selectedSFiles([]string{"helper.c"}); got != nil {
		t.Fatalf("selectedSFiles(non-assembly files) = %#v, want nil", got)
	}
}

func TestShouldSkipPlan9AsmSFilesForTarget(t *testing.T) {
	if !shouldSkipPlan9AsmSFilesForTarget(&Config{Target: "cortex-m-qemu", Goarch: "arm"}, "syscall") {
		t.Fatal("embedded arm syscall asm should be skipped")
	}
	if shouldSkipPlan9AsmSFilesForTarget(&Config{Target: "", Goarch: "arm"}, "syscall") {
		t.Fatal("host arm syscall asm should not be skipped")
	}
	if shouldSkipPlan9AsmSFilesForTarget(&Config{Target: "cortex-m-qemu", Goarch: "arm64"}, "syscall") {
		t.Fatal("arm64 syscall asm should not be skipped by arm-only rule")
	}
	if shouldSkipPlan9AsmSFilesForTarget(&Config{Target: "cortex-m-qemu", Goarch: "arm"}, "internal/bytealg") {
		t.Fatal("only syscall asm should be skipped by embedded arm rule")
	}
	if !shouldSkipPlan9AsmSFilesForTarget(&Config{Goarch: "arm64"}, "internal/chacha8rand") {
		t.Fatal("native chacha8rand assembly should be replaced by its pure-Go source patch")
	}
	if shouldSkipPlan9AsmSFilesForTarget(&Config{Goarch: "wasm"}, "internal/chacha8rand") {
		t.Fatal("native wasm chacha8rand should retain its standard Go implementation")
	}
	wasip2 := &Config{
		Target:                  "wasip2",
		Goarch:                  "arm",
		resolvedTargetBuildTags: []string{"tinygo.wasm", "wasip2"},
	}
	if !shouldSkipPlan9AsmSFilesForTarget(wasip2, "internal/chacha8rand") {
		t.Fatal("named WebAssembly chacha8rand trampoline should be replaced by its pure-Go source patch")
	}
	if !shouldSkipPlan9AsmSFilesForTarget(wasip2, "internal/runtime/syscall/linux") {
		t.Fatal("named WebAssembly Linux syscall assembly should be replaced by its fail-closed source patch")
	}
	if shouldSkipPlan9AsmSFilesForTarget(wasip2, "internal/bytealg") {
		t.Fatal("named WebAssembly bytealg assembly must remain available for LLVM translation")
	}
}

func TestPlan9AsmEnabledByDefaultForNamedWebAssemblyBytealg(t *testing.T) {
	conf := &Config{
		Target:                  "wasip2",
		Goos:                    "linux",
		Goarch:                  "arm",
		resolvedTargetBuildTags: []string{"tinygo.wasm", "wasip2"},
	}
	if !plan9asmEnabledByDefault(conf, "internal/bytealg") {
		t.Fatal("named WebAssembly target must translate its selected ARM internal/bytealg assembly")
	}
	if plan9asmEnabledByDefault(conf, "internal/chacha8rand") {
		t.Fatal("named WebAssembly target unexpectedly enabled unrelated ARM assembly")
	}
	if plan9asmEnabledByDefault(&Config{Goos: "linux", Goarch: "arm"}, "internal/bytealg") {
		t.Fatal("ordinary ARM target unexpectedly changed its Plan9 assembly default")
	}
}

func TestPkgSFilesUsesLoadedOtherFiles(t *testing.T) {
	pkgDir := t.TempDir()
	sfile := filepath.Join(pkgDir, "asm_amd64.s")
	t.Setenv("PATH", t.TempDir()) // pkgSFiles must not invoke a second go list.

	ctx := &context{
		buildConf: &Config{Goos: "linux", Goarch: "amd64"},
	}
	got, err := pkgSFiles(ctx, &packages.Package{
		ID:         "example.com/asm",
		PkgPath:    "example.com/asm",
		Dir:        pkgDir,
		OtherFiles: []string{sfile, filepath.Join(pkgDir, "helper.c")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != sfile {
		t.Fatalf("pkgSFiles = %v, want [%s]", got, sfile)
	}
}

func TestPkgSFilesNonWasmChachaUsesSourcePatch(t *testing.T) {
	pkgDir := t.TempDir()
	sfile := filepath.Join(pkgDir, "chacha8_stub.s")
	if err := os.WriteFile(sfile, []byte("TEXT ·block(SB),NOSPLIT,$0-0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, conf := range []*Config{
		{
			Target:                  "wasip2",
			Goos:                    "linux",
			Goarch:                  "arm",
			resolvedTargetBuildTags: []string{"tinygo.wasm", "wasip2"},
		},
		{Goos: runtime.GOOS, Goarch: runtime.GOARCH},
	} {
		ctx := &context{
			buildConf: conf,
		}
		got, err := pkgSFiles(ctx, &packages.Package{
			ID:         "internal/chacha8rand",
			PkgPath:    "internal/chacha8rand",
			Dir:        pkgDir,
			OtherFiles: []string{sfile},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Fatalf("%s/%s chacha8rand SFiles = %v, want none", conf.Goos, conf.Goarch, got)
		}
	}
}

func TestPkgSFilesSkipsSyntheticTestMain(t *testing.T) {
	pkgDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(pkgDir, "asm.s"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := &context{
		mode:      ModeTest,
		buildConf: &Config{Goos: "linux", Goarch: "amd64"},
	}
	got, err := pkgSFiles(ctx, &packages.Package{
		ID:      "example.com/p.test",
		PkgPath: "example.com/p.test",
		Name:    "main",
		Dir:     pkgDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("pkgSFiles = %v, want nil", got)
	}
	if cached, ok := ctx.sfilesCache["example.com/p.test"]; !ok || cached != nil {
		t.Fatalf("synthetic test main cache entry = %v, %v; want nil, true", cached, ok)
	}
}

func TestPlan9AsmEnabledInitializesSelectedPackages(t *testing.T) {
	t.Setenv(llgoPlan9ASMPkgs, "example.com/first, example.com/second")
	ctx := &context{buildConf: &Config{}}
	if !ctx.plan9asmEnabled("example.com/first") {
		t.Fatal("selected Plan 9 assembly package was disabled")
	}
	if ctx.plan9asmEnabled("example.com/other") {
		t.Fatal("unselected Plan 9 assembly package was enabled")
	}
	if !ctx.plan9asmReady || ctx.plan9asmMode != plan9asmEnvSelected || !ctx.plan9asmPkgs["example.com/second"] {
		t.Fatalf("prepared Plan 9 assembly policy = mode %v, packages %v", ctx.plan9asmMode, ctx.plan9asmPkgs)
	}
}

func TestPkgSFilesRejectsNilFrozenCache(t *testing.T) {
	ctx := &context{sfilesFrozen: true}
	_, err := pkgSFiles(ctx, &packages.Package{
		ID:      "example.com/unprepared",
		PkgPath: "example.com/unprepared",
	})
	if err == nil {
		t.Fatal("nil frozen SFiles cache accepted an unprepared package")
	}
}
