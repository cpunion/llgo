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

	llssa "github.com/goplus/llgo/ssa"
)

// coroParkOperation is the target-neutral compiler protocol shared by one
// timer, poll, or bounded-worker wait. Feature lowerers bind only their park
// hook, resume hook, and finite status vocabulary; the emitter owns the state
// transition, cancellation targets, fail-closed default, and continuation.
// Multi-candidate channel/select winner reconciliation is deliberately a
// separate protocol and must not be represented as several independent parks.
type coroParkOperation struct {
	shouldSuspend llssa.Expr
	park          func(llssa.Builder)
	resume        func(llssa.Builder) llssa.Expr
	normal        []uint64
	abort         uint64
	shutdown      uint64
}

const maxCoroParkResumeStatus = uint64(^uint32(0))

func validateCoroParkOperationStatuses(normal []uint64, abort, shutdown uint64) error {
	if len(normal) == 0 {
		return fmt.Errorf("coroutine park operation has no normal resume status")
	}
	seen := make(map[uint64]struct{}, len(normal)+2)
	for _, status := range normal {
		if status > maxCoroParkResumeStatus {
			return fmt.Errorf("coroutine park normal resume status %d does not fit the uint32 runtime ABI", status)
		}
		if _, duplicate := seen[status]; duplicate {
			return fmt.Errorf("coroutine park operation repeats resume status %d", status)
		}
		seen[status] = struct{}{}
	}
	if abort > maxCoroParkResumeStatus {
		return fmt.Errorf("coroutine park abort status %d does not fit the uint32 runtime ABI", abort)
	}
	if _, collision := seen[abort]; collision {
		return fmt.Errorf("coroutine park abort status %d is also normal", abort)
	}
	seen[abort] = struct{}{}
	if shutdown > maxCoroParkResumeStatus {
		return fmt.Errorf("coroutine park shutdown status %d does not fit the uint32 runtime ABI", shutdown)
	}
	if _, collision := seen[shutdown]; collision {
		return fmt.Errorf("coroutine park shutdown status %d is not distinct", shutdown)
	}
	return nil
}

func (c *coroBodyContext) emitCoroParkOperation(b llssa.Builder, operation coroParkOperation) {
	if c == nil || b == nil || c.coro == nil || c.unsupportedRunDecision == nil ||
		operation.shouldSuspend.IsNil() || operation.park == nil || operation.resume == nil {
		panic("coroutine park operation requires a complete physical emitter and protocol")
	}
	if err := validateCoroParkOperationStatuses(operation.normal, operation.abort, operation.shutdown); err != nil {
		panic(err)
	}
	if operation.shouldSuspend.Type != b.Prog.Bool() {
		panic("coroutine park suspend predicate must be bool")
	}
	join := c.coro.SuspendCurrentBlockIfWithResumeDispatch(
		operation.shouldSuspend,
		func(suspend llssa.Builder) {
			stateID := c.nextState
			c.nextState++
			c.instructions = 0
			c.publishState(suspend, coroSuspendPark, coroLifecycleSuspended, stateID)
			operation.park(suspend)
		},
		func(resume llssa.Builder, normal llssa.BasicBlock) {
			status := operation.resume(resume)
			if status.IsNil() {
				panic("coroutine park resume hook returned no status")
			}
			if status.Type != resume.Prog.Uint32() {
				panic("coroutine park resume hook must return a uint32 runtime status")
			}
			abort, shutdown := c.cancellationRunDecisionTargets(resume)
			dispatch := resume.Switch(status, c.unsupportedRunDecision)
			for _, value := range operation.normal {
				dispatch.Case(resume.Prog.IntVal(value, resume.Prog.Uint32()), normal)
			}
			dispatch.Case(resume.Prog.IntVal(operation.abort, resume.Prog.Uint32()), abort)
			dispatch.Case(resume.Prog.IntVal(operation.shutdown, resume.Prog.Uint32()), shutdown)
			dispatch.End(resume)
		},
	)
	b.SetBlock(join)
	c.activate(b)
}
