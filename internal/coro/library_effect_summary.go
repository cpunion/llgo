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
	LibraryEffectSummarySchema = "llgo.coro.library-effect-summary.v1"

	LibraryEffectSummaryDigestDomain = "llgo.coro.library-effect-summary.digest.v1"

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
	'E', 'F', 'F', 'E', 'C', 'T', 0, 1,
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
	ID             FunctionID  `json:"id"`
	ABIHash        string      `json:"abi_hash"`
	Effect         Effect      `json:"effect"`
	Exec           ExecFlags   `json:"exec"`
	FuncRep        FuncRep     `json:"func_rep"`
	Primary        PrimaryKind `json:"primary"`
	PrimarySymbol  string      `json:"primary_symbol"`
	RawPlainSymbol string      `json:"raw_plain_symbol,omitempty"`
}

// LibraryEffectSummary is one package producer summary. Functions contains
// only definitions physically present in this artifact. A missing function is
// therefore not a no-suspend fact: consumers must keep it opaque or reject the
// crossing.
type LibraryEffectSummary struct {
	Schema    string                  `json:"schema"`
	Package   string                  `json:"package"`
	Metadata  LibraryEffectMetadata   `json:"metadata"`
	Functions []LibraryEffectFunction `json:"functions"`
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
	if function.Primary == PrimaryExternal {
		return fmt.Errorf("coro: library function %q has no producer-owned primary", function.ID)
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

// Verify checks schema, target metadata, function facts, and unique identities.
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
	seen := make(map[FunctionID]struct{}, len(summary.Functions))
	for index, function := range summary.Functions {
		if err := function.validate(); err != nil {
			return fmt.Errorf("coro: library effect function %d: %w", index, err)
		}
		if _, duplicate := seen[function.ID]; duplicate {
			return fmt.Errorf("coro: duplicate library effect function ID %q", function.ID)
		}
		seen[function.ID] = struct{}{}
	}
	return nil
}

func (summary LibraryEffectSummary) canonical() (LibraryEffectSummary, error) {
	if err := summary.Verify(); err != nil {
		return LibraryEffectSummary{}, err
	}
	ret := summary
	ret.Functions = append([]LibraryEffectFunction(nil), summary.Functions...)
	if ret.Functions == nil {
		ret.Functions = make([]LibraryEffectFunction, 0)
	}
	sort.Slice(ret.Functions, func(i, j int) bool {
		return ret.Functions[i].ID < ret.Functions[j].ID
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
// package/function facts are errors rather than conservative sync defaults.
func ParseLibraryEffectSummaryRecords(data []byte) ([]LibraryEffectSummary, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("coro: library effect summary section is empty")
	}
	summaries := make([]LibraryEffectSummary, 0, 1)
	packages := make(map[string]struct{})
	functions := make(map[FunctionID]string)
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
		IgnoreBody:       true,
		External:         ExternalKnown,
		OverrideExternal: true,
		NeedsDispatch:    function.FuncRep == Dispatch,
	}, nil
}

// LibraryEffectIndex is an immutable lookup table constructed only after
// metadata compatibility has been checked.
type LibraryEffectIndex struct {
	functions map[FunctionID]LibraryEffectFunction
}

func NewLibraryEffectIndex(summaries []LibraryEffectSummary, consumer LibraryEffectMetadata) (*LibraryEffectIndex, error) {
	index := &LibraryEffectIndex{functions: make(map[FunctionID]LibraryEffectFunction)}
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
