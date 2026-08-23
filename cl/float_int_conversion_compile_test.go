//go:build !llgo

package cl

import (
	"strings"
	"testing"

	llssa "github.com/xgo-dev/llgo/ssa"
)

func TestFloatToIntegerConversionMode(t *testing.T) {
	const src = `package floatconvert
func Signed(x float32) int32 { return int32(x) }
func Unsigned(x float32) uint32 { return uint32(x) }
`
	tests := []struct {
		name         string
		saturating   bool
		signedWant   string
		unsignedWant string
	}{
		{name: "legacy", signedWant: "i32 -2147483648", unsignedWant: "fptosi float"},
		{name: "saturating", saturating: true, signedWant: "i32 2147483647", unsignedWant: "fptoui float"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ssaPkg, _, files := buildGoSSAPkg(t, src)
			prog := newLLSSAProgForTarget(t, &llssa.Target{
				GOOS:                 "linux",
				GOARCH:               "amd64",
				SaturatingFloatToInt: tt.saturating,
			})
			pkg, err := NewPackage(prog, ssaPkg, files)
			if err != nil {
				t.Fatal(err)
			}
			signedIR := mustNamedFunction(t, pkg.Module(), "floatconvert.Signed").String()
			if !strings.Contains(signedIR, tt.signedWant) {
				t.Fatalf("signed conversion IR does not use %s mode:\n%s", tt.name, signedIR)
			}
			unsignedIR := mustNamedFunction(t, pkg.Module(), "floatconvert.Unsigned").String()
			if !strings.Contains(unsignedIR, tt.unsignedWant) {
				t.Fatalf("unsigned conversion IR does not use %s mode:\n%s", tt.name, unsignedIR)
			}
		})
	}
}
