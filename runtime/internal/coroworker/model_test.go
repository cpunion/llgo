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

package coroworker

import (
	"testing"
	"unsafe"
)

func TestResultIsThreePointerWords(t *testing.T) {
	want := uintptr(3) * unsafe.Sizeof(uintptr(0))
	if got := unsafe.Sizeof(Result{}); got != want {
		t.Fatalf("Result size = %d, want %d", got, want)
	}
	if unsafe.Offsetof(Result{}.R1) != 0 ||
		unsafe.Offsetof(Result{}.R2) != unsafe.Sizeof(uintptr(0)) ||
		unsafe.Offsetof(Result{}.Errno) != 2*unsafe.Sizeof(uintptr(0)) {
		t.Fatalf("Result field offsets do not match the C ABI")
	}
}
