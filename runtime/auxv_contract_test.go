package runtime

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

func TestRuntimeGetAuxvDefinitionSelection(t *testing.T) {
	const dir = "internal/lib/runtime"
	for _, goos := range []string{
		"aix", "android", "darwin", "dragonfly", "freebsd", "illumos", "ios",
		"js", "linux", "netbsd", "openbsd", "plan9", "solaris", "wasip1", "windows",
	} {
		for _, baremetal := range []bool{false, true} {
			name := goos
			if baremetal {
				name += "/baremetal"
			}
			t.Run(name, func(t *testing.T) {
				ctx := build.Default
				ctx.GOOS = goos
				ctx.GOARCH = "arm64"
				if goos == "js" || goos == "wasip1" {
					ctx.GOARCH = "wasm"
				}
				ctx.BuildTags = []string{"llgo"}
				if baremetal {
					ctx.BuildTags = append(ctx.BuildTags, "baremetal")
				}
				pkg, err := ctx.ImportDir(dir, 0)
				if err != nil {
					t.Fatal(err)
				}
				wantFile := "link_auxv_empty_llgo.go"
				wantReturn := "nil"
				if (goos == "linux" || goos == "android") && !baremetal {
					wantFile = "link_linux_llgo.go"
					wantReturn = "auxv"
				}
				selected := false
				for _, filename := range pkg.GoFiles {
					selected = selected || filename == wantFile
				}
				if !selected {
					t.Errorf("selected Go files exclude %s", wantFile)
				}
				var definitions []string
				for _, filename := range pkg.GoFiles {
					file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, filename), nil, parser.ParseComments)
					if err != nil {
						t.Fatal(err)
					}
					for _, declaration := range file.Decls {
						fn, ok := declaration.(*ast.FuncDecl)
						if !ok || fn.Name.Name != "getAuxv" {
							continue
						}
						definitions = append(definitions, filename)
						if fn.Recv != nil || fn.Type.Params.NumFields() != 0 || fn.Type.Results.NumFields() != 1 {
							t.Fatal("getAuxv must have signature func() []uintptr")
						}
						result, ok := fn.Type.Results.List[0].Type.(*ast.ArrayType)
						if !ok || result.Len != nil {
							t.Fatal("getAuxv result must be a slice")
						}
						element, ok := result.Elt.(*ast.Ident)
						if !ok || element.Name != "uintptr" {
							t.Fatal("getAuxv result must be []uintptr")
						}
						linked := false
						if fn.Doc != nil {
							for _, comment := range fn.Doc.List {
								linked = linked || comment.Text == "//go:linkname getAuxv runtime.getAuxv"
							}
						}
						if !linked {
							t.Fatal("getAuxv must export the runtime.getAuxv linkname")
						}
						if fn.Body == nil || len(fn.Body.List) != 1 {
							t.Fatal("getAuxv must return the platform auxiliary vector directly")
						}
						statement, ok := fn.Body.List[0].(*ast.ReturnStmt)
						if !ok || len(statement.Results) != 1 {
							t.Fatal("getAuxv must have one return expression")
						}
						value, ok := statement.Results[0].(*ast.Ident)
						if !ok || value.Name != wantReturn {
							t.Errorf("getAuxv must return %s on this target", wantReturn)
						}
					}
				}
				if len(definitions) != 1 || definitions[0] != wantFile {
					t.Errorf("getAuxv definitions=%v, want [%s]", definitions, wantFile)
				}
			})
		}
	}
}
