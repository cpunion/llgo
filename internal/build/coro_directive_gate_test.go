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
	"crypto/sha256"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestCoroProductionDirectiveInventory is a monotonic architecture gate.
// Structural facts must stay in compiler analysis; only irreducible foreign
// behavior may remain in production source. Any count change requires this
// exact snapshot and owner-manifest review; an unreviewed increase or
// reintroduction of a removed class is an architecture regression.
func TestCoroProductionDirectiveInventory(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	got := make(map[string]int)
	var manifest []string
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
		entries, err := validateCoroProductionDirectivePlacement(
			repoRoot, path, source,
		)
		if err != nil {
			return err
		}
		manifest = append(manifest, entries...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]int{
		// The obsolete baremetal syscall.Open wrapper contract is removed.
		// The native doorbell still needs its O_NONBLOCK ownership contract,
		// which cannot be inferred from write's C signature.
		// Darwin clock_gettime_nsec_np is a by-value non-waiting clock read,
		// including managed time.Now callers. Raw-host-only use inference is
		// insufficient when the same declaration also has managed callers;
		// its exact foreign contract must not imply interrupt safety.
		"contract": 8,
		// The residual noblock leaves include the bounded native frame-cache
		// take/put operations, current-G TLS load/store, hosted TLS primitives,
		// and the Windows UCRT/fault-recursion accessors. Their raw C signatures
		// cannot express the fixed-size ownership transfer, independent
		// pthread/FLS lifetime root, or non-waiting access contract.
		// WASI proc_exit is an exact non-returning host leaf; it neither waits
		// for an I/O completion nor transfers a borrowed frame to a worker.
		"noblock": 40,
		// The three additional synchronous leaves cover same-thread BDWGC
		// unregister plus Windows fault-handler registration and process entropy.
		// Moving any of them to a worker would change thread ownership or startup
		// ordering; their C signatures cannot prove those semantics.
		// Five additional collector-owned leaves register/reveal/base weak
		// references and attach/detach collection notification. They may take
		// GC's allocation lock, but cannot suspend a managed frame or migrate
		// its collector-critical operation to a worker. Detach joins callbacks
		// before doorbell reuse. This is not a lock-free/noblock claim.
		// The primordial-thread exit wrapper combines native thread identity
		// with GC unregister. It must run on the retiring M; dispatching that
		// collector operation to a worker would unregister the wrong thread.
		"sync": 38,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf(
			"production //llgo:coro inventory = %v, want exact monotonic snapshot %v; "+
				"schedulerwait, workeraddr, workerresult, worker, and caller-coloring directives must remain absent",
			got, want,
		)
	}
	sort.Strings(manifest)
	// The Go 1.26 source-patch migration moved the Darwin mmap declaration, and
	// the hosted thread/sync unification replaced legacy pthread owners. Windows
	// FLS effects are attached to the raw declarations rather than their Go
	// wrappers. The weak/notification and WASI exit additions are exact foreign
	// boundaries, not Go caller coloring. The Darwin clock contract covers one
	// shared managed/raw declaration. Initial-thread exit adds only the exact
	// foreign GC wrapper, rather than a contract on its Go caller. This freezes
	// the reviewed 8/40/38 set.
	const wantManifestSHA256 = "bcb386feacf9d8efcb812fa6cc1174c34d25fa24c82dc0bf49b23d3c5d8785db"
	manifestSHA256 := fmt.Sprintf(
		"%x", sha256.Sum256([]byte(strings.Join(manifest, "\n"))),
	)
	if manifestSHA256 != wantManifestSHA256 {
		t.Fatalf(
			"production //llgo:coro owner/contract manifest SHA-256 = %s, want %s; "+
				"review every residual bottom contract before updating this gate:\n%s",
			manifestSHA256, wantManifestSHA256, strings.Join(manifest, "\n"),
		)
	}
}

func validateCoroProductionDirectivePlacement(
	repoRoot, path string,
	source []byte,
) ([]string, error) {
	files := token.NewFileSet()
	file, err := parser.ParseFile(files, path, source, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	relative, err := filepath.Rel(repoRoot, path)
	if err != nil {
		return nil, err
	}
	relative = filepath.ToSlash(relative)
	owners := make(map[*ast.CommentGroup]*ast.FuncDecl)
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Doc != nil {
			owners[function.Doc] = function
		}
	}
	var manifest []string
	for _, group := range file.Comments {
		for _, comment := range group.List {
			fields := strings.Fields(comment.Text)
			if len(fields) < 2 || fields[0] != "//llgo:coro" {
				continue
			}
			function := owners[group]
			if function == nil {
				return nil, fmt.Errorf(
					"%s:%d: production coroutine directive is not attached to an exact function declaration",
					path, files.Position(comment.Pos()).Line,
				)
			}
			manifest = append(
				manifest,
				relative+"\t"+function.Name.Name+"\t"+strings.Join(fields, " "),
			)
			if function.Body == nil || coroDirectiveDocLinksExternal(group) {
				continue
			}
			if fields[1] == "contract" && coroDirectiveHasField(fields[2:], "scope=wrapper") {
				continue
			}
			return nil, fmt.Errorf(
				"%s:%d: bodyful Go function %s carries %q caller-coloring metadata; "+
					"derive its effect from SSA or use an exact bottom-level scope=wrapper contract",
				path, files.Position(comment.Pos()).Line, function.Name.Name, fields[1],
			)
		}
	}
	return manifest, nil
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
