package asmselect

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestHeaderOverlaySelectsNativeDeclarationAssemblyAndImportsTogether(t *testing.T) {
	root := t.TempDir()
	// Match go list's canonical package directory (macOS /var is a symlink).
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "probe")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	sources := map[string]string{
		"go.mod":                "module example.com/asm-select-test\n\ngo 1.27.1\n",
		"probe/native_arm64.go": "//go:build gc && !purego && !llgo\n\npackage probe\nimport \"math/big\"\nvar Big = big.NewInt(9)\nfunc Kernel([]byte) uint64\n",
		"probe/fallback.go":     "//go:build purego || llgo\n\npackage probe\nfunc Kernel([]byte) uint64 { return 9 }\n",
		"probe/kernel_arm64.s":  "//go:build gc && !purego && !llgo\n\nTEXT ·Kernel(SB), $0-32\nRET\n",
	}
	for name, data := range sources {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	context := build.Default
	context.GOOS, context.GOARCH = "linux", "arm64"
	context.BuildTags = []string{}
	overlay, err := HeaderOverlay(dir, context)
	if err != nil {
		t.Fatal(err)
	}
	replace := map[string]string{}
	for name, data := range overlay {
		target := filepath.Join(root, "replacement-"+filepath.Base(name))
		if err := os.WriteFile(target, data, 0600); err != nil {
			t.Fatal(err)
		}
		replace[name] = target
	}
	data, err := json.Marshal(struct{ Replace map[string]string }{replace})
	if err != nil {
		t.Fatal(err)
	}
	mapfile := filepath.Join(root, "overlay.json")
	if err := os.WriteFile(mapfile, data, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "list", "-json", "-compiled", "-deps", "-overlay="+mapfile, "-tags=llgo,math_big_pure_go,purego", "./probe")
	cmd.Dir, cmd.Env = root, append(os.Environ(), "GOOS=linux", "GOARCH=arm64", "GOFLAGS=", "GOWORK=off")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go list: %v: %s", err, stderr.String())
	}
	decoder := json.NewDecoder(&stdout)
	seenProbe, seenBig := false, false
	for {
		var pkg struct {
			ImportPath                                string
			Dir                                       string
			GoFiles, CompiledGoFiles, SFiles, Imports []string
		}
		if err := decoder.Decode(&pkg); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatal(err)
		}
		switch pkg.ImportPath {
		case "math/big":
			seenBig = true
			if len(pkg.SFiles) != 0 {
				t.Fatalf("stdlib lost math_big_pure_go: %v", pkg.SFiles)
			}
		case "example.com/asm-select-test/probe":
			seenProbe = true
			if !slices.Contains(pkg.GoFiles, "native_arm64.go") || slices.Contains(pkg.GoFiles, "fallback.go") || !slices.Contains(pkg.SFiles, "kernel_arm64.s") || !slices.Contains(pkg.Imports, "math/big") {
				t.Fatalf("native Go declaration, .s and imports must be selected together: %+v", pkg)
			}
			for _, name := range pkg.CompiledGoFiles {
				if !filepath.IsAbs(name) {
					name = filepath.Join(pkg.Dir, name)
				}
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
					if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "Kernel" && fn.Body != nil {
						t.Fatal("native Kernel must have no Go body")
					}
				}
			}
		}
	}
	if !seenProbe || !seenBig {
		t.Fatalf("missing selected package/dependency: probe=%v big=%v", seenProbe, seenBig)
	}
	for name, expected := range sources {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || string(data) != expected {
			t.Fatalf("original source mutated: %s: %v", name, err)
		}
	}
	for _, data := range overlay {
		if strings.Contains(string(data), "return 0") {
			t.Fatal("overlay changed a function body")
		}
	}
}

func TestConstraintRemovalNeverChangesBodyOrInstructions(t *testing.T) {
	for _, source := range []string{
		"//go:build !purego\n// +build !purego\n\npackage p\nvar s = `\n//go:build body-data\n`\n",
		"/* copyright\n//go:build comment-data\n*/\n//go:build !purego\n\nTEXT ·f(SB), $0\n//go:build body-data\nRET\n",
	} {
		actual := withoutConstraintHeader([]byte(source))
		if !strings.Contains(string(actual), "//go:build body-data") || strings.Contains(string(actual), "//go:build !purego") {
			t.Fatalf("constraint removal changed body or retained tag: %q", actual)
		}
		if strings.Contains(source, "comment-data") && !strings.Contains(string(actual), "comment-data") {
			t.Fatal("constraint-like block comment was changed")
		}
	}
}

func TestNativeAssemblyPackageScopeRejectsStdlibRuntimeAndPatterns(t *testing.T) {
	for _, test := range []struct {
		name, module string
		standard     bool
	}{
		{"math/big", "", true},
		{"github.com/xgo-dev/llgo/runtime/internal/runtime", "github.com/xgo-dev/llgo/runtime", false},
		{"github.com/xgo-dev/llgo/internal/build", "github.com/xgo-dev/llgo", false},
	} {
		if err := ValidatePackage(test.name, test.module, test.standard); err == nil {
			t.Fatalf("unsafe native asm scope accepted: %s", test.name)
		}
	}
	for _, pattern := range []string{"all", "std,...", "example.com/...", "./local", "-tags=x", "example.com/*", "example.com/p,"} {
		if _, err := ParsePackages(pattern); err == nil {
			t.Fatalf("pattern accepted: %s", pattern)
		}
	}
	paths, err := ParsePackages("example.com/a,example.com/a,example.com/b")
	if err != nil || !slices.Equal(paths, []string{"example.com/a", "example.com/b"}) {
		t.Fatalf("exact paths: %v %v", paths, err)
	}
}
