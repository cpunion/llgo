// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package cl

import (
	"go/token"
	"go/types"
	"strings"
	"testing"

	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

func TestCoroLocalChannelAllocationWideExclusiveUses(t *testing.T) {
	for _, test := range []struct {
		name, source string
		want         bool
	}{
		{"balanced", `func Root() int { ch := make(chan int, 1); ch <- 3; return <-ch }`, true},
		{"direction alias", `type C chan int; func Root() int { ch := make(C, 1); send := (chan<- int)(ch); send <- 3; return <-ch }`, true},
		{"close and observe", `func Root() int { ch := make(chan int, 1); ch <- 3; close(ch); return <-ch + len(ch) + cap(ch) }`, true},
		{"parameter", `func Root(ch chan int) int { ch <- 3; return <-ch }`, false},
		{"zero capacity", `func Root() int { ch := make(chan int); ch <- 3; return <-ch }`, false},
		{"other capacity", `func Root() int { ch := make(chan int, 2); ch <- 3; return <-ch }`, false},
		{"dynamic capacity", `func Root(n int) int { ch := make(chan int, n); ch <- 3; return <-ch }`, false},
		{"pointer payload", `func Root() *int { ch := make(chan *int, 1); ch <- new(int); return <-ch }`, false},
		{"pass", `func sink(chan int); func Root() int { ch := make(chan int, 1); ch <- 3; sink(ch); return <-ch }`, false},
		{"return", `func Root() chan int { ch := make(chan int, 1); ch <- 3; return ch }`, false},
		{"global store", `var saved chan int; func Root() int { ch := make(chan int, 1); ch <- 3; saved = ch; return <-ch }`, false},
		{"interface", `func sink(any); func Root() int { ch := make(chan int, 1); ch <- 3; sink(ch); return <-ch }`, false},
		{"shadow builtin", `func len(chan int) int; func Root() int { ch := make(chan int, 1); ch <- 3; return <-ch + len(ch) }`, false},
		{"goroutine", `func Root() int { ch := make(chan int, 1); go func(){ ch <- 3 }(); return <-ch }`, false},
		{"closure", `func sink(func()); func Root() int { ch := make(chan int, 1); sink(func(){ ch <- 3 }); return <-ch }`, false},
		{"select", `func Root() int { ch := make(chan int, 1); select { case ch <- 3: default: }; return <-ch }`, false},
		{"defer", `func Root() int { ch := make(chan int, 1); defer close(ch); ch <- 3; return <-ch }`, false},
		{"phi", `func Root(other chan int, use bool) int { ch := make(chan int, 1); if use { ch = other }; ch <- 3; return <-ch }`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			pkg, _, _ := buildGoSSAPkg(t, "package foo\n"+test.source)
			fn := pkg.Func("Root")
			if got := len(coroLocalSingleSlotChannels(fn)) != 0; got != test.want {
				t.Fatalf("exclusive channel = %t, want %t", got, test.want)
			}
		})
	}
}

func TestCoroLocalChannelRejectsEscapesWithStaleReferrers(t *testing.T) {
	pkg, _, _ := buildGoSSAPkg(t, `package foo
func sink(chan int)
func Root() int {
	ch := make(chan int, 1)
	ch <- 3
	sink(nil)
	return <-ch
}`)
	fn := pkg.Func("Root")
	proofs := coroLocalSingleSlotChannels(fn)
	if len(proofs) == 0 {
		t.Fatal("initial local channel rejected")
	}
	var allocation *ssa.MakeChan
	for _, origin := range proofs {
		allocation = origin
		break
	}
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	physical := &coroPhysicalFunctionPlan{
		function:      fn,
		instructions:  make(map[ssa.Instruction]coroPhysicalInstructionPlan),
		localChannels: make(map[ssa.Instruction]*coroLocalChannelProof),
	}
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			if send, ok := instruction.(*ssa.Send); ok {
				physical.instructions[send] = coroPhysicalInstructionPlan{operation: coroPhysicalOperationChannelSend}
				physical.localChannels[send] = &coroLocalChannelProof{
					allocation: allocation, channel: send.Chan,
					elementSize: prog.PhysicalSizeOfGoType(types.Typ[types.Int]),
					fields:      planCoroLocalChannelLayout(prog),
				}
			}
		}
	}
	if err := physical.validateLocalChannels(); err != nil {
		t.Fatalf("initial physical proof: %v", err)
	}
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			if call, ok := instruction.(*ssa.Call); ok && call.Common().StaticCallee().Name() == "sink" {
				// Deliberately leave Referrers unchanged: operands are authoritative.
				call.Common().Args[0] = allocation
			}
		}
	}
	if len(coroLocalSingleSlotChannels(fn)) != 0 {
		t.Fatal("new escape accepted through stale Referrers")
	}
	if err := physical.validateLocalChannels(); err == nil {
		t.Fatal("physical proof accepted source mutation after planning")
	}
}

func TestCoroLocalChannelNativeAndWasm32(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	ssaPkg, _, _ := buildGoSSAPkg(t, `package foo
func Root() uint64 { ch := make(chan uint64, 1); ch <- 11; return <-ch }
`)
	root := ssaPkg.Func("Root")
	var origin *ssa.MakeChan
	for _, allocation := range coroLocalSingleSlotChannels(root) {
		origin = allocation
		break
	}
	if origin == nil {
		t.Fatal("local allocation proof missing")
	}
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog := newLLSSAProg(t)
			if test.target != nil {
				prog.Dispose()
				prog = newLLSSAProgForTarget(t, test.target)
			}
			defer prog.Dispose()
			pkg := prog.NewPackage("foo", "foo")
			module := pkg.Module()
			defer module.Dispose()
			fields := planCoroLocalChannelLayout(prog)
			if fields == nil {
				t.Fatal("production runtime channel layout missing")
			}
			physical := prog.RuntimeType("Chan").RawType().Underlying().(*types.Struct)
			for name, index := range fields {
				if physical.Field(index).Name() != name {
					t.Fatalf("source/physical channel field %q differs", name)
				}
			}
			pointer := types.NewPointer(types.Typ[types.Uint64])
			parameters := types.NewTuple(
				types.NewParam(token.NoPos, nil, "channel", origin.Type()),
				types.NewParam(token.NoPos, nil, "value", pointer),
			)
			results := types.NewTuple(types.NewParam(token.NoPos, nil, "status", types.Typ[types.Uint32]))
			for _, send := range []bool{false, true} {
				name := "recv"
				if send {
					name = "send"
				}
				function := pkg.NewFunc(name, types.NewSignatureType(nil, nil, nil, parameters, results, false), llssa.InGo)
				body := function.MakeBody(1)
				ctx := &context{prog: prog, pkg: pkg, goFn: root}
				proof := &coroLocalChannelProof{allocation: origin, channel: origin, elementSize: 8, fields: fields}
				status := ctx.emitCoroLocalChannelBufferTry(body, function.Param(0), function.Param(1), send, proof)
				body.Return(status)
			}
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify local channel: %v\n%s", err, module.String())
			}
			if strings.Contains(module.String(), "call ") {
				t.Fatalf("local buffer transaction unexpectedly calls a runtime helper:\n%s", module.String())
			}
			if !strings.Contains(module.String(), "store volatile i64 0") {
				t.Fatalf("local receive lost its collector-visible buffer clear:\n%s", module.String())
			}
		})
	}
}
