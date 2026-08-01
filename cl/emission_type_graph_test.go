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
