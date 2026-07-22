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
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

func (c *coroBodyContext) criticalCallDepth(common *ssa.CallCommon) (coroCriticalCallRole, uint32) {
	if c == nil || c.critical == nil || common == nil {
		panic("coroutine critical lowering requires a frozen CFG proof")
	}
	for call, role := range c.critical.roles {
		if call != nil && call.Common() == common {
			depth, ok := c.critical.beforeDepth[call]
			if !ok {
				panic("coroutine critical marker has no proven input depth")
			}
			return role, depth
		}
	}
	panic("coroutine critical lowering received an unproved marker")
}

func (p *context) compileCoroCriticalEnter(b llssa.Builder, common *ssa.CallCommon) {
	body := p.coroBody()
	if body == nil || b.Func != p.fn || body.criticalEnter.IsNil() {
		panic("llgo.coroCriticalEnter requires an active critical-capable coroutine body")
	}
	role, depth := body.criticalCallDepth(common)
	if role != coroCriticalCallEnter {
		panic("llgo.coroCriticalEnter disagrees with its frozen marker role")
	}
	// Entering the outer mask is itself a safepoint. Once the runtime depth is
	// nonzero no source poll may be emitted until the matching outer exit.
	if depth == 0 && body.needsPreempt && !body.sourceBlockPollFresh {
		body.pollAndSuspendForPreempt(b)
	}
	b.Call(body.criticalEnter, body.task)
	if depth == 0 {
		body.instructions = 0
	}
	body.sourceBlockPollFresh = false
}

func (p *context) compileCoroCriticalExit(b llssa.Builder, common *ssa.CallCommon) {
	body := p.coroBody()
	if body == nil || b.Func != p.fn || body.criticalExit.IsNil() {
		panic("llgo.coroCriticalExit requires an active critical-capable coroutine body")
	}
	role, depth := body.criticalCallDepth(common)
	if role != coroCriticalCallExit || depth == 0 {
		panic("llgo.coroCriticalExit disagrees with its frozen marker role/depth")
	}
	requested := b.Call(body.criticalExit, body.task)
	if depth == 1 {
		body.suspendCurrentFrameIfYieldRequested(b, requested)
		body.instructions = 0
		body.sourceBlockPollFresh = true
	}
}
