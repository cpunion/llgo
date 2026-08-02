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

	"github.com/goplus/llgo/internal/buildenv"
	"github.com/goplus/llgo/internal/lto"
	llpackages "github.com/goplus/llgo/internal/packages"
	llssa "github.com/goplus/llgo/ssa"
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
	stub := llvm.AddFunction(
		mod,
		closureStubPrefix+"example.com/p.live",
		llvm.FunctionType(llvmContext.VoidType(), nil, false),
	)
	block := llvmContext.AddBasicBlock(stub, "entry")
	builder := llvmContext.NewBuilder()
	builder.SetInsertPointAtEnd(block)
	builder.CreateRetVoid()
	builder.Dispose()

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
	if stubs := collectFuncInfoStubRecords([]Package{pkg}, funcInfo); len(stubs) != 1 || stubs[0].symbol != closureStubPrefix+"example.com/p.live" {
		t.Fatalf("snapshot closure stubs = %+v", stubs)
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
		"debug.FreeOSMemory()",
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
