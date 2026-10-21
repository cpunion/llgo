// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package cl

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"os"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

func waitGroupProofViewForTest(u *EmissionUniverse) coroWaitGroupProofView {
	return coroWaitGroupProofView{
		predicates: u.coroProgramIR.programPredicates,
		functions:  u.coroProgramIR.callableClassifications,
		resolve:    u.Resolve,
	}
}

func TestCoroWaitGroupPurePredicateRejectsObservableQueries(t *testing.T) {
	pkg, _, files := buildGoSSAPkg(t, `package foo
import "unsafe"
var observed bool
func False() bool { return false }
func True() bool { return true }
func Load() bool { return observed }
func Store() bool { observed=true;return false }
func Cycle() bool { return Cycle() }
func Nil() *int { return nil }
func Nested() bool { return Nil()!=nil }
func Folded() bool { if False(){return Load()};return false }
func Unknown() bool
func UnsafeNil() unsafe.Pointer { return nil }
func Converted() bool { return (*int)(UnsafeNil())!=nil }
//go:linkname Foreign C.wg_fast_query
func Foreign() bool { return false }
func ForeignWrapper() bool { return Foreign() }

`)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg, Files: files}})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name         string
		known, value bool
	}{
		{"False", true, false}, {"True", true, true}, {"Nested", true, false}, {"Folded", true, false}, {"Converted", true, false},
		{"Load", false, false}, {"Store", false, false}, {"Cycle", false, false}, {"Unknown", false, false}, {"Foreign", false, false}, {"ForeignWrapper", false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			value, ok := coroWaitGroupPurePredicate(waitGroupProofViewForTest(universe), pkg.Func(test.name), make(map[*ssa.Function]bool))
			if ok != test.known {
				t.Fatalf("known=%t want %t", ok, test.known)
			}
			if ok && (value.Value == nil || value.Value.Kind() != constant.Bool || constant.BoolVal(value.Value) != test.value) {
				t.Fatalf("constant=%v want %t", value, test.value)
			}
		})
	}
	t.Run("missing-frozen-classification", func(t *testing.T) {
		view := waitGroupProofViewForTest(universe)
		view.functions = nil
		if _, known := coroWaitGroupPurePredicate(view, pkg.Func("False"), make(map[*ssa.Function]bool)); known {
			t.Fatal("source body reconstructed a missing frozen callable classification")
		}
	})
}

func TestCoroWaitGroupFastUpdateIsCallFreeAtomicIR(t *testing.T) {
	pkg, _, _ := buildGoSSAPkg(t, `package foo;func Target(){}`)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	native := prog.NewPackage("wgfast", "wgfast")
	module := native.Module()
	defer module.Dispose()
	empty := types.NewStruct(nil, nil)
	at := types.NewStruct([]*types.Var{types.NewField(token.NoPos, nil, "_", empty, false), types.NewField(token.NoPos, nil, "_", empty, false), types.NewField(token.NoPos, nil, "v", types.Typ[types.Uint64], false)}, nil)
	wg := types.NewStruct([]*types.Var{types.NewField(token.NoPos, nil, "_", empty, false), types.NewField(token.NoPos, nil, "state", at, false), types.NewField(token.NoPos, nil, "sema", types.Typ[types.Uint32], false)}, nil)
	signature := types.NewSignatureType(nil, nil, nil, types.NewTuple(types.NewParam(token.NoPos, nil, "wg", types.NewPointer(wg)), types.NewParam(token.NoPos, nil, "delta", types.Typ[types.Int])), types.NewTuple(types.NewParam(token.NoPos, nil, "ok", types.Typ[types.Bool])), false)
	function := native.NewFunc("attempt", signature, llssa.InGo)
	body := function.MakeBody(1)
	ctx := &context{prog: prog, pkg: native}
	proof := &coroWaitGroupAddFastPlan{target: pkg.Func("Target"), sourceDigest: coroWaitGroupAddSourceDigest, stateField: 1, valueField: 2}
	value := ctx.emitCoroWaitGroupAddFast(body, []llssa.Expr{function.Param(0), function.Param(1)}, proof)
	body.Return(value)
	body.EndBuild()
	body.Dispose()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify: %v\n%s", err, module.String())
	}
	ir := module.String()
	for _, want := range []string{"load atomic i64", "cmpxchg ptr", "seq_cst seq_cst"} {
		if !strings.Contains(ir, want) {
			t.Fatalf("missing %q:\n%s", want, ir)
		}
	}
	if strings.Contains(ir, "call ") {
		t.Fatalf("atomic try unexpectedly calls helper:\n%s", ir)
	}
}

func TestCoroWaitGroupPurePredicateUsesFrozenBinding(t *testing.T) {
	pkg, _, files := buildGoSSAPkg(t, `package foo
import "unsafe"
//go:linkname Enabled llgo.coroExecutionDomainEnabled
func Enabled() bool
var observed unsafe.Pointer
func Context() unsafe.Pointer { if !Enabled(){return nil};return observed }
func Query() bool { return (*int)(Context())!=nil }
`)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	for _, test := range []struct {
		name   string
		values coro.ProgramCapabilities
		known  bool
	}{
		{"disabled", 0, true}, {"enabled", coro.ExecutionDomainProgramCapability(), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			universe, err := PrepareEmissionUniverseWithOptions(prog, nil, []EmissionPackage{{SSA: pkg, Files: files}}, EmissionUniverseOptions{CoroProgramPredicates: coro.ProgramPredicateRequirements{Mask: coro.ProgramPredicateMask(), Values: test.values}})
			if err != nil {
				t.Fatal(err)
			}
			value, ok := coroWaitGroupPurePredicate(waitGroupProofViewForTest(universe), pkg.Func("Query"), make(map[*ssa.Function]bool))
			if ok != test.known {
				t.Fatalf("known=%t want %t", ok, test.known)
			}
			if ok && (value.Value == nil || constant.BoolVal(value.Value)) {
				t.Fatalf("disabled query did not prove false: %v", value)
			}
		})
	}
}

func TestCoroWaitGroupFastPlanRequiresExactSourceAndBindings(t *testing.T) {
	body, err := os.ReadFile("testdata/waitgroup_add_go127.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, opcode, query, shadow             string
		noinline, changed, domain, race, layout bool
		reboundRace                             bool
		want                                    bool
	}{
		{name: "verified", opcode: "atomicAddReturnNew", query: "return false", want: true},
		{name: "wrong-atomic", opcode: "atomicXor", query: "return false"},
		{name: "query-load", opcode: "atomicAddReturnNew", query: "return observed"},
		{name: "query-store", opcode: "atomicAddReturnNew", query: "observed=true;return false"},
		{name: "query-true", opcode: "atomicAddReturnNew", query: "return true"},
		{name: "noinline", opcode: "atomicAddReturnNew", query: "return false", noinline: true},
		{name: "changed-source", opcode: "atomicAddReturnNew", query: "return false", changed: true},
		{name: "domain", opcode: "atomicAddReturnNew", query: "return false", domain: true},
		{name: "race", opcode: "atomicAddReturnNew", query: "return false", race: true},
		{name: "rebound-race-import", opcode: "atomicAddReturnNew", query: "return false", reboundRace: true},
		{name: "layout", opcode: "atomicAddReturnNew", query: "return false", layout: true},
		{name: "shadow-counter-type", opcode: "atomicAddReturnNew", query: "return false", shadow: "type int32=uint8"},
		{name: "shadow-waiter-type", opcode: "atomicAddReturnNew", query: "return false", shadow: "type uint32=uint64"},
		{name: "shadow-update-type", opcode: "atomicAddReturnNew", query: "return false", shadow: "type uint64=atomic.Word"},
	} {
		t.Run(test.name, func(t *testing.T) {
			program := newEmissionTestProgram()
			program.ssa.CreatePackage(types.Unsafe, nil, nil, true)
			atomicPkg := program.addPackage(t, "sync/atomic", `package atomic
import _ "unsafe"
type noCopy struct{}
type align64 struct{}
type Word=uint64
type Uint64 struct { _ noCopy; _ align64; v uint64 }
//go:linkname AddUint64 llgo.`+test.opcode+`
func AddUint64(p *uint64,delta uint64) uint64
func(x *Uint64)Add(delta uint64)uint64{return AddUint64(&x.v,delta)}
func(x *Uint64)Or(mask uint64)uint64{return 0}
func(x *Uint64)Load()uint64{return 0}
func(x *Uint64)Store(v uint64){}
`)
			raceValue := "false"
			if test.race {
				raceValue = "true"
			}
			racePkg := program.addPackage(t, "internal/race", `package race
import "unsafe"
const Enabled=`+raceValue+`
func ReleaseMerge(unsafe.Pointer){}
func Disable(){}
func Enable(){}
func Read(unsafe.Pointer){}
`)
			var reboundRacePkg emissionTestPackage
			raceImport := `"internal/race"`
			if test.reboundRace {
				raceImport = `_ "internal/race";race "replacement/race"`
				reboundRacePkg = program.addPackage(t, "replacement/race", `package race
import "unsafe"
const Enabled=true
func ReleaseMerge(unsafe.Pointer){}
func Disable(){}
func Enable(){}
func Read(unsafe.Pointer){}
`)
			}
			syncTestPkg := program.addPackage(t, "internal/synctest", `package synctest
var observed bool
func IsInBubble()bool{`+test.query+`}
type Association int
const(Unbubbled Association=iota;OtherBubble;CurrentBubble)
func Associate[T any](*T)Association{return Unbubbled}
func Disassociate[T any](*T){}
`)
			impl := string(body)
			if test.noinline {
				impl = strings.Replace(impl, "func (wg *WaitGroup) Add", "//go:noinline\nfunc (wg *WaitGroup) Add", 1)
			}
			if test.changed {
				impl = strings.Replace(impl, "if v > 0 || w == 0", "if v >= 0 || w == 0", 1)
			}
			noCopy := "struct{}"
			if test.layout {
				noCopy = "struct{ value uint64 }"
			}
			syncPkg := program.addPackage(t, "sync", `package sync
import(`+raceImport+`;"internal/synctest";"sync/atomic";"unsafe")
type noCopy `+noCopy+`
type WaitGroup struct{noCopy noCopy;state atomic.Uint64;sema uint32}
const waitGroupBubbleFlag=0x80000000
func fatal(string){panic("fatal")}
func runtime_Semrelease(*uint32,bool,int){}
`+test.shadow+"\n"+impl)
			program.ssa.Build()
			prog := newLLSSAProg(t)
			defer prog.Dispose()
			values := coro.ProgramCapabilities(0)
			if test.domain {
				values = coro.ExecutionDomainProgramCapability()
			}
			inputs := []EmissionPackage{}
			for _, p := range []emissionTestPackage{atomicPkg, racePkg, syncTestPkg, syncPkg} {
				inputs = append(inputs, EmissionPackage{SSA: p.ssa, Files: []*ast.File{p.file}})
			}
			if test.reboundRace {
				inputs = append(inputs, EmissionPackage{SSA: reboundRacePkg.ssa, Files: []*ast.File{reboundRacePkg.file}})
			}
			universe, err := PrepareEmissionUniverseWithOptions(prog, nil, inputs, EmissionUniverseOptions{CoroProgramPredicates: coro.ProgramPredicateRequirements{Mask: coro.ProgramPredicateMask(), Values: values}})
			if err != nil {
				t.Fatal(err)
			}
			named := syncPkg.types.Scope().Lookup("WaitGroup").Type().(*types.Named)
			fn := program.ssa.FuncValue(named.Method(0))
			ctx := &context{prog: prog, emissionUniverse: universe}
			proof := planCoroWaitGroupAddFast(ctx, fn)
			if (proof != nil) != test.want {
				t.Fatalf("proof=%v want=%t", proof, test.want)
			}
		})
	}
}
