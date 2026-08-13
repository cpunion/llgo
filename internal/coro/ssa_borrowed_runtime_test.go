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
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

func TestRuntimeChannelCommitTransactionsAreProvenBorrowed(t *testing.T) {
	runtimeRoot, err := filepath.Abs(filepath.Join("..", "..", "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := packages.Load(&packages.Config{
		Dir:        runtimeRoot,
		BuildFlags: []string{"-tags=llgo"},
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedImports | packages.NeedDeps | packages.NeedTypes |
			packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedTypesSizes,
	}, "./internal/runtime")
	if err != nil {
		t.Fatal(err)
	}
	if packages.PrintErrors(loaded) != 0 {
		t.Fatal("load llgo runtime channel package")
	}
	program, roots := ssautil.AllPackages(loaded, ssa.SanityCheckFunctions|ssa.InstantiateGenerics)
	program.Build()
	if len(roots) != 1 || roots[0] == nil {
		t.Fatalf("runtime SSA packages = %d, want one", len(roots))
	}
	for _, name := range []string{
		"commitCoroRecvWaiterLockedWithContext",
		"commitCoroSendWaiterLockedWithContext",
	} {
		function := roots[0].Func(name)
		if function == nil {
			t.Fatalf("runtime SSA has no %s", name)
		}
		var transaction *ssa.Alloc
		for _, block := range function.Blocks {
			for _, instruction := range block.Instrs {
				allocation, ok := instruction.(*ssa.Alloc)
				if ok && allocation.Heap && strings.Contains(allocation.Type().String(), "ChannelExternalCommit") {
					transaction = allocation
				}
			}
		}
		if transaction == nil {
			t.Fatalf("%s has no heap ChannelExternalCommit allocation", name)
		}
		if proof, ok := ProveSSABorrowedAllocation(transaction); !ok {
			var dump bytes.Buffer
			ssa.WriteFunction(&dump, function)
			t.Fatalf("%s transaction lacks borrowed-allocation proof:\n%s", name, dump.String())
		} else if proof.FunctionsVisited < 2 || proof.ParametersProven == 0 {
			t.Fatalf("%s transaction proof is not interprocedural: %+v", name, proof)
		}
	}
}
