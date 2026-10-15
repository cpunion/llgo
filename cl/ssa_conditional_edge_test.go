package cl

import (
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
)

func TestSSAConditionalEdgeFactsRejectBypassedChecks(t *testing.T) {
	for _, test := range []struct {
		name, guard string
		want        bool
	}{
		{"short-circuit-bypass", "if flag && BAD { return ZERO };", false},
		{"both-edges-merge", "if BAD { sink = 1 };", false},
		{"checked", "if BAD { return ZERO };", true},
		{"checked-before-merge", "if BAD { return ZERO }; if flag { sink = 1 } else { sink = 2 };", true},
		{"checked-before-loop", "if BAD { return ZERO }; for flag { sink = 1; flag = false };", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, kind := range []string{"pointer", "function", "nonzero", "nonnegative", "slice"} {
				t.Run(kind, func(t *testing.T) {
					bad, zero, params, operation := "p == nil", "0", "p *int, flag bool", "return *p"
					switch kind {
					case "function":
						params, operation = "p func() int, flag bool", "return p()"
					case "nonzero":
						bad, params, operation = "p == 0", "p int, flag bool", "return 42 / p"
					case "nonnegative":
						bad, params, operation = "p < 0", "p int, flag bool", "return 1 << p"
					case "slice":
						bad, params, operation = "len(p) == 0", "p []int, flag bool", "return p[0]"
					}
					guard := strings.NewReplacer("BAD", bad, "ZERO", zero).Replace(test.guard)
					pkg, _, _ := buildGoSSAPkg(t, "package foo\nvar sink int\nfunc Root("+params+") int { "+guard+operation+" }")
					fn := pkg.Func("Root")
					builder := &coroFrameRetentionRootBuilder{audit: &coroPhysicalPureSSAAudit{fn: fn}}
					var got bool
					switch kind {
					case "pointer":
						use := onlySSADeref(t, fn)
						got = ssaValueProvenNonNilAt(fn.Params[0], use)
						if test.name == "checked-before-loop" {
							proof, ok := proveSSADominatingNonNil(fn.Params[0], use)
							if !ok || len(proof.Successor.Preds) < 2 {
								t.Fatal("fixture did not exercise the shared-successor edge proof")
							}
						}
						_, retained := builder.dominatingNonNilEvidence(fn.Params[0], use)
						if retained != test.want {
							t.Errorf("retained non-nil fact = %t, want %t", retained, test.want)
						}
					case "function":
						var use *ssa.Call
						for _, block := range fn.Blocks {
							for _, instruction := range block.Instrs {
								if call, ok := instruction.(*ssa.Call); ok {
									use = call
								}
							}
						}
						if use == nil {
							t.Fatal("missing function call")
						}
						got = ssaFunctionValueProvenNonNilAt(fn.Params[0], use)
					case "nonzero":
						use := onlySSAIntegerDivision(t, fn)
						got = ssaIntegerValueProvenNonZeroAt(use.Y, use)
					case "nonnegative":
						use := onlySSAIntegerShift(t, fn)
						got = ssaIntegerValueProvenNonNegativeAt(use.Y, use)
					case "slice":
						use := onlySSAConditionalEdgeIndex(t, fn)
						_, got = builder.dominatingNonEmptySliceEvidence(fn.Params[0], use)
					}
					if got != test.want {
						t.Errorf("conditional fact = %t, want %t", got, test.want)
					}
				})
			}
		})
	}
}

func TestSSAConditionalEdgeFaultGuardsAgreeWithEmission(t *testing.T) {
	pkg, _, files := buildGoSSAPkg(t, `package foo
var sink int
func PointerBypass(p *int, flag bool) int { if flag && p == nil { return 0 }; return *p }
func PointerMerge(p *int) int { if p == nil { sink = 1 }; return *p }
func PointerChecked(p *int) int { if p == nil { return 0 }; return *p }
func DivisorBypass(p int, flag bool) int { if flag && p == 0 { return 0 }; return 42 / p }
func DivisorMerge(p int) int { if p == 0 { sink = 1 }; return 42 / p }
func DivisorChecked(p int) int { if p == 0 { return 0 }; return 42 / p }
func ShiftBypass(p int, flag bool) int { if flag && p < 0 { return 0 }; return 1 << p }
func ShiftMerge(p int) int { if p < 0 { sink = 1 }; return 1 << p }
func ShiftChecked(p int) int { if p < 0 { return 0 }; return 1 << p }
`)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	compiled, err := NewPackage(prog, pkg, files)
	if err != nil {
		t.Fatal(err)
	}
	universe := &EmissionUniverse{prog: prog}
	for _, kind := range []string{"Pointer", "Divisor", "Shift"} {
		for _, suffix := range []string{"Bypass", "Merge", "Checked"} {
			name := kind + suffix
			t.Run(name, func(t *testing.T) {
				fn := pkg.Func(name)
				ctx := &context{prog: prog, goFn: fn, goProg: pkg.Prog, goTyps: pkg.Pkg, goPkg: pkg}
				var use ssa.Instruction
				var helper string
				switch kind {
				case "Pointer":
					use, helper = onlySSADeref(t, fn), "AssertNilDeref"
				case "Divisor":
					use, helper = onlySSAIntegerDivision(t, fn), "AssertDivideByZero"
				case "Shift":
					use, helper = onlySSAIntegerShift(t, fn), "AssertNegativeShift"
				}
				planned := false
				for _, candidate := range universe.loweredRuntimeHelpers(ctx, use) {
					planned = planned || strings.HasPrefix(candidate, helper)
				}
				physical := compiled.Module().NamedFunction("foo." + name).String()
				want := suffix != "Checked"
				if planned != want || strings.Contains(physical, helper) != want {
					t.Fatalf("fault guard %s planned=%t want=%t; IR:\n%s", helper, planned, want, physical)
				}
			})
		}
	}
}

func TestSSAConditionalLoopEdgeFactsAreExact(t *testing.T) {
	for _, test := range []struct {
		name, condition string
		want            bool
	}{
		{"guarded", "i < len(p)", true},
		// The existing induction recognizer intentionally requires its phi
		// and comparison in one header; a split short-circuit header is not
		// promoted to a new induction proof by the edge query.
		{"noncanonical-split-header", "flag && i < len(p)", false},
		{"bypassed-upper-bound", "flag || i < len(p)", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			pkg, _, _ := buildGoSSAPkg(t, `package foo
func Root(p []int, flag bool) int {
total := 0
for i := 0; `+test.condition+`; i++ { total += p[i] + 42 / len(p) }
return total
}`)
			fn := pkg.Func("Root")
			index := onlySSAConditionalEdgeIndex(t, fn)
			builder := &coroFrameRetentionRootBuilder{audit: &coroPhysicalPureSSAAudit{fn: fn}}
			_, got := builder.dominatingSliceIndexEvidence(fn.Params[0], index.Index, index)
			if got != test.want {
				t.Errorf("slice loop bound = %t, want %t", got, test.want)
			}
			division := onlySSAIntegerDivision(t, fn)
			if got := ssaIntegerValueProvenNonZeroAt(division.Y, division); got != test.want {
				t.Errorf("loop positive length = %t, want %t", got, test.want)
			}
		})
	}
}

func onlySSAConditionalEdgeIndex(t *testing.T, fn *ssa.Function) *ssa.IndexAddr {
	t.Helper()
	var found *ssa.IndexAddr
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			if index, ok := instruction.(*ssa.IndexAddr); ok {
				if found != nil {
					t.Fatal("multiple index addresses")
				}
				found = index
			}
		}
	}
	if found == nil {
		t.Fatal("missing index address")
	}
	return found
}
