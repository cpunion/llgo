package build

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/env"
	"github.com/goplus/llgo/internal/packages"
	llruntime "github.com/goplus/llgo/runtime"
)

func TestWasmRuntimeSourcePatchTypeChecks(t *testing.T) {
	for _, goos := range []string{"js", "wasip1"} {
		t.Run(goos, func(t *testing.T) {
			cfgEnv := append(os.Environ(), "GOOS="+goos, "GOARCH=wasm")
			goroot, goversion, err := env.GOROOTAndGOVERSIONWithEnv(cfgEnv)
			if err != nil {
				t.Fatal(err)
			}
			overlay, _, err := buildSourcePatchOverlayForGOROOT(nil, env.LLGoRuntimeDir(), goroot, sourcePatchBuildContext{
				goos:      goos,
				goarch:    "wasm",
				goversion: goversion,
			})
			if err != nil {
				t.Fatal(err)
			}

			pkgs, err := packages.LoadEx(nil, func(types.Sizes, string, string) types.Sizes {
				return &types.StdSizes{WordSize: 4, MaxAlign: 4}
			}, &packages.Config{
				Mode:    loadSyntax | packages.NeedDeps | packages.NeedModule | packages.NeedExportFile,
				Env:     cfgEnv,
				Fset:    token.NewFileSet(),
				Overlay: overlay,
			}, "runtime")
			if err != nil {
				t.Fatal(err)
			}
			if len(pkgs) != 1 {
				t.Fatalf("loaded %d runtime packages, want 1", len(pkgs))
			}
			if pkgs[0].IllTyped {
				logPackageErrors(t, pkgs[0], make(map[string]bool))
				t.Fatal("runtime did not type-check with wasm32 sizes")
			}
		})
	}
}

func TestWASIP1CoroSyscallMetadataSourcePatch(t *testing.T) {
	goroot := runtime.GOROOT()
	overlay, err := buildSourcePatchOverlayForGOROOT(
		nil,
		env.LLGoRuntimeDir(),
		goroot,
		sourcePatchBuildContext{
			goos:       "wasip1",
			goarch:     "wasm",
			goversion:  "go1.26",
			buildFlags: []string{"-tags=llgo,llgo_coro"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(goroot, "src", "syscall", "fs_wasip1.go")
	source, ok := overlay[path]
	if !ok {
		t.Fatalf("WASI Preview 1 coroutine metadata patch did not update %s", path)
	}
	text := string(source)
	for _, name := range []string{
		"fd_fdstat_get",
		"fd_fdstat_set_flags",
		"fd_prestat_get",
		"fd_prestat_dir_name",
	} {
		if !strings.Contains(text, "//llgo:coro noblock\nfunc "+name+"(") {
			t.Errorf("WASI Preview 1 metadata import %s lacks exact noblock annotation", name)
		}
	}
	for _, name := range []string{"fd_read", "fd_write", "sock_accept"} {
		if strings.Contains(text, "//llgo:coro noblock\nfunc "+name+"(") {
			t.Errorf("blocking WASI Preview 1 import %s acquired noblock annotation", name)
		}
	}
}

func logPackageErrors(t *testing.T, pkg *packages.Package, seen map[string]bool) {
	t.Helper()
	if pkg == nil || seen[pkg.ID] {
		return
	}
	seen[pkg.ID] = true
	for _, err := range pkg.Errors {
		t.Log(err)
	}
	for _, imported := range pkg.Imports {
		if imported.IllTyped {
			logPackageErrors(t, imported, seen)
		}
	}
}

func TestWasmBytealgSourcePatchReplacesAsm(t *testing.T) {
	for _, pkgPath := range []string{"internal/bytealg", "internal/chacha8rand", "internal/runtime/atomic"} {
		if !llruntime.HasSourcePatchPkg(pkgPath) {
			t.Fatalf("%s should be registered as a source patch package", pkgPath)
		}
		if !llruntime.SourcePatchReplacesAsmForGOARCH(pkgPath, "wasm") {
			t.Fatalf("%s wasm assembly should be replaced by its source patch", pkgPath)
		}
		if llruntime.SourcePatchReplacesAsmForGOARCH(pkgPath, "arm64") {
			t.Fatalf("%s native assembly should remain enabled", pkgPath)
		}
	}

	overlay, _, err := buildSourcePatchOverlayForGOROOT(nil, env.LLGoRuntimeDir(), runtime.GOROOT(), sourcePatchBuildContext{
		goos:      "js",
		goarch:    "wasm",
		goversion: runtime.Version(),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{
		"internal/bytealg/compare_wasm.s",
		"internal/bytealg/equal_wasm.s",
		"internal/bytealg/indexbyte_wasm.s",
		"internal/chacha8rand/chacha8_stub.s",
		"internal/runtime/atomic/atomic_wasm.s",
	} {
		path := filepath.Join(runtime.GOROOT(), "src", filepath.FromSlash(file))
		if got := string(overlay[path]); got != "// replaced by LLGo source patch\n" {
			t.Fatalf("overlay[%q] = %q, want assembly replacement", path, got)
		}
	}

	compareNative := filepath.Join(runtime.GOROOT(), "src", "internal", "bytealg", "compare_native.go")
	filtered, ok := overlay[compareNative]
	if !ok {
		t.Fatalf("missing filtered wasm bytealg source %s", compareNative)
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), compareNative, filtered, 0)
	if err != nil {
		t.Fatalf("parse filtered wasm bytealg source: %v", err)
	}
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == "abigen_runtime_cmpstring" {
			t.Fatal("wasm bytealg source retained the competing runtime.cmpstring ABI declaration")
		}
	}
}

func TestCompilePkgSFilesSkipsSourcePatchedAssembly(t *testing.T) {
	got, err := compilePkgSFiles(
		&context{buildConf: &Config{Goarch: "wasm"}},
		nil,
		&packages.Package{PkgPath: "internal/bytealg"},
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("compilePkgSFiles returned %v, want no object files", got)
	}
}

func TestSourcePatchAssemblyMatchError(t *testing.T) {
	goroot := t.TempDir()
	runtimeDir := t.TempDir()
	const pkgPath = "internal/bytealg"
	srcDir := filepath.Join(goroot, "src", filepath.FromSlash(pkgPath))
	patchDir := filepath.Join(runtimeDir, "_patch", filepath.FromSlash(pkgPath))

	if err := os.MkdirAll(filepath.Join(srcDir, "adir"), 0755); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(srcDir, "bad_wasm.s"), "//go:build (\n")
	mustWriteFile(t, filepath.Join(patchDir, "bytealg_wasm.go"), `//go:build wasm

package bytealg
`)

	_, _, _, err := applySourcePatchForPkg(nil, nil, runtimeDir, goroot, pkgPath, sourcePatchBuildContext{
		goos:   "js",
		goarch: "wasm",
	})
	if err == nil || !strings.Contains(err.Error(), "match stdlib assembly file") {
		t.Fatalf("applySourcePatchForPkg error = %v, want assembly match error", err)
	}
}

func TestBuildSourcePatchOverlayForIter(t *testing.T) {
	overlay, _, err := buildSourcePatchOverlayForGOROOT(nil, env.LLGoRuntimeDir(), runtime.GOROOT(), sourcePatchBuildContext{})
	if err != nil {
		t.Fatal(err)
	}

	iterDir := filepath.Join(runtime.GOROOT(), "src", "iter")
	patchFile := filepath.Join(iterDir, "z_llgo_patch_iter.go")
	patchSrc, ok := overlay[patchFile]
	if !ok {
		t.Fatalf("missing source patch file %s", patchFile)
	}
	if !strings.Contains(string(patchSrc), "func Pull[V any]") {
		t.Fatalf("source patch file %s does not contain iter replacement", patchFile)
	}
	if !strings.HasPrefix(string(patchSrc), sourcePatchLineDirective(filepath.Join(env.LLGoRuntimeDir(), "_patch", "iter", "iter.go"))) {
		t.Fatalf("source patch file %s is missing line directive, got:\n%s", patchFile, patchSrc)
	}

	stdFile := filepath.Join(iterDir, "iter.go")
	stdSrc, ok := overlay[stdFile]
	if !ok {
		t.Fatalf("missing stub overlay for %s", stdFile)
	}
	got := string(stdSrc)
	if !strings.Contains(got, "package iter") {
		t.Fatalf("stub overlay for %s lost package clause", stdFile)
	}
	if strings.Contains(got, "func Pull") {
		t.Fatalf("stub overlay for %s still contains original declarations", stdFile)
	}
}

func TestIterUsesSourcePatchInsteadOfAltPkg(t *testing.T) {
	if !llruntime.HasSourcePatchPkg("iter") {
		t.Fatal("iter should be registered as a source patch package")
	}
	if llruntime.HasAltPkg("iter") {
		t.Fatal("iter should not remain an alt package")
	}
}

func TestBuildSourcePatchOverlayForGo126Payloads(t *testing.T) {
	goroot := t.TempDir()
	mustWriteFile(t, filepath.Join(goroot, "src", "internal", "sync", "hashtriemap.go"), `package sync

type HashTrieMap[K comparable, V any] struct{}
`)
	mustWriteFile(t, filepath.Join(goroot, "src", "internal", "sync", "mutex.go"), `package sync

type Mutex struct{}
`)
	mustWriteFile(t, filepath.Join(goroot, "src", "crypto", "internal", "constanttime", "constant_time.go"), `package constanttime

func boolToUint8(bool) uint8
`)

	overlay, _, err := buildSourcePatchOverlayForGOROOT(nil, env.LLGoRuntimeDir(), goroot, sourcePatchBuildContext{
		goos:      runtime.GOOS,
		goarch:    runtime.GOARCH,
		goversion: "go1.26.0",
	})
	if err != nil {
		t.Fatal(err)
	}

	syncDir := filepath.Join(goroot, "src", "internal", "sync")
	syncPatch := filepath.Join(syncDir, "z_llgo_patch_hashtriemap.go")
	if src, ok := overlay[syncPatch]; !ok {
		t.Fatalf("missing source patch file %s", syncPatch)
	} else if !strings.Contains(string(src), "type HashTrieMap") {
		t.Fatalf("source patch file %s does not contain HashTrieMap replacement", syncPatch)
	}
	if stdSrc := string(overlay[filepath.Join(syncDir, "hashtriemap.go")]); strings.Contains(stdSrc, "type HashTrieMap") {
		t.Fatalf("stub overlay for internal/sync still contains HashTrieMap: %s", stdSrc)
	}

	constanttimeDir := filepath.Join(goroot, "src", "crypto", "internal", "constanttime")
	constanttimePatch := filepath.Join(constanttimeDir, "z_llgo_patch_constant_time.go")
	if src, ok := overlay[constanttimePatch]; !ok {
		t.Fatalf("missing source patch file %s", constanttimePatch)
	} else if !strings.Contains(string(src), "//go:linkname boolToUint8 llgo.boolToUint8") {
		t.Fatalf("source patch file %s does not contain boolToUint8 linkname", constanttimePatch)
	}
}

func TestGo126PayloadsUseSourcePatchInsteadOfAltPkg(t *testing.T) {
	for _, pkgPath := range []string{"internal/sync", "crypto/internal/constanttime"} {
		if !llruntime.HasSourcePatchPkg(pkgPath) {
			t.Fatalf("%s should be registered as a source patch package", pkgPath)
		}
		if llruntime.HasAltPkg(pkgPath) {
			t.Fatalf("%s should not remain an alt package", pkgPath)
		}
	}
}

func TestNativeCoroTimeSleepUsesSourcePatch(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("native coroutine time.Sleep patch requires Darwin or Linux")
	}
	overlay, err := buildSourcePatchOverlayForGOROOT(nil, env.LLGoRuntimeDir(), runtime.GOROOT(), sourcePatchBuildContext{
		goos:       runtime.GOOS,
		goarch:     runtime.GOARCH,
		buildFlags: []string{"-tags=llgo,llgo_coro,llgo_coro_native_pipe,llgo_coro_native_timer,nogc"},
	})
	if err != nil {
		t.Fatal(err)
	}
	timeDir := filepath.Join(runtime.GOROOT(), "src", "time")
	patchFile := filepath.Join(timeDir, "z_llgo_patch_sleep_coro_native_llgo.go")
	patch, ok := overlay[patchFile]
	if !ok {
		t.Fatalf("missing native coroutine time.Sleep patch %s", patchFile)
	}
	patchText := string(patch)
	for _, want := range []string{
		"func Sleep(d Duration)",
		"llgo.coroTimerSleep",
		"func llgoCoroTimerSleep(delay int64)",
		"//go:linkname llgoCoroTimerNewV1 runtime.llgoCoroTimerNewV1",
		"func llgoCoroTimerNewV1(when, period int64, f func(any, uintptr, int64), arg any, cp unsafe.Pointer) unsafe.Pointer",
		"//go:linkname llgoCoroTimerStopV1 runtime.llgoCoroTimerStopV1",
		"func llgoCoroTimerStopV1(timer unsafe.Pointer) bool",
		"//go:linkname llgoCoroTimerResetV1 runtime.llgoCoroTimerResetV1",
		"func llgoCoroTimerResetV1(timer unsafe.Pointer, when, period int64) bool",
		"func newTimer(when, period int64, f func(any, uintptr, int64), arg any, cp unsafe.Pointer) *Timer",
		"return (*Timer)(llgoCoroTimerNewV1(when, period, f, arg, cp))",
		"func stopTimer(timer *Timer) bool",
		"return llgoCoroTimerStopV1(unsafe.Pointer(timer))",
		"func resetTimer(timer *Timer, when, period int64) bool",
		"return llgoCoroTimerResetV1(unsafe.Pointer(timer), when, period)",
		"func AfterFunc(d Duration, f func()) *Timer",
		"return newTimer(when(d), 0, nil, f, nil)",
	} {
		if !strings.Contains(patchText, want) {
			t.Fatalf("native coroutine time patch does not contain %q", want)
		}
	}
	if count := strings.Count(patchText, "llgoCoroTimerSleep(int64(d))"); count != 1 {
		t.Fatalf("native coroutine time.Sleep patch intrinsic calls = %d, want 1", count)
	}
	for _, obsolete := range []string{
		"__llgo_coro_timer_prepare_after_or_abort_v1",
		"__llgo_coro_timer_retire_completed_or_abort_v1",
		"llgo.coroPark",
		"//llgo:coro noblock",
	} {
		if strings.Contains(patchText, obsolete) {
			t.Fatalf("native coroutine time.Sleep patch retained obsolete transaction %q", obsolete)
		}
	}
	for _, forbidden := range []string{"libuv", "bdwgc", "pthread", "make(chan", "go func", "goFunc"} {
		if strings.Contains(patchText, forbidden) {
			t.Fatalf("native coroutine time patch unexpectedly contains %q", forbidden)
		}
	}

	stdlibSleep := filepath.Join(timeDir, "sleep.go")
	patchedStdlib, ok := overlay[stdlibSleep]
	if !ok {
		t.Fatalf("native coroutine time.Sleep patch did not filter %s", stdlibSleep)
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), stdlibSleep, patchedStdlib, 0)
	if err != nil {
		t.Fatalf("parse filtered time/sleep.go: %v", err)
	}
	replaced := map[string]bool{
		"Sleep":      true,
		"AfterFunc":  true,
		"newTimer":   true,
		"stopTimer":  true,
		"resetTimer": true,
	}
	for _, decl := range parsed.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && replaced[fn.Name.Name] {
			t.Fatalf("filtered GOROOT time/sleep.go retained the original %s declaration", fn.Name.Name)
		}
	}
}

func TestNativeCoroTimeSleepPatchIsCapabilityGated(t *testing.T) {
	overlay, err := buildSourcePatchOverlayForGOROOT(nil, env.LLGoRuntimeDir(), runtime.GOROOT(), sourcePatchBuildContext{
		goos:       runtime.GOOS,
		goarch:     runtime.GOARCH,
		buildFlags: []string{"-tags=llgo,llgo_coro,llgo_coro_native_pipe,nogc"},
	})
	if err != nil {
		t.Fatal(err)
	}
	patchFile := filepath.Join(runtime.GOROOT(), "src", "time", "z_llgo_patch_sleep_coro_native_llgo.go")
	if _, ok := overlay[patchFile]; ok {
		t.Fatalf("native coroutine time.Sleep patch selected without timer capability: %s", patchFile)
	}
	if !llruntime.HasSourcePatchPkg("time") || llruntime.HasAltPkg("time") {
		t.Fatalf("time patch registration = source:%t alt:%t", llruntime.HasSourcePatchPkg("time"), llruntime.HasAltPkg("time"))
	}
}

func TestNamedWebAssemblyChacha8UsesPureGoSourcePatch(t *testing.T) {
	overlay, err := buildSourcePatchOverlayForGOROOT(nil, env.LLGoRuntimeDir(), runtime.GOROOT(), sourcePatchBuildContext{
		goos:       "linux",
		goarch:     "arm",
		buildFlags: []string{"-tags=llgo,llgo_coro,tinygo.wasm,wasip2,nogc"},
	})
	if err != nil {
		t.Fatal(err)
	}
	chachaDir := filepath.Join(runtime.GOROOT(), "src", "internal", "chacha8rand")
	patchFile := filepath.Join(chachaDir, "z_llgo_patch_block_freestanding_webassembly_llgo.go")
	patch, ok := overlay[patchFile]
	if !ok {
		t.Fatalf("missing named WebAssembly chacha8rand source patch %s", patchFile)
	}
	if !strings.Contains(string(patch), "block_generic(seed, blocks, counter)") {
		t.Fatalf("chacha8rand source patch does not call the standard generic implementation:\n%s", patch)
	}
	assembly := filepath.Join(chachaDir, "chacha8_stub.s")
	replacement, ok := overlay[assembly]
	if !ok {
		t.Fatalf("chacha8rand source patch did not mask %s", assembly)
	}
	if bytes.Contains(replacement, []byte("TEXT")) {
		t.Fatalf("chacha8rand assembly replacement retained an executable TEXT definition:\n%s", replacement)
	}
	original := filepath.Join(chachaDir, "chacha8.go")
	filtered, ok := overlay[original]
	if !ok {
		t.Fatalf("chacha8rand source patch did not filter %s", original)
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), original, filtered, 0)
	if err != nil {
		t.Fatalf("parse filtered chacha8rand source: %v", err)
	}
	for _, decl := range parsed.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "block" {
			t.Fatal("filtered GOROOT chacha8rand retained its assembly block declaration")
		}
	}
	if !llruntime.HasSourcePatchPkg("internal/chacha8rand") {
		t.Fatal("internal/chacha8rand should be registered as a source patch package")
	}
}

func TestNativeWasmFrontendDoesNotDuplicateChacha8SourcePatch(t *testing.T) {
	overlay, err := buildSourcePatchOverlayForGOROOT(nil, env.LLGoRuntimeDir(), runtime.GOROOT(), sourcePatchBuildContext{
		goos:       "js",
		goarch:     "wasm",
		buildFlags: []string{"-tags=llgo,llgo_coro,tinygo.wasm,nogc"},
	})
	if err != nil {
		t.Fatal(err)
	}
	chachaDir := filepath.Join(runtime.GOROOT(), "src", "internal", "chacha8rand")
	patchFile := filepath.Join(chachaDir, "z_llgo_patch_block_freestanding_webassembly_llgo.go")
	if _, ok := overlay[patchFile]; ok {
		t.Fatalf("native wasm frontend selected the freestanding non-wasm chacha8rand patch %s", patchFile)
	}
	wasmSource := filepath.Join(chachaDir, "chacha8_wasm.go")
	if replacement, ok := overlay[wasmSource]; ok {
		t.Fatalf("native wasm frontend unexpectedly filtered its standard chacha8rand body:\n%s", replacement)
	}
}

func TestJSWasmTimeZoneSourcePatchUsesScalarHostFact(t *testing.T) {
	overlay, err := buildSourcePatchOverlayForGOROOT(
		nil,
		env.LLGoRuntimeDir(),
		runtime.GOROOT(),
		sourcePatchBuildContext{
			goos:      "js",
			goarch:    "wasm",
			goversion: runtime.Version(),
			buildFlags: []string{
				"-tags=llgo,llgo_coro,tinygo.wasm,wasm_unknown",
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	timeDir := filepath.Join(runtime.GOROOT(), "src", "time")
	injectedPath := filepath.Join(timeDir, "z_llgo_patch_zoneinfo_js_coro_llgo.go")
	injected, ok := overlay[injectedPath]
	if !ok {
		t.Fatalf("missing JS/WASM time-zone patch %s", injectedPath)
	}
	for _, required := range []string{
		"//go:wasmimport llgo_js timezone_offset",
		"func initLocal()",
		"var platformZoneSources",
	} {
		if !strings.Contains(string(injected), required) {
			t.Errorf("JS/WASM time-zone patch lacks %q", required)
		}
	}

	originalPath := filepath.Join(timeDir, "zoneinfo_js.go")
	filtered, ok := overlay[originalPath]
	if !ok {
		t.Fatalf("JS/WASM time-zone patch did not filter %s", originalPath)
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), originalPath, filtered, 0)
	if err != nil {
		t.Fatalf("parse filtered JS/WASM time source: %v", err)
	}
	for _, imported := range parsed.Imports {
		if imported.Path.Value == `"syscall/js"` {
			t.Error("filtered JS/WASM time source retained the syscall/js import")
		}
	}
	for _, declaration := range parsed.Decls {
		switch declaration := declaration.(type) {
		case *ast.FuncDecl:
			if declaration.Name.Name == "initLocal" {
				t.Error("filtered JS/WASM time source retained initLocal")
			}
		case *ast.GenDecl:
			for _, spec := range declaration.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, name := range value.Names {
					if name.Name == "platformZoneSources" {
						t.Error("filtered JS/WASM time source retained platformZoneSources")
					}
				}
			}
		}
	}
}

func TestCoroBytealgCountUsesPreemptibleGoSourcePatch(t *testing.T) {
	overlay, err := buildSourcePatchOverlayForGOROOT(nil, env.LLGoRuntimeDir(), runtime.GOROOT(), sourcePatchBuildContext{
		goos:       "linux",
		goarch:     "amd64",
		buildFlags: []string{"-tags=llgo,llgo_coro,nogc"},
	})
	if err != nil {
		t.Fatal(err)
	}
	bytealgDir := filepath.Join(runtime.GOROOT(), "src", "internal", "bytealg")
	patchFile := filepath.Join(bytealgDir, "z_llgo_patch_count_coro_llgo.go")
	patch, ok := overlay[patchFile]
	if !ok {
		t.Fatalf("missing coroutine bytealg count source patch %s", patchFile)
	}
	for _, call := range []string{"countGeneric(b, c)", "countGenericString(s, c)"} {
		if !strings.Contains(string(patch), call) {
			t.Fatalf("bytealg source patch lacks standard fallback call %q:\n%s", call, patch)
		}
	}
	original := filepath.Join(bytealgDir, "count_native.go")
	filtered, ok := overlay[original]
	if !ok {
		t.Fatalf("bytealg source patch did not filter %s", original)
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), original, filtered, 0)
	if err != nil {
		t.Fatalf("parse filtered bytealg source: %v", err)
	}
	functions := make(map[string]bool)
	for _, declaration := range parsed.Decls {
		if function, ok := declaration.(*ast.FuncDecl); ok {
			functions[function.Name.Name] = true
		}
	}
	if functions["Count"] || functions["CountString"] {
		t.Fatal("filtered GOROOT bytealg retained assembly Count declarations")
	}
	if !functions["countGeneric"] || !functions["countGenericString"] {
		t.Fatal("filtered GOROOT bytealg lost its standard generic implementations")
	}
	assembly := filepath.Join(bytealgDir, "count_amd64.s")
	replacement, ok := overlay[assembly]
	if !ok {
		t.Fatalf("bytealg source patch did not mask %s", assembly)
	}
	if bytes.Contains(replacement, []byte("TEXT")) {
		t.Fatalf("bytealg assembly replacement retained executable Count definitions:\n%s", replacement)
	}
	if !llruntime.HasSourcePatchPkg("internal/bytealg") {
		t.Fatal("internal/bytealg should be registered as a source patch package")
	}
}

func TestNamedWebAssemblyInternalLinuxSyscallFailsClosed(t *testing.T) {
	overlay, err := buildSourcePatchOverlayForGOROOT(nil, env.LLGoRuntimeDir(), runtime.GOROOT(), sourcePatchBuildContext{
		goos:       "linux",
		goarch:     "arm",
		buildFlags: []string{"-tags=llgo,llgo_coro,tinygo.wasm,wasip2,nogc"},
	})
	if err != nil {
		t.Fatal(err)
	}
	syscallDir := filepath.Join(runtime.GOROOT(), "src", "internal", "runtime", "syscall", "linux")
	patchFile := filepath.Join(syscallDir, "z_llgo_patch_syscall_freestanding_webassembly_llgo.go")
	patch, ok := overlay[patchFile]
	if !ok {
		t.Fatalf("missing named WebAssembly internal syscall patch %s", patchFile)
	}
	if !strings.Contains(string(patch), "return ^uintptr(0), 0, enosys") {
		t.Fatalf("internal syscall patch does not fail closed with ENOSYS:\n%s", patch)
	}
	assembly := filepath.Join(syscallDir, "asm_linux_arm.s")
	replacement, ok := overlay[assembly]
	if !ok {
		t.Fatalf("internal syscall patch did not mask %s", assembly)
	}
	if bytes.Contains(replacement, []byte("TEXT")) || bytes.Contains(replacement, []byte("SWI")) {
		t.Fatalf("internal syscall assembly replacement retained executable ARM syscall code:\n%s", replacement)
	}
	original := filepath.Join(syscallDir, "syscall_linux.go")
	filtered, ok := overlay[original]
	if !ok {
		t.Fatalf("internal syscall patch did not filter %s", original)
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), original, filtered, 0)
	if err != nil {
		t.Fatalf("parse filtered internal syscall source: %v", err)
	}
	for _, decl := range parsed.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "Syscall6" {
			t.Fatal("filtered GOROOT internal syscall retained its assembly declaration")
		}
	}
	if !llruntime.HasSourcePatchPkg("internal/runtime/syscall/linux") {
		t.Fatal("internal/runtime/syscall/linux should be registered as a source patch package")
	}
}

func TestSyncAtomicRemainsAltPkg(t *testing.T) {
	if llruntime.HasSourcePatchPkg("sync/atomic") {
		t.Fatal("sync/atomic should not be registered as a source patch package")
	}
	if !llruntime.HasAltPkg("sync/atomic") {
		t.Fatal("sync/atomic should remain an alt package")
	}
}

func TestInternalRuntimeMapsRemainsAltPkg(t *testing.T) {
	if llruntime.HasSourcePatchPkg("internal/runtime/maps") {
		t.Fatal("internal/runtime/maps should not be registered as a source patch package")
	}
	if !llruntime.HasAltPkg("internal/runtime/maps") {
		t.Fatal("internal/runtime/maps should remain an alt package")
	}
}

func TestInternalRuntimeSysRemainsAltPkg(t *testing.T) {
	if llruntime.HasSourcePatchPkg("internal/runtime/sys") {
		t.Fatal("internal/runtime/sys should not be registered as a source patch package")
	}
	if !llruntime.HasAltPkg("internal/runtime/sys") {
		t.Fatal("internal/runtime/sys should remain an alt package")
	}
	if !llruntime.HasAdditiveAltPkg("internal/runtime/sys") {
		t.Fatal("internal/runtime/sys should remain an additive alt package")
	}
	if !llruntime.HasAltPkg("internal/syscall/unix") || !llruntime.HasAdditiveAltPkg("internal/syscall/unix") {
		t.Fatal("internal/syscall/unix coroutine capability declarations should use an additive alt package")
	}
	if !llruntime.HasAltPkgForTarget("internal/syscall/unix", "darwin", "arm64") ||
		!llruntime.HasAdditiveAltPkgForTarget("internal/syscall/unix", "darwin", "arm64") {
		t.Fatal("internal/syscall/unix coroutine capability declarations should be enabled on Darwin")
	}
	if llruntime.HasAltPkgForTarget("internal/syscall/unix", "linux", "arm64") ||
		llruntime.HasAdditiveAltPkgForTarget("internal/syscall/unix", "linux", "arm64") {
		t.Fatal("Darwin-only internal/syscall/unix declarations should not enter Linux builds")
	}
}

func TestDarwinCompatibilityFiles(t *testing.T) {
	runtimeDir := filepath.Join("..", "..", "runtime", "internal", "lib")
	syscallDir := filepath.Join(runtimeDir, "syscall")
	for _, version := range []string{"go1.24", "go1.25"} {
		buildCtx, err := newSourcePatchMatchContext("", sourcePatchBuildContext{
			goos: "darwin", goarch: "arm64", goversion: version,
		})
		if err != nil {
			t.Fatal(err)
		}
		matched, err := buildCtx.MatchFile(
			filepath.Join(runtimeDir, "internal", "syscall", "unix"),
			"compat_darwin_pre_go126.go",
		)
		if err != nil {
			t.Fatal(err)
		}
		if !matched {
			t.Errorf("internal/syscall/unix compatibility placeholder is excluded for %s", version)
		}
		matched, err = buildCtx.MatchFile(syscallDir, "syscall_darwin.go")
		if err != nil {
			t.Fatal(err)
		}
		if !matched {
			t.Errorf("Darwin public syscall bridge is excluded for %s", version)
		}
	}

	go126, err := newSourcePatchMatchContext("", sourcePatchBuildContext{
		goos: "darwin", goarch: "arm64", goversion: "go1.26",
	})
	if err != nil {
		t.Fatal(err)
	}
	for dir, name := range map[string]string{
		filepath.Join(runtimeDir, "internal", "syscall", "unix"): "compat_darwin_pre_go126.go",
	} {
		matched, err := go126.MatchFile(dir, name)
		if err != nil {
			t.Fatal(err)
		}
		if matched {
			t.Errorf("pre-Go 1.26 compatibility file %s unexpectedly selected for Go 1.26", name)
		}
	}
	matched, err := go126.MatchFile(syscallDir, "syscall_darwin.go")
	if err != nil {
		t.Fatal(err)
	}
	if !matched {
		t.Error("Darwin public syscall bridge is excluded for Go 1.26")
	}
}

func TestDarwinCoroProcessSyscallsUseIsolatedRawCarrier(t *testing.T) {
	overlay, err := buildSourcePatchOverlayForGOROOT(
		nil,
		env.LLGoRuntimeDir(),
		runtime.GOROOT(),
		sourcePatchBuildContext{
			goos:      "darwin",
			goarch:    "arm64",
			goversion: "go1.26",
			buildFlags: []string{
				"-tags=llgo,llgo_coro,llgo_coro_native_pipe,llgo_coro_native_timer",
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	syscallDir := filepath.Join(runtime.GOROOT(), "src", "syscall")
	injectedPath := filepath.Join(
		syscallDir,
		"z_llgo_patch_process_raw_syscall_darwin_go126.go",
	)
	injected, ok := overlay[injectedPath]
	if !ok {
		t.Fatalf("missing Darwin coroutine process-syscall patch %s", injectedPath)
	}
	source := string(injected)
	for _, required := range []string{
		"//go:linkname llgoCoroFork llgo.controlFork",
		"//go:linkname llgoCoroExecve llgo.controlExecve",
		"//go:linkname llgoCoroExit llgo.controlExit",
		"//go:linkname llgoCoroProcessErrno C.cliteErrno",
		"func fork()",
		"func execve(",
		"func exit(",
	} {
		if !strings.Contains(source, required) {
			t.Errorf("Darwin process-syscall patch lacks %q", required)
		}
	}
	for _, forbidden := range []string{
		"llgo.syscall32",
		"abi.FuncPCABI0(",
		"llgoCoroProcessRawSyscall3",
		"//llgo:coro sync\n//go:linkname llgoCoroFork",
		"//llgo:coro sync\n//go:linkname llgoCoroExecve",
		"//llgo:coro noblock\n//go:linkname llgoCoroExit C.exit",
		"//llgo:coro noblock\n//go:linkname llgoCoroProcessErrno C.cliteErrno",
	} {
		if strings.Contains(source, forbidden) {
			t.Errorf("Darwin process-syscall patch retained dynamic carrier %q", forbidden)
		}
	}
	if strings.Contains(source, "//llgo:skip") {
		t.Fatal("source-patch control directive leaked into injected Go source")
	}

	generatedPath := filepath.Join(syscallDir, "zsyscall_darwin_arm64.go")
	filtered, ok := overlay[generatedPath]
	if !ok {
		t.Fatalf("Darwin process-syscall patch did not filter %s", generatedPath)
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), generatedPath, filtered, 0)
	if err != nil {
		t.Fatalf("parse filtered Darwin syscall source: %v", err)
	}
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok {
			continue
		}
		switch function.Name.Name {
		case "fork", "execve", "exit":
			t.Errorf("filtered GOROOT source retained original %s wrapper", function.Name.Name)
		}
	}
}

func TestApplySourcePatchForPkg_Cases(t *testing.T) {
	for _, caseName := range []string{
		"default-override",
		"generic-constraints-and-interface",
		"generic-type-and-method",
		"multi-file-skipall",
		"multi-file-with-asm",
		"skip-and-override",
		"skipall",
		"type-alias-and-grouped-values",
	} {
		t.Run(caseName, func(t *testing.T) {
			runSourcePatchCase(t, caseName)
		})
	}
}

func TestApplySourcePatchForPkg_MissingStdlibPkg(t *testing.T) {
	goroot := t.TempDir()
	runtimeDir := t.TempDir()
	pkgPath := "iter"
	patchDir := filepath.Join(runtimeDir, "_patch", pkgPath)
	mustWriteFile(t, filepath.Join(patchDir, "iter.go"), `package iter

//llgo:skipall

func Pull[V any](seq func(func(V) bool)) {}
`)

	changed, overlay, _, err := applySourcePatchForPkg(nil, nil, runtimeDir, goroot, pkgPath, sourcePatchBuildContext{})
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("expected missing stdlib package to skip source patching")
	}
	if overlay != nil {
		t.Fatalf("expected no overlay for missing stdlib package, got %v entries", len(overlay))
	}
}

func TestApplySourcePatchForPkg_BuildTaggedPatch(t *testing.T) {
	goroot := t.TempDir()
	runtimeDir := t.TempDir()
	pkgPath := "demo"
	srcDir := filepath.Join(goroot, "src", pkgPath)
	patchDir := filepath.Join(runtimeDir, "_patch", pkgPath)
	mustWriteFile(t, filepath.Join(srcDir, "demo.go"), `package demo

func Old() string { return "old" }
`)
	mustWriteFile(t, filepath.Join(patchDir, "patch.go"), `//go:build go1.26
//llgo:skipall
package demo

const Only = "patched"
`)

	changed, overlay, _, err := applySourcePatchForPkg(nil, nil, runtimeDir, goroot, pkgPath, sourcePatchBuildContext{
		goos:      runtime.GOOS,
		goarch:    runtime.GOARCH,
		goversion: "go1.24.11",
	})
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatalf("expected go1.26-tagged patch to be ignored on go1.24, got overlay: %#v", overlay)
	}

	changed, overlay, _, err = applySourcePatchForPkg(nil, nil, runtimeDir, goroot, pkgPath, sourcePatchBuildContext{
		goos:      runtime.GOOS,
		goarch:    runtime.GOARCH,
		goversion: "go1.26.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected go1.26-tagged patch to apply on go1.26")
	}
}

func TestApplySourcePatchForPkg_AnnotatesExactFunction(t *testing.T) {
	goroot := t.TempDir()
	runtimeDir := t.TempDir()
	const pkgPath = "demo"
	srcDir := filepath.Join(goroot, "src", pkgPath)
	patchDir := filepath.Join(runtimeDir, "_patch", pkgPath)
	sourceFile := filepath.Join(srcDir, "demo.go")
	mustWriteFile(t, sourceFile, `package demo

// Target keeps its existing source documentation.
//go:norace
func Target() {}

func Other() {}
`)
	mustWriteFile(t, filepath.Join(patchDir, "patch.go"), `package demo

//llgo:annotate Target rawcritical
//llgo:annotate Other coro noblock
`)

	changed, overlay, err := applySourcePatchForPkg(
		nil, nil, runtimeDir, goroot, pkgPath, sourcePatchBuildContext{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected source annotation patch to change the package")
	}
	got := string(overlay[sourceFile])
	if !strings.Contains(got, "//go:norace\n//llgo:rawcritical\nfunc Target()") {
		t.Fatalf("annotated function lost its attached directive group:\n%s", got)
	}
	if strings.Contains(got, "//llgo:rawcritical\nfunc Other()") {
		t.Fatalf("annotation leaked to another function:\n%s", got)
	}
	if !strings.Contains(got, "//llgo:coro noblock\nfunc Other()") {
		t.Fatalf("multiword coroutine annotation was not injected exactly:\n%s", got)
	}
	injected := filepath.Join(srcDir, "z_llgo_patch_patch.go")
	if strings.Contains(string(overlay[injected]), "//llgo:annotate") {
		t.Fatalf("build-only annotation directive leaked into injected source:\n%s", overlay[injected])
	}
}

func TestLinuxProcessControlSourcePatchMarksRawCriticalBodies(t *testing.T) {
	goroot := t.TempDir()
	const pkgPath = "syscall"
	sourceFile := filepath.Join(goroot, "src", pkgPath, "exec_linux.go")
	mustWriteFile(t, sourceFile, `package syscall

func forkAndExecInChild1() {}

func doCheckClonePidfd() {}
`)

	changed, overlay, err := applySourcePatchForPkg(
		nil, nil, env.LLGoRuntimeDir(), goroot, pkgPath,
		sourcePatchBuildContext{goos: "linux", goarch: "amd64", goversion: "go1.26.0"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected Linux process-control source annotations to change syscall")
	}
	got := string(overlay[sourceFile])
	for _, name := range []string{"forkAndExecInChild1", "doCheckClonePidfd"} {
		if !strings.Contains(got, "//llgo:rawcritical\nfunc "+name+"()") {
			t.Fatalf("Linux process-control function %s lacks raw-critical annotation:\n%s", name, got)
		}
	}
}

func TestApplySourcePatchForPkg_RejectsMissingAnnotationTarget(t *testing.T) {
	goroot := t.TempDir()
	runtimeDir := t.TempDir()
	const pkgPath = "demo"
	mustWriteFile(t, filepath.Join(goroot, "src", pkgPath, "demo.go"), "package demo\n")
	mustWriteFile(t, filepath.Join(runtimeDir, "_patch", pkgPath, "patch.go"), `package demo

//llgo:annotate Missing rawcritical
`)

	_, _, err := applySourcePatchForPkg(
		nil, nil, runtimeDir, goroot, pkgPath, sourcePatchBuildContext{},
	)
	if err == nil || !strings.Contains(err.Error(), `annotation target "Missing" was not found`) {
		t.Fatalf("missing annotation target error = %v", err)
	}
}

func TestApplySourcePatchForPkg_UnreadableStdlibPkg(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based permission test is Unix-only")
	}
	goroot := t.TempDir()
	runtimeDir := t.TempDir()
	pkgPath := "iter"
	srcDir := filepath.Join(goroot, "src", pkgPath)
	patchDir := filepath.Join(runtimeDir, "_patch", pkgPath)
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(srcDir, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(srcDir, 0755)
	mustWriteFile(t, filepath.Join(patchDir, "iter.go"), `package iter

//llgo:skipall

func Pull[V any](seq func(func(V) bool)) {}
`)

	changed, overlay, _, err := applySourcePatchForPkg(nil, nil, runtimeDir, goroot, pkgPath, sourcePatchBuildContext{})
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("expected unreadable stdlib package to skip source patching")
	}
	if overlay != nil {
		t.Fatalf("expected no overlay for unreadable stdlib package, got %v entries", len(overlay))
	}
}

func runSourcePatchCase(t *testing.T, caseName string) {
	t.Helper()

	assetRoot := filepath.Join(env.LLGoRuntimeDir(), "_patch", "_test", caseName)
	goroot := t.TempDir()
	runtimeDir := t.TempDir()
	const pkgPath = "demo"
	srcDir := filepath.Join(goroot, "src", pkgPath)
	patchDir := filepath.Join(runtimeDir, "_patch", pkgPath)

	copyTree(t, filepath.Join(assetRoot, "pkg"), srcDir)
	copyTree(t, filepath.Join(assetRoot, "patch"), patchDir)

	changed, overlay, _, err := applySourcePatchForPkg(nil, nil, runtimeDir, goroot, pkgPath, sourcePatchBuildContext{})
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected source patch overlay to change package")
	}

	assertOverlayMatchesOutput(t, overlay, srcDir, filepath.Join(assetRoot, "output"), runtimeDir)
	assertGeneratedPatchPositions(t, overlay, srcDir, patchDir)
}

func copyTree(t *testing.T, srcRoot, dstRoot string) {
	t.Helper()
	err := filepath.WalkDir(srcRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcRoot, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dstRoot, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func assertOverlayMatchesOutput(t *testing.T, overlay map[string][]byte, srcRoot, outputRoot, runtimeDir string) {
	t.Helper()

	got := overlayFilesUnderRoot(t, overlay, srcRoot)
	want := readTextFiles(t, outputRoot, runtimeDir)

	gotNames := sortedMapKeys(got)
	wantNames := sortedMapKeys(want)
	assertExactString(t, "overlay file list", strings.Join(gotNames, "\n"), strings.Join(wantNames, "\n"))

	for _, name := range wantNames {
		assertExactString(t, "overlay file "+name, got[name], want[name])
	}
}

func overlayFilesUnderRoot(t *testing.T, overlay map[string][]byte, root string) map[string]string {
	t.Helper()
	out := make(map[string]string)
	for filename, src := range overlay {
		rel, err := filepath.Rel(root, filename)
		if err != nil {
			t.Fatal(err)
		}
		if rel == "." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." {
			continue
		}
		out[filepath.ToSlash(rel)] = string(src)
	}
	return out
}

func readTextFiles(t *testing.T, root, runtimeDir string) map[string]string {
	t.Helper()
	out := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		if strings.HasSuffix(key, ".txt") {
			key = strings.TrimSuffix(key, ".txt")
		}
		out[key] = expandSourcePatchOutputTemplate(string(data), runtimeDir)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func expandSourcePatchOutputTemplate(src, runtimeDir string) string {
	patchRoot := filepath.ToSlash(filepath.Join(runtimeDir, "_patch"))
	return strings.ReplaceAll(src, "{{PATCH_ROOT}}", patchRoot)
}

func sortedMapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func assertGeneratedPatchPositions(t *testing.T, overlay map[string][]byte, srcRoot, patchRoot string) {
	t.Helper()
	for rel, src := range overlayFilesUnderRoot(t, overlay, srcRoot) {
		base := filepath.Base(rel)
		if !strings.HasPrefix(base, "z_llgo_patch_") {
			continue
		}
		original := strings.TrimPrefix(base, "z_llgo_patch_")
		patchFile := filepath.Join(patchRoot, filepath.Dir(rel), original)
		for _, target := range patchedTargetsOfFile(t, patchFile) {
			assertPatchedPosition(t, src, filepath.Join(srcRoot, filepath.FromSlash(rel)), patchFile, target.key, target.line)
		}
	}
}

type patchedTarget struct {
	key  string
	line int
}

func patchedTargetsOfFile(t *testing.T, filename string) []patchedTarget {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	targets := []patchedTarget{{
		key:  "package",
		line: fset.Position(file.Package).Line,
	}}
	for _, decl := range file.Decls {
		switch decl := decl.(type) {
		case *ast.FuncDecl:
			key := "func:" + decl.Name.Name
			if decl.Recv != nil && len(decl.Recv.List) != 0 {
				key = "method:" + recvPatchKey(decl.Recv.List[0].Type) + "." + decl.Name.Name
			}
			targets = append(targets, patchedTarget{
				key:  key,
				line: fset.Position(decl.Name.Pos()).Line,
			})
		case *ast.GenDecl:
			kind := strings.ToLower(decl.Tok.String())
			for _, spec := range decl.Specs {
				switch spec := spec.(type) {
				case *ast.TypeSpec:
					targets = append(targets, patchedTarget{
						key:  "type:" + spec.Name.Name,
						line: fset.Position(spec.Name.Pos()).Line,
					})
				case *ast.ValueSpec:
					for _, name := range spec.Names {
						targets = append(targets, patchedTarget{
							key:  kind + ":" + name.Name,
							line: fset.Position(name.Pos()).Line,
						})
					}
				}
			}
		}
	}
	return targets
}

func mustWriteFile(t *testing.T, filename, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func sourcePatchLineDirective(filename string) string {
	return "//line " + filepath.ToSlash(filename) + ":1\n"
}

func assertExactString(t *testing.T, label, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s mismatch\nwant:\n%q\n\ngot:\n%q", label, want, got)
	}
}

func assertPatchedPosition(t *testing.T, src, generatedFilename, wantFilename, target string, wantLine int) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, generatedFilename, src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	pos, ok := findPatchedPosition(file, target)
	if !ok {
		t.Fatalf("target %q not found", target)
	}
	got := fset.Position(pos)
	if filepath.ToSlash(got.Filename) != filepath.ToSlash(wantFilename) || got.Line != wantLine {
		t.Fatalf("target %q position mismatch: want %s:%d, got %s:%d", target, filepath.ToSlash(wantFilename), wantLine, filepath.ToSlash(got.Filename), got.Line)
	}
}

func findPatchedPosition(file *ast.File, target string) (token.Pos, bool) {
	if target == "package" {
		return file.Package, true
	}
	for _, decl := range file.Decls {
		switch decl := decl.(type) {
		case *ast.FuncDecl:
			key := "func:" + decl.Name.Name
			if decl.Recv != nil && len(decl.Recv.List) != 0 {
				key = "method:" + recvPatchKey(decl.Recv.List[0].Type) + "." + decl.Name.Name
			}
			if key == target {
				return decl.Name.Pos(), true
			}
		case *ast.GenDecl:
			for _, spec := range decl.Specs {
				switch spec := spec.(type) {
				case *ast.TypeSpec:
					if "type:"+spec.Name.Name == target {
						return spec.Name.Pos(), true
					}
				case *ast.ValueSpec:
					kind := strings.ToLower(decl.Tok.String())
					for _, name := range spec.Names {
						if kind+":"+name.Name == target {
							return name.Pos(), true
						}
					}
				}
			}
		}
	}
	return token.NoPos, false
}
