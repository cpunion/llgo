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
	"testing"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

func TestRuntimeSymtabParsersAreExactPlainIslands(t *testing.T) {
	runtimeRoot, err := filepath.Abs(filepath.Join("..", "..", "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := packages.Load(&packages.Config{
		Dir: runtimeRoot,
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedImports | packages.NeedDeps | packages.NeedTypes |
			packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedTypesSizes,
	}, "./internal/lib/runtime")
	if err != nil {
		t.Fatal(err)
	}
	if packages.PrintErrors(loaded) != 0 {
		t.Fatal("load runtime symtab package")
	}
	program, ssaPackages := ssautil.AllPackages(loaded, ssa.SanityCheckFunctions|ssa.InstantiateGenerics)
	program.Build()
	if len(ssaPackages) != 1 || ssaPackages[0] == nil {
		t.Fatalf("runtime SSA packages = %d, want one", len(ssaPackages))
	}
	spanHelper := ssaPackages[0].Func("staticSectionSpan")
	if spanHelper == nil {
		t.Fatal("runtime symtab has no staticSectionSpan helper")
	}
	for _, name := range []string{"parsePrebuiltFuncPCTable", "prebuiltFuncPCTablePresent", "staticSectionSpan"} {
		t.Run(name, func(t *testing.T) {
			parser := ssaPackages[0].Func(name)
			if parser == nil {
				t.Fatalf("runtime symtab has no %s helper", name)
			}
			for _, block := range parser.Blocks {
				for _, successor := range block.Succs {
					if successor.Dominates(block) {
						t.Fatalf("plain parser contains a CFG backedge from block %d to %d", block.Index, successor.Index)
					}
				}
				for _, instruction := range block.Instrs {
					switch instruction.(type) {
					case *ssa.Alloc, *ssa.MakeChan, *ssa.MakeClosure, *ssa.MakeMap, *ssa.MakeSlice:
						t.Fatalf("plain parser contains allocation-shaped SSA %T %q", instruction, instruction)
					case ssa.CallInstruction:
						call := instruction.(ssa.CallInstruction)
						if name != "prebuiltFuncPCTablePresent" || call.Common() == nil || call.Common().StaticCallee() != spanHelper {
							t.Fatalf("plain parser contains non-span call-shaped SSA %T %q", instruction, instruction)
						}
					}
				}
			}

			plan, err := AnalyzeSSA(program, Roots{{Function: parser, Demand: AsyncDemand}}, SSAConfig{
				MaxPlainInstructions: -1,
			})
			if err != nil {
				t.Fatal(err)
			}
			got := functionPlanFor(t, plan, parser)
			if got.External != Defined || got.Demand != AsyncDemand || got.Emission != EmitPlain ||
				got.Primary != PrimaryPlain || got.FuncRep != DirectPlain || got.Effect != NoSuspend || got.Exec != 0 {
				var dump bytes.Buffer
				ssa.WriteFunction(&dump, parser)
				t.Fatalf("runtime symtab parser plan = %+v, want demanded Defined DirectPlain NoSuspend/NoUnwind\n%s", got, dump.String())
			}
		})
	}
}
