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
	"fmt"

	"github.com/goplus/llgo/internal/coro"
)

// CoroControlOperation is the target-independent semantic identity of an
// inline control intrinsic. It is frozen on the exact source call site before
// whole-program analysis; later phases must not recover it from a C symbol
// name or function address.
type CoroControlOperation uint8

const (
	CoroControlNone CoroControlOperation = iota
	CoroControlReturnsTwice
	CoroControlNonlocalJump
	CoroControlProcessFork
	CoroControlProcessExec
	CoroControlProcessExit
	CoroControlTrap
)

func (operation CoroControlOperation) String() string {
	switch operation {
	case CoroControlNone:
		return "none"
	case CoroControlReturnsTwice:
		return "returns-twice"
	case CoroControlNonlocalJump:
		return "nonlocal-jump"
	case CoroControlProcessFork:
		return "process-fork"
	case CoroControlProcessExec:
		return "process-exec"
	case CoroControlProcessExit:
		return "process-exit"
	case CoroControlTrap:
		return "trap"
	default:
		return fmt.Sprintf("control-operation(%d)", uint8(operation))
	}
}

// ExecFlags returns occurrence-local execution constraints. A typed control
// leaf is direct and non-suspending, but that does not prove async-signal
// safety. Terminality remains a call-site/CFG fact: joining NoReturn into an
// arbitrary conditional caller would incorrectly claim the whole function
// cannot return.
func (operation CoroControlOperation) ExecFlags() coro.ExecFlags {
	if operation == CoroControlNone {
		return 0
	}
	return coro.IRQUnsafe
}

// Terminal reports that normal control never continues past this exact
// operation. Codegen emits both the backend noreturn contract and an explicit
// unreachable terminator; source SSA after the call is retained only in a
// detached structural continuation.
func (operation CoroControlOperation) Terminal() bool {
	switch operation {
	case CoroControlNonlocalJump, CoroControlProcessExit, CoroControlTrap:
		return true
	default:
		return false
	}
}

// NativeActivationBound reports operations whose state names the current
// native stack activation. LLVM may split and resume a stackless coroutine on
// another activation, so these operations are valid only in a plain/raw
// native-stack body or a future explicit native-region adapter.
func (operation CoroControlOperation) NativeActivationBound() bool {
	return operation == CoroControlReturnsTwice ||
		operation == CoroControlNonlocalJump
}

func coroControlOperationForIntrinsic(opcode int) CoroControlOperation {
	switch opcode {
	case llgoSigsetjmp:
		return CoroControlReturnsTwice
	case llgoSiglongjmp:
		return CoroControlNonlocalJump
	case llgoControlFork:
		return CoroControlProcessFork
	case llgoControlExecve:
		return CoroControlProcessExec
	case llgoControlExit:
		return CoroControlProcessExit
	case llgoControlTrap:
		return CoroControlTrap
	default:
		return CoroControlNone
	}
}
