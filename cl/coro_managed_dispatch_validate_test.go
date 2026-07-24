//go:build !llgo

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
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

func TestCoroManagedDispatchValidationRequiresCapability(t *testing.T) {
	fn, call, plan, functionPlan := buildCoroManagedDispatchValidationFixture(
		t, `func Apply(callback func()) { callback() }`, coro.UnknownManagedDispatch,
	)
	callPlan, ok := plan.CallPlan(call)
	if !ok {
		t.Fatal("managed dynamic call has no CallPlan")
	}
	if callPlan.Rep != coro.Dispatch || !callPlan.Open || callPlan.Unresolved != coro.UnknownManagedDispatch {
		t.Fatalf("managed dynamic CallPlan = %+v, want open UnknownManagedDispatch", callPlan)
	}
	if functionPlan.Emission != coro.EmitCoroutine || !functionPlan.Effect.Contains(coro.AwaitStructured) {
		t.Fatalf("Apply plan = %+v, want an await-structured coroutine", functionPlan)
	}
	if err := validateCoroManagedDispatchCall(plan, fn, call, callPlan); err != nil {
		t.Fatalf("valid managed descriptor call rejected: %v", err)
	}

	if err := validateCoroPhysicalABIWithUniverseCapabilitiesFrameRetentionAndChannel(
		fn, functionPlan, plan, nil, true, false, false, false, "", false, false, false,
	); err == nil || !strings.Contains(err.Error(), "requires the v1 descriptor dispatch capability") {
		t.Fatalf("physical gate-off error = %v", err)
	}
	if err := validateCoroPhysicalConsumersCapabilities(plan, nil, true, false, false); err == nil ||
		!strings.Contains(err.Error(), "requires the v1 descriptor dispatch capability") {
		t.Fatalf("consumer gate-off error = %v", err)
	}

	if err := validateCoroPhysicalABIWithUniverseCapabilitiesFrameRetentionAndChannel(
		fn, functionPlan, plan, nil, true, false, false, false, "", false, true, false,
	); err != nil {
		t.Fatalf("physical gate-on validation rejected managed descriptor call: %v", err)
	}
	if err := validateCoroPhysicalConsumersCapabilities(plan, nil, true, false, true); err != nil {
		t.Fatalf("consumer gate-on validation rejected managed descriptor call: %v", err)
	}
	// validateCoroPlainDispatchConsumers is reached only when
	// The stackless architecture must recognize the same open call rather
	// than routing it through the legacy closed/plain-only validator.
	if err := validateCoroPlainDispatchConsumers(plan, nil, nil, nil); err != nil {
		t.Fatalf("descriptor consumer validation rejected managed descriptor call: %v", err)
	}
}

func TestCoroManagedDispatchValidationTreatsCallPlanAsOccurrenceAuthority(t *testing.T) {
	callTargets := []coro.FunctionID{"a", "b", "c"}
	for _, test := range []struct {
		name   string
		values []coro.FunctionID
		want   bool
	}{
		{name: "empty structural set", want: true},
		{name: "strict structural subset", values: []coro.FunctionID{"a", "c"}, want: true},
		{name: "exact set", values: []coro.FunctionID{"a", "b", "c"}, want: true},
		{name: "producer outside occurrence", values: []coro.FunctionID{"a", "d"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			missing, ok := coroDispatchTargetsSubset(test.values, callTargets)
			if ok != test.want {
				t.Fatalf("subset = %t, missing=%q, want %t", ok, missing, test.want)
			}
			if !ok && missing != "d" {
				t.Fatalf("missing target = %q, want d", missing)
			}
		})
	}
}

func TestCoroManagedDispatchValidationAllowsConstantDeadAwaitSeed(t *testing.T) {
	fn, call, plan, functionPlan := buildCoroManagedDispatchValidationFixture(t, `
		const disabled = true
		func Apply(callback func()) {
			if disabled { return }
			callback()
		}`, coro.UnknownManagedDispatch,
	)
	if !functionPlan.LocalEffect.Contains(coro.AwaitStructured) {
		t.Fatalf("Apply local effect = %s, want conservative await seed from the dead SSA block", functionPlan.LocalEffect)
	}
	audit, err := newCoroPhysicalPureSSAAudit(nil, plan, fn, "")
	if err != nil {
		t.Fatal(err)
	}
	if audit.reachableBlocks[call.Block()] {
		t.Fatal("constant-disabled managed call is physically reachable")
	}
	if err := validateCoroPhysicalABIWithUniverseCapabilitiesFrameRetentionAndChannel(
		fn, functionPlan, plan, nil, true, false, false, false, "", false, true, false,
	); err != nil {
		t.Fatalf("constant-dead managed await seed rejected: %v", err)
	}
}

func TestCoroManagedDispatchValidationKeepsUnknownDomainsFailClosed(t *testing.T) {
	for _, unresolved := range []coro.UnknownTarget{coro.UnknownManaged, coro.UnknownForeign} {
		t.Run(coroManagedDispatchUnknownName(unresolved), func(t *testing.T) {
			fn, call, plan, functionPlan := buildCoroManagedDispatchValidationFixture(
				t, `func Apply(callback func()) { callback() }`, unresolved,
			)
			callPlan, ok := plan.CallPlan(call)
			if !ok {
				t.Fatal("dynamic call has no CallPlan")
			}
			if err := validateCoroManagedDispatchCall(plan, fn, call, callPlan); err == nil ||
				(!strings.Contains(err.Error(), "certified as UnknownManagedDispatch") &&
					!strings.Contains(err.Error(), "ordinary direct call instruction")) {
				t.Fatalf("managed descriptor validator error = %v", err)
			}
			if err := validateCoroPhysicalABIWithUniverseCapabilitiesFrameRetentionAndChannel(
				fn, functionPlan, plan, nil, true, true, false, false, "", false, true, false,
			); err == nil {
				t.Fatalf("physical validator accepted unresolved domain %v: %v", unresolved, err)
			}
			if err := validateCoroPhysicalConsumersCapabilities(plan, nil, true, false, true); err == nil ||
				!strings.Contains(err.Error(), "uncertified execution domain") {
				t.Fatalf("consumer validator accepted unresolved domain %v: %v", unresolved, err)
			}
		})
	}
}

func TestCoroManagedDispatchValidationAcceptsStdlibCallShapes(t *testing.T) {
	declarations := []string{
		`func Apply(callback func() error) error { return callback() }`,
		`func Apply(callback func(int, []byte) (int, error), fd int, data []byte) (int, error) {
			return callback(fd, data)
		}`,
		`func Apply(callback func(int) (int, error), fd int) (int, error) { return callback(fd) }`,
		`type Conn interface { Close() error }
		func Apply(callback func() (Conn, error)) (Conn, error) { return callback() }`,
		`func Apply(callback func(string, any, *byte) (string, any, *byte), text string, value any, pointer *byte) (string, any, *byte) {
			return callback(text, value, pointer)
		}`,
	}
	for _, declaration := range declarations {
		fn, call, plan, functionPlan := buildCoroManagedDispatchValidationFixture(
			t, declaration, coro.UnknownManagedDispatch,
		)
		callPlan, ok := plan.CallPlan(call)
		if !ok {
			t.Fatal("dynamic call has no CallPlan")
		}
		if err := validateCoroManagedDispatchCall(plan, fn, call, callPlan); err != nil {
			t.Fatalf("stdlib-shaped v1 signature rejected: %v", err)
		}
		if err := validateCoroPhysicalABIWithUniverseCapabilitiesFrameRetentionAndChannel(
			fn, functionPlan, plan, nil, true, false, false, false, "", false, true, false,
		); err != nil {
			t.Fatalf("physical validator rejected stdlib-shaped signature: %v", err)
		}
		if err := validateCoroPhysicalConsumersCapabilities(plan, nil, true, false, true); err != nil {
			t.Fatalf("consumer validator rejected stdlib-shaped signature: %v", err)
		}
	}
}

func TestCoroManagedDispatchValidationAcceptsInlineNestedFunctionTransport(t *testing.T) {
	fn, call, plan, functionPlan := buildCoroManagedDispatchValidationFixture(
		t, `type Inline struct { Callback func() }
		func Apply(callback func(Inline), value Inline) { callback(value) }`, coro.UnknownManagedDispatch,
	)
	callPlan, ok := plan.CallPlan(call)
	if !ok {
		t.Fatal("dynamic call has no CallPlan")
	}
	if err := validateCoroManagedDispatchCall(plan, fn, call, callPlan); err != nil {
		t.Fatalf("inline nested-function signature rejected: %v", err)
	}
	if err := validateCoroPhysicalABIWithUniverseCapabilitiesFrameRetentionAndChannel(
		fn, functionPlan, plan, nil, true, false, false, false, "", false, true, false,
	); err != nil {
		t.Fatalf("physical inline nested-function signature rejected: %v", err)
	}
	if err := validateCoroPhysicalConsumersCapabilities(plan, nil, true, false, true); err != nil {
		t.Fatalf("consumer inline nested-function signature rejected: %v", err)
	}
}

func buildCoroManagedDispatchValidationFixture(
	t *testing.T,
	declaration string,
	unresolved coro.UnknownTarget,
) (*ssa.Function, *ssa.Call, *coro.SSAPlan, coro.FunctionPlan) {
	t.Helper()
	ssaPkg, _, _ := buildGoSSAPkg(t, "package foo\n"+declaration)
	fn := ssaPkg.Func("Apply")
	call := onlyCoroManagedDispatchValidationCall(t, fn)
	plan, err := coro.AnalyzeSSA(
		ssaPkg.Prog,
		coro.Roots{{Function: fn, Demand: coro.AsyncDemand}},
		coro.SSAConfig{
			MaxPlainInstructions: -1,
			ClassifyUnknownCall: func(*ssa.Function, ssa.CallInstruction) (coro.UnknownTarget, error) {
				return unresolved, nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	functionPlan, ok := plan.FunctionPlan(fn)
	if !ok {
		t.Fatal("Apply has no FunctionPlan")
	}
	return fn, call, plan, functionPlan
}

func onlyCoroManagedDispatchValidationCall(t *testing.T, fn *ssa.Function) *ssa.Call {
	t.Helper()
	var found *ssa.Call
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok {
				continue
			}
			if _, builtin := call.Common().Value.(*ssa.Builtin); builtin {
				continue
			}
			if found != nil {
				t.Fatal("Apply contains more than one non-builtin call")
			}
			found = call
		}
	}
	if found == nil {
		t.Fatal("Apply contains no non-builtin call")
	}
	return found
}

func coroManagedDispatchUnknownName(target coro.UnknownTarget) string {
	switch target {
	case coro.UnknownManaged:
		return "managed"
	case coro.UnknownForeign:
		return "foreign"
	default:
		return "unknown"
	}
}
