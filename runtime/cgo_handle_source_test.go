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
	"strings"
	"testing"
)

func TestCgoHandleBridgeUsesManagedAtomicGate(t *testing.T) {
	const path = "internal/lib/runtime/cgo_handle_llgo.go"
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, forbidden := range []string{
		"clite/pthread",
		"psync.",
		".once.Do(",
		"initCgoHandleState",
	} {
		if strings.Contains(text, forbidden) {
			t.Errorf("cgo handle bridge retains blocking/callback initialization %q", forbidden)
		}
	}
	for _, required := range []string{
		"catomic.CompareAndExchange(&gate.state, uint32(0), uint32(1))",
		"catomic.CompareAndExchange(&gate.state, uint32(1), uint32(0))",
		"coroSchedulerYield()",
		"if cgoHandleState.handles == nil",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("cgo handle bridge lacks scheduler-safe gate operation %q", required)
		}
	}

	file, err := parser.ParseFile(token.NewFileSet(), path, source, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"cgoNewHandle":    "runtime/cgo.NewHandle",
		"cgoHandleValue":  "runtime/cgo.Handle.Value",
		"cgoHandleDelete": "runtime/cgo.Handle.Delete",
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok {
			continue
		}
		target, tracked := want[function.Name.Name]
		if !tracked {
			continue
		}
		delete(want, function.Name.Name)
		comments := make(map[string]bool)
		if function.Doc != nil {
			for _, comment := range function.Doc.List {
				comments[strings.TrimSpace(comment.Text)] = true
			}
		}
		linkname := "//go:linkname " + function.Name.Name + " " + target
		if !comments["//llgo:managedlink"] || !comments[linkname] || function.Body == nil {
			t.Errorf("%s managed/link/body = %t/%t/%t, want true/true/true",
				function.Name.Name, comments["//llgo:managedlink"], comments[linkname], function.Body != nil)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing managed cgo handle bridges: %v", want)
	}
}
