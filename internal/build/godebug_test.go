package build

import (
	"encoding/json"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/lto"
	"github.com/xgo-dev/llgo/internal/optlevel"
	"github.com/xgo-dev/llgo/internal/packages"
	llssa "github.com/xgo-dev/llgo/ssa"
)

const godebugMainSource = `package main
import "math/rand"
func seeded() bool { rand.Seed(1); a := rand.Int63(); rand.Seed(1); return a == rand.Int63() }
var initialized = seeded()
func main() { println(initialized, seeded()) }
`

func godebugFixture(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds GODEBUG applications")
	}
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLGO_ROOT", repo)
	t.Setenv("GOENV", "off")
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOTOOLCHAIN", "local")
	t.Setenv("GOWORK", "")
	t.Setenv("GODEBUG", "")
	t.Setenv(llgoBuildCache, "1")
	return t.TempDir()
}

func godebugConfig(mode Mode) *Config {
	return &Config{Mode: mode, Goos: runtime.GOOS, Goarch: runtime.GOARCH, OptLevel: optlevel.O0}
}

func godebugGo(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go %v: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func godebugAssertProgram(t *testing.T, app, want string) {
	t.Helper()
	output, err := exec.Command(app).CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != want {
		t.Fatalf("GODEBUG application: %v, output %q, want %q", err, output, want)
	}
}

func TestDefaultGODEBUGPrecedenceAndCache(t *testing.T) {
	root := godebugFixture(t)
	for _, tc := range []struct {
		name, mod, work, source, env, want string
	}{
		{"module-version", "go 1.23", "", "", "", "true true"},
		{"current-version", "go 1.27.0", "", "", "", "false false"},
		{"default-version", "go 1.27.0\ngodebug default=go1.23", "", "", "", "true true"},
		{"module-directive", "go 1.27.0\ngodebug randseednop=0", "", "", "", "true true"},
		{"workspace-version", "go 1.27.0\ngodebug randseednop=0", "go 1.27.0", "", "", "false false"},
		{"workspace-directive", "go 1.27.0\ngodebug randseednop=1", "go 1.27.0\ngodebug randseednop=0", "", "", "true true"},
		{"source-directive", "go 1.27.0", "go 1.27.0\ngodebug randseednop=1", "//go:debug randseednop=0\n", "", "true true"},
		{"source-change", "go 1.27.0", "go 1.27.0\ngodebug randseednop=0", "//go:debug randseednop=1\n", "", "false false"},
		{"environment-zero", "go 1.27.0", "go 1.27.0", "//go:debug randseednop=1\n", "randseednop=0", "true true"},
		{"environment-one", "go 1.27.0", "go 1.27.0\ngodebug randseednop=0", "", "randseednop=1", "false false"},
		{"remove-workspace-default", "go 1.27.0", "go 1.27.0", "", "", "false false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeFile(t, filepath.Join(root, "go.mod"), "module example.com/debugapp\n"+tc.mod+"\n")
			writeFile(t, filepath.Join(root, "main.go"), tc.source+godebugMainSource)
			if tc.work == "" {
				_ = os.Remove(filepath.Join(root, "go.work"))
			} else {
				writeFile(t, filepath.Join(root, "go.work"), tc.work+"\nuse .\n")
			}
			t.Setenv("GODEBUG", tc.env)
			if got := godebugGo(t, root, "run", "."); got != tc.want {
				t.Fatalf("Go baseline %q, want %q", got, tc.want)
			}
			conf := godebugConfig(ModeBuild)
			conf.OutFile = filepath.Join(t.TempDir(), "app"+defaultAppExt(conf))
			if _, err := Build(Invocation{Dir: root, Config: conf}); err != nil {
				t.Fatal(err)
			}
			godebugAssertProgram(t, conf.OutFile, tc.want)
		})
	}
}

func TestDefaultGODEBUGMultipleExecutables(t *testing.T) {
	root := godebugFixture(t)
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/debugmulti\ngo 1.27.0\n")
	for name, value := range map[string]string{"first": "0", "second": "1"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(root, name, "main.go"), "//go:debug randseednop="+value+"\n"+godebugMainSource)
	}
	conf := godebugConfig(ModeBuild)
	conf.OutFile = filepath.Join(root, "bin") + string(os.PathSeparator)
	if _, err := Build(Invocation{Dir: root, Args: []string{"./first", "./second"}, Config: conf}); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"first": "true true", "second": "false false"} {
		if got := godebugGo(t, root, "run", "./"+name); got != want {
			t.Fatalf("Go baseline %q, want %q", got, want)
		}
		godebugAssertProgram(t, filepath.Join(root, "bin", name+defaultAppExt(conf)), want)
	}
}

func TestDefaultGODEBUGTestMain(t *testing.T) {
	root := godebugFixture(t)
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/debugtest\ngo 1.27.0\n")
	writeFile(t, filepath.Join(root, "go.work"), "go 1.27.0\nuse .\ngodebug randseednop=1\n")
	writeFile(t, filepath.Join(root, "value.go"), "package value\n")
	const source = `//go:debug randseednop=0
package value
import ("math/rand"; "testing")
func seeded() bool { rand.Seed(1); a := rand.Int63(); rand.Seed(1); return a == rand.Int63() }
var initialized = seeded()
func TestDefaults(t *testing.T) { if !initialized || !seeded() { t.Fatal("test main defaults not initialized") } }
`
	writeFile(t, filepath.Join(root, "value_test.go"), source)
	godebugGo(t, root, "test", "-count=1", ".")
	if _, err := Build(Invocation{Dir: root, Config: godebugConfig(ModeTest)}); err != nil {
		t.Fatal(err)
	}
	// External test directives contribute to the same generated main.
	writeFile(t, filepath.Join(root, "value_test.go"), strings.TrimPrefix(source, "//go:debug randseednop=0\n"))
	writeFile(t, filepath.Join(root, "external_test.go"), strings.Replace(source, "package value", "package value_test", 1))
	if err := os.Mkdir(filepath.Join(root, "other"), 0755); err != nil {
		t.Fatal(err)
	}
	other := strings.ReplaceAll(source, "randseednop=0", "randseednop=1")
	other = strings.ReplaceAll(other, "package value", "package other")
	other = strings.ReplaceAll(other, "!initialized || !seeded()", "initialized || seeded()")
	writeFile(t, filepath.Join(root, "other", "other_test.go"), other)
	godebugGo(t, root, "test", "-count=1", "./...")
	if _, err := Build(Invocation{Dir: root, Args: []string{"./..."}, Config: godebugConfig(ModeTest)}); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultGODEBUGSourceSelection(t *testing.T) {
	root := godebugFixture(t)
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/debugselect\ngo 1.27.0\n")
	writeFile(t, filepath.Join(root, "main.go"), "//go:build !debugdefaults\n//go:debug randseednop=1\n"+godebugMainSource)
	writeFile(t, filepath.Join(root, "selected.go"), "//go:build debugdefaults\n//go:debug randseednop=0\n"+godebugMainSource)
	conf := godebugConfig(ModeBuild)
	conf.Tags = "debugdefaults"
	conf.OutFile = filepath.Join(root, "app"+defaultAppExt(conf))
	if got := godebugGo(t, root, "run", "-tags=debugdefaults", "."); got != "true true" {
		t.Fatalf("Go baseline %q", got)
	}
	if _, err := Build(Invocation{Dir: root, Config: conf}); err != nil {
		t.Fatal(err)
	}
	godebugAssertProgram(t, conf.OutFile, "true true")

	// A caller's Go overlay must contribute directives as well as Go code.
	writeFile(t, filepath.Join(root, "selected.go"), "//go:build debugdefaults\n//go:debug randseednop=1\n"+godebugMainSource)
	overlaid := filepath.Join(t.TempDir(), "selected.go")
	writeFile(t, overlaid, "//go:build debugdefaults\n//go:debug randseednop=0\n"+godebugMainSource)
	data, err := json.Marshal(struct{ Replace map[string]string }{map[string]string{filepath.Join(root, "selected.go"): overlaid}})
	if err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(root, "overlay.json")
	writeFile(t, overlay, string(data))
	conf.GoBuildFlags = []string{"-overlay=" + overlay}
	if got := godebugGo(t, root, "run", "-tags=debugdefaults", "-overlay="+overlay, "."); got != "true true" {
		t.Fatalf("Go overlay baseline %q", got)
	}
	if _, err := Build(Invocation{Dir: root, Config: conf}); err != nil {
		t.Fatal(err)
	}
	godebugAssertProgram(t, conf.OutFile, "true true")
}

func TestDefaultGODEBUGOptimized(t *testing.T) {
	root := godebugFixture(t)
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/debugopt\ngo 1.27.0\ngodebug randseednop=0\n")
	writeFile(t, filepath.Join(root, "main.go"), godebugMainSource)
	for _, mode := range []lto.Mode{lto.Off, lto.Thin, lto.Full} {
		t.Run(mode.String(), func(t *testing.T) {
			conf := godebugConfig(ModeBuild)
			conf.OptLevel, conf.LTO = optlevel.O2, mode
			conf.OutFile = filepath.Join(t.TempDir(), "app"+defaultAppExt(conf))
			if _, err := Build(Invocation{Dir: root, Config: conf}); err != nil {
				t.Fatal(err)
			}
			godebugAssertProgram(t, conf.OutFile, "true true")
		})
	}
}

func TestDefaultGODEBUGEntryInitialization(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		for _, mode := range []BuildMode{BuildModeExe, BuildModeCArchive, BuildModeCShared} {
			t.Run(goos+"/"+string(mode), func(t *testing.T) {
				ctx := &context{prog: llssa.NewProgram(nil), buildConf: &Config{Goos: goos, Goarch: "amd64", BuildMode: mode}}
				defer ctx.prog.Dispose()
				rt := types.NewPackage(llssa.PkgRuntime, "runtime")
				str := types.NewNamed(types.NewTypeName(token.NoPos, rt, "String", nil), types.NewStruct([]*types.Var{
					types.NewField(token.NoPos, rt, "Data", types.NewPointer(types.Typ[types.Byte]), false),
					types.NewField(token.NoPos, rt, "Len", types.Typ[types.Int], false),
				}, nil), nil)
				rt.Scope().Insert(str.Obj())
				ctx.prog.SetRuntime(rt)
				pkg := &packages.Package{PkgPath: "example.com/debugentry"}
				cfg := &genConfig{rtInit: true, defaultGODEBUG: "randseednop=0", abiSymbols: map[string]none{"runtime.godebugDefault": {}}}
				entry := genMainModule(ctx, llssa.PkgRuntime, pkg, cfg)
				ir := entry.LPkg.String()
				assertInOrder(t, ir, "call void @__llgo_godebug_init()", `call void @"github.com/xgo-dev/llgo/runtime/internal/runtime.init"()`)
				if !strings.Contains(ir, "@runtime.godebugDefault = external global") {
					t.Fatalf("default setter must reference shared runtime storage:\n%s", ir)
				}
			})
		}
	}
}
