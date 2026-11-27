//go:build llgo && !baremetal

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

package runtime

// loadCallerLocationStore and callerLocationStoreForGoroutine store the
// per-goroutine logical shadow stack directly on g so PushCallerLocationFrame
// and PopCallerLocationFrame avoid LocalPackageLogical's linked-list lookup
// while still migrating with the logical goroutine across OS threads.
func loadCallerLocationStore() *callerLocationStore {
	gp := getgIfPresent()
	if gp == nil {
		return nil
	}
	return gp.callerStore
}

func callerLocationStoreForGoroutine() *callerLocationStore {
	if gp := getgIfPresent(); gp != nil {
		if store := gp.callerStore; store != nil {
			return store
		}
		return initCallerLocationStore(gp)
	}
	return initCallerLocationStore(getg())
}

func initCallerLocationStore(gp *g) *callerLocationStore {
	store := gp.callerStore
	if store == nil {
		store = new(callerLocationStore)
		gp.callerStore = store
	}
	return store
}
