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

package cl

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/goplus/llgo/cl/blocks"
	"github.com/goplus/llgo/cl/ssawrap"
	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	"github.com/goplus/llgo/internal/typepatch"
	"golang.org/x/tools/go/ssa"

	llssa "github.com/goplus/llgo/ssa"
)

// -----------------------------------------------------------------------------

type dbgFlags = int

const (
	DbgFlagInstruction dbgFlags = 1 << iota
	DbgFlagGoSSA

	DbgFlagAll = DbgFlagInstruction | DbgFlagGoSSA
)

var (
	debugInstr bool
	debugGoSSA bool

	enableCallTracing bool
	enableDbg         bool
	enableDbgSyms     bool
	disableInline     bool

	// enableExportRename enables //export to use different C symbol names than Go function names.
	// This is for TinyGo compatibility when using -target flag for embedded targets.
	// Currently, using -target implies TinyGo embedded target mode.
	enableExportRename bool
)

// Options contains frontend behavior for one package compilation. Drivers that
// may host multiple builds in one process should pass Options explicitly
// instead of changing the legacy package-level Enable* settings.
type Options struct {
	Debug        bool
	DebugSymbols bool
	Trace        bool
	ExportRename bool
	ShadowStack  bool
}

func legacyOptions() Options {
	return Options{
		Debug:        enableDbg,
		DebugSymbols: enableDbgSyms,
		Trace:        enableCallTracing,
		ExportRename: enableExportRename,
		ShadowStack:  os.Getenv("LLGO_SHADOW_STACK") == "1",
	}
}

// SetDebug sets debug flags.
func SetDebug(dbgFlags dbgFlags) {
	debugInstr = (dbgFlags & DbgFlagInstruction) != 0
	debugGoSSA = (dbgFlags & DbgFlagGoSSA) != 0
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

const maxDirectDerefSize = 1 << 20

func (p *context) isLargeNonPointerValue(t llssa.Type) bool {
	raw := types.Unalias(t.RawType())
	if _, ok := raw.Underlying().(*types.Pointer); ok {
		return false
	}
	// Very large values may be addressed far beyond the first guard page. Emit
	// an explicit nil check instead of relying on the eventual load to fault.
	ptrSize := int64(p.prog.PointerSize())
	sizes := &types.StdSizes{WordSize: ptrSize, MaxAlign: ptrSize}
	return sizes.Sizeof(raw) > maxDirectDerefSize
}

func (p *context) isZeroSizedValue(t llssa.Type) bool {
	return p.prog.SizeOf(t) == 0
}

func dbgGoSSADump(f interface {
	WriteTo(io.Writer) (int64, error)
}) {
	if debugGoSSA {
		f.WriteTo(os.Stderr)
	}
}

func dbgGoSSAln(args ...any) {
	if debugGoSSA {
		log.Println(args...)
	}
}

// EnableDebug changes the legacy process-wide default.
// Deprecated: pass Options to NewPackageExWithEmbedMetaOptions.
func EnableDebug(b bool) {
	enableDbg = b
}

// EnableDbgSyms changes the legacy process-wide default.
// Deprecated: pass Options to NewPackageExWithEmbedMetaOptions.
func EnableDbgSyms(b bool) {
	enableDbgSyms = b
}

// EnableTrace changes the legacy process-wide default.
// Deprecated: pass Options to NewPackageExWithEmbedMetaOptions.
func EnableTrace(b bool) {
	enableCallTracing = b
}

// EnableExportRename enables or disables //export with different C symbol names.
// This is enabled when using -target flag for TinyGo compatibility.
// Deprecated: pass Options to NewPackageExWithEmbedMetaOptions.
func EnableExportRename(b bool) {
	enableExportRename = b
}

// -----------------------------------------------------------------------------

type instrOrValue interface {
	ssa.Instruction
	ssa.Value
}

const (
	PkgNormal = iota
	PkgLLGo
	PkgPyModule   // py.<module>
	PkgNoInit     // noinit: a package that don't need to be initialized
	PkgDeclOnly   // decl: a package that only have declarations
	PkgLinkIR     // link llvm ir (.ll)
	PkgLinkExtern // link external object (.a/.so/.dll/.dylib/etc.)
	// PkgLinkBitCode // link bitcode (.bc)
)

type pkgInfo struct {
	kind int
}

type none = struct{}

type context struct {
	prog                 llssa.Program
	pkg                  llssa.Package
	fn                   llssa.Function
	goFn                 *ssa.Function
	fset                 *token.FileSet
	goProg               *ssa.Program
	goTyps               *types.Package
	goPkg                *ssa.Package
	pyMod                string
	skips                map[string]none
	loaded               map[*types.Package]*pkgInfo // loaded packages
	bvals                map[ssa.Value]llssa.Expr    // block values
	coroValueAddrs       map[ssa.Value]llssa.Expr    // compiler-owned addresses for awaited aggregate values
	methodNilDerefChecks map[*ssa.UnOp]none
	patchOriginalInitIf  *ssa.If                     // exact synthetic guard whose successors are logically inverted
	unevaluatedSSA       map[ssa.Instruction]none    // values used only by unsafe.Sizeof/Alignof/Offsetof
	vargs                map[*ssa.Alloc][]llssa.Expr // varargs
	funcs                map[*ssa.Function]llssa.Function
	rawPlainFuncs        map[*ssa.Function]llssa.Function
	linkOnceFns          map[*ssa.Function]none
	stackDefers          map[*ssa.Function]bool
	anonDefers           map[*ssa.Function]bool
	debugDIVars          map[*types.Var]llssa.DIVar
	debugAllocVars       map[*ssa.Alloc]*types.Var
	stackClears          map[ssa.Instruction][]*ssa.Alloc
	finalizerPkgUses     map[*ssa.Package]bool
	runtimeCallerFuncs   map[*ssa.Function]bool
	logicalCallerFuncs   map[*ssa.Function]bool
	compilation          *Compilation
	emissionUniverse     *EmissionUniverse
	emissionOwner        *preparedEmissionPackage
	cacheRegistration    bool // cached archive: skip observers; emitted IR is transient
	pcLineSeq            uint64
	coroEmission         *coroPhysicalEmissionSession
	coroPlainSite        *coroSiteEmissionObserver
	rawPlainBody         bool // compiling the legacy ABI variant of a managed function
	coroOwnerBodySymbols map[string]none
	// preservePatchedNamed keeps an alternate package's named type intact
	// while constructing source-level ABI certificates. Ordinary codegen
	// immediately lowers that replacement to its physical raw shape.
	preservePatchedNamed bool
	coroRootFactories    []coroRootFactoryRegistration
	coroPlainDescriptors map[string]llssa.Expr
	options              Options
	optionsSet           bool

	patches          Patches
	blkInfos         []blocks.Info
	srcLines         map[string][]string
	addrOfFieldAddrs map[token.Pos]none

	inits     []func()
	phis      []func()
	initAfter func()

	state   pkgState
	inCFunc bool
	skipall bool

	cgoCalled   bool
	cgoReturned bool
	cgoArgs     []llssa.Expr
	cgoRet      llssa.Expr
	cgoErrno    llssa.Expr
	cgoErrnoTy  types.Type
	cgoSymbols  []string
	rewrites    map[string]string
	embedMap    goembed.VarMap
	embedInits  []embedInit

	trackCallerFrames bool
	staticGlobalInits map[*ssa.Global]llssa.Expr
	staticInitStores  map[*ssa.Store]none
	staticInitInstrs  map[ssa.Instruction]none
	locality          localityLowering
}

func (p *context) frontendOptions() Options {
	if p != nil && p.optionsSet {
		return p.options
	}
	return legacyOptions()
}

func (p *context) rewriteValue(name string) (string, bool) {
	if p.rewrites == nil {
		return "", false
	}
	dot := strings.LastIndex(name, ".")
	if dot < 0 || dot == len(name)-1 {
		return "", false
	}
	varName := name[dot+1:]
	val, ok := p.rewrites[varName]
	return val, ok
}

func filesUseRuntimeCaller(files []*ast.File) bool {
	for _, file := range files {
		imports := make(map[string]string)
		dotImports := make(map[string]bool)
		for _, imp := range file.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				continue
			}
			switch path {
			case "runtime", "runtime/debug":
			default:
				continue
			}
			name := path[strings.LastIndex(path, "/")+1:]
			if imp.Name != nil {
				switch imp.Name.Name {
				case ".":
					dotImports[path] = true
					continue
				case "_":
					continue
				default:
					name = imp.Name.Name
				}
			}
			imports[name] = path
		}
		if len(imports) == 0 && len(dotImports) == 0 {
			continue
		}
		found := false
		ast.Inspect(file, func(n ast.Node) bool {
			if found {
				return false
			}
			switch n := n.(type) {
			case *ast.SelectorExpr:
				ident, ok := n.X.(*ast.Ident)
				if !ok {
					return true
				}
				if runtimeCallerSelector(imports[ident.Name], n.Sel.Name) {
					found = true
					return false
				}
			case *ast.Ident:
				if (dotImports["runtime"] && isRuntimeCallerFrameName(n.Name)) ||
					(dotImports["runtime/debug"] && n.Name == "Stack") {
					found = true
					return false
				}
			}
			return true
		})
		if found {
			return true
		}
	}
	return false
}

func runtimeCallerSelector(path, name string) bool {
	switch path {
	case "runtime":
		return isRuntimeCallerFrameName(name)
	case "runtime/debug":
		return name == "Stack"
	default:
		return false
	}
}

// isStringPtrType checks if typ is a pointer to the basic string type (*string).
// This is used to validate that -ldflags -X can only rewrite variables of type *string,
// not derived string types like "type T string".
func (p *context) isStringPtrType(typ types.Type) bool {
	ptr, ok := typ.(*types.Pointer)
	if !ok {
		return false
	}
	basic, ok := ptr.Elem().(*types.Basic)
	return ok && basic.Kind() == types.String
}

func (p *context) globalFullName(g *ssa.Global) string {
	name, _, _ := p.varName(g.Pkg.Pkg, g)
	return name
}

func (p *context) rewriteInitStore(store *ssa.Store, g *ssa.Global) (string, bool) {
	if p.rewrites == nil {
		return "", false
	}
	fn := store.Block().Parent()
	if fn == nil || fn.Synthetic != "package initializer" {
		return "", false
	}
	if _, ok := store.Val.(*ssa.Const); !ok {
		return "", false
	}
	if !p.isStringPtrType(g.Type()) {
		return "", false
	}
	value, ok := p.rewriteValue(p.globalFullName(g))
	if !ok {
		return "", false
	}
	return value, true
}

type pkgState byte

const (
	pkgNormal pkgState = iota
	pkgHasPatch
	pkgInPatch

	pkgFNoOldInit = 0x80 // flag if no initFnNameOld
)

func (p *context) compileType(pkg llssa.Package, t *ssa.Type) {
	tn := t.Object().(*types.TypeName)
	if tn.IsAlias() { // don't need to compile alias type
		return
	}
	tnName := tn.Name()
	typ := tn.Type()
	name := llssa.FullName(tn.Pkg(), tnName)
	dbgInstrln("==> NewType", name, typ)
	p.compileMethods(pkg, typ)
	p.compileMethods(pkg, types.NewPointer(typ))
}

func (p *context) compileMethods(pkg llssa.Package, typ types.Type) {
	p.compileMethodsIf(pkg, typ, nil)
}

func (p *context) compileSyntheticMethods(pkg llssa.Package, typ types.Type) {
	p.compileMethodsIf(pkg, typ, func(m *ssa.Function) bool {
		return p.needsLinkOnce(m)
	})
}

func (p *context) compileMethodsIf(pkg llssa.Package, typ types.Type, keep func(*ssa.Function) bool) {
	prog := p.goProg
	mthds := prog.MethodSets.MethodSet(typ)
	for i, n := 0, mthds.Len(); i < n; i++ {
		mthd := mthds.At(i)
		if ssaMthd := p.methodValue(mthd); ssaMthd != nil {
			if keep != nil && !keep(ssaMthd) {
				continue
			}
			if p.omitUnemittedFunction(ssaMthd) {
				continue
			}
			p.compileFuncDecl(pkg, ssaMthd)
		}
	}
}

// Global variable.
func (p *context) compileGlobal(pkg llssa.Package, gbl *ssa.Global) {
	typ := p.patchType(gbl.Type())
	name, vtype, define := p.varName(gbl.Pkg.Pkg, gbl)
	if vtype == pyVar {
		return
	}
	dbgInstrln("==> NewVar", name, typ)
	g, skip := p.localityGlobalStorage(pkg, gbl, name, typ, llssa.Background(vtype))
	if skip {
		return
	}
	if p.emissionUniverse != nil {
		identity, certified, err := p.emissionUniverse.CoroGlobalPhysicalIdentity(gbl)
		if err != nil {
			panic(err)
		}
		if certified && identity.InternalLinkage {
			g.SetInternalLinkage()
		}
	}
	if p.tryEmbedGlobalInit(pkg, gbl, g, name) {
		return
	}
	if value, ok := p.rewriteValue(name); ok {
		if p.isStringPtrType(gbl.Type()) {
			g.Init(pkg.ConstString(value))
		} else {
			log.Printf("warning: ignoring rewrite for non-string variable %s (type: %v)", name, gbl.Type())
			if define {
				g.InitNil()
			}
		}
	} else if init, ok := p.staticGlobalInits[gbl]; ok {
		g.Init(init)
	} else if define {
		g.InitNil()
	}
}

func makeClosureCtx(pkg *types.Package, vars []*ssa.FreeVar) *types.Var {
	n := len(vars)
	flds := make([]*types.Var, n)
	for i, v := range vars {
		name := v.Name()
		if name == "" {
			name = "_"
		}
		flds[i] = types.NewField(token.NoPos, pkg, name, v.Type(), false)
	}
	t := types.NewPointer(types.NewStruct(flds, nil))
	return types.NewParam(token.NoPos, pkg, "__llgo_ctx", t)
}

func isCgoExternSymbol(f *ssa.Function) bool {
	name := f.Name()
	return isCgoCfunc(name) || isCgoCmacro(name) || isCgoC2func(name) || isCgoCMalloc(name)
}

func isCgoCfpvar(name string) bool {
	return strings.HasPrefix(name, "_Cfpvar_")
}

func isCgoCfunc(name string) bool {
	return strings.HasPrefix(name, "_Cfunc_")
}

func isCgoC2func(name string) bool {
	return strings.HasPrefix(name, "_C2func_")
}

func isCgoCMalloc(name string) bool {
	// Go 1.26 emits this generated allocation adapter in addition to the
	// ordinary _Cfunc_* family. It has the same //go:cgo_unsafe_args raw
	// lowering protocol and must not be compiled as an ordinary Go CFG.
	return name == "_cgo_cmalloc"
}

func isCgoCmacro(name string) bool {
	return strings.HasPrefix(name, "_Cmacro_")
}

func isCgoVar(name string) bool {
	return strings.HasPrefix(name, "_cgo_") || isCgoFuncPtrVar(name)
}

func isCgoFuncPtrVar(name string) bool {
	return strings.HasPrefix(name, "__cgo_")
}

func (p *context) methodValue(sel *types.Selection) *ssa.Function {
	f := p.goProg.MethodValue(sel)
	if f != nil && f.Pkg == nil && hasGenericInstantiation(f) {
		p.markLinkOnce(f)
	}
	return f
}

func (p *context) markLinkOnce(f *ssa.Function) {
	if p.linkOnceFns == nil {
		p.linkOnceFns = make(map[*ssa.Function]none)
	}
	p.linkOnceFns[f] = none{}
}

// needsLinkOnce reports whether f may be synthesized in multiple packages and
// therefore needs linkonce linkage when emitted on demand.
func (p *context) needsLinkOnce(f *ssa.Function) bool {
	for ; f != nil; f = f.Parent() {
		if _, ok := p.linkOnceFns[f]; ok {
			return true
		}
		if p.emissionUniverse != nil && p.emissionUniverse.generatedWrapperDefinitionNeedsLinkOnce(f) {
			return true
		}
		if hasGenericInstantiation(f) {
			return true
		}
	}
	return false
}

func hasGenericInstantiation(f *ssa.Function) bool {
	if f.Origin() != nil || len(f.TypeArgs()) != 0 {
		return true
	}
	if sig, ok := f.Type().(*types.Signature); ok && hasInstantiatedRecv(sig.Recv()) {
		return true
	}
	return hasInstantiatedMethodObject(f)
}

func hasInstantiatedMethodObject(f *ssa.Function) bool {
	obj, ok := f.Object().(*types.Func)
	if !ok {
		return false
	}
	if obj.Origin() != obj {
		return true
	}
	sig, ok := obj.Type().(*types.Signature)
	return ok && hasInstantiatedRecv(sig.Recv())
}

func hasInstantiatedRecv(recv *types.Var) bool {
	if recv == nil {
		return false
	}
	if recv.Origin() != recv {
		return true
	}
	if named := recvNamedOk(recv.Type()); named != nil {
		return hasTypeArgs(named)
	}
	return false
}

func (p *context) compileFuncDecl(pkg llssa.Package, f *ssa.Function) (llssa.Function, llssa.PyObjRef, int) {
	entry := p.mustFunctionSymbol(f)
	if entry.planned && entry.plan.Emission == coro.EmitRawPlain {
		// Eager package enumeration still materializes raw-only functions, but
		// their first and only body must use legacy-stack lowering. Starting in
		// managed mode here would manufacture the dead twin this plan excludes.
		return p.compileFuncDeclVariant(pkg, entry.function, true)
	}
	fn, py, kind := p.compileFuncDeclVariant(pkg, f, false)
	var needsRawTwin bool
	if entry.planned && entry.plan.Emission == coro.EmitCoroutine && p.compilation != nil {
		plan := p.compilation.CoroPlan
		needsRawTwin = plan != nil && plan.HasRawPlainVariant(entry.function)
	}
	if needsRawTwin {
		// RawPlainEntry is only the public address/ABI capability. A raw closure
		// helper may need a private legacy-stack twin without being an entry.
		// Eagerly materialize every planned twin in its defining package so a
		// raw caller compiled in another package never leaves an unresolved
		// declaration behind.
		p.compileFuncDeclVariant(pkg, entry.function, true)
	}
	return fn, py, kind
}

// compileFuncDeclVariant materializes either the managed primary or the exact
// legacy Go-ABI body requested by RawPlainEntry. The SSA CFG is shared, but the
// latter deliberately runs through ordinary native-stack lowering: no
// coroutine frame, explicit-status outcome, await, or preemption poll is
// emitted. Calls made while compiling that body are redirected by
// compileFunction to the corresponding raw/plain target entry.
func (p *context) compileFuncDeclVariant(pkg llssa.Package, f *ssa.Function, rawPlain bool) (llssa.Function, llssa.PyObjRef, int) {
	var entry plannedFunctionSymbol
	patchOriginal := f != nil && f.Name() == "init" && f.Signature != nil && f.Signature.Recv() == nil &&
		p.state == pkgHasPatch && p.compilation != nil
	if patchOriginal {
		entry = p.mustPatchOriginalInitFunctionSymbol(f)
	} else {
		entry = p.mustFunctionSymbol(f)
	}
	if rawPlain {
		if patchOriginal {
			entry = p.mustRawPlainFunctionSymbolFromEntry(entry, nil)
		} else {
			entry = p.mustRawPlainFunctionSymbol(f)
		}
	}
	return p.compileFuncDeclVariantEntry(pkg, entry, rawPlain)
}

// compileFuncDeclVariantEntry materializes an already-resolved physical symbol
// role. Ordinary definitions enter through compileFuncDeclVariant; the one
// compiler-owned patch-original await passes its private role directly so a
// second generic lookup cannot collapse it back to the public init symbol.
func (p *context) compileFuncDeclVariantEntry(pkg llssa.Package, entry plannedFunctionSymbol, rawPlain bool) (llssa.Function, llssa.PyObjRef, int) {
	f := entry.function
	pkgTypes, name, ftype := entry.pkgTypes, entry.name, entry.ftype
	if ftype != goFunc {
		return nil, nil, ignoredFunc
	}
	sig := func() *types.Signature {
		oldGoFn := p.goFn
		p.goFn = f
		defer func() {
			p.goFn = oldGoFn
		}()
		return p.patchType(f.Signature).(*types.Signature)
	}()
	sourceSig := sig
	state := p.state
	if entry.patchOriginalInit {
		state = pkgHasPatch
	}
	isInit := (f.Name() == "init" && sig.Recv() == nil)
	var patchOriginalInitIf *ssa.If
	if isInit && (entry.patchOriginalInit || state == pkgHasPatch) {
		// The explicit coroutine role already owns init$hasPatch. Legacy and
		// report-only compilation retain the historical state-derived spelling.
		if !entry.patchOriginalInit {
			name = initFnNameOfHasPatch(name)
		}
		if len(f.Blocks) == 0 || len(f.Blocks[0].Instrs) < 2 {
			panic("patch original initializer has no synthetic guard")
		}
		var ok bool
		patchOriginalInitIf, ok = f.Blocks[0].Instrs[1].(*ssa.If)
		if !ok || patchOriginalInitIf.Block() != f.Blocks[0] || len(f.Blocks[0].Succs) != 2 {
			panic("patch original initializer has an invalid synthetic guard")
		}
	}

	fn := pkg.FuncOf(name)
	if fn != nil && fn.HasBody() {
		return fn, nil, goFunc
	}

	var hasCtx = len(f.FreeVars) > 0
	if hasCtx {
		dbgInstrln("==> NewClosure", name, "type:", sig)
		ctx := makeClosureCtx(pkgTypes, f.FreeVars)
		sig = llssa.FuncAddCtx(ctx, sig)
	} else {
		dbgInstrln("==> NewFunc", name, "type:", sig.Recv(), sig, "ftype:", ftype)
	}
	var physicalABI *coroPhysicalABI
	var outcomePlainABI *outcomePlainPhysicalABI
	if entry.usesCoroPhysicalABI() {
		// x/tools exposes a declared method receiver as fn.Params[0]. Normalize
		// the callable source ABI before adding the two coroutine-owned hidden
		// parameters so compileValue's sourceParamBase maps every SSA parameter
		// to the same physical position.
		sourceSig = coroPhysicalNormalizeSourceSignature(sig)
		abi := newCoroPhysicalABI(p, entry, sourceSig)
		physicalABI = &abi
		sig = abi.physicalSig
		hasCtx = false
	} else if entry.usesOutcomePlainPhysicalABI() {
		sourceSig = coroPhysicalNormalizeSourceSignature(sig)
		abi := newOutcomePlainPhysicalABI(sourceSig)
		outcomePlainABI = &abi
		sig = abi.physicalSig
		hasCtx = false
	}
	managedPhysicalABI := physicalABI != nil || outcomePlainABI != nil
	// Always revisit an existing declaration when materializing its body.
	// NewFuncEx promotes that declaration to linkonce when required; declarations
	// themselves must retain external linkage because LLVM rejects a bodyless
	// linkonce global.
	fn = pkg.NewFuncEx(name, sig, llssa.Background(ftype), hasCtx, p.needsLinkOnce(f))
	if entry.hasWasmImport {
		fn.SetWasmImport(entry.wasmImport.module, entry.wasmImport.name)
	}
	noInlineDirective := hasNoInlineDirective(f)
	runtimeStackNoInline := needsRuntimeStackNoInline(pkgTypes, f)
	pcLineNoInline := p.needsPCLineNoInline(f)
	if disableInline || noInlineDirective || runtimeStackNoInline || pcLineNoInline {
		fn.Inline(llssa.NoInline)
	}
	if noInlineDirective || runtimeStackNoInline || pcLineNoInline {
		fn.DisableTailCalls()
	}
	if rawPlain {
		p.rawPlainFuncs[f] = fn
	} else {
		p.funcs[f] = fn
	}
	if physicalABI != nil && entry.plan.Emission == coro.EmitCoroutine {
		p.emitCoroRootFactory(pkg, entry, *physicalABI, sourceSig, fn)
	}
	isCgo := isCgoExternSymbol(f)
	if nblk := len(f.Blocks); nblk > 0 {
		if p.prog.FuncInfoMetadataEnabled() {
			goName := fn.Name()
			if pkgTypes != nil {
				goName = funcName(pkgTypes, f, false)
			}
			pos := p.funcInfoPosition(f)
			pkg.EmitFuncInfo(fn.Name(), funcInfoDisplayName(goName), pos.Filename, pos.Line, pos.Column)
		}
		var childInits []func()
		if !rawPlain && len(f.AnonFuncs) > 0 {
			parentInits := p.inits
			p.inits = nil
			for _, af := range f.AnonFuncs {
				if p.omitUnemittedFunction(af) {
					continue
				}
				p.compileFuncDecl(pkg, af)
			}
			childInits = append(childInits, p.inits...)
			p.inits = parentInits
		}
		p.cgoCalled = false
		p.cgoReturned = false
		p.cgoArgs = nil
		p.cgoErrno = llssa.Nil
		if physicalABI != nil {
			fn.MakeBlocks(1) // dedicated coroutine ramp entry
		} else if outcomePlainABI != nil {
			fn.MakeBlocks(nblk) // synchronous source CFG plus hidden completion ABI
		} else if isCgo {
			fn.MakeBlocks(1)
		} else {
			fn.MakeBlocks(nblk) // to set fn.HasBody() = true
		}
		if f.Recover != nil && !managedPhysicalABI { // set recover block
			fn.SetRecover(fn.Block(f.Recover.Index))
		}
		dbgEnabled := p.frontendOptions().Debug
		dbgSymsEnabled := p.frontendOptions().DebugSymbols && (f == nil || f.Origin() == nil)
		p.inits = append(p.inits, func() {
			oldFn, oldGoFn, oldMethodNilDerefChecks, oldPatchOriginalInitIf, oldUnevaluatedSSA, oldRawPlainBody := p.fn, p.goFn, p.methodNilDerefChecks, p.patchOriginalInitIf, p.unevaluatedSSA, p.rawPlainBody
			oldLocalityFunction := p.locality.function
			p.fn = fn
			p.goFn = f
			p.patchOriginalInitIf = patchOriginalInitIf
			p.rawPlainBody = rawPlain
			p.locality.function = localityFunction{}
			p.state = state // restore pkgState when compiling funcBody
			oldCoroValueAddrs := p.coroValueAddrs
			defer func() {
				p.fn, p.goFn, p.methodNilDerefChecks, p.patchOriginalInitIf, p.unevaluatedSSA, p.rawPlainBody = oldFn, oldGoFn, oldMethodNilDerefChecks, oldPatchOriginalInitIf, oldUnevaluatedSSA, oldRawPlainBody
				p.coroValueAddrs = oldCoroValueAddrs
				p.locality.function = oldLocalityFunction
			}()
			p.phis = nil
			if dbgSymsEnabled {
				p.debugDIVars = make(map[*types.Var]llssa.DIVar)
				p.debugAllocVars = collectDebugAllocVariables(f)
			} else {
				p.debugDIVars = nil
				p.debugAllocVars = nil
			}
			dbgGoSSADump(f)
			dbgInstrln("==> FuncBody", name)
			b := fn.NewBuilder()
			if dbgEnabled {
				pos := p.goProg.Fset.Position(f.Pos())
				bodyPos := p.getFuncBodyPos(f)
				b.DebugFunction(fn, debugFunctionScope(f), pos, bodyPos)
			}
			// Function bodies are emitted by deferred initializers on one shared
			// context. Reset cgo side-channel state at execution time as well as
			// declaration time so a completed C2 adapter cannot affect the next
			// generated wrapper.
			p.cgoCalled = false
			p.cgoReturned = false
			p.cgoArgs = nil
			p.cgoRet = llssa.Expr{}
			p.cgoErrno = llssa.Nil
			// A stackless physical body always runs with the scheduler-owned
			// runtime G installed, whose sidecar already owns LocalContext.
			// Enter/LeaveLocalContext belongs only to a synchronous native ABI
			// entry (including the separately emitted raw/plain twin).
			if !managedPhysicalABI {
				p.prepareExportedLocalContext(f)
			}
			p.bvals = make(map[ssa.Value]llssa.Expr)
			p.coroValueAddrs = make(map[ssa.Value]llssa.Expr)
			p.methodNilDerefChecks = collectMethodNilDerefChecks(f)
			if p.emissionUniverse != nil {
				var frozen bool
				p.unevaluatedSSA, frozen = p.emissionUniverse.frozenUnsafeLayoutUnevaluatedSSA(f)
				if !frozen {
					panic(fmt.Sprintf("function %q has no frozen unsafe layout-builtin lowering facts", f.String()))
				}
			} else {
				// Legacy one-package compilation has no whole-program inventory.
				p.unevaluatedSSA = collectUnsafeLayoutUnevaluatedSSA(f)
			}
			if p.enableConservativeLivenessClears(f) {
				p.stackClears = p.collectStackClearPlans(f)
			} else {
				p.stackClears = nil
			}
			if physicalABI != nil {
				p.compileCoroPhysicalBody(b, f, *physicalABI, isInit)
				// Anonymous bodies are collected while the physical owner is
				// declared, but their deferred initializers still have to run after
				// the owner's symbols and frame recipe exist. Returning here without
				// them leaves captured coroutine targets as empty LLVM declarations.
				for _, childInit := range childInits {
					childInit()
				}
				b.EndBuild()
				return
			}
			if outcomePlainABI != nil {
				p.compileOutcomePlainPhysicalBody(b, f, *outcomePlainABI, entry.plan, isInit)
				for _, childInit := range childInits {
					childInit()
				}
				b.EndBuild()
				return
			}
			off := make([]int, len(f.Blocks))
			if isCgo {
				p.cgoArgs = make([]llssa.Expr, len(f.Params))
				for i, param := range f.Params {
					p.cgoArgs[i] = p.compileValue(b, param)
				}
			} else {
				for i, block := range f.Blocks {
					off[i] = p.compilePhis(b, block)
				}
			}
			p.blkInfos = blocks.Infos(f.Blocks)
			i := 0
			for {
				block := f.Blocks[i]
				doModInit := (i == 1 && isInit)
				p.compileBlock(b, block, off[i], doModInit)
				if isCgo {
					// just process first block for performance
					break
				}
				if i = p.blkInfos[i].Next; i < 0 {
					break
				}
			}
			for _, phi := range p.phis {
				phi()
			}
			for _, childInit := range childInits {
				childInit()
			}
			b.EndBuild()
		})
	}
	return fn, nil, goFunc
}

// funcInfoDisplayName normalizes anonymous functions to gc's pkg.fn.funcN
// reporting convention (our linker symbols use $N). Linker symbols are not
// affected.
func funcInfoDisplayName(goName string) string {
	return normalizeRuntimeAnonFuncName(goName)
}

func hasNoInlineDirective(f *ssa.Function) bool {
	for f != nil {
		decl, _ := f.Syntax().(*ast.FuncDecl)
		if decl != nil && decl.Doc != nil {
			for _, c := range decl.Doc.List {
				if c.Text == "//go:noinline" {
					return true
				}
			}
		}
		origin := f.Origin()
		if origin == nil || origin == f {
			break
		}
		f = origin
	}
	return false
}

func needsRuntimeStackNoInline(pkg *types.Package, f *ssa.Function) bool {
	if pkg == nil || f == nil || f.Signature.Recv() != nil {
		return false
	}
	switch pkg.Path() {
	case "runtime", "github.com/goplus/llgo/runtime/internal/lib/runtime":
		switch f.Name() {
		case "Caller", "Callers", "callers":
			return true
		}
	case "github.com/goplus/llgo/runtime/internal/clite/debug":
		return f.Name() == "StackTrace"
	}
	return false
}

func (p *context) needsPCLineNoInline(f *ssa.Function) bool {
	if p == nil || f == nil || !p.prog.FuncInfoSitesEnabled() || !p.trackCallerFrames || !p.runtimeCallerFuncs[f] {
		return false
	}
	if !canEmitPCLineLabelsForTarget(p.prog.Target()) {
		return false
	}
	return p.pkg != nil && canTrackCallerFramesForPackage(p.pkg.Path())
}

func (p *context) getFuncBodyPos(f *ssa.Function) token.Position {
	if f.Object() != nil {
		if fn, ok := f.Object().(*types.Func); ok && fn.Scope() != nil {
			return p.goProg.Fset.Position(fn.Scope().Pos())
		}
	}
	return p.goProg.Fset.Position(f.Pos())
}

func (p *context) funcInfoPosition(f *ssa.Function) token.Position {
	if f == nil {
		return token.Position{}
	}
	pos := f.Pos()
	switch syntax := f.Syntax().(type) {
	case *ast.FuncDecl:
		if syntax.Body != nil && len(syntax.Body.List) != 0 {
			pos = syntax.Body.List[0].Pos()
		}
	case *ast.FuncLit:
		if syntax.Body != nil && len(syntax.Body.List) != 0 {
			pos = syntax.Body.List[0].Pos()
		}
	}
	position := p.goProg.Fset.Position(pos)
	position.Filename = directiveFilename(p.goProg.Fset, pos, position.Filename)
	return position
}

// directiveFilename normalizes a //line-directive-adjusted filename to the
// Go runtime's spelling. The package loader expands a relative directive
// (`//line relative.go:1`) to an absolute path under the declaring
// file's directory, but gc reports the directive text verbatim; empty
// directive filenames print as "??". Positions without a directive pass
// through untouched.
func directiveFilename(fset *token.FileSet, pos token.Pos, adjusted string) string {
	if pos == token.NoPos || fset == nil {
		return adjusted
	}
	original := fset.PositionFor(pos, false).Filename
	if original == "" || adjusted == original {
		return adjusted
	}
	if adjusted == "" {
		return "??"
	}
	if rel, err := filepath.Rel(filepath.Dir(original), adjusted); err == nil &&
		rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(rel)
	}
	return adjusted
}

func isGlobal(v *types.Var) bool {
	// TODO(lijie): better implementation
	return strings.HasPrefix(v.Parent().String(), "package ")
}

func (p *context) debugRef(b llssa.Builder, v *ssa.DebugRef) {
	object := v.Object()
	variable, ok := object.(*types.Var)
	if !ok {
		// Not a local variable.
		return
	}
	if variable.IsField() {
		// skip *ssa.FieldAddr
		return
	}
	if isGlobal(variable) {
		// avoid generate local variable debug info of global variable in function
		return
	}
	pos := p.goProg.Fset.Position(v.Pos())
	var value llssa.Expr
	if iv, ok := v.X.(instrOrValue); ok {
		var exists bool
		value, exists = p.bvals[iv]
		if !exists {
			// DebugRef is metadata-only. Do not rematerialize an SSA value that
			// executable lowering deliberately omitted.
			return
		}
	} else {
		value = p.compileValue(b, v.X)
	}
	fn := v.Parent()
	dbgVar := p.getLocalVariable(b, fn, variable)
	scope := variable.Parent()
	diScope := b.DIScope(p.fn, scope)
	if v.IsAddr {
		// *ssa.Alloc
		b.DIDeclare(variable, value, dbgVar, diScope, pos, p.sourceBlock(v.Block().Index))
	} else {
		b.DIValue(variable, value, dbgVar, diScope, pos, p.sourceBlock(v.Block().Index))
	}
}

func (p *context) debugParams(b llssa.Builder, f *ssa.Function) {
	for i, param := range f.Params {
		variable := param.Object().(*types.Var)
		if hasDebugAlloc(p.debugAllocVars, variable) {
			continue
		}
		pos := p.goProg.Fset.Position(param.Pos())
		v := p.compileValue(b, param)
		ty := param.Type()
		argNo := i + 1
		div := b.DIVarParam(p.fn, pos, param.Name(), p.type_(ty, llssa.InGo), argNo)
		if p.debugDIVars != nil {
			p.debugDIVars[variable] = div
		}
		b.DIParam(variable, v, div, p.fn, pos, p.sourceBlock(0))
	}
}

// sourceBlock maps a Go SSA basic-block index to the logical LLVM block used
// by the current lowering. Plain functions retain the historical one-to-one
// Function.Block mapping. A physical coroutine has a dedicated ramp and
// internal suspend blocks, so its source CFG uses an explicit stable map.
func (p *context) sourceBlock(index int) llssa.BasicBlock {
	if block, ok := p.coroEmissionSourceBlock(index); ok {
		return block
	}
	return p.fn.Block(index)
}

func (p *context) compileBlock(b llssa.Builder, block *ssa.BasicBlock, n int, doModInit bool) llssa.BasicBlock {
	oldLocalBlock := p.locality.function.block
	p.locality.function.block = block
	defer func() { p.locality.function.block = oldLocalBlock }()
	var last int
	var pyModInit bool
	var prog = p.prog
	var pkg = p.pkg
	var fn = p.fn
	var instrs = block.Instrs[n:]
	var ret = p.sourceBlock(block.Index)
	b.SetBlock(ret)
	if block.Index == 0 {
		p.emitFunctionPreambleWithCoroPlan(b, block.Parent())
	}
	if block.Index == 0 && p.frontendOptions().Trace && !strings.HasPrefix(fn.Name(), "github.com/goplus/llgo/runtime/internal/runtime.Print") {
		b.Printf("call " + fn.Name() + "\n\x00")
	}
	// place here to avoid wrong current-block
	if p.frontendOptions().DebugSymbols && block.Parent().Origin() == nil && block.Index == 0 {
		p.debugParams(b, block.Parent())
	}

	if doModInit {
		p.initializeLocalGuardsWithCoroPlan(b)
		if p.state != pkgInPatch {
			p.applyEmbedInits(b)
		}
		if pyModInit = p.pyMod != ""; pyModInit {
			last = len(instrs) - 1
			instrs = instrs[:last]
		} else if p.state != pkgHasPatch {
			// TODO(xsw): confirm pyMod don't need to call AfterInit
			p.initAfter = func() {
				pkg.AfterInit(b, ret)
			}
		}
	}

	fnName := block.Parent().Name()
	cgoReturned := false
	isCgoCfunc := isCgoCfunc(fnName) || isCgoCMalloc(fnName)
	isCgoC2 := isCgoC2func(fnName)
	isCgoCmacro := isCgoCmacro(fnName)
	for i, instr := range instrs {
		if _, skip := p.unevaluatedSSA[instr]; skip {
			continue
		}
		if p.compileCoroInstructionPrologue(b, instr) {
			continue
		}
		if i == 1 && doModInit && p.state == pkgInPatch { // in patch package but no pkgFNoOldInit
			initFnNameOld := initFnNameOfHasPatch(p.fn.Name())
			if !p.compileCoroPatchInitAtBlock(b) {
				fnOld := pkg.NewFunc(initFnNameOld, llssa.NoArgsNoRet, llssa.InC)
				b.Call(fnOld.Expr)
			}
		}
		if isCgoCfunc || isCgoC2 || isCgoCmacro {
			switch instr := instr.(type) {
			case *ssa.Alloc:
				// return value allocation
				p.compileInstr(b, instr)
			case *ssa.UnOp:
				// load cgo function pointer
				varName := instr.X.Name()
				if instr.Op == token.MUL && strings.HasPrefix(varName, "_cgo_") {
					p.cgoSymbols = append(p.cgoSymbols, varName)
					p.compileInstr(b, instr)
				}
			case *ssa.ChangeType:
				// Value-only conversion selected by the frozen dedicated cgo
				// lowering plan (notably size_t -> uint64 in Go 1.26's
				// _Cfunc__CMalloc wrapper).
				p.compileInstr(b, instr)
			case *ssa.Call:
				if isCgoCmacro {
					p.cgoRet = p.compileValue(b, instr.Call.Args[0])
					p.cgoCalled = true
				} else {
					// call c function
					p.compileInstr(b, instr)
					p.cgoCalled = true
					if p.cgoReturned {
						cgoReturned = true
						goto end
					}
				}
			case *ssa.Return:
				// return cgo function result
				if isCgoCmacro {
					ty := p.type_(instr.Results[0].Type(), llssa.InGo)
					p.cgoRet.Type = p.prog.Pointer(ty)
					p.cgoRet = b.Load(p.cgoRet)
				} else {
					if p.emissionUniverse != nil {
						value, direct, err := p.emissionUniverse.coroProgramIR.cgoDirectReturn(instr)
						if err != nil {
							panic(fmt.Errorf("load frozen cgo direct-return recipe: %w", err))
						}
						if direct {
							p.cgoRet = p.compileValue(b, value)
						}
					} else if len(instr.Results) == 1 {
						// Legacy one-package compilation has no ProgramIR. Admit
						// only an already-lowered direct call; production whole
						// builds always consume the frozen recipe above.
						if call, ok := instr.Results[0].(*ssa.Call); ok {
							if value, ready := p.bvals[call]; ready {
								p.cgoRet = value
							}
						}
					}
					p.cgoReturn(b, isCgoC2)
					cgoReturned = true
					continue
				}
				b.Return(p.cgoRet)
				cgoReturned = true
			}
		} else {
			p.compileInstr(b, instr)
		}
		if isTerminatingInstruction(instr) {
			continue
		}
		p.clearDeadAllocs(b, instr)
	}
	// is cgo cfunc but not return yet, some funcs has multiple blocks
	if (isCgoCfunc || isCgoC2 || isCgoCmacro) && !cgoReturned {
		if !p.cgoCalled {
			panic("cgo cfunc not called")
		}
		for _, block := range block.Parent().Blocks {
			for _, instr := range block.Instrs {
				if _, ok := instr.(*ssa.Return); ok {
					p.cgoReturn(b, isCgoC2)
					goto end
				}
			}
		}
	}
end:
	if pyModInit {
		jump := block.Instrs[n+last].(*ssa.Jump)
		jumpTo := p.jumpTo(jump)
		modPath := p.pyMod
		modName := pysymPrefix + modPath
		modPtr := pkg.PyNewModVar(modName, true).Expr
		mod := b.Load(modPtr)
		cond := b.BinOp(token.NEQ, mod, prog.Nil(mod.Type))
		newBlk := fn.MakeBlock()
		b.If(cond, jumpTo, newBlk)
		b.SetBlockEx(newBlk, llssa.AtEnd, false)
		b.Store(modPtr, b.PyImportMod(modPath))
		b.Jump(jumpTo)
	}
	return ret
}

const (
	RuntimeInit = llssa.PkgRuntime + ".init"
)

func isAny(t types.Type) bool {
	if t, ok := t.Underlying().(*types.Interface); ok {
		return t.Empty()
	}
	return false
}

func intVal(v ssa.Value) int64 {
	if c, ok := v.(*ssa.Const); ok {
		if iv, exact := constant.Int64Val(c.Value); exact {
			return iv
		}
	}
	panic("intVal: ssa.Value is not a const int")
}

func skipUnusedArrayDeref(v *ssa.UnOp) bool {
	if v.Op != token.MUL {
		return false
	}
	block := v.Block()
	if block == nil || len(block.Succs) != 1 || !strings.HasPrefix(block.Succs[0].Comment, "rangeindex.") {
		return false
	}
	refs, ok := nonDebugReferrers(v)
	if !ok || len(refs) != 0 {
		return false
	}
	if _, ok := v.Type().Underlying().(*types.Array); !ok {
		return false
	}
	return true
}

func shouldAssertDirectNilDeref(v *ssa.UnOp) bool {
	if v.Op != token.MUL {
		return false
	}
	if _, ok := v.X.(*ssa.Parameter); !ok {
		return false
	}
	switch types.Unalias(v.Type()).Underlying().(type) {
	case *types.Basic, *types.Pointer, *types.Chan, *types.Map, *types.Slice, *types.Interface:
		return true
	}
	return false
}

func (p *context) cgoErrnoType() types.Type {
	if p.cgoErrnoTy != nil {
		return p.cgoErrnoTy
	}
	if pkg := p.goProg.ImportedPackage("syscall"); pkg != nil {
		if obj := pkg.Pkg.Scope().Lookup("Errno"); obj != nil {
			p.cgoErrnoTy = obj.Type()
			return p.cgoErrnoTy
		}
	}
	p.cgoErrnoTy = types.Typ[types.Int32]
	return p.cgoErrnoTy
}

func (p *context) cgoReturn(b llssa.Builder, isCgoC2 bool) {
	if !isCgoC2 {
		p.cgoEmitReturn(b, p.cgoRet)
		return
	}
	sig := p.fn.Type.RawType().(*types.Signature)
	if p.hasCoroPhysicalBody() {
		if p.goFn == nil || p.goFn.Signature == nil {
			panic("physical cgo C2func has no source signature")
		}
		sig = p.goFn.Signature
	}
	if sig.Results().Len() != 2 {
		panic("cgo C2func should return (result, error)")
	}
	p.cgoC2Return(b, p.cgoRet, p.patchType(sig.Results().At(1).Type()))
}

func (p *context) cgoEmitReturn(b llssa.Builder, results ...llssa.Expr) {
	if p.hasCoroPhysicalBody() {
		p.compileCoroReturn(b, results)
		return
	}
	b.Return(results...)
}

func (p *context) cgoC2Return(b llssa.Builder, ret llssa.Expr, errType types.Type) {
	errTy := p.type_(errType, llssa.InGo)
	nilSlot := b.AllocU(errTy)
	b.Store(nilSlot, p.prog.Zero(errTy))
	nilErr := b.Load(nilSlot)
	if p.cgoErrno.IsNil() {
		p.cgoEmitReturn(b, ret, nilErr)
		return
	}
	i32 := p.type_(types.Typ[types.Int32], llssa.InGo)
	errno := p.cgoErrno
	if !types.Identical(errno.RawType(), i32.RawType()) {
		errno = b.Convert(i32, errno)
	}
	zero := p.prog.Zero(i32)
	cond := b.BinOp(token.NEQ, errno, zero)
	errnoVal := b.Convert(p.type_(p.cgoErrnoType(), llssa.InGo), errno)
	errIface := b.MakeInterface(errTy, errnoVal)
	fn := b.Func
	errBlk := fn.MakeBlock()
	okBlk := fn.MakeBlock()
	b.If(cond, errBlk, okBlk)
	b.SetBlockEx(errBlk, llssa.AtEnd, false)
	p.cgoEmitReturn(b, ret, errIface)
	b.SetBlockEx(okBlk, llssa.AtEnd, false)
	p.cgoEmitReturn(b, ret, nilErr)
}

func (p *context) isVArgs(v ssa.Value) (ret []llssa.Expr, ok bool) {
	switch v := v.(type) {
	case *ssa.Alloc:
		ret, ok = p.vargs[v] // varargs: this is a varargs index
	}
	return
}

func (p *context) checkVArgs(v *ssa.Alloc, t *types.Pointer) bool {
	if v.Comment == "varargs" { // this maybe a varargs allocation
		if arr, ok := t.Elem().(*types.Array); ok {
			if isAny(arr.Elem()) && isAllocVargs(p, v) {
				p.vargs[v] = make([]llssa.Expr, arr.Len())
				return true
			}
		}
	}
	return false
}

func (p *context) skipSyntheticMakeSliceAlloc(v *ssa.Alloc) bool {
	refs, ok := nonDebugReferrers(v)
	if !ok || len(refs) != 1 {
		return false
	}
	slice, ok := refs[0].(*ssa.Slice)
	if !ok {
		return false
	}
	_, ok = p.syntheticMakeSliceCap(slice)
	return ok
}

func (p *context) compileSyntheticMakeSlice(b llssa.Builder, v *ssa.Slice) (llssa.Expr, bool) {
	capacity, ok := p.syntheticMakeSliceCap(v)
	if !ok {
		return llssa.Expr{}, false
	}
	t := p.type_(v.Type(), llssa.InGo)
	length := p.compileValue(b, v.High)
	return b.MakeSlice(t, length, capacity), true
}

func (p *context) syntheticMakeSliceCap(v *ssa.Slice) (llssa.Expr, bool) {
	alloc, ok := v.X.(*ssa.Alloc)
	if !ok || alloc.Comment != "makeslice" || v.Low != nil || v.High == nil || v.Max != nil {
		return llssa.Expr{}, false
	}
	t, ok := alloc.Type().(*types.Pointer)
	if !ok {
		return llssa.Expr{}, false
	}
	arr, ok := t.Elem().(*types.Array)
	if !ok {
		return llssa.Expr{}, false
	}
	if high, ok := v.High.(*ssa.Const); ok {
		if n, exact := constant.Int64Val(high.Value); exact && n >= 0 && n <= arr.Len() {
			return llssa.Expr{}, false
		}
	}
	return p.prog.IntVal(uint64(arr.Len()), p.prog.Int()), true
}

func isAllocVargs(ctx *context, v *ssa.Alloc) bool {
	refs, ok := nonDebugReferrers(v)
	if !ok || len(refs) == 0 {
		return false
	}
	n := len(refs)
	lastref := refs[n-1]
	if i, ok := lastref.(*ssa.Slice); ok {
		if refs, _ = nonDebugReferrers(i); len(refs) == 1 {
			var call *ssa.CallCommon
			switch ref := refs[0].(type) {
			case *ssa.Call:
				call = &ref.Call
			case *ssa.Defer:
				call = &ref.Call
			case *ssa.Go:
				call = &ref.Call
			default:
				return false
			}
			if call.IsInvoke() {
				return llssa.HasNameValist(call.Signature())
			}
			return ctx.funcKind(call.Value) == fnHasVArg
		}
	}
	return false
}

func (p *context) enableConservativeLivenessClears(fn *ssa.Function) bool {
	if fn == nil || isCgoExternSymbol(fn) {
		return false
	}
	pkg := declaredSSAPackage(fn)
	if pkg == nil {
		return false
	}
	return p.packageUsesRuntimeSetFinalizer(pkg)
}

func (p *context) packageUsesRuntimeSetFinalizer(pkg *ssa.Package) bool {
	if pkg == nil {
		return false
	}
	if uses, ok := p.finalizerPkgUses[pkg]; ok {
		return uses
	}
	if p.finalizerPkgUses == nil {
		p.finalizerPkgUses = make(map[*ssa.Package]bool)
	}
	uses := false
	seen := make(map[*ssa.Function]bool)
	check := func(fn *ssa.Function) bool {
		return p.functionUsesRuntimeSetFinalizer(fn, seen)
	}
	for _, member := range pkg.Members {
		if fn, ok := member.(*ssa.Function); ok && check(fn) {
			uses = true
			break
		}
	}
	if !uses && pkg.Prog != nil {
		for _, member := range pkg.Members {
			typ, ok := member.(*ssa.Type)
			if !ok {
				continue
			}
			for _, recv := range []types.Type{typ.Type(), types.NewPointer(typ.Type())} {
				methods := pkg.Prog.MethodSets.MethodSet(recv)
				for i := 0; i < methods.Len(); i++ {
					obj, ok := methods.At(i).Obj().(*types.Func)
					if !ok {
						continue
					}
					if check(pkg.Prog.FuncValue(obj.Origin())) {
						uses = true
						break
					}
				}
				if uses {
					break
				}
			}
			if uses {
				break
			}
		}
	}
	p.finalizerPkgUses[pkg] = uses
	return uses
}

func declaredSSAPackage(fn *ssa.Function) *ssa.Package {
	for fn != nil {
		if fn.Pkg != nil {
			return fn.Pkg
		}
		if origin := fn.Origin(); origin != nil && origin != fn {
			fn = origin
			continue
		}
		fn = fn.Parent()
	}
	return nil
}

func (p *context) functionUsesRuntimeSetFinalizer(fn *ssa.Function, seen map[*ssa.Function]bool) bool {
	if fn == nil || seen[fn] {
		return false
	}
	seen[fn] = true
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			switch instr := instr.(type) {
			case *ssa.Call:
				if p.isRuntimeSetFinalizerCall(&instr.Call) {
					return true
				}
			case *ssa.Defer:
				if p.isRuntimeSetFinalizerCall(&instr.Call) {
					return true
				}
			case *ssa.Go:
				if p.isRuntimeSetFinalizerCall(&instr.Call) {
					return true
				}
			}
		}
	}
	for _, anon := range fn.AnonFuncs {
		if p.functionUsesRuntimeSetFinalizer(anon, seen) {
			return true
		}
	}
	return false
}

func hasConservativeGCPointers(t types.Type, seen map[types.Type]bool) bool {
	if t == nil {
		return false
	}
	t = types.Unalias(t)
	if seen[t] {
		return false
	}
	seen[t] = true
	switch t := t.Underlying().(type) {
	case *types.Pointer, *types.Slice, *types.Map, *types.Chan, *types.Signature, *types.Interface:
		return true
	case *types.Basic:
		return t.Kind() == types.String || t.Kind() == types.UnsafePointer
	case *types.Array:
		return hasConservativeGCPointers(t.Elem(), seen)
	case *types.Struct:
		for i := 0; i < t.NumFields(); i++ {
			if hasConservativeGCPointers(t.Field(i).Type(), seen) {
				return true
			}
		}
	}
	return false
}

func (p *context) shouldClearAlloc(v *ssa.Alloc) bool {
	if v == nil || v.Heap || v.Comment == "varargs" || v.Comment == "makeslice" {
		return false
	}
	ptr, ok := v.Type().Underlying().(*types.Pointer)
	return ok && hasConservativeGCPointers(ptr.Elem(), map[types.Type]bool{})
}

func cyclicBlocks(blocks []*ssa.BasicBlock) map[*ssa.BasicBlock]bool {
	// Compute strongly connected components once per function so liveness
	// candidates do not repeat reachability walks over the same CFG.
	cyclic := make(map[*ssa.BasicBlock]bool)
	indices := make(map[*ssa.BasicBlock]int, len(blocks))
	lowlinks := make(map[*ssa.BasicBlock]int, len(blocks))
	onStack := make(map[*ssa.BasicBlock]bool, len(blocks))
	stack := make([]*ssa.BasicBlock, 0, len(blocks))
	nextIndex := 1

	var visit func(*ssa.BasicBlock)
	visit = func(block *ssa.BasicBlock) {
		if block == nil {
			return
		}
		index := nextIndex
		nextIndex++
		indices[block] = index
		lowlinks[block] = index
		stack = append(stack, block)
		onStack[block] = true

		for _, succ := range block.Succs {
			if succ == nil {
				continue
			}
			if indices[succ] == 0 {
				visit(succ)
				lowlinks[block] = min(lowlinks[block], lowlinks[succ])
			} else if onStack[succ] {
				lowlinks[block] = min(lowlinks[block], indices[succ])
			}
		}
		if lowlinks[block] != index {
			return
		}

		var component []*ssa.BasicBlock
		for {
			last := len(stack) - 1
			member := stack[last]
			stack = stack[:last]
			onStack[member] = false
			component = append(component, member)
			if member == block {
				break
			}
		}
		if len(component) > 1 {
			for _, member := range component {
				cyclic[member] = true
			}
			return
		}
		for _, succ := range block.Succs {
			if succ == block {
				cyclic[block] = true
				return
			}
		}
	}

	for _, block := range blocks {
		if block != nil && indices[block] == 0 {
			visit(block)
		}
	}
	return cyclic
}

type instructionOperandScratch struct {
	inline   [8]*ssa.Value
	operands []*ssa.Value
}

type stackLivenessState struct {
	value       ssa.Value
	slotAddress bool
}

func (s *instructionOperandScratch) uses(instr ssa.Instruction, v ssa.Value) bool {
	if instr == nil || v == nil {
		return false
	}
	if s.operands == nil {
		s.operands = s.inline[:0]
	} else {
		s.operands = s.operands[:0]
	}
	// Referrer lists are mutable in x/tools. Re-scan operands deliberately so
	// stale entries that no longer name v make the liveness proof fail closed.
	s.operands = instr.Operands(s.operands)
	for _, operand := range s.operands {
		if operand != nil && *operand == v {
			return true
		}
	}
	return false
}

func instructionUsesValue(instr ssa.Instruction, v ssa.Value) bool {
	var scratch instructionOperandScratch
	return scratch.uses(instr, v)
}

func instructionRetainsAddress(instr ssa.Instruction, v ssa.Value) bool {
	// Side-effecting instructions can hide a stack address from the SSA
	// referrer graph, so the liveness walk cannot follow later aliases.
	//
	// This switch deliberately defaults to retaining: every known instruction
	// must either identify its non-retaining destination operand below or be a
	// pure value instruction whose uses the recursive walk can follow. A new
	// SSA instruction therefore fails closed until it is classified here.
	switch instr := instr.(type) {
	case *ssa.Store:
		if instr.Val == v {
			return true
		}
		return instr.Addr != v
	case *ssa.MapUpdate:
		if instr.Key == v || instr.Value == v {
			return true
		}
		return instr.Map != v
	case *ssa.Send:
		if instr.X == v {
			return true
		}
		return instr.Chan != v
	case *ssa.Call:
		// Calls may retain any operand, including invoke receivers.
		return true
	case *ssa.Select:
		channelOperand := false
		for _, state := range instr.States {
			if state.Dir == types.SendOnly && state.Send == v {
				return true
			}
			if state.Chan == v {
				channelOperand = true
			}
		}
		return !channelOperand
	case *ssa.Alloc, *ssa.BinOp, *ssa.UnOp, *ssa.ChangeType,
		*ssa.Convert, *ssa.MultiConvert, *ssa.ChangeInterface,
		*ssa.SliceToArrayPointer, *ssa.MakeInterface, *ssa.MakeMap,
		*ssa.MakeChan, *ssa.MakeSlice, *ssa.Slice, *ssa.FieldAddr,
		*ssa.Field, *ssa.IndexAddr, *ssa.Index, *ssa.Lookup, *ssa.Range,
		*ssa.Next, *ssa.TypeAssert, *ssa.Extract:
		// These instructions only produce values. Recursively walking the
		// result's referrers preserves address provenance until a load.
		return false
	}
	return true
}

func isTerminatingInstruction(instr ssa.Instruction) bool {
	switch instr.(type) {
	case *ssa.Jump, *ssa.Return, *ssa.If, *ssa.Panic:
		return true
	}
	return false
}

func (p *context) isRuntimeSetFinalizerCall(call *ssa.CallCommon) bool {
	if call == nil {
		return false
	}
	fn, ok := call.Value.(*ssa.Function)
	if !ok || fn == nil || fn.Pkg == nil || fn.Pkg.Pkg == nil {
		return false
	}
	if fn.Name() != "SetFinalizer" {
		return false
	}
	switch fn.Pkg.Pkg.Path() {
	case "runtime", "github.com/goplus/llgo/runtime/internal/lib/runtime":
		return true
	default:
		return false
	}
}

func (p *context) lastUseInBlock(v ssa.Value, blk *ssa.BasicBlock, order map[ssa.Instruction]int) (ssa.Instruction, bool) {
	var scratch instructionOperandScratch
	states := make(map[stackLivenessState]bool)
	_, slotAddress := v.(*ssa.Alloc)
	return p.lastUseInBlockValue(v, blk, order, states, slotAddress, &scratch)
}

func (p *context) lastUseInBlockValue(
	v ssa.Value,
	blk *ssa.BasicBlock,
	order map[ssa.Instruction]int,
	seen map[stackLivenessState]bool,
	slotAddress bool,
	scratch *instructionOperandScratch,
) (ssa.Instruction, bool) {
	state := stackLivenessState{value: v, slotAddress: slotAddress}
	if v == nil || seen[state] {
		return nil, true
	}
	seen[state] = true
	refs := v.Referrers()
	if refs == nil {
		return nil, true
	}
	// x/tools defines Referrers for function-local values as the inverse of
	// Instruction.Operands. Rely on that builder contract for completeness,
	// but reject stale entries that no longer name v or are no longer
	// scheduled in this block.
	var last ssa.Instruction
	updateLast := func(instr ssa.Instruction) {
		if instr == nil {
			return
		}
		if last == nil || order[instr] > order[last] {
			last = instr
		}
	}
	for _, ref := range *refs {
		switch ref := ref.(type) {
		case *ssa.DebugRef:
			continue
		case *ssa.Defer, *ssa.Go, *ssa.MakeClosure, *ssa.Phi:
			return nil, false
		default:
			instr, ok := ref.(ssa.Instruction)
			if !ok || !scratch.uses(instr, v) {
				return nil, false
			}
			if instr.Block() != blk {
				return nil, false
			}
			if _, ok := order[instr]; !ok {
				return nil, false
			}
			if slotAddress && instructionRetainsAddress(instr, v) {
				return nil, false
			}
			if refVal, ok := ref.(ssa.Value); ok {
				nextSlotAddress := slotAddress
				if unop, ok := refVal.(*ssa.UnOp); ok && unop.Op == token.MUL {
					// A load copies the slot contents; the result no longer
					// aliases the stack storage that will be cleared.
					nextSlotAddress = false
				}
				use, ok := p.lastUseInBlockValue(refVal, blk, order, seen, nextSlotAddress, scratch)
				if !ok {
					return nil, false
				}
				if use != nil {
					updateLast(use)
					continue
				}
			}
			updateLast(instr)
		}
	}
	return last, true
}

func (p *context) collectStackClearPlans(fn *ssa.Function) map[ssa.Instruction][]*ssa.Alloc {
	plans := make(map[ssa.Instruction][]*ssa.Alloc)
	blockCyclicity := cyclicBlocks(fn.Blocks)
	for _, blk := range fn.Blocks {
		if blockCyclicity[blk] {
			continue
		}
		var order map[ssa.Instruction]int
		for _, instr := range blk.Instrs {
			alloc, ok := instr.(*ssa.Alloc)
			if !ok || !p.shouldClearAlloc(alloc) {
				continue
			}
			// Deliberately limit clearing to exact, non-escaping slots whose
			// complete use graph stays in one acyclic basic block. This can
			// retain stale roots, but it cannot guess across control-flow,
			// closure, defer, goroutine, or heap-escape boundaries.
			useBlk := alloc.Block()
			if useBlk == nil || useBlk != blk {
				continue
			}
			if order == nil {
				order = make(map[ssa.Instruction]int, len(blk.Instrs))
				for i, useInstr := range blk.Instrs {
					order[useInstr] = i
				}
			}
			last, ok := p.lastUseInBlock(alloc, useBlk, order)
			if ok && last != nil && !isTerminatingInstruction(last) {
				plans[last] = append(plans[last], alloc)
			}
		}
	}
	return plans
}

func (p *context) clearAlloc(b llssa.Builder, alloc *ssa.Alloc) {
	// Eligible allocs are lowered before their later clear sites. Reuse that
	// exact stack pointer; rematerializing the alloc here would clear unrelated
	// storage and invalidate the liveness proof.
	ptr, ok := p.bvals[alloc]
	if !ok {
		log.Panicln("stack clear for unmaterialized alloc:", alloc)
	}
	elem := b.Prog.Elem(ptr.Type)
	b.StoreVolatile(ptr, p.prog.Zero(elem))
}

func (p *context) clearDeadAllocs(b llssa.Builder, instr ssa.Instruction) {
	allocs := p.stackClears[instr]
	if len(allocs) == 0 {
		return
	}
	for _, alloc := range allocs {
		p.clearAlloc(b, alloc)
	}
}

func isPhi(i ssa.Instruction) bool {
	_, ok := i.(*ssa.Phi)
	return ok
}

func (p *context) compilePhis(b llssa.Builder, block *ssa.BasicBlock) int {
	ret := p.sourceBlock(block.Index)
	b.SetBlockEx(ret, llssa.AtEnd, false)
	if ninstr := len(block.Instrs); ninstr > 0 {
		if isPhi(block.Instrs[0]) {
			n := 1
			for n < ninstr && isPhi(block.Instrs[n]) {
				n++
			}
			rets := make([]llssa.Expr, n) // TODO(xsw): check to remove this
			for i := 0; i < n; i++ {
				iv := block.Instrs[i].(*ssa.Phi)
				if _, skip := p.unevaluatedSSA[iv]; skip {
					continue
				}
				rets[i] = p.compilePhi(b, iv)
			}
			for i := 0; i < n; i++ {
				iv := block.Instrs[i].(*ssa.Phi)
				if _, skip := p.unevaluatedSSA[iv]; skip {
					continue
				}
				p.bvals[iv] = rets[i]
			}
			return n
		}
	}
	return 0
}

func (p *context) compilePhi(b llssa.Builder, v *ssa.Phi) (ret llssa.Expr) {
	phi := b.Phi(p.type_(v.Type(), llssa.InGo))
	ret = phi.Expr
	p.phis = append(p.phis, func() {
		finishSite := p.beginCoroSemanticInstructionEmission(v)
		defer finishSite()
		preds := v.Block().Preds
		bblks := make([]llssa.BasicBlock, len(preds))
		for i, pred := range preds {
			bblks[i] = p.sourceBlock(pred.Index)
		}
		edges := v.Edges
		phi.AddIncoming(b, bblks, func(i int, blk llssa.BasicBlock) llssa.Expr {
			b.SetBlockEx(blk, llssa.BeforeLast, false)
			return p.compileValue(b, edges[i])
		})
	})
	return
}

// beginCoroSemanticInstructionEmission is the single source-instruction
// boundary shared by ordinary instruction emission and Phi incoming-edge
// materialization. Phi nodes are declared before their operands can be
// compiled, so their frozen SitePlan must remain active around the deferred
// incoming-edge lowering rather than the earlier empty declaration.
func (p *context) beginCoroSemanticInstructionEmission(instr ssa.Instruction) func() {
	finishSite := p.beginCoroSiteEmission(instr)
	p.observeCoroSemanticInstruction(instr)
	return finishSite
}

func (p *context) compileInstrOrValue(b llssa.Builder, iv instrOrValue, asValue bool) (ret llssa.Expr) {
	if asValue {
		if v, ok := p.bvals[iv]; ok {
			return v
		}
		log.Panicln("unreachable:", iv)
	}
	var (
		frozenPhysical        coroPhysicalInstructionPlan
		frozenPhysicalPlanned bool
		frozenPhysicalLoaded  bool
	)
	physicalPlan := func() (coroPhysicalInstructionPlan, bool) {
		if !frozenPhysicalLoaded {
			frozenPhysical, frozenPhysicalPlanned = p.plannedCoroPhysicalInstruction(iv)
			frozenPhysicalLoaded = true
		}
		return frozenPhysical, frozenPhysicalPlanned
	}
	observePhysical := func(actual coroPhysicalInstructionRecipe) {
		plan, planned := physicalPlan()
		if !planned {
			panic(fmt.Sprintf("physical recipe %s escaped ordinary SSA emission", actual))
		}
		p.observeCoroPhysicalInstruction(iv, actual)
		if plan.recipe != actual {
			panic("physical recipe observation returned after a mismatched frozen plan")
		}
	}
	switch v := iv.(type) {
	case *ssa.Call:
		p.recordCallerLocationForCall(b, &v.Call)
		if value, handled := p.tryCompileCoroPatchInitRedirect(b, v); handled {
			ret = value
		} else if value, handled := p.tryCompileCoroRawPlainCall(b, v); handled {
			ret = value
		} else if p.rawPlainBody {
			if value, handled := p.tryCompileCoroRawPlainExactInterfaceCall(b, v); handled {
				ret = value
				break
			}
			// A compiler-frozen closed SyncDispatch (currently the TLS destructor
			// callback) has a complete singleton target and plain descriptor ABI.
			// Preserve that exact path before the general raw-body dynamic-call
			// rejection; open/invoke/method dispatch remains fail-closed.
			callPlan, planned := p.compilation.CoroPlan.CallPlan(v)
			handled := false
			if planned {
				switch {
				case callPlan.Transport == coro.RawCCodePointer:
					common := v.Common()
					if common == nil || common.StaticCallee() != nil || common.IsInvoke() || common.Method != nil ||
						callPlan.Kind != coro.CallForeign || callPlan.Rep != coro.DirectPlain || !callPlan.Open ||
						callPlan.Unresolved != coro.UnknownForeign || callPlan.SyncDispatch {
						panic(fmt.Errorf("raw plain body %q has malformed raw C code-pointer call %q", p.goFn.Name(), v.String()))
					}
					ret = p.callInstruction(b, llssa.Call, v)
					handled = true
				case callPlan.Rep == coro.Dispatch && !callPlan.SyncDispatch:
					panic(fmt.Errorf("raw plain body %q contains non-synchronous descriptor call %q", p.goFn.Name(), v.String()))
				case callPlan.SyncDispatch:
					value, dispatched := p.tryCompileCoroPlainDispatchCall(b, v)
					if !dispatched {
						panic(fmt.Errorf("raw plain body %q lost its planned synchronous descriptor call %q", p.goFn.Name(), v.String()))
					}
					ret = value
					handled = true
				}
			}
			if !handled {
				common := v.Common()
				if common == nil {
					panic("raw plain body contains a call without CallCommon")
				}
				if _, builtin := common.Value.(*ssa.Builtin); !builtin &&
					(common.StaticCallee() == nil || common.IsInvoke() || common.Method != nil) {
					panic(fmt.Errorf("raw plain body %q contains an unplanned dynamic call %q", p.goFn.Name(), v.String()))
				}
				ret = p.callInstruction(b, llssa.Call, v)
			}
		} else if p.hasStructuredOutcomePhysicalBody() {
			if coroDeferStackBuiltinCall(v) {
				ret = p.compileCoroDeferStack(b, v)
			} else if value, handled := p.tryCompileCoroPhysicalCall(b, v); handled {
				ret = value
			} else {
				ret = p.callInstruction(b, llssa.Call, v)
			}
		} else if value, handled := p.tryCompileCoroManagedInterfaceDispatch(b, v); handled {
			ret = value
		} else if value, handled := p.tryCompileCoroPlainDispatchCall(b, v); handled {
			ret = value
		} else {
			ret = p.callInstruction(b, llssa.Call, v)
		}
		if !p.hasCoroPhysicalBody() && p.rangeFuncCallNeedsDeferDrain(&v.Call) {
			b.DeferStackDrain()
		}
	case *ssa.BinOp:
		physicalInstruction, physicalPlanned := physicalPlan()
		if physicalPlanned && physicalInstruction.recipe == coroPhysicalInstructionInterfaceNilCompare {
			if physicalInstruction.valueOperand == nil {
				panic("interface nil comparison lost its frozen value operand")
			}
			observePhysical(coroPhysicalInstructionInterfaceNilCompare)
			physical := p.compileValue(b, physicalInstruction.valueOperand)
			typeWord := b.InterfaceTypeWord(physical)
			nilType := p.prog.Nil(p.prog.VoidPtr())
			ret = b.BinOp(v.Op, typeWord, nilType)
			break
		}
		if physicalPlanned && physicalInstruction.recipe != coroPhysicalInstructionOrdinary {
			panic(fmt.Sprintf("BinOp selected incompatible frozen physical recipe %s", physicalInstruction.recipe))
		}
		if isUntypedNilConst(v.X) && isUntypedNilConst(v.Y) {
			switch v.Op {
			case token.EQL:
				ret = p.prog.BoolVal(true)
				break
			case token.NEQ:
				ret = p.prog.BoolVal(false)
				break
			}
			if !ret.IsNil() {
				break
			}
		}
		x := p.compileValueAs(b, v.X, v.Y.Type())
		y := p.compileValueAs(b, v.Y, v.X.Type())
		if (v.Op == token.QUO || v.Op == token.REM) && ssaIntegerValueProvenNonZeroAt(v.Y, v) {
			ret = b.BinOpWithNonZeroDivisor(v.Op, x, y)
		} else {
			ret = b.BinOp(v.Op, x, y)
		}
	case *ssa.UnOp:
		if v.Op != token.ARROW {
			p.recordPanicLocation(b, v.Pos())
		}
		physicalInstruction, physicalPlanned := physicalPlan()
		if v.Op == token.MUL {
			if physicalPlanned &&
				physicalInstruction.recipe == coroPhysicalInstructionStaticArrayRangeDerefElided {
				// ProgramIR has already proved that this is the type-only
				// single-index range form. Preserve the legacy operand
				// materialization contract (its pure SSA producers may already
				// have been emitted), but form neither a load nor a nil fault.
				observePhysical(coroPhysicalInstructionStaticArrayRangeDerefElided)
				p.compileValue(b, v.X)
				return
			}
			plannedDeref := physicalPlanned && physicalInstruction.recipe == coroPhysicalInstructionDeref
			if _, ok := p.methodNilDerefChecks[v]; ok && !ssaValueProvenNonNilAt(v.X, v) {
				if physicalPlanned && p.coroUsesExplicitStatusFaults() {
					switch physicalInstruction.recipe {
					case coroPhysicalInstructionDeref:
						// The physical dereference recipe below either preserves the
						// same base pointer through an explicit-status guard or consumes
						// an address already checked by its producer. AssertNilDerefPtr
						// must not escape into this stackless coroutine body.
					case coroPhysicalInstructionOrdinary:
						// A non-elided SitePlan retains the managed checked-pointer helper;
						// its frozen lowered-call fact is observed by compileCheckedDeref.
						return p.compileCheckedDeref(b, v)
					default:
						panic(fmt.Sprintf("value-receiver dereference selected incompatible frozen physical recipe %s", physicalInstruction.recipe))
					}
				} else {
					return p.compileCheckedDeref(b, v)
				}
			}
			if isEffectfulArrayPointerDeref(v) && !plannedDeref {
				x := p.compileValue(b, v.X)
				b.AssertNilDeref(x)
			}
			if refs, ok := nonDebugReferrers(v); plannedDeref && ok && len(refs) == 0 {
				// An unused load still evaluates its pointer operand and must
				// preserve the source nil fault, but it has no value-side
				// memory effect. Let the frozen physical recipe either publish
				// that fault or consume its checked address exactly once.
				x := p.compileValue(b, v.X)
				p.compileCoroPlannedDeref(b, v, x, physicalInstruction)
				return
			}
			if refs, ok := nonDebugReferrers(v); ok && len(refs) == 0 {
				if t := p.type_(v.Type(), llssa.InGo); t.RawType() != nil {
					if p.isLargeNonPointerValue(t) {
						x := p.compileValue(b, v.X)
						p.assertNilDerefBase(b, v.X)
						b.AssertNilDeref(x)
						return
					}
				}
				if skipUnusedArrayDeref(v) {
					p.compileValue(b, v.X)
					return
				}
				if _, ok := types.Unalias(v.Type()).Underlying().(*types.Slice); ok {
					// Zero-length slice-to-array conversions can leave only
					// an unused slice deref; preserve its required nil check.
					x := p.compileValue(b, v.X)
					p.assertNilDerefBase(b, v.X)
					b.AssertNilDeref(x)
					return
				}
			}
			if _, fusion := coroInterfaceDerefConsumer(p, v); fusion == coroInterfaceDerefLarge {
				// Skip the large load: the MakeInterface handler below copies
				// from the original pointer and owns the nil check. A
				// zero-sized value still retains this dereference's nil edge.
				return
			}
			// "libc_XXX_trampoline_addr" -> "XXX"
			if strings.HasSuffix(v.X.Name(), "_trampoline_addr") {
				name := v.X.Name()
				if cname := strings.TrimPrefix(name[:len(name)-16], "libc_"); cname != "" {
					cname = p.remapTrampolineCName(cname)
					fnSig := p.syscallFnSig(0)
					cfn := b.Pkg.NewFunc(cname, fnSig, llssa.InC)
					ret = b.Convert(p.type_(types.Typ[types.Uintptr], llssa.InGo), cfn.Expr)
					p.bvals[iv] = ret
					return ret
				}
			}
		}
		x := p.compileValue(b, v.X)
		plannedDeref := physicalPlanned && physicalInstruction.recipe == coroPhysicalInstructionDeref
		if plannedDeref {
			x = p.compileCoroPlannedDeref(b, v, x, physicalInstruction)
		} else if physicalPlanned && physicalInstruction.recipe != coroPhysicalInstructionOrdinary {
			panic(fmt.Sprintf("typed load selected incompatible frozen physical recipe %s", physicalInstruction.recipe))
		} else if (!physicalPlanned || !p.coroUsesExplicitStatusFaults()) && shouldAssertDirectNilDeref(v) && !ssaValueProvenNonNilAt(v.X, v) {
			b.AssertNilDeref(x)
		}
		if v.Op == token.ARROW {
			operation, operationPlanned := p.plannedCoroPhysicalOperation(v)
			if operationPlanned && operation.operation == coroPhysicalOperationChannelReceive {
				p.observeCoroPhysicalOperation(v, coroPhysicalOperationChannelReceive)
				ret = p.compileCoroChanRecv(b, v, x)
			} else if !operationPlanned || operation.operation == coroPhysicalOperationNone {
				ret = b.Recv(x, v.CommaOk)
			} else {
				panic(fmt.Sprintf("channel receive selected incompatible frozen physical operation recipe %s", operation.operation))
			}
		} else {
			if v.Op == token.MUL {
				if t := p.type_(v.Type(), llssa.InGo); t.RawType() != nil && p.prog.SizeOf(t) == 0 {
					if plannedDeref || isKnownNonNilAddr(v.X) || ssaValueProvenNonNilAt(v.X, v) {
						// A physical explicit-status guard owns the nullable
						// case, or the address producer already checked it; a
						// separately proven non-nil source needs no memory access
						// in either physical or plain emission.
						// Avoid Builder.UnOp's legacy nil helper and materialize
						// the sole zero-sized value directly.
						ret = p.prog.Zero(t)
						break
					}
					p.assertNilDerefBase(b, v.X)
				}
				if isInterfaceCompareDeref(v) && !plannedDeref {
					p.assertNilDerefBase(b, v.X)
					b.AssertNilDeref(x)
				}
			}
			if plannedDeref || p.loadAddressOwnsNilFault(v.X) {
				// The frozen dereference recipe has already emitted the sole
				// source-language nil edge or recorded its checked producer.
				// Likewise, checked FieldAddr/IndexAddr producers own their
				// nil/bounds rules. Builder.UnOp delegates to Load, whose
				// static-null fallback would otherwise synthesize a second
				// legacy AssertNilDeref call for constant-folded pointers.
				ret = b.LoadKnownNonNil(x)
			} else {
				ret = b.UnOp(v.Op, x)
			}
		}
	case *ssa.ChangeType:
		t := v.Type()
		if isUntypedNilConst(v.X) {
			ret = p.nilOf(t)
			break
		}
		if value, handled := p.tryCompileCoroRawCChangeType(b, v); handled {
			ret = value
			break
		}
		x := p.compileValue(b, v.X)
		ret = b.ChangeType(p.type_(t, llssa.InGo), x)
	case *ssa.Convert:
		t := v.Type()
		if isUntypedNilConst(v.X) {
			ret = p.nilOf(t)
			break
		}
		x := p.compileValue(b, v.X)
		ret = b.Convert(p.type_(t, llssa.InGo), x)
	case *ssa.FieldAddr:
		x := p.compileValue(b, v.X)
		p.recordPanicLocation(b, v.Pos())
		physicalInstruction, physicalPlanned := physicalPlan()
		guardedFieldAddr := physicalPlanned && physicalInstruction.recipe == coroPhysicalInstructionFieldAddr
		if guardedFieldAddr {
			observePhysical(coroPhysicalInstructionFieldAddr)
			x = p.compileCoroImplicitNilFieldAddrGuard(b, v, x)
		} else if physicalPlanned && physicalInstruction.recipe != coroPhysicalInstructionOrdinary {
			panic(fmt.Sprintf("FieldAddr selected incompatible frozen physical recipe %s", physicalInstruction.recipe))
		} else if (!physicalPlanned || !p.coroUsesExplicitStatusFaults()) && p.isAddressOfFieldAddr(v) && !ssaAddressValueProvenNonNilAt(v.X, v) {
			b.AssertNilDeref(x)
		}
		if guardedFieldAddr {
			ret = b.FieldAddrKnownNonNil(x, v.Field)
		} else {
			ret = b.FieldAddr(x, v.Field)
		}
	case *ssa.Alloc:
		t := v.Type().(*types.Pointer)
		if p.checkVArgs(v, t) { // varargs: this maybe a varargs allocation
			return
		}
		if p.skipSyntheticMakeSliceAlloc(v) {
			return
		}
		elem := p.type_(t.Elem(), llssa.InGo)
		heap := v.Heap
		physicalInstruction, physicalPlanned := physicalPlan()
		if physicalPlanned {
			switch physicalInstruction.recipe {
			case coroPhysicalInstructionTerminalResultAllocation:
				observePhysical(coroPhysicalInstructionTerminalResultAllocation)
				ret = p.compileCoroTerminalResultAllocation(v)
			case coroPhysicalInstructionFrameBitcastAllocation:
				observePhysical(coroPhysicalInstructionFrameBitcastAllocation)
				ret = p.coroFrameAlloca(elem)
			case coroPhysicalInstructionFrameAllocation:
				observePhysical(coroPhysicalInstructionFrameAllocation)
				ret = p.coroFrameAlloc(elem)
			case coroPhysicalInstructionOrdinary:
			default:
				panic(fmt.Sprintf("Alloc selected incompatible frozen physical recipe %s", physicalInstruction.recipe))
			}
			if !ret.IsNil() {
				p.debugAlloc(b, v, ret)
				break
			}
		}
		exactBitcast := false
		if !physicalPlanned {
			bitcast, exact := coro.ProveSSAExactScalarBitcast(v.Parent())
			exactBitcast = exact && bitcast.Allocation == v
		}
		if exactBitcast {
			// The exact body stores the complete same-width scalar before its
			// single reinterpreted load, so zero initialization is both unnecessary
			// and would leave a misleading llvm.memset call in this call-free leaf.
			ret = b.AllocaT(elem)
			p.debugAlloc(b, v, ret)
			break
		}
		ret = b.Alloc(elem, heap)
		p.debugAlloc(b, v, ret)
	case *ssa.IndexAddr:
		p.recordPanicLocation(b, v.Pos())
		vx := v.X
		if _, ok := p.isVArgs(vx); ok { // varargs: this is a varargs index
			return
		}
		x := p.compileValue(b, vx)
		idx := p.compileValue(b, v.Index)
		physicalInstruction, physicalPlanned := physicalPlan()
		if physicalPlanned && physicalInstruction.recipe == coroPhysicalInstructionIndexAddr {
			observePhysical(coroPhysicalInstructionIndexAddr)
			ret = p.compileCoroIndexAddrPlanned(b, v, x, idx, physicalInstruction)
		} else if physicalPlanned && physicalInstruction.recipe != coroPhysicalInstructionOrdinary {
			panic(fmt.Sprintf("IndexAddr selected incompatible frozen physical recipe %s", physicalInstruction.recipe))
		} else if b.Prog.BoundsChecksDisabled() {
			if _, pointer := types.Unalias(p.patchType(v.X.Type())).Underlying().(*types.Pointer); pointer &&
				emissionArrayPointerNeedsNilCheck(v.X, v) {
				b.AssertNilDeref(x)
			}
			ret = b.IndexAddrUnchecked(x, idx)
		} else if p.frozenSafeFixedArrayIndex(v, v.X, v.Index) {
			if _, pointer := types.Unalias(p.patchType(v.X.Type())).Underlying().(*types.Pointer); pointer &&
				!emissionKnownNonNilArrayBase(v.X) && !ssaValueProvenNonNilAt(v.X, v) {
				// Bounds safety says nothing about the implicit *array
				// dereference. Keep its ordinary nil fault, routing it through
				// the explicit outcome only in a physical coroutine body.
				b.AssertNilDeref(x)
			}
			ret = b.IndexAddrUnchecked(x, idx)
		} else {
			ret = b.IndexAddr(x, idx)
		}
	case *ssa.Index:
		x := p.compileValue(b, v.X)
		idx := p.compileValue(b, v.Index)
		p.recordPanicLocation(b, v.Pos())
		physicalInstruction, physicalPlanned := physicalPlan()
		takeArrayAddr := func() (addr llssa.Expr, zero bool) {
			if physicalPlanned && physicalInstruction.reuseValueAddress {
				var found bool
				addr, found = p.coroValueAddrs[v.X]
				if !found || addr.IsNil() {
					panic("coroutine Index lost its frozen awaited-value address")
				}
				return
			}
			switch n := v.X.(type) {
			case *ssa.Const:
				zero = true
			case *ssa.UnOp:
				addr = p.compileValue(b, n.X)
			}
			return
		}
		if physicalPlanned && physicalInstruction.recipe == coroPhysicalInstructionIndex {
			observePhysical(coroPhysicalInstructionIndex)
			ret = p.compileCoroIndexPlanned(b, v, x, idx, takeArrayAddr, physicalInstruction)
		} else if physicalPlanned && physicalInstruction.recipe != coroPhysicalInstructionOrdinary {
			panic(fmt.Sprintf("Index selected incompatible frozen physical recipe %s", physicalInstruction.recipe))
		} else if b.Prog.BoundsChecksDisabled() {
			switch types.Unalias(p.patchType(v.X.Type())).Underlying().(type) {
			case *types.Basic, *types.Array:
				ret = b.IndexUnchecked(x, idx, takeArrayAddr)
			case *types.Slice:
				ret = b.Load(b.IndexAddrUnchecked(x, idx))
			case *types.Pointer:
				if emissionArrayPointerNeedsNilCheck(v.X, v) {
					b.AssertNilDeref(x)
				}
				ret = b.Load(b.IndexAddrUnchecked(x, idx))
			default:
				panic("bounds-disabled Index lost its frozen container shape")
			}
		} else if p.frozenSafeFixedArrayIndex(v, v.X, v.Index) {
			switch types.Unalias(p.patchType(v.X.Type())).Underlying().(type) {
			case *types.Array:
				ret = b.IndexUnchecked(x, idx, takeArrayAddr)
			case *types.Pointer:
				if !emissionKnownNonNilArrayBase(v.X) && !ssaValueProvenNonNilAt(v.X, v) {
					b.AssertNilDeref(x)
				}
				ret = b.Load(b.IndexAddrUnchecked(x, idx))
			default:
				panic("safe fixed-array Index lost its frozen container shape")
			}
		} else {
			ret = b.Index(x, idx, takeArrayAddr)
		}
	case *ssa.Lookup:
		x := p.compileValue(b, v.X)
		idx := p.compileValue(b, v.Index)
		ret = b.Lookup(x, idx, v.CommaOk)
	case *ssa.Slice:
		p.recordPanicLocation(b, v.Pos())
		if makeSlice, ok := p.compileSyntheticMakeSlice(b, v); ok {
			ret = makeSlice
			break
		}
		vx := v.X
		if _, ok := p.isVArgs(vx); ok { // varargs: this is a varargs slice
			return
		}
		var low, high, max llssa.Expr
		x := p.compileValue(b, vx)
		if v.Low != nil {
			low = p.compileValue(b, v.Low)
		}
		if v.High != nil {
			high = p.compileValue(b, v.High)
		}
		if v.Max != nil {
			max = p.compileValue(b, v.Max)
		}
		physicalInstruction, physicalPlanned := physicalPlan()
		if physicalPlanned && physicalInstruction.recipe == coroPhysicalInstructionSlice {
			observePhysical(coroPhysicalInstructionSlice)
			ret = p.compileCoroSlicePlanned(b, v, x, low, high, max, physicalInstruction)
		} else if physicalPlanned && physicalInstruction.recipe != coroPhysicalInstructionOrdinary {
			panic(fmt.Sprintf("Slice selected incompatible frozen physical recipe %s", physicalInstruction.recipe))
		} else if unchecked, ok := p.compileBoundsDisabledSlice(b, v, x, low, high, max); ok {
			ret = unchecked
		} else {
			ret = b.Slice(x, low, high, max)
		}
		ret.Type = p.type_(v.Type(), llssa.InGo)
	case *ssa.MakeInterface:
		physicalInstruction, physicalPlanned := physicalPlan()
		checkedInterfacePtr := physicalPlanned &&
			physicalInstruction.recipe == coroPhysicalInstructionInterfaceFromCheckedPtr
		if physicalPlanned && physicalInstruction.recipe == coroPhysicalInstructionSyntheticSelectNoCaseBox {
			observePhysical(coroPhysicalInstructionSyntheticSelectNoCaseBox)
			ret = p.prog.Nil(p.type_(v.Type(), llssa.InGo))
			break
		}
		if physicalPlanned && physicalInstruction.recipe != coroPhysicalInstructionOrdinary && !checkedInterfacePtr {
			panic(fmt.Sprintf("MakeInterface selected incompatible frozen physical recipe %s", physicalInstruction.recipe))
		}
		if refs, _ := nonDebugReferrers(v); len(refs) == 1 {
			switch ref := refs[0].(type) {
			case *ssa.Store:
				if va, ok := ref.Addr.(*ssa.IndexAddr); ok {
					if _, ok = p.isVArgs(va.X); ok { // varargs: this is a varargs store
						return
					}
				}
			case *ssa.Call:
				if fn, ok := ref.Call.Value.(*ssa.Function); ok {
					if _, _, ftype := p.funcOf(fn); ftype == llgoFuncAddr || ftype == llgoFuncPCABI0 { // llgo.funcAddr/funcPCABI0
						return
					}
				}
			}
		}
		t := p.type_(v.Type(), llssa.InGo)
		if isUntypedNilConst(v.X) {
			ret = p.prog.Nil(t)
			break
		}
		if unop, ok := v.X.(*ssa.UnOp); ok && unop.Op == token.MUL {
			if vt := p.type_(unop.Type(), llssa.InGo); vt.RawType() != nil {
				if p.isLargeNonPointerValue(vt) || p.isZeroSizedValue(vt) {
					if ptr := p.compileValue(b, unop.X); ptr.Type != nil {
						producerOwnsFault := p.loadAddressOwnsNilFault(unop.X)
						derefOwnsFault := p.coroPhysicalProducerHasRecipe(
							unop, coroPhysicalInstructionDeref,
						)
						checkedPointer := producerOwnsFault || derefOwnsFault
						if !checkedPointer {
							p.assertNilDerefBase(b, unop.X)
						}
						knownNonNil := checkedPointer || isKnownNonNilAddr(unop.X) ||
							ssaValueProvenNonNilAt(unop.X, v)
						if checkedInterfacePtr {
							if !checkedPointer {
								panic("interface-from-checked-ptr recipe lost its checked pointer producer")
							}
							observePhysical(coroPhysicalInstructionInterfaceFromCheckedPtr)
						}
						if knownNonNil {
							ret = b.MakeInterfaceFromKnownNonNilPtr(t, ptr)
						} else {
							ret = b.MakeInterfaceFromPtr(t, ptr)
						}
						break
					}
				}
			}
		}
		x := p.compileValue(b, v.X)
		ret = b.MakeInterface(t, x)
	case *ssa.MakeSlice:
		t := p.type_(v.Type(), llssa.InGo)
		nLen := p.compileValue(b, v.Len)
		nCap := p.compileValue(b, v.Cap)
		ret = b.MakeSlice(t, nLen, nCap)
	case *ssa.MakeMap:
		var nReserve llssa.Expr
		t := p.type_(v.Type(), llssa.InGo)
		if v.Reserve != nil {
			nReserve = p.compileValue(b, v.Reserve)
		}
		ret = b.MakeMap(t, nReserve)
	case *ssa.MakeClosure:
		if value, handled := p.tryCompileCoroPlainDispatchClosure(b, v); handled {
			ret = value
			break
		}
		var fn llssa.Expr
		if target, ok := v.Fn.(*ssa.Function); ok && p.compilation != nil {
			// The target's own ValuePlan may require a descriptor at another
			// producer. MakeClosure still needs the raw body entry; feeding a
			// descriptor-backed closure to Builder.MakeClosure would reinterpret
			// the descriptor pointer as executable code.
			fn = p.compileRawFunctionValue(target)
			if !p.rawPlainBody && len(target.FreeVars) != 0 && p.compilation.CoroPlan != nil {
				targetPlan, planned := p.compilation.CoroPlan.FunctionPlan(target)
				if planned && targetPlan.Emission == coro.EmitCoroutine {
					if p.emissionUniverse == nil {
						panic("captured coroutine closure requires a prepared emission universe")
					}
					entrySig, err := p.emissionUniverse.coroPhysicalEntrySourceSignature(target)
					if err != nil {
						panic(fmt.Errorf("captured coroutine closure %q: %w", targetPlan.ID, err))
					}
					// MakeClosure owns only the canonical {code,env} allocation. Retag
					// the managed (g,out,ctx,args) entry as an opaque (ctx,args)
					// carrier; no call is emitted through this temporary code word.
					carrierSig := p.prog.PhysicalFuncDecl(entrySig, llssa.InGo)
					fn = b.ChangeType(p.prog.Type(carrierSig, llssa.InC), fn)
				}
			}
		} else {
			fn = p.compileValue(b, v.Fn)
		}
		bindings := p.compileValues(b, v.Bindings, 0)
		ret = b.MakeClosure(fn, bindings)
	case *ssa.TypeAssert:
		x := p.compileValue(b, v.X)
		t := p.type_(v.AssertedType, llssa.InGo)
		p.recordPanicLocation(b, v.Pos())
		ret = b.TypeAssert(x, t, v.CommaOk)
	case *ssa.Extract:
		x := p.compileValue(b, v.Tuple)
		ret = b.Extract(x, v.Index)
	case *ssa.Range:
		x := p.compileValue(b, v.X)
		ret = b.Range(x)
	case *ssa.Next:
		var typ llssa.Type
		if !v.IsString {
			typ = p.type_(v.Iter.(*ssa.Range).X.Type(), llssa.InGo)
		}
		iter := p.compileValue(b, v.Iter)
		ret = p.compileRangeNext(b, v, typ, iter)
	case *ssa.ChangeInterface:
		t := v.Type()
		x := p.compileValue(b, v.X)
		ret = b.ChangeInterface(p.type_(t, llssa.InGo), x)
	case *ssa.Field:
		x := p.compileValue(b, v.X)
		ret = b.Field(x, v.Field)
	case *ssa.MakeChan:
		t := v.Type()
		size := p.compileValue(b, v.Size)
		ret = b.MakeChan(p.type_(t, llssa.InGo), size)
	case *ssa.Select:
		states := make([]*llssa.SelectState, len(v.States))
		for i, s := range v.States {
			states[i] = &llssa.SelectState{
				Chan: p.compileValue(b, s.Chan),
				Send: s.Dir == types.SendOnly,
			}
			if s.Send != nil {
				states[i].Value = p.compileValue(b, s.Send)
			}
		}
		operation, operationPlanned := p.plannedCoroPhysicalOperation(v)
		if !operationPlanned || operation.operation == coroPhysicalOperationNone {
			ret = b.Select(states, v.Blocking)
			break
		}
		switch operation.operation {
		case coroPhysicalOperationChannelSelectPark:
			p.observeCoroPhysicalOperation(v, coroPhysicalOperationChannelSelectPark)
			ret = p.compileCoroChanSelect(b, states)
		case coroPhysicalOperationChannelSelectTry:
			p.observeCoroPhysicalOperation(v, coroPhysicalOperationChannelSelectTry)
			ret = p.compileCoroChanTrySelect(b, states)
		default:
			panic(fmt.Sprintf("channel select selected incompatible frozen physical operation recipe %s", operation.operation))
		}
	case *ssa.SliceToArrayPointer:
		p.recordPanicLocation(b, v.Pos())
		t := p.type_(v.Type(), llssa.InGo)
		x := p.compileValue(b, v.X)
		physicalInstruction, physicalPlanned := physicalPlan()
		if physicalPlanned && physicalInstruction.recipe == coroPhysicalInstructionSliceToArrayPointer {
			observePhysical(coroPhysicalInstructionSliceToArrayPointer)
			if physicalInstruction.bound == 0 {
				ret = b.SliceToArrayPointerUnchecked(x, t)
				break
			}
			ret = p.compileCoroSliceToArrayPointer(b, v, x, t, physicalInstruction)
			break
		}
		if physicalPlanned && physicalInstruction.recipe != coroPhysicalInstructionOrdinary {
			panic(fmt.Sprintf("SliceToArrayPointer selected incompatible frozen physical recipe %s", physicalInstruction.recipe))
		}
		length, exact := coroSliceToArrayPointerLen(v, p.patchType)
		if exact && length == 0 {
			// Go deliberately preserves the slice data word here: a nil slice
			// converts to nil *[0]T, while an empty non-nil slice converts to a
			// non-nil pointer. There is no length fault for N==0.
			ret = b.SliceToArrayPointerUnchecked(x, t)
			break
		}
		ret = b.SliceToArrayPointer(x, t)
	default:
		panic(fmt.Sprintf("compileInstrAndValue: unknown instr - %T\n", iv))
	}
	p.bvals[iv] = ret
	return ret
}

// isEffectfulArrayPointerDeref reports whether v is an array dereference that
// must be evaluated even though range, len, or cap only needs the static array
// length. The language specification requires evaluation when the operand
// contains a function call or channel receive. See Go issue 72844.
func isEffectfulArrayPointerDeref(v *ssa.UnOp) bool {
	if v == nil || v.Op != token.MUL {
		return false
	}
	if _, ok := types.Unalias(v.Type()).Underlying().(*types.Array); !ok {
		return false
	}
	if !arrayPointerOperandHasEffectAfter(v.X, v.Pos(), nil) {
		return false
	}
	refs, ok := nonDebugReferrers(v)
	if !ok || len(refs) == 0 {
		return ok
	}
	if len(refs) != 1 {
		return false
	}
	call, ok := refs[0].(*ssa.Call)
	if !ok {
		return false
	}
	builtin, ok := call.Common().Value.(*ssa.Builtin)
	return ok && (builtin.Name() == "len" || builtin.Name() == "cap")
}

func arrayPointerOperandHasEffectAfter(v ssa.Value, after token.Pos, seen map[ssa.Value]bool) bool {
	if v == nil || seen[v] {
		return false
	}
	if seen == nil {
		seen = make(map[ssa.Value]bool)
	}
	seen[v] = true

	instr, ok := v.(ssa.Instruction)
	if !ok {
		return false
	}
	if pos := instr.Pos(); after.IsValid() && pos.IsValid() && pos <= after {
		// SSA eliminates local assignments. Do not mistake a call that produced
		// the assigned value for a call contained in the len, cap, or range
		// expression itself.
		return false
	}
	switch v := v.(type) {
	case *ssa.Call:
		return true
	case *ssa.UnOp:
		if v.Op == token.ARROW {
			return true
		}
	}
	for _, operand := range instr.Operands(nil) {
		if operand != nil && arrayPointerOperandHasEffectAfter(*operand, after, seen) {
			return true
		}
	}
	return false
}

func isInterfaceCompareDeref(v *ssa.UnOp) bool {
	if _, ok := types.Unalias(v.Type()).Underlying().(*types.Interface); !ok {
		return false
	}
	switch v.X.(type) {
	case *ssa.Alloc, *ssa.Extract, *ssa.FieldAddr, *ssa.FreeVar, *ssa.Global, *ssa.IndexAddr:
		return false
	}
	refs, ok := nonDebugReferrers(v)
	if !ok || len(refs) != 1 {
		return false
	}
	bin, ok := refs[0].(*ssa.BinOp)
	return ok && (bin.Op == token.EQL || bin.Op == token.NEQ)
}

func isUntypedNilConst(v ssa.Value) bool {
	c, ok := v.(*ssa.Const)
	if !ok || c.Value != nil {
		return false
	}
	basic, ok := c.Type().Underlying().(*types.Basic)
	return ok && basic.Kind() == types.UntypedNil
}

func (p *context) nilOf(typ types.Type) llssa.Expr {
	return p.prog.Nil(p.type_(typ, llssa.InGo))
}

func (p *context) compileValueAs(b llssa.Builder, v ssa.Value, typ types.Type) llssa.Expr {
	if isUntypedNilConst(v) {
		return p.nilOf(typ)
	}
	return p.compileValue(b, v)
}

// compileRangeNext makes x/tools' implicit range-assignment conversions
// explicit at the Next site. Builder.Next first returns the source key/value
// types; Next.Type may instead contain existing assignment-target types (for
// example any or a non-empty interface). Keeping the conversion here gives
// every Extract the exact SSA type and attributes any interface helper to the
// same frozen SitePlan.
func (p *context) compileRangeNext(
	b llssa.Builder,
	next *ssa.Next,
	sourceType llssa.Type,
	iter llssa.Expr,
) llssa.Expr {
	if next == nil {
		panic("range Next lowering requires one exact SSA instruction")
	}
	source := b.Next(sourceType, iter, next.IsString)
	result, ok := types.Unalias(p.patchType(next.Type())).Underlying().(*types.Tuple)
	if !ok || result.Len() != 3 {
		panic("range Next lowering lost its (bool, key, value) tuple")
	}
	fields := make([]llssa.Expr, result.Len())
	fieldTypes := make([]llssa.Type, result.Len())
	for index := 0; index < result.Len(); index++ {
		field := b.Extract(source, index)
		targetGo := result.At(index).Type()
		if coroPhysicalInvalidType(targetGo) {
			fields[index] = field
			fieldTypes[index] = field.Type
			continue
		}
		target := p.type_(targetGo, llssa.InGo)
		if !types.Identical(field.RawType(), target.RawType()) &&
			!types.AssignableTo(field.RawType(), target.RawType()) {
			panic(fmt.Sprintf(
				"range Next field %d source type %s is not assignable to %s",
				index, field.RawType(), target.RawType(),
			))
		}
		fields[index] = b.Assign(target, field)
		fieldTypes[index] = target
	}
	return b.Aggregate(p.prog.Struct(fieldTypes...), fields...)
}

func (p *context) assertNilDerefBase(b llssa.Builder, addr ssa.Value) {
	switch addr := addr.(type) {
	case *ssa.UnOp:
		if addr.Op != token.MUL || isKnownNonNilAddr(addr.X) || isWrapNilCheckCall(addr.X) {
			return
		}
		p.compileCheckedDeref(b, addr)
	case *ssa.FieldAddr:
		if isKnownNonNilAddr(addr.X) || isWrapNilCheckCall(addr.X) {
			return
		}
		p.assertNilDerefBase(b, addr.X)
		base := p.compileValue(b, addr.X)
		if isPointerGoType(addr.X.Type()) {
			base = b.NilDerefCheck(base)
		}
		p.bvals[addr] = b.FieldAddr(base, addr.Field)
	}
}

func (p *context) jumpTo(v *ssa.Jump) llssa.BasicBlock {
	succs := v.Block().Succs
	return p.sourceBlock(succs[0].Index)
}

func (p *context) getDebugLocScope(v *ssa.Function, pos token.Pos) *types.Scope {
	if v.Object() == nil {
		return nil
	}
	funcScope := v.Object().(*types.Func).Scope()
	if funcScope == nil {
		return nil
	}
	return funcScope.Innermost(pos)
}

func (p *context) compileInstr(b llssa.Builder, instr ssa.Instruction) {
	if _, ok := p.staticInitInstrs[instr]; ok {
		return
	}
	finishSite := p.beginCoroSemanticInstructionEmission(instr)
	defer finishSite()
	if p.frontendOptions().Debug && instr.Parent().Origin() == nil {
		if _, isDebugRef := instr.(*ssa.DebugRef); !isDebugRef {
			scope := p.getDebugLocScope(instr.Parent(), instr.Pos())
			if scope != nil {
				diScope := b.DIScope(p.fn, scope)
				pos := p.fset.Position(instr.Pos())
				b.DISetCurrentDebugLocation(diScope, pos)
			}
		}
	}
	if iv, ok := instr.(instrOrValue); ok {
		p.compileInstrOrValue(b, iv, false)
		return
	}
	switch v := instr.(type) {
	case *ssa.Store:
		if _, ok := p.staticInitStores[v]; ok {
			return
		}
		if p.compilation != nil {
			plan := p.compilation.CoroPlan
			if plan != nil && plan.ElidesConditionalManagedStore(v) {
				// Whole-program analysis proved this exact direct descriptor
				// publication has no live reader or other target consumer. Avoid
				// materializing a reference to the intentionally EmitNone target.
				return
			}
		}
		va := v.Addr
		if va, ok := va.(*ssa.IndexAddr); ok {
			if args, ok := p.isVArgs(va.X); ok { // varargs: this is a varargs store
				idx := intVal(va.Index)
				val := v.Val
				if vi, ok := val.(*ssa.MakeInterface); ok {
					val = vi.X
				}
				args[idx] = p.compileValue(b, val)
				return
			}
		}
		if isBlankFieldStore(va) {
			_ = p.compileValue(b, v.Val)
			return
		}
		if p.rewrites != nil {
			if g, ok := va.(*ssa.Global); ok {
				if _, ok := p.rewriteInitStore(v, g); ok {
					return
				}
			}
		}
		ptr := p.compileValue(b, va)
		val := p.compileValue(b, v.Val)
		physicalInstruction, physicalPlanned := p.plannedCoroPhysicalInstruction(v)
		if physicalPlanned && physicalInstruction.recipe == coroPhysicalInstructionStore {
			p.observeCoroPhysicalInstruction(v, coroPhysicalInstructionStore)
			if !physicalInstruction.nilGuard {
				panic("structured coroutine Store omitted its frozen nil guard")
			}
			ptr = p.compileCoroImplicitNilStoreGuard(b, v, ptr)
			b.StoreKnownNonNil(ptr, val)
			return
		}
		b.Store(ptr, val)
	case *ssa.Jump:
		jmpb := p.jumpTo(v)
		b.Jump(jmpb)
	case *ssa.Return:
		runDefers := p.returnNeedsImplicitRunDefers(v)
		if runDefers {
			p.recordPanicLocation(b, v.Pos())
			p.emitPCLineLabel(b, p.deferRunPos(v.Pos()))
			b.RunDefers()
		}
		var results []llssa.Expr
		if n := len(v.Results); n > 0 {
			results = make([]llssa.Expr, n)
			for i, r := range v.Results {
				// A deferred call may change a named result independently of
				// the SSA value in Return.Results. Reload the result's storage
				// in the RunDefers continuation instead of depending on the
				// particular SSA node used to form the return tuple.
				if runDefers {
					if slot := p.namedResultSlot(i); slot != nil {
						results[i] = b.Load(p.compileValue(b, slot))
						continue
					}
				}
				results[i] = p.compileValue(b, r)
			}
		}
		p.popCallerLocationFrame(b)
		p.leaveExportedLocalContext(b)
		outcome, outcomePlanned := p.plannedCoroPhysicalOutcome(v)
		if outcomePlanned {
			if outcome.outcome != coroPhysicalOutcomeReturn {
				panic(fmt.Sprintf("return selected incompatible frozen physical outcome recipe %s", outcome.outcome))
			}
			p.observeCoroPhysicalOutcome(v, coroPhysicalOutcomeReturn)
			p.compileCoroReturn(b, results)
			return
		}
		b.Return(results...)
	case *ssa.If:
		cond := p.compileValue(b, v.Cond)
		succs := v.Block().Succs
		thenIndex, elseIndex := 0, 1
		if v == p.patchOriginalInitIf {
			// The public patch initializer already claimed init$guard. Enter the
			// original source body through the opposite guard edge without
			// mutating the shared x/tools SSA CFG.
			thenIndex, elseIndex = 1, 0
		}
		thenb := p.sourceBlock(succs[thenIndex].Index)
		elseb := p.sourceBlock(succs[elseIndex].Index)
		b.If(cond, thenb, elseb)
	case *ssa.MapUpdate:
		m := p.compileValue(b, v.Map)
		key := p.compileValue(b, v.Key)
		val := p.compileValue(b, v.Value)
		p.recordPanicLocation(b, v.Pos())
		b.MapUpdate(m, key, val)
	case *ssa.Defer:
		p.recordCallerLocationForCall(b, &v.Call)
		outcome, outcomePlanned := p.plannedCoroPhysicalOutcome(v)
		if outcomePlanned {
			if outcome.outcome != coroPhysicalOutcomeDeferRegister {
				panic(fmt.Sprintf("defer selected incompatible frozen physical outcome recipe %s", outcome.outcome))
			}
			p.observeCoroPhysicalOutcome(v, coroPhysicalOutcomeDeferRegister)
			p.compileCoroDefer(b, v)
			return
		}
		if v.DeferStack != nil {
			p.callDeferStack(b, p.blkInfos[v.Block().Index].Kind, v, v.DeferStack, v.Parent())
			return
		}
		p.callInstruction(b, p.blkInfos[v.Block().Index].Kind, v)
	case *ssa.Go:
		p.recordCallerLocationForCall(b, &v.Call)
		if p.tryCompileCoroClosedStaticSpawn(b, v) {
			return
		}
		p.callInstruction(b, llssa.Go, v)
	case *ssa.RunDefers:
		p.recordPanicLocation(b, v.Pos())
		p.emitPCLineLabel(b, p.deferRunPos(v.Pos()))
		outcome, outcomePlanned := p.plannedCoroPhysicalOutcome(v)
		if outcomePlanned {
			if outcome.outcome != coroPhysicalOutcomeRunDefers {
				panic(fmt.Sprintf("RunDefers selected incompatible frozen physical outcome recipe %s", outcome.outcome))
			}
			p.observeCoroPhysicalOutcome(v, coroPhysicalOutcomeRunDefers)
			p.compileCoroRunDefers(b, v)
			return
		}
		b.RunDefers()
	case *ssa.Panic:
		p.recordPanicLocation(b, v.Pos())
		// panic is not a Call instruction, so callEx's statement anchor
		// does not cover it; the panic snapshot attributes the panicking
		// frame to this pc (issue5856 wants the panic line, not the
		// nearest call's).
		p.emitPCLineLabel(b, v.Pos())
		outcome, outcomePlanned := p.plannedCoroPhysicalOutcome(v)
		if outcomePlanned {
			p.observeCoroPhysicalOutcome(v, outcome.outcome)
			switch outcome.outcome {
			case coroPhysicalOutcomeSyntheticSelectTrap:
				p.compileCoroSyntheticSelectPanic(b, v)
			case coroPhysicalOutcomePanic:
				p.compileCoroExplicitStatusPanic(b, v)
			default:
				panic(fmt.Sprintf("panic selected incompatible frozen physical outcome recipe %s", outcome.outcome))
			}
			return
		}
		arg := p.compileValue(b, v.X)
		b.Panic(arg)
	case *ssa.Send:
		ch := p.compileValue(b, v.Chan)
		x := p.compileValue(b, v.X)
		p.recordPanicLocation(b, v.Pos())
		operation, operationPlanned := p.plannedCoroPhysicalOperation(v)
		if operationPlanned && operation.operation == coroPhysicalOperationChannelSend {
			p.observeCoroPhysicalOperation(v, coroPhysicalOperationChannelSend)
			p.compileCoroChanSend(b, ch, x)
		} else if !operationPlanned || operation.operation == coroPhysicalOperationNone {
			b.Send(ch, x)
		} else {
			panic(fmt.Sprintf("channel send selected incompatible frozen physical operation recipe %s", operation.operation))
		}
	case *ssa.DebugRef:
		if p.frontendOptions().DebugSymbols && v.Parent().Origin() == nil {
			p.debugRef(b, v)
		}
	default:
		panic(fmt.Sprintf("compileInstr: unknown instr - %T\n", instr))
	}
}

// compileCoroPlannedDeref is the sole bridge from the frozen physical
// dereference recipe to codegen. A recipe with nilGuard set emits the
// explicit-status guard; otherwise its address producer already owns the
// source fault and this site only records the helper elision.
func (p *context) compileCoroPlannedDeref(
	b llssa.Builder,
	deref *ssa.UnOp,
	base llssa.Expr,
	physical coroPhysicalInstructionPlan,
) llssa.Expr {
	if physical.recipe != coroPhysicalInstructionDeref {
		panic(fmt.Sprintf("planned dereference selected incompatible frozen physical recipe %s", physical.recipe))
	}
	p.observeCoroPhysicalInstruction(deref, coroPhysicalInstructionDeref)
	if !physical.nilGuard {
		return base
	}
	return p.compileCoroImplicitNilDerefGuard(b, deref, base)
}

func (p *context) getLocalVariable(b llssa.Builder, fn *ssa.Function, v *types.Var) llssa.DIVar {
	if p.debugDIVars != nil {
		if div, ok := p.debugDIVars[v]; ok {
			return div
		}
	}
	pos := p.fset.Position(v.Pos())
	t := p.type_(v.Type(), llssa.InGo)
	scope := b.DIScope(p.fn, v.Parent())
	div := b.DIVarAuto(scope, pos, v.Name(), t)
	if p.debugDIVars != nil {
		p.debugDIVars[v] = div
	}
	return div
}

func (p *context) compileFunction(v *ssa.Function) (goFn llssa.Function, pyFn llssa.PyObjRef, kind int) {
	if p.rawPlainBody {
		return p.compileRawPlainFunction(v)
	}
	return p.compileManagedFunction(v)
}

func (p *context) ownsFunctionEmission(v *ssa.Function) bool {
	if v == nil {
		return false
	}
	if p.emissionUniverse != nil && p.emissionOwner != nil &&
		p.emissionUniverse.CompleteRuntimeABI() {
		// The complete ProgramIR freezes one exact body owner key for every
		// emitted instance (and multiple keys for intentional multi-owner
		// generic/linkonce instances). x/tools' synthetic wrapper Pkg may name
		// the consumer rather than the receiver's declaring package, so neither
		// fn.Pkg nor receiver provenance is authoritative at emission time.
		_, owns := p.emissionUniverse.coroProgramIR.siteOwners[emissionFunctionOwnerKey{
			function: v,
			owner:    p.emissionOwner,
		}]
		return owns
	}
	if v.Pkg == p.goPkg {
		return true
	}
	if v.Pkg == nil {
		// x/tools promoted wrappers and compiler-generated adapters have no
		// SSA package even though the frozen universe assigns exactly one body
		// owner. A consumer may reference their linkonce symbol, but must emit
		// only a declaration; otherwise its body would consume the declaring
		// package's ProgramIR under the consumer owner.
		if p.emissionUniverse != nil && p.emissionOwner != nil {
			return p.emissionUniverse.ownerOf(v) == p.emissionOwner
		}
		return true
	}
	// A patched package's original and alternate SSA packages share one LLVM
	// module and one prepared emission owner. A nested alternate function may be
	// demanded before eager member traversal reaches its parent; treating it as
	// cross-package would return ignoredFunc because captured declarations
	// cannot be reconstructed outside their owning module.
	return p.emissionUniverse != nil && p.emissionOwner != nil &&
		p.emissionUniverse.ownerOf(v) == p.emissionOwner
}

func (p *context) compileManagedFunction(v *ssa.Function) (goFn llssa.Function, pyFn llssa.PyObjRef, kind int) {
	if p.compilation != nil &&
		p.compilation.CoroPlan != nil && p.compilation.EmissionUniverse != nil {
		canonical, ok := p.compilation.EmissionUniverse.Resolve(v)
		if !ok || canonical == nil {
			panic(fmt.Errorf("managed function resolution: function %q is absent from the prepared emission universe", v.Name()))
		}
		if plan, planned := p.compilation.CoroPlan.FunctionPlan(canonical); planned && plan.Emission == coro.EmitRawPlain {
			owner := "<unknown>"
			if p.goFn != nil {
				owner = p.goFn.String()
			}
			panic(fmt.Errorf(
				"managed function resolution: raw-plain-only function %q (%s) has no managed entry while compiling %s",
				plan.ID, canonical.String(), owner,
			))
		}
		v = canonical
	}
	// TODO(xsw) v.Pkg == nil: means auto generated function?
	if p.ownsFunctionEmission(v) {
		// function in this package
		goFn, pyFn, kind = p.compileFuncDecl(p.pkg, v)
		if kind != ignoredFunc {
			return
		}
	}
	return p.funcOf(v)
}

// compileFunctionEntry preserves a compiler-selected physical symbol role.
// Generic entries continue through the ordinary resolver. The private
// patch-original initializer must instead carry its already-frozen name into
// both a same-package definition and a cross-package declaration.
func (p *context) compileFunctionEntry(entry plannedFunctionSymbol) (goFn llssa.Function, pyFn llssa.PyObjRef, kind int) {
	if !entry.patchOriginalInit {
		return p.compileFunction(entry.function)
	}
	if p.rawPlainBody {
		panic("managed patch-original initializer entry requested from a raw plain body")
	}
	if err := entry.checkSupported(); err != nil {
		panic(err)
	}
	if entry.function.Pkg == p.goPkg || entry.function.Pkg == nil {
		return p.compileFuncDeclVariantEntry(p.pkg, entry, false)
	}
	return p.funcOfEntry(entry)
}

func (p *context) compileRawPlainFunction(v *ssa.Function) (goFn llssa.Function, pyFn llssa.PyObjRef, kind int) {
	if v == nil || p.compilation == nil || p.compilation.CoroPlan == nil || p.compilation.EmissionUniverse == nil {
		panic("raw plain function resolution requires an exact function, emission universe, and coroutine plan")
	}
	canonical, ok := p.compilation.EmissionUniverse.Resolve(v)
	if !ok || canonical == nil {
		panic(fmt.Errorf("raw plain function resolution: function %q is absent from the prepared emission universe", v.Name()))
	}
	v = canonical
	entry, err := p.resolveFunctionSymbol(v)
	if err != nil {
		panic(err)
	}
	if entry.ftype != goFunc {
		// Frontend intrinsics such as internal/abi.FuncPCABI0 intentionally have
		// no emitted Go body and therefore no raw-demand closure member. Preserve
		// their ordinary instruction classification before consulting the Go-body
		// emission plan, exactly as managed function resolution does.
		return p.funcOfEntry(entry)
	}
	plan, planned := p.compilation.CoroPlan.FunctionPlan(v)
	if !planned {
		panic(fmt.Errorf("raw plain function resolution: function %q is absent from the compilation plan", v.Name()))
	}
	switch plan.Emission {
	case coro.EmitPlain, coro.EmitExternal:
		// A bounded plain primary or an independently classified external leaf
		// already has the only physical ABI this raw caller needs.
		return p.compileManagedFunction(v)
	case coro.EmitRawPlain:
		if !p.compilation.CoroPlan.HasRawPlainVariant(v) {
			panic(fmt.Errorf("raw plain function resolution: raw-only function %q has no planned raw plain body", plan.ID))
		}
		if p.ownsFunctionEmission(v) {
			return p.compileFuncDeclVariant(p.pkg, v, true)
		}
		return p.funcOfEntry(p.mustRawPlainFunctionSymbol(v))
	case coro.EmitCoroutine:
		// Continue below: a mixed suspendable target has a separately lowered
		// raw body selected by the same frozen closure proof.
	case coro.EmitNone:
		caller := "<none>"
		if p.goFn != nil {
			caller = p.goFn.String()
			if callerPlan, ok := p.compilation.CoroPlan.FunctionPlan(p.goFn); ok {
				caller = fmt.Sprintf("%s [%s]", caller, callerPlan.ID)
			}
		}
		panic(fmt.Errorf(
			"raw plain function resolution: caller %s selected non-emitted target %s [%s] (synthetic=%q)",
			caller, v.String(), plan.ID, v.Synthetic,
		))
	default:
		panic(fmt.Errorf("raw plain function resolution: function %q has unsupported emission %s", plan.ID, plan.Emission))
	}
	if !p.compilation.CoroPlan.HasRawPlainVariant(v) {
		panic(fmt.Errorf("raw plain function resolution: managed coroutine %q has no planned raw plain variant", plan.ID))
	}
	if p.ownsFunctionEmission(v) {
		return p.compileFuncDeclVariant(p.pkg, v, true)
	}
	return p.funcOfEntry(p.mustRawPlainFunctionSymbol(v))
}

func (p *context) compileValue(b llssa.Builder, v ssa.Value) llssa.Expr {
	if iv, ok := v.(instrOrValue); ok {
		return p.compileInstrOrValue(b, iv, true)
	}
	switch v := v.(type) {
	case *ssa.Parameter:
		fn := v.Parent()
		for idx, param := range fn.Params {
			if param == v {
				return b.Param(idx + p.coroEmissionSourceParamBase())
			}
		}
	case *ssa.Function:
		if value, handled := p.tryCompileCoroPlainDispatchFunctionValue(b, v); handled {
			return value
		}
		value := p.compileRawFunctionValue(v)
		if facade, handled := p.compileGoLinknameFunctionValueFacade(b, v, value); handled {
			return facade
		}
		return value
	case *ssa.Global:
		varName := v.Name()
		val := p.varOf(b, v)
		if isCgoVar(varName) {
			p.cgoSymbols = append(p.cgoSymbols, val.Name())
		}
		if p.frontendOptions().DebugSymbols && p.localityAllowsGlobalDebug(v) {
			pos := p.fset.Position(v.Pos())
			b.DIGlobal(val, v.Name(), pos)
		}
		return val
	case *ssa.Const:
		t := types.Default(v.Type())
		bg := llssa.InGo
		if p.inCFunc {
			bg = llssa.InC
		}
		return b.Const(v.Value, p.type_(t, bg))
	case *ssa.FreeVar:
		fn := v.Parent()
		for idx, freeVar := range fn.FreeVars {
			if freeVar == v {
				if value, handled := p.tryCompileCoroFreeVar(b, fn, idx); handled {
					return value
				}
				return p.fn.FreeVar(b, idx)
			}
		}
	}
	panic(fmt.Sprintf("compileValue: unknown value - %T\n", v))
}

func isBlankFieldStore(addr ssa.Value) bool {
	field, ok := addr.(*ssa.FieldAddr)
	if !ok {
		return false
	}
	_, st, ok := fieldAddrStruct(field)
	return ok && st.Field(field.Field).Name() == "_"
}

const rangeOverFuncYieldSynthetic = "range-over-func yield"

func (p *context) rangeFuncCallNeedsDeferDrain(call *ssa.CallCommon) bool {
	for _, arg := range call.Args {
		closure, ok := arg.(*ssa.MakeClosure)
		if !ok {
			continue
		}
		fn, ok := closure.Fn.(*ssa.Function)
		if !ok || fn.Synthetic != rangeOverFuncYieldSynthetic {
			continue
		}
		if p.functionHasExplicitStackDefer(fn) {
			return true
		}
	}
	return false
}

// Explicit defer stacks live in nested yield closures, but their drain point
// belongs to the enclosing function immediately after the rangefunc call.
func (p *context) functionHasExplicitStackDefer(fn *ssa.Function) bool {
	if p.stackDefers == nil {
		p.stackDefers = make(map[*ssa.Function]bool)
	}
	return p.functionHasExplicitStackDeferSeen(fn, make(map[*ssa.Function]bool))
}

func (p *context) functionHasExplicitStackDeferSeen(fn *ssa.Function, seen map[*ssa.Function]bool) bool {
	if fn == nil || seen[fn] {
		return false
	}
	if p.stackDefers == nil {
		p.stackDefers = make(map[*ssa.Function]bool)
	}
	if has, ok := p.stackDefers[fn]; ok {
		return has
	}
	seen[fn] = true
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if d, ok := instr.(*ssa.Defer); ok && d.DeferStack != nil {
				p.stackDefers[fn] = true
				return true
			}
		}
	}
	for _, child := range fn.AnonFuncs {
		if p.functionHasExplicitStackDeferSeen(child, seen) {
			p.stackDefers[fn] = true
			return true
		}
	}
	p.stackDefers[fn] = false
	return false
}

// deferRunPos is where gc attributes a deferred function's caller frame:
// the function's closing brace — defers run at function exit, not at the
// defer statement (goroot issue14646, issue5856).
func (p *context) deferRunPos(fallback token.Pos) token.Pos {
	if p.goFn != nil {
		switch syntax := p.goFn.Syntax().(type) {
		case *ast.FuncDecl:
			if syntax.Body != nil && syntax.Body.Rbrace.IsValid() {
				return syntax.Body.Rbrace
			}
		case *ast.FuncLit:
			if syntax.Body != nil && syntax.Body.Rbrace.IsValid() {
				return syntax.Body.Rbrace
			}
		}
	}
	return fallback
}

func (p *context) returnNeedsImplicitRunDefers(ret *ssa.Return) bool {
	fn := ret.Parent()
	if fn == nil || fn.Synthetic != "" || ret.Block() == fn.Recover {
		return false
	}
	if previousNonDebugInstrIsRunDefers(ret) {
		return false
	}
	return p.functionHasExplicitStackDeferInAnon(fn)
}

// namedResultSlot returns the allocation for fn's named result at index.
// The SSA Function API exposes result variables through their source-level
// Alloc instructions, while Return.Results only exposes the values currently
// used to form a particular return tuple.
func (p *context) namedResultSlot(index int) *ssa.Alloc {
	fn := p.goFn
	if fn == nil || index < 0 || index >= fn.Signature.Results().Len() {
		return nil
	}
	result := fn.Signature.Results().At(index)
	if result.Name() == "" {
		return nil
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			alloc, ok := instr.(*ssa.Alloc)
			if ok && alloc.Comment == result.Name() && alloc.Pos() == result.Pos() {
				return alloc
			}
		}
	}
	return nil
}

func previousNonDebugInstrIsRunDefers(ret *ssa.Return) bool {
	block := ret.Block()
	if block == nil {
		return false
	}
	for i := len(block.Instrs) - 1; i >= 0; i-- {
		instr := block.Instrs[i]
		if instr == ret {
			continue
		}
		if _, ok := instr.(*ssa.DebugRef); ok {
			continue
		}
		_, ok := instr.(*ssa.RunDefers)
		return ok
	}
	return false
}

func (p *context) functionHasExplicitStackDeferInAnon(fn *ssa.Function) bool {
	if p.anonDefers == nil {
		p.anonDefers = make(map[*ssa.Function]bool)
	}
	return p.functionHasExplicitStackDeferInAnonSeen(fn, make(map[*ssa.Function]bool))
}

func (p *context) functionHasExplicitStackDeferInAnonSeen(fn *ssa.Function, seen map[*ssa.Function]bool) bool {
	if fn == nil || seen[fn] {
		return false
	}
	if p.anonDefers == nil {
		p.anonDefers = make(map[*ssa.Function]bool)
	}
	if has, ok := p.anonDefers[fn]; ok {
		return has
	}
	seen[fn] = true
	for _, child := range fn.AnonFuncs {
		if p.functionHasExplicitStackDeferSeen(child, seen) {
			p.anonDefers[fn] = true
			return true
		}
	}
	p.anonDefers[fn] = false
	return false
}

func (p *context) compileVArg(ret []llssa.Expr, b llssa.Builder, v ssa.Value) []llssa.Expr {
	_ = b
	switch v := v.(type) {
	case *ssa.Slice: // varargs: this is a varargs slice
		if args, ok := p.isVArgs(v.X); ok {
			return append(ret, args...)
		}
	case *ssa.Const:
		if v.Value == nil {
			return ret
		}
	case *ssa.Parameter:
		if llssa.HasNameValist(v.Parent().Signature) {
			return ret
		}
	}
	panic(fmt.Sprintf("compileVArg: unknown value - %T\n", v))
}

func (p *context) compileValues(b llssa.Builder, vals []ssa.Value, hasVArg int) []llssa.Expr {
	n := len(vals) - hasVArg
	ret := make([]llssa.Expr, n)
	for i := 0; i < n; i++ {
		ret[i] = p.compileValue(b, vals[i])
	}
	if hasVArg > 0 {
		ret = p.compileVArg(ret, b, vals[n])
	}
	return ret
}

// -----------------------------------------------------------------------------

// Patch is a patch of some package.
type Patch struct {
	Alt   *ssa.Package
	Types *types.Package
}

// Patches is patches of some packages.
type Patches = map[string]Patch

// NewPackage compiles a Go package to LLVM IR package.
// Deprecated: use NewPackageExWithEmbedMetaOptions with explicit Options.
func NewPackage(prog llssa.Program, pkg *ssa.Package, files []*ast.File) (ret llssa.Package, err error) {
	ret, _, err = NewPackageEx(prog, nil, nil, pkg, files)
	return
}

// NewPackageEx and NewPackage compile as a one-shot compilation: each
// call gets fresh caller-tracking memoization. Multi-package drivers
// use NewPackageExWithEmbedOptions with shared CallerTracking and Compilation
// inputs instead.

// NewPackageEx compiles a Go package to LLVM IR package.
//
// Parameters:
//   - prog: target LLVM SSA program context
//   - patches: optional package patches applied during compilation
//   - rewrites: per-package string initializers rewritten at compile time
//   - pkg: SSA package to compile
//   - files: parsed AST files that belong to the package
//
// The rewrites map uses short variable names (without package qualifier) and
// only affects string-typed globals defined in the current package.
// Deprecated: use NewPackageExWithEmbedMetaOptions with explicit Options.
func NewPackageEx(prog llssa.Program, patches Patches, rewrites map[string]string, pkg *ssa.Package, files []*ast.File) (ret llssa.Package, externs []string, err error) {
	return newPackageEx(prog, nil, patches, rewrites, pkg, files, nil, PackageOptions{})
}

// NewPackageExWithEmbed compiles a package using pre-loaded go:embed metadata.
//
// This avoids re-scanning directives when the caller already loaded them.
// ct carries the compilation-scoped caller-tracking memoization; drivers
// compiling multiple packages pass the same instance for every package
// of one compilation (like patches). nil means one-shot: a fresh
// instance is created for this call.
// Deprecated: use NewPackageExWithEmbedMetaOptions with explicit Options.
func NewPackageExWithEmbed(prog llssa.Program, ct *CallerTracking, patches Patches, rewrites map[string]string, pkg *ssa.Package, files []*ast.File, embedMap goembed.VarMap) (ret llssa.Package, externs []string, err error) {
	return newPackageEx(prog, ct, patches, rewrites, pkg, files, &embedMap, PackageOptions{})
}

// NewPackageExWithEmbedOptions compiles a package with compilation-scoped and
// per-package inputs. Existing one-shot entry points use zero PackageOptions.
func NewPackageExWithEmbedOptions(prog llssa.Program, ct *CallerTracking, patches Patches, rewrites map[string]string, pkg *ssa.Package, files []*ast.File, embedMap goembed.VarMap, opts PackageOptions) (ret llssa.Package, externs []string, err error) {
	return newPackageEx(prog, ct, patches, rewrites, pkg, files, &embedMap, opts)
}

// NewPackageExWithEmbedMeta preserves the metadata-aware frontend entry point
// while routing all per-package inputs through the single PackageOptions
// contract used by active coroutine compilation.
func NewPackageExWithEmbedMeta(prog llssa.Program, ct *CallerTracking, patches Patches, rewrites map[string]string, pkg *ssa.Package, files []*ast.File, embedMap goembed.VarMap, metaCollect bool) (ret llssa.Package, externs []string, err error) {
	return newPackageEx(prog, ct, patches, rewrites, pkg, files, &embedMap, PackageOptions{
		MetaCollect: metaCollect,
	})
}

// NewPackageExWithEmbedMetaOptions is NewPackageExWithEmbedMeta with explicit
// request-local frontend options.
func NewPackageExWithEmbedMetaOptions(prog llssa.Program, ct *CallerTracking, patches Patches, rewrites map[string]string, pkg *ssa.Package, files []*ast.File, embedMap goembed.VarMap, metaCollect bool, options Options) (ret llssa.Package, externs []string, err error) {
	return newPackageEx(prog, ct, patches, rewrites, pkg, files, &embedMap, PackageOptions{
		MetaCollect:        metaCollect,
		FrontendOptions:    options,
		FrontendOptionsSet: true,
	})
}

func newPackageEx(prog llssa.Program, ct *CallerTracking, patches Patches, rewrites map[string]string, pkg *ssa.Package, files []*ast.File, embedMap *goembed.VarMap, opts PackageOptions) (ret llssa.Package, externs []string, err error) {
	options := legacyOptions()
	if opts.FrontendOptionsSet {
		options = opts.FrontendOptions
	}
	var prepared *preparedEmissionPackage
	if opts.Compilation != nil {
		if err := opts.Compilation.preflightCoroPlan(); err != nil {
			return nil, nil, err
		}
		if err := opts.Compilation.validateCoroWorkerCodegenProgram(prog); err != nil {
			return nil, nil, err
		}
		if opts.CacheHit {
			if err := opts.Compilation.validateCoroCacheIdentity(); err != nil {
				return nil, nil, err
			}
		}
		if opts.Compilation.EmissionUniverse != nil {
			prepared, err = opts.Compilation.EmissionUniverse.checkPackage(pkg, files, patches)
			if err != nil {
				return nil, nil, fmt.Errorf("coroutine entry resolution: %w", err)
			}
		}
	}
	pkgProg := pkg.Prog
	pkgTypes := pkg.Pkg
	oldTypes := pkgTypes
	pkgName, pkgPath := pkgTypes.Name(), llssa.PathOf(pkgTypes)
	patch, hasPatch := patches[pkgPath]
	if prepared != nil {
		pkgTypes = prepared.pkgTypes
		oldTypes = prepared.oldTypes
		patch = prepared.patch
		hasPatch = prepared.hasPatch
	}
	if hasPatch {
		pkgTypes = patch.Types
		pkg.Pkg = pkgTypes
		patch.Alt.Pkg = pkgTypes
	}
	if err = ParsePkgSyntax(prog, pkgProg.Fset, pkgTypes, files); err != nil {
		return nil, nil, err
	}
	if err = prog.ValidateLocalitiesFor(pkgTypes); err != nil {
		return nil, nil, err
	}
	if err = validateLocalInitializers(prog, pkgTypes); err != nil {
		return nil, nil, err
	}
	if pkgPath == llssa.PkgRuntime {
		prog.SetRuntime(pkgTypes)
	}
	ret = prog.NewPackageEx(pkgName, pkgPath, opts.MetaCollect)
	if options.Debug {
		ret.InitDebug(pkgName, pkgPath, pkgProg.Fset)
		defer ret.FinalizeDebug()
	}

	if ct == nil {
		ct = NewCallerTracking()
	}
	ctx := &context{
		prog:             prog,
		pkg:              ret,
		fset:             pkgProg.Fset,
		goProg:           pkgProg,
		goTyps:           pkgTypes,
		goPkg:            pkg,
		patches:          patches,
		options:          options,
		optionsSet:       true,
		skips:            make(map[string]none),
		vargs:            make(map[*ssa.Alloc][]llssa.Expr),
		funcs:            make(map[*ssa.Function]llssa.Function),
		rawPlainFuncs:    make(map[*ssa.Function]llssa.Function),
		linkOnceFns:      make(map[*ssa.Function]none),
		addrOfFieldAddrs: collectAddrOfFieldSelectors(files),
		loaded: map[*types.Package]*pkgInfo{
			types.Unsafe: {kind: PkgDeclOnly}, // TODO(xsw): PkgNoInit or PkgDeclOnly?
		},
		cgoSymbols: make([]string, 0, 128),
		rewrites:   rewrites,

		compilation:       opts.Compilation,
		cacheRegistration: opts.CacheHit,

		trackCallerFrames:  filesUseRuntimeCaller(files) || packageUsesRuntimeCaller(ct, pkg),
		runtimeCallerFuncs: runtimeCallerFuncSet(ct, pkg),
		logicalCallerFuncs: runtimeLogicalCallerFuncSet(ct, pkg),
	}
	if opts.Compilation != nil {
		ctx.emissionUniverse = opts.Compilation.EmissionUniverse
		ctx.emissionOwner = prepared
	}
	ctx.observeCoroPlan()
	if embedMap != nil {
		ctx.embedMap = *embedMap
	} else {
		ctx.embedMap, err = goembed.LoadDirectives(ctx.fset, files)
		if err != nil {
			panic(err)
		}
	}
	ctx.initPyModule()
	ctx.initFiles(pkgPath, files, pkgName == "C")
	ctx.prog.SetPatch(ctx.patchType)
	ctx.prog.SetCompileMethods(ctx.checkCompileMethods)
	ret.SetResolveLinkname(ctx.resolveLinkname)
	if opts.Compilation != nil {
		ret.SetResolveMethodLinkname(ctx.resolveMethodLinkname)
		ret.SetResolveMethodEntry(ctx.resolveMethodEntry)
		ret.SetResolveInterfaceMethodDescriptor(ctx.resolveInterfaceMethodDescriptor)
		ret.SetResolveRuntimeCall(ctx.resolveCoroLoweredRuntimeCall)
	}

	if hasPatch {
		skips := ctx.skips
		typepatch.Merge(pkgTypes, oldTypes, skips, ctx.skipall)
		ctx.skips = nil
		ctx.state = pkgInPatch
		if _, ok := skips["init"]; ok || ctx.skipall {
			ctx.state |= pkgFNoOldInit
		}
		processPkg(ctx, ret, patch.Alt)
		ctx.state = pkgHasPatch
		ctx.skips = skips
	}
	if !ctx.skipall {
		processPkg(ctx, ret, pkg)
	}
	if err := ctx.emitCoroFrozenOwnerBodies(ret); err != nil {
		return nil, nil, err
	}
	for len(ctx.inits) > 0 {
		inits := ctx.inits
		ctx.inits = nil
		for _, ini := range inits {
			ini()
		}
	}
	if err := ctx.validateCoroFrozenOwnerBodies(ret); err != nil {
		return nil, nil, err
	}
	if fn := ctx.initAfter; fn != nil {
		ctx.initAfter = nil
		fn()
	}
	ctx.emitCoroRootPackageAnchor(ret)
	if err := ctx.emitCoroLibraryEffectSummary(); err != nil {
		return nil, nil, err
	}
	ret.MaterializePreserveSyms()
	if opts.MetaCollect {
		if err := ret.FinishMetaCollection(); err != nil {
			return nil, nil, fmt.Errorf("build meta for %s: %w", pkgPath, err)
		}
	}
	externs = ctx.cgoSymbols
	return
}

func (p *context) observeCoroPlan() {
	if p.cacheRegistration || p.compilation == nil || p.compilation.CoroPlan == nil {
		return
	}
	if observer := p.compilation.CoroPlanObserver; observer != nil {
		observer(p.goPkg, p.compilation.CoroPlan)
	}
}

// compileRawFunctionValue returns the selected body entry without applying a
// function-value representation conversion. Static calls and MakeClosure use
// this path even when a different exact producer for the same SSA target is
// descriptor-backed.
func (p *context) compileRawFunctionValue(v *ssa.Function) llssa.Expr {
	if p.compilation != nil && p.compilation.EmissionUniverse != nil {
		canonical, ok := p.compilation.EmissionUniverse.Resolve(v)
		if !ok {
			panic(fmt.Errorf("coroutine entry resolution: function value %q is absent from the prepared emission universe", v.Name()))
		}
		v = canonical
	}
	if _, _, ftype := p.funcName(v); ftype == llgoInstr {
		if p.compilation != nil && p.compilation.EmissionUniverse != nil {
			wrapper, ok := p.compilation.EmissionUniverse.intrinsicWrapper(p.goPkg, v)
			if !ok {
				panic(fmt.Errorf("coroutine entry resolution: intrinsic function value %q was not materialized before codegen", v.Name()))
			}
			v = wrapper
		} else {
			v = ssawrap.MakeCallWrapper(p.goProg, v)
		}
	}
	aFn, pyFn, _ := p.compileFunction(v)
	if aFn != nil {
		return aFn.Expr
	}
	return pyFn.Expr
}

// compileGoLinknameFunctionValueFacade preserves the declared Go type when a
// bodyless go:linkname function is used as a value. The exact paired
// definition owns the executable symbol and may use typed pointers (or a
// flattened method receiver) where the declaration deliberately exposes an
// unsafe.Pointer facade. Direct calls can consume that ABI-compatible symbol
// immediately, but boxing the implementation's type would publish the wrong
// reflect.Type and recursively materialize implementation-only ABI metadata.
//
// The first ChangeType treats the already-proven symbol as an opaque function
// pointer with the declaration's physical signature. The second constructs the
// ordinary {code, env} Go function value. No adapter body or second callable
// entry is introduced.
func (p *context) compileGoLinknameFunctionValueFacade(
	b llssa.Builder, source *ssa.Function, value llssa.Expr,
) (llssa.Expr, bool) {
	if source == nil || value.IsNil() {
		return llssa.Expr{}, false
	}
	signature, paired, err := p.goLinknameFunctionValueSignature(source)
	if !paired {
		return llssa.Expr{}, false
	}
	if err != nil {
		panic(err)
	}
	physical := p.prog.PhysicalFuncDecl(signature, llssa.InGo)
	code := b.ChangeType(p.prog.Type(physical, llssa.InC), value)
	closure := p.prog.Type(signature, llssa.InGo)
	return b.ChangeType(closure, code), true
}

// goLinknameFunctionValueSignature returns the exact declared signature whose
// Go value semantics survive an active bodyless go:linkname alias. Calls use
// the paired definition's symbol; values retain this source-side type facade.
func (p *context) goLinknameFunctionValueSignature(
	source *ssa.Function,
) (*types.Signature, bool, error) {
	universe := p.immutableEmissionUniverse()
	if universe == nil || source == nil {
		return nil, false, nil
	}
	pair, paired := universe.goLinknameDefinitions[source]
	if !paired {
		return nil, false, nil
	}
	canonical, resolved := universe.Resolve(source)
	if !resolved || canonical == nil || canonical != pair.definition {
		return nil, true, fmt.Errorf(
			"go:linkname function-value facade %q has no exact active definition",
			source.String(),
		)
	}
	if pair.declarationOwner == nil {
		return nil, true, fmt.Errorf(
			"go:linkname function-value facade %q has no frozen declaration owner",
			source.String(),
		)
	}
	sourceContext, err := universe.functionABIContext(source, pair.declarationOwner)
	if err != nil {
		return nil, true, fmt.Errorf("go:linkname function-value facade %q: %w", source.String(), err)
	}
	signature, ok := sourceContext.patchType(source.Signature).(*types.Signature)
	if !ok {
		return nil, true, fmt.Errorf(
			"go:linkname function-value facade %q has a non-signature declared type",
			source.String(),
		)
	}
	return signature, true, nil
}

func initFnNameOfHasPatch(name string) string {
	return name + "$hasPatch"
}

func processPkg(ctx *context, ret llssa.Package, pkg *ssa.Package) {
	type namedMember struct {
		name string
		val  ssa.Member
	}

	ctx.collectStaticGlobalInits(pkg)

	members := make([]*namedMember, 0, len(pkg.Members))
	skips := ctx.skips
	for name, v := range pkg.Members {
		if _, ok := skips[name]; !ok {
			members = append(members, &namedMember{name, v})
		}
	}
	sort.Slice(members, func(i, j int) bool {
		return members[i].name < members[j].name
	})
	localGlobals := make([]*ssa.Global, 0)
	for _, m := range members {
		global, ok := m.val.(*ssa.Global)
		if !ok || isCgoFuncPtrVar(global.Name()) {
			continue
		}
		localGlobals = append(localGlobals, global)
	}
	// Address accessors and replay guards must exist before any function body
	// can reference a local package variable, regardless of member sort order.
	ctx.prepareLocalVariables(ret, localGlobals)

	for _, m := range members {
		member := m.val
		switch member := member.(type) {
		case *ssa.Function:
			if strings.HasSuffix(member.Name(), "_trampoline") {
				continue
			}
			if member.TypeParams() != nil || member.TypeArgs() != nil {
				// TODO(xsw): don't compile generic functions
				// Do not try to build generic (non-instantiated) functions.
				continue
			}
			if ctx.omitUnemittedFunction(member) {
				continue
			}
			ctx.compileFuncDecl(ret, member)
		case *ssa.Type:
			ctx.compileType(ret, member)
		case *ssa.Global:
			if !isCgoFuncPtrVar(member.Name()) {
				ctx.compileGlobal(ret, member)
			}
		}
	}
}

func (p *context) type_(typ types.Type, bg llssa.Background) llssa.Type {
	return p.prog.Type(p.patchType(typ), bg)
}

func (p *context) patchType(typ types.Type) (r types.Type) {
	r, _ = p._patchType(typ)
	return
}

func (p *context) _patchType(typ types.Type) (types.Type, bool) {
	original := typ
	if universe := p.immutableEmissionUniverse(); universe != nil {
		typ, _ = universe.patchEmissionTypeGraph(p, typ)
	}
	switch typ := typ.(type) {
	case *types.Alias:
		actual := types.Unalias(typ)
		if patched, ok := p._patchType(actual); ok {
			return patched, true
		}
	case *types.Pointer:
		if t, ok := p._patchType(typ.Elem()); ok {
			return types.NewPointer(t), true
		}
	case *types.Slice:
		if t, ok := p._patchType(typ.Elem()); ok {
			return types.NewSlice(t), true
		}
	case *types.Array:
		if t, ok := p._patchType(typ.Elem()); ok {
			return types.NewArray(t, typ.Len()), true
		}
	case *types.Map:
		var patched bool
		key := typ.Key()
		elem := typ.Elem()
		if t, ok := p._patchType(key); ok {
			key = t
			patched = true
		}
		if t, ok := p._patchType(elem); ok {
			elem = t
			patched = true
		}
		if patched {
			return types.NewMap(key, elem), true
		}
	case *types.Chan:
		if t, ok := p._patchType(typ.Elem()); ok {
			return types.NewChan(typ.Dir(), t), true
		}
	case *types.Struct:
		var patched bool
		vars := make([]*types.Var, typ.NumFields())
		tags := make([]string, typ.NumFields())
		for i := 0; i < typ.NumFields(); i++ {
			v := typ.Field(i)
			if t, ok := p._patchType(v.Type()); ok {
				vars[i] = types.NewField(v.Pos(), v.Pkg(), v.Name(), t, v.Anonymous())
				patched = true
			} else {
				vars[i] = v
			}
			tags[i] = typ.Tag(i)
		}
		if patched {
			return types.NewStruct(vars, tags), true
		}
	case *types.Interface:
		typ.Complete()
		methods := make([]*types.Func, typ.NumExplicitMethods())
		embeddeds := make([]types.Type, typ.NumEmbeddeds())
		patched := false
		for index := range methods {
			method := typ.ExplicitMethod(index)
			methodType, ok := p._patchType(method.Type())
			if ok {
				methods[index] = types.NewFunc(method.Pos(), method.Pkg(), method.Name(), methodType.(*types.Signature))
				patched = true
			} else {
				methods[index] = method
			}
		}
		for index := range embeddeds {
			embedded := typ.EmbeddedType(index)
			if replacement, ok := p._patchType(embedded); ok {
				embeddeds[index] = replacement
				patched = true
			} else {
				embeddeds[index] = embedded
			}
		}
		if patched {
			iface := types.NewInterfaceType(methods, embeddeds)
			if typ.IsImplicit() {
				iface.MarkImplicit()
			}
			return iface.Complete(), true
		}
	case *types.Named:
		if t, ok := p.patchLocalGenericNamed(typ); ok {
			return t, true
		}
		o := typ.Obj()
		if pkg := o.Pkg(); pkg != nil {
			// ModeTest and package metadata can retain an equivalent
			// *types.Package copy that was not the exact object marked by
			// typepatch.Merge. The immutable patch table is path-keyed, so use
			// that same identity for every imported copy. An alternate type
			// already present in Patch.Types is its own replacement and must
			// terminate this recursion.
			if patch, ok := p.patches[pkg.Path()]; ok && patch.Types != nil {
				if obj := patch.Types.Scope().Lookup(o.Name()); obj != nil {
					replacement := instantiate(obj.Type(), typ)
					if replacement == typ {
						break
					}
					if p.preservePatchedNamed {
						return replacement, true
					}
					raw := p.prog.Type(replacement, llssa.InGo).RawType()
					return raw, typ != raw
				}
			}
		}
	case *types.Tuple:
		var patched bool
		vars := make([]*types.Var, typ.Len())
		for i := 0; i < typ.Len(); i++ {
			v := typ.At(i)
			if t, ok := p._patchType(v.Type()); ok {
				vars[i] = types.NewVar(v.Pos(), v.Pkg(), v.Name(), t)
				patched = true
			} else {
				vars[i] = v
			}
		}
		if patched {
			return types.NewTuple(vars...), true
		}
	case *types.Signature:
		params, ok1 := p._patchType(typ.Params())
		results, ok2 := p._patchType(typ.Results())
		if ok1 || ok2 {
			return types.NewSignature(typ.Recv(), params.(*types.Tuple), results.(*types.Tuple), typ.Variadic()), true
		}
	}
	return typ, typ != original
}

func (p *context) immutableEmissionUniverse() *EmissionUniverse {
	if p == nil {
		return nil
	}
	if p.emissionUniverse != nil {
		return p.emissionUniverse
	}
	return p.compilation.immutableEmissionUniverse()
}

func (p *context) patchLocalGenericNamed(t *types.Named) (*types.Named, bool) {
	if p.goFn == nil || isPatchedLocalGenericName(t.Obj().Name()) {
		return nil, false
	}
	universe := p.immutableEmissionUniverse()
	if universe != nil {
		if canonical := universe.cachedLocalGenericNamed(t); canonical != nil {
			return canonical, true
		}
	}
	localCtx := p.localGenericTypeContext(t)
	if localCtx == nil && universe != nil {
		localCtx = universe.registeredLocalGenericContext(p, t)
	}
	if localCtx == nil {
		return nil, false
	}
	if universe != nil {
		if canonical := universe.canonicalLocalGenericNamed(localCtx, t); canonical != nil {
			return canonical, true
		}
	}
	name := localCtx.localNamedName(t, false)
	obj := types.NewTypeName(t.Obj().Pos(), t.Obj().Pkg(), name, nil)
	return types.NewNamed(obj, t.Underlying(), nil), true
}

// localGenericTypeContext finds the instantiated lexical owner of a local
// named type. Anonymous functions share their parent's substitutions, but an
// x/tools local TypeName may have no scope parent; walking Function.Parent is
// therefore required to give outer-body and closure uses one canonical type.
func (p *context) localGenericTypeContext(t *types.Named) *context {
	if p == nil || p.goFn == nil || t == nil || t.Obj() == nil {
		return nil
	}
	ctx := *p
	for fn := p.goFn; fn != nil; fn = fn.Parent() {
		if len(fn.TypeArgs()) == 0 {
			continue
		}
		ctx.goFn = fn
		if ctx.isGenericLocalType(t.Obj()) {
			return &ctx
		}
	}
	return nil
}

func isPatchedLocalGenericName(name string) bool {
	// The patched name embeds type arguments in brackets. Go identifiers cannot
	// contain '[', so this also prevents repeatedly expanding the generated name.
	return strings.Contains(name, "[")
}

func (p *context) localNamedName(t *types.Named, suffix bool) string {
	obj := t.Obj()
	name := obj.Name()
	if isPatchedLocalGenericName(name) {
		return name
	}
	outer := p.localTypeOuterArgs(obj)
	own := typeListArgs(t.TypeArgs(), p.typeArgName)
	switch {
	case len(outer) != 0 && len(own) != 0:
		name += "[" + strings.Join(outer, ",") + ";" + strings.Join(own, ",") + "]"
	case len(outer) != 0:
		name += "[" + strings.Join(outer, ",") + "]"
	case len(own) != 0:
		name += "[" + strings.Join(own, ",") + "]"
	}
	if suffix {
		if n := p.localTypeOrdinal(obj); n != 0 {
			name += "·" + strconv.Itoa(n)
		}
	}
	return name
}

func (p *context) localTypeOuterArgs(obj types.Object) []string {
	// localNamedName is also used by non-local type arguments, so keep this
	// guard here even though patchLocalGenericNamed has already checked it.
	if p.goFn == nil || len(p.goFn.TypeArgs()) == 0 || !p.isGenericLocalType(obj) {
		return nil
	}
	args := p.goFn.TypeArgs()
	ret := make([]string, len(args))
	for i, arg := range args {
		ret[i] = p.typeArgName(arg)
	}
	return ret
}

func typeListArgs(list *types.TypeList, nameOf func(types.Type) string) []string {
	if list == nil {
		return nil
	}
	ret := make([]string, list.Len())
	for i := 0; i < list.Len(); i++ {
		ret[i] = nameOf(list.At(i))
	}
	return ret
}

func (p *context) typeArgName(t types.Type) string {
	// Keep this formatter aligned with ssa/abi.typeArgString; this variant must
	// additionally encode local generic type names while patching frontend types.
	if universe := p.immutableEmissionUniverse(); universe != nil {
		return universe.emissionTypeArgName(p, t)
	}
	switch t := t.(type) {
	case *types.Alias:
		return p.typeArgName(types.Unalias(t))
	case *types.Basic:
		return t.String()
	case *types.Named:
		name := p.localNamedName(t, p.isLocalType(t.Obj()))
		if pkg := t.Obj().Pkg(); pkg != nil {
			return reflectTypeArgPkgPath(pkg) + "." + name
		}
		return name
	case *types.Pointer:
		return "*" + p.typeArgName(t.Elem())
	case *types.Slice:
		return "[]" + p.typeArgName(t.Elem())
	case *types.Array:
		return fmt.Sprintf("[%v]%s", t.Len(), p.typeArgName(t.Elem()))
	case *types.Map:
		return fmt.Sprintf("map[%s]%s", p.typeArgName(t.Key()), p.typeArgName(t.Elem()))
	case *types.Chan:
		s := chanDirName(t.Dir())
		elem := p.typeArgName(t.Elem())
		if t.Dir() == types.SendRecv {
			if ch, ok := t.Elem().(*types.Chan); ok && ch.Dir() == types.RecvOnly {
				elem = "(" + elem + ")"
			}
		}
		return fmt.Sprintf("%s %s", s, elem)
	default:
		return types.TypeString(t, reflectTypeArgPkgPath)
	}
}

func chanDirName(dir types.ChanDir) string {
	switch dir {
	case types.SendRecv:
		return "chan"
	case types.SendOnly:
		return "chan<-"
	case types.RecvOnly:
		return "<-chan"
	default:
		panic("invalid channel direction")
	}
}

func reflectTypeArgPkgPath(pkg *types.Package) string {
	if pkg == nil {
		return ""
	}
	if pkg.Path() == "command-line-arguments" && pkg.Name() != "" {
		return pkg.Name()
	}
	return llssa.PathOf(pkg)
}

func (p *context) isGenericLocalType(obj types.Object) bool {
	if !p.isLocalType(obj) {
		return false
	}
	if obj.Parent() == nil {
		return p.inCurrentFunction(obj.Pos())
	}
	for scope := obj.Parent(); scope != nil; scope = scope.Parent() {
		if pkg := obj.Pkg(); pkg != nil && scope == pkg.Scope() {
			return false
		}
		if scopeHasTypeParams(scope) {
			return true
		}
	}
	return false
}

func (p *context) isLocalType(obj types.Object) bool {
	if obj == nil || obj.Pkg() == nil {
		return false
	}
	parent := obj.Parent()
	if parent == nil {
		return obj.Pos().IsValid()
	}
	return parent != obj.Pkg().Scope()
}

func scopeHasTypeParams(scope *types.Scope) bool {
	for _, name := range scope.Names() {
		if isTypeParamObject(scope.Lookup(name)) {
			return true
		}
	}
	return false
}

func (p *context) localTypeOrdinal(obj types.Object) int {
	scope := obj.Parent()
	if scope == nil || !obj.Pos().IsValid() {
		return p.localTypeOrdinalBySyntax(obj.Pos())
	}
	n := 0
	for _, name := range scope.Names() {
		o := scope.Lookup(name)
		if _, ok := o.(*types.TypeName); !ok || isTypeParamObject(o) {
			continue
		}
		if pos := o.Pos(); pos.IsValid() && pos <= obj.Pos() {
			n++
		}
	}
	return n
}

func (p *context) inCurrentFunction(pos token.Pos) bool {
	if !pos.IsValid() {
		return false
	}
	syntax := p.currentFunctionSyntax()
	return syntax != nil && syntax.Pos() <= pos && pos <= syntax.End()
}

func (p *context) localTypeOrdinalBySyntax(pos token.Pos) int {
	if !p.inCurrentFunction(pos) {
		return 0
	}
	n := 0
	ast.Inspect(p.currentFunctionSyntax(), func(node ast.Node) bool {
		spec, ok := node.(*ast.TypeSpec)
		if !ok {
			return true
		}
		if spec.Name != nil && spec.Name.Pos().IsValid() && spec.Name.Pos() <= pos {
			n++
		}
		return true
	})
	return n
}

func (p *context) currentFunctionSyntax() ast.Node {
	if p.goFn == nil {
		return nil
	}
	fn := p.goFn
	if origin := fn.Origin(); origin != nil {
		fn = origin
	}
	return fn.Syntax()
}

func isTypeParamObject(obj types.Object) bool {
	tn, ok := obj.(*types.TypeName)
	if !ok {
		return false
	}
	_, ok = tn.Type().(*types.TypeParam)
	return ok
}

func instantiate(orig types.Type, t *types.Named) (typ types.Type) {
	typ, _ = llssa.Instantiate(orig, t)
	return
}

func (p *context) resolveLinkname(name string) string {
	if link, ok := p.prog.Linkname(name); ok {
		prefix, ltarget, _ := strings.Cut(link, ".")
		if prefix != "C" {
			panic("resolveLinkname: invalid link: " + link)
		}
		return ltarget
	}
	return name
}

// resolveMethodLinkname maps the signature reconstructed by the ABI type
// builder back to the exact x/tools method or wrapper selected for that
// receiver. Active coroutine codegen must use the same frozen physical symbol
// for method-table references and compileFuncDecl definitions. The ordinary
// SetResolveLinkname path remains unchanged for report-only codegen.
func (p *context) resolveMethodLinkname(_ string, method *types.Func, sig *types.Signature) string {
	if name, planned := p.resolveCoroRawMethodSymbol(method, sig); planned {
		return name
	}
	fn := p.resolveInterfaceMethodSSA(method, sig)
	entry, err := p.resolveFunctionSymbol(fn)
	if err == nil {
		err = entry.checkSupported()
	}
	if err != nil {
		owner := "<no function>"
		if p.goFn != nil {
			owner = p.goFn.String()
		}
		panic(fmt.Errorf(
			"resolve ABI method linkname %q with signature %s while compiling %q through target %q (demand sources: %s): %w",
			method.FullName(), sig, owner, fn.String(),
			coroDemandReferenceTrace(p.immutableEmissionUniverse(), fn), err,
		))
	}
	return entry.name
}

// checkCompileMethods ensures that methods referenced from ABI method tables
// are available to the linker. Generic instances and anonymous structural
// types are emitted in the current SSA package. Package-level non-generic
// named types have declared methods emitted while the defining package's type
// members are compiled. Their generated wrappers are also materialized at each
// ABI-table use site: package archives are compiled independently, so the
// declaring package's plan cannot see every consumer demand. Deterministically
// named generated wrappers are linkonce and may therefore be coalesced safely.
// Active codegen uses the emission universe's declaration certificate instead
// of relying on cloned go/types scope pointers.
func (p *context) checkCompileMethods(pkg llssa.Package, typ types.Type) {
	nt := typ
retry:
	switch t := types.Unalias(nt).(type) {
	case *types.Named:
		if !hasTypeArgs(t) {
			if universe := p.immutableEmissionUniverse(); universe != nil {
				if _, packageNamed := universe.frozenPackageNamedType(t); packageNamed {
					p.compileSyntheticMethods(pkg, typ)
					return
				}
			}
			obj := t.Obj()
			// Legacy/report-only builds have no frozen provenance. Retain their
			// historical package-level test, while active builds above never depend
			// on scope pointer equality after typepatch.Clone/Merge.
			if obj != nil && obj.Pkg() != nil && obj.Parent() == obj.Pkg().Scope() {
				p.compileSyntheticMethods(pkg, typ)
				return
			}
		}
		p.compileMethods(pkg, typ)
	case *types.Struct:
		p.compileMethods(pkg, typ)
	case *types.Pointer:
		nt = t.Elem()
		goto retry
	}
}

// -----------------------------------------------------------------------------
