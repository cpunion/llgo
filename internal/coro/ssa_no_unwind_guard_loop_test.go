//go:build !llgo

package coro

import "testing"

func TestSSAExactNoUnwindFieldGuardAcrossLoopIterations(t *testing.T) {
	_, pkg := buildCoroTestSSA(t, "field_guard_loop.go", `package coroid
type Box struct { Value *int }
func readonlyLoop(v *Box, n int) int {
 if v == nil || v.Value == nil { return 0 }
 result := 0
 for i := 0; i < n; i++ { result += *v.Value }
 return result
}
func loopStoreAfterUse(v *Box, n int) int {
 if v == nil || v.Value == nil { return 0 }
 result := 0
 for i := 0; i < n; i++ { result += *v.Value; v.Value = nil }
 return result
}
func loopConditionalStoreAfterUse(v *Box, n int) int {
 if v == nil || v.Value == nil { return 0 }
 result := 0
 for i := 0; i < n; i++ {
  result += *v.Value
  if i & 1 == 0 { v.Value = nil }
 }
 return result
}
func recheckedLoopGuard(v *Box, n int) int {
 result := 0
 for v != nil && v.Value != nil && n > 0 {
  result += *v.Value; v.Value = nil; n--
 }
 return result
}
func terminalStoreAfterUse(v *Box) int {
 if v == nil || v.Value == nil { return 0 }
 result := *v.Value
 v.Value = nil
 return result
}
func savedGuardStoreBeforeBranch(v *Box) int {
 if v == nil { return 0 }
 nonNil := v.Value != nil
 v.Value = nil
 if nonNil { return *v.Value }
 return 0
}
func savedGuardStoreInEarlierBlock(v *Box, clear bool) int {
 if v == nil { return 0 }
 nonNil := v.Value != nil
 if clear { v.Value = nil }
 if nonNil { return *v.Value }
 return 0
}
func readonlySavedGuard(v *Box) int {
 if v == nil { return 0 }
 nonNil := v.Value != nil
 if nonNil { return *v.Value }
 return 0
}
func sameBlockStoreBetweenUses(v *Box) int {
 if v == nil || v.Value == nil { return 0 }
 result := *v.Value
 v.Value = nil
 return result + *v.Value
}
`)
	for _, tc := range []struct {
		name string
		safe bool
	}{
		{"readonlyLoop", true},
		{"loopStoreAfterUse", false},
		{"loopConditionalStoreAfterUse", false},
		{"recheckedLoopGuard", true},
		{"terminalStoreAfterUse", true},
		{"sameBlockStoreBetweenUses", false},
		{"savedGuardStoreBeforeBranch", false},
		{"savedGuardStoreInEarlierBlock", false},
		{"readonlySavedGuard", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fn := packageFunction(t, pkg, tc.name)
			analysis := newSSAExactNoUnwindAnalysis(fn, nil, nil, nil, nil, nil, nil, nil)
			got, err := analysis.scan()
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.safe {
				t.Fatalf("no-unwind certificate = %t, want %t", got, tc.safe)
			}
		})
	}
}
