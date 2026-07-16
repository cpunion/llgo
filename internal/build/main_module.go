//go:build !llgo
// +build !llgo

/*
 * Copyright (c) 2024 The XGo Authors (xgo.dev). All rights reserved.
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

// Package build contains the llgo compiler build orchestration logic.
//
// The main_module.go file generates the entry point module for llgo programs,
// which contains the main() function, initialization sequence, and global
// variables like argc/argv. This module is generated differently depending on
// BuildMode (exe, c-archive, c-shared).

package build

import (
	"fmt"
	"go/token"
	"go/types"

	"github.com/goplus/llgo/internal/packages"
	llvm "github.com/xgo-dev/llvm"

	llssa "github.com/goplus/llgo/ssa"
)

type genConfig struct {
	rtInit           bool
	pyInit           bool
	abiInit          int
	coroRootAnchors  []string
	coroManifestHash [16]byte
	coroBootstrap    *coroProgramBootstrapV1
	methodByIndex    map[int]none
	methodByName     map[string]none
	abiSymbols       map[string]none
	funcInfo         []funcInfoRecord
	pcLineInfo       []pcLineRecord
	funcInfoStubs    []funcInfoStubRecord
}

// genMainModule generates the main entry module for an llgo program.
//
// The module contains argc/argv globals and, for executable build modes,
// the entry function that wires initialization and main. For C archive or
// shared library modes, only the globals are emitted.
func genMainModule(ctx *context, rtPkgPath string, pkg *packages.Package, cfg *genConfig) Package {
	prog := ctx.prog
	mainPkg := prog.NewPackage("", pkg.ID+".main")
	defer mainPkg.MaterializePreserveSyms()

	argcVar := mainPkg.NewVarEx("__llgo_argc", prog.Pointer(prog.Int32()))
	argcVar.Init(prog.Zero(prog.Int32()))

	argvValueType := prog.Pointer(prog.CStr())
	argvVar := mainPkg.NewVarEx("__llgo_argv", prog.Pointer(argvValueType))
	argvVar.InitNil()
	emitFuncInfoTable(ctx, mainPkg, cfg.funcInfo, cfg.pcLineInfo, cfg.funcInfoStubs)
	emitCoroControlWrappers(ctx, mainPkg)
	emitCoroProgramManifest(ctx, mainPkg, cfg)

	exportFile := pkg.ExportFile
	if exportFile == "" {
		exportFile = pkg.PkgPath
	}
	mainAPkg := &aPackage{
		Package: &packages.Package{
			PkgPath:    pkg.PkgPath + ".main",
			ExportFile: exportFile + "-main",
		},
		LPkg: mainPkg,
	}

	if ctx.buildConf.BuildMode != BuildModeExe {
		return mainAPkg
	}

	runtimeStub := defineWeakNoArgStub(mainPkg, "runtime.init")
	// TODO(lijie): workaround for syscall patch
	defineWeakNoArgStub(mainPkg, "syscall.init")

	var pyInit llssa.Function
	var pyFinalize llssa.Function
	if cfg.pyInit {
		pyInit = declareNoArgFunc(mainPkg, "Py_Initialize")
		pyFinalize = declareNoArgFunc(mainPkg, "Py_Finalize")
	}

	var rtInit llssa.Function
	if cfg.rtInit {
		rtInit = declareNoArgFunc(mainPkg, rtPkgPath+".init")
	}

	var abiInit llssa.Function
	if cfg.abiInit != 0 {
		abiInit = mainPkg.InitAbiTypesFor("init$abitypes", func(sym *llssa.AbiSymbol) bool {
			if _, ok := cfg.abiSymbols[sym.Name]; !ok {
				return false
			}
			return filterAbiSymbol(cfg.abiInit, sym)
		})
	}

	mainInit := declareNoArgFunc(mainPkg, pkg.PkgPath+".init")
	mainMain := declareNoArgFunc(mainPkg, pkg.PkgPath+".main")

	entryFn := defineEntryFunction(ctx, mainPkg, argcVar, argvVar, argvValueType, entryFunctions{
		runtimeStub: runtimeStub,
		mainInit:    mainInit,
		mainMain:    mainMain,
		pyInit:      pyInit,
		pyFinalize:  pyFinalize,
		rtInit:      rtInit,
		abiInit:     abiInit,
	})

	if needStart(ctx) {
		defineStart(mainPkg, entryFn, argvValueType)
	}

	return mainAPkg
}

// emitCoroControlWrappers defines the compiler-owned handle control boundary
// used by the v1 scheduler. Keeping the LLVM coroutine intrinsics in the entry
// module gives every build mode one fixed C ABI without exposing LLVM's handle
// representation to the runtime.
func emitCoroControlWrappers(ctx *context, pkg llssa.Package) {
	if !ctx.buildConf.EnableCoroChildAwait {
		return
	}

	handleType := types.Typ[types.UnsafePointer]
	controlSignature := newSignature([]types.Type{handleType}, nil)

	resume := pkg.NewFunc("__llgo_coro_resume_v1", controlSignature, llssa.InC)
	resumeBody := resume.MakeBody(1)
	resumeBody.CoroResume(resume.Param(0))
	resumeBody.Return()

	done := pkg.NewFunc("__llgo_coro_done_v1", newSignature(
		[]types.Type{handleType},
		[]types.Type{types.Typ[types.Bool]},
	), llssa.InC)
	doneBody := done.MakeBody(1)
	doneBody.Return(doneBody.CoroDone(done.Param(0)))

	destroy := pkg.NewFunc("__llgo_coro_destroy_v1", controlSignature, llssa.InC)
	destroyBody := destroy.MakeBody(1)
	destroyBody.CoroDestroy(destroy.Param(0))
	destroyBody.Return()
}

const (
	coroProgramManifestSymbolV1  = "__llgo_coro_program_manifest_v1"
	coroProgramBootstrapSymbolV1 = "__llgo_coro_program_bootstrap_v1"
)

func emitCoroProgramManifest(ctx *context, pkg llssa.Package, cfg *genConfig) {
	if ctx == nil || ctx.buildConf == nil || !ctx.buildConf.EnableCoroChildAwait {
		return
	}
	prog := pkg.Prog
	anchorType := prog.Struct(
		prog.Uint32(),
		prog.Uint32(),
		prog.Uint64(),
		prog.Uint64(),
		prog.Uintptr(),
		prog.VoidPtr(),
	)
	anchors := make([]llssa.Expr, len(cfg.coroRootAnchors))
	for i, name := range cfg.coroRootAnchors {
		anchor := pkg.NewVarEx(name, prog.Pointer(anchorType))
		global := pkg.Module().NamedGlobal(name)
		global.SetLinkage(llvm.ExternalLinkage)
		global.SetVisibility(llvm.HiddenVisibility)
		anchors[i] = anchor.Expr
	}
	var bootstrap llssa.Expr
	if ctx.buildConf.EnableCoroProgramBootstrapABI {
		if cfg.coroBootstrap == nil {
			panic("coroutine program bootstrap ABI enabled without a validated startup table")
		}
		steps := make([]llssa.CoroProgramStep, len(cfg.coroBootstrap.Steps))
		for i, step := range cfg.coroBootstrap.Steps {
			target := declareNoArgFunc(pkg, step.Target)
			steps[i] = llssa.CoroProgramStep{
				Kind:   llssa.CoroProgramStepKind(step.Kind),
				Flags:  step.Role,
				Target: target.Expr,
				Aux:    uint64(step.Aux),
			}
		}
		bootstrap = pkg.NewCoroProgramBootstrap(coroProgramBootstrapSymbolV1, llssa.CoroProgramBootstrapOptions{
			Version: coroProgramBootstrapVersionV1,
			// The runtime validates one program ABI identity across the manifest
			// and startup table. StepHash is an input to this final manifest hash,
			// not a second externally visible ABI identity.
			ABIHash: cfg.coroManifestHash,
			Steps:   steps,
		})
	}
	pkg.NewCoroProgramManifest(coroProgramManifestSymbolV1, llssa.CoroProgramManifestOptions{
		Version:        1,
		ABIHash:        cfg.coroManifestHash,
		PackageAnchors: anchors,
		Bootstrap:      bootstrap,
	})
}

// lowerCoroControlWrappers runs the coroutine cleanup pipeline before the
// entry module reaches object selection. TargetMachine object emission cannot
// select raw llvm.coro.resume/done/destroy intrinsics; keeping this step next to
// their definitions makes the requirement independent of optimization level,
// LTO mode, or whether clang participates in the final codegen path.
func lowerCoroControlWrappers(ctx *context, pkg llssa.Package) error {
	if ctx == nil || ctx.buildConf == nil || !ctx.buildConf.EnableCoroChildAwait {
		return nil
	}
	if pkg == nil || ctx.prog == nil {
		return fmt.Errorf("coroutine control lowering requires an entry module and program")
	}
	mod := pkg.Module()
	mod.SetDataLayout(ctx.prog.DataLayout())
	mod.SetTarget(ctx.prog.TargetSpec().Triple)
	if err := llvm.VerifyModule(mod, llvm.ReturnStatusAction); err != nil {
		return fmt.Errorf("verify coroutine control wrappers before lowering: %w", err)
	}
	options := llvm.NewPassBuilderOptions()
	defer options.Dispose()
	if err := mod.RunPasses("default<O0>", ctx.prog.TargetMachine(), options); err != nil {
		return fmt.Errorf("lower coroutine control wrappers: %w", err)
	}
	if err := llvm.VerifyModule(mod, llvm.ReturnStatusAction); err != nil {
		return fmt.Errorf("verify coroutine control wrappers after lowering: %w", err)
	}
	return nil
}

func filterAbiSymbol(abiInit int, sym *llssa.AbiSymbol) bool {
	switch sym.Raw.(type) {
	case *types.Array:
		if abiInit&llssa.ReflectArrayOf != 0 {
			return true
		}
	case *types.Chan:
		if abiInit&llssa.ReflectChanOf != 0 {
			return true
		}
	case *types.Signature:
		if abiInit&llssa.ReflectFuncOf != 0 {
			return true
		}
		if abiInit&llssa.ReflectMethodMask != 0 {
			return true
		}
	case *types.Map:
		if abiInit&llssa.ReflectMapOf != 0 {
			return true
		}
	case *types.Pointer:
		if abiInit&llssa.ReflectPointerTo != 0 {
			return true
		}
	case *types.Slice:
		if abiInit&llssa.ReflectSliceOf != 0 {
			return true
		}
	case *types.Struct:
		if abiInit&llssa.ReflectStructOf != 0 {
			return true
		}
	}
	return false
}

type entryFunctions struct {
	runtimeStub llssa.Function
	mainInit    llssa.Function
	mainMain    llssa.Function
	pyInit      llssa.Function
	pyFinalize  llssa.Function
	rtInit      llssa.Function
	abiInit     llssa.Function
}

// defineEntryFunction creates the program's entry function. The name is
// "main" for standard targets, or "__main_argc_argv" with hidden visibility
// for WASM targets that don't require _start.
//
// The entry stores argc/argv, optionally disables stdio buffering, runs
// initialization hooks (Python, runtime, package init), calls main.main,
// finalizes Python if it was initialized, and returns 0.
func defineEntryFunction(ctx *context, pkg llssa.Package, argcVar, argvVar llssa.Global, argvType llssa.Type, fns entryFunctions) llssa.Function {
	prog := pkg.Prog
	entryName := "main"
	if !needStart(ctx) && isWasmTarget(ctx.buildConf.Goos) {
		entryName = "__main_argc_argv"
	}
	sig := newEntrySignature(argvType.RawType())
	fn := pkg.NewFunc(entryName, sig, llssa.InC)
	fnVal := pkg.Module().NamedFunction(entryName)
	if entryName != "main" {
		fnVal.SetVisibility(llvm.HiddenVisibility)
		fnVal.SetUnnamedAddr(true)
	}
	b := fn.MakeBody(1)
	b.Store(argcVar.Expr, fn.Param(0))
	b.Store(argvVar.Expr, fn.Param(1))
	if IsStdioNobuf() {
		emitStdioNobuf(b, pkg, ctx.buildConf.Goos)
	}
	if fns.pyInit != nil {
		b.Call(fns.pyInit.Expr)
	}
	if fns.rtInit != nil {
		b.Call(fns.rtInit.Expr)
	}
	if fns.abiInit != nil {
		b.Call(fns.abiInit.Expr)
	}
	b.Call(fns.runtimeStub.Expr)
	b.Call(fns.mainInit.Expr)
	b.Call(fns.mainMain.Expr)
	if fns.pyFinalize != nil {
		b.Call(fns.pyFinalize.Expr)
	}
	b.Return(prog.IntVal(0, prog.Int32()))
	return fn
}

func defineStart(pkg llssa.Package, entry llssa.Function, argvType llssa.Type) {
	fn := pkg.NewFunc("_start", llssa.NoArgsNoRet, llssa.InC)
	pkg.Module().NamedFunction("_start").SetLinkage(llvm.WeakAnyLinkage)
	b := fn.MakeBody(1)
	prog := pkg.Prog
	b.Call(entry.Expr, prog.IntVal(0, prog.Int32()), prog.Nil(argvType))
	b.Return()
}

func declareNoArgFunc(pkg llssa.Package, name string) llssa.Function {
	return pkg.NewFunc(name, llssa.NoArgsNoRet, llssa.InC)
}

func defineWeakNoArgStub(pkg llssa.Package, name string) llssa.Function {
	fn := pkg.NewFunc(name, llssa.NoArgsNoRet, llssa.InC)
	pkg.Module().NamedFunction(name).SetLinkage(llvm.WeakAnyLinkage)
	b := fn.MakeBody(1)
	b.Return()
	return fn
}

const (
	// ioNoBuf represents the _IONBF flag for setvbuf (no buffering)
	ioNoBuf = 2
)

// emitStdioNobuf generates code to disable buffering on stdout and stderr
// when the LLGO_STDIO_NOBUF environment variable is set. Only Darwin uses
// the alternate `__stdoutp`/`__stderrp` symbols; other targets rely on the
// standard `stdout`/`stderr` globals.
func emitStdioNobuf(b llssa.Builder, pkg llssa.Package, goos string) {
	prog := pkg.Prog
	streamType := prog.VoidPtr()
	streamPtrType := prog.Pointer(streamType)

	stdoutName := "stdout"
	stderrName := "stderr"
	if goos == "darwin" {
		stdoutName = "__stdoutp"
		stderrName = "__stderrp"
	}
	stdout := declareExternalPtrGlobal(pkg, stdoutName, streamPtrType)
	stderr := declareExternalPtrGlobal(pkg, stderrName, streamPtrType)
	stdoutPtr := b.Load(stdout)
	stderrPtr := b.Load(stderr)
	sizeType := prog.Uintptr()
	setvbuf := declareSetvbuf(pkg, streamPtrType, prog.CStr(), prog.Int32(), sizeType)

	noBufMode := prog.IntVal(ioNoBuf, prog.Int32())
	zeroSize := prog.Zero(sizeType)
	nullBuf := prog.Nil(prog.CStr())

	b.Call(setvbuf.Expr, stdoutPtr, nullBuf, noBufMode, zeroSize)
	b.Call(setvbuf.Expr, stderrPtr, nullBuf, noBufMode, zeroSize)
}

func declareExternalPtrGlobal(pkg llssa.Package, name string, valueType llssa.Type) llssa.Expr {
	global := pkg.NewVarEx(name, valueType)
	pkg.Module().NamedGlobal(name).SetLinkage(llvm.ExternalLinkage)
	return global.Expr
}

func declareSetvbuf(pkg llssa.Package, streamPtrType, bufPtrType, intType, sizeType llssa.Type) llssa.Function {
	sig := newSignature(
		[]types.Type{
			streamPtrType.RawType(),
			bufPtrType.RawType(),
			intType.RawType(),
			sizeType.RawType(),
		},
		[]types.Type{intType.RawType()},
	)
	return pkg.NewFunc("setvbuf", sig, llssa.InC)
}

func tupleOf(tys ...types.Type) *types.Tuple {
	if len(tys) == 0 {
		return types.NewTuple()
	}
	vars := make([]*types.Var, len(tys))
	for i, t := range tys {
		vars[i] = types.NewParam(token.NoPos, nil, "", t)
	}
	return types.NewTuple(vars...)
}

func newSignature(params []types.Type, results []types.Type) *types.Signature {
	return types.NewSignatureType(nil, nil, nil, tupleOf(params...), tupleOf(results...), false)
}

func newEntrySignature(argvType types.Type) *types.Signature {
	return newSignature(
		[]types.Type{types.Typ[types.Int32], argvType},
		[]types.Type{types.Typ[types.Int32]},
	)
}
