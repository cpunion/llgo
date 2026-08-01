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
	"math"
	"slices"

	"golang.org/x/tools/go/ssa"
)

// AtomicCostCertificateSchema identifies the first path-sensitive, transitive
// no-cut cost certificate. The certificate is target-independent; exact target
// and runtime ABI compatibility remain owned by PlanDigestMetadata and
// LibraryEffectMetadata.
const AtomicCostCertificateSchema = "llgo.coro.atomic-cost.v1"

// SSAAtomicCallSiteFacts identifies one evaluated call occurrence in a frozen
// ProgramIR block. InstructionIndex is the source SSA ordinal within that
// block. The pointer is used only while building the whole-program plan and is
// replaced by stable block/instruction coordinates in the certificate.
type SSAAtomicCallSiteFacts struct {
	Instruction      ssa.CallInstruction
	InstructionIndex int
}

// SSAAtomicBlockFacts is the minimal CFG projection needed to calculate a
// longest no-cut path. LocalCost counts evaluated, non-debug semantic recipes
// in the block, including each call instruction itself. Callee costs are added
// separately at the exact call occurrence.
type SSAAtomicBlockFacts struct {
	Index      int
	LocalCost  uint64
	Successors []int
	Calls      []SSAAtomicCallSiteFacts
}

// SSAAtomicPathFacts is an immutable ProgramIR projection. Blocks are dense in
// source block-index order and block zero is the entry. It deliberately does
// not duplicate SSA values, Phi inputs, terminators, or physical lowering.
type SSAAtomicPathFacts struct {
	Blocks []SSAAtomicBlockFacts
}

// NewSSAAtomicPathFacts validates and clones one path projection. The caller
// remains responsible for deriving it from the already-frozen semantic plans;
// this constructor never rescans raw SSA instructions or rebuilds a CFG.
func NewSSAAtomicPathFacts(function *ssa.Function, blocks []SSAAtomicBlockFacts) (*SSAAtomicPathFacts, error) {
	if function == nil || function.Blocks == nil {
		return nil, fmt.Errorf("coro: atomic path facts require one defined SSA function")
	}
	facts := &SSAAtomicPathFacts{Blocks: cloneSSAAtomicBlocks(blocks)}
	if err := facts.validate(function); err != nil {
		return nil, err
	}
	return facts, nil
}

func cloneSSAAtomicBlocks(blocks []SSAAtomicBlockFacts) []SSAAtomicBlockFacts {
	if blocks == nil {
		return nil
	}
	cloned := make([]SSAAtomicBlockFacts, len(blocks))
	for index, block := range blocks {
		cloned[index] = block
		cloned[index].Successors = slices.Clone(block.Successors)
		cloned[index].Calls = slices.Clone(block.Calls)
	}
	return cloned
}

func (facts *SSAAtomicPathFacts) clone() *SSAAtomicPathFacts {
	if facts == nil {
		return nil
	}
	return &SSAAtomicPathFacts{Blocks: cloneSSAAtomicBlocks(facts.Blocks)}
}

func (facts *SSAAtomicPathFacts) equal(other *SSAAtomicPathFacts) bool {
	if facts == nil || other == nil {
		return facts == other
	}
	if len(facts.Blocks) != len(other.Blocks) {
		return false
	}
	for index, left := range facts.Blocks {
		right := other.Blocks[index]
		if left.Index != right.Index || left.LocalCost != right.LocalCost ||
			!slices.Equal(left.Successors, right.Successors) || len(left.Calls) != len(right.Calls) {
			return false
		}
		for callIndex, call := range left.Calls {
			otherCall := right.Calls[callIndex]
			if call.Instruction != otherCall.Instruction || call.InstructionIndex != otherCall.InstructionIndex {
				return false
			}
		}
	}
	return true
}

func (facts *SSAAtomicPathFacts) validate(function *ssa.Function) error {
	if facts == nil || function == nil || function.Blocks == nil {
		return fmt.Errorf("coro: atomic path facts require one defined SSA function")
	}
	if len(facts.Blocks) == 0 || len(facts.Blocks) != len(function.Blocks) {
		return fmt.Errorf(
			"coro: atomic path for %q has %d blocks, want %d",
			function.Name(), len(facts.Blocks), len(function.Blocks),
		)
	}
	for index, block := range facts.Blocks {
		if block.Index != index {
			return fmt.Errorf("coro: atomic path for %q has non-canonical block index %d at %d", function.Name(), block.Index, index)
		}
		sourceBlock := function.Blocks[index]
		if sourceBlock == nil || sourceBlock.Index != index {
			return fmt.Errorf("coro: atomic path for %q has no canonical source block %d", function.Name(), index)
		}
		expectedSuccessors := make([]int, 0, len(sourceBlock.Succs))
		for _, successor := range sourceBlock.Succs {
			if successor == nil {
				return fmt.Errorf("coro: atomic path for %q has a nil source successor at block %d", function.Name(), index)
			}
			expectedSuccessors = append(expectedSuccessors, successor.Index)
		}
		slices.Sort(expectedSuccessors)
		if !slices.Equal(block.Successors, expectedSuccessors) {
			return fmt.Errorf("coro: atomic path block %d successors disagree with source CFG", index)
		}
		previousSuccessor := -1
		for _, successor := range block.Successors {
			if successor < 0 || successor >= len(facts.Blocks) {
				return fmt.Errorf("coro: atomic path block %d has invalid successor %d", index, successor)
			}
			if successor < previousSuccessor {
				return fmt.Errorf("coro: atomic path block %d successors are not canonical indexes", index)
			}
			previousSuccessor = successor
		}
		previousInstruction := -1
		for _, call := range block.Calls {
			if call.Instruction == nil || call.Instruction.Parent() != function {
				return fmt.Errorf("coro: atomic path block %d has a foreign or nil call occurrence", index)
			}
			if call.InstructionIndex < 0 || call.InstructionIndex >= len(sourceBlock.Instrs) ||
				call.InstructionIndex <= previousInstruction {
				return fmt.Errorf("coro: atomic path block %d calls are not in unique source order", index)
			}
			if sourceBlock.Instrs[call.InstructionIndex] != call.Instruction {
				return fmt.Errorf("coro: atomic path block %d call does not match source instruction %d", index, call.InstructionIndex)
			}
			previousInstruction = call.InstructionIndex
		}
	}
	return nil
}

// SSAAtomicCalleeCertificate is the exact producer capability consumed at one
// direct call occurrence. Physical planning uses the same type to prove that
// the frozen lowering still names the call graph certified by analysis.
type SSAAtomicCalleeCertificate struct {
	Function    FunctionID
	Cost        uint64
	Certificate string
}

type atomicCostCertificateDocument struct {
	Schema   string                       `json:"schema"`
	Function FunctionID                   `json:"function"`
	Proof    AtomicCostProof              `json:"proof"`
	Cost     uint64                       `json:"cost"`
	Blocks   []atomicCostCertificateBlock `json:"blocks"`
}

type atomicCostCertificateBlock struct {
	Index      int                         `json:"index"`
	LocalCost  uint64                      `json:"local_cost"`
	Successors []int                       `json:"successors"`
	Calls      []atomicCostCertificateCall `json:"calls"`
}

type atomicCostCertificateCall struct {
	Instruction int        `json:"instruction"`
	Target      FunctionID `json:"target"`
	Cost        uint64     `json:"cost"`
	Certificate string     `json:"certificate"`
}

// proveSSAAtomicPath computes the longest entry-to-terminal path and binds it
// to the exact local CFG projection and transitive callee certificates. A
// missing call fact, CFG/call cycle, unreachable block, zero/overflowed cost,
// or malformed imported certificate fails closed.
func proveSSAAtomicPath(
	function FunctionID,
	proof AtomicCostProof,
	facts *SSAAtomicPathFacts,
	callees map[ssa.CallInstruction]SSAAtomicCalleeCertificate,
) (uint64, string, bool) {
	if function == "" || facts == nil || len(facts.Blocks) == 0 || !proof.ProvesOutcomePlain() {
		return 0, "", false
	}
	document := atomicCostCertificateDocument{
		Schema:   AtomicCostCertificateSchema,
		Function: function,
		Proof:    proof,
		Blocks:   make([]atomicCostCertificateBlock, len(facts.Blocks)),
	}
	callCount := 0
	weights := make([]uint64, len(facts.Blocks))
	for index, block := range facts.Blocks {
		certificateBlock := atomicCostCertificateBlock{
			Index:      block.Index,
			LocalCost:  block.LocalCost,
			Successors: slices.Clone(block.Successors),
			Calls:      make([]atomicCostCertificateCall, 0, len(block.Calls)),
		}
		weight := block.LocalCost
		for _, occurrence := range block.Calls {
			callee, ok := callees[occurrence.Instruction]
			if !ok || callee.Function == "" || callee.Cost == 0 ||
				validateSHA256Hex("atomic callee certificate", callee.Certificate) != nil ||
				math.MaxUint64-weight < callee.Cost {
				return 0, "", false
			}
			weight += callee.Cost
			callCount++
			certificateBlock.Calls = append(certificateBlock.Calls, atomicCostCertificateCall{
				Instruction: occurrence.InstructionIndex,
				Target:      callee.Function,
				Cost:        callee.Cost,
				Certificate: callee.Certificate,
			})
		}
		weights[index] = weight
		document.Blocks[index] = certificateBlock
	}
	if len(callees) != callCount || proof == AtomicCostLeaf && callCount != 0 || proof == AtomicCostDAG && callCount == 0 {
		return 0, "", false
	}

	state := make([]uint8, len(facts.Blocks))
	memo := make([]uint64, len(facts.Blocks))
	visited := 0
	var longest func(int) (uint64, bool)
	longest = func(index int) (uint64, bool) {
		switch state[index] {
		case 1:
			return 0, false
		case 2:
			return memo[index], true
		}
		state[index] = 1
		visited++
		maxTail := uint64(0)
		for _, successor := range facts.Blocks[index].Successors {
			tail, ok := longest(successor)
			if !ok {
				return 0, false
			}
			if tail > maxTail {
				maxTail = tail
			}
		}
		if math.MaxUint64-weights[index] < maxTail {
			return 0, false
		}
		memo[index] = weights[index] + maxTail
		state[index] = 2
		return memo[index], true
	}
	cost, ok := longest(0)
	if !ok || cost == 0 || visited != len(facts.Blocks) {
		return 0, "", false
	}
	document.Cost = cost
	payload, err := json.Marshal(document)
	if err != nil {
		return 0, "", false
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(AtomicCostCertificateSchema))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(payload)
	return cost, hex.EncodeToString(hash.Sum(nil)), true
}

// VerifySSAAtomicCostCertificate recomputes one content-addressed path proof
// from a frozen ProgramIR projection and exact physical call edges.
func VerifySSAAtomicCostCertificate(
	function FunctionID,
	proof AtomicCostProof,
	cost uint64,
	certificate string,
	facts *SSAAtomicPathFacts,
	callees map[ssa.CallInstruction]SSAAtomicCalleeCertificate,
) error {
	computedCost, computedCertificate, ok := proveSSAAtomicPath(function, proof, facts, callees)
	if !ok {
		return fmt.Errorf("coro: atomic-cost certificate for %q cannot be reconstructed", function)
	}
	if computedCost != cost || computedCertificate != certificate {
		return fmt.Errorf(
			"coro: atomic-cost certificate for %q disagrees with frozen path: cost=%d/%d certificate=%q/%q",
			function, computedCost, cost, computedCertificate, certificate,
		)
	}
	return nil
}
