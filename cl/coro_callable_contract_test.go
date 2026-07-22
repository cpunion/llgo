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
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
)

func TestCoroCallableContractParsesExactDeclarationAndWrapperScopes(t *testing.T) {
	ssaPkg, _, _ := buildGoSSAPkg(t, `package callable

//llgo:coro contract foreign.v1 memory=borrow-until-complete progress=may-block reentry=none affinity=any-thread inline-memory=borrow-until-return inline-reentry=none inline-affinity=any-thread inline-progress=executor-safe
func Foreign(int) int

//llgo:coro contract foreign.v1 reentry=managed-callback abi=word-call.v1/1 scope=wrapper affinity=host-main memory=retained progress=async-completion
func Wrapper(v int) int { return v }

//llgo:coro contract foreign.v1 scope=declaration progress=unknown affinity=unknown reentry=unknown memory=unknown
func Unknown()

func Plain() {}
`)

	foreign, ok, err := coroCallableContractCertificateFor(ssaPkg.Func("Foreign"))
	if err != nil || !ok {
		t.Fatalf("Foreign contract = %+v, %t, %v", foreign, ok, err)
	}
	if foreign.Scope != coroCallableContractScopeDeclaration || foreign.ABI != "" ||
		foreign.Contract.ID != coroCallableContractIDForeignV1 ||
		foreign.Contract.Progress != coro.ProgressMayBlock ||
		foreign.Contract.Affinity != coro.AffinityAnyThread ||
		foreign.Contract.Reentry != coro.ReentryNone ||
		foreign.Contract.Memory != coro.MemoryBorrowUntilComplete ||
		!foreign.HasTrustedInlineContract ||
		foreign.TrustedInlineContract.Progress != coro.ProgressExecutorSafe ||
		foreign.TrustedInlineContract.Affinity != coro.AffinityAnyThread ||
		foreign.TrustedInlineContract.Reentry != coro.ReentryNone ||
		foreign.TrustedInlineContract.Memory != coro.MemoryBorrowUntilReturn {
		t.Fatalf("Foreign contract = %+v", foreign)
	}
	if want := "llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete inline-progress=executor-safe inline-affinity=any-thread inline-reentry=none inline-memory=borrow-until-return"; foreign.Canonical != want {
		t.Fatalf("Foreign canonical = %q, want %q", foreign.Canonical, want)
	}

	wrapper, ok, err := coroCallableContractCertificateFor(ssaPkg.Func("Wrapper"))
	if err != nil || !ok {
		t.Fatalf("Wrapper contract = %+v, %t, %v", wrapper, ok, err)
	}
	if wrapper.Scope != coroCallableContractScopeWrapper || wrapper.ABI != "word-call.v1/1" ||
		wrapper.Contract.Progress != coro.ProgressAsyncCompletion ||
		wrapper.Contract.Affinity != coro.AffinityHostMain ||
		wrapper.Contract.Reentry != coro.ReentryManagedCallback ||
		wrapper.Contract.Memory != coro.MemoryRetained {
		t.Fatalf("Wrapper contract = %+v", wrapper)
	}
	if want := "llgo:coro contract foreign.v1 scope=wrapper progress=async-completion affinity=host-main reentry=managed-callback memory=retained abi=word-call.v1/1"; wrapper.Canonical != want {
		t.Fatalf("Wrapper canonical = %q, want %q", wrapper.Canonical, want)
	}

	unknown, ok, err := coroCallableContractCertificateFor(ssaPkg.Func("Unknown"))
	if err != nil || !ok {
		t.Fatalf("Unknown contract = %+v, %t, %v", unknown, ok, err)
	}
	if unknown.Contract.Progress != coro.ProgressUnknown ||
		unknown.Contract.Affinity != coro.AffinityUnknown ||
		unknown.Contract.Reentry != coro.ReentryUnknown ||
		unknown.Contract.Memory != coro.MemoryUnknown {
		t.Fatalf("explicit unknown contract = %+v", unknown)
	}
	if plain, ok, err := coroCallableContractCertificateFor(ssaPkg.Func("Plain")); err != nil || ok || plain != (coroCallableContractCertificate{}) {
		t.Fatalf("Plain contract = %+v, %t, %v; want absent", plain, ok, err)
	}
	if nilContract, ok, err := coroCallableContractCertificateFor(nil); err != nil || ok || nilContract != (coroCallableContractCertificate{}) {
		t.Fatalf("nil contract = %+v, %t, %v; want absent", nilContract, ok, err)
	}
}

func TestCoroCallableContractRejectsMalformedAndBackendSpecificClaims(t *testing.T) {
	valid := "progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete"
	for _, test := range []struct {
		name      string
		directive string
		body      string
		want      string
	}{
		{name: "missing ID", directive: "//llgo:coro contract", want: "requires an ID"},
		{name: "unknown ID", directive: "//llgo:coro contract native.v1 " + valid, want: "unsupported callable contract ID"},
		{name: "backend ID", directive: "//llgo:coro contract worker.v1 " + valid, want: "backend vocabulary"},
		{name: "missing progress", directive: "//llgo:coro contract foreign.v1 affinity=any-thread reentry=none memory=by-value", want: "requires explicit progress"},
		{name: "missing affinity", directive: "//llgo:coro contract foreign.v1 progress=may-block reentry=none memory=by-value", want: "requires explicit affinity"},
		{name: "missing reentry", directive: "//llgo:coro contract foreign.v1 progress=may-block affinity=any-thread memory=by-value", want: "requires explicit reentry"},
		{name: "missing memory", directive: "//llgo:coro contract foreign.v1 progress=may-block affinity=any-thread reentry=none", want: "requires explicit memory"},
		{name: "inline missing progress", directive: "//llgo:coro contract foreign.v1 " + valid + " inline-affinity=any-thread inline-reentry=none inline-memory=by-value", want: "requires all of inline-progress"},
		{name: "inline missing affinity", directive: "//llgo:coro contract foreign.v1 " + valid + " inline-progress=executor-safe inline-reentry=none inline-memory=by-value", want: "requires all of inline-progress"},
		{name: "inline missing reentry", directive: "//llgo:coro contract foreign.v1 " + valid + " inline-progress=executor-safe inline-affinity=any-thread inline-memory=by-value", want: "requires all of inline-progress"},
		{name: "inline missing memory", directive: "//llgo:coro contract foreign.v1 " + valid + " inline-progress=executor-safe inline-affinity=any-thread inline-reentry=none", want: "requires all of inline-progress"},
		{name: "inline progress may block", directive: "//llgo:coro contract foreign.v1 " + valid + " inline-progress=may-block inline-affinity=any-thread inline-reentry=none inline-memory=by-value", want: "not executor-safe"},
		{name: "inline widens reentry", directive: "//llgo:coro contract foreign.v1 progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete inline-progress=executor-safe inline-affinity=any-thread inline-reentry=managed-callback inline-memory=borrow-until-return", want: "not a safe refinement"},
		{name: "inline widens memory", directive: "//llgo:coro contract foreign.v1 progress=may-block affinity=any-thread reentry=none memory=by-value inline-progress=executor-safe inline-affinity=any-thread inline-reentry=none inline-memory=borrow-until-return", want: "not a safe refinement"},
		{name: "duplicate key", directive: "//llgo:coro contract foreign.v1 " + valid + " progress=may-block", want: `duplicate callable contract key "progress"`},
		{name: "unknown key", directive: "//llgo:coro contract foreign.v1 " + valid + " latency=unbounded", want: `unknown callable contract key "latency"`},
		{name: "empty ABI", directive: "//llgo:coro contract foreign.v1 " + valid + " abi=", want: "must be key=value"},
		{name: "duplicate ABI", directive: "//llgo:coro contract foreign.v1 " + valid + " abi=word-call.v1/1 abi=word-call.v1/1", want: `duplicate callable contract key "abi"`},
		{name: "worker ABI", directive: "//llgo:coro contract foreign.v1 " + valid + " abi=worker-call.v1/1", want: "backend vocabulary"},
		{name: "poll ABI", directive: "//llgo:coro contract foreign.v1 " + valid + " abi=poll.v1", want: "backend vocabulary"},
		{name: "worker backend", directive: "//llgo:coro contract foreign.v1 " + valid + " backend=worker", want: "backend vocabulary"},
		{name: "poll backend value", directive: "//llgo:coro contract foreign.v1 progress=poll affinity=any-thread reentry=none memory=by-value", want: "backend vocabulary"},
		{name: "host token", directive: "//llgo:coro contract foreign.v1 " + valid + " host", want: "backend vocabulary"},
		{name: "unknown progress", directive: "//llgo:coro contract foreign.v1 progress=sometimes affinity=any-thread reentry=none memory=by-value", want: "unknown callable contract progress"},
		{name: "unknown affinity", directive: "//llgo:coro contract foreign.v1 progress=may-block affinity=wherever reentry=none memory=by-value", want: "unknown callable contract affinity"},
		{name: "unknown reentry", directive: "//llgo:coro contract foreign.v1 progress=may-block affinity=any-thread reentry=recursive memory=by-value", want: "unknown callable contract reentry"},
		{name: "unknown memory", directive: "//llgo:coro contract foreign.v1 progress=may-block affinity=any-thread reentry=none memory=shared", want: "unknown callable contract memory"},
		{name: "wrapper scope on declaration", directive: "//llgo:coro contract foreign.v1 scope=wrapper " + valid, want: "conflicts with exact declaration FuncDecl"},
		{name: "declaration scope on wrapper", directive: "//llgo:coro contract foreign.v1 scope=declaration " + valid, body: " {}", want: "conflicts with exact wrapper FuncDecl"},
		{name: "unknown scope", directive: "//llgo:coro contract foreign.v1 scope=callsite " + valid, want: "unknown callable contract scope"},
		{name: "malformed assignment", directive: "//llgo:coro contract foreign.v1 progress =may-block affinity=any-thread reentry=none memory=by-value", want: "must be key=value"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ssaPkg, _, _ := buildGoSSAPkg(t, "package malformed\n\n"+test.directive+"\nfunc Target()"+test.body+"\n")
			certificate, ok, err := coroCallableContractCertificateFor(ssaPkg.Func("Target"))
			if err == nil || ok || certificate != (coroCallableContractCertificate{}) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("contract = %+v, %t, %v; want error containing %q", certificate, ok, err, test.want)
			}
		})
	}
}

func TestCoroCallableContractRejectsDuplicateAndLegacyDirectiveConflicts(t *testing.T) {
	for _, test := range []struct {
		name    string
		comment string
		want    string
	}{
		{
			name: "duplicate contract",
			comment: `//llgo:coro contract foreign.v1 progress=may-block affinity=any-thread reentry=none memory=by-value
//llgo:coro contract foreign.v1 progress=may-block affinity=any-thread reentry=none memory=by-value`,
			want: "duplicate //llgo:coro contract directive",
		},
		{
			name: "legacy worker conflict",
			comment: `//llgo:coro worker
//llgo:coro contract foreign.v1 progress=may-block affinity=any-thread reentry=none memory=by-value`,
			want: "conflicts with legacy directive",
		},
		{
			name: "legacy noblock conflict",
			comment: `//llgo:coro contract foreign.v1 progress=executor-safe affinity=caller-thread reentry=none memory=borrow-until-return
//llgo:coro noblock`,
			want: "conflicts with legacy directive",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ssaPkg, _, _ := buildGoSSAPkg(t, "package conflict\n\n"+test.comment+"\nfunc Target()\n")
			_, ok, err := coroCallableContractCertificateFor(ssaPkg.Func("Target"))
			if err == nil || ok || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("contract = %t, %v; want error containing %q", ok, err, test.want)
			}
		})
	}

	// An old directive by itself remains the old parser's responsibility. The
	// new layer neither accepts it as a callable contract nor rejects it early.
	ssaPkg, _, _ := buildGoSSAPkg(t, `package legacy
//llgo:coro worker
func Worker()
`)
	if certificate, ok, err := coroCallableContractCertificateFor(ssaPkg.Func("Worker")); err != nil || ok || certificate != (coroCallableContractCertificate{}) {
		t.Fatalf("legacy-only contract = %+v, %t, %v; want absent", certificate, ok, err)
	}
}
