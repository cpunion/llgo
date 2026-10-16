//go:build !llgo

package cl

import (
	"strings"
	"testing"

	llssa "github.com/xgo-dev/llgo/ssa"
)

func TestExplicitPointerMethodReceiverUsesExistingNilGuard(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, target := range []struct {
		name   string
		target *llssa.Target
	}{
		{"native", nil},
		{"wasm32", &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(target.name, func(t *testing.T) {
			pkg, _, files := buildGoSSAPkg(t, `package foo
type T struct { x int }
var Touched int
func (p *T) Nop() { Touched++ }
func Explicit(p *T) { (*p).Nop() }
func Bound(p *T) func() { return (*p).Nop }
func Implicit(p *T) { p.Nop() }
`)
			prog := newLLSSAProgForTarget(t, target.target)
			defer prog.Dispose()
			compiled, err := NewPackage(prog, pkg, files)
			if err != nil {
				t.Fatal(err)
			}
			ir := compiled.Module().String()
			for _, name := range []string{"Explicit", "Bound"} {
				body := llvmFunction(t, ir, "foo."+name)
				if !strings.Contains(body, "AssertNilDeref") {
					t.Fatalf("%s lost explicit receiver nil guard:\n%s", name, body)
				}
			}
			if body := llvmFunction(t, ir, "foo.Implicit"); strings.Contains(body, "AssertNilDeref") {
				t.Fatalf("implicit nil receiver acquired a guard:\n%s", body)
			}
		})
	}
}
