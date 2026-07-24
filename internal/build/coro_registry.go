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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
)

const coroRootPackageAnchorPrefixV1 = "__llgo_coro_root_package_v1."

// collectLinkedCoroRootAnchors consumes cache-visible package metadata rather
// than scanning LLVM objects. The sorted symbols become ordinary relocations
// in entry_main.o, which is placed before package archives and therefore drives
// extraction of every package that contributes coroutine roots.
func collectLinkedCoroRootAnchors(pkgs []Package) ([]string, error) {
	anchors := make([]string, 0, len(pkgs))
	owners := make(map[string]string, len(pkgs))
	for _, pkg := range pkgs {
		if pkg == nil || pkg.CoroRootAnchorV1 == "" {
			continue
		}
		anchor := pkg.CoroRootAnchorV1
		if !validCoroRootPackageAnchorV1(anchor) {
			return nil, fmt.Errorf("package %s has invalid coroutine root anchor %q", pkg.PkgPath, anchor)
		}
		if owner, exists := owners[anchor]; exists {
			return nil, fmt.Errorf("packages %s and %s claim duplicate coroutine root anchor %q", owner, pkg.PkgPath, anchor)
		}
		owners[anchor] = pkg.PkgPath
		anchors = append(anchors, anchor)
	}
	sort.Strings(anchors)
	return anchors, nil
}

func validCoroRootPackageAnchorV1(name string) bool {
	if len(name) != len(coroRootPackageAnchorPrefixV1)+32 || name[:len(coroRootPackageAnchorPrefixV1)] != coroRootPackageAnchorPrefixV1 {
		return false
	}
	hash := name[len(coroRootPackageAnchorPrefixV1):]
	decoded, err := hex.DecodeString(hash)
	return err == nil && len(decoded) == 16 && hex.EncodeToString(decoded) == hash
}

func coroProgramManifestHashV1(ctx *context, anchors []string, bootstrap ...*coroProgramBootstrapV1) ([16]byte, error) {
	if ctx == nil || ctx.prog == nil || ctx.buildConf == nil {
		return [16]byte{}, fmt.Errorf("coroutine program manifest requires a build context")
	}
	if ctx.clCompilation != nil {
		decoded, err := hex.DecodeString(ctx.coroPlanDigest)
		if err != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != ctx.coroPlanDigest {
			return [16]byte{}, fmt.Errorf("coroutine program manifest requires a canonical CoroPlanDigest")
		}
	}
	target := ctx.prog.TargetSpec()
	h := sha256.New()
	write := func(value string) {
		h.Write([]byte(value))
		h.Write([]byte{0})
	}
	write("llgo.coro.program-manifest.v1")
	write(ctx.coroPlanDigest)
	write(activeCoroABIVersion(ctx.buildConf))
	write(activeCoroSchedulerABIVersion(ctx.buildConf))
	write(activeCoroPanicABIVersion(ctx.buildConf))
	write(target.Triple)
	write(target.CPU)
	write(target.Features)
	write(target.TargetABI)
	write(ctx.prog.DataLayout())
	for _, anchor := range anchors {
		write(anchor)
	}
	if len(bootstrap) > 1 {
		return [16]byte{}, fmt.Errorf("coroutine program manifest accepts at most one bootstrap table")
	}
	if len(bootstrap) == 1 && bootstrap[0] != nil {
		program := bootstrap[0]
		if program.Version != coroProgramBootstrapVersionV2 {
			return [16]byte{}, fmt.Errorf("coroutine program manifest requires the unique V2 startup table")
		}
		write("llgo.coro.program-bootstrap.v2")
		write(hex.EncodeToString(program.StepHash[:]))
		for _, step := range program.Steps {
			write(fmt.Sprintf("%d", step.Kind))
			write(fmt.Sprintf("%d", step.Role))
			write(string(step.FunctionID))
			write(step.Target)
			write(step.Owner)
			write(step.CatalogTarget)
			write(fmt.Sprintf("%d", step.Aux))
		}
	}
	sum := h.Sum(nil)
	var hash [16]byte
	copy(hash[:], sum[:len(hash)])
	return hash, nil
}
