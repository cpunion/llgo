//go:build !llgo

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

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTLSSlotInitializesAndRegistersBeforePublishing locks the thread-exit UAF
// fix in internal/clite/tls/tls_common.go. Under stackless coroutines,
// registerSlot (GC_add_roots) may suspend on a worker thread and resume on a
// different OS thread; publishing mem via key.Set before registerSlot would let
// the original OS thread exit and free mem while this coroutine still holds and
// writes s.
func TestTLSSlotInitializesAndRegistersBeforePublishing(t *testing.T) {
	path := filepath.Join("internal", "clite", "tls", "tls_common.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	start := strings.Index(src, "func setSlow[T any](")
	if start < 0 {
		t.Fatalf("%s missing setSlow", path)
	}
	end := strings.Index(src[start:], "\nfunc ")
	if end < 0 {
		end = len(src) - start
	}
	body := src[start : start+end]

	valueWrite := strings.Index(body, "s.value = value")
	destructorWrite := strings.Index(body, "s.destructor = destructor")
	registerCall := strings.Index(body, "registerSlot(s)")
	recheckGet := strings.Index(body, "existing := key.Get()")
	publishSet := strings.Index(body, "key.Set(mem)")
	if valueWrite < 0 || destructorWrite < 0 || registerCall < 0 || recheckGet < 0 || publishSet < 0 {
		t.Fatalf("setSlow missing required lifecycle step: value=%d destructor=%d register=%d recheck=%d publish=%d",
			valueWrite, destructorWrite, registerCall, recheckGet, publishSet)
	}
	if !(valueWrite < registerCall && destructorWrite < registerCall && registerCall < recheckGet && recheckGet < publishSet) {
		t.Fatalf("setSlow must initialize and registerSlot(s) before rechecking/publishing key.Set(mem): value=%d destructor=%d register=%d recheck=%d publish=%d",
			valueWrite, destructorWrite, registerCall, recheckGet, publishSet)
	}
	// After key.Set(mem) succeeds, setSlow must not write back through s.
	afterPublish := body[publishSet:]
	if strings.Contains(afterPublish, "s.value") || strings.Contains(afterPublish, "s.destructor") {
		t.Fatalf("setSlow dereferences slot after key.Set(mem):\n%s", afterPublish)
	}
}
