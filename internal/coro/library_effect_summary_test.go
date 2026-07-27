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

func testLibraryEffectSummary(pkg string, reverse bool) LibraryEffectSummary {
	functions := []LibraryEffectFunction{
		{
			ID:            "llgo.function.v0:alpha",
			ABIHash:       strings.Repeat("1", 64),
			Effect:        NoSuspend,
			Exec:          IRQUnsafe,
			FuncRep:       DirectPlain,
			Primary:       PrimaryPlain,
			PrimarySymbol: pkg + ".Alpha",
		},
		{
			ID:             "llgo.function.v0:beta",
			ABIHash:        strings.Repeat("2", 64),
			Effect:         MayPark.Join(AwaitStructured),
			Exec:           MayUnwind | NeedsCleanupFrame,
			FuncRep:        Dispatch,
			Primary:        PrimaryCoroutine,
			PrimarySymbol:  pkg + ".Beta$coro",
			RawPlainSymbol: pkg + ".Beta",
		},
	}
	if reverse {
		functions[0], functions[1] = functions[1], functions[0]
	}
	return LibraryEffectSummary{
		Schema:    LibraryEffectSummarySchema,
		Package:   pkg,
		Metadata:  testLibraryEffectMetadata(),
		Functions: functions,
	}
}

func TestLibraryEffectSummaryCanonicalRecordAndImportPolicy(t *testing.T) {
	first := testLibraryEffectSummary("example/a", true)
	second := testLibraryEffectSummary("example/a", false)
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
	other := testLibraryEffectSummary("example/b", false)
	for index := range other.Functions {
		other.Functions[index].ID = FunctionID(string(other.Functions[index].ID) + ".b")
		other.Functions[index].PrimarySymbol = "example/b." + other.Functions[index].PrimarySymbol
		if other.Functions[index].RawPlainSymbol != "" {
			other.Functions[index].RawPlainSymbol = "example/b." + other.Functions[index].RawPlainSymbol
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
		!policy.IgnoreBody || !policy.OverrideExternal ||
		policy.External != ExternalKnown || !policy.NeedsDispatch {
		t.Fatalf("imported policy = %+v", policy)
	}
}

func TestLibraryEffectSummaryFailsClosed(t *testing.T) {
	summary := testLibraryEffectSummary("example/a", false)
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
