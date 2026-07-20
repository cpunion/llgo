//go:build coro_channel_adapter_test && coro_channel_owner_test

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

import "testing"

func expectCoroChannelOwnerGateAbort(t *testing.T, operation func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("coroutine channel owner gate accepted an invalid transition")
		}
	}()
	operation()
}

func TestCoroChannelOwnerGateIsSingleAttemptAndFailClosed(t *testing.T) {
	var mutex channelMutex
	if result := mutex.Init(nil); result != 0 || mutex.state != channelOwnerGateIdle {
		t.Fatalf("initialize coroutine channel owner gate = (%d, %d)", result, mutex.state)
	}
	mutex.Lock()
	if mutex.state != channelOwnerGateHeld {
		t.Fatalf("locked coroutine channel owner gate = %d", mutex.state)
	}
	expectCoroChannelOwnerGateAbort(t, mutex.Lock)
	if mutex.state != channelOwnerGateHeld {
		t.Fatalf("reentrant acquisition changed coroutine channel owner gate = %d", mutex.state)
	}
	mutex.Unlock()
	if mutex.state != channelOwnerGateIdle {
		t.Fatalf("released coroutine channel owner gate = %d", mutex.state)
	}
	expectCoroChannelOwnerGateAbort(t, mutex.Unlock)
	if mutex.state != channelOwnerGateIdle {
		t.Fatalf("unbalanced release changed coroutine channel owner gate = %d", mutex.state)
	}
}
