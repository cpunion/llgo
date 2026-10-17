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
	"fmt"
	"go/token"
	"go/types"
	"strings"
)

const DeliveryRecipeVersionV1 = 1

// DeliveryRecipeRecord is one optional, pointer-free producer fact. It says
// how an exact callable's existing typed result storage is initialized and
// released while its parent has not yet delivered the result. It is not an
// effect inherited by arbitrary callers, an int/pointer ownership inference,
// or a second completion/cancellation protocol.
//
// ABI hashes come from the same final physical-signature authority as library
// summaries. They must not be reconstructed from a function name or address.
// Publication additionally requires the frontend's physical borrowed-output
// and cleanup proof; validating this record alone does not grant that proof.
type DeliveryRecipeRecord struct {
	RequiresState  bool       `json:"requires_state,omitzero"`
	Version        uint32     `json:"version"`
	Producer       FunctionID `json:"producer"`
	ProducerABI    string     `json:"producer_abi"`
	Initializer    FunctionID `json:"initializer"`
	InitializerABI string     `json:"initializer_abi"`
	Drop           FunctionID `json:"drop"`
	DropABI        string     `json:"drop_abi"`
	// DropView is an optional, producer-owned physical ABI adapter. Drop and
	// DropABI still name the actual logical helper in the one effect graph.
	// A transparent wrapper may reorder or fix arguments without inventing a
	// new helper SSA body or changing that helper's logical identity.
	DropView    DeliveryHelperViewRecord `json:"drop_view,omitzero"`
	ResultSize  uint64                   `json:"result_size"`
	ResultAlign uint64                   `json:"result_align"`
}

type DeliveryHelperViewRecord struct {
	Symbol  string `json:"symbol"`
	ABIHash string `json:"abi"`
}

func (view DeliveryHelperViewRecord) IsZero() bool { return view == (DeliveryHelperViewRecord{}) }

func (view DeliveryHelperViewRecord) Validate() error {
	if view.IsZero() {
		return nil
	}
	if view.Symbol == "" || strings.ContainsAny(view.Symbol, "\x00\r\n\t ") {
		return fmt.Errorf("coro: delivery helper view requires one exact physical symbol")
	}
	return validateSHA256Hex("delivery helper view ABI", view.ABIHash)
}

func (record DeliveryRecipeRecord) DropCallableABI() string {
	if !record.DropView.IsZero() {
		return record.DropView.ABIHash
	}
	return record.DropABI
}

func (record DeliveryRecipeRecord) IsZero() bool {
	return record == (DeliveryRecipeRecord{})
}

// Validate rejects partial presence and incompatible schema/layout facts. An
// absent recipe is represented by absence in its containing optional field,
// not by passing a zero record to Validate.
func (record DeliveryRecipeRecord) Validate() error {
	if record.Version != DeliveryRecipeVersionV1 {
		return fmt.Errorf("coro: delivery recipe version %d, want %d", record.Version, DeliveryRecipeVersionV1)
	}
	for _, item := range []struct {
		role string
		id   FunctionID
		hash string
	}{
		{"producer", record.Producer, record.ProducerABI},
		{"initializer", record.Initializer, record.InitializerABI},
		{"drop", record.Drop, record.DropABI},
	} {
		if err := item.id.validate(); err != nil {
			return fmt.Errorf("coro: delivery %s identity: %w", item.role, err)
		}
		if err := validateSHA256Hex("delivery "+item.role+" ABI", item.hash); err != nil {
			return err
		}
	}
	if record.Producer == record.Initializer || record.Producer == record.Drop || record.Initializer == record.Drop {
		return fmt.Errorf("coro: delivery producer, initializer, and drop must have distinct exact identities")
	}
	if err := record.DropView.Validate(); err != nil {
		return err
	}
	if record.ResultSize == 0 || record.ResultAlign == 0 ||
		record.ResultAlign&(record.ResultAlign-1) != 0 || record.ResultSize%record.ResultAlign != 0 {
		return fmt.Errorf("coro: delivery recipe has invalid result layout size=%d align=%d", record.ResultSize, record.ResultAlign)
	}
	return nil
}

// DeliveryCallable is the existing typed callable fact supplied by the
// frontend or a compatibility-checked library import. Signature is normalized
// exactly once by that authority: receivers are explicit parameters, variadic
// arguments are their slice parameter, and no type parameters remain.
type DeliveryCallable struct {
	ID        FunctionID
	ABIHash   string
	Signature *types.Signature
	// ResultSize/ResultAlign are the producer's existing target-layout facts.
	// Helper callables return no source values and leave these fields zero.
	ResultSize  uint64
	ResultAlign uint64
}

// DeliveryRecipe freezes the checked typed view of one producer record. Its
// fields are private; accessors return immutable compiler types or value copies.
// In particular there is no mutable public resource-custody bit or callback.
type DeliveryRecipe struct {
	record      DeliveryRecipeRecord
	signature   *types.Signature
	result      *types.Struct
	initializer *types.Signature
	drop        *types.Signature
}

func (recipe DeliveryRecipe) Record() DeliveryRecipeRecord { return recipe.record }
func (recipe DeliveryRecipe) IsZero() bool                 { return recipe.record.IsZero() }
func (recipe DeliveryRecipe) ResultType() *types.Struct    { return recipe.result }

// FreezeDeliveryRecipe is shared by exact static and descriptor publication.
// It binds all three function identities/ABIs and checks the full initializer
// and drop signatures against the producer's exact typed result tuple. Runtime
// dynamic dispatch consumes the resulting publication, not a second recipe
// inferred from a descriptor entry's code address.
func FreezeDeliveryRecipe(
	record DeliveryRecipeRecord,
	producer, initializer, drop DeliveryCallable,
) (DeliveryRecipe, error) {
	if err := record.Validate(); err != nil {
		return DeliveryRecipe{}, err
	}
	for _, item := range []struct {
		role string
		want FunctionID
		hash string
		got  DeliveryCallable
	}{
		{"producer", record.Producer, record.ProducerABI, producer},
		{"initializer", record.Initializer, record.InitializerABI, initializer},
		{"drop", record.Drop, record.DropCallableABI(), drop},
	} {
		if item.got.ID != item.want || item.got.ABIHash != item.hash {
			return DeliveryRecipe{}, fmt.Errorf("coro: delivery %s does not match its exact identity and ABI", item.role)
		}
		if err := validateDeliverySignature(item.got.Signature); err != nil {
			return DeliveryRecipe{}, fmt.Errorf("coro: delivery %s: %w", item.role, err)
		}
	}
	if producer.Signature.Results().Len() == 0 {
		return DeliveryRecipe{}, fmt.Errorf("coro: delivery producer has no result storage")
	}
	if producer.ResultSize != record.ResultSize || producer.ResultAlign != record.ResultAlign {
		return DeliveryRecipe{}, fmt.Errorf("coro: delivery result layout does not match the exact producer ABI")
	}
	if initializer.ResultSize != 0 || initializer.ResultAlign != 0 || drop.ResultSize != 0 || drop.ResultAlign != 0 {
		return DeliveryRecipe{}, fmt.Errorf("coro: delivery initializer and drop must not publish source results")
	}
	result, wantInit, wantDrop, err := DeliveryHelperSignatures(producer.Signature)
	if err != nil {
		return DeliveryRecipe{}, err
	}
	if !types.Identical(initializer.Signature, wantInit) {
		return DeliveryRecipe{}, fmt.Errorf("coro: delivery initializer signature %s, want %s", initializer.Signature, wantInit)
	}
	if !types.Identical(drop.Signature, wantDrop) {
		return DeliveryRecipe{}, fmt.Errorf("coro: delivery drop signature %s, want %s", drop.Signature, wantDrop)
	}
	return DeliveryRecipe{
		record: record, signature: producer.Signature, result: result,
		initializer: initializer.Signature, drop: drop.Signature,
	}, nil
}

func validateDeliverySignature(signature *types.Signature) error {
	if signature == nil || signature.Recv() != nil || signature.Variadic() ||
		signature.TypeParams().Len() != 0 || signature.RecvTypeParams().Len() != 0 ||
		TypeContainsUnresolvedTypeParam(signature, make(map[types.Type]bool)) {
		return fmt.Errorf("delivery requires a normalized non-generic callable signature")
	}
	return nil
}

// DeliveryHelperSignatures is the single typed contract used by source
// borrowed-result declarations and archive recipe binding. It does not grant
// physical custody: the caller still needs a protected output/cleanup proof.
func DeliveryHelperSignatures(producer *types.Signature) (result *types.Struct, initializer, drop *types.Signature, err error) {
	if err = validateDeliverySignature(producer); err != nil {
		return
	}
	if producer.Results().Len() == 0 {
		err = fmt.Errorf("coro: delivery producer has no result storage")
		return
	}
	result = ResultSlotType(producer.Results())
	initializer, drop = deliveryHelperSignatures(producer, result)
	return
}

func deliveryHelperSignatures(producer *types.Signature, result *types.Struct) (*types.Signature, *types.Signature) {
	output := types.NewParam(token.NoPos, nil, "out", types.NewPointer(result))
	init := types.NewSignatureType(nil, nil, nil, types.NewTuple(output), nil, false)
	parameters := make([]*types.Var, 0, producer.Params().Len()+2)
	parameters = append(parameters, output, types.NewParam(token.NoPos, nil, "env", types.Typ[types.UnsafePointer]))
	for index := 0; index < producer.Params().Len(); index++ {
		parameters = append(parameters, types.NewParam(token.NoPos, nil, "", producer.Params().At(index).Type()))
	}
	drop := types.NewSignatureType(nil, nil, nil, types.NewTuple(parameters...), nil, false)
	return init, drop
}

// DeliveryCallKind selects a physical consumer, not another effect fixed
// point. NoDelivery is the exact zero-cost result for ordinary static calls
// and closed dynamic target sets without recipes.
type DeliveryCallKind uint8

const (
	NoDelivery DeliveryCallKind = iota
	StaticDelivery
	DescriptorDelivery
)

// DeliveryCallPlan is the common result used by static and dynamic lowering.
// DescriptorDelivery requires a checked descriptor publication; it does not
// authorize reading an optional tail from the current base-only descriptor ABI.
type DeliveryCallPlan struct {
	Kind   DeliveryCallKind
	Static DeliveryRecipe
}

// PlanDeliveryCall resolves only explicit frozen producer facts. The lookup
// must be the compilation's existing exact function inventory, not a symbol
// heuristic. A missing recipe in a closed set means nonresource; an open Go
// descriptor requires the optional capability probe. Raw foreign calls cannot
// acquire this managed-parent guarantee.
//
// Only ordinary calls use this API. Spawn and source defer consumers retain
// their existing discarded-result/cleanup-shield routes; rejecting one here is
// an internal routing error, never a new restriction on valid Go source.
func PlanDeliveryCall(
	call SSACallPlan, signature *types.Signature,
	lookup func(FunctionID) (DeliveryRecipe, bool),
) (DeliveryCallPlan, error) {
	if lookup == nil {
		return DeliveryCallPlan{}, fmt.Errorf("coro: delivery call has no exact recipe inventory")
	}
	var selected DeliveryRecipe
	hasRecipe := false
	for _, target := range call.Targets {
		recipe, found := lookup(target)
		if !found {
			continue
		}
		if recipe.IsZero() || recipe.record.Producer != target ||
			!types.Identical(recipe.signature, signature) {
			return DeliveryCallPlan{}, fmt.Errorf("coro: delivery call target %q has no matching frozen typed recipe", target)
		}
		selected, hasRecipe = recipe, true
	}
	if !hasRecipe && !call.Open {
		return DeliveryCallPlan{}, nil
	}
	if !hasRecipe && (call.RawPlain || call.Transport != ManagedTransport) {
		return DeliveryCallPlan{}, nil
	}
	if signature == nil {
		return DeliveryCallPlan{}, fmt.Errorf("coro: delivery call requires its existing normalized signature")
	}
	if signature.Results().Len() == 0 {
		return DeliveryCallPlan{}, nil
	}
	if call.Kind != CallDirect || call.RawPlain || call.Transport != ManagedTransport {
		return DeliveryCallPlan{}, fmt.Errorf("coro: resource delivery requires an ordinary managed call")
	}
	if call.Open {
		if call.Rep != Dispatch || (call.Unresolved != UnknownManagedDispatch && call.Unresolved != UnknownManagedInterfaceDispatch) {
			return DeliveryCallPlan{}, fmt.Errorf("coro: open resource delivery requires a managed descriptor boundary")
		}
		return DeliveryCallPlan{Kind: DescriptorDelivery}, nil
	}
	if call.Rep == Dispatch {
		return DeliveryCallPlan{Kind: DescriptorDelivery}, nil
	}
	if call.Rep != DirectPlain && call.Rep != DirectCoro {
		return DeliveryCallPlan{}, fmt.Errorf("coro: resource delivery has an invalid function representation")
	}
	if len(call.Targets) != 1 {
		return DeliveryCallPlan{}, fmt.Errorf("coro: static resource delivery requires one exact target")
	}
	return DeliveryCallPlan{Kind: StaticDelivery, Static: selected}, nil
}
