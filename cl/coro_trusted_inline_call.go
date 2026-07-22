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
	"strconv"

	"github.com/goplus/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

const coroTrustedInlineCallCertificateDomain = "llgo-coro-trusted-inline-call-certificate-v1"

// freezeCoroTrustedInlineCallCertificates turns one deliberately narrow source
// policy into exact invocation capabilities:
//
//   - the caller is an annotated, bodyful Go wrapper that promises
//     executor-safe progress;
//   - the callee is one exact bodyless C declaration whose conservative
//     contract remains may-block;
//   - the callee itself owns an executor-safe trusted-inline refinement under
//     the same frozen callable ABI; and
//   - the source edge is an ordinary static *ssa.Call.
//
// The wrapper annotation is trusted frontend policy, but it cannot upgrade an
// arbitrary target: the target-owned refinement, exact SSA edge and physical
// ABI certificate are all required. The later SSA fixed point independently
// checks that the complete wrapper body actually satisfies its claimed
// executor-safe summary.
func (u *EmissionUniverse) freezeCoroTrustedInlineCallCertificates() error {
	if u == nil {
		return fmt.Errorf("prepare emission universe: cannot freeze trusted-inline calls in a nil universe")
	}
	u.trustedInlineCalls = make(map[ssa.CallInstruction]coro.SSATrustedInlineCallCertificate)

	for _, caller := range u.functions {
		caller = u.canonicalAlias(caller)
		if caller == nil || len(caller.Blocks) == 0 {
			continue
		}
		callerCertificate, ok := u.callableContracts[caller]
		if !ok || callerCertificate.Scope != coro.CallableContractScopeWrapper ||
			callerCertificate.Contract.Progress != coro.ProgressExecutorSafe {
			continue
		}
		callerIdentity := u.finalIdentity(caller)
		if callerIdentity == "" || callerIdentity == "<nil>" || callerIdentity == "<cyclic-alias>" {
			return fmt.Errorf("prepare emission universe: trusted-inline wrapper %q has no exact canonical identity", caller.Name())
		}

		for _, block := range caller.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(*ssa.Call)
				if !ok || call == nil || call.Parent() != caller || call.Common() == nil || call.Common().IsInvoke() {
					continue
				}
				target := u.canonicalAlias(call.Common().StaticCallee())
				if target == nil || target == caller {
					continue
				}
				targetCertificate, ok := u.callableContracts[target]
				if !ok || !coroTrustedInlineTargetEligible(targetCertificate) {
					continue
				}
				if _, required := u.required[target]; !required {
					continue
				}
				semantic, err := coro.SemanticInstructionOrdinal(call)
				if err != nil {
					return fmt.Errorf("prepare emission universe: identify trusted-inline call in %q: %w", caller.Name(), err)
				}
				targetIdentity := u.finalIdentity(target)
				if targetIdentity == "" || targetIdentity == "<nil>" || targetIdentity == "<cyclic-alias>" {
					return fmt.Errorf("prepare emission universe: trusted-inline target %q has no exact canonical identity", target.Name())
				}
				certificate := coro.SSATrustedInlineCallCertificate{
					ID: emissionDigest(framedEmissionKey(
						coroTrustedInlineCallCertificateDomain,
						callerCertificate.ID,
						targetCertificate.ID,
						callerIdentity,
						strconv.Itoa(block.Index),
						strconv.Itoa(semantic),
						targetIdentity,
						string(targetCertificate.TrustedInlineContract.ID),
						targetCertificate.CallableABI,
					)),
					Contract: targetCertificate.TrustedInlineContract.ID,
					ABI:      targetCertificate.CallableABI,
				}
				u.trustedInlineCalls[call] = certificate
			}
		}
	}
	return nil
}

// coroTrustedInlineTargetEligible describes what the current direct physical
// path can enforce. The default target remains conservative and may project
// ThreadAffine/OpaqueExec; the exact invocation substitutes the target-owned
// selected projection in graph analysis. The selected refinement itself must
// require no affinity/reentry/lifetime adapter because this path emits one
// direct call on the current runnable executor.
func coroTrustedInlineTargetEligible(certificate CoroCallableContractCertificate) bool {
	if certificate.IsZero() || certificate.Scope != coro.CallableContractScopeDeclaration ||
		!certificate.HasTrustedInlineContract ||
		certificate.TrustedInlineContract.Progress != coro.ProgressExecutorSafe ||
		coro.CallableContractExecConstraints(certificate.TrustedInlineContract) != 0 {
		return false
	}
	if err := certificate.Validate(); err != nil {
		return false
	}
	switch certificate.Contract.Progress {
	case coro.ProgressUnknown, coro.ProgressMayBlock, coro.ProgressAsyncCompletion:
		return true
	default:
		// ExecutorSafe needs no edge refinement; NoReturn cannot safely refine to
		// a returning executor-safe invocation.
		return false
	}
}

// CoroTrustedInlineCallCertificate returns the immutable capability for one
// exact wrapper call occurrence. Absence is ordinary Auto policy. The lookup is
// keyed by the SSA instruction itself, never by a code/data address, name or
// reconstructed physical symbol.
func (u *EmissionUniverse) CoroTrustedInlineCallCertificate(
	caller *ssa.Function,
	call ssa.CallInstruction,
) (certificate coro.SSATrustedInlineCallCertificate, certified bool, err error) {
	if u == nil {
		return certificate, false, fmt.Errorf("coroutine trusted-inline call certificate: nil emission universe")
	}
	direct, ok := call.(*ssa.Call)
	if !ok || direct == nil || direct.Common() == nil || direct.Common().IsInvoke() {
		return certificate, false, nil
	}
	canonicalCaller := u.canonicalAlias(caller)
	if canonicalCaller == nil {
		return certificate, false, fmt.Errorf("coroutine trusted-inline call certificate: caller has cyclic canonical aliases")
	}
	if direct.Parent() != canonicalCaller {
		return certificate, false, nil
	}
	if _, required := u.required[canonicalCaller]; !required {
		return certificate, false, nil
	}
	certificate, certified = u.trustedInlineCalls[direct]
	return certificate, certified, nil
}
