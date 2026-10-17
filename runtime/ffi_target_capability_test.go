package runtime

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"testing"
)

func TestFFIDynamicCallsMatchTargetCapability(t *testing.T) {
	for _, profile := range []struct {
		name, goos, goarch string
		tags               []string
		supported          bool
	}{
		{"native", "linux", "amd64", nil, true},
		{"wasi", "wasip1", "wasm", nil, false},
		{"js", "js", "wasm", nil, false},
		{"wasm-unknown", "linux", "arm", []string{"tinygo.wasm"}, false},
		{"baremetal", "linux", "arm", []string{"baremetal"}, false},
	} {
		t.Run(profile.name, func(t *testing.T) {
			ctx := build.Default
			ctx.GOOS, ctx.GOARCH, ctx.BuildTags = profile.goos, profile.goarch, profile.tags
			selected := 0
			for _, name := range []string{"ffi_link.go", "ffi_link_freestanding.go"} {
				ok, err := ctx.MatchFile("internal/clite/ffi", name)
				if err != nil {
					t.Fatal(err)
				}
				if !ok {
					continue
				}
				selected++
				file, err := parser.ParseFile(token.NewFileSet(), "internal/clite/ffi/"+name, nil, 0)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, decl := range file.Decls {
					gen, ok := decl.(*ast.GenDecl)
					if !ok || gen.Tok != token.CONST {
						continue
					}
					for _, spec := range gen.Specs {
						value := spec.(*ast.ValueSpec)
						if value.Names[0].Name != "DynamicCallsSupported" {
							continue
						}
						found = true
						literal, ok := value.Values[0].(*ast.Ident)
						if !ok || (literal.Name != "true" && literal.Name != "false") ||
							(literal.Name == "true") != profile.supported {
							t.Errorf("%s has incorrect compile-time dynamic call capability", name)
						}
					}
				}
				if !found {
					t.Errorf("%s has no target capability", name)
				}
			}
			if selected != 1 {
				t.Errorf("selected %d ffi target definitions, want one", selected)
			}
		})
	}

	file, err := parser.ParseFile(token.NewFileSet(), "internal/ffi/ffi.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "callLLGoManaged" {
			continue
		}
		guard, ok := fn.Body.List[0].(*ast.IfStmt)
		if !ok {
			t.Fatal("managed FFI entry must check target capability before any intrinsic")
		}
		negate, ok := guard.Cond.(*ast.UnaryExpr)
		if !ok || negate.Op != token.NOT {
			t.Fatal("managed FFI entry must reject unsupported targets")
		}
		capability, ok := negate.X.(*ast.SelectorExpr)
		if !ok || capability.Sel.Name != "DynamicCallsSupported" {
			t.Fatal("managed FFI entry must use the selected target constant")
		}
		statement, ok := guard.Body.List[0].(*ast.ExprStmt)
		if !ok {
			t.Fatal("unsupported dynamic calls must fail, not return fabricated results")
		}
		call, ok := statement.X.(*ast.CallExpr)
		if !ok {
			t.Fatal("unsupported dynamic calls must panic")
		}
		callee, ok := call.Fun.(*ast.Ident)
		if !ok || callee.Name != "panic" {
			t.Fatal("unsupported dynamic calls must panic")
		}
		return
	}
	t.Fatal("managed FFI entry not found")
}
