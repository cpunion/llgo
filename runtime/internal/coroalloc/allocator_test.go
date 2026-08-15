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

package coroalloc

import "testing"

func TestBootstrapStateSuccessAndIdempotence(t *testing.T) {
	current := bootstrapUninitialized
	current, got := beginBootstrap(current)
	if got != bootstrapStart || current != bootstrapInitializing {
		t.Fatalf("begin = %d, state=%d; want start/initializing", got, current)
	}
	current, success := finishBootstrap(current, true)
	if !success || current != bootstrapReady {
		t.Fatalf("finish success state=%d, want ready", current)
	}
	current, got = beginBootstrap(current)
	if got != bootstrapAlreadyReady || current != bootstrapReady {
		t.Fatalf("repeat begin = %d, state=%d; want already-ready/ready", got, current)
	}
}

func TestBootstrapStateFailsClosed(t *testing.T) {
	tests := []struct {
		name    string
		initial bootstrapState
		finish  bool
	}{
		{name: "backend failure", initial: bootstrapUninitialized, finish: true},
		{name: "recursive bootstrap", initial: bootstrapInitializing},
		{name: "prior failure", initial: bootstrapFailed},
		{name: "invalid state", initial: bootstrapState(0xff)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := test.initial
			current, decision := beginBootstrap(current)
			if test.finish {
				if decision != bootstrapStart {
					t.Fatalf("begin = %d, want start", decision)
				}
				var success bool
				current, success = finishBootstrap(current, false)
				if success {
					t.Fatal("failed backend committed ready")
				}
			} else if decision != bootstrapReject {
				t.Fatalf("begin = %d, want reject", decision)
			}
			if current != bootstrapFailed {
				t.Fatalf("state=%d, want permanently failed", current)
			}
			current, decision = beginBootstrap(current)
			current, success := finishBootstrap(current, true)
			if decision != bootstrapReject || success || current != bootstrapFailed {
				t.Fatal("failed state was recoverable")
			}
		})
	}
}

func TestSelectedBackendKindIsKnown(t *testing.T) {
	if !backendAllocationsAreZeroed {
		t.Fatal("selected coroutine allocator does not guarantee zero-filled storage")
	}
	switch backendKind {
	case "bdwgc", "malloc", "tinygogc":
	default:
		t.Fatalf("unknown statically selected backend %q", backendKind)
	}
}

func TestNativeCacheAllocationSizeClasses(t *testing.T) {
	tests := []struct {
		size uintptr
		want uintptr
	}{
		{0, 256}, {256, 256}, {257, 288},
		{1024, 1024}, {1025, 1152}, {1152, 1152},
		{4096, 4096}, {4097, 8192}, {8192, 8192},
		{8193, 16384}, {65536, 65536}, {65537, 65537},
	}
	for _, test := range tests {
		if got := nativeCacheAllocationSize(test.size); got != test.want {
			t.Fatalf("native class for %d = %d, want %d", test.size, got, test.want)
		}
	}
}
