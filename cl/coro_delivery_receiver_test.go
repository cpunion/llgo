//go:build !llgo

package cl

import (
	"go/ast"
	"go/types"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	llssa "github.com/xgo-dev/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

func TestCoroDeliveryReceiverSnapshotSurvivesForwarders(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	source := newEmissionTestProgram()
	source.ssa.CreatePackage(types.Unsafe, nil, nil, true)
	runtimePkg := source.addPackage(t, llssa.PkgRuntime, `package runtime
import "unsafe"
func AllocU(size uintptr) unsafe.Pointer { return nil }
func AllocZ(size uintptr) unsafe.Pointer { return nil }
func AssertNilDeref(value bool) {}
func PanicWrapNilPointer(value bool, receiver, method string) {}
`)
	foo := source.addPackage(t, "foo", `package foo
import "unsafe"
type Result = struct { R0 int; R1 error }
func initialize(out *Result) { out.R0 = -1 }
func drop(out *Result, _ unsafe.Pointer, token, b int) { out.R0 = token*10+b }
//go:linkname borrow llgo.coroBorrowResult
func borrow(func(*Result), func(*Result, unsafe.Pointer, int, int)) *Result
func Acquire(token, b int) (int,error) { out := borrow(initialize,drop); out.R0=token+b; return out.R0,out.R1 }
type Receiver struct { Token int }
func (r Receiver) Open(b int) (int,error) { return Acquire(r.Token,b) }
func (Receiver) Unused(b int) (int,error) { return Acquire(7,b) }
type Middle struct { Receiver }
type Outer struct { *Middle }
func Direct(p *Receiver, b int) (int,error) { return (*Receiver).Open(p,b) }
func Chain(p *Receiver, b int) (int,error) { return Direct(p,b) }
//go:noinline
func NoInline(p *Receiver, b int) (int,error) { return Chain(p,b) }
func Promoted(p *Outer, b int) (int,error) { return (*Outer).Open(p,b) }
var Published = NoInline
func Discard(p *Receiver) { (*Receiver).Open(p,1) }
func Deferred(p *Receiver) { defer (*Receiver).Open(p,2) }
func Spawned(p *Receiver) { go (*Receiver).Open(p,3) }
func Dead(p *Receiver) { if false { (*Receiver).Open(p,4) } }
func Ordinary(p *Receiver) (int,error) { n,e := p.Open(5); return n+1,e }
func LoopA(p *Receiver) (int,error) { return LoopB(p) }
func LoopB(p *Receiver) (int,error) { return LoopA(p) }
func Root(p *Receiver, outer *Outer) {
 Direct(p,1); Chain(p,2); NoInline(p,3); Promoted(outer,4)
 Discard(p); Deferred(p); Spawned(p); Dead(p); Ordinary(p)
 (*Receiver).Unused(p,6)
}
`)
	source.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	u, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{
		{SSA: runtimePkg.ssa, Files: []*ast.File{runtimePkg.file}},
		{SSA: foo.ssa, Files: []*ast.File{foo.file}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ir := u.coroProgramIR
	metadata := coro.LibraryEffectMetadata{FunctionIDSchema: coro.FunctionIDSchema, CoroABI: coro.PhysicalABIV1,
		SchedulerABI: coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0, PanicABI: coro.PanicExplicitStatusABIV0, FuncRepABI: coro.FuncRepABIV2,
		TargetTriple: prog.TargetSpec().Triple, PointerBits: prog.PointerSize() * 8, Endianness: "little", DataLayout: prog.DataLayout()}
	var shared *coroDeliveryReceiverSnapshot
	for _, name := range []string{"Direct", "Chain", "NoInline", "Promoted"} {
		function := foo.ssa.Func(name)
		if !ir.localBodyFacts[function].ResultDelivery {
			t.Fatalf("%s lost the result-delivery requirement needed to exclude unbound targets from trusted nonresource dispatch", name)
		}
		recipe, present, err := ir.sourceDelivery(function)
		if !present || err != nil || !recipe.requiresState() || recipe.snapshot == nil || recipe.snapshot.value == nil {
			t.Fatalf("%s lost its evaluated receiver snapshot: present=%t recipe=%+v err=%v", name, present, recipe, err)
		}
		if recipe.snapshot.box.NumFields() != 3 {
			t.Fatalf("%s did not capture both terminal cleanup arguments and padding: %s", name, recipe.snapshot.box)
		}
		if name != "Promoted" {
			if shared != nil && recipe.snapshot != shared {
				t.Fatalf("%s replaced the deepest evaluated snapshot", name)
			}
			shared = recipe.snapshot
		}
		if record, present, err := u.CoroLibraryEffects().SourceDeliveryRecord(function, metadata); !present || err != nil || !record.RequiresState {
			t.Fatalf("%s archive publication lost its receiver requirement: present=%t record=%+v err=%v", name, present, record, err)
		}
	}
	for _, name := range []string{"Discard", "Deferred", "Spawned"} {
		found := 0
		for _, block := range foo.ssa.Func(name).Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok || call.Common().StaticCallee() == nil {
					continue
				}
				_, present, err := u.CoroCallSitePlan(call)
				if !present || err != nil {
					t.Fatalf("%s call lost its receiver binding: present=%t err=%v", name, present, err)
				}
				found++
			}
		}
		if found != 1 {
			t.Fatalf("%s checked %d calls, want one exact method-expression occurrence", name, found)
		}
	}
	wrappers := 0
	for _, function := range u.Functions() {
		if function.Name() == "Open" && function.Signature.Recv() != nil && strings.HasPrefix(function.Synthetic, "wrapper for ") {
			if function.Signature.Recv().Type().String() == "foo.Middle" {
				if _, present, err := ir.sourceDelivery(function); !present || err != nil {
					t.Fatalf("immutable promoted value lost its valid recipe: present=%t err=%v", present, err)
				}
				continue
			}
			recipe, present, err := ir.sourceDelivery(function)
			if err != nil || !present || !recipe.requiresState() || recipe.snapshot == nil || ir.validateInterfaceDeliveryReceiver(function) != nil {
				t.Fatalf("%s interface descriptor lost resource custody: present=%t recipe=%+v err=%v", function, present, recipe, err)
			}
			wrappers++
		}
		if function.Name() == "Unused" && function.Signature.Recv() != nil {
			if _, present, err := ir.sourceDelivery(function); err != nil || !present {
				t.Fatalf("unused receiver %s acquired a false snapshot requirement: present=%t err=%v", function, present, err)
			}
		}
	}
	if wrappers < 3 {
		t.Fatalf("checked %d receiver wrappers, want direct and promoted value/pointer paths", wrappers)
	}
	for _, name := range []string{"Ordinary", "LoopA", "LoopB"} {
		if _, present, err := ir.sourceDelivery(foo.ssa.Func(name)); err != nil || present {
			t.Fatalf("ordinary %s acquired a receiver ownership effect: present=%t err=%v", name, present, err)
		}
	}
}
