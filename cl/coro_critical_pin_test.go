package cl

import (
	"strings"
	"testing"
)

func TestCoroCriticalPinPreservesRegionProof(t *testing.T) {
	preamble := strings.Replace(coroCriticalProofPreamble,
		"llgo.coroCriticalEnter\nfunc enter()", "llgo.coroCriticalPin\nfunc enter() int", 1)
	for _, test := range []struct {
		name, body, want string
	}{
		{"balanced-retry", `func Root(n uint32) { for n != 0 { enter(); if n == 1 { exit(); break }; cell = n; exit(); n-- } }`, ""},
		{"unbalanced-return", `func Root() { enter() }`, "function exit or panic is forbidden"},
		{"masked-loop", `func Root(n uint32) { enter(); for n != 0 { n-- }; exit() }`, "cyclic CFG path"},
		{"ordinary-call", `func helper() { cell = 1 }; func Root() { enter(); helper(); exit() }`, "ordinary or non-atomic call"},
	} {
		t.Run(test.name, func(t *testing.T) {
			proof, err := proveCoroCriticalFixture(t, preamble+test.body)
			if test.want == "" {
				if err != nil || proof == nil {
					t.Fatalf("balanced pin proof = %v, %v", proof, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("pin proof error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestCoroCriticalPinRejectsValueAndShapeMisuse(t *testing.T) {
	for _, test := range []struct {
		name, declaration, body, want string
	}{
		{"result-type", "func pin() uintptr", "pin()", "func() int"},
		{"argument", "func pin(int) int", "pin(1)", "func() int"},
		{"processor-identity", "func pin() int", "sink = pin()", "result must be unused"},
	} {
		t.Run(test.name, func(t *testing.T) {
			pkg, _, files := buildGoSSAPkg(t, `package foo
import _ "unsafe"
//go:linkname pin llgo.coroCriticalPin
`+test.declaration+`
var sink int
func Root() { `+test.body+` }
`)
			prog := newLLSSAProg(t)
			defer prog.Dispose()
			universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg, Files: files}})
			if err != nil {
				t.Fatal(err)
			}
			calls := allocaCStrTestCalls(pkg.Func("Root"))
			if len(calls) != 1 {
				t.Fatalf("calls = %d, want one", len(calls))
			}
			_, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(calls[0])
			if err == nil || !intrinsic || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("pin shape error = %v, intrinsic=%t, want %q", err, intrinsic, test.want)
			}
		})
	}
}

func TestCoroCriticalPinCannotBeMaterialized(t *testing.T) {
	pkg, _, files := buildGoSSAPkg(t, `package foo
import _ "unsafe"
//go:linkname pin llgo.coroCriticalPin
func pin() int
var marker = pin
func Root() { marker() }
`)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	_, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg, Files: files}})
	if err == nil || !strings.Contains(err.Error(), "cannot be materialized as a function value") {
		t.Fatalf("pin materialization error = %v", err)
	}
}
