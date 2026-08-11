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

package ssa

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/xgo-dev/llvm"
)

const (
	// CoroAtomicCostMetadataName carries compiler-only proof roots through the
	// LLVM coroutine pipeline. It contains symbols and content digests, never
	// code addresses, and therefore does not retain otherwise dead functions.
	CoroAtomicCostMetadataName = "llgo.coro.atomic_cost"
	coroAtomicCostMetadataV1   = 1
	coroAtomicCostReportSchema = "llgo.coro.post-llvm-atomic-cost.v1"

	// CoroAtomicBoundedCompilerCallMetadataName marks a compiler-generated
	// call which has an independently frozen bounded meaning. Source LLVM and
	// ordinary inline assembly cannot acquire this capability by inference.
	CoroAtomicBoundedCompilerCallMetadataName = "llgo.coro.atomic_bounded_compiler_call"
	// CoroAtomicCompilerDataAnchorV1 is the zero-runtime-work inline-assembly
	// fragment used to publish funcinfo/pclntab data records next to a body.
	CoroAtomicCompilerDataAnchorV1  = "llgo.compiler.data-anchor.v1"
	coroAtomicBoundedCompilerCallV1 = 1

	// LLVMCallBr is part of the stable llvm-c Opcode ABI but is not yet
	// exported by the Go binding. Treat it as an unsupported call/control
	// operation instead of letting a call-like instruction bypass projection.
	coroAtomicCallBrOpcode llvm.Opcode = 67
)

// MarkCoroAtomicBoundedCompilerCall injects the exact compiler-owned identity
// and inline-assembly content digest at creation time. The verifier consumes
// this metadata directly; it never classifies an arbitrary function pointer or
// reverse-engineers a call-site address.
func MarkCoroAtomicBoundedCompilerCall(ctx llvm.Context, call llvm.Value, identity string) {
	if call.IsNil() || call.InstructionOpcode() != llvm.Call {
		panic("ssa: invalid bounded compiler call")
	}
	callee := call.CalledValue()
	if callee.IsNil() || callee.IsAInlineAsm().IsNil() ||
		identity != CoroAtomicCompilerDataAnchorV1 {
		panic("ssa: invalid bounded compiler call")
	}
	call.SetMetadata(ctx.MDKindID(CoroAtomicBoundedCompilerCallMetadataName), ctx.MDNode([]llvm.Metadata{
		llvm.ConstInt(ctx.Int32Type(), coroAtomicBoundedCompilerCallV1, false).ConstantAsMetadata(),
		ctx.MDString(identity),
		ctx.MDString(coroAtomicInlineAsmDigest(identity, callee)),
	}))
}

// EmitCoroAtomicCostCertificate publishes one locally emitted outcome entry
// for structural verification before and after LLVM coroutine lowering.
func (p Package) EmitCoroAtomicCostCertificate(symbol string, cost uint64, proof uint8, certificate string) error {
	return p.emitCoroAtomicCostMetadata(symbol, cost, proof, certificate, true)
}

// EmitCoroAtomicCostDependency publishes an already compatibility-checked
// imported outcome entry used by a local certificate. Its producer artifact is
// responsible for verifying the body.
func (p Package) EmitCoroAtomicCostDependency(symbol string, cost uint64, proof uint8, certificate string) error {
	return p.emitCoroAtomicCostMetadata(symbol, cost, proof, certificate, false)
}

// CoroAtomicCostLocalSymbols returns the exact local bodies which need
// compiler-owned work inserted after ordinary LLVM verification to carry a
// bounded-call marker. A late inserter may treat an error as an empty set only
// when the mandatory final verifier still consumes and reports the malformed
// certificate table before object emission.
func CoroAtomicCostLocalSymbols(module llvm.Module) (map[string]struct{}, error) {
	records, err := readCoroAtomicCostMetadata(module)
	if err != nil {
		return nil, err
	}
	symbols := make(map[string]struct{})
	for symbol, record := range records {
		if record.local {
			symbols[symbol] = struct{}{}
		}
	}
	return symbols, nil
}

func (p Package) emitCoroAtomicCostMetadata(symbol string, cost uint64, proof uint8, certificate string, local bool) error {
	if p == nil || symbol == "" || cost == 0 || proof == 0 || proof > 2 {
		return fmt.Errorf("ssa: invalid coroutine atomic-cost metadata")
	}
	if len(certificate) != sha256.Size*2 {
		return fmt.Errorf("ssa: atomic-cost certificate must contain one SHA-256 digest")
	}
	if _, err := hex.DecodeString(certificate); err != nil {
		return fmt.Errorf("ssa: atomic-cost certificate is not hexadecimal: %w", err)
	}
	i32 := p.Prog.Int32().ll
	i64 := p.Prog.Int64().ll
	localValue := uint64(0)
	if local {
		localValue = 1
	}
	p.mod.AddNamedMetadataOperand(CoroAtomicCostMetadataName,
		p.Prog.ctx.MDNode([]llvm.Metadata{
			llvm.ConstInt(i32, coroAtomicCostMetadataV1, false).ConstantAsMetadata(),
			p.Prog.ctx.MDString(symbol),
			llvm.ConstInt(i64, cost, false).ConstantAsMetadata(),
			llvm.ConstInt(i32, uint64(proof), false).ConstantAsMetadata(),
			p.Prog.ctx.MDString(certificate),
			llvm.ConstInt(i32, localValue, false).ConstantAsMetadata(),
		}),
	)
	return nil
}

// CoroAtomicCostFunctionReport binds one semantic path certificate to the
// longest bounded path found in the actual LLVM function graph. LLVMMaxCost is
// an abstract IR work unit, not a target-cycle or wall-clock claim.
type CoroAtomicCostFunctionReport struct {
	Symbol       string `json:"symbol"`
	Certificate  string `json:"certificate"`
	SemanticCost uint64 `json:"semantic_cost"`
	Proof        uint8  `json:"proof"`
	LLVMMaxCost  uint64 `json:"llvm_max_cost"`
	Local        bool   `json:"local"`
}

// CoroAtomicCostReport is the deterministic post-LLVM structural certificate
// for one module.
type CoroAtomicCostReport struct {
	Schema    string                         `json:"schema"`
	Functions []CoroAtomicCostFunctionReport `json:"functions"`
	Digest    string                         `json:"digest"`
}

type coroAtomicMetadataRecord struct {
	symbol      string
	cost        uint64
	proof       uint8
	certificate string
	local       bool
}

type coroAtomicLLVMBlock struct {
	localCost  uint64
	successors []int
	calls      []string
}

type coroAtomicLLVMFunction struct {
	record coroAtomicMetadataRecord
	blocks []coroAtomicLLVMBlock
}

// VerifyCoroAtomicCostModule fails closed if an annotated outcome body is
// missing, cyclic, indirectly calls, contains coroutine intrinsics, reaches an
// unannotated helper, or acquires an unsupported LLVM control operation. The
// same verifier runs before and after CoroSplit and once more after ordinary
// optimization/compiler-owned site insertion, so backend-created structural
// work cannot silently escape the certificate boundary.
func VerifyCoroAtomicCostModule(module llvm.Module) (CoroAtomicCostReport, error) {
	report := CoroAtomicCostReport{Schema: coroAtomicCostReportSchema}
	if module.IsNil() {
		return report, fmt.Errorf("ssa: verify atomic-cost metadata in a nil module")
	}
	records, err := readCoroAtomicCostMetadata(module)
	if err != nil {
		return report, err
	}
	if len(records) == 0 {
		return report, nil
	}
	boundedCompilerCallKind := module.Context().MDKindID(CoroAtomicBoundedCompilerCallMetadataName)
	functions := make(map[string]coroAtomicLLVMFunction, len(records))
	for symbol, record := range records {
		function := module.NamedFunction(symbol)
		if function.IsNil() {
			return report, fmt.Errorf("ssa: atomic-cost symbol %q has no LLVM declaration", symbol)
		}
		if !record.local {
			if !function.IsDeclaration() {
				return report, fmt.Errorf("ssa: imported atomic-cost symbol %q unexpectedly has a local definition", symbol)
			}
			continue
		}
		if function.IsDeclaration() {
			return report, fmt.Errorf("ssa: atomic-cost symbol %q has no local LLVM definition", symbol)
		}
		projection, err := projectCoroAtomicLLVMFunction(function, records, boundedCompilerCallKind)
		if err != nil {
			return report, err
		}
		projection.record = record
		functions[symbol] = projection
	}

	state := make(map[string]uint8, len(functions))
	costs := make(map[string]uint64, len(functions))
	var functionCost func(string) (uint64, error)
	functionCost = func(symbol string) (uint64, error) {
		if record := records[symbol]; !record.local {
			return record.cost, nil
		}
		switch state[symbol] {
		case 1:
			return 0, fmt.Errorf("ssa: atomic-cost LLVM call graph contains a cycle at %q", symbol)
		case 2:
			return costs[symbol], nil
		}
		state[symbol] = 1
		function := functions[symbol]
		blockState := make([]uint8, len(function.blocks))
		blockCosts := make([]uint64, len(function.blocks))
		visited := 0
		var blockCost func(int) (uint64, error)
		blockCost = func(index int) (uint64, error) {
			switch blockState[index] {
			case 1:
				return 0, fmt.Errorf("ssa: atomic-cost LLVM function %q contains a CFG cycle", symbol)
			case 2:
				return blockCosts[index], nil
			}
			blockState[index] = 1
			visited++
			cost := function.blocks[index].localCost
			for _, callee := range function.blocks[index].calls {
				calleeCost, err := functionCost(callee)
				if err != nil {
					return 0, err
				}
				if math.MaxUint64-cost < calleeCost {
					return 0, fmt.Errorf("ssa: atomic-cost LLVM path overflows at %q", symbol)
				}
				cost += calleeCost
			}
			maxTail := uint64(0)
			for _, successor := range function.blocks[index].successors {
				tail, err := blockCost(successor)
				if err != nil {
					return 0, err
				}
				if tail > maxTail {
					maxTail = tail
				}
			}
			if math.MaxUint64-cost < maxTail {
				return 0, fmt.Errorf("ssa: atomic-cost LLVM path overflows at %q", symbol)
			}
			blockCosts[index] = cost + maxTail
			blockState[index] = 2
			return blockCosts[index], nil
		}
		cost, err := blockCost(0)
		if err != nil {
			return 0, err
		}
		if cost == 0 || visited != len(function.blocks) {
			return 0, fmt.Errorf("ssa: atomic-cost LLVM function %q has unreachable or zero-cost blocks", symbol)
		}
		state[symbol] = 2
		costs[symbol] = cost
		return cost, nil
	}

	symbols := make([]string, 0, len(functions))
	for symbol := range functions {
		symbols = append(symbols, symbol)
	}
	sort.Strings(symbols)
	for _, symbol := range symbols {
		llvmCost, err := functionCost(symbol)
		if err != nil {
			return report, err
		}
		record := records[symbol]
		report.Functions = append(report.Functions, CoroAtomicCostFunctionReport{
			Symbol: symbol, Certificate: record.certificate, SemanticCost: record.cost,
			Proof: record.proof, LLVMMaxCost: llvmCost, Local: record.local,
		})
	}
	payload, err := json.Marshal(struct {
		Schema    string                         `json:"schema"`
		Functions []CoroAtomicCostFunctionReport `json:"functions"`
	}{Schema: report.Schema, Functions: report.Functions})
	if err != nil {
		return report, fmt.Errorf("ssa: marshal post-LLVM atomic-cost report: %w", err)
	}
	digest := sha256.Sum256(payload)
	report.Digest = hex.EncodeToString(digest[:])
	return report, nil
}

func readCoroAtomicCostMetadata(module llvm.Module) (map[string]coroAtomicMetadataRecord, error) {
	rows := module.NamedMetadataOperands(CoroAtomicCostMetadataName)
	if len(rows) == 0 {
		return nil, nil
	}
	records := make(map[string]coroAtomicMetadataRecord, len(rows))
	for index, row := range rows {
		fields := row.MDNodeOperands()
		if len(fields) != 6 || fields[0].IsAConstantInt().IsNil() ||
			fields[2].IsAConstantInt().IsNil() || fields[3].IsAConstantInt().IsNil() ||
			fields[5].IsAConstantInt().IsNil() ||
			!fields[1].IsAMDString() || !fields[4].IsAMDString() {
			return nil, fmt.Errorf("ssa: malformed atomic-cost metadata row %d", index)
		}
		if fields[0].Type().IntTypeWidth() != 32 || fields[2].Type().IntTypeWidth() != 64 ||
			fields[3].Type().IntTypeWidth() != 32 || fields[5].Type().IntTypeWidth() != 32 {
			return nil, fmt.Errorf("ssa: malformed atomic-cost metadata integer widths in row %d", index)
		}
		if fields[0].ZExtValue() != coroAtomicCostMetadataV1 {
			return nil, fmt.Errorf("ssa: unsupported atomic-cost metadata version %d", fields[0].ZExtValue())
		}
		proofValue := fields[3].ZExtValue()
		record := coroAtomicMetadataRecord{
			symbol: fields[1].MDString(), cost: fields[2].ZExtValue(),
			proof: uint8(proofValue), certificate: fields[4].MDString(),
			local: fields[5].ZExtValue() == 1,
		}
		if fields[5].ZExtValue() > 1 || proofValue == 0 || proofValue > 2 || record.symbol == "" || record.cost == 0 ||
			len(record.certificate) != sha256.Size*2 {
			return nil, fmt.Errorf("ssa: invalid atomic-cost metadata row %d", index)
		}
		if _, err := hex.DecodeString(record.certificate); err != nil {
			return nil, fmt.Errorf("ssa: atomic-cost metadata row %d has invalid certificate: %w", index, err)
		}
		if previous, duplicate := records[record.symbol]; duplicate {
			if previous != record {
				return nil, fmt.Errorf("ssa: conflicting atomic-cost metadata for %q", record.symbol)
			}
			continue
		}
		records[record.symbol] = record
	}
	return records, nil
}

func projectCoroAtomicLLVMFunction(
	function llvm.Value,
	records map[string]coroAtomicMetadataRecord,
	boundedCompilerCallKind int,
) (coroAtomicLLVMFunction, error) {
	var projection coroAtomicLLVMFunction
	blocks := make([]llvm.BasicBlock, 0)
	indexes := make(map[llvm.BasicBlock]int)
	for block := function.FirstBasicBlock(); !block.IsNil(); block = llvm.NextBasicBlock(block) {
		indexes[block] = len(blocks)
		blocks = append(blocks, block)
	}
	if len(blocks) == 0 {
		return projection, fmt.Errorf("ssa: atomic-cost LLVM function %q has no body", function.Name())
	}
	projection.blocks = make([]coroAtomicLLVMBlock, len(blocks))
	for index, block := range blocks {
		projected := coroAtomicLLVMBlock{}
		for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
			if projected.localCost == math.MaxUint64 {
				return projection, fmt.Errorf("ssa: atomic-cost LLVM work overflows at %q", function.Name())
			}
			projected.localCost++
			opcode := instruction.InstructionOpcode()
			switch opcode {
			case llvm.Call:
				callee := instruction.CalledValue().Name()
				if strings.HasPrefix(callee, "llvm.coro.") {
					return projection, fmt.Errorf("ssa: atomic-cost LLVM function %q contains coroutine intrinsic %q", function.Name(), callee)
				}
				if strings.HasPrefix(callee, "llvm.dbg.") || strings.HasPrefix(callee, "llvm.lifetime.") || callee == "llvm.assume" {
					continue
				}
				if isCoroAtomicFixedIntegerIntrinsic(instruction) {
					continue
				}
				if isCoroAtomicConstantMemoryIntrinsic(callee) {
					if instruction.OperandsCount() < 3 || instruction.Operand(2).IsAConstantInt().IsNil() ||
						instruction.Operand(2).Type().IntTypeWidth() > 64 {
						return projection, fmt.Errorf(
							"ssa: atomic-cost LLVM function %q contains variable-length memory intrinsic %q",
							function.Name(), callee,
						)
					}
					byteCount := instruction.Operand(2).ZExtValue()
					if math.MaxUint64-projected.localCost < byteCount {
						return projection, fmt.Errorf("ssa: atomic-cost LLVM work overflows at %q", function.Name())
					}
					projected.localCost += byteCount
					continue
				}
				if callee == "" {
					bounded, err := isCoroAtomicBoundedCompilerCall(instruction, boundedCompilerCallKind)
					if err != nil {
						return projection, fmt.Errorf("ssa: atomic-cost LLVM function %q: %w", function.Name(), err)
					}
					if bounded {
						continue
					}
					return projection, fmt.Errorf("ssa: atomic-cost LLVM function %q contains an indirect or inline-assembly call", function.Name())
				}
				if _, certified := records[callee]; !certified {
					return projection, fmt.Errorf("ssa: atomic-cost LLVM function %q calls uncertified helper %q", function.Name(), callee)
				}
				projected.calls = append(projected.calls, callee)
			case llvm.Invoke, llvm.IndirectBr, llvm.Resume, llvm.LandingPad, llvm.CleanupRet,
				llvm.CatchRet, llvm.CatchPad, llvm.CleanupPad, llvm.CatchSwitch,
				coroAtomicCallBrOpcode:
				return projection, fmt.Errorf(
					"ssa: atomic-cost LLVM function %q contains unsupported control opcode %v",
					function.Name(), opcode,
				)
			case llvm.Alloca:
				if instruction.OperandsCount() == 0 || instruction.Operand(0).IsAConstantInt().IsNil() {
					return projection, fmt.Errorf("ssa: atomic-cost LLVM function %q contains a dynamic alloca", function.Name())
				}
			}
		}
		terminator := block.LastInstruction()
		if terminator.IsNil() {
			return projection, fmt.Errorf("ssa: atomic-cost LLVM function %q has an unterminated block", function.Name())
		}
		for successor := 0; successor < terminator.SuccessorsCount(); successor++ {
			target := terminator.Successor(successor)
			targetIndex, ok := indexes[target]
			if !ok {
				return projection, fmt.Errorf("ssa: atomic-cost LLVM function %q has an external CFG edge", function.Name())
			}
			projected.successors = append(projected.successors, targetIndex)
		}
		sort.Ints(projected.successors)
		projection.blocks[index] = projected
	}
	return projection, nil
}

func isCoroAtomicConstantMemoryIntrinsic(name string) bool {
	return strings.HasPrefix(name, "llvm.memset.") ||
		strings.HasPrefix(name, "llvm.memcpy.") ||
		strings.HasPrefix(name, "llvm.memmove.")
}

// isCoroAtomicFixedIntegerIntrinsic admits the scalar, fixed-width intrinsics
// which InstCombine may form from ordinary integer operations. Check LLVM's
// intrinsic identity, canonical overloaded name, and complete signature: a
// source declaration with a suggestive llvm.* name must not gain an
// atomic-cost capability.
func isCoroAtomicFixedIntegerIntrinsic(call llvm.Value) bool {
	callee := call.CalledValue()
	if callee.IsNil() {
		return false
	}
	functionType := call.CalledFunctionType()
	if functionType.IsNil() || functionType.TypeKind() != llvm.FunctionTypeKind ||
		functionType.IsFunctionVarArg() {
		return false
	}
	parameters := functionType.ParamTypes()
	resultType := functionType.ReturnType()
	if resultType.TypeKind() != llvm.IntegerTypeKind ||
		resultType.IntTypeWidth() == 0 || resultType.IntTypeWidth() > 64 {
		return false
	}
	width := resultType.IntTypeWidth()
	canonical := func(base string) bool {
		return callee.IntrinsicID() == llvm.LookupIntrinsicID(base) &&
			callee.Name() == fmt.Sprintf("%s.i%d", base, width)
	}
	if len(parameters) == 2 && parameters[0] == resultType && parameters[1] == resultType {
		for _, base := range []string{"llvm.umin", "llvm.umax", "llvm.smin", "llvm.smax"} {
			if canonical(base) {
				return true
			}
		}
	}
	// llvm.abs has one data operand and one i1 poison flag. On scalar Go
	// integer widths it lowers to a finite compare/negate/select recipe; unlike
	// a target-dependent bit-count intrinsic it cannot introduce a hidden
	// compiler-rt call after this structural verifier.
	if len(parameters) == 2 && parameters[0] == resultType &&
		parameters[1].TypeKind() == llvm.IntegerTypeKind &&
		parameters[1].IntTypeWidth() == 1 && canonical("llvm.abs") {
		return true
	}
	return false
}

func isCoroAtomicBoundedCompilerCall(call llvm.Value, metadataKind int) (bool, error) {
	callee := call.CalledValue()
	if callee.IsNil() || callee.IsAInlineAsm().IsNil() {
		return false, nil
	}
	node := call.Metadata(metadataKind)
	if node.IsNil() {
		return false, nil
	}
	fields := node.MDNodeOperands()
	if len(fields) != 3 || fields[0].IsAConstantInt().IsNil() || !fields[1].IsAMDString() || !fields[2].IsAMDString() {
		return false, fmt.Errorf("contains malformed bounded compiler-call metadata")
	}
	if fields[0].Type().IntTypeWidth() != 32 {
		return false, fmt.Errorf("contains malformed bounded compiler-call metadata version width")
	}
	if fields[0].ZExtValue() != coroAtomicBoundedCompilerCallV1 {
		return false, fmt.Errorf("contains unsupported bounded compiler-call metadata version %d", fields[0].ZExtValue())
	}
	identity := fields[1].MDString()
	if identity != CoroAtomicCompilerDataAnchorV1 {
		return false, fmt.Errorf("contains unknown bounded compiler-call identity %q", identity)
	}
	want := coroAtomicInlineAsmDigest(identity, callee)
	if fields[2].MDString() != want {
		return false, fmt.Errorf("bounded compiler call %q has a mismatched content digest", identity)
	}
	return true, nil
}

func coroAtomicInlineAsmDigest(identity string, asm llvm.Value) string {
	payload, err := json.Marshal(struct {
		Identity    string `json:"identity"`
		Assembly    string `json:"assembly"`
		Constraints string `json:"constraints"`
		SideEffects bool   `json:"side_effects"`
		AlignStack  bool   `json:"align_stack"`
		Dialect     uint32 `json:"dialect"`
		CanThrow    bool   `json:"can_throw"`
	}{
		Identity: identity, Assembly: asm.InlineAsmString(), Constraints: asm.InlineAsmConstraintString(),
		SideEffects: asm.InlineAsmHasSideEffects(), AlignStack: asm.InlineAsmNeedsAlignedStack(),
		Dialect: uint32(asm.InlineAsmDialect()), CanThrow: asm.InlineAsmCanThrow(),
	})
	if err != nil {
		panic("ssa: marshal inline-assembly certificate: " + err.Error())
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}
