//go:build tinygo.wasm && !(js && wasm) && !wasip1 && !wasi

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package syscall

// Layout-only Unix frontends retain their source-level creation flags. HostOp
// descriptors carry the nonblocking/close-on-exec semantics intrinsically.
const hostSocketCreationFlags = SOCK_NONBLOCK | SOCK_CLOEXEC
