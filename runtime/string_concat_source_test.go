//go:build !llgo

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package runtime

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func TestStringCatChecksLengthBeforeAllocation(t *testing.T) {
	const path = "internal/runtime/z_string.go"
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, source, 0)
	if err != nil {
		t.Fatal(err)
	}
	var function *ast.FuncDecl
	for _, declaration := range file.Decls {
		candidate, ok := declaration.(*ast.FuncDecl)
		if ok && candidate.Name.Name == "StringCat" {
			function = candidate
			break
		}
	}
	if function == nil || function.Body == nil {
		t.Fatal("runtime source has no StringCat body")
	}

	guardIndex, allocationIndex, copyIndex := -1, -1, -1
	for index, statement := range function.Body.List {
		if guard, ok := statement.(*ast.IfStmt); ok {
			var condition bytes.Buffer
			if err := format.Node(&condition, fset, guard.Cond); err != nil {
				t.Fatal(err)
			}
			compact := strings.NewReplacer(" ", "", "\n", "", "\t", "").Replace(condition.String())
			if compact == "a.len<0||b.len<0||n<a.len||uintptr(n)>maxAlloc" {
				guardIndex = index
				var checkedPanic bool
				ast.Inspect(guard.Body, func(node ast.Node) bool {
					call, ok := node.(*ast.CallExpr)
					if !ok {
						return true
					}
					name, ok := call.Fun.(*ast.Ident)
					if !ok || name.Name != "panic" || len(call.Args) != 1 {
						return true
					}
					payload, ok := call.Args[0].(*ast.CallExpr)
					if !ok || len(payload.Args) != 1 {
						return true
					}
					constructor, ok := payload.Fun.(*ast.Ident)
					message, messageOK := payload.Args[0].(*ast.BasicLit)
					checkedPanic = ok && constructor.Name == "errorString" && messageOK &&
						message.Kind == token.STRING && message.Value == `"string concatenation too long"`
					return true
				})
				if !checkedPanic {
					t.Fatal("StringCat length guard lacks the checked panic payload")
				}
			}
		}
		ast.Inspect(statement, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch callee := call.Fun.(type) {
			case *ast.Ident:
				if callee.Name == "AllocU" && allocationIndex < 0 {
					allocationIndex = index
				}
			case *ast.SelectorExpr:
				if owner, ok := callee.X.(*ast.Ident); ok && owner.Name == "c" && callee.Sel.Name == "Memcpy" && copyIndex < 0 {
					copyIndex = index
				}
			}
			return true
		})
	}
	if guardIndex < 0 {
		t.Fatal("StringCat lacks the exact negative/overflow/maxAlloc guard")
	}
	if allocationIndex < 0 || copyIndex < 0 || !(guardIndex < allocationIndex && allocationIndex < copyIndex) {
		t.Fatalf("StringCat statement order guard=%d allocation=%d copy=%d; want guard < allocation < copy", guardIndex, allocationIndex, copyIndex)
	}
}
