//go:build windows

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

import (
	_ "unsafe"

	c "github.com/xgo-dev/llgo/runtime/internal/clite"
	ctime "github.com/xgo-dev/llgo/runtime/internal/clite/time"
)

const (
	LLGoFiles   = "_wrap/sync_windows.c"
	LLGoPackage = "link"
)

const (
	PTHREAD_MUTEX_NORMAL     = 0
	PTHREAD_MUTEX_ERRORCHECK = 1
	PTHREAD_MUTEX_RECURSIVE  = 2
	PTHREAD_MUTEX_DEFAULT    = PTHREAD_MUTEX_NORMAL
)

const (
	errInvalid = c.Int(22)
)

// Once has the layout of Windows INIT_ONCE. Its zero value is ready for use.
type Once struct {
	state uintptr
}

//go:linkname winOnce C.llgo_win_once
func winOnce(once *Once, f *func()) c.Int

//export llgo_win_once_invoke
func llgo_win_once_invoke(f *func()) {
	(*f)()
}

func (o *Once) Do(f func()) c.Int {
	return winOnce(o, &f)
}

type MutexType c.Int

const (
	MUTEX_NORMAL     MutexType = PTHREAD_MUTEX_NORMAL
	MUTEX_ERRORCHECK MutexType = PTHREAD_MUTEX_ERRORCHECK
	MUTEX_RECURSIVE  MutexType = PTHREAD_MUTEX_RECURSIVE
	MUTEX_DEFAULT    MutexType = PTHREAD_MUTEX_DEFAULT
)

// MutexAttr keeps the portable API honest: SRW locks are neither recursive
// nor error-checking, so unsupported pthread attributes fail at Init.
type MutexAttr struct {
	typ MutexType
}

func (a *MutexAttr) Init(_ *MutexAttr) c.Int {
	a.typ = MUTEX_DEFAULT
	return 0
}

func (a *MutexAttr) Destroy() {}

func (a *MutexAttr) SetType(typ MutexType) c.Int {
	switch typ {
	case MUTEX_NORMAL:
		a.typ = typ
		return 0
	default:
		return errInvalid
	}
}

// Mutex has the layout of Windows SRWLOCK. Its zero value is ready for use.
type Mutex struct {
	state uintptr
}

//go:linkname winMutexLock C.llgo_win_mutex_lock
func winMutexLock(m *Mutex)

//go:linkname winMutexUnlock C.llgo_win_mutex_unlock
func winMutexUnlock(m *Mutex)

//go:linkname winMutexTryLock C.llgo_win_mutex_trylock
func winMutexTryLock(m *Mutex) c.Int

func (m *Mutex) Init(attr *MutexAttr) c.Int {
	if attr != nil && attr.typ != MUTEX_NORMAL && attr.typ != MUTEX_DEFAULT {
		return errInvalid
	}
	m.state = 0
	return 0
}

func (m *Mutex) Destroy() {}

func (m *Mutex) TryLock() c.Int {
	return winMutexTryLock(m)
}

func (m *Mutex) Lock() {
	winMutexLock(m)
}

func (m *Mutex) Unlock() {
	winMutexUnlock(m)
}

type RWLockAttr struct{}

func (a *RWLockAttr) Init(_ *RWLockAttr) c.Int { return 0 }

func (a *RWLockAttr) Destroy() {}

func (a *RWLockAttr) SetPShared(pshared c.Int) c.Int {
	if pshared != 0 {
		return errInvalid
	}
	return 0
}

func (a *RWLockAttr) GetPShared(pshared *c.Int) c.Int {
	if pshared == nil {
		return errInvalid
	}
	*pshared = 0
	return 0
}

// RWLock has the layout of Windows SRWLOCK. Its zero value is ready for use.
type RWLock struct {
	state uintptr
}

//go:linkname winRWLockRLock C.llgo_win_rwlock_rlock
func winRWLockRLock(rw *RWLock)

//go:linkname winRWLockTryRLock C.llgo_win_rwlock_tryrlock
func winRWLockTryRLock(rw *RWLock) c.Int

//go:linkname winRWLockRUnlock C.llgo_win_rwlock_runlock
func winRWLockRUnlock(rw *RWLock)

//go:linkname winRWLockLock C.llgo_win_rwlock_lock
func winRWLockLock(rw *RWLock)

//go:linkname winRWLockTryLock C.llgo_win_rwlock_trylock
func winRWLockTryLock(rw *RWLock) c.Int

//go:linkname winRWLockUnlock C.llgo_win_rwlock_unlock
func winRWLockUnlock(rw *RWLock)

func (rw *RWLock) Init(_ *RWLockAttr) c.Int {
	rw.state = 0
	return 0
}

func (rw *RWLock) Destroy() {}

func (rw *RWLock) RLock() {
	winRWLockRLock(rw)
}

func (rw *RWLock) TryRLock() c.Int {
	return winRWLockTryRLock(rw)
}

func (rw *RWLock) RUnlock() {
	winRWLockRUnlock(rw)
}

func (rw *RWLock) Lock() {
	winRWLockLock(rw)
}

func (rw *RWLock) TryLock() c.Int {
	return winRWLockTryLock(rw)
}

func (rw *RWLock) Unlock() {
	winRWLockUnlock(rw)
}

type CondAttr struct{}

func (a *CondAttr) Init(_ *CondAttr) c.Int { return 0 }

func (a *CondAttr) Destroy() {}

// Cond has the layout of Windows CONDITION_VARIABLE. Its zero value is ready
// for use with a Mutex backed by an exclusive SRW lock.
type Cond struct {
	state uintptr
}

//go:linkname winCondSignal C.llgo_win_cond_signal
func winCondSignal(cond *Cond) c.Int

//go:linkname winCondBroadcast C.llgo_win_cond_broadcast
func winCondBroadcast(cond *Cond) c.Int

//go:linkname winCondWait C.llgo_win_cond_wait
func winCondWait(cond *Cond, m *Mutex) c.Int

//go:linkname winCondTimedWait C.llgo_win_cond_timedwait
func winCondTimedWait(cond *Cond, m *Mutex, abstime *ctime.Timespec) c.Int

func (cond *Cond) Init(_ *CondAttr) c.Int {
	cond.state = 0
	return 0
}

func (cond *Cond) Destroy() {}

func (cond *Cond) Signal() c.Int {
	return winCondSignal(cond)
}

func (cond *Cond) Broadcast() c.Int {
	return winCondBroadcast(cond)
}

func (cond *Cond) Wait(m *Mutex) c.Int {
	return winCondWait(cond, m)
}

func (cond *Cond) TimedWait(m *Mutex, abstime *ctime.Timespec) c.Int {
	return winCondTimedWait(cond, m, abstime)
}
