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
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
)

const planDigestTestSource = `package coroid

type holder struct { callback func() }

func plain() {}
func alternate() {}
func consume(value holder) {
	if value.callback != nil { value.callback() }
}
func root(flag bool) {
	callback := plain
	if flag { callback = alternate }
	callback()
	consume(holder{callback: callback})
}
`

func TestCoroPlanDigestDeterministicCompleteAndDomainSeparated(t *testing.T) {
	plainPlan, _ := buildPlanDigestTestPlan(t, ssa.SanityCheckFunctions|ssa.InstantiateGenerics)
	debugPlan, debugPackage := buildPlanDigestTestPlan(t, ssa.SanityCheckFunctions|ssa.InstantiateGenerics|ssa.GlobalDebug)
	metadata := validPlanDigestMetadata()

	debugRefs := 0
	for _, function := range debugPlan.functions {
		for _, block := range function.Function.Blocks {
			for _, instruction := range block.Instrs {
				if _, debug := instruction.(*ssa.DebugRef); debug {
					debugRefs++
				}
			}
		}
	}
	if debugRefs == 0 {
		t.Fatalf("GlobalDebug package %q has no DebugRef instructions", debugPackage.Pkg.Path())
	}

	plainDigest, err := plainPlan.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	debugDigest, err := debugPlan.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if plainDigest != debugDigest {
		t.Fatalf("DebugRef instructions changed CoroPlanDigest:\nplain %s\ndebug %s", plainDigest, debugDigest)
	}
	if len(plainDigest) != sha256.Size*2 {
		t.Fatalf("digest length = %d, want %d", len(plainDigest), sha256.Size*2)
	}
	if _, err := hex.DecodeString(plainDigest); err != nil {
		t.Fatalf("digest is not lowercase hexadecimal: %v", err)
	}
	if plainPlan.functionIDs.CoroABI != metadata.CoroABI || plainPlan.functionIDs.SchedulerABI != metadata.SchedulerABI || !plainPlan.functionIDs.ArchiveReady {
		t.Fatalf("SSAPlan lost normalized FunctionID configuration: %+v", plainPlan.functionIDs)
	}

	document, err := plainPlan.canonicalPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if document.Schema != PlanDigestSchema || document.FunctionIDSchema != FunctionIDSchema {
		t.Fatalf("digest schemas = %q, %q", document.Schema, document.FunctionIDSchema)
	}
	if len(document.Functions) != len(plainPlan.functions) {
		t.Fatalf("function records = %d, want %d", len(document.Functions), len(plainPlan.functions))
	}
	for index, function := range document.Functions {
		if function.Emission != uint8(plainPlan.functions[index].Plan.Emission) {
			t.Fatalf("function %q digest emission = %d, want %s", function.ID, function.Emission, plainPlan.functions[index].Plan.Emission)
		}
	}
	if len(document.Roots) != len(plainPlan.roots) || len(document.Roots) == 0 {
		t.Fatalf("root records = %d, plan roots = %d", len(document.Roots), len(plainPlan.roots))
	}
	if len(document.Calls) != len(plainPlan.callPlans) || len(document.Calls) == 0 {
		t.Fatalf("call records = %d, map plans = %d", len(document.Calls), len(plainPlan.callPlans))
	}
	if len(document.Values) < len(plainPlan.valuePlans) || len(document.Values) == 0 {
		t.Fatalf("value projections = %d, map plans = %d", len(document.Values), len(plainPlan.valuePlans))
	}
	foundOperand := false
	foundAggregatePath := false
	for _, value := range document.Values {
		foundOperand = foundOperand || value.Site.Kind == "operand"
		for _, leaf := range value.Funcs {
			foundAggregatePath = foundAggregatePath || len(leaf.Path) != 0
			if leaf.Targets == nil {
				t.Fatal("canonical target list is nil")
			}
		}
	}
	if !foundOperand {
		t.Fatal("digest did not project a definition-less value by operand occurrence")
	}
	if !foundAggregatePath {
		t.Fatal("digest did not cover an aggregate function-value path")
	}

	payload, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	raw := sha256.Sum256(payload)
	if plainDigest == hex.EncodeToString(raw[:]) {
		t.Fatal("CoroPlanDigest omitted domain separation")
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(PlanDigestSchema))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(payload)
	if want := hex.EncodeToString(hash.Sum(nil)); plainDigest != want {
		t.Fatalf("digest = %s, want domain-separated %s", plainDigest, want)
	}
}

func TestCoroPlanDigestCanonicalizesSyntheticDependencyInitOrder(t *testing.T) {
	prog, pkg := buildPlanDigestDependencyInitSSA(t)
	root := packageFunction(t, pkg, "init")
	config := planDigestSSAConfig()
	config.MaxPlainInstructions = -1
	config.FunctionIDs.ResolveLinkIdentity = func(fn *ssa.Function) (string, error) {
		if fn == nil || fn.Pkg == nil || fn.Pkg.Pkg == nil || fn.Name() == "" {
			return "", fmt.Errorf("missing dependency-init test link identity")
		}
		return fn.Pkg.Pkg.Path() + "." + fn.Name(), nil
	}
	canonicalPackageKey := config.FunctionIDs.CanonicalPackageKey
	config.FunctionIDs.CanonicalPackageKey = func(pkg *types.Package) (string, error) {
		if strings.HasPrefix(pkg.Path(), "example.test/dependency/") {
			return "example.test/shared-emission-package", nil
		}
		return canonicalPackageKey(pkg)
	}
	plan, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: SyncDemand}}, config)
	if err != nil {
		t.Fatal(err)
	}

	var block *ssa.BasicBlock
	var slots []int
	for _, candidate := range root.Blocks {
		var candidateSlots []int
		for index, instruction := range candidate.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok || call.Common() == nil || call.Common().IsInvoke() {
				continue
			}
			target := call.Common().StaticCallee()
			if isDigestPackageInitializer(target) && target.Pkg != root.Pkg {
				candidateSlots = append(candidateSlots, index)
			}
		}
		if len(candidateSlots) >= 2 {
			block, slots = candidate, candidateSlots
			break
		}
	}
	if block == nil {
		t.Fatal("fixture has fewer than two direct dependency initializer calls")
	}

	metadata := validPlanDigestMetadata()
	baselineDigest, err := plan.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	baselineDocument, err := plan.canonicalPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}

	first, second := slots[0], slots[len(slots)-1]
	originalFirst, originalSecond := block.Instrs[first], block.Instrs[second]
	block.Instrs[first], block.Instrs[second] = block.Instrs[second], block.Instrs[first]
	t.Cleanup(func() {
		block.Instrs[first], block.Instrs[second] = originalFirst, originalSecond
	})
	permutedDigest, err := plan.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	permutedDocument, err := plan.canonicalPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if permutedDigest != baselineDigest {
		t.Fatalf("dependency initializer order changed plan digest:\nbaseline %s\npermuted %s", baselineDigest, permutedDigest)
	}
	if !reflect.DeepEqual(permutedDocument, baselineDocument) {
		t.Fatal("dependency initializer order changed canonical plan document")
	}

	block.Instrs[first], block.Instrs[second] = originalFirst, originalSecond
	firstCall, firstOK := originalFirst.(*ssa.Call)
	secondCall, secondOK := originalSecond.(*ssa.Call)
	if !firstOK || !secondOK {
		t.Fatalf("dependency initializer instructions have types %T and %T, want *ssa.Call", originalFirst, originalSecond)
	}
	originalSecondTarget := secondCall.Common().Value
	t.Cleanup(func() { secondCall.Common().Value = originalSecondTarget })
	secondCall.Common().Value = firstCall.Common().Value
	if _, err := plan.CoroPlanDigest(metadata); err == nil ||
		!strings.Contains(err.Error(), "more than once") {
		t.Fatalf("duplicate dependency initializer digest error = %v", err)
	}
	secondCall.Common().Value = originalSecondTarget
}

type planDigestPackageImporter map[string]*types.Package

func (p planDigestPackageImporter) Import(path string) (*types.Package, error) {
	pkg, ok := p[path]
	if !ok {
		return nil, fmt.Errorf("unexpected test import %q", path)
	}
	return pkg, nil
}

func buildPlanDigestDependencyInitSSA(t *testing.T) (*ssa.Program, *ssa.Package) {
	t.Helper()
	fset := token.NewFileSet()
	mode := ssa.SanityCheckFunctions | ssa.InstantiateGenerics
	type checkedPackage struct {
		pkg   *types.Package
		files []*ast.File
		info  *types.Info
	}
	check := func(path, filename, source string, imports planDigestPackageImporter) checkedPackage {
		t.Helper()
		file, err := parser.ParseFile(fset, filename, source, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		info := &types.Info{
			Types:      make(map[ast.Expr]types.TypeAndValue),
			Defs:       make(map[*ast.Ident]types.Object),
			Uses:       make(map[*ast.Ident]types.Object),
			Implicits:  make(map[ast.Node]types.Object),
			Instances:  make(map[*ast.Ident]types.Instance),
			Selections: make(map[*ast.SelectorExpr]*types.Selection),
			Scopes:     make(map[ast.Node]*types.Scope),
		}
		pkg, err := (&types.Config{Importer: imports}).Check(path, fset, []*ast.File{file}, info)
		if err != nil {
			t.Fatalf("check %s: %v", path, err)
		}
		return checkedPackage{pkg: pkg, files: []*ast.File{file}, info: info}
	}

	first := check("example.test/dependency/first", "first.go", `package first
var Value int
func init() { Value = 1 }
`, nil)
	second := check("example.test/dependency/second", "second.go", `package second
var Value int
func init() { Value = 2 }
`, nil)
	root := check("example.test/coroid", "root.go", `package coroid
import (
	_ "example.test/dependency/first"
	_ "example.test/dependency/second"
)
`, planDigestPackageImporter{
		first.pkg.Path():  first.pkg,
		second.pkg.Path(): second.pkg,
	})

	prog := ssa.NewProgram(fset, mode)
	firstSSA := prog.CreatePackage(first.pkg, first.files, first.info, true)
	secondSSA := prog.CreatePackage(second.pkg, second.files, second.info, true)
	rootSSA := prog.CreatePackage(root.pkg, root.files, root.info, true)
	firstSSA.Build()
	secondSSA.Build()
	rootSSA.Build()
	return prog, rootSSA
}

func TestCoroPlanDigestRecordsWholeBuildRawPlainVariant(t *testing.T) {
	if PlanDigestSchema != "llgo.coro.plan-digest.v31" {
		t.Fatalf("plan digest schema = %q, want managed-entry schema v31", PlanDigestSchema)
	}
	prog, pkg := buildCoroTestSSA(t, "raw_variant_digest.go", `package coroid
func root(seed int) int {
	callback := func(value int) int { return seed + value }
	return callback(1)
}
`)
	root := packageFunction(t, pkg, "root")
	captured := root.AnonFuncs[0]
	build := func(withVariant bool) *SSAPlan {
		t.Helper()
		config := planDigestSSAConfig()
		config.MaxPlainInstructions = -1
		config.ClassifyFunction = func(fn *ssa.Function) (SSAFunctionPolicy, error) {
			if withVariant && fn == captured {
				return SSAFunctionPolicy{RawPlainVariant: true}, nil
			}
			return SSAFunctionPolicy{}, nil
		}
		plan, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: SyncDemand}}, config)
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
	without := build(false)
	with := build(true)
	metadata := validPlanDigestMetadata()
	withoutDigest, err := without.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	withDigest, err := with.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if withDigest == withoutDigest {
		t.Fatal("raw plain variant capability is absent from CoroPlanDigest")
	}
	document, err := with.canonicalPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	capturedID, ok := with.FunctionID(captured)
	if !ok {
		t.Fatal("captured function has no plan identity")
	}
	found := false
	for _, function := range document.Functions {
		if function.ID == capturedID {
			found = function.RawPlainVariant && !function.RawPlainEntry
			break
		}
	}
	if !found {
		t.Fatalf("canonical digest did not record captured internal raw variant: %+v", document.Functions)
	}
}

func TestCoroPlanDigestFreezesExactInterfaceReceiver(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "exact_interface_digest.go", `package coroid
type Interface interface { Method() int }
type Concrete struct{}
func (Concrete) Method() int { return 1 }
func root() int {
	var value Interface = Concrete{}
	return value.Method()
}
`)
	root := packageFunction(t, pkg, "root")
	config := planDigestSSAConfig()
	config.DynamicResolution = DynamicCHAClosed
	config.MaxPlainInstructions = -1
	config.OutcomeMode = OutcomeExplicitStatus
	plan, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: AsyncDemand}}, config)
	if err != nil {
		t.Fatal(err)
	}

	var invoke *ssa.Call
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if ok && call.Common() != nil && call.Common().IsInvoke() {
				invoke = call
			}
		}
	}
	if invoke == nil {
		t.Fatal("fixture has no interface invoke")
	}
	if _, _, _, exact, err := plan.ResolveExactInterfaceCall(invoke); err != nil || !exact {
		t.Fatalf("exact interface receiver = %t, err = %v", exact, err)
	}

	metadata := validPlanDigestMetadata()
	baseline, err := plan.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	document, err := plan.canonicalPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	frozen := false
	for _, call := range document.Calls {
		if call.ExactInterfaceReceiver {
			frozen = true
			break
		}
	}
	if !frozen {
		t.Fatal("canonical plan digest omitted the exact interface receiver")
	}

	receiver := plan.exactInterfaceReceivers[invoke]
	delete(plan.exactInterfaceReceivers, invoke)
	without, err := plan.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if without == baseline {
		t.Fatal("removing the exact interface receiver did not change CoroPlanDigest")
	}
	plan.exactInterfaceReceivers[invoke] = receiver
	restored, err := plan.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if restored != baseline {
		t.Fatalf("restored exact interface digest = %s, want %s", restored, baseline)
	}
}

func TestCoroPlanDigestFreezesExactSafeFixedArrayIndexSites(t *testing.T) {
	const source = `package coroid
var values = [...]int{1, 2, 3, 4}
func safe() int {
	total := 0
	for index := range values { total += values[index] }
	return total
}
func unsafe(index int) int { return values[index] }
`
	build := func(mode ssa.BuilderMode) (*SSAPlan, *ssa.Package) {
		t.Helper()
		prog, pkg := buildCoroTestSSAWithMode(t, "safe_array_digest.go", source, mode)
		plan, err := AnalyzeSSA(prog, Roots{
			{Function: packageFunction(t, pkg, "safe"), Demand: SyncDemand},
			{Function: packageFunction(t, pkg, "unsafe"), Demand: SyncDemand},
		}, planDigestSSAConfig())
		if err != nil {
			t.Fatal(err)
		}
		return plan, pkg
	}

	plain, pkg := build(ssa.SanityCheckFunctions | ssa.InstantiateGenerics)
	debug, _ := build(ssa.SanityCheckFunctions | ssa.InstantiateGenerics | ssa.GlobalDebug)
	metadata := validPlanDigestMetadata()
	plainDigest, err := plain.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	debugDigest, err := debug.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if debugDigest != plainDigest {
		t.Fatalf("DebugRef instructions changed safe fixed-array site identity:\nplain %s\ndebug %s", plainDigest, debugDigest)
	}

	document, err := plain.canonicalPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if len(document.SafeArrayIndexes) != len(plain.safeFixedArrayIndexes) || len(document.SafeArrayIndexes) == 0 {
		t.Fatalf("safe fixed-array digest sites = %d, frozen facts = %d", len(document.SafeArrayIndexes), len(plain.safeFixedArrayIndexes))
	}
	safeFunction := packageFunction(t, pkg, "safe")
	safeID, ok := plain.FunctionID(safeFunction)
	if !ok {
		t.Fatal("safe fixture has no function ID")
	}
	digestSafeSites := 0
	for _, site := range document.SafeArrayIndexes {
		if site.Function == safeID {
			digestSafeSites++
			if site.Bound != 4 {
				t.Fatalf("safe function digest bound = %d, want 4", site.Bound)
			}
		}
	}
	if digestSafeSites != 1 {
		t.Fatalf("safe function digest sites = %d, want 1; all=%+v", digestSafeSites, document.SafeArrayIndexes)
	}
	var safeSite ssa.Instruction
	for instruction, bound := range plain.safeFixedArrayIndexes {
		if instruction.Parent() != safeFunction {
			continue
		}
		safeSite = instruction
		if bound != 4 {
			t.Fatalf("safe site bound = %d, want 4", bound)
		}
		if got, ok := plain.ExactSafeFixedArrayIndex(instruction); !ok || got != bound {
			t.Fatalf("ExactSafeFixedArrayIndex = (%d, %t), want (%d, true)", got, ok, bound)
		}
	}
	if safeSite == nil {
		t.Fatal("safe fixture has no frozen index site")
	}

	delete(plain.safeFixedArrayIndexes, safeSite)
	withoutSite, err := plain.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if withoutSite == plainDigest {
		t.Fatal("removing one safe fixed-array site did not change CoroPlanDigest")
	}
	plain.safeFixedArrayIndexes[safeSite] = 5
	if _, err := plain.CoroPlanDigest(metadata); err == nil ||
		!strings.Contains(err.Error(), "no longer has its exact bound proof") {
		t.Fatalf("forged safe fixed-array bound digest error = %v", err)
	}
	plain.safeFixedArrayIndexes[safeSite] = 4

	unsafeFunction := packageFunction(t, pkg, "unsafe")
	var unsafeSite ssa.Instruction
	for _, block := range unsafeFunction.Blocks {
		for _, instruction := range block.Instrs {
			switch instruction.(type) {
			case *ssa.Index, *ssa.IndexAddr:
				unsafeSite = instruction
			}
		}
	}
	if unsafeSite == nil {
		t.Fatal("unsafe fixture has no index instruction")
	}
	plain.safeFixedArrayIndexes[unsafeSite] = 4
	if _, err := plain.CoroPlanDigest(metadata); err == nil ||
		!strings.Contains(err.Error(), "no longer has its exact bound proof") {
		t.Fatalf("forged unsafe fixed-array site digest error = %v", err)
	}
}

func TestCoroPlanDigestIncludesExactElidedCallCertificate(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "elided_call_certificate_digest.go", `package coroid
func intrinsic()
func root() { intrinsic() }
`)
	root := packageFunction(t, pkg, "root")
	var exactCall ssa.CallInstruction
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			if call, ok := instruction.(ssa.CallInstruction); ok {
				exactCall = call
			}
		}
	}
	if exactCall == nil {
		t.Fatal("fixture has no exact call")
	}
	build := func(certificate string) *SSAPlan {
		config := planDigestSSAConfig()
		config.MaxPlainInstructions = -1
		config.ClassifyElidedCall = func(_ *ssa.Function, call ssa.CallInstruction) (bool, error) {
			return call == exactCall, nil
		}
		config.ClassifyElidedCallCertificate = func(_ *ssa.Function, call ssa.CallInstruction) (string, error) {
			if call == exactCall {
				return certificate, nil
			}
			return "", nil
		}
		plan, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: SyncDemand}}, config)
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
	first := build("worker-target-certificate-A")
	second := build("worker-target-certificate-B")
	metadata := validPlanDigestMetadata()
	firstDigest, err := first.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := second.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest == secondDigest {
		t.Fatal("elided-call certificate is absent from CoroPlanDigest")
	}
	document, err := first.canonicalPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if len(document.ElidedCalls) != 1 || document.ElidedCalls[0].Certificate != "worker-target-certificate-A" {
		t.Fatalf("canonical elided-call certificate = %+v", document.ElidedCalls)
	}
}

func TestCoroPlanDigestRecordsConditionalManagedStoreOccurrence(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "conditional_store_digest.go", `package coroid
var slot func()
func target() {}
func other() {}
func publish() { slot = target }
`)
	publish := packageFunction(t, pkg, "publish")
	target := packageFunction(t, pkg, "target")
	other := packageFunction(t, pkg, "other")
	var publication *ssa.Store
	for _, block := range publish.Blocks {
		for _, instruction := range block.Instrs {
			if store, ok := instruction.(*ssa.Store); ok && store.Val == target {
				publication = store
			}
		}
	}
	if publication == nil {
		t.Fatal("publish has no exact target Store")
	}
	config := planDigestSSAConfig()
	config.MaxPlainInstructions = -1
	config.ClassifyConditionalManagedStoreReference = func(owner *ssa.Function, store *ssa.Store) (*ssa.Function, bool, error) {
		if owner == publish && store == publication {
			return target, true, nil
		}
		return nil, false, nil
	}
	plan, err := AnalyzeSSA(prog, Roots{{Function: publish, Demand: AsyncDemand}}, config)
	if err != nil {
		t.Fatal(err)
	}
	document, err := plan.canonicalPlanDigest(validPlanDigestMetadata())
	if err != nil {
		t.Fatal(err)
	}
	if len(document.ConditionalStores) != 1 || document.ConditionalStores[0].Target == "" ||
		!document.ConditionalStores[0].Elided {
		t.Fatalf("conditional Store digest facts = %+v", document.ConditionalStores)
	}
	baseline, err := plan.CoroPlanDigest(validPlanDigestMetadata())
	if err != nil {
		t.Fatal(err)
	}
	stable, err := plan.CoroPlanDigest(validPlanDigestMetadata())
	if err != nil || stable != baseline {
		t.Fatalf("conditional Store digest is unstable: %q, %v, want %q", stable, err, baseline)
	}
	delete(plan.conditionalStores, publication)
	withoutOccurrence, err := plan.CoroPlanDigest(validPlanDigestMetadata())
	if err != nil {
		t.Fatal(err)
	}
	if withoutOccurrence == baseline {
		t.Fatal("removing conditional Store occurrence did not change digest")
	}
	plan.conditionalStores[publication] = other
	if _, err := plan.CoroPlanDigest(validPlanDigestMetadata()); err == nil ||
		!strings.Contains(err.Error(), "no longer carries its exact target") {
		t.Fatalf("retargeted conditional Store digest error = %v", err)
	}
}

func TestCoroPlanDigestSeparatesManagedAndRawPlainDemand(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "raw_plain_demand_digest.go", `package coroid
func helper() {}
func root() { helper() }
`)
	root := packageFunction(t, pkg, "root")
	config := planDigestSSAConfig()
	config.MaxPlainInstructions = -1
	managed, err := AnalyzeSSA(prog, Roots{{Function: root, ManagedDemand: SyncDemand}}, config)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := AnalyzeSSA(prog, Roots{{Function: root, RawPlainDemand: true}}, config)
	if err != nil {
		t.Fatal(err)
	}
	metadata := validPlanDigestMetadata()
	managedDigest, err := managed.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	rawDigest, err := raw.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if managedDigest == rawDigest {
		t.Fatal("managed and raw-only entry provenance share a plan digest")
	}
	document, err := raw.canonicalPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Roots) != 1 || document.Roots[0].ManagedDemand != 0 || !document.Roots[0].RawPlainDemand {
		t.Fatalf("raw digest roots = %+v", document.Roots)
	}
	want := make(map[FunctionID]bool)
	for _, fn := range []*ssa.Function{root, packageFunction(t, pkg, "helper")} {
		id, ok := raw.FunctionID(fn)
		if !ok {
			t.Fatalf("raw function %s has no ID", fn.Name())
		}
		want[id] = true
	}
	for _, function := range document.Functions {
		if !want[function.ID] {
			continue
		}
		if !function.RawPlainDemand || !function.RawPlainOnly || function.Emission != uint8(EmitRawPlain) {
			t.Fatalf("raw digest function = %+v", function)
		}
		delete(want, function.ID)
	}
	if len(want) != 0 {
		t.Fatalf("raw digest omitted functions %v", want)
	}
}

func TestCoroPlanDigestFrameRetentionIdentityIsExactAndDomainSeparated(t *testing.T) {
	prog, pkg := buildCoroTestSSAWithMode(
		t, "frame_retention_digest.go", planDigestTestSource,
		ssa.SanityCheckFunctions|ssa.InstantiateGenerics,
	)
	root := packageFunction(t, pkg, "root")
	config := planDigestSSAConfig()
	config.FunctionIDs.CoroABI = PhysicalABIV1
	config.FunctionIDs.SchedulerABI = SchedulerProgramBootstrapABIV2
	plan, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: AsyncDemand}}, config)
	if err != nil {
		t.Fatal(err)
	}
	metadata := validPlanDigestMetadata()
	metadata.CoroABI = PhysicalABIV1
	metadata.SchedulerABI = SchedulerProgramBootstrapABIV2

	withoutRetention, err := plan.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	metadata.FrameRetentionABI = FrameRetentionParkABIV2
	withRetention, err := plan.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if withRetention == withoutRetention {
		t.Fatal("frame-retention ABI identity is absent from CoroPlanDigest")
	}
	document, err := plan.canonicalPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if document.Metadata.FrameRetentionABI != FrameRetentionParkABIV2 {
		t.Fatalf("canonical frame-retention ABI = %q, want %q", document.Metadata.FrameRetentionABI, FrameRetentionParkABIV2)
	}
	unknown := metadata
	unknown.FrameRetentionABI += ".unknown"
	if _, err := plan.CoroPlanDigest(unknown); err == nil || !strings.Contains(err.Error(), "unknown frame-retention ABI") {
		t.Fatalf("unknown frame-retention ABI error = %v", err)
	}
	wrongPhysical := metadata
	wrongPhysical.CoroABI = PhysicalABIV0
	if _, err := plan.CoroPlanDigest(wrongPhysical); err == nil || !strings.Contains(err.Error(), "requires PhysicalABIV1 runnable program-bootstrap metadata") {
		t.Fatalf("frame retention with wrong physical ABI error = %v", err)
	}
	wrongScheduler := metadata
	wrongScheduler.SchedulerABI = SchedulerChildAwaitABIV0
	if _, err := plan.CoroPlanDigest(wrongScheduler); err == nil || !strings.Contains(err.Error(), "requires PhysicalABIV1 runnable program-bootstrap metadata") {
		t.Fatalf("frame retention with wrong scheduler ABI error = %v", err)
	}
}

func TestCoroPlanDigestAcceptsChannelSchedulerIdentities(t *testing.T) {
	for _, schedulerABI := range []string{
		SchedulerProgramBootstrapChannelABIV0,
		SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0,
	} {
		t.Run(schedulerABI, func(t *testing.T) {
			prog, pkg := buildCoroTestSSAWithMode(
				t, "channel_scheduler_digest.go", planDigestTestSource,
				ssa.SanityCheckFunctions|ssa.InstantiateGenerics,
			)
			root := packageFunction(t, pkg, "root")
			config := planDigestSSAConfig()
			config.FunctionIDs.CoroABI = PhysicalABIV1
			config.FunctionIDs.SchedulerABI = schedulerABI
			plan, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: AsyncDemand}}, config)
			if err != nil {
				t.Fatal(err)
			}
			metadata := validPlanDigestMetadata()
			metadata.CoroABI = PhysicalABIV1
			metadata.SchedulerABI = schedulerABI
			metadata.FrameRetentionABI = FrameRetentionParkABIV2
			if _, err := plan.CoroPlanDigest(metadata); err != nil {
				t.Fatalf("channel scheduler digest: %v", err)
			}
			document, err := plan.canonicalPlanDigest(metadata)
			if err != nil {
				t.Fatal(err)
			}
			if document.Metadata.SchedulerABI != schedulerABI {
				t.Fatalf("canonical scheduler ABI = %q, want %q", document.Metadata.SchedulerABI, schedulerABI)
			}
		})
	}
}

func TestCoroPlanDigestRecordsClosedStaticSpawnConsumerAndOwnerSeed(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "spawn_digest.go", `package coroid
func worker(value int) { _ = value }
func launch(value int) { go worker(value) }
`)
	launch := packageFunction(t, pkg, "launch")
	worker := packageFunction(t, pkg, "worker")
	build := func(seed bool) *SSAPlan {
		config := planDigestSSAConfig()
		config.FunctionIDs.CoroABI = PhysicalABIV1
		config.FunctionIDs.SchedulerABI = SchedulerProgramBootstrapClosedStaticSpawnABIV0
		config.MaxPlainInstructions = -1
		if seed {
			config.ClassifyFunction = func(fn *ssa.Function) (SSAFunctionPolicy, error) {
				if fn == launch || fn == worker {
					return SSAFunctionPolicy{Effect: YieldOnly}, nil
				}
				return SSAFunctionPolicy{}, nil
			}
		}
		plan, err := AnalyzeSSA(prog, Roots{{Function: launch, Demand: AsyncDemand}}, config)
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
	seeded := build(true)
	again := build(true)
	unseeded := build(false)
	metadata := validPlanDigestMetadata()
	metadata.CoroABI = PhysicalABIV1
	metadata.SchedulerABI = SchedulerProgramBootstrapClosedStaticSpawnABIV0
	digest, err := seeded.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	againDigest, err := again.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if digest != againDigest {
		t.Fatalf("closed static spawn digest is unstable: %s != %s", digest, againDigest)
	}
	unseededDigest, err := unseeded.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if digest == unseededDigest {
		t.Fatal("spawn owner YieldOnly/contextful-primary seed is absent from the digest")
	}
	document, err := seeded.canonicalPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, call := range document.Calls {
		if CallKind(call.Kind) == CallSpawn {
			found = true
			if call.Open || call.MayBeNil || len(call.Targets) != 1 {
				t.Fatalf("spawn digest call = %+v", call)
			}
		}
	}
	if !found {
		t.Fatal("canonical plan digest has no exact CallSpawn consumer")
	}
}

func TestCoroPlanDigestRecordsIgnoredPhysicalBodySemantics(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "ignored_digest.go", `package coroid
func external() {}
func root() { external() }
`)
	external := packageFunction(t, pkg, "external")
	root := packageFunction(t, pkg, "root")
	build := func(ignore bool) *SSAPlan {
		t.Helper()
		config := planDigestSSAConfig()
		config.ClassifyFunction = func(fn *ssa.Function) (SSAFunctionPolicy, error) {
			if fn == external {
				return SSAFunctionPolicy{
					IgnoreBody:       ignore,
					Exec:             MayUnwind,
					External:         ExternalUnknownForeign,
					OverrideExternal: true,
				}, nil
			}
			return SSAFunctionPolicy{}, nil
		}
		plan, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: AsyncDemand}}, config)
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}

	ordinary := build(false)
	ignored := build(true)
	ordinaryPlan := functionPlanFor(t, ordinary, external)
	ignoredPlan := functionPlanFor(t, ignored, external)
	if ordinaryPlan != ignoredPlan {
		t.Fatalf("fixture must isolate ignored-body identity:\nordinary %+v\nignored  %+v", ordinaryPlan, ignoredPlan)
	}
	metadata := validPlanDigestMetadata()
	ordinaryDigest, err := ordinary.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	ignoredDigest, err := ignored.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if ordinaryDigest == ignoredDigest {
		t.Fatal("ignored and physically emitted SSA bodies have the same plan digest")
	}
	document, err := ignored.canonicalPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	externalID, _ := ignored.FunctionID(external)
	found := false
	for _, function := range document.Functions {
		if function.ID == externalID {
			found = true
			if !function.IgnoredBody {
				t.Fatal("ignored external function record lost ignored_body=true")
			}
		}
	}
	if !found {
		t.Fatal("ignored external function is absent from digest")
	}
}

func TestCoroPlanDigestRecordsExactAssemblyNoSuspendCertificate(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "assembly_digest.go", `package coroid
func assemblyLeaf() {}
func root() { assemblyLeaf() }
`)
	leaf := packageFunction(t, pkg, "assemblyLeaf")
	root := packageFunction(t, pkg, "root")
	build := func(certificate string) *SSAPlan {
		t.Helper()
		config := planDigestSSAConfig()
		config.ClassifyFunction = func(fn *ssa.Function) (SSAFunctionPolicy, error) {
			if fn != leaf {
				return SSAFunctionPolicy{}, nil
			}
			return SSAFunctionPolicy{
				IgnoreBody:                   true,
				Exec:                         IRQUnsafe,
				External:                     ExternalKnown,
				OverrideExternal:             true,
				AssemblyNoSuspendCertificate: certificate,
			}, nil
		}
		plan, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: AsyncDemand}}, config)
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}

	const firstCertificate = "llgo.coro.asm-nosuspend.test.v1:first"
	first := build(firstCertificate)
	second := build("llgo.coro.asm-nosuspend.test.v1:second")
	if got, ok := first.AssemblyNoSuspendCertificate(leaf); !ok || got != firstCertificate {
		t.Fatalf("assembly certificate = (%q, %t), want (%q, true)", got, ok, firstCertificate)
	}
	if _, ok := first.AssemblyNoSuspendCertificate(root); ok {
		t.Fatal("ordinary root unexpectedly has an assembly certificate")
	}
	metadata := validPlanDigestMetadata()
	firstDigest, err := first.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := second.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest == secondDigest {
		t.Fatal("distinct translated-assembly proofs share a plan digest")
	}
	document, err := first.canonicalPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	leafID, _ := first.FunctionID(leaf)
	for _, function := range document.Functions {
		if function.ID != leafID {
			continue
		}
		if function.AssemblyNoSuspendCertificate != firstCertificate || !function.IgnoredBody {
			t.Fatalf("assembly digest record = %+v", function)
		}
		return
	}
	t.Fatal("assembly leaf is absent from canonical plan digest")
}

func TestCoroPlanDigestCanonicalTargetsAndPlanMutations(t *testing.T) {
	plan, _ := buildPlanDigestTestPlan(t, ssa.SanityCheckFunctions|ssa.InstantiateGenerics)
	metadata := validPlanDigestMetadata()
	baseline, err := plan.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}

	var multiTargetCall ssa.CallInstruction
	var originalCall SSACallPlan
	for call, callPlan := range plan.callPlans {
		if len(callPlan.Targets) >= 2 {
			multiTargetCall = call
			originalCall = callPlan
			originalCall.Targets = append([]FunctionID(nil), callPlan.Targets...)
			break
		}
	}
	if multiTargetCall == nil {
		t.Fatal("test plan has no multi-target CallPlan")
	}
	reordered := originalCall
	reordered.Targets = []FunctionID{originalCall.Targets[1], originalCall.Targets[0], originalCall.Targets[0]}
	plan.callPlans[multiTargetCall] = reordered
	canonical, err := plan.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if canonical != baseline {
		t.Fatalf("target order/duplicates changed canonical digest: %s != %s", canonical, baseline)
	}
	reordered.Open = !reordered.Open
	plan.callPlans[multiTargetCall] = reordered
	mutated, err := plan.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if mutated == baseline {
		t.Fatal("CallPlan mutation did not change digest")
	}
	reordered = originalCall
	reordered.SyncDispatch = !reordered.SyncDispatch
	plan.callPlans[multiTargetCall] = reordered
	mutated, err = plan.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if mutated == baseline {
		t.Fatal("CallPlan SyncDispatch is absent from digest")
	}
	plan.callPlans[multiTargetCall] = originalCall

	originalRoots := append([]SSARootPlan(nil), plan.roots...)
	var addedRoot SSARootPlan
	for _, function := range plan.functions {
		isRoot := false
		for _, root := range originalRoots {
			isRoot = isRoot || root.ID == function.Plan.ID
		}
		if !isRoot && function.Plan.Demand != NoDemand {
			addedRoot = SSARootPlan{
				Function: function.Function, ID: function.Plan.ID, Demand: function.Plan.Demand,
				ManagedDemand: function.Plan.ManagedDemand, RawPlainDemand: function.Plan.RawPlainDemand,
			}
			break
		}
	}
	if addedRoot.Function == nil {
		t.Fatal("test plan has no propagated non-root demand")
	}
	changedRoots := make([]SSARootPlan, 0, len(originalRoots)+1)
	inserted := false
	for _, root := range originalRoots {
		if !inserted && addedRoot.ID < root.ID {
			changedRoots = append(changedRoots, addedRoot)
			inserted = true
		}
		changedRoots = append(changedRoots, root)
	}
	if !inserted {
		changedRoots = append(changedRoots, addedRoot)
	}
	plan.roots = changedRoots
	mutated, err = plan.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if mutated == baseline {
		t.Fatal("explicit root mutation did not change digest")
	}
	plan.roots = originalRoots

	var value ssa.Value
	var originalValue SSAValuePlan
	for candidate, valuePlan := range plan.valuePlans {
		if len(valuePlan.Funcs) != 0 {
			value = candidate
			originalValue = cloneSSAValuePlan(valuePlan)
			break
		}
	}
	if value == nil {
		t.Fatal("test plan has no SSAValuePlan")
	}
	changedValue := cloneSSAValuePlan(originalValue)
	changedValue.Funcs[0].MayBeNil = !changedValue.Funcs[0].MayBeNil
	plan.valuePlans[value] = changedValue
	mutated, err = plan.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if mutated == baseline {
		t.Fatal("SSAValuePlan mutation did not change digest")
	}
	plan.valuePlans[value] = originalValue

	originalFunction := plan.functions[0].Plan
	changedFunction := originalFunction
	changedFunction.Recursive = !changedFunction.Recursive
	plan.functions[0].Plan = changedFunction
	plan.plan.functions[0] = changedFunction
	mutated, err = plan.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if mutated == baseline {
		t.Fatal("FunctionPlan mutation did not change digest")
	}
	plan.functions[0].Plan = originalFunction
	plan.plan.functions[0] = originalFunction
	if restored, err := plan.CoroPlanDigest(metadata); err != nil || restored != baseline {
		t.Fatalf("restored digest = %q, %v; want %q", restored, err, baseline)
	}
}

func TestCoroPlanDigestIncludesTrustedBoundedRecursion(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "bounded_recursion_digest.go", `package coroid
func recursive() { recursive() }
`)
	recursive := packageFunction(t, pkg, "recursive")
	plan, err := AnalyzeSSA(prog, Roots{{Function: recursive, Demand: AsyncDemand}}, planDigestSSAConfig())
	if err != nil {
		t.Fatal(err)
	}
	metadata := validPlanDigestMetadata()
	baseline, err := plan.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}

	index := -1
	for i := range plan.functions {
		if plan.functions[i].Function == recursive {
			index = i
			break
		}
	}
	if index < 0 {
		t.Fatal("recursive function is absent from plan")
	}
	original := plan.functions[index].Plan
	if !original.Recursive || original.TrustedBoundedRecursion {
		t.Fatalf("original recursive plan = %+v", original)
	}
	changed := original
	changed.TrustedBoundedRecursion = true
	plan.functions[index].Plan = changed
	baseIndex := plan.plan.byID[changed.ID]
	plan.plan.functions[baseIndex] = changed
	mutated, err := plan.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if mutated == baseline {
		t.Fatal("TrustedBoundedRecursion is absent from CoroPlanDigest")
	}
	document, err := plan.canonicalPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, function := range document.Functions {
		if function.ID == changed.ID {
			found = true
			if !function.TrustedBoundedRecursion {
				t.Fatalf("bounded-recursion digest record = %+v", function)
			}
		}
	}
	if !found {
		t.Fatal("recursive function is absent from canonical digest")
	}
}

func TestCoroPlanDigestRecordsFrontendElidedCalls(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "elided_digest.go", `package coroid
func target() {}
func root() { target() }
func other() { target() }
`)
	target := packageFunction(t, pkg, "target")
	root := packageFunction(t, pkg, "root")
	other := packageFunction(t, pkg, "other")
	rootCall := onlyNonBuiltinCall(t, root)
	otherCall := onlyNonBuiltinCall(t, other)
	includeWithoutTarget := func(fn *ssa.Function) (bool, error) { return fn != target, nil }
	config := planDigestSSAConfig()
	config.Include = includeWithoutTarget
	conservative, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: SyncDemand}}, config)
	if err != nil {
		t.Fatal(err)
	}
	config.ClassifyElidedCall = func(_ *ssa.Function, call ssa.CallInstruction) (bool, error) {
		return call == rootCall, nil
	}
	elided, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: SyncDemand}}, config)
	if err != nil {
		t.Fatal(err)
	}
	metadata := validPlanDigestMetadata()
	first, err := elided.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	again, err := elided.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if first != again {
		t.Fatalf("elided-call digest is unstable: %s != %s", first, again)
	}
	ordinary, err := conservative.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if first == ordinary {
		t.Fatal("frontend-elided policy did not change the canonical digest")
	}
	document, err := elided.canonicalPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	rootPlan, ok := elided.FunctionPlan(root)
	if !ok {
		t.Fatal("elided root has no FunctionPlan")
	}
	if len(document.ElidedCalls) != 1 || !document.ElidedCalls[0].Elided || document.ElidedCalls[0].Function != rootPlan.ID ||
		document.ElidedCalls[0].Block < 0 || document.ElidedCalls[0].Instruction < 0 {
		t.Fatalf("canonical elided-call record = %+v", document.ElidedCalls)
	}
	if len(document.Calls) != len(elided.callPlans) {
		t.Fatalf("elided call was disguised as a CallPlan: calls=%d plans=%d", len(document.Calls), len(elided.callPlans))
	}
	otherConfig := planDigestSSAConfig()
	otherConfig.Include = includeWithoutTarget
	otherConfig.ClassifyElidedCall = func(_ *ssa.Function, call ssa.CallInstruction) (bool, error) {
		return call == otherCall, nil
	}
	otherElided, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: SyncDemand}}, otherConfig)
	if err != nil {
		t.Fatal(err)
	}
	otherDigest, err := otherElided.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if otherDigest == first {
		t.Fatal("moving the exact elided identity to another SSA call site did not change the digest")
	}
	otherDocument, err := otherElided.canonicalPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	otherPlan, ok := otherElided.FunctionPlan(other)
	if !ok || len(otherDocument.ElidedCalls) != 1 || otherDocument.ElidedCalls[0].Function != otherPlan.ID {
		t.Fatalf("other exact elided-call record = %+v (plan=%+v, ok=%t)", otherDocument.ElidedCalls, otherPlan, ok)
	}

	delete(elided.elidedCalls, rootCall)
	if _, err := elided.CoroPlanDigest(metadata); err == nil || !strings.Contains(err.Error(), "missing CallPlan") {
		t.Fatalf("missing elided identity digest error = %v", err)
	}
	elided.elidedCalls[rootCall] = struct{}{}
	elided.elidedCalls[otherCall] = struct{}{}
	if _, err := elided.CoroPlanDigest(metadata); err == nil || !strings.Contains(err.Error(), "both elided and assigned a CallPlan") {
		t.Fatalf("overlapping elided/CallPlan digest error = %v", err)
	}
}

func TestCoroPlanDigestFailsClosedOnCallAndValueCoverage(t *testing.T) {
	plan, _ := buildPlanDigestTestPlan(t, ssa.SanityCheckFunctions|ssa.InstantiateGenerics)
	metadata := validPlanDigestMetadata()

	var call ssa.CallInstruction
	var callPlan SSACallPlan
	for candidate, candidatePlan := range plan.callPlans {
		call, callPlan = candidate, candidatePlan
		break
	}
	delete(plan.callPlans, call)
	if _, err := plan.CoroPlanDigest(metadata); err == nil || !strings.Contains(err.Error(), "missing CallPlan") {
		t.Fatalf("missing CallPlan error = %v", err)
	}
	plan.callPlans[call] = callPlan
	other, _ := buildPlanDigestTestPlan(t, ssa.SanityCheckFunctions|ssa.InstantiateGenerics)
	var foreignCall ssa.CallInstruction
	var foreignCallPlan SSACallPlan
	for candidate, candidatePlan := range other.callPlans {
		foreignCall, foreignCallPlan = candidate, candidatePlan
		break
	}
	plan.callPlans[foreignCall] = foreignCallPlan
	if _, err := plan.CoroPlanDigest(metadata); err == nil || !strings.Contains(err.Error(), "coverage mismatch") {
		t.Fatalf("unreachable CallPlan error = %v", err)
	}
	delete(plan.callPlans, foreignCall)

	var value ssa.Value
	var valuePlan SSAValuePlan
	for candidate, candidatePlan := range plan.valuePlans {
		value, valuePlan = candidate, cloneSSAValuePlan(candidatePlan)
		break
	}
	delete(plan.valuePlans, value)
	if _, err := plan.CoroPlanDigest(metadata); err == nil || !strings.Contains(err.Error(), "missing SSAValuePlan") {
		t.Fatalf("missing SSAValuePlan error = %v", err)
	}
	plan.valuePlans[value] = valuePlan

	var foreignValue ssa.Value
	var foreignPlan SSAValuePlan
	for candidate, candidatePlan := range other.valuePlans {
		foreignValue, foreignPlan = candidate, cloneSSAValuePlan(candidatePlan)
		break
	}
	plan.valuePlans[foreignValue] = foreignPlan
	if _, err := plan.CoroPlanDigest(metadata); err == nil || !strings.Contains(err.Error(), "coverage mismatch") {
		t.Fatalf("unreachable SSAValuePlan error = %v", err)
	}
	delete(plan.valuePlans, foreignValue)

	originalRoots := append([]SSARootPlan(nil), plan.roots...)
	rootMutations := []struct {
		name   string
		want   string
		mutate func()
	}{
		{"nil function", "nil function", func() { plan.roots[0].Function = nil }},
		{"no demand", "has no demand", func() {
			plan.roots[0].Demand = NoDemand
			plan.roots[0].ManagedDemand = NoDemand
			plan.roots[0].RawPlainDemand = false
		}},
		{"invalid demand", "unknown demand bits", func() { plan.roots[0].Demand = Demand(1 << 7) }},
		{"duplicate", "not in strict FunctionID order", func() { plan.roots = append(plan.roots, plan.roots[0]) }},
		{"foreign function", "missing forward root mapping", func() { plan.roots[0].Function = other.roots[0].Function }},
	}
	for _, test := range rootMutations {
		t.Run("root/"+test.name, func(t *testing.T) {
			plan.roots = append([]SSARootPlan(nil), originalRoots...)
			test.mutate()
			if _, err := plan.CoroPlanDigest(metadata); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("root mutation error = %v, want %q", err, test.want)
			}
		})
	}
	plan.roots = originalRoots

	originalFunction := plan.functions[0].Plan
	invalidFunction := originalFunction
	invalidFunction.Emission = BodyEmission(255)
	plan.functions[0].Plan = invalidFunction
	if _, err := plan.CoroPlanDigest(metadata); err == nil || !strings.Contains(err.Error(), "invalid body emission") {
		t.Fatalf("invalid emission error = %v", err)
	}
	invalidFunction.Emission = EmitNone
	if originalFunction.Emission == EmitNone {
		invalidFunction.Emission = EmitPlain
	}
	plan.functions[0].Plan = invalidFunction
	if _, err := plan.CoroPlanDigest(metadata); err == nil || !strings.Contains(err.Error(), "emission") || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatched emission error = %v", err)
	}
	plan.functions[0].Plan = originalFunction
}

func TestCoroPlanDigestMetadataValidation(t *testing.T) {
	plan, _ := buildPlanDigestTestPlan(t, ssa.SanityCheckFunctions|ssa.InstantiateGenerics)
	valid := validPlanDigestMetadata()
	if _, err := plan.CoroPlanDigest(valid); err != nil {
		t.Fatalf("valid metadata: %v", err)
	}

	tests := []struct {
		name   string
		change func(*PlanDigestMetadata)
		want   string
	}{
		{"empty coro ABI", func(m *PlanDigestMetadata) { m.CoroABI = "" }, "coroutine ABI is empty"},
		{"mismatched coro ABI", func(m *PlanDigestMetadata) { m.CoroABI = EntryResolutionABIV0 }, "does not match FunctionID ABI"},
		{"mismatched scheduler ABI", func(m *PlanDigestMetadata) { m.SchedulerABI = "llgo.coro.scheduler.other.v0" }, "does not match FunctionID ABI"},
		{"empty panic ABI", func(m *PlanDigestMetadata) { m.PanicABI = "" }, "panic ABI is empty"},
		{"empty func rep ABI", func(m *PlanDigestMetadata) { m.FuncRepABI = "" }, "function representation ABI is empty"},
		{"wrong lowering-facts schema", func(m *PlanDigestMetadata) { m.LoweringFactsSchema += ".other" }, "lowering-facts schema"},
		{"empty lowering-facts digest", func(m *PlanDigestMetadata) { m.LoweringFactsDigest = "" }, "lowering-facts digest"},
		{"invalid lowering-facts digest", func(m *PlanDigestMetadata) { m.LoweringFactsDigest = strings.Repeat("g", sha256.Size*2) }, "lowering-facts digest"},
		{"empty triple", func(m *PlanDigestMetadata) { m.TargetTriple = "" }, "target triple is empty"},
		{"invalid CPU UTF-8", func(m *PlanDigestMetadata) { m.TargetCPU = string([]byte{0xff}) }, "target CPU is not valid UTF-8"},
		{"NUL feature", func(m *PlanDigestMetadata) { m.TargetFeatures = "+simd\x00-bad" }, "target features contains NUL"},
		{"NUL target ABI", func(m *PlanDigestMetadata) { m.TargetABI = "default\x00bad" }, "target ABI contains NUL"},
		{"zero pointer", func(m *PlanDigestMetadata) { m.PointerBits = 0 }, "positive multiple of 8"},
		{"unaligned pointer", func(m *PlanDigestMetadata) { m.PointerBits = 31 }, "positive multiple of 8"},
		{"invalid endianness", func(m *PlanDigestMetadata) { m.Endianness = "middle" }, "not little or big"},
		{"empty data layout", func(m *PlanDigestMetadata) { m.DataLayout = "" }, "data layout is empty"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			metadata := valid
			test.change(&metadata)
			if _, err := plan.CoroPlanDigest(metadata); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}

	prog, pkg := buildCoroTestSSA(t, "report.go", `package coroid; func root() {}`)
	reportOnly, err := AnalyzeSSA(prog, Roots{{Function: packageFunction(t, pkg, "root"), Demand: AsyncDemand}}, SSAConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reportOnly.CoroPlanDigest(valid); err == nil || !strings.Contains(err.Error(), "archive-ready") {
		t.Fatalf("report-only plan digest error = %v", err)
	}
	var nilPlan *SSAPlan
	if _, err := nilPlan.CoroPlanDigest(valid); err == nil || !strings.Contains(err.Error(), "nil SSA plan") {
		t.Fatalf("nil plan digest error = %v", err)
	}
}

func TestCoroPlanDigestMetadataMutationsChangeDigest(t *testing.T) {
	plan, _ := buildPlanDigestTestPlan(t, ssa.SanityCheckFunctions|ssa.InstantiateGenerics)
	valid := validPlanDigestMetadata()
	baseline, err := plan.CoroPlanDigest(valid)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		change func(*PlanDigestMetadata)
	}{
		{"panic ABI", func(m *PlanDigestMetadata) { m.PanicABI += ".changed" }},
		{"func rep ABI", func(m *PlanDigestMetadata) { m.FuncRepABI += ".changed" }},
		{"lowering facts", func(m *PlanDigestMetadata) { m.LoweringFactsDigest = strings.Repeat("1", sha256.Size*2) }},
		{"triple", func(m *PlanDigestMetadata) { m.TargetTriple = "wasm32-unknown-unknown" }},
		{"CPU", func(m *PlanDigestMetadata) { m.TargetCPU = "generic" }},
		{"features", func(m *PlanDigestMetadata) { m.TargetFeatures += ",+atomics" }},
		{"target ABI", func(m *PlanDigestMetadata) { m.TargetABI = "eabi" }},
		{"pointer bits", func(m *PlanDigestMetadata) { m.PointerBits = 32 }},
		{"endianness", func(m *PlanDigestMetadata) { m.Endianness = "big" }},
		{"data layout", func(m *PlanDigestMetadata) { m.DataLayout += "-i128:128" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			metadata := valid
			test.change(&metadata)
			digest, err := plan.CoroPlanDigest(metadata)
			if err != nil {
				t.Fatal(err)
			}
			if digest == baseline {
				t.Fatalf("metadata mutation %q did not change digest", test.name)
			}
		})
	}
}

func TestCoroPlanDigestExplicitStatusPanicABIDomainSeparation(t *testing.T) {
	plan, _ := buildPlanDigestTestPlan(t, ssa.SanityCheckFunctions|ssa.InstantiateGenerics)
	legacy := validPlanDigestMetadata()
	legacyDigest, err := plan.CoroPlanDigest(legacy)
	if err != nil {
		t.Fatal(err)
	}
	explicitStatus := legacy
	explicitStatus.PanicABI = PanicExplicitStatusABIV0
	explicitStatusDigest, err := plan.CoroPlanDigest(explicitStatus)
	if err != nil {
		t.Fatal(err)
	}
	if explicitStatusDigest == legacyDigest {
		t.Fatalf("panic ABI identities share a plan digest: %s", legacyDigest)
	}
}

func TestCoroPlanDigestCanonicalEmptyArrays(t *testing.T) {
	prog, _ := buildCoroTestSSA(t, "empty.go", `package coroid; func root() {}`)
	plan, err := AnalyzeSSA(prog, nil, planDigestSSAConfig())
	if err != nil {
		t.Fatal(err)
	}
	document, err := plan.canonicalPlanDigest(validPlanDigestMetadata())
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	for _, field := range []string{`"roots":[]`, `"calls":[]`, `"lowered_calls":[]`, `"values":[]`} {
		if !strings.Contains(text, field) {
			t.Fatalf("canonical document %s does not contain %s", text, field)
		}
	}
}

func TestCoroPlanDigestIncludesExactLoweredCallMapping(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "lowered_digest.go", `package coroid
func root() {}
func first() {}
func second() {}
`)
	root := packageFunction(t, pkg, "root")
	first := packageFunction(t, pkg, "first")
	second := packageFunction(t, pkg, "second")
	build := func(calls []SSALoweredCall) *SSAPlan {
		t.Helper()
		config := planDigestSSAConfig()
		config.MaxPlainInstructions = -1
		config.ClassifyLoweredCalls = func(fn *ssa.Function) ([]SSALoweredCall, error) {
			if fn == root {
				return calls, nil
			}
			return nil, nil
		}
		plan, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: SyncDemand}}, config)
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
	baseline := build([]SSALoweredCall{
		{LogicalName: "runtime.first", Target: first},
		{LogicalName: "runtime.second", Target: second},
	})
	permuted := build([]SSALoweredCall{
		{LogicalName: "runtime.second", Target: second},
		{LogicalName: "runtime.first", Target: first},
	})
	swapped := build([]SSALoweredCall{
		{LogicalName: "runtime.first", Target: second},
		{LogicalName: "runtime.second", Target: first},
	})
	unwindOnly := build([]SSALoweredCall{
		{LogicalName: "runtime.first", Target: first, UnwindOnly: true},
		{LogicalName: "runtime.second", Target: second},
	})
	explicitStatusElided := build([]SSALoweredCall{
		{LogicalName: "runtime.first", Target: first, UnwindOnly: true, ExplicitStatusElided: true},
		{LogicalName: "runtime.second", Target: second},
	})
	rawPlain := build([]SSALoweredCall{
		{LogicalName: "runtime.first", Target: first, RawPlain: true},
		{LogicalName: "runtime.second", Target: second},
	})
	noUnwind := build([]SSALoweredCall{
		{LogicalName: "runtime.first", Target: first, NoUnwind: true},
		{LogicalName: "runtime.second", Target: second},
	})
	metadata := validPlanDigestMetadata()
	baselineDigest, err := baseline.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	permutedDigest, err := permuted.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if baselineDigest != permutedDigest {
		t.Fatalf("classifier order changed lowered-call digest:\n%s\n%s", baselineDigest, permutedDigest)
	}
	swappedDigest, err := swapped.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if baselineDigest == swappedDigest {
		t.Fatal("retargeting logical lowered-call identities did not change digest")
	}
	unwindDigest, err := unwindOnly.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if baselineDigest == unwindDigest {
		t.Fatal("changing a lowered call to unwind-only did not change digest")
	}
	explicitStatusElidedDigest, err := explicitStatusElided.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if unwindDigest == explicitStatusElidedDigest {
		t.Fatal("marking an unwind-only lowered call ExplicitStatus-elided did not change digest")
	}
	rawPlainDigest, err := rawPlain.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if baselineDigest == rawPlainDigest {
		t.Fatal("marking a lowered-call occurrence raw-plain did not change digest")
	}
	noUnwindDigest, err := noUnwind.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if baselineDigest == noUnwindDigest {
		t.Fatal("marking a lowered-call occurrence no-unwind did not change digest")
	}
	document, err := baseline.canonicalPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if len(document.LoweredCalls) != 2 || document.LoweredCalls[0].LogicalName != "runtime.first" || document.LoweredCalls[1].LogicalName != "runtime.second" {
		t.Fatalf("canonical lowered calls = %+v", document.LoweredCalls)
	}
	unwindDocument, err := unwindOnly.canonicalPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if len(unwindDocument.LoweredCalls) != 2 || !unwindDocument.LoweredCalls[0].UnwindOnly || unwindDocument.LoweredCalls[1].UnwindOnly {
		t.Fatalf("canonical unwind-only lowered calls = %+v", unwindDocument.LoweredCalls)
	}
	explicitStatusElidedDocument, err := explicitStatusElided.canonicalPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if len(explicitStatusElidedDocument.LoweredCalls) != 2 ||
		!explicitStatusElidedDocument.LoweredCalls[0].ExplicitStatusElided ||
		explicitStatusElidedDocument.LoweredCalls[1].ExplicitStatusElided {
		t.Fatalf("canonical ExplicitStatus-elided lowered calls = %+v", explicitStatusElidedDocument.LoweredCalls)
	}
	rawPlainDocument, err := rawPlain.canonicalPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if len(rawPlainDocument.LoweredCalls) != 2 || !rawPlainDocument.LoweredCalls[0].RawPlain || rawPlainDocument.LoweredCalls[1].RawPlain {
		t.Fatalf("canonical raw-plain lowered calls = %+v", rawPlainDocument.LoweredCalls)
	}
	noUnwindDocument, err := noUnwind.canonicalPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if len(noUnwindDocument.LoweredCalls) != 2 || !noUnwindDocument.LoweredCalls[0].NoUnwind ||
		noUnwindDocument.LoweredCalls[1].NoUnwind {
		t.Fatalf("canonical no-unwind lowered calls = %+v", noUnwindDocument.LoweredCalls)
	}

	// Bind the occurrence fact directly, independently of the fixed-point plan
	// changes that a freshly analyzed raw-plain reference also produces.
	mutated := build([]SSALoweredCall{
		{LogicalName: "runtime.first", Target: first},
		{LogicalName: "runtime.second", Target: second},
	})
	beforeMutation, err := mutated.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	mutated.loweredCalls[root][0].RawPlain = true
	afterMutation, err := mutated.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if beforeMutation == afterMutation {
		t.Fatal("mutating only the frozen raw-plain lowered-call bit did not change digest")
	}
}

func TestCoroPlanDigestDistinguishesExplicitAndPropagatedRoots(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "roots.go", `package coroid
func leaf(ch chan int) { <-ch }
func root(ch chan int) { leaf(ch) }
`)
	root := packageFunction(t, pkg, "root")
	leaf := packageFunction(t, pkg, "leaf")
	config := planDigestSSAConfig()
	propagated, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: AsyncDemand}}, config)
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := AnalyzeSSA(prog, Roots{
		{Function: root, Demand: AsyncDemand},
		{Function: leaf, Demand: AsyncDemand},
	}, config)
	if err != nil {
		t.Fatal(err)
	}
	permuted, err := AnalyzeSSA(prog, Roots{
		{Function: leaf, Demand: AsyncDemand},
		{Function: root, Demand: AsyncDemand},
	}, config)
	if err != nil {
		t.Fatal(err)
	}
	duplicated, err := AnalyzeSSA(prog, Roots{
		{Function: leaf, Demand: AsyncDemand},
		{Function: root, Demand: AsyncDemand},
		{Function: leaf, Demand: AsyncDemand},
		{Function: root, Demand: AsyncDemand},
	}, config)
	if err != nil {
		t.Fatal(err)
	}

	if got := functionPlanFor(t, propagated, leaf).Demand; got != AsyncDemand {
		t.Fatalf("propagated leaf demand = %s, want async", got)
	}
	if got, want := len(propagated.Roots()), 1; got != want {
		t.Fatalf("propagated roots = %d, want %d", got, want)
	}
	if got, want := len(explicit.Roots()), 2; got != want {
		t.Fatalf("explicit roots = %d, want %d", got, want)
	}
	for _, fn := range []*ssa.Function{root, leaf} {
		left, leftOK := propagated.FunctionPlan(fn)
		right, rightOK := explicit.FunctionPlan(fn)
		if !leftOK || !rightOK || left != right {
			t.Fatalf("function plan for %s differs: propagated=%+v,%v explicit=%+v,%v", fn.Name(), left, leftOK, right, rightOK)
		}
	}

	metadata := validPlanDigestMetadata()
	propagatedDocument, err := propagated.canonicalPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	explicitDocument, err := explicit.canonicalPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	propagatedDocument.Roots = nil
	explicitDocument.Roots = nil
	propagatedPayload, err := json.Marshal(propagatedDocument)
	if err != nil {
		t.Fatal(err)
	}
	explicitPayload, err := json.Marshal(explicitDocument)
	if err != nil {
		t.Fatal(err)
	}
	if string(propagatedPayload) != string(explicitPayload) {
		t.Fatalf("non-root digest plan changed:\npropagated %s\nexplicit %s", propagatedPayload, explicitPayload)
	}
	propagatedDigest, err := propagated.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	explicitDigest, err := explicit.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if explicitDigest == propagatedDigest {
		t.Fatal("explicit Async root and propagated AsyncDemand produced the same digest")
	}
	permutedDigest, err := permuted.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if permutedDigest != explicitDigest {
		t.Fatalf("root input order changed digest: %s != %s", permutedDigest, explicitDigest)
	}
	duplicatedDigest, err := duplicated.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if duplicatedDigest != explicitDigest {
		t.Fatalf("duplicate roots changed digest: %s != %s", duplicatedDigest, explicitDigest)
	}
}

func TestCoroPlanDigestProjectsDefinitionlessValueOccurrences(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "occurrences.go", `package coroid
func target() {}
func take(func()) {}
func root() {
	take(target)
	take(target)
}
`)
	root := packageFunction(t, pkg, "root")
	target := packageFunction(t, pkg, "target")
	plan, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: AsyncDemand}}, planDigestSSAConfig())
	if err != nil {
		t.Fatal(err)
	}
	document, err := plan.canonicalPlanDigest(validPlanDigestMetadata())
	if err != nil {
		t.Fatal(err)
	}
	rootID, ok := plan.FunctionID(root)
	if !ok {
		t.Fatal("root has no FunctionID")
	}
	targetID, ok := plan.FunctionID(target)
	if !ok {
		t.Fatal("target has no FunctionID")
	}
	var instructions []int
	for _, value := range document.Values {
		if value.Site.Function != rootID || value.Site.Kind != "operand" {
			continue
		}
		for _, leaf := range value.Funcs {
			if len(leaf.Targets) == 1 && leaf.Targets[0] == targetID {
				instructions = append(instructions, value.Site.Instruction)
			}
		}
	}
	if len(instructions) != 2 || instructions[0] == instructions[1] {
		t.Fatalf("definition-less target operand sites = %v, want two distinct stable occurrences", instructions)
	}
}

func TestCoroPlanDigestDoesNotPlanDeferredBuiltinCallee(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "deferred_builtin.go", `package coroid
func root(ch chan struct{}) {
	defer close(ch)
}
`)
	root := packageFunction(t, pkg, "root")
	plan, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: AsyncDemand}}, planDigestSSAConfig())
	if err != nil {
		t.Fatal(err)
	}
	var closeBuiltin *ssa.Builtin
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(ssa.CallInstruction)
			if !ok {
				continue
			}
			if builtin, ok := call.Common().Value.(*ssa.Builtin); ok && builtin.Name() == "close" {
				closeBuiltin = builtin
			}
		}
	}
	if closeBuiltin == nil {
		t.Fatal("fixture has no deferred close builtin")
	}
	if _, planned := plan.ValuePlan(closeBuiltin); planned {
		t.Fatal("deferred builtin callee acquired a first-class function-value plan")
	}
	if _, err := plan.CoroPlanDigest(validPlanDigestMetadata()); err != nil {
		t.Fatalf("digest deferred builtin call: %v", err)
	}
}

func buildPlanDigestTestPlan(t *testing.T, mode ssa.BuilderMode) (*SSAPlan, *ssa.Package) {
	t.Helper()
	prog, pkg := buildCoroTestSSAWithMode(t, "digest.go", planDigestTestSource, mode)
	root := packageFunction(t, pkg, "root")
	plan, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: AsyncDemand}}, planDigestSSAConfig())
	if err != nil {
		t.Fatal(err)
	}
	return plan, pkg
}

func planDigestSSAConfig() SSAConfig {
	return SSAConfig{FunctionIDs: FunctionIDConfig{
		CoroABI:      PhysicalABIV0,
		SchedulerABI: SchedulerNoneABIV0,
		ArchiveReady: true,
		ResolveLinkIdentity: func(fn *ssa.Function) (string, error) {
			if fn == nil || fn.Name() == "" {
				return "", fmt.Errorf("missing test link identity")
			}
			return "example.test/coroid." + fn.Name(), nil
		},
		CanonicalPackageKey: func(pkg *types.Package) (string, error) {
			if pkg == nil || pkg.Path() == "" {
				return "", fmt.Errorf("missing test package key")
			}
			return pkg.Path(), nil
		},
	}}
}

func validPlanDigestMetadata() PlanDigestMetadata {
	return PlanDigestMetadata{
		CoroABI:             PhysicalABIV0,
		SchedulerABI:        SchedulerNoneABIV0,
		PanicABI:            PanicLegacyABIV0,
		FuncRepABI:          FuncRepABIV0,
		LoweringFactsSchema: LoweringFactsSchema,
		LoweringFactsDigest: strings.Repeat("0", sha256.Size*2),
		TargetTriple:        "x86_64-unknown-linux-gnu",
		TargetCPU:           "",
		TargetFeatures:      "+sse2,-avx",
		TargetABI:           "",
		PointerBits:         64,
		Endianness:          "little",
		DataLayout:          "e-m:e-p:64:64-i64:64-n8:16:32:64-S128",
	}
}
