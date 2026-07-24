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

package build

import (
	"fmt"
	"go/ast"
	gobuild "go/build"
	"go/constant"
	"go/types"
	"path/filepath"
	"slices"
	"strings"

	"github.com/goplus/llgo/cl"
	"github.com/goplus/llgo/internal/packages"
	llplan9asm "github.com/goplus/llgo/internal/plan9asm"
	llruntime "github.com/goplus/llgo/runtime"
	"github.com/xgo-dev/llvm"
)

// coroRawGlobalSymbolInventory is the build-owned set of per-package profiles
// for symbols mentioned by non-Go inputs before coroutine planning. Profiles
// are keyed by the same stable EmissionPackage.Identity used by cl. An opaque
// input therefore opens only the package module that links it; unrelated Go
// modules can still internalize private cells after their own complete audit.
//
// The inventory deliberately does not use text search over C, assembly, LLVM
// IR, or object files as a proof. Plan9 assembly is accepted only after the
// exact target-selected input has passed the same structured translator used by
// codegen; Darwin cgo-import aliases are generated wholly from frozen pragmas.
// Inputs without an equivalent pre-plan representation remain fail closed.
type coroRawGlobalSymbolInventory struct {
	profiles map[string]*coroRawGlobalSymbolProfile
}

type coroRawGlobalSymbolProfile struct {
	complete bool
	mentions map[string][]coroRawGlobalSymbolMention
	blockers []string
}

type coroRawGlobalSymbolMention struct {
	kind   string
	origin string
}

type coroRawGlobalSymbolInventoryBuilder struct {
	mentions map[string][]coroRawGlobalSymbolMention
	blockers map[string]struct{}
}

func newCoroRawGlobalSymbolInventoryBuilder() *coroRawGlobalSymbolInventoryBuilder {
	return &coroRawGlobalSymbolInventoryBuilder{
		mentions: make(map[string][]coroRawGlobalSymbolMention),
		blockers: make(map[string]struct{}),
	}
}

func (b *coroRawGlobalSymbolInventoryBuilder) mention(symbol, kind, origin string) {
	if b == nil || symbol == "" {
		return
	}
	entry := coroRawGlobalSymbolMention{kind: kind, origin: origin}
	for _, previous := range b.mentions[symbol] {
		if previous == entry {
			return
		}
	}
	b.mentions[symbol] = append(b.mentions[symbol], entry)
}

func (b *coroRawGlobalSymbolInventoryBuilder) block(kind, origin string) {
	if b == nil {
		return
	}
	detail := strings.TrimSpace(kind)
	if strings.TrimSpace(origin) != "" {
		detail += ": " + origin
	}
	if detail != "" {
		b.blockers[detail] = struct{}{}
	}
}

func (b *coroRawGlobalSymbolInventoryBuilder) freeze() *coroRawGlobalSymbolProfile {
	result := &coroRawGlobalSymbolProfile{
		complete: b != nil && len(b.blockers) == 0,
		mentions: make(map[string][]coroRawGlobalSymbolMention),
	}
	if b == nil {
		result.blockers = []string{"missing raw-symbol inventory builder"}
		return result
	}
	for symbol, entries := range b.mentions {
		entries = append([]coroRawGlobalSymbolMention(nil), entries...)
		slices.SortFunc(entries, func(left, right coroRawGlobalSymbolMention) int {
			if order := strings.Compare(left.kind, right.kind); order != 0 {
				return order
			}
			return strings.Compare(left.origin, right.origin)
		})
		result.mentions[symbol] = entries
	}
	for blocker := range b.blockers {
		result.blockers = append(result.blockers, blocker)
	}
	slices.Sort(result.blockers)
	return result
}

// newCompleteCoroRawGlobalSymbolInventory returns a closed empty inventory.
// It exists primarily so isolated build tests can state explicitly that their
// synthetic universe has no non-Go linker inputs. Production callers freeze
// the inventory from the actual aPackage set instead.
func newCompleteCoroRawGlobalSymbolInventory(packageIdentities ...string) *coroRawGlobalSymbolInventory {
	result := &coroRawGlobalSymbolInventory{profiles: make(map[string]*coroRawGlobalSymbolProfile, len(packageIdentities))}
	for _, identity := range packageIdentities {
		if identity != "" {
			result.profiles[identity] = newCoroRawGlobalSymbolInventoryBuilder().freeze()
		}
	}
	return result
}

// proveNoDefinitionOrReference reports whether the frozen inventory proves
// that physicalSymbol is absent from every non-Go linker input. reason is
// deterministic and suitable for a fail-closed planner diagnostic.
func (i *coroRawGlobalSymbolInventory) proveNoDefinitionOrReference(packageIdentity, physicalSymbol string) (proved bool, reason string) {
	if i == nil {
		return false, "raw data-symbol inventory was not frozen"
	}
	profile := i.profiles[packageIdentity]
	if profile == nil {
		return false, fmt.Sprintf("raw data-symbol profile for package identity %q was not frozen", packageIdentity)
	}
	return profile.proveNoDefinitionOrReference(physicalSymbol)
}

func (i *coroRawGlobalSymbolInventory) emissionProfile(packageIdentity string) cl.CoroRawDataSymbolProfile {
	profile := i.profiles[packageIdentity]
	if profile == nil {
		return cl.CoroRawDataSymbolProfile{Blockers: []string{"raw data-symbol profile was not frozen"}}
	}
	mentions := make([]string, 0, len(profile.mentions))
	for symbol := range profile.mentions {
		mentions = append(mentions, symbol)
	}
	slices.Sort(mentions)
	return cl.CoroRawDataSymbolProfile{
		Complete: profile.complete,
		Mentions: mentions,
		Blockers: append([]string(nil), profile.blockers...),
	}
}

func (i *coroRawGlobalSymbolProfile) proveNoDefinitionOrReference(physicalSymbol string) (proved bool, reason string) {
	if i == nil {
		return false, "raw data-symbol package profile was not frozen"
	}
	if entries := i.mentions[physicalSymbol]; len(entries) != 0 {
		details := make([]string, 0, len(entries))
		for _, entry := range entries {
			detail := entry.kind
			if entry.origin != "" {
				detail += " " + entry.origin
			}
			details = append(details, detail)
		}
		return false, fmt.Sprintf("physical symbol %q is mentioned by %s", physicalSymbol, strings.Join(details, ", "))
	}
	if !i.complete {
		return false, "raw data-symbol inventory is incomplete: " + strings.Join(i.blockers, "; ")
	}
	if physicalSymbol == "" {
		return false, "raw data-symbol absence requires a non-empty physical symbol"
	}
	return true, ""
}

// freezeCoroRawGlobalSymbolInventory inventories the same package-level raw
// inputs selected later by buildPkg. Profiles are isolated by the exact stable
// identity passed to cl; original and selected Alt inputs share one profile.
func freezeCoroRawGlobalSymbolInventory(ctx *context, all []*aPackage) (*coroRawGlobalSymbolInventory, error) {
	result := &coroRawGlobalSymbolInventory{profiles: make(map[string]*coroRawGlobalSymbolProfile)}

	packagesInOrder := append([]*aPackage(nil), all...)
	slices.SortFunc(packagesInOrder, func(left, right *aPackage) int {
		if left == nil {
			if right == nil {
				return 0
			}
			return -1
		}
		if right == nil {
			return 1
		}
		if order := strings.Compare(left.ID, right.ID); order != 0 {
			return order
		}
		return strings.Compare(left.PkgPath, right.PkgPath)
	})
	for _, aPkg := range packagesInOrder {
		if aPkg == nil || aPkg.Package == nil || llruntime.SkipToBuild(aPkg.PkgPath) {
			continue
		}
		identity := coroRawPackageIdentity(aPkg)
		if identity == "" {
			return nil, fmt.Errorf("freeze raw data-symbol profile: package %q has no stable identity", aPkg.PkgPath)
		}
		if _, duplicate := result.profiles[identity]; duplicate {
			return nil, fmt.Errorf("freeze raw data-symbol profile: duplicate package identity %q", identity)
		}
		builder := newCoroRawGlobalSymbolInventoryBuilder()
		if ctx == nil {
			builder.block("missing build context", "")
		} else {
			if ctx.buildConf == nil {
				builder.block("missing target build configuration", "")
			}
		}
		kind, parameter := cl.PkgKindOf(aPkg.Types)
		if kind == cl.PkgDeclOnly {
			// buildAllPkgs never calls buildPkg for a declaration-only package.
			result.profiles[identity] = builder.freeze()
			continue
		}
		if kind == cl.PkgLinkExtern {
			builder.block("opaque external link input", aPkg.PkgPath+": "+strings.TrimSpace(parameter))
		}

		includeOriginalPlan9 := coroRawIncludesOriginalPlan9(aPkg)
		if err := inventoryCoroRawPackageInputs(ctx, builder, aPkg, aPkg.Package, "original", includeOriginalPlan9); err != nil {
			return nil, err
		}
		if aPkg.AltPkg != nil && aPkg.AltPkg.Package != nil {
			// buildPkg always compiles the selected Alt package's own SFiles. For
			// replacement patches it omits the original SFiles; additive patches
			// retain both sets. Keep this in lockstep with buildPkg.
			if err := inventoryCoroRawPackageInputs(ctx, builder, aPkg, aPkg.AltPkg.Package, "alt", true); err != nil {
				return nil, err
			}
		}
		result.profiles[identity] = builder.freeze()
	}
	return result, nil
}

func coroRawPackageIdentity(aPkg *aPackage) string {
	if aPkg == nil {
		return ""
	}
	if aPkg.ID != "" {
		return aPkg.ID
	}
	return aPkg.PkgPath
}

func coroRawIncludesOriginalPlan9(aPkg *aPackage) bool {
	return aPkg == nil || aPkg.AltPkg == nil || llruntime.HasAdditiveAltPkg(aPkg.PkgPath)
}

func inventoryCoroRawPackageInputs(
	ctx *context,
	builder *coroRawGlobalSymbolInventoryBuilder,
	aPkg *aPackage,
	pkg *packages.Package,
	role string,
	includePlan9 bool,
) error {
	if pkg == nil {
		return nil
	}
	origin := pkg.PkgPath + " (" + role + ")"
	if value, ok := coroRawLLGoFiles(pkg); ok && strings.TrimSpace(value) != "" {
		builder.block("opaque LLGoFiles input", origin+": "+value)
	}
	for _, other := range pkg.OtherFiles {
		if strings.EqualFold(filepath.Ext(other), ".syso") {
			builder.block("opaque syso input", other)
		}
	}

	// parseCgo_ is the selector used by buildCgo itself. Merely globbing or
	// searching package sources would not prove which files survive target tags.
	// Synthetic unit packages sometimes omit the FileSet; preserve fail-closed
	// state instead of attempting a position lookup through a nil set.
	if len(pkg.Syntax) != 0 && (aPkg == nil || aPkg.Fset == nil) {
		builder.block("missing package FileSet for raw C input selection", origin)
	} else {
		srcFiles, preambles, _, err := parseCgo_(coroRawBuildContext(ctx), aPkg, pkg.Syntax)
		if err != nil {
			return fmt.Errorf("freeze raw C inputs for %s: %w", origin, err)
		}
		for _, source := range srcFiles {
			kind := "opaque C input"
			if source.isAsm {
				kind = "opaque C-preprocessed assembly input"
			} else if source.isCXX {
				kind = "opaque C++ input"
			}
			builder.block(kind, source.path)
		}
		for _, preamble := range preambles {
			builder.block("opaque generated cgo preamble", preamble.goFile)
		}
	}
	linkFlags, _ := collectGoCgoPragmas(pkg.Syntax)
	for _, flag := range linkFlags {
		builder.block("opaque go:cgo_ldflag link input", origin+": "+flag)
	}

	if err := inventoryCoroDarwinDynimports(ctx, builder, pkg.PkgPath, pkg.Syntax); err != nil {
		return err
	}
	if includePlan9 {
		if err := inventoryCoroPlan9Symbols(ctx, builder, pkg); err != nil {
			return err
		}
	}
	return nil
}

func coroRawBuildContext(ctx *context) *gobuild.Context {
	result := gobuild.Default
	if ctx != nil && ctx.buildConf != nil {
		if ctx.buildConf.Goos != "" {
			result.GOOS = ctx.buildConf.Goos
		}
		if ctx.buildConf.Goarch != "" {
			result.GOARCH = ctx.buildConf.Goarch
		}
	}
	if ctx != nil && ctx.conf != nil {
		result.BuildTags = parseSourcePatchBuildTags(ctx.conf.BuildFlags)
	}
	return &result
}

func coroRawLLGoFiles(pkg *packages.Package) (string, bool) {
	if pkg == nil || pkg.Types == nil || pkg.Types.Scope() == nil {
		return "", false
	}
	object := pkg.Types.Scope().Lookup("LLGoFiles")
	constantObject, ok := object.(*types.Const)
	if !ok || constantObject.Val().Kind() != constant.String {
		return "", false
	}
	return constant.StringVal(constantObject.Val()), true
}

func inventoryCoroDarwinDynimports(
	ctx *context,
	builder *coroRawGlobalSymbolInventoryBuilder,
	pkgPath string,
	files []*ast.File,
) error {
	if ctx == nil || ctx.buildConf == nil || ctx.buildConf.Goos != "darwin" {
		return nil
	}
	_, declarations := collectGoCgoPragmas(files)
	aliases := make(map[string]map[string]struct{})
	for _, declaration := range declarations {
		if declaration.local == "" || declaration.alias == "" || declaration.local == declaration.alias {
			continue
		}
		if aliases[declaration.local] == nil {
			aliases[declaration.local] = make(map[string]struct{})
		}
		aliases[declaration.local][declaration.alias] = struct{}{}
	}
	locals := make([]string, 0, len(aliases))
	for local := range aliases {
		locals = append(locals, local)
	}
	slices.Sort(locals)
	for _, local := range locals {
		values := make([]string, 0, len(aliases[local]))
		for alias := range aliases[local] {
			values = append(values, alias)
		}
		slices.Sort(values)
		if len(values) != 1 {
			return fmt.Errorf("%s: conflicting go:cgo_import_dynamic for %q: %s", pkgPath, local, strings.Join(values, ", "))
		}
		// buildGoCgoAliasObjects emits no symbol for other Darwin arches.
		if ctx.buildConf.Goarch != "arm64" && ctx.buildConf.Goarch != "amd64" {
			continue
		}
		origin := pkgPath + " go:cgo_import_dynamic " + local + " -> " + values[0]
		builder.mention(local, "Darwin dynimport definition", origin)
		builder.mention(values[0], "Darwin dynimport reference", origin)
		builder.mention(local+"_trampoline", "Darwin dynimport definition", origin)
		builder.mention(local+"_trampoline_addr", "Darwin dynimport definition", origin)
	}
	return nil
}

func inventoryCoroPlan9Symbols(ctx *context, builder *coroRawGlobalSymbolInventoryBuilder, pkg *packages.Package) error {
	if ctx == nil || ctx.buildConf == nil || pkg == nil || !ctx.plan9asmEnabled(pkg.PkgPath) {
		return nil
	}
	sfiles, err := pkgSFiles(ctx, pkg)
	if err != nil {
		return fmt.Errorf("freeze Plan9 assembly inputs for %q: %w", pkg.PkgPath, err)
	}
	sfiles = plan9AsmSFiles(sfiles)
	skipDarwinDynimports := shouldCheckDarwinDynimportTrampolineAsm(ctx, pkg)
	var overlay map[string][]byte
	if ctx.conf != nil {
		overlay = ctx.conf.Overlay
	}
	for _, sfile := range sfiles {
		source, err := llplan9asm.ReadFileWithOverlay(overlay, sfile)
		if err != nil {
			return fmt.Errorf("%s: read %s for raw symbol inventory: %w", pkg.PkgPath, sfile, err)
		}
		if shouldSkipDarwinDynimportTrampolineAsm(skipDarwinDynimports, sfile, source) {
			continue
		}
		translation, err := llplan9asm.TranslateSourceModuleForPkg(pkg, sfile, source, ctx.buildConf.Goos, ctx.buildConf.Goarch)
		if err != nil {
			if strings.Contains(err.Error(), "no TEXT directive found") {
				continue
			}
			return fmt.Errorf("%s: translate %s for raw symbol inventory: %w", pkg.PkgPath, sfile, err)
		}
		if pkg.PkgPath != "runtime" {
			if ctx.cTransformer == nil {
				translation.Module.Dispose()
				return fmt.Errorf("%s: missing C ABI transformer for raw Plan9 symbol inventory", pkg.PkgPath)
			}
			ctx.cTransformer.TransformModule(pkg.PkgPath, translation.Module)
		}
		inventoryCoroLLVMModuleSymbols(builder, translation.Module, sfile)
		translation.Module.Dispose()
	}
	return nil
}

// inventoryCoroLLVMModuleSymbols records every linker-level name in the
// translated module. External references necessarily have a declaration in
// LLVM's global/function tables, while definitions occupy the same tables.
// The pinned Plan9 translator emits no aliases/ifuncs/module-level assembly;
// its instruction inline-asm templates contain only opcodes and registers, and
// every symbolic Go operand is lowered to an LLVM global value first.
func inventoryCoroLLVMModuleSymbols(builder *coroRawGlobalSymbolInventoryBuilder, module llvm.Module, origin string) {
	if builder == nil || module.IsNil() {
		return
	}
	for global := module.FirstGlobal(); !global.IsNil(); global = llvm.NextGlobal(global) {
		kind := "Plan9 global reference"
		if !global.IsDeclaration() {
			kind = "Plan9 global definition"
		}
		builder.mention(global.Name(), kind, origin)
	}
	for function := module.FirstFunction(); !function.IsNil(); function = llvm.NextFunction(function) {
		kind := "Plan9 function reference"
		if !function.IsDeclaration() {
			kind = "Plan9 function definition"
		}
		builder.mention(function.Name(), kind, origin)
	}
}
