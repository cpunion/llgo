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
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/tools/go/ssa"
)

// PlanDigestSchema is the independent canonical schema used for archive cache
// identity. It is deliberately separate from SummarySchema: summaries remain
// diagnostic snapshots, while this document covers every lowering plan site.
const PlanDigestSchema = "llgo.coro.plan-digest.v31"

// Current experimental ABI identities. Keeping these in the analysis package
// gives build, cache, and lowering code one version source of truth.
const (
	EntryResolutionABIV0 = "llgo.coro.entry-resolution.v0"
	PhysicalABIV0        = "llgo.coro.physical.v0"
	PhysicalABIV1        = "llgo.coro.physical.v1"
	SchedulerNoneABIV0   = "llgo.coro.scheduler.none.v0"
	// SchedulerChildAwaitABIV0 identifies the child-frame ownership contract.
	// Compiler PhysicalABIV1 bodies additionally name the versioned V2
	// parent-owned outcome hooks in their physical descriptor hash; bootstrap
	// factories retain the original V1 root-sequencing transaction.
	SchedulerChildAwaitABIV0 = "llgo.coro.scheduler.child-await.v0"
	// SchedulerProgramBootstrapABIV1 is the first compiler-owned stackless
	// program root and static single-P prepare/adopt/run driver. It does not
	// include preemption or heterogeneous startup steps.
	SchedulerProgramBootstrapABIV1 = "llgo.coro.scheduler.program-bootstrap.v1"
	// SchedulerProgramBootstrapABIV2 adds conditional compiler safepoints,
	// atomic preemption requests/requeue, and the heterogeneous startup-program
	// contract. It still does not claim spawn, park, timers, or a production
	// source of concurrent runnable Gs.
	SchedulerProgramBootstrapABIV2 = "llgo.coro.scheduler.program-bootstrap.v2"
	// SchedulerProgramBootstrapWorkerABIV0 adds the bounded native foreign-call
	// worker source and its prepare/suspend/resume transaction. It keeps the
	// synchronous Go source API while ensuring a blocking call never owns P.
	SchedulerProgramBootstrapWorkerABIV0 = "llgo.coro.scheduler.program-bootstrap.v2.worker.v0"
	// SchedulerProgramBootstrapChannelABIV0 adds the exact single-channel
	// fast-attempt/park/resume transaction and terminal send-closed status to
	// the runnable v2 scheduler. Channel payload storage remains in the LLVM
	// coroutine frame; no Future/Task object is introduced.
	SchedulerProgramBootstrapChannelABIV0 = "llgo.coro.scheduler.program-bootstrap.v2.channel.v0"
	// SchedulerProgramBootstrapChannelWorkerABIV0 is the explicit combined
	// identity when channel and worker operation sources are both enabled.
	SchedulerProgramBootstrapChannelWorkerABIV0 = "llgo.coro.scheduler.program-bootstrap.v2.channel.v0.worker.v0"
	// SchedulerProgramBootstrapClosedStaticSpawnABIV0 is the explicit superset
	// of SchedulerProgramBootstrapABIV2 that adds compiler-owned begin/commit
	// for one exact closed static `go f(args)` target and normal-main-return
	// cancellation. The runtime never receives a user callback; the compiler
	// creates the child only to its initial suspend before commit.
	SchedulerProgramBootstrapClosedStaticSpawnABIV0 = "llgo.coro.scheduler.program-bootstrap.v2.closed-static-spawn.v0"
	// SchedulerProgramBootstrapWorkerClosedStaticSpawnABIV0 combines the
	// bounded worker source with closed static spawn.
	SchedulerProgramBootstrapWorkerClosedStaticSpawnABIV0 = "llgo.coro.scheduler.program-bootstrap.v2.worker.v0.closed-static-spawn.v0"
	// SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0 is the explicit
	// combined identity when both independently gated capabilities are active.
	SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0 = "llgo.coro.scheduler.program-bootstrap.v2.channel.v0.closed-static-spawn.v0"
	// SchedulerProgramBootstrapChannelWorkerClosedStaticSpawnABIV0 is the
	// complete identity for all three independently gated scheduler sources.
	SchedulerProgramBootstrapChannelWorkerClosedStaticSpawnABIV0 = "llgo.coro.scheduler.program-bootstrap.v2.channel.v0.worker.v0.closed-static-spawn.v0"
	// SchedulerProgramBootstrapChannelHostOperationClosedStaticSpawnABIV0
	// replaces the native pthread transport with the host-pull external
	// operation catalog. Both share logical scalar completion semantics, but
	// their physical request/cancel ABIs must never share archive identity.
	SchedulerProgramBootstrapChannelHostOperationClosedStaticSpawnABIV0 = "llgo.coro.scheduler.program-bootstrap.v2.channel.v0.host-operation.v1.closed-static-spawn.v0"
	PanicLegacyABIV0                                                    = "llgo.coro.panic.legacy.v0"
	// PanicExplicitStatusABIV0 identifies compiler-carried panic outcomes. A
	// managed child publishes into its parent's CompletionRecord; a root
	// publishes into the task-local PanicRecord. Parent-frame direct-child scopes
	// and frame-rooted owner-local records implement static/dynamic cleanup and
	// direct recover without TLS; compiler-materialized implicit faults use the
	// same recoverable payload overlay. Structured Goexit uses the shared
	// payload-free completion channel; range-over-func cross-frame defer and
	// other unsupported language shapes remain independently fail-closed.
	PanicExplicitStatusABIV0 = "llgo.coro.panic.explicit-status.v0"
	FuncRepABIV0             = "llgo.coro.func-rep.v0"
	// FuncRepABIV1 introduces an explicit descriptor/context representation for
	// dynamically consumed Go function values. The first producer/consumer slice
	// supports only one no-capture, non-suspending plain body; unsupported value
	// shapes and call capabilities remain fail-closed.
	FuncRepABIV1 = "llgo.coro.func-rep.v1"
	// FrameRetentionParkABIV2 identifies the sole generic Park state lifetime
	// proof. Source-specific symbols are deliberately absent from this ABI.
	FrameRetentionParkABIV2 = "llgo.coro.frame-retention.park.v2"
)

// PlanDigestMetadata contains every effective ABI and target input that may
// affect coroutine lowering. TargetABI, TargetCPU, and TargetFeatures use the
// empty string for the target's canonical default.
type PlanDigestMetadata struct {
	CoroABI             string `json:"coro_abi"`
	SchedulerABI        string `json:"scheduler_abi"`
	PanicABI            string `json:"panic_abi"`
	FuncRepABI          string `json:"func_rep_abi"`
	FrameRetentionABI   string `json:"frame_retention_abi,omitempty"`
	LoweringFactsSchema string `json:"lowering_facts_schema"`
	LoweringFactsDigest string `json:"lowering_facts_digest"`
	TargetTriple        string `json:"target_triple"`
	TargetCPU           string `json:"target_cpu"`
	TargetFeatures      string `json:"target_features"`
	TargetABI           string `json:"target_abi"`
	PointerBits         int    `json:"pointer_bits"`
	Endianness          string `json:"endianness"`
	DataLayout          string `json:"data_layout"`
}

type planDigestDocument struct {
	Schema            string                       `json:"schema"`
	FunctionIDSchema  string                       `json:"function_id_schema"`
	Metadata          PlanDigestMetadata           `json:"metadata"`
	Roots             []planDigestRoot             `json:"roots"`
	Functions         []planDigestFunction         `json:"functions"`
	Calls             []planDigestCall             `json:"calls"`
	LoweredCalls      []planDigestLoweredCall      `json:"lowered_calls"`
	ElidedCalls       []planDigestElidedCall       `json:"elided_calls,omitempty"`
	ConditionalStores []planDigestConditionalStore `json:"conditional_managed_stores,omitempty"`
	SafeArrayIndexes  []planDigestSafeArrayIndex   `json:"safe_fixed_array_indexes,omitempty"`
	Values            []planDigestValue            `json:"values"`
}

type planDigestRoot struct {
	Function       FunctionID `json:"function"`
	Demand         uint8      `json:"demand"`
	ManagedDemand  uint8      `json:"managed_demand"`
	RawPlainDemand bool       `json:"raw_plain_demand"`
}

type planDigestFunction struct {
	ID                           FunctionID                   `json:"id"`
	IgnoredBody                  bool                         `json:"ignored_body"`
	CallableIdentityCertificate  *CallableIdentityCertificate `json:"callable_identity_certificate,omitempty"`
	CallableContractCertificate  *CallableContractCertificate `json:"callable_contract_certificate,omitempty"`
	ForeignNoBlockCertificate    string                       `json:"foreign_noblock_certificate,omitempty"`
	ForeignSyncCertificate       string                       `json:"foreign_sync_certificate,omitempty"`
	ForeignWorkerCertificate     string                       `json:"foreign_worker_certificate,omitempty"`
	AssemblyNoSuspendCertificate string                       `json:"assembly_nosuspend_certificate,omitempty"`
	DeclaredEffect               uint16                       `json:"declared_effect"`
	LocalEffect                  uint16                       `json:"local_effect"`
	Effect                       uint16                       `json:"effect"`
	DeclaredExec                 uint16                       `json:"declared_exec"`
	LocalExec                    uint16                       `json:"local_exec"`
	Exec                         uint16                       `json:"exec"`
	Demand                       uint8                        `json:"demand"`
	ManagedDemand                uint8                        `json:"managed_demand"`
	RawPlainDemand               bool                         `json:"raw_plain_demand"`
	Emission                     uint8                        `json:"emission"`
	ManagedEntry                 uint8                        `json:"managed_entry"`
	AtomicCost                   uint64                       `json:"atomic_cost"`
	AtomicCostProof              uint8                        `json:"atomic_cost_proof"`
	FuncRep                      uint8                        `json:"func_rep"`
	External                     uint8                        `json:"external"`
	Recursive                    bool                         `json:"recursive"`
	TrustedBoundedRecursion      bool                         `json:"trusted_bounded_recursion"`
	Primary                      uint8                        `json:"primary"`
	RawPlainOnly                 bool                         `json:"raw_plain_only"`
	RawPlainEntry                bool                         `json:"raw_plain_entry"`
	RawPlainVariant              bool                         `json:"raw_plain_variant"`
}

type planDigestCall struct {
	Function               FunctionID       `json:"function"`
	Block                  int              `json:"block"`
	Instruction            int              `json:"instruction"`
	Kind                   uint8            `json:"kind"`
	Rep                    uint8            `json:"rep"`
	Transport              uint8            `json:"transport"`
	Targets                []FunctionID     `json:"targets"`
	Open                   bool             `json:"open"`
	Unresolved             uint8            `json:"unresolved"`
	MayBeNil               bool             `json:"may_be_nil"`
	ExactInterfaceReceiver bool             `json:"exact_interface_receiver,omitempty"`
	SyncDispatch           bool             `json:"sync_dispatch"`
	RawPlain               bool             `json:"raw_plain"`
	RawPlainCertificate    string           `json:"raw_plain_certificate,omitempty"`
	InvocationPolicy       InvocationPolicy `json:"invocation_policy,omitempty"`
	InvocationContract     ContractID       `json:"invocation_contract,omitempty"`
	InvocationABI          string           `json:"invocation_abi,omitempty"`
	InvocationCertificate  string           `json:"invocation_certificate,omitempty"`
}

type planDigestLoweredCall struct {
	Owner                FunctionID `json:"owner"`
	LogicalName          string     `json:"logical_name"`
	Target               FunctionID `json:"target"`
	NoUnwind             bool       `json:"no_unwind"`
	RawPlain             bool       `json:"raw_plain"`
	UnwindOnly           bool       `json:"unwind_only"`
	ExplicitStatusElided bool       `json:"explicit_status_elided"`
}

type planDigestElidedCall struct {
	Function    FunctionID `json:"function"`
	Block       int        `json:"block"`
	Instruction int        `json:"instruction"`
	Elided      bool       `json:"elided"`
	Certificate string     `json:"certificate,omitempty"`
}

type planDigestConditionalStore struct {
	Function    FunctionID `json:"function"`
	Block       int        `json:"block"`
	Instruction int        `json:"instruction"`
	Target      FunctionID `json:"target"`
	Elided      bool       `json:"elided"`
}

type planDigestSafeArrayIndex struct {
	Function    FunctionID `json:"function"`
	Block       int        `json:"block"`
	Instruction int        `json:"instruction"`
	Bound       int64      `json:"bound"`
}

type planDigestValue struct {
	Site  planDigestValueSite  `json:"site"`
	Funcs []planDigestFuncLeaf `json:"funcs"`
}

type planDigestValueSite struct {
	Function    FunctionID `json:"function"`
	Kind        string     `json:"kind"`
	Index       int        `json:"index"`
	Block       int        `json:"block"`
	Instruction int        `json:"instruction"`
	Operand     int        `json:"operand"`
}

type planDigestFuncLeaf struct {
	Path      []planDigestPathStep `json:"path"`
	Rep       uint8                `json:"rep"`
	Transport uint8                `json:"transport"`
	Targets   []FunctionID         `json:"targets"`
	MayBeNil  bool                 `json:"may_be_nil"`
}

type planDigestPathStep struct {
	Kind  uint8 `json:"kind"`
	Index int   `json:"index"`
}

// CoroPlanDigest returns a domain-separated SHA-256 digest of the complete
// pointer-free plan. Archive-ready identities are mandatory: report-only SSA
// identities must never become cross-compilation cache keys.
func (p *SSAPlan) CoroPlanDigest(metadata PlanDigestMetadata) (string, error) {
	document, err := p.canonicalPlanDigest(metadata)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(document)
	if err != nil {
		return "", fmt.Errorf("coro: marshal canonical plan digest: %w", err)
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(PlanDigestSchema))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(payload)
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (p *SSAPlan) canonicalPlanDigest(metadata PlanDigestMetadata) (planDigestDocument, error) {
	if p == nil {
		return planDigestDocument{}, fmt.Errorf("coro: digest nil SSA plan")
	}
	if err := metadata.validate(); err != nil {
		return planDigestDocument{}, err
	}
	identity, err := p.functionIDs.normalized()
	if err != nil {
		return planDigestDocument{}, fmt.Errorf("coro: validate plan FunctionID configuration: %w", err)
	}
	if !identity.ArchiveReady {
		return planDigestDocument{}, fmt.Errorf("coro: CoroPlanDigest requires archive-ready FunctionIDs")
	}
	if metadata.CoroABI != identity.CoroABI {
		return planDigestDocument{}, fmt.Errorf("coro: plan digest coroutine ABI %q does not match FunctionID ABI %q", metadata.CoroABI, identity.CoroABI)
	}
	if metadata.SchedulerABI != identity.SchedulerABI {
		return planDigestDocument{}, fmt.Errorf("coro: plan digest scheduler ABI %q does not match FunctionID ABI %q", metadata.SchedulerABI, identity.SchedulerABI)
	}

	roots, err := p.canonicalDigestRoots()
	if err != nil {
		return planDigestDocument{}, err
	}
	functions, err := p.canonicalDigestFunctions()
	if err != nil {
		return planDigestDocument{}, err
	}
	definitions, err := p.digestValueDefinitions()
	if err != nil {
		return planDigestDocument{}, err
	}

	loweredCalls, err := p.canonicalDigestLoweredCalls()
	if err != nil {
		return planDigestDocument{}, err
	}

	document := planDigestDocument{
		Schema:            PlanDigestSchema,
		FunctionIDSchema:  FunctionIDSchema,
		Metadata:          metadata,
		Roots:             roots,
		Functions:         functions,
		Calls:             make([]planDigestCall, 0, len(p.callPlans)),
		LoweredCalls:      loweredCalls,
		ElidedCalls:       make([]planDigestElidedCall, 0, len(p.elidedCalls)),
		ConditionalStores: make([]planDigestConditionalStore, 0, len(p.conditionalStores)),
		SafeArrayIndexes:  make([]planDigestSafeArrayIndex, 0, len(p.safeFixedArrayIndexes)),
		Values:            make([]planDigestValue, 0, len(p.valuePlans)),
	}
	seenCalls := make(map[ssa.CallInstruction]struct{}, len(p.callPlans))
	seenElidedCalls := make(map[ssa.CallInstruction]struct{}, len(p.elidedCalls))
	seenConditionalStores := make(map[*ssa.Store]struct{}, len(p.conditionalStores))
	seenSafeArrayIndexes := make(map[ssa.Instruction]struct{}, len(p.safeFixedArrayIndexes))
	coveredValues := make(map[ssa.Value]struct{}, len(p.valuePlans))
	for _, function := range p.functions {
		fn := function.Function
		id := function.Plan.ID
		if p.IgnoresBody(fn) {
			continue
		}
		for index, value := range p.managedValueReferences[fn] {
			if err := p.validateDigestManagedValueReference(id, index, value); err != nil {
				return planDigestDocument{}, err
			}
			site := planDigestValueSite{Function: id, Kind: "managed-value", Index: index, Block: -1, Instruction: -1, Operand: -1}
			if err := p.appendDigestValue(&document.Values, coveredValues, value, site, true); err != nil {
				return planDigestDocument{}, err
			}
		}
		for index, value := range fn.Params {
			site := planDigestValueSite{Function: id, Kind: "param", Index: index, Block: -1, Instruction: -1, Operand: -1}
			if err := p.appendDigestValue(&document.Values, coveredValues, value, site, true); err != nil {
				return planDigestDocument{}, err
			}
		}
		for index, value := range fn.FreeVars {
			site := planDigestValueSite{Function: id, Kind: "freevar", Index: index, Block: -1, Instruction: -1, Operand: -1}
			if err := p.appendDigestValue(&document.Values, coveredValues, value, site, true); err != nil {
				return planDigestDocument{}, err
			}
		}
		operands := make([]*ssa.Value, 0, 8)
		for blockIndex, block := range fn.Blocks {
			if block == nil {
				return planDigestDocument{}, fmt.Errorf("coro: function %q has nil SSA block %d", id, blockIndex)
			}
			canonicalOrdinals, err := p.canonicalDigestPackageInitDependencyOrdinals(fn, block)
			if err != nil {
				return planDigestDocument{}, fmt.Errorf(
					"coro: function %q block %d canonical dependency-init ordinals: %w",
					id, blockIndex, err,
				)
			}
			semanticIndex := 0
			for _, instruction := range block.Instrs {
				if instruction == nil {
					return planDigestDocument{}, fmt.Errorf("coro: function %q block %d has nil SSA instruction", id, blockIndex)
				}
				if _, debug := instruction.(*ssa.DebugRef); debug {
					continue
				}
				digestInstruction := semanticIndex
				if canonical, ok := canonicalOrdinals[instruction]; ok {
					digestInstruction = canonical
				}
				if value, ok := instruction.(ssa.Value); ok {
					site := planDigestValueSite{Function: id, Kind: "instruction", Index: -1, Block: blockIndex, Instruction: digestInstruction, Operand: -1}
					if err := p.appendDigestValue(&document.Values, coveredValues, value, site, true); err != nil {
						return planDigestDocument{}, err
					}
				}
				if call, ok := instruction.(ssa.CallInstruction); ok {
					if _, builtin := call.Common().Value.(*ssa.Builtin); !builtin {
						if p.ElidesCall(call) {
							if _, planned := p.callPlans[call]; planned {
								return planDigestDocument{}, fmt.Errorf("coro: function %q block %d instruction %d is both elided and assigned a CallPlan", id, blockIndex, digestInstruction)
							}
							if _, duplicate := seenElidedCalls[call]; duplicate {
								return planDigestDocument{}, fmt.Errorf("coro: duplicate elided SSA call occurrence for function %q block %d instruction %d", id, blockIndex, digestInstruction)
							}
							seenElidedCalls[call] = struct{}{}
							document.ElidedCalls = append(document.ElidedCalls, planDigestElidedCall{
								Function: id, Block: blockIndex, Instruction: digestInstruction, Elided: true,
								Certificate: p.elidedCallCertificates[call],
							})
						} else {
							plan, ok := p.callPlans[call]
							if !ok {
								return planDigestDocument{}, fmt.Errorf("coro: missing CallPlan for function %q block %d instruction %d", id, blockIndex, digestInstruction)
							}
							entry, err := p.canonicalDigestCall(id, blockIndex, digestInstruction, call, plan)
							if err != nil {
								return planDigestDocument{}, err
							}
							if _, duplicate := seenCalls[call]; duplicate {
								return planDigestDocument{}, fmt.Errorf("coro: duplicate SSA call occurrence for function %q block %d instruction %d", id, blockIndex, digestInstruction)
							}
							seenCalls[call] = struct{}{}
							document.Calls = append(document.Calls, entry)
						}
					}
				}
				if store, ok := instruction.(*ssa.Store); ok {
					if target, conditional := p.conditionalStores[store]; conditional {
						if store.Parent() != fn || target == nil {
							return planDigestDocument{}, fmt.Errorf("coro: conditional managed Store at function %q block %d instruction %d has no exact owner/target", id, blockIndex, digestInstruction)
						}
						targetID, planned := p.byFunction[target]
						if !planned {
							return planDigestDocument{}, fmt.Errorf("coro: conditional managed Store at function %q block %d instruction %d targets a function outside the plan", id, blockIndex, digestInstruction)
						}
						value, valuePlanned := p.valuePlans[store.Val]
						if !valuePlanned || value.Value != store.Val || len(value.Funcs) != 1 ||
							len(value.Funcs[0].Path) != 0 || value.Funcs[0].Rep != Dispatch ||
							value.Funcs[0].MayBeNil || len(value.Funcs[0].Targets) != 1 ||
							value.Funcs[0].Targets[0] != targetID {
							return planDigestDocument{}, fmt.Errorf("coro: conditional managed Store at function %q block %d instruction %d no longer carries its exact target", id, blockIndex, digestInstruction)
						}
						if _, duplicate := seenConditionalStores[store]; duplicate {
							return planDigestDocument{}, fmt.Errorf("coro: duplicate conditional managed Store occurrence for function %q block %d instruction %d", id, blockIndex, digestInstruction)
						}
						seenConditionalStores[store] = struct{}{}
						document.ConditionalStores = append(document.ConditionalStores, planDigestConditionalStore{
							Function: id, Block: blockIndex, Instruction: digestInstruction,
							Target: targetID, Elided: p.ElidesConditionalManagedStore(store),
						})
					}
				}
				if bound, safe := p.safeFixedArrayIndexes[instruction]; safe {
					var base, index ssa.Value
					switch operation := instruction.(type) {
					case *ssa.Index:
						base, index = operation.X, operation.Index
					case *ssa.IndexAddr:
						base, index = operation.X, operation.Index
					default:
						return planDigestDocument{}, fmt.Errorf("coro: safe fixed-array index at function %q block %d instruction %d has type %T", id, blockIndex, digestInstruction, instruction)
					}
					actualBound, _, fixed := ssaExactFixedArrayBound(base)
					if !fixed || bound != actualBound || !ProveSSAExactSafeFixedArrayIndex(fn, index, bound, instruction) {
						return planDigestDocument{}, fmt.Errorf("coro: safe fixed-array index at function %q block %d instruction %d no longer has its exact bound proof", id, blockIndex, digestInstruction)
					}
					if _, duplicate := seenSafeArrayIndexes[instruction]; duplicate {
						return planDigestDocument{}, fmt.Errorf("coro: duplicate safe fixed-array index occurrence for function %q block %d instruction %d", id, blockIndex, digestInstruction)
					}
					seenSafeArrayIndexes[instruction] = struct{}{}
					document.SafeArrayIndexes = append(document.SafeArrayIndexes, planDigestSafeArrayIndex{
						Function: id, Block: blockIndex, Instruction: digestInstruction, Bound: bound,
					})
				}
				operands = instruction.Operands(operands[:0])
				for operandIndex, operand := range operands {
					if operand == nil || *operand == nil || skipDigestOperand(instruction, operand) {
						continue
					}
					value := *operand
					if _, defined := definitions[value]; defined {
						continue
					}
					site := planDigestValueSite{Function: id, Kind: "operand", Index: -1, Block: blockIndex, Instruction: digestInstruction, Operand: operandIndex}
					if err := p.appendDigestValue(&document.Values, coveredValues, value, site, true); err != nil {
						return planDigestDocument{}, err
					}
				}
				semanticIndex++
			}
		}
	}
	if len(seenCalls) != len(p.callPlans) {
		return planDigestDocument{}, fmt.Errorf("coro: CallPlan coverage mismatch: projected %d of %d plans", len(seenCalls), len(p.callPlans))
	}
	for call := range p.exactInterfaceReceivers {
		if _, covered := seenCalls[call]; !covered {
			return planDigestDocument{}, fmt.Errorf(
				"coro: exact interface receiver belongs to an uncovered call in %q",
				call.Parent().Name(),
			)
		}
	}
	if len(seenElidedCalls) != len(p.elidedCalls) {
		return planDigestDocument{}, fmt.Errorf("coro: elided-call coverage mismatch: projected %d of %d calls", len(seenElidedCalls), len(p.elidedCalls))
	}
	if len(seenConditionalStores) != len(p.conditionalStores) {
		return planDigestDocument{}, fmt.Errorf("coro: conditional managed Store coverage mismatch: projected %d of %d Stores", len(seenConditionalStores), len(p.conditionalStores))
	}
	if len(seenSafeArrayIndexes) != len(p.safeFixedArrayIndexes) {
		return planDigestDocument{}, fmt.Errorf("coro: safe fixed-array index coverage mismatch: projected %d of %d indexes", len(seenSafeArrayIndexes), len(p.safeFixedArrayIndexes))
	}
	for call, certificate := range p.elidedCallCertificates {
		if _, elided := seenElidedCalls[call]; !elided || certificate == "" {
			return planDigestDocument{}, fmt.Errorf("coro: elided-call certificate has no exact nonempty elided call")
		}
	}
	if len(coveredValues) != len(p.valuePlans) {
		uncovered := make([]string, 0, len(p.valuePlans)-len(coveredValues))
		for value := range p.valuePlans {
			if _, covered := coveredValues[value]; covered {
				continue
			}
			owner := "<package>"
			if parent := value.Parent(); parent != nil {
				if id, planned := p.byFunction[parent]; planned {
					owner = string(id)
				} else {
					owner = parent.String()
				}
			}
			uncovered = append(uncovered, fmt.Sprintf("%s: %T %q (%s)", owner, value, value.String(), value.Type()))
		}
		sort.Strings(uncovered)
		return planDigestDocument{}, fmt.Errorf(
			"coro: SSAValuePlan coverage mismatch: projected %d of %d plans; uncovered: %s",
			len(coveredValues), len(p.valuePlans), strings.Join(uncovered, "; "),
		)
	}
	sort.Slice(document.Calls, func(i, j int) bool {
		return comparePlanDigestInstructionSite(
			document.Calls[i].Function, document.Calls[i].Block, document.Calls[i].Instruction,
			document.Calls[j].Function, document.Calls[j].Block, document.Calls[j].Instruction,
		) < 0
	})
	sort.Slice(document.ElidedCalls, func(i, j int) bool {
		return comparePlanDigestInstructionSite(
			document.ElidedCalls[i].Function, document.ElidedCalls[i].Block, document.ElidedCalls[i].Instruction,
			document.ElidedCalls[j].Function, document.ElidedCalls[j].Block, document.ElidedCalls[j].Instruction,
		) < 0
	})
	return document, nil
}

// canonicalDigestPackageInitDependencyOrdinals maps x/tools' unordered direct
// dependency-init calls back onto their existing semantic-instruction slots in
// canonical package-key and source-import-path order. The canonical key covers
// test and patched package variants; the source path distinguishes separate Go
// packages deliberately emitted into one replacement package. Both facts also
// exist for calls intentionally excluded from the plan, unlike a final link
// identity. x/tools may emit these calls by ranging an import set, so their
// physical order can vary between otherwise identical builds. The mapping
// changes only pointer-free cache identity; source SSA and codegen retain their
// exact execution order.
func (p *SSAPlan) canonicalDigestPackageInitDependencyOrdinals(
	owner *ssa.Function,
	block *ssa.BasicBlock,
) (map[ssa.Instruction]int, error) {
	if !isDigestPackageInitializer(owner) || block == nil {
		return nil, nil
	}
	type dependency struct {
		instruction ssa.Instruction
		orderKey    string
		target      string
		slot        int
	}
	dependencies := make([]dependency, 0)
	identity := functionIDBuilder{config: p.functionIDs}
	ownerPackageKey, err := identity.packageKey(owner.Pkg.Pkg)
	if err != nil {
		return nil, fmt.Errorf("identify owner initializer %q package: %w", owner.String(), err)
	}
	ownerSourcePath := owner.Pkg.Pkg.Path()
	ownerOrderKey := identityNode(
		"package-init-dependency",
		identityPair{"canonical-package", ownerPackageKey},
		identityPair{"source-package", ownerSourcePath},
	)
	semantic := 0
	for _, instruction := range block.Instrs {
		if _, debug := instruction.(*ssa.DebugRef); debug {
			continue
		}
		call, direct := instruction.(*ssa.Call)
		if direct && call.Common() != nil && !call.Common().IsInvoke() {
			target := call.Common().StaticCallee()
			if isDigestPackageInitializer(target) {
				packageKey, err := identity.packageKey(target.Pkg.Pkg)
				if err != nil {
					return nil, fmt.Errorf("identify dependency initializer %q package: %w", target.String(), err)
				}
				sourcePath := target.Pkg.Pkg.Path()
				orderKey := identityNode(
					"package-init-dependency",
					identityPair{"canonical-package", packageKey},
					identityPair{"source-package", sourcePath},
				)
				if orderKey != ownerOrderKey {
					dependencies = append(dependencies, dependency{
						instruction: instruction,
						orderKey:    orderKey,
						target:      target.String(),
						slot:        semantic,
					})
				}
			}
		}
		semantic++
	}
	if len(dependencies) < 2 {
		return nil, nil
	}
	slots := make([]int, len(dependencies))
	for index := range dependencies {
		slots[index] = dependencies[index].slot
	}
	sort.Ints(slots)
	sort.Slice(dependencies, func(i, j int) bool {
		return dependencies[i].orderKey < dependencies[j].orderKey
	})
	ret := make(map[ssa.Instruction]int, len(dependencies))
	var previous dependency
	for index, dependency := range dependencies {
		if index != 0 && dependency.orderKey == previous.orderKey {
			return nil, fmt.Errorf(
				"owner initializer %q calls dependency initializer identity %q more than once at semantic slots %d and %d (%q, %q)",
				owner.String(), dependency.orderKey,
				previous.slot, dependency.slot, previous.target, dependency.target,
			)
		}
		previous = dependency
		ret[dependency.instruction] = slots[index]
	}
	return ret, nil
}

func isDigestPackageInitializer(function *ssa.Function) bool {
	if function == nil || function.Name() != "init" || function.Synthetic != "package initializer" ||
		function.Pkg == nil || function.Pkg.Pkg == nil ||
		function.Signature == nil || function.Signature.Recv() != nil {
		return false
	}
	params, results := function.Signature.Params(), function.Signature.Results()
	return (params == nil || params.Len() == 0) && (results == nil || results.Len() == 0)
}

func comparePlanDigestInstructionSite(
	leftFunction FunctionID,
	leftBlock, leftInstruction int,
	rightFunction FunctionID,
	rightBlock, rightInstruction int,
) int {
	if order := cmp.Compare(leftFunction, rightFunction); order != 0 {
		return order
	}
	if order := cmp.Compare(leftBlock, rightBlock); order != 0 {
		return order
	}
	return cmp.Compare(leftInstruction, rightInstruction)
}

func (p *SSAPlan) canonicalDigestLoweredCalls() ([]planDigestLoweredCall, error) {
	ret := make([]planDigestLoweredCall, 0)
	for owner, calls := range p.loweredCalls {
		ownerID, ok := p.byFunction[owner]
		if !ok {
			return nil, fmt.Errorf("coro: lowered-call owner %q is absent from the plan", owner.Name())
		}
		previous := ""
		for index, call := range calls {
			if call.LogicalName == "" || !utf8.ValidString(call.LogicalName) || strings.IndexByte(call.LogicalName, 0) >= 0 {
				return nil, fmt.Errorf("coro: lowered call %d in %q has invalid logical name %q", index, ownerID, call.LogicalName)
			}
			if index != 0 && previous >= call.LogicalName {
				return nil, fmt.Errorf("coro: lowered calls in %q are not in strict logical-name order", ownerID)
			}
			previous = call.LogicalName
			if call.NoUnwind && (call.RawPlain || call.UnwindOnly || call.ExplicitStatusElided) {
				return nil, fmt.Errorf("coro: lowered call %q in %q mixes no-unwind with raw-plain or unwind-only semantics", call.LogicalName, ownerID)
			}
			targetID, ok := p.byFunction[call.Target]
			if !ok {
				return nil, fmt.Errorf("coro: lowered call %q in %q targets a function outside the plan", call.LogicalName, ownerID)
			}
			ret = append(ret, planDigestLoweredCall{
				Owner:                ownerID,
				LogicalName:          call.LogicalName,
				Target:               targetID,
				NoUnwind:             call.NoUnwind,
				RawPlain:             call.RawPlain,
				UnwindOnly:           call.UnwindOnly,
				ExplicitStatusElided: call.ExplicitStatusElided,
			})
		}
	}
	sort.Slice(ret, func(i, j int) bool {
		if ret[i].Owner != ret[j].Owner {
			return ret[i].Owner < ret[j].Owner
		}
		return ret[i].LogicalName < ret[j].LogicalName
	})
	return ret, nil
}

func (m PlanDigestMetadata) validate() error {
	required := []struct {
		name  string
		value string
	}{
		{"coroutine ABI", m.CoroABI},
		{"scheduler ABI", m.SchedulerABI},
		{"panic ABI", m.PanicABI},
		{"function representation ABI", m.FuncRepABI},
		{"target triple", m.TargetTriple},
		{"data layout", m.DataLayout},
	}
	for _, field := range required {
		if err := validatePlanDigestText(field.name, field.value, false); err != nil {
			return err
		}
	}
	optional := []struct {
		name  string
		value string
	}{
		{"target CPU", m.TargetCPU},
		{"target features", m.TargetFeatures},
		{"target ABI", m.TargetABI},
		{"frame-retention ABI", m.FrameRetentionABI},
	}
	for _, field := range optional {
		if err := validatePlanDigestText(field.name, field.value, true); err != nil {
			return err
		}
	}
	if m.LoweringFactsSchema != LoweringFactsSchema {
		return fmt.Errorf("coro: plan digest lowering-facts schema %q, want %q", m.LoweringFactsSchema, LoweringFactsSchema)
	}
	decodedFacts, err := hex.DecodeString(m.LoweringFactsDigest)
	if err != nil || len(decodedFacts) != sha256.Size || hex.EncodeToString(decodedFacts) != m.LoweringFactsDigest {
		return fmt.Errorf("coro: plan digest lowering-facts digest is not a canonical SHA-256 digest")
	}
	switch m.FrameRetentionABI {
	case "":
	case FrameRetentionParkABIV2:
		if m.CoroABI != PhysicalABIV1 ||
			(m.SchedulerABI != SchedulerProgramBootstrapABIV2 &&
				m.SchedulerABI != SchedulerProgramBootstrapWorkerABIV0 &&
				m.SchedulerABI != SchedulerProgramBootstrapClosedStaticSpawnABIV0 &&
				m.SchedulerABI != SchedulerProgramBootstrapWorkerClosedStaticSpawnABIV0 &&
				m.SchedulerABI != SchedulerProgramBootstrapChannelABIV0 &&
				m.SchedulerABI != SchedulerProgramBootstrapChannelWorkerABIV0 &&
				m.SchedulerABI != SchedulerProgramBootstrapChannelWorkerClosedStaticSpawnABIV0 &&
				m.SchedulerABI != SchedulerProgramBootstrapChannelHostOperationClosedStaticSpawnABIV0 &&
				m.SchedulerABI != SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0) {
			return fmt.Errorf("coro: plan digest frame-retention ABI %q requires PhysicalABIV1 runnable program-bootstrap metadata", m.FrameRetentionABI)
		}
	default:
		return fmt.Errorf("coro: plan digest has unknown frame-retention ABI %q", m.FrameRetentionABI)
	}
	if m.PointerBits <= 0 || m.PointerBits%8 != 0 {
		return fmt.Errorf("coro: plan digest pointer width %d is not a positive multiple of 8", m.PointerBits)
	}
	if m.Endianness != "little" && m.Endianness != "big" {
		return fmt.Errorf("coro: plan digest endianness %q is not little or big", m.Endianness)
	}
	return nil
}

func validatePlanDigestText(name, value string, allowEmpty bool) error {
	if value == "" && !allowEmpty {
		return fmt.Errorf("coro: plan digest %s is empty", name)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("coro: plan digest %s is not valid UTF-8", name)
	}
	if strings.IndexByte(value, 0) >= 0 {
		return fmt.Errorf("coro: plan digest %s contains NUL", name)
	}
	return nil
}

func (p *SSAPlan) canonicalDigestRoots() ([]planDigestRoot, error) {
	if p.plan == nil {
		return nil, fmt.Errorf("coro: CoroPlanDigest requires a base plan")
	}
	ret := make([]planDigestRoot, 0, len(p.roots))
	var previous FunctionID
	for index, root := range p.roots {
		if root.Function == nil {
			return nil, fmt.Errorf("coro: SSA root plan %d has nil function", index)
		}
		if err := validateDigestFunctionID(root.ID); err != nil {
			return nil, fmt.Errorf("coro: validate SSA root plan %d: %w", index, err)
		}
		if err := root.Demand.Validate(); err != nil {
			return nil, fmt.Errorf("coro: validate SSA root plan %d demand: %w", index, err)
		}
		if err := root.ManagedDemand.Validate(); err != nil {
			return nil, fmt.Errorf("coro: validate SSA root plan %d managed demand: %w", index, err)
		}
		if want := aggregateDemand(root.ManagedDemand, root.RawPlainDemand); root.Demand != want {
			return nil, fmt.Errorf("coro: SSA root plan %d aggregate demand %s does not match managed=%s raw=%t", index, root.Demand, root.ManagedDemand, root.RawPlainDemand)
		}
		if root.ManagedDemand == NoDemand && !root.RawPlainDemand {
			return nil, fmt.Errorf("coro: SSA root plan %d has no demand", index)
		}
		if index != 0 && previous >= root.ID {
			return nil, fmt.Errorf("coro: SSA root plans are not in strict FunctionID order")
		}
		previous = root.ID
		if got, ok := p.byFunction[root.Function]; !ok || got != root.ID {
			return nil, fmt.Errorf("coro: missing forward root mapping for %q", root.ID)
		}
		if got, ok := p.byID[root.ID]; !ok || got != root.Function {
			return nil, fmt.Errorf("coro: missing reverse root mapping for %q", root.ID)
		}
		plan, ok := p.plan.Lookup(root.ID)
		if !ok {
			return nil, fmt.Errorf("coro: root %q is absent from the base plan", root.ID)
		}
		if !plan.ManagedDemand.Contains(root.ManagedDemand) || root.RawPlainDemand && !plan.RawPlainDemand {
			return nil, fmt.Errorf("coro: root %q demand managed=%s raw=%t is not contained in function demand managed=%s raw=%t", root.ID, root.ManagedDemand, root.RawPlainDemand, plan.ManagedDemand, plan.RawPlainDemand)
		}
		ret = append(ret, planDigestRoot{Function: root.ID, Demand: uint8(root.Demand), ManagedDemand: uint8(root.ManagedDemand), RawPlainDemand: root.RawPlainDemand})
	}
	return ret, nil
}

func (p *SSAPlan) canonicalDigestFunctions() ([]planDigestFunction, error) {
	if p.plan == nil {
		return nil, fmt.Errorf("coro: CoroPlanDigest requires a base plan")
	}
	baseFunctions := p.plan.Functions()
	if len(p.functions) != len(baseFunctions) || len(p.functions) != len(p.byFunction) || len(p.functions) != len(p.byID) {
		return nil, fmt.Errorf("coro: SSA function-plan coverage mismatch")
	}
	ret := make([]planDigestFunction, 0, len(p.functions))
	var previous FunctionID
	for index, function := range p.functions {
		if function.Function == nil {
			return nil, fmt.Errorf("coro: SSA function plan %d has nil function", index)
		}
		plan := function.Plan
		if err := validateDigestFunctionPlan(plan); err != nil {
			return nil, fmt.Errorf("coro: validate function plan %d: %w", index, err)
		}
		if err := validateDigestFunctionID(plan.ID); err != nil {
			return nil, err
		}
		if index != 0 && previous >= plan.ID {
			return nil, fmt.Errorf("coro: SSA function plans are not in strict FunctionID order")
		}
		previous = plan.ID
		if baseFunctions[index] != plan {
			return nil, fmt.Errorf("coro: SSA function plan %q differs from the base plan", plan.ID)
		}
		if got, ok := p.byFunction[function.Function]; !ok || got != plan.ID {
			return nil, fmt.Errorf("coro: missing forward function mapping for %q", plan.ID)
		}
		if got, ok := p.byID[plan.ID]; !ok || got != function.Function {
			return nil, fmt.Errorf("coro: missing reverse function mapping for %q", plan.ID)
		}
		ret = append(ret, planDigestFunction{
			ID:                      plan.ID,
			IgnoredBody:             p.IgnoresBody(function.Function),
			DeclaredEffect:          uint16(plan.DeclaredEffect),
			LocalEffect:             uint16(plan.LocalEffect),
			Effect:                  uint16(plan.Effect),
			DeclaredExec:            uint16(plan.DeclaredExec),
			LocalExec:               uint16(plan.LocalExec),
			Exec:                    uint16(plan.Exec),
			Demand:                  uint8(plan.Demand),
			ManagedDemand:           uint8(plan.ManagedDemand),
			RawPlainDemand:          plan.RawPlainDemand,
			Emission:                uint8(plan.Emission),
			ManagedEntry:            uint8(plan.ManagedEntry),
			AtomicCost:              plan.AtomicCost,
			AtomicCostProof:         uint8(plan.AtomicCostProof),
			FuncRep:                 uint8(plan.FuncRep),
			External:                uint8(plan.External),
			Recursive:               plan.Recursive,
			TrustedBoundedRecursion: plan.TrustedBoundedRecursion,
			Primary:                 uint8(plan.Primary),
			RawPlainOnly:            plan.RawPlainOnly,
			RawPlainEntry:           plan.RawPlainEntry,
			RawPlainVariant:         p.HasRawPlainVariant(function.Function),
		})
		if certificate, ok := p.ForeignNoBlockCertificate(function.Function); ok {
			ret[len(ret)-1].ForeignNoBlockCertificate = certificate
		}
		if certificate, ok := p.CallableIdentityCertificate(function.Function); ok {
			if err := certificate.Validate(); err != nil {
				return nil, fmt.Errorf("coro: function %q has invalid callable identity certificate in plan digest: %w", plan.ID, err)
			}
			frozen := certificate
			ret[len(ret)-1].CallableIdentityCertificate = &frozen
		}
		if certificate, ok := p.CallableContractCertificate(function.Function); ok {
			if err := certificate.Validate(); err != nil {
				return nil, fmt.Errorf("coro: function %q has invalid callable contract certificate in plan digest: %w", plan.ID, err)
			}
			frozen := certificate
			ret[len(ret)-1].CallableContractCertificate = &frozen
			if certificate.Scope == CallableContractScopeDeclaration && ret[len(ret)-1].CallableIdentityCertificate != nil {
				if err := ValidateCallableContractIdentity(*ret[len(ret)-1].CallableIdentityCertificate, certificate); err != nil {
					return nil, fmt.Errorf("coro: function %q callable identity/contract mismatch in plan digest: %w", plan.ID, err)
				}
			}
		}
		if certificate, ok := p.ForeignSyncCertificate(function.Function); ok {
			ret[len(ret)-1].ForeignSyncCertificate = certificate
		}
		if certificate, ok := p.ForeignWorkerCertificate(function.Function); ok {
			ret[len(ret)-1].ForeignWorkerCertificate = certificate
		}
		if certificate, ok := p.AssemblyNoSuspendCertificate(function.Function); ok {
			ret[len(ret)-1].AssemblyNoSuspendCertificate = certificate
		}
	}
	return ret, nil
}

func validateDigestFunctionPlan(plan FunctionPlan) error {
	if err := plan.ID.validate(); err != nil {
		return err
	}
	effects := []struct {
		name  string
		value Effect
	}{
		{"declared effect", plan.DeclaredEffect},
		{"local effect", plan.LocalEffect},
		{"effect", plan.Effect},
	}
	for _, effect := range effects {
		if err := effect.value.Validate(); err != nil {
			return fmt.Errorf("%s: %w", effect.name, err)
		}
	}
	flags := []struct {
		name  string
		value ExecFlags
	}{
		{"declared execution flags", plan.DeclaredExec},
		{"local execution flags", plan.LocalExec},
		{"execution flags", plan.Exec},
	}
	for _, flag := range flags {
		if err := flag.value.Validate(); err != nil {
			return fmt.Errorf("%s: %w", flag.name, err)
		}
	}
	if err := plan.Demand.Validate(); err != nil {
		return err
	}
	if err := plan.ManagedDemand.Validate(); err != nil {
		return err
	}
	if want := aggregateDemand(plan.ManagedDemand, plan.RawPlainDemand); plan.Demand != want {
		return fmt.Errorf("coro: function %q aggregate demand %s does not match managed=%s raw=%t", plan.ID, plan.Demand, plan.ManagedDemand, plan.RawPlainDemand)
	}
	if err := plan.Emission.Validate(); err != nil {
		return err
	}
	if err := plan.FuncRep.Validate(); err != nil {
		return err
	}
	if err := plan.External.validate(); err != nil {
		return err
	}
	if err := plan.Primary.validate(); err != nil {
		return err
	}
	if plan.TrustedBoundedRecursion && !plan.Recursive {
		return fmt.Errorf("coro: non-recursive function %q has a trusted bounded-recursion proof", plan.ID)
	}
	if err := validateManagedEntryPlan(plan); err != nil {
		return err
	}
	expectedEmission := bodyEmissionFor(plan.ManagedDemand, plan.RawPlainDemand, plan.Effect, plan.External)
	if plan.External == Defined && plan.ManagedEntry == ManagedEntryOutcomePlain {
		if plan.ManagedDemand == NoDemand || plan.Primary != PrimaryCoroutine {
			return fmt.Errorf("coro: owned outcome-plain function %q lacks managed demand/coroutine primary", plan.ID)
		}
		expectedEmission = EmitOutcomePlain
	}
	if plan.Emission != expectedEmission {
		return fmt.Errorf("coro: function %q emission %s does not match managed demand %s, raw demand %t, effect %s, and external kind %s (want %s)", plan.ID, plan.Emission, plan.ManagedDemand, plan.RawPlainDemand, plan.Effect, plan.External, expectedEmission)
	}
	if plan.RawPlainOnly != (plan.External == Defined && plan.RawPlainDemand && plan.ManagedDemand == NoDemand) {
		return fmt.Errorf("coro: function %q has inconsistent raw-plain-only state", plan.ID)
	}
	if plan.RawPlainOnly && (plan.Emission != EmitRawPlain || plan.Primary != PrimaryPlain || plan.FuncRep != DirectPlain) {
		return fmt.Errorf("coro: raw-plain-only function %q lacks raw/plain/direct physical selection", plan.ID)
	}
	if plan.RawPlainEntry && !plan.RawPlainDemand {
		return fmt.Errorf("coro: function %q has a raw plain entry without raw demand", plan.ID)
	}
	return nil
}

func validateDigestFunctionID(id FunctionID) error {
	prefix := FunctionIDSchema + ":"
	text := string(id)
	if !strings.HasPrefix(text, prefix) {
		return fmt.Errorf("coro: archive function ID %q does not use schema %q", id, FunctionIDSchema)
	}
	encoded := text[len(prefix):]
	decoded, err := hex.DecodeString(encoded)
	if err != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != encoded {
		return fmt.Errorf("coro: archive function ID %q does not contain a canonical SHA-256 digest", id)
	}
	return nil
}

func (p *SSAPlan) digestValueDefinitions() (map[ssa.Value]struct{}, error) {
	definitions := make(map[ssa.Value]struct{})
	add := func(value ssa.Value, description string) error {
		if value == nil {
			return fmt.Errorf("coro: nil SSA value definition at %s", description)
		}
		if _, exists := definitions[value]; exists {
			return fmt.Errorf("coro: duplicate SSA value definition at %s", description)
		}
		definitions[value] = struct{}{}
		return nil
	}
	for _, function := range p.functions {
		id := function.Plan.ID
		if p.IgnoresBody(function.Function) {
			continue
		}
		for index, value := range function.Function.Params {
			if err := add(value, fmt.Sprintf("function %q parameter %d", id, index)); err != nil {
				return nil, err
			}
		}
		for index, value := range function.Function.FreeVars {
			if err := add(value, fmt.Sprintf("function %q free variable %d", id, index)); err != nil {
				return nil, err
			}
		}
		for blockIndex, block := range function.Function.Blocks {
			if block == nil {
				return nil, fmt.Errorf("coro: function %q has nil SSA block %d", id, blockIndex)
			}
			semanticIndex := 0
			for _, instruction := range block.Instrs {
				if instruction == nil {
					return nil, fmt.Errorf("coro: function %q block %d has nil SSA instruction", id, blockIndex)
				}
				if _, debug := instruction.(*ssa.DebugRef); debug {
					continue
				}
				if value, ok := instruction.(ssa.Value); ok {
					if err := add(value, fmt.Sprintf("function %q block %d instruction %d", id, blockIndex, semanticIndex)); err != nil {
						return nil, err
					}
				}
				semanticIndex++
			}
		}
	}
	return definitions, nil
}

func skipDigestOperand(instruction ssa.Instruction, operand *ssa.Value) bool {
	call, ok := instruction.(ssa.CallInstruction)
	if !ok || operand != &call.Common().Value {
		return false
	}
	value := *operand
	if _, builtin := value.(*ssa.Builtin); builtin {
		return true
	}
	if call.Common().StaticCallee() != nil {
		_, function := value.(*ssa.Function)
		return function
	}
	return false
}

func requiresDigestValuePlan(value ssa.Value) bool {
	return value != nil && value.Type() != nil && len(funcLeafPaths(value.Type())) != 0
}

func (p *SSAPlan) appendDigestValue(output *[]planDigestValue, covered map[ssa.Value]struct{}, value ssa.Value, site planDigestValueSite, required bool) error {
	if !requiresDigestValuePlan(value) {
		return nil
	}
	plan, ok := p.valuePlans[value]
	if !ok {
		if required {
			return fmt.Errorf("coro: missing SSAValuePlan at %s", formatDigestValueSite(site))
		}
		return nil
	}
	entry, err := p.canonicalDigestValue(value, plan, site)
	if err != nil {
		return err
	}
	// Values with SSA definitions are visited once at that definition. Constants,
	// globals, and function values have no instruction definition, so the caller
	// deliberately projects every stable operand occurrence. Do not deduplicate
	// those occurrences by pointer: covered only proves that every map plan was
	// represented at least once in the pointer-free document.
	covered[value] = struct{}{}
	*output = append(*output, entry)
	return nil
}

func formatDigestValueSite(site planDigestValueSite) string {
	switch site.Kind {
	case "managed-value", "param", "freevar":
		return fmt.Sprintf("function %q %s %d", site.Function, site.Kind, site.Index)
	case "instruction":
		return fmt.Sprintf("function %q block %d instruction %d result", site.Function, site.Block, site.Instruction)
	default:
		return fmt.Sprintf("function %q block %d instruction %d operand %d", site.Function, site.Block, site.Instruction, site.Operand)
	}
}

func (p *SSAPlan) validateDigestManagedValueReference(owner FunctionID, index int, target *ssa.Function) error {
	site := planDigestValueSite{
		Function: owner, Kind: "managed-value", Index: index,
		Block: -1, Instruction: -1, Operand: -1,
	}
	if target == nil {
		return fmt.Errorf("coro: %s has a nil target", formatDigestValueSite(site))
	}
	targetID, planned := p.byFunction[target]
	if !planned {
		return fmt.Errorf("coro: %s targets a function outside the plan", formatDigestValueSite(site))
	}
	if target.Signature == nil || target.Signature.Recv() != nil ||
		len(target.FreeVars) != 0 || len(target.Blocks) == 0 {
		return fmt.Errorf("coro: %s no longer targets one bodyful context-free package function", formatDigestValueSite(site))
	}
	plan, valuePlanned := p.valuePlans[target]
	if !valuePlanned || plan.Value != target || len(plan.Funcs) != 1 ||
		len(plan.Funcs[0].Path) != 0 || plan.Funcs[0].Rep != Dispatch ||
		plan.Funcs[0].Transport != ManagedTransport || plan.Funcs[0].MayBeNil ||
		len(plan.Funcs[0].Targets) != 1 || plan.Funcs[0].Targets[0] != targetID {
		return fmt.Errorf("coro: %s no longer carries its exact managed Dispatch target", formatDigestValueSite(site))
	}
	return nil
}

func (p *SSAPlan) canonicalDigestCall(id FunctionID, block, instruction int, call ssa.CallInstruction, plan SSACallPlan) (planDigestCall, error) {
	if plan.Call != call {
		return planDigestCall{}, fmt.Errorf("coro: CallPlan at function %q block %d instruction %d references a different SSA call", id, block, instruction)
	}
	if err := plan.Kind.validate(); err != nil {
		return planDigestCall{}, err
	}
	if err := plan.Rep.Validate(); err != nil {
		return planDigestCall{}, err
	}
	if err := plan.Transport.Validate(); err != nil {
		return planDigestCall{}, err
	}
	if plan.Transport == RawCCodePointer {
		common := call.Common()
		if common == nil || common.StaticCallee() != nil || common.IsInvoke() || common.Method != nil ||
			plan.Kind != CallForeign || plan.Rep != DirectPlain || !plan.Open ||
			plan.Unresolved != UnknownForeign || plan.SyncDispatch || plan.RawPlain {
			return planDigestCall{}, fmt.Errorf("coro: CallPlan at function %q block %d instruction %d has malformed raw C code-pointer transport", id, block, instruction)
		}
	}
	if plan.RawPlain {
		common := call.Common()
		if common == nil || common.StaticCallee() == nil || common.IsInvoke() || common.Method != nil ||
			plan.Kind != CallDirect || plan.Rep != DirectPlain || plan.Transport != ManagedTransport ||
			plan.Open || plan.MayBeNil || len(plan.Targets) != 1 || plan.SyncDispatch {
			return planDigestCall{}, fmt.Errorf(
				"coro: CallPlan at function %q block %d instruction %d has malformed raw/plain invocation",
				id, block, instruction,
			)
		}
		if err := validateStableToken("raw/plain invocation certificate", plan.RawPlainCertificate); err != nil {
			return planDigestCall{}, err
		}
	} else if plan.RawPlainCertificate != "" {
		return planDigestCall{}, fmt.Errorf(
			"coro: CallPlan at function %q block %d instruction %d has raw/plain certificate data without the policy",
			id, block, instruction,
		)
	}
	if err := plan.Unresolved.validate(); err != nil {
		return planDigestCall{}, err
	}
	switch plan.InvocationPolicy {
	case "":
		if plan.Kind == CallTrustedInline || plan.InvocationContract != "" || plan.InvocationABI != "" || plan.InvocationCertificate != "" {
			return planDigestCall{}, fmt.Errorf("coro: CallPlan at function %q block %d instruction %d has incomplete invocation metadata", id, block, instruction)
		}
	case InvocationAuto, InvocationTrustedInline:
		if err := plan.InvocationPolicy.Validate(); err != nil {
			return planDigestCall{}, err
		}
		if plan.InvocationPolicy == InvocationTrustedInline && plan.Kind != CallTrustedInline {
			return planDigestCall{}, fmt.Errorf("coro: trusted-inline CallPlan at function %q block %d instruction %d has call kind %d", id, block, instruction, plan.Kind)
		}
		if err := validateStableToken("invocation contract", string(plan.InvocationContract)); err != nil {
			return planDigestCall{}, err
		}
		if err := validateStableToken("invocation ABI", plan.InvocationABI); err != nil {
			return planDigestCall{}, err
		}
		if err := validateStableToken("invocation certificate", plan.InvocationCertificate); err != nil {
			return planDigestCall{}, err
		}
	default:
		return planDigestCall{}, fmt.Errorf("coro: CallPlan at function %q block %d instruction %d has invalid invocation policy %q", id, block, instruction, plan.InvocationPolicy)
	}
	targets, err := p.canonicalDigestTargets(plan.Targets)
	if err != nil {
		return planDigestCall{}, fmt.Errorf("coro: CallPlan at function %q block %d instruction %d: %w", id, block, instruction, err)
	}
	exactInterfaceReceiver := false
	if direct, ok := call.(*ssa.Call); ok {
		_, _, _, exact, resolveErr := p.ResolveExactInterfaceCall(direct)
		if resolveErr != nil {
			return planDigestCall{}, fmt.Errorf(
				"coro: CallPlan at function %q block %d instruction %d: %w",
				id, block, instruction, resolveErr,
			)
		}
		exactInterfaceReceiver = exact
	}
	return planDigestCall{
		Function:               id,
		Block:                  block,
		Instruction:            instruction,
		Kind:                   uint8(plan.Kind),
		Rep:                    uint8(plan.Rep),
		Transport:              uint8(plan.Transport),
		Targets:                targets,
		Open:                   plan.Open,
		Unresolved:             uint8(plan.Unresolved),
		MayBeNil:               plan.MayBeNil,
		ExactInterfaceReceiver: exactInterfaceReceiver,
		SyncDispatch:           plan.SyncDispatch,
		RawPlain:               plan.RawPlain,
		RawPlainCertificate:    plan.RawPlainCertificate,
		InvocationPolicy:       plan.InvocationPolicy,
		InvocationContract:     plan.InvocationContract,
		InvocationABI:          plan.InvocationABI,
		InvocationCertificate:  plan.InvocationCertificate,
	}, nil
}

func (p *SSAPlan) canonicalDigestValue(value ssa.Value, plan SSAValuePlan, site planDigestValueSite) (planDigestValue, error) {
	if plan.Value != value {
		return planDigestValue{}, fmt.Errorf("coro: SSAValuePlan at %s references a different SSA value", formatDigestValueSite(site))
	}
	expectedPaths := funcLeafPaths(value.Type())
	if len(plan.Funcs) != len(expectedPaths) {
		return planDigestValue{}, fmt.Errorf("coro: SSAValuePlan at %s has %d function leaves, want %d", formatDigestValueSite(site), len(plan.Funcs), len(expectedPaths))
	}
	leaves := append(FuncRepMap(nil), plan.Funcs...)
	sort.SliceStable(leaves, func(i, j int) bool { return lessFuncPath(leaves[i].Path, leaves[j].Path) })
	ret := planDigestValue{Site: site, Funcs: make([]planDigestFuncLeaf, 0, len(leaves))}
	for index, leaf := range leaves {
		if !equalDigestFuncPath(leaf.Path, expectedPaths[index]) {
			return planDigestValue{}, fmt.Errorf("coro: SSAValuePlan at %s has a noncanonical function path", formatDigestValueSite(site))
		}
		if err := leaf.Rep.Validate(); err != nil {
			return planDigestValue{}, err
		}
		if err := leaf.Transport.Validate(); err != nil {
			return planDigestValue{}, err
		}
		if leaf.Transport == RawCCodePointer && leaf.Rep != DirectPlain {
			return planDigestValue{}, fmt.Errorf("coro: SSAValuePlan at %s has raw C code-pointer transport with representation %s", formatDigestValueSite(site), leaf.Rep)
		}
		targets, err := p.canonicalDigestTargets(leaf.Targets)
		if err != nil {
			return planDigestValue{}, fmt.Errorf("coro: SSAValuePlan at %s: %w", formatDigestValueSite(site), err)
		}
		path := make([]planDigestPathStep, len(leaf.Path))
		for pathIndex, step := range leaf.Path {
			if err := validateDigestPathStep(step); err != nil {
				return planDigestValue{}, fmt.Errorf("coro: SSAValuePlan at %s path step %d: %w", formatDigestValueSite(site), pathIndex, err)
			}
			path[pathIndex] = planDigestPathStep{Kind: uint8(step.Kind), Index: step.Index}
		}
		ret.Funcs = append(ret.Funcs, planDigestFuncLeaf{
			Path:      path,
			Rep:       uint8(leaf.Rep),
			Transport: uint8(leaf.Transport),
			Targets:   targets,
			MayBeNil:  leaf.MayBeNil,
		})
	}
	return ret, nil
}

func validateDigestPathStep(step FuncPathStep) error {
	if step.Kind > FuncPathChanElement {
		return fmt.Errorf("invalid function path kind %d", uint8(step.Kind))
	}
	switch step.Kind {
	case FuncPathTupleElement, FuncPathStructField:
		if step.Index < 0 {
			return fmt.Errorf("function path kind %d requires a nonnegative index", step.Kind)
		}
	default:
		if step.Index != -1 {
			return fmt.Errorf("function container path kind %d requires index -1", step.Kind)
		}
	}
	return nil
}

func equalDigestFuncPath(left, right []FuncPathStep) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (p *SSAPlan) canonicalDigestTargets(targets []FunctionID) ([]FunctionID, error) {
	ret := append([]FunctionID(nil), targets...)
	sortFunctionIDs(ret)
	canonical := make([]FunctionID, 0, len(ret))
	for _, target := range ret {
		if err := target.validate(); err != nil {
			return nil, err
		}
		if _, ok := p.byID[target]; !ok {
			return nil, fmt.Errorf("target function %q is absent from the SSA plan", target)
		}
		if len(canonical) == 0 || canonical[len(canonical)-1] != target {
			canonical = append(canonical, target)
		}
	}
	return canonical, nil
}
