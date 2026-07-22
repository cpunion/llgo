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
	coroEntry := emitCoroProgramManifest(ctx, mainPkg, cfg)

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

	managedBootstrapV2 := ctx.buildConf.coroProgramBootstrapActive()
	if managedBootstrapV2 && (cfg.coroBootstrap == nil || cfg.coroBootstrap.Version != coroProgramBootstrapVersionV2) {
		panic("stackless coroutine entry requires the unique V2 startup table")
	}
	var runtimeStub llssa.Function
	if !managedBootstrapV2 {
		// Legacy entry modes retain the historical optional public-runtime hook.
		// V2 resolves the exact public runtime SSA init through its managed table;
		// defining a weak symbol here would satisfy the archive relocation with a
		// no-op and could silently prevent extraction of the real strong body.
		runtimeStub = defineWeakNoArgStub(mainPkg, "runtime.init")
		// TODO(lijie): legacy workaround for syscall patch. It is deliberately
		// absent from V2: a weak entry-module definition could also intercept a
		// real syscall.init relocation reached through the managed package-init
		// chain and violate the single-primary plan.
		defineWeakNoArgStub(mainPkg, "syscall.init")
	}

	var pyInit llssa.Function
	var pyFinalize llssa.Function
	if cfg.pyInit {
		pyInit = declareNoArgFunc(mainPkg, "Py_Initialize")
		pyFinalize = declareNoArgFunc(mainPkg, "Py_Finalize")
	}

	var rtInit llssa.Function
	if cfg.rtInit && !managedBootstrapV2 {
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
	if managedBootstrapV2 {
		// The v2 table always contains the compiler ABI-init stage. Profiles with
		// no selected ABI symbols still define the exact target as a bounded no-op
		// so the five-stage program never relies on an optional external symbol.
		if abiInit == nil {
			abiInit = mainPkg.FuncOf("init$abitypes")
			if abiInit == nil {
				abiInit = declareNoArgFunc(mainPkg, "init$abitypes")
			}
			if !abiInit.HasBody() {
				body := abiInit.MakeBody(1)
				body.Return()
			}
		}
	}

	var mainInit, mainMain llssa.Function
	if !ctx.buildConf.coroProgramBootstrapActive() {
		mainInit = declareNoArgFunc(mainPkg, pkg.PkgPath+".init")
		mainMain = declareNoArgFunc(mainPkg, pkg.PkgPath+".main")
	}
	var coroBegin llssa.Function
	var coroRun llssa.Function
	var coroContinue llssa.Function
	var coroRunSliceV2 llssa.Function
	var coroContinueSliceV2 llssa.Function
	var coroHostPullCallbacks []retainedCoroCallbackV1
	var coroAllocatorBootstrap llssa.Function
	if ctx.buildConf.coroProgramBootstrapActive() {
		if coroEntry.manifest.IsNil() || coroEntry.factory == nil {
			panic("coroutine program bootstrap runtime enabled without a manifest and factory")
		}
		coroAllocatorBootstrap = declareNoArgFunc(mainPkg, coroFrameAllocatorBootstrapSymbolV1)
		coroBegin = declareCoroProgramBeginV1(mainPkg)
		if nativeCoroDoorbellRuntimeABI(ctx.buildConf) {
			coroRunSliceV2 = declareCoroProgramRunSliceV2(mainPkg)
			coroContinueSliceV2 = declareCoroProgramContinueSliceV2(mainPkg)
		} else if hostCoroPullRuntimeABI(ctx.buildConf) {
			coroRunSliceV2 = declareCoroProgramRunSliceV2(mainPkg)
			coroContinueSliceV2 = declareCoroProgramContinueSliceV2(mainPkg)
			coroHostPullCallbacks = declareCoroHostPullCallbacksV1(mainPkg)
		} else {
			coroRun = declareCoroProgramRunV1(mainPkg)
			coroContinue = declareCoroProgramContinueV1(mainPkg)
		}
	}

	bootstrapVersion := uint32(0)
	if cfg.coroBootstrap != nil {
		bootstrapVersion = cfg.coroBootstrap.Version
	}
	entryFn := defineEntryFunction(ctx, mainPkg, argcVar, argvVar, argvValueType, entryFunctions{
		runtimeStub:            runtimeStub,
		mainInit:               mainInit,
		mainMain:               mainMain,
		pyInit:                 pyInit,
		pyFinalize:             pyFinalize,
		rtInit:                 rtInit,
		abiInit:                abiInit,
		coroManifest:           coroEntry.manifest,
		coroFactory:            coroEntry.factory,
		coroAllocatorBootstrap: coroAllocatorBootstrap,
		coroBegin:              coroBegin,
		coroRun:                coroRun,
		coroRunSliceV2:         coroRunSliceV2,
		coroContinueSliceV2:    coroContinueSliceV2,
		coroHostPull:           hostCoroPullRuntimeABI(ctx.buildConf),
		coroBootstrapVersion:   bootstrapVersion,
	})
	if coroContinue != nil {
		retainCoroProgramContinueV1(mainPkg, entryFn, coroContinue)
	}
	for _, callback := range coroHostPullCallbacks {
		retainCoroCallbackV1(mainPkg, entryFn, callback.function, callback.symbol, callback.reference, callback.description)
	}

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
	if !ctx.buildConf.coroChildAwaitActive() {
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
	coroProgramManifestSymbolV1         = "__llgo_coro_program_manifest_v1"
	coroProgramBootstrapSymbolV2        = "__llgo_coro_program_bootstrap_v2"
	coroFrameAllocatorBootstrapSymbolV1 = "__llgo_coro_frame_allocator_bootstrap_v1"
)

type coroProgramEntryV1 struct {
	manifest llssa.Expr
	factory  llssa.Function
}

func emitCoroProgramManifest(ctx *context, pkg llssa.Package, cfg *genConfig) coroProgramEntryV1 {
	if ctx == nil || ctx.buildConf == nil || !ctx.buildConf.coroChildAwaitActive() {
		return coroProgramEntryV1{}
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
	anchorByName := make(map[string]llssa.Expr, len(cfg.coroRootAnchors))
	for i, name := range cfg.coroRootAnchors {
		anchor := pkg.NewVarEx(name, prog.Pointer(anchorType))
		global := pkg.Module().NamedGlobal(name)
		global.SetLinkage(llvm.ExternalLinkage)
		global.SetVisibility(llvm.HiddenVisibility)
		anchors[i] = anchor.Expr
		anchorByName[name] = anchor.Expr
	}
	var bootstrap llssa.Expr
	var factory llssa.Function
	if ctx.buildConf.coroProgramBootstrapABIActive() {
		if cfg.coroBootstrap == nil {
			panic("coroutine program bootstrap ABI enabled without a validated startup table")
		}
		if cfg.coroBootstrap.Version != coroProgramBootstrapVersionV2 {
			panic("coroutine manifest accepts only the V2 startup table")
		}
		steps := make([]llssa.CoroProgramStep, len(cfg.coroBootstrap.Steps))
		targets := make([]coroProgramBootstrapFactoryTargetV2, len(cfg.coroBootstrap.Steps))
		for i, step := range cfg.coroBootstrap.Steps {
			var tableTarget llssa.Expr
			switch step.Kind {
			case coroProgramStepDirectPlainV1:
				plain := declareNoArgFunc(pkg, step.Target)
				if step.FunctionID == coroProgramPublicRuntimeNoopIDV2 {
					if step.Role != coroProgramStepRolePublicRuntimeInitV2 || step.Target != coroProgramPublicRuntimeNoopSymbolV2 {
						panic("coroutine program bootstrap v2 public-runtime no-op has noncanonical identity")
					}
					if !plain.HasBody() {
						body := plain.MakeBody(1)
						body.Return()
					}
				}
				targets[i].Plain = plain
				tableTarget = plain.Expr
			case coroProgramStepCoroRootV1:
				anchor := anchorByName[step.CatalogTarget]
				if anchor.IsNil() {
					panic(fmt.Sprintf("coroutine program bootstrap v2 step %d has unlinked catalog anchor %q", i, step.CatalogTarget))
				}
				targets[i].Anchor = anchor
				tableTarget = anchor
			default:
				panic(fmt.Sprintf("coroutine program bootstrap v2 step %d has invalid kind %d", i, step.Kind))
			}
			steps[i] = llssa.CoroProgramStep{
				Kind: llssa.CoroProgramStepKind(step.Kind), Flags: step.Role,
				Target: tableTarget, Aux: step.Aux,
			}
		}
		if ctx.buildConf.coroProgramBootstrapActive() {
			factory = emitCoroProgramBootstrapFactoryV2(
				pkg, cfg.coroBootstrap, targets, cfg.coroManifestHash,
				ctx.buildConf.coroClosedStaticSpawnActive(),
			)
		}
		var factoryExpr llssa.Expr
		if factory != nil {
			factoryExpr = factory.Expr
		}
		bootstrap = pkg.NewCoroProgramBootstrap(coroProgramBootstrapSymbolV2, llssa.CoroProgramBootstrapOptions{
			Version: coroProgramBootstrapVersionV2,
			// The runtime validates one program ABI identity across the manifest
			// and startup table. StepHash is an input to this final manifest hash,
			// not a second externally visible ABI identity.
			ABIHash: cfg.coroManifestHash,
			Steps:   steps,
			Factory: factoryExpr,
		})
	}
	manifest := pkg.NewCoroProgramManifest(coroProgramManifestSymbolV1, llssa.CoroProgramManifestOptions{
		Version:        1,
		ABIHash:        cfg.coroManifestHash,
		PackageAnchors: anchors,
		Bootstrap:      bootstrap,
	})
	return coroProgramEntryV1{manifest: manifest, factory: factory}
}

// lowerCoroControlWrappers runs the coroutine cleanup pipeline before the
// entry module reaches object selection. TargetMachine object emission cannot
// select raw llvm.coro.resume/done/destroy intrinsics; keeping this step next to
// their definitions makes the requirement independent of optimization level,
// LTO mode, or whether clang participates in the final codegen path.
func lowerCoroControlWrappers(ctx *context, pkg llssa.Package) error {
	if ctx == nil || ctx.buildConf == nil || !ctx.buildConf.coroChildAwaitActive() {
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
	llssa.RemoveKeepAliveCallsAfterCoroSplit(mod)
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
	runtimeStub            llssa.Function
	mainInit               llssa.Function
	mainMain               llssa.Function
	pyInit                 llssa.Function
	pyFinalize             llssa.Function
	rtInit                 llssa.Function
	abiInit                llssa.Function
	coroManifest           llssa.Expr
	coroFactory            llssa.Function
	coroAllocatorBootstrap llssa.Function
	coroBegin              llssa.Function
	coroRun                llssa.Function
	coroRunSliceV2         llssa.Function
	coroContinueSliceV2    llssa.Function
	coroHostPull           bool
	coroBootstrapVersion   uint32
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
	if fns.coroAllocatorBootstrap != nil {
		b.Call(fns.coroAllocatorBootstrap.Expr)
	}
	if IsStdioNobuf() {
		emitStdioNobuf(b, pkg, ctx.buildConf.Goos)
	}
	if fns.pyInit != nil {
		b.Call(fns.pyInit.Expr)
	}
	if fns.coroBootstrapVersion != coroProgramBootstrapVersionV2 {
		if fns.rtInit != nil {
			b.Call(fns.rtInit.Expr)
		}
		if fns.abiInit != nil {
			b.Call(fns.abiInit.Expr)
		}
		b.Call(fns.runtimeStub.Expr)
	}
	if fns.coroFactory != nil {
		sliceV2 := fns.coroRunSliceV2 != nil && fns.coroContinueSliceV2 != nil
		legacyRunV1 := fns.coroRun != nil && fns.coroRunSliceV2 == nil && fns.coroContinueSliceV2 == nil
		if fns.coroManifest.IsNil() || fns.coroAllocatorBootstrap == nil || fns.coroBegin == nil ||
			(!sliceV2 && !legacyRunV1) || fns.coroHostPull && !sliceV2 {
			panic("coroutine program entry requires allocator bootstrap, manifest, begin, factory, and run")
		}
		null := prog.Nil(prog.VoidPtr())
		manifest := b.Convert(prog.VoidPtr(), fns.coroManifest)
		factory := b.Convert(prog.VoidPtr(), fns.coroFactory.Expr)
		g := b.Call(fns.coroBegin.Expr, manifest, factory)
		handle := b.Call(fns.coroFactory.Expr, g, null, null)
		if fns.coroHostPull {
			b = emitCoroHostInitialSliceV2(b, pkg, g, handle, fns.coroRunSliceV2)
		} else if sliceV2 {
			b = emitCoroNativeRunLoopV2(b, pkg, g, handle, fns.coroRunSliceV2, fns.coroContinueSliceV2)
		} else {
			b.Call(fns.coroRun.Expr, g, handle)
		}
	} else {
		b.Call(fns.mainInit.Expr)
		b.Call(fns.mainMain.Expr)
	}
	if fns.pyFinalize != nil {
		b.Call(fns.pyFinalize.Expr)
	}
	b.Return(prog.IntVal(0, prog.Int32()))
	return fn
}

func declareCoroProgramBeginV1(pkg llssa.Package) llssa.Function {
	pointer := types.Typ[types.UnsafePointer]
	return pkg.NewFunc(coroProgramBeginSymbolV1, newSignature(
		[]types.Type{pointer, pointer},
		[]types.Type{pointer},
	), llssa.InC)
}

func declareCoroProgramRunV1(pkg llssa.Package) llssa.Function {
	pointer := types.Typ[types.UnsafePointer]
	return pkg.NewFunc(coroProgramRunSymbolV1, newSignature(
		[]types.Type{pointer, pointer},
		nil,
	), llssa.InC)
}

const (
	coroProgramRunResultFlagsV2 = iota
	coroProgramRunResultUsedV2
	coroProgramRunResultExecutorSlotV2
	coroProgramRunResultExecutorGenerationV2
	coroProgramRunResultEpochV2
	coroProgramRunResultDeadlineLoV2
	coroProgramRunResultDeadlineHiV2
	coroProgramRunResultReservedV2
)

func coroProgramRunResultTypeV2(prog llssa.Program) llssa.Type {
	word := prog.Uint32()
	return prog.Struct(word, word, word, word, word, word, word, word)
}

func declareCoroProgramRunSliceV2(pkg llssa.Package) llssa.Function {
	pointer := types.Typ[types.UnsafePointer]
	word := types.Typ[types.Uint32]
	resultPointer := types.NewPointer(coroProgramRunResultTypeV2(pkg.Prog).RawType())
	return pkg.NewFunc(coroProgramRunSliceSymbolV2, newSignature(
		[]types.Type{pointer, pointer, word, resultPointer},
		[]types.Type{word},
	), llssa.InC)
}

func declareCoroProgramContinueSliceV2(pkg llssa.Package) llssa.Function {
	word := types.Typ[types.Uint32]
	resultPointer := types.NewPointer(coroProgramRunResultTypeV2(pkg.Prog).RawType())
	return pkg.NewFunc(coroProgramContinueSliceSymbolV2, newSignature(
		[]types.Type{word, word, word, word, resultPointer},
		[]types.Type{word},
	), llssa.InC)
}

// emitCoroHostInitialSliceV2 performs exactly one bounded startup activation
// for an embedding-owned reactor. Complete rejoins the ordinary platform
// return path. Yielded and Suspended transfer only POD obligations to the host
// adapter and return from the entry immediately; the embedding later consumes
// NextAction and invokes the host continuation wrapper. No continuation call or
// scheduler recursion exists in this entry. Panic, Invalid, Repost, Ignored,
// and malformed result tuples abort at the compiler/runtime trust boundary.
func emitCoroHostInitialSliceV2(
	b llssa.Builder,
	pkg llssa.Package,
	g, handle llssa.Expr,
	run llssa.Function,
) llssa.Builder {
	if b == nil || pkg == nil || run == nil {
		panic("host coroutine initial slice requires entry builder and exact slice ABI")
	}
	prog := pkg.Prog
	word := prog.Uint32()
	zero := prog.Zero(word)
	budget := prog.IntVal(uint64(coroProgramNativeRunBudgetV2), word)
	result := b.AllocaT(coroProgramRunResultTypeV2(prog))
	status := b.Call(run.Expr, g, handle, budget, result)

	blocks := b.Func.MakeBlocks(9)
	completeCheckBlock := blocks[0]
	yieldedStatusBlock := blocks[1]
	yieldedCheckBlock := blocks[2]
	suspendedStatusBlock := blocks[3]
	suspendedCheckBlock := blocks[4]
	deadlineCheckBlock := blocks[5]
	detachedBlock := blocks[6]
	completeBlock := blocks[7]
	failBlock := blocks[8]
	b.If(
		b.BinOp(token.EQL, status, prog.IntVal(uint64(coroProgramDriveCompleteV2), word)),
		completeCheckBlock,
		yieldedStatusBlock,
	)

	and := func(left, right llssa.Expr) llssa.Expr {
		return b.BinOp(token.AND, left, right)
	}
	or := func(left, right llssa.Expr) llssa.Expr {
		return b.BinOp(token.OR, left, right)
	}
	equalField := func(index int, value llssa.Expr) llssa.Expr {
		return b.BinOp(token.EQL, b.Load(b.FieldAddr(result, index)), value)
	}
	nonzeroField := func(index int) llssa.Expr {
		return b.BinOp(token.NEQ, b.Load(b.FieldAddr(result, index)), zero)
	}
	boundedUsed := func() llssa.Expr {
		return b.BinOp(token.LEQ, b.Load(b.FieldAddr(result, coroProgramRunResultUsedV2)), budget)
	}
	exactTuple := func(valid llssa.Expr) llssa.Expr {
		valid = and(valid, boundedUsed())
		for _, index := range []int{
			coroProgramRunResultExecutorSlotV2,
			coroProgramRunResultExecutorGenerationV2,
			coroProgramRunResultEpochV2,
		} {
			valid = and(valid, nonzeroField(index))
		}
		return and(valid, equalField(coroProgramRunResultReservedV2, zero))
	}

	b.SetBlock(completeCheckBlock)
	validComplete := and(equalField(coroProgramRunResultFlagsV2, zero), boundedUsed())
	for _, index := range []int{
		coroProgramRunResultExecutorSlotV2,
		coroProgramRunResultExecutorGenerationV2,
		coroProgramRunResultEpochV2,
		coroProgramRunResultDeadlineLoV2,
		coroProgramRunResultDeadlineHiV2,
		coroProgramRunResultReservedV2,
	} {
		validComplete = and(validComplete, equalField(index, zero))
	}
	b.If(validComplete, completeBlock, failBlock)

	b.SetBlock(yieldedStatusBlock)
	b.If(
		b.BinOp(token.EQL, status, prog.IntVal(uint64(coroProgramDriveYieldedV2), word)),
		yieldedCheckBlock,
		suspendedStatusBlock,
	)

	b.SetBlock(yieldedCheckBlock)
	validYielded := exactTuple(equalField(
		coroProgramRunResultFlagsV2,
		prog.IntVal(uint64(coroProgramRunMoreV2|coroProgramRunRequestQueuedV2), word),
	))
	validYielded = and(validYielded, equalField(coroProgramRunResultDeadlineLoV2, zero))
	validYielded = and(validYielded, equalField(coroProgramRunResultDeadlineHiV2, zero))
	b.If(validYielded, detachedBlock, failBlock)

	b.SetBlock(suspendedStatusBlock)
	b.If(
		b.BinOp(token.EQL, status, prog.IntVal(uint64(coroProgramDriveSuspendedV2), word)),
		suspendedCheckBlock,
		failBlock,
	)

	b.SetBlock(suspendedCheckBlock)
	flags := b.Load(b.FieldAddr(result, coroProgramRunResultFlagsV2))
	allowedSuspendedFlags := prog.IntVal(uint64(coroProgramRunBlockedV2|coroProgramRunHasDeadlineV2), word)
	validSuspended := exactTuple(b.BinOp(
		token.EQL,
		b.BinOp(token.AND, flags, allowedSuspendedFlags),
		flags,
	))
	validSuspended = and(validSuspended, b.BinOp(
		token.NEQ,
		b.BinOp(token.AND, flags, prog.IntVal(uint64(coroProgramRunBlockedV2), word)),
		zero,
	))
	b.If(validSuspended, deadlineCheckBlock, failBlock)

	b.SetBlock(deadlineCheckBlock)
	hasDeadline := b.BinOp(
		token.NEQ,
		b.BinOp(token.AND, flags, prog.IntVal(uint64(coroProgramRunHasDeadlineV2), word)),
		zero,
	)
	zeroDeadline := and(
		equalField(coroProgramRunResultDeadlineLoV2, zero),
		equalField(coroProgramRunResultDeadlineHiV2, zero),
	)
	b.If(or(hasDeadline, zeroDeadline), detachedBlock, failBlock)

	b.SetBlock(detachedBlock)
	// Production configuration rejects Python ownership for host-pull entries.
	// Keeping this as a direct return also ensures a hand-built module can never
	// run Py_Finalize while the managed program remains suspended.
	b.Return(prog.IntVal(0, prog.Int32()))

	b.SetBlock(failBlock)
	abort := declareNoArgFunc(pkg, "abort")
	b.Call(abort.Expr)
	b.Unreachable()

	return b.SetBlock(completeBlock)
}

// emitCoroNativeRunLoopV2 is the native pipe target's fixed machine-stack
// host loop. Each runtime call owns at most one bounded scheduler slice. The
// only legal re-entry is an exact Yielded/More/Inline tuple, and it happens
// after the public ABI call has returned, so target requestRun cannot recurse
// through the scheduler stack. Used counts only certified RunSlice reductions;
// it may be zero when target compatibility or fleet-transfer bookkeeping asks
// for an immediate retry without consuming one. Queued, blocked, stale, panic,
// or malformed results fail closed at this native-only boundary.
func emitCoroNativeRunLoopV2(
	b llssa.Builder,
	pkg llssa.Package,
	g, handle llssa.Expr,
	run, continueRun llssa.Function,
) llssa.Builder {
	if b == nil || pkg == nil || run == nil || continueRun == nil {
		panic("native coroutine run loop requires entry builder and exact slice ABI")
	}
	prog := pkg.Prog
	word := prog.Uint32()
	zero := prog.Zero(word)
	budget := prog.IntVal(uint64(coroProgramNativeRunBudgetV2), word)
	result := b.AllocaT(coroProgramRunResultTypeV2(prog))
	initialStatus := b.Call(run.Expr, g, handle, budget, result)
	initialBlock := b.Func.Block(0)

	blocks := b.Func.MakeBlocks(6)
	inspectBlock := blocks[0]
	completeCheckBlock := blocks[1]
	yieldedCheckBlock := blocks[2]
	continueBlock := blocks[3]
	completeBlock := blocks[4]
	failBlock := blocks[5]
	b.Jump(inspectBlock)

	b.SetBlock(inspectBlock)
	status := b.Phi(word)
	b.If(
		b.BinOp(token.EQL, status.Expr, prog.IntVal(uint64(coroProgramDriveCompleteV2), word)),
		completeCheckBlock,
		yieldedCheckBlock,
	)

	and := func(left, right llssa.Expr) llssa.Expr {
		return b.BinOp(token.AND, left, right)
	}
	equalField := func(index int, value llssa.Expr) llssa.Expr {
		return b.BinOp(token.EQL, b.Load(b.FieldAddr(result, index)), value)
	}
	nonzeroField := func(index int) llssa.Expr {
		return b.BinOp(token.NEQ, b.Load(b.FieldAddr(result, index)), zero)
	}

	b.SetBlock(completeCheckBlock)
	validComplete := equalField(coroProgramRunResultFlagsV2, zero)
	validComplete = and(validComplete, b.BinOp(
		token.LEQ,
		b.Load(b.FieldAddr(result, coroProgramRunResultUsedV2)),
		budget,
	))
	for _, index := range []int{
		coroProgramRunResultExecutorSlotV2,
		coroProgramRunResultExecutorGenerationV2,
		coroProgramRunResultEpochV2,
		coroProgramRunResultDeadlineLoV2,
		coroProgramRunResultDeadlineHiV2,
		coroProgramRunResultReservedV2,
	} {
		validComplete = and(validComplete, equalField(index, zero))
	}
	b.If(validComplete, completeBlock, failBlock)

	b.SetBlock(yieldedCheckBlock)
	validYielded := b.BinOp(
		token.EQL,
		status.Expr,
		prog.IntVal(uint64(coroProgramDriveYieldedV2), word),
	)
	validYielded = and(validYielded, equalField(
		coroProgramRunResultFlagsV2,
		prog.IntVal(uint64(coroProgramRunMoreV2|coroProgramRunRequestInlineV2), word),
	))
	used := b.Load(b.FieldAddr(result, coroProgramRunResultUsedV2))
	validYielded = and(validYielded, b.BinOp(token.LEQ, used, budget))
	for _, index := range []int{
		coroProgramRunResultExecutorSlotV2,
		coroProgramRunResultExecutorGenerationV2,
		coroProgramRunResultEpochV2,
	} {
		validYielded = and(validYielded, nonzeroField(index))
	}
	for _, index := range []int{
		coroProgramRunResultDeadlineLoV2,
		coroProgramRunResultDeadlineHiV2,
		coroProgramRunResultReservedV2,
	} {
		validYielded = and(validYielded, equalField(index, zero))
	}
	b.If(validYielded, continueBlock, failBlock)

	b.SetBlock(continueBlock)
	nextStatus := b.Call(
		continueRun.Expr,
		b.Load(b.FieldAddr(result, coroProgramRunResultExecutorSlotV2)),
		b.Load(b.FieldAddr(result, coroProgramRunResultExecutorGenerationV2)),
		b.Load(b.FieldAddr(result, coroProgramRunResultEpochV2)),
		budget,
		result,
	)
	b.Jump(inspectBlock)
	status.AddIncoming(b, []llssa.BasicBlock{initialBlock, continueBlock}, func(index int, _ llssa.BasicBlock) llssa.Expr {
		if index == 0 {
			return initialStatus
		}
		return nextStatus
	})

	b.SetBlock(failBlock)
	abort := declareNoArgFunc(pkg, "abort")
	b.Call(abort.Expr)
	b.Unreachable()

	return b.SetBlock(completeBlock)
}

func declareCoroProgramContinueV1(pkg llssa.Package) llssa.Function {
	return pkg.NewFunc(coroProgramContinueSymbolV1, newSignature(
		[]types.Type{types.Typ[types.Uint32]},
		nil,
	), llssa.InC)
}

type retainedCoroCallbackV1 struct {
	function    llssa.Function
	symbol      string
	reference   string
	description string
}

func declareCoroHostPullCallbacksV1(pkg llssa.Package) []retainedCoroCallbackV1 {
	pointer := types.Typ[types.UnsafePointer]
	word := types.Typ[types.Uint32]
	boolean := types.Typ[types.Bool]
	resultPointer := types.NewPointer(coroProgramRunResultTypeV2(pkg.Prog).RawType())
	declare := func(symbol string, params []types.Type, results []types.Type, reference, description string) retainedCoroCallbackV1 {
		return retainedCoroCallbackV1{
			function:    pkg.NewFunc(symbol, newSignature(params, results), llssa.InC),
			symbol:      symbol,
			reference:   reference,
			description: description,
		}
	}
	return []retainedCoroCallbackV1{
		declare(coroHostNextActionSymbolV1, []types.Type{pointer}, []types.Type{word},
			coroHostNextActionReferenceSymbolV1, "coroutine host next-action callback"),
		declare(coroHostProfileSymbolV1, nil, []types.Type{word},
			coroHostProfileReferenceSymbolV1, "coroutine host profile callback"),
		declare(coroHostNextDeadlineSymbolV1, []types.Type{pointer}, []types.Type{boolean},
			coroHostNextDeadlineReferenceSymbolV1, "coroutine host next-deadline callback"),
		declare(coroHostPublishTimeSymbolV1, []types.Type{word, word}, []types.Type{boolean},
			coroHostPublishTimeReferenceSymbolV1, "coroutine host publish-time callback"),
		declare(coroHostAckCancelSymbolV1, []types.Type{word, word, word, word}, []types.Type{boolean},
			coroHostAckCancelReferenceSymbolV1, "coroutine host cancel-ack callback"),
		declare(coroHostContinueSliceSymbolV1,
			[]types.Type{word, word, word, word, word, word, word, resultPointer}, []types.Type{word},
			coroHostContinueSliceReferenceSymbolV1, "coroutine host continue-slice callback"),
	}
}

const (
	coroProgramContinueReferenceSymbolV1   = "__llgo_coro_program_continue_reference_v1"
	coroHostNextActionReferenceSymbolV1    = "__llgo_coro_host_next_action_reference_v1"
	coroHostProfileReferenceSymbolV1       = "__llgo_coro_host_profile_reference_v1"
	coroHostNextDeadlineReferenceSymbolV1  = "__llgo_coro_host_next_deadline_reference_v1"
	coroHostPublishTimeReferenceSymbolV1   = "__llgo_coro_host_publish_time_reference_v1"
	coroHostAckCancelReferenceSymbolV1     = "__llgo_coro_host_ack_cancel_reference_v1"
	coroHostContinueSliceReferenceSymbolV1 = "__llgo_coro_host_continue_slice_reference_v1"
)

// retainCoroProgramContinueV1 gives the target callback ABI a live relocation
// from the always-selected entry object. The continuation is entered by a
// platform callback, so neither the Go SSA graph nor the entry control-flow
// graph contains an ordinary call edge that would extract its runtime archive
// member. A declaration or llvm.compiler.used alone would also be insufficient
// under --gc-sections/-dead_strip. The volatile load is target-neutral LLVM IR:
// it keeps the internal pointer anchor live, whose initializer in turn retains
// the exact external continuation body without invoking it during startup.
func retainCoroProgramContinueV1(pkg llssa.Package, entry, continuation llssa.Function) {
	retainCoroCallbackV1(pkg, entry, continuation, coroProgramContinueSymbolV1,
		coroProgramContinueReferenceSymbolV1, "coroutine program continuation")
}

func retainCoroCallbackV1(pkg llssa.Package, entry, callbackDeclaration llssa.Function, callbackSymbol, referenceSymbol, description string) {
	if pkg == nil || entry == nil || callbackDeclaration == nil {
		panic(description + " retention requires entry and callback functions")
	}
	module := pkg.Module()
	callback := module.NamedFunction(callbackSymbol)
	entryValue := module.NamedFunction(entry.Name())
	if callback.IsNil() || !callback.IsDeclaration() || entryValue.IsNil() || entryValue.IsDeclaration() {
		panic(description + " retention requires one external callback declaration and defined entry")
	}
	if !module.NamedGlobal(referenceSymbol).IsNil() {
		panic(description + " reference is already defined")
	}
	anchor := llvm.AddGlobal(module, callback.Type(), referenceSymbol)
	anchor.SetInitializer(callback)
	anchor.SetGlobalConstant(true)
	anchor.SetLinkage(llvm.InternalLinkage)
	anchor.SetUnnamedAddr(true)

	first := entryValue.EntryBasicBlock().FirstInstruction()
	if first.IsNil() {
		panic(description + " retention requires a non-empty entry block")
	}
	builder := module.Context().NewBuilder()
	defer builder.Dispose()
	builder.SetInsertPointBefore(first)
	load := builder.CreateLoad(anchor.GlobalValueType(), anchor, "")
	load.SetVolatile(true)
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
