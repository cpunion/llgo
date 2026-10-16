//go:build !llgo

package cl

import (
	"testing"

	"golang.org/x/tools/go/ssa"
)

func TestCoroSyntheticSliceFilterDoesNotCopyOrdinaryUses(t *testing.T) {
	pkg, _, _ := buildGoSSAPkg(t, `package foo
var Sink *[8]int
func Root() {
	var local [8]int
	Sink = &local
	local[0] = 1
	local[1] = 2
}
`)
	var allocation *ssa.Alloc
	for _, block := range pkg.Func("Root").Blocks {
		for _, instruction := range block.Instrs {
			if value, ok := instruction.(*ssa.Alloc); ok && value.Comment == "local" {
				allocation = value
			}
		}
	}
	if allocation == nil || allocation.Referrers() == nil || len(*allocation.Referrers()) < 2 {
		t.Fatal("fixture lost its ordinary allocation with multiple executable users")
	}
	ctx := &context{}
	if allocations := testing.AllocsPerRun(100, func() {
		if ctx.skipSyntheticMakeSliceAlloc(allocation) {
			t.Fatal("ordinary allocation classified as synthetic make")
		}
	}); allocations != 0 {
		t.Fatalf("ordinary allocation filter copied its use list: %g allocations per query", allocations)
	}
	if ctx.skipSyntheticMakeSliceAlloc(nil) {
		t.Fatal("nil allocation classified as synthetic make")
	}
}
