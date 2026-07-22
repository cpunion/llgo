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

func TestEmissionUniverseFreezesDistinctForeignCallCapabilities(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/capabilities", `package capabilities

//llgo:coro noblock
//go:linkname NoBlock C.cap_noblock
func NoBlock(int) int

//llgo:coro sync
//go:linkname Sync C.cap_sync
func Sync(int) int

//llgo:coro schedulerwait
//go:linkname SchedulerWait C.cap_schedulerwait
func SchedulerWait(int) int

//llgo:coro worker
//go:linkname Worker C.cap_worker
func Worker(int) int

//go:linkname Ordinary C.cap_ordinary
func Ordinary(int) int

func root(v int) int { return NoBlock(v) + Sync(v) + SchedulerWait(v) + Worker(v) + Ordinary(v) }
`)
	testProg.ssa.Build()
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{
		SSA: pkg.ssa, Files: []*ast.File{pkg.file}, Identity: "foreign-capability-owner",
	}})
	if err != nil {
		t.Fatal(err)
	}

	noBlock, noBlockOK, err := universe.CoroForeignNoBlockCertificate(pkg.ssa.Func("NoBlock"))
	if err != nil || !noBlockOK || noBlock.ID == "" || noBlock.PhysicalSymbol == "" || noBlock.ABISignature == "" {
		t.Fatalf("noblock certificate = %+v, %t, %v", noBlock, noBlockOK, err)
	}
	syncCertificate, syncOK, err := universe.CoroForeignSyncCertificate(pkg.ssa.Func("Sync"))
	if err != nil || !syncOK || syncCertificate.ID == "" || syncCertificate.PhysicalSymbol == "" || syncCertificate.ABISignature == "" {
		t.Fatalf("sync certificate = %+v, %t, %v", syncCertificate, syncOK, err)
	}
	waitCertificate, waitOK, err := universe.CoroForeignSchedulerWaitCertificate(pkg.ssa.Func("SchedulerWait"))
	if err != nil || !waitOK || waitCertificate.ID == "" || waitCertificate.PhysicalSymbol == "" || waitCertificate.ABISignature == "" {
		t.Fatalf("schedulerwait certificate = %+v, %t, %v", waitCertificate, waitOK, err)
	}
	workerCertificate, workerOK, err := universe.CoroForeignWorkerCertificate(pkg.ssa.Func("Worker"))
	if err != nil || !workerOK || workerCertificate.ID == "" || workerCertificate.PhysicalSymbol == "" || workerCertificate.ABISignature == "" {
		t.Fatalf("worker certificate = %+v, %t, %v", workerCertificate, workerOK, err)
	}
	identities := map[string]struct{}{
		noBlock.ID: {}, syncCertificate.ID: {}, waitCertificate.ID: {}, workerCertificate.ID: {},
	}
	if len(identities) != 4 {
		t.Fatalf("foreign capability identities are not domain-separated: noblock=%q sync=%q schedulerwait=%q worker=%q", noBlock.ID, syncCertificate.ID, waitCertificate.ID, workerCertificate.ID)
	}
	for _, test := range []struct {
		name string
		get  func() (bool, error)
	}{
		{"noblock on Sync", func() (bool, error) {
			_, ok, err := universe.CoroForeignNoBlockCertificate(pkg.ssa.Func("Sync"))
			return ok, err
		}},
		{"sync on SchedulerWait", func() (bool, error) {
			_, ok, err := universe.CoroForeignSyncCertificate(pkg.ssa.Func("SchedulerWait"))
			return ok, err
		}},
		{"schedulerwait on Ordinary", func() (bool, error) {
			_, ok, err := universe.CoroForeignSchedulerWaitCertificate(pkg.ssa.Func("Ordinary"))
			return ok, err
		}},
		{"worker on Ordinary", func() (bool, error) {
			_, ok, err := universe.CoroForeignWorkerCertificate(pkg.ssa.Func("Ordinary"))
			return ok, err
		}},
	} {
		if ok, err := test.get(); err != nil || ok {
			t.Fatalf("%s = %t, %v; want absent", test.name, ok, err)
		}
	}

	// Capability lookup is frozen construction metadata, not a late AST query.
	declaration := pkg.ssa.Func("Sync").Syntax().(*ast.FuncDecl)
	for _, comment := range declaration.Doc.List {
		if strings.Contains(comment.Text, "llgo:coro") {
			comment.Text = "//llgo:coro schedulerwait"
		}
	}
	again, ok, err := universe.CoroForeignSyncCertificate(pkg.ssa.Func("Sync"))
	if err != nil || !ok || again != syncCertificate {
		t.Fatalf("mutated source changed frozen sync certificate: %+v, %t, %v; want %+v", again, ok, err, syncCertificate)
	}
}

func TestEmissionUniverseForeignCallCapabilitiesFailClosed(t *testing.T) {
	for _, test := range []struct {
		name    string
		source  string
		wantErr string
	}{
		{
			name: "sync Go body",
			source: `package bad
//llgo:coro sync
func Fake() {}
`,
			wantErr: "requires an exact frozen C declaration",
		},
		{
			name: "schedulerwait Go body",
			source: `package bad
//llgo:coro schedulerwait
func Fake() {}
`,
			wantErr: "requires an exact frozen C declaration",
		},
		{
			name: "worker Go body",
			source: `package bad
//llgo:coro worker
func Fake() {}
`,
			wantErr: "requires an exact frozen C declaration",
		},
		{
			name: "mixed directives",
			source: `package bad
//llgo:coro sync
//llgo:coro schedulerwait
//go:linkname Fake C.fake
func Fake()
`,
			wantErr: "mutually exclusive",
		},
		{
			name: "schedulerwait ABI collision",
			source: `package bad
//llgo:coro schedulerwait
//go:linkname Wait C.same_wait
func Wait(int) int
//go:linkname Conflict C.same_wait
func Conflict(string) string
func root() { _ = Wait(1); _ = Conflict("") }
`,
			wantErr: "conflicting frozen ABI signatures",
		},
		{
			name: "worker ABI collision",
			source: `package bad
//llgo:coro worker
//go:linkname Wait C.same_worker
func Wait(int) int
//go:linkname Conflict C.same_worker
func Conflict(string) string
func root() { _ = Wait(1); _ = Conflict("") }
`,
			wantErr: "conflicting frozen ABI signatures",
		},
		{
			name: "malformed word ABI cannot hide collision",
			source: `package bad
//llgo:coro worker
//go:linkname Typed C.same_malformed_word
func Typed(int) int
//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=by-value abi=word-call.v1/not-an-arity
//go:linkname libc_same_malformed_word_trampoline C.same_malformed_word
func libc_same_malformed_word_trampoline()
func root() { _ = Typed(1); libc_same_malformed_word_trampoline() }
`,
			wantErr: "conflicting frozen ABI signatures",
		},
		{
			name: "same symbol mutually exclusive capabilities",
			source: `package bad
//llgo:coro sync
//go:linkname Sync C.same_capability
func Sync(int) int
//llgo:coro schedulerwait
//go:linkname Wait C.same_capability
func Wait(int) int
func root() { _ = Sync(1); _ = Wait(2) }
`,
			wantErr: "mutually exclusive",
		},
		{
			name: "same symbol worker and sync are mutually exclusive",
			source: `package bad
//llgo:coro worker
//go:linkname Worker C.same_worker_capability
func Worker(int) int
//llgo:coro sync
//go:linkname Sync C.same_worker_capability
func Sync(int) int
func root() { _ = Worker(1); _ = Sync(2) }
`,
			wantErr: "mutually exclusive",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			testProg := newEmissionTestProgram()
			pkg := testProg.addPackage(t, "example.com/emission/badcapability", test.source)
			testProg.ssa.Build()
			prog := llssa.NewProgram(nil)
			defer prog.Dispose()
			_, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{
				SSA: pkg.ssa, Files: []*ast.File{pkg.file}, Identity: "bad-foreign-capability-owner",
			}})
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("PrepareEmissionUniverse error = %v; want %q", err, test.wantErr)
			}
		})
	}
}
