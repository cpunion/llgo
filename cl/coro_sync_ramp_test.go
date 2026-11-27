/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package cl

import (
	"go/ast"
	"go/constant"
	"go/types"
	"os"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	"github.com/xgo-dev/llgo/internal/goembed"
	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

func compileCoroSyncRampTestPackages(
	t *testing.T,
	inputs []EmissionPackage,
	root *ssa.Function,
	predicateValues coro.ProgramCapabilities,
	dynamicResolution coro.DynamicResolution,
) (llssa.Program, *Compilation, *coro.SSAPlan, llssa.Package) {
	t.Helper()
	return compileCoroSyncRampTestPackagesWithOptions(t, inputs, root, predicateValues, dynamicResolution, PackageOptions{})
}

func compileCoroSyncRampTestPackagesWithOptions(
	t *testing.T,
	inputs []EmissionPackage,
	root *ssa.Function,
	predicateValues coro.ProgramCapabilities,
	dynamicResolution coro.DynamicResolution,
	pkgOpts PackageOptions,
) (llssa.Program, *Compilation, *coro.SSAPlan, llssa.Package) {
	t.Helper()
	prog := newLLSSAProg(t)
	universe, err := PrepareEmissionUniverseWithOptions(prog, nil, inputs, EmissionUniverseOptions{
		CoroProgramPredicates: coro.ProgramPredicateRequirements{
			Mask:   coro.ProgramPredicateMask(),
			Values: predicateValues,
		},
	})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(root.Prog, universe.Functions())
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	config := coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		DynamicResolution:    dynamicResolution,
		MaxPlainInstructions: 64,
		OutcomeMode:          coro.OutcomeExplicitStatus,
		ClassifyLocalBody:    universe.CoroLocalBodyFacts,
		ClassifyLoweredCalls: universe.CoroLoweredCalls,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			for _, blk := range fn.Blocks {
				for _, ins := range blk.Instrs {
					if _, isGo := ins.(*ssa.Go); isGo {
						return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
					}
				}
			}
			return coro.SSAFunctionPolicy{}, nil
		},
		ClassifyElidedCall: func(_ *ssa.Function, call ssa.CallInstruction) (bool, error) {
			callPlan, found, err := universe.CoroCallSitePlan(call)
			return found && callPlan.ElidesCall(), err
		},
	}
	if dynamicResolution != coro.DynamicUnknownOnly {
		config.ClassifyDemandReferences = universe.CoroDemandReferences
		config.ClassifySyncDemandReferences = universe.CoroSyncDemandReferences
		config.ClassifyManagedValueReferences = universe.CoroPlanningMetadata().ManagedValueReferences
	}
	plan, err := coro.AnalyzeSSA(root.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, config)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	compilation := &Compilation{
		CoroPlan:         plan,
		EmissionUniverse: universe,
		PanicABI:         coro.PanicExplicitStatusABIV0,
	}
	enableCoroChildAwaitCompilation(compilation)
	rootInput := inputs[len(inputs)-1]
	pkgOpts.Compilation = compilation
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, rootInput.SSA, rootInput.Files, goembed.VarMap{},
		pkgOpts,
	)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
		ir := pkg.Module().String()
		prog.Dispose()
		t.Fatalf("LLVM verify failed: %v\n%s", err, ir)
	}
	return prog, compilation, plan, pkg
}

func extractLLVMFuncDef(t *testing.T, ir, sym string) string {
	t.Helper()
	needle := `"` + sym + `"(`
	for _, line := range strings.Split(ir, "\n") {
		if strings.HasPrefix(line, "define ") && strings.Contains(line, needle) {
			idx := strings.Index(ir, line)
			end := strings.Index(ir[idx:], "\n}\n")
			if end < 0 {
				return ir[idx:]
			}
			return ir[idx : idx+end]
		}
	}
	t.Fatalf("missing LLVM function definition for %q", sym)
	return ""
}

func sealedSyncRampForTest(t *testing.T, compilation *Compilation, fn *ssa.Function) *coroPhysicalSyncRampPlan {
	t.Helper()
	if compilation == nil || compilation.EmissionUniverse == nil || fn == nil {
		return nil
	}
	owner := compilation.EmissionUniverse.ownerOf(fn)
	if owner == nil {
		return nil
	}
	phys, err := compilation.EmissionUniverse.coroProgramIR.physicalFunctionPlan(fn, owner)
	if err != nil || phys == nil {
		return nil
	}
	return phys.syncRamp
}

func TestCoroSyncRampPureConstBoolRejectsObservableQueries(t *testing.T) {
	pkg, _, files := buildGoSSAPkg(t, `package foo
import "unsafe"
var observed bool
func False() bool { return false }
func True() bool { return true }
func Load() bool { return observed }
func Store() bool { observed = true; return false }
func Cycle() bool { return Cycle() }
func Nil() *int { return nil }
func Nested() bool { return Nil() != nil }
func Folded() bool { if False() { return Load() }; return false }
func Unknown() bool
func UnsafeNil() unsafe.Pointer { return nil }
func Converted() bool { return (*int)(UnsafeNil()) != nil }
//go:linkname Foreign C.sync_ramp_query
func Foreign() bool { return false }
func ForeignWrapper() bool { return Foreign() }
`)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg, Files: files}})
	if err != nil {
		t.Fatal(err)
	}
	pl := &coroSyncPlanner{ir: universe.coroProgramIR, resolve: universe.Resolve}
	for _, tc := range []struct {
		name         string
		known, value bool
	}{
		{"False", true, false},
		{"True", true, true},
		{"Nested", true, false},
		{"Folded", true, false},
		{"Converted", true, false},
		{"Load", false, false},
		{"Store", false, false},
		{"Cycle", false, false},
		{"Unknown", false, false},
		{"Foreign", false, false},
		{"ForeignWrapper", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, ok := pl.evalPureConstFunc(pkg.Func(tc.name), make(map[*ssa.Function]bool))
			if ok != tc.known {
				t.Fatalf("known=%t want %t", ok, tc.known)
			}
			if ok && (c == nil || c.Value == nil || c.Value.Kind() != constant.Bool || constant.BoolVal(c.Value) != tc.value) {
				t.Fatalf("const=%v want %t", c, tc.value)
			}
		})
	}
	t.Run("missing-frozen-classification", func(t *testing.T) {
		stripped := &coroSyncPlanner{
			ir:      &coroProgramIR{programPredicates: universe.coroProgramIR.programPredicates},
			resolve: universe.Resolve,
		}
		if _, ok := stripped.evalPureConstFunc(pkg.Func("False"), make(map[*ssa.Function]bool)); ok {
			t.Fatal("missing frozen callable classification must not fold")
		}
	})
}

func TestCoroSyncRampTryBailoutAndTryHelperComposition(t *testing.T) {
	fixture, err := os.ReadFile("testdata/poll_mutex_go127.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		noinline  bool
		wantRamps bool
	}{
		{"verified-poll-mutex", false, true},
		{"noinline-rejected", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			program := newEmissionTestProgram()
			program.ssa.CreatePackage(types.Unsafe, nil, nil, true)
			atomicPkg := program.addPackage(t, "sync/atomic", `package atomic
import _ "unsafe"
//go:linkname LoadUint64 llgo.atomicLoad
func LoadUint64(p *uint64) uint64
//go:linkname CompareAndSwapUint64 llgo.atomicCmpXchgOK
func CompareAndSwapUint64(p *uint64, old, new uint64) bool
`)
			src := strings.Replace(
				strings.Replace(string(fixture), "func runtime_Semacquire(sema *uint32)", "func runtime_Semacquire(sema *uint32) { yieldNow() }", 1),
				"func runtime_Semrelease(sema *uint32)", "func runtime_Semrelease(sema *uint32) { yieldNow() }", 1,
			) + `
//go:linkname yieldNow llgo.coroYield
func yieldNow()

type FD struct { fdmu fdMutex }
func (fd *FD) readLock() error {
	if !fd.fdmu.rwlock(true, true) {
		return errClosing()
	}
	return nil
}
func (fd *FD) readUnlock() {
	if fd.fdmu.rwunlock(true) {
		fd.destroy()
	}
}
func (fd *FD) destroy() { yieldNow() }
func errClosing() error { return nil }

func Root(fd *FD) error {
	if err := fd.readLock(); err != nil {
		return err
	}
	fd.readUnlock()
	_ = fd.fdmu.incref()
	_ = fd.fdmu.decref()
	return nil
}
`
			if tc.noinline {
				src = strings.Replace(src, "func (mu *fdMutex) rwlock", "//go:noinline\nfunc (mu *fdMutex) rwlock", 1)
				src = strings.Replace(src, "func (fd *FD) readLock", "//go:noinline\nfunc (fd *FD) readLock", 1)
			}
			pollPkg := program.addPackage(t, "internal/poll", src)
			program.ssa.Build()
			inputs := []EmissionPackage{
				{SSA: atomicPkg.ssa, Files: []*ast.File{atomicPkg.file}},
				{SSA: pollPkg.ssa, Files: []*ast.File{pollPkg.file}},
			}
			prog, compilation, _, llpkg := compileCoroSyncRampTestPackages(
				t, inputs, pollPkg.ssa.Func("Root"), 0, coro.DynamicUnknownOnly,
			)
			defer prog.Dispose()

			muType := pollPkg.types.Scope().Lookup("fdMutex").Type().(*types.Named)
			fdType := pollPkg.types.Scope().Lookup("FD").Type().(*types.Named)
			findMethod := func(named *types.Named, name string) *ssa.Function {
				for i := 0; i < named.NumMethods(); i++ {
					if named.Method(i).Name() == name {
						return program.ssa.FuncValue(named.Method(i))
					}
				}
				t.Fatalf("method %s not found on %s", name, named.Obj().Name())
				return nil
			}
			rwlockRamp := sealedSyncRampForTest(t, compilation, findMethod(muType, "rwlock"))
			readLockRamp := sealedSyncRampForTest(t, compilation, findMethod(fdType, "readLock"))
			if tc.wantRamps {
				for _, m := range []string{"rwlock", "rwunlock", "incref", "decref"} {
					r := sealedSyncRampForTest(t, compilation, findMethod(muType, m))
					if r == nil || r.kind != coroSyncRampTryBailout || !r.hasTryHelper {
						t.Fatalf("fdMutex.%s ramp=%+v, want TryBailout with hasTryHelper", m, r)
					}
				}
				if readLockRamp == nil || readLockRamp.kind != coroSyncRampTryBailout {
					t.Fatalf("FD.readLock ramp=%+v, want TryBailout composing fdMutex.rwlock.try", readLockRamp)
				}
				ir := llpkg.Module().String()
				if !strings.Contains(ir, "rwlock$coro.try") || !strings.Contains(ir, "cmpxchg ") {
					t.Fatalf("expected inline cmpxchg and .try helper in LLVM IR")
				}
			} else {
				if rwlockRamp != nil || readLockRamp != nil {
					t.Fatalf("go:noinline should suppress sync-fast ramp: rwlock=%v readLock=%v", rwlockRamp, readLockRamp)
				}
			}
		})
	}
}

func TestCoroSyncRampWaitGroupAddAndExecutionDomainAndRace(t *testing.T) {
	addBody, err := os.ReadFile("testdata/waitgroup_add_go127.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		race     bool
		domain   bool
		noinline bool
		wantRamp bool
	}{
		{"fast-default", false, false, false, true},
		{"execution-domain-safe-fallback", false, true, false, false},
		{"race-defer-rejected", true, false, false, false},
		{"noinline-rejected", false, false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			program := newEmissionTestProgram()
			program.ssa.CreatePackage(types.Unsafe, nil, nil, true)
			atomicPkg := program.addPackage(t, "sync/atomic", `package atomic
import _ "unsafe"
type noCopy struct{}
type align64 struct{}
type Uint64 struct { _ noCopy; _ align64; v uint64 }
//go:linkname AddUint64 llgo.atomicAddReturnNew
func AddUint64(*uint64, uint64) uint64
//go:linkname LoadUint64 llgo.atomicLoad
func LoadUint64(*uint64) uint64
//go:linkname StoreUint64 llgo.atomicStore
func StoreUint64(*uint64, uint64)
//go:linkname OrUint64 llgo.atomicOr
func OrUint64(*uint64, uint64) uint64
func (x *Uint64) Add(delta uint64) uint64 { return AddUint64(&x.v, delta) }
func (x *Uint64) Load() uint64            { return LoadUint64(&x.v) }
func (x *Uint64) Store(v uint64)          { StoreUint64(&x.v, v) }
func (x *Uint64) Or(mask uint64) uint64   { return OrUint64(&x.v, mask) }
`)
			raceVal := "false"
			if tc.race {
				raceVal = "true"
			}
			racePkg := program.addPackage(t, "internal/race", `package race
import "unsafe"
const Enabled = `+raceVal+`
func ReleaseMerge(unsafe.Pointer) {}
func Disable() {}
func Enable() {}
func Read(unsafe.Pointer) {}
`)
			synctestPkg := program.addPackage(t, "internal/synctest", `package synctest
import _ "unsafe"
//go:linkname executionDomainEnabled llgo.coroExecutionDomainEnabled
func executionDomainEnabled() bool
var bubbleFlag bool
func IsInBubble() bool {
	if !executionDomainEnabled() {
		return false
	}
	return bubbleFlag
}
type Association int
const (
	Unbubbled Association = iota
	OtherBubble
	CurrentBubble
)
func Associate[T any](*T) Association { return Unbubbled }
func Disassociate[T any](*T)          {}
`)
			impl := string(addBody)
			if tc.noinline {
				impl = strings.Replace(impl, "func (wg *WaitGroup) Add", "//go:noinline\nfunc (wg *WaitGroup) Add", 1)
			}
			syncPkg := program.addPackage(t, "sync", `package sync
import (
	"internal/race"
	"internal/synctest"
	"sync/atomic"
	"unsafe"
)
//go:linkname yieldNow llgo.coroYield
func yieldNow()
type noCopy struct{}
type WaitGroup struct { noCopy noCopy; state atomic.Uint64; sema uint32 }
const waitGroupBubbleFlag = 0x80000000
func fatal(string) { panic("fatal") }
func runtime_Semrelease(*uint32, bool, int) { yieldNow() }
`+impl+`
func Root(wg *WaitGroup) { wg.Add(1); wg.Add(-1) }
`)
			program.ssa.Build()
			var pred coro.ProgramCapabilities
			if tc.domain {
				pred = coro.ExecutionDomainProgramCapability()
			}
			inputs := []EmissionPackage{
				{SSA: atomicPkg.ssa, Files: []*ast.File{atomicPkg.file}},
				{SSA: racePkg.ssa, Files: []*ast.File{racePkg.file}},
				{SSA: synctestPkg.ssa, Files: []*ast.File{synctestPkg.file}},
				{SSA: syncPkg.ssa, Files: []*ast.File{syncPkg.file}},
			}
			prog, compilation, plan, llpkg := compileCoroSyncRampTestPackages(
				t, inputs, syncPkg.ssa.Func("Root"), pred, coro.DynamicUnknownOnly,
			)
			defer prog.Dispose()

			wgType := syncPkg.types.Scope().Lookup("WaitGroup").Type().(*types.Named)
			addFn := program.ssa.FuncValue(wgType.Method(0))
			ramp := sealedSyncRampForTest(t, compilation, addFn)
			if tc.wantRamp {
				if ramp == nil || ramp.kind != coroSyncRampResumeSlow {
					logical, _ := plan.FunctionPlan(addFn)
					t.Fatalf("WaitGroup.Add ramp=%+v (logical=%+v), want ResumeSlow", ramp, logical)
				}
				ir := llpkg.Module().String()
				rampIR := extractLLVMFuncDef(t, ir, "sync.(*WaitGroup).Add$coro")
				if !strings.Contains(rampIR, "atomicrmw add") {
					t.Fatalf("expected inline atomicrmw add in Add$coro ramp:\n%s", rampIR)
				}
				if strings.Contains(rampIR, "IsInBubble") {
					t.Fatalf("expected IsInBubble folded away when ExecutionDomain==false:\n%s", rampIR)
				}
			} else if ramp != nil {
				t.Fatalf("expected no sync-fast ramp for %s, got %+v", tc.name, ramp)
			}
		})
	}
}

func TestCoroSyncRampTailSlowAndCallerInlineYield(t *testing.T) {
	src := `package demo
import _ "unsafe"

//go:linkname yieldNow llgo.coroYield
func yieldNow()

type Box struct {
	ready bool
	val   int
}

func (b *Box) tryRead() (int, bool) {
	if !b.ready {
		return 0, false
	}
	return b.val, true
}

func (b *Box) readSlow() int {
	yieldNow()
	return b.val
}

func (b *Box) Read() int {
	if v, ok := b.tryRead(); ok {
		return v
	}
	return b.readSlow()
}

func (b *Box) tryWaitFast() uint8 {
	if b.ready {
		return 0
	}
	if b.val > 0 {
		return 1
	}
	return 2
}

func (b *Box) tryWaitDone() bool { return b.ready }

func (b *Box) waitSlow() {
	for !b.ready {
		yieldNow()
	}
}

func (b *Box) Wait() {
	switch b.tryWaitFast() {
	case 0:
		return
	case 1:
		yieldNow()
		if b.tryWaitDone() {
			return
		}
		b.waitSlow()
	default:
		b.waitSlow()
	}
}

func Root(b *Box) int {
	b.Wait()
	return b.Read()
}
`
	ssaPkg, _, files := buildGoSSAPkg(t, src)
	prog, compilation, _, llpkg := compileCoroSyncRampTestPackages(
		t, []EmissionPackage{{SSA: ssaPkg, Files: files}}, ssaPkg.Func("Root"), 0, coro.DynamicUnknownOnly,
	)
	defer prog.Dispose()

	boxType := ssaPkg.Pkg.Scope().Lookup("Box").Type().(*types.Named)
	findMethod := func(name string) *ssa.Function {
		for i := 0; i < boxType.NumMethods(); i++ {
			if boxType.Method(i).Name() == name {
				return ssaPkg.Prog.FuncValue(boxType.Method(i))
			}
		}
		t.Fatalf("missing method %s", name)
		return nil
	}
	readRamp := sealedSyncRampForTest(t, compilation, findMethod("Read"))
	if readRamp == nil || readRamp.kind != coroSyncRampTailSlow {
		t.Fatalf("Box.Read ramp=%+v, want TailSlow", readRamp)
	}
	waitRamp := sealedSyncRampForTest(t, compilation, findMethod("Wait"))
	if waitRamp == nil || waitRamp.kind != coroSyncRampCallerInlineYield {
		t.Fatalf("Box.Wait ramp=%+v, want CallerInlineYield", waitRamp)
	}
	ir := llpkg.Module().String()
	if !strings.Contains(ir, coroOutcomePropagateHelperNameV1) {
		t.Fatalf("expected %s helper for $outcome non-Return propagation", coroOutcomePropagateHelperNameV1)
	}
}

func TestCoroSyncRampSingleAwaitAndTailForwardNullable(t *testing.T) {
	src := `package demo
import _ "unsafe"

//go:linkname yieldNow llgo.coroYield
func yieldNow()

type Reader interface {
	Read(buf []byte) (int, error)
}

type FastReader struct {
	data []byte
	pos  int
}

func (r *FastReader) tryRead(buf []byte) (int, bool) {
	if r.pos >= len(r.data) {
		return 0, false
	}
	if len(buf) == 0 {
		return 0, true
	}
	buf[0] = r.data[r.pos]
	r.pos++
	return 1, true
}

func (r *FastReader) readSlow(buf []byte) (int, error) {
	yieldNow()
	return 0, nil
}

func (r *FastReader) Read(buf []byte) (int, error) {
	if n, ok := r.tryRead(buf); ok {
		return n, nil
	}
	return r.readSlow(buf)
}

func ReadAtLeast(r Reader, buf []byte, min int) (n int, err error) {
	if len(buf) < min {
		return 0, nil
	}
	for n < min && err == nil {
		var nn int
		nn, err = r.Read(buf[n:])
		n += nn
	}
	return n, err
}

func ReadFull(r Reader, buf []byte) (int, error) {
	return ReadAtLeast(r, buf, len(buf))
}

func SpawnTarget(r Reader, buf []byte) {
	_, _ = ReadFull(r, buf)
}

func Root(r *FastReader, buf []byte) (int, error) {
	go SpawnTarget(r, buf)
	return ReadFull(r, buf)
}
`
	ssaPkg, _, files := buildGoSSAPkg(t, src)
	prog, compilation, _, llpkg := compileCoroSyncRampTestPackages(
		t, []EmissionPackage{{SSA: ssaPkg, Files: files}}, ssaPkg.Func("Root"), 0, coro.DynamicCHAClosed,
	)
	defer prog.Dispose()

	ralRamp := sealedSyncRampForTest(t, compilation, ssaPkg.Func("ReadAtLeast"))
	if ralRamp == nil || ralRamp.kind != coroSyncRampSingleAwait {
		t.Fatalf("ReadAtLeast ramp=%+v, want SingleAwait", ralRamp)
	}
	// Verify ReadFull tail-forwards to ReadAtLeast and SpawnTarget checks the
	// returned handle for null (regression for TCP nil-deref when calling a
	// tailForward wrapper whose target is a nullable-handle sync-fast ramp).
	rfOwner := compilation.EmissionUniverse.ownerOf(ssaPkg.Func("ReadFull"))
	rfPhys, err := compilation.EmissionUniverse.coroProgramIR.physicalFunctionPlan(ssaPkg.Func("ReadFull"), rfOwner)
	if err != nil || rfPhys == nil || rfPhys.tailForward == nil {
		t.Fatalf("ReadFull physical plan=%+v err=%v, want tailForward", rfPhys, err)
	}
	ir := llpkg.Module().String()
	// Verify SingleAwait $coro.slow switch has tags 0, 1 (mode==1 preChild), 2 (mode==2 post-call).
	slowIR := extractLLVMFuncDef(t, ir, "demo.ReadAtLeast$coro.slow")
	if !strings.Contains(slowIR, "i8 1, label") || !strings.Contains(slowIR, "i8 2, label") {
		t.Fatalf("ReadAtLeast$coro.slow missing mode==1/mode==2 resume dispatch")
	}
	// Verify mode==2 in ReadAtLeast$coro passes pre-call live value (0), NOT n+nn.
	rampIR := extractLLVMFuncDef(t, ir, "demo.ReadAtLeast$coro")
	if !strings.Contains(rampIR, "i8 2, ptr null, i64 0") {
		t.Fatalf("expected SingleAwait mode==2 to forward pre-call n=0 (not post-incremented n+nn):\n%s", rampIR)
	}
	// Verify SpawnTarget tests the nullable handle returned by ReadFull$coro.
	spawnIR := extractLLVMFuncDef(t, ir, "demo.SpawnTarget$coro")
	if !strings.Contains(spawnIR, "icmp ne ptr") && !strings.Contains(spawnIR, "icmp eq ptr") {
		t.Fatalf("expected SpawnTarget to null-check handle returned by tailForward ReadFull$coro:\n%s", spawnIR)
	}
}

func TestCoroSyncRampConstZeroIsBranchScoped(t *testing.T) {
	// Ensure a 0-valued Phi in one If branch cannot leak into a sibling branch.
	src := `package demo
import _ "unsafe"

//go:linkname yieldNow llgo.coroYield
func yieldNow()

func SlowSlice(b []byte) []byte {
	yieldNow()
	return b
}

func PickSlice(cond bool, b []byte, off int) []byte {
	var i int
	if cond {
		i = 0
	} else {
		i = off
	}
	if i == 0 && len(b) > 0 {
		return b[i:]
	}
	if i > 0 && i <= len(b) {
		return b[i:]
	}
	return SlowSlice(b)
}

func Root(cond bool, b []byte, off int) []byte {
	return PickSlice(cond, b, off)
}
`
	ssaPkg, _, files := buildGoSSAPkg(t, src)
	prog, compilation, _, llpkg := compileCoroSyncRampTestPackages(
		t, []EmissionPackage{{SSA: ssaPkg, Files: files}}, ssaPkg.Func("Root"), 0, coro.DynamicUnknownOnly,
	)
	defer prog.Dispose()

	ramp := sealedSyncRampForTest(t, compilation, ssaPkg.Func("PickSlice"))
	if ramp == nil || ramp.kind != coroSyncRampTailSlow {
		t.Fatalf("PickSlice ramp=%+v, want TailSlow", ramp)
	}
	ir := llpkg.Module().String()
	rampIR := extractLLVMFuncDef(t, ir, "demo.PickSlice$coro")
	// The cond==false arm (i = off) must NOT inherit cond==true's zero-int proof:
	// its non-identity b[i:] must bail to $coro.slow instead of emitting a raw-plain
	// NewSlice3Bounds call or treating b[off:] as b[0:].
	if strings.Contains(rampIR, "NewSlice") {
		t.Fatalf("sync-fast ramp must never emit raw-plain NewSlice calls:\n%s", rampIR)
	}
	if !strings.Contains(rampIR, "PickSlice$coro.slow") {
		t.Fatalf("expected non-zero sibling branch to bail to PickSlice$coro.slow:\n%s", rampIR)
	}
}

func TestCoroSyncFastTreeCoversLiveInsRejectsUndefinedPath(t *testing.T) {
	ssaPkg, _, _ := buildGoSSAPkg(t, `package demo
func F(cond bool, x int) int {
	if cond {
		y := x + 1
		return y
	}
	return x
}
`)
	fn := ssaPkg.Func("F")
	// Locate the `x + 1` BinOp in the true arm.
	var yVal ssa.Value
	for _, blk := range fn.Blocks {
		for _, ins := range blk.Instrs {
			if bin, ok := ins.(*ssa.BinOp); ok {
				yVal = bin
			}
		}
	}
	if yVal == nil {
		t.Fatal("failed to locate x+1 BinOp")
	}
	// Construct a fast tree where the false arm exits with tag=1 requiring yVal
	// (which is only defined in the true arm): coverage check MUST reject it.
	root := &coroSyncFastNode{
		block:     fn.Blocks[0],
		endInstr:  -1,
		exitKind:  coroSyncFastExitIf,
		trueNode:  &coroSyncFastNode{block: fn.Blocks[1], endInstr: -1, exitKind: coroSyncFastExitReturn},
		falseNode: &coroSyncFastNode{exitKind: coroSyncFastExitResumeSlow, resumeTag: 1},
	}
	liveByTag := map[uint8]map[ssa.Value]bool{1: {yVal: true}}
	if coroSyncFastTreeCoversLiveIns(root, liveByTag) {
		t.Fatal("expected coroSyncFastTreeCoversLiveIns to reject path missing required live-in")
	}
	// If tag=1 is reached only AFTER defining yVal, coverage check MUST pass.
	covered := &coroSyncFastNode{
		block:    fn.Blocks[0],
		endInstr: -1,
		exitKind: coroSyncFastExitJump,
		nextNode: &coroSyncFastNode{
			block:    fn.Blocks[1],
			endInstr: -1,
			exitKind: coroSyncFastExitJump,
			nextNode: &coroSyncFastNode{exitKind: coroSyncFastExitResumeSlow, resumeTag: 1},
		},
	}
	if !coroSyncFastTreeCoversLiveIns(covered, liveByTag) {
		t.Fatal("expected coroSyncFastTreeCoversLiveIns to accept path defining required live-in")
	}
}

func TestCoroSyncRampPostCASNilGuardDisablesTryHelper(t *testing.T) {
	// Regression for os.(*processHandle).acquire: a post-CAS FieldAddr nil guard
	// emits handle-returning outcome propagation, which is valid in $coro (ptr)
	// but must NOT publish a bool-returning $coro.try helper.
	program := newEmissionTestProgram()
	program.ssa.CreatePackage(types.Unsafe, nil, nil, true)
	atomicPkg := program.addPackage(t, "sync/atomic", `package atomic
import _ "unsafe"
type Int32 struct { v int32 }
//go:linkname LoadInt32 llgo.atomicLoad
func LoadInt32(*int32) int32
//go:linkname CompareAndSwapInt32 llgo.atomicCmpXchgOK
func CompareAndSwapInt32(*int32, int32, int32) bool
func (x *Int32) Load() int32                         { return LoadInt32(&x.v) }
func (x *Int32) CompareAndSwap(old, new int32) bool { return CompareAndSwapInt32(&x.v, old, new) }
`)
	demoPkg := program.addPackage(t, "demo", `package demo
import (
	"sync/atomic"
	_ "unsafe"
)
//go:linkname yieldNow llgo.coroYield
func yieldNow()
type Handle struct {
	id   uintptr
	refs atomic.Int32
}
func (h *Handle) acquire() (uintptr, bool) {
	for {
		refs := h.refs.Load()
		if refs < 0 {
			yieldNow()
			continue
		}
		if refs == 0 {
			return 0, false
		}
		if h.refs.CompareAndSwap(refs, refs+1) {
			return h.id, true
		}
	}
}
func Root(h *Handle) (uintptr, bool) { return h.acquire() }
`)
	program.ssa.Build()
	inputs := []EmissionPackage{
		{SSA: atomicPkg.ssa, Files: []*ast.File{atomicPkg.file}},
		{SSA: demoPkg.ssa, Files: []*ast.File{demoPkg.file}},
	}
	// compileCoroSyncRampTestPackages runs llvm.VerifyModule automatically.
	prog, compilation, _, _ := compileCoroSyncRampTestPackages(
		t, inputs, demoPkg.ssa.Func("Root"), 0, coro.DynamicUnknownOnly,
	)
	defer prog.Dispose()

	hType := demoPkg.types.Scope().Lookup("Handle").Type().(*types.Named)
	acqFn := program.ssa.FuncValue(hType.Method(0))
	ramp := sealedSyncRampForTest(t, compilation, acqFn)
	if ramp == nil || ramp.kind != coroSyncRampTryBailout {
		t.Fatalf("Handle.acquire ramp=%+v, want TryBailout", ramp)
	}
	if ramp.hasTryHelper {
		t.Fatalf("Handle.acquire must not publish .try when post-CAS nil guard exists")
	}
}

func TestCoroSyncRampResumeSlowDebugSymbolsDominate(t *testing.T) {
	// Regression for syscall.(*SockaddrUnix).sockaddr under -g (GlobalDebug +
	// Debug + DebugSymbols): aggregate DebugRef operands in blocks reachable
	// from slow-resume entry edges must load from live-in slots (or be skipped
	// if not live-in), never emitting non-dominating uses in $coro.slow.
	src := `package demo

type Addr struct {
	Name string
	Len  byte
	Buf  [104]byte
}

func (a *Addr) encode() (int, bool) {
	name := a.Name
	n := len(name)
	if n >= len(a.Buf) {
		return 0, false
	}
	if n == 0 {
		return 0, true
	}
	a.Len = byte(3 + n)
	for i := 0; i < n; i++ {
		a.Buf[i] = name[i]
	}
	return n, true
}

func Root(a *Addr) (int, bool) {
	return a.encode()
}
`
	ssaPkg, _, files := buildGoSSAPkgWithMode(t, src, ssa.SanityCheckFunctions|ssa.InstantiateGenerics|ssa.GlobalDebug)
	prog, compilation, _, _ := compileCoroSyncRampTestPackagesWithOptions(
		t,
		[]EmissionPackage{{SSA: ssaPkg, Files: files}},
		ssaPkg.Func("Root"),
		0,
		coro.DynamicUnknownOnly,
		PackageOptions{
			FrontendOptions: Options{
				Debug:        true,
				DebugSymbols: true,
			},
			FrontendOptionsSet: true,
		},
	)
	defer prog.Dispose()

	addrType := ssaPkg.Pkg.Scope().Lookup("Addr").Type().(*types.Named)
	encFn := ssaPkg.Prog.FuncValue(addrType.Method(0))
	ramp := sealedSyncRampForTest(t, compilation, encFn)
	if ramp == nil || ramp.kind != coroSyncRampResumeSlow {
		t.Fatalf("Addr.encode ramp=%+v, want ResumeSlow", ramp)
	}
}

func TestCoroSyncRampBoxedSingleAwaitAndDemandBoxHelper(t *testing.T) {
	// Verify that a SingleAwait caller whose awaited TailSlow callee returns
	// results uses the demand-emitted .box helper (and supports dynamic s[low:]
	// with bounds checking), while an unreferenced TailSlow callee emits no .box.
	program := newEmissionTestProgram()
	program.ssa.CreatePackage(types.Unsafe, nil, nil, true)
	demoPkg := program.addPackage(t, "demo", `package demo
import _ "unsafe"

//go:linkname yieldNow llgo.coroYield
func yieldNow()

func slowWrite(b []byte) (int, int) {
	yieldNow()
	return len(b), 0
}

func tryWrite(b []byte) (int, int, bool) {
	if len(b) > 0 && b[0] == 1 {
		return len(b), 0, true
	}
	return 0, 0, false
}

func WriteUsed(b []byte) (int, int) {
	if n, e, ok := tryWrite(b); ok {
		return n, e
	}
	return slowWrite(b)
}

func WriteUnused(b []byte) (int, int) {
	if n, e, ok := tryWrite(b); ok {
		return n, e
	}
	return slowWrite(b)
}

func WriteFull(b []byte) int {
	total := 0
	for len(b) != 0 {
		n, e := WriteUsed(b)
		if e != 0 {
			return -1
		}
		total += n
		b = b[n:]
	}
	return total
}

func Root(b []byte) int {
	a := WriteFull(b)
	c, _ := WriteUnused(b)
	return a + c
}
`)
	program.ssa.Build()
	inputs := []EmissionPackage{{SSA: demoPkg.ssa, Files: []*ast.File{demoPkg.file}}}
	prog, compilation, _, pkg := compileCoroSyncRampTestPackages(
		t, inputs, demoPkg.ssa.Func("Root"), 0, coro.DynamicUnknownOnly,
	)
	defer prog.Dispose()

	usedRamp := sealedSyncRampForTest(t, compilation, demoPkg.ssa.Func("WriteUsed"))
	if usedRamp == nil || usedRamp.kind != coroSyncRampTailSlow || !usedRamp.hasBoxHelper {
		t.Fatalf("WriteUsed ramp=%+v, want TailSlow with demand-marked hasBoxHelper", usedRamp)
	}
	unusedRamp := sealedSyncRampForTest(t, compilation, demoPkg.ssa.Func("WriteUnused"))
	if unusedRamp == nil || unusedRamp.kind != coroSyncRampTailSlow || unusedRamp.hasBoxHelper {
		t.Fatalf("WriteUnused ramp=%+v, want TailSlow without unreferenced .box helper", unusedRamp)
	}
	fullRamp := sealedSyncRampForTest(t, compilation, demoPkg.ssa.Func("WriteFull"))
	if fullRamp == nil || fullRamp.kind != coroSyncRampSingleAwait || !fullRamp.singleAwaitBoxed {
		t.Fatalf("WriteFull ramp=%+v, want boxed SingleAwait", fullRamp)
	}

	irText := pkg.Module().String()
	if !strings.Contains(irText, `"demo.WriteUsed$coro.box"`) {
		t.Fatalf("module did not emit demand-marked WriteUsed$coro.box:\n%s", irText)
	}
	if strings.Contains(irText, `"demo.WriteUnused$coro.box"`) {
		t.Fatalf("module unexpectedly emitted unreferenced WriteUnused$coro.box")
	}
}

func TestCoroSyncRampExcludesDeclaredInitAndWeightedSpawnPreempt(t *testing.T) {
	// Declared init#1 must never be split into a TailSlow sync ramp, and spawn
	// post-safepoints must charge coroSpawnPreemptWeight against the countdown.
	program := newEmissionTestProgram()
	program.ssa.CreatePackage(types.Unsafe, nil, nil, true)
	demoPkg := program.addPackage(t, "demo", `package demo
import _ "unsafe"

//go:linkname yieldNow llgo.coroYield
func yieldNow()

var sink int

func plain() { sink++ }
func worker() { yieldNow() }

func init() {
	plain()
	worker()
}

func SpawnMany(n int) {
	for i := 0; i < n; i++ {
		go worker()
	}
}
`)
	program.ssa.Build()
	inputs := []EmissionPackage{{SSA: demoPkg.ssa, Files: []*ast.File{demoPkg.file}}}
	prog, compilation, _, pkg := compileCoroSyncRampTestPackages(
		t, inputs, demoPkg.ssa.Func("SpawnMany"), 0, coro.DynamicUnknownOnly,
	)
	defer prog.Dispose()

	if initFn := demoPkg.ssa.Func("init#1"); initFn != nil {
		if ramp := sealedSyncRampForTest(t, compilation, initFn); ramp != nil {
			t.Fatalf("declared init#1 unexpectedly received sync ramp %+v", ramp)
		}
	}
	spawnIR := requireCoroPhysicalFunction(t, pkg.Module(), "demo.SpawnMany").String()
	if !strings.Contains(spawnIR, "sub i32 ") || !strings.Contains(spawnIR, ", 16") {
		t.Fatalf("SpawnMany did not emit weighted spawn preemption decrement (16):\n%s", spawnIR)
	}
}

func TestCoroSyncRampLocalNilGuardAfterCallUsesNilNewTrace(t *testing.T) {
	// A nil-pointer guard that trips inside a sync-fast ramp after a prior call
	// (mutated=true, e.g. `for range *nilPtrFunc()`) originates a new panic in
	// this frame rather than propagating an existing child panic trace. It must
	// pass coroOutcomePropagateLocalFaultNil (8) so the shared helper appends a
	// NilNew logical trace instead of NilPropagate.
	program := newEmissionTestProgram()
	program.ssa.CreatePackage(types.Unsafe, nil, nil, true)
	demoPkg := program.addPackage(t, "demo", `package demo
import _ "unsafe"

//go:linkname yieldNow llgo.coroYield
func yieldNow()

var sink int

//go:noinline
func nilPtr() *[4]int {
	sink++
	return nil
}

func RangeNilAfterCall(flag bool) {
	if flag {
		yieldNow()
	}
	for range *nilPtr() {
	}
}

func Entry(flag bool) {
	RangeNilAfterCall(flag)
}
`)
	program.ssa.Build()
	inputs := []EmissionPackage{{SSA: demoPkg.ssa, Files: []*ast.File{demoPkg.file}}}
	prog, compilation, _, pkg := compileCoroSyncRampTestPackages(
		t, inputs, demoPkg.ssa.Func("Entry"), 0, coro.DynamicUnknownOnly,
	)
	defer prog.Dispose()

	ramp := sealedSyncRampForTest(t, compilation, demoPkg.ssa.Func("RangeNilAfterCall"))
	if ramp == nil || !ramp.returnsNullableHandle() {
		t.Fatalf("RangeNilAfterCall ramp=%+v, want nullable-handle sync ramp", ramp)
	}
	irText := pkg.Module().String()
	if !strings.Contains(irText, coroOutcomePropagateHelperNameV1) {
		t.Fatalf("expected %s helper in module IR", coroOutcomePropagateHelperNameV1)
	}
	if !strings.Contains(irText, "i32 8, label") {
		t.Fatalf("expected local nil fault case (i32 8) in %s:\n%s", coroOutcomePropagateHelperNameV1, irText)
	}
}

func TestCoroSyncRampBoundedLoopLeafEarlyExitFullScanAndCallerPreempt(t *testing.T) {
	program := newEmissionTestProgram()
	program.ssa.CreatePackage(types.Unsafe, nil, nil, true)
	demoPkg := program.addPackage(t, "demo", `package demo

var escapeTbl = [128]uint8{'"': 1, '\\': 1, '<': 1, '>': 1, '&': 1}
var tokenTbl = [128]bool{'a': true, 'b': true, '-': true}

func ConsumeWS(b []byte) (n int) {
	for len(b) > n && (b[n] == ' ' || b[n] == '\t' || b[n] == '\r' || b[n] == '\n') {
		n++
	}
	return n
}

func ConsumeStr(b []byte) (n int) {
	if len(b) > 0 && b[0] == '"' {
		n++
		for len(b) > n && b[n] < 128 && escapeTbl[b[n]] == 0 {
			n++
		}
		if uint(len(b)) > uint(n) && b[n] == '"' {
			n++
			return n
		}
	}
	return 0
}

func ValidName(v string) bool {
	if len(v) == 0 {
		return false
	}
	for i := 0; i < len(v); i++ {
		if v[i] >= 128 || !tokenTbl[v[i]] {
			return false
		}
	}
	return true
}

func SumBytes(b []byte) int {
	sum := 0
	for i := 0; i < len(b); i++ {
		sum += int(b[i])
	}
	return sum
}

func RepeatedShortCaller(b []byte, limit int) int {
	total := 0
	for i := 0; i < limit; i++ {
		total += ConsumeWS(b)
	}
	return total
}

func Root(b []byte, s string, limit int) int {
	n := ConsumeWS(b) + ConsumeStr(b) + SumBytes(b) + RepeatedShortCaller(b, limit)
	if ValidName(s) {
		n++
	}
	return n
}
`)
	program.ssa.Build()
	inputs := []EmissionPackage{{SSA: demoPkg.ssa, Files: []*ast.File{demoPkg.file}}}

	// Use production DefaultMaxPlainInstructions (128) via SSAConfig.MaxPlainInstructions == 0.
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverseWithOptions(prog, nil, inputs, EmissionUniverseOptions{
		CoroProgramPredicates: coro.ProgramPredicateRequirements{
			Mask: coro.ProgramPredicateMask(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	root := demoPkg.ssa.Func("Root")
	ssaUniverse, err := coro.NewSSAEmissionUniverse(root.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(root.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		DynamicResolution:    coro.DynamicUnknownOnly,
		MaxPlainInstructions: 0, // production default: 128
		OutcomeMode:          coro.OutcomeExplicitStatus,
		ClassifyLocalBody:    universe.CoroLocalBodyFacts,
		ClassifyLoweredCalls: universe.CoroLoweredCalls,
	})
	if err != nil {
		t.Fatal(err)
	}
	compilation := &Compilation{
		CoroPlan:         plan,
		EmissionUniverse: universe,
		PanicABI:         coro.PanicExplicitStatusABIV0,
	}
	enableCoroPreemptCompilation(compilation)
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, demoPkg.ssa, []*ast.File{demoPkg.file}, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
		t.Fatalf("LLVM verify failed: %v\n%s", err, pkg.Module().String())
	}

	for _, tc := range []struct {
		name           string
		wantEntryGuard coroSyncLoopEntryGuardKind
	}{
		{"ConsumeWS", coroSyncLoopEntryGuardNone},
		{"ConsumeStr", coroSyncLoopEntryGuardNone},
		{"ValidName", coroSyncLoopEntryGuardNone},
		{"SumBytes", coroSyncLoopEntryGuardLen},
	} {
		fn := demoPkg.ssa.Func(tc.name)
		ramp := sealedSyncRampForTest(t, compilation, fn)
		if ramp == nil || ramp.kind != coroSyncRampBoundedLoop || ramp.boundedLoop == nil {
			t.Fatalf("%s ramp=%+v, want coroSyncRampBoundedLoop", tc.name, ramp)
		}
		if ramp.boundedLoop.maxBackedges == 0 {
			t.Fatalf("%s maxBackedges=0, want >= 1", tc.name)
		}
		if ramp.boundedLoop.entryGuardKind != tc.wantEntryGuard {
			t.Fatalf("%s entryGuardKind=%d, want %d", tc.name, ramp.boundedLoop.entryGuardKind, tc.wantEntryGuard)
		}
		if !ramp.returnsNullableHandle() || !ramp.needsSlowSplit {
			t.Fatalf("%s ramp must return nullable handle and split .slow", tc.name)
		}
	}

	irText := pkg.Module().String()
	for _, name := range []string{"ConsumeWS", "ConsumeStr", "ValidName", "SumBytes"} {
		rampIR := extractLLVMFuncDef(t, irText, "demo."+name+"$coro")
		if strings.Contains(rampIR, "__llgo_coro_frame_alloc") {
			t.Fatalf("%s$coro fast ramp unexpectedly allocates a coroutine frame:\n%s", name, rampIR)
		}
		if !strings.Contains(rampIR, `"demo.`+name+`$coro.slow"`) {
			t.Fatalf("%s$coro fast ramp missing .slow fallback:\n%s", name, rampIR)
		}
		slowIR := extractLLVMFuncDef(t, irText, "demo."+name+"$coro.slow")
		if !strings.Contains(slowIR, coroPreemptPollHookV1) {
			t.Fatalf("%s$coro.slow lost loop preemption poll:\n%s", name, slowIR)
		}
	}

	// Verify that RepeatedShortCaller's looping coroutine body (either $coro or
	// $coro.slow when SingleAwait peels iteration 0) retains its preemption
	// countdown and that the child==nil sync-fast return edge NEVER resets the
	// caller's countdown.
	callerFn := pkg.Module().NamedFunction("demo.RepeatedShortCaller$coro.slow")
	if callerFn.IsNil() {
		callerFn = requireCoroPhysicalFunction(t, pkg.Module(), "demo.RepeatedShortCaller")
	}
	callerIR := callerFn.String()
	if !strings.Contains(callerIR, coroPreemptPollHookV1) {
		t.Fatalf("RepeatedShortCaller lost caller preemption poll:\n%s", callerIR)
	}
	var countdown llvm.Value
	var syncEdges []llvm.BasicBlock
	for _, blk := range callerFn.BasicBlocks() {
		for ins := blk.FirstInstruction(); !ins.IsNil(); ins = llvm.NextInstruction(ins) {
			if ins.InstructionOpcode() == llvm.Store && !ins.Operand(0).IsAConstantInt().IsNil() &&
				ins.Operand(0).ZExtValue() == coroPreemptCheckpointStride {
				countdown = ins.Operand(1)
			}
			if ins.InstructionOpcode() == llvm.Call && ins.CalledValue().Name() == "demo.ConsumeWS$coro" {
				if term := blk.LastInstruction(); !term.IsNil() && term.InstructionOpcode() == llvm.Br && term.SuccessorsCount() == 2 {
					// icmp ne ptr %child, null -> succ(0)=async, succ(1)=sync
					syncEdges = append(syncEdges, term.Successor(1))
				}
			}
		}
	}
	if countdown.IsNil() || len(syncEdges) == 0 {
		t.Fatalf("failed to locate caller countdown or sync-fast edge in RepeatedShortCaller:\n%s", callerIR)
	}
	for _, syncBlk := range syncEdges {
		if coroInlineReturnReachesCountdownReset(syncBlk, countdown) {
			t.Fatalf("sync-fast return of bounded-loop callee must NOT reset caller preemptCountdown:\n%s", callerIR)
		}
	}
}

func TestCoroSyncRampBoundedLoopLeafRejections(t *testing.T) {
	program := newEmissionTestProgram()
	program.ssa.CreatePackage(types.Unsafe, nil, nil, true)
	demoPkg := program.addPackage(t, "demo", `package demo

var sink int

//go:noinline
func helper(x byte) int { return int(x) + 1 }

func MutatingLoop(b []byte) int {
	for i := 0; i < len(b); i++ {
		b[i] = 0
	}
	return len(b)
}

func CallingLoop(b []byte) int {
	s := 0
	for i := 0; i < len(b); i++ {
		s += helper(b[i])
	}
	return s
}

func NestedLoop(b []byte) int {
	s := 0
	for i := 0; i < len(b); i++ {
		for j := 0; j < len(b); j++ {
			s += int(b[j])
		}
	}
	return s
}

func NonCanonicalLoop(n int) int {
	c := 0
	for n > 0 {
		n >>= 1
		c++
	}
	return c
}

//go:noinline
func NoInlineLoop(b []byte) (n int) {
	for len(b) > n && b[n] == ' ' {
		n++
	}
	return n
}

func GlobalMutLoop(b []byte) int {
	for i := 0; i < len(b); i++ {
		sink += int(b[i])
	}
	return sink
}

func Root(b []byte, n int) int {
	return MutatingLoop(b) + CallingLoop(b) + NestedLoop(b) + NonCanonicalLoop(n) + NoInlineLoop(b) + GlobalMutLoop(b)
}
`)
	program.ssa.Build()
	inputs := []EmissionPackage{{SSA: demoPkg.ssa, Files: []*ast.File{demoPkg.file}}}
	prog, compilation, _, _ := compileCoroSyncRampTestPackages(
		t, inputs, demoPkg.ssa.Func("Root"), 0, coro.DynamicUnknownOnly,
	)
	defer prog.Dispose()

	for _, name := range []string{
		"MutatingLoop",
		"CallingLoop",
		"NestedLoop",
		"NonCanonicalLoop",
		"NoInlineLoop",
		"GlobalMutLoop",
	} {
		if ramp := sealedSyncRampForTest(t, compilation, demoPkg.ssa.Func(name)); ramp != nil {
			t.Fatalf("%s unexpectedly received sync ramp %+v, want nil", name, ramp)
		}
	}

	// Verify budget enforcement: with MaxPlainInstructions=8, even a clean loop
	// leaf cannot fit 2 passes inside the budget and must be rejected.
	t.Run("budget-too-small-rejected", func(t *testing.T) {
		p2 := newEmissionTestProgram()
		p2.ssa.CreatePackage(types.Unsafe, nil, nil, true)
		pkg2 := p2.addPackage(t, "demo2", `package demo2
func ConsumeWS(b []byte) (n int) {
	for len(b) > n && (b[n] == ' ' || b[n] == '\t') {
		n++
	}
	return n
}
func Root(b []byte) int { return ConsumeWS(b) }
`)
		p2.ssa.Build()
		prog2 := newLLSSAProg(t)
		defer prog2.Dispose()
		u2, err := PrepareEmissionUniverseWithOptions(prog2, nil, []EmissionPackage{{SSA: pkg2.ssa, Files: []*ast.File{pkg2.file}}}, EmissionUniverseOptions{
			CoroProgramPredicates: coro.ProgramPredicateRequirements{Mask: coro.ProgramPredicateMask()},
		})
		if err != nil {
			t.Fatal(err)
		}
		root2 := pkg2.ssa.Func("Root")
		su2, err := coro.NewSSAEmissionUniverse(root2.Prog, u2.Functions())
		if err != nil {
			t.Fatal(err)
		}
		fids := u2.FunctionIDConfig()
		fids.CoroABI = coro.PhysicalABIV1
		fids.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
		fids.ArchiveReady = true
		plan2, err := coro.AnalyzeSSA(root2.Prog, coro.Roots{{Function: root2, Demand: coro.AsyncDemand}}, coro.SSAConfig{
			EmissionUniverse:     su2,
			FunctionIDs:          fids,
			DynamicResolution:    coro.DynamicUnknownOnly,
			MaxPlainInstructions: 8,
			OutcomeMode:          coro.OutcomeExplicitStatus,
			ClassifyLocalBody:    u2.CoroLocalBodyFacts,
			ClassifyLoweredCalls: u2.CoroLoweredCalls,
		})
		if err != nil {
			t.Fatal(err)
		}
		comp2 := &Compilation{CoroPlan: plan2, EmissionUniverse: u2, PanicABI: coro.PanicExplicitStatusABIV0}
		enableCoroPreemptCompilation(comp2)
		if _, _, err := NewPackageExWithEmbedOptions(prog2, nil, nil, nil, pkg2.ssa, []*ast.File{pkg2.file}, goembed.VarMap{}, PackageOptions{Compilation: comp2}); err != nil {
			t.Fatal(err)
		}
		if ramp := sealedSyncRampForTest(t, comp2, pkg2.ssa.Func("ConsumeWS")); ramp != nil {
			t.Fatalf("ConsumeWS under tiny budget=8 unexpectedly got sync ramp %+v", ramp)
		}
	})
}

func TestCoroSyncRampStageLExtensions(t *testing.T) {
	program := newEmissionTestProgram()
	program.ssa.CreatePackage(types.Unsafe, nil, nil, true)
	demoPkg := program.addPackage(t, "demo", `package demo

import "unsafe"

//go:linkname yieldNow llgo.coroYield
func yieldNow()

type Box struct {
	V int
}

type Stringer interface {
	String() string
}

func (b *Box) String() string { return "box" }

type Pair struct {
	A int
	B byte
}

var sentinelErr error
var escTbl = [128]uint8{'"': 1, '\\': 1}

func slowOpaque() unsafe.Pointer {
	yieldNow()
	return nil
}

// 1. Pointer-bitcast tail call (*Box)(slowOpaque()).
func BitcastTail(fast bool, b *Box) *Box {
	if fast {
		return b
	}
	return (*Box)(slowOpaque())
}

// 2. Pure plain helper + pre-mutation Index/Slice + local struct return.
func pureTag(x byte) byte {
	return x ^ 1
}

func SliceAndStruct(b []byte, idx int, slow bool) (Pair, []byte) {
	if slow {
		yieldNow()
	}
	c := pureTag(b[idx])
	sub := b[idx : idx+1]
	return Pair{A: idx, B: c}, sub
}

// 3. Static non-empty MakeInterface (*Box -> Stringer).
func StaticIface(b *Box, slow bool) Stringer {
	if slow {
		yieldNow()
	}
	return b
}

// 4. Pre-defer early return with DirectTypeAssert (models cancelCtx.Done).
func unlockSlow(p *int) {
	yieldNow()
	*p = 0
}

func PreDeferTypeAssert(v any, mu *int) *Box {
	if v != nil {
		if b, ok := v.(*Box); ok {
			return b
		}
	}
	*mu = 1
	defer unlockSlow(mu)
	yieldNow()
	return nil
}

// 5. BoundedLoop .try composition + statically-nil interface Phi compare.
func ScanSimple(b []byte) (n int) {
	if len(b) > 0 && b[0] == '"' {
		n++
		for len(b) > n && b[n] < 128 && escTbl[b[n]] == 0 {
			n++
		}
		if uint(len(b)) > uint(n) && b[n] == '"' {
			n++
			return n
		}
	}
	return 0
}

func slowFallback(b []byte, pos int) (int, error) {
	yieldNow()
	return pos, sentinelErr
}

func ComposeTryAndNilIfacePhi(b []byte, pos int) (int, error) {
	var n int
	var err error
	if b[pos] == '"' {
		if n = ScanSimple(b[pos:]); n == 0 {
			return slowFallback(b, pos)
		}
	} else {
		return slowFallback(b, pos)
	}
	if err == sentinelErr {
		yieldNow()
		return pos, err
	}
	return pos + n, err
}

// 6. Deferred value-returning sync-fast call passes non-null stack result slot.
func FastRetInt(x *int, slow bool) int {
	if slow {
		yieldNow()
	}
	if x == nil {
		return 0
	}
	*x++
	return *x
}

func DeferDiscardCaller(x *int) {
	defer FastRetInt(x, false)
	yieldNow()
}

func Root(b []byte, box *Box, v any, mu *int, x *int) int {
	_ = BitcastTail(true, box)
	p, sub := SliceAndStruct(b, 0, false)
	s := StaticIface(box, false).String()
	_ = PreDeferTypeAssert(v, mu)
	n, _ := ComposeTryAndNilIfacePhi(b, 0)
	DeferDiscardCaller(x)
	return p.A + len(sub) + n + len(s)
}
`)
	program.ssa.Build()
	inputs := []EmissionPackage{{SSA: demoPkg.ssa, Files: []*ast.File{demoPkg.file}}}
	prog, compilation, _, pkg := compileCoroSyncRampTestPackages(
		t, inputs, demoPkg.ssa.Func("Root"), 0, coro.DynamicCHAClosed,
	)
	defer prog.Dispose()

	if ramp := sealedSyncRampForTest(t, compilation, demoPkg.ssa.Func("BitcastTail")); ramp == nil || ramp.kind != coroSyncRampTailSlow {
		t.Fatalf("BitcastTail ramp=%+v, want coroSyncRampTailSlow", ramp)
	}
	if ramp := sealedSyncRampForTest(t, compilation, demoPkg.ssa.Func("SliceAndStruct")); ramp == nil || !ramp.returnsNullableHandle() {
		t.Fatalf("SliceAndStruct ramp=%+v, want nullable-handle sync ramp", ramp)
	}
	if ramp := sealedSyncRampForTest(t, compilation, demoPkg.ssa.Func("StaticIface")); ramp == nil || !ramp.returnsNullableHandle() {
		t.Fatalf("StaticIface ramp=%+v, want nullable-handle sync ramp", ramp)
	}
	if ramp := sealedSyncRampForTest(t, compilation, demoPkg.ssa.Func("PreDeferTypeAssert")); ramp == nil || !ramp.returnsNullableHandle() || len(ramp.slowTargets) != 0 {
		t.Fatalf("PreDeferTypeAssert ramp=%+v, want pre-defer sync ramp with no slowTargets", ramp)
	}
	if ramp := sealedSyncRampForTest(t, compilation, demoPkg.ssa.Func("ScanSimple")); ramp == nil || ramp.kind != coroSyncRampBoundedLoop || !ramp.hasTryHelper {
		t.Fatalf("ScanSimple ramp=%+v, want coroSyncRampBoundedLoop with hasTryHelper", ramp)
	}
	if ramp := sealedSyncRampForTest(t, compilation, demoPkg.ssa.Func("ComposeTryAndNilIfacePhi")); ramp == nil || ramp.kind != coroSyncRampTailSlow {
		t.Fatalf("ComposeTryAndNilIfacePhi ramp=%+v, want coroSyncRampTailSlow", ramp)
	}

	irText := pkg.Module().String()
	composeIR := extractLLVMFuncDef(t, irText, "demo.ComposeTryAndNilIfacePhi$coro")
	if !strings.Contains(composeIR, `"demo.ScanSimple$coro.try"`) {
		t.Fatalf("ComposeTryAndNilIfacePhi$coro did not compose ScanSimple$coro.try:\n%s", composeIR)
	}
	if strings.Contains(composeIR, "__llgo_coro_frame_alloc") {
		t.Fatalf("ComposeTryAndNilIfacePhi$coro unexpectedly allocates a coroutine frame:\n%s", composeIR)
	}

	// Verify DeferDiscardCaller passes a non-null stack result slot to FastRetInt$coro.
	deferIR := requireCoroPhysicalFunction(t, pkg.Module(), "demo.DeferDiscardCaller").String()
	if !strings.Contains(deferIR, `"demo.FastRetInt$coro"`) {
		t.Fatalf("DeferDiscardCaller did not call FastRetInt$coro:\n%s", deferIR)
	}
	if strings.Contains(deferIR, `"demo.FastRetInt$coro"(ptr %0, ptr null,`) {
		t.Fatalf("DeferDiscardCaller passed null result slot to value-returning sync-fast callee:\n%s", deferIR)
	}
}
