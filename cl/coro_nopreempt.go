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
	"strings"

	"golang.org/x/tools/go/ssa"
)

const (
	coroNoPreemptCertificateDomain = "llgo-coro-nopreempt-certificate-v1"
	coroNoUnwindCertificateDomain  = "llgo-coro-nounwind-certificate-v1"
)

// freezeCoroNoPreemptCertificates binds each exact //llgo:nopreempt source
// declaration to its final managed identity. The certificate authorizes only
// suppression of the local CFG/instruction-budget preemption seed; ordinary
// suspend, foreign-wait, unwind, recursion and callee effects remain visible to
// the SSA fixed point.
func (u *EmissionUniverse) freezeCoroNoPreemptCertificates() error {
	rawCritical, err := u.freezeCoroBodyMarkerCertificates(
		"llgo:rawcritical", coroRawCriticalCertificateDomain,
	)
	if err != nil {
		return fmt.Errorf("prepare emission universe: freeze raw-critical certificates: %w", err)
	}
	for function := range rawCritical {
		if function.Signature == nil || function.Signature.Recv() != nil || len(function.FreeVars) != 0 {
			return fmt.Errorf(
				"prepare emission universe: //llgo:rawcritical on %q requires one receiver-free, non-capturing body",
				function.Name(),
			)
		}
	}
	u.rawCritical = rawCritical

	certificates, err := u.freezeCoroBodyMarkerCertificates("llgo:nopreempt", coroNoPreemptCertificateDomain)
	if err != nil {
		return fmt.Errorf("prepare emission universe: freeze no-preempt certificates: %w", err)
	}
	for function, certificate := range u.rawCritical {
		if _, duplicate := certificates[function]; duplicate {
			return fmt.Errorf(
				"prepare emission universe: function %q has both //llgo:nopreempt and //llgo:rawcritical",
				function.Name(),
			)
		}
		certificates[function] = certificate
	}
	u.noPreempt = certificates
	return nil
}

// freezeCoroNoUnwindCertificates binds the narrower runtime invariant that an
// exact body cannot initiate a Go unwind. The SSA no-unwind solver still
// follows every managed call dependency, so this marker cannot hide an
// explicit panic/defer, an open call, or a callee that may unwind.
func (u *EmissionUniverse) freezeCoroNoUnwindCertificates() error {
	certificates, err := u.freezeCoroBodyMarkerCertificates("llgo:nounwind", coroNoUnwindCertificateDomain)
	if err != nil {
		return fmt.Errorf("prepare emission universe: freeze no-unwind certificates: %w", err)
	}
	for function, certificate := range u.rawCritical {
		if _, duplicate := certificates[function]; duplicate {
			return fmt.Errorf(
				"prepare emission universe: function %q has both //llgo:nounwind and //llgo:rawcritical",
				function.Name(),
			)
		}
		certificates[function] = certificate
	}
	u.noUnwind = certificates
	for function := range certificates {
		for logicalName, lowered := range u.loweredCalls[function] {
			if lowered.rawPlain {
				continue
			}
			lowered.noUnwind = true
			u.loweredCalls[function][logicalName] = lowered
		}
	}
	return nil
}

func (u *EmissionUniverse) freezeCoroBodyMarkerCertificates(marker, domain string) (map[*ssa.Function]string, error) {
	if u == nil {
		return nil, fmt.Errorf("cannot freeze //%s certificates in a nil universe", marker)
	}
	certificates := make(map[*ssa.Function]string)
	for _, function := range u.functions {
		canonical := u.canonicalAlias(function)
		if canonical == nil {
			return nil, fmt.Errorf("//%s inventory contains cyclic aliases", marker)
		}
		if canonical != function {
			continue
		}
		present, err := coroBodyMarkerDirectiveFor(canonical, marker)
		if err != nil {
			return nil, fmt.Errorf("//%s directive on %q: %w", marker, canonical.Name(), err)
		}
		if !present {
			continue
		}
		if len(canonical.Blocks) == 0 {
			return nil, fmt.Errorf("//%s on %q requires an exact bodyful Go function", marker, canonical.Name())
		}
		identity := u.finalIdentity(canonical)
		linkIdentity := u.linkIdentities[canonical]
		if identity == "" || identity == "<nil>" || identity == "<cyclic-alias>" || linkIdentity == "" {
			return nil, fmt.Errorf("//%s on %q has no frozen function identity", marker, canonical.Name())
		}
		certificates[canonical] = emissionDigest(framedEmissionKey(
			domain,
			identity,
			linkIdentity,
			marker,
		))
	}
	return certificates, nil
}

func coroBodyMarkerDirectiveFor(fn *ssa.Function, marker string) (bool, error) {
	if fn == nil {
		return false, nil
	}
	declaration, _ := fn.Syntax().(*ast.FuncDecl)
	if declaration == nil || declaration.Doc == nil {
		return false, nil
	}
	present := false
	for _, comment := range declaration.Doc.List {
		if comment == nil {
			continue
		}
		line := strings.TrimSpace(comment.Text)
		directive := "//" + marker
		if !strings.HasPrefix(line, directive) {
			continue
		}
		if line != directive {
			return false, fmt.Errorf("%s accepts no arguments", directive)
		}
		if present {
			return false, fmt.Errorf("duplicate %s directive", directive)
		}
		present = true
	}
	return present, nil
}

// CoroNoPreemptCertificate returns the immutable source capability for one
// exact canonical body. An alias may query the same canonical certificate, but
// no consumer may manufacture the policy from a function name or late comment
// lookup.
func (u *EmissionUniverse) CoroNoPreemptCertificate(fn *ssa.Function) (certificate string, certified bool, err error) {
	return u.coroBodyMarkerCertificate(fn, u.noPreempt, "no-preempt")
}

// CoroNoUnwindCertificate returns the frozen exact-body capability consumed by
// the SSA greatest-fixed-point no-unwind proof.
func (u *EmissionUniverse) CoroNoUnwindCertificate(fn *ssa.Function) (certificate string, certified bool, err error) {
	return u.coroBodyMarkerCertificate(fn, u.noUnwind, "no-unwind")
}

func (u *EmissionUniverse) coroBodyMarkerCertificate(
	fn *ssa.Function,
	certificates map[*ssa.Function]string,
	label string,
) (certificate string, certified bool, err error) {
	if u == nil {
		return "", false, fmt.Errorf("coroutine %s certificate: nil emission universe", label)
	}
	if fn == nil {
		return "", false, fmt.Errorf("coroutine %s certificate: nil function", label)
	}
	canonical := u.canonicalAlias(fn)
	if canonical == nil {
		return "", false, fmt.Errorf("coroutine %s certificate: function has cyclic canonical aliases", label)
	}
	if _, required := u.required[canonical]; !required {
		return "", false, fmt.Errorf("coroutine %s certificate: function %q is absent from the frozen emission universe", label, canonical.Name())
	}
	certificate, certified = certificates[canonical]
	return certificate, certified, nil
}
