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

package coro

import (
	"fmt"
	"strings"
)

// ExecFlags are execution constraints that are deliberately independent from
// suspend effects. In particular, a function can be ThreadAffine without being
// suspendable, or MayUnwind without requiring a coroutine primary.
type ExecFlags uint16

const (
	BlockForeign ExecFlags = 1 << iota
	ThreadAffine
	IRQUnsafe
	NeedsPreempt
	MayUnwind
	NeedsCleanupFrame
	NoReturn
	PanicOnly
	// OpaqueExec marks an open managed target whose execution constraints are
	// unavailable. Verifiers must reject it in restricted contexts unless a
	// compatible external summary replaces it.
	OpaqueExec
)

const validExecFlags = BlockForeign | ThreadAffine | IRQUnsafe | NeedsPreempt |
	MayUnwind | NeedsCleanupFrame | NoReturn | PanicOnly | OpaqueExec

// propagatedExecFlags are conservative "may" constraints inherited by a
// managed caller. Control-flow guarantees and local lowering requirements are
// intentionally excluded.
const propagatedExecFlags = ThreadAffine | IRQUnsafe | MayUnwind | OpaqueExec

var execFlagNames = [...]struct {
	bit  ExecFlags
	name string
}{
	{BlockForeign, "block-foreign"},
	{ThreadAffine, "thread-affine"},
	{IRQUnsafe, "irq-unsafe"},
	{NeedsPreempt, "needs-preempt"},
	{MayUnwind, "may-unwind"},
	{NeedsCleanupFrame, "needs-cleanup-frame"},
	{NoReturn, "no-return"},
	{PanicOnly, "panic-only"},
	{OpaqueExec, "opaque"},
}

func (f ExecFlags) Validate() error {
	if unknown := f &^ validExecFlags; unknown != 0 {
		return fmt.Errorf("coro: unknown execution flag bits %#x", uint16(unknown))
	}
	return nil
}

func (f ExecFlags) Contains(other ExecFlags) bool { return f&other == other }

func (f ExecFlags) Join(other ExecFlags) ExecFlags { return f | other }

// IsOpaque reports that the execution constraints came from an open target
// without a compatible summary.
func (f ExecFlags) IsOpaque() bool { return f&OpaqueExec != 0 }

func (f ExecFlags) String() string {
	text, err := f.MarshalText()
	if err != nil {
		return fmt.Sprintf("exec-flags(%#x)", uint16(f))
	}
	return string(text)
}

func (f ExecFlags) MarshalText() ([]byte, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}
	if f == 0 {
		return []byte("none"), nil
	}
	parts := make([]string, 0, len(execFlagNames))
	for _, item := range execFlagNames {
		if f&item.bit != 0 {
			parts = append(parts, item.name)
		}
	}
	return []byte(strings.Join(parts, ",")), nil
}

func (f *ExecFlags) UnmarshalText(text []byte) error {
	if f == nil {
		return fmt.Errorf("coro: cannot unmarshal execution flags into nil receiver")
	}
	s := strings.TrimSpace(string(text))
	if s == "none" {
		*f = 0
		return nil
	}
	if s == "" {
		return fmt.Errorf("coro: empty execution flags")
	}
	var ret ExecFlags
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		matched := false
		for _, item := range execFlagNames {
			if part == item.name {
				ret |= item.bit
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("coro: unknown execution flag %q", part)
		}
	}
	*f = ret
	return nil
}

// Demand records which entry capabilities are required for a function. It is
// a bitset rather than a body-emission instruction: BothDemand never
// authorizes cloning a full source body. Per-callsite execution mode is a
// separate part of the eventual CallPlan.
type Demand uint8

const NoDemand Demand = 0

const (
	SyncDemand Demand = 1 << iota
	AsyncDemand
	BothDemand = SyncDemand | AsyncDemand
)

func (d Demand) Validate() error {
	if unknown := d &^ BothDemand; unknown != 0 {
		return fmt.Errorf("coro: unknown demand bits %#x", uint8(unknown))
	}
	return nil
}

func (d Demand) Contains(other Demand) bool { return d&other == other }

func (d Demand) Join(other Demand) Demand { return d | other }

func (d Demand) String() string {
	switch d {
	case NoDemand:
		return "none"
	case SyncDemand:
		return "sync"
	case AsyncDemand:
		return "async"
	case BothDemand:
		return "both"
	default:
		return fmt.Sprintf("demand(%#x)", uint8(d))
	}
}

func (d Demand) MarshalText() ([]byte, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}
	return []byte(d.String()), nil
}

func (d *Demand) UnmarshalText(text []byte) error {
	if d == nil {
		return fmt.Errorf("coro: cannot unmarshal demand into nil receiver")
	}
	switch strings.TrimSpace(string(text)) {
	case "none":
		*d = NoDemand
	case "sync":
		*d = SyncDemand
	case "async":
		*d = AsyncDemand
	case "both":
		*d = BothDemand
	default:
		return fmt.Errorf("coro: unknown demand %q", text)
	}
	return nil
}

// FuncRep is the canonical representation required for a function value.
// Dispatch is reserved for values that cross an open or dynamically typed
// boundary; ordinary direct calls retain a single plain or coroutine entry.
type FuncRep uint8

const (
	DirectPlain FuncRep = iota
	DirectCoro
	Dispatch
)

func (r FuncRep) Validate() error {
	if r > Dispatch {
		return fmt.Errorf("coro: invalid function representation %d", uint8(r))
	}
	return nil
}

func (r FuncRep) String() string {
	switch r {
	case DirectPlain:
		return "direct-plain"
	case DirectCoro:
		return "direct-coro"
	case Dispatch:
		return "dispatch"
	default:
		return fmt.Sprintf("func-rep(%d)", uint8(r))
	}
}

func (r FuncRep) MarshalText() ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	return []byte(r.String()), nil
}

func (r *FuncRep) UnmarshalText(text []byte) error {
	if r == nil {
		return fmt.Errorf("coro: cannot unmarshal function representation into nil receiver")
	}
	switch strings.TrimSpace(string(text)) {
	case "direct-plain":
		*r = DirectPlain
	case "direct-coro":
		*r = DirectCoro
	case "dispatch":
		*r = Dispatch
	default:
		return fmt.Errorf("coro: unknown function representation %q", text)
	}
	return nil
}

// BodyEmission is the physical body selected for the current closed-world
// plan. It is deliberately distinct from Demand, FuncRep, and PrimaryKind:
// Demand records required entry capabilities, FuncRep records the value ABI,
// and PrimaryKind records the one logical implementation ABI. In particular,
// EmitNone does not change an effectful function's PrimaryCoroutine identity;
// it only says that the current plan has no reachable consumer and therefore
// must not materialize that body.
type BodyEmission uint8

const (
	EmitNone BodyEmission = iota
	EmitPlain
	EmitCoroutine
	EmitExternal
)

// Validate reports whether e names a defined physical-emission choice.
func (e BodyEmission) Validate() error {
	if e > EmitExternal {
		return fmt.Errorf("coro: invalid body emission %d", uint8(e))
	}
	return nil
}

func (e BodyEmission) String() string {
	switch e {
	case EmitNone:
		return "none"
	case EmitPlain:
		return "plain"
	case EmitCoroutine:
		return "coroutine"
	case EmitExternal:
		return "external"
	default:
		return fmt.Sprintf("body-emission(%d)", uint8(e))
	}
}

// MarshalText implements encoding.TextMarshaler for stable summaries.
func (e BodyEmission) MarshalText() ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	return []byte(e.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler for stable summaries.
func (e *BodyEmission) UnmarshalText(text []byte) error {
	if e == nil {
		return fmt.Errorf("coro: cannot unmarshal body emission into nil receiver")
	}
	switch strings.TrimSpace(string(text)) {
	case "none":
		*e = EmitNone
	case "plain":
		*e = EmitPlain
	case "coroutine":
		*e = EmitCoroutine
	case "external":
		*e = EmitExternal
	default:
		return fmt.Errorf("coro: unknown body emission %q", text)
	}
	return nil
}
