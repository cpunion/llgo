//go:build !llgo

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package build

import (
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/buildenv"
	"github.com/xgo-dev/llgo/internal/lto"
	llpackages "github.com/xgo-dev/llgo/internal/packages"
	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
)

func TestPackageLinkSnapshotSurvivesModuleDisposal(t *testing.T) {
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	lpkg := prog.NewPackage("example.com/p", "example.com/p")
	lpkg.NeedRuntime = true
	lpkg.NeedPyInit = true
	lpkg.NeedAbiInit = 7
	lpkg.RecordReflectMethodByIndex("example.com/p.Use", 9)
	lpkg.RecordReflectMethodByIndex("example.com/p.Use", 2)
	lpkg.RecordReflectMethodByName("example.com/p.Use", "Zed")
	lpkg.RecordReflectMethodByName("example.com/p.Use", "Alpha")
	lpkg.SetExport("example.com/p.Local", "Exported")
	lpkg.EmitFuncInfo("example.com/p.live", "example.com/p.Live", "live.go", 17, 3)
	lpkg.EmitPCLineInfo(0x1234, "example.com/p.live", "live.go", 18, 4)

	mod := lpkg.Module()
	llvmContext := mod.Context()
	global := llvm.AddGlobal(mod, llvmContext.Int8Type(), "example.com/p.global")
	global.SetInitializer(llvm.ConstInt(llvmContext.Int8Type(), 1, false))

	pkg := &aPackage{
		Package: &llpackages.Package{ID: "example.com/p", Name: "p", PkgPath: "example.com/p"},
		LPkg:    lpkg,
	}
	freezePackageLinkSnapshot(pkg)
	if pkg.LinkSnapshot == nil {
		t.Fatal("package link snapshot was not frozen")
	}
	mod.Dispose()
	pkg.LPkg = nil

	if !pkg.NeedRt || !pkg.NeedPyInit || packageNeedAbiInit(pkg) != 7 {
		t.Fatalf("snapshot runtime/ABI requirements = (%t, %t, %d)", pkg.NeedRt, pkg.NeedPyInit, packageNeedAbiInit(pkg))
	}
	if got := packageMethodIndexes(pkg); !slices.Equal(got, []int{2, 9}) {
		t.Fatalf("snapshot method indexes = %v", got)
	}
	if got := packageMethodNames(pkg); !slices.Equal(got, []string{"Alpha", "Zed"}) {
		t.Fatalf("snapshot method names = %v", got)
	}
	if got := packageDefinedGlobals(pkg); !slices.Contains(got, "example.com/p.global") {
		t.Fatalf("snapshot globals = %v", got)
	}
	if got := packageExportFunctionNames(pkg); !slices.Equal(got, []string{"Exported"}) {
		t.Fatalf("snapshot exports = %v", got)
	}
	funcInfo := collectFuncInfo([]Package{pkg})
	if len(funcInfo) != 1 || funcInfo[0].symbol != "example.com/p.live" {
		t.Fatalf("snapshot funcinfo = %+v", funcInfo)
	}
	if pc := collectPCLineInfo([]Package{pkg}); len(pc) != 1 || pc[0].id != 0x1234 {
		t.Fatalf("snapshot pcline = %+v", pc)
	}
}

func TestStagedNativeBackendGateIsExact(t *testing.T) {
	if runtime.GOARCH == "wasm" {
		t.Skip("host-native staged backend is unavailable on wasm")
	}
	base := &context{
		mode: ModeBuild,
		buildConf: &Config{
			BuildMode: BuildModeExe,
			Goos:      runtime.GOOS,
			Goarch:    runtime.GOARCH,
		},
	}
	if !shouldStageNativeExecutableBackend(base) {
		t.Fatal("ordinary host executable did not select staged backend")
	}
	checkDisabled := func(name string, mutate func(*context)) {
		t.Helper()
		confCopy := *base.buildConf
		ctxCopy := &context{
			mode:      base.mode,
			buildConf: &confCopy,
		}
		mutate(ctxCopy)
		if shouldStageNativeExecutableBackend(ctxCopy) {
			t.Errorf("%s unexpectedly selected staged backend", name)
		}
	}
	checkDisabled("ModeGen", func(ctx *context) { ctx.mode = ModeGen })
	checkDisabled("C shared", func(ctx *context) { ctx.buildConf.BuildMode = BuildModeCShared })
	checkDisabled("LTO", func(ctx *context) { ctx.buildConf.LTO = lto.Full })
	if buildenv.Dev {
		checkDisabled("deadcode drop", func(ctx *context) { ctx.buildConf.DeadcodeDrop = true })
	}
	checkDisabled("GenLL", func(ctx *context) { ctx.buildConf.GenLL = true })
	checkDisabled("named target", func(ctx *context) { ctx.buildConf.Target = "fixture" })
	checkDisabled("cross GOOS", func(ctx *context) { ctx.buildConf.Goos = runtime.GOOS + "-other" })
}

func TestReleaseBuiltPackageSourcePreservesLinkGraph(t *testing.T) {
	dependency := &llpackages.Package{ID: "dep", Name: "dep", PkgPath: "example.com/dep"}
	source := &llpackages.Package{
		ID:              "root",
		Name:            "main",
		PkgPath:         "example.com/root",
		Dir:             "/root",
		ExportFile:      "/root.a",
		Imports:         map[string]*llpackages.Package{"example.com/dep": dependency},
		Syntax:          []*ast.File{{}},
		TypesInfo:       &types.Info{},
		TypesSizes:      types.SizesFor("gc", runtime.GOARCH),
		Types:           types.NewPackage("example.com/root", "main"),
		Fset:            token.NewFileSet(),
		GoFiles:         []string{"root.go"},
		CompiledGoFiles: []string{"root.go"},
	}
	altSource := &llpackages.Package{
		ID:              "alt-root",
		Name:            "main",
		PkgPath:         "example.com/root",
		Syntax:          []*ast.File{{}},
		TypesInfo:       &types.Info{},
		TypesSizes:      types.SizesFor("gc", runtime.GOARCH),
		Types:           types.NewPackage("example.com/root", "main"),
		Fset:            token.NewFileSet(),
		GoFiles:         []string{"root_llgo.go"},
		CompiledGoFiles: []string{"root_llgo.go"},
	}
	pkg := &aPackage{
		Package: source,
		AltPkg: &llpackages.Cached{
			Package:   altSource,
			Types:     altSource.Types,
			TypesInfo: altSource.TypesInfo,
			Syntax:    altSource.Syntax,
		},
		rewriteVars: map[string]string{"v": "x"},
	}
	releaseBuiltPackageSource(pkg)
	if source.Syntax != nil || source.TypesInfo != nil || source.TypesSizes != nil || source.Types != nil || source.Fset != nil {
		t.Fatalf("source-only state survived release: %+v", source)
	}
	if source.ID != "root" || source.Name != "main" || source.PkgPath != "example.com/root" ||
		source.ExportFile != "/root.a" || source.Imports["example.com/dep"] != dependency {
		t.Fatalf("link identity/import graph changed: %+v", source)
	}
	if source.GoFiles != nil || source.CompiledGoFiles != nil {
		t.Fatalf("selected source paths survived release: %+v", source)
	}
	if pkg.AltPkg != nil || altSource.Syntax != nil || altSource.TypesInfo != nil ||
		altSource.TypesSizes != nil || altSource.Types != nil || altSource.Fset != nil {
		t.Fatalf("alternate source graph survived release: %+v", pkg.AltPkg)
	}
	if pkg.sourceSelection == nil ||
		!slices.Equal(pkg.sourceSelection.goFiles, []string{"root.go"}) ||
		!slices.Equal(pkg.sourceSelection.altGoFiles, []string{"root_llgo.go"}) {
		t.Fatalf("selected source receipt = %+v", pkg.sourceSelection)
	}
}

func TestStagedBackendPhaseOrderIsMandatory(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("build.go"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	start := strings.Index(text, "allPkgs, err = buildAllPkgs(ctx, allPkgs, verbose)")
	if start < 0 {
		t.Fatal("cannot locate post-package staged backend block")
	}
	end := strings.Index(text[start:], "\n\tif mode == ModeGen")
	if end < 0 {
		t.Fatal("cannot locate post-package staged backend block")
	}
	block := text[start : start+end]
	ordered := []string{
		"stageMainEntryBitcodes(ctx, initial, allPkgs)",
		"releaseCoroFrontendForStagedBackend(ctx, allPkgs)",
		"prog.Dispose()",
		"prog = nil",
		"debug.FreeOSMemory()",
		"releaseNativeHeap()",
		"materializeStagedPackageBackends(ctx, allPkgs, verbose)",
	}
	previous := -1
	for _, needle := range ordered {
		index := strings.Index(block, needle)
		if index < 0 {
			t.Fatalf("mandatory staged phase %q is absent", needle)
		}
		if index <= previous {
			t.Fatalf("mandatory staged phases are out of order at %q", needle)
		}
		previous = index
	}
	if !strings.Contains(text, "backend.ParseBitcodeFile(pkg.StagedBitcode)") ||
		!strings.Contains(text, "lowerCoroPackageModuleWithProgram(backend, pkg.PkgPath, mod)") {
		t.Fatal("detached backend no longer owns bitcode parsing and coroutine lowering")
	}
}

func TestLinkedExecutionPhaseReleasesCompilerState(t *testing.T) {
	staged := filepath.Join(t.TempDir(), "staged.bc")
	if err := os.WriteFile(staged, []byte("bitcode"), 0o644); err != nil {
		t.Fatal(err)
	}
	dependency := &llpackages.Package{ID: "dep", PkgPath: "example.com/dep"}
	source := &llpackages.Package{
		ID:              "root",
		Name:            "main",
		PkgPath:         "example.com/root",
		Imports:         map[string]*llpackages.Package{dependency.PkgPath: dependency},
		Syntax:          []*ast.File{{}},
		TypesInfo:       &types.Info{},
		TypesSizes:      types.SizesFor("gc", runtime.GOARCH),
		Types:           types.NewPackage("example.com/root", "main"),
		Fset:            token.NewFileSet(),
		GoFiles:         []string{"root.go"},
		CompiledGoFiles: []string{"root.go"},
	}
	pkg := &aPackage{
		Package:                  source,
		LinkArgs:                 []string{"-lm"},
		ObjFiles:                 []string{"root.o"},
		LinkSnapshot:             &packageLinkSnapshot{definedGlobals: []string{"root.global"}},
		CoroLibraryEffectRecords: []byte("effects"),
		Fingerprint:              "fingerprint",
		Manifest:                 "manifest",
		CoroRootAnchorV1:         "anchor",
	}
	conf := &Config{
		RunArgs:        []string{"-test.run=TestNested"},
		Overlay:        map[string][]byte{"root.go": []byte("package main")},
		GlobalRewrites: map[string]Rewrites{"main": {"value": "replacement"}},
		GoBuildFlags:   []string{"-tags=fixture"},
	}
	ctx := &context{
		conf:                  &llpackages.Config{},
		fingerprinting:        map[string]bool{"root": true},
		patchFiles:            map[string][]string{"root": {"root.go"}},
		initial:               []*llpackages.Package{source},
		pkgs:                  map[*llpackages.Package]Package{source: pkg},
		pkgByID:               map[string]Package{source.ID: pkg},
		buildConf:             conf,
		commands:              commandEnv{dir: "/execution"},
		coroPlanDigest:        "digest",
		coroProgramBootstraps: map[string]*coroProgramBootstrapV1{"root": {}},
		stagedBitcodeFiles:    map[string]none{staged: {}},
		stagedMainEntries:     map[string]*stagedMainEntry{"root": {}},
	}

	releaseBuildStateBeforeExecution(ctx, []*aPackage{pkg})

	if ctx.conf != nil || ctx.pkgs != nil || ctx.pkgByID != nil || ctx.initial != nil ||
		ctx.patchFiles != nil || ctx.fingerprinting != nil || ctx.coroPlanDigest != "" ||
		ctx.coroProgramBootstraps != nil || ctx.stagedMainEntries != nil {
		t.Fatalf("compiler ownership survived execution boundary: %+v", ctx)
	}
	if ctx.buildConf != conf || ctx.commands.dir != "/execution" ||
		!slices.Equal(ctx.buildConf.RunArgs, []string{"-test.run=TestNested"}) {
		t.Fatalf("compact execution state was not preserved: conf=%p commands=%+v", ctx.buildConf, ctx.commands)
	}
	if ctx.buildConf.Overlay != nil || ctx.buildConf.GlobalRewrites != nil || ctx.buildConf.GoBuildFlags != nil {
		t.Fatalf("build-only config survived execution boundary: %+v", ctx.buildConf)
	}
	if pkg.LinkSnapshot != nil || pkg.sourceSelection != nil || pkg.LinkArgs != nil || pkg.ObjFiles != nil ||
		pkg.CoroLibraryEffectRecords != nil || pkg.Fingerprint != "" || pkg.Manifest != "" ||
		pkg.CoroRootAnchorV1 != "" {
		t.Fatalf("package compiler state survived execution boundary: %+v", pkg)
	}
	if source.Imports != nil || source.Syntax != nil || source.TypesInfo != nil || source.Types != nil || source.Fset != nil {
		t.Fatalf("loaded package graph survived execution boundary: %+v", source)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Fatalf("staged bitcode survived execution boundary: %v", err)
	}
}

func TestLinkedExecutionPhaseOrderIsMandatory(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("build.go"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	start := strings.Index(text, "var executions []linkedOutputExecution")
	if start < 0 {
		t.Fatal("cannot locate linked-output phase")
	}
	end := strings.Index(text[start:], "\n\tif mode == ModeTest")
	if end < 0 {
		t.Fatal("cannot locate linked-output phase end")
	}
	block := text[start : start+end]
	ordered := []string{
		"linkMainPkg(ctx, pkg, allPkgs, outFmts.Out, verbose)",
		"executions = append(executions",
		"prog.Dispose()",
		"prog = nil",
		"releaseBuildStateBeforeExecution(ctx, allPkgs)",
		"debug.FreeOSMemory()",
		"releaseNativeHeap()",
		"executeLinkedOutput(ctx, execution, conf, mode, verbose)",
	}
	previous := -1
	for _, needle := range ordered {
		index := strings.Index(block, needle)
		if index < 0 {
			t.Fatalf("mandatory linked-output phase %q is absent", needle)
		}
		if index <= previous {
			t.Fatalf("mandatory linked-output phases are out of order at %q", needle)
		}
		previous = index
	}
	if strings.Contains(text, "programOwnershipTransferred") {
		t.Fatal("boolean LLVM ownership receipt can retain the disposed Program through deferred cleanup")
	}
	beforeRelease := block[:strings.Index(block, "releaseBuildStateBeforeExecution(ctx, allPkgs)")]
	for _, forbidden := range []string{"runNative(ctx", "runInEmulator(", "flash.FlashDevice(", "monitor.Monitor("} {
		if strings.Contains(beforeRelease, forbidden) {
			t.Fatalf("execution operation %q remains interleaved with linking", forbidden)
		}
	}
}
