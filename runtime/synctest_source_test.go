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

func TestSynctestCallbacksStayOnManagedEntries(t *testing.T) {
	const path = "internal/lib/runtime/synctest_llgo.go"
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(source), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//go:build") {
			t.Fatalf("%s unexpectedly limits platform coverage with %q", path, line)
		}
	}
	want := map[string]string{
		"synctest_run":      "internal/synctest.Run",
		"synctest_wait":     "internal/synctest.Wait",
		"synctest_inBubble": "internal/synctest.inBubble",
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
		t.Fatalf("missing managed synctest bridges: %v", want)
	}
}
