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

func TestSSABorrowedAllocationProof(t *testing.T) {
	_, pkg := buildCoroTestSSA(t, "borrowed_allocation.go", `package coroid

type transaction struct {
	self *transaction
	endpoint *int
	phase uint8
}

func begin(out *transaction, endpoint *int) bool {
	if out == nil || *out != (transaction{}) {
		return false
	}
	*out = transaction{self: out, endpoint: endpoint, phase: 1}
	return true
}

func beginEffect(value *transaction) bool {
	if value == nil || value.self != value || value.phase != 1 {
		return false
	}
	value.phase = 2
	return check(value)
}

func check(value *transaction) bool {
	return value.self == value && value.phase == 2 && value.endpoint != nil
}

func safe(endpoint *int) bool {
	var value transaction
	if !begin(&value, endpoint) || !beginEffect(&value) {
		return false
	}
	value = transaction{}
	return true
}

var escaped *transaction

func escapeGlobal() { var value transaction; escaped = &value }
func escapeReturn() *transaction { var value transaction; return &value }
func borrow(value *transaction) { _ = value.phase }
func escapeGo() { var value transaction; go borrow(&value) }
func escapeDefer() { var value transaction; defer borrow(&value) }
func escapeDynamic(fn func(*transaction)) { var value transaction; fn(&value) }

func recursiveSafe(value *transaction, depth int) {
	if depth == 0 { return }
	recursiveSafe(value, depth-1)
	_ = value.phase
}

func safeRecursive() bool {
	var value transaction
	recursiveSafe(&value, 2)
	return value.phase == 0
}

func recurseA(value *transaction, escape bool) { recurseB(value, escape) }
func recurseB(value *transaction, escape bool) {
	if escape { escaped = value; return }
	recurseA(value, true)
}
func escapeRecursive() { var value transaction; recurseA(&value, false) }
`)

	safe := packageFunction(t, pkg, "safe")
	safeAlloc := exactHeapAllocation(t, safe)
	proof, ok := ProveSSABorrowedAllocation(safeAlloc)
	if !ok || proof.Allocation != safeAlloc || proof.FunctionsVisited < 4 || proof.ParametersProven < 3 {
		t.Fatalf("safe borrow proof = %+v, present=%t; want transitive begin/effect/check proof", proof, ok)
	}
	recursiveAlloc := exactHeapAllocation(t, packageFunction(t, pkg, "safeRecursive"))
	if proof, ok := ProveSSABorrowedAllocation(recursiveAlloc); !ok || proof.ParametersProven == 0 {
		t.Fatalf("safe recursive borrow proof = %+v, present=%t", proof, ok)
	}

	for _, name := range []string{
		"escapeGlobal", "escapeReturn", "escapeGo", "escapeDefer", "escapeDynamic", "escapeRecursive",
	} {
		allocation := exactHeapAllocation(t, packageFunction(t, pkg, name))
		if proof, ok := ProveSSABorrowedAllocation(allocation); ok {
			t.Fatalf("%s unexpectedly received borrowed-allocation proof: %+v", name, proof)
		}
	}
}

func TestSSABorrowedAllocationOpaqueSSATypeFailsClosed(t *testing.T) {
	program, _ := buildCoroTestSSA(t, "borrowed_range_func.go", `package coroid
func seq(yield func(int) bool) { yield(1) }
func save(int) {}
func root() {
	defer save(9)
	for value := range seq {
		defer save(value)
	}
}
`)

	foundOpaque := false
	for _, function := range matchingFunctions(program, func(*ssa.Function) bool { return true }) {
		for _, block := range function.Blocks {
			for _, instruction := range block.Instrs {
				allocation, ok := instruction.(*ssa.Alloc)
				if !ok {
					continue
				}
				_, proven := ProveSSABorrowedAllocation(allocation)
				if strings.Contains(allocation.Type().String(), "deferStack") {
					foundOpaque = true
					if proven {
						t.Fatalf("synthetic opaque allocation %s received a borrow proof", allocation)
					}
				}
			}
		}
	}
	if !foundOpaque {
		t.Fatal("range-function SSA did not contain its synthetic deferStack allocation")
	}
}

func exactHeapAllocation(t *testing.T, function *ssa.Function) *ssa.Alloc {
	t.Helper()
	var result *ssa.Alloc
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			allocation, ok := instruction.(*ssa.Alloc)
			if !ok || !allocation.Heap {
				continue
			}
			if result != nil {
				t.Fatalf("%s has multiple heap allocations: %s and %s", function, result, allocation)
			}
			result = allocation
		}
	}
	if result == nil {
		t.Fatalf("%s has no heap allocation", function)
	}
	return result
}
