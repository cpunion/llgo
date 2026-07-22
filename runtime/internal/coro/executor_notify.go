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

func PrepareExecutorNotifyWait(
	driver *ExecutorDriver,
	catalog *KeyedWaitCatalog,
	token *WaitToken,
	key uintptr,
	logicalTicket uint32,
) (WaitTicket, KeyedWaitHandle, KeyedWaitPrepareResult) {
	if !validRunningExecutorOwner(driver) {
		return 0, KeyedWaitHandle{}, KeyedWaitPrepareInvalid
	}
	return PrepareNotifyWait(
		driver.p, driver.sources.waitTable(), catalog, token, key, logicalTicket,
	)
}

func PostPreparedExecutorNotifyWait(
	driver *ExecutorDriver,
	catalog *KeyedWaitCatalog,
	handle KeyedWaitHandle,
	key uintptr,
	logicalTicket uint32,
) KeyedWaitPostResult {
	if !validRunningExecutorOwner(driver) {
		return KeyedWaitPostInvalid
	}
	return PostPreparedNotifyWait(
		catalog, driver.sources.waitTable(), handle, key, logicalTicket,
	)
}

func PostExecutorNotifyWaitOne(
	driver *ExecutorDriver,
	catalog *KeyedWaitCatalog,
	key uintptr,
	logicalTicket uint32,
) KeyedWaitPostResult {
	if !validRunningExecutorOwner(driver) {
		return KeyedWaitPostInvalid
	}
	return PostNotifyWaitOne(catalog, driver.sources.waitTable(), key, logicalTicket)
}

func PostExecutorNotifyWaitAll(
	driver *ExecutorDriver,
	catalog *KeyedWaitCatalog,
	key uintptr,
	first, next uint32,
) (uint32, bool) {
	if !validRunningExecutorOwner(driver) {
		return 0, false
	}
	return PostNotifyWaitAll(catalog, driver.sources.waitTable(), key, first, next)
}

func RetireCompletedExecutorNotifyWait(
	driver *ExecutorDriver,
	catalog *KeyedWaitCatalog,
	token *WaitToken,
	ticket WaitTicket,
	handle KeyedWaitHandle,
) bool {
	return validRunningExecutorOwner(driver) && RetireCompletedNotifyWait(
		catalog, driver.sources.waitTable(), handle, token, ticket,
	)
}
