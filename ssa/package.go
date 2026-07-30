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

package ssa

import (
	"fmt"
	"go/token"
	"go/types"
	"log"
	"runtime"
	"strconv"
	"sync"
	"unsafe"

	"github.com/goplus/llgo/internal/env"
	"github.com/goplus/llgo/internal/meta"
	"github.com/goplus/llgo/internal/optlevel"
	"github.com/goplus/llgo/ssa/abi"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/types/typeutil"
)

const (
	PkgPython  = "github.com/goplus/lib/py"
	PkgRuntime = env.LLGoRuntimePkg + "/internal/runtime"
)

// -----------------------------------------------------------------------------

type dbgFlags = int

const (
	DbgFlagInstruction dbgFlags = 1 << iota

	DbgFlagAll = DbgFlagInstruction
)

var (
	debugInstr bool
)

// SetDebug sets debug flags.
func SetDebug(dbgFlags dbgFlags) {
	debugInstr = (dbgFlags & DbgFlagInstruction) != 0
}

func dbgInstrf(format string, args ...any) {
	if debugInstr {
		log.Printf(format, args...)
	}
}

func dbgInstrln(args ...any) {
	if debugInstr {
		log.Println(args...)
	}
}

// -----------------------------------------------------------------------------

// InitFlags is a set of flags for initializing the LLVM library.
type InitFlags int

const (
	InitNativeTarget InitFlags = 1 << iota
	InitAllTargets
	InitAllTargetInfos
	InitAllTargetMCs

	InitNativeAsmPrinter
	InitAllAsmPrinters

	InitAllAsmParsers

	InitNative = InitNativeTarget | InitNativeAsmPrinter
	InitAll    = InitAllTargets | InitAllAsmParsers | InitAllAsmPrinters | InitAllTargetInfos | InitAllTargetMCs
)

// Initialize initializes the LLVM library.
func Initialize(flags InitFlags) {
	if flags&InitAllTargetInfos != 0 {
		llvm.InitializeAllTargetInfos()
	}
	if flags&InitAllTargets != 0 {
		llvm.InitializeAllTargets()
	}
	if flags&InitAllTargetMCs != 0 {
		llvm.InitializeAllTargetMCs()
	}
	if flags&InitAllAsmParsers != 0 {
		llvm.InitializeAllAsmParsers()
	}
	if flags&InitAllAsmPrinters != 0 {
		llvm.InitializeAllAsmPrinters()
	}
	if flags&InitNativeTarget != 0 {
		llvm.InitializeNativeTarget()
	}
	if flags&InitNativeAsmPrinter != 0 {
		llvm.InitializeNativeAsmPrinter()
	}
}

// -----------------------------------------------------------------------------

type aProgram struct {
	ctx   llvm.Context
	typs  typeutil.Map // rawType -> Type
	sizes types.Sizes  // provided by Go compiler
	gocvt goTypes

	patchType func(types.Type) types.Type

	compileMethods func(Package, types.Type)

	rt    *types.Package
	rtget func() *types.Package

	py    *types.Package
	pyget func() *types.Package

	target        *Target
	requestedSpec TargetSpec
	spec          TargetSpec
	td            llvm.TargetData
	tm            llvm.TargetMachine
	named         map[string]Type
	fnnamed       map[string]int

	intType   llvm.Type
	int1Type  llvm.Type
	int8Type  llvm.Type
	int16Type llvm.Type
	int32Type llvm.Type
	int64Type llvm.Type
	voidType  llvm.Type
	voidPtrTy llvm.Type

	c64Type  llvm.Type
	c128Type llvm.Type

	rtStringTy llvm.Type
	rtEfaceTy  llvm.Type
	rtIfaceTy  llvm.Type
	rtSliceTy  llvm.Type
	rtMapTy    llvm.Type
	rtChanTy   llvm.Type

	anyTy     Type
	voidTy    Type
	voidPtr   Type
	voidPPtr  Type
	boolTy    Type
	cstrTy    Type
	cintTy    Type
	cintPtr   Type
	stringTy  Type
	uintptrTy Type
	intTy     Type
	uintTy    Type
	f64Ty     Type
	f32Ty     Type
	c128Ty    Type
	c64Ty     Type
	byteTy    Type
	i32Ty     Type
	u32Ty     Type
	i64Ty     Type
	u64Ty     Type
	u16Ty     Type

	pyObjPtr  Type
	pyObjPPtr Type

	abiTy    Type
	abiTyPtr Type
	deferTy  Type
	deferPtr Type

	pyImpTy      *types.Signature
	pyNewList    *types.Signature
	pyListSetI   *types.Signature
	pyNewTuple   *types.Signature
	pyTupleSetI  *types.Signature
	floatFromDbl *types.Signature
	callNoArgs   *types.Signature
	callOneArg   *types.Signature
	callFOArgs   *types.Signature
	loadPyModS   *types.Signature
	getAttrStr   *types.Signature
	pyUniStr     *types.Signature

	pyBoolFromInt32       *types.Signature
	pyLongFromInt64       *types.Signature
	pyLongFromUint64      *types.Signature
	pyUniFromStrAndSize   *types.Signature
	pyComplexFromDbs      *types.Signature
	pyBytesFromStrAndSize *types.Signature

	mallocTy       *types.Signature
	freeTy         *types.Signature
	memsetInlineTy *types.Signature
	stackSaveTy    *types.Signature
	stackRestoreTy *types.Signature

	createKeyTy *types.Signature
	getSpecTy   *types.Signature
	setSpecTy   *types.Signature
	routineTy   *types.Signature
	destructTy  *types.Signature
	setjmpTy    *types.Signature
	sigsetjmpTy *types.Signature
	longjmpTy   *types.Signature

	printfTy *types.Signature

	paramObjPtr_ *types.Var
	linknameMu   sync.RWMutex
	linkname     map[string]string // pkgPath.nameInPkg => linkname
	localities   *localityInfos
	// logicalLocality makes every llgo:tls/llgo:gls variable follow the
	// stackless scheduler G instead of a physical executor thread.
	logicalLocality bool
	noInterface     map[string]none       // pkgPath.T.method or pkgPath.(*T).method
	abiSymbol       map[string]*AbiSymbol // abi symbol name => AbiSymbol

	ptrSize int

	abi abi.Builder

	is32Bits bool
	disposed bool

	enableGoGlobalDCE     bool
	enableDeadcodeDrop    bool
	disableBoundsChecks   bool
	pthreadStackSize      uint64
	enableLTOPluginMarker bool

	enableFuncInfoMetadata bool
	enableFuncInfoSites    bool
	debugInfoOptimized     bool
}

type AbiSymbol struct {
	Name    string
	PkgPath string
	Raw     types.Type
	Typ     Type
	MSet    *types.MethodSet
}

// A Program presents a program.
type Program = *aProgram

var arch32 = map[string]bool{
	"386":    true,
	"arm":    true,
	"mips":   true,
	"mipsle": true,
	"s390x":  true,
	"wasm":   true,
}

func is32Bits(arch string) bool {
	if v, ok := arch32[arch]; ok {
		return v
	}
	return false
}

// Dispose releases the LLVM resources owned by the program: the context
// (and with it every module built in it), the target machine and the
// target data. The Program and everything created from it must not be
// used afterwards. In-process drivers that compile many packages
// sequentially (llgen goldens, the cltest run harness) call this between
// compiles; without it each compile's C++-side memory lives until the
// process exits.
func (p Program) Dispose() {
	if p == nil || p.disposed {
		return
	}
	p.disposed = true
	p.tm.Dispose()
	p.td.Dispose()
	p.ctx.Dispose()
}

// NewProgram creates a new program.
func NewProgram(target *Target) Program {
	if target == nil {
		target = &Target{
			GOOS:   runtime.GOOS,
			GOARCH: runtime.GOARCH,
		}
	}
	ctx := llvm.NewContext()
	var td llvm.TargetData
	var tm llvm.TargetMachine
	programCreated := false
	defer func() {
		if !programCreated {
			if tm.C != nil {
				tm.Dispose()
			}
			if td.C != nil {
				td.Dispose()
			}
			ctx.Dispose()
		}
	}()
	requestedSpec := target.Spec()
	spec, td, tm := target.targetInfo(ctx, requestedSpec)
	/*
		arch := target.GOARCH
		if arch == "" {
			arch = runtime.GOARCH
		}
		sizes := types.SizesFor("gc", arch)

		// TODO(xsw): Finalize may cause panic, so comment it.
		ctx.Finalize()
	*/
	is32Bits := (td.PointerSize() == 4 || is32Bits(target.GOARCH))
	prog := &aProgram{
		ctx: ctx, gocvt: newGoTypes(),
		target: target, requestedSpec: requestedSpec, spec: spec, td: td, tm: tm, is32Bits: is32Bits,
		ptrSize: td.PointerSize(), named: make(map[string]Type), fnnamed: make(map[string]int),
		linkname: make(map[string]string), localities: newLocalityInfos(),
		noInterface: make(map[string]none), abiSymbol: make(map[string]*AbiSymbol),
		debugInfoOptimized: target.effectiveOptLevel() != optlevel.O0,
	}
	prog.abi.Init(uintptr(prog.ptrSize), (*goProgram)(unsafe.Pointer(prog)))
	programCreated = true
	return prog
}

func (p Program) Target() *Target {
	return p.target
}

// RequestedTargetSpec returns the immutable LLVM configuration requested when
// NewProgram was called. It can differ from TargetSpec when a target relies on
// an external LLVM backend that is unavailable to the in-process binding, or
// when its data layout is incompatible with the legacy GOOS/GOARCH surrogate
// DataLayout.
func (p Program) RequestedTargetSpec() TargetSpec {
	return p.requestedSpec
}

// TargetSpec returns the immutable, effective in-process LLVM configuration
// used to create this program's TargetMachine and DataLayout. It does not
// change if the input Target is modified after NewProgram returns.
func (p Program) TargetSpec() TargetSpec {
	return p.spec
}

func (p Program) TargetData() llvm.TargetData {
	return p.td
}

func (p Program) TargetMachine() llvm.TargetMachine {
	return p.tm
}

func (p Program) DataLayout() string {
	return p.td.String()
}

func (p Program) SetPatch(patchType func(types.Type) types.Type) {
	p.patchType = patchType
}

func (p Program) patch(typ types.Type) types.Type {
	if p.patchType != nil {
		return p.patchType(typ)
	}
	return typ
}

func (p Program) SetCompileMethods(check func(Package, types.Type)) {
	p.compileMethods = check
}

func (p Program) EnableGoGlobalDCE(enable bool) {
	p.enableGoGlobalDCE = enable
}

func (p Program) EnableDeadcodeDrop(enable bool) {
	p.enableDeadcodeDrop = enable
}

func (p Program) DeadcodeDropEnabled() bool {
	return p.enableDeadcodeDrop
}

// DisableBoundsChecks controls index, slice, and slice-to-array conversion
// bounds checks. Other dynamic validity checks, including nil pointer and
// unsafe builtin checks, are not affected.
func (p Program) DisableBoundsChecks(disable bool) {
	p.disableBoundsChecks = disable
}

// BoundsChecksDisabled reports the frozen bounds-check mode used by lowering.
// Coroutine physical planning consumes this bit before emission so its
// structured fault plan cannot disagree with ordinary LLSSA lowering.
func (p Program) BoundsChecksDisabled() bool {
	return p.disableBoundsChecks
}

func (p Program) SetPthreadStackSize(size uint64) {
	p.pthreadStackSize = size
}

func (p Program) EnableLTOPluginMarkers(enable bool) {
	p.enableLTOPluginMarker = enable
}

func (p Program) SetNoInterfaceMethod(fullName string) {
	p.noInterface[fullName] = none{}
}

func (p Program) isNoInterfaceMethod(fn *types.Func) bool {
	if fn == nil {
		return false
	}
	sig, ok := fn.Type().(*types.Signature)
	if !ok || sig.Recv() == nil {
		return false
	}
	_, ok = p.noInterface[FuncName(fn.Pkg(), fn.Name(), sig.Recv(), true)]
	return ok
}

// SetRuntime sets the runtime.
// Its type can be *types.Package or func() *types.Package.
func (p Program) SetRuntime(runtime any) {
	switch v := runtime.(type) {
	case *types.Package:
		p.rt = v
	case func() *types.Package:
		p.rtget = v
	}
}

func (p Program) SetTypeBackground(fullName string, bg Background) {
	p.gocvt.typbg.Store(fullName, bg)
}

func (p Program) SetLinkname(name, link string) {
	p.linknameMu.Lock()
	p.linkname[name] = link
	p.linknameMu.Unlock()
}

func (p Program) Linkname(name string) (link string, ok bool) {
	p.linknameMu.RLock()
	link, ok = p.linkname[name]
	p.linknameMu.RUnlock()
	return
}

func (p Program) runtime() *types.Package {
	if p.rt == nil {
		p.rt = p.rtget()
	}
	return p.rt
}

func (p Program) rtNamed(name string) *types.Named {
	if rt := p.runtime(); rt != nil {
		if rtScope := rt.Scope(); rtScope != nil {
			if obj := rtScope.Lookup(name); obj != nil {
				t := obj.Type()
				for {
					if alias, ok := t.(*types.Alias); ok {
						t = types.Unalias(alias)
					} else {
						break
					}
				}
				t, _ = p.gocvt.cvtNamed(t.(*types.Named))
				return t.(*types.Named)
			}
		}
	}
	panic(fmt.Errorf("runtime type (%s) not found, install from pre-built package or set LLGO_ROOT", name))
}

func (p Program) rtType(name string) Type {
	return p.rawType(p.rtNamed(name))
}

// RuntimeType returns the target-specific physical layout of one named LLGo
// runtime type. Compiler-owned lowering uses this only for typed storage whose
// address may cross an LLVM coroutine suspension; no runtime aggregate is
// passed through a C ABI.
func (p Program) RuntimeType(name string) Type {
	return p.rtType(name)
}

func (p Program) rtEface() llvm.Type {
	if p.rtEfaceTy.IsNil() {
		p.rtEfaceTy = p.rtType("Eface").ll
	}
	return p.rtEfaceTy
}

func (p Program) rtIface() llvm.Type {
	if p.rtIfaceTy.IsNil() {
		p.rtIfaceTy = p.rtType("Iface").ll
	}
	return p.rtIfaceTy
}

func (p Program) rtMap() llvm.Type {
	if p.rtMapTy.IsNil() {
		p.rtMapTy = p.rtType("Map").ll
	}
	return p.rtMapTy
}

func (p Program) rtSlice() llvm.Type {
	if p.rtSliceTy.IsNil() {
		p.rtSliceTy = p.rtType("Slice").ll
	}
	return p.rtSliceTy
}

func (p Program) rtString() llvm.Type {
	if p.rtStringTy.IsNil() {
		p.rtStringTy = p.rtType("String").ll
	}
	return p.rtStringTy
}

func (p Program) rtChan() llvm.Type {
	if p.rtChanTy.IsNil() {
		p.rtChanTy = p.rtType("Chan").ll
	}
	return p.rtChanTy
}

func (p Program) tyComplex64() llvm.Type {
	if p.c64Type.IsNil() {
		ctx := p.ctx
		f32 := ctx.FloatType()
		p.c64Type = ctx.StructType([]llvm.Type{f32, f32}, false)
	}
	return p.c64Type
}

func (p Program) tyComplex128() llvm.Type {
	if p.c128Type.IsNil() {
		ctx := p.ctx
		f64 := ctx.DoubleType()
		p.c128Type = ctx.StructType([]llvm.Type{f64, f64}, false)
	}
	return p.c128Type
}

// NewPackage creates a new package.
func (p Program) NewPackage(name, pkgPath string) Package {
	return p.NewPackageEx(name, pkgPath, false)
}

func (p Program) NewPackageEx(name, pkgPath string, metaCollect bool) Package {
	mod := p.ctx.NewModule(pkgPath)
	mod.SetDataLayout(p.DataLayout())
	mod.SetTarget(p.TargetSpec().Triple)
	// TODO(lijie): enable target output will check module override, but can't
	// pass the snapshot test, so disable it for now
	// if p.target.GOARCH != runtime.GOARCH && p.target.GOOS != runtime.GOOS {
	// 	mod.SetTarget(p.target.Spec().Triple)
	// }

	// TODO(xsw): Finalize may cause panic, so comment it.
	// mod.Finalize()
	gbls := make(map[string]Global)
	fns := make(map[string]Function)
	pyobjs := make(map[string]PyObjRef)
	pymods := make(map[string]Global)
	strs := make(map[string]llvm.Value)
	glbDbgVars := make(map[Expr]bool)
	nullPointerIsValidAttr := mod.Context().CreateEnumAttribute(llvm.AttributeKindID("null_pointer_is_valid"), 0)
	framePointerAttr := mod.Context().CreateStringAttribute("frame-pointer", "non-leaf")
	// Don't need reset p.needPyInit here
	// p.needPyInit = false
	ret := &aPackage{
		mod: mod, path: pkgPath, Prog: p, vars: gbls, fns: fns,
		nullPointerIsValidAttr: nullPointerIsValidAttr,
		framePointerAttr:       framePointerAttr,
		pyobjs:                 pyobjs, pymods: pymods, strs: strs,
		di: nil, cu: nil, glbDbgVars: glbDbgVars,
		export:             make(map[string]string),
		preserveSyms:       make(map[string]struct{}),
		llvmUsedValues:     make([]llvm.Value, 0, 4),
		llvmRetainedValues: make([]llvm.Value, 0, 1),
		runtimeFuncs:       make(map[Type]string),

		abiTypeFakeUseCache: make(map[llvm.Value][]llvm.Value),
	}
	if metaCollect {
		ret.metaBuilder = meta.NewBuilder()
		ret.abiTypeWithUncommon = make(map[llvm.Value]struct{})
	}
	if p.enableGoGlobalDCE {
		p.addVirtualFunctionElimModuleFlag(mod)
	}
	return ret
}

// Struct returns a struct type.
func (p Program) Struct(typs ...Type) Type {
	els := make([]*types.Var, len(typs))
	for i, t := range typs {
		els[i] = types.NewParam(token.NoPos, nil, "_llgo_f"+strconv.Itoa(i), t.raw.Type)
	}
	return p.rawType(types.NewStruct(els, nil))
}

// Defer returns runtime.Defer type.
func (p Program) Defer() Type {
	if p.deferTy == nil {
		p.deferTy = p.rtType("Defer")
	}
	return p.deferTy
}

// DeferPtr returns *runtime.Defer type.
func (p Program) DeferPtr() Type {
	if p.deferPtr == nil {
		p.deferPtr = p.Pointer(p.Defer())
	}
	return p.deferPtr
}

// AbiType returns abi.Type type.
func (p Program) AbiType() Type {
	if p.abiTy == nil {
		p.abiTy = p.rawType(p.rtNamed("Type"))
	}
	return p.abiTy
}

// AbiTypePtr returns *abi.Type type.
func (p Program) AbiTypePtr() Type {
	if p.abiTyPtr == nil {
		p.abiTyPtr = p.Pointer(p.AbiType())
	}
	return p.abiTyPtr
}

// Void returns void type.
func (p Program) Void() Type {
	if p.voidTy == nil {
		p.voidTy = &aType{p.tyVoid(), rawType{types.Typ[types.Invalid]}, vkInvalid}
	}
	return p.voidTy
}

// VoidPtr returns *void type.
func (p Program) VoidPtr() Type {
	if p.voidPtr == nil {
		p.voidPtr = p.rawType(types.Typ[types.UnsafePointer])
	}
	return p.voidPtr
}

// VoidPtrPtr returns **void type.
func (p Program) VoidPtrPtr() Type {
	if p.voidPPtr == nil {
		p.voidPPtr = p.rawType(types.NewPointer(types.Typ[types.UnsafePointer]))
	}
	return p.voidPPtr
}

// Bool returns bool type.
func (p Program) Bool() Type {
	if p.boolTy == nil {
		p.boolTy = p.rawType(types.Typ[types.Bool])
	}
	return p.boolTy
}

// CStr returns *int8 type.
func (p Program) CStr() Type {
	if p.cstrTy == nil { // *int8
		p.cstrTy = p.rawType(types.NewPointer(types.Typ[types.Int8]))
	}
	return p.cstrTy
}

// String returns string type.
func (p Program) String() Type {
	if p.stringTy == nil {
		p.stringTy = p.rawType(types.Typ[types.String])
	}
	return p.stringTy
}

// Any returns the any (empty interface) type.
func (p Program) Any() Type {
	if p.anyTy == nil {
		p.anyTy = p.rawType(tyAny)
	}
	return p.anyTy
}

/*
// Eface returns the empty interface type.
// It is equivalent to Any.
func (p Program) Eface() Type {
	return p.Any()
}
*/

// CIntPtr returns *c.Int type.
func (p Program) CIntPtr() Type {
	if p.cintPtr == nil {
		p.cintPtr = p.Pointer(p.CInt())
	}
	return p.cintPtr
}

// CInt returns c.Int type.
func (p Program) CInt() Type {
	if p.cintTy == nil { // C.int
		p.cintTy = p.rawType(types.Typ[types.Int32]) // TODO(xsw): support 64-bit
	}
	return p.cintTy
}

// Int returns int type.
func (p Program) Int() Type {
	if p.intTy == nil {
		p.intTy = p.rawType(types.Typ[types.Int])
	}
	return p.intTy
}

// Uint returns uint type.
func (p Program) Uint() Type {
	if p.uintTy == nil {
		p.uintTy = p.rawType(types.Typ[types.Uint])
	}
	return p.uintTy
}

// Uintptr returns uintptr type.
func (p Program) Uintptr() Type {
	if p.uintptrTy == nil {
		p.uintptrTy = p.rawType(types.Typ[types.Uintptr])
	}
	return p.uintptrTy
}

// Float64 returns float64 type.
func (p Program) Float64() Type {
	if p.f64Ty == nil {
		p.f64Ty = p.rawType(types.Typ[types.Float64])
	}
	return p.f64Ty
}

// Float32 returns float32 type.
func (p Program) Float32() Type {
	if p.f32Ty == nil {
		p.f32Ty = p.rawType(types.Typ[types.Float32])
	}
	return p.f32Ty
}

// Complex128 returns complex128 type.
func (p Program) Complex128() Type {
	if p.c128Ty == nil {
		p.c128Ty = p.rawType(types.Typ[types.Complex128])
	}
	return p.c128Ty
}

// Complex64 returns complex64 type.
func (p Program) Complex64() Type {
	if p.c64Ty == nil {
		p.c64Ty = p.rawType(types.Typ[types.Complex64])
	}
	return p.c64Ty
}

// Byte returns byte type.
func (p Program) Byte() Type {
	if p.byteTy == nil {
		p.byteTy = p.rawType(types.Typ[types.Byte])
	}
	return p.byteTy
}

// Int32 returns int32 type.
func (p Program) Int32() Type {
	if p.i32Ty == nil {
		p.i32Ty = p.rawType(types.Typ[types.Int32])
	}
	return p.i32Ty
}

// Uint32 returns uint32 type.
func (p Program) Uint32() Type {
	if p.u32Ty == nil {
		p.u32Ty = p.rawType(types.Typ[types.Uint32])
	}
	return p.u32Ty
}

// Uint16 returns uint16 type.
func (p Program) Uint16() Type {
	if p.u16Ty == nil {
		p.u16Ty = p.rawType(types.Typ[types.Uint16])
	}
	return p.u16Ty
}

// Int64 returns int64 type.
func (p Program) Int64() Type {
	if p.i64Ty == nil {
		p.i64Ty = p.rawType(types.Typ[types.Int64])
	}
	return p.i64Ty
}

// Uint64 returns uint64 type.
func (p Program) Uint64() Type {
	if p.u64Ty == nil {
		p.u64Ty = p.rawType(types.Typ[types.Uint64])
	}
	return p.u64Ty
}

// -----------------------------------------------------------------------------

// A Package is a single analyzed Go package containing Members for
// all package-level functions, variables, constants and types it
// declares.  These may be accessed directly via Members, or via the
// type-specific accessor methods Func, Type, Var and Const.
//
// Members also contains entries for "init" (the synthetic package
// initializer) and "init#%d", the nth declared init function,
// and unspecified other things too.
type aPackage struct {
	mod  llvm.Module
	path string

	Prog Program

	nullPointerIsValidAttr llvm.Attribute
	framePointerAttr       llvm.Attribute

	di         diBuilder
	cu         CompilationUnit
	glbDbgVars map[Expr]bool

	vars        map[string]Global
	fns         map[string]Function
	pyobjs      map[string]PyObjRef
	pymods      map[string]Global
	strs        map[string]llvm.Value
	goStrs      map[string]llvm.Value
	fnlink      func(string) string
	methodlink  func(string, *types.Func, *types.Signature) string
	methodEntry func(string, *types.Func, *types.Signature) (Expr, bool)
	// interfaceMethodDescriptor may replace one ABI Method.Ifn_ raw entry
	// with a compiler-owned descriptor pointer.
	interfaceMethodDescriptor func(string, *types.Func, *types.Signature) (Expr, bool)
	runtimeCall               RuntimeCallResolver
	runtimeFuncs              map[Type]string

	iRoutine int

	NeedRuntime         bool
	NeedPyInit          bool
	NeedAbiInit         int // bitmask of Reflect* flags indicating which reflect type-construction operations are used
	MethodByIndex       map[int]none
	MethodByName        map[string]none
	Meta                *meta.PackageMeta
	metaBuilder         *meta.Builder
	abiTypeWithUncommon map[llvm.Value]struct{}

	export               map[string]string   // pkgPath.nameInPkg => exportname
	preserveSyms         map[string]struct{} // set of exported symbol names
	llvmUsedValues       []llvm.Value
	llvmRetainedValues   []llvm.Value
	compilerMetadata     []CompilerMetadataBlob
	coroRootAnchor       string
	coroProgramManifest  string
	coroProgramBootstrap string

	abiTypeFakeUseCache map[llvm.Value][]llvm.Value
}

type none struct{}

type Package = *aPackage

// CompilerMetadataBlob is one byte-exact compiler artifact record. Section is
// its physical object-section name; consumers that also publish an archive
// index must use Data rather than recovering bytes from LLVM IR or object
// formats. Returned blobs never alias package-owned storage.
type CompilerMetadataBlob struct {
	Name    string
	Section string
	Data    []byte
}

func (p Package) Module() llvm.Module {
	return p.mod
}

func (p Package) FinishMetaCollection() error {
	extractOrdinaryEdges(p.metaBuilder, p.mod, p.abiTypeWithUncommon)
	pm, err := p.metaBuilder.Build()
	if err != nil {
		return err
	}
	p.Meta = pm
	p.metaBuilder = nil
	return nil
}

func (p Package) SetExport(name, export string) {
	p.export[name] = export
	p.preserveSyms[export] = struct{}{}
}

func (p Package) ExportFuncs() map[string]string {
	return p.export
}

func (p Package) isPreservedName(name string) bool {
	_, ok := p.preserveSyms[name]
	return ok
}

func (p Package) markLLVMUsed(v llvm.Value) {
	elemTyp := p.Prog.VoidPtr().ll
	p.llvmUsedValues = append(p.llvmUsedValues, llvm.ConstBitCast(v, elemTyp))
}

// markLLVMRetained preserves a linker-discoverable value through compiler
// optimization, object emission, and final-link section garbage collection.
// Unlike llvm.compiler.used, llvm.used is part of the linker retention
// contract and must be reserved for values that are discovered out of band.
func (p Package) markLLVMRetained(v llvm.Value) {
	elemTyp := p.Prog.VoidPtr().ll
	p.llvmRetainedValues = append(p.llvmRetainedValues, llvm.ConstBitCast(v, elemTyp))
}

// AddCompilerMetadataBlob adds an immutable, byte-exact object-section record
// which survives LLVM optimization but may be discarded by the final native
// linker. This is intended for compiler/importer metadata consumed from
// package objects or archives before linking; it must not be used for runtime
// registration records, which require markLLVMRetained instead.
func (p Package) AddCompilerMetadataBlob(name, section string, data []byte) error {
	if name == "" {
		return fmt.Errorf("ssa: compiler metadata blob requires a symbol name")
	}
	if section == "" {
		return fmt.Errorf("ssa: compiler metadata blob %q requires a section", name)
	}
	if len(data) == 0 {
		return fmt.Errorf("ssa: compiler metadata blob %q is empty", name)
	}
	if existing := p.mod.NamedGlobal(name); !existing.IsNil() {
		return fmt.Errorf("ssa: compiler metadata blob symbol %q already exists", name)
	}
	initial := p.mod.Context().ConstString(string(data), false)
	global := llvm.AddGlobal(p.mod, initial.Type(), name)
	global.SetInitializer(initial)
	global.SetGlobalConstant(true)
	global.SetLinkage(llvm.InternalLinkage)
	global.SetUnnamedAddr(true)
	global.SetAlignment(1)
	global.SetSection(section)
	p.markLLVMUsed(global)
	p.compilerMetadata = append(p.compilerMetadata, CompilerMetadataBlob{
		Name:    name,
		Section: section,
		Data:    append([]byte(nil), data...),
	})
	return nil
}

// CompilerMetadataBlobs returns the immutable compiler artifact records added
// to this package. It deliberately exposes producer bytes, not LLVM globals,
// so package archiving remains independent of native object format and LTO
// mode.
func (p Package) CompilerMetadataBlobs() []CompilerMetadataBlob {
	if p == nil || len(p.compilerMetadata) == 0 {
		return nil
	}
	blobs := make([]CompilerMetadataBlob, len(p.compilerMetadata))
	for index, blob := range p.compilerMetadata {
		blobs[index] = CompilerMetadataBlob{
			Name:    blob.Name,
			Section: blob.Section,
			Data:    append([]byte(nil), blob.Data...),
		}
	}
	return blobs
}

func (p Package) MaterializePreserveSyms() {
	p.materializeLLVMUsed("llvm.compiler.used", p.llvmUsedValues)
	p.materializeLLVMUsed("llvm.used", p.llvmRetainedValues)
}

func (p Package) materializeLLVMUsed(name string, values []llvm.Value) {
	if len(values) == 0 {
		return
	}
	elemTyp := p.Prog.VoidPtr().ll
	init := llvm.ConstArray(elemTyp, values)
	global := llvm.AddGlobal(p.mod, init.Type(), name)
	global.SetInitializer(init)
	global.SetLinkage(llvm.AppendingLinkage)
	global.SetSection("llvm.metadata")
}

func (p Package) rtFunc(fnName string) Expr {
	return p.rtFuncAs(fnName, fnName)
}

// rtFuncAs returns the declaration for fnName while tagging the private
// compiler-inserted call marker with logicalName. This lets a frontend give
// two independently planned lowering recipes distinct identities even when
// both recipes intentionally target the same runtime function.
func (p Package) rtFuncAs(logicalName, fnName string) Expr {
	p.NeedRuntime = true
	fn := p.Prog.runtime().Scope().Lookup(fnName).(*types.Func)
	name := FullName(fn.Pkg(), fnName)
	if p.fnlink != nil {
		name = p.fnlink(name)
	}
	sig := fn.Type().(*types.Signature)
	ret := p.NewFunc(name, sig, InGo).Expr
	if p.runtimeCall == nil {
		return ret
	}
	// NewFunc reuses the declaration and its canonical Type. Clone only the
	// Type wrapper so Builder.Call can recognize this exact compiler-inserted
	// runtime helper expression without confusing an ordinary call to the same
	// LLVM declaration, or a helper whose address is merely retained in an ABI
	// table. The raw and LLVM function types remain unchanged.
	typ := *ret.Type
	ret.Type = &typ
	p.runtimeFuncs[ret.Type] = logicalName
	return ret
}

// RuntimeFunc returns a declaration for a function in LLGo's internal runtime.
func (p Package) RuntimeFunc(fnName string) Expr {
	return p.rtFunc(fnName)
}

func (p Package) cFunc(fullName string, sig *types.Signature) Expr {
	return p.NewFunc(fullName, sig, InC).Expr
}

const (
	closureCtx  = "__llgo_ctx"
	closureStub = "__llgo_stub."
)

// closureStub creates or reuses the explicit-context fallback wrapper for
// targets that cannot transport closure ctx in a dedicated register.
func (p Package) closureStub(b Builder, fn Expr, sig *types.Signature, origKind valueKind) (Expr, Expr) {
	prog := b.Prog
	switch origKind {
	case vkFuncDecl:
		wrap := p.closureWrapDecl(fn, sig)
		return wrap.Expr, prog.Nil(prog.VoidPtr())
	case vkFuncPtr:
		wrap := p.closureWrapPtr(sig)
		ptr := b.AllocU(prog.rawType(sig))
		b.Store(ptr, fn)
		data := b.Convert(prog.VoidPtr(), ptr)
		return wrap.Expr, data
	default:
		return fn, prog.Nil(prog.VoidPtr())
	}
}

// -----------------------------------------------------------------------------

// Path returns the package path.
func (p Package) Path() string {
	return p.path
}

// String returns a string representation of the package.
func (p Package) String() string {
	return p.mod.String()
}

// SetResolveLinkname sets a function to resolve linkname.
func (p Package) SetResolveLinkname(fn func(string) string) {
	p.fnlink = fn
}

// SetResolveMethodLinkname installs the resolver used when an ABI method
// table declares a concrete method entry. Unlike SetResolveLinkname, this
// resolver receives both the declared method object and the exact emitted
// signature, including its receiver. Those inputs let a frontend recover the
// exact SSA method or wrapper even when distinct local or structural receiver
// types have colliding legacy textual names.
//
// A nil resolver preserves the legacy SetResolveLinkname behavior.
func (p Package) SetResolveMethodLinkname(fn func(string, *types.Func, *types.Signature) string) {
	p.methodlink = fn
}

// SetResolveMethodEntry installs the resolver for a Method.Tfn_ word. The
// result may be either an exact raw function or a coroutine dispatch
// descriptor; returning ok=true bypasses the legacy closure-context stub.
func (p Package) SetResolveMethodEntry(fn func(string, *types.Func, *types.Signature) (Expr, bool)) {
	p.methodEntry = fn
}

// SetResolveInterfaceMethodDescriptor installs the narrowly-scoped resolver
// for ABI Method.Ifn_ words. A successful result replaces only the interface
// call entry; Method.Tfn_ and explicit raw function addresses keep their
// ordinary callable ABI. ok=false preserves the legacy Ifn_ word.
func (p Package) SetResolveInterfaceMethodDescriptor(fn func(string, *types.Func, *types.Signature) (Expr, bool)) {
	p.interfaceMethodDescriptor = fn
}

// RuntimeCallResolver may replace a compiler-inserted runtime helper call.
// helper is the logical runtime function name passed to rtFunc; fn already
// contains the resolved physical symbol and args are already lowered.
// Returning ok=false preserves the ordinary direct call.
type RuntimeCallResolver func(b Builder, helper string, fn Expr, args []Expr) (ret Expr, ok bool)

// SetResolveRuntimeCall installs the resolver for compiler-inserted runtime
// helper calls. Install it before lowering function bodies. A nil resolver
// preserves the legacy lowering, including the canonical function Type.
func (p Package) SetResolveRuntimeCall(fn RuntimeCallResolver) {
	p.runtimeCall = fn
}

// -----------------------------------------------------------------------------

// AfterInit is called after the package is initialized (init all packages that depends on).
func (p Package) AfterInit(b Builder, ret BasicBlock) {
	doPyLoadModSyms := p.pyHasModSyms()
	if doPyLoadModSyms {
		b.SetBlockEx(ret, afterInit, false)
		p.pyLoadModSyms(b)
	}
}

func (p Package) InitDebug(name, pkgPath string, positioner Positioner) {
	p.di = newDIBuilder(p.Prog, p, positioner)
	p.cu = p.di.createCompileUnit(name, pkgPath)
}

// FinalizeDebug resolves temporary debug metadata and releases the package's
// DI builder. No debug records may be added after this call.
func (p Package) FinalizeDebug() {
	if p.di != nil {
		p.di.finalize()
	}
}

func (p Package) createGlobalStr(v string) (ret llvm.Value) {
	if ret, ok := p.strs[v]; ok {
		return ret
	}
	prog := p.Prog
	if v != "" {
		typ := llvm.ArrayType(prog.tyInt8(), len(v))
		global := llvm.AddGlobal(p.mod, typ, "")
		global.SetInitializer(prog.ctx.ConstString(v, false))
		global.SetLinkage(llvm.PrivateLinkage)
		global.SetGlobalConstant(true)
		global.SetUnnamedAddr(true)
		global.SetAlignment(1)
		ret = llvm.ConstInBoundsGEP(typ, global, []llvm.Value{prog.Val(0).impl})
	} else {
		ret = llvm.ConstNull(prog.CStr().ll)
	}
	p.strs[v] = ret
	return
}

func (p Package) createGlobalBytes(v []byte) (ret llvm.Value) {
	prog := p.Prog
	if len(v) == 0 {
		return llvm.ConstNull(prog.CStr().ll)
	}
	typ := llvm.ArrayType(prog.tyInt8(), len(v))
	global := llvm.AddGlobal(p.mod, typ, "")
	global.SetInitializer(prog.ctx.ConstString(string(v), false))
	global.SetLinkage(llvm.PrivateLinkage)
	global.SetAlignment(1)
	return llvm.ConstInBoundsGEP(typ, global, []llvm.Value{prog.Val(0).impl})
}

// -----------------------------------------------------------------------------

/*
type CodeGenFileType = llvm.CodeGenFileType

const (
	AssemblyFile = llvm.AssemblyFile
	ObjectFile   = llvm.ObjectFile
)

func (p *Package) CodeGen(ft CodeGenFileType) (ret []byte, err error) {
	buf, err := p.prog.targetMachine().EmitToMemoryBuffer(p.mod, ft)
	if err != nil {
		return
	}
	ret = buf.Bytes()
	buf.Dispose()
	return
}

func (p *Package) Bitcode() []byte {
	buf := llvm.WriteBitcodeToMemoryBuffer(p.mod)
	ret := buf.Bytes()
	buf.Dispose()
	return ret
}

func (p *Package) WriteTo(w io.Writer) (int64, error) {
	n, err := w.Write(p.Bitcode())
	return int64(n), err
}

func (p *Package) WriteFile(file string) (err error) {
	f, err := os.Create(file)
	if err != nil {
		return
	}
	defer f.Close()
	return llvm.WriteBitcodeToFile(p.mod, f)
}
*/

// -----------------------------------------------------------------------------
