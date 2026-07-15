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

// Package coro contains the target-independent compilation plan used by the
// LLVM coroutine backend. It deliberately does not depend on LLGo's LLVM
// builder: callers first analyze a complete call graph, then hand the resulting
// immutable plan to later lowering stages.
package coro

import (
	"fmt"
	"strings"
)

// Effect is the suspend-effect lattice for a function or call site.
//
// Effects are capabilities and therefore form a powerset lattice. NoSuspend is
// the bottom element (the zero value), while OpaqueSuspend is the top element.
// WaitHost implies WaitPlatform and is normalized accordingly.
type Effect uint16

const (
	YieldOnly Effect = 1 << iota
	AwaitStructured
	MayPark
	WaitPlatform
	WaitHost
	WaitForeign
	opaqueSuspendBit
)

const (
	// NoSuspend is the bottom of the effect lattice.
	NoSuspend Effect = 0

	knownSuspendEffects = YieldOnly | AwaitStructured | MayPark | WaitPlatform | WaitHost | WaitForeign
	validEffectBits     = knownSuspendEffects | opaqueSuspendBit

	// OpaqueSuspend is the top of the effect lattice. It is used when an open
	// managed call has no compatible summary.
	OpaqueSuspend Effect = knownSuspendEffects | opaqueSuspendBit
)

var effectNames = [...]struct {
	bit  Effect
	name string
}{
	{YieldOnly, "yield-only"},
	{AwaitStructured, "await-structured"},
	{MayPark, "may-park"},
	{WaitPlatform, "wait-platform"},
	{WaitHost, "wait-host"},
	{WaitForeign, "wait-foreign"},
}

// Normalize returns the canonical representative of an effect.
func (e Effect) Normalize() Effect {
	if e&opaqueSuspendBit != 0 {
		return e | OpaqueSuspend
	}
	if e&WaitHost != 0 {
		e |= WaitPlatform
	}
	return e
}

// Validate reports whether e contains only defined effect bits.
func (e Effect) Validate() error {
	if unknown := e &^ validEffectBits; unknown != 0 {
		return fmt.Errorf("coro: unknown effect bits %#x", uint16(unknown))
	}
	return nil
}

// Join computes the least upper bound of e and other.
func (e Effect) Join(other Effect) Effect {
	return (e | other).Normalize()
}

// Contains reports whether e is greater than or equal to other in the effect
// lattice.
func (e Effect) Contains(other Effect) bool {
	e = e.Normalize()
	other = other.Normalize()
	return e&other == other
}

// MaySuspend reports whether the effect requires a coroutine-capable caller.
func (e Effect) MaySuspend() bool {
	return e.Normalize() != NoSuspend
}

// IsOpaque reports whether the effect came from an unknown managed target.
func (e Effect) IsOpaque() bool {
	return e&opaqueSuspendBit != 0
}

func (e Effect) String() string {
	text, err := e.MarshalText()
	if err != nil {
		return fmt.Sprintf("effect(%#x)", uint16(e))
	}
	return string(text)
}

// MarshalText encodes an effect using a stable, human-readable spelling.
func (e Effect) MarshalText() ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	e = e.Normalize()
	if e == NoSuspend {
		return []byte("no-suspend"), nil
	}
	if e.IsOpaque() {
		return []byte("opaque-suspend"), nil
	}
	parts := make([]string, 0, len(effectNames))
	for _, item := range effectNames {
		if e&item.bit != 0 {
			parts = append(parts, item.name)
		}
	}
	return []byte(strings.Join(parts, ",")), nil
}

// UnmarshalText decodes the canonical effect spelling. Component order is not
// significant on input; subsequent marshaling always uses canonical order.
func (e *Effect) UnmarshalText(text []byte) error {
	if e == nil {
		return fmt.Errorf("coro: cannot unmarshal effect into nil receiver")
	}
	s := strings.TrimSpace(string(text))
	switch s {
	case "no-suspend":
		*e = NoSuspend
		return nil
	case "opaque-suspend":
		*e = OpaqueSuspend
		return nil
	case "":
		return fmt.Errorf("coro: empty effect")
	}

	var ret Effect
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		matched := false
		for _, item := range effectNames {
			if part == item.name {
				ret |= item.bit
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("coro: unknown effect %q", part)
		}
	}
	*e = ret.Normalize()
	return nil
}

// managedCallEffect is the effect introduced in a caller by a normal managed
// call. A suspendable callee is represented as a structured child await, while
// a bounded plain callee introduces no suspend effect.
func managedCallEffect(callee Effect) Effect {
	callee = callee.Normalize()
	if callee == NoSuspend {
		return NoSuspend
	}
	return callee.Join(AwaitStructured)
}
