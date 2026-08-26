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
`
	ssaPkg, _, _ := buildGoSSAPkg(t, source)
	findCompare := func(name string) *ssa.BinOp {
		t.Helper()
		for _, block := range ssaPkg.Func(name).Blocks {
			for _, instr := range block.Instrs {
				if bin, ok := instr.(*ssa.BinOp); ok && bin.Op == token.EQL {
					return bin
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
	snapshot := findCompare("snapshot")
	if _, ok := immutableLocalArrayLoadAddr(snapshot.X); ok {
		t.Fatal("array changed after its load was treated as immutable")
	}
	if _, ok := immutableLocalArrayLoadAddr(snapshot.Y); !ok {
		t.Fatal("unchanged snapshot operand was not recognized")
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
}

func TestImmutableLocalArrayLoadAddrRejectsUnsupportedSSA(t *testing.T) {
	const source = `
package foo

func compare() bool {
	x := [32]byte{1}
	y := [32]byte{2}
	return x == y
}

func scalar(p *int) int { return *p }
`
	ssaPkg, _, _ := buildGoSSAPkg(t, source)
	findLoad := func(t *testing.T, name string, accept func(*ssa.UnOp) bool) *ssa.UnOp {
		t.Helper()
		for _, block := range ssaPkg.Func(name).Blocks {
			for _, instr := range block.Instrs {
				if load, ok := instr.(*ssa.UnOp); ok && load.Op == token.MUL && accept(load) {
					return load
				}
			}
		}
		t.Fatalf("matching load not found in %s", name)
		return nil
	}

	scalarLoad := findLoad(t, "scalar", func(*ssa.UnOp) bool { return true })
	if _, ok := immutableLocalArrayLoadAddr(scalarLoad); ok {
		t.Fatal("scalar load was treated as an immutable array")
	}

	arrayLoad := findLoad(t, "compare", func(load *ssa.UnOp) bool {
		_, ok := load.Type().Underlying().(*types.Array)
		return ok
	})
	alloc, ok := arrayLoad.X.(*ssa.Alloc)
	if !ok {
		t.Fatalf("array load base is %T, want *ssa.Alloc", arrayLoad.X)
	}
	refs := alloc.Referrers()
	if refs == nil {
		t.Fatal("array allocation does not track referrers")
	}
	originalRefs := append([]ssa.Instruction(nil), (*refs)...)
	defer func() { *refs = originalRefs }()

	t.Run("foreign field base", func(t *testing.T) {
		*refs = []ssa.Instruction{&ssa.FieldAddr{X: new(ssa.Alloc)}}
		if _, ok := immutableLocalArrayLoadAddr(arrayLoad); ok {
			t.Fatal("field address from another base was accepted")
		}
	})

	t.Run("invalid dereference", func(t *testing.T) {
		*refs = []ssa.Instruction{&ssa.UnOp{Op: token.NOT, X: alloc}}
		if _, ok := immutableLocalArrayLoadAddr(arrayLoad); ok {
			t.Fatal("non-dereference unary use was accepted")
		}
	})

	t.Run("unsupported instruction", func(t *testing.T) {
		*refs = []ssa.Instruction{new(ssa.Call)}
		if _, ok := immutableLocalArrayLoadAddr(arrayLoad); ok {
			t.Fatal("unsupported allocation use was accepted")
		}
	})

	if instructionPrecedes(new(ssa.Store), arrayLoad) {
		t.Fatal("instruction without a block was ordered before an array load")
	}
	block := arrayLoad.Block()
	if block == nil || alloc.Block() != block {
		t.Fatal("array allocation and load are not in the same block")
	}
	func() {
		instructions := block.Instrs
		block.Instrs = nil
		defer func() { block.Instrs = instructions }()
		if instructionPrecedes(alloc, arrayLoad) {
			t.Fatal("instructions absent from their block were ordered")
		}
	}()
}
