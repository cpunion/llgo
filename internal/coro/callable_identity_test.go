//go:build !llgo

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
	"strings"
	"testing"
)

func testCallableIdentityCertificate(t *testing.T) CallableIdentityCertificate {
	t.Helper()
	certificate, err := FreezeCallableIdentityCertificate(CallableIdentityCertificate{
		CanonicalFunctionIdentity: "test/canonical",
		LinkIdentity:              "test/link",
		CallableABI:               "typed.v1/test",
		TypedABISignature:         "func(int) int",
		PhysicalSymbol:            "test_foreign",
		PhysicalABISignature:      "func(int) int",
		Origin:                    CallableIdentityOriginManagedCDeclaration,
		Evidence:                  CallableIdentityEvidenceManagedFinalShape,
	})
	if err != nil {
		t.Fatal(err)
	}
	return certificate
}

func TestCallableIdentityCertificateIsContentAddressedAndExact(t *testing.T) {
	first := testCallableIdentityCertificate(t)
	if len(first.ID) != 64 {
		t.Fatalf("identity ID = %q", first.ID)
	}
	if err := first.Validate(); err != nil {
		t.Fatal(err)
	}
	again := testCallableIdentityCertificate(t)
	if again != first {
		t.Fatalf("stable identity changed: first=%+v again=%+v", first, again)
	}
	changedFields := first
	changedFields.ID = ""
	changedFields.CanonicalFunctionIdentity = "test/other-canonical"
	changed, err := FreezeCallableIdentityCertificate(changedFields)
	if err != nil {
		t.Fatal(err)
	}
	if changed.ID == first.ID {
		t.Fatalf("different exact declarations share identity %q", first.ID)
	}
	// A repeated physical target is not a declaration identity collision.
	if changed.PhysicalSymbol != first.PhysicalSymbol || changed.PhysicalABISignature != first.PhysicalABISignature {
		t.Fatal("test did not preserve the repeated physical target")
	}
}

func TestCallableIdentityCertificateFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*CallableIdentityCertificate)
		want string
	}{
		{"forged ID", func(c *CallableIdentityCertificate) { c.CanonicalFunctionIdentity = "changed" }, "does not match"},
		{"ABI mismatch", func(c *CallableIdentityCertificate) { c.PhysicalABISignature = "func(string)" }, "differs"},
		{"origin", func(c *CallableIdentityCertificate) { c.Origin = "guessed-address" }, "origin"},
		{"evidence", func(c *CallableIdentityCertificate) { c.Evidence = "symbol-scan" }, "evidence"},
	} {
		t.Run(test.name, func(t *testing.T) {
			certificate := testCallableIdentityCertificate(t)
			test.edit(&certificate)
			if err := certificate.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate error = %v, want %q", err, test.want)
			}
		})
	}
}
