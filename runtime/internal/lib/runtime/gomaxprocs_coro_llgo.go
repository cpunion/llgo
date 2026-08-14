//go:build llgo && llgo_coro

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

import llruntime "github.com/goplus/llgo/runtime/internal/runtime"

func GOMAXPROCS(n int) int {
	previous := llruntime.CoroGOMAXPROCS(n)
	if n > 0 && previous != n {
		// The logical quota changes synchronously above. One explicit scheduler
		// boundary makes the next bounded run slice observe its placement policy;
		// unrelated channel and timer actions therefore need no epoch poll.
		coroSchedulerYield()
	}
	return previous
}
