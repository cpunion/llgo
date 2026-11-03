//go:build !llgo

package cl

import (
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
)

func TestStaticMapInitIntervals(t *testing.T) {
	for _, tc := range []struct {
		name, source         string
		want, updates, loads int
	}{
		{"constants", `var M = map[string]int{"a": 1, "b": 2}`, 1, 2, 0},
		{"global-value", `var Value int; var M = map[string]int{"a": Value}`, 1, 1, 1},
		{"unicode-shape", `type Table struct{ N int }; var Range *Table; var M = map[string]*Table{"Lu": Range}`, 1, 1, 1},
		{"aliases", `type K string; type V string; var M = map[K]V{"alias": "canonical"}`, 1, 1, 0},
		{"typed-nil", `var M = map[string]*int{"a": nil}`, 1, 1, 0},
		{"separate-maps", `var M = map[string]int{"a": 1}; var N = map[string]int{"b": 2}`, 2, 2, 0},
		// Local purity does not prove the initialized cell is unobserved.
		{"read-global-still-only-local-proof", `var M = map[string]int{"a": 1}; var Observed = M["a"]`, 1, 1, 0},
		{"call-key", `func key() string { panic("key") }; var M = map[string]int{key(): 1}`, 0, 0, 0},
		{"call-value", `func value() int { panic("value") }; var M = map[string]int{"a": value()}`, 0, 0, 0},
		{"unhashable-interface-key", `var M = map[any]int{[]int{1}: 1}`, 0, 0, 0},
		{"indirect-load-can-panic", `var P *int; var M = map[string]int{"a": *P}`, 0, 0, 0},
		{"receive-value", `var C = make(chan int); var M = map[string]int{"a": <-C}`, 0, 0, 0},
		{"allocated-value", `var M = map[string]*int{"a": new(int)}`, 0, 0, 0},
		{"factory", `func makeMap() map[string]int { return map[string]int{"a": 1} }; var M = makeMap()`, 0, 0, 0},
		{"empty", `var M = map[string]int{}`, 0, 0, 0},
		{"non-string-key", `var M = map[int]int{1: 1}`, 0, 0, 0},
		{"user-init-not-synthetic", `var M map[string]int; func init() { M = map[string]int{"a": 1} }`, 0, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, mode := range []ssa.BuilderMode{ssa.SanityCheckFunctions, ssa.SanityCheckFunctions | ssa.GlobalDebug} {
				pkg, _ := buildSSAPackageWithPathAndFilesMode(t, "example.com/mapinit", "mapinit", "package mapinit\n"+tc.source, mode)
				fn := pkg.Func("init")
				var before strings.Builder
				fn.WriteTo(&before)
				intervals := collectStaticMapInitIntervals(fn)
				if len(intervals) != tc.want {
					t.Fatalf("mode %v: got %d intervals, want %d\n%s", mode, len(intervals), tc.want, before.String())
				}
				updates, loads := 0, 0
				for _, interval := range intervals {
					updates += len(interval.updates)
					loads += len(interval.globalLoads)
					if interval.block.Instrs[interval.start] != interval.allocation || interval.block.Instrs[interval.end] != interval.store {
						t.Fatal("interval endpoints do not name the exact allocation and store")
					}
				}
				if updates != tc.updates || loads != tc.loads {
					t.Fatalf("mode %v: updates/loads = %d/%d, want %d/%d", mode, updates, loads, tc.updates, tc.loads)
				}
				var after strings.Builder
				fn.WriteTo(&after)
				if before.String() != after.String() {
					t.Fatal("recognizer changed the SSA")
				}
				if fn := pkg.Func("makeMap"); fn != nil && len(collectStaticMapInitIntervals(fn)) != 0 {
					t.Fatal("ordinary function accepted as a synthetic initializer")
				}
			}
		})
	}
	if len(collectStaticMapInitIntervals(nil)) != 0 {
		t.Fatal("nil function accepted")
	}
}
