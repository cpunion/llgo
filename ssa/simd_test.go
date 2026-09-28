//go:build !llgo

package ssa

import (
	"strings"
	"testing"
)

func TestSIMDFeaturesPreserveExistingRequirements(t *testing.T) {
	prog := NewProgram(&Target{GOOS: "wasip1", GOARCH: "wasm"})
	defer prog.Dispose()
	pkg := prog.NewPackage("p", "p")
	fn := pkg.NewFunc("p.f", NoArgsNoRet, InGo)
	fn.impl.AddFunctionAttr(prog.ctx.CreateStringAttribute("target-features", "+bulk-memory"))
	b := fn.MakeBody(1)
	b.simdFeatures(SIMDAdd)
	b.simdFeatures(SIMDSub)
	b.Return()
	b.EndBuild()
	for _, attr := range fn.impl.GetFunctionAttributes() {
		if attr.IsString() && attr.GetStringKind() == "target-features" {
			got := attr.GetStringValue()
			if !strings.Contains(got, "+bulk-memory") || strings.Count(got, "+simd128") != 1 {
				t.Fatalf("target features = %q", got)
			}
			return
		}
	}
	t.Fatal("missing target features")
}
