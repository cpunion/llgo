package build

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
)

func TestCgoDeferExportEntries(t *testing.T) {
	switch runtime.GOOS {
	case "darwin", "linux", "windows":
	default:
		t.Skip("hosted native C callbacks")
	}
	conf := NewDefaultConf(ModeGen)
	pkgs, err := Do([]string{"../../cl/_testgo/cgodefer"}, conf)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("got %d packages, want the cgodefer command package", len(pkgs))
	}
	pkg := pkgs[0]
	defer pkg.LPkg.Prog.Dispose()
	module := pkg.LPkg.Module()
	names := []string{"go_callback", "go_callback_c_only"}
	for _, name := range names {
		goName := "main." + name
		if fn := module.NamedFunction(goName); fn.IsNil() || fn.IsDeclaration() {
			t.Fatalf("package module is missing Go implementation %q:\n%s", goName, module.String())
		}
		if fn := module.NamedFunction(name); !fn.IsNil() && !fn.IsDeclaration() {
			t.Fatalf("package module defines C entry %q before final-link wrapping:\n%s", name, fn.String())
		}
	}

	// The package checks above and the final-link checks below must agree on
	// both symbols: the public C name guards the qualified Go implementation.
	for _, goos := range []string{"darwin", "linux", "windows"} {
		for _, mode := range []BuildMode{BuildModeExe, BuildModeCShared, BuildModeCArchive} {
			t.Run(goos+"/"+string(mode), func(t *testing.T) {
				ctx := &context{
					prog: llssa.NewProgram(nil),
					buildConf: &Config{
						BuildMode: mode, Goos: goos, Goarch: runtime.GOARCH,
					},
				}
				defer ctx.prog.Dispose()
				exports, err := linkedCExports(ctx, pkgs)
				if err != nil {
					t.Fatal(err)
				}
				if len(exports) != len(names) {
					t.Fatalf("got %d C exports, want %d", len(exports), len(names))
				}
				entry := genMainModule(ctx, llssa.PkgRuntime, pkg.Package, &genConfig{
					rtInit: true, cExports: exports,
				}).LPkg.Module()
				if err := llvm.VerifyModule(entry, llvm.ReturnStatusAction); err != nil {
					t.Fatalf("invalid entry module: %v\n%s", err, entry.String())
				}
				for _, name := range names {
					goName := "main." + name
					wrapper := entry.NamedFunction(name)
					if wrapper.IsNil() || wrapper.IsDeclaration() {
						t.Fatalf("final-link module is missing public C entry %q:\n%s", name, entry.String())
					}
					if fn := entry.NamedFunction(goName); fn.IsNil() || !fn.IsDeclaration() {
						t.Fatalf("final-link module must reference the package's Go implementation %q:\n%s", goName, entry.String())
					}
					ir := wrapper.String()
					assertInOrder(t, ir,
						"define i32 @"+name+"(i32 %0)",
						`call i1 @"github.com/xgo-dev/llgo/runtime/internal/runtime.EnterForeignThread"()`,
						"call i32 @"+goName+"(i32 %0)",
						`call void @"github.com/xgo-dev/llgo/runtime/internal/runtime.ExitForeignThread"(i1`,
						"ret i32",
					)
					if goos == "windows" && mode == BuildModeCShared {
						assertInOrder(t, ir,
							"call void @__llgo_runtime_ensure_initialized()",
							`call i1 @"github.com/xgo-dev/llgo/runtime/internal/runtime.EnterForeignThread"()`,
						)
					}
				}
			})
		}
	}
}

func TestCExportForeignThreadsFromExecutableAndDependency(t *testing.T) {
	switch runtime.GOOS {
	case "darwin", "linux", "windows":
	default:
		t.Skip("hosted native C callbacks")
	}
	dir, err := filepath.Abs("testdata/foreigncallback")
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "callback")
	if runtime.GOOS == "windows" {
		output += ".exe"
	}
	t.Chdir(dir)
	conf := NewDefaultConf(ModeBuild)
	conf.OutFile = output
	if _, err := Do([]string{"."}, conf); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(output).CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != "ok" {
		t.Fatalf("native thread C exports: %v\n%s", err, out)
	}
}
