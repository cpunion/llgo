// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package cl

import (
	"go/ast"
	"go/types"
	"os"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	"github.com/xgo-dev/llgo/internal/goembed"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

func TestCoroWaitGroupWaitFastRequiresExactProof(t *testing.T) {
	add, err := os.ReadFile("testdata/waitgroup_add_go127.txt")
	if err != nil {
		t.Fatal(err)
	}
	patch, err := os.ReadFile("../runtime/_patch/sync/waitgroup_coro_go127.go")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(patch), "//go:linkname llgoWaitGroupReadyPeer")
	if start < 0 {
		t.Fatal("missing Wait patch declarations")
	}
	for _, change := range []string{"verified", "wait-source", "loop-source", "receiver", "opportunity", "noinline", "loop-noinline", "load", "load-side-effect", "load-return", "load-field", "domain", "race", "add-source", "missing-load-classification", "shadow-true", "shadow-false", "shadow-bool"} {
		t.Run(change, func(t *testing.T) {
			program := newEmissionTestProgram()
			program.ssa.CreatePackage(types.Unsafe, nil, nil, true)
			loadBody := "return LoadUint64(&x.v)"
			loadField := ""
			loadDecl := "//go:linkname LoadUint64 llgo.atomicLoad\nfunc LoadUint64(*uint64)uint64"
			switch change {
			case "load":
				loadDecl = "func LoadUint64(*uint64)uint64{return 0}"
			case "load-side-effect":
				loadBody = "observed++;return LoadUint64(&x.v)"
			case "load-return":
				loadBody = "return LoadUint64(&x.v)+1"
			case "load-field":
				loadBody = "return LoadUint64(&x.other)"
				loadField = "other uint64"
			}
			atomic := program.addPackage(t, "sync/atomic", `package atomic
import _ "unsafe"
type noCopy struct{}
type align64 struct{}
type BoolAlias=bool
type Uint64 struct{_ noCopy;_ align64;v uint64;`+loadField+`}
var observed int
`+loadDecl+`
//go:linkname AddUint64 llgo.atomicAddReturnNew
func AddUint64(*uint64,uint64)uint64
func(x *Uint64)Load()uint64{`+loadBody+`}
func(x *Uint64)Add(delta uint64)uint64{return AddUint64(&x.v,delta)}
func(x *Uint64)Or(mask uint64)uint64{return 0}
func(x *Uint64)Store(v uint64){}
func(x *Uint64)CompareAndSwap(old,new uint64)bool{return false}
`)
			raceValue := "false"
			if change == "race" {
				raceValue = "true"
			}
			race := program.addPackage(t, "internal/race", `package race
import "unsafe"
const Enabled=`+raceValue+`
func ReleaseMerge(unsafe.Pointer){}
func Disable(){}
func Enable(){}
func Read(unsafe.Pointer){}
func Write(unsafe.Pointer){}
func Acquire(unsafe.Pointer){}
`)
			synctest := program.addPackage(t, "internal/synctest", `package synctest
func IsInBubble()bool{return false}
type Association int
const(Unbubbled Association=iota;OtherBubble;CurrentBubble)
func Associate[T any](*T)Association{return Unbubbled}
func Disassociate[T any](*T){}
func IsAssociated[T any](*T)bool{return false}
`)
			runtime := program.addPackage(t, "github.com/xgo-dev/llgo/runtime/internal/runtime", `package runtime
var observed bool
func CoroSemaphoreReadyPeerV1()bool{if observed{panic("ready")};return false}
`)
			body := string(patch)[start:]
			addBody := string(add)
			switch change {
			case "wait-source":
				body = strings.Replace(body, "wg.llgoWait(true)", "wg.llgoWait(false)", 1)
			case "loop-source":
				body = strings.Replace(body, "if v == 0", "if v <= 0", 1)
			case "receiver":
				body = strings.Replace(body, "func (wg *WaitGroup) Wait()", "func (other *WaitGroup) Wait()", 1) + "\nvar wg *WaitGroup\n"
			case "opportunity":
				body = strings.Replace(body, "llgoWait(opportunity bool)", "llgoWait(other bool)", 1) + "\nvar opportunity bool\n"
			case "noinline":
				body = strings.Replace(body, "func (wg *WaitGroup) Wait()", "//go:noinline\nfunc (wg *WaitGroup) Wait()", 1)
			case "loop-noinline":
				body = strings.Replace(body, "func (wg *WaitGroup) llgoWait", "//go:noinline\nfunc (wg *WaitGroup) llgoWait", 1)
			case "add-source":
				addBody = strings.Replace(addBody, "if v > 0 || w == 0", "if v >= 0 || w == 0", 1)
			}
			switch change {
			case "shadow-true":
				body += "\nconst true = false\n"
			case "shadow-false":
				body += "\nconst false = true\n"
			case "shadow-bool":
				body += "\ntype bool = boolAlias\ntype boolAlias = predeclaredBool\ntype predeclaredBool = atomic.BoolAlias\n"
			}
			syncPkg := program.addPackage(t, "sync", `package sync
import("internal/race";"internal/synctest";"sync/atomic";"unsafe")
type noCopy struct{}
type WaitGroup struct{noCopy noCopy;state atomic.Uint64;sema uint32}
const waitGroupBubbleFlag=0x80000000
func fatal(string){panic("fatal")}
func runtime_Semrelease(*uint32,bool,int){}
func runtime_SemacquireWaitGroup(*uint32,bool){llgoWaitGroupYield()}
func Root(wg *WaitGroup){wg.Add(1);wg.Wait()}
`+addBody+body)
			program.ssa.Build()
			prog := newLLSSAProg(t)
			defer prog.Dispose()
			values := coro.ProgramCapabilities(0)
			if change == "domain" {
				values = coro.ExecutionDomainProgramCapability()
			}
			var inputs []EmissionPackage
			for _, pkg := range []emissionTestPackage{atomic, race, synctest, runtime, syncPkg} {
				inputs = append(inputs, EmissionPackage{SSA: pkg.ssa, Files: []*ast.File{pkg.file}})
			}
			u, err := PrepareEmissionUniverseWithOptions(prog, nil, inputs, EmissionUniverseOptions{CoroProgramPredicates: coro.ProgramPredicateRequirements{Mask: coro.ProgramPredicateMask(), Values: values}})
			if err != nil {
				t.Fatal(err)
			}
			universe, err := coro.NewSSAEmissionUniverse(program.ssa, u.Functions())
			if err != nil {
				t.Fatal(err)
			}
			ids := u.FunctionIDConfig()
			ids.CoroABI = coro.PhysicalABIV1
			ids.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
			ids.ArchiveReady = true
			whole, err := coro.AnalyzeSSA(program.ssa, coro.Roots{{Function: syncPkg.ssa.Func("Root"), Demand: coro.AsyncDemand}}, coro.SSAConfig{EmissionUniverse: universe, FunctionIDs: ids, MaxPlainInstructions: 64, OutcomeMode: coro.OutcomeExplicitStatus, ClassifyLocalBody: u.CoroLocalBodyFacts, ClassifyLoweredCalls: u.CoroLoweredCalls, ResolveFunction: func(f *ssa.Function) (*ssa.Function, bool, error) {
				canonical, ok := u.Resolve(f)
				return canonical, ok, nil
			},
				ClassifyElidedCall: func(_ *ssa.Function, call ssa.CallInstruction) (bool, error) {
					site, found, err := u.CoroCallSitePlan(call)
					return found && site.ElidesCall(), err
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			named := syncPkg.types.Scope().Lookup("WaitGroup").Type().(*types.Named)
			var wait *ssa.Function
			for i := 0; i < named.NumMethods(); i++ {
				if named.Method(i).Name() == "Wait" {
					wait = program.ssa.FuncValue(named.Method(i))
				}
			}
			if change == "missing-load-classification" {
				for _, f := range u.Functions() {
					if f.Pkg == atomic.ssa && f.Name() == "Load" {
						delete(u.coroProgramIR.callableClassifications, f)
					}
				}
			}
			proof := planCoroWaitGroupWaitFast(&context{prog: prog, emissionUniverse: u}, whole, wait)
			if (proof != nil) != (change == "verified") {
				t.Fatalf("proof=%+v, want admission=%t", proof, change == "verified")
			}
			if proof != nil && !proof.valid() {
				t.Fatal("admitted incomplete proof")
			}
			if proof != nil {
				compilation := &Compilation{CoroPlan: whole, EmissionUniverse: u}
				enableCoroChildAwaitCompilation(compilation)
				compilation.PanicABI = coro.PanicExplicitStatusABIV0
				pkg, _, err := NewPackageExWithEmbedOptions(prog, nil, nil, nil, syncPkg.ssa, []*ast.File{syncPkg.file}, goembed.VarMap{}, PackageOptions{Compilation: compilation})
				if err != nil {
					t.Fatal(err)
				}
				module := pkg.Module()
				defer module.Dispose()
				if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
					t.Fatal(err)
				}
				root := requireCoroPhysicalFunction(t, module, "sync.Root").String()
				for _, want := range []string{"load atomic i64", "seq_cst", "__llgo_coro_yield_prepare_v1", "llgoWait$coro", "CoroSemaphoreReadyPeerV1$outcome"} {
					if !strings.Contains(root, want) {
						t.Fatalf("direct Wait caller lacks %q:\n%s", want, root)
					}
				}
				if strings.Contains(root, "@sync.(*WaitGroup).Wait$coro(") {
					t.Fatalf("fast caller retained the wrapper await:\n%s", root)
				}
				runCoroABITestPipeline(t, prog, module)
			}
		})
	}
}
