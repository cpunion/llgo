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
	"os"
	"runtime/debug"
)

func init() {
	// Go's GC budget excludes LLVM's allocations. On Darwin, scavenged Go
	// pages can also stay resident after FreeOSMemory, overlapping the fresh
	// backend's working set. Bound the CLI's temporary Go heap growth before
	// loading/planning a large program. An explicit GOGC remains authoritative;
	// do not change the environment inherited by Go tools or generated programs.
	if _, explicit := os.LookupEnv("GOGC"); !explicit {
		debug.SetGCPercent(20)
	}
}
