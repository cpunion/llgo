//go:build !llgo

// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package runtime

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
)

// Compile certificates extracted from production declarations, rather than a
// separately maintained copy of the slot layout. Every negative case changes
// that layout and must be rejected by its recursive structural constraints.
func TestCoroKeyedMappedStoragePODCertificate(t *testing.T) {
	declarations := map[string]*ast.TypeSpec{}
	for _, name := range []string{"internal/runtime/coro_resume_materialize.go", "internal/coro/operation_v2.go"} {
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			general, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range general.Specs {
				if typ, ok := spec.(*ast.TypeSpec); ok {
					declarations[typ.Name.Name] = typ
				}
			}
		}
	}
	names := []string{"OperationID", "coroKeyedParkKindV2", "coroKeyedRegistrySlotV2", "coroKeyedPODUint32V2", "coroKeyedPODOperationV2", "coroKeyedPODSlotV2"}
	var text bytes.Buffer
	text.WriteString("package proof\nimport \"unsafe\"\n")
	for _, name := range names {
		typ := declarations[name]
		if typ == nil {
			t.Fatalf("missing production declaration %s", name)
		}
		text.WriteString("type ")
		if err := format.Node(&text, token.NewFileSet(), typ); err != nil {
			t.Fatal(err)
		}
		text.WriteByte('\n')
	}
	base := strings.ReplaceAll(text.String(), "coro.OperationID", "OperationID") + `
type _ coroKeyedPODUint32V2[coroKeyedParkKindV2]
type _ coroKeyedPODOperationV2[OperationID]
type _ coroKeyedPODSlotV2[coroKeyedRegistrySlotV2]
`
	// The gc compiler uses 64-bit uintptr for js/wasm. LLGo wasm32
	// retains its distinct target layout and is checked by native target IR.
	for _, target := range []struct {
		os, arch string
		bytes    int
	}{{"darwin", "arm64", 40}, {"linux", "amd64", 40}, {"linux", "386", 28}, {"js", "wasm", 40}} {
		for _, kind := range []string{"valid", "slot-pointer", "slot-zero-array-pointer", "key-pointer", "operation-pointer", "operation-interface", "kind-pointer"} {
			t.Run(target.arch+"/"+kind, func(t *testing.T) {
				file, err := parser.ParseFile(token.NewFileSet(), "proof.go", base, 0)
				if err != nil {
					t.Fatal(err)
				}
				ast.Inspect(file, func(node ast.Node) bool {
					typ, ok := node.(*ast.TypeSpec)
					if !ok {
						return true
					}
					if kind == "kind-pointer" && typ.Name.Name == "coroKeyedParkKindV2" {
						typ.Type = &ast.StarExpr{X: ast.NewIdent("byte")}
						return true
					}
					structure, ok := typ.Type.(*ast.StructType)
					if !ok {
						return true
					}
					if typ.Name.Name == "coroKeyedRegistrySlotV2" {
						if kind == "slot-pointer" || kind == "slot-zero-array-pointer" {
							var field ast.Expr = &ast.StarExpr{X: ast.NewIdent("byte")}
							if kind == "slot-zero-array-pointer" {
								field = &ast.ArrayType{Len: &ast.BasicLit{Kind: token.INT, Value: "0"}, Elt: field}
							}
							structure.Fields.List = append(structure.Fields.List, &ast.Field{Names: []*ast.Ident{ast.NewIdent("managed")}, Type: field})
						}
						if kind == "key-pointer" {
							for _, field := range structure.Fields.List {
								if len(field.Names) == 1 && field.Names[0].Name == "key" {
									field.Type = &ast.StarExpr{X: ast.NewIdent("byte")}
								}
							}
						}
					}
					if typ.Name.Name == "OperationID" && (kind == "operation-pointer" || kind == "operation-interface") {
						var field ast.Expr = &ast.StarExpr{X: ast.NewIdent("byte")}
						if kind == "operation-interface" {
							field = &ast.InterfaceType{Methods: &ast.FieldList{}}
						}
						structure.Fields.List = append(structure.Fields.List, &ast.Field{Names: []*ast.Ident{ast.NewIdent("managed")}, Type: field})
					}
					return true
				})
				var output bytes.Buffer
				if err := format.Node(&output, token.NewFileSet(), file); err != nil {
					t.Fatal(err)
				}
				output.WriteString("\nvar _ [8-unsafe.Sizeof(OperationID{})]byte\n")
				if kind == "valid" {
					output.WriteString("var _ [unsafe.Sizeof(OperationID{})-8]byte\n")
					output.WriteString("var _ [")
					output.WriteString(map[int]string{40: "40", 28: "28"}[target.bytes])
					output.WriteString("-unsafe.Sizeof(coroKeyedRegistrySlotV2{})]byte\nvar _ [unsafe.Sizeof(coroKeyedRegistrySlotV2{})-")
					output.WriteString(map[int]string{40: "40", 28: "28"}[target.bytes])
					output.WriteString("]byte\n")
				}
				dir := t.TempDir()
				source := filepath.Join(dir, "proof.go")
				if err := os.WriteFile(source, output.Bytes(), 0600); err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command(filepath.Join(goruntime.GOROOT(), "bin", "go"), "tool", "compile", "-lang=go1.20", "-o", filepath.Join(dir, "proof.o"), source)
				for _, entry := range os.Environ() {
					if !strings.HasPrefix(entry, "GOOS=") && !strings.HasPrefix(entry, "GOARCH=") {
						cmd.Env = append(cmd.Env, entry)
					}
				}
				cmd.Env = append(cmd.Env, "GOOS="+target.os, "GOARCH="+target.arch)
				out, err := cmd.CombinedOutput()
				if kind == "valid" {
					if err != nil {
						t.Fatalf("production POD representation failed: %v\n%s", err, out)
					}
				} else if err == nil || !bytes.Contains(out, []byte("does not satisfy")) {
					t.Fatalf("managed layout must fail the structural certificate: err=%v\n%s", err, out)
				}
			})
		}
	}
}
