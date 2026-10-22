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

// OSThreadSuspension roots one ordinary suspended locked task while another M
// owns the same P. The target keeps this record in stable M storage and carries
// its own scalar owner identity forward. No native handle or callback is held
// by the core, and no pointer is added to G or to each parked coroutine frame.
//
// This record is scheduler-owner-only. Physical ownership must be returned and
// acknowledged by the target before RestoreOSThreadSuspension. A copied record
// is not a capability: only the exact address linked into P may be restored.
type OSThreadSuspension struct {
	driver *ExecutorDriver
	task   *G
	next   *OSThreadSuspension
	owner  uint32
	phase  osThreadSuspendPhase
}

func suspendedOSThreadTask(p *P, task *G) *OSThreadSuspension {
	if p == nil || task == nil {
		return nil
	}
	for slow, fast := p.osThreadSuspensions, p.osThreadSuspensions; fast != nil && fast.next != nil; {
		slow, fast = slow.next, fast.next.next
		if slow == fast {
			return nil
		}
	}
	for record := p.osThreadSuspensions; record != nil; record = record.next {
		if record.task == task {
			return record
		}
	}
	return nil
}

func validOSThreadSuspensions(p *P) bool {
	if p == nil {
		return false
	}
	for slow, fast := p.osThreadSuspensions, p.osThreadSuspensions; fast != nil && fast.next != nil; {
		slow, fast = slow.next, fast.next.next
		if slow == fast {
			return false
		}
	}
	for record := p.osThreadSuspensions; record != nil; record = record.next {
		for other := record.next; other != nil; other = other.next {
			if other.task == record.task || other.owner == record.owner {
				return false
			}
		}
		g := record.task
		if record.driver == nil || record.driver.p != p || record.owner == 0 ||
			g == nil || g == p.osThreadLockOwner || !ValidG(g) || g.osThreadLockDepth == 0 ||
			g.runP != nil || g.transferState != runnableTransferGIdle ||
			(record.phase != osThreadSuspendPark && record.phase != osThreadSuspendYieldNeedsPeer && record.phase != osThreadSuspendYieldPeerServiced) {
			return false
		}
		if g.queued {
			if g.state != GRunnable || g.waiting {
				return false
			}
		} else if g.state != GWaiting || !g.waiting {
			return false
		}
	}
	return true
}

// DetachOSThreadSuspension follows the ordinary committed Yield/Park prepare
// boundary. It removes only the P's physical-selection header; the task and
// all source/result ownership remain on their original P and queues.
func DetachOSThreadSuspension(driver *ExecutorDriver, task *G, record *OSThreadSuspension, owner uint32) bool {
	if record == nil || *record != (OSThreadSuspension{}) || owner == 0 || task == nil {
		return false
	}
	detached, _, ok := OSThreadSuspendHandoffStatus(driver)
	if !ok || !detached || driver == nil || driver.p.osThreadLockOwner != task ||
		!idleExecutorScheduler(driver.p) || driver.run.issued != ActionInvalid || driver.poll.phase != executorPollIdle {
		return false
	}
	p := driver.p
	if !validReadyQueue(p) || !validSchedulerWaitQueues(p) || suspendedOSThreadTask(p, task) != nil {
		return false
	}
	for prior := p.osThreadSuspensions; prior != nil; prior = prior.next {
		if prior.owner == owner {
			return false
		}
	}
	record.driver, record.task, record.owner = driver, task, owner
	record.phase, record.next = p.osThreadSuspend, p.osThreadSuspensions
	p.osThreadSuspensions = record
	p.osThreadLockOwner = nil
	p.osThreadSuspend = osThreadSuspendAttached
	driver.run.readyDebt = driver.run.readyDebt && runnableForOSThreadOwner(p)
	return true
}

// ReadyOSThreadSuspension chooses an eligible suspended owner in ready FIFO
// order. A yielding owner first owes another task one committed physical
// action. A parked owner is eligible only after its original source reducer
// has promoted it, preserving the existing cancel/ack/cleanup protocol.
func ReadyOSThreadSuspension(driver *ExecutorDriver) (record *OSThreadSuspension, owner uint32, ok bool) {
	if !validExecutorDriverHeader(driver) || driver.state != executorDriverActive ||
		driver.run.issued != ActionInvalid || driver.poll.phase != executorPollIdle ||
		!idleExecutorScheduler(driver.p) || !validReadyQueue(driver.p) || !validSchedulerWaitQueues(driver.p) {
		return nil, 0, false
	}
	p := driver.p
	if p.osThreadLockOwner != nil || p.osThreadSuspend != osThreadSuspendAttached {
		return nil, 0, true
	}
	if record := nextReadyOSThreadSuspension(p); record != nil {
		return record, record.owner, true
	}

	return nil, 0, true
}

// A routed completion can owe a ready physical action to an M which is
// currently dormant. This is logical readiness, not permission for the
// compensation M to select that locked G. The target performs the exact
// returned-M handoff before Restore grants physical execution again.
func nextReadyOSThreadSuspension(p *P) *OSThreadSuspension {
	if p == nil || p.osThreadSuspensions == nil {
		return nil
	}
	switch p.osThreadSuspend {
	case osThreadSuspendAttached:
		if p.osThreadLockOwner != nil {
			return nil
		}
	case osThreadSuspendPark, osThreadSuspendYieldNeedsPeer, osThreadSuspendYieldPeerServiced:
		if p.osThreadLockOwner == nil {
			return nil
		}
	default:
		return nil
	}
	for slow, fast := p.readyHead, p.readyHead; fast != nil && fast.nextReady != nil; {
		slow, fast = slow.nextReady, fast.nextReady.nextReady
		if slow == fast {
			return nil
		}
	}
	for task := p.readyHead; task != nil; task = task.nextReady {
		record := suspendedOSThreadTask(p, task)
		if record != nil && record.driver != nil && record.driver.p == p && record.owner != 0 &&
			task.queued && task.state == GRunnable && task.runP == nil && task.osThreadLockDepth != 0 &&
			(record.phase == osThreadSuspendPark || record.phase == osThreadSuspendYieldPeerServiced) {
			return record
		}
	}
	return nil
}

// RestoreOSThreadSuspension consumes the exact selected cold record. The
// target must already own P on the named physical M. It may not restore a
// waiting task or substitute another M with the same logical route.
func RestoreOSThreadSuspension(driver *ExecutorDriver, record *OSThreadSuspension, owner uint32) bool {
	selected, selectedOwner, ok := ReadyOSThreadSuspension(driver)
	if !ok || selected == nil || selected != record || selectedOwner != owner {
		return false
	}
	return restoreOSThreadSuspension(driver, record, owner)
}

// AbortOSThreadSuspension is only for the original physical owner before any
// claimant is published. It may restore an unserviced yield or still-waiting
// park; after publication the target must use the ready/returned protocol.
func AbortOSThreadSuspension(driver *ExecutorDriver, record *OSThreadSuspension, owner uint32) bool {
	if driver == nil || !validExecutorDriverHeader(driver) || driver.state != executorDriverActive ||
		driver.run.issued != ActionInvalid || driver.poll.phase != executorPollIdle ||
		!idleExecutorScheduler(driver.p) || !validReadyQueue(driver.p) || !validSchedulerWaitQueues(driver.p) ||
		record == nil || record.phase == osThreadSuspendYieldPeerServiced {
		return false
	}
	return restoreOSThreadSuspension(driver, record, owner)
}

func restoreOSThreadSuspension(driver *ExecutorDriver, record *OSThreadSuspension, owner uint32) bool {
	p := driver.p
	if record == nil || record.driver != driver || record.owner != owner || owner == 0 ||
		p.osThreadLockOwner != nil || p.osThreadSuspend != osThreadSuspendAttached {
		return false
	}
	var previous *OSThreadSuspension
	for current := p.osThreadSuspensions; current != nil; current = current.next {
		if current != record {
			previous = current
			continue
		}
		if previous == nil {
			p.osThreadSuspensions = record.next
		} else {
			previous.next = record.next
		}
		p.osThreadLockOwner, p.osThreadSuspend = record.task, osThreadSuspendAttached
		*record = OSThreadSuspension{}
		driver.run.readyDebt = p.osThreadLockOwner.queued
		return true
	}
	return false
}

func OSThreadSuspensionsPresent(driver *ExecutorDriver) bool {
	return driver != nil && driver.p != nil && driver.p.osThreadSuspensions != nil
}
