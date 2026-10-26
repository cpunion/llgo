//go:build !llgo

// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package cl

import (
	"regexp"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	"github.com/xgo-dev/llgo/internal/goembed"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

func TestCoroInlineFaultActivationBelongsToDirectCaller(t *testing.T) {
	const source = `package foo
import "unsafe"

func Fault() bool { return *(*byte)(unsafe.Pointer(uintptr(1))) == 0 }
func Mid() bool { Sibling(); return Fault() }
func Root() bool { return Mid() }
func Sibling() {}
func Unrelated(value uint32) uint32 { return value + 1 }
`
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverse(
		prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}},
	)
	if err != nil {
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	fault, mid, root, sibling, unrelated := ssaPkg.Func("Fault"), ssaPkg.Func("Mid"), ssaPkg.Func("Root"), ssaPkg.Func("Sibling"), ssaPkg.Func("Unrelated")
	plan, err := coro.AnalyzeSSA(
		ssaPkg.Prog,
		coro.Roots{
			{Function: root, Demand: coro.AsyncDemand},
			{Function: unrelated, Demand: coro.AsyncDemand},
		},
		coro.SSAConfig{
			EmissionUniverse:     ssaUniverse,
			FunctionIDs:          universe.FunctionIDConfig(),
			MaxPlainInstructions: -1,
			ClassifyFunction: func(function *ssa.Function) (coro.SSAFunctionPolicy, error) {
				if function == fault || function == mid || function == root || function == sibling || function == unrelated {
					return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
				}
				return coro.SSAFunctionPolicy{}, nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	capabilities, err := compilation.CoroProgramCapabilities()
	if err != nil {
		t.Fatal(err)
	}
	if !capabilities.NativeDefaultFaultBoundary() || capabilities.DynamicPanicOnFault() {
		t.Fatalf("closed-program fault capabilities = %#x, want native default only", capabilities)
	}
	compilation.FinalCoroProgramCapabilities = capabilities
	compilation.FinalCoroProgramCapabilitiesFrozen = true
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatal(err)
	}
	module := pkg.Module()
	defer module.Dispose()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify function-local native-default fault module: %v\n%s", err, module.String())
	}
	for _, test := range []struct {
		name   string
		want   bool
		inline bool
	}{
		{name: "Fault", want: true},
		{name: "Mid", inline: true},
		{name: "Root"},
		{name: "Sibling"},
		{name: "Unrelated"},
	} {
		physical := module.NamedFunction("foo." + test.name + "$coro")
		if physical.IsNil() {
			t.Fatalf("function-local fault case=%s has no physical entry:\n%s", test.name, module.String())
		}
		if got := strings.Contains(physical.String(), coroPanicBoundaryPushHookV1); test.name != "Fault" && got != test.inline {
			t.Fatalf("%s inline child activation=%t want %t; local landing demand must remain independent:\n%s", test.name, got, test.inline, physical.String())
		}
		if test.name == "Mid" && strings.Count(physical.String(), "call void @"+coroPanicBoundaryPushHookV1) != 1 {
			t.Fatal("direct caller must protect exactly the faulting child")
		}
		match := regexp.MustCompile(`@(__llgo_coro_frame_descriptor_v1\.[0-9a-f]+)`).FindStringSubmatch(physical.String())
		if len(match) != 2 {
			// A transparent forwarding ramp may legally reuse its child's
			// physical frame. Its own code must still add no activation.
			if test.want || test.inline {
				t.Fatalf("case=%s has no frame descriptor: %s", test.name, physical.String())
			}
			continue
		}
		descriptor := module.NamedGlobal(match[1])
		flags := uint32(descriptor.Initializer().Operand(1).ZExtValue())
		if excluded := flags&coro.FrameDescriptorNoNativeFaultBoundaryV1 != 0; excluded == test.want {
			t.Fatalf("case=%s descriptor flags=%#x, boundary required=%t", test.name, flags, test.want)
		}
		for _, marker := range []string{
			coroPanicBoundaryTakeHookV1,
			coroPanicBoundaryTypeHookV1,
			coroPanicBoundaryDataReleaseHookV1,
		} {
			if got := strings.Contains(physical.String(), marker); got != test.want {
				t.Fatalf("function-local fault case=%s marker %q present=%t, want %t:\n%s", test.name, marker, got, test.want, physical.String())
			}
		}
	}
}
