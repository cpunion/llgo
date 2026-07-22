//go:build darwin && !go1.26

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

package syscall

import (
	c "github.com/goplus/llgo/runtime/internal/clite"
	cliteos "github.com/goplus/llgo/runtime/internal/clite/os"
)

// Go 1.26 has fixed libc target declarations that let coroutine lowering move
// fcntl to the worker executor. Earlier standard libraries call runtime.fcntl
// directly and need only this private compatibility bridge. Keep their
// existing synchronous ABI instead of importing the Go 1.26 syscall catalog.
func llgoRuntimeFcntl(fd, cmd, arg int32) (result, errno int32) {
	r := cliteos.Fcntl(c.Int(fd), c.Int(cmd), c.Int(arg))
	if r == -1 {
		return -1, int32(cliteos.Errno())
	}
	return int32(r), 0
}
