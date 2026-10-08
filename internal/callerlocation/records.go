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

// Package callerlocation simplifies compiler-generated source location records.
package callerlocation

import "github.com/xgo-dev/llvm"

const runtimePrefix = "github.com/xgo-dev/llgo/runtime/internal/runtime."

// DeduplicateWasmRecords removes identical location records within a basic
// block when no other call intervenes and the debug locations match. Large
// initializers otherwise repeat the same record for every SSA operation on a
// source line, causing quadratic work in WebAssembly register stackification.
// Non-record calls and block boundaries invalidate the remembered record,
// because they can change the caller state. Distinct debug locations remain
// as stepping and variable-location anchors.
//
// It returns the number of removed calls.
func DeduplicateWasmRecords(mod llvm.Module) int {
	removed := 0
	for fn := mod.FirstFunction(); !fn.IsNil(); fn = llvm.NextFunction(fn) {
		for block := fn.FirstBasicBlock(); !block.IsNil(); block = llvm.NextBasicBlock(block) {
			var previous llvm.Value
			for instr := block.FirstInstruction(); !instr.IsNil(); {
				next := llvm.NextInstruction(instr)
				if !instr.IsACallInst().IsNil() {
					if isRecord(instr) {
						if sameCall(previous, instr) && previous.InstructionDebugLoc() == instr.InstructionDebugLoc() {
							instr.EraseFromParentAsInstruction()
							removed++
						} else {
							previous = instr
						}
					} else {
						previous = llvm.Value{}
					}
				}
				instr = next
			}
		}
	}
	return removed
}

func isRecord(call llvm.Value) bool {
	callee := call.CalledValue()
	if callee.IsAFunction().IsNil() {
		return false
	}
	switch callee.Name() {
	case runtimePrefix + "RecordPanicLocationWasm", runtimePrefix + "RecordCallerLocationWasm":
		return true
	}
	return false
}

func sameCall(left, right llvm.Value) bool {
	if left.IsNil() {
		return false
	}
	n := left.OperandsCount()
	if n != right.OperandsCount() {
		return false
	}
	for i := 0; i < n; i++ {
		if left.Operand(i) != right.Operand(i) {
			return false
		}
	}
	return true
}
