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

// CoroGoLinknameVisibilityCertificate proves that one exact bodyful,
// one-argument `//go:linkname local` directive (two lexical fields including
// the directive token) changes only linker visibility. It
// does not redirect the function's default managed Go symbol and therefore is
// not, by itself, a raw synchronous caller or a request for a second body.
// Actual bodyless Go consumers are still joined by final symbol + structural
// signature before this certificate is frozen.
type CoroGoLinknameVisibilityCertificate struct {
	ID             string
	PhysicalSymbol string
	ABISignature   string
}

// attachedGoLinknameVisibilityDirective accepts only the exact visibility-only
// source shape. Redirecting linknames, malformed/duplicate directives, and any
// additional export, cgo, wasm, or custom physical ABI directive remain raw
// boundaries and deliberately receive no certificate.
func attachedGoLinknameVisibilityDirective(fn *ssa.Function) (string, bool) {
	if fn == nil || fn.Parent() != nil || len(fn.FreeVars) != 0 {
		return "", false
	}
	decl, _ := fn.Syntax().(*ast.FuncDecl)
	if decl == nil || decl.Body == nil || decl.Doc == nil || decl.Name == nil || decl.Recv != nil {
		return "", false
	}
	_, localName := astFuncName("", decl)
	var found string
	for _, comment := range decl.Doc.List {
		if comment == nil {
			continue
		}
		text := strings.TrimSpace(comment.Text)
		fields := strings.Fields(text)
		if len(fields) != 0 && fields[0] == "//go:linkname" {
			if found != "" || len(fields) != 2 || fields[1] != localName {
				return "", false
			}
			found = text
			continue
		}
		for _, prefix := range []string{
			"//llgo:link", "// llgo:link", "//export", "//go:wasmexport", "//go:wasmimport",
		} {
			if text == prefix || strings.HasPrefix(text, prefix+" ") {
				return "", false
			}
		}
		if strings.HasPrefix(text, "//go:cgo_") {
			return "", false
		}
	}
	if found == "" || fn.Signature == nil || fn.Signature.Recv() != nil || fn.Signature.Variadic() || functionNeedsLinkOnce(fn) {
		return "", false
	}
	if params := fn.TypeParams(); params != nil && params.Len() != 0 {
		return "", false
	}
	if params := fn.Signature.TypeParams(); params != nil && params.Len() != 0 {
		return "", false
	}
	if params := fn.Signature.RecvTypeParams(); params != nil && params.Len() != 0 {
		return "", false
	}
	return found, true
}

// freezeCoroGoLinknameVisibilityCertificates binds the strict source shape to
// the already frozen frontend kind, owner, structural ABI, final symbol, and
// target identity. A source directive alone is never sufficient evidence.
func (u *EmissionUniverse) freezeCoroGoLinknameVisibilityCertificates() error {
	for _, fn := range u.functions {
		if _, exact := attachedGoLinknameVisibilityDirective(fn); !exact {
			continue
		}
		if fn.Pkg == nil || fn.Pkg.Pkg == nil || len(fn.Blocks) == 0 {
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
			continue
		}
		owners := u.sortedUseOwners(fn)
		if len(owners) != 1 {
			continue
		}
		owner := owners[0]
		ownerKey := emissionFunctionOwnerKey{function: fn, owner: owner}
		if u.functionKinds[ownerKey] != goFunc {
			continue
		}
		finalKey := u.finalKeys[ownerKey]
		kind, symbol, signature, valid := splitManagedSymbolKey(finalKey)
		if !valid || kind != goFunc || signature == "" {
			continue
		}
		if physical := u.physicalNames[ownerKey]; physical != "" {
			symbol = physical
		}
		defaultSymbol := funcName(fn.Pkg.Pkg, fn, false)
		if symbol != defaultSymbol {
			continue
		}
		linkIdentity := u.linkIdentities[fn]
		if linkIdentity == "" {
			return fmt.Errorf("prepare emission universe: go:linkname visibility function %q has no frozen link identity", fn.Name())
		}
		target := u.prog.TargetSpec()
		u.goLinknameVisibility[fn] = CoroGoLinknameVisibilityCertificate{
			ID: framedEmissionKey(
				"llgo-coro-go-linkname-visibility-v0",
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
		}
	}
	return nil
}

func (u *EmissionUniverse) coroGoLinknameVisibilityCertificate(fn *ssa.Function) (certificate CoroGoLinknameVisibilityCertificate, certified bool, err error) {
	if u == nil {
		return certificate, false, fmt.Errorf("coroutine go:linkname visibility certificate: nil emission universe")
	}
	if fn == nil {
		return certificate, false, fmt.Errorf("coroutine go:linkname visibility certificate: nil function")
	}
	canonical := u.canonicalAlias(fn)
	if canonical == nil {
		return certificate, false, fmt.Errorf("coroutine go:linkname visibility certificate: function has cyclic canonical aliases")
	}
	if _, required := u.required[canonical]; !required {
		return certificate, false, fmt.Errorf("coroutine go:linkname visibility certificate: function %q is absent from the frozen emission universe", canonical.Name())
	}
	certificate, certified = u.goLinknameVisibility[canonical]
	return certificate, certified, nil
}
