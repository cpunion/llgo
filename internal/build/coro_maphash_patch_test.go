package build

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/env"
	"github.com/xgo-dev/llgo/internal/packages"
	llruntime "github.com/xgo-dev/llgo/runtime"
)

func TestCoroMaphashSourcePatchUsesStaticTypeHash(t *testing.T) {
	const pkgPath = "hash/maphash"
	if !llruntime.HasSourcePatchPkg(pkgPath) || llruntime.HasAltPkg(pkgPath) {
		t.Fatal("hash/maphash must keep its standard source with one ABI-boundary patch")
	}
	for _, target := range []struct{ os, arch string }{
		{"darwin", "arm64"}, {"linux", "amd64"}, {"wasip1", "wasm"},
	} {
		t.Run(target.os+"-"+target.arch, func(t *testing.T) {
			_, overlay, files, err := applySourcePatchForPkg(nil, nil, env.LLGoRuntimeDir(), runtime.GOROOT(), pkgPath, sourcePatchBuildContext{
				goos: target.os, goarch: target.arch, goversion: runtime.Version(),
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(files) != 1 {
				t.Fatalf("selected %d patches, want one exact comparable hash boundary", len(files))
			}
			dir := filepath.Join(runtime.GOROOT(), "src", filepath.FromSlash(pkgPath))
			original, err := parser.ParseFile(token.NewFileSet(), "maphash.go", overlay[filepath.Join(dir, "maphash.go")], 0)
			if err != nil {
				t.Fatal(err)
			}
			public := false
			for _, declaration := range original.Decls {
				if function, ok := declaration.(*ast.FuncDecl); ok {
					if function.Name.Name == "comparableHash" {
						t.Fatal("original private comparableHash helper was not removed")
					}
					public = public || function.Name.Name == "Comparable"
				}
			}
			patch := string(overlay[filepath.Join(dir, "z_llgo_patch_comparable_llgo.go")])
			if !public {
				t.Fatal("patch removed the public standard Comparable implementation")
			}
			for _, required := range []string{"abi.TypeFor[T]()", "typehashImpl", "maps.Use64BitHash"} {
				if !strings.Contains(patch, required) {
					t.Fatalf("patch lacks %q", required)
				}
			}
			for _, forbidden := range []string{".Hasher", "MapType", "//llgo:coro"} {
				if strings.Contains(patch, forbidden) {
					t.Fatalf("patch retains unnecessary private ABI or effect contract %q", forbidden)
				}
			}
			pkgs, err := packages.LoadEx(nil, func(types.Sizes, string, string) types.Sizes {
				return types.SizesFor("gc", target.arch)
			}, &packages.Config{
				Mode: loadSyntax | packages.NeedDeps | packages.NeedModule | packages.NeedExportFile,
				Env:  append(os.Environ(), "GOOS="+target.os, "GOARCH="+target.arch, "CGO_ENABLED=0"),
				Fset: token.NewFileSet(), Overlay: overlay,
			}, pkgPath)
			if err != nil {
				t.Fatal(err)
			}
			if len(pkgs) != 1 || pkgs[0].IllTyped {
				for _, pkg := range pkgs {
					logPackageErrors(t, pkg, make(map[string]bool))
				}
				t.Fatal("patched maphash did not type-check")
			}
		})
	}
}
