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

package build

import (
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"

	"github.com/goplus/llgo/cl"
	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
)

func TestCoroTLSDestructorClosedDynamicCallProof(t *testing.T) {
	fixture := buildRequiredCoroRuntimeFixture(t, coroTLSRuntimeFixtureSource(`func callback(*int) {}`))
	if len(fixture.closedDynamic) != 1 {
		t.Fatalf("TLS closed dynamic certificates = %d, want 1", len(fixture.closedDynamic))
	}
	if len(fixture.directPlain) != 1 {
		t.Fatalf("TLS direct-plain C callbacks = %d, want 1", len(fixture.directPlain))
	}
	callback := fixture.pkg.Func("callback")
	slotDestructor := fixture.pkg.Func("slotDestructor")
	var dynamicCall ssa.CallInstruction
	for call, certificate := range fixture.closedDynamic {
		dynamicCall = call
		if call.Parent() != slotDestructor || !certificate.MayBeNil || !certificate.SyncDispatch ||
			len(certificate.Targets) != 1 || certificate.Targets[0] != callback {
			t.Fatalf("TLS certificate = call:%v parent:%v certificate:%+v", call, call.Parent(), certificate)
		}
		if len(certificate.SyncOnlyCallArguments) != 1 {
			t.Fatalf("TLS synchronous publications = %+v, want one allocator argument", certificate.SyncOnlyCallArguments)
		}
		publication := certificate.SyncOnlyCallArguments[0]
		if publication.Call == nil || publication.Call.Common() == nil || publication.Argument < 0 ||
			publication.Argument >= len(publication.Call.Common().Args) {
			t.Fatalf("TLS synchronous publication = %+v, want one exact call argument", publication)
		}
		publishedTarget, exact := exactCoroStaticFunctionValue(fixture.ctx, publication.Call.Common().Args[publication.Argument])
		if !exact || publishedTarget != callback {
			t.Fatalf("TLS synchronous publication target = %v, exact=%t; want %v", publishedTarget, exact, callback)
		}
	}
	if use := fixture.directPlain[0]; use.target != slotDestructor {
		t.Fatalf("TLS direct-plain target = %v, want slotDestructor", use.target)
	}
	foundRoot := false
	for _, root := range fixture.roots {
		if root.Function == slotDestructor && root.ManagedDemand == coro.NoDemand && root.RawPlainDemand {
			foundRoot = true
			break
		}
	}
	if !foundRoot {
		t.Fatal("slotDestructor raw ABI entry was not retained as a raw-provenance root")
	}
	if _, required := fixture.requiredPlain[slotDestructor]; !required {
		t.Fatal("slotDestructor did not enter the exact scheduler-stack callback island")
	}
	if _, trusted := fixture.requiredPlain[callback]; trusted {
		t.Fatal("descriptor target callback incorrectly entered the trusted no-preempt island")
	}

	plan, err := fixture.analyze(coro.SSAConfig{MaxPlainInstructions: -1})
	if err != nil {
		t.Fatal(err)
	}
	callPlan, ok := plan.CallPlan(dynamicCall)
	if !ok || callPlan.Rep != coro.Dispatch || callPlan.Open || !callPlan.SyncDispatch || !callPlan.MayBeNil || len(callPlan.Targets) != 1 {
		t.Fatalf("TLS dynamic CallPlan = %+v, present=%t", callPlan, ok)
	}
	callbackPlan := functionPlanForBuildTest(t, plan, callback)
	if callbackPlan.Effect != coro.NoSuspend || callbackPlan.Exec.Contains(coro.NeedsPreempt) ||
		callbackPlan.FuncRep != coro.Dispatch || callbackPlan.Primary != coro.PrimaryPlain || callbackPlan.Emission != coro.EmitPlain {
		t.Fatalf("TLS callback plan = %+v, want descriptor-backed non-suspending plain body", callbackPlan)
	}
	destructorPlan := functionPlanForBuildTest(t, plan, slotDestructor)
	if destructorPlan.Effect != coro.NoSuspend || destructorPlan.Exec.Contains(coro.NeedsPreempt) ||
		destructorPlan.ManagedDemand != coro.NoDemand || !destructorPlan.RawPlainDemand || !destructorPlan.RawPlainOnly ||
		destructorPlan.FuncRep != coro.DirectPlain || destructorPlan.Emission != coro.EmitRawPlain {
		t.Fatalf("slotDestructor plan = %+v, want one exact raw-only C callback body", destructorPlan)
	}

	explicitFixture := fixture
	explicitPlan, err := explicitFixture.analyze(coro.SSAConfig{
		MaxPlainInstructions: -1,
		OutcomeMode:          coro.OutcomeExplicitStatus,
	})
	if err != nil {
		t.Fatal(err)
	}
	explicitCall, ok := explicitPlan.CallPlan(dynamicCall)
	if !ok || !explicitCall.SyncDispatch || explicitCall.Open || explicitCall.Rep != coro.Dispatch {
		t.Fatalf("explicit-status TLS CallPlan = %+v, present=%t", explicitCall, ok)
	}
	explicitDestructor := functionPlanForBuildTest(t, explicitPlan, slotDestructor)
	if explicitDestructor.LocalEffect != coro.NoSuspend || explicitDestructor.Effect != coro.NoSuspend ||
		explicitDestructor.Emission != coro.EmitRawPlain || !explicitDestructor.RawPlainOnly ||
		explicitDestructor.Primary != coro.PrimaryPlain {
		t.Fatalf("explicit-status slotDestructor plan = %+v, want uncolored raw-only plain callback", explicitDestructor)
	}

	_, err = fixture.analyze(coro.SSAConfig{
		MaxPlainInstructions: -1,
		ClassifyClosedDynamicCall: func(_ *ssa.Function, call ssa.CallInstruction) (coro.SSAClosedDynamicCallCertificate, bool, error) {
			if call != dynamicCall {
				return coro.SSAClosedDynamicCallCertificate{}, false, nil
			}
			return coro.SSAClosedDynamicCallCertificate{MayBeNil: true}, true, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "conflicts with the frozen compiler proof") {
		t.Fatalf("builder certificate override error = %v", err)
	}

	_, err = fixture.analyze(coro.SSAConfig{
		MaxPlainInstructions: -1,
		ClassifyClosedDynamicCall: func(_ *ssa.Function, call ssa.CallInstruction) (coro.SSAClosedDynamicCallCertificate, bool, error) {
			if call != dynamicCall {
				return coro.SSAClosedDynamicCallCertificate{}, false, nil
			}
			return coro.SSAClosedDynamicCallCertificate{MayBeNil: true}, false, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "facts without classifying") {
		t.Fatalf("builder unclassified certificate error = %v", err)
	}
}

func TestCoroTLSDestructorNilOnlyProof(t *testing.T) {
	fixture := buildRequiredCoroRuntimeFixture(t, coroTLSRuntimeFixtureSource(`
func install() {
	handle := Alloc(nil)
	handle.ensureSlot(new(slot))
}
`))
	if len(fixture.closedDynamic) != 1 || len(fixture.directPlain) != 1 {
		t.Fatalf("nil-only TLS proof = closed:%d direct:%d, want 1/1", len(fixture.closedDynamic), len(fixture.directPlain))
	}
	var dynamicCall ssa.CallInstruction
	for call, certificate := range fixture.closedDynamic {
		dynamicCall = call
		if !certificate.MayBeNil || !certificate.SyncDispatch || len(certificate.Targets) != 0 {
			t.Fatalf("nil-only TLS certificate = %+v", certificate)
		}
		if len(certificate.SyncOnlyCallArguments) != 1 {
			t.Fatalf("nil-only TLS synchronous publications = %+v, want one allocator argument", certificate.SyncOnlyCallArguments)
		}
		publication := certificate.SyncOnlyCallArguments[0]
		if publication.Call == nil || publication.Call.Common() == nil || publication.Argument < 0 ||
			publication.Argument >= len(publication.Call.Common().Args) ||
			!coroTLSNilFunctionValue(publication.Call.Common().Args[publication.Argument]) {
			t.Fatalf("nil-only TLS synchronous publication = %+v, want exact nil allocator argument", publication)
		}
	}
	plan, err := fixture.analyze(coro.SSAConfig{MaxPlainInstructions: -1})
	if err != nil {
		t.Fatal(err)
	}
	callPlan, ok := plan.CallPlan(dynamicCall)
	if !ok || callPlan.Rep != coro.Dispatch || callPlan.Open || !callPlan.SyncDispatch || !callPlan.MayBeNil || len(callPlan.Targets) != 0 {
		t.Fatalf("nil-only TLS CallPlan = %+v, present=%t", callPlan, ok)
	}
	if got := functionPlanForBuildTest(t, plan, fixture.pkg.Func("slotDestructor")); got.Effect != coro.NoSuspend || got.FuncRep != coro.DirectPlain {
		t.Fatalf("nil-only slotDestructor plan = %+v", got)
	}
}

func TestCoroTLSDestructorProofFailsClosed(t *testing.T) {
	tests := []struct {
		name  string
		extra string
		want  string
	}{
		{
			name: "unknown write",
			extra: `
func poison(s *slot, destructor func(*int)) { s.destructor = destructor }
func callback(*int) {}
`,
			want: "unknown non-nil write",
		},
		{
			name: "field address escape",
			extra: `
func leak(s *slot) unsafe.Pointer { return unsafe.Pointer(&s.destructor) }
func callback(*int) {}
`,
			want: "field address escapes",
		},
		{
			name: "unsafe aggregate write",
			extra: `
func poison(s *slot, destructor func(*int)) {
	ptr := unsafe.Pointer(s)
	*(*func(*int))(ptr) = destructor
}
func callback(*int) {}
`,
			want: "crosses unsafe conversion",
		},
		{
			name: "unknown opaque ingress",
			extra: `
func publish(ptr unsafe.Pointer) *slot { return (*slot)(ptr) }
func callback(*int) {}
`,
			want: "crosses unsafe conversion",
		},
		{
			name: "mutating root range helper",
			extra: `
func (s *slot) rootRange() unsafe.Pointer {
	ptr := unsafe.Pointer(s)
	*(*func(*int))(ptr) = callback
	return ptr
}
func callback(*int) {}
`,
			want: "crosses unsafe conversion",
		},
		{
			name: "named pointer escape",
			extra: `
type slotPointer *slot
func leak(s *slot) slotPointer { return slotPointer(s) }
func callback(*int) {}
`,
			want: "crosses named pointer conversion",
		},
		{
			name: "whole aggregate overwrite",
			extra: `
func overwrite(dst *slot, src slot) { *dst = src }
func callback(*int) {}
`,
			want: "whole-value write",
		},
		{
			name: "foreign pointer escape",
			extra: `
func foreign(*slot)
func publish(s *slot) { foreign(s) }
func callback(*int) {}
`,
			want: "non-Go callee",
		},
		{
			name: "interface escape",
			extra: `
func box(handle Handle) any { return handle }
func callback(*int) {}
`,
			want: "escapes through interface conversion",
		},
		{
			name: "multiple targets",
			extra: `
func callback(*int) {}
func other(*int) {}
func install() {
	first := Alloc(callback)
	first.ensureSlot(new(slot))
	second := Alloc(other)
	second.ensureSlot(new(slot))
}
`,
			want: "multiple non-nil targets",
		},
		{
			name: "captured target",
			extra: `
func install() {
	value := 1
	handle := Alloc(func(out *int) { *out = value })
	handle.ensureSlot(new(slot))
}
`,
			want: "not nil or one exact no-capture function",
		},
		{
			name: "open forwarded target",
			extra: `
func forward(destructor func(*int)) { _ = Alloc(destructor) }
func callback(*int) {}
func install() { go forward(callback) }
`,
			want: "not nil or one exact no-capture function",
		},
		{
			name: "allocator function escape",
			extra: `
var indirectAlloc = Alloc
func forward(destructor func(*int)) { _ = indirectAlloc(destructor) }
func callback(*int) {}
func install() {
	handle := Alloc(callback)
	handle.ensureSlot(new(slot))
}
`,
			want: "allocator function escapes",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := buildCoroTLSRuntimePlanError(t, coroTLSRuntimeFixtureSource(test.extra))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("TLS proof error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestCoroTLSDestructorTargetMustRemainAtomic(t *testing.T) {
	for _, test := range []struct {
		name     string
		callback string
	}{
		{name: "suspends", callback: `var channel chan int; func callback(*int) { <-channel }`},
		{name: "needs preemption", callback: `func callback(*int) { for {} }`},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := buildRequiredCoroRuntimeFixture(t, coroTLSRuntimeFixtureSource(test.callback))
			_, err := fixture.analyze(coro.SSAConfig{MaxPlainInstructions: -1})
			if err == nil || (!strings.Contains(err.Error(), "non-suspending plain body") &&
				!strings.Contains(err.Error(), "non-suspending descriptor-backed plain body") &&
				!strings.Contains(err.Error(), "non-suspending plain primary") &&
				!strings.Contains(err.Error(), "non-suspending descriptor-backed plain primary")) {
				t.Fatalf("non-atomic TLS destructor error = %v", err)
			}
		})
	}
}

func TestCoroTLSDestructorDirectPlainClosureFrozenCLeaf(t *testing.T) {
	source := coroTLSRuntimeFixtureSource(`
func hiddenFallback() {}
func callback(*int) {}
func ordinaryCaller() { ordinaryC() }
`)
	source = strings.Replace(source, "func slotDestructor(dst *slot) {", `
//llgo:link tlsCLeaf C.tls_c_leaf
func tlsCLeaf() { hiddenFallback() }

//llgo:link ordinaryC C.ordinary_c
func ordinaryC()

func slotDestructor(dst *slot) {
	tlsCLeaf()
`, 1)
	fixture := buildRequiredCoroRuntimeFixture(t, source)
	if len(fixture.directPlain) != 1 {
		t.Fatalf("TLS direct-plain callbacks = %d, want 1", len(fixture.directPlain))
	}
	slotDestructor := fixture.pkg.Func("slotDestructor")
	tlsCLeaf := fixture.pkg.Func("tlsCLeaf")
	if use := fixture.directPlain[0]; use.target != slotDestructor {
		t.Fatalf("TLS direct-plain target = %v, want slotDestructor", use.target)
	}
	if _, required := fixture.requiredPlain[tlsCLeaf]; !required {
		t.Fatal("exact frozen TLS C leaf did not enter the required plain island")
	}
	if _, required := fixture.requiredPlain[fixture.pkg.Func("hiddenFallback")]; required {
		t.Fatal("ignored C fallback body leaked into the required plain island")
	}
	if _, required := fixture.requiredPlain[fixture.pkg.Func("ordinaryC")]; required {
		t.Fatal("ordinary external C declaration entered the required plain island")
	}

	plan, err := fixture.analyze(coro.SSAConfig{MaxPlainInstructions: -1})
	if err != nil {
		t.Fatal(err)
	}
	callbackPlan := functionPlanForBuildTest(t, plan, slotDestructor)
	if callbackPlan.External != coro.Defined || callbackPlan.Effect != coro.WaitForeign || callbackPlan.Exec.Contains(coro.NeedsPreempt) ||
		callbackPlan.ManagedDemand != coro.NoDemand || !callbackPlan.RawPlainDemand || !callbackPlan.RawPlainOnly ||
		callbackPlan.FuncRep != coro.DirectPlain || callbackPlan.Primary != coro.PrimaryPlain || callbackPlan.Emission != coro.EmitRawPlain {
		t.Fatalf("TLS callback plan = %+v, want a raw-only body retaining the managed foreign-wait summary", callbackPlan)
	}
	leafPlan := functionPlanForBuildTest(t, plan, tlsCLeaf)
	if !plan.IgnoresBody(tlsCLeaf) || leafPlan.External != coro.ExternalUnknownForeign || leafPlan.Effect != coro.NoSuspend ||
		leafPlan.Exec != coro.BlockForeign|coro.IRQUnsafe || leafPlan.FuncRep != coro.DirectPlain || leafPlan.Emission != coro.EmitExternal {
		t.Fatalf("TLS C leaf plan = %+v, ignored=%t; want the conservative default foreign declaration", leafPlan, plan.IgnoresBody(tlsCLeaf))
	}
	ordinaryC := functionPlanForBuildTest(t, plan, fixture.pkg.Func("ordinaryC"))
	if ordinaryC.External != coro.ExternalUnknownForeign || !ordinaryC.Exec.Contains(coro.BlockForeign|coro.IRQUnsafe) {
		t.Fatalf("ordinary C declaration plan = %+v, want unknown foreign", ordinaryC)
	}
}

func TestCoroTLSDestructorDirectPlainClosureCLeafPolicy(t *testing.T) {
	t.Run("user callback on raw caller stack", func(t *testing.T) {
		fixture := buildRequiredCoroRuntimeFixture(t, coroTLSRuntimeFixtureSource(`
//llgo:link userCLeaf C.user_c_leaf
func userCLeaf()
func callback(*int) {}
func userCallback(*slot) { userCLeaf() }
func install() {
	handle := Alloc(callback)
	handle.ensureSlot(new(slot))
	installC(CCallback(userCallback))
}
`))
		slotDestructor := fixture.pkg.Func("slotDestructor")
		userCallback := fixture.pkg.Func("userCallback")
		callbacks := make(map[*ssa.Function]bool, len(fixture.directPlain))
		for _, use := range fixture.directPlain {
			callbacks[use.target] = true
		}
		if len(fixture.directPlain) != 2 || !callbacks[slotDestructor] || !callbacks[userCallback] {
			t.Fatalf("direct-plain callbacks = %+v, want slotDestructor and userCallback", fixture.directPlain)
		}
		if _, required := fixture.requiredPlain[userCallback]; !required {
			t.Fatal("raw C callback did not enter its exact required plain closure")
		}
		userCLeaf := fixture.pkg.Func("userCLeaf")
		if _, required := fixture.requiredPlain[userCLeaf]; required {
			t.Fatal("default may-block C leaf entered requiredPlain instead of retaining its foreign contract")
		}
		closure, ok, err := provenCoroDirectPlainStaticClosure(
			fixture.ctx, userCallback, fixture.closedDynamic,
		)
		if err != nil || !ok || len(closure) != 1 || closure[0] != userCallback {
			t.Fatalf("user callback closure proof = closure:%v ok:%t err:%v, want only userCallback", closure, ok, err)
		}
		plan, err := fixture.analyze(coro.SSAConfig{MaxPlainInstructions: -1})
		if err != nil {
			t.Fatalf("raw callback with non-reentrant may-block C leaf: %v", err)
		}
		callbackPlan := functionPlanForBuildTest(t, plan, userCallback)
		if callbackPlan.External != coro.Defined || !callbackPlan.RawPlainDemand ||
			!callbackPlan.RawPlainOnly || callbackPlan.Emission != coro.EmitRawPlain {
			t.Fatalf("user callback plan = %+v, want an exact raw-only plain body", callbackPlan)
		}
		leafPlan := functionPlanForBuildTest(t, plan, userCLeaf)
		if leafPlan.External != coro.ExternalUnknownForeign ||
			!leafPlan.Exec.Contains(coro.BlockForeign|coro.IRQUnsafe) ||
			leafPlan.Emission != coro.EmitExternal {
			t.Fatalf("user callback C leaf plan = %+v, want the conservative foreign declaration policy", leafPlan)
		}
	})

	t.Run("non C declaration", func(t *testing.T) {
		source := coroTLSRuntimeFixtureSource(`
func unknownManaged()
func callback(*int) {}
`)
		source = strings.Replace(source, "func slotDestructor(dst *slot) {", `func slotDestructor(dst *slot) {
	unknownManaged()
`, 1)
		fixture := buildRequiredCoroRuntimeFixture(t, source)
		if len(fixture.closedDynamic) != 1 {
			t.Fatalf("TLS closed dynamic certificates = %d, want 1", len(fixture.closedDynamic))
		}
		if len(fixture.directPlain) != 0 {
			t.Fatalf("unknown managed leaf produced direct-plain callback uses: %+v", fixture.directPlain)
		}
		if _, required := fixture.requiredPlain[fixture.pkg.Func("unknownManaged")]; required {
			t.Fatal("non-C declaration entered the required plain island")
		}
		if _, ok, err := provenCoroDirectPlainStaticClosure(fixture.ctx, fixture.pkg.Func("slotDestructor"), fixture.closedDynamic); err != nil || ok {
			t.Fatalf("non-C leaf closure proof = ok:%t err:%v, want false/nil", ok, err)
		}
	})
}

func coroTLSRuntimeFixtureSource(extra string) string {
	base := `
//llgo:type C
type CCallback func(*slot)

func installC(CCallback) {}

type Handle struct { destructor func(*int) }
type slot struct {
	value int
	destructor func(*int)
}

func Alloc(destructor func(*int)) Handle {
	installC(CCallback(slotDestructor))
	var handle Handle
	handle.destructor = destructor
	return handle
}

func (handle Handle) ensureSlot(dst *slot) {
	dst.destructor = handle.destructor
}

func slotDestructor(dst *slot) {
	if dst.destructor != nil {
		dst.destructor(&dst.value)
	}
	dst.destructor = nil
}
`
	if strings.Contains(extra, "func install()") {
		return base + extra
	}
	return base + extra + `
func install() {
	handle := Alloc(callback)
	handle.ensureSlot(new(slot))
}
`
}

func buildCoroTLSRuntimePlanError(t *testing.T, body string) error {
	t.Helper()
	source := "package runtime\nimport \"unsafe\"\n"
	source += `
func __llgo_coro_program_begin_v1() { install() }
func __llgo_coro_program_run_v1() {}
func __llgo_coro_program_continue_v1(uint32) {}
func __llgo_coro_frame_allocator_bootstrap_v1() {}
func __llgo_coro_frame_alloc_v1() {}
func __llgo_coro_frame_publish_v1() {}
func __llgo_coro_frame_publish_v3() {}
func __llgo_coro_frame_destroy_commit_v2() {}
func __llgo_coro_await_prepare_v1() {}
func __llgo_coro_preempt_poll_v1() bool { return false }
func __llgo_coro_yield_prepare_v1() {}
func __llgo_coro_run_decision_take_v1(unsafe.Pointer, uint32, uint32, *uint32, *uint32, *uint32, *uint32, *uint32) {}
func __llgo_coro_run_decision_take_zero_v1(unsafe.Pointer) uint32 { return 0 }
func __llgo_coro_complete_prepare_v1() {}
func __llgo_coro_frame_free_v1() {}
` + body
	ssaPkg, files := buildCoroPlanTestPackage(t, llssa.PkgRuntime, source, nil)
	prog := llssa.NewProgram(nil)
	t.Cleanup(prog.Dispose)
	cl.ParsePkgSyntax(prog, ssaPkg.Prog.Fset, ssaPkg.Pkg, files)
	emission, err := cl.PrepareEmissionUniverse(prog, nil, []cl.EmissionPackage{{
		SSA: ssaPkg, Files: files, Identity: llssa.PkgRuntime,
	}})
	if err != nil {
		t.Fatal(err)
	}
	ssaEmission, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, emission.Functions())
	if err != nil {
		t.Fatal(err)
	}
	ctx := &context{
		prog:                        prog,
		buildConf:                   &Config{},
		coroEmission:                emission,
		coroSSAEmission:             ssaEmission,
		coroTLSDestructorFixturePkg: llssa.PkgRuntime,
	}
	_, _, _, _, err = requiredCoroProgramRuntimePlan(ctx)
	return err
}
