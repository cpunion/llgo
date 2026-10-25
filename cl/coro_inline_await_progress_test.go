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

package cl

import (
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/goembed"
	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
)

func TestCoroInlineAwaitPreservesCallerPreemptCountdown(t *testing.T) {
	const source = `package foo
func Child(value uint32) uint32 { return value + 1 }
func Parent(limit uint32) uint32 {
	var value uint32
	for value < limit { value = Child(value) }
	return value
}
`
	for _, target := range []struct {
		name string
		arch *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", arch: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(target.name, func(t *testing.T) {
			prog, ssaPkg, files, universe, plan := prepareCoroChildAwaitPhysicalABISource(t, target.arch, source)
			defer prog.Dispose()
			compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
			enableCoroPreemptCompilation(compilation)
			pkg, _, err := NewPackageExWithEmbedOptions(prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{}, PackageOptions{Compilation: compilation})
			if err != nil {
				t.Fatal(err)
			}
			module := pkg.Module()
			defer module.Dispose()
			parent := requireCoroPhysicalFunction(t, module, "foo.Parent")
			countdowns := make(map[llvm.Value]bool)
			var completed []llvm.BasicBlock
			resumedResets := 0
			for _, block := range parent.BasicBlocks() {
				hasConsume, hasReset := false, false
				for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
					if instruction.InstructionOpcode() == llvm.Store && !instruction.Operand(0).IsAConstantInt().IsNil() && instruction.Operand(0).ZExtValue() == coroPreemptCheckpointStride {
						countdowns[instruction.Operand(1)] = true
						hasReset = true
					}
					if instruction.InstructionOpcode() == llvm.Call {
						switch instruction.CalledValue().Name() {
						case coroAwaitInlineDestroyConsumeHookV4:
							completed = append(completed, block)
						case coroAwaitConsumeHookV1:
							hasConsume = true
						}
					}
				}
				if hasConsume && hasReset {
					resumedResets++
				}
			}
			if len(countdowns) != 1 || len(completed) == 0 || resumedResets < 2 {
				t.Fatalf("invalid progress fixture: countdowns=%d inline completions=%d resumed/canceled resets=%d\n%s", len(countdowns), len(completed), resumedResets, parent.String())
			}
			for countdown := range countdowns {
				for _, block := range completed {
					if coroInlineReturnReachesCountdownReset(block, countdown) {
						t.Fatalf("synchronous inline return resets caller countdown before a physical suspension or full poll; repeated managed calls can starve ready work:\n%s", parent.String())
					}
				}
			}
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("invalid progress control flow: %v", err)
			}
			runCoroABITestPipeline(t, prog, module)
			t.Logf("Parent frame after CoroSplit: %d bytes", coroFrameAllocationSize(t, module.NamedFunction("foo.Parent$coro"), prog.PointerSize()*8))
		})
	}
}

// Follow the completed child's executable continuation, retaining the true PHI
// fact which skips the parent's physical suspend. Stop at the next child call
// after inspecting this return continuation. A real suspend or full poll is an
// independent service boundary and may legitimately reload the budget.
func coroInlineReturnReachesCountdownReset(start llvm.BasicBlock, countdown llvm.Value) bool {
	type edge struct {
		block, predecessor llvm.BasicBlock
	}
	type state struct {
		edge
		constants map[llvm.Value]uint64
	}
	seen := make(map[edge][]map[llvm.Value]uint64)
	pending := []state{{edge: edge{block: start}, constants: make(map[llvm.Value]uint64)}}
	for len(pending) != 0 {
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		visited := false
		for _, previous := range seen[current.edge] {
			if sameCoroCFGConstants(previous, current.constants) {
				visited = true
				break
			}
		}
		if visited {
			continue
		}
		seen[current.edge] = append(seen[current.edge], current.constants)
		constants := copyCoroCFGConstants(current.constants)
		boundary := false
		for instruction := current.block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
			if !instruction.IsAPHINode().IsNil() {
				if value, ok := coroCFGPHIIncomingConstant(instruction, current.predecessor, constants); ok {
					constants[instruction] = value
				} else {
					delete(constants, instruction)
				}
			}
			if instruction.InstructionOpcode() == llvm.Xor {
				left, leftOK := coroCFGConstant(instruction.Operand(0), constants)
				right, rightOK := coroCFGConstant(instruction.Operand(1), constants)
				if leftOK && rightOK {
					constants[instruction] = left ^ right
				}
			}
			if instruction.InstructionOpcode() == llvm.Store && instruction.Operand(1) == countdown && !instruction.Operand(0).IsAConstantInt().IsNil() && instruction.Operand(0).ZExtValue() == coroPreemptCheckpointStride {
				return true
			}
			if instruction.InstructionOpcode() == llvm.Call {
				callee := instruction.CalledValue().Name()
				if callee == coroPreemptPollHookV1 || strings.HasPrefix(callee, "llvm.coro.suspend") || callee == "foo.Child$coro" {
					boundary = true
					break
				}
			}
		}
		if boundary {
			continue
		}
		for _, successor := range executableTerminatorSuccessors(current.block.LastInstruction(), constants) {
			pending = append(pending, state{edge: edge{block: successor, predecessor: current.block}, constants: constants})
		}
	}
	return false
}
