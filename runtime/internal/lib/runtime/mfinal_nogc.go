//go:build nogc || baremetal

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

import "unsafe"

// SetFinalizer is deliberately inert when no finalizer-capable collector is
// present. Objects are not reclaimed by the leaking/nogc profile, and tinygogc
// does not implement finalizer queues, so the callback can never run.
func SetFinalizer(obj any, finalizer any) {}

// addCleanupPtr mirrors SetFinalizer for the leaking profile. Weak and cleanup
// bookkeeping may retain a tombstone forever, but it must never claim that a
// callback can run without a collector.
func addCleanupPtr(_ unsafe.Pointer, _ func()) (cancel func()) {
	return func() {}
}
