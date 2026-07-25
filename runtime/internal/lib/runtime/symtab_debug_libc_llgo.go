//go:build !baremetal && !wasm && !tinygo.wasm

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
	c "github.com/goplus/llgo/runtime/internal/clite"
	cliteos "github.com/goplus/llgo/runtime/internal/clite/os"
	latomic "github.com/goplus/llgo/runtime/internal/lib/sync/atomic"
)

func runtimeFuncInfoDebugEnabled() bool {
	state := latomic.LoadUint32(&runtimeFuncInfoDebugState)
	if state == 0 {
		state = 1
		if p := cliteos.Getenv(c.AllocaCStr("LLGO_FUNCINFO_DEBUG")); p != nil {
			if v := c.GoString(p); v != "" && v != "0" {
				state = 2
			}
		}
		latomic.StoreUint32(&runtimeFuncInfoDebugState, state)
	}
	return state == 2
}
