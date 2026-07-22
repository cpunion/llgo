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

// CoroFrameRetentionParkABIV2 is the sole stackless frame-retention identity.
// A generic llgo.coroPark state is frame-owned only when its exact prepare call
// has a frozen executor-safe, borrow-until-return callable contract. Event
// source symbols never participate in this compiler profile.
const CoroFrameRetentionParkABIV2 = coro.FrameRetentionParkABIV2

const (
	CoroProfileNone      = coro.RuntimeProfileNone
	CoroProfileStackless = coro.RuntimeProfileStackless
)

func CoroNativeTargetCapabilities() coro.TargetCapabilities {
	return coro.NewTargetCapabilities(true, true)
}

// Compilation contains immutable inputs shared by every package compiled as
// part of one frontend compilation. Pass it by pointer and do not copy it after
// first use. A CoroPlan remains report-only unless CoroProfileStackless is
// selected. The prepared emission universe freezes every function that
// codegen may materialize, and any later out-of-universe lookup fails closed at
// its first symbol resolution.
type Compilation struct {
	CoroPlan               *coro.SSAPlan
	CoroPlanObserver       CoroPlanObserver
	CoroProfile            coro.RuntimeProfile
	CoroTargetCapabilities coro.TargetCapabilities
	// CoroPlanDigest and the ABI identities are populated by the build driver
	// after whole-program analysis and participate in every package archive
	// fingerprint. They are required before an active compilation may register
	// a cache hit.
	CoroPlanDigest          string
	CoroLoweringFacts       coro.LoweringFacts
	CoroLoweringFactsDigest string
	CoroABI                 string
	SchedulerABI            string
	PanicABI                string
	FuncRepABI              string
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
	coroFactsValidation      sync.Once
	coroFactsValidationErr   error
	coroClosedInterfacePlain *coroClosedInterfacePlainPlan
	coroManagedInterface     *coroManagedInterfaceDispatchPlan
}

// CoroProfileActive reports whether this compilation owns the complete
// stackless architecture.
func (c *Compilation) CoroProfileActive() bool {
	return c != nil && c.CoroProfile.Active()
}

func (c *Compilation) CoroEntryResolutionActive() bool {
	return c.CoroProfileActive()
}

func (c *Compilation) CoroPhysicalABIActive() bool {
	return c.CoroProfileActive()
}

func (c *Compilation) CoroChildAwaitActive() bool {
	return c.CoroProfileActive()
}

func (c *Compilation) CoroPlainDispatchActive() bool {
	return c.CoroProfileActive()
}

func (c *Compilation) CoroClosedStaticSpawnActive() bool {
	return c.CoroProfileActive()
}

func (c *Compilation) CoroProgramBootstrapActive() bool {
	return c.CoroProfileActive()
}

func (c *Compilation) CoroChannelActive() bool {
	return c.CoroProfileActive()
}

func (c *Compilation) CoroExplicitStatusActive() bool {
	return c.CoroProfileActive()
}

func (c *Compilation) CoroWorkerActive() bool {
	return c != nil && c.CoroProfile.Active() && c.CoroTargetCapabilities.Worker()
}

func (c *Compilation) validateCoroProfile() error {
	if c == nil {
		return nil
	}
	if !c.CoroProfile.Valid() {
		return fmt.Errorf("unknown coroutine runtime profile %d", c.CoroProfile)
	}
	if !c.CoroTargetCapabilities.Valid() {
		return fmt.Errorf("invalid coroutine target capability set %d", c.CoroTargetCapabilities)
	}
	if !c.CoroProfile.Active() && c.CoroTargetCapabilities != 0 {
		return fmt.Errorf("coroutine target capabilities require the stackless runtime profile")
	}
	return nil
}

func (c *Compilation) validateCoroCacheIdentity() error {
	if c == nil {
		return fmt.Errorf("coroutine cache registration requires a compilation")
	}
	decoded, err := hex.DecodeString(c.CoroPlanDigest)
	if err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != c.CoroPlanDigest {
		return fmt.Errorf("coroutine cache registration requires a canonical SHA-256 CoroPlanDigest")
	}
	if err := c.validateCoroLoweringFactsIdentity(); err != nil {
		return err
	}
	return c.validateCoroABIIdentity(true)
}

func (c *Compilation) validateCoroLoweringFactsIdentity() error {
	if c == nil {
		return fmt.Errorf("coroutine lowering-facts validation requires a compilation")
	}
	c.coroFactsValidation.Do(func() {
		if c.CoroLoweringFacts.Schema != coro.LoweringFactsSchema {
			c.coroFactsValidationErr = fmt.Errorf("coroutine cache registration lowering-facts schema %q, want %q", c.CoroLoweringFacts.Schema, coro.LoweringFactsSchema)
			return
		}
		decoded, err := hex.DecodeString(c.CoroLoweringFactsDigest)
		if err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != c.CoroLoweringFactsDigest {
			c.coroFactsValidationErr = fmt.Errorf("coroutine cache registration requires a canonical SHA-256 lowering-facts digest")
			return
		}
		digest, err := c.CoroLoweringFacts.Digest()
		if err != nil {
			c.coroFactsValidationErr = fmt.Errorf("coroutine cache registration validates lowering facts: %w", err)
			return
		}
		if digest != c.CoroLoweringFactsDigest {
			c.coroFactsValidationErr = fmt.Errorf("coroutine cache registration lowering-facts digest mismatch: have %q, want %q", digest, c.CoroLoweringFactsDigest)
		}
	})
	return c.coroFactsValidationErr
}

func (c *Compilation) validateCoroABIIdentity(required bool) error {
	if c == nil {
		return fmt.Errorf("coroutine ABI validation requires a compilation")
	}
	if err := c.validateCoroProfile(); err != nil {
		return err
	}
	if !c.CoroProfile.Active() {
		if !required && c.CoroABI == "" && c.SchedulerABI == "" && c.PanicABI == "" && c.FuncRepABI == "" && c.CoroFrameRetentionABI == "" {
			return nil
		}
		return fmt.Errorf("coroutine ABI identity requires the stackless runtime profile")
	}
	return c.validateStacklessCoroABIIdentity(required)
}

func (c *Compilation) validateStacklessCoroABIIdentity(required bool) error {
	if err := c.validateCoroProfile(); err != nil {
		return err
	}
	wantScheduler := coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	if c.CoroWorkerActive() {
		wantScheduler = coro.SchedulerProgramBootstrapChannelWorkerClosedStaticSpawnABIV0
	}
	switch c.CoroFrameRetentionABI {
	case "", CoroFrameRetentionParkABIV2:
	default:
		return fmt.Errorf("unknown coroutine frame-retention ABI %q", c.CoroFrameRetentionABI)
	}
	checks := []struct {
		name string
		got  string
		want string
	}{
		{"coroutine", c.CoroABI, coro.PhysicalABIV1},
		{"scheduler", c.SchedulerABI, wantScheduler},
		{"panic", c.PanicABI, coro.PanicExplicitStatusABIV0},
		{"function representation", c.FuncRepABI, coro.FuncRepABIV1},
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
