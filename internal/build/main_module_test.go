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
	bootstrap := &coroProgramBootstrapV1{
		Version: coroProgramBootstrapVersionV2,
		Steps: []coroProgramBootstrapStepV1{
			{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRoleRuntimeInitV2, FunctionID: "runtime-init", Target: llssa.PkgRuntime + ".init"},
			{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRoleABIInitV2, FunctionID: "abi-init", Target: "init$abitypes"},
			{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRolePublicRuntimeInitV2, FunctionID: coroProgramPublicRuntimeNoopIDV2, Target: coroProgramPublicRuntimeNoopSymbolV2},
			{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRolePackageInitV2, FunctionID: "package-init", Target: pkg.PkgPath + ".init"},
			{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRoleMainV2, FunctionID: "main", Target: pkg.PkgPath + ".main"},
		},
	}
	mod := genMainModule(ctx, llssa.PkgRuntime, pkg,
		&genConfig{rtInit: true, pyInit: true, coroBootstrap: bootstrap})
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
	factory := mod.LPkg.Module().NamedFunction(coroProgramBootstrapFactorySymbolV2).String()
	assertInOrder(t, factory,
		"call void @\"example.com/foo.init\"()",
		"call void @\"example.com/foo.main\"()",
	)
	entry := mod.LPkg.Module().NamedFunction("main").String()
	assertInOrder(t, entry,
		"call void @Py_Initialize()",
		"call ptr @"+coroProgramBeginSymbolV1,
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
	if !strings.Contains(ir, "@llvm.global_ctors") {
		t.Fatalf("library mode missing constructor:\n%s", ir)
	}
}

func TestGenMainModuleLibraryInitializesRuntime(t *testing.T) {
	llvm.InitializeAllTargets()
	t.Setenv(llgoStdioNobuf, "")
	for _, mode := range []BuildMode{BuildModeCArchive, BuildModeCShared} {
		t.Run(string(mode), func(t *testing.T) {
			ctx := &context{
				prog: llssa.NewProgram(nil),
				buildConf: &Config{
					BuildMode: mode,
					Goos:      "linux",
					Goarch:    "amd64",
				},
			}
			pkg := &packages.Package{PkgPath: "example.com/foo", ExportFile: "foo.a"}
			mod := genMainModule(ctx, llssa.PkgRuntime, pkg, &genConfig{rtInit: true})
			ir := mod.LPkg.String()
			checks := []string{
				"@llvm.global_ctors = appending global",
				"define internal void @__llgo_runtime_ctor()",
				"call void @\"github.com/goplus/llgo/runtime/internal/runtime.init\"()",
			}
			for _, want := range checks {
				if !strings.Contains(ir, want) {
					t.Fatalf("library module IR missing %q:\n%s", want, ir)
				}
			}
			if strings.Contains(ir, "define i32 @main") {
				t.Fatalf("library mode should not emit main function:\n%s", ir)
			}
		})
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
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prog := llssa.NewProgram(test.target)
			defer prog.Dispose()
			ctx := &context{
				prog: prog,
				buildConf: &Config{
					BuildMode: test.buildMode,
					Goos:      test.goos,
					Goarch:    test.goarch},
			}
			bootstrap := &coroProgramBootstrapV1{
				Version: coroProgramBootstrapVersionV2,
				Steps: []coroProgramBootstrapStepV1{
					{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRoleRuntimeInitV2, FunctionID: "runtime-init", Target: llssa.PkgRuntime + ".init"},
					{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRoleABIInitV2, FunctionID: "abi-init", Target: "init$abitypes"},
					{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRolePublicRuntimeInitV2, FunctionID: coroProgramPublicRuntimeNoopIDV2, Target: coroProgramPublicRuntimeNoopSymbolV2},
					{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRolePackageInitV2, FunctionID: "package-init", Target: "example.com/foo.init"},
					{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRoleMainV2, FunctionID: "main", Target: "example.com/foo.main"},
				},
			}
			mod := genMainModule(ctx, llssa.PkgRuntime,
				&packages.Package{PkgPath: "example.com/foo", ExportFile: "foo.a"},
				&genConfig{coroBootstrap: bootstrap})
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
		})
	}
}

func TestGenMainModuleAlwaysOwnsCoroControlWrappers(t *testing.T) {
	llvm.InitializeAllTargets()
	t.Setenv(llgoStdioNobuf, "")
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	ctx := &context{
		prog: prog,
		buildConf: &Config{
			BuildMode: BuildModeCShared,
			Goos:      "linux",
			Goarch:    "amd64",
		},
	}
	mod := genMainModule(ctx, llssa.PkgRuntime,
		&packages.Package{PkgPath: "example.com/foo", ExportFile: "foo.a"},
		&genConfig{})
	for _, name := range []string{"__llgo_coro_resume_v1", "__llgo_coro_done_v1", "__llgo_coro_destroy_v1"} {
		fn := mod.LPkg.Module().NamedFunction(name)
		if fn.IsNil() || fn.IsDeclaration() {
			t.Fatalf("single stackless architecture did not define %q:\n%s", name, mod.LPkg.String())
		}
	}
}

func TestGenMainModuleRuntimeLinkedLibraryOwnsCoroControlWrappers(t *testing.T) {
	llvm.InitializeAllTargets()
	t.Setenv(llgoStdioNobuf, "")
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	ctx := &context{
		prog: prog,
		buildConf: &Config{
			BuildMode: BuildModeCShared,
			Goos:      "linux",
			Goarch:    "amd64",
		},
	}
	entry := genMainModule(ctx, llssa.PkgRuntime,
		&packages.Package{PkgPath: "example.com/library", ExportFile: "library.a"},
		&genConfig{rtInit: true})
	for _, name := range []string{
		"__llgo_coro_resume_v1",
		"__llgo_coro_done_v1",
		"__llgo_coro_destroy_v1",
	} {
		fn := entry.LPkg.Module().NamedFunction(name)
		if fn.IsNil() || fn.IsDeclaration() {
			t.Fatalf("runtime-linked library is missing compiler-owned wrapper %s:\n%s", name, entry.LPkg.String())
		}
	}
	if err := lowerCoroControlWrappers(ctx, entry.LPkg); err != nil {
		t.Fatalf("lower runtime-linked library wrappers: %v\n%s", err, entry.LPkg.String())
	}
	post := entry.LPkg.String()
	for _, intrinsic := range []string{"llvm.coro.resume", "llvm.coro.done", "llvm.coro.destroy"} {
		if strings.Contains(post, "call ") && strings.Contains(post, "@"+intrinsic) {
			t.Fatalf("runtime-linked library retained %s after lowering:\n%s", intrinsic, post)
		}
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
					BuildMode: BuildModeExe,
					Goos:      test.goos,
					Goarch:    test.goarch},
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
			} else if hostCoroPullRuntimeABI(ctx.buildConf) {
				assertCoroProgramHostSliceV2(t, mod, test.entryName)
				assertCoroHostPullRetentionV1(t, mod, test.entryName)
			} else {
				assertCoroProgramContinueRetention(t, mod, test.entryName)
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
			if nativeCoroDoorbellRuntimeABI(ctx.buildConf) || hostCoroPullRuntimeABI(ctx.buildConf) {
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

func TestGenMainModuleCoroProgramBootstrapV2DefinesOwnedPublicRuntimeNoop(t *testing.T) {
	llvm.InitializeAllTargets()
	t.Setenv(llgoStdioNobuf, "")
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	ctx := &context{
		prog: prog,
		buildConf: &Config{
			BuildMode: BuildModeExe,
			Goos:      "linux",
			Goarch:    "amd64"},
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
	if function := module.NamedFunction(coroProgramMainReturnSymbolV1); function.IsNil() || !function.IsDeclaration() {
		t.Fatalf("unique stackless bootstrap did not declare main-return cancellation:\n%s", module.String())
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify absent-public-runtime v2 module: %v\n%s", err, module.String())
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
	// Used counts only certified reductions. Fleet-transfer and compatibility
	// bookkeeping may legitimately request another inline pass with zero Used;
	// every budget-checked Used SSA value must therefore remain zero-admissible.
	boundedUses := regexp.MustCompile(`icmp ule i32 ([^,\n]+), 1024`).FindAllStringSubmatch(body, -1)
	if len(boundedUses) < 2 {
		t.Fatalf("native V2 entry has %d bounded Used checks, want complete and yielded:\n%s", len(boundedUses), body)
	}
	for _, match := range boundedUses {
		if strings.Contains(body, "icmp ne i32 "+match[1]+", 0") {
			t.Fatalf("native V2 entry rejects zero certified reductions for %s:\n%s", match[1], body)
		}
	}
}

func assertCoroProgramHostSliceV2(t *testing.T, module llvm.Module, entryName string) {
	t.Helper()
	run := module.NamedFunction(coroProgramRunSliceSymbolV2)
	if run.IsNil() || !run.IsDeclaration() || run.GlobalValueType().String() != "i32 (ptr, ptr, i32, ptr)" {
		t.Fatalf("host program run-slice declaration has the wrong ABI: %v\n%s", run, module.String())
	}
	continueRun := module.NamedFunction(coroProgramContinueSliceSymbolV2)
	// The entry never invokes ContinueSlice directly. Before cleanup it is an
	// exact declaration; after CoroSplit/global cleanup it may be removed from
	// this module because the retained runtime host wrapper owns that edge.
	if !continueRun.IsNil() && (!continueRun.IsDeclaration() || continueRun.GlobalValueType().String() != "i32 (i32, i32, i32, i32, ptr)") {
		t.Fatalf("host program continue-slice declaration has the wrong ABI: %v\n%s", continueRun, module.String())
	}
	if legacy := module.NamedFunction(coroProgramRunSymbolV1); !legacy.IsNil() {
		t.Fatalf("host V2 entry retained the legacy whole-program run ABI: %v\n%s", legacy, module.String())
	}
	if legacy := module.NamedFunction(coroProgramContinueSymbolV1); !legacy.IsNil() {
		t.Fatalf("host V2 entry retained the legacy callback ABI: %v\n%s", legacy, module.String())
	}
	if anchor := module.NamedGlobal(coroProgramContinueReferenceSymbolV1); !anchor.IsNil() {
		t.Fatalf("host V2 entry retained the legacy callback anchor: %v\n%s", anchor, module.String())
	}
	entry := module.NamedFunction(entryName)
	if entry.IsNil() || entry.IsDeclaration() {
		t.Fatalf("host V2 program entry %q is missing: %s", entryName, module.String())
	}
	body := entry.String()
	for _, want := range []string{
		"alloca { i32, i32, i32, i32, i32, i32, i32, i32 }",
		"call i32 @" + coroProgramRunSliceSymbolV2 + "(ptr",
		"i32 " + strconv.FormatUint(uint64(coroProgramNativeRunBudgetV2), 10),
		"icmp eq i32",
		"icmp ule i32",
		"call void @abort()",
		"unreachable",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("host V2 program entry missing %q:\n%s", want, body)
		}
	}
	if got := strings.Count(body, "call i32 @"+coroProgramRunSliceSymbolV2); got != 1 {
		t.Fatalf("host V2 initial run calls = %d, want exactly one bounded activation:\n%s", got, body)
	}
	if strings.Contains(body, "call i32 @"+coroProgramContinueSliceSymbolV2) {
		t.Fatalf("host V2 entry recursively drove a continuation instead of returning to the embedding:\n%s", body)
	}
	for label, pattern := range map[string]string{
		"complete status":  `icmp eq i32 [^,\n]+, 1`,
		"suspended status": `icmp eq i32 [^,\n]+, 2`,
		"yielded status":   `icmp eq i32 [^,\n]+, 3`,
		"queued flags":     `icmp eq i32 [^,\n]+, 17`,
		"blocked mask":     `and i32 [^,\n]+, 6`,
		"bounded used":     `icmp ule i32 [^,\n]+, 1024`,
	} {
		if !regexp.MustCompile(pattern).MatchString(body) {
			t.Fatalf("host V2 entry has no exact %s check %q:\n%s", label, pattern, body)
		}
	}
	// Complete owns the ordinary return path. Both async states share a second
	// direct return, so a hand-built module with Python declarations cannot run
	// Py_Finalize while scheduler work remains host-owned.
	if got := strings.Count(body, "ret i32 0"); got != 2 {
		t.Fatalf("host V2 entry return paths = %d, want complete and detached returns:\n%s", got, body)
	}
	if strings.Count(body, "call void @Py_Finalize()") > 1 {
		t.Fatalf("host V2 entry duplicated Py_Finalize across detached paths:\n%s", body)
	}
}

func assertCoroHostPullRetentionV1(t *testing.T, module llvm.Module, entryName string) {
	t.Helper()
	wants := []struct {
		symbol, reference, functionType string
	}{
		{coroHostNextActionSymbolV1, coroHostNextActionReferenceSymbolV1, "i32 (ptr)"},
		{coroHostProfileSymbolV1, coroHostProfileReferenceSymbolV1, "i32 ()"},
		{coroHostNextDeadlineSymbolV1, coroHostNextDeadlineReferenceSymbolV1, "i1 (ptr)"},
		{coroHostPublishTimeSymbolV1, coroHostPublishTimeReferenceSymbolV1, "i1 (i32, i32)"},
		{coroHostAckCancelSymbolV1, coroHostAckCancelReferenceSymbolV1, "i1 (i32, i32, i32, i32)"},
		{coroHostContinueSliceSymbolV1, coroHostContinueSliceReferenceSymbolV1, "i32 (i32, i32, i32, i32, i32, i32, i32, ptr)"},
		{coroHostNextOperationSymbolV1, coroHostNextOperationReferenceSymbolV1, "i32 (ptr)"},
		{coroHostCompleteOperationSymbolV1, coroHostCompleteOperationReferenceSymbolV1, "i32 (i32, i32, i32, i32, i32, i32, i32, i32, i32, i32)"},
	}
	entry := module.NamedFunction(entryName)
	if entry.IsNil() || entry.IsDeclaration() {
		t.Fatalf("host-pull retention entry %q is missing: %s", entryName, module.String())
	}
	body := entry.String()
	for _, want := range wants {
		callback := module.NamedFunction(want.symbol)
		if callback.IsNil() || !callback.IsDeclaration() || callback.GlobalValueType().String() != want.functionType {
			t.Fatalf("host-pull callback %q has wrong declaration %v, want %s:\n%s", want.symbol, callback, want.functionType, module.String())
		}
		anchor := module.NamedGlobal(want.reference)
		if anchor.IsNil() || !anchor.IsGlobalConstant() || anchor.Linkage() != llvm.InternalLinkage ||
			anchor.Initializer().IsNil() || anchor.Initializer().C != callback.C {
			t.Fatalf("host-pull reference %q does not retain exact callback %q: %v\n%s", want.reference, want.symbol, anchor, module.String())
		}
		if got := strings.Count(body, "load volatile ptr, ptr @"+want.reference); got != 1 {
			t.Fatalf("host-pull reference %q volatile loads = %d, want 1:\n%s", want.reference, got, body)
		}
		if regexp.MustCompile(`call [^\n]*@` + regexp.QuoteMeta(want.symbol) + `\(`).MatchString(body) {
			t.Fatalf("program entry invoked host callback %q during startup:\n%s", want.symbol, body)
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
			BuildMode: BuildModeExe,
			Goos:      "linux",
			Goarch:    "amd64"},
	}
	bootstrap := &coroProgramBootstrapV1{
		Version: coroProgramBootstrapVersionV2,
		Steps: []coroProgramBootstrapStepV1{
			{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRoleRuntimeInitV2, FunctionID: "runtime-init", Target: llssa.PkgRuntime + ".init"},
			{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRoleABIInitV2, FunctionID: "abi-init", Target: "init$abitypes"},
			{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRolePublicRuntimeInitV2, FunctionID: coroProgramPublicRuntimeNoopIDV2, Target: coroProgramPublicRuntimeNoopSymbolV2},
			{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRolePackageInitV2, FunctionID: "package-init", Target: "example.com/foo.init"},
			{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRoleMainV2, FunctionID: "main", Target: "example.com/foo.main"},
		},
	}
	entry := genMainModule(ctx, llssa.PkgRuntime,
		&packages.Package{PkgPath: "example.com/foo", ExportFile: "foo.a"},
		&genConfig{coroBootstrap: bootstrap})
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
