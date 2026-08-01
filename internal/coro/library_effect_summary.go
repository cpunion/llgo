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
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"unicode/utf8"
)

const (
	// LibraryEffectSummarySchema is the producer ABI summary embedded in LLGo
	// package objects and archives. It is deliberately independent from the
	// whole-program Summary and PlanDigest schemas: consumer demand may change,
	// while these producer effects and physically available entries may not.
	LibraryEffectSummarySchema = "llgo.coro.library-effect-summary.v3"

	LibraryEffectSummaryDigestDomain = "llgo.coro.library-effect-summary.digest.v3"

	// LibraryEffectSummarySection is the portable object-section identity.
	// Mach-O emission uses the same leaf name in an explicit segment.
	LibraryEffectSummarySection = "llgo_coro_effect"

	// LibraryEffectArchiveMember is the reserved package-archive index member.
	// Its payload is a minimal native object whose compiler-only section holds
	// the format-neutral record. Keeping it separate from LTO members lets an
	// importer read the record without loading LLVM bitcode, while every native
	// linker still sees a valid object member.
	LibraryEffectArchiveMember = "__.LLGOCORO"

	maxLibraryEffectSummaryPayload = 32 << 20
)

var libraryEffectSummaryRecordMagic = [16]byte{
	'L', 'L', 'G', 'O', 'C', 'O', 'R', 'O',
	'E', 'F', 'F', 'E', 'C', 'T', 0, 3,
}

const libraryEffectSummaryRecordHeaderSize = len(libraryEffectSummaryRecordMagic) + 4 + sha256.Size

// LibraryEffectArchiveMemberMaxBytes bounds one metadata object, including
// object headers and alignment. The framed payload retains its tighter 32 MiB
// bound when parsed.
const LibraryEffectArchiveMemberMaxBytes = 64 << 20

// LibraryEffectMetadata freezes every target-wide property needed to decide
// whether a consumer may import the function facts without reinterpretation.
// Empty CPU/features/target ABI are canonical target defaults.
type LibraryEffectMetadata struct {
	FunctionIDSchema   string             `json:"function_id_schema"`
	CoroABI            string             `json:"coro_abi"`
	SchedulerABI       string             `json:"scheduler_abi"`
	PanicABI           string             `json:"panic_abi"`
	FuncRepABI         string             `json:"func_rep_abi"`
	TargetTriple       string             `json:"target_triple"`
	TargetCPU          string             `json:"target_cpu"`
	TargetFeatures     string             `json:"target_features"`
	TargetABI          string             `json:"target_abi"`
	PointerBits        int                `json:"pointer_bits"`
	Endianness         string             `json:"endianness"`
	DataLayout         string             `json:"data_layout"`
	TargetCapabilities TargetCapabilities `json:"target_capabilities"`
}

// LibraryEffectFunction is the complete producer fact needed to color a
// bodyless imported Go declaration and select an entry already present in the
// library. ABIHash binds the effective structural function signature; it is
// not recovered from a symbol address. A RawPlainSymbol is an additional exact
// legacy crossing, never a second managed source implementation.
type LibraryEffectFunction struct {
	ID              FunctionID       `json:"id"`
	ABIHash         string           `json:"abi_hash"`
	Effect          Effect           `json:"effect"`
	Exec            ExecFlags        `json:"exec"`
	FuncRep         FuncRep          `json:"func_rep"`
	Primary         PrimaryKind      `json:"primary"`
	ManagedEntry    ManagedEntryKind `json:"managed_entry"`
	AtomicCost      uint64           `json:"atomic_cost"`
	AtomicCostProof AtomicCostProof  `json:"atomic_cost_proof"`
	PrimarySymbol   string           `json:"primary_symbol"`
	RawPlainSymbol  string           `json:"raw_plain_symbol,omitempty"`
}

// LibraryEffectForeignCallable publishes one exact producer-side C
// declaration. Identity owns its physical symbol and typed ABI. Contract is
// optional because an address-publication-only declaration can have an exact
// identity without authorizing an invocation policy.
//
// This record contains producer facts only. It does not select inline, same-M,
// worker, event, or raw-host lowering for any consumer call site.
type LibraryEffectForeignCallable struct {
	Function    FunctionID                  `json:"function"`
	Identity    CallableIdentityCertificate `json:"identity"`
	Contract    CallableContractCertificate `json:"contract"`
	HasContract bool                        `json:"has_contract"`
}

// Validate checks one pointer-free foreign producer fact.
func (callable LibraryEffectForeignCallable) Validate() error {
	return callable.validate()
}

// ImportedPolicy projects one compatibility-checked producer record into the
// exact target-neutral SSA declaration policy. It does not choose a worker,
// same-M, event, raw-host, or trusted-inline operation.
func (callable LibraryEffectForeignCallable) ImportedPolicy() (SSAFunctionPolicy, error) {
	if err := callable.validate(); err != nil {
		return SSAFunctionPolicy{}, err
	}
	policy := SSAFunctionPolicy{
		CallableIdentityCertificate: callable.Identity,
		IgnoreBody:                  true,
		External:                    ExternalUnknownForeign,
		OverrideExternal:            true,
		Exec:                        BlockForeign | IRQUnsafe,
	}
	if !callable.HasContract {
		return policy, nil
	}
	external, exec, err := CallableDeclarationPolicy(callable.Contract.Contract)
	if err != nil {
		return SSAFunctionPolicy{}, err
	}
	policy.External = external
	policy.Exec = exec
	policy.CallableContractCertificate = callable.Contract
	return policy, nil
}

func (callable LibraryEffectForeignCallable) validate() error {
	if err := callable.Function.validate(); err != nil {
		return err
	}
	if err := callable.Identity.Validate(); err != nil {
		return fmt.Errorf("coro: library foreign callable %q identity: %w", callable.Function, err)
	}
	if !callable.HasContract {
		if !callable.Contract.IsZero() {
			return fmt.Errorf("coro: library foreign callable %q has contract data without presence", callable.Function)
		}
		return nil
	}
	if err := callable.Contract.Validate(); err != nil {
		return fmt.Errorf("coro: library foreign callable %q contract: %w", callable.Function, err)
	}
	if callable.Contract.Scope != CallableContractScopeDeclaration {
		return fmt.Errorf("coro: library foreign callable %q has non-declaration contract scope %q", callable.Function, callable.Contract.Scope)
	}
	if err := ValidateCallableContractIdentity(callable.Identity, callable.Contract); err != nil {
		return fmt.Errorf("coro: library foreign callable %q: %w", callable.Function, err)
	}
	return nil
}

// LibraryEffectExportBinding freezes a source export before a physical code
// address exists. ABIHash binds the effective raw C signature. Function and
// ManagedPrimarySymbol name the exact managed producer target.
//
// The binding alone grants no raw-entry capability. Code generation must
// separately prove and publish the versioned ingress adapter that owns Symbol.
type LibraryEffectExportBinding struct {
	Symbol               string      `json:"symbol"`
	ABIHash              string      `json:"abi_hash"`
	Function             FunctionID  `json:"function"`
	ManagedPrimary       PrimaryKind `json:"managed_primary"`
	ManagedPrimarySymbol string      `json:"managed_primary_symbol"`
}

// Validate checks one pointer-free export-to-managed binding.
func (binding LibraryEffectExportBinding) Validate() error {
	return binding.validate()
}

func (binding LibraryEffectExportBinding) validate() error {
	if err := validateStableIdentityText("library export symbol", binding.Symbol); err != nil {
		return err
	}
	if err := validateSHA256Hex("library export ABI hash", binding.ABIHash); err != nil {
		return err
	}
	if err := binding.Function.validate(); err != nil {
		return err
	}
	if err := binding.ManagedPrimary.validate(); err != nil {
		return err
	}
	if binding.ManagedPrimary == PrimaryExternal {
		return fmt.Errorf("coro: library export %q has no producer-owned managed primary", binding.Symbol)
	}
	if err := validateStableIdentityText("library export managed primary symbol", binding.ManagedPrimarySymbol); err != nil {
		return err
	}
	return nil
}

// LibraryEffectSummary is one package producer summary. Functions contains
// only managed definitions physically present in this artifact.
// ForeignCallables and ExportBindings contain pointer-free producer facts, not
// consumer invocation choices. A missing record never proves no-suspend,
// executor safety, or a callable raw entry.
type LibraryEffectSummary struct {
	Schema           string                         `json:"schema"`
	Package          string                         `json:"package"`
	Metadata         LibraryEffectMetadata          `json:"metadata"`
	Functions        []LibraryEffectFunction        `json:"functions"`
	ForeignCallables []LibraryEffectForeignCallable `json:"foreign_callables"`
	ExportBindings   []LibraryEffectExportBinding   `json:"export_bindings"`
}

func (metadata LibraryEffectMetadata) validate() error {
	if metadata.FunctionIDSchema != FunctionIDSchema {
		return fmt.Errorf("coro: library effect FunctionID schema %q, want %q", metadata.FunctionIDSchema, FunctionIDSchema)
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{"coroutine ABI", metadata.CoroABI},
		{"scheduler ABI", metadata.SchedulerABI},
		{"panic ABI", metadata.PanicABI},
		{"function representation ABI", metadata.FuncRepABI},
		{"target triple", metadata.TargetTriple},
		{"data layout", metadata.DataLayout},
	} {
		if err := validateStableIdentityText(field.name, field.value); err != nil {
			return fmt.Errorf("coro: library effect metadata: %w", err)
		}
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{"target CPU", metadata.TargetCPU},
		{"target features", metadata.TargetFeatures},
		{"target ABI", metadata.TargetABI},
	} {
		if field.value != "" {
			if err := validateStableIdentityText(field.name, field.value); err != nil {
				return fmt.Errorf("coro: library effect metadata: %w", err)
			}
		}
	}
	if metadata.PointerBits <= 0 || metadata.PointerBits%8 != 0 {
		return fmt.Errorf("coro: library effect metadata has invalid pointer width %d", metadata.PointerBits)
	}
	switch metadata.Endianness {
	case "little", "big":
	default:
		return fmt.Errorf("coro: library effect metadata has invalid endianness %q", metadata.Endianness)
	}
	if !metadata.TargetCapabilities.Valid() {
		return fmt.Errorf("coro: library effect metadata has invalid target capabilities %d", metadata.TargetCapabilities)
	}
	return nil
}

func (function LibraryEffectFunction) validate() error {
	if err := function.ID.validate(); err != nil {
		return err
	}
	if err := validateSHA256Hex("library function ABI hash", function.ABIHash); err != nil {
		return err
	}
	if err := function.Effect.Validate(); err != nil {
		return err
	}
	if err := function.Exec.Validate(); err != nil {
		return err
	}
	if err := function.FuncRep.Validate(); err != nil {
		return err
	}
	if err := function.Primary.validate(); err != nil {
		return err
	}
	if err := function.ManagedEntry.Validate(); err != nil {
		return err
	}
	if err := function.AtomicCostProof.Validate(); err != nil {
		return err
	}
	if function.Primary == PrimaryExternal {
		return fmt.Errorf("coro: library function %q has no producer-owned primary", function.ID)
	}
	if function.ManagedEntry == ManagedEntryNone {
		return fmt.Errorf("coro: library function %q has no producer managed entry", function.ID)
	}
	if function.Effect.MaySuspend() != (function.Primary == PrimaryCoroutine) {
		return fmt.Errorf(
			"coro: library function %q effect %s disagrees with primary %s",
			function.ID, function.Effect, function.Primary,
		)
	}
	switch function.FuncRep {
	case DirectPlain:
		if function.Primary != PrimaryPlain {
			return fmt.Errorf("coro: library function %q has direct-plain representation with %s primary", function.ID, function.Primary)
		}
	case DirectCoro:
		if function.Primary != PrimaryCoroutine {
			return fmt.Errorf("coro: library function %q has direct-coro representation with %s primary", function.ID, function.Primary)
		}
	}
	switch function.ManagedEntry {
	case ManagedEntryPlain:
		if function.Primary != PrimaryPlain {
			return fmt.Errorf("coro: library function %q has a plain entry with %s primary", function.ID, function.Primary)
		}
	case ManagedEntryCoroutine:
		if function.Primary != PrimaryCoroutine {
			return fmt.Errorf("coro: library function %q has a coroutine entry with %s primary", function.ID, function.Primary)
		}
	case ManagedEntryOutcomePlain:
		if function.Primary != PrimaryCoroutine || function.FuncRep != DirectCoro ||
			function.Effect != OutcomeStructured || function.Exec&^MayUnwind != 0 {
			return fmt.Errorf("coro: library function %q has an invalid outcome-plain entry capability", function.ID)
		}
	}
	if function.ManagedEntry == ManagedEntryOutcomePlain {
		if function.AtomicCostProof != AtomicCostLeaf || function.AtomicCost == 0 {
			return fmt.Errorf("coro: library function %q has an outcome entry without a leaf atomic-cost proof", function.ID)
		}
	} else if function.AtomicCostProof != AtomicCostUnproven || function.AtomicCost != 0 {
		return fmt.Errorf("coro: library function %q has atomic-cost metadata without an outcome entry", function.ID)
	}
	if err := validateStableIdentityText("library function primary symbol", function.PrimarySymbol); err != nil {
		return err
	}
	if function.RawPlainSymbol != "" {
		if err := validateStableIdentityText("library function raw-plain symbol", function.RawPlainSymbol); err != nil {
			return err
		}
	}
	return nil
}

// Verify checks schema, target metadata, producer facts, and unique identities.
func (summary LibraryEffectSummary) Verify() error {
	if summary.Schema != LibraryEffectSummarySchema {
		return fmt.Errorf("coro: library effect summary schema %q, want %q", summary.Schema, LibraryEffectSummarySchema)
	}
	if err := validateStableIdentityText("library package identity", summary.Package); err != nil {
		return err
	}
	if err := summary.Metadata.validate(); err != nil {
		return err
	}
	managed := make(map[FunctionID]LibraryEffectFunction, len(summary.Functions))
	for index, function := range summary.Functions {
		if err := function.validate(); err != nil {
			return fmt.Errorf("coro: library effect function %d: %w", index, err)
		}
		if _, duplicate := managed[function.ID]; duplicate {
			return fmt.Errorf("coro: duplicate library effect function ID %q", function.ID)
		}
		managed[function.ID] = function
	}
	foreignFunctions := make(map[FunctionID]struct{}, len(summary.ForeignCallables))
	foreignIdentities := make(map[string]struct{}, len(summary.ForeignCallables))
	for index, callable := range summary.ForeignCallables {
		if err := callable.validate(); err != nil {
			return fmt.Errorf("coro: library foreign callable %d: %w", index, err)
		}
		if _, duplicate := foreignFunctions[callable.Function]; duplicate {
			return fmt.Errorf("coro: duplicate library foreign callable function %q", callable.Function)
		}
		foreignFunctions[callable.Function] = struct{}{}
		if _, duplicate := foreignIdentities[callable.Identity.ID]; duplicate {
			return fmt.Errorf("coro: duplicate library foreign callable identity %q", callable.Identity.ID)
		}
		foreignIdentities[callable.Identity.ID] = struct{}{}
	}
	exportSymbols := make(map[string]struct{}, len(summary.ExportBindings))
	exportFunctions := make(map[FunctionID]struct{}, len(summary.ExportBindings))
	for index, binding := range summary.ExportBindings {
		if err := binding.validate(); err != nil {
			return fmt.Errorf("coro: library export binding %d: %w", index, err)
		}
		function, ok := managed[binding.Function]
		if !ok {
			return fmt.Errorf("coro: library export %q references missing managed function %q", binding.Symbol, binding.Function)
		}
		if function.Primary != binding.ManagedPrimary ||
			function.PrimarySymbol != binding.ManagedPrimarySymbol {
			return fmt.Errorf(
				"coro: library export %q managed primary disagrees with function %q",
				binding.Symbol, binding.Function,
			)
		}
		if _, duplicate := exportSymbols[binding.Symbol]; duplicate {
			return fmt.Errorf("coro: duplicate library export symbol %q", binding.Symbol)
		}
		exportSymbols[binding.Symbol] = struct{}{}
		if _, duplicate := exportFunctions[binding.Function]; duplicate {
			return fmt.Errorf("coro: managed function %q has duplicate library exports", binding.Function)
		}
		exportFunctions[binding.Function] = struct{}{}
	}
	return nil
}

func (summary LibraryEffectSummary) canonical() (LibraryEffectSummary, error) {
	if err := summary.Verify(); err != nil {
		return LibraryEffectSummary{}, err
	}
	ret := summary
	ret.Functions = append([]LibraryEffectFunction(nil), summary.Functions...)
	ret.ForeignCallables = append([]LibraryEffectForeignCallable(nil), summary.ForeignCallables...)
	ret.ExportBindings = append([]LibraryEffectExportBinding(nil), summary.ExportBindings...)
	if ret.Functions == nil {
		ret.Functions = make([]LibraryEffectFunction, 0)
	}
	if ret.ForeignCallables == nil {
		ret.ForeignCallables = make([]LibraryEffectForeignCallable, 0)
	}
	if ret.ExportBindings == nil {
		ret.ExportBindings = make([]LibraryEffectExportBinding, 0)
	}
	sort.Slice(ret.Functions, func(i, j int) bool {
		return ret.Functions[i].ID < ret.Functions[j].ID
	})
	sort.Slice(ret.ForeignCallables, func(i, j int) bool {
		if ret.ForeignCallables[i].Function != ret.ForeignCallables[j].Function {
			return ret.ForeignCallables[i].Function < ret.ForeignCallables[j].Function
		}
		return ret.ForeignCallables[i].Identity.ID < ret.ForeignCallables[j].Identity.ID
	})
	sort.Slice(ret.ExportBindings, func(i, j int) bool {
		if ret.ExportBindings[i].Symbol != ret.ExportBindings[j].Symbol {
			return ret.ExportBindings[i].Symbol < ret.ExportBindings[j].Symbol
		}
		return ret.ExportBindings[i].Function < ret.ExportBindings[j].Function
	})
	return ret, nil
}

// MarshalStable emits the only accepted JSON representation. Importers reject
// semantically equivalent but noncanonical JSON so omitted zero-valued ABI
// fields cannot silently acquire meaning.
func (summary LibraryEffectSummary) MarshalStable() ([]byte, error) {
	canonical, err := summary.canonical()
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(canonical)
	if err != nil {
		return nil, fmt.Errorf("coro: marshal library effect summary: %w", err)
	}
	return data, nil
}

// Digest returns a domain-separated SHA-256 identity for the canonical bytes.
func (summary LibraryEffectSummary) Digest() (string, error) {
	data, err := summary.MarshalStable()
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(LibraryEffectSummaryDigestDomain))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(data)
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// ParseLibraryEffectSummary accepts only canonical, strictly versioned JSON.
func ParseLibraryEffectSummary(data []byte) (LibraryEffectSummary, error) {
	if !utf8.Valid(data) {
		return LibraryEffectSummary{}, fmt.Errorf("coro: library effect summary is not valid UTF-8")
	}
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return LibraryEffectSummary{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var summary LibraryEffectSummary
	if err := decoder.Decode(&summary); err != nil {
		return LibraryEffectSummary{}, fmt.Errorf("coro: decode library effect summary: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return LibraryEffectSummary{}, fmt.Errorf("coro: trailing JSON value in library effect summary")
		}
		return LibraryEffectSummary{}, fmt.Errorf("coro: decode trailing library effect summary data: %w", err)
	}
	canonical, err := summary.canonical()
	if err != nil {
		return LibraryEffectSummary{}, err
	}
	want, err := json.Marshal(canonical)
	if err != nil {
		return LibraryEffectSummary{}, fmt.Errorf("coro: marshal canonical library effect summary: %w", err)
	}
	if !bytes.Equal(data, want) {
		return LibraryEffectSummary{}, fmt.Errorf("coro: library effect summary is not canonical")
	}
	return canonical, nil
}

// MarshalRecord frames one summary for an object section. The digest precedes
// the payload so archive scanners can reject corruption before trusting JSON.
func (summary LibraryEffectSummary) MarshalRecord() ([]byte, error) {
	payload, err := summary.MarshalStable()
	if err != nil {
		return nil, err
	}
	if len(payload) > maxLibraryEffectSummaryPayload {
		return nil, fmt.Errorf("coro: library effect summary payload is too large: %d bytes", len(payload))
	}
	digestText, err := summary.Digest()
	if err != nil {
		return nil, err
	}
	digest, err := hex.DecodeString(digestText)
	if err != nil || len(digest) != sha256.Size {
		return nil, fmt.Errorf("coro: invalid library effect summary digest %q", digestText)
	}
	record := make([]byte, libraryEffectSummaryRecordHeaderSize+len(payload))
	copy(record, libraryEffectSummaryRecordMagic[:])
	binary.BigEndian.PutUint32(record[len(libraryEffectSummaryRecordMagic):], uint32(len(payload)))
	copy(record[len(libraryEffectSummaryRecordMagic)+4:], digest)
	copy(record[libraryEffectSummaryRecordHeaderSize:], payload)
	return record, nil
}

// ParseLibraryEffectSummaryRecords parses an exact concatenation of framed
// object-section records. Padding, truncation, digest mismatch, and duplicate
// package/producer facts are errors rather than conservative sync defaults.
func ParseLibraryEffectSummaryRecords(data []byte) ([]LibraryEffectSummary, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("coro: library effect summary section is empty")
	}
	summaries := make([]LibraryEffectSummary, 0, 1)
	packages := make(map[string]struct{})
	functions := make(map[FunctionID]string)
	foreignFunctions := make(map[FunctionID]string)
	foreignIdentities := make(map[string]string)
	exportSymbols := make(map[string]string)
	for offset := 0; offset < len(data); {
		if len(data)-offset < libraryEffectSummaryRecordHeaderSize {
			return nil, fmt.Errorf("coro: truncated library effect summary record at offset %d", offset)
		}
		header := data[offset : offset+libraryEffectSummaryRecordHeaderSize]
		if !bytes.Equal(header[:len(libraryEffectSummaryRecordMagic)], libraryEffectSummaryRecordMagic[:]) {
			return nil, fmt.Errorf("coro: invalid library effect summary record magic at offset %d", offset)
		}
		rawLength := binary.BigEndian.Uint32(header[len(libraryEffectSummaryRecordMagic):])
		if rawLength > maxLibraryEffectSummaryPayload {
			return nil, fmt.Errorf("coro: library effect summary record at offset %d is too large: %d bytes", offset, rawLength)
		}
		length := int(rawLength)
		end := offset + libraryEffectSummaryRecordHeaderSize + length
		if end > len(data) {
			return nil, fmt.Errorf("coro: truncated library effect summary payload at offset %d", offset)
		}
		payload := data[offset+libraryEffectSummaryRecordHeaderSize : end]
		summary, err := ParseLibraryEffectSummary(payload)
		if err != nil {
			return nil, fmt.Errorf("coro: parse library effect summary record at offset %d: %w", offset, err)
		}
		digestText, err := summary.Digest()
		if err != nil {
			return nil, err
		}
		digest, _ := hex.DecodeString(digestText)
		have := header[len(libraryEffectSummaryRecordMagic)+4 : libraryEffectSummaryRecordHeaderSize]
		if !bytes.Equal(have, digest) {
			return nil, fmt.Errorf("coro: library effect summary digest mismatch at offset %d", offset)
		}
		if _, duplicate := packages[summary.Package]; duplicate {
			return nil, fmt.Errorf("coro: duplicate library effect package %q", summary.Package)
		}
		packages[summary.Package] = struct{}{}
		for _, function := range summary.Functions {
			if previous, duplicate := functions[function.ID]; duplicate {
				return nil, fmt.Errorf(
					"coro: library effect function %q appears in packages %q and %q",
					function.ID, previous, summary.Package,
				)
			}
			functions[function.ID] = summary.Package
		}
		for _, callable := range summary.ForeignCallables {
			if previous, duplicate := foreignFunctions[callable.Function]; duplicate {
				return nil, fmt.Errorf(
					"coro: library foreign callable function %q appears in packages %q and %q",
					callable.Function, previous, summary.Package,
				)
			}
			foreignFunctions[callable.Function] = summary.Package
			if previous, duplicate := foreignIdentities[callable.Identity.ID]; duplicate {
				return nil, fmt.Errorf(
					"coro: library foreign callable identity %q appears in packages %q and %q",
					callable.Identity.ID, previous, summary.Package,
				)
			}
			foreignIdentities[callable.Identity.ID] = summary.Package
		}
		for _, binding := range summary.ExportBindings {
			if previous, duplicate := exportSymbols[binding.Symbol]; duplicate {
				return nil, fmt.Errorf(
					"coro: library export symbol %q appears in packages %q and %q",
					binding.Symbol, previous, summary.Package,
				)
			}
			exportSymbols[binding.Symbol] = summary.Package
		}
		summaries = append(summaries, summary)
		offset = end
	}
	return summaries, nil
}

// ValidateLibraryEffectCompatibility requires an exact target and runtime ABI
// match. A future schema may distinguish required from merely available target
// capabilities; v1 deliberately fails closed instead of guessing.
func ValidateLibraryEffectCompatibility(producer, consumer LibraryEffectMetadata) error {
	if err := producer.validate(); err != nil {
		return fmt.Errorf("coro: invalid producer library effect metadata: %w", err)
	}
	if err := consumer.validate(); err != nil {
		return fmt.Errorf("coro: invalid consumer library effect metadata: %w", err)
	}
	if producer != consumer {
		return fmt.Errorf("coro: incompatible library effect metadata: producer=%+v consumer=%+v", producer, consumer)
	}
	return nil
}

// ImportedPolicy converts an already compatibility-checked producer fact into
// an analyzer seed for a bodyless managed declaration. It does not grant an
// entry or ABI bridge; lowering must separately use and verify the published
// symbol and ABIHash.
func (function LibraryEffectFunction) ImportedPolicy() (SSAFunctionPolicy, error) {
	if err := function.validate(); err != nil {
		return SSAFunctionPolicy{}, err
	}
	return SSAFunctionPolicy{
		Effect:           function.Effect,
		Exec:             function.Exec,
		ManagedEntry:     function.ManagedEntry,
		AtomicCost:       function.AtomicCost,
		AtomicCostProof:  function.AtomicCostProof,
		IgnoreBody:       true,
		External:         ExternalKnown,
		OverrideExternal: true,
		NeedsDispatch:    function.FuncRep == Dispatch,
	}, nil
}

// LibraryEffectIndex is an immutable lookup table constructed only after
// metadata compatibility has been checked.
type LibraryEffectIndex struct {
	functions         map[FunctionID]LibraryEffectFunction
	foreignFunctions  map[FunctionID]LibraryEffectForeignCallable
	foreignIdentities map[string]LibraryEffectForeignCallable
	exportSymbols     map[string]LibraryEffectExportBinding
}

func NewLibraryEffectIndex(summaries []LibraryEffectSummary, consumer LibraryEffectMetadata) (*LibraryEffectIndex, error) {
	index := &LibraryEffectIndex{
		functions:         make(map[FunctionID]LibraryEffectFunction),
		foreignFunctions:  make(map[FunctionID]LibraryEffectForeignCallable),
		foreignIdentities: make(map[string]LibraryEffectForeignCallable),
		exportSymbols:     make(map[string]LibraryEffectExportBinding),
	}
	for summaryIndex, summary := range summaries {
		canonical, err := summary.canonical()
		if err != nil {
			return nil, fmt.Errorf("coro: library effect summary %d: %w", summaryIndex, err)
		}
		if err := ValidateLibraryEffectCompatibility(canonical.Metadata, consumer); err != nil {
			return nil, fmt.Errorf("coro: library effect summary %q: %w", canonical.Package, err)
		}
		for _, function := range canonical.Functions {
			if _, duplicate := index.functions[function.ID]; duplicate {
				return nil, fmt.Errorf("coro: duplicate imported library effect function %q", function.ID)
			}
			index.functions[function.ID] = function
		}
		for _, callable := range canonical.ForeignCallables {
			if _, duplicate := index.foreignFunctions[callable.Function]; duplicate {
				return nil, fmt.Errorf("coro: duplicate imported library foreign callable function %q", callable.Function)
			}
			if _, duplicate := index.foreignIdentities[callable.Identity.ID]; duplicate {
				return nil, fmt.Errorf("coro: duplicate imported library foreign callable identity %q", callable.Identity.ID)
			}
			index.foreignFunctions[callable.Function] = callable
			index.foreignIdentities[callable.Identity.ID] = callable
		}
		for _, binding := range canonical.ExportBindings {
			if _, duplicate := index.exportSymbols[binding.Symbol]; duplicate {
				return nil, fmt.Errorf("coro: duplicate imported library export symbol %q", binding.Symbol)
			}
			index.exportSymbols[binding.Symbol] = binding
		}
	}
	return index, nil
}

func (index *LibraryEffectIndex) Lookup(id FunctionID) (LibraryEffectFunction, bool) {
	if index == nil {
		return LibraryEffectFunction{}, false
	}
	function, ok := index.functions[id]
	return function, ok
}

// LookupForeignFunction returns one exact imported C declaration by its stable
// producer FunctionID.
func (index *LibraryEffectIndex) LookupForeignFunction(id FunctionID) (LibraryEffectForeignCallable, bool) {
	if index == nil {
		return LibraryEffectForeignCallable{}, false
	}
	callable, ok := index.foreignFunctions[id]
	return callable, ok
}

// LookupForeignIdentity returns one exact imported C declaration by its
// pointer-free callable identity certificate ID.
func (index *LibraryEffectIndex) LookupForeignIdentity(id string) (LibraryEffectForeignCallable, bool) {
	if index == nil {
		return LibraryEffectForeignCallable{}, false
	}
	callable, ok := index.foreignIdentities[id]
	return callable, ok
}

// LookupExport returns the exact producer binding for one external C symbol.
// The record does not itself authorize a raw entry; lowering must validate the
// versioned ingress adapter separately.
func (index *LibraryEffectIndex) LookupExport(symbol string) (LibraryEffectExportBinding, bool) {
	if index == nil {
		return LibraryEffectExportBinding{}, false
	}
	binding, ok := index.exportSymbols[symbol]
	return binding, ok
}
