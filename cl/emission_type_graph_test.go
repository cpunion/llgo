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
	"go/token"
	"go/types"
	"testing"
)

func TestStructuralGoLinknameABITypeKeyCompactsSharedTypeDAG(t *testing.T) {
	left := testSharedGoLinknameType("example.com/left", 20, types.Typ[types.Uintptr])
	right := testSharedGoLinknameType("example.com/right", 20, types.Typ[types.Uintptr])
	leftKey := structuralGoLinknameABITypeKey(left)
	rightKey := structuralGoLinknameABITypeKey(right)
	if leftKey != rightKey {
		t.Fatal("private mirror type DAGs produced different ABI keys")
	}
	const wantLength = len("graph-sha256-v1:") + 64
	if len(leftKey) != wantLength {
		t.Fatalf("compact ABI key length = %d, want %d", len(leftKey), wantLength)
	}

	changed := testSharedGoLinknameType("example.com/changed", 20, types.Typ[types.Uint64])
	if structuralGoLinknameABITypeKey(changed) == leftKey {
		t.Fatal("different leaf ABI acquired the same compact key")
	}
}

func TestStructuralEmissionTypeGraphsCompactSharedDAGAndPreserveMetadataModes(t *testing.T) {
	shared := testSharedAnonymousEmissionType(14)
	strict := structuralEmissionTypeKey(shared)
	strictABI := structuralEmissionABITypeKey(shared)
	identityFree := structuralNamedIdentityFreeABITypeKey(shared)
	for name, test := range map[string]struct {
		key    string
		prefix string
	}{
		"strict":        {key: strict, prefix: emissionStrictTypeGraphKeyPrefix},
		"strict ABI":    {key: strictABI, prefix: emissionStrictABITypeGraphKeyPrefix},
		"identity-free": {key: identityFree, prefix: emissionIdentityFreeTypeGraphPrefix},
	} {
		if want := len(test.prefix) + 64; len(test.key) != want {
			t.Errorf("%s compact key length = %d, want %d", name, len(test.key), want)
		}
	}

	paramsX := types.NewTuple(types.NewParam(token.NoPos, nil, "x", shared))
	paramsY := types.NewTuple(types.NewParam(token.NoPos, nil, "y", shared))
	if structuralEmissionTypeKey(paramsX) == structuralEmissionTypeKey(paramsY) {
		t.Fatal("strict emission type key erased tuple variable names")
	}
	if structuralEmissionABITypeKey(paramsX) != structuralEmissionABITypeKey(paramsY) {
		t.Fatal("strict ABI type key retained tuple variable names")
	}

	fieldX := types.NewStruct([]*types.Var{
		types.NewField(token.NoPos, nil, "x", types.Typ[types.Uintptr], false),
	}, []string{"json:\"x\""})
	fieldY := types.NewStruct([]*types.Var{
		types.NewField(token.NoPos, nil, "y", types.Typ[types.Uintptr], false),
	}, []string{"json:\"y\""})
	if structuralEmissionABITypeKey(fieldX) == structuralEmissionABITypeKey(fieldY) {
		t.Fatal("strict ABI type key erased struct field metadata")
	}
	if structuralNamedIdentityFreeABITypeKey(fieldX) != structuralNamedIdentityFreeABITypeKey(fieldY) {
		t.Fatal("identity-free ABI type key retained struct field metadata")
	}
}

func TestStructuralEmissionABITypeGraphIgnoresIncidentalPointerSharing(t *testing.T) {
	child := func() types.Type {
		return types.NewStruct([]*types.Var{
			types.NewField(token.NoPos, nil, "value", types.Typ[types.Uintptr], false),
		}, nil)
	}
	sharedChild := child()
	shared := types.NewStruct([]*types.Var{
		types.NewField(token.NoPos, nil, "left", sharedChild, false),
		types.NewField(token.NoPos, nil, "right", sharedChild, false),
	}, nil)
	duplicated := types.NewStruct([]*types.Var{
		types.NewField(token.NoPos, nil, "left", child(), false),
		types.NewField(token.NoPos, nil, "right", child(), false),
	}, nil)
	if structuralEmissionABITypeKey(shared) != structuralEmissionABITypeKey(duplicated) {
		t.Fatal("incidental go/types pointer sharing changed the strict ABI key")
	}
}

func TestEmissionUniverseStructuralTypeKeyCacheIsSessionLocalAndModeSeparated(t *testing.T) {
	typ := testSharedAnonymousEmissionType(12)
	alias := types.NewAlias(types.NewTypeName(token.NoPos, nil, "Alias", nil), typ)
	universe := new(EmissionUniverse)
	tests := []struct {
		mode emissionTypeKeyMode
		want string
	}{
		{mode: emissionTypeKeyStrict, want: structuralEmissionTypeKey(typ)},
		{mode: emissionTypeKeyStrictABI, want: structuralEmissionABITypeKey(typ)},
		{mode: emissionTypeKeyGoLinknameABI, want: structuralGoLinknameABITypeKey(typ)},
		{mode: emissionTypeKeyIdentityFreeABI, want: structuralNamedIdentityFreeABITypeKey(typ)},
	}
	for _, test := range tests {
		if got := universe.cachedStructuralEmissionTypeKey(test.mode, typ); got != test.want {
			t.Fatalf("cached mode %d key = %q, want %q", test.mode, got, test.want)
		}
		if got := universe.cachedStructuralEmissionTypeKey(test.mode, alias); got != test.want {
			t.Fatalf("cached alias mode %d key = %q, want %q", test.mode, got, test.want)
		}
	}
	universe.emissionTypeKeyMu.RLock()
	entries := len(universe.emissionTypeKeys)
	universe.emissionTypeKeyMu.RUnlock()
	if entries != len(tests) {
		t.Fatalf("session cache entries = %d, want one canonical entry per mode (%d)", entries, len(tests))
	}
	other := new(EmissionUniverse)
	if got := other.cachedStrictEmissionABITypeKey(typ); got != tests[1].want {
		t.Fatalf("independent session key = %q, want %q", got, tests[1].want)
	}
	other.emissionTypeKeyMu.RLock()
	otherEntries := len(other.emissionTypeKeys)
	other.emissionTypeKeyMu.RUnlock()
	if otherEntries != 1 {
		t.Fatalf("independent session cache entries = %d, want 1", otherEntries)
	}
}

func TestStructuralGoLinknameABITypeKeyIgnoresIncidentalPointerSharing(t *testing.T) {
	child := func() types.Type {
		return types.NewStruct([]*types.Var{
			types.NewField(token.NoPos, nil, "value", types.Typ[types.Uintptr], false),
		}, nil)
	}
	sharedChild := child()
	shared := types.NewStruct([]*types.Var{
		types.NewField(token.NoPos, nil, "left", sharedChild, false),
		types.NewField(token.NoPos, nil, "right", sharedChild, false),
	}, nil)
	duplicated := types.NewStruct([]*types.Var{
		types.NewField(token.NoPos, nil, "left", child(), false),
		types.NewField(token.NoPos, nil, "right", child(), false),
	}, nil)

	if structuralGoLinknameABITypeKey(shared) != structuralGoLinknameABITypeKey(duplicated) {
		t.Fatal("incidental go/types pointer sharing changed the structural ABI key")
	}
}

func TestStructuralGoLinknameABITypeKeyPreservesRecursiveTopology(t *testing.T) {
	selfPackage := types.NewPackage("example.com/self", "self")
	self := types.NewNamed(types.NewTypeName(token.NoPos, selfPackage, "Self", nil), nil, nil)
	self.SetUnderlying(types.NewPointer(self))

	pairPackage := types.NewPackage("example.com/pair", "pair")
	left := types.NewNamed(types.NewTypeName(token.NoPos, pairPackage, "Left", nil), nil, nil)
	right := types.NewNamed(types.NewTypeName(token.NoPos, pairPackage, "Right", nil), nil, nil)
	left.SetUnderlying(types.NewPointer(right))
	right.SetUnderlying(types.NewPointer(left))

	if structuralGoLinknameABITypeKey(self) == structuralGoLinknameABITypeKey(left) {
		t.Fatal("one-node and two-node recursive ABI graphs acquired the same key")
	}
}

func TestStructuralGoLinknameABITypeGraphPreservesLegacyEqualityClasses(t *testing.T) {
	selfPackage := types.NewPackage("example.com/equality/self", "self")
	self := types.NewNamed(types.NewTypeName(token.NoPos, selfPackage, "Self", nil), nil, nil)
	self.SetUnderlying(types.NewPointer(self))

	pairPackage := types.NewPackage("example.com/equality/pair", "pair")
	pairLeft := types.NewNamed(types.NewTypeName(token.NoPos, pairPackage, "Left", nil), nil, nil)
	pairRight := types.NewNamed(types.NewTypeName(token.NoPos, pairPackage, "Right", nil), nil, nil)
	pairLeft.SetUnderlying(types.NewPointer(pairRight))
	pairRight.SetUnderlying(types.NewPointer(pairLeft))

	named := testSharedGoLinknameType("example.com/equality/named", 6, types.Typ[types.Uintptr])
	typesToCompare := []types.Type{
		named,
		named.Underlying(),
		testSharedGoLinknameType("example.com/equality/mirror", 6, types.Typ[types.Uintptr]),
		testSharedGoLinknameType("example.com/equality/changed", 6, types.Typ[types.Uint64]),
		self,
		pairLeft,
	}
	for left := range typesToCompare {
		for right := range typesToCompare {
			legacyEqual := structuralNamedIdentityFreeABITypeKey(typesToCompare[left]) ==
				structuralNamedIdentityFreeABITypeKey(typesToCompare[right])
			compactEqual := structuralGoLinknameABITypeKey(typesToCompare[left]) ==
				structuralGoLinknameABITypeKey(typesToCompare[right])
			if compactEqual != legacyEqual {
				t.Fatalf("compact equality for type pair %d/%d = %t, legacy %t", left, right, compactEqual, legacyEqual)
			}
		}
	}
}

func BenchmarkStructuralGoLinknameABITypeKeySharedDAG(b *testing.B) {
	typ := testSharedGoLinknameType("example.com/benchmark", 18, types.Typ[types.Uintptr])
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = structuralGoLinknameABITypeKey(typ)
	}
}

func BenchmarkStructuralEmissionABITypeKeySharedDAG(b *testing.B) {
	typ := testSharedAnonymousEmissionType(18)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = structuralEmissionABITypeKey(typ)
	}
}

func BenchmarkEmissionUniverseCachedStructuralEmissionABITypeKeySharedDAG(b *testing.B) {
	typ := testSharedAnonymousEmissionType(18)
	universe := new(EmissionUniverse)
	_ = universe.cachedStrictEmissionABITypeKey(typ)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = universe.cachedStrictEmissionABITypeKey(typ)
	}
}

func testSharedAnonymousEmissionType(depth int) types.Type {
	current := types.Type(types.Typ[types.Uintptr])
	for range depth {
		current = types.NewStruct([]*types.Var{
			types.NewField(token.NoPos, nil, "left", current, false),
			types.NewField(token.NoPos, nil, "right", current, false),
		}, nil)
	}
	return current
}

func testSharedGoLinknameType(path string, depth int, leaf types.Type) types.Type {
	pkg := types.NewPackage(path, "shared")
	current := leaf
	for index := 0; index < depth; index++ {
		underlying := types.NewStruct([]*types.Var{
			types.NewField(token.NoPos, pkg, "left", current, false),
			types.NewField(token.NoPos, pkg, "right", current, false),
		}, nil)
		current = types.NewNamed(
			types.NewTypeName(token.NoPos, pkg, fmt.Sprintf("Level%d", index), nil),
			underlying,
			nil,
		)
	}
	return current
}
