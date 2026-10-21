// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package cl

import (
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

func TestCoroPollMutexFastPlanRequiresExactBindings(t *testing.T) {
	fixture, err := os.ReadFile("testdata/poll_mutex_go127.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"rwlock", "rwunlock"} {
		for _, change := range []string{"verified", "receiver", "read", "wait", "layout", "constant", "true", "false", "type", "source", "noinline", "load", "cas", "import", "domain", "missing-classification"} {
			if method == "rwunlock" && change == "wait" {
				continue
			}
			t.Run(method+"/"+change, func(t *testing.T) {
				program := newEmissionTestProgram()
				program.ssa.CreatePackage(types.Unsafe, nil, nil, true)
				load := "//go:linkname LoadUint64 llgo.atomicLoad\nfunc LoadUint64(p *uint64) uint64\n"
				cas := "//go:linkname CompareAndSwapUint64 llgo.atomicCmpXchgOK\nfunc CompareAndSwapUint64(p *uint64,old,new uint64) bool\n"
				if change == "load" {
					load = "func LoadUint64(p *uint64) uint64 {return *p}\n"
				}
				if change == "cas" {
					cas = "func CompareAndSwapUint64(p *uint64,old,new uint64) bool {return false}\n"
				}
				atomic := program.addPackage(t, "sync/atomic", "package atomic\nimport _ \"unsafe\"\ntype Word=uint64\n"+load+cas)
				source := string(fixture)
				header := "func (mu *fdMutex) " + method
				switch change {
				case "receiver":
					source = strings.Replace(source, header, "func (receiver *fdMutex) "+method, 1) + "\nvar mu *fdMutex\n"
				case "read":
					source = strings.Replace(source, header+"(read bool", header+"(direction bool", 1) + "\nvar read bool\n"
				case "wait":
					source = strings.Replace(source, "rwlock(read bool, wait bool)", "rwlock(read bool, blocking bool)", 1) + "\nvar wait bool\n"
				case "layout":
					source = strings.Replace(source, "state uint64", "padding uint64;state uint64", 1)
				case "constant":
					source = strings.Replace(source, "mutexRLock   = 1 << 1", "mutexRLock   = 1 << 3", 1)
				case "true", "false":
					value := "1==0"
					if change == "false" {
						value = "1==1"
					}
					source += "\nconst " + change + " = " + value + "\n"
				case "type":
					source += "\ntype uint64=atomic.Word\n"
				case "source":
					start := strings.Index(source, header)
					brace := strings.Index(source[start:], "{") + start
					source = source[:brace+1] + "\nif mu==nil{return false}\n" + source[brace+1:]
				case "noinline":
					source = strings.Replace(source, header, "//go:noinline\n"+header, 1)
				}
				inputs := []EmissionPackage{{SSA: atomic.ssa, Files: []*ast.File{atomic.file}}}
				if change == "import" {
					rebound := program.addPackage(t, "replacement/atomic", "package atomic\n"+"func LoadUint64(p *uint64)uint64{return *p}\nfunc CompareAndSwapUint64(*uint64,uint64,uint64)bool{return false}\n")
					inputs = append(inputs, EmissionPackage{SSA: rebound.ssa, Files: []*ast.File{rebound.file}})
					source = strings.Replace(source, `import "sync/atomic"`, `import(_ "sync/atomic";atomic "replacement/atomic")`, 1)
				}
				pkg := program.addPackage(t, "internal/poll", source)
				program.ssa.Build()
				prog := newLLSSAProg(t)
				defer prog.Dispose()
				inputs = append(inputs, EmissionPackage{SSA: pkg.ssa, Files: []*ast.File{pkg.file}})
				values := coro.ProgramCapabilities(0)
				if change == "domain" {
					values = coro.ExecutionDomainProgramCapability()
				}
				universe, err := PrepareEmissionUniverseWithOptions(prog, nil, inputs, EmissionUniverseOptions{CoroProgramPredicates: coro.ProgramPredicateRequirements{Mask: coro.ProgramPredicateMask(), Values: values}})
				if err != nil {
					t.Fatal(err)
				}
				if change == "missing-classification" {
					universe.coroProgramIR.callableClassifications = nil
				}
				named := pkg.types.Scope().Lookup("fdMutex").Type().(*types.Named)
				var fn *ssa.Function
				for i := 0; i < named.NumMethods(); i++ {
					if named.Method(i).Name() == method {
						fn = program.ssa.FuncValue(named.Method(i))
					}
				}
				proof := planCoroPollMutexFast(&context{prog: prog, emissionUniverse: universe}, fn)
				if (proof != nil) != (change == "verified") {
					t.Fatalf("proof=%v for %s", proof, change)
				}
			})
		}
	}
}

func TestCoroPollMutexFastUpdateIsCallFreeAtomicIR(t *testing.T) {
	for _, unlock := range []bool{false, true} {
		name, digest := "rwlock", coroPollMutexLockDigest
		if unlock {
			name, digest = "rwunlock", coroPollMutexUnlockDigest
		}
		t.Run(name, func(t *testing.T) {
			pkg, _, _ := buildGoSSAPkg(t, "package foo;func "+name+"(){}")
			prog := newLLSSAProg(t)
			defer prog.Dispose()
			native := prog.NewPackage("pollfast", "pollfast")
			module := native.Module()
			defer module.Dispose()
			shape := types.NewStruct([]*types.Var{types.NewField(token.NoPos, nil, "state", types.Typ[types.Uint64], false), types.NewField(token.NoPos, nil, "rsema", types.Typ[types.Uint32], false), types.NewField(token.NoPos, nil, "wsema", types.Typ[types.Uint32], false)}, nil)
			params := []*types.Var{types.NewParam(token.NoPos, nil, "mu", types.NewPointer(shape)), types.NewParam(token.NoPos, nil, "read", types.Typ[types.Bool])}
			if !unlock {
				params = append(params, types.NewParam(token.NoPos, nil, "wait", types.Typ[types.Bool]))
			}
			sig := types.NewSignatureType(nil, nil, nil, types.NewTuple(params...), types.NewTuple(types.NewParam(token.NoPos, nil, "done", types.Typ[types.Bool]), types.NewParam(token.NoPos, nil, "value", types.Typ[types.Bool])), false)
			function := native.NewFunc("attempt", sig, llssa.InGo)
			body := function.MakeBody(1)
			ctx := &context{prog: prog, pkg: native}
			args := []llssa.Expr{function.Param(0), function.Param(1)}
			if !unlock {
				args = append(args, function.Param(2))
			}
			value := body.AllocaT(prog.Bool())
			done := ctx.emitCoroPollMutexFast(body, args, &coroPollMutexFastPlan{target: pkg.Func(name), sourceDigest: digest, unlock: unlock}, value)
			body.Return(done, body.Load(value))
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
				t.Fatalf("guarded update calls helper:\n%s", ir)
			}
		})
	}
}
