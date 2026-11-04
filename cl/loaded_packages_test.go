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
	"fmt"
	"go/constant"
	"go/token"
	"go/types"
	"sync"
	"testing"
)

func loadedPackageContexts(t *testing.T, count int) (*EmissionUniverse, []*context) {
	t.Helper()
	program := newEmissionTestProgram()
	pkg := program.addPackage(t, "example.com/packageindex", "package packageindex\nfunc F() {}\n")
	program.ssa.Build()
	universe, owner := newEmissionABIDemandTestUniverse(program, pkg)
	universe.loadedPackageSnapshot = universe.loadedPackages()
	contexts := make([]*context, count)
	for index := range contexts {
		ctx, err := universe.functionABIContext(pkg.ssa.Func("F"), owner)
		if err != nil {
			t.Fatal(err)
		}
		ctx.options.PreloadedSyntax = true
		contexts[index] = ctx
	}
	return universe, contexts
}

func loadedPackageNoInit(path string) *types.Package {
	pkg := types.NewPackage(path, "extra")
	pkg.Scope().Insert(types.NewConst(token.NoPos, pkg, "LLGoPackage", types.Typ[types.String], constant.MakeString("noinit")))
	return pkg
}

func TestEmissionUniverseLoadedPackageContextIsolation(t *testing.T) {
	universe, contexts := loadedPackageContexts(t, 2)
	first, sibling := contexts[0], contexts[1]
	copyOfFirst := *first
	frozenCount := len(universe.loadedPackageSnapshot)
	for _, ctx := range []*context{first, sibling, &copyOfFirst} {
		ctx.ensureLoaded(ctx.goTyps)
		if !ctx.pkgNoInit(types.Unsafe) || !ctx.loadedShared {
			t.Fatal("known package lookup changed the frozen index or unsafe import kind")
		}
	}
	extra := loadedPackageNoInit("example.com/first-private")
	if !first.pkgNoInit(extra) || first.loadedShared || len(first.loaded) != frozenCount+1 {
		t.Fatal("private import did not detach and apply its noinit directive")
	}
	for _, ctx := range []*context{sibling, &copyOfFirst} {
		if _, exists := ctx.loaded[extra]; exists || !ctx.loadedShared || len(ctx.loaded) != frozenCount {
			t.Fatal("private import escaped into a sibling or copied context")
		}
	}
	if len(universe.loadedPackageSnapshot) != frozenCount || universe.loadedPackageSnapshot[extra] != nil {
		t.Fatal("private import escaped into the universe")
	}
	secondExtra := loadedPackageNoInit("example.com/second-private")
	firstInfo := first.loaded[extra]
	if !first.pkgNoInit(secondExtra) || first.loaded[extra] != firstInfo || len(first.loaded) != frozenCount+2 {
		t.Fatal("a later private import lost the first import or its metadata")
	}
	if first.loaded[types.Unsafe] != universe.loadedPackageSnapshot[types.Unsafe] {
		t.Fatal("detaching changed existing package metadata")
	}
}

func TestEmissionUniverseLoadedPackageConcurrentExtensions(t *testing.T) {
	const count = 32
	universe, contexts := loadedPackageContexts(t, count)
	frozenCount := len(universe.loadedPackageSnapshot)
	var group sync.WaitGroup
	for index, ctx := range contexts {
		index, ctx := index, ctx
		group.Add(1)
		go func() {
			defer group.Done()
			extra := loadedPackageNoInit(fmt.Sprintf("example.com/private/%d", index))
			for repeat := 0; repeat < 100; repeat++ {
				if !ctx.pkgNoInit(types.Unsafe) || !ctx.pkgNoInit(extra) || len(ctx.loaded) != frozenCount+1 {
					t.Errorf("context %d lost an import or observed another context's extension", index)
					return
				}
			}
		}()
	}
	group.Wait()
	if len(universe.loadedPackageSnapshot) != frozenCount {
		t.Fatal("concurrent extensions modified the universe")
	}
	for index, ctx := range contexts {
		if len(ctx.loaded) != frozenCount+1 || ctx.loadedShared {
			t.Errorf("context %d did not retain exactly its own extension", index)
		}
	}
}

func TestLoadedPackageLegacyOwnedIndex(t *testing.T) {
	loaded := map[*types.Package]*pkgInfo{types.Unsafe: {kind: PkgDeclOnly}}
	ctx := &context{loaded: loaded}
	ctx.options.PreloadedSyntax = true
	extra := loadedPackageNoInit("example.com/legacy-private")
	if !ctx.pkgNoInit(extra) || loaded[extra] == nil || loaded[extra].kind != PkgNoInit {
		t.Fatal("an owned context no longer updates its original import index")
	}
}

func TestEmissionUniverseLoadedPackageFallbackIndex(t *testing.T) {
	var universe *EmissionUniverse
	first, second := universe.loadedPackages(), universe.loadedPackages()
	extra := loadedPackageNoInit("example.com/fallback-private")
	ctx := &context{loaded: first, loadedShared: true}
	ctx.options.PreloadedSyntax = true
	if !ctx.pkgNoInit(extra) || first[extra] != nil || second[extra] != nil || !ctx.pkgNoInit(types.Unsafe) {
		t.Fatal("fallback import indexes share mutable state or lost unsafe metadata")
	}
}

func BenchmarkEmissionUniverseLoadedPackageIndex(b *testing.B) {
	index := map[*types.Package]*pkgInfo{types.Unsafe: {kind: PkgDeclOnly}}
	for i := 0; i < 2048; i++ {
		index[types.NewPackage(fmt.Sprintf("example.com/p%d", i), "p")] = &pkgInfo{kind: PkgNormal}
	}
	universe := &EmissionUniverse{loadedPackageSnapshot: index}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx := context{loaded: universe.loadedPackages(), loadedShared: true}
		if !ctx.pkgNoInit(types.Unsafe) {
			b.Fatal("unsafe lost its declaration-only kind")
		}
	}
}
