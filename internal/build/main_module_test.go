//go:build !llgo
// +build !llgo

package build

import (
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
			assertInOrder(t, ir,
				"call void @\"example.com/foo.init\"()",
				"call void @\"example.com/foo.main\"()",
			)
		})
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
