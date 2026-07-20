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
	"unicode"
	"unicode/utf8"

	"github.com/goplus/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

const coroCallableContractIDForeignV1 = "foreign.v1"

type coroCallableContractScope string

const (
	coroCallableContractScopeWrapper     coroCallableContractScope = "wrapper"
	coroCallableContractScopeDeclaration coroCallableContractScope = "declaration"
)

// coroCallableContractCertificate is a frozen, target-neutral description of
// one exact source declaration. Scope is deliberately frontend metadata: the
// shared coroutine model describes callable behavior, while the frontend must
// still prove whether that behavior belongs to a Go wrapper body or to a
// bodyless external declaration.
type coroCallableContractCertificate struct {
	Contract                 coro.CallableContract
	TrustedInlineContract    coro.CallableContract
	HasTrustedInlineContract bool
	Scope                    coroCallableContractScope
	ABI                      string
	Canonical                string
}

// coroCallableContractCertificateFor reads only the exact ast.FuncDecl owned
// by fn. Synthetic wrappers, instantiated helper functions without their own
// declaration, and late comment-map guesses cannot acquire a certificate.
func coroCallableContractCertificateFor(fn *ssa.Function) (coroCallableContractCertificate, bool, error) {
	if fn == nil {
		return coroCallableContractCertificate{}, false, nil
	}
	decl, _ := fn.Syntax().(*ast.FuncDecl)
	if decl == nil {
		return coroCallableContractCertificate{}, false, nil
	}
	return parseCoroCallableContractDecl(decl)
}

func parseCoroCallableContractDecl(decl *ast.FuncDecl) (coroCallableContractCertificate, bool, error) {
	if decl == nil || decl.Doc == nil {
		return coroCallableContractCertificate{}, false, nil
	}

	var directive []string
	var otherCoroDirectives []string
	for _, comment := range decl.Doc.List {
		if comment == nil {
			continue
		}
		line := strings.TrimSpace(comment.Text)
		if !strings.HasPrefix(line, "//") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "//"))
		fields := strings.Fields(payload)
		if len(fields) == 0 || fields[0] != "llgo:coro" {
			continue
		}
		if len(fields) >= 2 && fields[1] == "workerresult" {
			// Worker result projection is an orthogonal, compiler-owned wrapper
			// contract. It neither changes callable behavior nor conflicts with a
			// target-neutral callable contract on the same body.
			continue
		}
		if len(fields) < 2 || fields[1] != "contract" {
			otherCoroDirectives = append(otherCoroDirectives, payload)
			continue
		}
		if directive != nil {
			return coroCallableContractCertificate{}, false, fmt.Errorf("duplicate //llgo:coro contract directive")
		}
		directive = fields
	}
	if directive == nil {
		return coroCallableContractCertificate{}, false, nil
	}
	if len(otherCoroDirectives) != 0 {
		return coroCallableContractCertificate{}, false, fmt.Errorf(
			"//llgo:coro contract conflicts with legacy directive %q",
			otherCoroDirectives[0],
		)
	}
	if len(directive) < 3 {
		return coroCallableContractCertificate{}, false, fmt.Errorf("//llgo:coro contract requires an ID")
	}
	if directive[2] != coroCallableContractIDForeignV1 {
		if coroCallableContractBackendVocabulary(directive[2]) {
			return coroCallableContractCertificate{}, false, fmt.Errorf("callable contract ID %q contains backend vocabulary", directive[2])
		}
		return coroCallableContractCertificate{}, false, fmt.Errorf("unsupported callable contract ID %q", directive[2])
	}

	inferredScope := coroCallableContractScopeDeclaration
	if decl.Body != nil {
		inferredScope = coroCallableContractScopeWrapper
	}
	scope := inferredScope
	values := make(map[string]string, 10)
	for _, field := range directive[3:] {
		key, value, ok := strings.Cut(field, "=")
		if !ok || key == "" || value == "" {
			if coroCallableContractBackendVocabulary(field) {
				return coroCallableContractCertificate{}, false, fmt.Errorf("callable contract token %q contains backend vocabulary", field)
			}
			return coroCallableContractCertificate{}, false, fmt.Errorf("callable contract token %q must be key=value", field)
		}
		if _, duplicate := values[key]; duplicate {
			return coroCallableContractCertificate{}, false, fmt.Errorf("duplicate callable contract key %q", key)
		}
		switch key {
		case "scope", "progress", "affinity", "reentry", "memory", "abi",
			"inline-progress", "inline-affinity", "inline-reentry", "inline-memory":
		default:
			if coroCallableContractBackendVocabulary(key) || coroCallableContractBackendVocabulary(value) {
				return coroCallableContractCertificate{}, false, fmt.Errorf("callable contract field %q contains backend vocabulary", field)
			}
			return coroCallableContractCertificate{}, false, fmt.Errorf("unknown callable contract key %q", key)
		}
		values[key] = value
	}

	if value, explicit := values["scope"]; explicit {
		switch value {
		case string(coroCallableContractScopeWrapper):
			scope = coroCallableContractScopeWrapper
		case string(coroCallableContractScopeDeclaration):
			scope = coroCallableContractScopeDeclaration
		default:
			if coroCallableContractBackendVocabulary(value) {
				return coroCallableContractCertificate{}, false, fmt.Errorf("callable contract scope %q contains backend vocabulary", value)
			}
			return coroCallableContractCertificate{}, false, fmt.Errorf("unknown callable contract scope %q", value)
		}
		if scope != inferredScope {
			return coroCallableContractCertificate{}, false, fmt.Errorf(
				"callable contract scope %q conflicts with exact %s FuncDecl",
				scope, inferredScope,
			)
		}
	}

	for _, key := range []string{"progress", "affinity", "reentry", "memory"} {
		if _, present := values[key]; !present {
			return coroCallableContractCertificate{}, false, fmt.Errorf("callable contract requires explicit %s", key)
		}
	}
	contract := coro.CallableContract{ID: coroCallableContractIDForeignV1}
	if err := setCoroCallableProgress(&contract, values["progress"]); err != nil {
		return coroCallableContractCertificate{}, false, err
	}
	if err := setCoroCallableAffinity(&contract, values["affinity"]); err != nil {
		return coroCallableContractCertificate{}, false, err
	}
	if err := setCoroCallableReentry(&contract, values["reentry"]); err != nil {
		return coroCallableContractCertificate{}, false, err
	}
	if err := setCoroCallableMemory(&contract, values["memory"]); err != nil {
		return coroCallableContractCertificate{}, false, err
	}
	if err := contract.Validate(); err != nil {
		return coroCallableContractCertificate{}, false, fmt.Errorf("invalid callable contract: %w", err)
	}
	inlineKeys := []string{"inline-progress", "inline-affinity", "inline-reentry", "inline-memory"}
	inlineCount := 0
	for _, key := range inlineKeys {
		if _, present := values[key]; present {
			inlineCount++
		}
	}
	if inlineCount != 0 && inlineCount != len(inlineKeys) {
		return coroCallableContractCertificate{}, false, fmt.Errorf(
			"trusted-inline callable contract requires all of inline-progress, inline-affinity, inline-reentry, and inline-memory",
		)
	}
	trustedInline := coro.CallableContract{}
	hasTrustedInline := inlineCount != 0
	if hasTrustedInline {
		trustedInline.ID = coroCallableContractIDForeignV1
		if err := setCoroCallableProgress(&trustedInline, values["inline-progress"]); err != nil {
			return coroCallableContractCertificate{}, false, fmt.Errorf("inline-progress: %w", err)
		}
		if err := setCoroCallableAffinity(&trustedInline, values["inline-affinity"]); err != nil {
			return coroCallableContractCertificate{}, false, fmt.Errorf("inline-affinity: %w", err)
		}
		if err := setCoroCallableReentry(&trustedInline, values["inline-reentry"]); err != nil {
			return coroCallableContractCertificate{}, false, fmt.Errorf("inline-reentry: %w", err)
		}
		if err := setCoroCallableMemory(&trustedInline, values["inline-memory"]); err != nil {
			return coroCallableContractCertificate{}, false, fmt.Errorf("inline-memory: %w", err)
		}
		if err := coro.ValidateTrustedInlineCallableContractRefinement(trustedInline, contract); err != nil {
			return coroCallableContractCertificate{}, false, err
		}
	}
	abi := values["abi"]
	if abi != "" {
		if err := validateCoroCallableABI(abi); err != nil {
			return coroCallableContractCertificate{}, false, err
		}
	}

	canonicalFields := []string{
		"llgo:coro", "contract", coroCallableContractIDForeignV1,
		"scope=" + string(scope),
		"progress=" + values["progress"],
		"affinity=" + values["affinity"],
		"reentry=" + values["reentry"],
		"memory=" + values["memory"],
	}
	if hasTrustedInline {
		canonicalFields = append(canonicalFields,
			"inline-progress="+values["inline-progress"],
			"inline-affinity="+values["inline-affinity"],
			"inline-reentry="+values["inline-reentry"],
			"inline-memory="+values["inline-memory"],
		)
	}
	if abi != "" {
		canonicalFields = append(canonicalFields, "abi="+abi)
	}
	canonical := strings.Join(canonicalFields, " ")
	return coroCallableContractCertificate{
		Contract: contract, TrustedInlineContract: trustedInline, HasTrustedInlineContract: hasTrustedInline,
		Scope: scope, ABI: abi, Canonical: canonical,
	}, true, nil
}

func setCoroCallableProgress(contract *coro.CallableContract, value string) error {
	switch value {
	case "unknown":
		contract.Progress = coro.ProgressUnknown
	case "executor-safe":
		contract.Progress = coro.ProgressExecutorSafe
	case "may-block":
		contract.Progress = coro.ProgressMayBlock
	case "async-completion":
		contract.Progress = coro.ProgressAsyncCompletion
	case "no-return":
		contract.Progress = coro.ProgressNoReturn
	default:
		return invalidCoroCallableContractValue("progress", value)
	}
	return nil
}

func setCoroCallableAffinity(contract *coro.CallableContract, value string) error {
	switch value {
	case "unknown":
		contract.Affinity = coro.AffinityUnknown
	case "any-thread":
		contract.Affinity = coro.AffinityAnyThread
	case "caller-thread":
		contract.Affinity = coro.AffinityCallerThread
	case "owner-thread":
		contract.Affinity = coro.AffinityOwnerThread
	case "host-main":
		// host-main is an abstract affinity class, not a host backend
		// selection. Backend nouns remain rejected everywhere else below.
		contract.Affinity = coro.AffinityHostMain
	default:
		return invalidCoroCallableContractValue("affinity", value)
	}
	return nil
}

func setCoroCallableReentry(contract *coro.CallableContract, value string) error {
	switch value {
	case "unknown":
		contract.Reentry = coro.ReentryUnknown
	case "none":
		contract.Reentry = coro.ReentryNone
	case "managed-callback":
		contract.Reentry = coro.ReentryManagedCallback
	default:
		return invalidCoroCallableContractValue("reentry", value)
	}
	return nil
}

func setCoroCallableMemory(contract *coro.CallableContract, value string) error {
	switch value {
	case "unknown":
		contract.Memory = coro.MemoryUnknown
	case "by-value":
		contract.Memory = coro.MemoryByValue
	case "borrow-until-return":
		contract.Memory = coro.MemoryBorrowUntilReturn
	case "borrow-until-complete":
		contract.Memory = coro.MemoryBorrowUntilComplete
	case "retained":
		contract.Memory = coro.MemoryRetained
	default:
		return invalidCoroCallableContractValue("memory", value)
	}
	return nil
}

func invalidCoroCallableContractValue(key, value string) error {
	if coroCallableContractBackendVocabulary(value) {
		return fmt.Errorf("callable contract %s %q contains backend vocabulary", key, value)
	}
	return fmt.Errorf("unknown callable contract %s %q", key, value)
}

func validateCoroCallableABI(value string) error {
	if value == "" {
		return fmt.Errorf("callable contract ABI must not be empty")
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("callable contract ABI is not valid UTF-8")
	}
	if coroCallableContractBackendVocabulary(value) {
		return fmt.Errorf("callable contract ABI %q contains backend vocabulary", value)
	}
	for _, char := range value {
		if unicode.IsSpace(char) || unicode.IsControl(char) {
			return fmt.Errorf("callable contract ABI %q is not a stable token", value)
		}
	}
	return nil
}

func coroCallableContractBackendVocabulary(value string) bool {
	for _, word := range strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return r != '-' && r != '_' && r != '.' && (r < '0' || r > '9') && (r < 'a' || r > 'z')
	}) {
		for _, part := range strings.FieldsFunc(word, func(r rune) bool { return r == '-' || r == '_' || r == '.' }) {
			switch part {
			case "worker", "poll", "host", "backend":
				return true
			}
		}
	}
	return false
}
