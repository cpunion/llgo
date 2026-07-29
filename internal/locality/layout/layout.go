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

// Package layout plans the internal storage layout for one package's local variables. It
// is intentionally independent of LLGo SSA, LLVM, the runtime, and the build
// cache so every integration layer consumes the same policy result.
package layout

import (
	"fmt"
	"go/types"
	"sort"

	"github.com/goplus/llgo/internal/locality"
)

// Storage identifies the physical addressing strategy selected for a variable.
type Storage uint8

const (
	StorageUnknown Storage = iota
	StorageNativeTLS
	StoragePackage
)

// Declaration is the source information needed to plan one variable.
type Declaration struct {
	Name string
	Type types.Type
	Info locality.Info
}

// Variable is one planned local variable. Field is valid for package storage.
type Variable struct {
	Declaration
	Storage Storage
	Field   int
}

// Initializer identifies one replay helper in Go package initialization order.
type Initializer struct {
	Name  string
	Order int
}

// Package is a deterministic storage plan. Block contains all pointer-bearing
// TLS and GLS variables in their shared physical package block.
type Package struct {
	Path      string
	Variables []Variable
	Block     []Variable
	Thread    []Initializer
	Goroutine []Initializer
	// ThreadDispatch and GoroutineDispatch are source-SSA func() bodies that
	// replay the corresponding initializer list. Coroutine analysis consumes
	// these exact functions instead of discovering generated LLVM call edges.
	ThreadDispatch    string
	GoroutineDispatch string

	byName map[string]int
}

// Plan creates a deterministic package layout. TLS and GLS remain logically
// distinct, while the current one-thread-per-goroutine backend may share their
// pointer-bearing package block.
func Plan(path string, declarations []Declaration) (Package, error) {
	ret := Package{Path: path}
	decls := append([]Declaration(nil), declarations...)
	sort.Slice(decls, func(i, j int) bool { return decls[i].Name < decls[j].Name })
	ret.byName = make(map[string]int, len(decls))
	initializers := map[locality.Kind]map[int]string{
		locality.Thread:    {},
		locality.Goroutine: {},
	}
	dispatchers := map[locality.Kind]string{
		locality.Thread:    "",
		locality.Goroutine: "",
	}
	for _, decl := range decls {
		if decl.Info.Locality == locality.None {
			continue
		}
		if decl.Name == "" || decl.Type == nil {
			return Package{}, fmt.Errorf("locality layout: incomplete declaration %q", decl.Name)
		}
		if _, exists := ret.byName[decl.Name]; exists {
			return Package{}, fmt.Errorf("locality layout: duplicate declaration %s", decl.Name)
		}
		if decl.Info.Locality != locality.Thread && decl.Info.Locality != locality.Goroutine {
			return Package{}, fmt.Errorf("locality layout: invalid locality for %s", decl.Name)
		}
		hasMetadata := decl.Info.InitFunc != "" || decl.Info.InitOrder != 0 || decl.Info.InitDispatch != ""
		prepared := decl.Info.InitFunc != "" && decl.Info.InitOrder != 0 && decl.Info.InitDispatch != ""
		if decl.Info.HasInitializer != prepared || !decl.Info.HasInitializer && hasMetadata {
			return Package{}, fmt.Errorf("locality layout: inconsistent initializer metadata for %s", decl.Name)
		}
		variable := Variable{Declaration: decl, Field: -1, Storage: StorageForType(decl.Type)}
		if variable.Storage == StoragePackage {
			variable.Field = len(ret.Block)
			ret.Block = append(ret.Block, variable)
		}
		ret.byName[decl.Name] = len(ret.Variables)
		ret.Variables = append(ret.Variables, variable)
		if decl.Info.InitFunc != "" {
			byOrder := initializers[decl.Info.Locality]
			if current, exists := byOrder[decl.Info.InitOrder]; exists && current != decl.Info.InitFunc {
				return Package{}, fmt.Errorf("locality layout: initializer order %d names both %s and %s", decl.Info.InitOrder, current, decl.Info.InitFunc)
			}
			byOrder[decl.Info.InitOrder] = decl.Info.InitFunc
			if current := dispatchers[decl.Info.Locality]; current != "" && current != decl.Info.InitDispatch {
				return Package{}, fmt.Errorf(
					"locality layout: %s initializers name both dispatchers %s and %s",
					decl.Info.Locality, current, decl.Info.InitDispatch,
				)
			}
			dispatchers[decl.Info.Locality] = decl.Info.InitDispatch
		}
	}
	ret.Thread = orderedInitializers(initializers[locality.Thread])
	ret.Goroutine = orderedInitializers(initializers[locality.Goroutine])
	ret.ThreadDispatch = dispatchers[locality.Thread]
	ret.GoroutineDispatch = dispatchers[locality.Goroutine]
	return ret, nil
}

// StorageForType returns the physical storage class for a local variable type.
// Pointer-free values use native LLVM TLS; values visible to the GC share the
// package block rooted by LocalContext.
func StorageForType(typ types.Type) Storage {
	if hasPointers(typ) {
		return StoragePackage
	}
	return StorageNativeTLS
}

func hasPointers(typ types.Type) bool {
	typ = types.Unalias(typ)
	switch typ := typ.(type) {
	case *types.Basic:
		return typ.Kind() == types.String || typ.Kind() == types.UnsafePointer
	case *types.Pointer, *types.Slice, *types.Map, *types.Chan, *types.Signature, *types.Interface:
		return true
	case *types.Array:
		return typ.Len() != 0 && hasPointers(typ.Elem())
	case *types.Struct:
		for i := 0; i < typ.NumFields(); i++ {
			if hasPointers(typ.Field(i).Type()) {
				return true
			}
		}
		return false
	case *types.Named:
		return hasPointers(typ.Underlying())
	case *types.TypeParam:
		return true
	default:
		return false
	}
}

func orderedInitializers(byOrder map[int]string) []Initializer {
	ret := make([]Initializer, 0, len(byOrder))
	for order, name := range byOrder {
		ret = append(ret, Initializer{Name: name, Order: order})
	}
	sort.Slice(ret, func(i, j int) bool { return ret[i].Order < ret[j].Order })
	return ret
}

// Lookup returns a variable from the package plan.
func (p Package) Lookup(name string) (Variable, bool) {
	index, ok := p.byName[name]
	if !ok {
		return Variable{}, false
	}
	return p.Variables[index], true
}

// Initializers returns the ordered replay helpers for kind.
func (p Package) Initializers(kind locality.Kind) []Initializer {
	if kind == locality.Thread {
		return p.Thread
	}
	if kind == locality.Goroutine {
		return p.Goroutine
	}
	return nil
}

// Dispatcher returns the exact source-SSA func() that replays kind's
// initializers, or an empty string when the kind has no initializers.
func (p Package) Dispatcher(kind locality.Kind) string {
	if kind == locality.Thread {
		return p.ThreadDispatch
	}
	if kind == locality.Goroutine {
		return p.GoroutineDispatch
	}
	return ""
}

// BlockName returns the shared package-block accessor symbol.
func BlockName(path string) string { return qualify(path, "__llgo_local_block") }

// BlockCacheName returns the package block's owner-local direct-cache symbol.
func BlockCacheName(path string) string { return qualify(path, "__llgo_local_cache") }

// InitName returns the package/kind initializer dispatcher symbol.
func InitName(path string, kind locality.Kind) string {
	return qualify(path, "__llgo_"+kind.String()+"_init")
}

// EnsureName returns the package/kind first-use initializer symbol.
func EnsureName(path string, kind locality.Kind) string { return InitName(path, kind) + "$ensure" }

// GuardName returns the package/kind native TLS state symbol.
func GuardName(path string, kind locality.Kind) string { return InitName(path, kind) + "$guard" }

// FailureCacheName returns the package/kind initializer failure-cache symbol.
func FailureCacheName(path string, kind locality.Kind) string {
	return InitName(path, kind) + "$failure_cache"
}

func qualify(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}
