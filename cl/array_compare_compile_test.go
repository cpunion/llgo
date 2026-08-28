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
	"go/token"
	"go/types"
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
)

func TestArrayCompareReusesImmutableLocalStorage(t *testing.T) {
	const source = `
package foo

func immutable() bool {
	x := [32]byte{1}
	y := [32]byte{2}
	return x == y
}

func snapshot() bool {
	x := [32]byte{1}
	y := [32]byte{2}
	old := x
	x[0] = 3
	return old == y
}

func small() bool {
	x := [4]byte{1}
	y := [4]byte{2}
	return x == y
}

func parameters(x, y [32]byte) bool {
	return x == y
}

func hugeParameters(x, y [65537]byte) bool {
	return x == y
}

func budgetParameters(x, y [40000]byte) bool {
	return x == y
}

type hugeBox struct {
	values [65537]byte
}

func hugeStructParameters(x, y hugeBox) bool {
	return x == y
}

func scalar(p *int) int {
	return *p
}

func crossBlock(change bool) bool {
	x := [32]byte{1}
	y := [32]byte{2}
	if change {
		x[0] = 3
	}
	return x == y
}

type item struct {
	value byte
}

func structs() bool {
	x := [32]item{{value: 1}}
	y := [32]item{{value: 2}}
	return x == y
}

type textItem struct {
	value string
}

func textItems() bool {
	x := [32]textItem{{value: "left"}}
	y := [32]textItem{{value: "right"}}
	return x == y
}

var escaped *[32]byte

func heap() bool {
	x := [32]byte{1}
	y := [32]byte{2}
	escaped = &x
	return x == y
}
`
	ssaPkg, _, _ := buildGoSSAPkg(t, source)
	findCompare := func(name string) *ssa.BinOp {
		t.Helper()
		for _, block := range ssaPkg.Func(name).Blocks {
			for _, instr := range block.Instrs {
				if bin, ok := instr.(*ssa.BinOp); ok && bin.Op == token.EQL {
					switch bin.X.Type().Underlying().(type) {
					case *types.Array, *types.Struct:
						return bin
					}
				}
			}
		}
		t.Fatalf("array comparison not found in %s", name)
		return nil
	}
	immutable := findCompare("immutable")
	if _, ok := immutableLocalArrayLoadAddr(immutable.X); !ok {
		t.Fatal("immutable left array was not recognized")
	}
	if _, ok := immutableLocalArrayLoadAddr(immutable.Y); !ok {
		t.Fatal("immutable right array was not recognized")
	}
	if !canElideArrayCompareLoad(immutable.X.(*ssa.UnOp)) || !canElideArrayCompareLoad(immutable.Y.(*ssa.UnOp)) {
		t.Fatal("immutable comparison loads were not eligible for elision")
	}
	snapshot := findCompare("snapshot")
	if _, ok := immutableLocalArrayLoadAddr(snapshot.X); ok {
		t.Fatal("array changed after its load was treated as immutable")
	}
	if _, ok := immutableLocalArrayLoadAddr(snapshot.Y); !ok {
		t.Fatal("unchanged snapshot operand was not recognized")
	}
	if canElideArrayCompareLoad(snapshot.X.(*ssa.UnOp)) {
		t.Fatal("mutable snapshot operand was eligible for load elision")
	}
	if !canElideArrayCompareLoad(snapshot.Y.(*ssa.UnOp)) {
		t.Fatal("independently stable operand was not eligible for load elision")
	}
	parameters := findCompare("parameters")
	if _, ok := immutableLocalArrayLoadAddr(parameters.X); ok {
		t.Fatal("array parameter was treated as reusable local storage")
	}
	if coroAggregateEqualityNeedsManagedScratch(nil, parameters) {
		t.Fatal("array comparison without a compilation context required managed scratch")
	}
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	ctx := &context{prog: prog}
	if coroAggregateEqualityNeedsManagedScratch(ctx, parameters) {
		t.Fatal("small array parameters required managed scratch")
	}
	if !coroAggregateEqualityNeedsManagedScratch(ctx, findCompare("hugeParameters")) {
		t.Fatal("oversized array parameters did not require managed scratch")
	}
	if !coroAggregateEqualityNeedsManagedScratch(ctx, findCompare("budgetParameters")) {
		t.Fatal("combined array snapshots exceeding the temporary budget did not require managed scratch")
	}
	if !coroAggregateEqualityNeedsManagedScratch(ctx, findCompare("hugeStructParameters")) {
		t.Fatal("oversized nested array parameters did not require managed scratch")
	}
	var scalarLoad *ssa.UnOp
	for _, block := range ssaPkg.Func("scalar").Blocks {
		for _, instr := range block.Instrs {
			if load, ok := instr.(*ssa.UnOp); ok && load.Op == token.MUL {
				scalarLoad = load
			}
		}
	}
	if scalarLoad == nil {
		t.Fatal("scalar load not found")
	}
	if _, ok := immutableLocalArrayLoadAddr(scalarLoad); ok {
		t.Fatal("scalar load was treated as reusable array storage")
	}
	if canElideArrayCompareLoad(scalarLoad) || canElideArrayCompareLoad(nil) {
		t.Fatal("non-array load was eligible for array comparison load elision")
	}
	small := findCompare("small")
	if canElideArrayCompareLoad(small.X.(*ssa.UnOp)) {
		t.Fatal("inline array comparison load was eligible for elision")
	}
	immutableLoad := immutable.X.(*ssa.UnOp)
	loadRefs := immutableLoad.Referrers()
	originalLoadRefs := append([]ssa.Instruction(nil), (*loadRefs)...)
	*loadRefs = nil
	if canElideArrayCompareLoad(immutableLoad) {
		t.Fatal("array load without executable comparisons was eligible for elision")
	}
	*loadRefs = []ssa.Instruction{&ssa.BinOp{Op: token.EQL}}
	if canElideArrayCompareLoad(immutableLoad) {
		t.Fatal("unrelated comparison was treated as a use of the array load")
	}
	*loadRefs = originalLoadRefs
	rejectRef := func(name string, ref ssa.Instruction) {
		t.Helper()
		refs := immutableLoad.X.Referrers()
		original := *refs
		defer func() { *refs = original }()
		*refs = append(*refs, ref)
		if _, ok := immutableLocalArrayLoadAddr(immutable.X); ok {
			t.Fatalf("array with %s was treated as immutable", name)
		}
	}
	rejectRef("an unknown address use", &ssa.BinOp{})
	rejectRef("a foreign field address", &ssa.FieldAddr{})
	rejectRef("a non-load unary use", &ssa.UnOp{})
	noReferrers := ssa.NewConst(nil, types.NewPointer(types.Typ[types.Int]))
	if immutableArrayAddrUses(noReferrers, immutableLoad, make(map[ssa.Value]bool)) {
		t.Fatal("address without referrer metadata was treated as immutable")
	}
	if !immutableArrayAddrUses(noReferrers, immutableLoad, map[ssa.Value]bool{noReferrers: true}) {
		t.Fatal("previously visited address did not terminate the use walk")
	}
	crossBlock := findCompare("crossBlock")
	if _, ok := immutableLocalArrayLoadAddr(crossBlock.X); ok {
		t.Fatal("array written from another block was treated as immutable")
	}
	if _, ok := immutableLocalArrayLoadAddr(crossBlock.Y); !ok {
		t.Fatal("unchanged cross-block operand was not recognized")
	}
	structs := findCompare("structs")
	if _, ok := immutableLocalArrayLoadAddr(structs.X); !ok {
		t.Fatal("immutable struct array with field stores was not recognized")
	}
	if _, ok := immutableLocalArrayLoadAddr(structs.Y); !ok {
		t.Fatal("second immutable struct array with field stores was not recognized")
	}
	textItems := findCompare("textItems")
	if _, ok := immutableLocalArrayLoadAddr(textItems.X); !ok {
		t.Fatal("immutable non-regular array was not recognized")
	}
	if _, ok := immutableLocalArrayLoadAddr(textItems.Y); !ok {
		t.Fatal("second immutable non-regular array was not recognized")
	}
	heap := findCompare("heap")
	if _, ok := immutableLocalArrayLoadAddr(heap.X); ok {
		t.Fatal("escaping heap array was treated as reusable local storage")
	}

	_, mod := mustCompileLLPkgFromSrc(t, source)
	immutableIR := mustNamedFunction(t, mod, "foo.immutable").String()
	if got := strings.Count(immutableIR, "alloca [32 x i8]"); got != 2 {
		t.Fatalf("immutable comparison has %d array allocations, want only its two source values:\n%s", got, immutableIR)
	}
	if !strings.Contains(immutableIR, ".memequal") || strings.Contains(immutableIR, "store [32 x i8]") || strings.Contains(immutableIR, "stacksave") {
		t.Fatalf("immutable comparison copied an aggregate value:\n%s", immutableIR)
	}
	snapshotIR := mustNamedFunction(t, mod, "foo.snapshot").String()
	if !strings.Contains(snapshotIR, ".memequal") || !strings.Contains(snapshotIR, "store [32 x i8]") || !strings.Contains(snapshotIR, "stacksave") {
		t.Fatalf("mutable source did not preserve the loaded array snapshot:\n%s", snapshotIR)
	}
	if got := strings.Count(snapshotIR, "load [32 x i8]"); got != 2 {
		// One load initializes x from its composite literal and one snapshots x
		// before the mutation; the independently stable y needs no load.
		t.Fatalf("mixed-stability comparison has %d aggregate loads, want only literal initialization and mutable snapshot:\n%s", got, snapshotIR)
	}
	if got := strings.Count(snapshotIR, "call void @llvm.memset"); got != 3 {
		t.Fatalf("mixed-stability comparison has %d zeroing calls, want only its three source allocations:\n%s", got, snapshotIR)
	}
	hugeIR := mustNamedFunction(t, mod, "foo.hugeParameters").String()
	if got := strings.Count(hugeIR, "runtime.AllocZ"); got != 2 ||
		strings.Contains(hugeIR, "alloca [65537 x i8]") ||
		strings.Contains(hugeIR, "llvm.stacksave") || strings.Contains(hugeIR, "llvm.memset") {
		t.Fatalf("oversized array comparison did not use exactly two planned managed snapshots:\n%s", hugeIR)
	}
	budgetIR := mustNamedFunction(t, mod, "foo.budgetParameters").String()
	if got := strings.Count(budgetIR, "runtime.AllocZ"); got != 1 ||
		strings.Count(budgetIR, "alloca [40000 x i8]") != 1 ||
		!strings.Contains(budgetIR, "llvm.stacksave") || strings.Contains(budgetIR, "llvm.memset") {
		t.Fatalf("combined temporary budget did not pack one snapshot on stack and one in managed storage:\n%s", budgetIR)
	}
	hugeStructIR := mustNamedFunction(t, mod, "foo.hugeStructParameters").String()
	if got := strings.Count(hugeStructIR, "runtime.AllocZ"); got != 2 ||
		strings.Contains(hugeStructIR, "alloca [65537 x i8]") ||
		strings.Contains(hugeStructIR, "llvm.stacksave") || strings.Contains(hugeStructIR, "llvm.memset") {
		t.Fatalf("oversized nested-array comparison did not use exactly two planned managed snapshots:\n%s", hugeStructIR)
	}
	if strings.Contains(immutableIR, "load [32 x i8]") {
		t.Fatalf("immutable comparison retained unused aggregate loads:\n%s", immutableIR)
	}
	textItemsIR := mustNamedFunction(t, mod, "foo.textItems").String()
	if got := strings.Count(textItemsIR, "alloca [32 x"); got != 2 {
		t.Fatalf("non-regular comparison has %d array allocations, want only its two source values:\n%s", got, textItemsIR)
	}
	if !strings.Contains(textItemsIR, "phi i64") || strings.Count(textItemsIR, ".StringEqual") != 1 ||
		strings.Contains(textItemsIR, "store [32 x") || strings.Contains(textItemsIR, ".arrayequalImpl") ||
		strings.Contains(textItemsIR, "stacksave") {
		t.Fatalf("non-regular comparison was not one address-reusing element loop:\n%s", textItemsIR)
	}
	smallIR := mustNamedFunction(t, mod, "foo.small").String()
	if strings.Contains(smallIR, ".memequal") || strings.Contains(smallIR, "stacksave") {
		t.Fatalf("small array comparison used the runtime path:\n%s", smallIR)
	}
}
