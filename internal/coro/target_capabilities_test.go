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

import "testing"

func TestProgramCapabilitiesKeepWorkerAndPanicOnFaultOrthogonal(t *testing.T) {
	for _, test := range []struct {
		worker, panicOnFault bool
	}{
		{},
		{worker: true},
		{panicOnFault: true},
		{worker: true, panicOnFault: true},
	} {
		capabilities := NewProgramCapabilities(test.worker, test.panicOnFault)
		if !capabilities.Valid() || capabilities.Worker() != test.worker ||
			capabilities.PanicOnFault() != test.panicOnFault {
			t.Fatalf("capabilities(%t, %t) = %#x", test.worker, test.panicOnFault, capabilities)
		}
	}
	if invalid := ProgramCapabilities(1 << 7); invalid.Valid() {
		t.Fatalf("unknown capability %#x was accepted", invalid)
	}
}
