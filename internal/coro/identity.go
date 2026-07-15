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

package coro

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// FunctionIDSchema is the schema of StableFunctionID's canonical text. It is
// intentionally versioned independently from the experimental plan summary.
const FunctionIDSchema = "llgo.function.v0"

const (
	defaultCoroABI      = "analysis-v0"
	defaultSchedulerABI = "analysis-v0"
)

// FunctionIDConfig supplies compilation-wide identity inputs.
//
// ArchiveReady must remain false unless the plan will cross an archive or
// compilation boundary. When it is true, both ResolveLinkIdentity and
// CanonicalPackageKey are required so the caller can account for linkname,
// patches, test variants, and command-line packages. The default identity is
// deterministic for one unpatched in-memory SSA program but is deliberately
// not an archive ABI or final CoroPlanDigest key.
type FunctionIDConfig struct {
	CoroABI      string
	SchedulerABI string
	ArchiveReady bool

	ResolveLinkIdentity func(*ssa.Function) (string, error)
	CanonicalPackageKey func(*types.Package) (string, error)

	// ResolveLocalTypeOwner supplies provenance for an x/tools-substituted
	// local named type when the generic function instance that created it is
	// no longer reachable from the SSA program's package roots. Tools that
	// retain isolated function instances must record this relationship while
	// constructing SSA. Returning ok=false requests automatic recovery.
	ResolveLocalTypeOwner func(local *types.Named) (owner *ssa.Function, ok bool, err error)

	// ResolveSynthetic provides a structural key for a synthetic function not
	// covered by the x/tools forms known to this schema. Returning ok=false
	// rejects the function instead of depending on x/tools diagnostic text.
	ResolveSynthetic func(*ssa.Function) (key string, ok bool, err error)
}

func (c FunctionIDConfig) normalized() (FunctionIDConfig, error) {
	if c.ArchiveReady && c.CoroABI == "" {
		return FunctionIDConfig{}, fmt.Errorf("coro: archive-ready FunctionID requires explicit coroutine ABI")
	}
	if c.ArchiveReady && c.SchedulerABI == "" {
		return FunctionIDConfig{}, fmt.Errorf("coro: archive-ready FunctionID requires explicit scheduler ABI")
	}
	if c.CoroABI == "" {
		c.CoroABI = defaultCoroABI
	}
	if c.SchedulerABI == "" {
		c.SchedulerABI = defaultSchedulerABI
	}
	if !utf8.ValidString(c.CoroABI) {
		return FunctionIDConfig{}, fmt.Errorf("coro: coroutine ABI is not valid UTF-8")
	}
	if !utf8.ValidString(c.SchedulerABI) {
		return FunctionIDConfig{}, fmt.Errorf("coro: scheduler ABI is not valid UTF-8")
	}
	if c.ArchiveReady && c.ResolveLinkIdentity == nil {
		return FunctionIDConfig{}, fmt.Errorf("coro: archive-ready FunctionID requires final link identity resolver")
	}
	if c.ArchiveReady && c.CanonicalPackageKey == nil {
		return FunctionIDConfig{}, fmt.Errorf("coro: archive-ready FunctionID requires canonical package key resolver")
	}
	return c, nil
}

// StableFunctionID constructs a deterministic, structurally framed identity
// for one SSA function. It never uses Function.String, RelString, source paths,
// token.Pos, or the human-readable Synthetic description as identity data.
// Recovering provenance for a local named type freshly substituted by x/tools
// may enumerate the program and materialize lazy method wrappers.
func StableFunctionID(fn *ssa.Function, config FunctionIDConfig) (FunctionID, error) {
	if fn == nil {
		return "", fmt.Errorf("coro: cannot identify nil SSA function")
	}
	config, err := config.normalized()
	if err != nil {
		return "", err
	}
	builder := functionIDBuilder{config: config}
	return builder.stableFunctionID(fn)
}

func (b *functionIDBuilder) stableFunctionID(fn *ssa.Function) (FunctionID, error) {
	if fn == nil {
		return "", fmt.Errorf("coro: cannot identify nil SSA function")
	}
	config := b.config
	key, err := b.functionKey(fn)
	if err != nil {
		return "", err
	}
	linkIdentity := "report-only"
	if config.ResolveLinkIdentity != nil {
		linkIdentity, err = config.ResolveLinkIdentity(fn)
		if err != nil {
			return "", fmt.Errorf("coro: resolve final link identity for %q: %w", fn.Name(), err)
		}
		if linkIdentity == "" {
			return "", fmt.Errorf("coro: empty final link identity for %q", fn.Name())
		}
		if !utf8.ValidString(linkIdentity) {
			return "", fmt.Errorf("coro: final link identity for %q is not valid UTF-8", fn.Name())
		}
	}

	var text strings.Builder
	text.WriteString(FunctionIDSchema)
	text.WriteByte(';')
	appendIdentityField(&text, "coro", config.CoroABI)
	appendIdentityField(&text, "scheduler", config.SchedulerABI)
	appendIdentityField(&text, "link", linkIdentity)
	appendIdentityField(&text, "function", key)
	sum := sha256.Sum256([]byte(text.String()))
	id := FunctionID(FunctionIDSchema + ":" + hex.EncodeToString(sum[:]))
	if err := id.validate(); err != nil {
		return "", err
	}
	return id, nil
}

type functionIDBuilder struct {
	config FunctionIDConfig
	prog   *ssa.Program
	active map[*ssa.Function]bool
	cache  map[*ssa.Function]string

	typeActive map[types.Type]bool
	typeCache  map[types.Type]string

	localTypeOwnersReady bool
	localTypeOwners      map[*types.Named]*ssa.Function
	localTypeOwnerSpans  map[*types.Named]int64
	localTypeAmbiguous   map[*types.Named]bool
	localTypeCandidates  []*ssa.Function
}

func (b *functionIDBuilder) functionKey(fn *ssa.Function) (string, error) {
	if fn == nil {
		return "", fmt.Errorf("coro: cannot identify nil SSA function")
	}
	if b.prog == nil {
		b.prog = fn.Prog
	} else if fn.Prog != b.prog {
		return "", fmt.Errorf("coro: SSA function %q belongs to another program", fn.Name())
	}
	if b.cache == nil {
		b.cache = make(map[*ssa.Function]string)
		b.active = make(map[*ssa.Function]bool)
	}
	if key, ok := b.cache[fn]; ok {
		return key, nil
	}
	if b.active[fn] {
		return "", fmt.Errorf("coro: cyclic SSA function identity at %q", fn.Name())
	}
	b.active[fn] = true
	defer delete(b.active, fn)

	key, err := b.uncachedFunctionKey(fn)
	if err != nil {
		return "", err
	}
	b.cache[fn] = key
	return key, nil
}

func (b *functionIDBuilder) uncachedFunctionKey(fn *ssa.Function) (string, error) {
	if origin := fn.Origin(); origin != nil {
		originKey, err := b.functionKey(origin)
		if err != nil {
			return "", err
		}
		fields := []identityPair{{"origin", identityKeyDigest(originKey)}}
		for i, arg := range fn.TypeArgs() {
			key, err := b.typeKey(arg)
			if err != nil {
				return "", fmt.Errorf("coro: type argument %d of %q: %w", i, fn.Name(), err)
			}
			fields = append(fields, identityPair{"arg", key})
		}
		return identityNode("instance", fields...), nil
	}

	if parent := fn.Parent(); parent != nil {
		kind := ""
		switch {
		case fn.Synthetic == "":
			kind = "closure"
		case isRangeYield(fn):
			kind = "range-yield"
		default:
			return b.customSyntheticKey(fn)
		}
		parentKey, err := b.functionKey(parent)
		if err != nil {
			return "", err
		}
		ordinal := -1
		for i, child := range parent.AnonFuncs {
			if child == fn {
				ordinal = i
				break
			}
		}
		if ordinal < 0 {
			return "", fmt.Errorf("coro: nested function %q is absent from parent %q", fn.Name(), parent.Name())
		}
		return identityNode("child",
			identityPair{"parent", identityKeyDigest(parentKey)},
			identityPair{"ordinal", strconv.Itoa(ordinal)},
			identityPair{"kind", kind},
		), nil
	}

	if fn.Name() == "init" && fn.Synthetic == "package initializer" && fn.Pkg != nil {
		pkgKey, err := b.packageKey(fn.Pkg.Pkg)
		if err != nil {
			return "", err
		}
		return identityNode("package-init", identityPair{"package", pkgKey}), nil
	}

	obj, _ := fn.Object().(*types.Func)
	if obj != nil && obj.Type().(*types.Signature).Recv() != nil {
		declared, err := b.declaredMethodKey(obj)
		if err != nil {
			return "", err
		}
		switch {
		case strings.HasSuffix(fn.Name(), "$bound") && len(fn.FreeVars) == 1:
			receiver, err := b.typeKey(fn.FreeVars[0].Type())
			if err != nil {
				return "", err
			}
			return identityNode("bound-method",
				identityPair{"receiver", receiver},
				identityPair{"method", declared},
			), nil
		case strings.HasSuffix(fn.Name(), "$thunk") && fn.Signature.Recv() == nil && fn.Signature.Params().Len() > 0:
			receiver, err := b.typeKey(fn.Signature.Params().At(0).Type())
			if err != nil {
				return "", err
			}
			return identityNode("method-thunk",
				identityPair{"receiver", receiver},
				identityPair{"method", declared},
			), nil
		case strings.HasPrefix(fn.Synthetic, "wrapper for ") && fn.Signature.Recv() != nil:
			receiver, err := b.typeKey(fn.Signature.Recv().Type())
			if err != nil {
				return "", err
			}
			return identityNode("method-wrapper",
				identityPair{"receiver", receiver},
				identityPair{"method", declared},
			), nil
		case fn.Synthetic == "", fn.Synthetic == "from type information", fn.Synthetic == "from type information (on demand)":
			return declared, nil
		}
	}

	if obj != nil && obj.Type().(*types.Signature).Recv() == nil {
		switch fn.Synthetic {
		case "", "from type information", "from type information (on demand)":
			pkgKey, err := b.packageKey(obj.Pkg())
			if err != nil {
				return "", err
			}
			return identityNode("function",
				identityPair{"package", pkgKey},
				identityPair{"name", fn.Name()},
			), nil
		}
	}

	return b.customSyntheticKey(fn)
}

func (b *functionIDBuilder) customSyntheticKey(fn *ssa.Function) (string, error) {
	if b.config.ResolveSynthetic != nil {
		key, ok, err := b.config.ResolveSynthetic(fn)
		if err != nil {
			return "", fmt.Errorf("coro: resolve synthetic %q: %w", fn.Name(), err)
		}
		if ok {
			if key == "" || !utf8.ValidString(key) {
				return "", fmt.Errorf("coro: invalid custom synthetic key for %q", fn.Name())
			}
			return identityNode("custom-synthetic", identityPair{"key", key}), nil
		}
	}
	return "", fmt.Errorf("coro: unsupported synthetic function %q (%s)", fn.Name(), syntheticKind(fn))
}

func isRangeYield(fn *ssa.Function) bool {
	_, ok := fn.Syntax().(*ast.RangeStmt)
	return ok
}

func (b *functionIDBuilder) declaredMethodKey(obj *types.Func) (string, error) {
	sig, ok := obj.Type().(*types.Signature)
	if !ok || sig.Recv() == nil {
		return "", fmt.Errorf("coro: %q is not a declared method", obj.Name())
	}
	pkgKey, err := b.packageKey(obj.Pkg())
	if err != nil {
		return "", err
	}
	receiver, err := b.declaredReceiverKey(sig.Recv().Type())
	if err != nil {
		return "", err
	}
	methodID, err := b.objectID(obj)
	if err != nil {
		return "", err
	}
	return identityNode("method",
		identityPair{"package", pkgKey},
		identityPair{"id", methodID},
		identityPair{"receiver", receiver},
	), nil
}

// declaredReceiverKey identifies the declaration that owns a method without
// encoding the receiver's instantiated arguments. Receiver type parameters are
// alpha-bound by their position in the named type declaration. In particular,
// gcimporter recreates those parameters without a lexical scope, so treating
// their TypeName objects as ordinary local objects would make source and export
// data produce different identities (or reject the imported method entirely).
// Concrete receiver arguments remain part of an SSA instance or wrapper key.
func (b *functionIDBuilder) declaredReceiverKey(receiver types.Type) (string, error) {
	pointer := false
	if indirect, ok := types.Unalias(receiver).(*types.Pointer); ok {
		pointer = true
		receiver = indirect.Elem()
	}
	named, ok := types.Unalias(receiver).(*types.Named)
	if !ok {
		return "", fmt.Errorf("coro: declared method receiver has unsupported type %T", receiver)
	}
	object, err := b.objectKey(named.Obj())
	if err != nil {
		return "", err
	}
	return identityNode("declared-receiver",
		identityPair{"object", object},
		identityPair{"pointer", strconv.FormatBool(pointer)},
	), nil
}

func (b *functionIDBuilder) packageKey(pkg *types.Package) (string, error) {
	if pkg == nil {
		return "", nil
	}
	key := pkg.Path()
	var err error
	if b.config.CanonicalPackageKey != nil {
		key, err = b.config.CanonicalPackageKey(pkg)
		if err != nil {
			return "", fmt.Errorf("coro: canonical package key for %q: %w", pkg.Path(), err)
		}
	}
	if key == "" {
		return "", fmt.Errorf("coro: empty canonical package key for %q", pkg.Path())
	}
	if !utf8.ValidString(key) {
		return "", fmt.Errorf("coro: canonical package key for %q is not valid UTF-8", pkg.Path())
	}
	return key, nil
}

func (b *functionIDBuilder) typeKey(typ types.Type) (string, error) {
	if typ == nil {
		return identityNode("nil-type"), nil
	}
	if b.typeCache == nil {
		b.typeCache = make(map[types.Type]string)
		b.typeActive = make(map[types.Type]bool)
	}
	if key, ok := b.typeCache[typ]; ok {
		return key, nil
	}
	if b.typeActive[typ] {
		return "", fmt.Errorf("coro: cyclic anonymous identity type %T", typ)
	}
	b.typeActive[typ] = true
	defer delete(b.typeActive, typ)
	key, err := b.uncachedTypeKey(typ)
	if err != nil {
		return "", err
	}
	b.typeCache[typ] = key
	return key, nil
}

func (b *functionIDBuilder) uncachedTypeKey(typ types.Type) (string, error) {
	switch typ := types.Unalias(typ).(type) {
	case *types.Basic:
		return identityNode("basic", identityPair{"kind", strconv.Itoa(int(typ.Kind()))}), nil
	case *types.Pointer:
		return b.unaryTypeKey("pointer", typ.Elem())
	case *types.Slice:
		return b.unaryTypeKey("slice", typ.Elem())
	case *types.Array:
		elem, err := b.typeKey(typ.Elem())
		if err != nil {
			return "", err
		}
		return identityNode("array", identityPair{"length", strconv.FormatInt(typ.Len(), 10)}, identityPair{"element", elem}), nil
	case *types.Map:
		key, err := b.typeKey(typ.Key())
		if err != nil {
			return "", err
		}
		elem, err := b.typeKey(typ.Elem())
		if err != nil {
			return "", err
		}
		return identityNode("map", identityPair{"key", key}, identityPair{"element", elem}), nil
	case *types.Chan:
		elem, err := b.typeKey(typ.Elem())
		if err != nil {
			return "", err
		}
		return identityNode("chan", identityPair{"direction", strconv.Itoa(int(typ.Dir()))}, identityPair{"element", elem}), nil
	case *types.Named:
		return b.namedTypeKey(typ)
	case *types.Signature:
		return b.signatureKey(typ)
	case *types.Tuple:
		return b.tupleKey(typ)
	case *types.Struct:
		fields := make([]identityPair, 0, typ.NumFields()*4)
		for i := 0; i < typ.NumFields(); i++ {
			field := typ.Field(i)
			fieldID, err := b.objectID(field)
			if err != nil {
				return "", err
			}
			fieldType, err := b.typeKey(field.Type())
			if err != nil {
				return "", err
			}
			fields = append(fields,
				identityPair{"field-id", fieldID},
				identityPair{"field-type", fieldType},
				identityPair{"field-tag", typ.Tag(i)},
				identityPair{"field-embedded", strconv.FormatBool(field.Embedded())},
			)
		}
		return identityNode("struct", fields...), nil
	case *types.Interface:
		typ.Complete()
		if !typ.IsMethodSet() {
			return "", fmt.Errorf("coro: constraint interface identity is not supported in v0")
		}
		fields := make([]identityPair, 0, typ.NumMethods())
		for i := 0; i < typ.NumMethods(); i++ {
			method := typ.Method(i)
			methodID, err := b.objectID(method)
			if err != nil {
				return "", err
			}
			sig, err := b.typeKey(method.Type())
			if err != nil {
				return "", err
			}
			fields = append(fields, identityPair{"method", identityNode("interface-method", identityPair{"id", methodID}, identityPair{"signature", sig})})
		}
		return identityNode("interface", fields...), nil
	case *types.TypeParam:
		obj := typ.Obj()
		object, err := b.objectKey(obj)
		if err != nil {
			return "", err
		}
		return identityNode("type-param",
			identityPair{"object", object},
			identityPair{"index", strconv.Itoa(typ.Index())},
		), nil
	case *types.Union:
		fields := make([]identityPair, 0, typ.Len())
		for i := 0; i < typ.Len(); i++ {
			term := typ.Term(i)
			termType, err := b.typeKey(term.Type())
			if err != nil {
				return "", err
			}
			fields = append(fields, identityPair{"term", identityNode("union-term", identityPair{"tilde", strconv.FormatBool(term.Tilde())}, identityPair{"type", termType})})
		}
		sort.Slice(fields, func(i, j int) bool { return fields[i].value < fields[j].value })
		return identityNode("union", fields...), nil
	default:
		return "", fmt.Errorf("coro: unsupported identity type %T", typ)
	}
}

func (b *functionIDBuilder) unaryTypeKey(kind string, elemType types.Type) (string, error) {
	elem, err := b.typeKey(elemType)
	if err != nil {
		return "", err
	}
	return identityNode(kind, identityPair{"element", elem}), nil
}

func (b *functionIDBuilder) namedTypeKey(typ *types.Named) (string, error) {
	obj := typ.Obj()
	var fields []identityPair
	if obj != nil && obj.Pkg() != nil && obj.Parent() == nil {
		declaration, owner, err := b.instantiatedLocalType(typ)
		if err != nil {
			return "", err
		}
		object, err := b.objectKey(declaration)
		if err != nil {
			return "", err
		}
		ownerKey, err := b.functionKey(owner)
		if err != nil {
			return "", err
		}
		fields = []identityPair{
			{"object", object},
			{"owner-instance", identityKeyDigest(ownerKey)},
		}
	} else {
		object, err := b.objectKey(obj)
		if err != nil {
			return "", err
		}
		fields = []identityPair{{"object", object}}
	}
	if args := typ.TypeArgs(); args != nil {
		for i := 0; i < args.Len(); i++ {
			arg, err := b.typeKey(args.At(i))
			if err != nil {
				return "", err
			}
			fields = append(fields, identityPair{"arg", arg})
		}
	}
	return identityNode("named", fields...), nil
}

// instantiatedLocalType recovers the lexical declaration and the generic SSA
// function instance that owns an x/tools-substituted local named type.
//
// x/tools deliberately creates a fresh *types.Named for each instantiation of
// a generic function containing a local type, but the fresh TypeName is not
// inserted into a go/types Scope and has no public Origin link. Its source
// position is used only to recover the original declaration; the emitted key
// contains the checkout-independent lexical scope path and owner function key,
// never the token position itself.
func (b *functionIDBuilder) instantiatedLocalType(typ *types.Named) (*types.TypeName, *ssa.Function, error) {
	obj := typ.Obj()
	if obj == nil || obj.Pkg() == nil || obj.Parent() != nil {
		return nil, nil, fmt.Errorf("coro: named type %v is not an instantiated local type", typ)
	}
	declaration, err := lexicalTypeDeclaration(obj)
	if err != nil {
		return nil, nil, err
	}
	if b.prog == nil {
		return nil, nil, fmt.Errorf("coro: instantiated local type %q has no SSA program", obj.Name())
	}
	if b.config.ResolveLocalTypeOwner != nil {
		owner, ok, err := b.config.ResolveLocalTypeOwner(typ)
		if err != nil {
			return nil, nil, fmt.Errorf("coro: resolve owner of instantiated local type %q: %w", obj.Name(), err)
		}
		if ok {
			if err := b.validateLocalTypeOwner(typ, owner); err != nil {
				return nil, nil, err
			}
			return declaration, owner, nil
		}
	}
	b.prepareLocalTypeOwners()
	if b.localTypeAmbiguous[typ] {
		return nil, nil, fmt.Errorf("coro: instantiated local type %q has ambiguous SSA owners", obj.Name())
	}
	owner := b.localTypeOwners[typ]
	if owner == nil {
		return nil, nil, fmt.Errorf("coro: cannot find SSA owner of instantiated local type %q", obj.Name())
	}
	return declaration, owner, nil
}

func (b *functionIDBuilder) validateLocalTypeOwner(local *types.Named, owner *ssa.Function) error {
	if owner == nil {
		return fmt.Errorf("coro: resolver returned nil owner for instantiated local type %q", local.Obj().Name())
	}
	if owner.Prog != b.prog {
		return fmt.Errorf("coro: owner of instantiated local type %q belongs to another SSA program", local.Obj().Name())
	}
	if syntax := owner.Syntax(); syntax == nil || local.Obj().Pos() < syntax.Pos() || local.Obj().Pos() >= syntax.End() {
		return fmt.Errorf("coro: owner of instantiated local type %q does not contain its declaration", local.Obj().Name())
	}
	if functionTypeArgsContain(owner, local) {
		return fmt.Errorf("coro: owner of instantiated local type %q receives that type as an argument", local.Obj().Name())
	}
	return nil
}

func lexicalTypeDeclaration(obj *types.TypeName) (*types.TypeName, error) {
	if obj == nil || obj.Pkg() == nil {
		return nil, fmt.Errorf("coro: instantiated local type has no package")
	}
	if obj.Pos() == token.NoPos {
		return nil, fmt.Errorf("coro: instantiated local type %q has no source declaration", obj.Name())
	}
	var matches []*types.TypeName
	var visit func(*types.Scope)
	visit = func(scope *types.Scope) {
		for _, name := range scope.Names() {
			candidate, ok := scope.Lookup(name).(*types.TypeName)
			if ok && candidate.Name() == obj.Name() && candidate.Pos() == obj.Pos() && candidate.Parent() != nil {
				matches = append(matches, candidate)
			}
		}
		for i := 0; i < scope.NumChildren(); i++ {
			visit(scope.Child(i))
		}
	}
	visit(obj.Pkg().Scope())
	if len(matches) != 1 {
		return nil, fmt.Errorf("coro: instantiated local type %q matched %d lexical declarations", obj.Name(), len(matches))
	}
	return matches[0], nil
}

func (b *functionIDBuilder) prepareLocalTypeOwners() {
	if b.localTypeOwnersReady {
		return
	}
	b.localTypeOwnersReady = true
	b.localTypeOwners = make(map[*types.Named]*ssa.Function)
	b.localTypeOwnerSpans = make(map[*types.Named]int64)
	b.localTypeAmbiguous = make(map[*types.Named]bool)
	if b.prog == nil {
		return
	}
	functions := b.localTypeCandidates
	if functions == nil {
		functions = make([]*ssa.Function, 0)
		for fn := range ssautil.AllFunctions(b.prog) {
			functions = append(functions, fn)
		}
	}
	for _, fn := range functions {
		if fn == nil || fn.Prog != b.prog || fn.Syntax() == nil {
			continue
		}
		found := parentlessNamedTypesInFunction(fn)
		for named := range found {
			obj := named.Obj()
			if obj == nil || obj.Pkg() == nil || obj.Pos() == token.NoPos {
				continue
			}
			syntax := fn.Syntax()
			if obj.Pos() < syntax.Pos() || obj.Pos() >= syntax.End() {
				continue
			}
			if functionTypeArgsContain(fn, named) {
				// The type entered this instance as an argument; it was not
				// freshly declared by this invocation of the source function.
				continue
			}
			span := int64(syntax.End() - syntax.Pos())
			previous, exists := b.localTypeOwners[named]
			previousSpan := b.localTypeOwnerSpans[named]
			switch {
			case !exists || span < previousSpan:
				b.localTypeOwners[named] = fn
				b.localTypeOwnerSpans[named] = span
				b.localTypeAmbiguous[named] = false
			case span == previousSpan && previous != fn:
				b.localTypeAmbiguous[named] = true
			}
		}
	}
}

func parentlessNamedTypesInFunction(fn *ssa.Function) map[*types.Named]struct{} {
	collector := localNamedTypeCollector{
		found: make(map[*types.Named]struct{}),
		seen:  make(map[types.Type]bool),
	}
	collector.typ(fn.Signature)
	for _, parameter := range fn.Params {
		collector.value(parameter)
	}
	for _, free := range fn.FreeVars {
		collector.value(free)
	}
	for _, local := range fn.Locals {
		collector.value(local)
	}
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			if value, ok := instruction.(ssa.Value); ok {
				collector.value(value)
			}
			for _, operand := range instruction.Operands(nil) {
				if operand != nil && *operand != nil {
					collector.value(*operand)
				}
			}
			if call, ok := instruction.(ssa.CallInstruction); ok {
				collector.function(call.Common().StaticCallee())
			}
		}
	}
	return collector.found
}

type localNamedTypeCollector struct {
	found map[*types.Named]struct{}
	seen  map[types.Type]bool
}

func (c *localNamedTypeCollector) value(value ssa.Value) {
	if value == nil {
		return
	}
	c.typ(value.Type())
	if fn, ok := value.(*ssa.Function); ok {
		c.function(fn)
	}
}

func (c *localNamedTypeCollector) function(fn *ssa.Function) {
	if fn == nil {
		return
	}
	for _, arg := range fn.TypeArgs() {
		c.typ(arg)
	}
}

func (c *localNamedTypeCollector) typ(typ types.Type) {
	if typ == nil {
		return
	}
	typ = types.Unalias(typ)
	if c.seen[typ] {
		return
	}
	c.seen[typ] = true
	switch typ := typ.(type) {
	case *types.Basic:
	case *types.Pointer:
		c.typ(typ.Elem())
	case *types.Slice:
		c.typ(typ.Elem())
	case *types.Array:
		c.typ(typ.Elem())
	case *types.Map:
		c.typ(typ.Key())
		c.typ(typ.Elem())
	case *types.Chan:
		c.typ(typ.Elem())
	case *types.Named:
		if obj := typ.Obj(); obj != nil && obj.Pkg() != nil && obj.Parent() == nil {
			c.found[typ] = struct{}{}
		}
		if args := typ.TypeArgs(); args != nil {
			for i := 0; i < args.Len(); i++ {
				c.typ(args.At(i))
			}
		}
	case *types.Signature:
		if typ.Recv() != nil {
			c.typ(typ.Recv().Type())
		}
		c.typ(typ.Params())
		c.typ(typ.Results())
	case *types.Tuple:
		for i := 0; i < typ.Len(); i++ {
			c.typ(typ.At(i).Type())
		}
	case *types.Struct:
		for i := 0; i < typ.NumFields(); i++ {
			c.typ(typ.Field(i).Type())
		}
	case *types.Interface:
		typ.Complete()
		for i := 0; i < typ.NumMethods(); i++ {
			c.typ(typ.Method(i).Type())
		}
		for i := 0; i < typ.NumEmbeddeds(); i++ {
			c.typ(typ.EmbeddedType(i))
		}
	case *types.TypeParam:
		c.typ(typ.Constraint())
	case *types.Union:
		for i := 0; i < typ.Len(); i++ {
			c.typ(typ.Term(i).Type())
		}
	}
}

func functionTypeArgsContain(fn *ssa.Function, target *types.Named) bool {
	collector := localNamedTypeCollector{
		found: make(map[*types.Named]struct{}),
		seen:  make(map[types.Type]bool),
	}
	collector.function(fn)
	_, ok := collector.found[target]
	return ok
}

func (b *functionIDBuilder) objectKey(obj types.Object) (string, error) {
	if obj == nil {
		return "", fmt.Errorf("coro: nil type object")
	}
	pkgKey, err := b.packageKey(obj.Pkg())
	if err != nil {
		return "", err
	}
	scope, err := objectScopePath(obj)
	if err != nil {
		return "", err
	}
	return identityNode("object",
		identityPair{"package", pkgKey},
		identityPair{"name", obj.Name()},
		identityPair{"scope", scope},
	), nil
}

func (b *functionIDBuilder) objectID(obj types.Object) (string, error) {
	if obj == nil {
		return "", fmt.Errorf("coro: nil object identity")
	}
	if obj.Exported() || obj.Pkg() == nil {
		return obj.Name(), nil
	}
	pkgKey, err := b.packageKey(obj.Pkg())
	if err != nil {
		return "", err
	}
	return identityNode("unexported-id",
		identityPair{"package", pkgKey},
		identityPair{"name", obj.Name()},
	), nil
}

func objectScopePath(obj types.Object) (string, error) {
	if obj == nil || obj.Pkg() == nil {
		return "", nil
	}
	if obj.Parent() == nil {
		return "", fmt.Errorf("coro: package object %q has no lexical owner", obj.Name())
	}
	if obj.Parent() == obj.Pkg().Scope() {
		return "", nil
	}
	root := obj.Pkg().Scope()
	current := obj.Parent()
	indices := make([]int, 0, 4)
	for current != root {
		parent := current.Parent()
		if parent == nil {
			return "", fmt.Errorf("coro: local object %q has no package scope ancestry", obj.Name())
		}
		index := -1
		for i := 0; i < parent.NumChildren(); i++ {
			if parent.Child(i) == current {
				index = i
				break
			}
		}
		if index < 0 {
			return "", fmt.Errorf("coro: local object %q has an unindexed lexical scope", obj.Name())
		}
		indices = append(indices, index)
		current = parent
	}
	var text strings.Builder
	for i := len(indices) - 1; i >= 0; i-- {
		if text.Len() != 0 {
			text.WriteByte('.')
		}
		text.WriteString(strconv.Itoa(indices[i]))
	}
	return text.String(), nil
}

func (b *functionIDBuilder) signatureKey(sig *types.Signature) (string, error) {
	if typeParams := sig.TypeParams(); typeParams != nil && typeParams.Len() != 0 {
		return "", fmt.Errorf("coro: generic function type identity is not supported in v0")
	}
	if receiverTypeParams := sig.RecvTypeParams(); receiverTypeParams != nil && receiverTypeParams.Len() != 0 {
		return "", fmt.Errorf("coro: generic receiver function type identity is not supported in v0")
	}
	fields := []identityPair{{"variadic", strconv.FormatBool(sig.Variadic())}}
	params, err := b.tupleKey(sig.Params())
	if err != nil {
		return "", err
	}
	results, err := b.tupleKey(sig.Results())
	if err != nil {
		return "", err
	}
	fields = append(fields, identityPair{"params", params}, identityPair{"results", results})
	return identityNode("signature", fields...), nil
}

func (b *functionIDBuilder) tupleKey(tuple *types.Tuple) (string, error) {
	fields := make([]identityPair, 0, tuple.Len())
	for i := 0; i < tuple.Len(); i++ {
		key, err := b.typeKey(tuple.At(i).Type())
		if err != nil {
			return "", err
		}
		fields = append(fields, identityPair{"element", key})
	}
	return identityNode("tuple", fields...), nil
}

type identityPair struct {
	name  string
	value string
}

func identityNode(kind string, fields ...identityPair) string {
	var text strings.Builder
	appendIdentityField(&text, "kind", kind)
	for _, field := range fields {
		appendIdentityField(&text, field.name, field.value)
	}
	return text.String()
}

func identityKeyDigest(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func appendIdentityField(text *strings.Builder, name, value string) {
	text.WriteString(name)
	text.WriteByte('=')
	text.WriteString(strconv.Itoa(len(value)))
	text.WriteByte(':')
	text.WriteString(value)
	text.WriteByte(';')
}

func syntheticKind(fn *ssa.Function) string {
	switch {
	case fn.Synthetic == "":
		return "none"
	case fn.Synthetic == "package initializer":
		return "package-initializer"
	case strings.HasPrefix(fn.Synthetic, "wrapper for "):
		return "method-wrapper"
	case strings.HasPrefix(fn.Synthetic, "thunk for "):
		return "method-thunk"
	case strings.HasPrefix(fn.Synthetic, "bound method wrapper for "):
		return "bound-method"
	case strings.HasPrefix(fn.Synthetic, "instance of "):
		return "generic-instance"
	case strings.HasPrefix(fn.Synthetic, "instantiation wrapper of "):
		return "generic-wrapper"
	case fn.Synthetic == "from type information", fn.Synthetic == "from type information (on demand)":
		return "type-information"
	default:
		return "unknown"
	}
}
