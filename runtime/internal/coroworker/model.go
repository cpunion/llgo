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

// MaxArgs is the fixed V1 scalar argument capacity. It covers the uintptr-only
// llgo.syscall families used by POSIX file and socket paths. Wider or typed
// foreign signatures fail closed before submission.
const MaxArgs = 6

// Result is the pointer-free result copied into a WorkerOperationSource
// payload before publication.
type Result struct {
	R1    uintptr
	R2    uintptr
	Errno uintptr
}
