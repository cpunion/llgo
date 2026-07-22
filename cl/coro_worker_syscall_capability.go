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

// CoroWorkerSyscallCertificate freezes both capabilities required before an
// llgo.syscall function word may cross to a native worker:
//
//   - the producer-forward callable shadow remains exact through every private
//     carrier edge;
//   - every target owns a generic callable contract or legacy workeraddr
//     compatibility contract with the exact word-call ABI.
//
// ID binds the exact call occurrence, target physical-symbol set, worker word
// ABI, target layout, and every private parameter owner traversed by the shadow.
// The diagnostic fields are not capabilities; consumers must compare ID.
type CoroWorkerSyscallCertificate struct {
	ID                       string
	WorkerABISignature       string
	PhysicalTargetSetID      string
	CallableShadowSetID      string
	StaticTargetCount        int
	ForeignPointerResultMask uint8
}

type coroWorkerAddressTarget struct {
	target                   *ssa.Function
	physicalSymbol           string
	workerArity              int
	foreignPointerResultMask uint8
	contractCertificateID    string
	legacyWorkerAddressOnly  bool
}

// coroWorkerSyscallIncomingEdge is one exact, frozen static call into a
// private function-word carrier. Certified says that the producer-forward
// shadow on the edge has the required callable ABI. An
// uncertified edge does not invalidate the conditional universe certificate:
// the final SSA-plan join requires its caller to have EmitNone. This lets one
// standard-library carrier serve a demanded safe wrapper while every unused
// fork/exec/thread-affine wrapper remains fail-closed.
type coroWorkerSyscallIncomingEdge struct {
	call                     *ssa.Call
	carrier                  *ssa.Function
	parameter                int
	certified                bool
	reason                   string
	targetKeys               []string
	foreignPointerResultMask uint8
	resultProjectionID       string
	stableIdentity           string
}

type coroWorkerSyscallIncomingKey struct {
	call      *ssa.Call
	carrier   *ssa.Function
	parameter int
}

// coroSelectPatchedWorkerAddressTrampoline makes an alternate-package
// workeraddr declaration participate in ordinary managed-symbol selection.
// Upstream Darwin FuncPCABI0 operands still point at the original SSA
// declaration; selecting the same-name/same-ABI alternate first lets the
// existing exact C-symbol winner logic install the canonical alias when that
// operand is materialized. No unannotated trampoline is selected or inferred.
func coroSelectPatchedWorkerAddressTrampoline(fn *ssa.Function, fromPatch bool) (bool, error) {
	if !fromPatch || fn == nil {
		return false, nil
	}
	directive, err := coroForeignCallDirectiveFor(fn)
	if err != nil {
		return false, err
	}
	if directive == coroForeignCallWorkerAddress {
		return true, nil
	}
	_, generic, err := coroWorkerCallableDeclarationContractArity(fn)
	return generic, err
}

// aliasPatchedWorkerAddressTrampolines validates patch-owned workeraddr
// declarations and, when an upstream declaration of the same name exists,
// connects that upstream FuncPCABI0 operand to the certified alternate.
// FuncPCABI0 intentionally synthesizes C addresses without materializing
// trampoline SSA declarations, so ordinary reachability-driven patch aliasing
// cannot establish this bridge. A patch may also introduce a new fixed C
// adapter used only by patch code; that form has no upstream alias to install
// but is held to the same frozen symbol, declaration, and arity constraints.
func (u *EmissionUniverse) aliasPatchedWorkerAddressTrampolines() error {
	if u == nil || !u.CoroWorkerEnabled() {
		return nil
	}
	packages := make([]*preparedEmissionPackage, 0, len(u.packages))
	for _, prepared := range u.packages {
		if prepared != nil && prepared.hasPatch && !prepared.metadataOnly {
			packages = append(packages, prepared)
		}
	}
	sort.SliceStable(packages, func(i, j int) bool {
		if packages[i].order != packages[j].order {
			return packages[i].order < packages[j].order
		}
		return packages[i].identity < packages[j].identity
	})
	for _, prepared := range packages {
		names := make([]string, 0, len(prepared.patch.Alt.Members))
		for name := range prepared.patch.Alt.Members {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			alternate, ok := prepared.patch.Alt.Members[name].(*ssa.Function)
			if !ok || !strings.HasSuffix(name, "_trampoline") {
				continue
			}
			directive, err := coroForeignCallDirectiveFor(alternate)
			if err != nil {
				return fmt.Errorf("prepare emission universe: patch worker-address target %q: %w", name, err)
			}
			legacy := directive == coroForeignCallWorkerAddress
			_, generic, err := coroWorkerCallableDeclarationContractArity(alternate)
			if err != nil {
				return fmt.Errorf("prepare emission universe: patch worker callable target %q: %w", name, err)
			}
			if !legacy && !generic {
				continue
			}
			if !coroWorkerAddressAliasDeclaration(alternate) {
				return fmt.Errorf(
					"prepare emission universe: patched workeraddr target %q requires an exact bodyless non-method alternate declaration",
					name,
				)
			}
			physical := remapTrampolineCNameForTarget(u.prog.Target(), extractTrampolineCName(name))
			if physical == "" {
				return fmt.Errorf("prepare emission universe: patched workeraddr target %q has no physical trampoline symbol", name)
			}
			ownerKey := emissionFunctionOwnerKey{function: alternate, owner: prepared}
			kind, kindOK := u.functionKinds[ownerKey]
			finalKey, keyOK := u.finalKeys[ownerKey]
			finalKind, finalSymbol, _, keyValid := splitManagedSymbolKey(finalKey)
			if !kindOK || kind != cFunc || !keyOK || !keyValid || finalKind != cFunc || finalSymbol != physical {
				return fmt.Errorf(
					"prepare emission universe: patched workeraddr target %q must explicitly link to physical C symbol %q",
					name, physical,
				)
			}
			if canonical := u.canonicalAlias(alternate); canonical == nil || canonical != alternate {
				return fmt.Errorf("prepare emission universe: patched workeraddr target %q is not its exact canonical declaration", name)
			}
			if _, required := u.required[alternate]; !required {
				return fmt.Errorf("prepare emission universe: patched workeraddr target %q is absent from the frozen universe", name)
			}
			originalMember, exists := prepared.ssa.Members[name]
			if !exists {
				// Patch-private fixed adapters are already canonical physical
				// targets. There is intentionally no upstream SSA identity to
				// redirect; calls in the alternate package refer to this exact
				// declaration.
				continue
			}
			original, ok := originalMember.(*ssa.Function)
			if !ok || !coroWorkerAddressAliasDeclaration(original) {
				return fmt.Errorf(
					"prepare emission universe: patched workeraddr target %q requires an exact bodyless non-method original declaration when the upstream name exists",
					name,
				)
			}
			if structuralGoLinknameABITypeKey(original.Signature) != structuralGoLinknameABITypeKey(alternate.Signature) {
				return fmt.Errorf("prepare emission universe: patched workeraddr target %q changes the upstream trampoline ABI", name)
			}
			if canonical := u.canonicalAlias(original); canonical == nil || canonical != original {
				return fmt.Errorf("prepare emission universe: upstream workeraddr target %q already has a conflicting canonical alias", name)
			}
			u.aliases[original] = alternate
			u.fnOwners[original] = prepared
		}
	}
	return nil
}

func coroWorkerAddressAliasDeclaration(fn *ssa.Function) bool {
	if fn == nil || fn.Parent() != nil || len(fn.FreeVars) != 0 || fn.Signature == nil ||
		fn.Signature.Recv() != nil || fn.Signature.Variadic() || fn.TypeParams() != nil ||
		len(fn.TypeArgs()) != 0 || len(fn.Blocks) != 0 {
		return false
	}
	decl, _ := fn.Syntax().(*ast.FuncDecl)
	return decl != nil && decl.Body == nil && decl.Recv == nil
}

// freezeCoroWorkerSyscallCertificates runs after frontend identities and
// aliases are immutable. Unsupported call sites deliberately remain ordinary
// synchronous intrinsics; a physical coroutine cannot elide/lower them.
func (u *EmissionUniverse) freezeCoroWorkerSyscallCertificates() error {
	if u == nil || !u.CoroWorkerEnabled() {
		return nil
	}
	shadows, err := AnalyzeCoroCallableShadows(u)
	if err != nil {
		return fmt.Errorf("prepare emission universe: freeze producer-forward callable shadows: %w", err)
	}
	for _, fn := range u.functions {
		if fn == nil || len(fn.Blocks) == 0 || u.canonicalAlias(fn) != fn {
			continue
		}
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(*ssa.Call)
				if !ok || call.Common() == nil || call.Common().IsInvoke() {
					continue
				}
				callee := call.Common().StaticCallee()
				opcode, intrinsic, err := u.coroIntrinsicOpcode(callee)
				if err != nil || !intrinsic || !isLLGoSyscallIntrinsic(opcode) {
					continue
				}
				if err := validateCoroWorkerSyscallIntrinsicCallSite(call); err != nil {
					continue
				}
				shadow, observed := shadows.Sink(call)
				if !observed || !shadow.Certified {
					// No producer-forward shadow means no worker authority.
					continue
				}
				certificate, owners, incoming, err := freezeCoroWorkerSyscallShadowCertificate(u, call, opcode, shadow)
				if err != nil {
					return fmt.Errorf("prepare emission universe: worker llgo.syscall call %q: %w", call.String(), err)
				}
				u.workerSyscalls[call] = certificate
				u.workerSyscallOwners[call] = owners
				u.workerSyscallIncoming[call] = incoming
			}
		}
	}
	return nil
}

func coroWorkerAddressFunctionIdentity(universe *EmissionUniverse, fn *ssa.Function) string {
	if fn == nil {
		return framedEmissionKey("llgo-coro-worker-address-function-v0", "<nil>")
	}
	pkgPath := ""
	provenance := "synthetic"
	if fn.Pkg != nil && fn.Pkg.Pkg != nil {
		pkgPath = llssa.PathOf(fn.Pkg.Pkg)
		provenance = "original"
		if universe != nil {
			if owner := universe.ownerOf(fn); owner != nil && owner.hasPatch && fn.Pkg == owner.patch.Alt {
				provenance = "alternate-patch"
			}
		}
	}
	signature := ""
	if fn.Signature != nil {
		signature = structuralGoLinknameABITypeKey(fn.Signature)
	}
	return framedEmissionKey(
		"llgo-coro-worker-address-function-v0",
		pkgPath,
		fn.Name(),
		signature,
		provenance,
	)
}

func coroWorkerAddressDirectiveArity(fn *ssa.Function) (int, error) {
	decl, _ := fn.Syntax().(*ast.FuncDecl)
	if decl == nil || decl.Doc == nil {
		return 0, fmt.Errorf("//llgo:coro workeraddr target %q has no attached directive", fn.Name())
	}
	for _, comment := range decl.Doc.List {
		if comment == nil {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(comment.Text), "//"))
		fields := strings.Fields(payload)
		if len(fields) != 3 || fields[0] != "llgo:coro" || fields[1] != "workeraddr" {
			continue
		}
		arity, err := strconv.Atoi(fields[2])
		if err != nil || arity < 0 || arity > coroWorkerMaxArgsV1 {
			return 0, fmt.Errorf("//llgo:coro workeraddr target %q has invalid arity %q", fn.Name(), fields[2])
		}
		return arity, nil
	}
	return 0, fmt.Errorf("//llgo:coro workeraddr target %q has no exact arity", fn.Name())
}

func coroWorkerCallableTargetSetKey(universe *EmissionUniverse, target coroWorkerAddressTarget) string {
	identity := ""
	if universe != nil && target.target != nil {
		identity = universe.finalIdentity(target.target)
	}
	return framedEmissionKey(
		"llgo-coro-worker-callable-target-set-entry-v1",
		identity,
		target.physicalSymbol,
		strconv.Itoa(target.workerArity),
		strconv.FormatUint(uint64(target.foreignPointerResultMask), 10),
		target.contractCertificateID,
		strconv.FormatBool(target.legacyWorkerAddressOnly),
	)
}

func coroWorkerCallableShadowTarget(shadow CoroCallableShadow) coroWorkerAddressTarget {
	return coroWorkerAddressTarget{
		target:                   shadow.Target,
		physicalSymbol:           shadow.PhysicalSymbol,
		workerArity:              shadow.ABI.WordArgs,
		foreignPointerResultMask: shadow.ForeignPointerResultMask,
		contractCertificateID:    shadow.ContractCertificateID,
		legacyWorkerAddressOnly:  shadow.LegacyWorkerAddressCompat,
	}
}

func coroWorkerCallableCompatibleShadowTargets(
	universe *EmissionUniverse,
	candidates []CoroCallableShadow,
	abi CoroCallableShadowABI,
) map[string]none {
	targets := make(map[string]none)
	for _, candidate := range candidates {
		if candidate.ABI != abi {
			continue
		}
		targets[coroWorkerCallableTargetSetKey(universe, coroWorkerCallableShadowTarget(candidate))] = none{}
	}
	return targets
}

// freezeCoroWorkerSyscallShadowCertificate materializes the final worker
// certificate inventory directly from producer-forward facts. No consumer
// value is walked backwards and no emitted address is inspected.
func freezeCoroWorkerSyscallShadowCertificate(
	universe *EmissionUniverse,
	call *ssa.Call,
	opcode int,
	shadow CoroCallableShadowSink,
) (CoroWorkerSyscallCertificate, map[*ssa.Function]none, []coroWorkerSyscallIncomingEdge, error) {
	if universe == nil || call == nil || call.Parent() == nil || shadow.Call != call || !shadow.Certified {
		return CoroWorkerSyscallCertificate{}, nil, nil, fmt.Errorf("producer-forward callable shadow is absent or uncertified")
	}
	if shadow.ABI.Family != coroCallableShadowWorkerSyscallFamily ||
		shadow.ABI.WordArgs != len(call.Common().Args)-1 {
		return CoroWorkerSyscallCertificate{}, nil, nil, fmt.Errorf("producer-forward callable shadow ABI differs from worker syscall")
	}
	parent := universe.canonicalAlias(call.Parent())
	if parent == nil || parent != call.Parent() {
		return CoroWorkerSyscallCertificate{}, nil, nil, fmt.Errorf("worker syscall owner is not canonical")
	}
	linkIdentity := universe.linkIdentities[parent]
	if linkIdentity == "" {
		return CoroWorkerSyscallCertificate{}, nil, nil, fmt.Errorf("worker syscall owner %q has no frozen link identity", parent.Name())
	}

	targetSet := coroWorkerCallableCompatibleShadowTargets(universe, shadow.Candidates, shadow.ABI)
	if len(targetSet) == 0 {
		return CoroWorkerSyscallCertificate{}, nil, nil, fmt.Errorf("producer-forward callable shadow has no compatible target")
	}
	for _, candidate := range shadow.Candidates {
		if candidate.ABI != shadow.ABI {
			continue
		}
		if candidate.Producer == nil || candidate.Target == nil || candidate.PhysicalSymbol == "" ||
			candidate.ContractCertificateID == "" || universe.canonicalAlias(candidate.Target) != candidate.Target {
			return CoroWorkerSyscallCertificate{}, nil, nil, fmt.Errorf("producer-forward callable shadow has an incomplete target")
		}
		exact, reason, err := coroWorkerCallableTarget(universe, candidate.SourceTarget, candidate.Target)
		if err != nil {
			return CoroWorkerSyscallCertificate{}, nil, nil, err
		}
		if reason != "" || exact != coroWorkerCallableShadowTarget(candidate) {
			return CoroWorkerSyscallCertificate{}, nil, nil, fmt.Errorf("callable shadow target differs from its exact producer contract")
		}
	}
	targetKeys := sortedCoroWorkerStringSet(targetSet)
	targetSetID := framedEmissionKey(append([]string{"llgo-coro-worker-callable-target-set-v1"}, targetKeys...)...)
	foreignPointerResultMask := uint8(^uint8(0))
	compatibleTargets := 0
	for _, candidate := range shadow.Candidates {
		if candidate.ABI != shadow.ABI {
			continue
		}
		foreignPointerResultMask &= candidate.ForeignPointerResultMask
		compatibleTargets++
	}
	if compatibleTargets == 0 {
		return CoroWorkerSyscallCertificate{}, nil, nil, fmt.Errorf("producer-forward callable shadow has no compatible result contract")
	}

	owners := make(map[*ssa.Function]none)
	edgeSet := make(map[coroWorkerSyscallIncomingKey]none)
	incoming := make([]coroWorkerSyscallIncomingEdge, 0, len(shadow.Incoming))
	certifiedIncoming := 0
	for _, edge := range shadow.Incoming {
		key := coroWorkerSyscallIncomingKey{call: edge.Call, carrier: edge.Carrier, parameter: edge.Parameter}
		if edge.Call == nil || edge.Carrier == nil || edge.Parameter < 0 {
			return CoroWorkerSyscallCertificate{}, nil, nil, fmt.Errorf("producer-forward callable shadow has an incomplete incoming edge")
		}
		if _, duplicate := edgeSet[key]; duplicate {
			return CoroWorkerSyscallCertificate{}, nil, nil, fmt.Errorf("producer-forward callable shadow has a duplicate incoming edge")
		}
		edgeSet[key] = none{}
		edgeTargets := coroWorkerCallableCompatibleShadowTargets(universe, edge.Candidates, shadow.ABI)
		for target := range edgeTargets {
			if _, belongs := targetSet[target]; !belongs {
				return CoroWorkerSyscallCertificate{}, nil, nil, fmt.Errorf("incoming edge target is absent from the callable shadow target set")
			}
		}
		frozen := coroWorkerSyscallIncomingEdge{
			call:       edge.Call,
			carrier:    edge.Carrier,
			parameter:  edge.Parameter,
			certified:  edge.Certified,
			reason:     edge.Reason,
			targetKeys: sortedCoroWorkerStringSet(edgeTargets),
		}
		edgeForeignPointerMask := uint8(^uint8(0))
		edgeCompatibleTargets := 0
		for _, candidate := range edge.Candidates {
			if candidate.ABI != shadow.ABI {
				continue
			}
			edgeForeignPointerMask &= candidate.ForeignPointerResultMask
			edgeCompatibleTargets++
		}
		if edgeCompatibleTargets == 0 {
			edgeForeignPointerMask = 0
		}
		if projection, ok := universe.workerResultProjections[edge.Carrier]; ok &&
			projection.functionParameter == edge.Parameter {
			frozen.resultProjectionID = projection.id
			for wrapperResult, workerResult := range projection.resultToWorker {
				if workerResult >= 0 && edgeForeignPointerMask&(uint8(1)<<uint(workerResult)) != 0 {
					frozen.foreignPointerResultMask |= uint8(1) << uint(wrapperResult)
				}
			}
		}
		if frozen.certified {
			if len(frozen.targetKeys) == 0 {
				return CoroWorkerSyscallCertificate{}, nil, nil, fmt.Errorf("certified callable shadow incoming edge has no compatible target")
			}
			frozen.reason = ""
			certifiedIncoming++
		} else if frozen.reason == "" {
			frozen.reason = "unspecified"
		}
		identity, err := coroWorkerSyscallIncomingIdentity(universe, frozen)
		if err != nil {
			return CoroWorkerSyscallCertificate{}, nil, nil, err
		}
		frozen.stableIdentity = identity
		incoming = append(incoming, frozen)
		owners[edge.Carrier] = none{}
	}
	if len(incoming) != 0 && certifiedIncoming == 0 {
		return CoroWorkerSyscallCertificate{}, nil, nil, fmt.Errorf("callable shadow has no certified incoming edge")
	}
	sort.SliceStable(incoming, func(i, j int) bool { return incoming[i].stableIdentity < incoming[j].stableIdentity })
	incomingKeys := make([]string, len(incoming))
	for index, edge := range incoming {
		incomingKeys[index] = edge.stableIdentity
	}
	incomingSetID := framedEmissionKey(append([]string{"llgo-coro-worker-static-incoming-set-v1"}, incomingKeys...)...)
	ownerKeys := make([]string, 0, len(owners))
	for owner := range owners {
		identity := universe.linkIdentities[owner]
		if identity == "" {
			return CoroWorkerSyscallCertificate{}, nil, nil, fmt.Errorf("worker syscall parameter owner %q has no frozen link identity", owner.Name())
		}
		ownerKeys = append(ownerKeys, identity)
	}
	sort.Strings(ownerKeys)

	shadowSetID := coroWorkerCallableShadowSetID(universe, shadow)
	if shadowSetID == "" {
		return CoroWorkerSyscallCertificate{}, nil, nil, fmt.Errorf("producer-forward callable shadow has no stable identity")
	}
	blockIndex, instructionIndex := coroWorkerSyscallInstructionSite(call)
	workerABI := framedEmissionKey(
		"llgo-coro-worker-syscall-word-abi-v1",
		strconv.Itoa(universe.prog.PointerSize()*8),
		strconv.Itoa(len(call.Common().Args)-1),
		strconv.Itoa(opcode),
	)
	target := universe.prog.TargetSpec()
	fields := []string{
		"llgo-coro-worker-syscall-call-v1",
		linkIdentity,
		strconv.Itoa(blockIndex),
		strconv.Itoa(instructionIndex),
		workerABI,
		targetSetID,
		incomingSetID,
		shadowSetID,
		target.Triple,
		target.CPU,
		target.Features,
		target.TargetABI,
		universe.prog.DataLayout(),
	}
	fields = append(fields, ownerKeys...)
	return CoroWorkerSyscallCertificate{
		ID:                       framedEmissionKey(fields...),
		WorkerABISignature:       workerABI,
		PhysicalTargetSetID:      targetSetID,
		CallableShadowSetID:      shadowSetID,
		StaticTargetCount:        len(targetKeys),
		ForeignPointerResultMask: foreignPointerResultMask,
	}, owners, incoming, nil
}

func coroWorkerCallableShadowSetID(universe *EmissionUniverse, shadow CoroCallableShadowSink) string {
	if universe == nil || shadow.Call == nil || shadow.Call.Parent() == nil {
		return ""
	}
	fields := []string{"llgo-coro-callable-shadow-set-v1"}
	ownerIdentity := universe.linkIdentities[shadow.Call.Parent()]
	block, instruction := coroWorkerSyscallInstructionSite(shadow.Call)
	fields = append(fields, ownerIdentity, strconv.Itoa(block), strconv.Itoa(instruction),
		shadow.ABI.Family, strconv.Itoa(shadow.ABI.WordArgs))
	producerKeys := make([]string, 0, len(shadow.Candidates))
	for _, candidate := range shadow.Candidates {
		producerKeys = append(producerKeys, coroWorkerCallableShadowProducerKey(universe, candidate))
	}
	sort.Strings(producerKeys)
	fields = append(fields, producerKeys...)
	edgeKeys := make([]string, 0, len(shadow.Incoming))
	for _, edge := range shadow.Incoming {
		callerIdentity, carrierIdentity := "", ""
		block, instruction := -1, -1
		if edge.Call != nil {
			callerIdentity = universe.linkIdentities[edge.Call.Parent()]
			block, instruction = coroWorkerSyscallInstructionSite(edge.Call)
		}
		if edge.Carrier != nil {
			carrierIdentity = universe.linkIdentities[edge.Carrier]
		}
		candidateKeys := make([]string, 0, len(edge.Candidates))
		for _, candidate := range edge.Candidates {
			candidateKeys = append(candidateKeys, coroWorkerCallableShadowProducerKey(universe, candidate))
		}
		sort.Strings(candidateKeys)
		edgeFields := []string{
			"llgo-coro-callable-shadow-incoming-v1",
			callerIdentity, strconv.Itoa(block), strconv.Itoa(instruction),
			carrierIdentity, strconv.Itoa(edge.Parameter),
			strconv.FormatBool(edge.Certified), edge.Reason,
		}
		edgeFields = append(edgeFields, candidateKeys...)
		edgeKeys = append(edgeKeys, framedEmissionKey(edgeFields...))
	}
	sort.Strings(edgeKeys)
	fields = append(fields, edgeKeys...)
	return emissionDigest(framedEmissionKey(fields...))
}

func sortedCoroWorkerStringSet(values map[string]none) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func coroWorkerSyscallIncomingIdentity(universe *EmissionUniverse, edge coroWorkerSyscallIncomingEdge) (string, error) {
	if universe == nil || edge.call == nil || edge.call.Parent() == nil || edge.carrier == nil || edge.parameter < 0 {
		return "", fmt.Errorf("incomplete worker syscall static incoming edge")
	}
	caller := universe.canonicalAlias(edge.call.Parent())
	carrier := universe.canonicalAlias(edge.carrier)
	if caller == nil || caller != edge.call.Parent() || carrier == nil || carrier != edge.carrier {
		return "", fmt.Errorf("worker syscall static incoming edge is not canonically owned")
	}
	callerIdentity := universe.linkIdentities[caller]
	carrierIdentity := universe.linkIdentities[carrier]
	if callerIdentity == "" || carrierIdentity == "" {
		return "", fmt.Errorf("worker syscall static incoming edge has no frozen function identity")
	}
	block, instruction := coroWorkerSyscallInstructionSite(edge.call)
	if block < 0 || instruction < 0 {
		return "", fmt.Errorf("worker syscall static incoming edge has no exact SSA site")
	}
	status, reason := "uncertified", edge.reason
	if edge.certified {
		status, reason = "certified", ""
	} else if reason == "" {
		reason = "unspecified"
	}
	fields := []string{
		"llgo-coro-worker-static-incoming-v1",
		callerIdentity, strconv.Itoa(block), strconv.Itoa(instruction),
		carrierIdentity, strconv.Itoa(edge.parameter), status, reason,
		edge.resultProjectionID,
		strconv.FormatUint(uint64(edge.foreignPointerResultMask), 10),
	}
	targetKeys := append([]string(nil), edge.targetKeys...)
	sort.Strings(targetKeys)
	fields = append(fields, targetKeys...)
	return framedEmissionKey(fields...), nil
}

func coroWorkerCallableShadowProducerKey(universe *EmissionUniverse, shadow CoroCallableShadow) string {
	parentIdentity := ""
	block, instruction := -1, -1
	if universe != nil && shadow.Producer != nil {
		parentIdentity = universe.linkIdentities[shadow.Producer.Parent()]
		block, instruction = coroWorkerSyscallInstructionSite(shadow.Producer)
	}
	sourceIdentity := ""
	if shadow.SourceTarget != nil {
		sourceIdentity = coroWorkerAddressFunctionIdentity(universe, shadow.SourceTarget)
	}
	return framedEmissionKey(
		"llgo-coro-callable-shadow-producer-v1",
		parentIdentity, strconv.Itoa(block), strconv.Itoa(instruction), sourceIdentity,
		coroWorkerCallableTargetSetKey(universe, coroWorkerCallableShadowTarget(shadow)),
	)
}

func coroWorkerSyscallInstructionSite(call *ssa.Call) (int, int) {
	if call == nil || call.Parent() == nil {
		return -1, -1
	}
	for _, block := range call.Parent().Blocks {
		semantic := 0
		for _, instruction := range block.Instrs {
			if _, debug := instruction.(*ssa.DebugRef); debug {
				continue
			}
			if instruction == call {
				return block.Index, semantic
			}
			semantic++
		}
	}
	return -1, -1
}

// CoroWorkerSyscallCertificate returns the immutable exact-call certificate
// from ProgramIR. Absence is an ordinary synchronous llgo.syscall site, never
// worker authority.
func (u *EmissionUniverse) CoroWorkerSyscallCertificate(
	call ssa.CallInstruction,
) (certificate CoroWorkerSyscallCertificate, certified bool, err error) {
	if u == nil || u.coroProgramIR == nil {
		return certificate, false, fmt.Errorf("coroutine worker syscall certificate: emission universe has no ProgramIR")
	}
	frozen, found, err := u.coroProgramIR.callSitePlan(call)
	if err != nil || !found {
		return certificate, false, err
	}
	if !isLLGoSyscallIntrinsic(frozen.opcode) {
		return certificate, false, nil
	}
	if frozen.failure != "" {
		return certificate, false, fmt.Errorf("%s", frozen.failure)
	}
	return frozen.workerCertificate, frozen.workerCertified, nil
}

// validateCoroWorkerSyscallCall is the final plan/universe join used by both
// physical preflight and codegen. A builder cannot forge worker authority by
// merely marking an intrinsic call elided.
func validateCoroWorkerSyscallCall(plan *coro.SSAPlan, universe *EmissionUniverse, call *ssa.Call) error {
	if plan == nil || universe == nil || call == nil {
		return fmt.Errorf("worker llgo.syscall requires an exact plan, emission universe, and direct call")
	}
	frozen, found, err := universe.coroProgramIR.callSitePlan(call)
	if err != nil || !found {
		if err == nil {
			err = fmt.Errorf("worker llgo.syscall call is absent from ProgramIR")
		}
		return err
	}
	if frozen.failure != "" {
		return fmt.Errorf("worker llgo.syscall has an invalid frozen SitePlan: %s", frozen.failure)
	}
	certificate := frozen.workerCertificate
	if !frozen.workerCertified || certificate.ID == "" {
		return fmt.Errorf("worker llgo.syscall function word has no frozen static target capability")
	}
	plannedCertificate, planned := plan.ElidedCallCertificate(call)
	if !planned || plannedCertificate != certificate.ID || !plan.ElidesCall(call) {
		return fmt.Errorf("worker llgo.syscall exact call certificate disagrees with the frozen SSA plan")
	}
	owners := frozen.workerOwners
	for _, root := range plan.Roots() {
		if _, parameterEntry := owners[root.Function]; parameterEntry {
			return fmt.Errorf("worker llgo.syscall parameter owner %q is an externally established entry", root.ID)
		}
	}
	for owner := range owners {
		ownerPlan, ok := plan.FunctionPlan(owner)
		managedOwner := ok && ownerPlan.External == coro.Defined && ownerPlan.ManagedDemand != coro.NoDemand &&
			ownerPlan.Emission == coro.EmitCoroutine && ownerPlan.Primary == coro.PrimaryCoroutine &&
			ownerPlan.FuncRep == coro.DirectCoro && (!ownerPlan.RawPlainDemand ||
			coroWorkerHasExactRawPlainVariant(plan, owner, ownerPlan))
		rawOwner := ok && coroWorkerHasExactRawPlainOnly(plan, owner, ownerPlan)
		if !managedOwner && !rawOwner {
			return fmt.Errorf("worker llgo.syscall parameter owner %q has no closed direct managed plan", owner.Name())
		}
	}
	for _, edge := range frozen.workerIncoming {
		if edge.call == nil || edge.call.Parent() == nil || edge.carrier == nil || edge.stableIdentity == "" {
			return fmt.Errorf("worker llgo.syscall has an incomplete frozen static incoming edge")
		}
		callerPlan, callerPlanned := plan.FunctionPlan(edge.call.Parent())
		if !callerPlanned {
			return fmt.Errorf(
				"worker llgo.syscall static incoming caller %q is absent from the frozen SSA plan",
				edge.call.Parent().Name(),
			)
		}
		if callerPlan.Emission == coro.EmitNone {
			// Conditional certificates deliberately retain every static wrapper
			// edge. An unused wrapper cannot supply a runtime function word and is
			// the only case in which an uncertified edge may be ignored.
			continue
		}
		carrierPlan, carrierPlanned := plan.FunctionPlan(edge.carrier)
		callPlan, callPlanned := plan.CallPlan(edge.call)
		if coroWorkerHasExactRawPlainOnly(plan, edge.call.Parent(), callerPlan) {
			// A raw-only wrapper executes the carrier's independently emitted
			// legacy-stack body. It cannot enqueue a worker request, so the
			// worker-address certificate is intentionally irrelevant on this
			// physical path. Keep this exception tied to both exact SSA objects:
			// the caller must have a closed direct CallPlan and the carrier must
			// have the frozen raw-plain variant that codegen will call. CallPlan
			// records the logical primary, so a mixed carrier remains DirectCoro;
			// rawPlainBody selects its separately proven plain variant at codegen.
			if !carrierPlanned || !coroWorkerHasExactRawPlainVariant(plan, edge.carrier, carrierPlan) ||
				!callPlanned || callPlan.Kind != coro.CallDirect || callPlan.Rep != carrierPlan.FuncRep ||
				callPlan.Open || callPlan.MayBeNil || len(callPlan.Targets) != 1 || callPlan.Targets[0] != carrierPlan.ID {
				return fmt.Errorf(
					"worker llgo.syscall raw-only static incoming edge %q disagrees with the frozen raw-compatible CallPlan or carrier variant (caller=%+v carrier=%+v carrier-variant=%t call=%+v)",
					edge.stableIdentity, callerPlan, carrierPlan,
					carrierPlanned && coroWorkerHasExactRawPlainVariant(plan, edge.carrier, carrierPlan), callPlan,
				)
			}
			continue
		}
		if callerPlan.External != coro.Defined || callerPlan.ManagedDemand == coro.NoDemand ||
			callerPlan.Emission != coro.EmitCoroutine || callerPlan.Primary != coro.PrimaryCoroutine ||
			(callerPlan.RawPlainDemand && !coroWorkerHasExactRawPlainVariant(plan, edge.call.Parent(), callerPlan)) {
			return fmt.Errorf(
				"worker llgo.syscall static incoming caller %q has raw, external, or non-coroutine reachability",
				edge.call.Parent().Name(),
			)
		}
		if !edge.certified {
			return fmt.Errorf(
				"worker llgo.syscall active static incoming edge %q is uncertified (%s; caller=%+v carrier=%+v carrier-variant=%t call=%+v)",
				edge.stableIdentity, edge.reason, callerPlan, carrierPlan,
				carrierPlanned && coroWorkerHasExactRawPlainVariant(plan, edge.carrier, carrierPlan), callPlan,
			)
		}
		if !carrierPlanned || !callPlanned || callPlan.Kind != coro.CallDirect || callPlan.Rep != coro.DirectCoro ||
			callPlan.Open || callPlan.MayBeNil || len(callPlan.Targets) != 1 || callPlan.Targets[0] != carrierPlan.ID {
			return fmt.Errorf(
				"worker llgo.syscall certified static incoming edge %q disagrees with the frozen SSA CallPlan",
				edge.stableIdentity,
			)
		}
	}
	return nil
}

// validateCoroWorkerProjectedForeignPointerResult joins one exact wrapper call
// with the producer-forward edge that supplied its function-word parameter.
// The wrapper annotation only maps tuple positions; pointer provenance still
// requires the exact incoming callable candidate set and the fully validated
// worker sink. This is intentionally a call-result operation, not a general
// uintptr value analysis.
func validateCoroWorkerProjectedForeignPointerResult(
	plan *coro.SSAPlan,
	universe *EmissionUniverse,
	call *ssa.Call,
	result int,
) error {
	if plan == nil || universe == nil || call == nil || call.Common() == nil ||
		call.Common().IsInvoke() || result < 0 || result >= coroWorkerResultProjectionWidthV1 {
		return fmt.Errorf("worker result projection requires an exact plan, universe, direct call, and result word")
	}
	if parent := call.Parent(); parent == nil || universe.canonicalAlias(parent) != parent {
		return fmt.Errorf("worker result projection caller is not an exact canonical function")
	}
	carrier, resolved := universe.Resolve(call.Common().StaticCallee())
	if !resolved || carrier == nil {
		return fmt.Errorf("worker result projection call has no exact canonical target")
	}
	projection, projected := universe.workerResultProjections[carrier]
	if !projected || projection.id == "" || projection.resultToWorker[result] < 0 {
		return fmt.Errorf("worker result projection target has no frozen mapping for result %s", coroWorkerResultWord(result))
	}
	carrierPlan, carrierPlanned := plan.FunctionPlan(carrier)
	callPlan, callPlanned := plan.CallPlan(call)
	if !carrierPlanned || !callPlanned || callPlan.Kind != coro.CallDirect || callPlan.Open || callPlan.MayBeNil ||
		len(callPlan.Targets) != 1 || callPlan.Targets[0] != carrierPlan.ID || callPlan.Rep != carrierPlan.FuncRep {
		return fmt.Errorf("worker result projection call disagrees with the frozen exact static CallPlan")
	}

	matches := 0
	for workerCall, workerSite := range universe.coroProgramIR.callPlans {
		if !workerSite.workerCertified {
			continue
		}
		direct, ok := workerCall.(*ssa.Call)
		if !ok || direct == nil {
			continue
		}
		for _, edge := range workerSite.workerIncoming {
			if edge.call != call || edge.carrier != carrier ||
				edge.parameter != projection.functionParameter ||
				edge.resultProjectionID != projection.id {
				continue
			}
			matches++
			if err := validateCoroWorkerSyscallCall(plan, universe, direct); err != nil {
				return fmt.Errorf("worker result projection sink is not valid in the frozen plan: %w", err)
			}
			if edge.foreignPointerResultMask&(uint8(1)<<uint(result)) == 0 {
				return fmt.Errorf("worker result projection call result %s is not a pointer on every exact worker sink", coroWorkerResultWord(result))
			}
		}
	}
	if matches == 0 {
		return fmt.Errorf("worker result projection call has no exact worker sink")
	}
	return nil
}

func coroWorkerHasExactRawPlainOnly(plan *coro.SSAPlan, fn *ssa.Function, functionPlan coro.FunctionPlan) bool {
	return functionPlan.External == coro.Defined && functionPlan.ManagedDemand == coro.NoDemand &&
		functionPlan.RawPlainDemand && functionPlan.RawPlainOnly && functionPlan.Emission == coro.EmitRawPlain &&
		functionPlan.Primary == coro.PrimaryPlain && functionPlan.FuncRep == coro.DirectPlain &&
		plan.HasRawPlainVariant(fn)
}

func coroWorkerHasExactRawPlainVariant(plan *coro.SSAPlan, fn *ssa.Function, functionPlan coro.FunctionPlan) bool {
	if functionPlan.External != coro.Defined || !functionPlan.RawPlainDemand || !plan.HasRawPlainVariant(fn) {
		return false
	}
	if functionPlan.RawPlainOnly {
		return functionPlan.ManagedDemand == coro.NoDemand && functionPlan.Emission == coro.EmitRawPlain &&
			functionPlan.Primary == coro.PrimaryPlain && functionPlan.FuncRep == coro.DirectPlain
	}
	return functionPlan.ManagedDemand != coro.NoDemand && functionPlan.Emission == coro.EmitCoroutine &&
		functionPlan.Primary == coro.PrimaryCoroutine && functionPlan.FuncRep == coro.DirectCoro
}

func coroWorkerUintptrType(typ types.Type) bool {
	basic, ok := types.Unalias(typ).Underlying().(*types.Basic)
	return ok && basic.Kind() == types.Uintptr
}
