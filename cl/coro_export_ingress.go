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
	"go/types"

	"github.com/xgo-dev/llgo/internal/coro"
	llssa "github.com/xgo-dev/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

const coroExportIngressCertificateDomainV1 = "llgo-coro-export-ingress-certificate-v1"

// coroExportIngressCertificate binds one source export to its exact managed
// definition before physical code addresses exist. It is a compiler-internal
// capability: the SSA plan carries only ID, while emission replays every
// symbol, ABI, function, and link-identity field against this frozen record.
type coroExportIngressCertificate struct {
	ID                   string
	FunctionIdentity     string
	LinkIdentity         string
	PhysicalSymbol       string
	PhysicalABISignature string
}

// CoroExportIngressCertificate returns the immutable candidate binding for an
// exact bodyful //export definition. Target selection still decides whether
// the current platform has a physical ingress implementation; a certificate
// alone never authorizes code generation.
func (u *EmissionUniverse) CoroExportIngressCertificate(
	fn *ssa.Function,
) (certificateID string, certified bool, err error) {
	if u == nil || fn == nil {
		return "", false, fmt.Errorf("coroutine export ingress certificate requires one prepared function")
	}
	canonical, ok := u.Resolve(fn)
	if !ok || canonical == nil || canonical != fn {
		return "", false, fmt.Errorf(
			"coroutine export ingress function %q is not one canonical emitted definition",
			fn.Name(),
		)
	}
	certificate, certified := u.exportIngressBindings[canonical]
	return certificate.ID, certified, nil
}

func (u *EmissionUniverse) coroExportIngressCertificate(
	fn *ssa.Function,
) (coroExportIngressCertificate, bool) {
	if u == nil || fn == nil {
		return coroExportIngressCertificate{}, false
	}
	canonical := u.canonicalAlias(fn)
	if canonical == nil || canonical != fn {
		return coroExportIngressCertificate{}, false
	}
	certificate, ok := u.exportIngressBindings[canonical]
	return certificate, ok
}

func (u *EmissionUniverse) freezeCoroExportIngressCertificates(
	targets map[*ssa.Function]coroLocalExportIngressTarget,
) error {
	if u == nil {
		return fmt.Errorf("prepare emission universe: cannot freeze export ingress certificates in a nil universe")
	}
	bindings := make(map[*ssa.Function]coroExportIngressCertificate)
	for function, target := range targets {
		if function == nil || u.canonicalAlias(function) != function ||
			target.FunctionIdentity == "" || target.LinkIdentity == "" ||
			target.PhysicalSymbol == "" || target.PhysicalABISignature == "" {
			return fmt.Errorf("prepare emission universe: export ingress target inventory is incomplete")
		}
		certificate := coroExportIngressCertificate{
			FunctionIdentity:     target.FunctionIdentity,
			LinkIdentity:         target.LinkIdentity,
			PhysicalSymbol:       target.PhysicalSymbol,
			PhysicalABISignature: target.PhysicalABISignature,
		}
		certificate.ID = emissionDigest(framedEmissionKey(
			coroExportIngressCertificateDomainV1,
			certificate.FunctionIdentity,
			certificate.LinkIdentity,
			certificate.PhysicalSymbol,
			certificate.PhysicalABISignature,
		))
		bindings[function] = certificate
	}
	u.exportIngressBindings = bindings
	return nil
}

func validateCoroExportIngressSignature(signature *types.Signature) bool {
	return signature != nil && signature.Recv() == nil && !signature.Variadic() &&
		typeParamCount(signature.TypeParams()) == 0 &&
		typeParamCount(signature.RecvTypeParams()) == 0
}

func validateCoroExportIngressRoots(
	plan *coro.SSAPlan,
	universe *EmissionUniverse,
	capabilities coro.TargetCapabilities,
) error {
	if plan == nil || universe == nil {
		return fmt.Errorf("coroutine export ingress validation requires one plan and emission universe")
	}
	for _, root := range plan.Roots() {
		if !root.IngressEntry {
			continue
		}
		if !capabilities.NativeFleet() {
			return fmt.Errorf(
				"coroutine export ingress %q requires the native fleet runtime capability",
				root.ID,
			)
		}
		certificate, certified := universe.coroExportIngressCertificate(root.Function)
		if !certified || certificate.ID == "" || certificate.ID != root.IngressCertificate {
			return fmt.Errorf(
				"coroutine export ingress %q does not match its frozen certificate",
				root.ID,
			)
		}
		symbol, exported := exactLocalCExportSymbol(root.Function)
		signature, err := universe.coroPhysicalEntrySourceSignature(root.Function)
		if err != nil {
			return fmt.Errorf("coroutine export ingress %q signature: %w", root.ID, err)
		}
		physicalABI := universe.emissionTypeKeys.cFunctionABI(signature)
		if !exported || symbol == "" || symbol != certificate.PhysicalSymbol ||
			physicalABI == "" || physicalABI != certificate.PhysicalABISignature ||
			universe.finalIdentity(root.Function) != certificate.FunctionIdentity ||
			universe.linkIdentities[root.Function] != certificate.LinkIdentity {
			return fmt.Errorf(
				"coroutine export ingress %q failed symbol, ABI, or target identity replay",
				root.ID,
			)
		}
		function, planned := plan.FunctionPlan(root.Function)
		if !planned || function.ID != root.ID || function.External != coro.Defined ||
			!function.ManagedDemand.Contains(coro.AsyncDemand) ||
			function.RawPlainDemand || function.RawPlainEntry ||
			(function.Emission != coro.EmitPlain && function.Emission != coro.EmitCoroutine) ||
			!validateCoroExportIngressSignature(signature) ||
			signature.Results().Len() > 1 {
			return fmt.Errorf(
				"coroutine export ingress %q has an unsupported managed plan or physical signature",
				root.ID,
			)
		}
	}
	return nil
}

// compileCoroExportIngressAdapter consumes one already-validated plan root and
// emits the exact public C symbol bound by its frozen certificate. The managed
// target remains the sole source-body implementation; the public adapter only
// performs the shared typed C-to-managed child transaction.
func (p *context) compileCoroExportIngressAdapter(
	target *ssa.Function,
) llssa.Function {
	if p == nil || p.emissionUniverse == nil || target == nil {
		panic("coroutine export ingress requires one frozen compilation target")
	}
	plan := p.immutablePlan()
	if plan == nil {
		panic("coroutine export ingress requires one immutable plan")
	}
	root, planned := plan.ForeignIngressRoot(target)
	certificate, certified :=
		p.emissionUniverse.coroExportIngressCertificate(target)
	if !planned || !certified || root.IngressCertificate == "" ||
		root.IngressCertificate != certificate.ID || certificate.PhysicalSymbol == "" {
		panic("coroutine export ingress escaped its frozen certificate")
	}
	signature, err := p.emissionUniverse.coroPhysicalEntrySourceSignature(target)
	if err != nil {
		panic(fmt.Errorf("coroutine export ingress %q ABI: %w", root.ID, err))
	}
	if !validateCoroExportIngressSignature(signature) ||
		signature.Results().Len() > 1 ||
		p.emissionUniverse.emissionTypeKeys.cFunctionABI(signature) !=
			certificate.PhysicalABISignature {
		panic("coroutine export ingress escaped its frozen physical ABI")
	}
	entry := p.mustFunctionSymbol(target)
	if !entry.planned || entry.baseName != certificate.PhysicalSymbol ||
		entry.name == certificate.PhysicalSymbol {
		panic("coroutine export ingress public symbol collides with its managed primary")
	}
	return p.coroForeignIngressAdapter(
		target, signature, certificate.PhysicalSymbol, false,
	)
}
