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

// ExecutorSourceSet is the statically assembled set of durable event sources
// owned by one executor/P. It deliberately contains no interface or function
// value: targets with a closed source catalog can compile the protocol as
// direct calls, including targets whose dynamic coroutine dispatch is not yet
// available.
//
// Source-specific submission, cancellation, detach, and quiescence remain in
// each source module. The executor sees only the aggregate scan, pending,
// deadline, empty, bind, and unbind operations below. Adding another source
// therefore extends this file rather than duplicating the executor's
// drain/ack/idle/close transactions.
//
// A source publishes an exact operation outcome; it does not directly choose a
// select winner or enqueue a G. The scheduler-owned park state consumes all
// candidate outcomes, resolves winner/cancellation, and exposes at most one
// runnable G during the promotion step. The current one-token wait path is the
// first park-state implementation, not a SourceSet restriction.
//
// The set is embedded in ExecutorDriver and must remain at a stable address
// from bind through unbind. Its fields are scheduler-owner-only; producers
// retain only their source's scalar handle and the ExecutorHandle doorbell.
type ExecutorSourceSet struct {
	magic  uint32
	owner  *P
	waits  *WaitRegistrationTable
	timers *TimerRegistrationTable
}

const executorSourceSetMagic uint32 = 0x53524331 // "SRC1"

type executorSourceScan struct {
	completed   int
	waits       int
	timers      int
	promoted    int
	deadline    int64
	hasDeadline bool
}

func (scan *executorSourceScan) add(other executorSourceScan) {
	scan.completed += other.completed
	scan.waits += other.waits
	scan.timers += other.timers
	scan.promoted += other.promoted
	// Every successful source-set scan reports the complete current deadline
	// view, so the last scan is authoritative rather than a minimum of stale
	// observations from earlier passes.
	scan.deadline = other.deadline
	scan.hasDeadline = other.hasDeadline
}

func validExecutorSourceSet(sources *ExecutorSourceSet, p *P) bool {
	if sources == nil || sources.magic != executorSourceSetMagic || p == nil || sources.owner != p ||
		sources.waits == nil || sources.waits.owner != p {
		return false
	}
	return sources.timers == nil || sources.timers.owner == p
}

// bindExecutorSourceSet binds every statically configured source as one
// transaction. A later-source failure rolls back earlier empty bindings and
// leaves the source set exact-zero.
func bindExecutorSourceSet(sources *ExecutorSourceSet, p *P, waits *WaitRegistrationTable, timers *TimerRegistrationTable) bool {
	if sources == nil || *sources != (ExecutorSourceSet{}) || p == nil || waits == nil ||
		!bindRegistrationTable(waits, p) {
		return false
	}
	if timers != nil && !bindTimerRegistrationTable(timers, p) {
		_ = unbindRegistrationTable(waits, p)
		return false
	}
	sources.magic = executorSourceSetMagic
	sources.owner = p
	sources.waits = waits
	sources.timers = timers
	return true
}

func (sources *ExecutorSourceSet) usesMonotonicTime() bool {
	return sources != nil && sources.timers != nil
}

func (sources *ExecutorSourceSet) acceptsScan(p *P, now int64, withDeadline bool) bool {
	return validExecutorSourceSet(sources, p) &&
		withDeadline == sources.usesMonotonicTime() && (!withDeadline || now >= 0)
}

func (sources *ExecutorSourceSet) waitTable() *WaitRegistrationTable {
	if sources == nil {
		return nil
	}
	return sources.waits
}

func (sources *ExecutorSourceSet) timerTable() *TimerRegistrationTable {
	if sources == nil {
		return nil
	}
	return sources.timers
}

// drain consumes one complete source-set snapshot and then asks scheduler park
// state to promote newly ready Gs. Source order is a property of the static
// catalog, not of the executor transaction. Partial completion counts are
// retained on failure.
func (sources *ExecutorSourceSet) drain(p *P, now int64, withDeadline bool) (scan executorSourceScan, ok bool) {
	if !sources.acceptsScan(p, now, withDeadline) {
		return executorSourceScan{}, false
	}
	scan.waits, ok = sources.waits.drainFor(p)
	scan.completed += scan.waits
	if !ok {
		return scan, false
	}
	if sources.timers != nil {
		scan.timers, scan.deadline, scan.hasDeadline, ok = sources.timers.drainDueFor(p, now)
		scan.completed += scan.timers
		if !ok {
			return scan, false
		}
	}
	scan.promoted, ok = pollReady(p)
	return scan, ok
}

// pending reports producer-published facts that require another owner scan.
// Deadline sources are sampled by drain and represented by the aggregate
// deadline; future deadlines are not pending runnable work.
func (sources *ExecutorSourceSet) pending(p *P) bool {
	return validExecutorSourceSet(sources, p) && sources.waits.Pending()
}

func (sources *ExecutorSourceSet) nextDeadline(p *P) (deadline int64, hasDeadline, ok bool) {
	if !validExecutorSourceSet(sources, p) || sources.timers == nil {
		return 0, false, false
	}
	return sources.timers.nextDeadlineFor(p)
}

func (sources *ExecutorSourceSet) empty(p *P) bool {
	return validExecutorSourceSet(sources, p) && registrationTableEmpty(sources.waits, p) &&
		(sources.timers == nil || timerRegistrationTableEmpty(sources.timers, p))
}

// drainForClose consumes sources that can publish without a clock sample and
// verifies that the complete set is empty. A deadline source must already be
// empty before close; guessing a timestamp during shutdown would change timer
// semantics.
func (sources *ExecutorSourceSet) drainForClose(p *P) (scan executorSourceScan, ok bool) {
	if !validExecutorSourceSet(sources, p) {
		return executorSourceScan{}, false
	}
	scan.waits, ok = sources.waits.drainFor(p)
	scan.completed = scan.waits
	if !ok || !sources.empty(p) {
		return scan, false
	}
	return scan, true
}

func unbindExecutorSourceSet(sources *ExecutorSourceSet, p *P) bool {
	if !validExecutorSourceSet(sources, p) || !sources.empty(p) {
		return false
	}
	if sources.timers != nil && !unbindTimerRegistrationTable(sources.timers, p) {
		return false
	}
	if !unbindRegistrationTable(sources.waits, p) {
		return false
	}
	*sources = ExecutorSourceSet{}
	return true
}
