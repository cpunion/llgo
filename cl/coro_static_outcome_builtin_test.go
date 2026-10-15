//go:build !llgo

package cl

import (
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

func TestCoroStaticOutcomeBuiltinSourceCapability(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want bool
	}{
		{"close", "close(ch)", false},
		{"len", "return len(ch)", true},
		{"cap", "return cap(ch)", true},
		{"copy", "return copy(dst, src)", true},
		{"delete", "delete(values, 0)", true},
		{"clear", "clear(values)", true},
		{"panic", "panic(1)", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := "package foo\nfunc F(ch chan int, dst, src []int, values map[int]int) int { " + test.body + "; return 0 }"
			pkg, _, _ := buildGoSSAPkg(t, source)
			found := false
			for _, block := range pkg.Func("F").Blocks {
				for _, instruction := range block.Instrs {
					call, ok := instruction.(*ssa.Call)
					if !ok {
						if _, panicInstruction := instruction.(*ssa.Panic); !panicInstruction || test.name != "panic" {
							continue
						}
					} else if builtin, ok := call.Common().Value.(*ssa.Builtin); !ok || builtin.Name() != test.name {
						continue
					}
					found = true
					plan, err := planCoroSemanticInstruction(instruction)
					if err != nil {
						t.Fatal(err)
					}
					if plan.staticOutcome != test.want || plan.effect != coro.NoSuspend {
						t.Fatalf("%s capability = %+v; want static=%t without a fabricated suspension effect", test.name, plan, test.want)
					}
					if test.name == "close" && (plan.class != coro.OpChannel || !plan.materialized) {
						t.Fatalf("close lost its task-bound channel operation: %+v", plan)
					}
				}
			}
			if !found {
				t.Fatal("fixture has no matching builtin instruction")
			}
		})
	}
}

func TestCoroStaticOutcomeOperationSourceCapability(t *testing.T) {
	for _, test := range []struct {
		name, body, recipe string
		want               bool
	}{
		{"make channel", "_ = make(chan int)", "cl.ssa.make-chan.v1", true},
		{"make map", "_ = make(map[int]int)", "cl.ssa.make-map.v1", true},
		{"map update", "values[0] = 1", "cl.ssa.map-update.v1", true},
		{"map lookup", "_ = values[0]", "cl.ssa.lookup.v1", true},
		{"send", "ch <- 1", "cl.ssa.channel-send.v0", false},
		{"receive", "_ = <-ch", "cl.ssa.channel-recv.v0", false},
		{"try select", "select { case ch <- 1: default: }", "cl.ssa.select.v0", false},
		{"park select", "select { case ch <- 1: case <-ch: }", "cl.ssa.select.v0", false},
		{"spawn", "go func() {}()", "cl.ssa.spawn.v0", false},
		{"defer", "defer func() {}()", "cl.ssa.defer.v0", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			pkg, _, _ := buildGoSSAPkg(t, "package foo\nfunc F(ch chan int, values map[int]int) { "+test.body+" }")
			found := false
			for _, block := range pkg.Func("F").Blocks {
				for _, instruction := range block.Instrs {
					plan, err := planCoroSemanticInstruction(instruction)
					if err != nil {
						t.Fatal(err)
					}
					if string(plan.recipe) != test.recipe {
						continue
					}
					found = true
					if plan.staticOutcome != test.want {
						t.Fatalf("source operation capability = %+v; want static=%t", plan, test.want)
					}
				}
			}
			if !found {
				t.Fatal("fixture has no matching source recipe")
			}
		})
	}
}
