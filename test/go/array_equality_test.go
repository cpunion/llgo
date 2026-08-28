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

package gotest

import (
	"math"
	"testing"
)

func TestLargeArrayEquality(t *testing.T) {
	x := [1024]byte{1}
	y := [1024]byte{1}
	if x != y {
		t.Fatal("equal byte arrays compared unequal")
	}
	y[len(y)-1] = 2
	if x == y {
		t.Fatal("unequal byte arrays compared equal")
	}
}

func TestArrayEqualityPreservesLoadedValue(t *testing.T) {
	x := [32]byte{1}
	want := [32]byte{1}
	snapshot := x
	x[0] = 2
	if snapshot != want {
		t.Fatalf("comparison observed a later source mutation: got %v, want %v", snapshot, want)
	}
}

func TestArrayEqualityUsesElementSemantics(t *testing.T) {
	x := [5]float64{math.NaN()}
	if x == x {
		t.Fatal("array comparison treated NaN as regular memory")
	}
	x32 := [5]float32{float32(math.NaN())}
	if x32 == x32 {
		t.Fatal("float32 array comparison treated NaN as regular memory")
	}
	c64 := [5]complex64{complex(float32(math.NaN()), 0)}
	if c64 == c64 {
		t.Fatal("complex64 array comparison treated NaN as regular memory")
	}
	c128 := [5]complex128{complex(math.NaN(), 0)}
	if c128 == c128 {
		t.Fatal("complex128 array comparison treated NaN as regular memory")
	}
	positiveZero := [5]float64{0}
	negativeZero := [5]float64{math.Copysign(0, -1)}
	if positiveZero != negativeZero {
		t.Fatal("float array comparison distinguished positive and negative zero")
	}

	// Array equality stops at the first unequal element. Comparing the second
	// interface value would panic because slices are not comparable.
	a := [2]any{0, []int{1}}
	b := [2]any{1, []int{1}}
	if a == b {
		t.Fatal("arrays with unequal first elements compared equal")
	}
}

func TestArrayEqualityPropagatesInterfacePanic(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("comparing interface arrays containing slices did not panic")
		}
	}()
	a := [2]any{[]int{1}, 0}
	b := [2]any{[]int{1}, 0}
	_ = a == b
}
