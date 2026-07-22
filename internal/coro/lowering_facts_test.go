//go:build !llgo

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
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
)

func TestSemanticInstructionOrdinalIgnoresDebugRefs(t *testing.T) {
	const source = `package coroid

func target(value int) int {
	value++
	if value > 2 {
		value *= 3
	}
	return value
}
`
	mode := ssa.SanityCheckFunctions | ssa.InstantiateGenerics
	_, plainPackage := buildCoroTestSSAWithMode(t, "facts.go", source, mode)
	_, debugPackage := buildCoroTestSSAWithMode(t, "facts.go", source, mode|ssa.GlobalDebug)
	plain := packageFunction(t, plainPackage, "target")
	debug := packageFunction(t, debugPackage, "target")

	type semanticInstruction struct {
		block    int
		ordinal  int
		typeName string
	}
	collect := func(function *ssa.Function) ([]semanticInstruction, int) {
		var result []semanticInstruction
		debugRefs := 0
		instance, err := NewEmissionInstanceID(FunctionID("test.target"), "example.test/coroid", "test-context-v0")
		if err != nil {
			t.Fatal(err)
		}
		for _, block := range function.Blocks {
			semantic := 0
			for _, instruction := range block.Instrs {
				if _, isDebug := instruction.(*ssa.DebugRef); isDebug {
					debugRefs++
					if _, err := SemanticInstructionOrdinal(instruction); err == nil {
						t.Fatal("DebugRef unexpectedly acquired a semantic ordinal")
					}
					continue
				}
				ordinal, err := SemanticInstructionOrdinal(instruction)
				if err != nil {
					t.Fatal(err)
				}
				if ordinal != semantic {
					t.Fatalf("block %d semantic ordinal = %d, want %d", block.Index, ordinal, semantic)
				}
				site, err := NewInstructionEmissionSiteID(instance, instruction, RolePrimary, 0)
				if err != nil {
					t.Fatal(err)
				}
				if site.Source.Block != block.Index || site.Source.Instruction != semantic {
					t.Fatalf("instruction site = %+v, want block %d instruction %d", site.Source, block.Index, semantic)
				}
				result = append(result, semanticInstruction{block.Index, semantic, fmt.Sprintf("%T", instruction)})
				semantic++
			}
		}
		return result, debugRefs
	}
	plainInstructions, _ := collect(plain)
	debugInstructions, debugRefs := collect(debug)
	if debugRefs == 0 {
		t.Fatal("GlobalDebug target has no DebugRef instructions")
	}
	if !reflect.DeepEqual(plainInstructions, debugInstructions) {
		t.Fatalf("DebugRef changed semantic instruction anchors:\nplain %+v\ndebug %+v", plainInstructions, debugInstructions)
	}
}

func TestEmissionIDsAreStructuralAndValidateAnchors(t *testing.T) {
	instance, err := NewEmissionInstanceID(FunctionID("test.function"), "owner/test", "context-v0")
	if err != nil {
		t.Fatal(err)
	}
	assertPointerFreeIDType(t, reflect.TypeOf(instance))
	assertPointerFreeIDType(t, reflect.TypeOf(EmissionSiteID{}))

	block, err := NewBlockEntryEmissionSiteID(instance, 3, RolePoll, 0)
	if err != nil {
		t.Fatal(err)
	}
	edge, err := NewEdgeEmissionSiteID(instance, 3, 1, RolePoll, 1)
	if err != nil {
		t.Fatal(err)
	}
	function, err := NewFunctionEmissionSiteID(instance, RolePrimary, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, site := range []EmissionSiteID{block, edge, function} {
		if err := site.Validate(); err != nil {
			t.Fatalf("valid site %+v: %v", site, err)
		}
	}
	bad := block
	bad.Source.Function = FunctionID("other.function")
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatched source function error = %v", err)
	}
	bad = block
	bad.Source.Instruction = 0
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "noncanonical coordinates") {
		t.Fatalf("noncanonical block-entry error = %v", err)
	}
	if _, err := NewEmissionInstanceID("", "owner", "context"); err == nil {
		t.Fatal("empty logical function unexpectedly accepted")
	}
	if _, err := NewEmissionInstanceID(FunctionID("fn"), "", "context"); err == nil {
		t.Fatal("empty owner unexpectedly accepted")
	}
}

func assertPointerFreeIDType(t *testing.T, typ reflect.Type) {
	t.Helper()
	var visit func(reflect.Type)
	visit = func(current reflect.Type) {
		switch current.Kind() {
		case reflect.Pointer, reflect.UnsafePointer, reflect.Map, reflect.Slice, reflect.Interface, reflect.Func, reflect.Chan:
			t.Fatalf("ID type %v contains pointer-bearing %v", typ, current)
		case reflect.Struct:
			for index := 0; index < current.NumField(); index++ {
				visit(current.Field(index).Type)
			}
		}
	}
	visit(typ)
}

func TestLoweringFactsCanonicalJSONAndDigest(t *testing.T) {
	first := validLoweringFacts(t)
	second := cloneLoweringFacts(first)
	second.Functions[0], second.Functions[1] = second.Functions[1], second.Functions[0]
	for index := range second.Functions {
		if len(second.Functions[index].Sites) == 2 {
			second.Functions[index].Sites[0], second.Functions[index].Sites[1] = second.Functions[index].Sites[1], second.Functions[index].Sites[0]
		}
	}
	use := &second.Functions[0].Sites[1].FunctionUses[0]
	use.Targets[0], use.Targets[1] = use.Targets[1], use.Targets[0]

	firstJSON, err := first.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := second.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("worklist/set order changed canonical facts:\n%s\n%s", firstJSON, secondJSON)
	}
	if first.Functions[0].Instance.Function != FunctionID("test.z") {
		t.Fatal("CanonicalJSON mutated its input function order")
	}

	var decoded LoweringFacts
	if err := json.Unmarshal(firstJSON, &decoded); err != nil {
		t.Fatalf("canonical facts cannot be parsed: %v\n%s", err, firstJSON)
	}
	if len(decoded.Functions[1].Sites) != 0 || decoded.Functions[1].Sites == nil {
		t.Fatalf("canonical empty sparse site list = %#v, want non-nil empty", decoded.Functions[1].Sites)
	}
	call := decoded.Functions[0].Sites[1]
	if len(call.Helpers) != 2 || call.Helpers[0].Target != call.Helpers[1].Target {
		t.Fatalf("exact repeated helper edges were folded: %+v", call.Helpers)
	}
	if got := call.FunctionUses[0].Targets; !reflect.DeepEqual(got, []FunctionID{"test.a", "test.z"}) {
		t.Fatalf("canonical function-value targets = %v", got)
	}

	digest, err := first.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if len(digest) != sha256.Size*2 {
		t.Fatalf("digest length = %d", len(digest))
	}
	if _, err := hex.DecodeString(digest); err != nil {
		t.Fatalf("digest is not hexadecimal: %v", err)
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(LoweringFactsDigestDomain))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(firstJSON)
	if want := hex.EncodeToString(hash.Sum(nil)); digest != want {
		t.Fatalf("digest = %s, want domain-separated %s", digest, want)
	}
	plain := sha256.Sum256(firstJSON)
	if digest == hex.EncodeToString(plain[:]) {
		t.Fatal("lowering facts digest is not domain-separated")
	}
	mutated := cloneLoweringFacts(first)
	mutated.Functions[0].LocalExec = NeedsPreempt
	if other, err := mutated.Digest(); err != nil || other == digest {
		t.Fatalf("fact mutation digest = %q, %v; want a different digest", other, err)
	}
}

func TestLoweringFactsVerifierRejectsMalformedFacts(t *testing.T) {
	tests := []struct {
		name string
		want string
		edit func(*LoweringFacts)
	}{
		{"schema", "schema", func(facts *LoweringFacts) { facts.Schema = "old" }},
		{"duplicate instance", "duplicate lowering function", func(facts *LoweringFacts) { facts.Functions = append(facts.Functions, facts.Functions[0]) }},
		{"duplicate site", "duplicate lowering site", func(facts *LoweringFacts) {
			facts.Functions[1].Sites = append(facts.Functions[1].Sites, facts.Functions[1].Sites[0])
		}},
		{"wrong container", "does not match containing instance", func(facts *LoweringFacts) { facts.Functions[1].Sites[0].Site.Instance.Owner = "other-owner" }},
		{"missing local effect", "does not cover site effect", func(facts *LoweringFacts) { facts.Functions[1].LocalEffect = NoSuspend }},
		{"empty recipe", "empty lowering recipe", func(facts *LoweringFacts) { facts.Functions[1].Sites[0].Recipe = "" }},
		{"nonnormal effect", "not normalized", func(facts *LoweringFacts) {
			facts.Functions[1].Sites[0].Effect = WaitHost
			facts.Functions[1].LocalEffect = WaitHost.Normalize()
		}},
		{"missing suspend footprint", "suspend effect", func(facts *LoweringFacts) { facts.Functions[1].Sites[0].Footprint &^= FootprintSuspend }},
		{"missing managed footprint", "managed helpers", func(facts *LoweringFacts) { facts.Functions[1].Sites[0].Footprint &^= FootprintManagedCall }},
		{"helper order", "has order", func(facts *LoweringFacts) { facts.Functions[1].Sites[0].Helpers[1].Order = 4 }},
		{"duplicate helper role", "duplicate managed helper", func(facts *LoweringFacts) { facts.Functions[1].Sites[0].Helpers[1].Ordinal = 0 }},
		{"empty helper target", "empty function ID", func(facts *LoweringFacts) { facts.Functions[1].Sites[0].Helpers[0].Target = "" }},
		{"duplicate value target", "duplicate target", func(facts *LoweringFacts) {
			facts.Functions[1].Sites[0].FunctionUses[0].Targets[1] = facts.Functions[1].Sites[0].FunctionUses[0].Targets[0]
		}},
		{"pure helper", "pure lowering site", func(facts *LoweringFacts) { facts.Functions[1].Sites[0].Class = OpPure }},
		{"bad coordinates", "noncanonical coordinates", func(facts *LoweringFacts) { facts.Functions[1].Sites[0].Site.Source.Successor = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			facts := cloneLoweringFacts(validLoweringFacts(t))
			test.edit(&facts)
			err := facts.Verify()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Verify error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestLoweringFactsVerifierAllowsSparseFunctions(t *testing.T) {
	facts := validLoweringFacts(t)
	if len(facts.Functions[0].Sites) != 0 {
		t.Fatal("fixture is not sparse")
	}
	if err := facts.Verify(); err != nil {
		t.Fatalf("sparse facts: %v", err)
	}
}

func validLoweringFacts(t *testing.T) LoweringFacts {
	t.Helper()
	instanceZ, err := NewEmissionInstanceID(FunctionID("test.z"), "owner/z", "context-v0")
	if err != nil {
		t.Fatal(err)
	}
	instanceA, err := NewEmissionInstanceID(FunctionID("test.a"), "owner/a", "context-v0")
	if err != nil {
		t.Fatal(err)
	}
	callSite, err := NewFunctionEmissionSiteID(instanceA, RoleCall, 0)
	if err != nil {
		t.Fatal(err)
	}
	pollSite, err := NewBlockEntryEmissionSiteID(instanceA, 2, RolePoll, 0)
	if err != nil {
		t.Fatal(err)
	}
	return NewLoweringFacts([]FunctionLoweringFacts{
		{Instance: instanceZ, Sites: []LoweringFact{}},
		{
			Instance:    instanceA,
			LocalEffect: MayPark,
			Sites: []LoweringFact{
				{
					Site:      callSite,
					Class:     OpCall,
					Recipe:    RecipeID("call.park.v0"),
					Effect:    MayPark,
					Footprint: FootprintManagedCall | FootprintSuspend | FootprintPanic,
					Helpers: []ManagedEdge{
						{Order: 0, Role: RoleHelper, Ordinal: 0, LogicalName: "runtime.park", Target: FunctionID("test.helper")},
						{Order: 1, Role: RoleHelper, Ordinal: 1, LogicalName: "runtime.park", Target: FunctionID("test.helper")},
					},
					ImplicitPanic: []ImplicitPanicFact{{Order: 0, Role: RolePanic, Kind: "nil"}},
					FunctionUses:  []FunctionValueFact{{Order: 0, Role: RoleFunctionValue, Targets: []FunctionID{"test.z", "test.a"}}},
					Contract:      ContractID("park-region.v0"),
				},
				{
					Site:      pollSite,
					Class:     OpLowered,
					Recipe:    RecipeID("poll.check.v0"),
					Footprint: FootprintBarrier,
					Helpers:   []ManagedEdge{}, ImplicitPanic: []ImplicitPanicFact{}, FunctionUses: []FunctionValueFact{},
				},
			},
		},
	})
}

func cloneLoweringFacts(facts LoweringFacts) LoweringFacts {
	clone := LoweringFacts{Schema: facts.Schema, Functions: append([]FunctionLoweringFacts(nil), facts.Functions...)}
	for functionIndex := range clone.Functions {
		function := &clone.Functions[functionIndex]
		function.Sites = append([]LoweringFact(nil), function.Sites...)
		for siteIndex := range function.Sites {
			fact := &function.Sites[siteIndex]
			fact.Helpers = append([]ManagedEdge(nil), fact.Helpers...)
			fact.ImplicitPanic = append([]ImplicitPanicFact(nil), fact.ImplicitPanic...)
			fact.FunctionUses = append([]FunctionValueFact(nil), fact.FunctionUses...)
			for useIndex := range fact.FunctionUses {
				fact.FunctionUses[useIndex].Targets = append([]FunctionID(nil), fact.FunctionUses[useIndex].Targets...)
			}
		}
	}
	return clone
}
