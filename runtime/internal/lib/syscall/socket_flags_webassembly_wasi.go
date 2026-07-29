//go:build tinygo.wasm && (wasip1 || wasi)

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package syscall

// Go's WASI Preview 1 syscall surface has no POSIX socket-creation flags.
// Preview 1 also has no standard socket creation/connect HostOp backend; the
// command reactor reports ENOSYS without making up Linux flag values.
const hostSocketCreationFlags = 0
