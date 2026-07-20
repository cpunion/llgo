//go:build !llgo_coro && !baremetal

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
	"github.com/goplus/llgo/runtime/internal/clite/libuv"
	cliteos "github.com/goplus/llgo/runtime/internal/clite/os"
)

func timerDebug() bool {
	timerDebugOnce.Do(func() {
		timerDebugEnabled = cliteos.Getenv(c.AllocaCStr("LLGO_TIMER_DEBUG")) != nil
	})
	return timerDebugEnabled
}

func timerDebugLoop(label string, loop *libuv.Loop) {
	if timerDebug() {
		c.Fprintf(c.Stderr, c.Str("timer: %s=%p\n"), c.AllocaCStr(label), loop)
	}
}

func timerDebugUint(label string, value uintptr) {
	if timerDebug() {
		c.Fprintf(c.Stderr, c.Str("timer: %s=%u\n"), c.AllocaCStr(label), c.Uint(value))
	}
}

func timerDebugInt(label string, value int) {
	if timerDebug() {
		c.Fprintf(c.Stderr, c.Str("timer: %s=%d\n"), c.AllocaCStr(label), c.Int(value))
	}
}

func timerDebugMsg(label string) {
	if timerDebug() {
		c.Fprintf(c.Stderr, c.Str("timer: %s\n"), c.AllocaCStr(label))
	}
}
