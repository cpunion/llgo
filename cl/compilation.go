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

package cl

import (
	"github.com/goplus/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

// CoroPlanObserver observes the immutable, compilation-scoped coroutine plan
// immediately before cl processes a package from source. It is report-only:
// installing an observer does not enable coroutine lowering or change LLVM IR.
// The observer is not called for a package whose compiled archive came from
// the build cache. Observers must treat both arguments as read-only.
type CoroPlanObserver func(pkg *ssa.Package, plan *coro.SSAPlan)

// Compilation contains inputs shared by every package compiled as part of one
// frontend compilation. CoroPlan remains report-only until coroutine lowering
// is implemented.
type Compilation struct {
	CoroPlan         *coro.SSAPlan
	CoroPlanObserver CoroPlanObserver
}

// PackageOptions contains inputs that vary for each package invocation.
type PackageOptions struct {
	Compilation *Compilation

	// CacheHit means cl is rebuilding frontend type registrations for an
	// already-compiled archive. Such an invocation must not report or perform
	// coroutine lowering; Compilation is not installed in its cl context.
	CacheHit bool
}
