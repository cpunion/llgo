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
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"testing"
)

func TestSliceCopyKeepsOverlapSafeRuntimeLowering(t *testing.T) {
	path := "internal/runtime/z_slice.go"
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	ast.Inspect(file, func(node ast.Node) bool {
		function, ok := node.(*ast.FuncDecl)
		if !ok || function.Name.Name != "SliceCopy" {
			return true
		}
		found = true
		var memmove, memcpy int
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			owner, ok := selector.X.(*ast.Ident)
			if !ok || owner.Name != "c" {
				return true
			}
			switch selector.Sel.Name {
			case "Memmove":
				memmove++
			case "Memcpy":
				memcpy++
			}
			return true
		})
		if memmove != 1 || memcpy != 0 {
			t.Errorf("SliceCopy memory primitive = Memmove:%d Memcpy:%d, want 1/0", memmove, memcpy)
		}
		return false
	})
	if !found {
		t.Fatal("runtime source has no SliceCopy helper")
	}
}

func TestSliceCopyOverlapRuleMatrix(t *testing.T) {
	for _, test := range []struct {
		name        string
		destination int
		source      int
		count       int
		want        [6]byte
	}{
		{
			name: "destination follows source", destination: 1, source: 0, count: 4,
			want: [6]byte{0, 0, 1, 2, 3, 5},
		},
		{
			name: "source follows destination", destination: 0, source: 1, count: 4,
			want: [6]byte{1, 2, 3, 4, 4, 5},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := [6]byte{0, 1, 2, 3, 4, 5}
			if copied := copy(data[test.destination:test.destination+test.count], data[test.source:test.source+test.count]); copied != test.count {
				t.Fatalf("copied = %d, want %d", copied, test.count)
			}
			if data != test.want {
				t.Fatalf("overlap result = %v, want %v", data, test.want)
			}
		})
	}
}
