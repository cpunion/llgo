//go:build !llgo

package build

import (
	"debug/buildinfo"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/debug"
	"testing"

	"github.com/xgo-dev/llgo/internal/packages"
	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
	gopackages "golang.org/x/tools/go/packages"
)

func TestLinkedBuildInfo(t *testing.T) {
	main := &gopackages.Module{Path: "example.org/app", Main: true}
	root := &packages.Package{PkgPath: "example.org/app/cmd", Module: main}
	dep := &gopackages.Module{Path: "example.org/dep", Version: "v1.2.3", Replace: &gopackages.Module{Path: "../local"}}
	linked := []Package{
		{Package: root},
		{Package: &packages.Package{Module: &gopackages.Module{Path: "example.org/z", Version: "v0.1.0"}}},
		{Package: &packages.Package{Module: dep}},
		{Package: &packages.Package{Module: dep}},
		{Package: &packages.Package{PkgPath: "fmt"}},
	}
	conf := &Config{Goos: "linux", Goarch: "arm64", BuildMode: BuildModeExe}
	framed := linkedBuildInfo(conf, root, linked)
	info, err := debug.ParseBuildInfo(framed[16 : len(framed)-16])
	if err != nil {
		t.Fatal(err)
	}
	if info.Path != root.PkgPath || info.Main.Path != main.Path || info.Main.Version != "(devel)" {
		t.Fatalf("main build information = %#v", info)
	}
	if len(info.Deps) != 2 || info.Deps[0].Path != dep.Path || info.Deps[0].Version != dep.Version ||
		info.Deps[0].Replace == nil || info.Deps[0].Replace.Path != "../local" || info.Deps[1].Path != "example.org/z" {
		t.Fatalf("dependency build information = %#v", info.Deps)
	}
	reversed := append([]Package(nil), linked...)
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	if got := linkedBuildInfo(conf, root, reversed); got != framed {
		t.Fatal("build information depends on package traversal order")
	}
}

// Read the final linked images with the public Go parser. Section collection
// is enabled so a merely emitted, unretained record cannot satisfy this test.
func TestBuildInfoSurvivesNativeLink(t *testing.T) {
	for _, target := range []struct{ goos, goarch, linker string }{
		{"linux", "amd64", "ld.lld"},
		{"linux", "386", "ld.lld"},
		{"linux", "ppc64", "ld.lld"},
		{"windows", "amd64", "lld-link"},
		{"darwin", runtime.GOARCH, "clang"},
	} {
		t.Run(target.goos+"/"+target.goarch, func(t *testing.T) {
			if target.goos == "darwin" && runtime.GOOS != "darwin" {
				t.Skip("Mach-O executable link requires the macOS SDK")
			}
			linker, err := exec.LookPath(target.linker)
			if err != nil {
				t.Skipf("%s is required: %v", target.linker, err)
			}
			prog := llssa.NewProgram(&llssa.Target{GOOS: target.goos, GOARCH: target.goarch})
			defer prog.Dispose()
			rt := types.NewPackage(llssa.PkgRuntime, "runtime")
			stringName := types.NewTypeName(token.NoPos, rt, "String", nil)
			types.NewNamed(stringName, types.NewStruct([]*types.Var{
				types.NewVar(token.NoPos, rt, "Data", types.NewPointer(types.Typ[types.Byte])),
				types.NewVar(token.NoPos, rt, "Len", types.Typ[types.Int]),
			}, nil), nil)
			rt.Scope().Insert(stringName)
			prog.SetRuntime(rt)
			pkg := prog.NewPackage("main", "main")
			entry := pkg.NewFunc("main", newSignature(nil, []types.Type{types.Typ[types.Int32]}), llssa.InC)
			entry.MakeBody(1).Return(prog.IntVal(0, prog.Int32()))
			conf := &Config{Goos: target.goos, Goarch: target.goarch, BuildMode: BuildModeExe,
				GlobalRewrites: map[string]Rewrites{"runtime": {"buildVersion": "go1.27-test"}}}
			framed := linkedBuildInfo(conf, &packages.Package{PkgPath: "example.org/app"}, nil)
			emitBuildInfo(&context{prog: prog, buildConf: conf}, pkg, framed, true)
			pkg.MaterializePreserveSyms()
			buffer, err := prog.TargetMachine().EmitToMemoryBuffer(pkg.Module(), llvm.ObjectFile)
			if err != nil {
				t.Fatal(err)
			}
			defer buffer.Dispose()
			dir := t.TempDir()
			object, image := filepath.Join(dir, "main.o"), filepath.Join(dir, "app")
			if err := os.WriteFile(object, buffer.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
			var args []string
			switch target.goos {
			case "linux":
				args = []string{"--gc-sections", "-e", "main", "-o", image, object}
			case "windows":
				args = []string{"/entry:main", "/subsystem:console", "/nodefaultlib", "/opt:ref", "/out:" + image, object}
			case "darwin":
				args = []string{"-Wl,-dead_strip", "-o", image, object}
			}
			if out, err := exec.Command(linker, args...).CombinedOutput(); err != nil {
				t.Fatalf("link: %v\n%s", err, out)
			}
			got, err := buildinfo.ReadFile(image)
			if err != nil {
				t.Fatal(err)
			}
			want, err := debug.ParseBuildInfo(framed[16 : len(framed)-16])
			if err != nil {
				t.Fatal(err)
			}
			want.GoVersion = "go1.27-test"
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("linked build information = %#v, want %#v", got, want)
			}
		})
	}
}
