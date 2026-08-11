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

import c "github.com/goplus/llgo/runtime/internal/clite"

// releaseGAndCheckDeadlock is the sole last-goroutine decision shared by the
// pthread and stackless task lifecycles. Main marks its exit before releasing
// its own context, so the final goroutine observes both facts atomically.
func releaseGAndCheckDeadlock() {
	remaining, mainExited := releaseG()
	if remaining == 0 && mainExited {
		fatal("no goroutines (main called runtime.Goexit) - deadlock!")
		c.Exit(2)
	}
}
