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

func EnableDebug(b bool) {
	enableDbg = b
}

func EnableDbgSyms(b bool) {
	enableDbgSyms = b
}

func EnableTrace(b bool) {
	enableCallTracing = b
}

// EnableExportRename enables or disables //export with different C symbol names.
// This is enabled when using -target flag for TinyGo compatibility.
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
	methodNilDerefChecks map[*ssa.UnOp]none
	patchOriginalInitIf  *ssa.If                     // exact synthetic guard whose successors are logically inverted
	unevaluatedSSA       map[ssa.Instruction]none    // values used only by unsafe.Sizeof/Alignof
	vargs                map[*ssa.Alloc][]llssa.Expr // varargs
	funcs                map[*ssa.Function]llssa.Function
	rawPlainFuncs        map[*ssa.Function]llssa.Function
	linkOnceFns          map[*ssa.Function]none
	stackDefers          map[*ssa.Function]bool
	anonDefers           map[*ssa.Function]bool
	paramDIVars          map[*types.Var]llssa.DIVar
	runtimeCallerFuncs   map[*ssa.Function]bool
	compilation          *Compilation
	emissionUniverse     *EmissionUniverse
	cacheRegistration    bool // cached archive: skip observers; emitted IR is transient
	pcLineSeq            uint64
	sourceParamBase      int // hidden physical parameters before source params
	currentCoro          *coroBodyContext
	rawPlainBody         bool               // compiling the legacy ABI variant of a managed function
	coroSourceBlocks     []llssa.BasicBlock // source SSA block index -> logical LLVM block
	coroRootFactories    []coroRootFactoryRegistration
	coroPlainDescriptors map[string]llssa.Expr

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

	cgoCalled  bool
	cgoArgs    []llssa.Expr
	cgoRet     llssa.Expr
	cgoErrno   llssa.Expr
	cgoErrnoTy types.Type
	cgoSymbols []string
	rewrites   map[string]string
	embedMap   goembed.VarMap
	embedInits []embedInit

	trackCallerFrames bool
	callerFrameMark   llssa.Expr

	staticGlobalInits map[*ssa.Global]llssa.Expr
	staticInitStores  map[*ssa.Store]none
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
	g := pkg.NewVar(name, typ, llssa.Background(vtype))
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
	return isCgoCfunc(name) || isCgoCmacro(name) || isCgoC2func(name)
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
	if entry.planned && entry.plan.Emission == coro.EmitCoroutine &&
		p.compilation != nil && p.compilation.CoroPlan != nil &&
		p.compilation.CoroPlan.HasRawPlainVariant(entry.function) {
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
		p.state == pkgHasPatch && p.compilation != nil && p.compilation.EnableCoroEntryResolution
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
	if entry.physical && entry.plan.Emission == coro.EmitCoroutine {
		// x/tools exposes a declared method receiver as fn.Params[0]. Normalize
		// the callable source ABI before adding the two coroutine-owned hidden
		// parameters so compileValue's sourceParamBase maps every SSA parameter
		// to the same physical position.
		sourceSig = coroPhysicalNormalizeSourceSignature(sig)
		abi := newCoroPhysicalABI(p, entry, sourceSig)
		physicalABI = &abi
		sig = abi.physicalSig
		hasCtx = false
	}
	// Always revisit an existing declaration when materializing its body.
	// NewFuncEx promotes that declaration to linkonce when required; declarations
	// themselves must retain external linkage because LLVM rejects a bodyless
	// linkonce global.
	fn = pkg.NewFuncEx(name, sig, llssa.Background(ftype), hasCtx, p.needsLinkOnce(f))
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
	if physicalABI != nil && entry.childAwait {
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
			pkg.EmitFuncInfo(fn.Name(), funcInfoDisplayName(pkgTypes, goName), pos.Filename, pos.Line, pos.Column)
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
		p.cgoArgs = nil
		p.cgoErrno = llssa.Nil
		if physicalABI != nil {
			fn.MakeBlocks(1) // dedicated coroutine ramp entry
		} else if isCgo {
			fn.MakeBlocks(1)
		} else {
			fn.MakeBlocks(nblk) // to set fn.HasBody() = true
		}
		if f.Recover != nil && physicalABI == nil { // set recover block
			fn.SetRecover(fn.Block(f.Recover.Index))
		}
		dbgEnabled := enableDbg && (f == nil || f.Origin() == nil)
		dbgSymsEnabled := enableDbgSyms && (f == nil || f.Origin() == nil)
		p.inits = append(p.inits, func() {
			oldFn, oldGoFn, oldMethodNilDerefChecks, oldPatchOriginalInitIf, oldUnevaluatedSSA, oldCallerFrameMark, oldRawPlainBody := p.fn, p.goFn, p.methodNilDerefChecks, p.patchOriginalInitIf, p.unevaluatedSSA, p.callerFrameMark, p.rawPlainBody
			p.fn = fn
			p.goFn = f
			p.patchOriginalInitIf = patchOriginalInitIf
			p.rawPlainBody = rawPlain
			p.callerFrameMark = llssa.Nil
			p.state = state // restore pkgState when compiling funcBody
			defer func() {
				p.fn, p.goFn, p.methodNilDerefChecks, p.patchOriginalInitIf, p.unevaluatedSSA, p.callerFrameMark, p.rawPlainBody = oldFn, oldGoFn, oldMethodNilDerefChecks, oldPatchOriginalInitIf, oldUnevaluatedSSA, oldCallerFrameMark, oldRawPlainBody
			}()
			p.phis = nil
			if dbgSymsEnabled {
				p.paramDIVars = make(map[*types.Var]llssa.DIVar)
			} else {
				p.paramDIVars = nil
			}
			dbgGoSSADump(f)
			dbgInstrln("==> FuncBody", name)
			b := fn.NewBuilder()
			if dbgEnabled {
				pos := p.goProg.Fset.Position(f.Pos())
				bodyPos := p.getFuncBodyPos(f)
				b.DebugFunction(fn, pos, bodyPos)
			}
			p.bvals = make(map[ssa.Value]llssa.Expr)
			p.methodNilDerefChecks = collectMethodNilDerefChecks(f)
			if p.emissionUniverse != nil {
				var frozen bool
				p.unevaluatedSSA, frozen = p.emissionUniverse.frozenUnsafeSizeAlignUnevaluatedSSA(f)
				if !frozen {
					panic(fmt.Sprintf("function %q has no frozen unsafe.Sizeof/Alignof lowering facts", f.String()))
				}
			} else {
				// Legacy one-package compilation has no whole-program inventory.
				p.unevaluatedSSA = collectUnsafeSizeAlignUnevaluatedSSA(f)
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

// funcInfoDisplayName normalizes a funcinfo metadata display name to gc's
// reporting conventions: the main package is "main" no matter what the
// module names it (frame filters in the wild match on the "main." prefix),
// and anonymous functions are pkg.fn.funcN (our linker symbols use $N).
// Linker symbols are not affected.
func funcInfoDisplayName(pkgTypes *types.Package, goName string) string {
	if pkgTypes != nil && pkgTypes.Name() == "main" {
		if path := llssa.PathOf(pkgTypes); path != "main" && strings.HasPrefix(goName, path+".") {
			goName = "main" + goName[len(path):]
		}
	}
	return normalizeRuntimeAnonFuncName(goName)
}

func hasNoInlineDirective(f *ssa.Function) bool {
	decl, _ := f.Syntax().(*ast.FuncDecl)
	if decl == nil || decl.Doc == nil {
		return false
	}
	for _, c := range decl.Doc.List {
		if c.Text == "//go:noinline" {
			return true
		}
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
	value := p.compileValue(b, v.X)
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
		pos := p.goProg.Fset.Position(param.Pos())
		v := p.compileValue(b, param)
		ty := param.Type()
		argNo := i + 1
		div := b.DIVarParam(p.fn, pos, param.Name(), p.type_(ty, llssa.InGo), argNo)
		if p.paramDIVars != nil {
			p.paramDIVars[variable] = div
		}
		b.DIParam(variable, v, div, p.fn, pos, p.sourceBlock(0))
	}
}

// sourceBlock maps a Go SSA basic-block index to the logical LLVM block used
// by the current lowering. Plain functions retain the historical one-to-one
// Function.Block mapping. A physical coroutine has a dedicated ramp and
// internal suspend blocks, so its source CFG uses an explicit stable map.
func (p *context) sourceBlock(index int) llssa.BasicBlock {
	if len(p.coroSourceBlocks) != 0 {
		if index < 0 || index >= len(p.coroSourceBlocks) {
			panic(fmt.Sprintf("source basic block index %d is outside coroutine map of length %d", index, len(p.coroSourceBlocks)))
		}
		return p.coroSourceBlocks[index]
	}
	return p.fn.Block(index)
}

func (p *context) compileBlock(b llssa.Builder, block *ssa.BasicBlock, n int, doModInit bool) llssa.BasicBlock {
	var last int
	var pyModInit bool
	var prog = p.prog
	var pkg = p.pkg
	var fn = p.fn
	var instrs = block.Instrs[n:]
	var ret = p.sourceBlock(block.Index)
	b.SetBlock(ret)
	if block.Index == 0 && p.shouldTrackCallerFrames() {
		p.pushCallerLocationFrame(b, block.Parent())
	}
	if block.Index == 0 && enableCallTracing && !strings.HasPrefix(fn.Name(), "github.com/goplus/llgo/runtime/internal/runtime.Print") {
		b.Printf("call " + fn.Name() + "\n\x00")
	}
	// place here to avoid wrong current-block
	if enableDbgSyms && block.Parent().Origin() == nil && block.Index == 0 {
		p.debugParams(b, block.Parent())
	}

	if doModInit {
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
	isCgoCfunc := isCgoCfunc(fnName)
	isCgoC2 := isCgoC2func(fnName)
	isCgoCmacro := isCgoCmacro(fnName)
	for i, instr := range instrs {
		if _, skip := p.unevaluatedSSA[instr]; skip {
			continue
		}
		if p.currentCoro != nil {
			if _, debug := instr.(*ssa.DebugRef); debug {
				p.compileInstr(b, instr)
				continue
			}
			role := coroFrameRetentionInstructionNone
			if p.currentCoro.frameRetention != nil {
				role = p.currentCoro.frameRetention.roles[instr]
			}
			criticalRole := coroCriticalCallNone
			criticalDepth := uint32(0)
			if p.currentCoro.critical != nil {
				var proven bool
				criticalDepth, proven = p.currentCoro.critical.beforeDepth[instr]
				if !proven {
					panic("coroutine critical proof has no instruction input depth")
				}
				if call, ok := instr.(*ssa.Call); ok {
					criticalRole = p.currentCoro.critical.roles[call]
				}
			}
			outerCriticalEnter := criticalRole == coroCriticalCallEnter && criticalDepth == 0
			switch role {
			case coroFrameRetentionInstructionPrepare:
				if p.currentCoro.frameRetaining {
					panic("nested coroutine frame-retention critical span")
				}
				// A retained frame pointer must never exist while an ordinary
				// preemption handoff can make this G independently runnable. Poll
				// immediately before the fail-stop prepare, then suppress budget
				// polls until the exact fail-stop retire has returned.
				if p.currentCoro.needsPreempt {
					p.currentCoro.pollAndSuspendForPreempt(b)
				}
				p.currentCoro.instructions = 0
				p.currentCoro.frameRetaining = true
			case coroFrameRetentionInstructionPark, coroFrameRetentionInstructionRetire:
				if !p.currentCoro.frameRetaining {
					panic("coroutine frame-retention park/retire outside its critical span")
				}
			default:
				if !p.currentCoro.frameRetaining && criticalDepth == 0 && !outerCriticalEnter {
					p.currentCoro.countInstructionAndMaybeYield(b)
				}
			}
			if !outerCriticalEnter {
				p.currentCoro.sourceBlockPollFresh = false
			}
		}
		if i == 1 && doModInit && p.state == pkgInPatch { // in patch package but no pkgFNoOldInit
			initFnNameOld := initFnNameOfHasPatch(p.fn.Name())
			if p.currentCoro != nil {
				p.compileCoroPatchInitAwait(b)
			} else {
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
			case *ssa.Call:
				if isCgoCmacro {
					p.cgoRet = p.compileValue(b, instr.Call.Args[0])
					p.cgoCalled = true
				} else {
					// call c function
					p.compileInstr(b, instr)
					p.cgoCalled = true
				}
			case *ssa.Return:
				// return cgo function result
				if isCgoCmacro {
					ty := p.type_(instr.Results[0].Type(), llssa.InGo)
					p.cgoRet.Type = p.prog.Pointer(ty)
					p.cgoRet = b.Load(p.cgoRet)
				} else {
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
		if p.currentCoro != nil && p.currentCoro.frameRetention != nil &&
			p.currentCoro.frameRetention.roles[instr] == coroFrameRetentionInstructionRetire {
			p.currentCoro.frameRetaining = false
			p.currentCoro.instructions = 0
		}
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
	refs := v.Referrers()
	if refs == nil || len(*refs) != 0 {
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
		b.Return(p.cgoRet)
		return
	}
	sig := p.fn.Type.RawType().(*types.Signature)
	if sig.Results().Len() != 2 {
		panic("cgo C2func should return (result, error)")
	}
	p.cgoC2Return(b, p.cgoRet, sig.Results().At(1).Type())
}

func (p *context) cgoC2Return(b llssa.Builder, ret llssa.Expr, errType types.Type) {
	errTy := p.type_(errType, llssa.InGo)
	nilSlot := b.AllocU(errTy)
	b.Store(nilSlot, p.prog.Zero(errTy))
	nilErr := b.Load(nilSlot)
	if p.cgoErrno.IsNil() {
		b.Return(ret, nilErr)
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
	b.Return(ret, errIface)
	b.SetBlockEx(okBlk, llssa.AtEnd, false)
	b.Return(ret, nilErr)
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
	refs := v.Referrers()
	if refs == nil || len(*refs) != 1 {
		return false
	}
	slice, ok := (*refs)[0].(*ssa.Slice)
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
	refs := *v.Referrers()
	n := len(refs)
	lastref := refs[n-1]
	if i, ok := lastref.(*ssa.Slice); ok {
		if refs = *i.Referrers(); len(refs) == 1 {
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

func (p *context) compileInstrOrValue(b llssa.Builder, iv instrOrValue, asValue bool) (ret llssa.Expr) {
	if asValue {
		if v, ok := p.bvals[iv]; ok {
			return v
		}
		log.Panicln("unreachable:", iv)
	}
	switch v := iv.(type) {
	case *ssa.Call:
		if value, handled := p.tryCompileCoroPatchInitRedirect(b, v); handled {
			ret = value
		} else if p.rawPlainBody {
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
					ret = p.call(b, llssa.Call, &v.Call)
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
				ret = p.call(b, llssa.Call, &v.Call)
			}
		} else if value, handled := p.tryCompileCoroManagedInterfaceDispatch(b, v); handled {
			ret = value
		} else if value, handled := p.tryCompileCoroInterfaceDispatchAwait(b, v); handled {
			ret = value
		} else if value, handled := p.tryCompileCoroManagedDispatchAwait(b, v); handled {
			ret = value
		} else if value, handled := p.tryCompileCoroPlainDispatchCall(b, v); handled {
			ret = value
		} else if value, handled := p.tryCompileCoroWorkerForeignCall(b, v); handled {
			ret = value
		} else if value, handled := p.tryCompileCoroStaticAwait(b, v); handled {
			ret = value
		} else {
			ret = p.call(b, llssa.Call, &v.Call)
		}
		if p.rangeFuncCallNeedsDeferDrain(&v.Call) {
			b.DeferStackDrain()
		}
	case *ssa.BinOp:
		if value, handled := p.tryCompileCoroInterfaceNilCompare(b, v); handled {
			ret = value
			break
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
		if v.Op == token.MUL {
			if _, ok := p.methodNilDerefChecks[v]; ok && !ssaValueProvenNonNilAt(v.X, v) {
				return p.compileCheckedDeref(b, v)
			}
			if refs := v.Referrers(); refs != nil && len(*refs) == 0 {
				if t := p.type_(v.Type(), llssa.InGo); t.RawType() != nil {
					if p.isLargeNonPointerValue(t) {
						x := p.compileValue(b, v.X)
						p.recordPanicLocation(b, v.Pos())
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
					p.recordPanicLocation(b, v.Pos())
					p.assertNilDerefBase(b, v.X)
					b.AssertNilDeref(x)
					return
				}
			}
			if refs := v.Referrers(); refs != nil && len(*refs) == 1 {
				if _, ok := (*refs)[0].(*ssa.MakeInterface); ok {
					if t := p.type_(v.Type(), llssa.InGo); t.RawType() != nil {
						if p.isLargeNonPointerValue(t) {
							// Skip the load: the MakeInterface handler below copies
							// from the original pointer and preserves the nil check.
							return
						}
					}
				}
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
		if v.Op != token.ARROW {
			p.recordPanicLocation(b, v.Pos())
		}
		guardedDeref := v.Op == token.MUL && p.coroDerefRequiresImplicitNilFault(v)
		if guardedDeref {
			x = p.compileCoroImplicitNilDerefGuard(b, v, x)
		} else if shouldAssertDirectNilDeref(v) && !ssaValueProvenNonNilAt(v.X, v) {
			b.AssertNilDeref(x)
		}
		if v.Op == token.ARROW {
			if p.currentCoro != nil && p.compilation != nil && p.compilation.EnableCoroChannel {
				ret = p.compileCoroChanRecv(b, v, x)
			} else {
				ret = b.Recv(x, v.CommaOk)
			}
		} else {
			if v.Op == token.MUL {
				if t := p.type_(v.Type(), llssa.InGo); t.RawType() != nil && p.prog.SizeOf(t) == 0 {
					if p.currentCoro != nil {
						// The explicit-status guard above owns the nullable case;
						// a proven non-nil source needs no memory access. Avoid
						// Builder.UnOp's legacy native-stack nil helper and
						// materialize the sole zero-sized value directly.
						ret = p.prog.Zero(t)
						break
					}
					p.assertNilDerefBase(b, v.X)
				}
				if isInterfaceCompareDeref(v) {
					p.assertNilDerefBase(b, v.X)
					b.AssertNilDeref(x)
				}
			}
			ret = b.UnOp(v.Op, x)
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
		if p.coroFieldAddrRequiresImplicitNilFault(v) {
			x = p.compileCoroImplicitNilFieldAddrGuard(b, v, x)
		} else if p.isAddressOfFieldAddr(v) && !ssaAddressValueProvenNonNilAt(v.X, v) {
			b.AssertNilDeref(x)
		}
		ret = b.FieldAddr(x, v.Field)
	case *ssa.Alloc:
		t := v.Type().(*types.Pointer)
		if p.checkVArgs(v, t) { // varargs: this maybe a varargs allocation
			return
		}
		if p.skipSyntheticMakeSliceAlloc(v) {
			return
		}
		if p.currentCoro != nil {
			if value, selected := p.currentCoro.terminalResultAllocs[v]; selected {
				if !v.Heap || v.Block() == nil || v.Block().Index != 0 {
					panic("coroutine terminal-result allocation lost its source-entry heap identity")
				}
				ret = value
				break
			}
		}
		elem := p.type_(t.Elem(), llssa.InGo)
		heap := v.Heap
		if bitcast, exact := coro.ProveSSAExactScalarBitcast(v.Parent()); exact && bitcast.Allocation == v {
			// The exact body stores the complete same-width scalar before its
			// single reinterpreted load, so zero initialization is both unnecessary
			// and would leave a misleading llvm.memset call in this call-free leaf.
			if p.currentCoro != nil {
				ret = p.coroFrameAlloca(elem)
			} else {
				ret = b.AllocaT(elem)
			}
			break
		}
		frameOwned := p.currentCoro != nil && !heap
		if heap && p.currentCoro != nil && p.currentCoro.frameRetention != nil {
			_, retained := p.currentCoro.frameRetention.allocations[v]
			heap = !retained
			frameOwned = retained
		}
		if frameOwned {
			ret = p.coroFrameAlloc(elem)
			break
		}
		ret = b.Alloc(elem, heap)
	case *ssa.IndexAddr:
		vx := v.X
		if _, ok := p.isVArgs(vx); ok { // varargs: this is a varargs index
			return
		}
		x := p.compileValue(b, vx)
		idx := p.compileValue(b, v.Index)
		p.recordPanicLocation(b, v.Pos())
		if p.frozenSafeFixedArrayIndex(v, v.X, v.Index) {
			if _, pointer := types.Unalias(p.patchType(v.X.Type())).Underlying().(*types.Pointer); pointer &&
				!emissionKnownNonNilArrayBase(v.X) && !ssaValueProvenNonNilAt(v.X, v) {
				// Bounds safety says nothing about the implicit *array
				// dereference. Keep its ordinary nil fault, routing it through
				// the explicit outcome only in a physical coroutine body.
				if p.currentCoro != nil && p.compilation != nil && p.compilation.EnableCoroExplicitStatusPanicABI {
					x = p.compileCoroImplicitNilAccessGuard(b, x)
				} else {
					b.AssertNilDeref(x)
				}
			}
			ret = b.IndexAddrUnchecked(x, idx)
		} else if p.currentCoro != nil && p.compilation != nil && p.compilation.EnableCoroExplicitStatusPanicABI {
			ret = p.compileCoroIndexAddrGuarded(b, v, x, idx)
		} else {
			ret = b.IndexAddr(x, idx)
		}
	case *ssa.Index:
		x := p.compileValue(b, v.X)
		idx := p.compileValue(b, v.Index)
		p.recordPanicLocation(b, v.Pos())
		takeArrayAddr := func() (addr llssa.Expr, zero bool) {
			switch n := v.X.(type) {
			case *ssa.Const:
				zero = true
			case *ssa.UnOp:
				addr = p.compileValue(b, n.X)
			}
			return
		}
		if p.frozenSafeFixedArrayIndex(v, v.X, v.Index) {
			switch types.Unalias(p.patchType(v.X.Type())).Underlying().(type) {
			case *types.Array:
				ret = b.IndexUnchecked(x, idx, takeArrayAddr)
			case *types.Pointer:
				if !emissionKnownNonNilArrayBase(v.X) && !ssaValueProvenNonNilAt(v.X, v) {
					if p.currentCoro != nil && p.compilation != nil && p.compilation.EnableCoroExplicitStatusPanicABI {
						x = p.compileCoroImplicitNilAccessGuard(b, x)
					} else {
						b.AssertNilDeref(x)
					}
				}
				ret = b.Load(b.IndexAddrUnchecked(x, idx))
			default:
				panic("safe fixed-array Index lost its frozen container shape")
			}
		} else if p.currentCoro != nil && p.compilation != nil && p.compilation.EnableCoroExplicitStatusPanicABI {
			ret = p.compileCoroIndexGuarded(b, v, x, idx, takeArrayAddr)
		} else {
			ret = b.Index(x, idx, takeArrayAddr)
		}
	case *ssa.Lookup:
		x := p.compileValue(b, v.X)
		idx := p.compileValue(b, v.Index)
		ret = b.Lookup(x, idx, v.CommaOk)
	case *ssa.Slice:
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
		p.recordPanicLocation(b, v.Pos())
		if p.currentCoro != nil && p.compilation != nil && p.compilation.EnableCoroExplicitStatusPanicABI {
			ret = p.compileCoroSliceGuarded(b, v, x, low, high, max)
		} else {
			ret = b.Slice(x, low, high, max)
		}
		ret.Type = p.type_(v.Type(), llssa.InGo)
	case *ssa.MakeInterface:
		if p.currentCoro != nil && coroSyntheticSelectNoCaseBox(v) {
			ret = p.prog.Nil(p.type_(v.Type(), llssa.InGo))
			break
		}
		if refs := *v.Referrers(); len(refs) == 1 {
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
						p.assertNilDerefBase(b, unop.X)
						ret = b.MakeInterfaceFromPtr(t, ptr)
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
		if !p.rawPlainBody {
			if value, handled := p.tryCompileCoroPlainDispatchClosure(b, v); handled {
				ret = value
				break
			}
		}
		var fn llssa.Expr
		if target, ok := v.Fn.(*ssa.Function); ok && p.compilation != nil && p.compilation.EnableCoroEntryResolution {
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
		ret = b.Next(typ, iter, v.IsString)
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
		if p.currentCoro != nil && p.compilation != nil && p.compilation.EnableCoroChannel {
			if v.Blocking {
				ret = p.compileCoroChanSelect(b, states)
			} else {
				ret = p.compileCoroChanTrySelect(b, states)
			}
		} else {
			ret = b.Select(states, v.Blocking)
		}
	case *ssa.SliceToArrayPointer:
		t := p.type_(v.Type(), llssa.InGo)
		x := p.compileValue(b, v.X)
		length, exact := coroSliceToArrayPointerLen(v, p.patchType)
		if exact && length == 0 {
			// Go deliberately preserves the slice data word here: a nil slice
			// converts to nil *[0]T, while an empty non-nil slice converts to a
			// non-nil pointer. There is no length fault for N==0.
			ret = b.SliceToArrayPointerUnchecked(x, t)
			break
		}
		p.recordPanicLocation(b, v.Pos())
		if p.currentCoro != nil && p.compilation != nil && p.compilation.EnableCoroExplicitStatusPanicABI {
			ret = p.compileCoroSliceToArrayPointer(b, v, x, t)
		} else {
			ret = b.SliceToArrayPointer(x, t)
		}
	default:
		panic(fmt.Sprintf("compileInstrAndValue: unknown instr - %T\n", iv))
	}
	p.bvals[iv] = ret
	return ret
}

func isInterfaceCompareDeref(v *ssa.UnOp) bool {
	if _, ok := types.Unalias(v.Type()).Underlying().(*types.Interface); !ok {
		return false
	}
	switch v.X.(type) {
	case *ssa.Alloc, *ssa.Extract, *ssa.FieldAddr, *ssa.FreeVar, *ssa.Global, *ssa.IndexAddr:
		return false
	}
	refs := v.Referrers()
	if refs == nil || len(*refs) != 1 {
		return false
	}
	bin, ok := (*refs)[0].(*ssa.BinOp)
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
	if iv, ok := instr.(instrOrValue); ok {
		p.compileInstrOrValue(b, iv, false)
		return
	}
	if enableDbg && instr.Parent().Origin() == nil {
		scope := p.getDebugLocScope(instr.Parent(), instr.Pos())
		if scope != nil {
			diScope := b.DIScope(p.fn, scope)
			pos := p.fset.Position(instr.Pos())
			b.DISetCurrentDebugLocation(diScope, pos)
		}
	}
	switch v := instr.(type) {
	case *ssa.Store:
		if _, ok := p.staticInitStores[v]; ok {
			return
		}
		if p.compilation != nil && p.compilation.CoroPlan != nil &&
			p.compilation.CoroPlan.ElidesConditionalManagedStore(v) {
			// Whole-program analysis proved this exact direct descriptor
			// publication has no live reader or other target consumer. Avoid
			// materializing a reference to the intentionally EmitNone target.
			return
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
		b.Store(ptr, val)
	case *ssa.Jump:
		jmpb := p.jumpTo(v)
		b.Jump(jmpb)
	case *ssa.Return:
		var results []llssa.Expr
		if n := len(v.Results); n > 0 {
			results = make([]llssa.Expr, n)
			for i, r := range v.Results {
				results[i] = p.compileValue(b, r)
			}
		}
		if p.returnNeedsImplicitRunDefers(v) {
			p.recordPanicLocation(b, v.Pos())
			b.RunDefers()
		}
		if p.shouldTrackCallerFrames() {
			p.popCallerLocationFrame(b)
		}
		if p.currentCoro != nil {
			if p.currentCoro.completion == nil {
				panic("coroutine return has no completion block")
			}
			p.storeCoroLeafResult(b, p.currentCoro.abi, p.currentCoro.resultSlot, results)
			b.Jump(p.currentCoro.completion)
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
		if p.currentCoro != nil && p.currentCoro.cleanup != nil {
			p.currentCoro.cleanup.register(p, b, v)
			return
		}
		if v.DeferStack != nil {
			p.callDeferStack(b, p.blkInfos[v.Block().Index].Kind, &v.Call, v.DeferStack, v.Parent())
			return
		}
		p.call(b, p.blkInfos[v.Block().Index].Kind, &v.Call)
	case *ssa.Go:
		if p.tryCompileCoroClosedStaticSpawn(b, v) {
			return
		}
		p.call(b, llssa.Go, &v.Call)
	case *ssa.RunDefers:
		if p.currentCoro != nil && p.currentCoro.cleanup != nil {
			p.currentCoro.cleanup.runDefers(b, v)
			return
		}
		p.recordPanicLocation(b, v.Pos())
		b.RunDefers()
	case *ssa.Panic:
		if p.currentCoro != nil && coroSyntheticSelectNoCasePanic(v) {
			if p.currentCoro.unsupportedRunDecision == nil {
				panic("coroutine select invariant panic requires a fail-closed trap block")
			}
			b.Jump(p.currentCoro.unsupportedRunDecision)
			return
		}
		if p.tryCompileCoroExplicitStatusPanic(b, v) {
			return
		}
		arg := p.compileValue(b, v.X)
		p.recordPanicLocation(b, v.Pos())
		b.Panic(arg)
	case *ssa.Send:
		ch := p.compileValue(b, v.Chan)
		x := p.compileValue(b, v.X)
		p.recordPanicLocation(b, v.Pos())
		if p.currentCoro != nil && p.compilation != nil && p.compilation.EnableCoroChannel {
			p.compileCoroChanSend(b, ch, x)
		} else {
			b.Send(ch, x)
		}
	case *ssa.DebugRef:
		if enableDbgSyms && v.Parent().Origin() == nil {
			p.debugRef(b, v)
		}
	default:
		panic(fmt.Sprintf("compileInstr: unknown instr - %T\n", instr))
	}
}

func (p *context) getLocalVariable(b llssa.Builder, fn *ssa.Function, v *types.Var) llssa.DIVar {
	if p.paramDIVars != nil {
		if div, ok := p.paramDIVars[v]; ok {
			return div
		}
	}
	pos := p.fset.Position(v.Pos())
	t := p.type_(v.Type(), llssa.InGo)
	scope := b.DIScope(p.fn, v.Parent())
	return b.DIVarAuto(scope, pos, v.Name(), t)
}

func (p *context) compileFunction(v *ssa.Function) (goFn llssa.Function, pyFn llssa.PyObjRef, kind int) {
	if p.rawPlainBody {
		return p.compileRawPlainFunction(v)
	}
	return p.compileManagedFunction(v)
}

func (p *context) compileManagedFunction(v *ssa.Function) (goFn llssa.Function, pyFn llssa.PyObjRef, kind int) {
	if p.compilation != nil && p.compilation.EnableCoroEntryResolution &&
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
	if v.Pkg == p.goPkg || v.Pkg == nil {
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
		if v.Pkg == p.goPkg || v.Pkg == nil {
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
	if v.Pkg == p.goPkg || v.Pkg == nil {
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
				return b.Param(idx + p.sourceParamBase)
			}
		}
	case *ssa.Function:
		if !p.rawPlainBody {
			if value, handled := p.tryCompileCoroPlainDispatchFunctionValue(b, v); handled {
				return value
			}
		}
		return p.compileRawFunctionValue(v)
	case *ssa.Global:
		varName := v.Name()
		val := p.varOf(b, v)
		if isCgoVar(varName) {
			p.cgoSymbols = append(p.cgoSymbols, val.Name())
		}
		if enableDbgSyms {
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
				if p.currentCoro != nil && len(fn.FreeVars) != 0 {
					// Physical captured coroutine entries expose their typed context
					// explicitly at (g,out,ctx,...). Do not use Function.FreeVar:
					// that legacy helper hard-codes implicit ctx at parameter zero,
					// which is the G word in the coroutine ABI. Load per use so the
					// value is dominated in every resumed block after CoroSplit.
					ctx := b.Load(p.fn.PhysicalParam(2))
					return b.Field(ctx, idx)
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
func NewPackageExWithEmbed(prog llssa.Program, ct *CallerTracking, patches Patches, rewrites map[string]string, pkg *ssa.Package, files []*ast.File, embedMap goembed.VarMap) (ret llssa.Package, externs []string, err error) {
	return newPackageEx(prog, ct, patches, rewrites, pkg, files, &embedMap, PackageOptions{})
}

// NewPackageExWithEmbedOptions compiles a package with compilation-scoped and
// per-package inputs. Existing one-shot entry points use zero PackageOptions.
func NewPackageExWithEmbedOptions(prog llssa.Program, ct *CallerTracking, patches Patches, rewrites map[string]string, pkg *ssa.Package, files []*ast.File, embedMap goembed.VarMap, opts PackageOptions) (ret llssa.Package, externs []string, err error) {
	return newPackageEx(prog, ct, patches, rewrites, pkg, files, &embedMap, opts)
}

func newPackageEx(prog llssa.Program, ct *CallerTracking, patches Patches, rewrites map[string]string, pkg *ssa.Package, files []*ast.File, embedMap *goembed.VarMap, opts PackageOptions) (ret llssa.Package, externs []string, err error) {
	var prepared *preparedEmissionPackage
	if opts.Compilation != nil && (opts.Compilation.EnableCoroEntryResolution || opts.Compilation.EnableCoroPhysicalABI) {
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
	if pkgPath == llssa.PkgRuntime {
		prog.SetRuntime(pkgTypes)
	}
	ret = prog.NewPackage(pkgName, pkgPath)
	if enableDbg {
		ret.InitDebug(pkgName, pkgPath, pkgProg.Fset)
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
	}
	if opts.Compilation != nil && opts.Compilation.EnableCoroEntryResolution {
		ctx.emissionUniverse = opts.Compilation.EmissionUniverse
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
	if opts.Compilation != nil && opts.Compilation.EnableCoroEntryResolution {
		ret.SetResolveMethodLinkname(ctx.resolveMethodLinkname)
		ret.SetResolveMethodToken(ctx.resolveMethodToken)
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
	for len(ctx.inits) > 0 {
		inits := ctx.inits
		ctx.inits = nil
		for _, ini := range inits {
			ini()
		}
	}
	if fn := ctx.initAfter; fn != nil {
		ctx.initAfter = nil
		fn()
	}
	ctx.emitCoroRootPackageAnchor(ret)
	ret.MaterializePreserveSyms()
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
	if p.compilation != nil && p.compilation.EnableCoroEntryResolution && p.compilation.EmissionUniverse != nil {
		canonical, ok := p.compilation.EmissionUniverse.Resolve(v)
		if !ok {
			panic(fmt.Errorf("coroutine entry resolution: function value %q is absent from the prepared emission universe", v.Name()))
		}
		v = canonical
	}
	if _, _, ftype := p.funcName(v); ftype == llgoInstr {
		if p.compilation != nil && p.compilation.EnableCoroEntryResolution && p.compilation.EmissionUniverse != nil {
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
	if universe := p.emissionUniverseForPatch(); universe != nil {
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
		if pkg := o.Pkg(); typepatch.IsPatched(pkg) {
			if patch, ok := p.patches[pkg.Path()]; ok {
				if obj := patch.Types.Scope().Lookup(o.Name()); obj != nil {
					raw := p.prog.Type(instantiate(obj.Type(), typ), llssa.InGo).RawType()
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

func (p *context) emissionUniverseForPatch() *EmissionUniverse {
	if p == nil {
		return nil
	}
	if p.emissionUniverse != nil {
		return p.emissionUniverse
	}
	if p.compilation != nil && p.compilation.EnableCoroEntryResolution {
		return p.compilation.EmissionUniverse
	}
	return nil
}

func (p *context) patchLocalGenericNamed(t *types.Named) (*types.Named, bool) {
	if p.goFn == nil || isPatchedLocalGenericName(t.Obj().Name()) {
		return nil, false
	}
	universe := p.emissionUniverseForPatch()
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
	if universe := p.emissionUniverseForPatch(); universe != nil {
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
	if name, managed := p.resolveManagedInterfaceRawMethodSymbol(method, sig); managed {
		return name
	}
	fn := p.resolveInterfaceMethodSSA(method, sig)
	return p.mustFunctionSymbol(fn).name
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
			if universe := p.emissionUniverseForPatch(); universe != nil {
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
