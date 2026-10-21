// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package cl

import (
	"fmt"
	"go/ast"
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

func TestCoroPollFDFastRequiresExactWrapperAndMutex(t *testing.T) {
	mu, err := os.ReadFile("testdata/poll_mutex_go127.txt")
	if err != nil {
		t.Fatal(err)
	}
	fd, err := os.ReadFile("testdata/poll_fd_go127.txt")
	if err != nil {
		t.Fatal(err)
	}
	fixture := string(mu) + "\n" + string(fd)[strings.Index(string(fd), "type FD struct"):]
	for _, method := range []string{"readLock", "readUnlock", "writeLock", "writeUnlock", "incref", "decref"} {
		for _, change := range []string{"verified", "receiver", "layout", "mutex-type", "readlock", "writeLock", "waitLock", "nil", "error", "source", "noinline", "mutex-source", "domain", "missing-classification", "rebound-mutex", "missing-canonical"} {
			t.Run(method+"/"+change, func(t *testing.T) {
				program := newEmissionTestProgram()
				program.ssa.CreatePackage(types.Unsafe, nil, nil, true)
				atomic := program.addPackage(t, "sync/atomic", "package atomic\nimport _ \"unsafe\"\n//go:linkname LoadUint64 llgo.atomicLoad\nfunc LoadUint64(*uint64)uint64\n//go:linkname CompareAndSwapUint64 llgo.atomicCmpXchgOK\nfunc CompareAndSwapUint64(*uint64,uint64,uint64)bool\n")
				source := fixture
				header := "func (fd *FD) " + method
				switch change {
				case "receiver":
					source = strings.Replace(source, header, "func (receiver *FD) "+method, 1) + "\nvar fd *FD\n"
				case "layout":
					source = strings.Replace(source, "fdmu fdMutex", "padding uint64; fdmu fdMutex", 1)
				case "mutex-type":
					source = strings.ReplaceAll(source, "fdMutex", "otherMutex")
				case "readlock":
					source = strings.Replace(source, "readlock  = true", "readlock  = false", 1)
				case "writeLock":
					source = strings.Replace(source, "writeLock = false", "writeLock = true", 1)
				case "waitLock":
					source = strings.Replace(source, "waitLock  = true", "waitLock  = false", 1)
				case "nil":
					source += "\nvar nil error\n"
				case "error":
					source += "\ntype error interface { DifferentError() }\n"
				case "source":
					start := strings.Index(source, header)
					brace := start + strings.Index(source[start:], "{")
					source = source[:brace+1] + "\nruntime_Semrelease(&fd.fdmu.rsema)\n" + source[brace+1:]
				case "noinline":
					source = strings.Replace(source, header, "//go:noinline\n"+header, 1)
				case "mutex-source":
					source = strings.ReplaceAll(source, "old := atomic.LoadUint64(&mu.state)", "old := atomic.LoadUint64(&mu.state) ^ 1")
				}
				source += "\nfunc replacement() {}\n"
				pkg := program.addPackage(t, "internal/poll", source)
				program.ssa.Build()
				prog := newLLSSAProg(t)
				defer prog.Dispose()
				values := coro.ProgramCapabilities(0)
				if change == "domain" {
					values = coro.ExecutionDomainProgramCapability()
				}
				universe, err := PrepareEmissionUniverseWithOptions(prog, nil, []EmissionPackage{{SSA: atomic.ssa, Files: []*ast.File{atomic.file}}, {SSA: pkg.ssa, Files: []*ast.File{pkg.file}}}, EmissionUniverseOptions{CoroProgramPredicates: coro.ProgramPredicateRequirements{Mask: coro.ProgramPredicateMask(), Values: values}})
				if err != nil {
					t.Fatal(err)
				}
				named := pkg.types.Scope().Lookup("FD").Type().(*types.Named)
				var fn *ssa.Function
				for i := 0; i < named.NumMethods(); i++ {
					if named.Method(i).Name() == method {
						fn = program.ssa.FuncValue(named.Method(i))
					}
				}
				for _, block := range fn.Blocks {
					for _, instruction := range block.Instrs {
						call, ok := instruction.(*ssa.Call)
						if !ok {
							continue
						}
						target := call.Common().StaticCallee()
						if target != nil && (target.Name() == "rwlock" || target.Name() == "rwunlock" || target.Name() == "incref" || target.Name() == "decref") {
							if change == "rebound-mutex" {
								universe.aliases[target] = pkg.ssa.Func("replacement")
							}
							if change == "missing-canonical" {
								delete(universe.required, target)
							}
						}
					}
				}
				if change == "missing-classification" {
					universe.coroProgramIR.callableClassifications = nil
				}
				proof := planCoroPollFDFast(&context{prog: prog, emissionUniverse: universe}, fn)
				if (proof != nil) != (change == "verified") {
					t.Fatalf("proof=%v for %s", proof, change)
				}
			})
		}
	}
}

// Execute the emitted CAS itself. In particular, a last closed reference must
// leave the word untouched, not decrement it and then ask the wrapper to retry.
func TestCoroPollFDFastNativeUpdatesPreserveSlowPathOwnership(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	llvm.LinkInMCJIT()
	source, _, _ := buildGoSSAPkg(t, "package foo;func readLock(){};func readUnlock(){};func writeLock(){};func writeUnlock(){};func rwlock(){};func rwunlock(){};func incref(){};func decref(){}")
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	pkg := prog.NewPackage("pollfdexec", "pollfdexec")
	module := pkg.Module()
	field := func(name string, ty types.Type) *types.Var { return types.NewField(token.NoPos, nil, name, ty, false) }
	mu := types.NewStruct([]*types.Var{field("state", types.Typ[types.Uint64]), field("rsema", types.Typ[types.Uint32]), field("wsema", types.Typ[types.Uint32])}, nil)
	fd := types.NewStruct([]*types.Var{field("fdmu", mu), field("isFile", types.Typ[types.Bool])}, nil)
	u64 := prog.Type(types.Typ[types.Uint64], llssa.InGo)
	ctx := &context{prog: prog, pkg: pkg}
	expected := map[string]uint64{}
	index := 0
	type stateCase struct {
		name        string
		old, want   uint64
		done, nilFD bool
	}
	operations := []struct {
		name                  string
		read, unlock, refOnly bool
	}{{"readLock", true, false, false}, {"readUnlock", true, true, false}, {"writeLock", false, false, false}, {"writeUnlock", false, true, false}, {"incref", false, false, true}, {"decref", false, true, true}}
	for _, op := range operations {
		bit, wait, otherWait := uint64(2), uint64(1<<23), uint64(1<<43)
		if !op.read {
			bit, wait, otherWait = 4, 1<<43, 1<<23
		}
		var tests []stateCase
		switch {
		case op.refOnly && op.unlock:
			tests = []stateCase{{name: "release", old: 8, done: true}, {name: "closed-last", old: 9, want: 9}, {name: "closed-other-ref", old: 17, want: 9, done: true}, {name: "no-reference"}, {name: "beside-read", old: 10, want: 2, done: true}, {name: "waiter", old: 8 + 1<<23, want: 1 << 23, done: true}}
		case op.refOnly:
			tests = []stateCase{{name: "free", want: 8, done: true}, {name: "closed", old: 1, want: 1}, {name: "overflow", old: (1<<20 - 1) << 3, want: (1<<20 - 1) << 3}, {name: "beside-read", old: 10, want: 18, done: true}, {name: "beside-write", old: 12, want: 20, done: true}, {name: "waiter", old: 1 << 23, want: 8 + 1<<23, done: true}}
		case op.unlock:
			tests = []stateCase{{name: "unlock", old: bit + 8, done: true}, {name: "closed-last", old: bit + 9, want: bit + 9}, {name: "closed-other-ref", old: bit + 17, want: 9, done: true}, {name: "wakeup", old: bit + 8 + wait, want: bit + 8 + wait}, {name: "other-waiter", old: bit + 8 + otherWait, want: otherWait, done: true}, {name: "no-reference", old: bit, want: bit}, {name: "no-lock", old: 8, want: 8}, {name: "both-locks", old: 22, want: (bit ^ 6) + 8, done: true}}
		default:
			tests = []stateCase{{name: "free", want: bit + 8, done: true}, {name: "closed", old: 1, want: 1}, {name: "busy", old: bit + 8, want: bit + 8}, {name: "other-lock", old: (bit ^ 6) + 8, want: 22, done: true}, {name: "overflow", old: (1<<20 - 1) << 3, want: (1<<20 - 1) << 3}}
		}
		tests = append(tests, stateCase{name: "nil", old: 123, want: 123, nilFD: true})
		for _, test := range tests {
			name := fmt.Sprintf("probe%d", index)
			index++
			sig := types.NewSignatureType(nil, nil, nil, nil, types.NewTuple(types.NewParam(token.NoPos, nil, "result", types.Typ[types.Uint64])), false)
			fn := pkg.NewFunc(name, sig, llssa.InC)
			b := fn.MakeBody(1)
			storage := b.AllocaT(prog.Type(fd, llssa.InGo))
			b.StoreKnownNonNil(storage, prog.Zero(prog.Type(fd, llssa.InGo)))
			state := b.FieldAddrKnownNonNil(b.FieldAddrKnownNonNil(storage, 0), 0)
			b.StoreKnownNonNil(state, prog.IntVal(test.old, u64))
			argument := storage
			if test.nilFD {
				argument = prog.Nil(storage.Type)
			}
			mutexName, digest := "rwlock", coroPollMutexLockDigest
			if op.unlock {
				mutexName, digest = "rwunlock", coroPollMutexUnlockDigest
			}
			if op.refOnly {
				mutexName, digest = "incref", coroPollMutexIncrefDigest
				if op.unlock {
					mutexName, digest = "decref", coroPollMutexDecrefDigest
				}
			}
			proof := &coroPollFDFastPlan{target: source.Func(op.name), mutex: &coroPollMutexFastPlan{target: source.Func(mutexName), sourceDigest: digest, unlock: op.unlock, refOnly: op.refOnly}, sourceDigest: coroPollFDSourceDigests[op.name], read: op.read}
			done := ctx.emitCoroPollFDFast(b, []llssa.Expr{argument}, proof)
			b.Return(b.BinOp(token.OR, b.BinOp(token.SHL, b.LoadKnownNonNil(state), prog.IntVal(1, u64)), b.SelectValue(done, prog.IntVal(1, u64), prog.IntVal(0, u64))))
			b.EndBuild()
			b.Dispose()
			expected[fn.Name()] = test.want << 1
			if test.done {
				expected[fn.Name()]++
			}
			t.Logf("%s/%s: %s", op.name, test.name, fn.Name())
		}
	}
	// The primitive's bool is distinct from the FD wrapper's nil error. A
	// standalone final decref must still commit and report the destroy fact.
	for _, test := range []struct {
		name                string
		old, want           uint64
		unlock, done, value bool
	}{{"incref", 0, 8, false, true, true}, {"incref", 1, 1, false, false, false}, {"decref", 8, 0, true, true, false}, {"decref", 9, 1, true, true, true}, {"decref", 17, 9, true, true, false}, {"decref", 0, 0, true, false, false}} {
		name := fmt.Sprintf("primitive%d", index)
		index++
		sig := types.NewSignatureType(nil, nil, nil, nil, types.NewTuple(types.NewParam(token.NoPos, nil, "result", types.Typ[types.Uint64])), false)
		fn := pkg.NewFunc(name, sig, llssa.InC)
		b := fn.MakeBody(1)
		storage := b.AllocaT(prog.Type(mu, llssa.InGo))
		b.StoreKnownNonNil(storage, prog.Zero(prog.Type(mu, llssa.InGo)))
		state := b.FieldAddrKnownNonNil(storage, 0)
		b.StoreKnownNonNil(state, prog.IntVal(test.old, u64))
		value := b.AllocaT(prog.Bool())
		digest := coroPollMutexIncrefDigest
		if test.unlock {
			digest = coroPollMutexDecrefDigest
		}
		done := ctx.emitCoroPollMutexFast(b, []llssa.Expr{storage}, &coroPollMutexFastPlan{target: source.Func(test.name), sourceDigest: digest, unlock: test.unlock, refOnly: true}, value)
		flags := b.BinOp(token.OR, b.SelectValue(done, prog.IntVal(2, u64), prog.IntVal(0, u64)), b.SelectValue(b.LoadKnownNonNil(value), prog.IntVal(1, u64), prog.IntVal(0, u64)))
		b.Return(b.BinOp(token.OR, b.BinOp(token.SHL, b.LoadKnownNonNil(state), prog.IntVal(2, u64)), flags))
		b.EndBuild()
		b.Dispose()
		expected[fn.Name()] = test.want << 2
		if test.done {
			expected[fn.Name()] |= 2
		}
		if test.value {
			expected[fn.Name()] |= 1
		}
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		module.Dispose()
		t.Fatalf("verify: %v\n%s", err, module.String())
	}
	ir := module.String()
	if strings.Contains(ir, "call ") {
		module.Dispose()
		t.Fatalf("fast updates called a runtime helper:\n%s", ir)
	}
	engine, err := llvm.NewMCJITCompiler(module, llvm.NewMCJITCompilerOptions())
	if err != nil {
		module.Dispose()
		t.Fatal(err)
	}
	defer engine.Dispose()
	for name, want := range expected {
		result := engine.RunFunction(module.NamedFunction(name), nil)
		got := result.Int(false)
		result.Dispose()
		if got != want {
			t.Errorf("%s: encoded state=%#x want=%#x", name, got, want)
		}
	}
}

func TestCoroPollFDFastCleanupProofPreservesSiteIdentity(t *testing.T) {
	for _, change := range []string{"verified", "target", "source", "kind", "environment", "closure", "arguments", "instruction"} {
		t.Run(change, func(t *testing.T) {
			pkg, _, _ := buildGoSSAPkg(t, "package foo;func readUnlock(int){};func rwunlock(){};func other(){};func Root(){defer readUnlock(0)}")
			fn := pkg.Func("Root")
			var deferred *ssa.Defer
			for _, block := range fn.Blocks {
				for _, instruction := range block.Instrs {
					if d, ok := instruction.(*ssa.Defer); ok {
						deferred = d
					}
				}
			}
			proof := &coroPollFDFastPlan{target: pkg.Func("readUnlock"), mutex: &coroPollMutexFastPlan{target: pkg.Func("rwunlock"), sourceDigest: coroPollMutexUnlockDigest, unlock: true}, sourceDigest: coroPollFDSourceDigests["readUnlock"], read: true}
			site := &coroStaticCleanupSitePlan{instruction: deferred, target: proof.target, targetPlan: coro.FunctionPlan{ID: "foo.readUnlock"}, kind: coroStaticCleanupCoroutine, arguments: deferred.Call.Args, pollFDFast: proof}
			switch change {
			case "target":
				site.target = pkg.Func("other")
			case "source":
				proof.sourceDigest = "wrong"
			case "kind":
				site.kind = coroStaticCleanupPlain
			case "environment":
				site.closureEnvironment = types.NewVar(token.NoPos, nil, "env", types.Typ[types.UnsafePointer])
			case "closure":
				site.closure = &ssa.MakeClosure{}
			case "arguments":
				site.arguments = nil
			case "instruction":
				site.instruction = nil
			}
			plan := &coroPhysicalFunctionPlan{function: fn, owner: &preparedEmissionPackage{identity: "foo"}, reachableBlocks: coroPhysicalConstantReachableBlocks(fn), instructions: make(map[ssa.Instruction]coroPhysicalInstructionPlan), cleanup: &coroStaticCleanupPlan{sites: []*coroStaticCleanupSitePlan{site}}}
			for _, block := range fn.Blocks {
				for _, instruction := range block.Instrs {
					plan.instructions[instruction] = coroPhysicalInstructionPlan{}
				}
			}
			err := newCoroPhysicalPlanStage().freezePhysicalFunctionPlan(plan)
			if (err == nil) != (change == "verified") {
				t.Fatalf("freeze error=%v for %s", err, change)
			}
		})
	}
}
