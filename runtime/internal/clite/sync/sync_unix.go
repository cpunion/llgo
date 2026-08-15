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

// Package sync exposes the hosted runtime's native synchronization backend.
package sync

import psync "github.com/xgo-dev/llgo/runtime/internal/clite/pthread/sync"

const (
	PTHREAD_MUTEX_NORMAL     = psync.PTHREAD_MUTEX_NORMAL
	PTHREAD_MUTEX_ERRORCHECK = psync.PTHREAD_MUTEX_ERRORCHECK
	PTHREAD_MUTEX_RECURSIVE  = psync.PTHREAD_MUTEX_RECURSIVE
	PTHREAD_MUTEX_DEFAULT    = psync.PTHREAD_MUTEX_DEFAULT
)

type (
	Once       = psync.Once
	MutexType  = psync.MutexType
	MutexAttr  = psync.MutexAttr
	Mutex      = psync.Mutex
	RWLockAttr = psync.RWLockAttr
	RWLock     = psync.RWLock
	CondAttr   = psync.CondAttr
	Cond       = psync.Cond
)

const (
	MUTEX_NORMAL     = psync.MUTEX_NORMAL
	MUTEX_ERRORCHECK = psync.MUTEX_ERRORCHECK
	MUTEX_RECURSIVE  = psync.MUTEX_RECURSIVE
	MUTEX_DEFAULT    = psync.MUTEX_DEFAULT
)

var OnceInit = psync.OnceInit
