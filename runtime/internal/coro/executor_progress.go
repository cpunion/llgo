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

// ExecutorPollProgress is the pointer-free host boundary for one bounded
// source-service entry. Counts are cumulative for the current A/ack/B
// transaction; Used is charged only for this call. Every production catalog
// slot, affected wait-set decision, candidate scan/settle/apply, promotion, and
// legacy-G visit is resumable and charged as one reduction. ApplyVisits counts
// only source-specific candidate ApplyOne actions. Complete means that
// the transaction reached the end of epoch B. More requests a later,
// non-recursive scheduler entry, while Blocked means that only a new external
// fact (or a reported future deadline) can make progress. More and Blocked are
// mutually exclusive.
type ExecutorPollProgress struct {
	Used          uint32
	Completed     uint32
	Timers        uint32
	Manual        uint32
	ManualLost    uint32
	Worker        uint32
	WorkerLost    uint32
	Control       uint32
	ControlLate   uint32
	ApplyVisits   uint32
	Promoted      uint32
	NextDeadline  int64
	Epochs        uint8
	Complete      bool
	More          bool
	Blocked       bool
	HasDeadline   bool
	AtomicResolve bool
	Overshot      bool
}

type executorPollPhase uint8

const (
	executorPollIdle executorPollPhase = iota
	executorPollEpochAPublish
	executorPollEpochAResolve
	executorPollAcknowledge
	executorPollEpochBPublish
	executorPollEpochBResolve
)

type executorCatalogSource uint8

const (
	executorCatalogTimers executorCatalogSource = iota
	executorCatalogPoll
	executorCatalogManual
	executorCatalogWorker
	executorCatalogChannel
	executorCatalogControl
	executorCatalogDone
)

// executorPollTransaction is scheduler-owner-only continuation state. It has
// no callback-visible pointer and is embedded at a stable address in the
// driver. now is captured once per logical epoch and is intentionally
// unchanged across that epoch's host entries: using a later sample for a later
// slot would make equal-deadline select candidates depend on slot order. If an
// entry ends exactly after acknowledgement, epoch B may capture a fresh sample
// from the next entry before it visits its first source slot.
type executorPollTransaction struct {
	total    executorSourceScan
	now      int64
	deadline int64
	// cursor covers one configured source's linear 15-bit OperationID slot
	// namespace. Paged sources are capped at 32704 slots, so uint16 remains
	// sufficient without changing the public ExecutorPollProgress POD.
	cursor        uint16
	phase         executorPollPhase
	source        executorCatalogSource
	withDeadline  bool
	hasDeadline   bool
	retryBudget   bool
	awaitExternal bool
	resampleNow   bool
	_             [2]byte
	resolve       publishedEpochResolveCursor
	resolveStep   publishedEpochResolveStep
}

func executorCatalogScanLimit(sources *ExecutorSourceSet, source executorCatalogSource) (uint32, bool) {
	if sources == nil {
		return 0, false
	}
	switch source {
	case executorCatalogTimers:
		if sources.timers == nil {
			return 0, true
		}
		return timerRegistrationScanLimit(sources.timers)
	case executorCatalogPoll:
		if sources.poll == nil {
			return 0, true
		}
		return PollOperationScanLimit(sources.poll)
	case executorCatalogManual:
		if sources.manual == nil {
			return 0, true
		}
		return ManualOperationScanLimit(sources.manual)
	case executorCatalogWorker:
		if sources.worker == nil {
			return 0, true
		}
		return workerOperationScanLimit(sources.worker)
	case executorCatalogChannel:
		if sources.channel == nil {
			return 0, true
		}
		return channelOperationScanLimit(sources.channel)
	case executorCatalogControl:
		if sources.control == nil {
			return 0, true
		}
		return taskControlScanLimit(sources.control)
	case executorCatalogDone:
		return 0, true
	default:
		return 0, false
	}
}

func firstExecutorCatalogSource(sources *ExecutorSourceSet) (executorCatalogSource, bool) {
	for source := executorCatalogTimers; source < executorCatalogDone; source++ {
		limit, ok := executorCatalogScanLimit(sources, source)
		if !ok {
			return executorCatalogDone, false
		}
		if limit != 0 {
			return source, true
		}
	}
	return executorCatalogDone, true
}

func validExecutorPollTransaction(transaction *executorPollTransaction, sources *ExecutorSourceSet) bool {
	if transaction == nil {
		return false
	}
	if transaction.phase == executorPollIdle {
		return *transaction == (executorPollTransaction{})
	}
	if sources == nil || transaction.phase < executorPollEpochAPublish || transaction.phase > executorPollEpochBResolve ||
		transaction.withDeadline != sources.usesMonotonicTime() || transaction.withDeadline && transaction.now < 0 ||
		!transaction.withDeadline && transaction.now != 0 || transaction.source > executorCatalogDone {
		return false
	}
	if transaction.resampleNow {
		first, firstOK := firstExecutorCatalogSource(sources)
		if !firstOK || !transaction.withDeadline || transaction.phase != executorPollEpochBPublish ||
			first == executorCatalogDone || transaction.source != first || transaction.cursor != 0 {
			return false
		}
	}
	if transaction.total.epochs > 1 || transaction.phase <= executorPollEpochAResolve && transaction.total.epochs != 0 ||
		transaction.phase == executorPollAcknowledge && transaction.total.epochs != 1 ||
		transaction.phase >= executorPollEpochBPublish && transaction.total.epochs != 1 {
		return false
	}
	if transaction.phase == executorPollEpochAPublish || transaction.phase == executorPollEpochBPublish {
		if transaction.resolve != (publishedEpochResolveCursor{}) {
			return false
		}
		limit, limitOK := executorCatalogScanLimit(sources, transaction.source)
		if !limitOK {
			return false
		}
		if transaction.source == executorCatalogDone {
			return transaction.cursor == 0
		}
		return limit != 0 && uint32(transaction.cursor) < limit
	}
	if transaction.source != executorCatalogDone || transaction.cursor != 0 {
		return false
	}
	if transaction.phase == executorPollEpochAResolve || transaction.phase == executorPollEpochBResolve {
		return transaction.resolve == (publishedEpochResolveCursor{}) ||
			validPublishedEpochResolveCursor(&transaction.resolve, sources.owner)
	}
	return transaction.resolve == (publishedEpochResolveCursor{})
}

func beginExecutorPollTransaction(driver *ExecutorDriver, now int64, withDeadline bool) bool {
	if driver == nil || driver.poll != (executorPollTransaction{}) ||
		!driver.sources.acceptsScan(driver.p, now, withDeadline) {
		return false
	}
	first, firstOK := firstExecutorCatalogSource(&driver.sources)
	if !firstOK {
		return false
	}
	driver.poll.phase = executorPollEpochAPublish
	driver.poll.source = first
	driver.poll.now = now
	driver.poll.withDeadline = withDeadline
	return true
}

func beginExecutorPollEpoch(transaction *executorPollTransaction, sources *ExecutorSourceSet, phase executorPollPhase) bool {
	first, firstOK := firstExecutorCatalogSource(sources)
	if transaction == nil || !firstOK {
		return false
	}
	transaction.phase = phase
	transaction.source = first
	transaction.cursor = 0
	transaction.deadline = 0
	transaction.hasDeadline = false
	transaction.retryBudget = false
	transaction.resolve = publishedEpochResolveCursor{}
	// AwaitExternal is transaction-sticky: unlike a budget retry, epoch B does
	// not itself satisfy a physical acknowledgement missing in epoch A.
	transaction.resampleNow = transaction.withDeadline && first != executorCatalogDone
	return true
}

// executorMinPollBudget counts every slot in each binding-local active prefix,
// plus one common resolve action per epoch and the single request
// acknowledgement between A and B. Configured-but-never-allocated tail slots
// remain covered by full-capacity structural audits, not routine service.
func executorMinPollBudget(sources *ExecutorSourceSet) (uint32, bool) {
	if sources == nil {
		return 0, false
	}
	epoch := uint32(1) // common resolve
	for source := executorCatalogTimers; source < executorCatalogDone; source++ {
		limit, ok := executorCatalogScanLimit(sources, source)
		if !ok {
			return 0, false
		}
		epoch += limit
	}
	return epoch*2 + 1, true // A + acknowledge + B
}

// MinExecutorPollBudget is the exact base budget for one idle driver's fixed
// A/ack/B catalog and two empty common-resolution actions. Non-empty affected
// waits and legacy waiters add explicitly charged reductions; smaller budgets
// are valid and retain exact source and resolution cursors for a later entry.
func MinExecutorPollBudget(driver *ExecutorDriver) (uint32, bool) {
	if !validExecutorDriver(driver) || driver.state != executorDriverActive || driver.poll.phase != executorPollIdle {
		return 0, false
	}
	return executorMinPollBudget(&driver.sources)
}

func (transaction *executorPollTransaction) advanceCatalogSource(sources *ExecutorSourceSet) bool {
	transaction.cursor = 0
	for transaction.source < executorCatalogDone {
		transaction.source++
		limit, ok := executorCatalogScanLimit(sources, transaction.source)
		if !ok {
			return false
		}
		if limit != 0 {
			return true
		}
	}
	return transaction.source == executorCatalogDone
}

// publishExecutorCatalogEntry visits one real source slot and advances the
// durable cursor exactly once. pending is cleared only immediately before slot
// zero of each source. A producer arriving behind the cursor leaves a sticky
// source fact/pending bit for the next epoch and cannot delay this epoch's
// resolve boundary.
func publishExecutorCatalogEntry(driver *ExecutorDriver) bool {
	transaction, sources, p := &driver.poll, &driver.sources, driver.p
	index := uint32(transaction.cursor)
	limit, limitOK := executorCatalogScanLimit(sources, transaction.source)
	if !limitOK || limit == 0 || index >= limit {
		return false
	}
	switch transaction.source {
	case executorCatalogTimers:
		completed, deadline, hasDeadline, ok := sources.timers.drainDueSlotFor(p, transaction.now, index)
		transaction.total.timers += completed
		transaction.total.completed += completed
		if !ok {
			return false
		}
		if hasDeadline && (!transaction.hasDeadline || deadline < transaction.deadline) {
			transaction.deadline, transaction.hasDeadline = deadline, true
		}
		transaction.cursor++
		if uint32(transaction.cursor) == limit && !transaction.advanceCatalogSource(sources) {
			return false
		}
	case executorCatalogPoll:
		if index == 0 && !sources.poll.beginDrainPass(p) {
			return false
		}
		completed, deadline, hasDeadline, ok := sources.poll.drainSlotFor(p, transaction.now, index)
		transaction.total.poll += completed
		transaction.total.completed += completed
		if !ok {
			return false
		}
		if hasDeadline && (!transaction.hasDeadline || deadline < transaction.deadline) {
			transaction.deadline, transaction.hasDeadline = deadline, true
		}
		transaction.cursor++
		if uint32(transaction.cursor) == limit && !transaction.advanceCatalogSource(sources) {
			return false
		}
	case executorCatalogManual:
		if index == 0 && !sources.manual.beginPublishPass(p) {
			return false
		}
		published, lost, ok := sources.manual.publishSlot(p, index)
		transaction.total.manual += int(published)
		transaction.total.manualLost += int(lost)
		transaction.total.completed += int(published + lost)
		if !ok {
			return false
		}
		transaction.cursor++
		if uint32(transaction.cursor) == limit && !transaction.advanceCatalogSource(sources) {
			return false
		}
	case executorCatalogWorker:
		if index == 0 && !sources.worker.beginPublishPass(p) {
			return false
		}
		published, lost, ok := sources.worker.publishSlot(p, index)
		transaction.total.worker += int(published)
		transaction.total.workerLost += int(lost)
		transaction.total.completed += int(published + lost)
		if !ok {
			return false
		}
		transaction.cursor++
		if uint32(transaction.cursor) == limit && !transaction.advanceCatalogSource(sources) {
			return false
		}
	case executorCatalogChannel:
		if index == 0 && !sources.channel.beginPublishPass(p) {
			return false
		}
		published, lost, ok := sources.channel.publishSlot(p, index)
		transaction.total.channel += int(published)
		transaction.total.channelLost += int(lost)
		transaction.total.completed += int(published + lost)
		if !ok {
			return false
		}
		transaction.cursor++
		if uint32(transaction.cursor) == limit && !transaction.advanceCatalogSource(sources) {
			return false
		}
	case executorCatalogControl:
		if index == 0 && !sources.control.beginPublishPass(p) {
			return false
		}
		delivered, late, ok := sources.control.publishSlot(p, nil, index)
		transaction.total.control += int(delivered)
		transaction.total.controlLate += int(late)
		transaction.total.completed += int(delivered + late)
		if !ok {
			return false
		}
		transaction.cursor++
		if uint32(transaction.cursor) == limit && !transaction.advanceCatalogSource(sources) {
			return false
		}
	default:
		return false
	}
	return true
}

func executorProgressFromScan(scan executorSourceScan, used, budget uint32, complete, more, blocked bool) (ExecutorPollProgress, bool) {
	if scan.completed < 0 || scan.timers < 0 || scan.poll < 0 || scan.manual < 0 || scan.manualLost < 0 ||
		scan.worker < 0 || scan.workerLost < 0 ||
		scan.channel < 0 || scan.channelLost < 0 ||
		scan.control < 0 || scan.controlLate < 0 || scan.applyVisits < 0 || scan.promoted < 0 ||
		used > budget || more && blocked {
		return ExecutorPollProgress{}, false
	}
	return ExecutorPollProgress{
		Used:          used,
		Completed:     uint32(scan.completed),
		Timers:        uint32(scan.timers),
		Manual:        uint32(scan.manual),
		ManualLost:    uint32(scan.manualLost),
		Worker:        uint32(scan.worker),
		WorkerLost:    uint32(scan.workerLost),
		Control:       uint32(scan.control),
		ControlLate:   uint32(scan.controlLate),
		ApplyVisits:   uint32(scan.applyVisits),
		Promoted:      uint32(scan.promoted),
		NextDeadline:  scan.deadline,
		Epochs:        scan.epochs,
		Complete:      complete,
		More:          more,
		Blocked:       blocked,
		HasDeadline:   scan.hasDeadline,
		AtomicResolve: false,
		Overshot:      false,
	}, true
}

// pollExecutorSliceAt advances the first production-bounded part of one
// A/ack/B transaction without recursively re-entering the scheduler. Every
// source entry, acknowledgement, candidate action, promotion, and legacy-G
// visit costs exactly one reduction. Administrative phase transitions are
// folded into the action they expose and never hide a collection scan.
func pollExecutorSliceAt(driver *ExecutorDriver, now int64, withDeadline bool, budget uint32) (scan executorSourceScan, progress ExecutorPollProgress, ok bool) {
	if budget == 0 || !validExecutorDriver(driver) || driver.state != executorDriverActive || !idleExecutorScheduler(driver.p) ||
		!driver.sources.acceptsScan(driver.p, now, withDeadline) {
		return executorSourceScan{}, ExecutorPollProgress{}, false
	}
	if driver.poll.phase == executorPollIdle {
		if !beginExecutorPollTransaction(driver, now, withDeadline) {
			return executorSourceScan{}, ExecutorPollProgress{}, false
		}
	} else if driver.poll.withDeadline != withDeadline {
		return driver.poll.total, ExecutorPollProgress{}, false
	}

	used := uint32(0)
	for used < budget {
		transaction := &driver.poll
		switch transaction.phase {
		case executorPollEpochAPublish, executorPollEpochBPublish:
			if transaction.resampleNow {
				// The prior entry ended at Acknowledge before B visited a slot.
				// Capture this entry's fresh sample for all of epoch B.
				transaction.now = now
				transaction.resampleNow = false
			}
			if transaction.source == executorCatalogDone {
				if transaction.phase == executorPollEpochAPublish {
					transaction.phase = executorPollEpochAResolve
				} else {
					transaction.phase = executorPollEpochBResolve
				}
				continue
			}
			if !publishExecutorCatalogEntry(driver) {
				return transaction.total, ExecutorPollProgress{}, false
			}
			used++
		case executorPollEpochAResolve, executorPollEpochBResolve:
			step := &transaction.resolveStep
			resolved := resolvePublishedEpochStep(&driver.sources, driver.p, &transaction.resolve, step)
			if !resolved || step.applyVisits < 0 || step.promoted < 0 {
				return transaction.total, ExecutorPollProgress{}, false
			}
			transaction.total.promoted += step.promoted
			transaction.total.applyVisits += step.applyVisits
			transaction.retryBudget = transaction.retryBudget || step.retryBudget
			transaction.awaitExternal = transaction.awaitExternal || step.awaitExternal
			used++
			if !step.complete {
				continue
			}
			transaction.total.epochs++
			transaction.total.deadline = transaction.deadline
			transaction.total.hasDeadline = transaction.hasDeadline
			if transaction.phase == executorPollEpochAResolve {
				transaction.phase = executorPollAcknowledge
				transaction.source = executorCatalogDone
				transaction.cursor = 0
				continue
			}

			// B is the transaction boundary. Copy diagnostics before returning
			// the continuation state to exact zero. Facts published behind B's
			// cursor remain sticky and produce More for a later host entry.
			completed := transaction.total
			retryBudget, awaitExternal := transaction.retryBudget, transaction.awaitExternal
			*transaction = executorPollTransaction{}
			sourceMore := retryBudget || driver.sources.pending(driver.p) ||
				driver.registry.ObserveRequested(driver.handle) || preemptLoad(&driver.p.schedule) != scheduleIdle
			more := sourceMore || driver.p.readyHead != nil
			blocked := !more && (awaitExternal || HasWaiting(driver.p))
			progress, progressOK := executorProgressFromScan(completed, used, budget, true, more, blocked)
			return completed, progress, progressOK
		case executorPollAcknowledge:
			if _, acknowledged := driver.registry.Acknowledge(driver.handle); !acknowledged {
				return transaction.total, ExecutorPollProgress{}, false
			}
			used++
			if !beginExecutorPollEpoch(transaction, &driver.sources, executorPollEpochBPublish) {
				return transaction.total, ExecutorPollProgress{}, false
			}
		default:
			return transaction.total, ExecutorPollProgress{}, false
		}
	}

	scan = driver.poll.total
	progress, ok = executorProgressFromScan(scan, used, budget, false, true, false)
	return scan, progress, ok
}

// PollExecutorSlice services a no-deadline source catalog for at most budget
// catalog, resolution, and acknowledgement reductions. More never authorizes
// direct recursion; a target schedules a later host entry and returns first.
// A non-empty bounded-runner cursor requires EnterExecutorRunCompatibility;
// the runner itself advances the private primitive so its fairness debt is not
// silently discarded by this legacy exported entry.
func PollExecutorSlice(driver *ExecutorDriver, budget uint32) (ExecutorPollProgress, bool) {
	if driver == nil || driver.sources.usesMonotonicTime() || !emptyExecutorRunCursor(driver) {
		return ExecutorPollProgress{}, false
	}
	_, progress, ok := pollExecutorSliceAt(driver, 0, false, budget)
	return progress, ok
}

// PollExecutorSliceAt is the deadline-capable counterpart. now is frozen by
// the first slice of each logical epoch; later samples passed while that epoch
// is incomplete are ignored by the driver. When a prior entry ended exactly at
// Acknowledge, B takes the next call's fresh value before its first source slot.
// Like PollExecutorSlice it rejects a non-empty bounded-runner cursor.
func PollExecutorSliceAt(driver *ExecutorDriver, now int64, budget uint32) (ExecutorPollProgress, bool) {
	if driver == nil || !driver.sources.usesMonotonicTime() || !emptyExecutorRunCursor(driver) {
		return ExecutorPollProgress{}, false
	}
	_, progress, ok := pollExecutorSliceAt(driver, now, true, budget)
	return progress, ok
}
