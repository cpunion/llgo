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

func TestSSAExactScalarBitcastProofAndNoUnwindPlan(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "scalar_bitcast.go", `package coroid
import "unsafe"

var escaped unsafe.Pointer

func ToFloat32(value int32) float32 { return *(*float32)(unsafe.Pointer(&value)) }
func FromFloat32(value float32) int32 { return *(*int32)(unsafe.Pointer(&value)) }
func ToFloat64(value int64) float64 { return *(*float64)(unsafe.Pointer(&value)) }
func FromFloat64(value float64) int64 { return *(*int64)(unsafe.Pointer(&value)) }

func escape(value int32) float32 {
	escaped = unsafe.Pointer(&value)
	return *(*float32)(unsafe.Pointer(&value))
}
func arithmetic(value int32) float32 {
	result := *(*float32)(unsafe.Pointer(&value))
	return result + 1
}
func wrongWidth(value int32) float64 { return *(*float64)(unsafe.Pointer(&value)) }

func Root(a int32, b float32, c int64, d float64) {
	_, _, _, _ = ToFloat32(a), FromFloat32(b), ToFloat64(c), FromFloat64(d)
}
`)
	exactNames := []string{"ToFloat32", "FromFloat32", "ToFloat64", "FromFloat64"}
	for _, name := range exactNames {
		proof, ok := ProveSSAExactScalarBitcast(packageFunction(t, pkg, name))
		if !ok || proof.Allocation == nil || !proof.Allocation.Heap {
			t.Fatalf("%s exact bitcast proof = %+v, present=%t; want one conservatively Heap-marked slot", name, proof, ok)
		}
	}
	for _, name := range []string{"escape", "arithmetic", "wrongWidth"} {
		if proof, ok := ProveSSAExactScalarBitcast(packageFunction(t, pkg, name)); ok {
			t.Fatalf("%s unexpectedly received exact scalar-bitcast proof: %+v", name, proof)
		}
	}

	plan, err := AnalyzeSSA(prog, Roots{{Function: packageFunction(t, pkg, "Root"), Demand: AsyncDemand}}, SSAConfig{
		MaxPlainInstructions: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range exactNames {
		function := packageFunction(t, pkg, name)
		got := functionPlanFor(t, plan, function)
		if got.External != Defined || got.Demand == NoDemand || got.Emission != EmitPlain ||
			got.Primary != PrimaryPlain || got.FuncRep != DirectPlain || got.Effect != NoSuspend || got.Exec != 0 {
			t.Fatalf("%s plan = %+v, want demanded Defined DirectPlain NoSuspend/NoUnwind", name, got)
		}
	}
}
