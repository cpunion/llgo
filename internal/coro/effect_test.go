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

func TestEffectLattice(t *testing.T) {
	effects := []Effect{
		NoSuspend,
		YieldOnly,
		AwaitStructured,
		MayPark,
		WaitPlatform,
		WaitHost,
		WaitForeign,
		YieldOnly | MayPark,
		OpaqueSuspend,
	}
	for _, a := range effects {
		for _, b := range effects {
			if got, want := a.Join(b), b.Join(a); got != want {
				t.Fatalf("join is not commutative: %s join %s = %s, reverse = %s", a, b, got, want)
			}
			if got := a.Join(b); !got.Contains(a) || !got.Contains(b) {
				t.Fatalf("join %s does not contain both %s and %s", got, a, b)
			}
		}
		if got := a.Join(a); got != a.Normalize() {
			t.Fatalf("join is not idempotent: %s join itself = %s", a, got)
		}
	}

	if WaitHost.Normalize() != WaitHost|WaitPlatform {
		t.Fatalf("WaitHost normalization = %s, want wait-host + wait-platform", WaitHost.Normalize())
	}
	if !OpaqueSuspend.Contains(knownSuspendEffects) {
		t.Fatalf("OpaqueSuspend should be lattice top, got %s", OpaqueSuspend)
	}
	if NoSuspend.MaySuspend() {
		t.Fatal("NoSuspend unexpectedly suspends")
	}
}

func TestEffectTextRoundTrip(t *testing.T) {
	want := (WaitHost | MayPark | YieldOnly).Normalize()
	text, err := want.MarshalText()
	if err != nil {
		t.Fatal(err)
	}
	if got := string(text); got != "yield-only,may-park,wait-platform,wait-host" {
		t.Fatalf("stable effect text = %q", got)
	}
	var parsed Effect
	if err := parsed.UnmarshalText(text); err != nil {
		t.Fatal(err)
	}
	if parsed != want {
		t.Fatalf("round-trip effect = %s, want %s", parsed, want)
	}
	if err := parsed.UnmarshalText([]byte("future-effect")); err == nil {
		t.Fatal("unknown effect unexpectedly accepted")
	}
	if err := (Effect(1 << 15)).Validate(); err == nil {
		t.Fatal("unknown effect bit unexpectedly accepted")
	}
}

func TestEffectLatticeExhaustive(t *testing.T) {
	for a := Effect(0); a <= validEffectBits; a++ {
		if err := a.Validate(); err != nil {
			t.Fatalf("valid effect %#x rejected: %v", a, err)
		}
		if got := a.Join(NoSuspend); got != a.Normalize() {
			t.Fatalf("bottom identity for %#x = %#x", a, got)
		}
		if got := a.Join(OpaqueSuspend); got != OpaqueSuspend {
			t.Fatalf("top join for %#x = %#x", a, got)
		}
		for b := Effect(0); b <= validEffectBits; b++ {
			ab := a.Join(b)
			if ab != b.Join(a) || !ab.Contains(a) || !ab.Contains(b) {
				t.Fatalf("invalid join %#x + %#x = %#x", a, b, ab)
			}
			for c := Effect(0); c <= validEffectBits; c++ {
				if a.Join(b).Join(c) != a.Join(b.Join(c)) {
					t.Fatalf("join is not associative for %#x, %#x, %#x", a, b, c)
				}
			}
		}
	}
}
