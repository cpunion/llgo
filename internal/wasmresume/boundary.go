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

package wasmresume

import "strings"

const (
	runtimeResumePrefix = "github.com/goplus/llgo/runtime/internal/wasmresume."
	runtimeAllocRoot    = "github.com/goplus/llgo/runtime/internal/runtime.AllocRoot"
	runtimeFreeRoot     = "github.com/goplus/llgo/runtime/internal/runtime.FreeRoot"
	runtimeRunWasmMain  = "github.com/goplus/llgo/runtime/internal/runtime.RunWasmMain"
)

// IsRuntimeABIImplementation reports functions which implement the resumable
// ABI itself and therefore cannot be lowered through that same ABI.
func IsRuntimeABIImplementation(name string) bool {
	return strings.HasPrefix(name, runtimeResumePrefix)
}

// IsNonSuspendingBoundary reports leaf runtime entry points which remain
// callable without allocating a resumable frame.
func IsNonSuspendingBoundary(name string) bool {
	return (IsRuntimeABIImplementation(name) && name != SuspendSymbol) ||
		name == runtimeAllocRoot ||
		name == runtimeFreeRoot ||
		name == runtimeRunWasmMain
}
