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
	"go/token"
	"go/types"
	"strings"

	llssa "github.com/xgo-dev/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

type coroDarwinImportTarget struct {
	source, canonical *ssa.Function
}

// coroDarwinImportGlobals joins generated x/sys relocations to the syscall
// SDK's existing worker-address catalog. Darwin emission replaces the
// generated assembly with dynamic-import trampolines and lowers ordinary
// loads of their address words to the remapped C entry. Record the same entry
// before freezing the coroutine capabilities.
func (u *EmissionUniverse) coroDarwinImportGlobals() map[*ssa.Global]coroDarwinImportTarget {
	target := u.prog.Target()
	if target == nil || target.GOOS != "darwin" ||
		(target.GOARCH != "arm64" && target.GOARCH != "amd64") {
		return nil
	}
	catalog := [2]*preparedEmissionPackage{u.byPath["syscall"], u.byPath["internal/syscall/unix"]}
	globals := make(map[*ssa.Global]coroDarwinImportTarget)
	for _, pkg := range u.packages {
		if pkg.pkgPath != "golang.org/x/sys/unix" || pkg.metadataOnly {
			continue
		}
		for _, file := range pkg.files {
			if file == nil {
				continue
			}
			for _, directive := range file.Comments {
				for _, comment := range directive.List {
					fields := strings.Fields(comment.Text)
					if len(fields) != 4 || fields[0] != "//go:cgo_import_dynamic" ||
						fields[3] != `"/usr/lib/libSystem.B.dylib"` || fields[1] != "libc_"+fields[2] {
						continue
					}
					if global, origin, ok := u.coroDarwinImportGlobal(pkg, catalog, file, fields[1]); ok {
						globals[global] = origin
					}
				}
			}
		}
	}
	u.rejectWritableDarwinImportGlobals(globals)
	return globals
}

func (u *EmissionUniverse) coroDarwinImportGlobal(
	pkg *preparedEmissionPackage, catalog [2]*preparedEmissionPackage, file *ast.File, local string,
) (*ssa.Global, coroDarwinImportTarget, bool) {
	global, ok := pkg.ssa.Members[local+"_trampoline_addr"].(*ssa.Global)
	if !ok || global.Pkg != pkg.ssa || global.Pos() < file.Pos() || global.Pos() >= file.End() {
		return nil, coroDarwinImportTarget{}, false
	}
	pointer, ok := global.Type().(*types.Pointer)
	if !ok || !types.Identical(pointer.Elem(), types.Typ[types.Uintptr]) {
		return nil, coroDarwinImportTarget{}, false
	}
	name := local + "_trampoline"
	var found coroDarwinImportTarget
	for _, sdk := range catalog {
		origin, ok := u.coroDarwinImportCatalogTarget(sdk, name)
		if !ok {
			continue
		}
		if found.canonical != nil && found != origin {
			return nil, coroDarwinImportTarget{}, false
		}
		found = origin
	}
	return global, found, found.canonical != nil
}

func (u *EmissionUniverse) coroDarwinImportCatalogTarget(sdk *preparedEmissionPackage, name string) (coroDarwinImportTarget, bool) {
	var origin coroDarwinImportTarget
	if sdk == nil {
		return origin, false
	}
	origin.source, _ = sdk.ssa.Members[name].(*ssa.Function)
	if origin.source == nil && sdk.hasPatch {
		origin.source, _ = sdk.patch.Alt.Members[name].(*ssa.Function)
	}
	if origin.source == nil {
		return origin, false
	}
	origin.canonical = u.canonicalAlias(origin.source)
	if origin.canonical == nil {
		return origin, false
	}
	owner := u.ownerOf(origin.canonical)
	if !coroInferredPatchWorkerAddressDeclaration(owner, origin.canonical) {
		return origin, false
	}
	key := u.finalKeys[emissionFunctionOwnerKey{function: origin.canonical, owner: owner}]
	physical := remapTrampolineCNameForTarget(u.prog.Target(), extractTrampolineCName(name))
	if key == "" {
		link, ok := u.prog.Linkname(llssa.FullName(owner.pkgTypes, origin.canonical.Name()))
		return origin, ok && link == "C."+physical
	}
	kind, symbol, _, valid := splitManagedSymbolKey(key)
	return origin, valid && kind == cFunc && symbol == physical
}

func (u *EmissionUniverse) rejectWritableDarwinImportGlobals(globals map[*ssa.Global]coroDarwinImportTarget) {
	if len(globals) == 0 {
		return
	}
	var operands [8]*ssa.Value
	for _, fn := range u.functions {
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				switch instruction := instruction.(type) {
				case *ssa.DebugRef:
					continue
				case *ssa.UnOp:
					if instruction.Op == token.MUL {
						continue
					}
				}
				for _, operand := range instruction.Operands(operands[:0]) {
					if operand != nil {
						if global, ok := (*operand).(*ssa.Global); ok {
							delete(globals, global)
						}
					}
				}
			}
		}
	}
}

func (b *coroCallableShadowBuilder) seedDarwinImport(load *ssa.UnOp) error {
	if load.Op != token.MUL {
		return nil
	}
	global, ok := load.X.(*ssa.Global)
	if !ok {
		return nil
	}
	origin, ok := b.darwinGlobals[global]
	if !ok {
		return nil
	}
	capability, reason, err := coroWorkerCallableTarget(b.universe, origin.source, origin.canonical)
	if err != nil {
		return err
	}
	if reason != "" {
		b.failures[load] = reason
		return nil
	}
	shadow := capability.shadow(load, origin.source)
	b.result.producers[load] = shadow
	b.addFact(load, shadow)
	return nil
}

// A generated import can join an existing physical contract. Some generated
// wrappers use a different syscall transport width from the SDK wrapper;
// those distinct edges stay uncertified, including unreachable wrappers.
func (b *coroCallableShadowBuilder) bindDarwinImportContracts(existing map[string]CoroCallableShadow) {
	if len(b.darwinGlobals) == 0 {
		return
	}
	importOnly := make(map[string]CoroCallableShadow)
	ambiguous := make(map[string]bool)
	for producer, shadow := range b.result.producers {
		if _, load := producer.(*ssa.UnOp); !load {
			continue
		}
		symbol := shadow.PhysicalSymbol
		if authoritative, ok := existing[symbol]; ok {
			if !authoritative.sameContract(shadow) {
				b.rejectProducer(producer, shadow, "darwin-import-conflicts-with-sdk-worker-abi")
			}
			continue
		}
		if previous, ok := importOnly[symbol]; ok {
			ambiguous[symbol] = ambiguous[symbol] || !previous.sameContract(shadow)
		} else {
			importOnly[symbol] = shadow
		}
	}
	for producer, shadow := range b.result.producers {
		if _, load := producer.(*ssa.UnOp); load && ambiguous[shadow.PhysicalSymbol] {
			b.rejectProducer(producer, shadow, "darwin-import-has-conflicting-worker-abis")
		}
	}
}
