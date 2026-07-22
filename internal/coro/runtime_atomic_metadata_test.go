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

func TestRuntimeAtomicMetadataLoopsArePreemptibleWithoutOwnedLocks(t *testing.T) {
	runtimeRoot, err := filepath.Abs(filepath.Join("..", "..", "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := packages.Load(&packages.Config{
		Dir: runtimeRoot,
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedImports | packages.NeedDeps | packages.NeedTypes |
			packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedTypesSizes,
	}, "./internal/atomiccache")
	if err != nil {
		t.Fatal(err)
	}
	if packages.PrintErrors(loaded) != 0 {
		t.Fatal("load runtime atomic metadata cache")
	}
	program, roots := ssautil.AllPackages(loaded, ssa.SanityCheckFunctions|ssa.InstantiateGenerics)
	program.Build()
	if len(roots) != 1 || roots[0] == nil {
		t.Fatalf("atomic metadata SSA packages = %d, want one", len(roots))
	}

	wanted := map[string]bool{
		"findPair":   false,
		"Intern":     false,
		"prune":      false,
		"InternWeak": false,
	}
	for function := range ssautil.AllFunctions(program) {
		if function == nil || function.Pkg != roots[0] || function.Blocks == nil {
			continue
		}
		name := function.Name()
		if _, ok := wanted[name]; !ok {
			continue
		}
		// Distinguish PairTable.Intern from any future same-named helper by its
		// receiver spelling while retaining the two package-level helpers.
		if name == "Intern" && !strings.Contains(function.String(), "PairTable") {
			continue
		}
		wanted[name] = true

		hasBackedge := false
		for _, block := range function.Blocks {
			for _, successor := range block.Succs {
				if successor.Dominates(block) {
					hasBackedge = true
				}
			}
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok {
					continue
				}
				target := call.Common().StaticCallee()
				if target == nil {
					var dump bytes.Buffer
					ssa.WriteFunction(&dump, function)
					t.Fatalf("atomic metadata function %s contains dynamic call %q\n%s", function, instruction, dump.String())
				}
				path := ""
				if target.Pkg != nil && target.Pkg.Pkg != nil {
					path = target.Pkg.Pkg.Path()
				}
				if strings.Contains(path, "pthread") || target.Name() == "Lock" || target.Name() == "Unlock" {
					t.Fatalf("atomic metadata function %s reaches owned lock call %s", function, target)
				}
			}
		}
		if !hasBackedge {
			t.Errorf("atomic metadata function %s lost its expected retry/traversal backedge", function)
		}
		_, exec := scanSSAFunctionBody(function, -1)
		if !exec.Contains(NeedsPreempt) {
			t.Errorf("atomic metadata function %s backedge is no longer classified preemptible: %s", function, exec)
		}
	}
	for name, found := range wanted {
		if !found {
			t.Errorf("atomic metadata SSA lacks expected function %s", name)
		}
	}
}
