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
	"testing"
)

func TestAnalyzeDirectPropagation(t *testing.T) {
	g := NewGraph()
	mustAddFunction(t, g, FunctionSpec{ID: "root"})
	mustAddFunction(t, g, FunctionSpec{ID: "middle"})
	mustAddFunction(t, g, FunctionSpec{ID: "leaf", Seed: MayPark})
	mustAddCall(t, g, CallEdge{Caller: "root", Callee: "middle", Kind: CallDirect})
	mustAddCall(t, g, CallEdge{Caller: "middle", Callee: "leaf", Kind: CallDirect})

	plan, err := g.Analyze()
	if err != nil {
		t.Fatal(err)
	}
	assertEffect(t, plan, "leaf", MayPark)
	assertEffect(t, plan, "middle", MayPark|AwaitStructured)
	assertEffect(t, plan, "root", MayPark|AwaitStructured)
	if fn := mustLookup(t, plan, "root"); fn.Primary != PrimaryCoroutine {
		t.Fatalf("root primary = %s, want coroutine", fn.Primary)
	}
}

func TestAnalyzeRecursiveSCC(t *testing.T) {
	g := NewGraph()
	for _, spec := range []FunctionSpec{
		{ID: "entry"},
		{ID: "a"},
		{ID: "b"},
		{ID: "leaf", Seed: WaitHost},
	} {
		mustAddFunction(t, g, spec)
	}
	// Put the only semantic suspend seed outside the cycle. The fixed point must
	// carry it through both members and back to the entry. The SCC itself also
	// receives YieldOnly because recursive managed execution needs preemption.
	mustAddCall(t, g, CallEdge{Caller: "entry", Callee: "a", Kind: CallDirect})
	mustAddCall(t, g, CallEdge{Caller: "a", Callee: "b", Kind: CallDirect})
	mustAddCall(t, g, CallEdge{Caller: "b", Callee: "a", Kind: CallDirect})
	mustAddCall(t, g, CallEdge{Caller: "b", Callee: "leaf", Kind: CallDirect})

	plan, err := g.Analyze()
	if err != nil {
		t.Fatal(err)
	}
	cycleEffect := YieldOnly | AwaitStructured | WaitPlatform | WaitHost
	for _, id := range []FunctionID{"a", "b"} {
		fn := mustLookup(t, plan, id)
		if !fn.Recursive {
			t.Fatalf("%s was not marked recursive", id)
		}
		if fn.Effect != cycleEffect {
			t.Fatalf("%s effect = %s, want %s", id, fn.Effect, cycleEffect)
		}
		if !fn.LocalEffect.Contains(YieldOnly) {
			t.Fatalf("%s local effect lacks recursive yield seed: %s", id, fn.LocalEffect)
		}
		if !fn.Exec.Contains(NeedsPreempt) {
			t.Fatalf("%s execution flags lack recursive preemption: %s", id, fn.Exec)
		}
	}
	assertEffect(t, plan, "entry", cycleEffect)
	if fn := mustLookup(t, plan, "entry"); fn.Recursive {
		t.Fatal("entry incorrectly marked recursive")
	}
}

func TestAnalyzeSelfRecursionAddsPreemptSeed(t *testing.T) {
	g := NewGraph()
	mustAddFunction(t, g, FunctionSpec{ID: "self"})
	mustAddCall(t, g, CallEdge{Caller: "self", Callee: "self", Kind: CallDirect})
	plan, err := g.Analyze()
	if err != nil {
		t.Fatal(err)
	}
	fn := mustLookup(t, plan, "self")
	if !fn.Recursive || !fn.LocalEffect.Contains(YieldOnly) {
		t.Fatalf("self recursion plan = %+v", fn)
	}
	if !fn.Effect.Contains(AwaitStructured) {
		t.Fatalf("self recursion effect lacks structured recursive await: %s", fn.Effect)
	}
}

func TestAnalyzeTrustedBoundedRecursionRequiresCompleteSCC(t *testing.T) {
	build := func(complete bool) *Plan {
		t.Helper()
		g := NewGraph()
		mustAddFunction(t, g, FunctionSpec{
			ID: "a", Demand: AsyncDemand, TrustedBoundedRecursion: true,
		})
		mustAddFunction(t, g, FunctionSpec{
			ID: "b", TrustedBoundedRecursion: complete,
		})
		mustAddCall(t, g, CallEdge{Caller: "a", Callee: "b", Kind: CallDirect})
		mustAddCall(t, g, CallEdge{Caller: "b", Callee: "a", Kind: CallDirect})
		plan, err := g.Analyze()
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}

	complete := build(true)
	for _, id := range []FunctionID{"a", "b"} {
		fn := mustLookup(t, complete, id)
		if !fn.Recursive || !fn.TrustedBoundedRecursion || fn.Effect != NoSuspend ||
			fn.Exec.Contains(NeedsPreempt) || fn.Primary != PrimaryPlain || fn.Emission != EmitPlain {
			t.Fatalf("complete bounded SCC member %s = %+v, want recursive plain body without recursive preemption seed", id, fn)
		}
	}

	partial := build(false)
	for _, id := range []FunctionID{"a", "b"} {
		fn := mustLookup(t, partial, id)
		if !fn.Recursive || fn.TrustedBoundedRecursion || !fn.LocalEffect.Contains(YieldOnly) ||
			!fn.LocalExec.Contains(NeedsPreempt) || fn.Primary != PrimaryCoroutine || fn.Emission != EmitCoroutine {
			t.Fatalf("partial bounded SCC member %s = %+v, want ordinary fail-closed recursive plan", id, fn)
		}
	}
}

func TestAnalyzeTrustedBoundedRecursionPreservesDeclaredAndCalleeEffects(t *testing.T) {
	tests := []struct {
		name string
		b    FunctionSpec
	}{
		{
			name: "explicit preemption",
			b:    FunctionSpec{ID: "b", Exec: NeedsPreempt, TrustedBoundedRecursion: true},
		},
		{
			name: "real suspend",
			b:    FunctionSpec{ID: "b", Seed: WaitHost, TrustedBoundedRecursion: true},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGraph()
			mustAddFunction(t, g, FunctionSpec{
				ID: "a", Demand: AsyncDemand, TrustedBoundedRecursion: true,
			})
			mustAddFunction(t, g, test.b)
			mustAddCall(t, g, CallEdge{Caller: "a", Callee: "b", Kind: CallDirect})
			mustAddCall(t, g, CallEdge{Caller: "b", Callee: "a", Kind: CallDirect})
			plan, err := g.Analyze()
			if err != nil {
				t.Fatal(err)
			}
			for _, id := range []FunctionID{"a", "b"} {
				fn := mustLookup(t, plan, id)
				if !fn.Recursive || !fn.TrustedBoundedRecursion || !fn.Effect.MaySuspend() ||
					fn.Primary != PrimaryCoroutine || fn.Emission != EmitCoroutine {
					t.Fatalf("bounded SCC member %s = %+v, want preserved suspendable plan", id, fn)
				}
			}
			b := mustLookup(t, plan, "b")
			if test.b.Exec.Contains(NeedsPreempt) &&
				(!b.LocalExec.Contains(NeedsPreempt) || !b.LocalEffect.Contains(YieldOnly)) {
				t.Fatalf("explicit preemption was cleared: %+v", b)
			}
			if test.b.Seed.MaySuspend() && !b.LocalEffect.Contains(test.b.Seed) {
				t.Fatalf("declared suspend effect was cleared: %+v", b)
			}
		})
	}
}

func TestAnalyzeSpawnDoesNotTaintCaller(t *testing.T) {
	g := NewGraph()
	mustAddFunction(t, g, FunctionSpec{ID: "caller"})
	mustAddFunction(t, g, FunctionSpec{ID: "worker", Seed: MayPark})
	mustAddCall(t, g, CallEdge{Caller: "caller", Callee: "worker", Kind: CallSpawn})
	mustAddUnknownCall(t, g, UnknownCall{Caller: "caller", Kind: CallSpawn, Target: UnknownManaged})
	mustAddUnknownCall(t, g, UnknownCall{Caller: "caller", Kind: CallSpawn, Target: UnknownManagedDispatch})

	plan, err := g.Analyze()
	if err != nil {
		t.Fatal(err)
	}
	assertEffect(t, plan, "caller", NoSuspend)
	if fn := mustLookup(t, plan, "caller"); fn.Primary != PrimaryPlain {
		t.Fatalf("spawn caller primary = %s, want plain", fn.Primary)
	}
}

func TestAnalyzeExternalAndUnknownPolicies(t *testing.T) {
	g := NewGraph()
	for _, spec := range []FunctionSpec{
		{ID: "known-caller"},
		{ID: "unknown-caller"},
		{ID: "foreign-caller"},
		{ID: "known", Seed: WaitHost, External: ExternalKnown},
		{ID: "unknown", External: ExternalUnknownManaged},
		{ID: "foreign", External: ExternalUnknownForeign},
	} {
		mustAddFunction(t, g, spec)
	}
	mustAddCall(t, g, CallEdge{Caller: "known-caller", Callee: "known", Kind: CallDirect})
	mustAddCall(t, g, CallEdge{Caller: "unknown-caller", Callee: "unknown", Kind: CallDirect})
	mustAddCall(t, g, CallEdge{Caller: "foreign-caller", Callee: "foreign", Kind: CallForeign})

	plan, err := g.Analyze()
	if err != nil {
		t.Fatal(err)
	}
	assertEffect(t, plan, "known-caller", AwaitStructured|WaitPlatform|WaitHost)
	if got := mustLookup(t, plan, "unknown-caller").Effect; !got.IsOpaque() {
		t.Fatalf("unknown managed caller effect = %s, want opaque", got)
	}
	if got := mustLookup(t, plan, "unknown-caller").Exec; !got.IsOpaque() {
		t.Fatalf("unknown managed caller execution flags = %s, want opaque", got)
	}
	unknown := mustLookup(t, plan, "unknown")
	if !unknown.Exec.IsOpaque() || unknown.FuncRep != Dispatch {
		t.Fatalf("unknown managed external plan = %+v, want opaque dispatch", unknown)
	}
	assertEffect(t, plan, "foreign-caller", WaitForeign)
	if got := mustLookup(t, plan, "foreign-caller").Exec; !got.Contains(IRQUnsafe) {
		t.Fatalf("foreign caller execution flags = %s, want irq-unsafe", got)
	}
	for _, id := range []FunctionID{"known", "unknown", "foreign"} {
		if fn := mustLookup(t, plan, id); fn.Primary != PrimaryExternal {
			t.Fatalf("%s primary = %s, want external", id, fn.Primary)
		}
	}
	foreign := mustLookup(t, plan, "foreign")
	if foreign.Effect != NoSuspend || !foreign.Exec.Contains(BlockForeign|IRQUnsafe) {
		t.Fatalf("foreign external plan = %+v, want plain blocking foreign", foreign)
	}

	g2 := NewGraph()
	mustAddFunction(t, g2, FunctionSpec{ID: "managed"})
	mustAddFunction(t, g2, FunctionSpec{ID: "foreign"})
	mustAddUnknownCall(t, g2, UnknownCall{Caller: "managed", Kind: CallDirect, Target: UnknownManaged})
	mustAddUnknownCall(t, g2, UnknownCall{Caller: "foreign", Kind: CallDirect, Target: UnknownForeign})
	plan, err = g2.Analyze()
	if err != nil {
		t.Fatal(err)
	}
	managed := mustLookup(t, plan, "managed")
	if got := managed.Effect; !got.IsOpaque() {
		t.Fatalf("unknown managed call effect = %s, want opaque", got)
	}
	if !managed.Exec.IsOpaque() {
		t.Fatalf("unknown managed call execution flags = %s, want opaque", managed.Exec)
	}
	assertEffect(t, plan, "foreign", WaitForeign)
	if got := mustLookup(t, plan, "foreign").Exec; !got.Contains(IRQUnsafe) {
		t.Fatalf("unknown foreign execution flags = %s, want irq-unsafe", got)
	}
}

func TestAnalyzeTrustedInlineSuppressesOnlyExactForeignWait(t *testing.T) {
	g := NewGraph()
	mustAddFunction(t, g, FunctionSpec{ID: "trusted-caller", Demand: SyncDemand})
	mustAddFunction(t, g, FunctionSpec{ID: "affine-caller", Demand: SyncDemand})
	mustAddFunction(t, g, FunctionSpec{ID: "auto-caller", Demand: SyncDemand})
	mustAddFunction(t, g, FunctionSpec{
		ID: "foreign", External: ExternalUnknownForeign, Exec: ThreadAffine | OpaqueExec | MayUnwind,
	})
	mustAddCall(t, g, CallEdge{
		Caller: "trusted-caller", Callee: "foreign", Kind: CallTrustedInline,
		DefaultContractExec: ThreadAffine | OpaqueExec,
	})
	mustAddCall(t, g, CallEdge{
		Caller: "affine-caller", Callee: "foreign", Kind: CallTrustedInline,
		DefaultContractExec: ThreadAffine | OpaqueExec, SelectedContractExec: ThreadAffine,
	})
	mustAddCall(t, g, CallEdge{Caller: "auto-caller", Callee: "foreign", Kind: CallForeign})

	plan, err := g.Analyze()
	if err != nil {
		t.Fatal(err)
	}
	trusted := mustLookup(t, plan, "trusted-caller")
	if trusted.Effect != NoSuspend || trusted.Exec != IRQUnsafe|MayUnwind || trusted.Primary != PrimaryPlain {
		t.Fatalf("trusted-inline caller = %+v", trusted)
	}
	affine := mustLookup(t, plan, "affine-caller")
	if affine.Effect != NoSuspend || affine.Exec != ThreadAffine|IRQUnsafe|MayUnwind || affine.Primary != PrimaryPlain {
		t.Fatalf("affine trusted-inline caller = %+v", affine)
	}
	auto := mustLookup(t, plan, "auto-caller")
	if !auto.Effect.Contains(WaitForeign) || auto.Exec != ThreadAffine|OpaqueExec|IRQUnsafe|MayUnwind || auto.Primary != PrimaryCoroutine {
		t.Fatalf("auto caller = %+v", auto)
	}
	foreign := mustLookup(t, plan, "foreign")
	if foreign.External != ExternalUnknownForeign || foreign.Demand != SyncDemand ||
		foreign.Exec != BlockForeign|ThreadAffine|OpaqueExec|IRQUnsafe|MayUnwind {
		t.Fatalf("foreign target = %+v", foreign)
	}
}

func TestTrustedInlineCallEdgeContractExecFailsClosed(t *testing.T) {
	tests := []struct {
		name string
		edge CallEdge
		want string
	}{
		{
			name: "ordinary edge carries projection",
			edge: CallEdge{Caller: "caller", Callee: "foreign", Kind: CallDirect, DefaultContractExec: ThreadAffine},
			want: "carries trusted-inline",
		},
		{
			name: "non-contract IRQ flag",
			edge: CallEdge{Caller: "caller", Callee: "foreign", Kind: CallTrustedInline, DefaultContractExec: IRQUnsafe},
			want: "non-contract flags",
		},
		{
			name: "non-contract unwind flag",
			edge: CallEdge{Caller: "caller", Callee: "foreign", Kind: CallTrustedInline, DefaultContractExec: MayUnwind},
			want: "non-contract flags",
		},
		{
			name: "selected widening",
			edge: CallEdge{Caller: "caller", Callee: "foreign", Kind: CallTrustedInline, SelectedContractExec: OpaqueExec},
			want: "widens default",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGraph()
			if err := g.AddCall(test.edge); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("AddCall error = %v; want %q", err, test.want)
			}
		})
	}

	duplicate := NewGraph()
	first := CallEdge{
		Caller: "caller", Callee: "foreign", Kind: CallTrustedInline,
		DefaultContractExec: ThreadAffine | OpaqueExec,
	}
	if err := duplicate.AddCall(first); err != nil {
		t.Fatal(err)
	}
	if err := duplicate.AddCall(first); err != nil {
		t.Fatalf("identical duplicate edge rejected: %v", err)
	}
	conflict := first
	conflict.SelectedContractExec = ThreadAffine
	if err := duplicate.AddCall(conflict); err == nil || !strings.Contains(err.Error(), "conflicting duplicate") {
		t.Fatalf("conflicting duplicate error = %v", err)
	}

	laneMismatch := NewGraph()
	mustAddFunction(t, laneMismatch, FunctionSpec{ID: "caller", Demand: SyncDemand})
	mustAddFunction(t, laneMismatch, FunctionSpec{ID: "foreign", External: ExternalUnknownForeign, Exec: ThreadAffine})
	mustAddCall(t, laneMismatch, CallEdge{
		Caller: "caller", Callee: "foreign", Kind: CallTrustedInline,
		DefaultContractExec: ThreadAffine | OpaqueExec,
	})
	if _, err := laneMismatch.Analyze(); err == nil || !strings.Contains(err.Error(), "lanes are") {
		t.Fatalf("target contract lane mismatch error = %v", err)
	}

	propagatedLane := NewGraph()
	mustAddFunction(t, propagatedLane, FunctionSpec{ID: "caller", Demand: SyncDemand})
	mustAddFunction(t, propagatedLane, FunctionSpec{ID: "foreign", External: ExternalUnknownForeign})
	mustAddFunction(t, propagatedLane, FunctionSpec{ID: "independent-affine", Exec: ThreadAffine})
	mustAddCall(t, propagatedLane, CallEdge{Caller: "caller", Callee: "foreign", Kind: CallTrustedInline})
	mustAddCall(t, propagatedLane, CallEdge{Caller: "foreign", Callee: "independent-affine", Kind: CallDirect})
	if _, err := propagatedLane.Analyze(); err == nil || !strings.Contains(err.Error(), "lanes are") {
		t.Fatalf("independently propagated contract-shaped lane error = %v", err)
	}
}

func TestAnalyzeUnknownCallMatrix(t *testing.T) {
	for _, kind := range []CallKind{CallDirect, CallDefer, CallSpawn, CallForeign} {
		for _, target := range []UnknownTarget{UnknownManaged, UnknownForeign, UnknownManagedDispatch, UnknownManagedInterfaceDispatch} {
			t.Run(kindName(kind)+"/"+unknownTargetName(target), func(t *testing.T) {
				g := NewGraph()
				mustAddFunction(t, g, FunctionSpec{ID: "caller"})
				mustAddUnknownCall(t, g, UnknownCall{Caller: "caller", Kind: kind, Target: target})
				plan, err := g.Analyze()
				if err != nil {
					t.Fatal(err)
				}
				caller := mustLookup(t, plan, "caller")
				switch {
				case kind == CallSpawn:
					if caller.Effect != NoSuspend || caller.Exec.Contains(IRQUnsafe) {
						t.Fatalf("unknown spawn polluted caller: %+v", caller)
					}
				case kind == CallForeign || target == UnknownForeign:
					if caller.Effect != WaitForeign || !caller.Exec.Contains(IRQUnsafe) {
						t.Fatalf("unknown foreign call plan = %+v", caller)
					}
				case target.managedDispatch():
					if caller.Effect != AwaitStructured || caller.Exec.Contains(OpaqueExec|IRQUnsafe) {
						t.Fatalf("unknown managed dispatch call plan = %+v", caller)
					}
				default:
					if !caller.Effect.IsOpaque() || !caller.Exec.IsOpaque() {
						t.Fatalf("unknown managed call plan = %+v", caller)
					}
				}
			})
		}
	}
}

func TestAnalyzeDemandAndFunctionRepresentation(t *testing.T) {
	g := NewGraph()
	mustAddFunction(t, g, FunctionSpec{ID: "entry", Demand: AsyncDemand})
	mustAddFunction(t, g, FunctionSpec{ID: "helper"})
	mustAddFunction(t, g, FunctionSpec{
		ID:            "callback",
		Seed:          MayPark,
		Demand:        SyncDemand,
		NeedsDispatch: true,
	})
	mustAddCall(t, g, CallEdge{Caller: "entry", Callee: "helper", Kind: CallDirect})
	mustAddCall(t, g, CallEdge{Caller: "entry", Callee: "callback", Kind: CallSpawn})

	plan, err := g.Analyze()
	if err != nil {
		t.Fatal(err)
	}
	entry := mustLookup(t, plan, "entry")
	if entry.Effect != NoSuspend || entry.Demand != AsyncDemand || entry.Emission != EmitPlain || entry.FuncRep != DirectPlain {
		t.Fatalf("entry plan = %+v", entry)
	}
	helper := mustLookup(t, plan, "helper")
	if helper.Demand != SyncDemand || helper.Emission != EmitPlain || helper.FuncRep != DirectPlain {
		t.Fatalf("bounded helper plan = %+v", helper)
	}
	callback := mustLookup(t, plan, "callback")
	if callback.Demand != BothDemand || callback.Emission != EmitCoroutine || callback.FuncRep != Dispatch || callback.Primary != PrimaryCoroutine {
		t.Fatalf("dynamic callback plan = %+v", callback)
	}
}

func TestAnalyzeExplicitStatusColorsOnlyAsyncMayUnwindBodies(t *testing.T) {
	build := func(demand Demand, mode OutcomeMode) *Plan {
		t.Helper()
		g := NewGraph()
		mustAddFunction(t, g, FunctionSpec{ID: "entry", Demand: demand, Exec: MayUnwind})
		plan, err := g.AnalyzeWithConfig(GraphAnalysisConfig{OutcomeMode: mode})
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}

	for _, test := range []struct {
		name   string
		demand Demand
		mode   OutcomeMode
		want   BodyEmission
	}{
		{name: "legacy async", demand: AsyncDemand, mode: OutcomeLegacy, want: EmitPlain},
		{name: "explicit sync", demand: SyncDemand, mode: OutcomeExplicitStatus, want: EmitPlain},
		{name: "explicit async", demand: AsyncDemand, mode: OutcomeExplicitStatus, want: EmitCoroutine},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := mustLookup(t, build(test.demand, test.mode), "entry")
			if got.Emission != test.want {
				t.Fatalf("entry plan = %+v, want emission %s", got, test.want)
			}
			if test.want == EmitCoroutine {
				if got.Effect != OutcomeStructured || got.Primary != PrimaryCoroutine || got.FuncRep != DirectCoro {
					t.Fatalf("explicit async outcome plan = %+v", got)
				}
			} else if got.Effect != NoSuspend || got.Primary != PrimaryPlain || got.FuncRep != DirectPlain {
				t.Fatalf("plain outcome plan = %+v", got)
			}
		})
	}
}

func TestAnalyzeExplicitStatusColorsDirectAndUnwindChildren(t *testing.T) {
	g := NewGraph()
	for _, spec := range []FunctionSpec{
		{ID: "entry", Demand: AsyncDemand, Exec: MayUnwind},
		{ID: "direct", Exec: MayUnwind},
		{ID: "unwind", Exec: MayUnwind},
		{ID: "source-panic-helper", Exec: MayUnwind},
	} {
		mustAddFunction(t, g, spec)
	}
	mustAddCall(t, g, CallEdge{Caller: "entry", Callee: "direct", Kind: CallDirect})
	mustAddCall(t, g, CallEdge{Caller: "entry", Callee: "unwind", Kind: CallUnwind})
	mustAddCall(t, g, CallEdge{Caller: "entry", Callee: "source-panic-helper", Kind: CallExplicitStatusElided})

	plan, err := g.AnalyzeWithConfig(GraphAnalysisConfig{OutcomeMode: OutcomeExplicitStatus})
	if err != nil {
		t.Fatal(err)
	}
	entry := mustLookup(t, plan, "entry")
	if entry.Effect != OutcomeStructured|AwaitStructured || entry.Emission != EmitCoroutine {
		t.Fatalf("entry plan = %+v", entry)
	}
	for _, id := range []FunctionID{"direct", "unwind"} {
		child := mustLookup(t, plan, id)
		if child.Demand != AsyncDemand || child.Effect != OutcomeStructured || child.Emission != EmitCoroutine ||
			child.Primary != PrimaryCoroutine || child.FuncRep != DirectCoro {
			t.Fatalf("%s outcome child plan = %+v", id, child)
		}
	}
	helper := mustLookup(t, plan, "source-panic-helper")
	if helper.Demand != NoDemand || helper.ManagedDemand != NoDemand || helper.RawPlainDemand || helper.Effect != NoSuspend || helper.Emission != EmitNone ||
		helper.Primary != PrimaryPlain || helper.FuncRep != DirectPlain {
		t.Fatalf("ExplicitStatus-elided source-panic helper = %+v", helper)
	}
}

func TestAnalyzeExplicitStatusPhysicalSyncRootColorsMayUnwindChild(t *testing.T) {
	g := NewGraph()
	mustAddFunction(t, g, FunctionSpec{ID: "entry", Demand: SyncDemand, Seed: MayPark, Exec: MayUnwind})
	mustAddFunction(t, g, FunctionSpec{ID: "helper", Exec: MayUnwind})
	mustAddCall(t, g, CallEdge{Caller: "entry", Callee: "helper", Kind: CallDirect})

	plan, err := g.AnalyzeWithConfig(GraphAnalysisConfig{OutcomeMode: OutcomeExplicitStatus})
	if err != nil {
		t.Fatal(err)
	}
	entry := mustLookup(t, plan, "entry")
	if entry.Demand != SyncDemand || !entry.Effect.Contains(MayPark|OutcomeStructured|AwaitStructured) || entry.Emission != EmitCoroutine {
		t.Fatalf("physical sync root plan = %+v", entry)
	}
	helper := mustLookup(t, plan, "helper")
	if helper.Demand != AsyncDemand || helper.Effect != OutcomeStructured || helper.Emission != EmitCoroutine {
		t.Fatalf("physical sync root helper plan = %+v", helper)
	}
}

func TestAnalyzeRawPlainOnlyClosurePreservesManagedFacts(t *testing.T) {
	g := NewGraph()
	for _, spec := range []FunctionSpec{
		{ID: "raw-root", RawPlainDemand: true, RawPlainEntry: true},
		{ID: "panic-helper", Seed: WaitForeign, Exec: BlockForeign},
		{ID: "schedulerwait", External: ExternalUnknownForeign},
	} {
		mustAddFunction(t, g, spec)
	}
	mustAddCall(t, g, CallEdge{Caller: "raw-root", Callee: "panic-helper", Kind: CallDirect})
	mustAddCall(t, g, CallEdge{Caller: "panic-helper", Callee: "schedulerwait", Kind: CallForeign})

	plan, err := g.Analyze()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []FunctionID{"raw-root", "panic-helper"} {
		got := mustLookup(t, plan, id)
		if got.ManagedDemand != NoDemand || !got.RawPlainDemand || !got.RawPlainOnly || got.Emission != EmitRawPlain ||
			got.Primary != PrimaryPlain || got.FuncRep != DirectPlain || !got.Effect.MaySuspend() {
			t.Fatalf("raw-only %s plan = %+v", id, got)
		}
	}
	helper := mustLookup(t, plan, "panic-helper")
	if !helper.Exec.Contains(BlockForeign) || helper.Effect != WaitForeign || helper.RawPlainEntry {
		t.Fatalf("raw-only helper lost exact facts or gained address capability: %+v", helper)
	}
	wait := mustLookup(t, plan, "schedulerwait")
	if wait.ManagedDemand != NoDemand || !wait.RawPlainDemand || wait.RawPlainOnly || wait.Emission != EmitExternal ||
		wait.External != ExternalUnknownForeign || !wait.Exec.Contains(BlockForeign|IRQUnsafe) {
		t.Fatalf("raw schedulerwait plan = %+v", wait)
	}
}

func TestAnalyzeRawAndManagedDemandSelectsDualCoroutine(t *testing.T) {
	g := NewGraph()
	mustAddFunction(t, g, FunctionSpec{ID: "raw-root", RawPlainDemand: true})
	mustAddFunction(t, g, FunctionSpec{ID: "managed-root", Demand: AsyncDemand})
	mustAddFunction(t, g, FunctionSpec{ID: "helper", Seed: MayPark})
	mustAddFunction(t, g, FunctionSpec{ID: "plain-helper"})
	mustAddCall(t, g, CallEdge{Caller: "raw-root", Callee: "helper", Kind: CallDirect})
	mustAddCall(t, g, CallEdge{Caller: "managed-root", Callee: "helper", Kind: CallDirect})
	mustAddCall(t, g, CallEdge{Caller: "raw-root", Callee: "plain-helper", Kind: CallDirect})
	mustAddCall(t, g, CallEdge{Caller: "managed-root", Callee: "plain-helper", Kind: CallDirect})

	plan, err := g.Analyze()
	if err != nil {
		t.Fatal(err)
	}
	helper := mustLookup(t, plan, "helper")
	if helper.ManagedDemand != AsyncDemand || !helper.RawPlainDemand || helper.Demand != BothDemand || helper.RawPlainOnly ||
		helper.Emission != EmitCoroutine || helper.Primary != PrimaryCoroutine || helper.FuncRep != DirectCoro {
		t.Fatalf("mixed helper plan = %+v", helper)
	}
	plain := mustLookup(t, plan, "plain-helper")
	if plain.ManagedDemand != SyncDemand || !plain.RawPlainDemand || plain.RawPlainOnly || plain.Emission != EmitPlain ||
		plain.Primary != PrimaryPlain || plain.FuncRep != DirectPlain {
		t.Fatalf("mixed no-suspend helper plan = %+v", plain)
	}
}

func TestAnalyzeRawReferenceCannotMakeOpenValueRawPlainOnly(t *testing.T) {
	g := NewGraph()
	mustAddFunction(t, g, FunctionSpec{ID: "raw-root", RawPlainDemand: true})
	mustAddFunction(t, g, FunctionSpec{ID: "escaped", Seed: YieldOnly, NeedsDispatch: true})
	mustAddReference(t, g, ReferenceEdge{Owner: "raw-root", Target: "escaped", RawPlain: true})

	plan, err := g.Analyze()
	if err != nil {
		t.Fatal(err)
	}
	escaped := mustLookup(t, plan, "escaped")
	if escaped.ManagedDemand != AsyncDemand || !escaped.RawPlainDemand || escaped.RawPlainOnly ||
		escaped.Emission != EmitCoroutine || escaped.Primary != PrimaryCoroutine || escaped.FuncRep != Dispatch {
		t.Fatalf("escaped raw reference plan = %+v", escaped)
	}
}

func TestAnalyzeExplicitStatusElidedDemandFollowsPhysicalDomain(t *testing.T) {
	g := NewGraph()
	for _, spec := range []FunctionSpec{
		{ID: "managed-coro", Demand: AsyncDemand, Exec: MayUnwind},
		{ID: "managed-plain", Demand: SyncDemand},
		{ID: "raw-owner", RawPlainDemand: true},
		{ID: "elided"}, {ID: "plain-helper"}, {ID: "raw-helper"},
	} {
		mustAddFunction(t, g, spec)
	}
	mustAddCall(t, g, CallEdge{Caller: "managed-coro", Callee: "elided", Kind: CallExplicitStatusElided})
	mustAddCall(t, g, CallEdge{Caller: "managed-plain", Callee: "plain-helper", Kind: CallExplicitStatusElided})
	mustAddCall(t, g, CallEdge{Caller: "raw-owner", Callee: "raw-helper", Kind: CallExplicitStatusElided})

	plan, err := g.AnalyzeWithConfig(GraphAnalysisConfig{OutcomeMode: OutcomeExplicitStatus})
	if err != nil {
		t.Fatal(err)
	}
	if got := mustLookup(t, plan, "elided"); got.Demand != NoDemand || got.Emission != EmitNone {
		t.Fatalf("physically elided managed helper = %+v", got)
	}
	if got := mustLookup(t, plan, "plain-helper"); got.ManagedDemand != SyncDemand || got.RawPlainDemand || got.Emission != EmitPlain {
		t.Fatalf("plain managed helper = %+v", got)
	}
	if got := mustLookup(t, plan, "raw-helper"); got.ManagedDemand != NoDemand || !got.RawPlainDemand || !got.RawPlainOnly || got.Emission != EmitRawPlain {
		t.Fatalf("raw legacy helper = %+v", got)
	}
}

func TestAnalyzeExplicitStatusSyncOnlyReferenceRetainsPlainABI(t *testing.T) {
	g := NewGraph()
	mustAddFunction(t, g, FunctionSpec{ID: "entry", Demand: AsyncDemand, Exec: MayUnwind})
	mustAddFunction(t, g, FunctionSpec{ID: "callback", Exec: MayUnwind})
	mustAddReference(t, g, ReferenceEdge{Owner: "entry", Target: "callback", SyncOnly: true})

	plan, err := g.AnalyzeWithConfig(GraphAnalysisConfig{OutcomeMode: OutcomeExplicitStatus})
	if err != nil {
		t.Fatal(err)
	}
	entry := mustLookup(t, plan, "entry")
	if entry.Effect != OutcomeStructured || entry.Emission != EmitCoroutine {
		t.Fatalf("ExplicitStatus reference owner = %+v", entry)
	}
	callback := mustLookup(t, plan, "callback")
	if callback.Demand != SyncDemand || callback.Effect != NoSuspend || callback.Emission != EmitPlain ||
		callback.Primary != PrimaryPlain || callback.FuncRep != DirectPlain {
		t.Fatalf("synchronous-only callback = %+v", callback)
	}

	// One ordinary publication in the same graph conservatively wins over the
	// exact synchronous-only use.
	mustAddReference(t, g, ReferenceEdge{Owner: "entry", Target: "callback"})
	plan, err = g.AnalyzeWithConfig(GraphAnalysisConfig{OutcomeMode: OutcomeExplicitStatus})
	if err != nil {
		t.Fatal(err)
	}
	callback = mustLookup(t, plan, "callback")
	if callback.Demand != BothDemand || callback.Effect != OutcomeStructured || callback.Emission != EmitCoroutine {
		t.Fatalf("mixed callback references = %+v", callback)
	}
}

func TestAnalyzeExplicitStatusEffectChangesDoNotDemandDormantOwners(t *testing.T) {
	g := NewGraph()
	for _, spec := range []FunctionSpec{
		{ID: "live", Demand: AsyncDemand, Exec: MayUnwind},
		{ID: "dead-call-owner"},
		{ID: "dead-call-child"},
		{ID: "dead-reference-owner"},
		{ID: "dead-reference-child"},
	} {
		mustAddFunction(t, g, spec)
	}

	// ExplicitStatus adds OutcomeStructured to live after ordinary effect
	// propagation has finished. Its reverse call and reference owners must not
	// become demand work merely because that effect changed.
	mustAddCall(t, g, CallEdge{Caller: "dead-call-owner", Callee: "live", Kind: CallDirect})
	mustAddCall(t, g, CallEdge{Caller: "dead-call-owner", Callee: "dead-call-child", Kind: CallDirect})
	mustAddReference(t, g, ReferenceEdge{Owner: "dead-reference-owner", Target: "live"})
	mustAddReference(t, g, ReferenceEdge{Owner: "dead-reference-owner", Target: "dead-reference-child"})

	plan, err := g.AnalyzeWithConfig(GraphAnalysisConfig{OutcomeMode: OutcomeExplicitStatus})
	if err != nil {
		t.Fatal(err)
	}
	live := mustLookup(t, plan, "live")
	if live.Demand != AsyncDemand || live.Effect != OutcomeStructured || live.Emission != EmitCoroutine {
		t.Fatalf("live outcome plan = %+v", live)
	}
	for _, id := range []FunctionID{"dead-call-owner", "dead-call-child", "dead-reference-owner", "dead-reference-child"} {
		got := mustLookup(t, plan, id)
		if got.Demand != NoDemand || got.Emission != EmitNone {
			t.Fatalf("dormant %s plan = %+v, want no demand and no emission", id, got)
		}
	}
}

func TestAnalyzeBodyEmissionUsesDemandEffectAndExternalKind(t *testing.T) {
	g := NewGraph()
	for _, spec := range []FunctionSpec{
		{ID: "dead-plain"},
		{ID: "dead-coro", Seed: MayPark},
		{ID: "live-plain", Demand: SyncDemand},
		{ID: "live-coro", Seed: YieldOnly, Demand: BothDemand},
		{ID: "external-dead", Seed: WaitHost, External: ExternalKnown},
		{ID: "external-live", Demand: AsyncDemand, External: ExternalKnown},
	} {
		mustAddFunction(t, g, spec)
	}
	plan, err := g.Analyze()
	if err != nil {
		t.Fatal(err)
	}

	checks := map[FunctionID]struct {
		emission BodyEmission
		primary  PrimaryKind
		rep      FuncRep
	}{
		"dead-plain":    {EmitNone, PrimaryPlain, DirectPlain},
		"dead-coro":     {EmitNone, PrimaryCoroutine, DirectCoro},
		"live-plain":    {EmitPlain, PrimaryPlain, DirectPlain},
		"live-coro":     {EmitCoroutine, PrimaryCoroutine, DirectCoro},
		"external-dead": {EmitNone, PrimaryExternal, DirectCoro},
		"external-live": {EmitExternal, PrimaryExternal, DirectPlain},
	}
	for id, want := range checks {
		got := mustLookup(t, plan, id)
		if got.Emission != want.emission || got.Primary != want.primary || got.FuncRep != want.rep {
			t.Fatalf("%s plan = %+v, want emission=%s primary=%s rep=%s", id, got, want.emission, want.primary, want.rep)
		}
	}
}

func TestAnalyzeReferencePropagatesDemandOnly(t *testing.T) {
	g := NewGraph()
	for _, spec := range []FunctionSpec{
		{ID: "root", Demand: AsyncDemand},
		{ID: "owner"},
		{ID: "plain-target", Exec: MayUnwind},
		{ID: "coro-target", Seed: MayPark, Exec: NeedsCleanupFrame},
		{ID: "dead-target", Seed: YieldOnly},
	} {
		mustAddFunction(t, g, spec)
	}
	mustAddCall(t, g, CallEdge{Caller: "root", Callee: "owner", Kind: CallDirect})
	mustAddReference(t, g, ReferenceEdge{Owner: "owner", Target: "plain-target"})
	mustAddReference(t, g, ReferenceEdge{Owner: "owner", Target: "coro-target"})

	plan, err := g.Analyze()
	if err != nil {
		t.Fatal(err)
	}
	root := mustLookup(t, plan, "root")
	owner := mustLookup(t, plan, "owner")
	if root.Effect != NoSuspend || root.Exec != 0 || owner.Effect != NoSuspend || owner.Exec != 0 {
		t.Fatalf("reference edge propagated target semantics: root=%+v owner=%+v", root, owner)
	}
	if owner.Demand != SyncDemand || owner.Emission != EmitPlain {
		t.Fatalf("owner plan = %+v", owner)
	}
	plain := mustLookup(t, plan, "plain-target")
	if plain.Demand != SyncDemand || plain.Emission != EmitPlain {
		t.Fatalf("plain reference target = %+v", plain)
	}
	coro := mustLookup(t, plan, "coro-target")
	if coro.Demand != AsyncDemand || coro.Emission != EmitCoroutine {
		t.Fatalf("coroutine reference target = %+v", coro)
	}
	dead := mustLookup(t, plan, "dead-target")
	if dead.Demand != NoDemand || dead.Emission != EmitNone || dead.Primary != PrimaryCoroutine {
		t.Fatalf("unreferenced effectful target = %+v", dead)
	}
}

func TestAnalyzeSyncBoundaryUsesAsyncBodyForSuspendableChild(t *testing.T) {
	g := NewGraph()
	mustAddFunction(t, g, FunctionSpec{ID: "export", Demand: SyncDemand})
	mustAddFunction(t, g, FunctionSpec{ID: "park", Seed: MayPark})
	mustAddCall(t, g, CallEdge{Caller: "export", Callee: "park", Kind: CallDirect})

	plan, err := g.Analyze()
	if err != nil {
		t.Fatal(err)
	}
	export := mustLookup(t, plan, "export")
	if export.Demand != SyncDemand || export.Primary != PrimaryCoroutine {
		t.Fatalf("hard-sync boundary plan = %+v", export)
	}
	park := mustLookup(t, plan, "park")
	if park.Demand != AsyncDemand || park.Primary != PrimaryCoroutine {
		t.Fatalf("suspendable child plan = %+v", park)
	}
}

func TestAnalyzeExplicitPreemptionAndBlockingCallee(t *testing.T) {
	g := NewGraph()
	mustAddFunction(t, g, FunctionSpec{ID: "loop", Exec: NeedsPreempt})
	mustAddFunction(t, g, FunctionSpec{ID: "caller"})
	mustAddFunction(t, g, FunctionSpec{ID: "foreign", Exec: BlockForeign, External: ExternalKnown})
	mustAddCall(t, g, CallEdge{Caller: "caller", Callee: "foreign", Kind: CallDirect})

	plan, err := g.Analyze()
	if err != nil {
		t.Fatal(err)
	}
	loop := mustLookup(t, plan, "loop")
	if !loop.Effect.Contains(YieldOnly) || loop.Primary != PrimaryCoroutine || loop.FuncRep != DirectCoro {
		t.Fatalf("explicit preemption plan = %+v", loop)
	}
	caller := mustLookup(t, plan, "caller")
	if !caller.Effect.Contains(WaitForeign) || caller.Primary != PrimaryCoroutine {
		t.Fatalf("blocking callee did not stack-cut caller: %+v", caller)
	}
}

func TestAnalyzePropagatesOnlyInheritableExecFlags(t *testing.T) {
	g := NewGraph()
	mustAddFunction(t, g, FunctionSpec{ID: "entry"})
	mustAddFunction(t, g, FunctionSpec{ID: "middle"})
	mustAddFunction(t, g, FunctionSpec{
		ID:   "leaf",
		Exec: ThreadAffine | IRQUnsafe | MayUnwind | OpaqueExec | NeedsCleanupFrame | NoReturn | PanicOnly,
	})
	mustAddCall(t, g, CallEdge{Caller: "entry", Callee: "middle", Kind: CallDirect})
	mustAddCall(t, g, CallEdge{Caller: "middle", Callee: "leaf", Kind: CallDirect})

	plan, err := g.Analyze()
	if err != nil {
		t.Fatal(err)
	}
	want := ThreadAffine | IRQUnsafe | MayUnwind | OpaqueExec
	for _, id := range []FunctionID{"entry", "middle"} {
		fn := mustLookup(t, plan, id)
		if fn.Exec != want {
			t.Fatalf("%s execution flags = %s, want %s", id, fn.Exec, want)
		}
		if fn.LocalExec != 0 {
			t.Fatalf("%s local execution flags unexpectedly propagated: %s", id, fn.LocalExec)
		}
	}
	leaf := mustLookup(t, plan, "leaf")
	if leaf.DeclaredExec != leaf.LocalExec || leaf.LocalExec != leaf.Exec {
		t.Fatalf("leaf execution layers = declared %s, local %s, final %s", leaf.DeclaredExec, leaf.LocalExec, leaf.Exec)
	}
}

func TestAnalyzeLongReverseDependencyChain(t *testing.T) {
	const count = 10000
	g := NewGraph()
	ids := make([]FunctionID, count)
	for i := range ids {
		ids[i] = FunctionID(fmt.Sprintf("chain.f%05d", i))
		spec := FunctionSpec{ID: ids[i]}
		if i == count-1 {
			spec.Seed = MayPark
		}
		mustAddFunction(t, g, spec)
	}
	for i := 0; i+1 < count; i++ {
		mustAddCall(t, g, CallEdge{Caller: ids[i], Callee: ids[i+1], Kind: CallDirect})
	}
	plan, err := g.Analyze()
	if err != nil {
		t.Fatal(err)
	}
	root := mustLookup(t, plan, ids[0])
	if !root.Effect.Contains(MayPark|AwaitStructured) || root.Primary != PrimaryCoroutine {
		t.Fatalf("long-chain root plan = %+v", root)
	}
}

func TestAnalyzeValidation(t *testing.T) {
	if _, err := NewGraph().AnalyzeWithConfig(GraphAnalysisConfig{OutcomeMode: OutcomeMode(255)}); err == nil ||
		!strings.Contains(err.Error(), "invalid outcome mode") {
		t.Fatalf("invalid outcome mode error = %v", err)
	}
	var zero Graph
	if err := zero.AddFunction(FunctionSpec{ID: "caller"}); err != nil {
		t.Fatalf("zero-value graph AddFunction: %v", err)
	}
	if err := zero.AddCall(CallEdge{Caller: "caller", Callee: "missing", Kind: CallDirect}); err != nil {
		t.Fatal(err)
	}
	if _, err := zero.Analyze(); err == nil {
		t.Fatal("missing callee unexpectedly accepted")
	}
	if err := zero.AddFunction(FunctionSpec{ID: "caller"}); err == nil {
		t.Fatal("duplicate function unexpectedly accepted")
	}

	missingReference := NewGraph()
	mustAddFunction(t, missingReference, FunctionSpec{ID: "owner", Demand: SyncDemand})
	mustAddReference(t, missingReference, ReferenceEdge{Owner: "owner", Target: "missing"})
	if _, err := missingReference.Analyze(); err == nil {
		t.Fatal("missing reference target unexpectedly accepted")
	}

	conflict := NewGraph()
	mustAddFunction(t, conflict, FunctionSpec{ID: "bad", Seed: MayPark, Exec: BlockForeign})
	if _, err := conflict.Analyze(); err == nil {
		t.Fatal("blocking foreign function with suspend effect unexpectedly accepted")
	}

	foreignEdge := NewGraph()
	mustAddFunction(t, foreignEdge, FunctionSpec{ID: "caller"})
	mustAddFunction(t, foreignEdge, FunctionSpec{ID: "bad-target", Seed: MayPark})
	mustAddCall(t, foreignEdge, CallEdge{Caller: "caller", Callee: "bad-target", Kind: CallForeign})
	if _, err := foreignEdge.Analyze(); err == nil {
		t.Fatal("foreign edge to coroutine target unexpectedly accepted")
	}

	trustedInline := NewGraph()
	mustAddFunction(t, trustedInline, FunctionSpec{ID: "caller"})
	mustAddFunction(t, trustedInline, FunctionSpec{ID: "bad-target", External: ExternalKnown})
	mustAddCall(t, trustedInline, CallEdge{Caller: "caller", Callee: "bad-target", Kind: CallTrustedInline})
	if _, err := trustedInline.Analyze(); err == nil {
		t.Fatal("trusted-inline edge to a globally known target unexpectedly accepted")
	}

	unknownTrustedInline := NewGraph()
	mustAddFunction(t, unknownTrustedInline, FunctionSpec{ID: "caller"})
	if err := unknownTrustedInline.AddUnknownCall(UnknownCall{
		Caller: "caller", Kind: CallTrustedInline, Target: UnknownForeign,
	}); err == nil {
		t.Fatal("unknown trusted-inline call unexpectedly accepted")
	}
}

func TestAnalyzeValidationIsDeterministic(t *testing.T) {
	build := func(reverse bool) string {
		t.Helper()
		g := NewGraph()
		mustAddFunction(t, g, FunctionSpec{ID: "known"})
		edges := []CallEdge{
			{Caller: "missing-z", Callee: "known", Kind: CallDirect},
			{Caller: "missing-a", Callee: "known", Kind: CallDirect},
		}
		if reverse {
			reverseEdges(edges)
		}
		for _, edge := range edges {
			mustAddCall(t, g, edge)
		}
		_, err := g.Analyze()
		if err == nil {
			t.Fatal("invalid graph unexpectedly analyzed")
		}
		return err.Error()
	}
	if a, b := build(false), build(true); a != b {
		t.Fatalf("invalid graph diagnostic depends on insertion order: %q vs %q", a, b)
	}
}

func TestAnalyzeReferenceValidationIsDeterministic(t *testing.T) {
	build := func(reverse bool) string {
		t.Helper()
		g := NewGraph()
		mustAddFunction(t, g, FunctionSpec{ID: "known"})
		references := []ReferenceEdge{
			{Owner: "missing-z", Target: "known"},
			{Owner: "missing-a", Target: "known"},
		}
		if reverse {
			for i, j := 0, len(references)-1; i < j; i, j = i+1, j-1 {
				references[i], references[j] = references[j], references[i]
			}
		}
		for _, edge := range references {
			mustAddReference(t, g, edge)
		}
		_, err := g.Analyze()
		if err == nil {
			t.Fatal("invalid reference graph unexpectedly analyzed")
		}
		return err.Error()
	}
	if a, b := build(false), build(true); a != b || !strings.Contains(a, "missing-a") {
		t.Fatalf("reference diagnostic depends on insertion order: %q vs %q", a, b)
	}
}

func mustAddFunction(t *testing.T, g *Graph, spec FunctionSpec) {
	t.Helper()
	if err := g.AddFunction(spec); err != nil {
		t.Fatal(err)
	}
}

func mustAddCall(t *testing.T, g *Graph, edge CallEdge) {
	t.Helper()
	if err := g.AddCall(edge); err != nil {
		t.Fatal(err)
	}
}

func mustAddReference(t *testing.T, g *Graph, edge ReferenceEdge) {
	t.Helper()
	if err := g.AddReference(edge); err != nil {
		t.Fatal(err)
	}
}

func mustAddUnknownCall(t *testing.T, g *Graph, call UnknownCall) {
	t.Helper()
	if err := g.AddUnknownCall(call); err != nil {
		t.Fatal(err)
	}
}

func mustLookup(t *testing.T, plan *Plan, id FunctionID) FunctionPlan {
	t.Helper()
	fn, ok := plan.Lookup(id)
	if !ok {
		t.Fatalf("missing plan for %q", id)
	}
	return fn
}

func assertEffect(t *testing.T, plan *Plan, id FunctionID, want Effect) {
	t.Helper()
	got := mustLookup(t, plan, id).Effect
	want = want.Normalize()
	if got != want {
		t.Fatalf("%s effect = %s, want %s", id, got, want)
	}
}

func kindName(kind CallKind) string {
	switch kind {
	case CallDirect:
		return "direct"
	case CallDefer:
		return "defer"
	case CallSpawn:
		return "spawn"
	case CallForeign:
		return "foreign"
	case CallTrustedInline:
		return "trusted-inline"
	default:
		return "invalid"
	}
}

func unknownTargetName(target UnknownTarget) string {
	switch target {
	case UnknownManaged:
		return "managed"
	case UnknownForeign:
		return "foreign"
	case UnknownManagedDispatch:
		return "managed-dispatch"
	case UnknownManagedInterfaceDispatch:
		return "managed-interface-dispatch"
	default:
		return "invalid"
	}
}
