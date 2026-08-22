//go:build !windows

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

// Package thread exposes the hosted runtime's native thread and TLS backend.
package thread

import (
	c "github.com/xgo-dev/llgo/runtime/internal/clite"
	"github.com/xgo-dev/llgo/runtime/internal/clite/pthread"
)

type (
	RoutineFunc   = pthread.RoutineFunc
	KeyDestructor = pthread.KeyDestructor
	Key           = pthread.Key
)

// CreateDetached starts a detached host thread.
func CreateDetached(stackSize uintptr, routine RoutineFunc, arg c.Pointer) c.Int {
	var attr pthread.Attr
	if ret := attr.Init(); ret != 0 {
		return ret
	}
	if ret := attr.SetDetached(pthread.CreateDetached); ret != 0 {
		_ = attr.Destroy()
		return ret
	}
	if stackSize != 0 {
		if ret := attr.SetStackSize(stackSize); ret != 0 {
			_ = attr.Destroy()
			return ret
		}
	}
	var id pthread.Thread
	ret := pthread.Create(&id, &attr, routine, arg)
	// Once Create succeeds, arg belongs to the detached thread. A destroy
	// failure cannot be reported as creation failure without freeing live data.
	_ = attr.Destroy()
	return ret
}

func Exit() {
	pthread.Exit(nil)
}
