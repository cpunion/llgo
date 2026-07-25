//go:build llgo && llgo_coro && (wasm || tinygo.wasm) && !baremetal && !coro_runtime_adapter_test

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

package net

import (
	"context"
	"os"
	"runtime"
	"syscall"
)

// connect initializes the host poll descriptor before submitting the complete
// HostOp. That reverses the POSIX EINPROGRESS order deliberately: the host
// operation itself is the asynchronous wait, so its exact write-lane
// OperationID must exist before a context deadline or cancellation can evict
// it. The public net API and its context race semantics remain synchronous.
func (fd *netFD) connect(ctx context.Context, la, ra syscall.Sockaddr) (rsa syscall.Sockaddr, ret error) {
	_ = la
	select {
	case <-ctx.Done():
		return nil, mapErr(ctx.Err())
	default:
	}
	if err := fd.pfd.Init(fd.net, true); err != nil {
		return nil, err
	}

	ctxDone := ctx.Done()
	if ctxDone != nil {
		if deadline, hasDeadline := ctx.Deadline(); hasDeadline {
			_ = fd.pfd.SetWriteDeadline(deadline)
			defer fd.pfd.SetWriteDeadline(noDeadline)
		}
		testHookCanceledDial := testHookCanceledDial
		stop := context.AfterFunc(ctx, func() {
			_ = fd.pfd.SetWriteDeadline(aLongTimeAgo)
			testHookCanceledDial()
		})
		defer func() {
			if !stop() && ret == nil {
				ret = mapErr(ctx.Err())
			}
		}()
	}

	if err := fd.pfd.Connect(ra); err != nil {
		select {
		case <-ctxDone:
			return nil, mapErr(ctx.Err())
		default:
		}
		return nil, os.NewSyscallError("connect", err)
	}
	select {
	case <-ctxDone:
		return nil, mapErr(ctx.Err())
	default:
	}
	runtime.KeepAlive(fd)
	return nil, nil
}
