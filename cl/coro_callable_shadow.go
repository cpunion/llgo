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

package cl

import (
	"fmt"
	"go/ast"
	"go/types"
	"sort"
	"strconv"
	"strings"

	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

// CoroCallableShadowABI is producer metadata, not a property recovered from a
// uintptr. A FuncPCABI0 producer publishes the exact foreign-call family and
// word arity that its target declaration permits. Consumers may compare this
// value with their own call ABI, but may never manufacture it from the emitted
// address.
type CoroCallableShadowABI struct {
	Family   string
	WordArgs int
}

const coroCallableShadowWorkerSyscallFamily = "word-call.v1"

// CoroCallableShadow is the compiler-only fact paired with one exact
// FuncPCABI0 SSA result. Producer is deliberately part of the identity: two
// syntactically independent publications of the same text address remain two
// facts even though Target and PhysicalSymbol match.
type CoroCallableShadow struct {
	Producer       *ssa.Call
	SourceTarget   *ssa.Function
	Target         *ssa.Function
	PhysicalSymbol string
	ABI            CoroCallableShadowABI
	// ForeignPointerResultMask marks worker result words that the exact C
	// declaration promises are pointers to non-Go storage. The fact is injected
	// at FuncPCABI0 formation and never reconstructed from the returned uintptr.
	ForeignPointerResultMask  uint8
	ContractCertificateID     string
	LegacyWorkerAddressCompat bool
}

func coroWorkerWordCallableABI(arity int) string {
	return coroCallableShadowWorkerSyscallFamily + "/" + strconv.Itoa(arity)
}

type coroWorkerWordCallableABIShape struct {
	wordArgs                 int
	foreignPointerResultMask uint8
}

const coroWorkerForeignPointerResultR1 = "+foreign-pointer-result=r1"

func parseCoroWorkerWordCallableABI(value string) (coroWorkerWordCallableABIShape, bool) {
	var shape coroWorkerWordCallableABIShape
	prefix := coroCallableShadowWorkerSyscallFamily + "/"
	if !strings.HasPrefix(value, prefix) {
		return shape, false
	}
	text := strings.TrimPrefix(value, prefix)
	if strings.HasSuffix(text, coroWorkerForeignPointerResultR1) {
		shape.foreignPointerResultMask = 1
		text = strings.TrimSuffix(text, coroWorkerForeignPointerResultR1)
	}
	arity, err := strconv.Atoi(text)
	if err != nil || arity < 0 || arity > coroWorkerMaxArgsV1 || text != strconv.Itoa(arity) {
		return coroWorkerWordCallableABIShape{}, false
	}
	shape.wordArgs = arity
	return shape, true
}

func coroWorkerCallableContractCompatible(contract coro.CallableContract) bool {
	return contract.Progress == coro.ProgressMayBlock &&
		contract.Affinity == coro.AffinityAnyThread &&
		contract.Reentry == coro.ReentryNone &&
		contract.Memory != coro.MemoryUnknown && contract.Memory != coro.MemoryRetained
}

// coroWorkerCallableDeclarationContractArity is used only while the emission
// universe is still discovering address-only FuncPCABI0 operands. It parses an
// exact declaration; it does not issue a capability. The production shadow is
// injected later from CoroCallableContractCertificate after aliases and ABI
// identities have frozen.
func coroWorkerCallableDeclarationContractArity(fn *ssa.Function) (int, bool, error) {
	parsed, present, err := coroCallableContractCertificateFor(fn)
	if err != nil || !present {
		return 0, false, err
	}
	if parsed.Scope != coroCallableContractScopeDeclaration ||
		!coroWorkerCallableContractCompatible(parsed.Contract) || parsed.ABI == "" {
		return 0, false, nil
	}
	shape, ok := parseCoroWorkerWordCallableABI(parsed.ABI)
	return shape.wordArgs, ok, nil
}

// coroWorkerAddressOnlyDeclaration reports whether fn is one exact declaration
// whose Go signature exists only so FuncPCABI0 can publish a physical C text
// address.  Its callable ABI is the explicit word-call ABI carried beside that
// address, not the otherwise-unused Go declaration signature.  In particular,
// a catalog declaration such as func libc_write_trampoline() must not make the
// ordinary typed C ABI inventory believe that C.write has a second zero-argument
// calling convention.
//
// Keep this classification deliberately narrower than "has a callable
// contract": only an exact bodyless trampoline plus either a valid explicit
// word-call ABI or the legacy workeraddr spelling is address-only.  Ordinary
// typed declarations, malformed aliases, and contracts without a word-call ABI
// remain in the physical ABI collision inventory.
func (u *EmissionUniverse) coroWorkerAddressOnlyDeclaration(fn *ssa.Function) (bool, error) {
	if u == nil || fn == nil {
		return false, nil
	}
	canonical := u.canonicalAlias(fn)
	if canonical == nil {
		return false, fmt.Errorf("worker address-only declaration has cyclic canonical aliases")
	}
	if !coroWorkerAddressAliasDeclaration(canonical) {
		return false, nil
	}
	physical := extractTrampolineCName(canonical.Name())
	if physical == "" {
		return false, nil
	}
	physical = remapTrampolineCNameForTarget(u.prog.Target(), physical)

	if certificate, certified := u.callableContracts[canonical]; certified {
		_, wordABI := parseCoroWorkerWordCallableABI(certificate.CallableABI)
		if certificate.Scope != CoroCallableContractScopeDeclaration ||
			!certificate.CallableABIExplicit || !wordABI {
			return false, nil
		}
		if certificate.PhysicalSymbol != physical {
			return false, fmt.Errorf(
				"worker address-only declaration %q contract physical symbol %q differs from trampoline symbol %q",
				canonical.Name(), certificate.PhysicalSymbol, physical,
			)
		}
		return true, nil
	}

	directive, err := coroForeignCallDirectiveFor(canonical)
	if err != nil {
		return false, err
	}
	if directive != coroForeignCallWorkerAddress {
		return false, nil
	}
	if _, err := coroWorkerAddressDirectiveArity(canonical); err != nil {
		return false, err
	}
	return true, nil
}

// coroWorkerCallableTarget freezes the only two accepted producer sources:
// the target-neutral declaration contract, and the temporary workeraddr
// migration spelling. It consumes no uintptr and performs no address lookup.
func coroWorkerCallableTarget(
	universe *EmissionUniverse,
	sourceTarget, target *ssa.Function,
) (coroWorkerAddressTarget, string, error) {
	if universe == nil || sourceTarget == nil || target == nil {
		return coroWorkerAddressTarget{}, "invalid-funcpcabi0-target", nil
	}
	decl, _ := target.Syntax().(*ast.FuncDecl)
	if decl == nil || decl.Body != nil || decl.Recv != nil || target.Signature == nil ||
		target.Signature.Recv() != nil || target.Signature.Variadic() || len(target.Blocks) != 0 {
		return coroWorkerAddressTarget{}, "", fmt.Errorf(
			"worker callable target %q must be an exact bodyless non-method declaration", target.Name(),
		)
	}
	physical := extractTrampolineCName(target.Name())
	if physical == "" {
		return coroWorkerAddressTarget{}, "", fmt.Errorf(
			"worker callable target %q has no FuncPCABI0 C trampoline lowering", target.Name(),
		)
	}
	physical = remapTrampolineCNameForTarget(universe.prog.Target(), physical)

	// Address-only trampoline declarations are deliberately absent from the
	// managed required set. The contract freezer nevertheless owns the exact
	// canonical-keyed map; this internal consumer must not route through the
	// public accessor, whose required-function check is correct for ordinary
	// managed callers.
	certificate, certified := universe.callableContracts[target]
	if certified {
		if certificate.Scope != CoroCallableContractScopeDeclaration {
			return coroWorkerAddressTarget{}, "callable-contract-is-not-a-declaration", nil
		}
		if !coroWorkerCallableContractCompatible(certificate.Contract) {
			return coroWorkerAddressTarget{}, "callable-contract-is-not-worker-compatible", nil
		}
		if !certificate.CallableABIExplicit {
			return coroWorkerAddressTarget{}, "callable-contract-requires-explicit-word-abi", nil
		}
		shape, ok := parseCoroWorkerWordCallableABI(certificate.CallableABI)
		if !ok {
			return coroWorkerAddressTarget{}, "callable-contract-has-incompatible-word-abi", nil
		}
		if certificate.PhysicalSymbol != physical {
			return coroWorkerAddressTarget{}, "", fmt.Errorf(
				"worker callable target %q contract physical symbol %q differs from FuncPCABI0 symbol %q",
				target.Name(), certificate.PhysicalSymbol, physical,
			)
		}
		return coroWorkerAddressTarget{
			target:                   target,
			physicalSymbol:           physical,
			workerArity:              shape.wordArgs,
			foreignPointerResultMask: shape.foreignPointerResultMask,
			contractCertificateID:    certificate.ID,
			legacyWorkerAddressOnly:  false,
		}, "", nil
	}
	// Address-only declarations are intentionally removed from the managed
	// function inventory before the general callable-contract freezer runs.
	// Freeze their exact source contract into the producer shadow here, while
	// the SSA target and its typed trampoline ABI are still available. This is
	// still producer-side metadata; no emitted uintptr participates.
	parsed, present, err := coroCallableContractCertificateFor(target)
	if err != nil {
		return coroWorkerAddressTarget{}, "", err
	}
	if present {
		if parsed.Scope != coroCallableContractScopeDeclaration {
			return coroWorkerAddressTarget{}, "callable-contract-is-not-a-declaration", nil
		}
		if !coroWorkerCallableContractCompatible(parsed.Contract) {
			return coroWorkerAddressTarget{}, "callable-contract-is-not-worker-compatible", nil
		}
		shape, ok := parseCoroWorkerWordCallableABI(parsed.ABI)
		if !ok {
			return coroWorkerAddressTarget{}, "callable-contract-has-incompatible-word-abi", nil
		}
		behaviorDigest, err := coro.CallableContractBehaviorDigest(parsed.Contract.ID, parsed.Contract)
		if err != nil {
			return coroWorkerAddressTarget{}, "", err
		}
		certificateID := emissionDigest(framedEmissionKey(
			"llgo-coro-address-only-callable-contract-v1",
			coroWorkerAddressFunctionIdentity(universe, sourceTarget),
			coroWorkerAddressFunctionIdentity(universe, target),
			physical,
			structuralGoLinknameABITypeKey(target.Signature),
			parsed.Canonical,
			behaviorDigest,
		))
		return coroWorkerAddressTarget{
			target:                   target,
			physicalSymbol:           physical,
			workerArity:              shape.wordArgs,
			foreignPointerResultMask: shape.foreignPointerResultMask,
			contractCertificateID:    certificateID,
			legacyWorkerAddressOnly:  false,
		}, "", nil
	}

	directive, err := coroForeignCallDirectiveFor(target)
	if err != nil {
		return coroWorkerAddressTarget{}, "", fmt.Errorf("worker-address target %q: %w", target.Name(), err)
	}
	if directive != coroForeignCallWorkerAddress {
		return coroWorkerAddressTarget{}, "target-lacks-workeraddr", nil
	}
	arity, err := coroWorkerAddressDirectiveArity(target)
	if err != nil {
		return coroWorkerAddressTarget{}, "", err
	}
	return coroWorkerAddressTarget{
		target:                  target,
		physicalSymbol:          physical,
		workerArity:             arity,
		contractCertificateID:   "legacy-workeraddr.v0",
		legacyWorkerAddressOnly: true,
	}, "", nil
}

// CoroCallableShadowIncomingEdge records one exact static call that supplies a
// private parameter carrier. An uncertified edge remains in the inventory so a
// later SSA-plan join can prove that it is inactive in managed execution. This
// is what permits a shared syscall wrapper to have both a safe and an
// incompatible caller without silently trusting the latter.
type CoroCallableShadowIncomingEdge struct {
	Call       *ssa.Call
	Carrier    *ssa.Function
	Parameter  int
	Candidates []CoroCallableShadow
	Certified  bool
	Reason     string
}

// CoroCallableShadowSink is the result at one exact llgo.syscall call. For a
// direct producer, Certified means that the producer ABI exactly matches the
// sink. For a private parameter carrier, it means that at least one exact
// incoming edge is certified; all other edges are retained in Incoming and
// must be narrowed by the eventual whole-plan verifier.
type CoroCallableShadowSink struct {
	Call       *ssa.Call
	ABI        CoroCallableShadowABI
	Candidates []CoroCallableShadow
	Incoming   []CoroCallableShadowIncomingEdge
	Certified  bool
	Reason     string
}

// CoroCallableShadowAnalysis is an immutable, reportable producer-forward
// analysis. It intentionally has no address-keyed lookup API.
type CoroCallableShadowAnalysis struct {
	producers map[*ssa.Call]CoroCallableShadow
	rejected  map[*ssa.Call]string
	sinks     map[*ssa.Call]CoroCallableShadowSink
}

// Producer returns the shadow injected at an exact FuncPCABI0 producer.
func (a *CoroCallableShadowAnalysis) Producer(call *ssa.Call) (CoroCallableShadow, bool) {
	if a == nil || call == nil {
		return CoroCallableShadow{}, false
	}
	shadow, ok := a.producers[call]
	return shadow, ok
}

// ProducerRejection returns the fail-closed reason for a FuncPCABI0 call that
// could not publish a callable shadow.
func (a *CoroCallableShadowAnalysis) ProducerRejection(call *ssa.Call) (string, bool) {
	if a == nil || call == nil {
		return "", false
	}
	reason, ok := a.rejected[call]
	return reason, ok
}

// Sink returns a copy of the producer-forward result for an exact
// llgo.syscall call.
func (a *CoroCallableShadowAnalysis) Sink(call ssa.CallInstruction) (CoroCallableShadowSink, bool) {
	if a == nil || call == nil {
		return CoroCallableShadowSink{}, false
	}
	direct, ok := call.(*ssa.Call)
	if !ok {
		return CoroCallableShadowSink{}, false
	}
	sink, ok := a.sinks[direct]
	if !ok {
		return CoroCallableShadowSink{}, false
	}
	sink.Candidates = cloneCoroCallableShadows(sink.Candidates)
	sink.Incoming = cloneCoroCallableShadowIncoming(sink.Incoming)
	return sink, true
}

type coroCallableShadowFactKey struct {
	value    ssa.Value
	producer *ssa.Call
}

type coroCallableShadowBuilder struct {
	universe *EmissionUniverse
	result   *CoroCallableShadowAnalysis

	incoming map[*ssa.Function][]*ssa.Call
	escaped  map[*ssa.Function]bool
	closed   map[*ssa.Function]string

	facts    map[ssa.Value]map[*ssa.Call]CoroCallableShadow
	failures map[ssa.Value]string
	queue    []coroCallableShadowFactKey
	sinkABI  map[*ssa.Call]CoroCallableShadowABI
}

// AnalyzeCoroCallableShadows builds the compiler-side shadow flow from exact
// FuncPCABI0 producers to exact llgo.syscall consumers. The accepted transport
// is intentionally small: an SSA value may flow directly to the consumer or
// through uintptr parameters of closed, private, statically called Go
// functions. No integer operation, store, return, indirect call, exported
// entry, or escaped carrier preserves the shadow.
func AnalyzeCoroCallableShadows(universe *EmissionUniverse) (*CoroCallableShadowAnalysis, error) {
	if universe == nil {
		return nil, fmt.Errorf("callable shadow analysis requires a prepared emission universe")
	}
	b := &coroCallableShadowBuilder{
		universe: universe,
		result: &CoroCallableShadowAnalysis{
			producers: make(map[*ssa.Call]CoroCallableShadow),
			rejected:  make(map[*ssa.Call]string),
			sinks:     make(map[*ssa.Call]CoroCallableShadowSink),
		},
		incoming: make(map[*ssa.Function][]*ssa.Call),
		escaped:  make(map[*ssa.Function]bool),
		closed:   make(map[*ssa.Function]string),
		facts:    make(map[ssa.Value]map[*ssa.Call]CoroCallableShadow),
		failures: make(map[ssa.Value]string),
		sinkABI:  make(map[*ssa.Call]CoroCallableShadowABI),
	}
	b.indexCallsAndEscapes()
	if err := b.seedProducersAndSinks(); err != nil {
		return nil, err
	}
	if err := b.propagate(); err != nil {
		return nil, err
	}
	if err := b.finishSinks(); err != nil {
		return nil, err
	}
	return b.result, nil
}

func (b *coroCallableShadowBuilder) indexCallsAndEscapes() {
	for _, fn := range b.universe.functions {
		if fn == nil || len(fn.Blocks) == 0 || b.universe.canonicalAlias(fn) != fn {
			continue
		}
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				if _, debug := instruction.(*ssa.DebugRef); debug {
					continue
				}
				if call, ok := instruction.(*ssa.Call); ok && call.Common() != nil && !call.Common().IsInvoke() {
					if target, resolved := b.universe.Resolve(call.Common().StaticCallee()); resolved && target != nil {
						b.incoming[target] = append(b.incoming[target], call)
					}
				}
				for _, operand := range instruction.Operands(nil) {
					if operand == nil {
						continue
					}
					reference, ok := (*operand).(*ssa.Function)
					if !ok {
						continue
					}
					target, resolved := b.universe.Resolve(reference)
					if !resolved || target == nil {
						continue
					}
					call, direct := instruction.(*ssa.Call)
					if direct && call.Common() != nil && !call.Common().IsInvoke() {
						callee, calleeResolved := b.universe.Resolve(call.Common().StaticCallee())
						if calleeResolved && callee == target {
							continue
						}
					}
					b.escaped[target] = true
				}
			}
		}
	}
}

func (b *coroCallableShadowBuilder) seedProducersAndSinks() error {
	physicalTargets := make(map[string]CoroCallableShadow)
	for _, fn := range b.universe.functions {
		if fn == nil || len(fn.Blocks) == 0 || b.universe.canonicalAlias(fn) != fn {
			continue
		}
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(*ssa.Call)
				if !ok || call.Common() == nil || call.Common().IsInvoke() || call.Common().StaticCallee() == nil {
					continue
				}
				opcode, intrinsic, err := b.universe.coroIntrinsicOpcode(call.Common().StaticCallee())
				if err != nil {
					continue
				}
				if !intrinsic {
					continue
				}
				switch {
				case opcode == llgoFuncPCABI0:
					shadow, reason, err := b.injectProducer(call)
					if err != nil {
						return nilErrorWithCallableShadowContext(call, err)
					}
					if reason != "" {
						b.result.rejected[call] = reason
						b.failures[call] = reason
						continue
					}
					if previous, exists := physicalTargets[shadow.PhysicalSymbol]; exists &&
						(previous.Target != shadow.Target || previous.ABI != shadow.ABI ||
							previous.ForeignPointerResultMask != shadow.ForeignPointerResultMask ||
							previous.ContractCertificateID != shadow.ContractCertificateID ||
							previous.LegacyWorkerAddressCompat != shadow.LegacyWorkerAddressCompat) {
						return fmt.Errorf(
							"callable shadow analysis: physical target %q has conflicting producer targets or ABIs",
							shadow.PhysicalSymbol,
						)
					}
					physicalTargets[shadow.PhysicalSymbol] = shadow
					b.result.producers[call] = shadow
					b.addFact(call, shadow)
				case isLLGoSyscallIntrinsic(opcode):
					arity := len(call.Common().Args) - 1
					abi := CoroCallableShadowABI{Family: coroCallableShadowWorkerSyscallFamily, WordArgs: arity}
					b.sinkABI[call] = abi
					if err := validateCoroWorkerSyscallIntrinsicCallSite(call); err != nil {
						b.result.sinks[call] = CoroCallableShadowSink{Call: call, ABI: abi, Reason: "invalid-syscall-call-shape"}
					}
				}
			}
		}
	}
	return nil
}

func nilErrorWithCallableShadowContext(call *ssa.Call, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("callable shadow producer %q: %w", call.String(), err)
}

func (b *coroCallableShadowBuilder) injectProducer(call *ssa.Call) (CoroCallableShadow, string, error) {
	if err := b.universe.validateCoroFuncPCABI0CallSite(call); err != nil {
		return CoroCallableShadow{}, "invalid-funcpcabi0-operand", nil
	}
	args := call.Common().Args
	if len(args) != 1 {
		return CoroCallableShadow{}, "invalid-funcpcabi0-arity", nil
	}
	boxed, ok := args[0].(*ssa.MakeInterface)
	if !ok {
		return CoroCallableShadow{}, "dynamic-funcpcabi0-operand", nil
	}
	source, ok := boxed.X.(*ssa.Function)
	if !ok || source == nil || source.Parent() != nil || len(source.FreeVars) != 0 {
		return CoroCallableShadow{}, "dynamic-funcpcabi0-target", nil
	}
	target := b.universe.canonicalAlias(source)
	if target == nil || target.Parent() != nil || len(target.FreeVars) != 0 {
		return CoroCallableShadow{}, "uncanonical-funcpcabi0-target", nil
	}
	if b.universe.Contains(target) {
		background, classified, err := b.universe.FunctionBackground(target)
		if err != nil {
			return CoroCallableShadow{}, "", err
		}
		if classified && background == llssa.InGo {
			// FuncPCABI0 and FuncPCABIInternal are also Go runtime primitives for
			// publishing managed entry PCs (for example, map algorithm-table and
			// race-instrumentation callbacks). Such a producer is useful code-address
			// metadata, but it is not a foreign worker-call capability. Keep an exact
			// rejection on the producer so an unrelated publication cannot abort the
			// global shadow inventory while any path into llgo.syscall still fails
			// closed without a worker certificate.
			return CoroCallableShadow{}, "managed-go-code-address-is-not-worker-callable", nil
		}
	}
	capability, reason, err := coroWorkerCallableTarget(b.universe, source, target)
	if err != nil {
		return CoroCallableShadow{}, "", err
	}
	if reason != "" {
		return CoroCallableShadow{}, reason, nil
	}
	return CoroCallableShadow{
		Producer:                  call,
		SourceTarget:              source,
		Target:                    target,
		PhysicalSymbol:            capability.physicalSymbol,
		ForeignPointerResultMask:  capability.foreignPointerResultMask,
		ContractCertificateID:     capability.contractCertificateID,
		LegacyWorkerAddressCompat: capability.legacyWorkerAddressOnly,
		ABI: CoroCallableShadowABI{
			Family:   coroCallableShadowWorkerSyscallFamily,
			WordArgs: capability.workerArity,
		},
	}, "", nil
}

func (b *coroCallableShadowBuilder) addFact(value ssa.Value, shadow CoroCallableShadow) {
	if value == nil || shadow.Producer == nil {
		return
	}
	byProducer := b.facts[value]
	if byProducer == nil {
		byProducer = make(map[*ssa.Call]CoroCallableShadow)
		b.facts[value] = byProducer
	}
	if _, exists := byProducer[shadow.Producer]; exists {
		return
	}
	byProducer[shadow.Producer] = shadow
	b.queue = append(b.queue, coroCallableShadowFactKey{value: value, producer: shadow.Producer})
}

func (b *coroCallableShadowBuilder) propagate() error {
	for len(b.queue) != 0 {
		item := b.queue[0]
		b.queue = b.queue[1:]
		shadow, exists := b.facts[item.value][item.producer]
		if !exists {
			continue
		}
		refs := item.value.Referrers()
		if refs == nil {
			continue
		}
		for _, ref := range *refs {
			if _, debug := ref.(*ssa.DebugRef); debug {
				continue
			}
			call, isCall := ref.(*ssa.Call)
			if !isCall || call.Common() == nil || call.Common().IsInvoke() || call.Common().StaticCallee() == nil {
				reason := "callable-shadow-escape-or-unsupported-operation"
				if _, arithmetic := ref.(*ssa.BinOp); arithmetic {
					reason = "arithmetic-destroys-callable-shadow"
				}
				b.rejectDerivedValue(ref, reason)
				continue
			}
			indices := coroCallableShadowArgumentIndices(call, item.value)
			if len(indices) == 0 {
				continue
			}
			opcode, intrinsic, err := b.universe.coroIntrinsicOpcode(call.Common().StaticCallee())
			if err != nil {
				b.rejectDerivedValue(call, "callable-shadow-passed-outside-universe")
				continue
			}
			if intrinsic && isLLGoSyscallIntrinsic(opcode) {
				// The fact is consumed only from argument zero. Any other use is
				// deliberately not a callable transport.
				for _, index := range indices {
					if index != 0 {
						b.rejectDerivedValue(call, "callable-shadow-used-as-syscall-data")
					}
				}
				continue
			}
			if intrinsic {
				b.rejectDerivedValue(call, "callable-shadow-passed-to-intrinsic")
				continue
			}
			carrier, resolved := b.universe.Resolve(call.Common().StaticCallee())
			if !resolved || carrier == nil {
				b.rejectDerivedValue(call, "callable-shadow-passed-outside-universe")
				continue
			}
			closed, _, err := b.closedCarrier(carrier)
			if err != nil {
				return err
			}
			if !closed {
				continue
			}
			for _, index := range indices {
				if index < 0 || index >= len(carrier.Params) || !coroWorkerUintptrType(carrier.Params[index].Type()) {
					continue
				}
				b.addFact(carrier.Params[index], shadow)
			}
		}
	}
	return nil
}

func (b *coroCallableShadowBuilder) rejectDerivedValue(instruction ssa.Instruction, reason string) {
	value, ok := instruction.(ssa.Value)
	if !ok || value == nil {
		return
	}
	if _, exists := b.failures[value]; !exists {
		b.failures[value] = reason
	}
}

func coroCallableShadowArgumentIndices(call *ssa.Call, value ssa.Value) []int {
	if call == nil || call.Common() == nil || value == nil {
		return nil
	}
	var indices []int
	for index, argument := range call.Common().Args {
		if argument == value {
			indices = append(indices, index)
		}
	}
	return indices
}

func (b *coroCallableShadowBuilder) closedCarrier(fn *ssa.Function) (bool, string, error) {
	if reason, cached := b.closed[fn]; cached {
		return reason == "", reason, nil
	}
	reason := ""
	switch {
	case fn == nil || fn.Parent() != nil || len(fn.Blocks) == 0 || len(fn.FreeVars) != 0:
		reason = "open-or-escaped-parameter-carrier"
	case fn.Signature == nil || fn.Signature.Recv() != nil || fn.Signature.Variadic() ||
		fn.TypeParams() != nil || len(fn.TypeArgs()) != 0:
		reason = "open-or-escaped-parameter-carrier"
	case b.escaped[fn]:
		reason = "open-or-escaped-parameter-carrier"
	default:
		object, _ := fn.Object().(*types.Func)
		decl, _ := fn.Syntax().(*ast.FuncDecl)
		if object == nil || object.Exported() || decl == nil || decl.Body == nil {
			reason = "open-or-escaped-parameter-carrier"
		}
	}
	if reason == "" {
		background, classified, err := b.universe.FunctionBackground(fn)
		if err != nil {
			return false, "", err
		}
		if !classified || background != llssa.InGo {
			reason = "open-or-escaped-parameter-carrier"
		}
	}
	if reason == "" {
		directive, err := coroRawABIDirective(fn, b.universe)
		if err != nil {
			return false, "", err
		}
		if directive != "" {
			reason = "open-or-escaped-parameter-carrier"
		}
	}
	b.closed[fn] = reason
	return reason == "", reason, nil
}

func (b *coroCallableShadowBuilder) finishSinks() error {
	for call, abi := range b.sinkABI {
		if existing, invalid := b.result.sinks[call]; invalid && existing.Reason != "" {
			continue
		}
		sink := CoroCallableShadowSink{Call: call, ABI: abi}
		if call.Common() == nil || len(call.Common().Args) == 0 {
			sink.Reason = "invalid-syscall-call-shape"
			b.result.sinks[call] = sink
			continue
		}
		source := call.Common().Args[0]
		sink.Candidates = b.sortedFacts(source)
		if parameter, ok := source.(*ssa.Parameter); ok {
			incoming, certified, reason, err := b.parameterInventory(parameter, abi, make(map[*ssa.Parameter]bool))
			if err != nil {
				return err
			}
			sink.Incoming = incoming
			sink.Certified = certified
			sink.Reason = reason
		} else {
			sink.Certified = coroCallableShadowAllCompatible(sink.Candidates, abi)
			if !sink.Certified {
				sink.Reason = b.failureReason(source, abi)
			}
		}
		sortCoroCallableShadowIncoming(sink.Incoming)
		b.result.sinks[call] = sink
	}
	return nil
}

func (b *coroCallableShadowBuilder) parameterInventory(
	parameter *ssa.Parameter,
	abi CoroCallableShadowABI,
	visiting map[*ssa.Parameter]bool,
) ([]CoroCallableShadowIncomingEdge, bool, string, error) {
	if parameter == nil || parameter.Parent() == nil {
		return nil, false, "open-or-escaped-parameter-carrier", nil
	}
	if visiting[parameter] {
		return nil, false, "cyclic-parameter-carrier", nil
	}
	visiting[parameter] = true
	defer delete(visiting, parameter)

	owner := parameter.Parent()
	closed, closedReason, err := b.closedCarrier(owner)
	if err != nil {
		return nil, false, "", err
	}
	index := -1
	for candidateIndex, candidate := range owner.Params {
		if candidate == parameter {
			index = candidateIndex
			break
		}
	}
	if index < 0 || !closed {
		return b.openCarrierInventory(owner, index, abi, closedReason), false, closedReason, nil
	}
	calls := b.incoming[owner]
	if len(calls) == 0 {
		return nil, false, "parameter-carrier-has-no-static-incoming", nil
	}
	var inventory []CoroCallableShadowIncomingEdge
	anyCertified := false
	for _, call := range calls {
		edge := CoroCallableShadowIncomingEdge{Call: call, Carrier: owner, Parameter: index}
		if call == nil || call.Common() == nil || index >= len(call.Common().Args) {
			edge.Reason = "invalid-static-incoming-edge"
			inventory = append(inventory, edge)
			continue
		}
		source := call.Common().Args[index]
		edge.Candidates = b.sortedFacts(source)
		if upstream, ok := source.(*ssa.Parameter); ok {
			nested, nestedCertified, nestedReason, err := b.parameterInventory(upstream, abi, visiting)
			if err != nil {
				return nil, false, "", err
			}
			inventory = append(inventory, nested...)
			edge.Certified = nestedCertified && coroCallableShadowAnyCompatible(edge.Candidates, abi)
			if !edge.Certified {
				edge.Reason = nestedReason
			}
		} else {
			edge.Certified = coroCallableShadowAllCompatible(edge.Candidates, abi)
			if !edge.Certified {
				edge.Reason = b.failureReason(source, abi)
			}
		}
		if edge.Certified {
			anyCertified = true
		}
		inventory = append(inventory, edge)
	}
	if anyCertified {
		return inventory, true, "", nil
	}
	reason := "parameter-carrier-has-no-certified-incoming"
	if len(inventory) == 1 && inventory[0].Reason != "" {
		reason = inventory[0].Reason
	}
	return inventory, false, reason, nil
}

func (b *coroCallableShadowBuilder) openCarrierInventory(
	owner *ssa.Function,
	parameter int,
	abi CoroCallableShadowABI,
	reason string,
) []CoroCallableShadowIncomingEdge {
	if owner == nil || parameter < 0 {
		return nil
	}
	var inventory []CoroCallableShadowIncomingEdge
	for _, call := range b.incoming[owner] {
		edge := CoroCallableShadowIncomingEdge{
			Call:      call,
			Carrier:   owner,
			Parameter: parameter,
			Reason:    reason,
		}
		if call != nil && call.Common() != nil && parameter < len(call.Common().Args) {
			edge.Candidates = b.sortedFacts(call.Common().Args[parameter])
			if reason == "" && !coroCallableShadowAllCompatible(edge.Candidates, abi) {
				edge.Reason = b.failureReason(call.Common().Args[parameter], abi)
			}
		}
		inventory = append(inventory, edge)
	}
	return inventory
}

func (b *coroCallableShadowBuilder) sortedFacts(value ssa.Value) []CoroCallableShadow {
	byProducer := b.facts[value]
	shadows := make([]CoroCallableShadow, 0, len(byProducer))
	for _, shadow := range byProducer {
		shadows = append(shadows, shadow)
	}
	sort.SliceStable(shadows, func(i, j int) bool {
		return coroCallableShadowSortKey(shadows[i]) < coroCallableShadowSortKey(shadows[j])
	})
	return shadows
}

func (b *coroCallableShadowBuilder) failureReason(value ssa.Value, abi CoroCallableShadowABI) string {
	if reason := b.failures[value]; reason != "" {
		return reason
	}
	candidates := b.sortedFacts(value)
	if len(candidates) != 0 && !coroCallableShadowAllCompatible(candidates, abi) {
		return "callable-shadow-abi-mismatch"
	}
	if _, arithmetic := value.(*ssa.BinOp); arithmetic {
		return "arithmetic-destroys-callable-shadow"
	}
	if _, parameter := value.(*ssa.Parameter); parameter {
		return "parameter-carrier-has-no-certified-incoming"
	}
	return "missing-exact-callable-shadow"
}

func coroCallableShadowAnyCompatible(candidates []CoroCallableShadow, abi CoroCallableShadowABI) bool {
	for _, candidate := range candidates {
		if candidate.ABI == abi {
			return true
		}
	}
	return false
}

func coroCallableShadowAllCompatible(candidates []CoroCallableShadow, abi CoroCallableShadowABI) bool {
	if len(candidates) == 0 {
		return false
	}
	for _, candidate := range candidates {
		if candidate.ABI != abi {
			return false
		}
	}
	return true
}

func coroCallableShadowSortKey(shadow CoroCallableShadow) string {
	parent := ""
	block, instruction := -1, -1
	if shadow.Producer != nil {
		if shadow.Producer.Parent() != nil {
			parent = shadow.Producer.Parent().String()
		}
		block, instruction = coroWorkerSyscallInstructionSite(shadow.Producer)
	}
	target := ""
	if shadow.Target != nil {
		target = shadow.Target.String()
	}
	return fmt.Sprintf("%s/%08d/%08d/%s/%s/%08d", parent, block, instruction, target, shadow.PhysicalSymbol, shadow.ABI.WordArgs)
}

func sortCoroCallableShadowIncoming(edges []CoroCallableShadowIncomingEdge) {
	sort.SliceStable(edges, func(i, j int) bool {
		left, right := edges[i], edges[j]
		leftCarrier, rightCarrier := "", ""
		if left.Carrier != nil {
			leftCarrier = left.Carrier.String()
		}
		if right.Carrier != nil {
			rightCarrier = right.Carrier.String()
		}
		if leftCarrier != rightCarrier {
			return leftCarrier < rightCarrier
		}
		leftParent, rightParent := "", ""
		if left.Call != nil && left.Call.Parent() != nil {
			leftParent = left.Call.Parent().String()
		}
		if right.Call != nil && right.Call.Parent() != nil {
			rightParent = right.Call.Parent().String()
		}
		if leftParent != rightParent {
			return leftParent < rightParent
		}
		leftBlock, leftInstruction := coroWorkerSyscallInstructionSite(left.Call)
		rightBlock, rightInstruction := coroWorkerSyscallInstructionSite(right.Call)
		if leftBlock != rightBlock {
			return leftBlock < rightBlock
		}
		if leftInstruction != rightInstruction {
			return leftInstruction < rightInstruction
		}
		return left.Parameter < right.Parameter
	})
}

func cloneCoroCallableShadows(src []CoroCallableShadow) []CoroCallableShadow {
	return append([]CoroCallableShadow(nil), src...)
}

func cloneCoroCallableShadowIncoming(src []CoroCallableShadowIncomingEdge) []CoroCallableShadowIncomingEdge {
	dst := make([]CoroCallableShadowIncomingEdge, len(src))
	for index, edge := range src {
		edge.Candidates = cloneCoroCallableShadows(edge.Candidates)
		dst[index] = edge
	}
	return dst
}
