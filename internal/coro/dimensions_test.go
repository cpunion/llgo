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

func TestDemandFuncRepAndBodyEmissionText(t *testing.T) {
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
	for _, transport := range []FuncTransport{ManagedTransport, RawCCodePointer} {
		text, err := transport.MarshalText()
		if err != nil {
			t.Fatal(err)
		}
		var parsed FuncTransport
		if err := parsed.UnmarshalText(text); err != nil {
			t.Fatal(err)
		}
		if parsed != transport {
			t.Fatalf("function transport round trip = %s, want %s", parsed, transport)
		}
	}
	for _, emission := range []BodyEmission{EmitNone, EmitPlain, EmitCoroutine, EmitExternal} {
		text, err := emission.MarshalText()
		if err != nil {
			t.Fatal(err)
		}
		var parsed BodyEmission
		if err := parsed.UnmarshalText(text); err != nil {
			t.Fatal(err)
		}
		if parsed != emission {
			t.Fatalf("body emission round trip = %s, want %s", parsed, emission)
		}
	}
	if err := (BodyEmission(255)).Validate(); err == nil {
		t.Fatal("invalid body emission unexpectedly accepted")
	}
	if err := (FuncTransport(255)).Validate(); err == nil {
		t.Fatal("invalid function transport unexpectedly accepted")
	}
}

func TestDemandAndFuncRepTextWhitespace(t *testing.T) {
	for text, want := range map[string]Demand{
		"  none\n":   NoDemand,
		"\tsync ":    SyncDemand,
		" async\r\n": AsyncDemand,
		"\nboth\t":   BothDemand,
	} {
		var got Demand
		if err := got.UnmarshalText([]byte(text)); err != nil {
			t.Fatalf("Demand.UnmarshalText(%q): %v", text, err)
		}
		if got != want {
			t.Fatalf("Demand.UnmarshalText(%q) = %s, want %s", text, got, want)
		}
	}

	for text, want := range map[string]FuncRep{
		"  direct-plain\n": DirectPlain,
		"\tdirect-coro ":   DirectCoro,
		" dispatch\r\n":    Dispatch,
	} {
		var got FuncRep
		if err := got.UnmarshalText([]byte(text)); err != nil {
			t.Fatalf("FuncRep.UnmarshalText(%q): %v", text, err)
		}
		if got != want {
			t.Fatalf("FuncRep.UnmarshalText(%q) = %s, want %s", text, got, want)
		}
	}

	for text, want := range map[string]FuncTransport{
		"  managed\n":           ManagedTransport,
		"\traw-c-code-pointer ": RawCCodePointer,
	} {
		var got FuncTransport
		if err := got.UnmarshalText([]byte(text)); err != nil {
			t.Fatalf("FuncTransport.UnmarshalText(%q): %v", text, err)
		}
		if got != want {
			t.Fatalf("FuncTransport.UnmarshalText(%q) = %s, want %s", text, got, want)
		}
	}

	for text, want := range map[string]BodyEmission{
		"  none\n":       EmitNone,
		"\tplain ":       EmitPlain,
		" coroutine\r\n": EmitCoroutine,
		"\nexternal\t":   EmitExternal,
	} {
		var got BodyEmission
		if err := got.UnmarshalText([]byte(text)); err != nil {
			t.Fatalf("BodyEmission.UnmarshalText(%q): %v", text, err)
		}
		if got != want {
			t.Fatalf("BodyEmission.UnmarshalText(%q) = %s, want %s", text, got, want)
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
