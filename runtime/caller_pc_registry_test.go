//go:build !llgo

package runtime

import (
	"bytes"
	"context"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestCallerPCRegistryProductionRace(t *testing.T) {
	fset := token.NewFileSet()
	selected := map[string]bool{
		"CallerFrame": true, "callerLocationStore": true, "callerPCMask": true,
		"callerSyntheticPCBase": true, "callerSyntheticPCSequence": true,
		"sameSyntheticFrame": true, "captureFrame": true, "capturePC": true,
		"captureFrameAt": true, "staticPC": true,
	}
	var declarations bytes.Buffer
	for _, path := range []string{"internal/runtime/caller.go", "internal/runtime/caller_pc.go"} {
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			if group, ok := decl.(*ast.GenDecl); ok && group.Tok == token.IMPORT {
				continue
			}
			keep := path == "internal/runtime/caller_pc.go"
			ast.Inspect(decl, func(node ast.Node) bool {
				name := ""
				switch node := node.(type) {
				case *ast.FuncDecl:
					name = node.Name.Name
				case *ast.TypeSpec:
					name = node.Name.Name
				case *ast.ValueSpec:
					if len(node.Names) == 1 {
						name = node.Names[0].Name
					}
				}
				if selected[name] {
					keep = true
					delete(selected, name)
				}
				return true
			})
			if keep {
				if err := format.Node(&declarations, fset, decl); err != nil {
					t.Fatal(err)
				}
				declarations.WriteByte('\n')
			}
		}
	}
	if len(selected) != 0 {
		t.Fatalf("missing production declarations: %v", selected)
	}
	fixture, err := os.ReadFile("testdata/caller_pc_registry/registry_test.go")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	source := "package runtime\nimport (\"sync/atomic\"; \"unsafe\")\n" + declarations.String()
	for name, data := range map[string][]byte{"registry.go": []byte(source), "registry_test.go": fixture} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-race", "-count=1", "registry.go", "registry_test.go")
	cmd.Dir, cmd.Env = dir, append(os.Environ(), "GOWORK=off")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("production caller PC registry: %v\n%s", err, output)
	}
}
