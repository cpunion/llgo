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

package build

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestCoroProductionDirectiveInventory is a monotonic architecture gate.
// Structural facts must stay in compiler analysis; only irreducible foreign
// behavior may remain in production source. Lowering a count is allowed only
// together with this exact snapshot. Raising one, or reintroducing a removed
// class, is an architecture regression.
func TestCoroProductionDirectiveInventory(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	got := make(map[string]int)
	err := filepath.WalkDir(repoRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "testdata", "_testdata", "_testgo":
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(source), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[0] == "//llgo:coro" {
				got[fields[1]]++
			}
		}
		if err := validateCoroProductionDirectivePlacement(path, source); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]int{
		"contract": 7,
		"noblock":  60,
		"sync":     40,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf(
			"production //llgo:coro inventory = %v, want exact monotonic snapshot %v; "+
				"schedulerwait, workeraddr, workerresult, worker, and caller-coloring directives must remain absent",
			got, want,
		)
	}
}

func validateCoroProductionDirectivePlacement(path string, source []byte) error {
	files := token.NewFileSet()
	file, err := parser.ParseFile(files, path, source, parser.ParseComments)
	if err != nil {
		return err
	}
	owners := make(map[*ast.CommentGroup]*ast.FuncDecl)
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Doc != nil {
			owners[function.Doc] = function
		}
	}
	for _, group := range file.Comments {
		for _, comment := range group.List {
			fields := strings.Fields(comment.Text)
			if len(fields) < 2 || fields[0] != "//llgo:coro" {
				continue
			}
			function := owners[group]
			if function == nil {
				return fmt.Errorf(
					"%s:%d: production coroutine directive is not attached to an exact function declaration",
					path, files.Position(comment.Pos()).Line,
				)
			}
			if function.Body == nil || coroDirectiveDocLinksExternal(group) {
				continue
			}
			if fields[1] == "contract" && coroDirectiveHasField(fields[2:], "scope=wrapper") {
				continue
			}
			return fmt.Errorf(
				"%s:%d: bodyful Go function %s carries %q caller-coloring metadata; "+
					"derive its effect from SSA or use an exact bottom-level scope=wrapper contract",
				path, files.Position(comment.Pos()).Line, function.Name.Name, fields[1],
			)
		}
	}
	return nil
}

func coroDirectiveDocLinksExternal(group *ast.CommentGroup) bool {
	if group == nil {
		return false
	}
	for _, comment := range group.List {
		fields := strings.Fields(comment.Text)
		if len(fields) < 2 {
			continue
		}
		link := fields[0] == "//go:linkname" ||
			(len(fields) >= 3 && fields[0] == "//" && fields[1] == "llgo:link")
		if link && strings.HasPrefix(fields[len(fields)-1], "C.") {
			return true
		}
	}
	return false
}

func coroDirectiveHasField(fields []string, want string) bool {
	for _, field := range fields {
		if field == want {
			return true
		}
	}
	return false
}
