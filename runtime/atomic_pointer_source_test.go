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
	"strings"
	"testing"
)

func TestRuntimeOwnsInternalAtomicPointerGoHooks(t *testing.T) {
	source, err := os.ReadFile("internal/lib/runtime/atomic_pointer_llgo.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, required := range []string{
		"//go:linkname atomic_storePointer internal/runtime/atomic.storePointer",
		"func atomic_storePointer(ptr *unsafe.Pointer, new unsafe.Pointer)",
		"atomic.StorePointer(ptr, new)",
		"//go:linkname atomic_casPointer internal/runtime/atomic.casPointer",
		"func atomic_casPointer(ptr *unsafe.Pointer, old, new unsafe.Pointer) bool",
		"atomic.CompareAndSwapPointer(ptr, old, new)",
		"conservative, non-moving collector",
		"bodyless stdlib hook",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("atomic pointer runtime adapter lacks %q", required)
		}
	}
	for _, forbidden := range []string{"go func", "llgo.syscall", "coroPark", "pthread", "libuv"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("atomic pointer runtime adapter contains asynchronous/foreign mechanism %q", forbidden)
		}
	}
}
