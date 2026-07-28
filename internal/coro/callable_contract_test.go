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

package coro

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestCallableContractFactsCanonicalJSONAndDigest(t *testing.T) {
	first := validCallableContractFacts()
	second := first
	second.Contracts = []CallableContract{first.Contracts[4], first.Contracts[3], first.Contracts[1], first.Contracts[0], first.Contracts[2]}
	second.Callables = []CallableFact{first.Callables[1], first.Callables[0]}
	second.Invocations = []InvocationFact{first.Invocations[1], first.Invocations[0]}
	second.Invocations[1].Candidates = []CallableRefID{"callable.b", "callable.a"}

	firstJSON, err := first.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := second.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatalf("canonical JSON depends on input order:\n%s\n%s", firstJSON, secondJSON)
	}
	if got := second.Invocations[1].Candidates; !reflect.DeepEqual(got, []CallableRefID{"callable.b", "callable.a"}) {
		t.Fatalf("CanonicalJSON mutated input candidates: %v", got)
	}
	firstDigest, err := first.Digest()
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := second.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest != secondDigest || len(firstDigest) != 64 {
		t.Fatalf("digests = %q, %q", firstDigest, secondDigest)
	}

	changed := validCallableContractFacts()
	changed.Contracts[0].Memory = MemoryRetained
	changedDigest, err := changed.Digest()
	if err == nil || changedDigest != "" {
		// The mutation invalidates the closed Auto join. Digest must fail closed,
		// not silently hash facts whose selected contract is stale.
		t.Fatalf("invalid changed facts digest = %q, %v", changedDigest, err)
	}
}

func TestCallableContractFactsOpenAutoUsesUnknownContract(t *testing.T) {
	facts := validCallableContractFacts()
	facts.Invocations = []InvocationFact{{
		Site:       callableTestSite("caller.open", 0),
		Candidates: []CallableRefID{"callable.a"},
		Open:       true,
		Policy:     InvocationAuto,
		Contract:   "contract.unknown",
		ABI:        "go.typed.v1",
	}}
	if err := facts.Verify(); err != nil {
		t.Fatal(err)
	}
	facts.Invocations[0].Policy = InvocationTrustedInline
	if err := facts.Verify(); err == nil || !strings.Contains(err.Error(), "open") {
		t.Fatalf("trusted open error = %v", err)
	}
}

func TestTrustedInlineCallableContractUsesSharedSafeRefinement(t *testing.T) {
	base := CallableContract{
		ID: "contract.default", Progress: ProgressMayBlock, Affinity: AffinityAnyThread,
		Reentry: ReentryManagedCallback, Memory: MemoryBorrowUntilComplete,
	}
	trusted := CallableContract{
		ID: "contract.inline", Progress: ProgressExecutorSafe, Affinity: AffinityCallerThread,
		Reentry: ReentryNone, Memory: MemoryBorrowUntilReturn,
	}
	if err := ValidateTrustedInlineCallableContractRefinement(trusted, base); err != nil {
		t.Fatalf("safe trusted-inline refinement rejected: %v", err)
	}
	for _, test := range []struct {
		name string
		edit func(*CallableContract, *CallableContract)
		want string
	}{
		{"not executor safe", func(_ *CallableContract, refined *CallableContract) { refined.Progress = ProgressMayBlock }, "not executor-safe"},
		{"affinity widening", func(base, refined *CallableContract) {
			base.Affinity, refined.Affinity = AffinityCallerThread, AffinityHostMain
		}, "not a safe refinement"},
		{"reentry widening", func(base, refined *CallableContract) {
			base.Reentry, refined.Reentry = ReentryNone, ReentryManagedCallback
		}, "not a safe refinement"},
		{"memory widening", func(base, refined *CallableContract) {
			base.Memory, refined.Memory = MemoryByValue, MemoryBorrowUntilReturn
		}, "not a safe refinement"},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidateBase, candidateTrusted := base, trusted
			test.edit(&candidateBase, &candidateTrusted)
			if err := ValidateTrustedInlineCallableContractRefinement(candidateTrusted, candidateBase); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("refinement error = %v; want %q", err, test.want)
			}
		})
	}
}

func TestCallableContractDirectExecutorCompatible(t *testing.T) {
	base := CallableContract{
		ID:       "test.v1",
		Progress: ProgressExecutorSafe,
		Affinity: AffinityCallerThread,
		Reentry:  ReentryNone,
		Memory:   MemoryBorrowUntilReturn,
	}
	if !CallableContractDirectExecutorCompatible(base) {
		t.Fatal("exact caller-thread borrowed executor leaf was rejected")
	}
	for _, mutate := range []func(*CallableContract){
		func(contract *CallableContract) { contract.Progress = ProgressMayBlock },
		func(contract *CallableContract) { contract.Affinity = AffinityOwnerThread },
		func(contract *CallableContract) { contract.Reentry = ReentryManagedCallback },
		func(contract *CallableContract) { contract.Memory = MemoryBorrowUntilComplete },
	} {
		candidate := base
		mutate(&candidate)
		if CallableContractDirectExecutorCompatible(candidate) {
			t.Fatalf("incompatible direct executor contract was accepted: %+v", candidate)
		}
	}
}

func TestCallableContractFactsFailClosed(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*CallableContractFacts)
		want string
	}{
		{"schema", func(f *CallableContractFacts) { f.Schema = "wrong" }, "schema"},
		{"invalid progress", func(f *CallableContractFacts) { f.Contracts[0].Progress = "" }, "progress class"},
		{"invalid affinity", func(f *CallableContractFacts) { f.Contracts[0].Affinity = "worker" }, "affinity class"},
		{"invalid reentry", func(f *CallableContractFacts) { f.Contracts[0].Reentry = "recursive" }, "reentry class"},
		{"invalid memory", func(f *CallableContractFacts) { f.Contracts[0].Memory = "borrow" }, "memory class"},
		{"duplicate contract", func(f *CallableContractFacts) { f.Contracts = append(f.Contracts, f.Contracts[0]) }, "duplicate callable contract"},
		{"empty ABI", func(f *CallableContractFacts) { f.Callables[0].ABI = "" }, "empty callable ABI"},
		{"unknown default", func(f *CallableContractFacts) { f.Callables[0].Contract = "missing" }, "unknown contract"},
		{"unknown trusted", func(f *CallableContractFacts) { f.Callables[0].TrustedInlineContract = "missing" }, "unknown trusted-inline"},
		{"trusted not executor safe", func(f *CallableContractFacts) { f.Callables[0].TrustedInlineContract = "contract.join" }, "not executor-safe"},
		{"duplicate callable", func(f *CallableContractFacts) { f.Callables = append(f.Callables, f.Callables[0]) }, "duplicate callable reference"},
		{"no candidates", func(f *CallableContractFacts) { f.Invocations[0].Candidates = nil }, "no callable candidates"},
		{"duplicate candidate", func(f *CallableContractFacts) {
			f.Invocations[0].Candidates = []CallableRefID{"callable.a", "callable.a"}
		}, "duplicate candidate"},
		{"unknown candidate", func(f *CallableContractFacts) { f.Invocations[0].Candidates = []CallableRefID{"missing"} }, "unknown callable"},
		{"ABI mismatch", func(f *CallableContractFacts) { f.Invocations[0].ABI = "word-call.v1/1" }, "differs from candidate"},
		{"wrong auto join", func(f *CallableContractFacts) { f.Invocations[0].Contract = "contract.default.a" }, "conservative candidate join"},
		{"trusted candidate mismatch", func(f *CallableContractFacts) { f.Invocations[1].Candidates = []CallableRefID{"callable.b"} }, "does not own contract"},
		{"unknown invocation contract", func(f *CallableContractFacts) { f.Invocations[0].Contract = "missing" }, "unknown contract"},
		{"duplicate site", func(f *CallableContractFacts) { f.Invocations[1].Site = f.Invocations[0].Site }, "duplicate callable invocation site"},
	} {
		t.Run(test.name, func(t *testing.T) {
			facts := validCallableContractFacts()
			test.edit(&facts)
			if err := facts.Verify(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Verify error = %v, want %q", err, test.want)
			}
		})
	}
}

func validCallableContractFacts() CallableContractFacts {
	return CallableContractFacts{
		Schema: CallableContractFactsSchema,
		Contracts: []CallableContract{
			{ID: "contract.default.a", Progress: ProgressMayBlock, Affinity: AffinityAnyThread, Reentry: ReentryManagedCallback, Memory: MemoryBorrowUntilComplete},
			{ID: "contract.default.b", Progress: ProgressExecutorSafe, Affinity: AffinityCallerThread, Reentry: ReentryNone, Memory: MemoryByValue},
			{ID: "contract.inline", Progress: ProgressExecutorSafe, Affinity: AffinityCallerThread, Reentry: ReentryNone, Memory: MemoryBorrowUntilReturn},
			{ID: "contract.join", Progress: ProgressMayBlock, Affinity: AffinityCallerThread, Reentry: ReentryManagedCallback, Memory: MemoryBorrowUntilComplete},
			{ID: "contract.unknown", Progress: ProgressUnknown, Affinity: AffinityUnknown, Reentry: ReentryUnknown, Memory: MemoryUnknown},
		},
		Callables: []CallableFact{
			{Ref: "callable.a", Function: "function.a", ABI: "go.typed.v1", Contract: "contract.default.a", TrustedInlineContract: "contract.inline"},
			{Ref: "callable.b", Function: "function.b", ABI: "go.typed.v1", Contract: "contract.default.b"},
		},
		Invocations: []InvocationFact{
			{
				Site: callableTestSite("caller.auto", 0), Candidates: []CallableRefID{"callable.a", "callable.b"},
				Policy: InvocationAuto, Contract: "contract.join", ABI: "go.typed.v1",
			},
			{
				Site: callableTestSite("caller.inline", 1), Candidates: []CallableRefID{"callable.a"},
				Policy: InvocationTrustedInline, Contract: "contract.inline", ABI: "go.typed.v1",
			},
		},
	}
}

func callableTestSite(function FunctionID, ordinal int) SourceSiteID {
	return SourceSiteID{
		Function: function, Kind: SourceFunction,
		Block: -1, Instruction: -1, Successor: -1,
		Role: RoleCall, Ordinal: ordinal,
	}
}
