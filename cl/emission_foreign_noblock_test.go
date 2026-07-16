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

package cl

import (
	"go/ast"
	"strings"
	"testing"

	llssa "github.com/goplus/llgo/ssa"
)

func TestEmissionUniverseFreezesExactForeignNoBlockCertificate(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/noblock", `package noblock

//llgo:coro noblock
//go:linkname Safe C.audit_safe
func Safe(int) int

//go:linkname Memcpy C.memcpy
func Memcpy(uintptr)

func SameDisplayName() {}
func root(n uintptr) { _ = Safe(1); Memcpy(n); SameDisplayName() }
`)
	testProg.ssa.Build()
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{
		SSA: pkg.ssa, Files: []*ast.File{pkg.file}, Identity: "noblock-owner",
	}})
	if err != nil {
		t.Fatal(err)
	}
	safe := pkg.ssa.Func("Safe")
	certificate, certified, err := universe.CoroForeignNoBlockCertificate(safe)
	if err != nil || !certified || certificate.ID == "" || certificate.ABISignature == "" || !strings.Contains(certificate.PhysicalSymbol, "audit_safe") {
		t.Fatalf("Safe certificate = %+v, %t, %v; want exact frozen physical proof", certificate, certified, err)
	}
	for _, name := range []string{"Memcpy", "SameDisplayName"} {
		if certificate, certified, err := universe.CoroForeignNoBlockCertificate(pkg.ssa.Func(name)); err != nil || certified || certificate != (CoroForeignNoBlockCertificate{}) {
			t.Fatalf("%s certificate = %+v, %t, %v; want no name-derived proof", name, certificate, certified, err)
		}
	}
	// The certificate is immutable construction metadata, not a late AST query.
	for _, comment := range safe.Syntax().(*ast.FuncDecl).Doc.List {
		if strings.Contains(comment.Text, "llgo:coro") {
			comment.Text = "// ordinary comment"
		}
	}
	again, certified, err := universe.CoroForeignNoBlockCertificate(safe)
	if err != nil || !certified || again != certificate {
		t.Fatalf("mutated-source certificate = %+v, %t, %v; want frozen %+v", again, certified, err, certificate)
	}
}

func TestEmissionUniverseForeignNoBlockFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name    string
		source  string
		wantErr string
	}{
		{
			name: "Go body",
			source: `package bad
//llgo:coro noblock
func Fake() {}
`,
			wantErr: "requires an exact frozen C declaration",
		},
		{
			name: "unsupported spelling",
			source: `package bad
//llgo:coro nosuspend
//go:linkname Fake C.fake
func Fake()
`,
			wantErr: "unsupported directive",
		},
		{
			name: "duplicate",
			source: `package bad
//llgo:coro noblock
//llgo:coro noblock
//go:linkname Fake C.fake
func Fake()
`,
			wantErr: "duplicate",
		},
		{
			name: "physical signature conflict",
			source: `package bad
//llgo:coro noblock
//go:linkname Safe C.same_physical
func Safe(int) int
//go:linkname Conflict C.same_physical
func Conflict(string) string
func root() { _ = Safe(1); _ = Conflict("") }
`,
			wantErr: "conflicting frozen ABI signatures",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			testProg := newEmissionTestProgram()
			pkg := testProg.addPackage(t, "example.com/emission/badnoblock", test.source)
			testProg.ssa.Build()
			prog := llssa.NewProgram(nil)
			defer prog.Dispose()
			_, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{
				SSA: pkg.ssa, Files: []*ast.File{pkg.file}, Identity: "bad-noblock-owner",
			}})
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("PrepareEmissionUniverse error = %v; want %q", err, test.wantErr)
			}
		})
	}
}
