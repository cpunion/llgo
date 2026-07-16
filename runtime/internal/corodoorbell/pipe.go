//go:build (darwin || linux) && !baremetal

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

// Package corodoorbell provides the native retained wake primitive for the
// stackless coroutine executor backend. It follows the same pipe/fcntl/poll
// substrate as the minimal runtime poller without sharing its global pipe or
// latch. It has no goroutine, pthread, libuv, or garbage-collector dependency.
package corodoorbell

const (
	invalidFD          int32 = -1
	physicalPollMaxMS  int32 = 1000
	physicalPollIn     int16 = 0x0001
	physicalPollError  int16 = 0x0008
	physicalPollHangup int16 = 0x0010
	physicalPollBadFD  int16 = 0x0020
)

type Pipe struct {
	readFD  int32
	writeFD int32
	pending uint32
	open    uint32
}

// Open is owner-only and creates one nonblocking, close-on-exec pipe. Both
// properties are part of the correctness contract: a blocking producer write
// could deadlock an ordinary producer-thread completion ingress, while
// inherited descriptors would prevent bounded lifecycle ownership across exec.
func (pipe *Pipe) Open() bool {
	if pipe == nil || nativeAtomicLoad(&pipe.open) != 0 {
		return false
	}
	fds, ok := nativePipeOpen()
	if !ok {
		return false
	}
	pipe.readFD = fds[0]
	pipe.writeFD = fds[1]
	nativeAtomicStore(&pipe.pending, 0)
	nativeAtomicStore(&pipe.open, 1)
	return true
}

// ReadFD is an owner-only diagnostic accessor. Producers must never retain or
// inspect descriptors; they use Ring while holding TargetIngress instead.
func (pipe *Pipe) ReadFD() (int32, bool) {
	if pipe == nil || nativeAtomicLoad(&pipe.open) != 1 || pipe.readFD < 0 {
		return invalidFD, false
	}
	return pipe.readFD, true
}

// Ring is the only producer-concurrent Pipe operation. It latches the wake
// before attempting the nonblocking write. A byte is retained across the
// CommitSleep-to-poll window. EAGAIN/EWOULDBLOCK is success because a full pipe
// is itself a retained wake; EINTR is retried. An unexpected write failure
// leaves pending set, and Wait's bounded poll pass rechecks that latch rather
// than sleeping forever.
func (pipe *Pipe) Ring() bool {
	if pipe == nil || nativeAtomicLoad(&pipe.open) != 1 || pipe.writeFD < 0 {
		return false
	}
	if nativeAtomicExchange(&pipe.pending, 1) != 0 {
		return true
	}
	var value byte
	for {
		written, errno := nativePipeWrite(pipe.writeFD, &value, 1)
		switch {
		case written == 1:
			return true
		case written < 0 && nativeErrInterrupted(errno):
			continue
		case written < 0 && nativeErrWouldBlock(errno):
			return true
		default:
			return false
		}
	}
}

// Drain is owner-only. It consumes every currently retained byte and clears
// the advisory latch. A producer racing the clear either leaves a byte,
// republishes pending, or both. Read retries EINTR and treats
// EAGAIN/EWOULDBLOCK as a complete drain.
func (pipe *Pipe) Drain() bool {
	if pipe == nil || nativeAtomicLoad(&pipe.open) != 1 || pipe.readFD < 0 {
		return false
	}
	nativeAtomicStore(&pipe.pending, 0)
	var buffer [64]byte
	for {
		read, errno := nativePipeRead(pipe.readFD, &buffer[0], uintptr(len(buffer)))
		switch {
		case read > 0:
			continue
		case read < 0 && nativeErrInterrupted(errno):
			continue
		case read < 0 && nativeErrWouldBlock(errno):
			return true
		default:
			// A zero read means EOF; any other error means the retained
			// descriptor can no longer satisfy the wake contract.
			return false
		}
	}
}

// Wait is owner-only and blocks the current native executor thread. It never
// creates a worker and never invokes a managed callback. The bounded physical
// poll timeout is only a fault-containment recheck for an unexpected failed
// write; ordinary wakes are pipe-driven and immediate.
func (pipe *Pipe) Wait() bool {
	if pipe == nil || nativeAtomicLoad(&pipe.open) != 1 || pipe.readFD < 0 {
		return false
	}
	for {
		woke, ok := pipe.WaitBounded(physicalPollMaxMS)
		if !ok {
			return false
		}
		if woke {
			return true
		}
	}
}

// WaitBounded is owner-only and performs one timeout-bounded retained wait.
// It retries EINTR, so a sustained signal storm can extend its wall-clock
// duration beyond timeoutMS. woke=false,ok=true is an ordinary timeout. Close
// uses this as a kernel scheduling point while joining a producer admitted
// before Seal; it never assumes that such a producer owes the closing executor
// a pipe byte.
func (pipe *Pipe) WaitBounded(timeoutMS int32) (woke, ok bool) {
	if pipe == nil || nativeAtomicLoad(&pipe.open) != 1 || pipe.readFD < 0 || timeoutMS < 0 {
		return false, false
	}
	if nativeAtomicExchange(&pipe.pending, 0) != 0 {
		drained := pipe.Drain()
		return drained, drained
	}
	for {
		// The test-only hook linearizes a producer after the pending recheck
		// and immediately before the real poll syscall. Production builds use
		// an empty implementation. This exact placement exercises the retained
		// CommitSleep-to-poll window without timing guesses.
		if nativeBeforePollHookEnabled && !nativeBeforePollHook() {
			return false, false
		}
		result, revents, errno := nativePipePoll(pipe.readFD, timeoutMS)
		switch {
		case result < 0 && nativeErrInterrupted(errno):
			continue
		case result < 0:
			return false, false
		case result == 0:
			return false, true
		case revents&physicalPollBadFD != 0:
			return false, false
		case revents&(physicalPollIn|physicalPollError|physicalPollHangup) != 0:
			drained := pipe.Drain()
			return drained, drained
		default:
			return false, false
		}
	}
}

// Close is owner-only and is called only after the owning TargetIngress has
// been sealed and strongly joined. It performs no retry after EINTR: POSIX
// close error state is platform-dependent and retrying could close a descriptor
// already reused by another owner. Both descriptors are invalidated before the
// calls.
func (pipe *Pipe) Close() bool {
	if pipe == nil || !nativeAtomicCompareAndSwap(&pipe.open, 1, 0) {
		return false
	}
	readFD, writeFD := pipe.readFD, pipe.writeFD
	pipe.readFD, pipe.writeFD = invalidFD, invalidFD
	nativeAtomicStore(&pipe.pending, 0)
	readOK := readFD >= 0 && nativePipeClose(readFD)
	writeOK := writeFD >= 0 && nativePipeClose(writeFD)
	return readOK && writeOK
}

func (pipe *Pipe) Closed() bool {
	return pipe != nil && nativeAtomicLoad(&pipe.open) == 0 && pipe.readFD == invalidFD && pipe.writeFD == invalidFD
}
