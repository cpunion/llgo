//go:build js && wasm

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package syscall

// Go's js/wasm syscall surface exposes the portable socket base types but no
// POSIX creation flags. The host operation is asynchronous by construction,
// so there are no executor-blocking or inherited-descriptor bits to strip.
const hostSocketCreationFlags = 0
