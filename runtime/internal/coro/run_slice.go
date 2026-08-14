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

// executorRunSourceQuantum bounds how long a continuously ready FIFO can avoid
// sampling deadline sources when no producer request is pending. A requested or
// retryable source is serviced immediately; the quantum is only the fallback
// for a hot CPU-only workload.
const executorRunSourceQuantum uint8 = 64

// executorRunSourceBatchQuantum is the bounded number of catalog, resolution,
// and acknowledgement reductions collapsed into one Source host step. The
// resumable A/ack/B transaction remains the single semantic owner; batching
// only avoids re-entering the target/runtime selector between adjacent direct
// core reductions. Typed runtime cleanup is still an explicit Materialize
// boundary, and readyDebt still prevents a completed source epoch from
// starting another epoch before a newly runnable continuation is paid.
const executorRunSourceBatchQuantum uint32 = 16

// executorRunCursor is cold, scheduler-owner-only continuation state. It has
// no callback-visible pointer. readyDebt forces one physical ready action after
// a completed source epoch before a hot source can start another A/ack/B epoch.
// issued marks the no-return interval between selecting a stable action and the
// runtime adapter committing its complete physical reduction.
type executorRunCursor struct {
	sourceMore         bool
	readyDebt          bool
	blocked            bool
	actionsSinceSource uint8
	issued             ActionKind
}

func validExecutorRunCursor(cursor *executorRunCursor, p *P) bool {
	if cursor == nil || p == nil || cursor.actionsSinceSource > executorRunSourceQuantum {
		return false
	}
	switch cursor.issued {
	case ActionInvalid:
	case ActionCheckResume, ActionCheckDestroy, ActionPanicDestroy:
	default:
		return false
	}
	return cursor.issued != ActionInvalid || !cursor.readyDebt || p.current != nil || runnableForOSThreadOwner(p)
}

func emptyExecutorRunCursor(driver *ExecutorDriver) bool {
	return driver != nil && driver.run == (executorRunCursor{}) &&
		emptyOwnerLocalCompletion(&driver.local) &&
		executorDirectChannelInboxIdle(driver)
}

// EnterExecutorRunCompatibility is the only supported stable-idle switch from
// the bounded runner to legacy whole-operation poll/sleep/command-close APIs.
// The final-root receipt has its separate CommitDestroyedReceiptCompatibility
// boundary because P intentionally remains current there. This switch discards
// only cold fairness/accounting state; a started source transaction or issued
// physical action cannot cross it.
func EnterExecutorRunCompatibility(driver *ExecutorDriver) bool {
	if !validExecutorDriver(driver) || driver.state != executorDriverActive ||
		driver.run.issued != ActionInvalid || driver.poll.phase != executorPollIdle ||
		!emptyOwnerLocalCompletion(&driver.local) ||
		!executorDirectChannelInboxIdle(driver) ||
		!idleExecutorScheduler(driver.p) {
		return false
	}
	driver.run = executorRunCursor{}
	return true
}

// EnterExecutorRunStandbyCompatibility is the retained-wait variant of
// EnterExecutorRunCompatibility. false,true means a producer won the stable
// idle boundary by publishing a direct-channel completion, so the owner must
// re-enter the bounded runner instead of treating that ordinary race as
// corruption. Owner-local completion and every in-progress reducer field
// remain hard failures; only the lock-free producer inbox is asynchronous.
func EnterExecutorRunStandbyCompatibility(driver *ExecutorDriver) (entered, ok bool) {
	if EnterExecutorRunCompatibility(driver) {
		return true, true
	}
	if !validExecutorDriver(driver) || driver.state != executorDriverActive ||
		driver.run.issued != ActionInvalid || driver.poll.phase != executorPollIdle ||
		!emptyOwnerLocalCompletion(&driver.local) ||
		executorDirectChannelInboxIdle(driver) ||
		!idleExecutorScheduler(driver.p) {
		return false, false
	}
	return false, true
}

// ExecutorRunStepKind is one reduction selected by the unified resumable core.
// Dispatch remains available when the caller cannot yet prove a managed
// execution lease or has only one budget unit. A lease-holding caller may ask
// the selector to combine that stable transition with the immediately
// following Action; Dispatched records the extra charged reduction. Source
// advances exactly one PollExecutorSlice reduction. Action must be completed
// and committed without returning through another host boundary.
type ExecutorRunStepKind uint8

const (
	ExecutorRunStepInvalid ExecutorRunStepKind = iota
	ExecutorRunStepSource
	ExecutorRunStepMaterialize
	ExecutorRunStepDirectChannel
	ExecutorRunStepDispatch
	ExecutorRunStepAction
	ExecutorRunStepDestroyCommit
	ExecutorRunStepIdle
)

// ExecutorRunStep carries no callback or interface value. Action handles are
// live only for Dispatch/Action. DestroyCommit is always handle-free.
type ExecutorRunStep struct {
	Kind ExecutorRunStepKind
	// Dispatched is valid only for Action. It certifies that this selection also
	// dequeued G and completed BeginRunG, so the reducer charges two units and
	// applies the command-cancellation gate before the physical operation.
	Dispatched bool
	G          *G
	Action     Action
	Poll       ExecutorPollProgress
	Cleanup    ResumeCleanupStep
	Direct     *DirectChannelCompletion
}

// ExecutorRunActionStep is the compact high-frequency half of
// ExecutorRunStep. Poll progress and typed cleanup are cold event payloads;
// carrying their storage through every runnable dequeue and physical resume
// made the common scheduler ABI larger than the state it actually transfers.
// The selector below returns this shape only when the ordinary priority rules
// have already selected Dispatch/Action. Every source, materialize, direct
// completion, destroy receipt, and idle boundary retains ExecutorRunStep.
type ExecutorRunActionStep struct {
	Dispatched bool
	G          *G
	Action     Action
}

func executorRunExternalSourceRequested(driver *ExecutorDriver) bool {
	return executorDirectChannelCompletionPending(driver) || driver.sources.pending(driver.p) ||
		driver.registry.ObserveRequested(driver.handle) ||
		preemptLoad(&driver.p.schedule) != scheduleIdle
}

func executorRunSourceRequested(driver *ExecutorDriver) bool {
	return driver.run.sourceMore || executorRunExternalSourceRequested(driver)
}

func serviceExecutorRunSource(driver *ExecutorDriver, now int64, withDeadline bool) (ExecutorRunStep, bool) {
	var progress ExecutorPollProgress
	var ok bool
	if withDeadline {
		_, progress, ok = pollBoundExecutorSliceAt(
			driver, now, true, executorRunSourceBatchQuantum,
		)
	} else {
		_, progress, ok = pollBoundExecutorSliceAt(
			driver, 0, false, executorRunSourceBatchQuantum,
		)
	}
	if !ok || progress.Used == 0 || progress.Used > executorRunSourceBatchQuantum {
		return ExecutorRunStep{}, false
	}
	if progress.Complete {
		driver.run.sourceMore = executorRunExternalSourceRequested(driver)
		driver.run.blocked = progress.Blocked
		driver.run.actionsSinceSource = 0
		if runnableForOSThreadOwner(driver.p) {
			driver.run.readyDebt = true
		}
	} else {
		driver.run.sourceMore = true
		driver.run.blocked = false
	}
	return ExecutorRunStep{Kind: ExecutorRunStepSource, Poll: progress}, true
}

// serviceExecutorRunLocal advances one completion which was published by the
// currently owning G on this same P. It deliberately uses the ordinary Source
// host step so target-side ready distribution and readyDebt keep one common
// boundary, but AtomicResolve identifies that no catalog scan or executor
// acknowledgement was performed.
const ownerLocalResolveBatchQuantum uint32 = 16

func serviceExecutorRunLocal(driver *ExecutorDriver) (ExecutorRunStep, bool) {
	var applyVisits, promoted uint32
	complete := false
	resolved := &driver.local.scratch
	if *resolved != (publishedEpochResolveStep{}) {
		return ExecutorRunStep{}, false
	}
	for reduction := uint32(0); reduction < ownerLocalResolveBatchQuantum; reduction++ {
		// Typed runtime cleanup is the only boundary which cannot be reduced by
		// the target-neutral core. Return it as the next explicit Materialize
		// step; the following local service entry continues from the same cursor.
		if _, pending := pendingResumeCleanupStepForCursor(&driver.local.resolve); pending {
			break
		}
		if !resolveOwnerLocalCompletionStep(driver, resolved) ||
			resolved.applyVisits < 0 || resolved.promoted < 0 ||
			uint64(applyVisits)+uint64(resolved.applyVisits) > uint64(^uint32(0)) ||
			uint64(promoted)+uint64(resolved.promoted) > uint64(^uint32(0)) {
			*resolved = publishedEpochResolveStep{}
			return ExecutorRunStep{}, false
		}
		applyVisits += uint32(resolved.applyVisits)
		promoted += uint32(resolved.promoted)
		resolvedComplete := resolved.complete
		*resolved = publishedEpochResolveStep{}
		if resolvedComplete {
			complete = true
			break
		}
	}
	if complete {
		if ownerLocalCompletionPending(driver) {
			return ExecutorRunStep{}, false
		}
		driver.run.blocked = false
		driver.run.actionsSinceSource = 0
		if runnableForOSThreadOwner(driver.p) {
			driver.run.readyDebt = true
		}
	}
	progress := ExecutorPollProgress{
		Used:          1,
		ApplyVisits:   applyVisits,
		Promoted:      promoted,
		Complete:      complete,
		More:          !complete || runnableForOSThreadOwner(driver.p) || executorRunExternalSourceRequested(driver),
		AtomicResolve: true,
	}
	return ExecutorRunStep{Kind: ExecutorRunStepSource, Poll: progress}, true
}

// nextExecutorRunLocalStep is deliberately kept out of the common selector.
// Resolving and materializing an owner-local completion is a selected cold
// path; inlining its typed cursor machinery into every ordinary runnable or
// compute reduction expands the scheduler hot loop even when the local FIFO is
// empty.
//
//go:noinline
func nextExecutorRunLocalStep(driver *ExecutorDriver) (ExecutorRunStep, bool) {
	if cleanup, pending := pendingResumeCleanupStepForCursor(&driver.local.resolve); pending {
		return ExecutorRunStep{Kind: ExecutorRunStepMaterialize, Cleanup: cleanup}, true
	}
	return serviceExecutorRunLocal(driver)
}

// CommitExecutorRunSourceDistribution closes the optional target-side ready
// distribution boundary after one complete Source reduction. Source completion
// records readyDebt before returning so a hot source cannot starve a newly
// materialized continuation. If the target durably transferred the last such
// continuation to another demanded route, that debt has been physically paid
// by the transfer and must not make the now-empty source driver invalid.
//
// The target reports only whether a durable transfer was published. It never
// mutates the runner cursor directly. A remaining local runnable retains the
// debt and therefore still wins before another source epoch.
func CommitExecutorRunSourceDistribution(driver *ExecutorDriver, distributed bool) bool {
	if driver == nil || driver.state != executorDriverActive || driver.p == nil ||
		driver.run.issued != ActionInvalid || driver.poll.phase != executorPollIdle ||
		driver.p.current != nil {
		return false
	}
	if !distributed || runnableForOSThreadOwner(driver.p) {
		return validExecutorDriverHeader(driver)
	}
	if !driver.run.readyDebt {
		return false
	}
	driver.run.readyDebt = false
	if validExecutorDriverHeader(driver) {
		return true
	}
	// Preserve the fail-closed diagnostic state if an unrelated invariant was
	// already broken; callers must reject the complete source reduction.
	driver.run.readyDebt = true
	return false
}

func dispatchExecutorRunReadyAction(
	driver *ExecutorDriver,
	selected *G,
	combineAction bool,
) (ExecutorRunActionStep, bool) {
	p := driver.p
	if selected == nil || p == nil {
		return ExecutorRunActionStep{}, false
	}
	// nextExecutorRunActionValidated selected this task immediately before this
	// call from the same owner-only queue. Preserve the rollback path below for
	// a malformed G, but consume the selected queue capability instead of
	// replaying the full head/tail/count header relation here.
	var g *G
	imported := selected.transferState == runnableTransferGImported
	ordinaryOwner := p.osThreadLockOwner == nil && p.osThreadSuspend == osThreadSuspendAttached
	if ordinaryOwner {
		// In the overwhelmingly common attached/unlocked phase, selection is the
		// FIFO head. The caller has just selected it, so consume the non-empty
		// queue capability without making dequeue repeat the same header checks.
		if selected != p.readyHead || p.readyCount == 0 {
			return ExecutorRunActionStep{}, false
		}
		g = dequeueReadyHeadUnchecked(p)
		if g != nil && g.transferState == runnableTransferGImported {
			g.transferState = runnableTransferGIdle
		}
	} else {
		g, imported = dequeueSelectedOSThreadRunnableWithTransfer(p, selected)
	}
	if g == nil {
		return ExecutorRunActionStep{}, false
	}
	peerRunnable := p.readyHead != nil
	if !ordinaryOwner {
		peerRunnable = nextOSThreadRunnable(p) != nil
	}
	var action Action
	var ok bool
	if g.runAction == ActionInvalid && g.park.phase == parkMaterialized && g.park.directChannel {
		action, ok = beginExecutorRunDirectChannelContinuation(p, g, peerRunnable)
	} else if g.runAction == ActionCheckResume ||
		g.runAction == ActionInvalid && g.park.phase == parkMaterialized {
		action, ok = beginExecutorRunContinuation(p, g, peerRunnable)
	} else {
		action, ok = BeginRunG(p, g)
	}
	if !ok {
		// dequeue only clears the selected head's scheduler-owned queue fields.
		// Restore those exact fields on a fail-closed BeginRunG rejection so a
		// malformed head cannot turn a rejected bounded reduction into a hidden
		// queue mutation.
		if imported {
			g.transferState = runnableTransferGImported
		}
		prependReadyUnchecked(p, g)
		return ExecutorRunActionStep{}, false
	}
	if combineAction {
		driver.run.issued = action.Kind
		return ExecutorRunActionStep{Dispatched: true, G: g, Action: action}, true
	}
	return ExecutorRunActionStep{G: g, Action: action}, true
}

func dispatchExecutorRunReady(
	driver *ExecutorDriver,
	selected *G,
	combineAction bool,
) (ExecutorRunStep, bool) {
	action, ok := dispatchExecutorRunReadyAction(driver, selected, combineAction)
	if !ok {
		return ExecutorRunStep{}, false
	}
	kind := ExecutorRunStepDispatch
	if combineAction {
		kind = ExecutorRunStepAction
	}
	return ExecutorRunStep{
		Kind: kind, Dispatched: action.Dispatched, G: action.G, Action: action.Action,
	}, true
}

// nextExecutorRunActionValidated applies the same priority order as
// nextExecutorRunStepAtValidated but stops before every cold event reduction.
// selected=false is a side-effect-free request to use the complete selector.
// This keeps one scheduling authority while allowing the production runner to
// avoid constructing the large cold-event union for the overwhelmingly common
// Action path.
func nextExecutorRunActionValidated(
	driver *ExecutorDriver,
	combineDispatch bool,
) (step ExecutorRunActionStep, selected, ok bool) {
	p := driver.p
	// A completed same-owner materialization publishes readyDebt only after its
	// full wait/frame transaction and an idle-P commit. While this bounded-slice
	// capability is retained, no producer can mutate P-local scheduler fields;
	// it can only publish another source fact. Fairness requires paying the
	// existing debt first, so the ordinary unlocked head may cross directly into
	// the adjacent dequeue/action reducer without replaying the complete idle-P
	// tuple or probing lower-priority producer queues.
	if combineDispatch && driver.run.readyDebt && driver.poll.phase == executorPollIdle &&
		p.current == nil && p.osThreadLockOwner == nil &&
		p.osThreadSuspend == osThreadSuspendAttached && p.readyHead != nil {
		step, ok = dispatchExecutorRunReadyAction(driver, p.readyHead, true)
		return step, ok, ok
	}
	if p.current != nil {
		action, g := p.action, p.current
		if action.Kind == ActionCommitDestroy {
			return ExecutorRunActionStep{}, false, true
		}
		if action.Handle == nil ||
			(action.Kind != ActionCheckResume && action.Kind != ActionCheckDestroy && action.Kind != ActionPanicDestroy) {
			return ExecutorRunActionStep{}, false, false
		}
		driver.run.readyDebt = false
		driver.run.issued = action.Kind
		return ExecutorRunActionStep{G: g, Action: action}, true, true
	}
	if p.inResume || p.inlineAwaitDepth != 0 || p.action != (Action{}) || p.runDecision != (RunDecision{}) ||
		p.runDecisionTaken || p.servicePreemptBudget != 0 {
		return ExecutorRunActionStep{}, false, false
	}
	if !combineDispatch || driver.poll.phase != executorPollIdle {
		return ExecutorRunActionStep{}, false, true
	}
	runnable := nextOSThreadRunnable(p)
	// A completed source or same-owner materialization has already selected one
	// runnable as its bounded fairness payment. Dispatch that stable owner-local
	// fact before probing producer queues again. Concurrent completion facts stay
	// published and are observed immediately after this one physical action.
	if driver.run.readyDebt && runnable != nil {
		step, ok := dispatchExecutorRunReadyAction(driver, runnable, true)
		return step, ok, ok
	}
	if executorDirectChannelCompletionPending(driver) || ownerLocalCompletionPending(driver) {
		return ExecutorRunActionStep{}, false, true
	}
	if executorRunSourceRequested(driver) ||
		driver.run.actionsSinceSource == executorRunSourceQuantum ||
		runnable == nil && HasWaiting(p) && !driver.run.blocked {
		return ExecutorRunActionStep{}, false, true
	}
	driver.run.readyDebt = false
	if runnable == nil {
		return ExecutorRunActionStep{}, false, true
	}
	step, ok = dispatchExecutorRunReadyAction(driver, runnable, true)
	return step, ok, ok
}

func nextExecutorRunStepAtValidated(
	driver *ExecutorDriver,
	now int64,
	withDeadline bool,
	combineDispatch bool,
) (ExecutorRunStep, bool) {
	p := driver.p
	if p.current != nil {
		action, g := p.action, p.current
		if action.Kind == ActionCommitDestroy {
			if !validDestroyCommitReceipt(p, g, action) {
				return ExecutorRunStep{}, false
			}
			return ExecutorRunStep{Kind: ExecutorRunStepDestroyCommit, G: g, Action: action}, true
		}
		if action.Handle == nil ||
			(action.Kind != ActionCheckResume && action.Kind != ActionCheckDestroy && action.Kind != ActionPanicDestroy) {
			return ExecutorRunStep{}, false
		}
		// Dispatch has already selected this action as payment for any prior
		// ready debt. Clear it before opening the no-return physical interval;
		// a completion produced by the resumed coroutine may raise a new debt
		// which CommitExecutorRunAction must preserve.
		driver.run.readyDebt = false
		driver.run.issued = action.Kind
		return ExecutorRunStep{Kind: ExecutorRunStepAction, G: g, Action: action}, true
	}
	if p.inResume || p.inlineAwaitDepth != 0 || p.action != (Action{}) || p.runDecision != (RunDecision{}) ||
		p.runDecisionTaken || p.servicePreemptBudget != 0 {
		return ExecutorRunStep{}, false
	}

	// Once epoch A starts, acknowledgement and epoch B finish before any G.
	if driver.poll.phase != executorPollIdle {
		if cleanup, pending := pendingResumeCleanupStepForCursor(&driver.poll.resolve); pending {
			return ExecutorRunStep{Kind: ExecutorRunStepMaterialize, Cleanup: cleanup}, true
		}
		if withDeadline && now < 0 {
			return ExecutorRunStep{}, false
		}
		return serviceExecutorRunSource(driver, now, withDeadline)
	}
	// A compact hchan completion is already a terminal typed fact and has no
	// source epoch to acknowledge. Consume it before unrelated catalog work; the
	// runtime adapter removes the typed queue node and commits the returned
	// frame-local packet in this single explicit reduction.
	if executorDirectChannelCompletionPending(driver) {
		completion, taken := takeExecutorDirectChannelCompletion(driver)
		if !taken {
			return ExecutorRunStep{}, false
		}
		if completion != nil {
			return ExecutorRunStep{Kind: ExecutorRunStepDirectChannel, Direct: completion}, true
		}
	}
	// An owner-local completion has already published its exact typed source
	// fact. Resolve it before starting an unrelated external A/ack/B epoch and
	// before dispatching another G; typed cleanup still returns through the
	// ordinary direct-runtime Materialize boundary.
	if ownerLocalCompletionPending(driver) {
		return nextExecutorRunLocalStep(driver)
	}
	runnable := nextOSThreadRunnable(p)
	if driver.run.readyDebt {
		if runnable != nil {
			return dispatchExecutorRunReady(driver, runnable, combineDispatch)
		}
	}
	if executorRunSourceRequested(driver) ||
		driver.run.actionsSinceSource == executorRunSourceQuantum ||
		runnable == nil && HasWaiting(p) && !driver.run.blocked {
		// A negative timestamp is the explicit before-time probe used by the
		// native adapter. Selection has not mutated the cursor yet, so its
		// caller can sample a fresh clock and retry this exact decision.
		if withDeadline && now < 0 {
			return ExecutorRunStep{}, false
		}
		driver.run.readyDebt = false
		return serviceExecutorRunSource(driver, now, withDeadline)
	}
	driver.run.readyDebt = false
	if runnable != nil {
		return dispatchExecutorRunReady(driver, runnable, combineDispatch)
	}
	return ExecutorRunStep{Kind: ExecutorRunStepIdle}, true
}

func nextExecutorRunStepAt(driver *ExecutorDriver, now int64, withDeadline bool) (ExecutorRunStep, bool) {
	if !validExecutorDriverHeader(driver) || driver.state != executorDriverActive ||
		driver.sources.usesMonotonicTime() != withDeadline ||
		driver.run.issued != ActionInvalid {
		return ExecutorRunStep{}, false
	}
	return nextExecutorRunStepAtValidated(driver, now, withDeadline, false)
}

// ExecutorRunSliceCapability is a scheduler-owner-only proof that one bounded
// RunSlice entry audited the complete immutable driver/source binding. It is
// deliberately stack-scoped by the runtime adapter: a host return discards it,
// and the next entry must call BeginExecutorRunSlice again. Producers retain
// only source/registry POD identities and cannot manufacture or mutate this
// private P/driver pair.
type ExecutorRunSliceCapability struct {
	driver       *ExecutorDriver
	withDeadline bool
}

// BeginExecutorRunSlice performs the full cold-boundary audit once before a
// bounded owner loop. Exact source reducers, physical actions, and commits
// continue to validate every mutable transition; only the immutable catalog,
// route, and run-cursor header are not re-read before each adjacent step.
func BeginExecutorRunSlice(driver *ExecutorDriver) (ExecutorRunSliceCapability, bool) {
	if !validExecutorDriverHeader(driver) || driver.state != executorDriverActive ||
		driver.run.issued != ActionInvalid {
		return ExecutorRunSliceCapability{}, false
	}
	return ExecutorRunSliceCapability{
		driver:       driver,
		withDeadline: driver.sources.usesMonotonicTime(),
	}, true
}

func (capability *ExecutorRunSliceCapability) owner(
	withDeadline bool,
) (*ExecutorDriver, bool) {
	if capability == nil || capability.driver == nil ||
		capability.withDeadline != withDeadline {
		return nil, false
	}
	// BeginExecutorRunSlice authenticated the immutable driver/P/source binding
	// before this stack-scoped capability was returned. A bounded runner never
	// crosses a host boundary while retaining it, and physical/source reducers
	// cannot close or rebind the executor. Only the issued no-return interval is
	// mutable between adjacent selections, so rechecking the complete binding on
	// every hot action would duplicate the entry proof.
	driver := capability.driver
	return driver, driver.run.issued == ActionInvalid
}

func (capability *ExecutorRunSliceCapability) nextAction(
	combineDispatch bool,
) (ExecutorRunActionStep, bool, bool) {
	// This stack-scoped capability already fixed its driver and source-time
	// shape at BeginExecutorRunSlice. The compact action probe does not consume
	// time, so replaying owner(withDeadline) here was a tautological comparison
	// plus an otherwise unused cached P load on every physical resume. Only the
	// adjacent issued interval can invalidate another selection.
	if capability == nil || capability.driver == nil ||
		capability.driver.run.issued != ActionInvalid {
		return ExecutorRunActionStep{}, false, false
	}
	return nextExecutorRunActionValidated(capability.driver, combineDispatch)
}

// NextAction selects an already dispatched physical action without carrying
// the cold source/materialization payload union. selected=false means the
// complete Next/NextBeforeTime/NextAt selector still owns the next reduction.
func (capability *ExecutorRunSliceCapability) NextAction() (
	step ExecutorRunActionStep,
	selected bool,
	ok bool,
) {
	return capability.nextAction(false)
}

// NextActionCombined additionally combines a ready dequeue with its physical
// action. The caller must hold the same managed-execution lease and two-unit
// budget required by NextCombined/NextAtCombined.
func (capability *ExecutorRunSliceCapability) NextActionCombined() (
	step ExecutorRunActionStep,
	selected bool,
	ok bool,
) {
	return capability.nextAction(true)
}

// Next selects one step for a no-deadline bounded slice.
func (capability *ExecutorRunSliceCapability) Next() (ExecutorRunStep, bool) {
	driver, ok := capability.owner(false)
	if !ok {
		return ExecutorRunStep{}, false
	}
	return nextExecutorRunStepAtValidated(driver, 0, false, false)
}

// NextCombined is Next with the additional proof that the caller already owns
// a managed-execution lease and has at least two budget units. An immediately
// runnable G therefore crosses dequeue/BeginRunG and the issued Action boundary
// in one selector entry.
func (capability *ExecutorRunSliceCapability) NextCombined() (ExecutorRunStep, bool) {
	driver, ok := capability.owner(false)
	if !ok {
		return ExecutorRunStep{}, false
	}
	return nextExecutorRunStepAtValidated(driver, 0, false, true)
}

// NextBeforeTime performs the timer-aware before-clock probe without
// re-auditing immutable driver/source structure.
func (capability *ExecutorRunSliceCapability) NextBeforeTime() (ExecutorRunStep, bool) {
	driver, ok := capability.owner(true)
	if !ok {
		return ExecutorRunStep{}, false
	}
	return nextExecutorRunStepAtValidated(driver, -1, true, false)
}

// NextBeforeTimeCombined is NextBeforeTime with adjacent dispatch/action
// selection enabled under the caller's retained lease and budget proof.
func (capability *ExecutorRunSliceCapability) NextBeforeTimeCombined() (ExecutorRunStep, bool) {
	driver, ok := capability.owner(true)
	if !ok {
		return ExecutorRunStep{}, false
	}
	return nextExecutorRunStepAtValidated(driver, -1, true, true)
}

// NextAt selects one timer-aware step using the host's current monotonic
// sample. The logical A/B epoch still freezes time in its own transaction.
func (capability *ExecutorRunSliceCapability) NextAt(now int64) (ExecutorRunStep, bool) {
	driver, ok := capability.owner(true)
	if !ok || now < 0 {
		return ExecutorRunStep{}, false
	}
	return nextExecutorRunStepAtValidated(driver, now, true, false)
}

// NextAtCombined is NextAt with adjacent dispatch/action selection enabled
// under the caller's retained lease and budget proof.
func (capability *ExecutorRunSliceCapability) NextAtCombined(now int64) (ExecutorRunStep, bool) {
	driver, ok := capability.owner(true)
	if !ok || now < 0 {
		return ExecutorRunStep{}, false
	}
	return nextExecutorRunStepAtValidated(driver, now, true, true)
}

// NextExecutorRunStep selects one no-deadline runner step. It never calls
// PollExecutor, PollReady, or NextRunnable; source work goes through the
// bounded PollExecutorSlice cursor.
func NextExecutorRunStep(driver *ExecutorDriver) (ExecutorRunStep, bool) {
	if driver == nil || driver.sources.usesMonotonicTime() {
		return ExecutorRunStep{}, false
	}
	return nextExecutorRunStepAt(driver, 0, false)
}

// NextExecutorRunStepBeforeTime selects one timer-aware reduction only when
// that reduction does not consume a monotonic sample. A false result is
// deliberately ambiguous: the driver is invalid or the next reduction needs
// fresh time. In either case a native adapter may sample its clock and retry
// through NextExecutorRunStepAt; a time-required rejection does not mutate the
// runner cursor. This keeps ordinary action, channel-cleanup, and ready-queue
// traffic off the platform clock without weakening source/deadline ordering.
func NextExecutorRunStepBeforeTime(driver *ExecutorDriver) (ExecutorRunStep, bool) {
	if driver == nil || !driver.sources.usesMonotonicTime() {
		return ExecutorRunStep{}, false
	}
	return nextExecutorRunStepAt(driver, -1, true)
}

// NextExecutorRunStepAt is the deadline-capable counterpart. A fresh sample is
// accepted at each source reduction; PollExecutorSliceAt freezes the correct
// sample across each logical A or B epoch.
func NextExecutorRunStepAt(driver *ExecutorDriver, now int64) (ExecutorRunStep, bool) {
	if driver == nil || !driver.sources.usesMonotonicTime() || now < 0 {
		return ExecutorRunStep{}, false
	}
	return nextExecutorRunStepAt(driver, now, true)
}

// ExecutorRunManagedResumePending reports whether the next bounded runner
// reduction will enter a managed llvm.coro.resume. It is deliberately
// observational and must be called before NextExecutorRunStep marks the
// physical Action interval issued. A target can therefore acquire its
// process-level P lease without returning across an issued action or
// teaching the target-neutral driver about threads and GOMAXPROCS.
func ExecutorRunManagedResumePending(driver *ExecutorDriver) (pending, ok bool) {
	// This is only a pre-step lease probe. NextExecutorRunStep performs the
	// complete owner-header validation before it can issue an action, so the
	// probe needs only the exact active P back-pointer and issued-state gate.
	if driver == nil || driver.magic != executorDriverMagic || driver.state != executorDriverActive ||
		driver.p == nil || driver.p.executor != driver ||
		preemptLoad(&driver.p.executorMode) != executorModeBound || driver.run.issued != ActionInvalid {
		return false, false
	}
	p := driver.p
	if p.current == nil {
		return false, true
	}
	action := p.action
	switch action.Kind {
	case ActionCheckResume:
		return action.Handle != nil, action.Handle != nil
	case ActionCheckDestroy, ActionPanicDestroy:
		return false, action.Handle != nil
	case ActionCommitDestroy:
		return false, action.Handle == nil
	default:
		return false, false
	}
}

// RequestExecutorSourceService is the scheduler-owner wake half for a target
// wait which deliberately kept the driver active instead of entering the
// retained IdleArmed protocol. Native replacement owners use it when their
// physical deadline expires: the next bounded runner reduction performs the
// same resumable source A/ack/B transaction as every other wake path.
//
// This is not a producer API. It accepts only a stable active-owner boundary
// and retains no platform state; asynchronous producers must still publish a
// durable source and use the registry request/doorbell protocol.
func RequestExecutorSourceService(driver *ExecutorDriver) bool {
	if !validExecutorDriverHeader(driver) || driver.state != executorDriverActive ||
		driver.run.issued != ActionInvalid || driver.poll.phase != executorPollIdle ||
		!idleExecutorScheduler(driver.p) {
		return false
	}
	driver.run.sourceMore = true
	driver.run.blocked = false
	return validExecutorDriverHeader(driver)
}

// ExecutorOwnerWaitPending closes the running-owner-to-physical-wait window
// without entering the retained IdleArmed state. The caller must first publish
// its target-specific wake marker, then call this method; a preexisting ready
// G, source fact, registry request, or scheduler request makes blocking
// unnecessary, while a later producer observes that marker and rings.
func ExecutorOwnerWaitPending(driver *ExecutorDriver) (pending, ok bool) {
	if !validExecutorDriverHeader(driver) || driver.state != executorDriverActive ||
		driver.run.issued != ActionInvalid || driver.poll.phase != executorPollIdle ||
		!idleExecutorScheduler(driver.p) {
		return false, false
	}
	return runnableForOSThreadOwner(driver.p) ||
		ownerLocalCompletionPending(driver) || executorRunSourceRequested(driver), true
}

func completedExecutorRunAction(p *P, g *G, action Action) bool {
	if p == nil || g == nil || !validActionFlags(action) || action.Handle != nil || p.current != nil || p.inResume ||
		p.inlineAwaitDepth != 0 ||
		p.action != (Action{}) || p.runDecision != (RunDecision{}) || p.runDecisionTaken ||
		p.servicePreemptBudget != 0 || g.runP != nil || g.runAction != ActionInvalid ||
		g.transferState != runnableTransferGIdle {
		return false
	}
	switch action.Kind {
	case ActionYield:
		return action.Flags == 0 && gPreemptEnabledAtDepthZero(g) && g.state == GRunnable && g.queued
	case ActionPark:
		return action.Flags == 0 && gPreemptEnabledAtDepthZero(g) && g.state == GWaiting &&
			(g.waiting || g.active != nil && g.active.parkWait != nil)
	case ActionComplete:
		return gPreemptStateAtDepthZero(g, preemptDisabled) && g.state == GDead && !g.panicUnwind
	case ActionPanicComplete:
		return gPreemptStateAtDepthZero(g, preemptDisabled) && g.state == GDead &&
			publishedPanicRecord(&g.panicRecord)
	default:
		return false
	}
}

// validIssuedExecutorRunAction is the commit-side proof for the no-return
// physical interval opened by nextExecutorRunStepAt. The selector already
// audited the immutable registry, source-set, route, and run-cursor binding
// before setting run.issued. No target or coroutine callback can retain the
// private driver or cross a host boundary during that interval, so repeating
// those immutable audits after llvm.coro.resume/destroy only adds hot-path
// work. The mutable P/G/action episode is still checked in full by the exact
// commit reducer below, and the next selector repeats the complete header gate.
func validIssuedExecutorRunAction(driver *ExecutorDriver) bool {
	// run.issued is written only after the bounded selector's complete driver,
	// source-set, registry, and action audit. Nothing outside this package can
	// manufacture or retain that private field, and no host boundary exists
	// before the matching commit clears it. The exact P back-pointer and bound
	// mode remain the independent owner gate; consume the capability for the
	// remaining immutable driver fields instead of replaying them here.
	if driver == nil || driver.p == nil || driver.p.executor != driver ||
		preemptLoad(&driver.p.executorMode) != executorModeBound {
		return false
	}
	switch driver.run.issued {
	case ActionCheckResume, ActionCheckDestroy, ActionPanicDestroy:
		return true
	default:
		return false
	}
}

// ResumedExecutorRun consumes the exact ActionResume return for an issued
// executor step. Yield/Park are terminal control receipts constructed entirely
// by Resumed; no runtime or target policy chooses a queue placement for them.
// Commit those adjacent receipts here so the post-resume path does not replay
// completedExecutorRunAction's arbitrary-caller audit. Handle continuations,
// completion, panic, and destroy receipts retain the ordinary explicit commit
// APIs because their placement or terminal policy is selected by the runtime.
func ResumedExecutorRun(
	driver *ExecutorDriver,
	p *P,
	g *G,
	action Action,
) (next Action, committed, ok bool) {
	// BeginIssuedExecutorResumeRuntimeContext has just consumed this private
	// issued episode and opened p.action=ActionResume. No host boundary can
	// unbind the driver before the adjacent physical resume returns; only the
	// resumed G may publish scheduler state. Keep the exact driver/P identity
	// and issued-kind checks, but do not replay the P.executor/executorMode
	// binding already certified by the selector and Begin. Stable-boundary
	// callers still use CommitExecutorRunAction and its complete owner gate.
	if driver == nil || p == nil || driver.p != p ||
		driver.run.issued != ActionCheckResume {
		return Action{}, false, false
	}
	if g != nil && g.pending.kind == pendingParkSet && g.pending.directChannel && g.active != nil &&
		g.pending.from == g.active && g.active.handle == action.Handle &&
		g.active.parkWait != nil && g.active.parkWait.directChannel {
		// PrepareCurrentDirectChannelPark installed the pending direct marker
		// only after building the complete frame/wait/park graph. The issued
		// action and current/inResume relation freeze every owner field until
		// this adjacent return from llvm.coro.resume; a peer may change only the
		// completion's atomic word. Consume that certificate in one flat commit
		// instead of calling the generic Resumed graph validator. An inline child
		// may be the deepest active frame while action still names its physical
		// outer resume; that ancestry is intentionally left to Resumed.
		frame := g.pending.from
		if p.current != g || g.runP != p || !p.inResume || p.inlineAwaitDepth != 0 ||
			p.action != action || !p.runDecisionTaken || frame == nil || frame != g.active ||
			frame.handle != action.Handle || frame.parkWait != g.active.parkWait ||
			frame.parkWait.resumeKind != resumeBindingDirectChannel ||
			!acknowledgeSuspendedGPreempt(g) {
			return Action{}, false, false
		}
		wait := frame.parkWait
		g.pending = pendingTransition{}
		frame.state = FrameSuspended
		p.inResume = false
		p.runDecisionTaken = false
		g.state = GWaiting
		g.runP = nil
		p.current = nil
		p.servicePreemptBudget = 0
		p.action = Action{}
		activateWaitSetRecordUnchecked(p, g, wait)
		next, ok = Action{Kind: ActionPark}, true
	} else {
		next, ok = Resumed(p, g, action)
	}
	if !ok || (next.Kind != ActionYield && next.Kind != ActionPark) {
		return next, false, ok
	}
	if !validOSThreadPeerActionCommit(p, g) {
		return Action{}, false, false
	}
	// Resumed has just produced the complete stable Yield/Park state and cleared
	// P.current/action/service state. Preserve a same-resume ready publication,
	// close the issued interval, and apply the exceptional detached-owner debt.
	publishedReady := driver.run.readyDebt
	driver.run.issued = ActionInvalid
	if !publishedReady {
		driver.run.readyDebt = false
	} else if p.osThreadLockOwner == nil && p.osThreadSuspend == osThreadSuspendAttached {
		// The direct-channel handoff normally publishes exactly one peer while
		// the physical owner is unattached. Consume the stable queue head here;
		// entering nextOSThreadRunnable would only rediscover these two scalar
		// affinity facts on every rendezvous.
		driver.run.readyDebt = p.readyHead != nil
	} else {
		driver.run.readyDebt = runnableForOSThreadOwner(p)
	}
	driver.run.blocked = false
	if driver.run.actionsSinceSource < executorRunSourceQuantum {
		driver.run.actionsSinceSource++
	}
	commitOSThreadPeerAction(p)
	return next, true, true
}

// CommitExecutorRunAction closes the no-return physical interval opened by an
// Action step. A live continuation is moved to the ready tail; terminal and
// yield/park control actions are already stable. The function retains neither
// the completed G nor its old handle, so a runtime may reclaim a dynamic G
// immediately after a successful ActionComplete commit.
func commitExecutorRunAction(driver *ExecutorDriver, g *G, next Action, placement executorRunQueuePlacement) bool {
	if !validIssuedExecutorRunAction(driver) || g == nil ||
		!validOSThreadPeerActionCommit(driver.p, g) {
		return false
	}
	p := driver.p
	// Dispatch consumes the ready debt which selected this physical action, but
	// a same-owner completion may publish a new runnable while llvm.coro.resume
	// is executing. Preserve only that newly raised debt across the action
	// receipt so its peer runs before an unrelated source epoch.
	publishedReady := driver.run.readyDebt
	committed := false
	switch next.Kind {
	case ActionCheckResume, ActionCheckDestroy, ActionPanicDestroy:
		committed = pauseExecutorRunAction(p, g, next, placement)
	case ActionYield, ActionPark, ActionComplete, ActionPanicComplete:
		if placement != executorRunQueueTail {
			return false
		}
		committed = completedExecutorRunAction(p, g, next)
	case ActionCommitDestroy:
		if placement != executorRunQueueTail {
			return false
		}
		committed = validDestroyCommitReceipt(p, g, next)
	case ActionForeignReentryComplete:
		if placement != executorRunQueueTail {
			return false
		}
		committed = commitForeignReentryCompletion(driver, g, next)
	}
	if !committed {
		return false
	}
	driver.run.issued = ActionInvalid
	if !publishedReady {
		driver.run.readyDebt = false
	} else if p.osThreadLockOwner == nil && p.osThreadSuspend == osThreadSuspendAttached {
		driver.run.readyDebt = p.readyHead != nil
	} else {
		driver.run.readyDebt = runnableForOSThreadOwner(p)
	}
	driver.run.blocked = false
	if driver.run.actionsSinceSource < executorRunSourceQuantum {
		driver.run.actionsSinceSource++
	}
	commitOSThreadPeerAction(p)
	return true
}

// CommitExecutorRunAction closes an ordinary physical action and retains FIFO
// ordering for every live continuation.
func CommitExecutorRunAction(driver *ExecutorDriver, g *G, next Action) bool {
	return commitExecutorRunAction(driver, g, next, executorRunQueueTail)
}

// CommitExecutorRunDomainDestroy settles the handle-free final-root receipt
// of one ordinary long-lived executor domain. An empty command executor uses
// CommitDestroyedReceiptCompatibility to begin process-terminal close; an
// ordinary fleet P instead completes only this G and keeps its exact executor
// gate active for future routed work.
func CommitExecutorRunDomainDestroy(driver *ExecutorDriver, g *G, receipt Action) (Action, bool) {
	if !validExecutorDriver(driver) || driver.state != executorDriverActive ||
		driver.run.issued != ActionInvalid || !validDestroyCommitReceipt(driver.p, g, receipt) ||
		driver.p.executor != driver || preemptLoad(&driver.p.executorMode) != executorModeBound ||
		driver.p.readyHead != nil || driver.p.readyTail != nil || !emptySchedulerWaitQueues(driver.p) {
		return Action{}, false
	}
	p := driver.p
	schedule := preemptLoad(&p.schedule)
	if schedule != scheduleIdle && schedule != scheduleRequested {
		return Action{}, false
	}
	panicking := g.state == GPanicking
	if panicking {
		if !g.panicUnwind || !publishedPanicRecord(&g.panicRecord) {
			return Action{}, false
		}
	} else if g.state != GDispatching || g.panicUnwind || !emptyPanicRecord(&g.panicRecord) {
		return Action{}, false
	}
	flags := receipt.Flags
	retireOwner, released := releaseOSThreadLockForExit(p, g)
	if !released || !disableGPreempt(g) {
		return Action{}, false
	}
	flags |= physicalOwnerRetireFlags(retireOwner)
	g.destroyRoot = false
	if panicking {
		g.panicUnwind = false
	}
	g.state = GDead
	g.runP = nil
	p.current = nil
	p.servicePreemptBudget = 0
	p.action = Action{}
	if panicking {
		return Action{Kind: ActionPanicComplete, Flags: flags}, true
	}
	return Action{Kind: ActionComplete, Flags: flags}, true
}

// CommitExecutorRunCommandBootstrapDirectChildHandoff retains the frozen
// command-bootstrap G at the ready head while one direct CoroRoot step is
// destroyed and the exact bootstrap-root continuation is resumed. This covers
// each fixed runtime/package-init/main step, with a strict upper bound of one
// child destroy plus one root resume per step. Nested non-root cleanup remains
// ordinary FIFO work; normal-main return's final root destroy is separate.
func CommitExecutorRunCommandBootstrapDirectChildHandoff(driver *ExecutorDriver, g *G, next Action) bool {
	// This helper is probed for every live command-main action. Reject the
	// overwhelmingly common non-bootstrap shape before auditing the complete
	// source and owner-local cursors; an exact candidate still receives the
	// same full driver validation before any scheduler state is mutated.
	if driver == nil || g == nil || g.root == nil || g.active != g.root ||
		g.panicUnwind || !emptyPanicRecord(&g.panicRecord) {
		return false
	}
	switch next.Kind {
	case ActionCheckDestroy:
		target := g.destroyTarget
		if driver.run.issued != ActionCheckResume || g.state != GDispatching || target == nil ||
			target == g.root || target.parent != g.root || target.handle != next.Handle ||
			target.state != FrameDestroyPending || g.destroyRoot {
			return false
		}
	case ActionCheckResume:
		if driver.run.issued != ActionCheckDestroy || g.state != GRunning ||
			g.destroyTarget != nil || g.destroyRoot || g.root.handle != next.Handle ||
			(g.root.state != FrameInitialSuspended && g.root.state != FrameSuspended) {
			return false
		}
	default:
		return false
	}
	if !validExecutorDriver(driver) || driver.state != executorDriverActive {
		return false
	}
	return commitExecutorRunAction(driver, g, next, executorRunQueueCommandBootstrapDirectChildHandoff)
}

// CommitExecutorRunCommandRootDestroy is valid only for the one final root
// destroy after command main published its normal-return marker; running
// another user G first would violate Go process exit semantics. The destroy
// remains a separately charged later reduction.
func CommitExecutorRunCommandRootDestroy(driver *ExecutorDriver, g *G, next Action) bool {
	if g == nil || next.Kind != ActionCheckDestroy || g.destroyTarget == nil ||
		g.destroyTarget != g.root || !g.destroyRoot || g.active != nil || g.panicUnwind ||
		!emptyPanicRecord(&g.panicRecord) {
		return false
	}
	return commitExecutorRunAction(driver, g, next, executorRunQueueCommandRootDestroy)
}
