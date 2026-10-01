package build

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/xgo-dev/llgo/internal/packages"
)

func TestNativeASMSelectionDefaultDoesNotInvokeGo(t *testing.T) {
	overlay, err := nativeASMSelectionOverlay(commandEnv{dir: "/nonexistent-native-asm-selection"}, &Config{}, nil, "")
	if err != nil || len(overlay) != 0 {
		t.Fatalf("default selection changed: %v %v", overlay, err)
	}
}

func TestNativeASMPackageConfigurationCloneIsIndependent(t *testing.T) {
	conf := &Config{NativeASMPackages: []string{"example.com/asm/pkg"}}
	copy := conf.clone()
	copy.NativeASMPackages[0] = "example.com/other"
	if conf.NativeASMPackages[0] != "example.com/asm/pkg" {
		t.Fatal("native assembly package list aliased across invocations")
	}
}

func TestNativeASMSelectionActualDriverKeepsStdlibTags(t *testing.T) {
	root := t.TempDir()
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "probe")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	arch := runtime.GOARCH
	for name, data := range map[string]string{
		"go.mod":                       "module example.com/native-asm-driver\n\ngo 1.27.0\n",
		"probe/native_" + arch + ".go": "//go:build gc && !purego && !llgo\n\npackage probe\nimport \"math/big\"\nvar Big = big.NewInt(9)\nfunc Kernel([]byte) uint64\n",
		"probe/fallback.go":            "//go:build purego || llgo\n\npackage probe\nfunc Kernel([]byte) uint64 { return 9 }\n",
		"probe/kernel_" + arch + ".s":  "//go:build gc && !purego && !llgo\n\nTEXT ·Kernel(SB), $0-32\nRET\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	environ := withEnv(os.Environ(), "GOWORK=off", "GOFLAGS=", "GOOS="+runtime.GOOS, "GOARCH="+arch)
	commands := commandEnv{dir: root, environ: environ}
	flags := []string{"-tags=" + DefaultBuildTags()}
	source, err := resolveSourceGoConfig(commands, "", flags...)
	if err != nil {
		t.Fatal(err)
	}
	conf := &Config{Goos: runtime.GOOS, Goarch: arch, NativeASMPackages: []string{"example.com/native-asm-driver/probe"}, sourceGoVersion: source.GOVERSION, toolTags: source.toolTags}
	overlay, err := nativeASMSelectionOverlay(commands, conf, flags, source.GOROOT)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &packages.Config{
		Dir: root, Env: environ, Overlay: overlay, BuildFlags: flags,
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles | packages.NeedImports | packages.NeedDeps | packages.NeedModule,
	}
	loaded, err := packages.LoadEx(packages.NewDeduper(), nil, cfg, conf.NativeASMPackages...)
	if err != nil || len(loaded) != 1 {
		t.Fatalf("actual packages.Load: %v %v", loaded, err)
	}
	probe := loaded[0]
	if len(probe.Errors) != 0 || !slices.Contains(probe.OtherFiles, filepath.Join(dir, "kernel_"+arch+".s")) {
		t.Fatalf("actual driver did not select asm: %+v", probe)
	}
	if probe.Imports["math/big"] == nil || len(selectedSFiles(probe.Imports["math/big"].OtherFiles)) != 0 {
		t.Fatal("native imports missing or stdlib purego selection changed")
	}
	decls := 0
	for _, name := range probe.CompiledGoFiles {
		data, ok := overlay[name]
		if !ok {
			data, err = os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, data, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "Kernel" {
				decls++
				if fn.Body != nil {
					t.Fatal("actual driver selected Go fallback body")
				}
			}
		}
	}
	if decls != 1 {
		t.Fatalf("actual native declaration count %d", decls)
	}
	for _, name := range []string{"math/big", "github.com/xgo-dev/llgo/runtime"} {
		conf.NativeASMPackages = []string{name}
		if _, err := nativeASMSelectionOverlay(commands, conf, flags, source.GOROOT); err == nil {
			t.Fatalf("stdlib/runtime opt-in accepted: %s", name)
		}
	}
}
