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

// coroPhysicalEmissionPhase makes installation of a physical body an explicit
// transaction. Prologue callbacks may consume the frozen plan, but ordinary
// source lowering cannot observe the body or source-block map until both have
// been bound atomically.
type coroPhysicalEmissionPhase uint8

const (
	coroPhysicalEmissionPrologue coroPhysicalEmissionPhase = iota + 1
	coroPhysicalEmissionBody
	coroPhysicalEmissionComplete
)

// coroPhysicalEmissionSession is the sole mutable owner of state that exists
// only while one physical coroutine body is emitted. Keeping the plan, body,
// nested site ledger, source-block projection, and physical parameter layout in
// one session prevents independently installed context fields from describing
// different functions after a failed or nested emission.
type coroPhysicalEmissionSession struct {
	phase           coroPhysicalEmissionPhase
	plan            *coroPhysicalFunctionPlan
	body            *coroBodyContext
	site            *coroSiteEmissionObserver
	sourceBlocks    []llssa.BasicBlock
	sourceParamBase int
	explicitStatus  bool
}

// beginCoroPhysicalEmission installs the complete prologue-visible portion of
// a session in one operation. Physical emission is deliberately non-nestable:
// deferred function bodies use their own later initializer and must not borrow
// the caller's body, plan, or site observer.
func (p *context) beginCoroPhysicalEmission(
	plan *coroPhysicalFunctionPlan,
	sourceParamBase int,
	explicitStatus bool,
) (*coroPhysicalEmissionSession, func()) {
	if p == nil || plan == nil || sourceParamBase < 2 {
		panic("coroutine physical emission requires a context, frozen plan, and physical parameter base")
	}
	if p.coroEmission != nil {
		panic("nested coroutine physical emission session")
	}
	if p.coroPlainSite != nil {
		panic("coroutine physical emission overlaps a plain source SitePlan observer")
	}
	session := &coroPhysicalEmissionSession{
		phase:           coroPhysicalEmissionPrologue,
		plan:            plan,
		sourceParamBase: sourceParamBase,
		explicitStatus:  explicitStatus,
	}
	p.coroEmission = session
	return session, func() {
		recovered := recover()
		if p.coroEmission != session {
			panic("coroutine physical emission session ownership changed before close")
		}
		p.coroEmission = nil
		if recovered != nil {
			panic(recovered)
		}
		if session.site != nil {
			panic("coroutine physical emission closed with an active source SitePlan observer")
		}
		if session.phase != coroPhysicalEmissionComplete {
			panic(fmt.Sprintf("coroutine physical emission closed in phase %d", session.phase))
		}
	}
}

// bindCoroPhysicalBody publishes the body and source-block projection together.
// No ordinary lowering consumer can observe one without the other.
func (s *coroPhysicalEmissionSession) bindCoroPhysicalBody(
	body *coroBodyContext,
	sourceBlocks []llssa.BasicBlock,
) {
	if s == nil || s.phase != coroPhysicalEmissionPrologue || s.plan == nil || s.body != nil || body == nil || len(sourceBlocks) == 0 {
		panic("coroutine physical body may be bound exactly once after a complete prologue")
	}
	s.body = body
	s.sourceBlocks = sourceBlocks
	s.phase = coroPhysicalEmissionBody
}

func (s *coroPhysicalEmissionSession) completeCoroPhysicalBody(body *coroBodyContext) {
	if s == nil || s.phase != coroPhysicalEmissionBody || s.body == nil || s.body != body || s.site != nil {
		panic("coroutine physical body may complete exactly once with no active source SitePlan observer")
	}
	s.phase = coroPhysicalEmissionComplete
}

// coroBody is intentionally available only to coroutine-specific lowering
// modules. The ordinary SSA compiler uses semantic adapter methods instead of
// reading physical state directly.
func (p *context) coroBody() *coroBodyContext {
	return p.activeCoroEmissionBody()
}

func (p *context) activeCoroEmissionBody() *coroBodyContext {
	if p == nil {
		return nil
	}
	session := p.coroEmission
	if session == nil || session.phase != coroPhysicalEmissionBody {
		return nil
	}
	return session.body
}

func (p *context) hasCoroPhysicalBody() bool {
	return p.coroBody() != nil
}

// coroTask and coroCleanup expose narrow capabilities owned by the active
// physical-emission session. Lowerers that need only one capability must not
// retain or inspect the complete mutable body.
func (p *context) coroTask() llssa.Expr {
	body := p.activeCoroEmissionBody()
	if body == nil {
		return llssa.Expr{}
	}
	return body.task
}

func (p *context) coroCleanup() *coroStaticCleanupState {
	body := p.activeCoroEmissionBody()
	if body == nil {
		return nil
	}
	return body.cleanup
}

// emitCoroPanicTraceReplacement exposes the narrow cleanup capability without
// giving defer lowering another direct dependency on the complete mutable
// physical body. The architecture debt gate deliberately keeps coroBody
// access centralized while this exact runtime transaction remains available
// to the static cleanup emitter.
func (p *context) emitCoroPanicTraceReplacement(b llssa.Builder) {
	body := p.activeCoroEmissionBody()
	if body == nil || b == nil || b.Func != p.fn || body.panicTraceReplace.IsNil() {
		panic("inline coroutine panic replacement requires an exact runtime hook")
	}
	b.Call(body.panicTraceReplace, body.task, body.coro.Handle())
}

func (p *context) hasCoroPhysicalEmission() bool {
	return p != nil && p.coroEmission != nil
}

func (p *context) coroEmissionPlan() *coroPhysicalFunctionPlan {
	if p == nil || p.coroEmission == nil {
		return nil
	}
	return p.coroEmission.plan
}

func (p *context) coroEmissionExplicitStatus() bool {
	return p != nil && p.coroEmission != nil && p.coroEmission.explicitStatus
}

func (p *context) coroEmissionSourceParamBase() int {
	if p == nil || p.coroEmission == nil {
		return 0
	}
	return p.coroEmission.sourceParamBase
}

func (p *context) coroEmissionSourceBlock(index int) (llssa.BasicBlock, bool) {
	if p == nil || p.coroEmission == nil || p.coroEmission.phase != coroPhysicalEmissionBody {
		return nil, false
	}
	blocks := p.coroEmission.sourceBlocks
	if index < 0 || index >= len(blocks) {
		panic(fmt.Sprintf("source basic block index %d is outside coroutine map of length %d", index, len(blocks)))
	}
	return blocks[index], true
}

func (p *context) coroEmissionSite() *coroSiteEmissionObserver {
	if p == nil {
		return nil
	}
	if p.coroEmission != nil {
		return p.coroEmission.site
	}
	return p.coroPlainSite
}

func (p *context) setCoroEmissionSite(site *coroSiteEmissionObserver) {
	if p == nil {
		panic("coroutine source SitePlan observer requires a compiler context")
	}
	if p.coroEmission != nil {
		p.coroEmission.site = site
		return
	}
	p.coroPlainSite = site
}
