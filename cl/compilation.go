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
	"encoding/hex"
	"fmt"
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

// CoroFrameRetentionTimerABIV1 names the one current-frame pointer-retention
// contract implemented by the native scheduler timer owner. It is deliberately
// separate from //llgo:coro noblock: a nonblocking C call may still retain any
// pointer passed to it. This identity authorizes cl to prove only the exact
// prepare/park/retire transaction whose retained pointer dies before the
// current LLVM coroutine frame can complete.
const CoroFrameRetentionTimerABIV1 = coro.FrameRetentionTimerABIV1

// Compilation contains immutable inputs shared by every package compiled as
// part of one frontend compilation. Pass it by pointer and do not copy it after
// first use. A CoroPlan remains report-only unless EnableCoroEntryResolution is
// explicitly set. The prepared emission universe freezes every function that
// codegen may materialize, and any later out-of-universe lookup fails closed at
// its first symbol resolution.
type Compilation struct {
	CoroPlan                  *coro.SSAPlan
	CoroPlanObserver          CoroPlanObserver
	EnableCoroEntryResolution bool
	// CoroPlanDigest and the ABI identities are populated by the build driver
	// after whole-program analysis and participate in every package archive
	// fingerprint. They are required before an active compilation may register
	// a cache hit.
	CoroPlanDigest string
	CoroABI        string
	SchedulerABI   string
	PanicABI       string
	FuncRepABI     string
	// EnableCoroExplicitStatusPanicABI selects the target-wide explicit-status
	// panic identity. The first lowering slice accepts only exact cleanup-free
	// physical coroutine bodies whose explicit panic payload can outlive frame
	// destruction; every wider hidden-outcome or unwind shape remains fail-closed.
	EnableCoroExplicitStatusPanicABI bool
	// EnableCoroPhysicalABI permits the conservative leaf-only coroutine ABI
	// lowering implemented by the current experimental slice. It requires entry
	// resolution and does not by itself enable await, dispatch, roots, or a
	// scheduler.
	EnableCoroPhysicalABI bool
	// EnableCoroChildAwait permits the narrowly-scoped static child handoff ABI.
	// It requires the physical ABI and emits typed factories for explicit async
	// roots. A generated parent only publishes an initial-suspended child and
	// suspends itself; a matching scheduler owns every resume and destroy
	// operation.
	EnableCoroChildAwait bool
	// EnableCoroPlainDispatch permits the first descriptor/context function-value
	// ABI. Only a no-capture, non-suspending plain target at an ordinary scalar
	// call is accepted by this capability; every wider dynamic form remains an
	// unsupported preflight error.
	EnableCoroPlainDispatch bool
	// EnableCoroClosedStaticSpawn permits only the compilation-plan-certified
	// closed static spawn transaction. The physical parent G is passed to both
	// runtime hooks; no TLS lookup or indirect user callback is permitted.
	EnableCoroClosedStaticSpawn bool
	// EnableCoroProgramBootstrapRun selects the program-root scheduler ABI for
	// package identities. The factory itself lives in the uncached entry module,
	// but every linked archive must agree with the runtime driver contract.
	EnableCoroProgramBootstrapRun bool
	// EnableCoroChannel enables the exact single blocking send/receive lowering
	// on the runnable scheduler. It requires PhysicalABIV1 program bootstrap and
	// is independently fingerprinted from child-await, spawn, and timer support.
	EnableCoroChannel bool
	// EnableCoroWorker enables the bounded ForeignWait operation recipe used by
	// exact uintptr-only llgo.syscall sites. It requires the runnable scheduler;
	// the blocking foreign call executes only on a fixed native worker pool.
	EnableCoroWorker bool
	// CoroFrameRetentionABI selects one compiler/runtime-owned contract under
	// which x/tools Heap Allocs may be re-proved as current LLVM coroutine-frame
	// storage. The zero value preserves the ordinary managed-allocation rule.
	// Unknown identities and identities without runnable PhysicalABIV1 lowering
	// fail before LLVM code generation.
	CoroFrameRetentionABI string

	// EmissionUniverse is the immutable, compilation-scoped set of exact SSA
	// functions that cl may resolve while emitting this compilation. Active
	// coroutine entry resolution requires the universe to have been prepared
	// before any package enters LLVM codegen.
	EmissionUniverse *EmissionUniverse

	coroPreflight            sync.Once
	coroPreflightErr         error
	coroClosedInterfacePlain *coroClosedInterfacePlainPlan
}

func (c *Compilation) validateCoroCacheIdentity() error {
	if c == nil {
		return fmt.Errorf("coroutine cache registration requires a compilation")
	}
	decoded, err := hex.DecodeString(c.CoroPlanDigest)
	if err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != c.CoroPlanDigest {
		return fmt.Errorf("coroutine cache registration requires a canonical SHA-256 CoroPlanDigest")
	}
	return c.validateCoroABIIdentity(true)
}

func (c *Compilation) validateCoroABIIdentity(required bool) error {
	if c == nil {
		return fmt.Errorf("coroutine ABI validation requires a compilation")
	}
	wantCoroABI := coro.EntryResolutionABIV0
	if c.EnableCoroPhysicalABI {
		wantCoroABI = coro.PhysicalABIV0
	}
	if c.EnableCoroChildAwait {
		wantCoroABI = coro.PhysicalABIV1
	}
	wantSchedulerABI := coro.SchedulerNoneABIV0
	if c.EnableCoroChildAwait {
		wantSchedulerABI = coro.SchedulerChildAwaitABIV0
	}
	if c.EnableCoroChannel {
		if !c.EnableCoroChildAwait || !c.EnableCoroProgramBootstrapRun {
			return fmt.Errorf("coroutine channel lowering requires runnable PhysicalABIV1 program-bootstrap lowering")
		}
		wantSchedulerABI = coro.SchedulerProgramBootstrapChannelABIV0
	}
	if c.EnableCoroWorker {
		if !c.EnableCoroChildAwait || !c.EnableCoroProgramBootstrapRun {
			return fmt.Errorf("coroutine worker lowering requires runnable PhysicalABIV1 program-bootstrap lowering")
		}
		if c.EnableCoroChannel {
			wantSchedulerABI = coro.SchedulerProgramBootstrapChannelWorkerABIV0
		} else {
			wantSchedulerABI = coro.SchedulerProgramBootstrapWorkerABIV0
		}
	}
	if c.EnableCoroClosedStaticSpawn {
		if !c.EnableCoroChildAwait {
			return fmt.Errorf("coroutine closed static spawn requires child-await lowering")
		}
		if !c.EnableCoroProgramBootstrapRun {
			return fmt.Errorf("coroutine closed static spawn requires the runnable program-bootstrap v2 scheduler")
		}
		if c.EnableCoroChannel && c.EnableCoroWorker {
			wantSchedulerABI = coro.SchedulerProgramBootstrapChannelWorkerClosedStaticSpawnABIV0
		} else if c.EnableCoroChannel {
			wantSchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
		} else if c.EnableCoroWorker {
			wantSchedulerABI = coro.SchedulerProgramBootstrapWorkerClosedStaticSpawnABIV0
		} else {
			wantSchedulerABI = coro.SchedulerProgramBootstrapClosedStaticSpawnABIV0
		}
	} else if c.EnableCoroProgramBootstrapRun {
		if !c.EnableCoroChildAwait {
			return fmt.Errorf("coroutine program bootstrap runtime requires child-await lowering")
		}
		if !c.EnableCoroChannel && !c.EnableCoroWorker {
			wantSchedulerABI = coro.SchedulerProgramBootstrapABIV2
		}
	}
	if c.EnableCoroPlainDispatch && !c.EnableCoroEntryResolution {
		return fmt.Errorf("coroutine plain dispatch requires coroutine entry resolution")
	}
	if c.EnableCoroExplicitStatusPanicABI && !c.EnableCoroEntryResolution {
		return fmt.Errorf("coroutine explicit-status panic ABI requires coroutine entry resolution")
	}
	switch c.CoroFrameRetentionABI {
	case "":
	case CoroFrameRetentionTimerABIV1:
		if !c.EnableCoroEntryResolution || !c.EnableCoroPhysicalABI || !c.EnableCoroChildAwait || !c.EnableCoroProgramBootstrapRun {
			return fmt.Errorf("coroutine frame-retention ABI %q requires runnable PhysicalABIV1 program-bootstrap lowering", c.CoroFrameRetentionABI)
		}
	default:
		return fmt.Errorf("unknown coroutine frame-retention ABI %q", c.CoroFrameRetentionABI)
	}
	wantPanicABI := coro.PanicLegacyABIV0
	if c.EnableCoroExplicitStatusPanicABI {
		wantPanicABI = coro.PanicExplicitStatusABIV0
	}
	wantFuncRepABI := coro.FuncRepABIV0
	if c.EnableCoroPlainDispatch {
		wantFuncRepABI = coro.FuncRepABIV1
	}
	checks := []struct {
		name string
		got  string
		want string
	}{
		{"coroutine", c.CoroABI, wantCoroABI},
		{"scheduler", c.SchedulerABI, wantSchedulerABI},
		{"panic", c.PanicABI, wantPanicABI},
		{"function representation", c.FuncRepABI, wantFuncRepABI},
	}
	if !required {
		populated := false
		for _, check := range checks {
			populated = populated || check.got != ""
		}
		if !populated {
			return nil
		}
	}
	for _, check := range checks {
		if check.got != check.want {
			return fmt.Errorf("coroutine compilation %s ABI %q does not match %q", check.name, check.got, check.want)
		}
	}
	return nil
}

// PackageOptions contains inputs that vary for each package invocation.
type PackageOptions struct {
	Compilation *Compilation

	// CacheHit means cl is rebuilding frontend registrations and link-time
	// metadata for an already-compiled archive. The transient module is discarded
	// by the build driver. Report-only observers are skipped; active coroutine
	// entry resolution accepts the cache hit only after the driver has matched the
	// archive's canonical plan digest and ABI identity, and keeps that plan
	// installed so symbol and physical-ABI metadata match a source compilation.
	CacheHit bool
}
