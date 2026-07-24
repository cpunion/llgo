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

func TestCoroTimerOwnerV2SourceABI(t *testing.T) {
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
		name      string
		params    []string
		result    string
		delegates string
		failStop  bool
	}{
		{
			name:      "__llgo_coro_timer_park_v2",
			params:    []string{"unsafe.Pointer", "unsafe.Pointer", "unsafe.Pointer", "unsafe.Pointer", "int64"},
			delegates: "coro.PrepareCurrentExecutorTimerPark",
			failStop:  true,
		},
		{
			name: "__llgo_coro_timer_park_controlled_v2",
			params: []string{
				"unsafe.Pointer", "unsafe.Pointer", "unsafe.Pointer", "unsafe.Pointer",
				"unsafe.Pointer", "*uint32", "*uint32", "uint32", "int64",
			},
			delegates: "coro.PrepareCurrentExecutorControlledTimerPark",
			failStop:  true,
		},
		{
			name:      "__llgo_coro_timer_resume_v2",
			params:    []string{"unsafe.Pointer", "unsafe.Pointer"},
			result:    "uint32",
			delegates: "coro.FinishCurrentExecutorTimerPark",
			failStop:  true,
		},
		{
			name:      "__llgo_coro_timer_request_controlled_v2",
			params:    []string{"uint32"},
			result:    "uint32",
			delegates: "coroTargetRequestControlledTimerV2",
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
			if got := coroTimerOwnerParameterTypes(t, function); !slices.Equal(got, test.params) {
				t.Fatalf("%s parameter types = %v, want %v", test.name, got, test.params)
			}
			gotResult := ""
			if function.Type.Results != nil && len(function.Type.Results.List) != 0 {
				if len(function.Type.Results.List) != 1 {
					t.Fatalf("%s result count = %d", test.name, len(function.Type.Results.List))
				}
				gotResult = coroTimerOwnerNodeText(t, function.Type.Results.List[0].Type)
			}
			if gotResult != test.result {
				t.Fatalf("%s result = %q, want %q", test.name, gotResult, test.result)
			}
			if function.Doc == nil || !strings.Contains(string(data), "//export "+test.name) {
				t.Fatalf("%s lacks exact C export", test.name)
			}
			body := coroTimerOwnerNodeText(t, function.Body)
			if !strings.Contains(body, test.delegates+"(") {
				t.Fatalf("%s does not delegate to %s:\n%s", test.name, test.delegates, body)
			}
			if test.failStop && !strings.Contains(body, "coroTimerAbortV2(") {
				t.Fatalf("%s does not fail-stop malformed ownership:\n%s", test.name, body)
			}
		})
	}
	for _, obsolete := range []string{
		"__llgo_coro_timer_prepare_after_v1",
		"__llgo_coro_timer_retire_completed_v1",
		"__llgo_coro_timer_prepare_after_or_abort_v1",
		"__llgo_coro_timer_retire_completed_or_abort_v1",
	} {
		if strings.Contains(string(data), obsolete) {
			t.Errorf("%s retains obsolete timer ABI %q", source, obsolete)
		}
	}
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
