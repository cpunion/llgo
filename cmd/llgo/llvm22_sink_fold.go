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

package main

import (
	"strings"

	"github.com/xgo-dev/llvm"
)

func init() {
	// LLVM 22's AArch64 sink-and-fold clears an address register's complete
	// use list for each folded memory operation. A large coroutine frame can
	// make this quadratic (issue9604b spends its entire codegen guard there).
	// Retain MachineSink and the requested optimization level; disable only
	// this AArch64 subfeature in the CLI's LLVM instance. Other LLVM versions
	// and external backends retain their own policy.
	if strings.HasPrefix(llvm.Version, "22.") {
		llvm.ParseCommandLineOptions([]string{"llgo", "-aarch64-enable-sink-fold=false"},
			"LLVM 22 AArch64 compile-time workaround")
	}
}
