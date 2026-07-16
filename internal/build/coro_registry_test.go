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

package build

import (
	"testing"

	"github.com/goplus/llgo/internal/packages"
	llssa "github.com/goplus/llgo/ssa"
)

func TestCollectLinkedCoroRootAnchors(t *testing.T) {
	a := coroRootPackageAnchorPrefixV1 + "11111111111111111111111111111111"
	b := coroRootPackageAnchorPrefixV1 + "22222222222222222222222222222222"
	pkgs := []Package{
		{Package: &packages.Package{PkgPath: "example.com/b"}, CoroRootAnchorV1: b},
		{Package: &packages.Package{PkgPath: "example.com/plain"}},
		{Package: &packages.Package{PkgPath: "example.com/a"}, CoroRootAnchorV1: a},
	}
	got, err := collectLinkedCoroRootAnchors(pkgs)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != a || got[1] != b {
		t.Fatalf("anchors = %q, want [%q %q]", got, a, b)
	}

	pkgs[1].CoroRootAnchorV1 = "invalid"
	if _, err := collectLinkedCoroRootAnchors(pkgs); err == nil {
		t.Fatal("invalid coroutine root anchor accepted")
	}
	pkgs[1].CoroRootAnchorV1 = a
	if _, err := collectLinkedCoroRootAnchors(pkgs); err == nil {
		t.Fatal("duplicate coroutine root anchor accepted")
	}
}

func TestCoroProgramManifestHashV1StableAndComplete(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	ctx := &context{
		prog: prog,
		buildConf: &Config{
			Goos:                      "linux",
			Goarch:                    "amd64",
			EnableCoroEntryResolution: true,
			EnableCoroPhysicalABI:     true,
			EnableCoroChildAwait:      true,
		},
	}
	a := coroRootPackageAnchorPrefixV1 + "11111111111111111111111111111111"
	b := coroRootPackageAnchorPrefixV1 + "22222222222222222222222222222222"
	first, err := coroProgramManifestHashV1(ctx, []string{a, b})
	if err != nil {
		t.Fatal(err)
	}
	again, err := coroProgramManifestHashV1(ctx, []string{a, b})
	if err != nil {
		t.Fatal(err)
	}
	if first != again {
		t.Fatalf("manifest hash is unstable: %x != %x", first, again)
	}
	changed, err := coroProgramManifestHashV1(ctx, []string{a})
	if err != nil {
		t.Fatal(err)
	}
	if changed == first {
		t.Fatal("manifest hash ignored the ordered anchor catalog")
	}
}
