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
	"sync"

	"github.com/goplus/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

// CoroPlanObserver observes the immutable, compilation-scoped coroutine plan
// immediately before cl processes a package from source. It is report-only:
// installing an observer does not enable coroutine lowering or change LLVM IR.
// The observer is not called for a package whose compiled archive came from
// the build cache. Observers must treat both arguments as read-only.
type CoroPlanObserver func(pkg *ssa.Package, plan *coro.SSAPlan)

// Compilation contains immutable inputs shared by every package compiled as
// part of one frontend compilation. Pass it by pointer and do not copy it after
// first use. A CoroPlan remains report-only unless EnableCoroEntryResolution is
// explicitly set. Functions materialized after analysis still fail closed at
// their first symbol resolution; a later slice will establish the complete
// effective emission universe before codegen.
type Compilation struct {
	CoroPlan                  *coro.SSAPlan
	CoroPlanObserver          CoroPlanObserver
	EnableCoroEntryResolution bool
	// EnableCoroPhysicalABI permits the conservative leaf-only coroutine ABI
	// lowering implemented by the current experimental slice. It requires entry
	// resolution and does not enable await, dispatch, roots, or a scheduler.
	EnableCoroPhysicalABI bool

	// EmissionUniverse is the immutable, compilation-scoped set of exact SSA
	// functions that cl may resolve while emitting this compilation. Active
	// coroutine entry resolution requires the universe to have been prepared
	// before any package enters LLVM codegen.
	EmissionUniverse *EmissionUniverse

	coroPreflight    sync.Once
	coroPreflightErr error
}

// PackageOptions contains inputs that vary for each package invocation.
type PackageOptions struct {
	Compilation *Compilation

	// CacheHit means cl is rebuilding frontend type registrations for an
	// already-compiled archive. Report-only plans are not installed in that cl
	// context. Active coroutine entry resolution rejects cache registration
	// until its plan digest is part of the archive fingerprint.
	CacheHit bool
}
