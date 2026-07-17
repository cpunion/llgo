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
	"slices"
	"strings"
	"testing"
)

func TestCoroTimerOwnerOrAbortSourceABI(t *testing.T) {
	const source = "internal/runtime/coro_timer_owner_llgo.go"
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), source, data, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		params     []string
		delegates  string
		exportLine string
	}{
		{
			name: "__llgo_coro_timer_prepare_after_or_abort_v1",
			params: []string{
				"unsafe.Pointer", "int64", "*uint32", "*uint32", "*uint32",
			},
			delegates:  "__llgo_coro_timer_prepare_after_v1",
			exportLine: "//export __llgo_coro_timer_prepare_after_or_abort_v1",
		},
		{
			name: "__llgo_coro_timer_retire_completed_or_abort_v1",
			params: []string{
				"unsafe.Pointer", "uint32", "uint32", "uint32",
			},
			delegates:  "__llgo_coro_timer_retire_completed_v1",
			exportLine: "//export __llgo_coro_timer_retire_completed_or_abort_v1",
		},
	}

	functions := make(map[string]*ast.FuncDecl)
	for _, decl := range file.Decls {
		if function, ok := decl.(*ast.FuncDecl); ok {
			functions[function.Name.Name] = function
		}
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			function := functions[test.name]
			if function == nil {
				t.Fatalf("missing compiler-certified timer owner %q", test.name)
			}
			if function.Type.Results != nil && len(function.Type.Results.List) != 0 {
				t.Fatalf("%s returns a result; want fail-stop void ABI", test.name)
			}
			if got := coroTimerOwnerParameterTypes(t, function); !slices.Equal(got, test.params) {
				t.Fatalf("%s parameter types = %v, want %v", test.name, got, test.params)
			}
			doc := ""
			if function.Doc != nil {
				doc = function.Doc.Text()
				for _, comment := range function.Doc.List {
					doc += "\n" + comment.Text
				}
			}
			if !strings.Contains(doc, test.exportLine) {
				t.Fatalf("%s lacks exact C export %q", test.name, test.exportLine)
			}
			body := coroTimerOwnerNodeText(t, function.Body)
			if !strings.Contains(body, "!"+test.delegates+"(") || !strings.Contains(body, "coroRuntimeAbort(") {
				t.Fatalf("%s body is not a bool-owner delegation with terminal failure:\n%s", test.name, body)
			}
			if !coroTimerOwnerFailureIsSyntacticallyTerminal(function) {
				t.Fatalf("%s failure branch can return after coroRuntimeAbort:\n%s", test.name, body)
			}
		})
	}
}

func coroTimerOwnerFailureIsSyntacticallyTerminal(function *ast.FuncDecl) bool {
	if function == nil || function.Body == nil || len(function.Body.List) != 1 {
		return false
	}
	conditional, ok := function.Body.List[0].(*ast.IfStmt)
	if !ok || conditional.Else != nil || conditional.Body == nil || len(conditional.Body.List) < 2 {
		return false
	}
	loop, ok := conditional.Body.List[len(conditional.Body.List)-1].(*ast.ForStmt)
	return ok && loop.Init == nil && loop.Cond == nil && loop.Post == nil && loop.Body != nil && len(loop.Body.List) == 0
}

func coroTimerOwnerParameterTypes(t *testing.T, function *ast.FuncDecl) []string {
	t.Helper()
	var result []string
	if function == nil || function.Type.Params == nil {
		return result
	}
	for _, field := range function.Type.Params.List {
		typeText := coroTimerOwnerNodeText(t, field.Type)
		count := len(field.Names)
		if count == 0 {
			count = 1
		}
		for index := 0; index < count; index++ {
			result = append(result, typeText)
		}
	}
	return result
}

func coroTimerOwnerNodeText(t *testing.T, node any) string {
	t.Helper()
	var buffer bytes.Buffer
	if err := format.Node(&buffer, token.NewFileSet(), node); err != nil {
		t.Fatal(err)
	}
	return buffer.String()
}
