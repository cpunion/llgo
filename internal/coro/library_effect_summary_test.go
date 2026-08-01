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
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func testLibraryEffectMetadata() LibraryEffectMetadata {
	return LibraryEffectMetadata{
		FunctionIDSchema:   FunctionIDSchema,
		CoroABI:            PhysicalABIV1,
		SchedulerABI:       SchedulerProgramBootstrapChannelWorkerClosedStaticSpawnABIV0,
		PanicABI:           PanicExplicitStatusABIV0,
		FuncRepABI:         FuncRepABIV1,
		TargetTriple:       "x86_64-unknown-linux-gnu",
		PointerBits:        64,
		Endianness:         "little",
		DataLayout:         "e-m:e-p270:32:32-p271:32:32-p272:64:64-p:64:64",
		TargetCapabilities: NewTargetCapabilities(true, true, false),
	}
}

func testLibraryForeignCallable(t *testing.T, pkg string) LibraryEffectForeignCallable {
	t.Helper()
	signature := "func(*byte, uintptr) int32"
	identity, err := FreezeCallableIdentityCertificate(CallableIdentityCertificate{
		CanonicalFunctionIdentity: pkg + "/foreign/read",
		LinkIdentity:              pkg + "/C.read",
		CallableABI:               "typed.v1/" + pkg,
		TypedABISignature:         signature,
		PhysicalSymbol:            pkg + "_read",
		PhysicalABISignature:      signature,
		Origin:                    CallableIdentityOriginManagedCDeclaration,
		Evidence:                  CallableIdentityEvidenceManagedFinalShape,
	})
	if err != nil {
		t.Fatal(err)
	}
	contract := CallableContract{
		ID:       "foreign.v1",
		Progress: ProgressMayBlock,
		Affinity: AffinityCallerThread,
		Reentry:  ReentryManagedCallback,
		Memory:   MemoryBorrowUntilReturn,
	}
	digest, err := CallableContractBehaviorDigest(contract.ID, contract)
	if err != nil {
		t.Fatal(err)
	}
	contract.ID = ContractID(string(contract.ID) + "/" + digest)
	certificateID := sha256.Sum256([]byte(pkg + ":" + identity.ID + ":" + digest))
	certificate := CallableContractCertificate{
		ID:                        hex.EncodeToString(certificateID[:]),
		CanonicalFunctionIdentity: identity.CanonicalFunctionIdentity,
		LinkIdentity:              identity.LinkIdentity,
		Contract:                  contract,
		ContractDigest:            digest,
		Scope:                     CallableContractScopeDeclaration,
		CallableABI:               identity.CallableABI,
		CallableABIExplicit:       identity.CallableABIExplicit,
		TypedABISignature:         identity.TypedABISignature,
		PhysicalSymbol:            identity.PhysicalSymbol,
		PhysicalABISignature:      identity.PhysicalABISignature,
	}
	if err := certificate.Validate(); err != nil {
		t.Fatal(err)
	}
	return LibraryEffectForeignCallable{
		Function:    FunctionID("llgo.function.v0:" + pkg + ".foreign"),
		Identity:    identity,
		Contract:    certificate,
		HasContract: true,
	}
}

func testLibraryEffectSummary(t *testing.T, pkg string, reverse bool) LibraryEffectSummary {
	t.Helper()
	functions := []LibraryEffectFunction{
		{
			ID:            "llgo.function.v0:alpha",
			ABIHash:       strings.Repeat("1", 64),
			Effect:        NoSuspend,
			Exec:          IRQUnsafe,
			FuncRep:       DirectPlain,
			Primary:       PrimaryPlain,
			ManagedEntry:  ManagedEntryPlain,
			PrimarySymbol: pkg + ".Alpha",
		},
		{
			ID:             "llgo.function.v0:beta",
			ABIHash:        strings.Repeat("2", 64),
			Effect:         MayPark.Join(AwaitStructured),
			Exec:           MayUnwind | NeedsCleanupFrame,
			FuncRep:        Dispatch,
			Primary:        PrimaryCoroutine,
			ManagedEntry:   ManagedEntryCoroutine,
			PrimarySymbol:  pkg + ".Beta$coro",
			RawPlainSymbol: pkg + ".Beta",
		},
	}
	alpha := functions[0]
	if reverse {
		functions[0], functions[1] = functions[1], functions[0]
	}
	return LibraryEffectSummary{
		Schema:           LibraryEffectSummarySchema,
		Package:          pkg,
		Metadata:         testLibraryEffectMetadata(),
		Functions:        functions,
		ForeignCallables: []LibraryEffectForeignCallable{testLibraryForeignCallable(t, pkg)},
		ExportBindings: []LibraryEffectExportBinding{{
			Symbol:               pkg + "_alpha",
			ABIHash:              strings.Repeat("3", 64),
			Function:             alpha.ID,
			ManagedPrimary:       alpha.Primary,
			ManagedPrimarySymbol: alpha.PrimarySymbol,
		}},
	}
}

func TestLibraryEffectSummaryCanonicalRecordAndImportPolicy(t *testing.T) {
	first := testLibraryEffectSummary(t, "example/a", true)
	second := testLibraryEffectSummary(t, "example/a", false)
	firstData, err := first.MarshalStable()
	if err != nil {
		t.Fatal(err)
	}
	secondData, err := second.MarshalStable()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstData, secondData) {
		t.Fatalf("library summary depends on insertion order:\n%s\n%s", firstData, secondData)
	}
	parsed, err := ParseLibraryEffectSummary(firstData)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Functions) != 2 || parsed.Functions[0].ID != "llgo.function.v0:alpha" {
		t.Fatalf("unexpected canonical functions: %+v", parsed.Functions)
	}

	recordA, err := parsed.MarshalRecord()
	if err != nil {
		t.Fatal(err)
	}
	other := testLibraryEffectSummary(t, "example/b", false)
	for index := range other.Functions {
		other.Functions[index].ID = FunctionID(string(other.Functions[index].ID) + ".b")
		other.Functions[index].PrimarySymbol = "example/b." + other.Functions[index].PrimarySymbol
		if other.Functions[index].RawPlainSymbol != "" {
			other.Functions[index].RawPlainSymbol = "example/b." + other.Functions[index].RawPlainSymbol
		}
	}
	for index := range other.ExportBindings {
		binding := &other.ExportBindings[index]
		binding.Function = FunctionID(string(binding.Function) + ".b")
		for _, function := range other.Functions {
			if function.ID == binding.Function {
				binding.ManagedPrimary = function.Primary
				binding.ManagedPrimarySymbol = function.PrimarySymbol
				break
			}
		}
	}
	recordB, err := other.MarshalRecord()
	if err != nil {
		t.Fatal(err)
	}
	records, err := ParseLibraryEffectSummaryRecords(append(recordA, recordB...))
	if err != nil {
		t.Fatal(err)
	}
	index, err := NewLibraryEffectIndex(records, testLibraryEffectMetadata())
	if err != nil {
		t.Fatal(err)
	}
	function, ok := index.Lookup("llgo.function.v0:beta")
	if !ok {
		t.Fatal("imported library function is missing")
	}
	policy, err := function.ImportedPolicy()
	if err != nil {
		t.Fatal(err)
	}
	if policy.Effect != function.Effect || policy.Exec != function.Exec ||
		policy.ManagedEntry != function.ManagedEntry ||
		!policy.IgnoreBody || !policy.OverrideExternal ||
		policy.External != ExternalKnown || !policy.NeedsDispatch {
		t.Fatalf("imported policy = %+v", policy)
	}
	foreign := first.ForeignCallables[0]
	importedForeign, ok := index.LookupForeignFunction(foreign.Function)
	if !ok || importedForeign != foreign {
		t.Fatalf("imported foreign callable = %+v, want %+v", importedForeign, foreign)
	}
	if byIdentity, ok := index.LookupForeignIdentity(foreign.Identity.ID); !ok || byIdentity != foreign {
		t.Fatalf("imported foreign identity = %+v, want %+v", byIdentity, foreign)
	}
	export := first.ExportBindings[0]
	if importedExport, ok := index.LookupExport(export.Symbol); !ok || importedExport != export {
		t.Fatalf("imported export binding = %+v, want %+v", importedExport, export)
	}
}

func TestLibraryEffectSummaryCarriesOutcomePlainCapability(t *testing.T) {
	summary := testLibraryEffectSummary(t, "example/outcome", false)
	summary.Functions = []LibraryEffectFunction{{
		ID:              "llgo.function.v0:outcome",
		ABIHash:         strings.Repeat("4", 64),
		Effect:          OutcomeStructured,
		Exec:            MayUnwind,
		FuncRep:         DirectCoro,
		Primary:         PrimaryCoroutine,
		ManagedEntry:    ManagedEntryOutcomePlain,
		AtomicCost:      7,
		AtomicCostProof: AtomicCostLeaf,
		PrimarySymbol:   "example/outcome.Leaf$outcome",
	}}
	summary.ForeignCallables = nil
	summary.ExportBindings = nil
	data, err := summary.MarshalStable()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseLibraryEffectSummary(data)
	if err != nil {
		t.Fatal(err)
	}
	function := parsed.Functions[0]
	policy, err := function.ImportedPolicy()
	if err != nil {
		t.Fatal(err)
	}
	if policy.ManagedEntry != ManagedEntryOutcomePlain ||
		policy.AtomicCost != function.AtomicCost || policy.AtomicCostProof != AtomicCostLeaf {
		t.Fatalf("imported outcome policy = %+v", policy)
	}
	dag := summary
	dag.Functions = append([]LibraryEffectFunction(nil), summary.Functions...)
	dag.Functions[0].AtomicCostProof = AtomicCostDAG
	dagData, err := dag.MarshalStable()
	if err != nil {
		t.Fatalf("marshal outcome DAG capability: %v", err)
	}
	dagParsed, err := ParseLibraryEffectSummary(dagData)
	if err != nil {
		t.Fatalf("parse outcome DAG capability: %v", err)
	}
	dagPolicy, err := dagParsed.Functions[0].ImportedPolicy()
	if err != nil {
		t.Fatalf("import outcome DAG capability: %v", err)
	}
	if dagPolicy.AtomicCostProof != AtomicCostDAG || dagPolicy.AtomicCost != function.AtomicCost {
		t.Fatalf("imported outcome DAG policy = %+v", dagPolicy)
	}

	for _, mutate := range []func(*LibraryEffectFunction){
		func(function *LibraryEffectFunction) { function.AtomicCost = 0 },
		func(function *LibraryEffectFunction) { function.AtomicCostProof = AtomicCostUnproven },
		func(function *LibraryEffectFunction) { function.ManagedEntry = ManagedEntryCoroutine },
	} {
		invalid := summary
		invalid.Functions = append([]LibraryEffectFunction(nil), summary.Functions...)
		mutate(&invalid.Functions[0])
		if _, err := invalid.MarshalStable(); err == nil {
			t.Fatalf("invalid outcome library capability was accepted: %+v", invalid.Functions[0])
		}
	}
}

func TestLibraryEffectSummaryFailsClosed(t *testing.T) {
	summary := testLibraryEffectSummary(t, "example/a", false)
	data, err := summary.MarshalStable()
	if err != nil {
		t.Fatal(err)
	}
	noncanonical := append([]byte(" "), data...)
	if _, err := ParseLibraryEffectSummary(noncanonical); err == nil || !strings.Contains(err.Error(), "canonical") {
		t.Fatalf("noncanonical summary error = %v", err)
	}
	unknown := bytes.Replace(data, []byte(`"functions"`), []byte(`"future"`), 1)
	if _, err := ParseLibraryEffectSummary(unknown); err == nil {
		t.Fatal("unknown summary field unexpectedly accepted")
	}

	record, err := summary.MarshalRecord()
	if err != nil {
		t.Fatal(err)
	}
	tampered := append([]byte(nil), record...)
	tampered[len(tampered)-1] ^= 1
	if _, err := ParseLibraryEffectSummaryRecords(tampered); err == nil {
		t.Fatal("tampered summary record unexpectedly accepted")
	}
	if _, err := ParseLibraryEffectSummaryRecords(record[:len(record)-1]); err == nil {
		t.Fatal("truncated summary record unexpectedly accepted")
	}

	consumer := testLibraryEffectMetadata()
	consumer.PanicABI = PanicLegacyABIV0
	if _, err := NewLibraryEffectIndex([]LibraryEffectSummary{summary}, consumer); err == nil ||
		!strings.Contains(err.Error(), "incompatible") {
		t.Fatalf("ABI mismatch error = %v", err)
	}

	invalid := summary
	invalid.Functions = append([]LibraryEffectFunction(nil), summary.Functions...)
	invalid.Functions[0].Effect = MayPark
	if _, err := invalid.MarshalStable(); err == nil || !strings.Contains(err.Error(), "disagrees") {
		t.Fatalf("effect/primary mismatch error = %v", err)
	}
}

func TestLibraryEffectSummaryV2ProducerFactsFailClosed(t *testing.T) {
	summary := testLibraryEffectSummary(t, "example/a", false)

	t.Run("contract data requires explicit presence", func(t *testing.T) {
		invalid := summary
		invalid.ForeignCallables = append([]LibraryEffectForeignCallable(nil), summary.ForeignCallables...)
		invalid.ForeignCallables[0].HasContract = false
		if _, err := invalid.MarshalStable(); err == nil || !strings.Contains(err.Error(), "without presence") {
			t.Fatalf("contract presence error = %v", err)
		}
	})

	t.Run("contract identity must match declaration", func(t *testing.T) {
		invalid := summary
		invalid.ForeignCallables = append([]LibraryEffectForeignCallable(nil), summary.ForeignCallables...)
		other := testLibraryForeignCallable(t, "example/other")
		invalid.ForeignCallables[0].Identity = other.Identity
		if _, err := invalid.MarshalStable(); err == nil || !strings.Contains(err.Error(), "contract") {
			t.Fatalf("contract identity error = %v", err)
		}
	})

	t.Run("export must reference an emitted managed function", func(t *testing.T) {
		invalid := summary
		invalid.ExportBindings = append([]LibraryEffectExportBinding(nil), summary.ExportBindings...)
		invalid.ExportBindings[0].Function = "llgo.function.v0:missing"
		if _, err := invalid.MarshalStable(); err == nil || !strings.Contains(err.Error(), "missing managed function") {
			t.Fatalf("missing managed function error = %v", err)
		}
	})

	t.Run("export primary must match managed fact", func(t *testing.T) {
		invalid := summary
		invalid.ExportBindings = append([]LibraryEffectExportBinding(nil), summary.ExportBindings...)
		invalid.ExportBindings[0].ManagedPrimarySymbol += ".stale"
		if _, err := invalid.MarshalStable(); err == nil || !strings.Contains(err.Error(), "managed primary disagrees") {
			t.Fatalf("managed primary mismatch error = %v", err)
		}
	})
}

func TestLibraryEffectSummaryV2RejectsCrossArchiveConflicts(t *testing.T) {
	first := testLibraryEffectSummary(t, "example/a", false)
	otherSummary := func() LibraryEffectSummary {
		other := testLibraryEffectSummary(t, "example/b", false)
		managedIDs := make(map[FunctionID]FunctionID, len(other.Functions))
		for index := range other.Functions {
			old := other.Functions[index].ID
			next := FunctionID(string(old) + ".b")
			other.Functions[index].ID = next
			managedIDs[old] = next
		}
		for index := range other.ExportBindings {
			other.ExportBindings[index].Function = managedIDs[other.ExportBindings[index].Function]
		}
		return other
	}
	marshal := func(summary LibraryEffectSummary) []byte {
		t.Helper()
		record, err := summary.MarshalRecord()
		if err != nil {
			t.Fatal(err)
		}
		return record
	}
	firstRecord := marshal(first)

	t.Run("foreign identity", func(t *testing.T) {
		other := otherSummary()
		other.ForeignCallables[0] = first.ForeignCallables[0]
		other.ForeignCallables[0].Function = "llgo.function.v0:example-b.foreign"
		if _, err := ParseLibraryEffectSummaryRecords(append(firstRecord, marshal(other)...)); err == nil ||
			!strings.Contains(err.Error(), "foreign callable identity") {
			t.Fatalf("duplicate foreign identity error = %v", err)
		}
	})

	t.Run("export symbol", func(t *testing.T) {
		other := otherSummary()
		other.ExportBindings[0].Symbol = first.ExportBindings[0].Symbol
		if _, err := ParseLibraryEffectSummaryRecords(append(firstRecord, marshal(other)...)); err == nil ||
			!strings.Contains(err.Error(), "export symbol") {
			t.Fatalf("duplicate export symbol error = %v", err)
		}
	})
}
