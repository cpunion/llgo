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

// PrepareExecutorSemaphoreWait is the running-owner entry for publishing one
// synchronous-style semaphore acquisition before its exact coroPark.
func PrepareExecutorSemaphoreWait(
	driver *ExecutorDriver,
	catalog *SemaphoreWaitCatalog,
	token *WaitToken,
	key uintptr,
) (WaitTicket, SemaphoreWaitHandle, SemaphoreWaitPrepareResult) {
	if !validRunningExecutorOwner(driver) {
		return 0, SemaphoreWaitHandle{}, SemaphoreWaitPrepareInvalid
	}
	return PrepareSemaphoreWait(driver.p, driver.sources.waitTable(), catalog, token, key)
}

// PostExecutorSemaphoreWait publishes one keyed waiter from the currently
// running executor owner. The caller must perform the target-specific executor
// request when the result is SemaphoreWaitPosted.
func PostExecutorSemaphoreWait(driver *ExecutorDriver, catalog *SemaphoreWaitCatalog, key uintptr) SemaphoreWaitPostResult {
	if !validRunningExecutorOwner(driver) {
		return SemaphoreWaitPostInvalid
	}
	return PostSemaphoreWait(catalog, driver.sources.waitTable(), key)
}

// PostPreparedExecutorSemaphoreWait is the exact-handle repair edge used by a
// prepare owner after its post-publication semaphore counter recheck.
func PostPreparedExecutorSemaphoreWait(
	driver *ExecutorDriver,
	catalog *SemaphoreWaitCatalog,
	handle SemaphoreWaitHandle,
) SemaphoreWaitPostResult {
	if !validRunningExecutorOwner(driver) {
		return SemaphoreWaitPostInvalid
	}
	return PostPreparedSemaphoreWait(catalog, driver.sources.waitTable(), handle)
}

// RetireCompletedExecutorSemaphoreWait releases the exact catalog and common
// wait registrations after the synchronous-style continuation resumed.
func RetireCompletedExecutorSemaphoreWait(
	driver *ExecutorDriver,
	catalog *SemaphoreWaitCatalog,
	token *WaitToken,
	ticket WaitTicket,
	handle SemaphoreWaitHandle,
) bool {
	return validRunningExecutorOwner(driver) && RetireCompletedSemaphoreWait(
		catalog,
		driver.sources.waitTable(),
		handle,
		token,
		ticket,
	)
}
