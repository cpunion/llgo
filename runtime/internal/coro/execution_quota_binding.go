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

type executionQuotaLifecycle uint32

const (
	executionQuotaUnused executionQuotaLifecycle = iota
	executionQuotaActive
	executionQuotaSealed
	executionQuotaRetired
)

// BindExecutorServicePressure gives one live executor a read-only view of
// remote quota pressure. The quota and driver remain stable through driver
// retirement. The hint lets compiler safepoints return to the scheduler while
// TryAcquire and Release retain sole authority over managed execution.
func BindExecutorServicePressure(driver *ExecutorDriver, quota *ExecutionQuota) bool {
	if !validExecutorDriver(driver) || driver.state != executorDriverActive || driver.servicePressure != nil {
		return false
	}
	if _, ok := quota.Limit(); !ok {
		return false
	}
	driver.servicePressure = quota
	return true
}
