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

	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

// CoroManagedGoLinknameCertificate proves that one exact bodyful Go function
// publishes only a managed Go entry under its frozen physical linkname. It is
// issued for either:
//
//   - an ordinary one-argument `//go:linkname local`, which changes visibility
//     without redirecting the default managed symbol; or
//   - an exact `//llgo:managedlink` plus two-argument redirecting linkname,
//     which explicitly promises that the redirected symbol has no raw,
//     assembly, C, or otherwise synchronous caller.
//
// ManagedOnly distinguishes the explicit second form. Both forms permit one
// coroutine primary and never request a legacy raw/plain body.
type CoroManagedGoLinknameCertificate struct {
	ID             string
	PhysicalSymbol string
	ABISignature   string
	ManagedOnly    bool
}

// CoroGoLinknameVisibilityCertificate preserves the narrow public name used by
// existing visibility-only consumers. The generalized frozen record is shared
// so raw-ABI classification has one source of truth.
type CoroGoLinknameVisibilityCertificate = CoroManagedGoLinknameCertificate

type coroManagedGoLinknameSource struct {
	directive   string
	target      string
	managedOnly bool
	gorootAuto  bool
}

// attachedGoLinknameVisibilityDirective accepts only the exact visibility-only
// source shape. Redirecting linknames, malformed/duplicate directives, and any
// additional export, cgo, wasm, or custom physical ABI directive remain raw
// boundaries and deliberately receive no certificate.
func attachedGoLinknameVisibilityDirective(fn *ssa.Function) (string, bool) {
	source, exact, err := attachedManagedGoLinknameSource(fn)
	if err != nil || !exact || source.managedOnly {
		return "", false
	}
	return source.directive, true
}

// attachedManagedGoLinknameSource recognizes the two deliberately narrow
// source forms described above plus an unannotated redirecting candidate from
// GOROOT. The latter becomes managed only after the build-owned raw-symbol
// proof in freezeCoroManagedGoLinknameCertificates; user-source redirecting
// linknames remain ordinary raw boundaries. The explicit marker is fail-closed:
// malformed marker text, a bodyless/method/generic function, a non-redirecting
// linkname, duplicate linknames, or any competing physical ABI directive is an
// error.
func attachedManagedGoLinknameSource(fn *ssa.Function) (coroManagedGoLinknameSource, bool, error) {
	var source coroManagedGoLinknameSource
	if fn == nil {
		return source, false, nil
	}
	decl, _ := fn.Syntax().(*ast.FuncDecl)
	if decl == nil || decl.Doc == nil {
		return source, false, nil
	}

	const marker = "//llgo:managedlink"
	markerPresent := false
	var markerErr string
	var linkname string
	var linkFields []string
	var linkErr string
	var competingABI string
	for _, comment := range decl.Doc.List {
		if comment == nil {
			continue
		}
		text := strings.TrimSpace(comment.Text)
		if text == marker || strings.HasPrefix(text, marker+" ") {
			switch {
			case markerPresent:
				markerErr = "duplicate " + marker + " directive"
			case text != marker:
				markerErr = marker + " accepts no arguments"
			}
			markerPresent = true
			continue
		}
		fields := strings.Fields(text)
		if len(fields) != 0 && fields[0] == "//go:linkname" {
			if linkname != "" {
				linkErr = "duplicate //go:linkname directive"
				continue
			}
			linkname = text
			linkFields = fields
			continue
		}
		for _, prefix := range []string{
			"//llgo:link", "// llgo:link", "//export", "//go:wasmexport", "//go:wasmimport",
		} {
			if text == prefix || strings.HasPrefix(text, prefix+" ") {
				competingABI = text
				break
			}
		}
		if competingABI == "" && strings.HasPrefix(text, "//go:cgo_") {
			competingABI = text
		}
	}

	if markerPresent && markerErr != "" {
		return source, false, fmt.Errorf("%s", markerErr)
	}
	if markerPresent && linkErr != "" {
		return source, false, fmt.Errorf("%s", linkErr)
	}
	if markerPresent && competingABI != "" {
		return source, false, fmt.Errorf("%s conflicts with physical ABI directive %q", marker, competingABI)
	}

	_, localName := astFuncName("", decl)
	exactFunction := fn.Parent() == nil && len(fn.FreeVars) == 0 && decl.Body != nil &&
		decl.Name != nil && decl.Recv == nil && fn.Signature != nil &&
		fn.Signature.Recv() == nil && !functionNeedsLinkOnce(fn)
	if params := fn.TypeParams(); params != nil && params.Len() != 0 {
		exactFunction = false
	}
	if fn.Signature != nil {
		if params := fn.Signature.TypeParams(); params != nil && params.Len() != 0 {
			exactFunction = false
		}
		if params := fn.Signature.RecvTypeParams(); params != nil && params.Len() != 0 {
			exactFunction = false
		}
	}
	// A source variadic parameter is already one ordinary []T SSA parameter
	// at every static Go call and is normalized to that same packed physical
	// coroutine ABI. Permit it only at an explicit managed-only boundary:
	// visibility-only linknames retain their narrower historical proof, while
	// dynamic variadic function values still fail later dispatch validation.
	if !markerPresent && fn.Signature != nil && fn.Signature.Variadic() {
		exactFunction = false
	}

	if markerPresent {
		if !exactFunction {
			return source, false, fmt.Errorf("%s requires an exact bodyful, non-method, non-generic Go function", marker)
		}
		if len(linkFields) != 3 || linkFields[1] != localName {
			return source, false, fmt.Errorf("%s requires one exact redirecting //go:linkname %s <target>", marker, localName)
		}
		source = coroManagedGoLinknameSource{
			directive:   linkname,
			target:      linkFields[2],
			managedOnly: true,
		}
		return source, true, nil
	}

	if linkErr != "" || competingABI != "" || !exactFunction {
		return source, false, nil
	}
	switch {
	case len(linkFields) == 2 && linkFields[1] == localName:
		source = coroManagedGoLinknameSource{
			directive: linkname,
			target:    funcName(fn.Pkg.Pkg, fn, false),
		}
	case len(linkFields) == 3 && linkFields[1] == localName:
		source = coroManagedGoLinknameSource{
			directive:   linkname,
			target:      linkFields[2],
			managedOnly: true,
			gorootAuto:  true,
		}
	default:
		return source, false, nil
	}
	return source, true, nil
}

// freezeCoroManagedGoLinknameCertificates binds the strict source proof to the
// already frozen frontend kind, owner, structural ABI, final symbol, and target
// identity. A source marker alone is never sufficient evidence.
func (u *EmissionUniverse) freezeCoroManagedGoLinknameCertificates() error {
	for _, fn := range u.functions {
		source, exact, err := attachedManagedGoLinknameSource(fn)
		if err != nil {
			return fmt.Errorf("prepare emission universe: managed go:linkname function %q: %w", fn.Name(), err)
		}
		if !exact {
			continue
		}
		explicitManagedOnly := source.managedOnly && !source.gorootAuto
		if fn.Pkg == nil || fn.Pkg.Pkg == nil || len(fn.Blocks) == 0 {
			if explicitManagedOnly {
				return fmt.Errorf("prepare emission universe: //llgo:managedlink function %q has no exact Go body", fn.Name())
			}
			continue
		}
		canonical := u.canonicalAlias(fn)
		if canonical == nil {
			return fmt.Errorf("prepare emission universe: go:linkname visibility function %q has cyclic canonical aliases", fn.Name())
		}
		if canonical != fn {
			continue
		}
		background, classified, err := u.FunctionBackground(fn)
		if err != nil {
			return err
		}
		if !classified || background != llssa.InGo {
			if explicitManagedOnly {
				return fmt.Errorf("prepare emission universe: //llgo:managedlink function %q is not classified as managed Go", fn.Name())
			}
			continue
		}
		owners := u.sortedUseOwners(fn)
		if len(owners) != 1 {
			if explicitManagedOnly {
				return fmt.Errorf("prepare emission universe: //llgo:managedlink function %q has %d emission owners; want exactly one", fn.Name(), len(owners))
			}
			continue
		}
		owner := owners[0]
		ownerKey := emissionFunctionOwnerKey{function: fn, owner: owner}
		if u.functionKinds[ownerKey] != goFunc {
			if explicitManagedOnly {
				return fmt.Errorf("prepare emission universe: //llgo:managedlink function %q has no managed Go frontend kind", fn.Name())
			}
			continue
		}
		finalKey := u.finalKeys[ownerKey]
		kind, symbol, signature, valid := splitManagedSymbolKey(finalKey)
		if !valid || kind != goFunc || signature == "" {
			if explicitManagedOnly {
				return fmt.Errorf("prepare emission universe: //llgo:managedlink function %q has no exact frozen Go symbol and ABI", fn.Name())
			}
			continue
		}
		if physical := u.physicalNames[ownerKey]; physical != "" {
			symbol = physical
		}
		if symbol != source.target {
			if explicitManagedOnly {
				return fmt.Errorf(
					"prepare emission universe: //llgo:managedlink function %q freezes symbol %q, want directive target %q",
					fn.Name(), symbol, source.target,
				)
			}
			continue
		}
		if source.gorootAuto && !u.gorootManagedGoLinknameHasNoRawConsumer(fn, symbol) {
			continue
		}
		linkIdentity := u.linkIdentities[fn]
		if linkIdentity == "" {
			return fmt.Errorf("prepare emission universe: go:linkname visibility function %q has no frozen link identity", fn.Name())
		}
		target := u.prog.TargetSpec()
		mode := "visibility-only"
		if source.managedOnly {
			mode = "managed-only"
		}
		if source.gorootAuto {
			mode = "goroot-managed-only"
		}
		u.managedGoLinknames[fn] = CoroManagedGoLinknameCertificate{
			ID: framedEmissionKey(
				"llgo-coro-managed-go-linkname-v1",
				mode,
				owner.identity,
				owner.pkgPath,
				linkIdentity,
				finalKey,
				symbol,
				signature,
				target.Triple,
				target.CPU,
				target.Features,
				target.TargetABI,
				u.prog.DataLayout(),
			),
			PhysicalSymbol: symbol,
			ABISignature:   signature,
			ManagedOnly:    source.managedOnly,
		}
	}
	return nil
}

func (u *EmissionUniverse) coroManagedGoLinknameCertificate(fn *ssa.Function) (certificate CoroManagedGoLinknameCertificate, certified bool, err error) {
	if u == nil {
		return certificate, false, fmt.Errorf("coroutine managed go:linkname certificate: nil emission universe")
	}
	if fn == nil {
		return certificate, false, fmt.Errorf("coroutine managed go:linkname certificate: nil function")
	}
	canonical := u.canonicalAlias(fn)
	if canonical == nil {
		return certificate, false, fmt.Errorf("coroutine managed go:linkname certificate: function has cyclic canonical aliases")
	}
	if _, required := u.required[canonical]; !required {
		return certificate, false, fmt.Errorf("coroutine managed go:linkname certificate: function %q is absent from the frozen emission universe", canonical.Name())
	}
	certificate, certified = u.managedGoLinknames[canonical]
	return certificate, certified, nil
}

func (u *EmissionUniverse) coroGoLinknameVisibilityCertificate(fn *ssa.Function) (certificate CoroGoLinknameVisibilityCertificate, certified bool, err error) {
	certificate, certified, err = u.coroManagedGoLinknameCertificate(fn)
	if err != nil || !certified || !certificate.ManagedOnly {
		return certificate, certified, err
	}
	return CoroGoLinknameVisibilityCertificate{}, false, nil
}
