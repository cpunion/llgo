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

	"golang.org/x/tools/go/ssa"
)

func TestSSAAtomicCostCertificateBindsCFGAndCallees(t *testing.T) {
	_, pkg := buildCoroTestSSA(t, "atomic_certificate.go", `package coroid

func left(value any, fail bool) int {
	if fail { panic(value) }
	return 1
}
func right(value any, fail bool) int {
	if fail { panic(value) }
	return 2
}
func choose(value any, fail, useLeft bool) int {
	if useLeft { return left(value, fail) }
	return right(value, fail)
}
`)
	left := packageFunction(t, pkg, "left")
	right := packageFunction(t, pkg, "right")
	choose := packageFunction(t, pkg, "choose")
	certificates := make(map[*ssa.Function]SSAAtomicCalleeCertificate)
	for _, leaf := range []*ssa.Function{left, right} {
		function := FunctionID("test." + leaf.Name())
		cost, certificate, ok := proveSSAAtomicPath(
			function, AtomicCostLeaf, scanSSAFunctionBody(leaf).AtomicPath, nil,
		)
		if !ok {
			t.Fatalf("prove leaf %q", leaf.Name())
		}
		certificates[leaf] = SSAAtomicCalleeCertificate{
			Function: function, Cost: cost, Certificate: certificate,
		}
	}
	callees := make(map[ssa.CallInstruction]SSAAtomicCalleeCertificate)
	var firstCall ssa.CallInstruction
	for _, block := range choose.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok || call.Common().StaticCallee() == nil {
				continue
			}
			certificate, ok := certificates[call.Common().StaticCallee()]
			if !ok {
				t.Fatalf("unexpected static target %q", call.Common().StaticCallee().Name())
			}
			callees[call] = certificate
			if firstCall == nil {
				firstCall = call
			}
		}
	}
	facts := scanSSAFunctionBody(choose).AtomicPath
	cost, certificate, ok := proveSSAAtomicPath("test.choose", AtomicCostDAG, facts, callees)
	if !ok {
		t.Fatal("prove branch DAG")
	}
	if err := VerifySSAAtomicCostCertificate(
		"test.choose", AtomicCostDAG, cost, certificate, facts, callees,
	); err != nil {
		t.Fatal(err)
	}

	mutated := make(map[ssa.CallInstruction]SSAAtomicCalleeCertificate, len(callees))
	for call, callee := range callees {
		mutated[call] = callee
	}
	changed := mutated[firstCall]
	changed.Certificate = strings.Repeat("b", 64)
	mutated[firstCall] = changed
	if err := VerifySSAAtomicCostCertificate(
		"test.choose", AtomicCostDAG, cost, certificate, facts, mutated,
	); err == nil {
		t.Fatal("changed transitive certificate was accepted")
	}

	blocks := cloneSSAAtomicBlocks(facts.Blocks)
	for index := range blocks {
		if len(blocks[index].Successors) != 0 {
			blocks[index].Successors = blocks[index].Successors[:len(blocks[index].Successors)-1]
			break
		}
	}
	if _, err := NewSSAAtomicPathFacts(choose, blocks); err == nil || !strings.Contains(err.Error(), "successors disagree") {
		t.Fatalf("mutated CFG error = %v", err)
	}
}
