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
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"unicode/utf8"
)

// SummarySchema is the experimental wire schema for deterministic plan
// snapshots. Version v4 is intentionally not an archive ABI: producer
// artifacts use the separate LibraryEffectSummarySchema, and cache identity
// uses PlanDigestSchema.
const SummarySchema = "llgo.coro.plan.v4"

// SummaryMetadata identifies ABI and target properties that affect an
// experimental plan snapshot. Empty fields are permitted during early
// analysis. This v4 type must not be used as an archive compatibility record;
// LibraryEffectMetadata owns that strict target/ABI contract.
type SummaryMetadata struct {
	CoroABI      string `json:"coro_abi"`
	SchedulerABI string `json:"scheduler_abi"`
	PanicABI     string `json:"panic_abi"`
	TargetTriple string `json:"target_triple"`
}

// FunctionSummary is the stable, pointer-free form of FunctionPlan.
type FunctionSummary struct {
	ID                      FunctionID   `json:"id"`
	DeclaredEffect          Effect       `json:"declared_effect"`
	LocalEffect             Effect       `json:"local_effect"`
	Effect                  Effect       `json:"effect"`
	DeclaredExec            ExecFlags    `json:"declared_exec"`
	LocalExec               ExecFlags    `json:"local_exec"`
	Exec                    ExecFlags    `json:"exec"`
	Demand                  Demand       `json:"demand"`
	ManagedDemand           Demand       `json:"managed_demand"`
	RawPlainDemand          bool         `json:"raw_plain_demand"`
	Emission                BodyEmission `json:"emission"`
	FuncRep                 FuncRep      `json:"func_rep"`
	External                ExternalKind `json:"external"`
	Recursive               bool         `json:"recursive"`
	TrustedBoundedRecursion bool         `json:"trusted_bounded_recursion"`
	Primary                 PrimaryKind  `json:"primary"`
	RawPlainOnly            bool         `json:"raw_plain_only"`
	RawPlainEntry           bool         `json:"raw_plain_entry"`
}

// Summary is a stable v4 snapshot used to test the target-independent graph
// plan. It intentionally contains no maps or pointer identities and is neither
// LibraryEffectSummary nor the separate CoroPlanDigest wire format. Exact
// whole-build SSA capabilities such as RawPlainVariant therefore live only in
// SSAPlan and its physical CoroPlanDigest; they never cross a library summary.
type Summary struct {
	Schema    string            `json:"schema"`
	Metadata  SummaryMetadata   `json:"metadata"`
	Functions []FunctionSummary `json:"functions"`
}

// Pointer fields let ParseSummary distinguish a required zero value from a
// field that was omitted or explicitly set to null.
type summaryWire struct {
	Schema    *string                `json:"schema"`
	Metadata  *summaryMetadataWire   `json:"metadata"`
	Functions *[]functionSummaryWire `json:"functions"`
}

type summaryMetadataWire struct {
	CoroABI      *string `json:"coro_abi"`
	SchedulerABI *string `json:"scheduler_abi"`
	PanicABI     *string `json:"panic_abi"`
	TargetTriple *string `json:"target_triple"`
}

type functionSummaryWire struct {
	ID                      *FunctionID   `json:"id"`
	DeclaredEffect          *Effect       `json:"declared_effect"`
	LocalEffect             *Effect       `json:"local_effect"`
	Effect                  *Effect       `json:"effect"`
	DeclaredExec            *ExecFlags    `json:"declared_exec"`
	LocalExec               *ExecFlags    `json:"local_exec"`
	Exec                    *ExecFlags    `json:"exec"`
	Demand                  *Demand       `json:"demand"`
	ManagedDemand           *Demand       `json:"managed_demand"`
	RawPlainDemand          *bool         `json:"raw_plain_demand"`
	Emission                *BodyEmission `json:"emission"`
	FuncRep                 *FuncRep      `json:"func_rep"`
	External                *ExternalKind `json:"external"`
	Recursive               *bool         `json:"recursive"`
	TrustedBoundedRecursion *bool         `json:"trusted_bounded_recursion"`
	Primary                 *PrimaryKind  `json:"primary"`
	RawPlainOnly            *bool         `json:"raw_plain_only"`
	RawPlainEntry           *bool         `json:"raw_plain_entry"`
}

// Summary creates a stable summary of p.
func (p *Plan) Summary(metadata SummaryMetadata) Summary {
	ret := Summary{
		Schema:    SummarySchema,
		Metadata:  metadata,
		Functions: make([]FunctionSummary, 0),
	}
	if p == nil {
		return ret
	}
	ret.Functions = make([]FunctionSummary, 0, len(p.functions))
	for _, fn := range p.functions {
		ret.Functions = append(ret.Functions, FunctionSummary{
			ID:                      fn.ID,
			DeclaredEffect:          fn.DeclaredEffect,
			LocalEffect:             fn.LocalEffect,
			Effect:                  fn.Effect,
			DeclaredExec:            fn.DeclaredExec,
			LocalExec:               fn.LocalExec,
			Exec:                    fn.Exec,
			Demand:                  fn.Demand,
			ManagedDemand:           fn.ManagedDemand,
			RawPlainDemand:          fn.RawPlainDemand,
			Emission:                fn.Emission,
			FuncRep:                 fn.FuncRep,
			External:                fn.External,
			Recursive:               fn.Recursive,
			TrustedBoundedRecursion: fn.TrustedBoundedRecursion,
			Primary:                 fn.Primary,
			RawPlainOnly:            fn.RawPlainOnly,
			RawPlainEntry:           fn.RawPlainEntry,
		})
	}
	return ret
}

// MarshalStable serializes s in canonical FunctionID order.
func (s Summary) MarshalStable() ([]byte, error) {
	canonical, err := s.canonical()
	if err != nil {
		return nil, err
	}
	return json.Marshal(canonical)
}

// Digest returns the SHA-256 digest of the stable serialization.
func (s Summary) Digest() (string, error) {
	data, err := s.MarshalStable()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// ParseSummary parses and validates a summary. Unknown fields are rejected so
// an unsupported newer ABI cannot silently look compatible.
func ParseSummary(data []byte) (Summary, error) {
	if !utf8.Valid(data) {
		return Summary{}, fmt.Errorf("coro: summary is not valid UTF-8")
	}
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return Summary{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var wire summaryWire
	if err := dec.Decode(&wire); err != nil {
		return Summary{}, fmt.Errorf("coro: decode summary: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return Summary{}, fmt.Errorf("coro: trailing JSON value in summary")
		}
		return Summary{}, fmt.Errorf("coro: decode trailing summary data: %w", err)
	}
	summary, err := wire.summary()
	if err != nil {
		return Summary{}, err
	}
	return summary.canonical()
}

func (w summaryWire) summary() (Summary, error) {
	if w.Schema == nil {
		return Summary{}, fmt.Errorf("coro: summary missing required field %q", "schema")
	}
	if *w.Schema != SummarySchema {
		return Summary{}, fmt.Errorf("coro: unsupported summary schema %q", *w.Schema)
	}
	if w.Metadata == nil {
		return Summary{}, fmt.Errorf("coro: summary missing required field %q", "metadata")
	}
	if w.Functions == nil {
		return Summary{}, fmt.Errorf("coro: summary missing required field %q", "functions")
	}
	metadata, err := w.Metadata.metadata()
	if err != nil {
		return Summary{}, err
	}
	ret := Summary{
		Schema:    *w.Schema,
		Metadata:  metadata,
		Functions: make([]FunctionSummary, len(*w.Functions)),
	}
	for i, fn := range *w.Functions {
		parsed, err := fn.summary(i)
		if err != nil {
			return Summary{}, err
		}
		ret.Functions[i] = parsed
	}
	return ret, nil
}

func (w summaryMetadataWire) metadata() (SummaryMetadata, error) {
	fields := []struct {
		name  string
		value *string
	}{
		{"coro_abi", w.CoroABI},
		{"scheduler_abi", w.SchedulerABI},
		{"panic_abi", w.PanicABI},
		{"target_triple", w.TargetTriple},
	}
	for _, field := range fields {
		if field.value == nil {
			return SummaryMetadata{}, fmt.Errorf("coro: summary metadata missing required field %q", field.name)
		}
	}
	return SummaryMetadata{
		CoroABI:      *w.CoroABI,
		SchedulerABI: *w.SchedulerABI,
		PanicABI:     *w.PanicABI,
		TargetTriple: *w.TargetTriple,
	}, nil
}

func (w functionSummaryWire) summary(index int) (FunctionSummary, error) {
	missing := func(name string) (FunctionSummary, error) {
		return FunctionSummary{}, fmt.Errorf("coro: summary function %d missing required field %q", index, name)
	}
	if w.ID == nil {
		return missing("id")
	}
	if w.DeclaredEffect == nil {
		return missing("declared_effect")
	}
	if w.LocalEffect == nil {
		return missing("local_effect")
	}
	if w.Effect == nil {
		return missing("effect")
	}
	if w.DeclaredExec == nil {
		return missing("declared_exec")
	}
	if w.LocalExec == nil {
		return missing("local_exec")
	}
	if w.Exec == nil {
		return missing("exec")
	}
	if w.Demand == nil {
		return missing("demand")
	}
	if w.ManagedDemand == nil {
		return missing("managed_demand")
	}
	if w.RawPlainDemand == nil {
		return missing("raw_plain_demand")
	}
	if w.Emission == nil {
		return missing("emission")
	}
	if w.FuncRep == nil {
		return missing("func_rep")
	}
	if w.External == nil {
		return missing("external")
	}
	if w.Recursive == nil {
		return missing("recursive")
	}
	if w.TrustedBoundedRecursion == nil {
		return missing("trusted_bounded_recursion")
	}
	if w.Primary == nil {
		return missing("primary")
	}
	if w.RawPlainOnly == nil {
		return missing("raw_plain_only")
	}
	if w.RawPlainEntry == nil {
		return missing("raw_plain_entry")
	}
	return FunctionSummary{
		ID:                      *w.ID,
		DeclaredEffect:          *w.DeclaredEffect,
		LocalEffect:             *w.LocalEffect,
		Effect:                  *w.Effect,
		DeclaredExec:            *w.DeclaredExec,
		LocalExec:               *w.LocalExec,
		Exec:                    *w.Exec,
		Demand:                  *w.Demand,
		ManagedDemand:           *w.ManagedDemand,
		RawPlainDemand:          *w.RawPlainDemand,
		Emission:                *w.Emission,
		FuncRep:                 *w.FuncRep,
		External:                *w.External,
		Recursive:               *w.Recursive,
		TrustedBoundedRecursion: *w.TrustedBoundedRecursion,
		Primary:                 *w.Primary,
		RawPlainOnly:            *w.RawPlainOnly,
		RawPlainEntry:           *w.RawPlainEntry,
	}, nil
}

// rejectDuplicateJSONKeys walks the token stream before decoding into structs.
// encoding/json otherwise accepts duplicate object keys and silently keeps the
// last value, which would make an ABI summary ambiguous.
func rejectDuplicateJSONKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := scanJSONValue(dec); err != nil {
		return fmt.Errorf("coro: decode summary: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("coro: trailing JSON value in summary")
		}
		return fmt.Errorf("coro: decode trailing summary data: %w", err)
	}
	return nil
}

func scanJSONValue(dec *json.Decoder) error {
	token, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for dec.More() {
			keyToken, err := dec.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("object key is not a string")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate JSON key %q", key)
			}
			if !isCanonicalJSONKey(key) {
				return fmt.Errorf("non-canonical JSON key %q", key)
			}
			seen[key] = struct{}{}
			if err := scanJSONValue(dec); err != nil {
				return err
			}
		}
		end, err := dec.Token()
		if err != nil {
			return err
		}
		if end != json.Delim('}') {
			return fmt.Errorf("invalid object terminator %v", end)
		}
	case '[':
		for dec.More() {
			if err := scanJSONValue(dec); err != nil {
				return err
			}
		}
		end, err := dec.Token()
		if err != nil {
			return err
		}
		if end != json.Delim(']') {
			return fmt.Errorf("invalid array terminator %v", end)
		}
	default:
		return fmt.Errorf("unexpected delimiter %q", delim)
	}
	return nil
}

func isCanonicalJSONKey(key string) bool {
	if key == "" {
		return false
	}
	for _, ch := range key {
		if ch == '_' || ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' {
			continue
		}
		return false
	}
	return true
}

func (s Summary) canonical() (Summary, error) {
	if s.Schema != SummarySchema {
		return Summary{}, fmt.Errorf("coro: unsupported summary schema %q", s.Schema)
	}
	ret := s
	metadataFields := []struct {
		name  string
		value string
	}{
		{"coro ABI", ret.Metadata.CoroABI},
		{"scheduler ABI", ret.Metadata.SchedulerABI},
		{"panic ABI", ret.Metadata.PanicABI},
		{"target triple", ret.Metadata.TargetTriple},
	}
	for _, field := range metadataFields {
		if !utf8.ValidString(field.value) {
			return Summary{}, fmt.Errorf("coro: summary %s is not valid UTF-8", field.name)
		}
	}
	ret.Functions = append([]FunctionSummary(nil), s.Functions...)
	if ret.Functions == nil {
		ret.Functions = []FunctionSummary{}
	}
	sort.Slice(ret.Functions, func(i, j int) bool {
		return ret.Functions[i].ID < ret.Functions[j].ID
	})
	seen := make(map[FunctionID]struct{}, len(ret.Functions))
	for i := range ret.Functions {
		fn := &ret.Functions[i]
		if err := fn.ID.validate(); err != nil {
			return Summary{}, err
		}
		if _, exists := seen[fn.ID]; exists {
			return Summary{}, fmt.Errorf("coro: duplicate summary function %q", fn.ID)
		}
		seen[fn.ID] = struct{}{}
		for _, item := range []struct {
			name   string
			effect Effect
		}{
			{"declared", fn.DeclaredEffect},
			{"local", fn.LocalEffect},
			{"final", fn.Effect},
		} {
			if err := item.effect.Validate(); err != nil {
				return Summary{}, fmt.Errorf("coro: function %q %s effect: %w", fn.ID, item.name, err)
			}
		}
		fn.DeclaredEffect = fn.DeclaredEffect.Normalize()
		fn.LocalEffect = fn.LocalEffect.Normalize()
		fn.Effect = fn.Effect.Normalize()
		if !fn.LocalEffect.Contains(fn.DeclaredEffect) {
			return Summary{}, fmt.Errorf("coro: function %q local effect does not contain declared effect", fn.ID)
		}
		if !fn.Effect.Contains(fn.LocalEffect) {
			return Summary{}, fmt.Errorf("coro: function %q final effect does not contain local effect", fn.ID)
		}
		for _, item := range []struct {
			name string
			exec ExecFlags
		}{
			{"declared", fn.DeclaredExec},
			{"local", fn.LocalExec},
			{"final", fn.Exec},
		} {
			if err := item.exec.Validate(); err != nil {
				return Summary{}, fmt.Errorf("coro: function %q %s execution flags: %w", fn.ID, item.name, err)
			}
		}
		if !fn.LocalExec.Contains(fn.DeclaredExec) {
			return Summary{}, fmt.Errorf("coro: function %q local execution flags do not contain declared flags", fn.ID)
		}
		if !fn.Exec.Contains(fn.LocalExec) {
			return Summary{}, fmt.Errorf("coro: function %q final execution flags do not contain local flags", fn.ID)
		}
		if extra := fn.Exec &^ fn.LocalExec; extra&^propagatedExecFlags != 0 {
			return Summary{}, fmt.Errorf("coro: function %q propagated non-inheritable execution flags %s", fn.ID, extra&^propagatedExecFlags)
		}
		if err := fn.Demand.Validate(); err != nil {
			return Summary{}, fmt.Errorf("coro: function %q: %w", fn.ID, err)
		}
		if err := fn.ManagedDemand.Validate(); err != nil {
			return Summary{}, fmt.Errorf("coro: function %q managed demand: %w", fn.ID, err)
		}
		if want := aggregateDemand(fn.ManagedDemand, fn.RawPlainDemand); fn.Demand != want {
			return Summary{}, fmt.Errorf("coro: function %q aggregate demand %s does not match managed=%s raw=%t (want %s)", fn.ID, fn.Demand, fn.ManagedDemand, fn.RawPlainDemand, want)
		}
		if err := fn.Emission.Validate(); err != nil {
			return Summary{}, fmt.Errorf("coro: function %q: %w", fn.ID, err)
		}
		if err := fn.FuncRep.Validate(); err != nil {
			return Summary{}, fmt.Errorf("coro: function %q: %w", fn.ID, err)
		}
		if fn.FuncRep == DirectPlain && fn.Effect.MaySuspend() && !fn.RawPlainOnly {
			return Summary{}, fmt.Errorf("coro: suspendable function %q has direct-plain representation", fn.ID)
		}
		if fn.FuncRep == DirectCoro && !fn.Effect.MaySuspend() {
			return Summary{}, fmt.Errorf("coro: non-suspendable function %q has direct-coro representation", fn.ID)
		}
		if err := fn.External.validate(); err != nil {
			return Summary{}, fmt.Errorf("coro: function %q: %w", fn.ID, err)
		}
		expectedEmission := bodyEmissionFor(fn.ManagedDemand, fn.RawPlainDemand, fn.Effect, fn.External)
		if fn.Emission != expectedEmission {
			return Summary{}, fmt.Errorf("coro: function %q emission %s does not match demand %s, effect %s, and external kind %s (want %s)", fn.ID, fn.Emission, fn.Demand, fn.Effect, fn.External, expectedEmission)
		}
		if err := fn.Primary.validate(); err != nil {
			return Summary{}, fmt.Errorf("coro: function %q: %w", fn.ID, err)
		}
		if fn.External == Defined {
			expected := PrimaryPlain
			if fn.Effect.MaySuspend() && !fn.RawPlainOnly {
				expected = PrimaryCoroutine
			}
			if fn.Primary != expected {
				return Summary{}, fmt.Errorf("coro: function %q primary %s does not match effect %s", fn.ID, fn.Primary, fn.Effect)
			}
		} else if fn.Primary != PrimaryExternal {
			return Summary{}, fmt.Errorf("coro: external function %q has non-external primary %s", fn.ID, fn.Primary)
		}
		if fn.RawPlainEntry && fn.External != Defined {
			return Summary{}, fmt.Errorf("coro: external function %q has a raw plain entry", fn.ID)
		}
		if fn.RawPlainEntry && !fn.RawPlainDemand {
			return Summary{}, fmt.Errorf("coro: function %q has a raw plain entry without raw demand", fn.ID)
		}
		if fn.RawPlainOnly != (fn.External == Defined && fn.RawPlainDemand && fn.ManagedDemand == NoDemand) {
			return Summary{}, fmt.Errorf("coro: function %q has inconsistent raw-plain-only state", fn.ID)
		}
		if fn.RawPlainOnly && (fn.Emission != EmitRawPlain || fn.Primary != PrimaryPlain || fn.FuncRep != DirectPlain) {
			return Summary{}, fmt.Errorf("coro: raw-plain-only function %q lacks raw/plain/direct physical selection", fn.ID)
		}
		if fn.TrustedBoundedRecursion && !fn.Recursive {
			return Summary{}, fmt.Errorf("coro: non-recursive function %q has a trusted bounded-recursion proof", fn.ID)
		}
		if fn.Recursive && !fn.TrustedBoundedRecursion && !fn.LocalEffect.Contains(YieldOnly) {
			return Summary{}, fmt.Errorf("coro: recursive function %q lacks yield-only seed", fn.ID)
		}
		if fn.Recursive && !fn.TrustedBoundedRecursion && !fn.LocalExec.Contains(NeedsPreempt) {
			return Summary{}, fmt.Errorf("coro: recursive function %q lacks needs-preempt flag", fn.ID)
		}
		if fn.LocalExec.Contains(NeedsPreempt) && !fn.LocalEffect.Contains(YieldOnly) {
			return Summary{}, fmt.Errorf("coro: preemptible function %q lacks yield-only seed", fn.ID)
		}
		if fn.LocalExec.Contains(BlockForeign) && fn.Effect.MaySuspend() && !fn.RawPlainOnly {
			return Summary{}, fmt.Errorf("coro: blocking foreign function %q also has suspend effect %s", fn.ID, fn.Effect)
		}
		switch fn.External {
		case ExternalUnknownManaged:
			if !fn.Effect.IsOpaque() || !fn.LocalExec.IsOpaque() || fn.FuncRep != Dispatch {
				return Summary{}, fmt.Errorf("coro: unknown managed function %q lacks opaque dispatch plan", fn.ID)
			}
		case ExternalUnknownForeign:
			if !fn.LocalExec.Contains(BlockForeign | IRQUnsafe) {
				return Summary{}, fmt.Errorf("coro: unknown foreign function %q lacks blocking/IRQ-unsafe flags", fn.ID)
			}
		}
	}
	return ret, nil
}
