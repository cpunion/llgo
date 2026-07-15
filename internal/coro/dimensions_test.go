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

import "testing"

func TestExecFlagsTextRoundTrip(t *testing.T) {
	want := BlockForeign | ThreadAffine | NeedsPreempt
	text, err := want.MarshalText()
	if err != nil {
		t.Fatal(err)
	}
	if got := string(text); got != "block-foreign,thread-affine,needs-preempt" {
		t.Fatalf("stable execution flags = %q", got)
	}
	var parsed ExecFlags
	if err := parsed.UnmarshalText(text); err != nil {
		t.Fatal(err)
	}
	if parsed != want {
		t.Fatalf("execution flags round trip = %s, want %s", parsed, want)
	}
	if err := parsed.UnmarshalText([]byte("future-flag")); err == nil {
		t.Fatal("unknown execution flag unexpectedly accepted")
	}
}

func TestDemandAndFuncRepText(t *testing.T) {
	if SyncDemand == NoDemand || AsyncDemand == NoDemand || BothDemand != SyncDemand|AsyncDemand {
		t.Fatalf("invalid demand lattice: sync=%d async=%d both=%d", SyncDemand, AsyncDemand, BothDemand)
	}
	for _, demand := range []Demand{NoDemand, SyncDemand, AsyncDemand, BothDemand} {
		text, err := demand.MarshalText()
		if err != nil {
			t.Fatal(err)
		}
		var parsed Demand
		if err := parsed.UnmarshalText(text); err != nil {
			t.Fatal(err)
		}
		if parsed != demand {
			t.Fatalf("demand round trip = %s, want %s", parsed, demand)
		}
	}
	for _, rep := range []FuncRep{DirectPlain, DirectCoro, Dispatch} {
		text, err := rep.MarshalText()
		if err != nil {
			t.Fatal(err)
		}
		var parsed FuncRep
		if err := parsed.UnmarshalText(text); err != nil {
			t.Fatal(err)
		}
		if parsed != rep {
			t.Fatalf("function representation round trip = %s, want %s", parsed, rep)
		}
	}
}

func TestDemandAndExecLatticesExhaustive(t *testing.T) {
	demands := []Demand{NoDemand, SyncDemand, AsyncDemand, BothDemand}
	for _, a := range demands {
		for _, b := range demands {
			joined := a.Join(b)
			if joined != b.Join(a) || !joined.Contains(a) || !joined.Contains(b) {
				t.Fatalf("invalid demand join %s + %s = %s", a, b, joined)
			}
		}
	}
	for a := ExecFlags(0); a <= validExecFlags; a++ {
		if err := a.Validate(); err != nil {
			t.Fatalf("valid execution flags %#x rejected: %v", a, err)
		}
		for b := ExecFlags(0); b <= validExecFlags; b++ {
			joined := a.Join(b)
			if joined != b.Join(a) || !joined.Contains(a) || !joined.Contains(b) {
				t.Fatalf("invalid execution flag join %#x + %#x = %#x", a, b, joined)
			}
		}
	}
}
