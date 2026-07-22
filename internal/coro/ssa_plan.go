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
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/tools/go/callgraph/cha"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// DefaultMaxPlainInstructions is the initial static cost bound used when
// SSAConfig.MaxPlainInstructions is zero. A negative value disables this seed.
const DefaultMaxPlainInstructions = 128

// DynamicResolution selects how AnalyzeSSA resolves interface and function
// value calls. The default avoids a potentially quadratic CHA graph and keeps
// every dynamic call conservatively open.
type DynamicResolution uint8

const (
	// DynamicUnknownOnly emits a conservative unknown edge for every dynamic
	// call and does not use CHA candidates.
	DynamicUnknownOnly DynamicResolution = iota
	// DynamicCHAOpen emits known CHA candidates and retains an unknown edge.
	DynamicCHAOpen
	// DynamicCHAClosed removes the unknown edge only when CHA reports a nonempty
	// candidate set and every candidate remains in the effective program. The
	// caller is responsible for establishing the closed-world assumption.
	DynamicCHAClosed
)

func (r DynamicResolution) validate() error {
	if r > DynamicCHAClosed {
		return fmt.Errorf("coro: invalid dynamic resolution mode %d", uint8(r))
	}
	return nil
}

// Root is an externally established entry demand. Hard synchronous crossings
// use SyncDemand; main, init, and goroutine roots use AsyncDemand. Duplicate
// roots are joined, so the same function may become BothDemand.
type Root struct {
	Function *ssa.Function
	// Demand is the compatibility managed-demand seed. New producers should
	// use ManagedDemand; both are joined.
	Demand Demand
	// ManagedDemand selects ordinary Go entry capabilities.
	ManagedDemand Demand
	// RawPlainDemand selects exact legacy-stack reachability without creating a
	// managed entry demand.
	RawPlainDemand bool
}

// Roots is a set of externally established SSA entry demands.
type Roots []Root

// SSAFunctionPolicy adds trusted frontend or imported-summary facts to the
// conservative facts inferred from a function body.
type SSAFunctionPolicy struct {
	Effect Effect
	Exec   ExecFlags
	// CallableIdentityCertificate is the execution-policy-neutral identity of
	// one exact managed C declaration. It may coexist with either a generic
	// behavior contract, a legacy physical capability, or neither.
	CallableIdentityCertificate CallableIdentityCertificate
	// CallableContractCertificate is the exact generic callable contract frozen
	// by the frontend. Declaration scope may classify an otherwise bodyless
	// foreign boundary from its progress dimension. Wrapper scope is metadata
	// only and never suppresses SSA body analysis.
	CallableContractCertificate CallableContractCertificate
	// ForeignNoBlockCertificate is a frozen frontend proof that one exact
	// external declaration has a bounded, nonblocking physical ABI. The
	// opaque certificate identity is retained in SSAPlan and its archive digest;
	// it must never be synthesized from a display name. Certified declarations
	// remain IRQUnsafe unless a separate proof exists; this certificate removes
	// only BlockForeign/WaitForeign.
	ForeignNoBlockCertificate string
	// ForeignSyncCertificate is a frozen proof for one exact C declaration
	// carrying //llgo:coro sync. Like noblock it produces an
	// ExternalKnown/NoSuspend/IRQUnsafe plan, but its distinct identity records
	// the weaker latency contract: internal locks and GC pauses are permitted.
	ForeignSyncCertificate string
	// ForeignSchedulerWaitCertificate freezes one exact //llgo:coro
	// schedulerwait C symbol+ABI. It deliberately retains the ordinary managed
	// ExternalUnknownForeign/BlockForeign/WaitForeign model. Only a separate
	// compiler-owned raw host/scheduler-stack closure validator may consume this
	// physical capability.
	ForeignSchedulerWaitCertificate string
	// ForeignWorkerCertificate freezes one exact //llgo:coro worker C
	// symbol+structural-ABI+link identity. It retains the managed
	// ExternalUnknownForeign/BlockForeign/WaitForeign model and authorizes only
	// a separate worker-call validator which also proves the call-site transport
	// ABI. The declaration contract is thread-independent, synchronous to worker
	// completion, callback-free, by-value, and non-retaining for Go pointers.
	ForeignWorkerCertificate string
	// AssemblyNoSuspendCertificate is an exact frontend/build proof over one
	// retained translated-assembly definition and its complete direct-call
	// closure. It has the same no-suspend/IRQ-unsafe external summary as a
	// certified C leaf, but remains a physical Go-ABI call and is never elided.
	AssemblyNoSuspendCertificate string
	// IgnoreBody states that the frontend does not emit this SSA body's Go
	// instructions because the function is an external declaration in the
	// frozen physical ABI. AnalyzeSSA excludes that body from value flow, calls,
	// references, recursion, local-body identity, Call/Value plans, and digest
	// sites. The same trusted policy must explicitly override External to a
	// non-Defined kind.
	IgnoreBody bool
	// TrustedNoPreempt clears only the scanner's local CFG/instruction-budget
	// NeedsPreempt seed. It is reserved for bounded compiler/runtime islands
	// that execute on the scheduler stack and therefore must retain a plain ABI.
	// It does not clear recursion, suspend effects, or any other execution flag.
	TrustedNoPreempt bool
	// TrustedBoundedRecursion certifies that this exact function has a bounded
	// recursion depth. The claim is effective only when every member of its
	// complete managed recursive SCC is certified. It suppresses only the
	// YieldOnly/NeedsPreempt pair that graph analysis would otherwise add for
	// recursion; scanner seeds, explicit facts, and callee effects remain intact.
	TrustedBoundedRecursion bool
	// RawPlainEntry is a frozen frontend proof that one exact owned,
	// non-capturing Go function is directly entered by a raw synchronous ABI
	// crossing. It requests an externally addressable legacy entry without
	// suppressing managed unwind, suspension, or preemption facts.
	RawPlainEntry bool
	// RawPlainVariant is a frozen whole-build proof that this exact owned Go body
	// belongs to the synchronously executable closure of a RawPlainEntry. Unlike
	// RawPlainEntry it is not an address-publication capability and may describe
	// a captured anonymous function whose legacy body is reached only through its
	// canonical closure context. RawPlainEntry implies RawPlainVariant.
	RawPlainVariant bool

	External         ExternalKind
	OverrideExternal bool
	NeedsDispatch    bool
}

func validateSSACallableContractPolicy(fn *ssa.Function, policy SSAFunctionPolicy) error {
	certificate := policy.CallableContractCertificate
	switch certificate.Scope {
	case CallableContractScopeWrapper:
		if fn == nil || len(fn.Blocks) == 0 || policy.IgnoreBody ||
			policy.OverrideExternal && policy.External != Defined {
			return fmt.Errorf("callable wrapper contract requires an analyzed defined Go body")
		}
		required := CallableContractExecConstraints(certificate.Contract)
		if certificate.Contract.Progress == ProgressNoReturn {
			required |= NoReturn
		}
		if !policy.Exec.Contains(required) {
			return fmt.Errorf("callable wrapper contract requires execution constraints %s", required)
		}
		return nil
	case CallableContractScopeDeclaration:
		if !policy.IgnoreBody || !policy.OverrideExternal || policy.NeedsDispatch || policy.Effect != NoSuspend {
			return fmt.Errorf("callable declaration contract requires one ignored, direct external declaration")
		}
		expectedExternal := ExternalUnknownForeign
		expectedExec := BlockForeign | IRQUnsafe | CallableContractExecConstraints(certificate.Contract)
		switch certificate.Contract.Progress {
		case ProgressExecutorSafe:
			expectedExternal = ExternalKnown
			expectedExec &^= BlockForeign
		case ProgressMayBlock, ProgressUnknown, ProgressAsyncCompletion:
			// Auto remains a foreign stack cut. The caller receives WaitForeign
			// from CallForeign; the declaration itself retains BlockForeign.
		case ProgressNoReturn:
			// NoReturn alone does not prove executor safety. Preserve the
			// foreign stack cut while retaining the terminal control-flow fact.
			expectedExec |= NoReturn
		default:
			return fmt.Errorf("callable declaration has unsupported progress %q", certificate.Contract.Progress)
		}
		if policy.External != expectedExternal || policy.Exec != expectedExec {
			return fmt.Errorf(
				"callable declaration progress %q requires external=%s exec=%s, got external=%s exec=%s",
				certificate.Contract.Progress, expectedExternal, expectedExec, policy.External, policy.Exec,
			)
		}
		return nil
	default:
		return fmt.Errorf("callable contract has invalid scope %q", certificate.Scope)
	}
}

// SSAFunctionResolver maps an SSA function referenced by an effective body to
// the exact canonical function analyzed and emitted by the frontend. ok=false
// means that the reference has no managed target in the effective compilation.
// A successful result must be non-nil, belong to the analyzed Program, and,
// when EmissionUniverse is set, be an exact member of that universe.
//
// AnalyzeSSA memoizes results by input pointer, so the resolver must be pure.
// Frontends use this hook for patch aliases whose unchanged caller bodies still
// refer to the replaced SSA declaration.
type SSAFunctionResolver func(fn *ssa.Function) (canonical *ssa.Function, ok bool, err error)

// SSASyncOnlyCallArgument identifies one exact ordinary static-call argument
// whose function value is published solely for a certified synchronous
// descriptor consumer. The physical value still crosses canonical storage and
// therefore remains Dispatch; only this exact reference edge is SyncOnly.
// Other uses of the same value or target remain ordinary references.
type SSASyncOnlyCallArgument struct {
	Call     ssa.CallInstruction
	Argument int
}

// SSAClosedDynamicCallCertificate is a trusted frontend proof for one exact
// ordinary dynamic call or owner-local defer. V0 intentionally accepts at
// most one non-nil target:
// the narrow form is sufficient for fields whose whole-program writes are
// proven to contain either nil or one descriptor-backed function value.
//
// Targets is copied and validated before analysis. An empty Targets slice is a
// closed nil-only value and therefore requires MayBeNil. A singleton may be
// either nullable or non-null. The target must be an exact canonical, owned Go
// body in the effective emission universe with the call's exact signature.
// Captured bodies are admissible because a certified managed call retains the
// canonical Dispatch representation; the trusted producer proof must also own
// the exact MakeClosure environment carried by that descriptor.
type SSAClosedDynamicCallCertificate struct {
	Targets  []*ssa.Function
	MayBeNil bool
	// SyncDispatch proves that this exact call site synchronously invokes the
	// selected descriptor plain entry. It is a physical frontend/build fact,
	// not an effect inference from the target name or current body. Analysis
	// retains synchronous demand for the closed target without propagating its
	// effects into the owner, then fails closed unless the fixed point selects
	// one non-suspending plain primary. Ordinary closed descriptor calls keep
	// this false and remain managed effect-propagating calls.
	SyncDispatch bool
	// SyncOnlyCallArguments are the exact source publications audited by the
	// same proof that closes this synchronous descriptor call. They suppress
	// async-context demand only at these argument operands; upstream transfers
	// and every other publication remain conservative ordinary references.
	SyncOnlyCallArguments []SSASyncOnlyCallArgument
}

// SSATrustedInlineCallCertificate binds one exact ordinary static call to an
// executor-safe refinement already owned by its conservative foreign target.
// ID is the frozen producer/invocation proof identity; Contract and ABI are
// retained independently so plan verification and archive digests cannot
// silently accept a capability under a different call shape.
type SSATrustedInlineCallCertificate struct {
	ID       string
	Contract ContractID
	ABI      string
}

// SSALoweredCall records one exact call inserted by frontend lowering even
// though no CallInstruction for it exists in the source SSA body.
// LogicalName is a frontend-owned stable identity used to resolve the exact
// helper again during code generation; it is not a symbol-name heuristic.
//
// AnalyzeSSA projects an ordinary record as a managed direct call and may
// refine that edge to a foreign boundary from the target's frozen function
// policy, exactly as it does for an explicit static call. UnwindOnly records
// remain exact managed demand edges, but do not propagate target effects into
// the owner's normal-return plan. RawPlain records instead describe a
// compiler-owned raw/plain invocation and therefore propagate only raw-plain
// target demand, never managed call effects into the owner.
type SSALoweredCall struct {
	LogicalName string
	Target      *ssa.Function
	// RawPlain is a frozen occurrence-level proof that every physical use of
	// this owner/logical-name pair invokes Target's raw plain body directly on
	// the current legacy stack. It is neither a managed call nor an unwind
	// boundary. Code generation must consume this record explicitly; the fact
	// must not be reconstructed from Target or its symbol name.
	RawPlain bool
	// UnwindOnly is true only when every physical use of LogicalName in this
	// owner is in a CFG block that cannot reach a normal Return. It is a frozen
	// frontend proof, not a target-name or runtime-policy heuristic.
	UnwindOnly bool
	// ExplicitStatusElided is an exact site-aggregate proof that every use
	// represented by this owner/logical-name pair is the terminal helper of a
	// source panic instruction. A physical ExplicitStatus coroutine publishes
	// that instruction's outcome directly and emits no helper call. Plain bodies
	// still use the mapping, so analysis retains synchronous target demand.
	// This field is valid only together with UnwindOnly.
	ExplicitStatusElided bool
}

// SSAConfig controls the SSA-to-Graph analysis bridge. It deliberately has no
// lowering or runtime switches.
type SSAConfig struct {
	FunctionIDs FunctionIDConfig

	// EmissionUniverse restricts analysis to the exact SSA function objects
	// materialized for this frontend compilation. When non-nil, AnalyzeSSA does
	// not add package members, static callees, or CHA nodes outside the universe,
	// and every root must be a universe member. A nil universe preserves the
	// legacy whole-Program enumeration.
	EmissionUniverse *SSAEmissionUniverse

	// ResolveFunction canonicalizes patched or otherwise aliased function
	// pointers before every graph, function-value-flow, CHA, and CallPlan use.
	// Nil is the identity resolver. With EmissionUniverse, successful results
	// must be exact universe members.
	ResolveFunction SSAFunctionResolver

	// MaxPlainInstructions seeds NeedsPreempt on a longer body. Zero selects
	// DefaultMaxPlainInstructions; a negative value disables the cost seed.
	// This is an early heuristic, not the final cross-call MaxAtomicCost proof.
	MaxPlainInstructions int

	// ClassifyLocalBody supplies the immutable frontend-owned semantic recipe
	// projection for one defined body. Active LLGo builds install this callback
	// from CoroProgramIR so analysis does not rescan raw SSA to rediscover local
	// channel, cleanup, panic, instruction-budget, or CFG-cycle facts. A nil
	// callback preserves the standalone analyzer used by internal unit tests and
	// third-party report tooling.
	ClassifyLocalBody func(*ssa.Function) (SSAFunctionBodyFacts, error)

	// DynamicResolution defaults to DynamicUnknownOnly. AnalyzeSSA's function
	// enumeration may lazily materialize method wrappers in legacy whole-Program
	// mode. With EmissionUniverse, CHA candidate discovery examines only the
	// frozen functions and does not enumerate the Program.
	DynamicResolution DynamicResolution

	// OutcomeMode selects legacy stack unwind or demand-sensitive structured
	// Return/Panic outcomes. The zero value preserves legacy planning.
	OutcomeMode OutcomeMode

	// DynamicImplements supplies the frozen frontend's exact effective-type
	// implementation relation for restricted CHA. It exists for emission
	// universes whose patched Go types are not pointer-identical to the raw SSA
	// invoke interface or method receiver types. A nil callback uses
	// go/types.Implements. The callback must be pure and deterministic and is
	// accepted only with EmissionUniverse; any callback error aborts analysis
	// before a partial plan can escape.
	DynamicImplements func(candidate types.Type, iface *types.Interface) (bool, error)

	// Include filters the effective program (for example, after patch/skip
	// resolution). A static edge to an excluded target becomes an unknown call.
	Include func(*ssa.Function) (bool, error)

	// ClassifyFunction supplies trusted effect, execution, external, and value
	// representation facts. A nil callback leaves bodyless functions as
	// ExternalUnknownManaged; the scanner never guesses C/assembly by name.
	ClassifyFunction func(*ssa.Function) (SSAFunctionPolicy, error)

	// ClassifyUnknownCall distinguishes the fallback execution domain when
	// static or structural function-value flow cannot completely resolve the
	// managed targets. UnknownManagedDispatch certifies universal function-value
	// descriptor transport. UnknownManagedInterfaceDispatch separately certifies
	// that an ordinary interface invoke's itab Ifn_ word is a universal method
	// descriptor. Both contribute a structured await, but neither changes an
	// unrelated operand ABI nor certifies raw/foreign dispatch. Exact Go targets
	// use ClassifyFunction instead. The default is UnknownManaged.
	ClassifyUnknownCall func(caller *ssa.Function, call ssa.CallInstruction) (UnknownTarget, error)

	// ClassifyRawCFunctionType identifies one exact named frontend function
	// type whose physical value is a single C code pointer. It is a transport
	// fact only: invocation progress, affinity, reentry, and memory behavior are
	// still classified independently as a foreign call. AnalyzeSSA never infers
	// this from a package or type name.
	ClassifyRawCFunctionType func(typ types.Type) (bool, error)

	// ClassifyElidedCall identifies a direct static call for which the frontend
	// emits no callable edge to that exact SSA declaration: either the call is
	// omitted entirely, a proven no-suspend compiler intrinsic is lowered inline
	// in the caller, or the declaration is replaced by exact calls supplied
	// through ClassifyLoweredCalls. Such a site has no CallPlan but remains in the
	// plan/digest. Eliding the declaration does not elide separately classified
	// lowered calls or their effects. The callback is trusted frontend policy,
	// not an effect summary: AnalyzeSSA rejects attempts to elide go, defer, or
	// dynamic calls. Argument-producing SSA instructions remain analyzed
	// independently.
	ClassifyElidedCall func(caller *ssa.Function, call ssa.CallInstruction) (bool, error)

	// ClassifyElidedCallCertificate freezes an optional opaque frontend
	// capability for one exact elided call occurrence. It is consulted only
	// after ClassifyElidedCall accepted the same call, copied into SSAPlan, and
	// included in CoroPlanDigest. An empty string means the elision needs no
	// additional capability.
	ClassifyElidedCallCertificate func(caller *ssa.Function, call ssa.CallInstruction) (string, error)

	// ClassifyTrustedInlineCall identifies one exact ordinary static call whose
	// target remains conservatively ExternalUnknownForeign/BlockForeign for all
	// other uses, but whose frozen callable fact contains an executor-safe
	// refinement valid at this occurrence. AnalyzeSSA accepts only a closed
	// direct target and copies ID, Contract, and ABI into its CallPlan/digest.
	// It suppresses WaitForeign/BlockForeign at this edge and replaces only the
	// target default callable-contract execution projection with the selected
	// refinement. Independent IRQ, unwind, and every unrelated body effect or
	// execution constraint remain conservative.
	ClassifyTrustedInlineCall func(caller *ssa.Function, call ssa.CallInstruction) (SSATrustedInlineCallCertificate, bool, error)

	// ClassifyDirectPlainCallArgument identifies one exact static-call argument
	// use whose frontend ABI synchronously invokes a direct plain entry while
	// retaining managed SyncDemand, rather than materializing a Go
	// closure/dispatch value. The exemption applies only to
	// that (call, argument-index) boundary: any store, interface conversion,
	// ordinary Go argument, open flow, or multi-target flow in the same value
	// component still requires Dispatch and makes the trusted claim fail closed.
	// The callback must not classify go, defer, dynamic, builtin, or non-function
	// arguments. Frontends should reserve it for managed synchronous callback
	// ABI facts. Raw C callback publications use the independent classifier
	// below so they do not manufacture managed demand.
	ClassifyDirectPlainCallArgument func(caller *ssa.Function, call ssa.CallInstruction, argument int) (bool, error)

	// ClassifyRawDirectPlainCallArgument identifies the same exact closed,
	// non-nil singleton/direct-plain static-call argument shape as
	// ClassifyDirectPlainCallArgument, but its reference propagates only
	// RawPlainDemand. This is the physical provenance for a source-level C
	// function-pointer parameter synchronously invoked on a legacy/native stack.
	//
	// A use must not be classified by both callbacks. Open, nullable,
	// multi-target, stored, boxed/interface, or otherwise canonically published
	// values fail the shared function-value-flow proof closed.
	ClassifyRawDirectPlainCallArgument func(caller *ssa.Function, call ssa.CallInstruction, argument int) (bool, error)

	// ClassifyRawFunctionAddressCallArgument identifies an exact direct static
	// call argument whose frontend lowering consumes a transient
	// MakeInterface{X:*ssa.Function} structurally and emits only X's raw entry
	// address. The interface value is never materialized, so this one use must
	// not force X into Dispatch representation. AnalyzeSSA validates the exact
	// SSA shape and sole-consumer relationship; all ordinary interface uses keep
	// their canonical descriptor boundary.
	ClassifyRawFunctionAddressCallArgument func(caller *ssa.Function, call ssa.CallInstruction, argument int) (bool, error)

	// ClassifyStaticCodeAddressCallArgument identifies the same exact transient
	// MakeInterface{X:*ssa.Function} shape when the frontend only observes X's
	// selected code address (for example internal/abi.FuncPCABIInternal). Unlike
	// ClassifyRawFunctionAddressCallArgument, the resulting pointer is not a
	// synchronous invocation capability and therefore must not demand a plain
	// entry. The exact use still suppresses interface materialization and
	// Dispatch representation; ordinary uses of the same value remain canonical
	// descriptor boundaries.
	ClassifyStaticCodeAddressCallArgument func(caller *ssa.Function, call ssa.CallInstruction, argument int) (bool, error)

	// ClassifyClosedDynamicCall supplies a frozen whole-program proof for one
	// exact ordinary dynamic *ssa.Call or owner-local *ssa.Defer whose callee value crosses descriptor
	// storage but has a closed nil-or-singleton target set. This is not a general
	// points-to hint: AnalyzeSSA rejects static calls, invokes, go sites,
	// multiple targets, captured functions, signature mismatches, aliases,
	// external declarations, and targets outside the effective universe.
	//
	// A certified callee remains Dispatch because it crossed canonical storage;
	// the certificate only closes its graph edge and CallPlan target set. The
	// callback is trusted to have rejected every unknown physical write or escape
	// that could reach the exact value loaded at call. SyncDispatch additionally
	// freezes the exact synchronous consumer ABI; it never applies to an open,
	// mixed, invoke, go, or defer site.
	ClassifyClosedDynamicCall func(caller *ssa.Function, call ssa.CallInstruction) (SSAClosedDynamicCallCertificate, bool, error)

	// ClassifyDemandReferences supplies exact function addresses that the
	// frontend implicitly embeds while lowering one function body, even though
	// they are not operands in that body's SSA instructions. Runtime ABI method
	// tables are the canonical example. These references propagate entry demand
	// only; they do not propagate suspend effects or execution flags.
	//
	// Every returned target must be a non-nil exact canonical member of the
	// effective emission universe. AnalyzeSSA calls the classifier only for
	// owned, non-ignored bodies and copies the returned slice before use.
	ClassifyDemandReferences func(owner *ssa.Function) ([]*ssa.Function, error)

	// ClassifySyncDemandReferences selects the exact subset of
	// ClassifyDemandReferences that is consumed only through a synchronous raw
	// ABI word. Such a reference always contributes SyncDemand, even when its
	// owner is a coroutine. The analyzer verifies canonical identity and subset
	// membership; this is a physical use-site contract, not a callee effect
	// exemption. Returning a target that is absent from the ordinary demand
	// references fails closed.
	ClassifySyncDemandReferences func(owner *ssa.Function) ([]*ssa.Function, error)

	// ClassifyRawPlainDemandReferences supplies exact legacy-stack references
	// embedded by frontend lowering. Unlike ClassifySyncDemandReferences these
	// references enter only the raw provenance domain. They must not be used for
	// any/interface/open managed transport.
	ClassifyRawPlainDemandReferences func(owner *ssa.Function) ([]*ssa.Function, error)

	// ClassifyConditionalManagedStoreReference identifies one exact Store whose
	// scalar function-valued operand is published into a frozen, closed managed
	// descriptor cell. The returned target is the one exact canonical body
	// carried by Store.Val. Unlike an ordinary publication this occurrence does
	// not itself demand an entry: each frozen dynamic reader contributes its own
	// managed call edge. If no reader or other consumer is live, code generation
	// may elide this otherwise unobservable Store and leave target EmitNone.
	//
	// This is an occurrence capability, not a global/type/name policy. AnalyzeSSA
	// rejects a target that is absent from Store.Val's exact singleton flow. The
	// classifier must be backed by a complete writer/reader/escape proof; it does
	// not authorize raw invocation or weaken an active reader's Dispatch ABI.
	ClassifyConditionalManagedStoreReference func(owner *ssa.Function, store *ssa.Store) (target *ssa.Function, classified bool, err error)

	// ClassifyLoweredCalls supplies exact runtime/helper calls that the frontend
	// inserts while lowering one function body but which have no corresponding
	// source SSA CallInstruction. Unlike ClassifyDemandReferences, these are real
	// calls: their effects and inheritable execution constraints propagate into
	// owner, and demand reaches their selected plain or coroutine entry only when
	// owner itself is demanded.
	//
	// LogicalName must be nonempty and unique within owner. Every target must be
	// a non-nil exact canonical member of the effective emission universe. The
	// classifier is called only for owned, non-ignored bodies and its result is
	// copied, validated, and sorted before it becomes part of the immutable plan.
	ClassifyLoweredCalls func(owner *ssa.Function) ([]SSALoweredCall, error)
}

// SSAFunctionBodyFacts is the local, non-call-propagated body projection
// frozen by the frontend ProgramIR builder. InstructionCount excludes debug
// instructions. HasCycle records only source CFG topology; AnalyzeSSA applies
// the configured preemption budget after loading these facts.
type SSAFunctionBodyFacts struct {
	Effect           Effect
	Exec             ExecFlags
	InstructionCount int
	HasCycle         bool
}

func (facts SSAFunctionBodyFacts) validate() error {
	if err := facts.Effect.Validate(); err != nil {
		return fmt.Errorf("local body effect: %w", err)
	}
	if facts.Effect != facts.Effect.Normalize() {
		return fmt.Errorf("local body effect %s is not normalized", facts.Effect)
	}
	if err := facts.Exec.Validate(); err != nil {
		return fmt.Errorf("local body execution flags: %w", err)
	}
	if facts.InstructionCount < 0 {
		return fmt.Errorf("local body instruction count is negative")
	}
	return nil
}

// SSAFunctionPlan binds an immutable FunctionPlan back to its SSA function.
type SSAFunctionPlan struct {
	Function *ssa.Function
	Plan     FunctionPlan
}

// SSARootPlan records one canonical externally established entry demand.
// Duplicate and aliased input roots are joined before this record is created.
type SSARootPlan struct {
	Function       *ssa.Function
	ID             FunctionID
	Demand         Demand
	ManagedDemand  Demand
	RawPlainDemand bool
}

// SSAPlan is the compilation-scoped whole-program result. Its maps remain
// private so consumers cannot reconstruct identities from display strings.
type SSAPlan struct {
	plan                   *Plan
	roots                  []SSARootPlan
	functions              []SSAFunctionPlan
	byFunction             map[*ssa.Function]FunctionID
	byID                   map[FunctionID]*ssa.Function
	ignoredBodies          map[*ssa.Function]struct{}
	rawPlainVariants       map[*ssa.Function]struct{}
	valuePlans             map[ssa.Value]SSAValuePlan
	callPlans              map[ssa.CallInstruction]SSACallPlan
	elidedCalls            map[ssa.CallInstruction]struct{}
	elidedCallCertificates map[ssa.CallInstruction]string
	rawAddressArgs         map[ssaCallArgumentUse]struct{}
	codeAddressArgs        map[ssaCallArgumentUse]struct{}
	conditionalStores      map[*ssa.Store]*ssa.Function
	safeFixedArrayIndexes  map[ssa.Instruction]int64
	loweredCalls           map[*ssa.Function][]SSALoweredCall
	foreignNoBlock         map[*ssa.Function]string
	foreignSync            map[*ssa.Function]string
	foreignSchedulerWait   map[*ssa.Function]string
	foreignWorker          map[*ssa.Function]string
	callableIdentities     map[*ssa.Function]CallableIdentityCertificate
	callableContracts      map[*ssa.Function]CallableContractCertificate
	assemblyNoSuspend      map[*ssa.Function]string
	functionIDs            FunctionIDConfig
}

type ssaFunctionResolution struct {
	canonical *ssa.Function
	ok        bool
	err       error
}

type ssaFunctionCanonicalizer struct {
	prog     *ssa.Program
	universe *SSAEmissionUniverse
	callback SSAFunctionResolver
	memo     map[*ssa.Function]ssaFunctionResolution
	active   map[*ssa.Function]bool
}

func newSSAFunctionCanonicalizer(prog *ssa.Program, config SSAConfig) *ssaFunctionCanonicalizer {
	return &ssaFunctionCanonicalizer{
		prog:     prog,
		universe: config.EmissionUniverse,
		callback: config.ResolveFunction,
		memo:     make(map[*ssa.Function]ssaFunctionResolution),
		active:   make(map[*ssa.Function]bool),
	}
}

func (r *ssaFunctionCanonicalizer) resolve(fn *ssa.Function) (*ssa.Function, bool, error) {
	if fn == nil {
		return nil, false, nil
	}
	if result, ok := r.memo[fn]; ok {
		return result.canonical, result.ok, result.err
	}
	if r.active[fn] {
		return nil, false, fmt.Errorf("resolver cycle at function %q", fn.Name())
	}
	r.active[fn] = true
	defer delete(r.active, fn)

	canonical, ok, err := fn, true, error(nil)
	if r.callback != nil {
		canonical, ok, err = r.callback(fn)
	} else if r.universe != nil && !r.universe.Contains(fn) {
		ok = false
	}
	if err == nil && ok && canonical == nil {
		err = fmt.Errorf("resolver returned a nil canonical function")
	}
	if err == nil && ok && canonical.Prog != r.prog {
		err = fmt.Errorf("resolver returned function %q from another SSA program", canonical.Name())
	}
	if err == nil && r.universe != nil {
		switch {
		case r.universe.Contains(fn) && !ok:
			err = fmt.Errorf("resolver rejected exact emission-universe member %q", fn.Name())
		case r.universe.Contains(fn) && canonical != fn:
			err = fmt.Errorf("resolver remapped exact emission-universe member %q", fn.Name())
		case ok && !r.universe.Contains(canonical):
			err = fmt.Errorf("resolver returned function %q outside the SSA emission universe", canonical.Name())
		}
	}
	if err == nil && ok && r.callback != nil && canonical != fn {
		// The callback contract requires a final canonical pointer. Verify that
		// successful aliases do not form chains or depend on the call site.
		final, finalOK, finalErr := r.resolve(canonical)
		switch {
		case finalErr != nil:
			err = fmt.Errorf("verify canonical function %q: %w", canonical.Name(), finalErr)
		case !finalOK || final == nil:
			err = fmt.Errorf("canonical function %q does not resolve to itself", canonical.Name())
		case final != canonical:
			err = fmt.Errorf("canonical function %q resolves to a different function", canonical.Name())
		default:
			r.memo[canonical] = ssaFunctionResolution{canonical: canonical, ok: true}
		}
	}
	result := ssaFunctionResolution{canonical: canonical, ok: ok, err: err}
	r.memo[fn] = result
	return result.canonical, result.ok, result.err
}

// BasePlan returns the target-independent immutable fixed-point plan.
func (p *SSAPlan) BasePlan() *Plan {
	if p == nil {
		return nil
	}
	return p.plan
}

// Functions returns SSA/function-plan pairs in FunctionID order.
func (p *SSAPlan) Functions() []SSAFunctionPlan {
	if p == nil {
		return nil
	}
	return append([]SSAFunctionPlan(nil), p.functions...)
}

// Roots returns canonical joined explicit roots in strict FunctionID order.
// The returned slice is a defensive copy.
func (p *SSAPlan) Roots() []SSARootPlan {
	if p == nil {
		return nil
	}
	return append([]SSARootPlan(nil), p.roots...)
}

// FunctionID returns the stable identity assigned to fn.
func (p *SSAPlan) FunctionID(fn *ssa.Function) (FunctionID, bool) {
	if p == nil {
		return "", false
	}
	id, ok := p.byFunction[fn]
	return id, ok
}

// FunctionPlan returns the immutable plan assigned to the exact SSA function
// object fn. It does not derive or match an identity for a function from a
// different SSA program.
func (p *SSAPlan) FunctionPlan(fn *ssa.Function) (FunctionPlan, bool) {
	if p == nil {
		return FunctionPlan{}, false
	}
	id, ok := p.byFunction[fn]
	if !ok {
		return FunctionPlan{}, false
	}
	return p.plan.Lookup(id)
}

// IgnoresBody reports whether trusted frontend policy declared that fn's SSA
// body is not part of the physically emitted program. Such a body contributes
// no flow, calls, references, effects, recursion, or digest sites.
func (p *SSAPlan) IgnoresBody(fn *ssa.Function) bool {
	if p == nil || fn == nil {
		return false
	}
	_, ok := p.ignoredBodies[fn]
	return ok
}

// HasRawPlainVariant reports whether fn has an exact, independently validated
// legacy-stack body for use inside a raw synchronous closure. This does not
// authorize publishing fn as a raw address; only FunctionPlan.RawPlainEntry
// carries that physical crossing capability.
func (p *SSAPlan) HasRawPlainVariant(fn *ssa.Function) bool {
	if p == nil || fn == nil {
		return false
	}
	_, ok := p.rawPlainVariants[fn]
	return ok
}

// ForeignNoBlockCertificate returns the opaque frozen frontend certificate
// attached to one exact external declaration. The certificate is part of the
// immutable SSA plan and CoroPlanDigest; callers must not infer it from a
// function name or external symbol spelling.
func (p *SSAPlan) ForeignNoBlockCertificate(fn *ssa.Function) (string, bool) {
	if p == nil || fn == nil {
		return "", false
	}
	certificate, ok := p.foreignNoBlock[fn]
	return certificate, ok
}

// ForeignSyncCertificate returns the opaque frozen same-thread synchronous C
// certificate attached to fn.
func (p *SSAPlan) ForeignSyncCertificate(fn *ssa.Function) (string, bool) {
	if p == nil || fn == nil {
		return "", false
	}
	certificate, ok := p.foreignSync[fn]
	return certificate, ok
}

// ForeignSchedulerWaitCertificate returns the opaque exact physical wait
// certificate attached to fn. It does not weaken fn's managed plan.
func (p *SSAPlan) ForeignSchedulerWaitCertificate(fn *ssa.Function) (string, bool) {
	if p == nil || fn == nil {
		return "", false
	}
	certificate, ok := p.foreignSchedulerWait[fn]
	return certificate, ok
}

// ForeignWorkerCertificate returns the opaque exact worker-safe C declaration
// certificate attached to fn. It does not weaken fn's managed blocking plan.
func (p *SSAPlan) ForeignWorkerCertificate(fn *ssa.Function) (string, bool) {
	if p == nil || fn == nil {
		return "", false
	}
	certificate, ok := p.foreignWorker[fn]
	return certificate, ok
}

// CallableContractCertificate returns the complete frozen target-neutral
// contract attached to fn. The returned value is a copy; callers cannot mutate
// the immutable plan or reconstruct a contract from a raw function address.
func (p *SSAPlan) CallableContractCertificate(fn *ssa.Function) (CallableContractCertificate, bool) {
	if p == nil || fn == nil {
		return CallableContractCertificate{}, false
	}
	certificate, ok := p.callableContracts[fn]
	return certificate, ok
}

// CallableIdentityCertificate returns the execution-policy-neutral identity
// frozen for one exact managed C declaration.
func (p *SSAPlan) CallableIdentityCertificate(fn *ssa.Function) (CallableIdentityCertificate, bool) {
	if p == nil || fn == nil {
		return CallableIdentityCertificate{}, false
	}
	certificate, ok := p.callableIdentities[fn]
	return certificate, ok
}

// AssemblyNoSuspendCertificate returns the opaque proof attached to one exact
// retained translated-assembly definition. The proof participates in the plan
// digest and must not be reconstructed from a package or symbol name.
func (p *SSAPlan) AssemblyNoSuspendCertificate(fn *ssa.Function) (string, bool) {
	if p == nil || fn == nil {
		return "", false
	}
	certificate, ok := p.assemblyNoSuspend[fn]
	return certificate, ok
}

// LoweredCalls returns the exact compiler-inserted calls frozen for owner in
// LogicalName order. The returned slice is a defensive copy.
func (p *SSAPlan) LoweredCalls(owner *ssa.Function) []SSALoweredCall {
	if p == nil || owner == nil {
		return nil
	}
	return append([]SSALoweredCall(nil), p.loweredCalls[owner]...)
}

// ResolveLoweredCall resolves one frontend logical helper identity for owner.
// ok is false when the exact owner has no call with that identity.
func (p *SSAPlan) ResolveLoweredCall(owner *ssa.Function, logicalName string) (*ssa.Function, bool) {
	call, ok := p.ResolveLoweredCallRecord(owner, logicalName)
	if !ok {
		return nil, false
	}
	return call.Target, true
}

// ResolveLoweredCallRecord resolves one frontend logical helper identity for
// owner and returns its complete frozen occurrence record. ok is false when the
// exact owner has no call with that identity.
func (p *SSAPlan) ResolveLoweredCallRecord(owner *ssa.Function, logicalName string) (SSALoweredCall, bool) {
	if p == nil || owner == nil || logicalName == "" {
		return SSALoweredCall{}, false
	}
	calls := p.loweredCalls[owner]
	index := sort.Search(len(calls), func(index int) bool {
		return calls[index].LogicalName >= logicalName
	})
	if index == len(calls) || calls[index].LogicalName != logicalName {
		return SSALoweredCall{}, false
	}
	return calls[index], true
}

// ExactSafeFixedArrayIndex returns the frozen constant bound for an Index or
// IndexAddr whose SSA control flow proves the index is in range. The fact says
// nothing about a pointer-to-array base being non-nil.
func (p *SSAPlan) ExactSafeFixedArrayIndex(instruction ssa.Instruction) (bound int64, ok bool) {
	if p == nil || instruction == nil {
		return 0, false
	}
	bound, ok = p.safeFixedArrayIndexes[instruction]
	return bound, ok
}

// Function returns the SSA function assigned to id.
func (p *SSAPlan) Function(id FunctionID) (*ssa.Function, bool) {
	if p == nil {
		return nil, false
	}
	fn, ok := p.byID[id]
	return fn, ok
}

// AnalyzeSSA scans a built x/tools SSA program, constructs a conservative
// target-independent Graph, and computes its least fixed point. It emits and
// updates no build, LLVM, cache, archive, or runtime artifacts. x/tools function
// enumeration and opt-in CHA may materialize lazy wrapper objects in prog when
// EmissionUniverse is nil.
func AnalyzeSSA(prog *ssa.Program, roots Roots, config SSAConfig) (*SSAPlan, error) {
	if prog == nil {
		return nil, fmt.Errorf("coro: analyze nil SSA program")
	}
	universe := config.EmissionUniverse
	if universe != nil && universe.Program() != prog {
		return nil, fmt.Errorf("coro: SSA emission universe belongs to another program")
	}
	canonicalizer := newSSAFunctionCanonicalizer(prog, config)
	identityConfig, err := config.FunctionIDs.normalized()
	if err != nil {
		return nil, err
	}
	config.FunctionIDs = identityConfig
	if err := config.DynamicResolution.validate(); err != nil {
		return nil, err
	}
	if err := config.OutcomeMode.validate(); err != nil {
		return nil, err
	}
	if config.DynamicImplements != nil && universe == nil {
		return nil, fmt.Errorf("coro: dynamic implements resolver requires an SSA emission universe")
	}
	maxPlain := config.MaxPlainInstructions
	if maxPlain == 0 {
		maxPlain = DefaultMaxPlainInstructions
	}

	type rootEntryDemand struct {
		managed Demand
		raw     bool
	}
	rootDemand := make(map[*ssa.Function]rootEntryDemand, len(roots))
	for i, root := range roots {
		if root.Function == nil {
			return nil, fmt.Errorf("coro: root %d has nil SSA function", i)
		}
		if root.Function.Prog != prog {
			return nil, fmt.Errorf("coro: root %d function %q belongs to another SSA program", i, root.Function.Name())
		}
		canonical, ok, err := canonicalizer.resolve(root.Function)
		if err != nil {
			return nil, fmt.Errorf("coro: resolve root %d function %q: %w", i, root.Function.Name(), err)
		}
		if !ok {
			if universe != nil {
				return nil, fmt.Errorf("coro: root %d function %q is absent from the SSA emission universe", i, root.Function.Name())
			}
			return nil, fmt.Errorf("coro: root %d function %q has no canonical managed target", i, root.Function.Name())
		}
		if err := root.Demand.Validate(); err != nil {
			return nil, fmt.Errorf("coro: root %d function %q: %w", i, root.Function.Name(), err)
		}
		if err := root.ManagedDemand.Validate(); err != nil {
			return nil, fmt.Errorf("coro: root %d function %q managed demand: %w", i, root.Function.Name(), err)
		}
		managed := root.Demand.Join(root.ManagedDemand)
		if managed == NoDemand && !root.RawPlainDemand {
			return nil, fmt.Errorf("coro: root %d function %q has no demand", i, root.Function.Name())
		}
		joined := rootDemand[canonical]
		joined.managed = joined.managed.Join(managed)
		joined.raw = joined.raw || root.RawPlainDemand
		rootDemand[canonical] = joined
	}

	dynamicCandidates := make(map[ssa.CallInstruction]map[*ssa.Function]struct{})
	functionSet := make(map[*ssa.Function]struct{})
	var allFunctions []*ssa.Function
	if universe != nil {
		// Preserve the frozen frontend order. Round-tripping through a map
		// makes equal raw presentation keys nondeterministic (notably distinct
		// generic instances over local named types) before structural IDs are
		// available to order the included functions.
		allFunctions = append([]*ssa.Function(nil), universe.functions...)
		if config.DynamicResolution != DynamicUnknownOnly {
			implements := config.DynamicImplements
			if implements == nil {
				implements = func(candidate types.Type, iface *types.Interface) (bool, error) {
					return types.Implements(candidate, iface), nil
				}
			}
			dynamicCandidates, err = restrictedSSACHACandidatesWithDynamicImplements(universe.functions, implements)
			if err != nil {
				return nil, err
			}
		}
	} else if config.DynamicResolution == DynamicUnknownOnly {
		for fn := range ssautil.AllFunctions(prog) {
			if fn != nil && fn.Prog == prog {
				functionSet[fn] = struct{}{}
			}
		}
	} else {
		callGraph := cha.CallGraph(prog)
		for fn, node := range callGraph.Nodes {
			if fn == nil || fn.Prog != prog || node == nil {
				continue
			}
			functionSet[fn] = struct{}{}
			for _, edge := range node.Out {
				if edge.Site == nil || edge.Callee == nil || edge.Callee.Func == nil {
					continue
				}
				if edge.Site.Common().StaticCallee() != nil {
					continue
				}
				candidates := dynamicCandidates[edge.Site]
				if candidates == nil {
					candidates = make(map[*ssa.Function]struct{})
					dynamicCandidates[edge.Site] = candidates
				}
				candidates[edge.Callee.Func] = struct{}{}
				if edge.Callee.Func.Prog == prog {
					functionSet[edge.Callee.Func] = struct{}{}
				}
			}
		}
	}
	if universe == nil {
		for _, pkg := range prog.AllPackages() {
			for _, member := range pkg.Members {
				if fn, ok := member.(*ssa.Function); ok {
					functionSet[fn] = struct{}{}
				}
			}
		}
		for fn := range rootDemand {
			functionSet[fn] = struct{}{}
		}
		closeStaticFunctions(functionSet, prog)
	}

	if universe == nil {
		type keyedFunction struct {
			function *ssa.Function
			key      string
		}
		keyedFunctions := make([]keyedFunction, 0, len(functionSet))
		for fn := range functionSet {
			keyedFunctions = append(keyedFunctions, keyedFunction{
				function: fn,
				key:      rawSSAFunctionKey(fn),
			})
		}
		sort.Slice(keyedFunctions, func(i, j int) bool {
			return keyedFunctions[i].key < keyedFunctions[j].key
		})
		allFunctions = make([]*ssa.Function, len(keyedFunctions))
		for i, keyed := range keyedFunctions {
			allFunctions[i] = keyed.function
		}
	}

	canonicalFunctions := make([]*ssa.Function, 0, len(allFunctions))
	seenCanonical := make(map[*ssa.Function]struct{}, len(allFunctions))
	for _, fn := range allFunctions {
		canonical, ok, resolveErr := canonicalizer.resolve(fn)
		if resolveErr != nil {
			return nil, fmt.Errorf("coro: resolve enumerated SSA function %q: %w", fn.Name(), resolveErr)
		}
		if !ok {
			continue
		}
		if _, seen := seenCanonical[canonical]; seen {
			continue
		}
		seenCanonical[canonical] = struct{}{}
		canonicalFunctions = append(canonicalFunctions, canonical)
	}
	if universe == nil {
		canonicalFunctions, err = closeCanonicalStaticFunctions(canonicalFunctions, prog, canonicalizer)
		if err != nil {
			return nil, err
		}
	}
	allFunctions = canonicalFunctions

	included := make([]*ssa.Function, 0, len(allFunctions))
	includedSet := make(map[*ssa.Function]bool, len(allFunctions))
	for _, fn := range allFunctions {
		keep := !isUninstantiatedGeneric(fn)
		if config.Include != nil {
			requested, includeErr := config.Include(fn)
			err = includeErr
			if err != nil {
				return nil, fmt.Errorf("coro: include SSA function %q: %w", fn.Name(), err)
			}
			keep = keep && requested
		}
		if keep {
			included = append(included, fn)
			includedSet[fn] = true
		}
	}
	for fn := range rootDemand {
		if !includedSet[fn] {
			return nil, fmt.Errorf("coro: root function %q is excluded", fn.Name())
		}
	}

	// Freeze trusted per-function policy before inspecting any body. In
	// particular, a frontend-owned external declaration may retain an SSA stub
	// body even though lowering emits no Go instructions for it. Such a body is
	// outside the physical program and must be absent from every downstream
	// analysis, not merely have its scanner effects discarded afterwards.
	trustedPolicies := make(map[*ssa.Function]SSAFunctionPolicy, len(included))
	ignoredBodies := make(map[*ssa.Function]struct{})
	bodyFunctions := make([]*ssa.Function, 0, len(included))
	bodyFunctionSet := make(map[*ssa.Function]bool, len(included))
	for _, fn := range included {
		trusted := SSAFunctionPolicy{}
		if config.ClassifyFunction != nil {
			trusted, err = config.ClassifyFunction(fn)
			if err != nil {
				return nil, fmt.Errorf("coro: classify SSA function %q: %w", fn.Name(), err)
			}
		}
		if trusted.IgnoreBody {
			if !trusted.OverrideExternal || trusted.External == Defined {
				return nil, fmt.Errorf("coro: classify SSA function %q: IgnoreBody requires an explicit non-defined external classification", fn.Name())
			}
			ignoredBodies[fn] = struct{}{}
		} else {
			bodyFunctions = append(bodyFunctions, fn)
			bodyFunctionSet[fn] = true
		}
		if trusted.TrustedBoundedRecursion && (trusted.IgnoreBody || fn.Blocks == nil ||
			trusted.OverrideExternal && trusted.External != Defined) {
			return nil, fmt.Errorf("coro: classify SSA function %q: TrustedBoundedRecursion requires an owned defined body", fn.Name())
		}
		if trusted.RawPlainVariant || trusted.RawPlainEntry {
			if trusted.IgnoreBody || fn.Blocks == nil || trusted.OverrideExternal && trusted.External != Defined ||
				fn.Signature == nil {
				return nil, fmt.Errorf("coro: classify SSA function %q: RawPlainVariant requires an owned defined body", fn.Name())
			}
		}
		if trusted.RawPlainEntry && len(fn.FreeVars) != 0 {
			return nil, fmt.Errorf("coro: classify SSA function %q: RawPlainEntry requires a non-capturing body", fn.Name())
		}
		foreignCertificates := 0
		for _, certificate := range []string{
			trusted.ForeignNoBlockCertificate,
			trusted.ForeignSyncCertificate,
			trusted.ForeignSchedulerWaitCertificate,
			trusted.ForeignWorkerCertificate,
		} {
			if certificate != "" {
				foreignCertificates++
			}
		}
		if foreignCertificates > 1 {
			return nil, fmt.Errorf("coro: classify SSA function %q: foreign noblock, sync, schedulerwait, and worker certificates are mutually exclusive", fn.Name())
		}
		if !trusted.CallableIdentityCertificate.IsZero() {
			if err := trusted.CallableIdentityCertificate.Validate(); err != nil {
				return nil, fmt.Errorf("coro: classify SSA function %q: invalid callable identity certificate: %w", fn.Name(), err)
			}
			if !trusted.IgnoreBody || !trusted.OverrideExternal ||
				(trusted.External != ExternalKnown && trusted.External != ExternalUnknownForeign) ||
				trusted.Effect != NoSuspend || trusted.NeedsDispatch {
				return nil, fmt.Errorf("coro: classify SSA function %q: callable identity requires one ignored direct external C declaration", fn.Name())
			}
			if trusted.AssemblyNoSuspendCertificate != "" {
				return nil, fmt.Errorf("coro: classify SSA function %q: callable identity and assembly certificate are mutually exclusive", fn.Name())
			}
		}
		if !trusted.CallableContractCertificate.IsZero() {
			if err := trusted.CallableContractCertificate.Validate(); err != nil {
				return nil, fmt.Errorf("coro: classify SSA function %q: invalid callable contract certificate: %w", fn.Name(), err)
			}
			if foreignCertificates != 0 {
				return nil, fmt.Errorf("coro: classify SSA function %q: generic callable contract and legacy foreign-call certificates are mutually exclusive", fn.Name())
			}
			if err := validateSSACallableContractPolicy(fn, trusted); err != nil {
				return nil, fmt.Errorf("coro: classify SSA function %q: %w", fn.Name(), err)
			}
			if trusted.CallableContractCertificate.Scope == CallableContractScopeDeclaration &&
				!trusted.CallableIdentityCertificate.IsZero() {
				if err := ValidateCallableContractIdentity(trusted.CallableIdentityCertificate, trusted.CallableContractCertificate); err != nil {
					return nil, fmt.Errorf("coro: classify SSA function %q: %w", fn.Name(), err)
				}
			}
		}
		if certificate := trusted.ForeignNoBlockCertificate; certificate != "" {
			if !utf8.ValidString(certificate) {
				return nil, fmt.Errorf("coro: classify SSA function %q: foreign noblock certificate is not a valid UTF-8 identity", fn.Name())
			}
			if !trusted.IgnoreBody || !trusted.OverrideExternal || trusted.External != ExternalKnown ||
				trusted.Effect != NoSuspend || trusted.Exec != IRQUnsafe || trusted.NeedsDispatch {
				return nil, fmt.Errorf("coro: classify SSA function %q: foreign noblock certificate requires an ignored external-known declaration with no suspend effect, exactly irq-unsafe execution, and no dispatch", fn.Name())
			}
		}
		if certificate := trusted.ForeignSyncCertificate; certificate != "" {
			if !utf8.ValidString(certificate) {
				return nil, fmt.Errorf("coro: classify SSA function %q: foreign sync certificate is not a valid UTF-8 identity", fn.Name())
			}
			if !trusted.IgnoreBody || !trusted.OverrideExternal || trusted.External != ExternalKnown ||
				trusted.Effect != NoSuspend || trusted.Exec != IRQUnsafe || trusted.NeedsDispatch {
				return nil, fmt.Errorf("coro: classify SSA function %q: foreign sync certificate requires an ignored external-known declaration with no suspend effect, exactly irq-unsafe execution, and no dispatch", fn.Name())
			}
		}
		if certificate := trusted.ForeignSchedulerWaitCertificate; certificate != "" {
			if !utf8.ValidString(certificate) {
				return nil, fmt.Errorf("coro: classify SSA function %q: foreign schedulerwait certificate is not a valid UTF-8 identity", fn.Name())
			}
			if !trusted.IgnoreBody || !trusted.OverrideExternal || trusted.External != ExternalUnknownForeign ||
				trusted.Effect != NoSuspend || trusted.Exec != BlockForeign|IRQUnsafe || trusted.NeedsDispatch {
				return nil, fmt.Errorf("coro: classify SSA function %q: foreign schedulerwait certificate requires an ignored unknown-foreign declaration with no suspend effect, exactly block-foreign/irq-unsafe execution, and no dispatch", fn.Name())
			}
		}
		if certificate := trusted.ForeignWorkerCertificate; certificate != "" {
			if !utf8.ValidString(certificate) {
				return nil, fmt.Errorf("coro: classify SSA function %q: foreign worker certificate is not a valid UTF-8 identity", fn.Name())
			}
			if !trusted.IgnoreBody || !trusted.OverrideExternal || trusted.External != ExternalUnknownForeign ||
				trusted.Effect != NoSuspend || trusted.Exec != BlockForeign|IRQUnsafe || trusted.NeedsDispatch {
				return nil, fmt.Errorf("coro: classify SSA function %q: foreign worker certificate requires an ignored unknown-foreign declaration with no suspend effect, exactly block-foreign/irq-unsafe execution, and no dispatch", fn.Name())
			}
		}
		if certificate := trusted.AssemblyNoSuspendCertificate; certificate != "" {
			if !utf8.ValidString(certificate) {
				return nil, fmt.Errorf("coro: classify SSA function %q: assembly no-suspend certificate is not a valid UTF-8 identity", fn.Name())
			}
			if !trusted.CallableContractCertificate.IsZero() {
				return nil, fmt.Errorf("coro: classify SSA function %q: generic callable contract and assembly certificate are mutually exclusive", fn.Name())
			}
			if foreignCertificates != 0 {
				return nil, fmt.Errorf("coro: classify SSA function %q: assembly and foreign C-call certificates are mutually exclusive", fn.Name())
			}
			if !trusted.IgnoreBody || !trusted.OverrideExternal || trusted.External != ExternalKnown ||
				trusted.Effect != NoSuspend || trusted.Exec != IRQUnsafe || trusted.NeedsDispatch {
				return nil, fmt.Errorf("coro: classify SSA function %q: assembly no-suspend certificate requires an ignored external-known declaration with no suspend effect, exactly irq-unsafe execution, and no dispatch", fn.Name())
			}
		}
		trustedPolicies[fn] = trusted
	}
	dynamicCandidates, err = filterSSADynamicCandidateSites(dynamicCandidates, bodyFunctionSet, canonicalizer)
	if err != nil {
		return nil, err
	}
	dynamicCandidates, err = canonicalizeSSADynamicCandidates(dynamicCandidates, canonicalizer)
	if err != nil {
		return nil, err
	}

	ids := make(map[*ssa.Function]FunctionID, len(included))
	byID := make(map[FunctionID]*ssa.Function, len(included))
	idBuilder := functionIDBuilder{
		config:                 config.FunctionIDs,
		localTypeCandidates:    allFunctions,
		localTypeIgnoredBodies: ignoredBodies,
	}
	for _, fn := range included {
		id, err := idBuilder.stableFunctionID(fn)
		if err != nil {
			return nil, fmt.Errorf("coro: identify SSA function %q: %w", fn.Name(), err)
		}
		if previous, exists := byID[id]; exists && previous != fn {
			return nil, fmt.Errorf("coro: FunctionID collision between %q and %q", previous.Name(), fn.Name())
		}
		ids[fn] = id
		byID[id] = fn
	}
	sort.Slice(included, func(i, j int) bool { return ids[included[i]] < ids[included[j]] })
	canonicalRoots := make([]SSARootPlan, 0, len(rootDemand))
	for fn, demand := range rootDemand {
		id, ok := ids[fn]
		if !ok {
			return nil, fmt.Errorf("coro: canonical root function %q has no FunctionID", fn.Name())
		}
		canonicalRoots = append(canonicalRoots, SSARootPlan{
			Function: fn, ID: id,
			Demand: aggregateDemand(demand.managed, demand.raw), ManagedDemand: demand.managed, RawPlainDemand: demand.raw,
		})
	}
	sort.Slice(canonicalRoots, func(i, j int) bool { return canonicalRoots[i].ID < canonicalRoots[j].ID })

	directPlainCallArguments, err := classifySSADirectPlainCallArguments(bodyFunctions, config)
	if err != nil {
		return nil, err
	}
	rawDirectPlainCallArguments, err := classifySSARawDirectPlainCallArguments(bodyFunctions, config)
	if err != nil {
		return nil, err
	}
	directPlainKinds := make(map[ssaCallArgumentUse]struct{}, len(directPlainCallArguments)+len(rawDirectPlainCallArguments))
	allDirectPlainCallArguments := make([]ssaCallArgumentUse, 0, len(directPlainCallArguments)+len(rawDirectPlainCallArguments))
	for _, use := range directPlainCallArguments {
		directPlainKinds[use] = struct{}{}
		allDirectPlainCallArguments = append(allDirectPlainCallArguments, use)
	}
	for _, use := range rawDirectPlainCallArguments {
		if _, duplicate := directPlainKinds[use]; duplicate {
			return nil, fmt.Errorf("coro: call argument is classified as both managed and raw direct-plain invocation")
		}
		directPlainKinds[use] = struct{}{}
		allDirectPlainCallArguments = append(allDirectPlainCallArguments, use)
	}
	rawFunctionAddressCallArguments, err := classifySSARawFunctionAddressCallArguments(bodyFunctions, config)
	if err != nil {
		return nil, err
	}
	staticCodeAddressCallArguments, err := classifySSAStaticCodeAddressCallArguments(bodyFunctions, config)
	if err != nil {
		return nil, err
	}
	functionAddressCallArguments := append([]ssaCallArgumentUse(nil), rawFunctionAddressCallArguments...)
	addressKinds := make(map[ssaCallArgumentUse]struct{}, len(rawFunctionAddressCallArguments)+len(staticCodeAddressCallArguments))
	for _, use := range rawFunctionAddressCallArguments {
		addressKinds[use] = struct{}{}
	}
	for _, use := range staticCodeAddressCallArguments {
		if _, duplicate := addressKinds[use]; duplicate {
			return nil, fmt.Errorf("coro: call argument is classified as both a raw invocation address and a code-address observation")
		}
		addressKinds[use] = struct{}{}
		functionAddressCallArguments = append(functionAddressCallArguments, use)
	}
	dynamicCandidates, err = filterSSAStaticCodeAddressOnlyCandidates(
		dynamicCandidates, allFunctions, staticCodeAddressCallArguments, canonicalizer,
	)
	if err != nil {
		return nil, err
	}
	closedDynamicCalls, err := classifySSAClosedDynamicCalls(bodyFunctions, includedSet, bodyFunctionSet, trustedPolicies, canonicalizer, config)
	if err != nil {
		return nil, err
	}
	flow, err := analyzeSSAFunctionFlow(
		bodyFunctions, includedSet, ids, dynamicCandidates, config.DynamicResolution, canonicalizer,
		allDirectPlainCallArguments, functionAddressCallArguments, closedDynamicCalls, config.ClassifyRawCFunctionType,
	)
	if err != nil {
		return nil, fmt.Errorf("coro: analyze SSA function-value flow: %w", err)
	}
	if err := flow.validateDirectPlainCallArguments(); err != nil {
		return nil, fmt.Errorf("coro: validate trusted direct-plain call arguments: %w", err)
	}
	if err := flow.validateRawFunctionAddressCallArguments(rawFunctionAddressCallArguments); err != nil {
		return nil, fmt.Errorf("coro: validate trusted raw function-address call arguments: %w", err)
	}
	rawFunctionAddressEntries := make(map[*ssa.Function]struct{}, len(rawFunctionAddressCallArguments))
	for _, use := range rawFunctionAddressCallArguments {
		boxed, ok := use.call.Common().Args[use.argument].(*ssa.MakeInterface)
		if !ok {
			return nil, fmt.Errorf("coro: validated raw function-address argument lost its MakeInterface shape")
		}
		target, ok := boxed.X.(*ssa.Function)
		if !ok {
			return nil, fmt.Errorf("coro: validated raw function-address argument lost its static function target")
		}
		canonical, resolved, resolveErr := canonicalizer.resolve(target)
		if resolveErr != nil {
			return nil, fmt.Errorf("coro: resolve raw function-address entry %q: %w", target.Name(), resolveErr)
		}
		if !resolved || canonical == nil || !includedSet[canonical] || canonical.Signature == nil ||
			len(canonical.FreeVars) != 0 || canonical.Blocks == nil {
			return nil, fmt.Errorf("coro: raw function-address entry %q is not one owned non-capturing Go body", target.Name())
		}
		rawFunctionAddressEntries[canonical] = struct{}{}
	}
	conditionalStores, err := classifySSAConditionalManagedStoreReferences(
		bodyFunctions, includedSet, canonicalizer, flow, config,
	)
	if err != nil {
		return nil, err
	}
	syncOnlyDescriptorArguments, err := flow.validateSyncOnlyDescriptorCallArguments(closedDynamicCalls)
	if err != nil {
		return nil, fmt.Errorf("coro: validate synchronous descriptor publications: %w", err)
	}
	elidedCalls, err := classifySSAElidedCalls(bodyFunctions, config)
	if err != nil {
		return nil, err
	}
	elidedCallCertificates, err := classifySSAElidedCallCertificates(elidedCalls, config)
	if err != nil {
		return nil, err
	}
	safeFixedArrayIndexes := analyzeSSAExactSafeFixedArrayIndexes(bodyFunctions)
	unknownTargets, err := classifySSAUnknownCalls(bodyFunctions, includedSet, flow, elidedCalls, config)
	if err != nil {
		return nil, err
	}
	needsDispatch := flow.descriptorTargets(unknownTargets)
	buildPolicies := func(exactNoUnwind map[*ssa.Function]bool) (map[*ssa.Function]SSAFunctionPolicy, error) {
		policies := make(map[*ssa.Function]SSAFunctionPolicy, len(included))
		for _, fn := range included {
			policy := SSAFunctionPolicy{}
			if fn.Blocks == nil {
				policy.External = ExternalUnknownManaged
				policy.OverrideExternal = true
			}
			trusted := trustedPolicies[fn]
			if _, ignored := ignoredBodies[fn]; !ignored {
				bodyFacts := SSAFunctionBodyFacts{}
				if config.ClassifyLocalBody != nil {
					var bodyErr error
					bodyFacts, bodyErr = config.ClassifyLocalBody(fn)
					if bodyErr != nil {
						return nil, fmt.Errorf("coro: classify local SSA body %q: %w", fn.Name(), bodyErr)
					}
				} else {
					bodyFacts = scanSSAFunctionBody(fn)
				}
				if err := bodyFacts.validate(); err != nil {
					return nil, fmt.Errorf("coro: local SSA body %q: %w", fn.Name(), err)
				}
				bodyEffect, bodyExec := bodyFacts.Effect, bodyFacts.Exec
				if bodyFacts.HasCycle || maxPlain >= 0 && bodyFacts.InstructionCount > maxPlain {
					bodyExec = bodyExec.Join(NeedsPreempt)
				}
				if exactNoUnwind[fn] {
					bodyExec &^= MayUnwind
				}
				policy.Effect = policy.Effect.Join(bodyEffect)
				policy.Exec = policy.Exec.Join(bodyExec)
				if trusted.TrustedNoPreempt {
					policy.Exec &^= NeedsPreempt
				}
			}
			policy.Effect = policy.Effect.Join(trusted.Effect)
			// TrustedNoPreempt suppresses only the scanner's local budget/CFG
			// seed above. An explicit trusted NeedsPreempt declaration remains
			// authoritative and is joined only after that suppression.
			policy.Exec = policy.Exec.Join(trusted.Exec)
			policy.NeedsDispatch = policy.NeedsDispatch || trusted.NeedsDispatch
			policy.TrustedBoundedRecursion = trusted.TrustedBoundedRecursion
			_, rawFunctionAddressEntry := rawFunctionAddressEntries[fn]
			policy.RawPlainEntry = trusted.RawPlainEntry || rawFunctionAddressEntry
			policy.RawPlainVariant = trusted.RawPlainVariant || policy.RawPlainEntry
			policy.CallableIdentityCertificate = trusted.CallableIdentityCertificate
			policy.CallableContractCertificate = trusted.CallableContractCertificate
			policy.ForeignNoBlockCertificate = trusted.ForeignNoBlockCertificate
			policy.ForeignSyncCertificate = trusted.ForeignSyncCertificate
			policy.ForeignSchedulerWaitCertificate = trusted.ForeignSchedulerWaitCertificate
			policy.ForeignWorkerCertificate = trusted.ForeignWorkerCertificate
			policy.AssemblyNoSuspendCertificate = trusted.AssemblyNoSuspendCertificate
			if trusted.OverrideExternal {
				policy.External = trusted.External
				policy.OverrideExternal = true
			}
			policy.NeedsDispatch = policy.NeedsDispatch || needsDispatch[fn]
			if !policy.OverrideExternal {
				policy.External = Defined
			}
			if fn.Blocks == nil && policy.External == Defined {
				return nil, fmt.Errorf("coro: bodyless SSA function %q classified as defined", fn.Name())
			}
			policies[fn] = policy
		}
		return policies, nil
	}
	preliminaryPolicies, err := buildPolicies(nil)
	if err != nil {
		return nil, err
	}
	trustedInlineCalls, err := classifySSATrustedInlineCalls(
		bodyFunctions, includedSet, ignoredBodies, preliminaryPolicies, elidedCalls, canonicalizer, config,
	)
	if err != nil {
		return nil, err
	}
	trustedInlineNoUnwind, err := classifySSATrustedInlineNoUnwindCalls(
		trustedInlineCalls, preliminaryPolicies, canonicalizer,
	)
	if err != nil {
		return nil, err
	}
	exactNoUnwind, err := proveSSAExactNoUnwind(
		bodyFunctions, trustedPolicies, closedDynamicCalls, trustedInlineNoUnwind, elidedCalls, safeFixedArrayIndexes, canonicalizer,
	)
	if err != nil {
		return nil, err
	}
	policies, err := buildPolicies(exactNoUnwind)
	if err != nil {
		return nil, err
	}

	graph := NewGraph()
	for _, fn := range included {
		policy := policies[fn]
		root := rootDemand[fn]
		if err := graph.AddFunction(FunctionSpec{
			ID:                      ids[fn],
			Seed:                    policy.Effect,
			Exec:                    policy.Exec,
			ManagedDemand:           root.managed,
			RawPlainDemand:          root.raw,
			External:                policy.External,
			TrustedBoundedRecursion: policy.TrustedBoundedRecursion,
			NeedsDispatch:           policy.NeedsDispatch,
			RawPlainEntry:           policy.RawPlainEntry,
		}); err != nil {
			return nil, err
		}
	}

	callKinds := make(map[ssa.CallInstruction]CallKind)
	for _, caller := range bodyFunctions {
		for _, block := range caller.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok {
					continue
				}
				if elidedCalls[call] {
					continue
				}
				common := call.Common()
				if _, builtin := common.Value.(*ssa.Builtin); builtin {
					continue
				}
				kind := ssaCallKind(call)
				if rawCallee := common.StaticCallee(); rawCallee != nil {
					callee, resolved, resolveErr := flow.resolveTarget(rawCallee)
					if resolveErr != nil {
						return nil, fmt.Errorf("coro: resolve static callee %q in %q while building graph: %w", rawCallee.Name(), caller.Name(), resolveErr)
					}
					if resolved && includedSet[callee] {
						edgeKind := staticCallKind(kind, policies[callee])
						edge := CallEdge{Caller: ids[caller], Callee: ids[callee], Kind: edgeKind}
						if _, trustedInline := trustedInlineCalls[call]; trustedInline {
							if edgeKind != CallForeign {
								return nil, fmt.Errorf("coro: trusted-inline call in %q did not resolve to one conservative foreign edge", caller.Name())
							}
							edgeKind = CallTrustedInline
							defaultExec, selectedExec, projectionErr := trustedInlineContractExecProjections(
								policies[callee].CallableContractCertificate,
							)
							if projectionErr != nil {
								return nil, fmt.Errorf("coro: derive trusted-inline execution projections in %q: %w", caller.Name(), projectionErr)
							}
							edge.Kind = edgeKind
							edge.DefaultContractExec = defaultExec
							edge.SelectedContractExec = selectedExec
						}
						callKinds[call] = edgeKind
						if err := graph.AddCall(edge); err != nil {
							return nil, err
						}
					} else {
						target := unknownTargets[call]
						callKinds[call] = unknownCallKind(kind, target)
						if err := addSSAUnknownCall(graph, ids[caller], kind, target); err != nil {
							return nil, err
						}
					}
					continue
				}

				if flow.rawCValue(common.Value) {
					target := unknownTargets[call]
					if target != UnknownForeign || common.IsInvoke() || common.Method != nil {
						return nil, fmt.Errorf("coro: raw C code-pointer call in %q lacks its exact unknown-foreign domain", caller.Name())
					}
					callKinds[call] = unknownCallKind(kind, target)
					if err := addSSAUnknownCall(graph, ids[caller], kind, target); err != nil {
						return nil, err
					}
					continue
				}

				flowTargets, flowComplete := flow.scalarCallTargets(call)
				if flowComplete {
					candidates := sortedSSACandidates(flowTargets, ids, includedSet)
					callKinds[call] = kind
					if certificate, certified := closedDynamicCalls[call]; certified && certificate.SyncDispatch {
						// The exact frontend/build proof selects the descriptor's
						// synchronous plain entry. Retain target liveness/demand, but do
						// not reinterpret the call as a managed child or propagate the
						// target effect into this required-synchronous owner.
						if err := addSSASyncDispatchTargetDemand(graph, ids[caller], candidates, ids); err != nil {
							return nil, err
						}
						continue
					}
					targetDomain := unknownTargets[call]
					if targetDomain.managedDispatch() {
						// The selected descriptor target runs as a managed child. Its
						// exact body still needs entry demand, while the parent inherits
						// only this structured boundary. Spawn uses a real CallSpawn edge
						// so even a bounded target receives AsyncDemand; direct/defer use
						// demand-only references to avoid leaking the selected target's
						// effect through the descriptor isolation boundary.
						if err := addSSAManagedDescriptorTargetDemand(graph, ids[caller], candidates, ids, kind); err != nil {
							return nil, err
						}
						if err := addSSAUnknownCall(graph, ids[caller], kind, targetDomain); err != nil {
							return nil, err
						}
						continue
					}
					for _, callee := range candidates {
						edgeKind := staticCallKind(kind, policies[callee])
						if len(candidates) == 1 {
							callKinds[call] = edgeKind
						}
						if err := graph.AddCall(CallEdge{Caller: ids[caller], Callee: ids[callee], Kind: edgeKind}); err != nil {
							return nil, err
						}
					}
					continue
				}
				target := unknownTargets[call]
				if target.managedDispatch() {
					callKinds[call] = kind
					managedTargets := make(map[*ssa.Function]struct{}, len(flowTargets)+len(dynamicCandidates[call]))
					for candidate := range flowTargets {
						managedTargets[candidate] = struct{}{}
					}
					for candidate := range dynamicCandidates[call] {
						managedTargets[candidate] = struct{}{}
					}
					if err := addSSAManagedDescriptorTargetDemand(
						graph, ids[caller], sortedSSACandidates(managedTargets, ids, includedSet), ids, kind,
					); err != nil {
						return nil, err
					}
					if err := addSSAUnknownCall(graph, ids[caller], kind, target); err != nil {
						return nil, err
					}
					continue
				}
				// Structural flow may know a strict subset even when another source
				// remains unresolved. Preserve those real Go edges regardless of the
				// fallback execution domain selected below.
				for _, callee := range sortedSSACandidates(flowTargets, ids, includedSet) {
					edgeKind := staticCallKind(kind, policies[callee])
					if err := graph.AddCall(CallEdge{Caller: ids[caller], Callee: ids[callee], Kind: edgeKind}); err != nil {
						return nil, err
					}
				}

				// An explicitly classified foreign function value has a different
				// invocation domain from CHA's managed Go candidates. Preserve the
				// foreign boundary in every resolution mode.
				if target == UnknownForeign {
					callKinds[call] = kind
					// A mixed dispatch keeps the syntax kind for known managed
					// descriptors; Unresolved selects the foreign fallback only.
					if len(flowTargets) == 0 {
						callKinds[call] = unknownCallKind(kind, target)
					}
					if err := addSSAUnknownCall(graph, ids[caller], kind, target); err != nil {
						return nil, err
					}
					continue
				}
				callKinds[call] = kind

				rawCandidates := dynamicCandidates[call]
				candidates := sortedSSACandidates(rawCandidates, ids, includedSet)
				for _, callee := range candidates {
					edgeKind := staticCallKind(kind, policies[callee])
					if err := graph.AddCall(CallEdge{Caller: ids[caller], Callee: ids[callee], Kind: edgeKind}); err != nil {
						return nil, err
					}
				}
				closedWorldResolved := config.DynamicResolution == DynamicCHAClosed && len(rawCandidates) != 0
				if closedWorldResolved {
					for candidate := range rawCandidates {
						if !includedSet[candidate] {
							closedWorldResolved = false
							break
						}
					}
				}
				if !closedWorldResolved {
					if err := addSSAUnknownCall(graph, ids[caller], kind, target); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	loweredCalls, err := addSSAClassifiedLoweredCalls(graph, bodyFunctions, includedSet, ids, canonicalizer, policies, config)
	if err != nil {
		return nil, err
	}
	if err := addSSAReferenceEdges(
		graph, bodyFunctions, includedSet, ids, flow,
		directPlainCallArguments, rawDirectPlainCallArguments,
		rawFunctionAddressCallArguments, staticCodeAddressCallArguments, syncOnlyDescriptorArguments,
		conditionalStores,
	); err != nil {
		return nil, err
	}
	if err := addSSAClassifiedDemandReferences(graph, bodyFunctions, includedSet, ids, canonicalizer, config); err != nil {
		return nil, err
	}
	if err := addSSAClassifiedRawPlainReferences(graph, bodyFunctions, includedSet, ids, canonicalizer, config); err != nil {
		return nil, err
	}

	base, err := graph.AnalyzeWithConfig(GraphAnalysisConfig{OutcomeMode: config.OutcomeMode})
	if err != nil {
		return nil, err
	}
	if err := validateSSACallableContractPlans(base, policies, ids); err != nil {
		return nil, err
	}
	valuePlans, callPlans, err := flow.finalize(base, callKinds, unknownTargets)
	if err != nil {
		return nil, fmt.Errorf("coro: finalize SSA value and call plans: %w", err)
	}
	if err := validateSSASyncDispatchCallPlans(base, callPlans, ids); err != nil {
		return nil, err
	}
	if err := applySSATrustedInlineCallPlans(callPlans, trustedInlineCalls); err != nil {
		return nil, err
	}
	elidedCallSet := make(map[ssa.CallInstruction]struct{}, len(elidedCalls))
	for call, elided := range elidedCalls {
		if elided {
			elidedCallSet[call] = struct{}{}
		}
	}
	result := &SSAPlan{
		plan:                   base,
		roots:                  canonicalRoots,
		functions:              make([]SSAFunctionPlan, 0, len(included)),
		byFunction:             ids,
		byID:                   byID,
		ignoredBodies:          ignoredBodies,
		rawPlainVariants:       make(map[*ssa.Function]struct{}),
		valuePlans:             valuePlans,
		callPlans:              callPlans,
		elidedCalls:            elidedCallSet,
		elidedCallCertificates: elidedCallCertificates,
		rawAddressArgs:         make(map[ssaCallArgumentUse]struct{}, len(rawFunctionAddressCallArguments)),
		codeAddressArgs:        make(map[ssaCallArgumentUse]struct{}, len(staticCodeAddressCallArguments)),
		conditionalStores:      make(map[*ssa.Store]*ssa.Function, len(conditionalStores)),
		safeFixedArrayIndexes:  safeFixedArrayIndexes,
		loweredCalls:           loweredCalls,
		foreignNoBlock:         make(map[*ssa.Function]string),
		foreignSync:            make(map[*ssa.Function]string),
		foreignSchedulerWait:   make(map[*ssa.Function]string),
		foreignWorker:          make(map[*ssa.Function]string),
		callableIdentities:     make(map[*ssa.Function]CallableIdentityCertificate),
		callableContracts:      make(map[*ssa.Function]CallableContractCertificate),
		assemblyNoSuspend:      make(map[*ssa.Function]string),
		functionIDs:            config.FunctionIDs,
	}
	for fn, policy := range policies {
		functionPlan, planned := base.Lookup(ids[fn])
		if policy.RawPlainVariant || planned && functionPlan.RawPlainDemand && functionPlan.External == Defined {
			result.rawPlainVariants[fn] = struct{}{}
		}
		if policy.ForeignNoBlockCertificate != "" {
			result.foreignNoBlock[fn] = policy.ForeignNoBlockCertificate
		}
		if policy.ForeignSyncCertificate != "" {
			result.foreignSync[fn] = policy.ForeignSyncCertificate
		}
		if policy.ForeignSchedulerWaitCertificate != "" {
			result.foreignSchedulerWait[fn] = policy.ForeignSchedulerWaitCertificate
		}
		if policy.ForeignWorkerCertificate != "" {
			result.foreignWorker[fn] = policy.ForeignWorkerCertificate
		}
		if !policy.CallableIdentityCertificate.IsZero() {
			result.callableIdentities[fn] = policy.CallableIdentityCertificate
		}
		if !policy.CallableContractCertificate.IsZero() {
			result.callableContracts[fn] = policy.CallableContractCertificate
		}
		if policy.AssemblyNoSuspendCertificate != "" {
			result.assemblyNoSuspend[fn] = policy.AssemblyNoSuspendCertificate
		}
	}
	for _, use := range rawFunctionAddressCallArguments {
		result.rawAddressArgs[use] = struct{}{}
	}
	for _, use := range staticCodeAddressCallArguments {
		result.codeAddressArgs[use] = struct{}{}
	}
	for store, target := range conditionalStores {
		result.conditionalStores[store] = target
	}
	for _, functionPlan := range base.Functions() {
		result.functions = append(result.functions, SSAFunctionPlan{
			Function: byID[functionPlan.ID],
			Plan:     functionPlan,
		})
	}
	return result, nil
}

// validateSSACallableContractPlans prevents wrapper metadata from becoming a
// second, contradictory truth beside the inferred Go body.  A wrapper
// contract may add conservative execution constraints, but it may never erase
// suspension, unbounded preemption, affinity, or an unknown physical adapter
// discovered by the fixed point.
func validateSSACallableContractPlans(
	base *Plan,
	policies map[*ssa.Function]SSAFunctionPolicy,
	ids map[*ssa.Function]FunctionID,
) error {
	if base == nil {
		return fmt.Errorf("coro: validate callable contracts against nil plan")
	}
	for fn, policy := range policies {
		certificate := policy.CallableContractCertificate
		if certificate.IsZero() || certificate.Scope != CallableContractScopeWrapper {
			continue
		}
		id, ok := ids[fn]
		plan, planned := base.Lookup(id)
		if !ok || !planned || plan.External != Defined {
			return fmt.Errorf("coro: callable wrapper %q has no exact defined function plan", fn.Name())
		}
		required := CallableContractExecConstraints(certificate.Contract)
		if certificate.Contract.Progress == ProgressNoReturn {
			required |= NoReturn
		}
		if !plan.Exec.Contains(required) {
			return fmt.Errorf("coro: callable wrapper %q lost required execution constraints %s", fn.Name(), required)
		}
		if (certificate.Contract.Affinity == AffinityAnyThread || certificate.Contract.Affinity == AffinityCallerThread) &&
			plan.Exec.Contains(ThreadAffine) {
			return fmt.Errorf(
				"coro: callable wrapper %q claims affinity %q but its analyzed plan is thread-affine",
				fn.Name(), certificate.Contract.Affinity,
			)
		}
		if certificate.Contract.Reentry == ReentryNone &&
			certificate.Contract.Memory != MemoryUnknown && certificate.Contract.Memory != MemoryRetained &&
			plan.Exec.Contains(OpaqueExec) {
			return fmt.Errorf(
				"coro: callable wrapper %q claims closed reentry/memory behavior but its analyzed execution constraints are opaque",
				fn.Name(),
			)
		}
		switch certificate.Contract.Progress {
		case ProgressExecutorSafe:
			if plan.Effect != NoSuspend || plan.Exec.Contains(BlockForeign|NeedsPreempt|OpaqueExec) {
				return fmt.Errorf(
					"coro: callable wrapper %q claims executor-safe but its analyzed plan has effect=%s exec=%s (declared effect=%s exec=%s; local effect=%s exec=%s)",
					fn.Name(), plan.Effect, plan.Exec,
					plan.DeclaredEffect, plan.DeclaredExec, plan.LocalEffect, plan.LocalExec,
				)
			}
		case ProgressNoReturn:
			if !plan.Exec.Contains(NoReturn) {
				return fmt.Errorf("coro: callable wrapper %q claims no-return without a no-return plan", fn.Name())
			}
		case ProgressMayBlock, ProgressUnknown, ProgressAsyncCompletion:
			// These are conservative or require a later operation recipe.  Body
			// analysis remains authoritative and no effect is removed here.
		default:
			return fmt.Errorf("coro: callable wrapper %q has invalid progress %q", fn.Name(), certificate.Contract.Progress)
		}
	}
	return nil
}

func addSSAManagedDescriptorTargetDemand(
	graph *Graph,
	owner FunctionID,
	targets []*ssa.Function,
	ids map[*ssa.Function]FunctionID,
	kind CallKind,
) error {
	for _, target := range targets {
		targetID, ok := ids[target]
		if !ok {
			return fmt.Errorf("coro: managed descriptor target %q has no function identity", target.Name())
		}
		if kind == CallSpawn {
			if err := graph.AddCall(CallEdge{Caller: owner, Callee: targetID, Kind: CallSpawn}); err != nil {
				return err
			}
			continue
		}
		if err := graph.AddReference(ReferenceEdge{Owner: owner, Target: targetID}); err != nil {
			return err
		}
	}
	return nil
}

func addSSASyncDispatchTargetDemand(
	graph *Graph,
	owner FunctionID,
	targets []*ssa.Function,
	ids map[*ssa.Function]FunctionID,
) error {
	for _, target := range targets {
		targetID, ok := ids[target]
		if !ok {
			return fmt.Errorf("coro: synchronous descriptor target %q has no function identity", target.Name())
		}
		if err := graph.AddReference(ReferenceEdge{Owner: owner, Target: targetID, SyncOnly: true}); err != nil {
			return err
		}
	}
	return nil
}

func validateSSASyncDispatchCallPlans(
	base *Plan,
	calls map[ssa.CallInstruction]SSACallPlan,
	ids map[*ssa.Function]FunctionID,
) error {
	for call, callPlan := range calls {
		if !callPlan.SyncDispatch {
			continue
		}
		owner := "<unknown>"
		if call != nil && call.Parent() != nil {
			owner = call.Parent().Name()
		}
		if call == nil || call.Common() == nil || callPlan.Kind != CallDirect || callPlan.Rep != Dispatch ||
			callPlan.Open || len(callPlan.Targets) > 1 {
			return fmt.Errorf("coro: synchronous descriptor call in %q has an invalid closed CallPlan", owner)
		}
		ownerID, ok := ids[call.Parent()]
		ownerPlan, planned := base.Lookup(ownerID)
		plainOwner := ok && planned && ownerPlan.External == Defined && ownerPlan.Effect == NoSuspend &&
			!ownerPlan.Exec.Contains(NeedsPreempt) && ownerPlan.Primary == PrimaryPlain && ownerPlan.Emission == EmitPlain
		rawOwner := ok && planned && ownerPlan.External == Defined && ownerPlan.RawPlainDemand && (ownerPlan.Emission == EmitRawPlain && ownerPlan.RawPlainOnly && ownerPlan.ManagedDemand == NoDemand &&
			ownerPlan.Primary == PrimaryPlain && ownerPlan.FuncRep == DirectPlain ||
			ownerPlan.Emission == EmitCoroutine && !ownerPlan.RawPlainOnly && ownerPlan.ManagedDemand != NoDemand &&
				ownerPlan.Primary == PrimaryCoroutine && ownerPlan.Effect.MaySuspend())
		if !plainOwner && !rawOwner {
			return fmt.Errorf(
				"coro: synchronous descriptor call owner %q is not one defined non-suspending plain primary or exact raw-variant owner (external=%s effect=%s exec=%s managed=%s raw=%t raw-only=%t representation=%s primary=%s emission=%s)",
				owner, ownerPlan.External, ownerPlan.Effect, ownerPlan.Exec, ownerPlan.ManagedDemand,
				ownerPlan.RawPlainDemand, ownerPlan.RawPlainOnly, ownerPlan.FuncRep, ownerPlan.Primary, ownerPlan.Emission,
			)
		}
		for _, targetID := range callPlan.Targets {
			target, ok := base.Lookup(targetID)
			if !ok {
				return fmt.Errorf("coro: synchronous descriptor call in %q targets absent function %q", owner, targetID)
			}
			if target.External != Defined || target.Effect != NoSuspend || target.Exec.Contains(NeedsPreempt) ||
				target.Primary != PrimaryPlain || target.Emission != EmitPlain || target.FuncRep != Dispatch {
				return fmt.Errorf(
					"coro: synchronous descriptor call in %q target %q is not one defined non-suspending descriptor-backed plain primary (external=%s effect=%s exec=%s representation=%s primary=%s emission=%s)",
					owner, targetID, target.External, target.Effect, target.Exec, target.FuncRep, target.Primary, target.Emission,
				)
			}
		}
	}
	return nil
}

func addSSAClassifiedLoweredCalls(
	graph *Graph,
	functions []*ssa.Function,
	included map[*ssa.Function]bool,
	ids map[*ssa.Function]FunctionID,
	canonicalizer *ssaFunctionCanonicalizer,
	policies map[*ssa.Function]SSAFunctionPolicy,
	config SSAConfig,
) (map[*ssa.Function][]SSALoweredCall, error) {
	result := make(map[*ssa.Function][]SSALoweredCall)
	if config.ClassifyLoweredCalls == nil {
		return result, nil
	}
	for _, owner := range functions {
		calls, err := config.ClassifyLoweredCalls(owner)
		if err != nil {
			return nil, fmt.Errorf("coro: classify lowered calls in %q: %w", owner.Name(), err)
		}
		// The classifier owns its backing storage. Copy before validating or
		// retaining any record in the immutable plan.
		calls = append([]SSALoweredCall(nil), calls...)
		seen := make(map[string]struct{}, len(calls))
		for index := range calls {
			call := &calls[index]
			if call.LogicalName == "" {
				return nil, fmt.Errorf("coro: lowered call %d in %q has an empty logical name", index, owner.Name())
			}
			if !utf8.ValidString(call.LogicalName) || strings.IndexByte(call.LogicalName, 0) >= 0 {
				return nil, fmt.Errorf("coro: lowered call %d in %q has an invalid logical name %q", index, owner.Name(), call.LogicalName)
			}
			if _, duplicate := seen[call.LogicalName]; duplicate {
				return nil, fmt.Errorf("coro: lowered call logical name %q is duplicated in %q", call.LogicalName, owner.Name())
			}
			seen[call.LogicalName] = struct{}{}
			if call.RawPlain && call.UnwindOnly {
				return nil, fmt.Errorf("coro: lowered call %q in %q is both raw-plain and unwind-only", call.LogicalName, owner.Name())
			}
			if call.RawPlain && call.ExplicitStatusElided {
				return nil, fmt.Errorf("coro: lowered call %q in %q is both raw-plain and ExplicitStatus-elided", call.LogicalName, owner.Name())
			}
			if call.ExplicitStatusElided && !call.UnwindOnly {
				return nil, fmt.Errorf("coro: lowered call %q in %q is ExplicitStatus-elided but not unwind-only", call.LogicalName, owner.Name())
			}
			target := call.Target
			if target == nil {
				return nil, fmt.Errorf("coro: lowered call %q in %q has a nil target", call.LogicalName, owner.Name())
			}
			if target.Prog != owner.Prog {
				return nil, fmt.Errorf("coro: lowered call %q in %q targets function %q from another SSA program", call.LogicalName, owner.Name(), target.Name())
			}
			canonical, resolved, resolveErr := canonicalizer.resolve(target)
			if resolveErr != nil {
				return nil, fmt.Errorf("coro: resolve lowered call %q target %q in %q: %w", call.LogicalName, target.Name(), owner.Name(), resolveErr)
			}
			if !resolved || canonical == nil || !included[canonical] {
				return nil, fmt.Errorf("coro: lowered call %q target %q in %q is outside the effective emission universe", call.LogicalName, target.Name(), owner.Name())
			}
			if canonical != target {
				return nil, fmt.Errorf("coro: lowered call %q target %q in %q is not the exact canonical function", call.LogicalName, target.Name(), owner.Name())
			}
		}
		sort.Slice(calls, func(i, j int) bool {
			return calls[i].LogicalName < calls[j].LogicalName
		})
		if len(calls) != 0 {
			result[owner] = calls
		}
		for _, call := range calls {
			if call.RawPlain {
				if err := graph.AddReference(ReferenceEdge{Owner: ids[owner], Target: ids[call.Target], RawPlain: true}); err != nil {
					return nil, fmt.Errorf("coro: add raw-plain lowered call %q from %q to %q: %w", call.LogicalName, owner.Name(), call.Target.Name(), err)
				}
				continue
			}
			kind := CallUnwind
			if call.ExplicitStatusElided {
				kind = CallExplicitStatusElided
			} else if !call.UnwindOnly {
				kind = staticCallKind(CallDirect, policies[call.Target])
			}
			if err := graph.AddCall(CallEdge{Caller: ids[owner], Callee: ids[call.Target], Kind: kind}); err != nil {
				return nil, fmt.Errorf("coro: add lowered call %q from %q to %q: %w", call.LogicalName, owner.Name(), call.Target.Name(), err)
			}
		}
	}
	return result, nil
}

func addSSAClassifiedDemandReferences(
	graph *Graph,
	functions []*ssa.Function,
	included map[*ssa.Function]bool,
	ids map[*ssa.Function]FunctionID,
	canonicalizer *ssaFunctionCanonicalizer,
	config SSAConfig,
) error {
	if config.ClassifyDemandReferences == nil {
		if config.ClassifySyncDemandReferences != nil {
			return fmt.Errorf("coro: synchronous demand references require an ordinary demand-reference classifier")
		}
		return nil
	}
	for _, owner := range functions {
		targets, err := config.ClassifyDemandReferences(owner)
		if err != nil {
			return fmt.Errorf("coro: classify demand-only references in %q: %w", owner.Name(), err)
		}
		// The classifier owns its backing storage. Copy before validation so
		// analysis never retains a frontend-owned slice.
		targets = append([]*ssa.Function(nil), targets...)
		var syncTargets []*ssa.Function
		if config.ClassifySyncDemandReferences != nil {
			syncTargets, err = config.ClassifySyncDemandReferences(owner)
			if err != nil {
				return fmt.Errorf("coro: classify synchronous demand-only references in %q: %w", owner.Name(), err)
			}
			syncTargets = append([]*ssa.Function(nil), syncTargets...)
		}
		ordinary := make(map[*ssa.Function]struct{}, len(targets))
		for index, target := range targets {
			if target == nil {
				return fmt.Errorf("coro: demand-only reference %d in %q has a nil target", index, owner.Name())
			}
			if target.Prog != owner.Prog {
				return fmt.Errorf("coro: demand-only reference %d in %q targets function %q from another SSA program", index, owner.Name(), target.Name())
			}
			canonical, resolved, resolveErr := canonicalizer.resolve(target)
			if resolveErr != nil {
				return fmt.Errorf("coro: resolve demand-only target %q in %q: %w", target.Name(), owner.Name(), resolveErr)
			}
			if !resolved || canonical == nil || !included[canonical] {
				return fmt.Errorf("coro: demand-only target %q in %q is outside the effective emission universe", target.Name(), owner.Name())
			}
			if canonical != target {
				return fmt.Errorf("coro: demand-only target %q in %q is not the exact canonical function", target.Name(), owner.Name())
			}
			ordinary[target] = struct{}{}
		}
		synchronous := make(map[*ssa.Function]struct{}, len(syncTargets))
		for index, target := range syncTargets {
			if target == nil {
				return fmt.Errorf("coro: synchronous demand-only reference %d in %q has a nil target", index, owner.Name())
			}
			if _, present := ordinary[target]; !present {
				return fmt.Errorf("coro: synchronous demand-only target %q in %q is not an ordinary demand reference", target.Name(), owner.Name())
			}
			synchronous[target] = struct{}{}
		}
		for _, target := range targets {
			_, syncOnly := synchronous[target]
			if err := graph.AddReference(ReferenceEdge{Owner: ids[owner], Target: ids[target], SyncOnly: syncOnly}); err != nil {
				return fmt.Errorf("coro: add demand-only function reference from %q to %q: %w", owner.Name(), target.Name(), err)
			}
		}
	}
	return nil
}

func addSSAClassifiedRawPlainReferences(
	graph *Graph,
	functions []*ssa.Function,
	included map[*ssa.Function]bool,
	ids map[*ssa.Function]FunctionID,
	canonicalizer *ssaFunctionCanonicalizer,
	config SSAConfig,
) error {
	if config.ClassifyRawPlainDemandReferences == nil {
		return nil
	}
	for _, owner := range functions {
		targets, err := config.ClassifyRawPlainDemandReferences(owner)
		if err != nil {
			return fmt.Errorf("coro: classify raw-plain demand references in %q: %w", owner.Name(), err)
		}
		for index, target := range append([]*ssa.Function(nil), targets...) {
			if target == nil {
				return fmt.Errorf("coro: raw-plain demand reference %d in %q has a nil target", index, owner.Name())
			}
			if target.Prog != owner.Prog {
				return fmt.Errorf("coro: raw-plain demand reference %d in %q targets function %q from another SSA program", index, owner.Name(), target.Name())
			}
			canonical, resolved, resolveErr := canonicalizer.resolve(target)
			if resolveErr != nil {
				return fmt.Errorf("coro: resolve raw-plain target %q in %q: %w", target.Name(), owner.Name(), resolveErr)
			}
			if !resolved || canonical == nil || !included[canonical] {
				return fmt.Errorf("coro: raw-plain target %q in %q is outside the effective emission universe", target.Name(), owner.Name())
			}
			if canonical != target {
				return fmt.Errorf("coro: raw-plain target %q in %q is not the exact canonical function", target.Name(), owner.Name())
			}
			if err := graph.AddReference(ReferenceEdge{Owner: ids[owner], Target: ids[target], RawPlain: true}); err != nil {
				return fmt.Errorf("coro: add raw-plain function reference from %q to %q: %w", owner.Name(), target.Name(), err)
			}
		}
	}
	return nil
}

func classifySSAConditionalManagedStoreReferences(
	functions []*ssa.Function,
	included map[*ssa.Function]bool,
	canonicalizer *ssaFunctionCanonicalizer,
	flow *ssaFuncFlow,
	config SSAConfig,
) (map[*ssa.Store]*ssa.Function, error) {
	stores := make(map[*ssa.Store]*ssa.Function)
	if config.ClassifyConditionalManagedStoreReference == nil {
		return stores, nil
	}
	for _, owner := range functions {
		for _, block := range owner.Blocks {
			for _, instruction := range block.Instrs {
				store, ok := instruction.(*ssa.Store)
				if !ok {
					continue
				}
				target, classified, err := config.ClassifyConditionalManagedStoreReference(owner, store)
				if err != nil {
					return nil, fmt.Errorf("coro: classify conditional managed Store reference in %q: %w", owner.Name(), err)
				}
				if !classified {
					if target != nil {
						return nil, fmt.Errorf("coro: conditional managed Store reference in %q returned a target without classifying the occurrence", owner.Name())
					}
					continue
				}
				if target == nil || store.Parent() != owner || store.Val == nil || !isScalarFuncType(store.Val.Type()) {
					return nil, fmt.Errorf("coro: conditional managed Store reference in %q is not one exact scalar function publication", owner.Name())
				}
				canonical, resolved, resolveErr := canonicalizer.resolve(target)
				if resolveErr != nil {
					return nil, fmt.Errorf("coro: resolve conditional managed Store target %q in %q: %w", target.Name(), owner.Name(), resolveErr)
				}
				if !resolved || canonical == nil || canonical != target || !included[target] {
					return nil, fmt.Errorf("coro: conditional managed Store target %q in %q is not one exact canonical emission member", target.Name(), owner.Name())
				}
				index, present := flow.index[store.Val]
				if !present {
					return nil, fmt.Errorf("coro: conditional managed Store value in %q has no scalar function-value flow component", owner.Name())
				}
				root := flow.root(index)
				if flow.unknown[root] || flow.mayBeNil[root] || len(flow.targets[root]) != 1 {
					return nil, fmt.Errorf(
						"coro: conditional managed Store value in %q is not a closed non-nil singleton (unknown=%t nil=%t targets=%d)",
						owner.Name(), flow.unknown[root], flow.mayBeNil[root], len(flow.targets[root]),
					)
				}
				if _, exact := flow.targets[root][target]; !exact {
					return nil, fmt.Errorf("coro: conditional managed Store value in %q does not carry certified target %q", owner.Name(), target.Name())
				}
				stores[store] = target
			}
		}
	}
	return stores, nil
}

// addSSAReferenceEdges projects known function values used by demanded bodies
// into demand-only graph edges. Every CallInstruction callee operand is skipped:
// static and dynamic invocation are already represented by CallEdge and must
// not be mistaken for first-class publication. All other operands remain
// eligible, covering arguments, boxing, stores, returns, and closure bindings.
func addSSAReferenceEdges(
	graph *Graph,
	functions []*ssa.Function,
	included map[*ssa.Function]bool,
	ids map[*ssa.Function]FunctionID,
	flow *ssaFuncFlow,
	directPlainCallArguments []ssaCallArgumentUse,
	rawDirectPlainCallArguments []ssaCallArgumentUse,
	rawFunctionAddressCallArguments []ssaCallArgumentUse,
	staticCodeAddressCallArguments []ssaCallArgumentUse,
	syncOnlyDescriptorCallArguments []ssaCallArgumentUse,
	conditionalStores map[*ssa.Store]*ssa.Function,
) error {
	syncOnlyArguments := make(map[ssaCallArgumentUse]struct{}, len(directPlainCallArguments))
	rawPlainArguments := make(map[ssaCallArgumentUse]struct{}, len(rawDirectPlainCallArguments)+len(rawFunctionAddressCallArguments))
	functionAddressArguments := make(map[ssaCallArgumentUse]struct{}, len(rawFunctionAddressCallArguments)+len(staticCodeAddressCallArguments))
	functionAddressBoxes := make(map[*ssa.MakeInterface]struct{}, len(functionAddressArguments))
	for _, use := range directPlainCallArguments {
		syncOnlyArguments[use] = struct{}{}
	}
	for _, use := range rawDirectPlainCallArguments {
		rawPlainArguments[use] = struct{}{}
	}
	for _, use := range rawFunctionAddressCallArguments {
		rawPlainArguments[use] = struct{}{}
		functionAddressArguments[use] = struct{}{}
	}
	for _, use := range staticCodeAddressCallArguments {
		functionAddressArguments[use] = struct{}{}
	}
	for use := range functionAddressArguments {
		if use.call != nil && use.call.Common() != nil && use.argument >= 0 && use.argument < len(use.call.Common().Args) {
			if boxed, ok := use.call.Common().Args[use.argument].(*ssa.MakeInterface); ok {
				functionAddressBoxes[boxed] = struct{}{}
			}
		}
	}
	syncOnlyDescriptorArguments := make(map[ssaCallArgumentUse]struct{}, len(syncOnlyDescriptorCallArguments))
	for _, use := range syncOnlyDescriptorCallArguments {
		syncOnlyDescriptorArguments[use] = struct{}{}
	}
	syncOnlyRoots := make(map[int]struct{}, len(syncOnlyArguments)+len(rawPlainArguments))
	// A source-level Go function converted to one exact raw-C callback type is
	// an ABI adapter, not a managed publication. Raw and managed transports do
	// not union in ssaFuncFlow (their physical layouts differ), so remember the
	// exact conversion edge separately. The classified call argument below owns
	// the target's RawPlainDemand; suppressing only this operand prevents the
	// conversion instruction from manufacturing an unrelated managed consumer
	// while leaving every other use of the source value visible.
	rawPlainAdapterOperands := make(map[ssa.Value]ssa.Value, len(rawPlainArguments))
	allExactPlainArguments := make(map[ssaCallArgumentUse]struct{}, len(syncOnlyArguments)+len(rawPlainArguments))
	for use := range syncOnlyArguments {
		allExactPlainArguments[use] = struct{}{}
	}
	for use := range rawPlainArguments {
		allExactPlainArguments[use] = struct{}{}
	}
	for use := range allExactPlainArguments {
		if use.call == nil || use.call.Common() == nil || use.argument < 0 || use.argument >= len(use.call.Common().Args) {
			continue
		}
		value := use.call.Common().Args[use.argument]
		if boxed, ok := value.(*ssa.MakeInterface); ok {
			if _, functionAddress := functionAddressBoxes[boxed]; functionAddress {
				value = boxed.X
			}
		}
		if index, ok := flow.index[value]; ok {
			syncOnlyRoots[flow.root(index)] = struct{}{}
		}
		if _, rawPlain := rawPlainArguments[use]; rawPlain {
			for current := value; current != nil; {
				var source ssa.Value
				switch converted := current.(type) {
				case *ssa.ChangeType:
					source = converted.X
				case *ssa.Convert:
					source = converted.X
				default:
					current = nil
					continue
				}
				if !flow.rawCValue(current) || flow.rawCValue(source) {
					break
				}
				if _, exact := exactSSAContextFreeFunctionValue(source); !exact {
					break
				}
				rawPlainAdapterOperands[current] = source
				current = source
			}
		}
	}
	operands := make([]*ssa.Value, 0, 8)
	for _, owner := range functions {
		for _, block := range owner.Blocks {
			for _, instruction := range block.Instrs {
				if _, debug := instruction.(*ssa.DebugRef); debug {
					continue
				}
				call, callInstruction := instruction.(ssa.CallInstruction)
				if callInstruction && call.Common() != nil {
					for argument, value := range call.Common().Args {
						use := ssaCallArgumentUse{call: call, argument: argument}
						_, managedSyncOnly := syncOnlyArguments[use]
						_, rawPlain := rawPlainArguments[use]
						_, functionAddress := functionAddressArguments[use]
						_, descriptorSyncOnly := syncOnlyDescriptorArguments[use]
						if !managedSyncOnly && !rawPlain && !functionAddress && !descriptorSyncOnly {
							continue
						}
						referenceValue := value
						if boxed, ok := value.(*ssa.MakeInterface); ok {
							if _, address := functionAddressBoxes[boxed]; address {
								referenceValue = boxed.X
							}
						}
						for _, target := range sortedSSACandidates(flow.materializedTargets(referenceValue), ids, included) {
							if err := graph.AddReference(ReferenceEdge{
								Owner: ids[owner], Target: ids[target], SyncOnly: managedSyncOnly || descriptorSyncOnly, RawPlain: rawPlain,
							}); err != nil {
								return fmt.Errorf("coro: add classified SSA function reference from %q to %q: %w", owner.Name(), target.Name(), err)
							}
						}
					}
				}

				operands = instruction.Operands(operands[:0])
				var calleeOperand *ssa.Value
				if callInstruction {
					calleeOperand = &call.Common().Value
				}
				for _, operand := range operands {
					if operand == nil || *operand == nil || operand == calleeOperand {
						continue
					}
					if store, ok := instruction.(*ssa.Store); ok && operand == &store.Val {
						if _, conditional := conditionalStores[store]; conditional {
							// A build-owned complete slot proof retains this exact
							// descriptor producer without treating publication as
							// invocation. Active readers contribute their own managed
							// edges; a reader-free Store is elided by code generation.
							continue
						}
					}
					if index, ok := flow.index[*operand]; ok {
						if _, syncOnly := syncOnlyRoots[flow.root(index)]; syncOnly {
							// Flow validation proved that this complete scalar component
							// has no materialization boundary other than one or more exact
							// synchronous-only ABI arguments. Those arguments retained its
							// target above; representation-preserving SSA transfers must
							// not add a second, async-context reference.
							continue
						}
					}
					if value, ok := instruction.(ssa.Value); ok && rawPlainAdapterOperands[value] == *operand {
						// This exact Go -> raw-C conversion is consumed by a
						// compiler-certified callback boundary. The classified
						// argument already retained its target as RawPlainDemand.
						continue
					}
					if boxed, functionAddress := instruction.(*ssa.MakeInterface); functionAddress {
						if _, certified := functionAddressBoxes[boxed]; certified && operand == &boxed.X {
							// The exact sole consumer lowers this MakeInterface to the
							// contained function address. The interface is never materialized,
							// and the call argument above already retained the target with
							// the invocation or observation demand appropriate to that use.
							continue
						}
					}
					if callInstruction && call.Common() != nil {
						syncOnly := false
						for argument := range call.Common().Args {
							if operand != &call.Common().Args[argument] {
								continue
							}
							use := ssaCallArgumentUse{call: call, argument: argument}
							_, syncOnly = allExactPlainArguments[use]
							if !syncOnly {
								_, syncOnly = syncOnlyDescriptorArguments[use]
							}
							if _, functionAddress := functionAddressArguments[use]; functionAddress {
								syncOnly = true // classified call argument was retained above
							}
							break
						}
						if syncOnly {
							continue
						}
					}
					for _, target := range sortedSSACandidates(flow.materializedTargets(*operand), ids, included) {
						if err := graph.AddReference(ReferenceEdge{Owner: ids[owner], Target: ids[target]}); err != nil {
							return fmt.Errorf("coro: add SSA function reference from %q to %q: %w", owner.Name(), target.Name(), err)
						}
					}
				}
			}
		}
	}
	return nil
}

func classifySSADirectPlainCallArguments(functions []*ssa.Function, config SSAConfig) ([]ssaCallArgumentUse, error) {
	return classifySSAExactDirectPlainCallArguments(
		functions, config.ClassifyDirectPlainCallArgument, "managed direct-plain",
	)
}

func classifySSARawDirectPlainCallArguments(functions []*ssa.Function, config SSAConfig) ([]ssaCallArgumentUse, error) {
	return classifySSAExactDirectPlainCallArguments(
		functions, config.ClassifyRawDirectPlainCallArgument, "raw direct-plain",
	)
}

func classifySSAExactDirectPlainCallArguments(
	functions []*ssa.Function,
	classify func(*ssa.Function, ssa.CallInstruction, int) (bool, error),
	kind string,
) ([]ssaCallArgumentUse, error) {
	var result []ssaCallArgumentUse
	if classify == nil {
		return nil, nil
	}
	for _, caller := range functions {
		for _, block := range caller.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok || call.Common() == nil {
					continue
				}
				for argument, value := range call.Common().Args {
					directPlain, err := classify(caller, call, argument)
					if err != nil {
						return nil, fmt.Errorf("coro: classify trusted %s call argument %d in %q: %w", kind, argument, caller.Name(), err)
					}
					if !directPlain {
						continue
					}
					if _, direct := call.(*ssa.Call); !direct || call.Common().StaticCallee() == nil {
						return nil, fmt.Errorf("coro: trusted %s call argument %d in %q must belong to a direct static call", kind, argument, caller.Name())
					}
					if _, builtin := call.Common().Value.(*ssa.Builtin); builtin {
						return nil, fmt.Errorf("coro: trusted %s call argument %d in %q cannot belong to a builtin call", kind, argument, caller.Name())
					}
					if value == nil || !isScalarFuncType(value.Type()) {
						return nil, fmt.Errorf("coro: trusted %s call argument %d in %q must be a scalar function value", kind, argument, caller.Name())
					}
					result = append(result, ssaCallArgumentUse{call: call, argument: argument})
				}
			}
		}
	}
	return result, nil
}

func classifySSARawFunctionAddressCallArguments(functions []*ssa.Function, config SSAConfig) ([]ssaCallArgumentUse, error) {
	return classifySSAStaticFunctionAddressCallArguments(
		functions, config.ClassifyRawFunctionAddressCallArgument, "raw function-address",
	)
}

func classifySSAStaticCodeAddressCallArguments(functions []*ssa.Function, config SSAConfig) ([]ssaCallArgumentUse, error) {
	return classifySSAStaticFunctionAddressCallArguments(
		functions, config.ClassifyStaticCodeAddressCallArgument, "static code-address",
	)
}

func classifySSAStaticFunctionAddressCallArguments(
	functions []*ssa.Function,
	classify func(*ssa.Function, ssa.CallInstruction, int) (bool, error),
	kind string,
) ([]ssaCallArgumentUse, error) {
	var result []ssaCallArgumentUse
	if classify == nil {
		return nil, nil
	}
	for _, caller := range functions {
		for _, block := range caller.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok || call.Common() == nil {
					continue
				}
				for argument, value := range call.Common().Args {
					address, err := classify(caller, call, argument)
					if err != nil {
						return nil, fmt.Errorf("coro: classify trusted %s call argument %d in %q: %w", kind, argument, caller.Name(), err)
					}
					if !address {
						continue
					}
					direct, directCall := call.(*ssa.Call)
					if !directCall || call.Common().StaticCallee() == nil || call.Common().IsInvoke() {
						return nil, fmt.Errorf("coro: trusted %s argument %d in %q must belong to a direct static call", kind, argument, caller.Name())
					}
					if _, builtin := call.Common().Value.(*ssa.Builtin); builtin {
						return nil, fmt.Errorf("coro: trusted %s argument %d in %q cannot belong to a builtin call", kind, argument, caller.Name())
					}
					boxed, ok := value.(*ssa.MakeInterface)
					if !ok {
						return nil, fmt.Errorf("coro: trusted %s argument %d in %q must be a MakeInterface", kind, argument, caller.Name())
					}
					target, ok := boxed.X.(*ssa.Function)
					if !ok || len(target.FreeVars) != 0 {
						return nil, fmt.Errorf("coro: trusted %s argument %d in %q must contain a static function without captured state", kind, argument, caller.Name())
					}
					refs := boxed.Referrers()
					if refs == nil || len(*refs) != 1 || (*refs)[0] != direct {
						return nil, fmt.Errorf("coro: trusted %s argument %d in %q must be the MakeInterface value's exact sole consumer", kind, argument, caller.Name())
					}
					result = append(result, ssaCallArgumentUse{call: call, argument: argument})
				}
			}
		}
	}
	return result, nil
}

func classifySSAClosedDynamicCalls(
	functions []*ssa.Function,
	included map[*ssa.Function]bool,
	bodyFunctions map[*ssa.Function]bool,
	policies map[*ssa.Function]SSAFunctionPolicy,
	canonicalizer *ssaFunctionCanonicalizer,
	config SSAConfig,
) (map[ssa.CallInstruction]SSAClosedDynamicCallCertificate, error) {
	result := make(map[ssa.CallInstruction]SSAClosedDynamicCallCertificate)
	if config.ClassifyClosedDynamicCall == nil {
		return result, nil
	}
	for _, caller := range functions {
		for _, block := range caller.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok {
					continue
				}
				certificate, certified, err := config.ClassifyClosedDynamicCall(caller, call)
				if err != nil {
					return nil, fmt.Errorf("coro: classify closed dynamic call in %q: %w", caller.Name(), err)
				}
				if !certified {
					if len(certificate.Targets) != 0 || certificate.MayBeNil || certificate.SyncDispatch || len(certificate.SyncOnlyCallArguments) != 0 {
						return nil, fmt.Errorf("coro: unclassified dynamic call in %q returned non-empty certificate facts", caller.Name())
					}
					continue
				}
				common := call.Common()
				if common == nil || call.Parent() != caller {
					return nil, fmt.Errorf("coro: closed dynamic call certificate in %q must identify one exact owned call instruction", caller.Name())
				}
				switch exact := call.(type) {
				case *ssa.Call:
					if exact == nil {
						return nil, fmt.Errorf("coro: closed dynamic call certificate in %q has a nil ordinary call", caller.Name())
					}
				case *ssa.Defer:
					if exact == nil || exact.DeferStack != nil {
						return nil, fmt.Errorf("coro: closed dynamic defer certificate in %q requires one owner-local defer stack", caller.Name())
					}
					if certificate.SyncDispatch || len(certificate.SyncOnlyCallArguments) != 0 {
						return nil, fmt.Errorf("coro: closed dynamic defer certificate in %q cannot claim synchronous dispatch/publications", caller.Name())
					}
				default:
					return nil, fmt.Errorf("coro: closed dynamic call certificate in %q supports only ordinary call or defer instructions", caller.Name())
				}
				if common.StaticCallee() != nil {
					return nil, fmt.Errorf("coro: closed dynamic call certificate in %q cannot identify a static call", caller.Name())
				}
				if common.IsInvoke() {
					return nil, fmt.Errorf("coro: closed dynamic call certificate in %q cannot identify an interface invoke", caller.Name())
				}
				if _, builtin := common.Value.(*ssa.Builtin); builtin || common.Value == nil || !isScalarFuncType(common.Value.Type()) {
					return nil, fmt.Errorf("coro: closed dynamic call certificate in %q requires a scalar Go function callee", caller.Name())
				}
				if len(certificate.Targets) > 1 {
					return nil, fmt.Errorf("coro: closed dynamic call certificate in %q has %d targets; only nil or one exact target is supported", caller.Name(), len(certificate.Targets))
				}
				if len(certificate.Targets) == 0 && !certificate.MayBeNil {
					return nil, fmt.Errorf("coro: closed dynamic call certificate in %q has neither a target nor nil", caller.Name())
				}
				if len(certificate.SyncOnlyCallArguments) != 0 && !certificate.SyncDispatch {
					return nil, fmt.Errorf("coro: closed dynamic call certificate in %q has synchronous publications without SyncDispatch", caller.Name())
				}

				cloned := SSAClosedDynamicCallCertificate{
					MayBeNil:              certificate.MayBeNil,
					SyncDispatch:          certificate.SyncDispatch,
					SyncOnlyCallArguments: make([]SSASyncOnlyCallArgument, 0, len(certificate.SyncOnlyCallArguments)),
				}
				seenSyncArguments := make(map[ssaCallArgumentUse]struct{}, len(certificate.SyncOnlyCallArguments))
				for index, publication := range certificate.SyncOnlyCallArguments {
					publishedCall := publication.Call
					if publishedCall == nil || publishedCall.Common() == nil || publishedCall.Parent() == nil ||
						!bodyFunctions[publishedCall.Parent()] || publishedCall.Parent().Prog != caller.Prog {
						return nil, fmt.Errorf("coro: synchronous descriptor publication %d in %q is not in one owned emitted body", index, caller.Name())
					}
					if _, ordinary := publishedCall.(*ssa.Call); !ordinary || publishedCall.Common().StaticCallee() == nil || publishedCall.Common().IsInvoke() {
						return nil, fmt.Errorf("coro: synchronous descriptor publication %d in %q must be an ordinary static *ssa.Call argument", index, caller.Name())
					}
					if publication.Argument < 0 || publication.Argument >= len(publishedCall.Common().Args) ||
						!isScalarFuncType(publishedCall.Common().Args[publication.Argument].Type()) {
						return nil, fmt.Errorf("coro: synchronous descriptor publication %d in %q has an invalid scalar function argument %d", index, caller.Name(), publication.Argument)
					}
					use := ssaCallArgumentUse{call: publishedCall, argument: publication.Argument}
					if _, duplicate := seenSyncArguments[use]; duplicate {
						return nil, fmt.Errorf("coro: synchronous descriptor publication %d in %q duplicates one exact call argument", index, caller.Name())
					}
					seenSyncArguments[use] = struct{}{}
					cloned.SyncOnlyCallArguments = append(cloned.SyncOnlyCallArguments, publication)
				}
				if len(certificate.Targets) == 1 {
					target := certificate.Targets[0]
					if target == nil {
						return nil, fmt.Errorf("coro: closed dynamic call certificate in %q has a nil target entry", caller.Name())
					}
					if target.Prog != caller.Prog {
						return nil, fmt.Errorf("coro: closed dynamic call certificate in %q targets function %q from another SSA program", caller.Name(), target.Name())
					}
					canonical, resolved, resolveErr := canonicalizer.resolve(target)
					if resolveErr != nil {
						return nil, fmt.Errorf("coro: resolve closed dynamic target %q in %q: %w", target.Name(), caller.Name(), resolveErr)
					}
					if !resolved || canonical == nil || !included[canonical] {
						return nil, fmt.Errorf("coro: closed dynamic target %q in %q is outside the effective emission universe", target.Name(), caller.Name())
					}
					if canonical != target {
						return nil, fmt.Errorf("coro: closed dynamic target %q in %q is not the exact canonical function", target.Name(), caller.Name())
					}
					policy := policies[target]
					if !bodyFunctions[target] || len(target.Blocks) == 0 || policy.IgnoreBody || (policy.OverrideExternal && policy.External != Defined) {
						return nil, fmt.Errorf("coro: closed dynamic target %q in %q must be an owned emitted Go body, not an external target", target.Name(), caller.Name())
					}
					callSignature := common.Signature()
					if callSignature == nil || target.Signature == nil || !types.Identical(callSignature, target.Signature) {
						return nil, fmt.Errorf("coro: closed dynamic target %q in %q has signature %v, want %v", target.Name(), caller.Name(), target.Signature, callSignature)
					}
					cloned.Targets = []*ssa.Function{target}
				}
				result[call] = cloned
			}
		}
	}
	return result, nil
}

func classifySSAElidedCalls(functions []*ssa.Function, config SSAConfig) (map[ssa.CallInstruction]bool, error) {
	result := make(map[ssa.CallInstruction]bool)
	if config.ClassifyElidedCall == nil {
		return result, nil
	}
	for _, caller := range functions {
		for _, block := range caller.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok {
					continue
				}
				elided, err := config.ClassifyElidedCall(caller, call)
				if err != nil {
					return nil, fmt.Errorf("coro: classify frontend-elided call in %q: %w", caller.Name(), err)
				}
				if !elided {
					continue
				}
				if call.Common() == nil || call.Common().StaticCallee() == nil || ssaCallKind(call) != CallDirect {
					return nil, fmt.Errorf("coro: frontend-elided call in %q must be a direct static call", caller.Name())
				}
				result[call] = true
			}
		}
	}
	return result, nil
}

func classifySSAElidedCallCertificates(
	elided map[ssa.CallInstruction]bool,
	config SSAConfig,
) (map[ssa.CallInstruction]string, error) {
	result := make(map[ssa.CallInstruction]string)
	if config.ClassifyElidedCallCertificate == nil {
		return result, nil
	}
	for call := range elided {
		if call == nil || call.Parent() == nil {
			return nil, fmt.Errorf("coro: frontend-elided call certificate has no exact call owner")
		}
		certificate, err := config.ClassifyElidedCallCertificate(call.Parent(), call)
		if err != nil {
			return nil, fmt.Errorf("coro: classify frontend-elided call certificate in %q: %w", call.Parent().Name(), err)
		}
		if certificate == "" {
			continue
		}
		if !utf8.ValidString(certificate) {
			return nil, fmt.Errorf("coro: frontend-elided call certificate in %q is not a valid opaque identity", call.Parent().Name())
		}
		result[call] = certificate
	}
	return result, nil
}

func classifySSAUnknownCalls(
	functions []*ssa.Function,
	included map[*ssa.Function]bool,
	flow *ssaFuncFlow,
	elided map[ssa.CallInstruction]bool,
	config SSAConfig,
) (map[ssa.CallInstruction]UnknownTarget, error) {
	result := make(map[ssa.CallInstruction]UnknownTarget)
	for _, caller := range functions {
		for _, block := range caller.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok {
					continue
				}
				if elided[call] {
					continue
				}
				common := call.Common()
				if _, builtin := common.Value.(*ssa.Builtin); builtin {
					continue
				}
				if callee := common.StaticCallee(); callee != nil {
					canonical, ok, err := flow.resolveTarget(callee)
					if err != nil {
						return nil, fmt.Errorf("coro: resolve static callee %q in %q while classifying unknown calls: %w", callee.Name(), caller.Name(), err)
					}
					if ok && included[canonical] {
						continue
					}
				}
				if flow.rawCValue(common.Value) {
					target, err := classifyUnknownCall(config, caller, call)
					if err != nil {
						return nil, err
					}
					if target != UnknownForeign {
						return nil, fmt.Errorf("coro: raw C code-pointer call in %q classified as %v, want unknown foreign", caller.Name(), target)
					}
					result[call] = target
					continue
				}
				if targets, complete := flow.scalarCallTargets(call); complete {
					// A closed empty target set is an always-nil callee. It may
					// still require Dispatch storage and a nil-call fault path, but
					// there is no non-nil descriptor capability to invoke. Asking
					// the frontend to classify that nonexistent boundary would
					// manufacture a structured await (or an opaque open call) from
					// an exact nil-only certificate.
					if len(targets) == 0 {
						continue
					}
					// Closed scalar flow can still use the universal descriptor ABI.
					// Ask the frontend only for that physical boundary certificate;
					// the exact targets remain frozen in the later CallPlan.
					if common.StaticCallee() == nil {
						if index, ok := flow.index[common.Value]; ok && !flow.requiresDispatch(flow.root(index)) {
							continue
						}
						target, err := classifyUnknownCall(config, caller, call)
						if err != nil {
							return nil, err
						}
						if target.managedDispatch() {
							result[call] = target
						}
					}
					continue
				}
				target, err := classifyUnknownCall(config, caller, call)
				if err != nil {
					return nil, err
				}
				result[call] = target
			}
		}
	}
	return result, nil
}

func canonicalizeSSADynamicCandidates(
	candidates map[ssa.CallInstruction]map[*ssa.Function]struct{},
	canonicalizer *ssaFunctionCanonicalizer,
) (map[ssa.CallInstruction]map[*ssa.Function]struct{}, error) {
	result := make(map[ssa.CallInstruction]map[*ssa.Function]struct{}, len(candidates))
	for call, rawTargets := range candidates {
		canonicalTargets := make(map[*ssa.Function]struct{}, len(rawTargets))
		for raw := range rawTargets {
			canonical, ok, err := canonicalizer.resolve(raw)
			if err != nil {
				caller := "<unknown>"
				if call != nil && call.Parent() != nil {
					caller = call.Parent().Name()
				}
				return nil, fmt.Errorf("coro: resolve dynamic candidate %q for call in %q: %w", raw.Name(), caller, err)
			}
			if ok {
				canonicalTargets[canonical] = struct{}{}
			} else {
				// Retain an excluded sentinel. Dropping it would let CHAClosed
				// incorrectly treat a partially unresolved candidate set as closed.
				canonicalTargets[raw] = struct{}{}
			}
		}
		if len(canonicalTargets) != 0 {
			result[call] = canonicalTargets
		}
	}
	return result, nil
}

// filterSSADynamicCandidateSites removes CHA sites belonging to SSA stub bodies
// that the frontend does not physically emit. Candidate target functions remain
// untouched: a real emitted call may still dispatch to an external declaration.
func filterSSADynamicCandidateSites(
	candidates map[ssa.CallInstruction]map[*ssa.Function]struct{},
	bodyFunctions map[*ssa.Function]bool,
	canonicalizer *ssaFunctionCanonicalizer,
) (map[ssa.CallInstruction]map[*ssa.Function]struct{}, error) {
	if len(candidates) == 0 {
		return candidates, nil
	}
	result := make(map[ssa.CallInstruction]map[*ssa.Function]struct{}, len(candidates))
	for call, targets := range candidates {
		if call == nil || call.Parent() == nil {
			continue
		}
		owner, ok, err := canonicalizer.resolve(call.Parent())
		if err != nil {
			return nil, fmt.Errorf("coro: resolve dynamic-call owner %q before candidate classification: %w", call.Parent().Name(), err)
		}
		if !ok || !bodyFunctions[owner] {
			continue
		}
		result[call] = targets
	}
	return result, nil
}

// filterSSAStaticCodeAddressOnlyCandidates removes non-method CHA candidates
// whose only address-taking occurrences are exact frontend-certified code
// observations. Such an occurrence emits no managed function value and cannot
// make the target callable by an unrelated dynamic Go call. Canonicalization
// is applied to the remaining publication inventory so patched declarations
// and their emitted definitions retain one identity. An unresolved ordinary
// publication remains eligible and therefore keeps closed-world analysis open.
func filterSSAStaticCodeAddressOnlyCandidates(
	candidates map[ssa.CallInstruction]map[*ssa.Function]struct{},
	functions []*ssa.Function,
	codeAddressUses []ssaCallArgumentUse,
	canonicalizer *ssaFunctionCanonicalizer,
) (map[ssa.CallInstruction]map[*ssa.Function]struct{}, error) {
	if len(candidates) == 0 || len(codeAddressUses) == 0 {
		return candidates, nil
	}
	managedAddresses := make(map[*ssa.Function]struct{})
	for raw := range restrictedSSAAddressTakenFunctionsExcluding(functions, codeAddressUses) {
		managedAddresses[raw] = struct{}{}
		canonical, resolved, err := canonicalizer.resolve(raw)
		if err != nil {
			return nil, fmt.Errorf("coro: resolve managed address-taken function %q while filtering code-address-only CHA candidates: %w", raw.Name(), err)
		}
		if resolved && canonical != nil {
			managedAddresses[canonical] = struct{}{}
		}
	}

	result := make(map[ssa.CallInstruction]map[*ssa.Function]struct{}, len(candidates))
	for call, targets := range candidates {
		if call == nil || call.Common() == nil || call.Common().IsInvoke() {
			result[call] = targets
			continue
		}
		filtered := make(map[*ssa.Function]struct{}, len(targets))
		for target := range targets {
			if _, published := managedAddresses[target]; published {
				filtered[target] = struct{}{}
			}
		}
		if len(filtered) != 0 {
			result[call] = filtered
		}
	}
	return result, nil
}

func closeCanonicalStaticFunctions(
	functions []*ssa.Function,
	prog *ssa.Program,
	canonicalizer *ssaFunctionCanonicalizer,
) ([]*ssa.Function, error) {
	result := append([]*ssa.Function(nil), functions...)
	seen := make(map[*ssa.Function]struct{}, len(result))
	for _, fn := range result {
		seen[fn] = struct{}{}
	}
	add := func(raw, caller *ssa.Function) error {
		if raw == nil || raw.Prog != prog {
			return nil
		}
		canonical, ok, err := canonicalizer.resolve(raw)
		if err != nil {
			return fmt.Errorf("coro: resolve static function %q reached from %q: %w", raw.Name(), caller.Name(), err)
		}
		if !ok {
			return nil
		}
		if _, exists := seen[canonical]; !exists {
			seen[canonical] = struct{}{}
			result = append(result, canonical)
		}
		return nil
	}
	for head := 0; head < len(result); head++ {
		fn := result[head]
		for _, child := range fn.AnonFuncs {
			if err := add(child, fn); err != nil {
				return nil, err
			}
		}
		operands := make([]*ssa.Value, 0, 8)
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				if _, debug := instruction.(*ssa.DebugRef); debug {
					continue
				}
				operands = instruction.Operands(operands[:0])
				for _, operand := range operands {
					if operand == nil {
						continue
					}
					if target, ok := (*operand).(*ssa.Function); ok {
						if err := add(target, fn); err != nil {
							return nil, err
						}
					}
				}
				call, ok := instruction.(ssa.CallInstruction)
				if !ok {
					continue
				}
				if err := add(call.Common().StaticCallee(), fn); err != nil {
					return nil, err
				}
			}
		}
	}
	return result, nil
}

func closeStaticFunctions(functions map[*ssa.Function]struct{}, prog *ssa.Program) {
	queue := make([]*ssa.Function, 0, len(functions))
	for fn := range functions {
		queue = append(queue, fn)
	}
	for head := 0; head < len(queue); head++ {
		fn := queue[head]
		for _, child := range fn.AnonFuncs {
			if child != nil && child.Prog == prog {
				if _, ok := functions[child]; !ok {
					functions[child] = struct{}{}
					queue = append(queue, child)
				}
			}
		}
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok {
					continue
				}
				callee := call.Common().StaticCallee()
				if callee != nil && callee.Prog == prog {
					if _, ok := functions[callee]; !ok {
						functions[callee] = struct{}{}
						queue = append(queue, callee)
					}
				}
			}
		}
	}
}

func rawSSAFunctionKey(fn *ssa.Function) string {
	pkgPath := ""
	if fn.Pkg != nil && fn.Pkg.Pkg != nil {
		pkgPath = fn.Pkg.Pkg.Path()
	} else if obj := fn.Object(); obj != nil && obj.Pkg() != nil {
		pkgPath = obj.Pkg().Path()
	}
	objectID := ""
	if obj := fn.Object(); obj != nil {
		objectID = obj.Id()
	}
	signature := ""
	if fn.Signature != nil {
		signature = types.TypeString(fn.Signature, func(pkg *types.Package) string {
			if pkg == nil {
				return ""
			}
			return pkg.Path()
		})
	}
	var typeArgs strings.Builder
	for _, arg := range fn.TypeArgs() {
		appendIdentityField(&typeArgs, "arg", types.TypeString(arg, func(pkg *types.Package) string {
			if pkg == nil {
				return ""
			}
			return pkg.Path()
		}))
	}
	parent := ""
	if fn.Parent() != nil {
		parent = fn.Parent().Name() + fmt.Sprintf("@%020d", int(fn.Parent().Pos()))
	}
	return fmt.Sprintf("%s\x00%s\x00%020d\x00%s\x00%s\x00%s\x00%s\x00%s",
		pkgPath, fn.Name(), int(fn.Pos()), fn.Synthetic, objectID, signature, typeArgs.String(), parent)
}

func isUninstantiatedGeneric(fn *ssa.Function) bool {
	params := fn.TypeParams()
	return params != nil && params.Len() != 0 && len(fn.TypeArgs()) == 0
}

func scanSSAFunctionBody(fn *ssa.Function) SSAFunctionBodyFacts {
	if fn == nil || fn.Blocks == nil {
		return SSAFunctionBodyFacts{Effect: NoSuspend}
	}
	effect := NoSuspend
	// SSA operations may panic implicitly (bounds, nil dereference, division,
	// type assertion, send/close, allocation, and more). Until a complete
	// no-unwind proof exists, every defined body conservatively carries this
	// independent execution flag. It does not itself force coroutine lowering.
	exec := MayUnwind
	instructions := 0
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			if _, debug := instruction.(*ssa.DebugRef); debug {
				continue
			}
			instructions++
			switch instruction := instruction.(type) {
			case *ssa.Send:
				effect = effect.Join(MayPark)
			case *ssa.UnOp:
				if instruction.Op == token.ARROW {
					effect = effect.Join(MayPark)
				}
			case *ssa.Select:
				if instruction.Blocking {
					effect = effect.Join(MayPark)
				}
			case *ssa.Defer, *ssa.RunDefers:
				exec = exec.Join(NeedsCleanupFrame)
			case *ssa.Panic:
				exec = exec.Join(MayUnwind)
			case *ssa.Call:
				if builtin, ok := instruction.Common().Value.(*ssa.Builtin); ok && builtin.Name() == "panic" {
					exec = exec.Join(MayUnwind)
				}
			}
		}
	}
	return SSAFunctionBodyFacts{
		Effect:           effect.Normalize(),
		Exec:             exec,
		InstructionCount: instructions,
		HasCycle:         cfgHasCycle(fn.Blocks),
	}
}

func cfgHasCycle(blocks []*ssa.BasicBlock) bool {
	if len(blocks) == 0 {
		return false
	}
	indegree := make([]int, len(blocks))
	for _, block := range blocks {
		for _, successor := range block.Succs {
			indegree[successor.Index]++
		}
	}
	queue := make([]*ssa.BasicBlock, 0, len(blocks))
	for _, block := range blocks {
		if indegree[block.Index] == 0 {
			queue = append(queue, block)
		}
	}
	visited := 0
	for head := 0; head < len(queue); head++ {
		block := queue[head]
		visited++
		for _, successor := range block.Succs {
			indegree[successor.Index]--
			if indegree[successor.Index] == 0 {
				queue = append(queue, successor)
			}
		}
	}
	return visited != len(blocks)
}

func ssaCallKind(call ssa.CallInstruction) CallKind {
	switch call.(type) {
	case *ssa.Go:
		return CallSpawn
	case *ssa.Defer:
		return CallDefer
	default:
		return CallDirect
	}
}

func staticCallKind(syntax CallKind, policy SSAFunctionPolicy) CallKind {
	if syntax == CallDirect && (policy.External == ExternalUnknownForeign || policy.Exec.Contains(BlockForeign)) {
		return CallForeign
	}
	return syntax
}

func classifyUnknownCall(config SSAConfig, caller *ssa.Function, call ssa.CallInstruction) (UnknownTarget, error) {
	target := UnknownManaged
	if config.ClassifyUnknownCall != nil {
		var err error
		target, err = config.ClassifyUnknownCall(caller, call)
		if err != nil {
			return 0, fmt.Errorf("coro: classify unknown call in %q: %w", caller.Name(), err)
		}
	}
	if err := target.validate(); err != nil {
		return 0, fmt.Errorf("coro: unknown call in %q: %w", caller.Name(), err)
	}
	common := call.Common()
	if target == UnknownManagedDispatch && common != nil && common.IsInvoke() {
		return 0, fmt.Errorf("coro: unknown call in %q: interface invoke requires the distinct UnknownManagedInterfaceDispatch transport certificate", caller.Name())
	}
	if target == UnknownManagedInterfaceDispatch {
		direct, ordinary := call.(*ssa.Call)
		if !ordinary || direct == nil || common == nil || common.StaticCallee() != nil ||
			!common.IsInvoke() || common.Method == nil {
			return 0, fmt.Errorf("coro: unknown call in %q: UnknownManagedInterfaceDispatch requires an ordinary interface invoke", caller.Name())
		}
		sig := common.Signature()
		if sig == nil || sig.Variadic() ||
			(sig.TypeParams() != nil && sig.TypeParams().Len() != 0) ||
			(sig.RecvTypeParams() != nil && sig.RecvTypeParams().Len() != 0) {
			return 0, fmt.Errorf("coro: unknown call in %q: managed interface descriptor requires a receiver-free, non-variadic, non-generic call signature", caller.Name())
		}
		if _, ok := types.Unalias(common.Value.Type()).Underlying().(*types.Interface); !ok {
			return 0, fmt.Errorf("coro: unknown call in %q: managed interface descriptor receiver is not an interface", caller.Name())
		}
	}
	return target, nil
}

func addSSAUnknownCall(graph *Graph, caller FunctionID, syntax CallKind, target UnknownTarget) error {
	kind := unknownCallKind(syntax, target)
	return graph.AddUnknownCall(UnknownCall{Caller: caller, Kind: kind, Target: target})
}

func unknownCallKind(syntax CallKind, target UnknownTarget) CallKind {
	if syntax == CallDirect && target == UnknownForeign {
		return CallForeign
	}
	return syntax
}

func sortedSSACandidates(candidates map[*ssa.Function]struct{}, ids map[*ssa.Function]FunctionID, included map[*ssa.Function]bool) []*ssa.Function {
	result := make([]*ssa.Function, 0, len(candidates))
	for candidate := range candidates {
		if included[candidate] {
			result = append(result, candidate)
		}
	}
	sort.Slice(result, func(i, j int) bool { return ids[result[i]] < ids[result[j]] })
	return result
}
