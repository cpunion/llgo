//go:build !llgo

package build

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"testing"
)

func TestDarwinDynamicImportLinkArgs(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "imports.go", `package p
//go:cgo_ldflag "-pthread"
//go:cgo_import_dynamic local CFRelease "/System/Library/Frameworks/CoreFoundation.framework/Versions/A/CoreFoundation"
//go:cgo_import_dynamic second CFRetain "/System/Library/Frameworks/CoreFoundation.framework/Versions/A/CoreFoundation"
//go:cgo_import_dynamic write write "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic _ _ "/custom/libfoo.dylib"
//go:cgo_import_dynamic typo mach_vm_region "/usr/lib/libSystem.B.dylib""
//go:cgo_import_dynamic absent absent
`, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	files := []*ast.File{f}
	want := []string{"-pthread", "-framework", "CoreFoundation", "-lSystem.B", "/custom/libfoo.dylib"}
	if got, err := goCgoLinkArgs(files, "darwin"); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("link args = %q, %v; want %q", got, err, want)
	}
	for _, goos := range []string{"linux", "windows"} {
		if got, err := goCgoLinkArgs(files, goos); err != nil || !reflect.DeepEqual(got, []string{"-pthread"}) {
			t.Fatalf("%s args = %q, %v", goos, got, err)
		}
	}
}

func TestDirectiveQuotedFields(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{`a b "/path with spaces/lib.dylib"`, []string{"a", "b", "/path with spaces/lib.dylib"}},
		{`local"library"`, []string{"local", "library"}},
		{`a b "lib.dylib""`, []string{"a", "b", "lib.dylib"}},
		{`a "unterminated`, []string{"a"}},
	} {
		if got := splitDirectiveArgs(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("fields(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSingleTokenDynamicImport(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "imports.go", "package p\n//go:cgo_import_dynamic strlen\n", parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	_, got := collectGoCgoPragmas([]*ast.File{f})
	want := []cgoImportDynamicDecl{{local: "strlen", alias: "strlen"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("imports=%v, want %v", got, want)
	}
}

func TestDarwinDynamicImportLibraryPath(t *testing.T) {
	for _, lib := range []string{"-Wl,-force_load,other.a", "@linker.rsp", "relative/libfoo.dylib", "./@literal.a", "/custom/path with spaces/libfoo.tbd"} {
		t.Run(lib, func(t *testing.T) {
			src := fmt.Sprintf("package p\n//go:cgo_import_dynamic local remote %q\n", lib)
			f, err := parser.ParseFile(token.NewFileSet(), "imports.go", src, parser.ParseComments)
			if err != nil {
				t.Fatal(err)
			}
			got, err := goCgoLinkArgs([]*ast.File{f}, "darwin")
			if lib[0] == '-' || lib[0] == '@' {
				if err == nil || !strings.Contains(err.Error(), "expected a library path") || got != nil {
					t.Fatalf("accepted linker option as library: %q, %v", got, err)
				}
			} else if err != nil || !reflect.DeepEqual(got, []string{lib}) {
				t.Fatalf("library args = %q, %v; want %q", got, err, lib)
			}
		})
	}
}
