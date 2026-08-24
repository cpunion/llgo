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

package coro

import "testing"

func TestProveSSAImmutableCaptureSnapshotsPerIterationLoop(t *testing.T) {
	_, pkg := buildCoroTestSSA(t, "capture.go", `package coroid

func step(value, salt int) int { return value + salt }

func parallel(count, rounds int, results chan int) {
	for worker := 0; worker < count; worker++ {
		go func() {
			value := worker + 1
			for index := 0; index < rounds; index++ {
				value = step(value, worker*rounds+index)
			}
			results <- value
		}()
	}
}
`)
	parent := packageFunction(t, pkg, "parallel")
	if len(parent.AnonFuncs) != 1 {
		t.Fatalf("parallel anonymous functions = %d, want 1", len(parent.AnonFuncs))
	}
	closure := parent.AnonFuncs[0]
	proofs := ProveSSAImmutableCaptureSnapshots(closure)
	if len(proofs) != 3 {
		t.Fatalf("immutable capture proofs = %d, want 3", len(proofs))
	}
	wantLoads := []int{2, 2, 1}
	for index, proof := range proofs {
		if proof.Index != index || proof.FreeVar != closure.FreeVars[index] {
			t.Fatalf("proof %d identity = index %d free %p, want index %d free %p", index, proof.Index, proof.FreeVar, index, closure.FreeVars[index])
		}
		if len(proof.Loads) != wantLoads[index] {
			t.Fatalf("proof %d loads = %d, want %d", index, len(proof.Loads), wantLoads[index])
		}
		if len(proof.Producers) != 1 {
			t.Fatalf("proof %d producers = %d, want 1", index, len(proof.Producers))
		}
	}
}

func TestProveSSAImmutableCaptureSnapshotsRejectsMutationAndEscape(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{
			name: "store-after-closure",
			source: `package coroid
func use(int) {}
func source() func() {
	x := 1
	closure := func() { use(x) }
	x = 2
	return closure
}`,
		},
		{
			name: "shared-loop-cell",
			source: `package coroid
func use(int) {}
func source(count int) {
	x := 0
	for index := 0; index < count; index++ {
		go func() { use(x) }()
		x++
	}
}`,
		},
		{
			name: "closure-store",
			source: `package coroid
func source() func() {
	x := 0
	return func() { x++ }
}`,
		},
		{
			name: "address-escape",
			source: `package coroid
func escape(*int) {}
func use(int) {}
func source() func() {
	x := 0
	escape(&x)
	return func() { use(x) }
}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, pkg := buildCoroTestSSA(t, "capture.go", test.source)
			parent := packageFunction(t, pkg, "source")
			if len(parent.AnonFuncs) != 1 {
				t.Fatalf("source anonymous functions = %d, want 1", len(parent.AnonFuncs))
			}
			if proofs := ProveSSAImmutableCaptureSnapshots(parent.AnonFuncs[0]); len(proofs) != 0 {
				t.Fatalf("unsafe immutable capture proofs = %d, want 0", len(proofs))
			}
		})
	}
}

func TestProveSSAImmutableCaptureSnapshotsKeepsIndependentCapture(t *testing.T) {
	_, pkg := buildCoroTestSSA(t, "capture.go", `package coroid
func use(int, int) {}
func source() func() {
	mutable := 1
	stable := 2
	closure := func() { use(mutable, stable) }
	mutable = 3
	return closure
}`)
	parent := packageFunction(t, pkg, "source")
	closure := parent.AnonFuncs[0]
	proofs := ProveSSAImmutableCaptureSnapshots(closure)
	if len(proofs) != 1 || proofs[0].FreeVar.Name() != "stable" {
		t.Fatalf("independent immutable capture proofs = %+v, want stable only", proofs)
	}
}
