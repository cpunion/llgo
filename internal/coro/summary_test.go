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
	"strings"
	"testing"
)

func TestSummaryStableAcrossInsertionOrder(t *testing.T) {
	build := func(reverse bool) Summary {
		t.Helper()
		g := NewGraph()
		functions := []FunctionSpec{
			{ID: "pkg.a"},
			{ID: "pkg.b"},
			{ID: "runtime.sleep", Seed: WaitPlatform, External: ExternalKnown},
		}
		edges := []CallEdge{
			{Caller: "pkg.a", Callee: "pkg.b", Kind: CallDirect},
			{Caller: "pkg.b", Callee: "runtime.sleep", Kind: CallDirect},
		}
		if reverse {
			reverseFunctions(functions)
			reverseEdges(edges)
		}
		for _, fn := range functions {
			mustAddFunction(t, g, fn)
		}
		for _, edge := range edges {
			mustAddCall(t, g, edge)
		}
		plan, err := g.Analyze()
		if err != nil {
			t.Fatal(err)
		}
		return plan.Summary(SummaryMetadata{
			CoroABI:      "v1",
			SchedulerABI: "v1",
			PanicABI:     "explicit-status-v1",
			TargetTriple: "wasm32-unknown-unknown",
		})
	}

	a := build(false)
	b := build(true)
	aData, err := a.MarshalStable()
	if err != nil {
		t.Fatal(err)
	}
	bData, err := b.MarshalStable()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(aData, bData) {
		t.Fatalf("summary depends on insertion order:\n%s\n%s", aData, bData)
	}
	aDigest, err := a.Digest()
	if err != nil {
		t.Fatal(err)
	}
	bDigest, err := b.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if aDigest != bDigest || len(aDigest) != 64 {
		t.Fatalf("digest mismatch: %q vs %q", aDigest, bDigest)
	}
	if !strings.Contains(string(aData), `"effect":"await-structured,wait-platform"`) {
		t.Fatalf("summary does not use stable effect spelling: %s", aData)
	}

	parsed, err := ParseSummary(aData)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := parsed.MarshalStable()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(aData, roundTrip) {
		t.Fatalf("summary round trip changed bytes:\n%s\n%s", aData, roundTrip)
	}
}

func TestEmptySummaryRoundTrip(t *testing.T) {
	summary := Summary{Schema: SummarySchema}
	data, err := summary.MarshalStable()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseSummary(data); err != nil {
		t.Fatalf("parse canonical empty summary: %v\n%s", err, data)
	}
}

func TestSummaryRejectsIncompatibleOrInvalidInput(t *testing.T) {
	if _, err := ParseSummary([]byte(`{"schema":"llgo.coro.plan.v1","metadata":{},"functions":[]}`)); err == nil {
		t.Fatal("newer schema unexpectedly accepted")
	}
	if _, err := ParseSummary([]byte(`{"schema":"llgo.coro.plan.v0","metadata":{},"functions":[],"future":true}`)); err == nil {
		t.Fatal("unknown summary field unexpectedly accepted")
	}
	if _, err := ParseSummary([]byte(`{"schema":"llgo.coro.plan.v0","schema":"llgo.coro.plan.v0","metadata":{"coro_abi":"","scheduler_abi":"","panic_abi":"","target_triple":""},"functions":[]}`)); err == nil {
		t.Fatal("duplicate JSON key unexpectedly accepted")
	}
	if _, err := ParseSummary([]byte(`{"schema":"llgo.coro.plan.v0","metadata":{"coro_abi":"","scheduler_abi":"","panic_abi":"","target_triple":""},"functions":[{"id":"f"}]}`)); err == nil {
		t.Fatal("truncated function summary unexpectedly accepted")
	}
	if _, err := ParseSummary([]byte(`{"schema":"bad","Schema":"llgo.coro.plan.v0","metadata":{"coro_abi":"","scheduler_abi":"","panic_abi":"","target_triple":""},"functions":[]}`)); err == nil {
		t.Fatal("non-canonical JSON key unexpectedly accepted")
	}
	invalidUTF8 := []byte(`{"schema":"llgo.coro.plan.v0","metadata":{"coro_abi":"`)
	invalidUTF8 = append(invalidUTF8, 0xff)
	invalidUTF8 = append(invalidUTF8, []byte(`","scheduler_abi":"","panic_abi":"","target_triple":""},"functions":[]}`)...)
	if _, err := ParseSummary(invalidUTF8); err == nil {
		t.Fatal("invalid UTF-8 summary unexpectedly accepted")
	}

	summary := Summary{
		Schema: SummarySchema,
		Functions: []FunctionSummary{
			{ID: "f", Effect: MayPark, Primary: PrimaryPlain},
		},
	}
	if _, err := summary.MarshalStable(); err == nil {
		t.Fatal("plain primary with suspend effect unexpectedly accepted")
	}

	invalidID := Summary{
		Schema: SummarySchema,
		Functions: []FunctionSummary{{
			ID:      FunctionID(string([]byte{0xff})),
			FuncRep: DirectPlain,
			Primary: PrimaryPlain,
		}},
	}
	if _, err := invalidID.MarshalStable(); err == nil {
		t.Fatal("invalid UTF-8 function ID unexpectedly accepted")
	}

	unknownManaged := Summary{
		Schema: SummarySchema,
		Functions: []FunctionSummary{{
			ID:          "managed",
			LocalEffect: OpaqueSuspend,
			Effect:      OpaqueSuspend,
			FuncRep:     DirectCoro,
			External:    ExternalUnknownManaged,
			Primary:     PrimaryExternal,
		}},
	}
	if _, err := unknownManaged.MarshalStable(); err == nil {
		t.Fatal("unknown managed function without opaque dispatch unexpectedly accepted")
	}

	unknownForeign := Summary{
		Schema: SummarySchema,
		Functions: []FunctionSummary{{
			ID:       "foreign",
			FuncRep:  DirectPlain,
			External: ExternalUnknownForeign,
			Primary:  PrimaryExternal,
		}},
	}
	if _, err := unknownForeign.MarshalStable(); err == nil {
		t.Fatal("unknown foreign function without conservative flags unexpectedly accepted")
	}

	invalidPropagatedExec := Summary{
		Schema: SummarySchema,
		Functions: []FunctionSummary{{
			ID:      "caller",
			Exec:    BlockForeign | NeedsPreempt | NoReturn,
			FuncRep: DirectPlain,
			Primary: PrimaryPlain,
		}},
	}
	if _, err := invalidPropagatedExec.MarshalStable(); err == nil {
		t.Fatal("non-inheritable execution flags unexpectedly appeared only in final plan")
	}
}

func reverseFunctions(values []FunctionSpec) {
	for i, j := 0, len(values)-1; i < j; i, j = i+1, j-1 {
		values[i], values[j] = values[j], values[i]
	}
}

func reverseEdges(values []CallEdge) {
	for i, j := 0, len(values)-1; i < j; i, j = i+1, j-1 {
		values[i], values[j] = values[j], values[i]
	}
}

func FuzzParseSummary(f *testing.F) {
	valid, err := (Summary{Schema: SummarySchema}).MarshalStable()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(valid)
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"schema":"llgo.coro.plan.v0","schema":"duplicate"}`))
	f.Add([]byte{0xff})

	f.Fuzz(func(t *testing.T, data []byte) {
		summary, err := ParseSummary(data)
		if err != nil {
			return
		}
		first, err := summary.MarshalStable()
		if err != nil {
			t.Fatalf("accepted summary cannot be marshaled: %v", err)
		}
		roundTrip, err := ParseSummary(first)
		if err != nil {
			t.Fatalf("canonical summary cannot be parsed: %v\n%s", err, first)
		}
		second, err := roundTrip.MarshalStable()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(first, second) {
			t.Fatalf("canonical summary is unstable:\n%s\n%s", first, second)
		}
	})
}
