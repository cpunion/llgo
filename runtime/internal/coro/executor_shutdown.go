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

// RequestExecutorShutdownDrain is the ordinary-domain counterpart of command
// shutdown. It runs only on the exact P owner after the bounded run cursor has
// entered compatibility. Every task must cross its compiler cancellation gate
// while typed sources are still bound, so selected payloads, worker results,
// channel claims, and parked operations retain their normal cleanup ordering.
//
// A prepared destroy continuation contains no user code and is allowed to
// finish without inserting a cancellation request in front of the physical
// destroy. All other runnable and waiting tasks receive sticky Shutdown.
func RequestExecutorShutdownDrain(driver *ExecutorDriver) (needed, ok bool) {
	if !validExecutorDriver(driver) || driver.state != executorDriverActive ||
		driver.poll.phase != executorPollIdle || !emptyExecutorRunCursor(driver) ||
		!idleExecutorScheduler(driver.p) || !validReadyQueue(driver.p) ||
		!validSchedulerWaitQueues(driver.p) {
		return false, false
	}
	p := driver.p
	request := func(g *G, resumeGate bool) bool {
		if g == nil {
			return false
		}
		needed = true
		return !resumeGate || RequestTaskCancellation(p, g, TaskCancelShutdown)
	}
	for g := p.readyHead; g != nil; g = g.nextReady {
		switch g.runAction {
		case ActionInvalid, ActionCheckResume:
			if !request(g, true) {
				return false, false
			}
		case ActionCheckDestroy, ActionPanicDestroy:
			if !request(g, false) {
				return false, false
			}
		default:
			return false, false
		}
	}
	for record := p.parkWaitHead; record != nil; record = record.activeNext {
		if record.g == nil || !request(record.g, true) {
			return false, false
		}
	}
	return needed, true
}

// ExecutorShutdownDrained is stronger than the BeginExecutorClose preflight:
// command shutdown may deliberately unbind a driver while source-independent
// runnable frames remain for direct destroy, whereas an ordinary fleet owner
// must drain and reclaim every task before its physical M can leave.
func ExecutorShutdownDrained(driver *ExecutorDriver) bool {
	return canBeginExecutorClose(driver) && driver.p.readyHead == nil && driver.p.readyTail == nil
}
