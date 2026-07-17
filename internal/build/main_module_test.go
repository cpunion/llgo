//go:build !llgo
// +build !llgo

package build

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/xgo-dev/llvm"

	"github.com/goplus/llgo/internal/packages"
	llssa "github.com/goplus/llgo/ssa"
)

func init() {
	llssa.Initialize(llssa.InitAll)
}

func TestGenMainModuleExecutable(t *testing.T) {
	llvm.InitializeAllTargets()
	t.Setenv(llgoStdioNobuf, "")
	ctx := &context{
		prog: llssa.NewProgram(nil),
		buildConf: &Config{
			BuildMode: BuildModeExe,
			Goos:      "linux",
			Goarch:    "amd64",
		},
	}
	pkg := &packages.Package{PkgPath: "example.com/foo", ExportFile: "foo.a"}
	mod := genMainModule(ctx, llssa.PkgRuntime, pkg,
		&genConfig{rtInit: true, pyInit: true})
	if mod.ExportFile != "foo.a-main" {
		t.Fatalf("unexpected export file: %s", mod.ExportFile)
	}
	ir := mod.LPkg.String()
	checks := []string{
		"define i32 @main(",
		"call void @Py_Initialize()",
		"call void @Py_Finalize()",
		"call void @\"example.com/foo.init\"()",
		"define weak void @_start()",
	}
	for _, want := range checks {
		if !strings.Contains(ir, want) {
			t.Fatalf("main module IR missing %q:\n%s", want, ir)
		}
	}
	assertInOrder(t, ir,
		"call void @Py_Initialize()",
		"call void @\"example.com/foo.init\"()",
		"call void @\"example.com/foo.main\"()",
		"call void @Py_Finalize()",
	)
}

func TestGenMainModuleLibrary(t *testing.T) {
	llvm.InitializeAllTargets()
	t.Setenv(llgoStdioNobuf, "")
	ctx := &context{
		prog: llssa.NewProgram(nil),
		buildConf: &Config{
			BuildMode: BuildModeCArchive,
			Goos:      "linux",
			Goarch:    "amd64",
		},
	}
	pkg := &packages.Package{PkgPath: "example.com/foo", ExportFile: "foo.a"}
	mod := genMainModule(ctx, llssa.PkgRuntime, pkg, &genConfig{})
	ir := mod.LPkg.String()
	if strings.Contains(ir, "define i32 @main") {
		t.Fatalf("library mode should not emit main function:\n%s", ir)
	}
	if !strings.Contains(ir, "@__llgo_argc = global i32 0") {
		t.Fatalf("library mode missing argc global:\n%s", ir)
	}
}

func TestGenMainModuleCoroControlWrappersBuildModes(t *testing.T) {
	llvm.InitializeAllTargets()
	t.Setenv(llgoStdioNobuf, "")

	tests := []struct {
		name      string
		buildMode BuildMode
		goos      string
		goarch    string
		target    *llssa.Target
		entry     string
	}{
		{
			name:      "native executable",
			buildMode: BuildModeExe,
			goos:      "linux",
			goarch:    "amd64",
			entry:     "define i32 @main(",
		},
		{
			name:      "wasm executable",
			buildMode: BuildModeExe,
			goos:      "wasip1",
			goarch:    "wasm",
			target:    &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"},
			entry:     "define hidden i32 @__main_argc_argv(",
		},
		{
			name:      "C archive",
			buildMode: BuildModeCArchive,
			goos:      "linux",
			goarch:    "amd64",
		},
		{
			name:      "C shared library",
			buildMode: BuildModeCShared,
			goos:      "linux",
			goarch:    "amd64",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prog := llssa.NewProgram(test.target)
			defer prog.Dispose()
			ctx := &context{
				prog: prog,
				buildConf: &Config{
					BuildMode:            test.buildMode,
					Goos:                 test.goos,
					Goarch:               test.goarch,
					EnableCoroChildAwait: true,
				},
			}
			mod := genMainModule(ctx, llssa.PkgRuntime,
				&packages.Package{PkgPath: "example.com/foo", ExportFile: "foo.a"},
				&genConfig{})
			ir := mod.LPkg.String()
			for _, want := range []string{
				"define void @__llgo_coro_resume_v1(ptr",
				"call void @llvm.coro.resume(ptr",
				"define i1 @__llgo_coro_done_v1(ptr",
				"call i1 @llvm.coro.done(ptr",
				"define void @__llgo_coro_destroy_v1(ptr",
				"call void @llvm.coro.destroy(ptr",
			} {
				if !strings.Contains(ir, want) {
					t.Fatalf("entry module IR missing %q:\n%s", want, ir)
				}
			}
			if test.entry != "" && !strings.Contains(ir, test.entry) {
				t.Fatalf("entry module IR missing %q:\n%s", test.entry, ir)
			}
			if test.buildMode != BuildModeExe && strings.Contains(ir, "define i32 @main(") {
				t.Fatalf("library mode should not emit main function:\n%s", ir)
			}
		})
	}
}

func TestGenMainModuleCoroControlWrappersDisabled(t *testing.T) {
	llvm.InitializeAllTargets()
	t.Setenv(llgoStdioNobuf, "")
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	ctx := &context{
		prog: prog,
		buildConf: &Config{
			BuildMode: BuildModeExe,
			Goos:      "linux",
			Goarch:    "amd64",
		},
	}
	mod := genMainModule(ctx, llssa.PkgRuntime,
		&packages.Package{PkgPath: "example.com/foo", ExportFile: "foo.a"},
		&genConfig{})
	if strings.Contains(mod.LPkg.String(), "__llgo_coro_") {
		t.Fatalf("disabled child-await mode emitted coroutine control ABI:\n%s", mod.LPkg.String())
	}
}

func TestGenMainModuleCoroProgramManifest(t *testing.T) {
	llvm.InitializeAllTargets()
	t.Setenv(llgoStdioNobuf, "")
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	ctx := &context{
		prog: prog,
		buildConf: &Config{
			BuildMode:            BuildModeCArchive,
			Goos:                 "linux",
			Goarch:               "amd64",
			EnableCoroChildAwait: true,
		},
	}
	a := coroRootPackageAnchorPrefixV1 + "11111111111111111111111111111111"
	b := coroRootPackageAnchorPrefixV1 + "22222222222222222222222222222222"
	var hash [16]byte
	for i := range hash {
		hash[i] = byte(i + 1)
	}
	entry := genMainModule(ctx, llssa.PkgRuntime,
		&packages.Package{PkgPath: "example.com/foo", ExportFile: "foo.a"},
		&genConfig{coroRootAnchors: []string{a, b}, coroManifestHash: hash})
	ir := entry.LPkg.String()
	for _, want := range []string{
		"@" + a + " = external hidden constant",
		"@" + b + " = external hidden constant",
		"@" + coroProgramManifestSymbolV1 + ".packages = internal unnamed_addr constant [2 x ptr] [ptr @" + a + ", ptr @" + b + "]",
		"@" + coroProgramManifestSymbolV1 + " = hidden constant",
		"i32 1, i32 0, i64 72623859790382856, i64 651345242494996240, i64 2",
		"ptr @" + coroProgramManifestSymbolV1 + ".packages, ptr null",
		"@llvm.used = appending global [1 x ptr] [ptr @" + coroProgramManifestSymbolV1 + "]",
	} {
		if !strings.Contains(ir, want) {
			t.Fatalf("coroutine program manifest missing %q:\n%s", want, ir)
		}
	}
	if got := entry.LPkg.CoroProgramManifest(); got != coroProgramManifestSymbolV1 {
		t.Fatalf("program manifest symbol = %q, want %q", got, coroProgramManifestSymbolV1)
	}
}

func TestGenMainModuleEmptyCoroProgramManifest(t *testing.T) {
	llvm.InitializeAllTargets()
	t.Setenv(llgoStdioNobuf, "")
	prog := llssa.NewProgram(&llssa.Target{GOOS: "wasip1", GOARCH: "wasm"})
	defer prog.Dispose()
	ctx := &context{
		prog: prog,
		buildConf: &Config{
			BuildMode:            BuildModeExe,
			Goos:                 "wasip1",
			Goarch:               "wasm",
			EnableCoroChildAwait: true,
		},
	}
	entry := genMainModule(ctx, llssa.PkgRuntime,
		&packages.Package{PkgPath: "example.com/empty", ExportFile: "empty.a"},
		&genConfig{})
	ir := entry.LPkg.String()
	if strings.Contains(ir, coroProgramManifestSymbolV1+".packages") {
		t.Fatalf("empty coroutine catalog emitted a zero-length package array:\n%s", ir)
	}
	manifestLine := ""
	for _, line := range strings.Split(ir, "\n") {
		if strings.HasPrefix(line, "@"+coroProgramManifestSymbolV1+" =") {
			manifestLine = line
			break
		}
	}
	if manifestLine == "" || !strings.Contains(manifestLine, "i32 0, ptr null, ptr null") {
		t.Fatalf("empty wasm coroutine manifest does not contain count=0/packages=null/bootstrap=null: %s\n%s", manifestLine, ir)
	}
	if strings.Contains(ir, coroProgramBootstrapSymbolV1) {
		t.Fatalf("bootstrap gate disabled but bootstrap symbol was emitted:\n%s", ir)
	}
}

func TestGenMainModuleCoroProgramBootstrapNativeAndWasm(t *testing.T) {
	llvm.InitializeAllTargets()
	t.Setenv(llgoStdioNobuf, "")
	tests := []struct {
		name      string
		target    *llssa.Target
		goos      string
		goarch    string
		uintptrIR string
		entryIR   string
	}{
		{
			name:      "native",
			goos:      "linux",
			goarch:    "amd64",
			uintptrIR: "i64",
			entryIR:   "define i32 @main(",
		},
		{
			name:      "wasm",
			target:    &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"},
			goos:      "wasip1",
			goarch:    "wasm",
			uintptrIR: "i32",
			entryIR:   "define hidden i32 @__main_argc_argv(",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prog := llssa.NewProgram(test.target)
			defer prog.Dispose()
			ctx := &context{
				prog: prog,
				buildConf: &Config{
					BuildMode:                     BuildModeExe,
					Goos:                          test.goos,
					Goarch:                        test.goarch,
					EnableCoroEntryResolution:     true,
					EnableCoroPhysicalABI:         true,
					EnableCoroChildAwait:          true,
					EnableCoroProgramBootstrapABI: true,
				},
			}
			var programHash [16]byte
			for i := range programHash {
				programHash[i] = byte(i + 1)
			}
			entry := genMainModule(ctx, llssa.PkgRuntime,
				&packages.Package{ID: "example.com/foo", PkgPath: "example.com/foo", ExportFile: "foo.a"},
				&genConfig{
					coroManifestHash: programHash,
					coroBootstrap: &coroProgramBootstrapV1{Steps: []coroProgramBootstrapStepV1{
						{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRoleInitV1, FunctionID: "init-id", Target: "example.com/foo.init"},
						{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRoleMainV1, FunctionID: "main-id", Target: "example.com/foo.main"},
					}},
				})
			ir := entry.LPkg.String()
			if !strings.Contains(ir, test.entryIR) {
				t.Fatalf("bootstrap entry module missing %q:\n%s", test.entryIR, ir)
			}
			stepsLine := irLineWithPrefix(ir, "@"+coroProgramBootstrapSymbolV1+".steps =")
			bootstrapLine := irLineWithPrefix(ir, "@"+coroProgramBootstrapSymbolV1+" =")
			manifestLine := irLineWithPrefix(ir, "@"+coroProgramManifestSymbolV1+" =")
			if stepsLine == "" || bootstrapLine == "" || manifestLine == "" {
				t.Fatalf("missing bootstrap/manifest globals:\n%s", ir)
			}
			for _, want := range []string{
				"i32 1, i32 1, ptr @\"example.com/foo.init\", " + test.uintptrIR + " 0",
				"i32 1, i32 2, ptr @\"example.com/foo.main\", " + test.uintptrIR + " 0",
			} {
				if !strings.Contains(stepsLine, want) {
					t.Fatalf("startup table missing %q: %s", want, stepsLine)
				}
			}
			hashWords := "i64 72623859790382856, i64 651345242494996240"
			if !strings.Contains(bootstrapLine, hashWords) || !strings.Contains(manifestLine, hashWords) {
				t.Fatalf("manifest/bootstrap ABI hashes differ:\nbootstrap: %s\nmanifest: %s", bootstrapLine, manifestLine)
			}
			if !strings.Contains(bootstrapLine, test.uintptrIR+" 2, ptr @"+coroProgramBootstrapSymbolV1+".steps, ptr null") {
				t.Fatalf("bootstrap count/steps/factory are not 2/non-null/null: %s", bootstrapLine)
			}
			if !strings.Contains(manifestLine, "ptr @"+coroProgramBootstrapSymbolV1) {
				t.Fatalf("manifest bootstrap pointer is null: %s", manifestLine)
			}
			if got := entry.LPkg.CoroProgramBootstrap(); got != coroProgramBootstrapSymbolV1 {
				t.Fatalf("program bootstrap symbol = %q, want %q", got, coroProgramBootstrapSymbolV1)
			}
			if function := entry.LPkg.Module().NamedFunction(coroProgramContinueSymbolV1); !function.IsNil() {
				t.Fatalf("descriptor-only bootstrap declared runnable continuation ABI:\n%s", ir)
			}
			if reference := entry.LPkg.Module().NamedGlobal(coroProgramContinueReferenceSymbolV1); !reference.IsNil() {
				t.Fatalf("descriptor-only bootstrap retained runnable continuation ABI:\n%s", ir)
			}
			assertInOrder(t, ir,
				"call void @\"example.com/foo.init\"()",
				"call void @\"example.com/foo.main\"()",
			)
		})
	}
}

func TestGenMainModuleCoroProgramBootstrapV2MixedNativeAndWasm(t *testing.T) {
	llvm.InitializeAllTargets()
	t.Setenv(llgoStdioNobuf, "")
	tests := []struct {
		name      string
		target    *llssa.Target
		goos      string
		goarch    string
		uintptrIR string
		entryIR   string
		entryName string
	}{
		{
			name:      "native",
			goos:      "linux",
			goarch:    "amd64",
			uintptrIR: "i64",
			entryIR:   "define i32 @main(",
			entryName: "main",
		},
		{
			name:      "wasm",
			target:    &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"},
			goos:      "wasip1",
			goarch:    "wasm",
			uintptrIR: "i32",
			entryIR:   "define hidden i32 @__main_argc_argv(",
			entryName: "__main_argc_argv",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prog := llssa.NewProgram(test.target)
			defer prog.Dispose()
			ctx := &context{
				prog: prog,
				buildConf: &Config{
					BuildMode:                     BuildModeExe,
					Goos:                          test.goos,
					Goarch:                        test.goarch,
					EnableCoroEntryResolution:     true,
					EnableCoroPhysicalABI:         true,
					EnableCoroChildAwait:          true,
					EnableCoroProgramBootstrapABI: true,
					EnableCoroProgramBootstrapRun: true,
					EnableCoroClosedStaticSpawn:   true,
				},
			}
			const anchor = "__llgo_coro_root_package_v1.0123456789abcdef0123456789abcdef"
			var programHash [16]byte
			for i := range programHash {
				programHash[i] = byte(i + 1)
			}
			bootstrap := &coroProgramBootstrapV1{
				Version: coroProgramBootstrapVersionV2,
				Steps: []coroProgramBootstrapStepV1{
					{
						Kind: coroProgramStepCoroRootV1, Role: coroProgramStepRoleRuntimeInitV2,
						FunctionID: "runtime-init-id", Target: llssa.PkgRuntime + ".init$coro",
						Owner: llssa.PkgRuntime, CatalogTarget: anchor, Aux: 0,
					},
					{
						Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRoleABIInitV2,
						FunctionID: "abi-init-id", Target: "init$abitypes",
					},
					{
						Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRolePublicRuntimeInitV2,
						FunctionID: "public-runtime-init-id", Target: "runtime.init",
					},
					{
						Kind: coroProgramStepCoroRootV1, Role: coroProgramStepRolePackageInitV2,
						FunctionID: "package-init-id", Target: "example.com/foo.init$coro",
						Owner: "example.com/foo", CatalogTarget: anchor, Aux: 1,
					},
					{
						Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRoleMainV2,
						FunctionID: "main-id", Target: "example.com/foo.main",
					},
				},
			}
			entry := genMainModule(ctx, llssa.PkgRuntime,
				&packages.Package{ID: "example.com/foo", PkgPath: "example.com/foo", ExportFile: "foo.a"},
				&genConfig{
					rtInit:           true,
					pyInit:           true,
					coroRootAnchors:  []string{anchor},
					coroManifestHash: programHash,
					coroBootstrap:    bootstrap,
				})
			ir := entry.LPkg.String()
			if !strings.Contains(ir, test.entryIR) {
				t.Fatalf("mixed v2 bootstrap entry module missing %q:\n%s", test.entryIR, ir)
			}
			stepsLine := irLineWithPrefix(ir, "@"+coroProgramBootstrapSymbolV2+".steps =")
			bootstrapLine := irLineWithPrefix(ir, "@"+coroProgramBootstrapSymbolV2+" =")
			manifestLine := irLineWithPrefix(ir, "@"+coroProgramManifestSymbolV1+" =")
			if stepsLine == "" || bootstrapLine == "" || manifestLine == "" {
				t.Fatalf("missing mixed v2 bootstrap/manifest globals:\n%s", ir)
			}
			for _, want := range []string{
				"i32 2, i32 1, ptr @" + anchor + ", " + test.uintptrIR + " 0",
				"i32 1, i32 2, ptr @\"init$abitypes\", " + test.uintptrIR + " 0",
				"i32 1, i32 4, ptr @runtime.init, " + test.uintptrIR + " 0",
				"i32 2, i32 8, ptr @" + anchor + ", " + test.uintptrIR + " 1",
				"i32 1, i32 16, ptr @\"example.com/foo.main\", " + test.uintptrIR + " 0",
			} {
				if !strings.Contains(stepsLine, want) {
					t.Fatalf("mixed v2 startup table missing %q: %s", want, stepsLine)
				}
			}
			if !strings.Contains(bootstrapLine, "i32 2, i32 0") ||
				!strings.Contains(bootstrapLine, test.uintptrIR+" 5, ptr @"+coroProgramBootstrapSymbolV2+".steps, ptr @"+coroProgramBootstrapFactorySymbolV2) {
				t.Fatalf("mixed v2 bootstrap version/count/steps/factory are not canonical: %s", bootstrapLine)
			}
			if !strings.Contains(manifestLine, "ptr @"+coroProgramBootstrapSymbolV2) {
				t.Fatalf("manifest does not reference the mixed v2 bootstrap: %s", manifestLine)
			}
			if got := entry.LPkg.CoroProgramBootstrap(); got != coroProgramBootstrapSymbolV2 {
				t.Fatalf("program bootstrap symbol = %q, want %q", got, coroProgramBootstrapSymbolV2)
			}

			mod := entry.LPkg.Module()
			if nativeCoroDoorbellRuntimeABI(ctx.buildConf) {
				assertCoroProgramNativeSliceV2(t, mod, test.entryName)
				assertCoroNativePostWaitRetention(t, mod, test.entryName)
			} else {
				assertCoroProgramContinueRetention(t, mod, test.entryName)
				if callback := mod.NamedFunction(coroNativePostWaitSymbolV1); !callback.IsNil() {
					t.Fatalf("non-native entry declared native post-wait callback:\n%s", ir)
				}
			}
			publicRuntimeInit := mod.NamedFunction("runtime.init")
			if publicRuntimeInit.IsNil() || !publicRuntimeInit.IsDeclaration() {
				t.Fatalf("managed public runtime init must remain an unresolved archive reference, not an entry-module weak body:\n%s", ir)
			}
			factory := mod.NamedFunction(coroProgramBootstrapFactorySymbolV2)
			if factory.IsNil() || factory.IsDeclaration() {
				t.Fatalf("compiler-owned mixed v2 bootstrap factory is missing:\n%s", ir)
			}
			factoryBody := factory.String()
			if got := strings.Count(factoryBody, "call void @__llgo_coro_await_prepare_v1"); got != 2 {
				t.Fatalf("mixed v2 main-module factory await calls = %d, want 2:\n%s", got, factoryBody)
			}
			if got := strings.Count(factoryBody, "call void @"+coroProgramMainReturnSymbolV1); got != 1 {
				t.Fatalf("mixed v2 main-module main-return calls = %d, want 1:\n%s", got, factoryBody)
			}
			assertInOrder(t, factoryBody,
				"call ptr %",
				"call void @__llgo_coro_await_prepare_v1",
				"call void @\"init$abitypes\"()",
				"call void @runtime.init()",
				"call ptr %",
				"call void @__llgo_coro_await_prepare_v1",
				"call void @\"example.com/foo.main\"()",
				"call void @"+coroProgramMainReturnSymbolV1,
				"call void @"+coroProgramCompletePrepareHookV1,
			)

			entryBody := mod.NamedFunction(test.entryName).String()
			for _, legacyCall := range []string{
				"call void @\"" + llssa.PkgRuntime + ".init\"()",
				"call void @\"init$abitypes\"()",
				"call void @runtime.init()",
				"call void @\"example.com/foo.init\"()",
				"call void @\"example.com/foo.main\"()",
			} {
				if strings.Contains(entryBody, legacyCall) {
					t.Fatalf("mixed v2 platform entry retained legacy call %q:\n%s", legacyCall, entryBody)
				}
			}
			driverCall := "call void @" + coroProgramRunSymbolV1
			if nativeCoroDoorbellRuntimeABI(ctx.buildConf) {
				driverCall = "call i32 @" + coroProgramRunSliceSymbolV2
			}
			assertInOrder(t, entryBody,
				"call void @"+coroFrameAllocatorBootstrapSymbolV1+"()",
				"call void @Py_Initialize()",
				"call ptr @"+coroProgramBeginSymbolV1,
				"call ptr @"+coroProgramBootstrapFactorySymbolV2,
				driverCall,
				"call void @Py_Finalize()",
			)
			if err := llvm.VerifyModule(mod, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify mixed v2 main module before coroutine passes: %v\n%s", err, ir)
			}
			if err := lowerCoroControlWrappers(ctx, entry.LPkg); err != nil {
				t.Fatalf("lower mixed v2 main module coroutine: %v\n%s", err, entry.LPkg.String())
			}
			post := mod.String()
			for _, suffix := range []string{".resume", ".destroy"} {
				if mod.NamedFunction(coroProgramBootstrapFactorySymbolV2 + suffix).IsNil() {
					t.Fatalf("main-module CoroSplit did not create mixed v2 factory%s:\n%s", suffix, post)
				}
			}
			for _, intrinsic := range []string{"llvm.coro.id", "llvm.coro.begin", "llvm.coro.suspend", "llvm.coro.resume", "llvm.coro.done", "llvm.coro.destroy"} {
				if regexp.MustCompile(`call [^\n]*@` + regexp.QuoteMeta(intrinsic) + `\b`).MatchString(post) {
					t.Fatalf("lowered mixed v2 main module still references %s:\n%s", intrinsic, post)
				}
			}
			object, err := prog.TargetMachine().EmitToMemoryBuffer(mod, llvm.ObjectFile)
			if err != nil {
				t.Fatalf("emit mixed v2 main-module object: %v\n%s", err, post)
			}
			object.Dispose()
		})
	}
}

func TestGenMainModuleCoroProgramBootstrapV2DefinesOnlyOwnedPublicRuntimeNoop(t *testing.T) {
	llvm.InitializeAllTargets()
	t.Setenv(llgoStdioNobuf, "")
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	ctx := &context{
		prog: prog,
		buildConf: &Config{
			BuildMode:                     BuildModeExe,
			Goos:                          "linux",
			Goarch:                        "amd64",
			EnableCoroEntryResolution:     true,
			EnableCoroPhysicalABI:         true,
			EnableCoroChildAwait:          true,
			EnableCoroProgramBootstrapABI: true,
			EnableCoroProgramBootstrapRun: true,
		},
	}
	bootstrap := &coroProgramBootstrapV1{
		Version: coroProgramBootstrapVersionV2,
		Steps: []coroProgramBootstrapStepV1{
			{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRoleRuntimeInitV2, FunctionID: "internal-runtime", Target: llssa.PkgRuntime + ".init"},
			{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRoleABIInitV2, FunctionID: "compiler-abi", Target: "init$abitypes"},
			{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRolePublicRuntimeInitV2, FunctionID: coroProgramPublicRuntimeNoopIDV2, Target: coroProgramPublicRuntimeNoopSymbolV2},
			{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRolePackageInitV2, FunctionID: "package-init", Target: "example.com/no-public-runtime.init"},
			{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRoleMainV2, FunctionID: "main", Target: "example.com/no-public-runtime.main"},
		},
	}
	entry := genMainModule(ctx, llssa.PkgRuntime,
		&packages.Package{ID: "example.com/no-public-runtime", PkgPath: "example.com/no-public-runtime", ExportFile: "no-public-runtime.a"},
		&genConfig{coroBootstrap: bootstrap},
	)
	module := entry.LPkg.Module()
	if function := module.NamedFunction(coroProgramPublicRuntimeNoopSymbolV2); function.IsNil() || function.IsDeclaration() {
		t.Fatalf("compiler-owned public runtime no-op is not defined:\n%s", module.String())
	}
	if function := module.NamedFunction("runtime.init"); !function.IsNil() {
		t.Fatalf("absent public runtime acquired a guessed runtime.init symbol:\n%s", module.String())
	}
	if function := module.NamedFunction("syscall.init"); !function.IsNil() {
		t.Fatalf("managed V2 entry retained a weak syscall.init interception body:\n%s", module.String())
	}
	if function := module.NamedFunction(coroProgramMainReturnSymbolV1); !function.IsNil() {
		t.Fatalf("V2 bootstrap without closed-static spawn declared main-return cancellation:\n%s", module.String())
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify absent-public-runtime v2 module: %v\n%s", err, module.String())
	}
}

func TestGenMainModuleCoroProgramBootstrapRuntimeSwitch(t *testing.T) {
	llvm.InitializeAllTargets()
	t.Setenv(llgoStdioNobuf, "")
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	ctx := &context{
		prog: prog,
		buildConf: &Config{
			BuildMode:                     BuildModeExe,
			Goos:                          "linux",
			Goarch:                        "amd64",
			EnableCoroEntryResolution:     true,
			EnableCoroPhysicalABI:         true,
			EnableCoroChildAwait:          true,
			EnableCoroProgramBootstrapABI: true,
			EnableCoroProgramBootstrapRun: true,
		},
	}
	var programHash [16]byte
	for i := range programHash {
		programHash[i] = byte(i + 1)
	}
	entry := genMainModule(ctx, llssa.PkgRuntime,
		&packages.Package{ID: "example.com/foo", PkgPath: "example.com/foo", ExportFile: "foo.a"},
		&genConfig{
			rtInit:           true,
			pyInit:           true,
			coroManifestHash: programHash,
			coroBootstrap: &coroProgramBootstrapV1{Steps: []coroProgramBootstrapStepV1{
				{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRoleInitV1, FunctionID: "init-id", Target: "example.com/foo.init"},
				{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRoleMainV1, FunctionID: "main-id", Target: "example.com/foo.main"},
			}},
		})
	ir := entry.LPkg.String()
	bootstrapLine := irLineWithPrefix(ir, "@"+coroProgramBootstrapSymbolV1+" =")
	if !strings.Contains(bootstrapLine, "ptr @"+coroProgramBootstrapFactorySymbolV1) {
		t.Fatalf("runnable bootstrap does not publish its factory: %s\n%s", bootstrapLine, ir)
	}
	factory := entry.LPkg.Module().NamedFunction(coroProgramBootstrapFactorySymbolV1)
	if factory.IsNil() || factory.IsDeclaration() {
		t.Fatalf("compiler-owned bootstrap factory is missing:\n%s", ir)
	}
	assertInOrder(t, factory.String(),
		"call void @\"example.com/foo.init\"()",
		"call void @\"example.com/foo.main\"()",
		"call void @"+coroProgramCompletePrepareHookV1,
	)
	entryBody := entry.LPkg.Module().NamedFunction("main").String()
	assertCoroProgramNativeSliceV2(t, entry.LPkg.Module(), "main")
	assertCoroNativePostWaitRetention(t, entry.LPkg.Module(), "main")
	if strings.Contains(entryBody, "call void @\"example.com/foo.init\"()") || strings.Contains(entryBody, "call void @\"example.com/foo.main\"()") {
		t.Fatalf("platform entry retained legacy direct init/main calls:\n%s", entryBody)
	}
	if got := strings.Count(entryBody, "call void @"+coroFrameAllocatorBootstrapSymbolV1+"()"); got != 1 {
		t.Fatalf("platform entry allocator bootstrap calls = %d, want exactly one:\n%s", got, entryBody)
	}
	assertInOrder(t, entryBody,
		"call void @"+coroFrameAllocatorBootstrapSymbolV1+"()",
		"call void @Py_Initialize()",
		"call void @\""+llssa.PkgRuntime+".init\"()",
		"call void @runtime.init()",
		"call ptr @"+coroProgramBeginSymbolV1,
		"call ptr @"+coroProgramBootstrapFactorySymbolV1,
		"call i32 @"+coroProgramRunSliceSymbolV2,
		"call void @Py_Finalize()",
	)
	if strings.Contains(entryBody, "call ptr %") {
		t.Fatalf("platform entry introduced indirect factory dispatch:\n%s", entryBody)
	}
}

func TestGenMainModuleCoroProgramBootstrapRuntimeAfterCoroPasses(t *testing.T) {
	llvm.InitializeAllTargets()
	t.Setenv(llgoStdioNobuf, "")
	tests := []struct {
		name   string
		target *llssa.Target
		goos   string
		goarch string
	}{
		{name: "native", goos: "linux", goarch: "amd64"},
		{name: "wasm", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}, goos: "wasip1", goarch: "wasm"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prog := llssa.NewProgram(test.target)
			defer prog.Dispose()
			ctx := &context{
				prog: prog,
				buildConf: &Config{
					BuildMode:                     BuildModeExe,
					Goos:                          test.goos,
					Goarch:                        test.goarch,
					EnableCoroEntryResolution:     true,
					EnableCoroPhysicalABI:         true,
					EnableCoroChildAwait:          true,
					EnableCoroProgramBootstrapABI: true,
					EnableCoroProgramBootstrapRun: true,
				},
			}
			entry := genMainModule(ctx, llssa.PkgRuntime,
				&packages.Package{ID: "example.com/foo", PkgPath: "example.com/foo", ExportFile: "foo.a"},
				&genConfig{coroBootstrap: &coroProgramBootstrapV1{Steps: []coroProgramBootstrapStepV1{
					{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRoleInitV1, FunctionID: "init-id", Target: "example.com/foo.init"},
					{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRoleMainV1, FunctionID: "main-id", Target: "example.com/foo.main"},
				}}})
			if err := lowerCoroControlWrappers(ctx, entry.LPkg); err != nil {
				t.Fatalf("lower production entry coroutine: %v\n%s", err, entry.LPkg.String())
			}
			mod := entry.LPkg.Module()
			post := mod.String()
			entryName := func() string {
				if isWasmTarget(test.goos) {
					return "__main_argc_argv"
				}
				return "main"
			}()
			if nativeCoroDoorbellRuntimeABI(ctx.buildConf) {
				assertCoroProgramNativeSliceV2(t, mod, entryName)
				assertCoroNativePostWaitRetention(t, mod, "main")
			} else {
				assertCoroProgramContinueRetention(t, mod, entryName)
				if callback := mod.NamedFunction(coroNativePostWaitSymbolV1); !callback.IsNil() {
					t.Fatalf("non-native lowered entry declared native post-wait callback:\n%s", post)
				}
			}
			for _, suffix := range []string{".resume", ".destroy"} {
				if mod.NamedFunction(coroProgramBootstrapFactorySymbolV1 + suffix).IsNil() {
					t.Fatalf("entry CoroSplit did not create factory%s:\n%s", suffix, post)
				}
			}
			for _, intrinsic := range []string{"llvm.coro.id", "llvm.coro.begin", "llvm.coro.suspend", "llvm.coro.resume", "llvm.coro.done", "llvm.coro.destroy"} {
				if regexp.MustCompile(`call [^\n]*@` + regexp.QuoteMeta(intrinsic) + `\b`).MatchString(post) {
					t.Fatalf("lowered production entry still references %s:\n%s", intrinsic, post)
				}
			}
			object, err := prog.TargetMachine().EmitToMemoryBuffer(mod, llvm.ObjectFile)
			if err != nil {
				t.Fatalf("emit production entry object: %v\n%s", err, post)
			}
			object.Dispose()
		})
	}
}

func assertCoroProgramNativeSliceV2(t *testing.T, module llvm.Module, entryName string) {
	t.Helper()
	run := module.NamedFunction(coroProgramRunSliceSymbolV2)
	if run.IsNil() || !run.IsDeclaration() || run.GlobalValueType().String() != "i32 (ptr, ptr, i32, ptr)" {
		t.Fatalf("native program run-slice declaration has the wrong ABI: %v\n%s", run, module.String())
	}
	continueRun := module.NamedFunction(coroProgramContinueSliceSymbolV2)
	if continueRun.IsNil() || !continueRun.IsDeclaration() || continueRun.GlobalValueType().String() != "i32 (i32, i32, i32, i32, ptr)" {
		t.Fatalf("native program continue-slice declaration has the wrong ABI: %v\n%s", continueRun, module.String())
	}
	if legacy := module.NamedFunction(coroProgramRunSymbolV1); !legacy.IsNil() {
		t.Fatalf("native V2 entry retained the legacy whole-program run ABI: %v\n%s", legacy, module.String())
	}
	if legacy := module.NamedFunction(coroProgramContinueSymbolV1); !legacy.IsNil() {
		t.Fatalf("native V2 entry retained the legacy callback ABI: %v\n%s", legacy, module.String())
	}
	if anchor := module.NamedGlobal(coroProgramContinueReferenceSymbolV1); !anchor.IsNil() {
		t.Fatalf("native V2 entry retained the legacy callback anchor: %v\n%s", anchor, module.String())
	}
	entry := module.NamedFunction(entryName)
	if entry.IsNil() || entry.IsDeclaration() {
		t.Fatalf("native V2 program entry %q is missing: %s", entryName, module.String())
	}
	body := entry.String()
	for _, want := range []string{
		"alloca { i32, i32, i32, i32, i32, i32, i32, i32 }",
		"call i32 @" + coroProgramRunSliceSymbolV2 + "(ptr",
		"i32 " + strconv.FormatUint(uint64(coroProgramNativeRunBudgetV2), 10),
		"phi i32",
		"icmp eq i32",
		"call i32 @" + coroProgramContinueSliceSymbolV2 + "(i32",
		"call void @abort()",
		"unreachable",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("native V2 program entry missing %q:\n%s", want, body)
		}
	}
	if got := strings.Count(body, "call i32 @"+coroProgramRunSliceSymbolV2); got != 1 {
		t.Fatalf("native V2 initial run calls = %d, want 1:\n%s", got, body)
	}
	if got := strings.Count(body, "call i32 @"+coroProgramContinueSliceSymbolV2); got != 1 {
		t.Fatalf("native V2 continuation calls = %d, want one fixed-stack loop edge:\n%s", got, body)
	}
	for label, pattern := range map[string]string{
		"complete status": `icmp eq i32 [^,\n]+, 1`,
		"yielded status":  `icmp eq i32 [^,\n]+, 3`,
		"inline flags":    `icmp eq i32 [^,\n]+, 9`,
		"bounded used":    `icmp ule i32 [^,\n]+, 1024`,
	} {
		if !regexp.MustCompile(pattern).MatchString(body) {
			t.Fatalf("native V2 entry has no exact %s check %q:\n%s", label, pattern, body)
		}
	}
}

func assertCoroProgramContinueRetention(t *testing.T, module llvm.Module, entryName string) {
	t.Helper()
	if run := module.NamedFunction(coroProgramRunSliceSymbolV2); !run.IsNil() {
		t.Fatalf("non-native V1 entry retained the native run-slice ABI: %v\n%s", run, module.String())
	}
	if continueRun := module.NamedFunction(coroProgramContinueSliceSymbolV2); !continueRun.IsNil() {
		t.Fatalf("non-native V1 entry retained the native continue-slice ABI: %v\n%s", continueRun, module.String())
	}
	callback := module.NamedFunction(coroProgramContinueSymbolV1)
	if callback.IsNil() || !callback.IsDeclaration() || callback.GlobalValueType().String() != "void (i32)" {
		t.Fatalf("program continuation declaration is not void(i32): %v\n%s", callback, module.String())
	}
	anchor := module.NamedGlobal(coroProgramContinueReferenceSymbolV1)
	if anchor.IsNil() || !anchor.IsGlobalConstant() || anchor.Linkage() != llvm.InternalLinkage ||
		anchor.Initializer().IsNil() || anchor.Initializer().C != callback.C {
		t.Fatalf("program continuation reference does not retain the exact callback: %v\n%s", anchor, module.String())
	}
	entry := module.NamedFunction(entryName)
	if entry.IsNil() || entry.IsDeclaration() {
		t.Fatalf("program continuation retention entry %q is missing: %s", entryName, module.String())
	}
	body := entry.String()
	if got := strings.Count(body, "load volatile ptr, ptr @"+coroProgramContinueReferenceSymbolV1); got != 1 {
		t.Fatalf("program entry continuation-reference volatile loads = %d, want 1:\n%s", got, body)
	}
	if strings.Contains(body, "call void @"+coroProgramContinueSymbolV1) {
		t.Fatalf("program entry invoked the asynchronous continuation during startup:\n%s", body)
	}
}

func assertCoroNativePostWaitRetention(t *testing.T, module llvm.Module, entryName string) {
	t.Helper()
	callback := module.NamedFunction(coroNativePostWaitSymbolV1)
	if callback.IsNil() || !callback.IsDeclaration() || callback.GlobalValueType().String() != "i32 (i32, i32, i32, i32)" {
		t.Fatalf("native post-wait declaration is not i32(i32,i32,i32,i32): %v\n%s", callback, module.String())
	}
	anchor := module.NamedGlobal(coroNativePostWaitReferenceSymbolV1)
	if anchor.IsNil() || !anchor.IsGlobalConstant() || anchor.Linkage() != llvm.InternalLinkage ||
		anchor.Initializer().IsNil() || anchor.Initializer().C != callback.C {
		t.Fatalf("native post-wait reference does not retain the exact callback: %v\n%s", anchor, module.String())
	}
	entry := module.NamedFunction(entryName)
	if entry.IsNil() || entry.IsDeclaration() {
		t.Fatalf("native post-wait retention entry %q is missing: %s", entryName, module.String())
	}
	body := entry.String()
	if got := strings.Count(body, "load volatile ptr, ptr @"+coroNativePostWaitReferenceSymbolV1); got != 1 {
		t.Fatalf("program entry native-post-wait-reference volatile loads = %d, want 1:\n%s", got, body)
	}
	if strings.Contains(body, "call i32 @"+coroNativePostWaitSymbolV1) {
		t.Fatalf("program entry invoked native post-wait callback during startup:\n%s", body)
	}
}

func irLineWithPrefix(ir, prefix string) string {
	for _, line := range strings.Split(ir, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	return ""
}

func TestGenMainModuleCoroControlWrappersAfterCoroPasses(t *testing.T) {
	llvm.InitializeAllTargets()
	t.Setenv(llgoStdioNobuf, "")
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	ctx := &context{
		prog: prog,
		buildConf: &Config{
			BuildMode:            BuildModeCArchive,
			Goos:                 "linux",
			Goarch:               "amd64",
			EnableCoroChildAwait: true,
		},
	}
	entry := genMainModule(ctx, llssa.PkgRuntime,
		&packages.Package{PkgPath: "example.com/foo", ExportFile: "foo.a"},
		&genConfig{})
	mod := entry.LPkg.Module()
	if err := llvm.VerifyModule(mod, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify coroutine control wrappers before passes: %v\n%s", err, mod.String())
	}
	if err := lowerCoroControlWrappers(ctx, entry.LPkg); err != nil {
		t.Fatalf("lower coroutine control wrappers: %v\n%s", err, mod.String())
	}
	if err := llvm.VerifyModule(mod, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify coroutine control wrappers after passes: %v\n%s", err, mod.String())
	}
	post := mod.String()
	for _, name := range []string{
		"__llgo_coro_resume_v1",
		"__llgo_coro_done_v1",
		"__llgo_coro_destroy_v1",
	} {
		fn := mod.NamedFunction(name)
		if fn.IsNil() || fn.IsDeclaration() {
			t.Fatalf("coroutine control wrapper %s missing after passes:\n%s", name, post)
		}
	}
	for _, intrinsic := range []string{
		"call void @llvm.coro.resume",
		"call i1 @llvm.coro.done",
		"call void @llvm.coro.destroy",
	} {
		if strings.Contains(post, intrinsic) {
			t.Fatalf("post-pass wrapper still calls %s:\n%s", intrinsic, post)
		}
	}
	object, err := prog.TargetMachine().EmitToMemoryBuffer(mod, llvm.ObjectFile)
	if err != nil {
		t.Fatalf("emit lowered coroutine control wrapper object: %v\n%s", err, post)
	}
	object.Dispose()
}

func assertInOrder(t *testing.T, s string, wants ...string) {
	t.Helper()
	offset := 0
	for _, want := range wants {
		i := strings.Index(s[offset:], want)
		if i < 0 {
			t.Fatalf("main module IR missing ordered entry %q after byte %d:\n%s", want, offset, s)
		}
		offset += i + len(want)
	}
}
