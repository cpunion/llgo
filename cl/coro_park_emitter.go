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

// coroParkFaultRoute maps one source-specific resume status to the canonical
// terminal-fault path. Keeping the route semantic prevents feature lowerers
// from injecting arbitrary physical dispatch callbacks into the envelope.
type coroParkFaultRoute struct {
	status uint64
	kind   uint32
}

// coroParkOperation is the target-neutral compiler envelope shared by one
// timer, poll, worker, channel, or WaitSet park. Feature lowerers bind only
// their typed park/resume hooks, finite status vocabulary, and terminal fault
// outcomes; the emitter owns the state transition, cancellation targets,
// fail-closed default, and joined continuation.
// Multi-candidate channel/select winner reconciliation remains one typed
// WaitSet transaction inside these hooks and is never represented as several
// independent parks.
type coroParkOperation struct {
	shouldSuspend llssa.Expr
	park          func(llssa.Builder)
	resume        func(llssa.Builder) llssa.Expr
	normal        []uint64
	faults        []coroParkFaultRoute
	abort         uint64
	shutdown      uint64
}

const maxCoroParkResumeStatus = uint64(^uint32(0))

func validateCoroParkOperationStatuses(
	normal []uint64,
	faults []coroParkFaultRoute,
	abort, shutdown uint64,
) error {
	if len(normal) == 0 {
		return fmt.Errorf("coroutine park operation has no normal resume status")
	}
	seen := make(map[uint64]string, len(normal)+len(faults)+2)
	add := func(kind string, status uint64) error {
		if status > maxCoroParkResumeStatus {
			return fmt.Errorf("coroutine park %s resume status %d does not fit the uint32 runtime ABI", kind, status)
		}
		if previous, duplicate := seen[status]; duplicate {
			return fmt.Errorf("coroutine park %s resume status %d duplicates %s status", kind, status, previous)
		}
		seen[status] = kind
		return nil
	}
	for _, status := range normal {
		if err := add("normal", status); err != nil {
			return err
		}
	}
	for index, route := range faults {
		if route.kind == 0 || route.kind >= coroFaultLimitV1 {
			return fmt.Errorf("coroutine park fault resume route %d has invalid fault kind %d", index, route.kind)
		}
		if err := add("fault", route.status); err != nil {
			return err
		}
	}
	if err := add("abort", abort); err != nil {
		return err
	}
	if err := add("shutdown", shutdown); err != nil {
		return err
	}
	return nil
}

func (c *coroBodyContext) emitCoroParkOperation(p *context, b llssa.Builder, operation coroParkOperation) {
	if c == nil || p == nil || b == nil || b.Func != p.fn || c.coro == nil || c.unsupportedRunDecision == nil ||
		operation.shouldSuspend.IsNil() || operation.park == nil || operation.resume == nil {
		panic("coroutine park operation requires a complete physical emitter and protocol")
	}
	if err := validateCoroParkOperationStatuses(
		operation.normal,
		operation.faults,
		operation.abort,
		operation.shutdown,
	); err != nil {
		panic(err)
	}
	if operation.shouldSuspend.Type != b.Prog.Bool() {
		panic("coroutine park suspend predicate must be bool")
	}
	faultTargets := make([]llssa.BasicBlock, len(operation.faults))
	for index := range faultTargets {
		faultTargets[index] = b.Func.MakeBlock()
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
			for index, route := range operation.faults {
				dispatch.Case(resume.Prog.IntVal(route.status, resume.Prog.Uint32()), faultTargets[index])
			}
			dispatch.Case(resume.Prog.IntVal(operation.abort, resume.Prog.Uint32()), abort)
			dispatch.Case(resume.Prog.IntVal(operation.shutdown, resume.Prog.Uint32()), shutdown)
			dispatch.End(resume)
		},
	)
	for index, target := range faultTargets {
		b.SetBlockEx(target, llssa.AtEnd, false)
		p.compileCoroTerminalFault(b, operation.faults[index].kind)
	}
	b.SetBlock(join)
	c.activate(b)
}
