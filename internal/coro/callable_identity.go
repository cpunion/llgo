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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

const CallableIdentityCertificateDigestDomain = "llgo.coro.callable-identity-certificate.digest.v1"

// CallableIdentityOrigin names the semantic producer kind without implying
// any execution policy. Stage one inventories only exact C declarations that
// remain in the managed emission universe.
type CallableIdentityOrigin string

const CallableIdentityOriginManagedCDeclaration CallableIdentityOrigin = "managed-c-declaration"

func (origin CallableIdentityOrigin) Validate() error {
	switch origin {
	case CallableIdentityOriginManagedCDeclaration:
		return nil
	default:
		return fmt.Errorf("coro: invalid callable identity origin %q", origin)
	}
}

// CallableIdentityEvidence records which immutable frontend artifact proved
// the identity. It is deliberately independent from generic/legacy/unknown
// behavior evidence, so changing execution policy does not create a different
// declaration identity.
type CallableIdentityEvidence string

const CallableIdentityEvidenceManagedFinalShape CallableIdentityEvidence = "managed-final-shape"

func (evidence CallableIdentityEvidence) Validate() error {
	switch evidence {
	case CallableIdentityEvidenceManagedFinalShape:
		return nil
	default:
		return fmt.Errorf("coro: invalid callable identity evidence %q", evidence)
	}
}

// CallableIdentityCertificate is the pointer-free identity of one exact
// callable declaration. It proves neither progress nor a permitted execution
// backend. In particular, an identity-only C declaration remains governed by
// the existing conservative unknown-foreign SSA policy.
//
// Multiple certificates may deliberately carry the same PhysicalSymbol and
// PhysicalABISignature: DeclarationRef is derived from ID, whose digest also
// binds the exact canonical and link identities.
type CallableIdentityCertificate struct {
	ID                        string                   `json:"id"`
	CanonicalFunctionIdentity string                   `json:"canonical_function_identity"`
	LinkIdentity              string                   `json:"link_identity"`
	CallableABI               string                   `json:"callable_abi"`
	CallableABIExplicit       bool                     `json:"callable_abi_explicit"`
	TypedABISignature         string                   `json:"typed_abi_signature"`
	PhysicalSymbol            string                   `json:"physical_symbol"`
	PhysicalABISignature      string                   `json:"physical_abi_signature"`
	Origin                    CallableIdentityOrigin   `json:"origin"`
	Evidence                  CallableIdentityEvidence `json:"evidence"`
}

func (certificate CallableIdentityCertificate) IsZero() bool {
	return certificate == (CallableIdentityCertificate{})
}

// FreezeCallableIdentityCertificate validates fields and assigns their
// canonical domain-separated SHA-256 identity. Callers must not supply ID.
func FreezeCallableIdentityCertificate(certificate CallableIdentityCertificate) (CallableIdentityCertificate, error) {
	if certificate.ID != "" {
		return CallableIdentityCertificate{}, fmt.Errorf("coro: cannot freeze callable identity certificate with a preexisting ID")
	}
	if err := validateCallableIdentityCertificateFields(certificate); err != nil {
		return CallableIdentityCertificate{}, err
	}
	payload, err := json.Marshal(certificate)
	if err != nil {
		return CallableIdentityCertificate{}, fmt.Errorf("coro: marshal callable identity certificate: %w", err)
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(CallableIdentityCertificateDigestDomain))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(payload)
	certificate.ID = hex.EncodeToString(hash.Sum(nil))
	return certificate, nil
}

func (certificate CallableIdentityCertificate) Validate() error {
	if err := validateSHA256Hex("callable identity certificate ID", certificate.ID); err != nil {
		return err
	}
	fields := certificate
	fields.ID = ""
	want, err := FreezeCallableIdentityCertificate(fields)
	if err != nil {
		return err
	}
	if want.ID != certificate.ID {
		return fmt.Errorf("coro: callable identity certificate ID does not match its frozen fields")
	}
	return nil
}

func validateCallableIdentityCertificateFields(certificate CallableIdentityCertificate) error {
	if err := validateCallableOpaqueIdentity("callable canonical function identity", certificate.CanonicalFunctionIdentity); err != nil {
		return err
	}
	if err := validateCallableOpaqueIdentity("callable link identity", certificate.LinkIdentity); err != nil {
		return err
	}
	if err := validateStableToken("callable identity ABI", certificate.CallableABI); err != nil {
		return err
	}
	if err := validateStableIdentityText("callable typed ABI signature", certificate.TypedABISignature); err != nil {
		return err
	}
	if err := validateStableIdentityText("callable physical symbol", certificate.PhysicalSymbol); err != nil {
		return err
	}
	if err := validateStableIdentityText("callable physical ABI signature", certificate.PhysicalABISignature); err != nil {
		return err
	}
	if certificate.PhysicalABISignature != certificate.TypedABISignature {
		return fmt.Errorf("coro: callable identity physical ABI differs from its typed ABI")
	}
	if err := certificate.Origin.Validate(); err != nil {
		return err
	}
	if err := certificate.Evidence.Validate(); err != nil {
		return err
	}
	return nil
}

func validateCallableOpaqueIdentity(name, value string) error {
	if value == "" {
		return fmt.Errorf("coro: empty %s", name)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("coro: %s is not valid UTF-8", name)
	}
	return nil
}

// ValidateCallableContractIdentity verifies that a declaration-scoped generic
// behavior certificate describes the same exact callable. Behavior fields are
// intentionally absent: this is identity equality, not execution-policy join.
func ValidateCallableContractIdentity(identity CallableIdentityCertificate, contract CallableContractCertificate) error {
	if err := identity.Validate(); err != nil {
		return fmt.Errorf("invalid callable identity: %w", err)
	}
	if err := contract.Validate(); err != nil {
		return fmt.Errorf("invalid callable contract: %w", err)
	}
	if contract.Scope != CallableContractScopeDeclaration {
		return fmt.Errorf("coro: callable identity can only bind a declaration-scoped contract")
	}
	if identity.CanonicalFunctionIdentity != contract.CanonicalFunctionIdentity ||
		identity.LinkIdentity != contract.LinkIdentity ||
		identity.CallableABI != contract.CallableABI ||
		identity.CallableABIExplicit != contract.CallableABIExplicit ||
		identity.TypedABISignature != contract.TypedABISignature ||
		identity.PhysicalSymbol != contract.PhysicalSymbol ||
		identity.PhysicalABISignature != contract.PhysicalABISignature {
		return fmt.Errorf("coro: callable contract identity fields differ from the frozen callable identity certificate")
	}
	return nil
}
