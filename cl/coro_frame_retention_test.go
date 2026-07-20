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
	"bytes"
	"go/ast"
	"go/types"
	"regexp"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	llssa "github.com/goplus/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

func TestCoroFrameRetentionNilConstRecognizesUnsafePointer(t *testing.T) {
	value := ssa.NewConst(nil, types.Typ[types.UnsafePointer])
	if !coroFrameRetentionNilConst(value) {
		t.Fatalf("unsafe.Pointer zero constant %v was not recognized as nil", value)
	}
}

const coroFrameRetentionFixture = `package foo

import "unsafe"

type WaitToken struct { word uint32 }

//llgo:coro noblock
//go:linkname prepare C.__llgo_coro_timer_prepare_after_or_abort_v1
func prepare(unsafe.Pointer, int64, *uint32, *uint32, *uint32)

//go:linkname park llgo.coroPark
func park(*WaitToken, uint32)

//llgo:coro noblock
//go:linkname retire C.__llgo_coro_timer_retire_completed_or_abort_v1
func retire(unsafe.Pointer, uint32, uint32, uint32)

func Root(delay int64) {
	var token WaitToken
	var ticket, slot, generation uint32
	prepare(unsafe.Pointer(&token), delay, &ticket, &slot, &generation)
	park(&token, ticket)
	retire(unsafe.Pointer(&token), ticket, slot, generation)
}
`

const coroSemaphoreFrameRetentionFixture = `package foo

import "unsafe"

type WaitToken struct { word uint32 }

//llgo:coro noblock
//go:linkname prepare C.__llgo_coro_sema_prepare_or_abort_v1
func prepare(unsafe.Pointer, unsafe.Pointer, *uint32, *uint32, *uint32)

//go:linkname park llgo.coroPark
func park(*WaitToken, uint32)

//llgo:coro noblock
//go:linkname retire C.__llgo_coro_sema_retire_completed_or_abort_v1
func retire(unsafe.Pointer, uint32, uint32, uint32)

func Root(addr *uint32) uint32 {
	if addr == nil {
		return 0
	}
	var token WaitToken
	var ticket, slot, generation uint32
	prepare(unsafe.Pointer(&token), unsafe.Pointer(addr), &ticket, &slot, &generation)
	park(&token, ticket)
	retire(unsafe.Pointer(&token), ticket, slot, generation)
	return *addr
}
`

const coroNotifyFrameRetentionFixture = `package foo

import "unsafe"

type WaitToken struct { word uint32 }
type Notify struct {
	wait, notify uint32
	lock uintptr
	head, tail unsafe.Pointer
}

//llgo:coro noblock
//go:linkname prepare C.__llgo_coro_notify_prepare_or_abort_v1
func prepare(unsafe.Pointer, unsafe.Pointer, uint32, *uint32, *uint32, *uint32)

//go:linkname park llgo.coroPark
func park(*WaitToken, uint32)

//llgo:coro noblock
//go:linkname retire C.__llgo_coro_notify_retire_completed_or_abort_v1
func retire(unsafe.Pointer, uint32, uint32, uint32)

func Root(list *Notify, target uint32) uint32 {
	if list == nil {
		return 0
	}
	if int32(target-list.notify) < 0 {
		return list.notify
	}
	var token WaitToken
	var ticket, slot, generation uint32
	prepare(unsafe.Pointer(&token), unsafe.Pointer(&list.notify), target, &ticket, &slot, &generation)
	park(&token, ticket)
	retire(unsafe.Pointer(&token), ticket, slot, generation)
	return list.notify
}
`

func TestCoroParkFrameRetentionContractTableIsSourceGeneric(t *testing.T) {
	tests := []struct {
		name            string
		source          string
		abi             string
		wantAllocations int
		wantRoles       int
	}{
		{name: "timer remains supported by park v2", source: coroFrameRetentionFixture, abi: CoroFrameRetentionParkABIV2, wantAllocations: 4, wantRoles: 3},
		{name: "sync certificate supports timer transaction", source: strings.ReplaceAll(coroFrameRetentionFixture, "//llgo:coro noblock", "//llgo:coro sync"), abi: CoroFrameRetentionParkABIV2, wantAllocations: 4, wantRoles: 3},
		{name: "semaphore is supported by park v2", source: coroSemaphoreFrameRetentionFixture, abi: CoroFrameRetentionParkABIV2, wantAllocations: 4, wantRoles: 3},
		{name: "notify is supported by park v2", source: coroNotifyFrameRetentionFixture, abi: CoroFrameRetentionParkABIV2, wantAllocations: 4, wantRoles: 3},
		{name: "timer v1 cannot authorize semaphore", source: coroSemaphoreFrameRetentionFixture, abi: CoroFrameRetentionTimerABIV1},
		{name: "timer v1 cannot authorize notify", source: coroNotifyFrameRetentionFixture, abi: CoroFrameRetentionTimerABIV1},
		{
			name: "notify target type is exact",
			source: strings.NewReplacer(
				"func prepare(unsafe.Pointer, unsafe.Pointer, uint32, *uint32, *uint32, *uint32)",
				"func prepare(unsafe.Pointer, unsafe.Pointer, int32, *uint32, *uint32, *uint32)",
				"unsafe.Pointer(&list.notify), target, &ticket",
				"unsafe.Pointer(&list.notify), int32(target), &ticket",
			).Replace(coroNotifyFrameRetentionFixture),
			abi: CoroFrameRetentionParkABIV2,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prog, ssaPkg, _, _, proof := prepareCoroFrameRetentionProof(t, test.source, test.abi)
			defer prog.Dispose()
			if len(proof.allocations) != test.wantAllocations || len(proof.roles) != test.wantRoles {
				var dump bytes.Buffer
				ssa.WriteFunction(&dump, ssaPkg.Func("Root"))
				t.Fatalf("proof = %d allocations/%d roles, want %d/%d\n%s",
					len(proof.allocations), len(proof.roles), test.wantAllocations, test.wantRoles, dump.String())
			}
			for instruction := range proof.roles {
				if proof.contracts[instruction] == "" {
					t.Fatal("proved park role has no frozen source contract")
				}
			}
		})
	}
}

func TestCoroNotifyCurrentFrameRetentionLowersThroughGenericParkContract(t *testing.T) {
	prog, ssaPkg, files, universe, proof := prepareCoroFrameRetentionProof(
		t, coroNotifyFrameRetentionFixture, CoroFrameRetentionParkABIV2,
	)
	defer prog.Dispose()
	if len(proof.allocations) != 4 || len(proof.roles) != 3 {
		t.Fatalf("notify transaction proof = %d allocations/%d roles, want 4/3", len(proof.allocations), len(proof.roles))
	}
	root := ssaPkg.Func("Root")
	rootedList := false
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok || call.Common() == nil || call.Common().StaticCallee() == nil ||
				call.Common().StaticCallee().Name() != "prepare" {
				continue
			}
			for _, retained := range proof.exactCallKeepaliveRoots(call) {
				rootedList = rootedList || retained == root.Params[0]
			}
		}
	}
	if !rootedList {
		t.Fatal("notify prepare owner did not retain its typed notifyList root")
	}
	plan := analyzeCoroFrameRetentionFixture(t, ssaPkg, universe, root, 1)
	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe, CoroFrameRetentionABI: CoroFrameRetentionParkABIV2}
	enableCoroPreemptCompilation(compilation)
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{}, PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatal(err)
	}
	module := pkg.Module()
	defer module.Dispose()
	body := requireCoroPhysicalFunction(t, module, "foo.Root").String()
	prepare := strings.Index(body, "call void @"+coroNotifyPrepareOrAbortSymbolV1)
	retire := strings.Index(body, "call void @"+coroNotifyRetireCompletedOrAbortSymbolV1)
	if prepare < 0 || retire <= prepare || strings.Contains(body, "AllocZ") ||
		!strings.Contains(body, "alloca %foo.WaitToken") || strings.Count(body, "alloca i32") < 3 {
		t.Fatalf("generic notify transaction did not lower into the coroutine frame:\n%s", body)
	}
	span := body[prepare:retire]
	if strings.Contains(span, "call i1 @"+coroPreemptPollHookV1) ||
		strings.Contains(span, "call void @"+coroYieldPrepareHookV1) ||
		strings.Count(span, "call void @"+coroParkPrepareHookV1) != 1 ||
		strings.Count(span, "call i8 @llvm.coro.suspend") != 1 {
		t.Fatalf("generic notify retained span has an unsafe suspension shape:\n%s", span)
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify generic notify transaction before CoroSplit: %v\n%s", err, module.String())
	}
	runCoroABITestPipeline(t, prog, module)
	post := module.String()
	if strings.Contains(post, "AllocZ") || !strings.Contains(post, coroNotifyPrepareOrAbortSymbolV1) ||
		!strings.Contains(post, coroNotifyRetireCompletedOrAbortSymbolV1) || module.NamedFunction("foo.Root$coro.resume").IsNil() {
		t.Fatalf("CoroSplit lost the generic notify frame transaction:\n%s", post)
	}
}

func TestCoroSemaphoreCurrentFrameRetentionLowersThroughGenericParkContract(t *testing.T) {
	prog, ssaPkg, files, universe, proof := prepareCoroFrameRetentionProof(
		t, coroSemaphoreFrameRetentionFixture, CoroFrameRetentionParkABIV2,
	)
	defer prog.Dispose()
	if len(proof.allocations) != 4 || len(proof.roles) != 3 {
		t.Fatalf("semaphore transaction proof = %d allocations/%d roles, want 4/3", len(proof.allocations), len(proof.roles))
	}
	root := ssaPkg.Func("Root")
	rootedAddr := false
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok || call.Common() == nil || call.Common().StaticCallee() == nil ||
				call.Common().StaticCallee().Name() != "prepare" {
				continue
			}
			for _, retained := range proof.exactCallKeepaliveRoots(call) {
				rootedAddr = rootedAddr || retained == root.Params[0]
			}
		}
	}
	if !rootedAddr {
		t.Fatal("semaphore prepare owner did not retain its typed counter root")
	}
	plan := analyzeCoroFrameRetentionFixture(t, ssaPkg, universe, root, 1)
	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe, CoroFrameRetentionABI: CoroFrameRetentionParkABIV2}
	enableCoroPreemptCompilation(compilation)
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{}, PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatal(err)
	}
	module := pkg.Module()
	defer module.Dispose()
	body := requireCoroPhysicalFunction(t, module, "foo.Root").String()
	prepare := strings.Index(body, "call void @"+coroSemaphorePrepareOrAbortSymbolV1)
	retire := strings.Index(body, "call void @"+coroSemaphoreRetireCompletedOrAbortSymbolV1)
	if prepare < 0 || retire <= prepare || strings.Contains(body, "AllocZ") ||
		!strings.Contains(body, "alloca %foo.WaitToken") || strings.Count(body, "alloca i32") < 3 {
		t.Fatalf("generic semaphore transaction did not lower into the coroutine frame:\n%s", body)
	}
	span := body[prepare:retire]
	if strings.Contains(span, "call i1 @"+coroPreemptPollHookV1) ||
		strings.Contains(span, "call void @"+coroYieldPrepareHookV1) ||
		strings.Count(span, "call void @"+coroParkPrepareHookV1) != 1 ||
		strings.Count(span, "call i8 @llvm.coro.suspend") != 1 {
		t.Fatalf("generic semaphore retained span has an unsafe suspension shape:\n%s", span)
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify generic semaphore transaction before CoroSplit: %v\n%s", err, module.String())
	}
	runCoroABITestPipeline(t, prog, module)
	post := module.String()
	if strings.Contains(post, "AllocZ") || !strings.Contains(post, coroSemaphorePrepareOrAbortSymbolV1) ||
		!strings.Contains(post, coroSemaphoreRetireCompletedOrAbortSymbolV1) || module.NamedFunction("foo.Root$coro.resume").IsNil() {
		t.Fatalf("CoroSplit lost the generic semaphore frame transaction:\n%s", post)
	}
}

func TestCoroCurrentFrameRetentionProofIsExact(t *testing.T) {
	tests := []struct {
		name   string
		source string
		abi    string
	}{
		{name: "ABI not selected", source: coroFrameRetentionFixture},
		{name: "unknown ABI identity", source: coroFrameRetentionFixture, abi: CoroFrameRetentionTimerABIV1 + ".unknown"},
		{
			name: "old bool owner ABI",
			source: strings.NewReplacer(
				"C.__llgo_coro_timer_prepare_after_or_abort_v1", "C.__llgo_coro_timer_prepare_after_v1",
				"C.__llgo_coro_timer_retire_completed_or_abort_v1", "C.__llgo_coro_timer_retire_completed_v1",
				"func prepare(unsafe.Pointer, int64, *uint32, *uint32, *uint32)", "func prepare(unsafe.Pointer, int64, *uint32, *uint32, *uint32) bool",
				"func retire(unsafe.Pointer, uint32, uint32, uint32)", "func retire(unsafe.Pointer, uint32, uint32, uint32) bool",
				"prepare(unsafe.Pointer(&token), delay, &ticket, &slot, &generation)", "_ = prepare(unsafe.Pointer(&token), delay, &ticket, &slot, &generation)",
				"retire(unsafe.Pointer(&token), ticket, slot, generation)", "_ = retire(unsafe.Pointer(&token), ticket, slot, generation)",
			).Replace(coroFrameRetentionFixture),
			abi: CoroFrameRetentionTimerABIV1,
		},
		{
			name: "typed owner token parameter",
			source: strings.NewReplacer(
				"import \"unsafe\"", "import _ \"unsafe\"",
				"func prepare(unsafe.Pointer, int64, *uint32, *uint32, *uint32)", "func prepare(*WaitToken, int64, *uint32, *uint32, *uint32)",
				"func retire(unsafe.Pointer, uint32, uint32, uint32)", "func retire(*WaitToken, uint32, uint32, uint32)",
				"prepare(unsafe.Pointer(&token), delay", "prepare(&token, delay",
				"retire(unsafe.Pointer(&token), ticket", "retire(&token, ticket",
			).Replace(coroFrameRetentionFixture),
			abi: CoroFrameRetentionTimerABIV1,
		},
		{
			name: "defined owner output pointer parameter",
			source: strings.NewReplacer(
				"type WaitToken struct { word uint32 }", "type WaitToken struct { word uint32 }\ntype WordPtr *uint32",
				"func prepare(unsafe.Pointer, int64, *uint32, *uint32, *uint32)", "func prepare(unsafe.Pointer, int64, WordPtr, *uint32, *uint32)",
				"delay, &ticket, &slot", "delay, WordPtr(&ticket), &slot",
			).Replace(coroFrameRetentionFixture),
			abi: CoroFrameRetentionTimerABIV1,
		},
		{
			name:   "missing frozen noblock certificate",
			source: strings.Replace(coroFrameRetentionFixture, "//llgo:coro noblock\n//go:linkname prepare", "// ordinary declaration\n//go:linkname prepare", 1),
			abi:    CoroFrameRetentionTimerABIV1,
		},
		{
			name: "wrong prepare result",
			source: strings.Replace(
				strings.Replace(coroFrameRetentionFixture, "func prepare(unsafe.Pointer, int64, *uint32, *uint32, *uint32)", "func prepare(unsafe.Pointer, int64, *uint32, *uint32, *uint32) bool", 1),
				"prepare(unsafe.Pointer(&token), delay, &ticket, &slot, &generation)", "_ = prepare(unsafe.Pointer(&token), delay, &ticket, &slot, &generation)", 1,
			),
			abi: CoroFrameRetentionTimerABIV1,
		},
		{
			name: "managed pointer token",
			source: strings.Replace(coroFrameRetentionFixture,
				"type WaitToken struct { word uint32 }", "type WaitToken struct { word uint32; pointer *byte }", 1),
			abi: CoroFrameRetentionTimerABIV1,
		},
		{
			name: "wrong token word shape",
			source: strings.Replace(coroFrameRetentionFixture,
				"type WaitToken struct { word uint32 }", "type WaitToken struct { word uint16 }", 1),
			abi: CoroFrameRetentionTimerABIV1,
		},
		{
			name: "token field address use",
			source: strings.Replace(coroFrameRetentionFixture,
				"prepare(unsafe.Pointer(&token), delay", "token.word = 1\n\tprepare(unsafe.Pointer(&token), delay", 1),
			abi: CoroFrameRetentionTimerABIV1,
		},
		{
			name: "output store",
			source: strings.Replace(coroFrameRetentionFixture,
				"prepare(unsafe.Pointer(&token), delay", "ticket = 1\n\tprepare(unsafe.Pointer(&token), delay", 1),
			abi: CoroFrameRetentionTimerABIV1,
		},
		{
			name: "extra output load",
			source: strings.Replace(
				strings.Replace(coroFrameRetentionFixture, "func Root(delay int64)", "var sink uint32\n\nfunc Root(delay int64)", 1),
				"\tretire(unsafe.Pointer(&token), ticket, slot, generation)", "\tretire(unsafe.Pointer(&token), ticket, slot, generation)\n\tsink = ticket", 1,
			),
			abi: CoroFrameRetentionTimerABIV1,
		},
		{
			name: "extra scalar use of exact output load",
			source: strings.Replace(
				strings.Replace(
					strings.Replace(coroFrameRetentionFixture, "func Root(delay int64)", "var sink uint32\n\nfunc Root(delay int64)", 1),
					"\tpark(&token, ticket)", "\tvalue := ticket\n\tpark(&token, value)", 1,
				),
				"\tretire(unsafe.Pointer(&token), ticket, slot, generation)", "\tretire(unsafe.Pointer(&token), ticket, slot, generation)\n\tsink = value", 1,
			),
			abi: CoroFrameRetentionTimerABIV1,
		},
		{
			name: "numeric scalar conversion",
			source: strings.NewReplacer(
				"type WaitToken struct { word uint32 }", "type WaitToken struct { word uint32 }\ntype WaitTicket uint32",
				"func park(*WaitToken, uint32)", "func park(*WaitToken, WaitTicket)",
				"park(&token, ticket)", "park(&token, WaitTicket(ticket))",
			).Replace(coroFrameRetentionFixture),
			abi: CoroFrameRetentionTimerABIV1,
		},
		{
			name: "mismatched ticket",
			source: strings.Replace(coroFrameRetentionFixture,
				"park(&token, ticket)", "park(&token, slot)", 1),
			abi: CoroFrameRetentionTimerABIV1,
		},
		{
			name: "missing retire",
			source: strings.Replace(coroFrameRetentionFixture,
				"\tretire(unsafe.Pointer(&token), ticket, slot, generation)\n", "", 1),
			abi: CoroFrameRetentionTimerABIV1,
		},
		{
			name: "cross block early termination",
			source: strings.Replace(coroFrameRetentionFixture,
				"\tpark(&token, ticket)", "\tif delay < 0 { return }\n\tpark(&token, ticket)", 1),
			abi: CoroFrameRetentionTimerABIV1,
		},
		{
			name: "ordinary call in retained span",
			source: strings.Replace(
				strings.Replace(coroFrameRetentionFixture, "func Root(delay int64)", "func touch() {}\n\nfunc Root(delay int64)", 1),
				"\tpark(&token, ticket)", "\ttouch()\n\tpark(&token, ticket)", 1,
			),
			abi: CoroFrameRetentionTimerABIV1,
		},
		{
			name: "extra address call",
			source: strings.Replace(
				strings.Replace(coroFrameRetentionFixture, "func Root(delay int64)", "func inspect(*WaitToken) {}\n\nfunc Root(delay int64)", 1),
				"\tretire(unsafe.Pointer(&token), ticket, slot, generation)", "\tretire(unsafe.Pointer(&token), ticket, slot, generation)\n\tinspect(&token)", 1,
			),
			abi: CoroFrameRetentionTimerABIV1,
		},
		{
			name: "sequential reuse",
			source: strings.Replace(coroFrameRetentionFixture,
				"\tretire(unsafe.Pointer(&token), ticket, slot, generation)",
				"\tretire(unsafe.Pointer(&token), ticket, slot, generation)\n\tprepare(unsafe.Pointer(&token), delay, &ticket, &slot, &generation)\n\tpark(&token, ticket)\n\tretire(unsafe.Pointer(&token), ticket, slot, generation)", 1),
			abi: CoroFrameRetentionTimerABIV1,
		},
		{
			name: "dynamic prepare call",
			source: strings.Replace(
				strings.Replace(coroFrameRetentionFixture, "func Root(delay int64)", "var prepareValue = prepare\n\nfunc Root(delay int64)", 1),
				"\tprepare(unsafe.Pointer(&token), delay", "\tprepareValue(unsafe.Pointer(&token), delay", 1),
			abi: CoroFrameRetentionTimerABIV1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prog, _, _, _, proof := prepareCoroFrameRetentionProof(t, test.source, test.abi)
			defer prog.Dispose()
			if len(proof.allocations) != 0 || len(proof.roles) != 0 {
				t.Fatalf("invalid transaction proof = %d allocations, %d roles; want empty", len(proof.allocations), len(proof.roles))
			}
		})
	}
}

func TestCoroCurrentFrameRetentionRejectsUnmatchedOwnerCalls(t *testing.T) {
	const root = `func Root(delay int64) {
	var token WaitToken
	var ticket, slot, generation uint32
	prepare(unsafe.Pointer(&token), delay, &ticket, &slot, &generation)
	park(&token, ticket)
	retire(unsafe.Pointer(&token), ticket, slot, generation)
}`
	globalRoot := `var globalToken WaitToken
var globalTicket, globalSlot, globalGeneration uint32

func Root(delay int64) {
	prepare(unsafe.Pointer(&globalToken), delay, &globalTicket, &globalSlot, &globalGeneration)
	park(&globalToken, globalTicket)
	retire(unsafe.Pointer(&globalToken), globalTicket, globalSlot, globalGeneration)
}`
	extraPrepare := `var extraToken WaitToken
var extraTicket, extraSlot, extraGeneration uint32

` + strings.Replace(root,
		"\tretire(unsafe.Pointer(&token), ticket, slot, generation)",
		"\tretire(unsafe.Pointer(&token), ticket, slot, generation)\n\tprepare(unsafe.Pointer(&extraToken), delay, &extraTicket, &extraSlot, &extraGeneration)", 1,
	)
	tests := []struct {
		name            string
		source          string
		wantAllocations int
		wantRoles       int
	}{
		{
			name:            "global token and outputs",
			source:          strings.Replace(coroFrameRetentionFixture, root, globalRoot, 1),
			wantAllocations: 0,
			wantRoles:       0,
		},
		{
			name:            "extra unmatched prepare",
			source:          strings.Replace(coroFrameRetentionFixture, root, extraPrepare, 1),
			wantAllocations: 4,
			wantRoles:       3,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prog, ssaPkg, _, universe, proof := prepareCoroFrameRetentionProof(
				t, test.source, CoroFrameRetentionTimerABIV1,
			)
			defer prog.Dispose()
			if len(proof.allocations) != test.wantAllocations || len(proof.roles) != test.wantRoles {
				t.Fatalf("proof = %d allocations/%d roles, want %d/%d",
					len(proof.allocations), len(proof.roles), test.wantAllocations, test.wantRoles)
			}
			rootFn := ssaPkg.Func("Root")
			plan := analyzeCoroFrameRetentionFixture(t, ssaPkg, universe, rootFn, 1)
			rootPlan, ok := plan.FunctionPlan(rootFn)
			if !ok {
				t.Fatal("Root is absent from the coroutine plan")
			}
			err := validateCoroPhysicalABIWithUniverseCapabilitiesAndFrameRetention(
				rootFn, rootPlan, plan, universe, true, true, false, false, CoroFrameRetentionTimerABIV1,
			)
			if err == nil || !strings.Contains(err.Error(), "exact frame-retention owner call is outside a certified prepare/park/retire transaction") {
				t.Fatalf("unmatched owner preflight error = %v", err)
			}
		})
	}
}

func TestCoroCurrentFrameRetentionLowersToCoroFrame(t *testing.T) {
	prog, ssaPkg, files, universe, proof := prepareCoroFrameRetentionProof(
		t, coroFrameRetentionFixture, CoroFrameRetentionTimerABIV1,
	)
	defer prog.Dispose()
	if len(proof.allocations) != 4 || len(proof.roles) != 3 {
		t.Fatalf("valid transaction proof = %d allocations, %d roles; want 4, 3", len(proof.allocations), len(proof.roles))
	}
	root := ssaPkg.Func("Root")
	heapBefore := coroFrameRetentionHeapAllocs(root)
	if len(heapBefore) != 4 {
		t.Fatalf("x/tools Heap allocs = %d, want 4", len(heapBefore))
	}
	plan := analyzeCoroFrameRetentionFixture(t, ssaPkg, universe, root, 1)
	rootPlan, ok := plan.FunctionPlan(root)
	if !ok || !rootPlan.Exec.Contains(coro.NeedsPreempt) {
		t.Fatalf("Root plan = %+v, present=%t; want NeedsPreempt coverage", rootPlan, ok)
	}
	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe, CoroFrameRetentionABI: CoroFrameRetentionTimerABIV1}
	enableCoroPreemptCompilation(compilation)
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{}, PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatal(err)
	}
	module := pkg.Module()
	defer module.Dispose()
	if got := coroFrameRetentionHeapAllocs(root); len(got) != len(heapBefore) {
		t.Fatalf("codegen mutated SSA Heap flags: before=%d after=%d", len(heapBefore), len(got))
	}
	body := requireCoroPhysicalFunction(t, module, "foo.Root").String()
	if strings.Contains(body, "AllocZ") || !strings.Contains(body, "alloca %foo.WaitToken") || strings.Count(body, "alloca i32") < 3 {
		t.Fatalf("retained locals were not lowered to frame-compatible allocas:\n%s", body)
	}
	prepare := strings.Index(body, "call void @"+coroTimerPrepareAfterOrAbortSymbolV1)
	retire := strings.Index(body, "call void @"+coroTimerRetireCompletedOrAbortSymbolV1)
	if prepare < 0 || retire <= prepare {
		t.Fatalf("retention owner calls are absent or unordered:\n%s", body)
	}
	before := body[:prepare]
	span := body[prepare:retire]
	if !strings.Contains(before, "call i1 @"+coroPreemptPollHookV1) ||
		strings.Contains(span, "call i1 @"+coroPreemptPollHookV1) ||
		strings.Contains(span, "call void @"+coroYieldPrepareHookV1) ||
		strings.Count(span, "call void @"+coroParkPrepareHookV1) != 1 ||
		strings.Count(span, "call i8 @llvm.coro.suspend") != 1 {
		t.Fatalf("retained critical span has an unsafe preemption/suspend shape:\n%s", span)
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify retained coroutine before CoroSplit: %v\n%s", err, module.String())
	}
	runCoroABITestPipeline(t, prog, module)
	post := module.String()
	if strings.Contains(post, "AllocZ") || !strings.Contains(post, coroTimerPrepareAfterOrAbortSymbolV1) ||
		!strings.Contains(post, coroTimerRetireCompletedOrAbortSymbolV1) {
		t.Fatalf("CoroSplit lost the frame-retained transaction or introduced managed allocation:\n%s", post)
	}
	resume := module.NamedFunction("foo.Root$coro.resume")
	if resume.IsNil() {
		t.Fatalf("CoroSplit did not create Root.resume:\n%s", post)
	}
	assertCoroFrameRetentionUsesSplitFrameAddresses(t, resume.String())
}

func assertCoroFrameRetentionUsesSplitFrameAddresses(t *testing.T, resume string) {
	t.Helper()
	frameAddress := regexp.MustCompile(`(?m)^\s*(%[-a-zA-Z$._0-9]+) = getelementptr(?: inbounds)? %"foo\.Root\$coro\.Frame", ptr %coro\.handle,`)
	frameDerived := make(map[string]bool)
	for _, match := range frameAddress.FindAllStringSubmatch(resume, -1) {
		frameDerived[match[1]] = true
	}
	preparePattern := regexp.MustCompile(
		`call void @` + regexp.QuoteMeta(coroTimerPrepareAfterOrAbortSymbolV1) +
			`\(ptr (%[-a-zA-Z$._0-9]+), i64 [^,]+, ptr (%[-a-zA-Z$._0-9]+), ptr (%[-a-zA-Z$._0-9]+), ptr (%[-a-zA-Z$._0-9]+)\)`,
	)
	prepare := preparePattern.FindStringSubmatch(resume)
	if len(prepare) != 5 {
		t.Fatalf("post-CoroSplit prepare call has no exact four-pointer shape:\n%s", resume)
	}
	retirePattern := regexp.MustCompile(
		`call void @` + regexp.QuoteMeta(coroTimerRetireCompletedOrAbortSymbolV1) +
			`\(ptr (%[-a-zA-Z$._0-9]+), i32 [^,]+, i32 [^,]+, i32 [^)]+\)`,
	)
	retire := retirePattern.FindStringSubmatch(resume)
	if len(retire) != 2 || retire[1] != prepare[1] {
		t.Fatalf("post-CoroSplit retire does not reuse the prepared token address:\n%s", resume)
	}
	seen := make(map[string]bool, 4)
	for _, address := range prepare[1:] {
		if !frameDerived[address] || seen[address] {
			t.Fatalf("post-CoroSplit prepare pointer %q is not a distinct frame-derived address:\n%s", address, resume)
		}
		seen[address] = true
	}
}

func prepareCoroFrameRetentionProof(t *testing.T, source, abi string) (
	llssa.Program, *ssa.Package, []*ast.File, *EmissionUniverse, *coroFrameRetentionProof,
) {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	prog := newLLSSAProg(t)
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	audit, err := newCoroPhysicalPureSSAAudit(universe, nil, ssaPkg.Func("Root"), abi)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	return prog, ssaPkg, files, universe, audit.currentFrameRetentionProof()
}

func analyzeCoroFrameRetentionFixture(t *testing.T, ssaPkg *ssa.Package, universe *EmissionUniverse, root *ssa.Function, maxPlain int) *coro.SSAPlan {
	t.Helper()
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapABIV2
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: maxPlain,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if fn == root {
				return coro.SSAFunctionPolicy{Effect: coro.MayPark}, nil
			}
			background, classified, err := universe.FunctionBackground(fn)
			if err != nil || !classified || background != llssa.InC {
				return coro.SSAFunctionPolicy{}, err
			}
			certificate, certified, err := universe.CoroForeignNoBlockCertificate(fn)
			if err != nil {
				return coro.SSAFunctionPolicy{}, err
			}
			if certified {
				return coro.SSAFunctionPolicy{
					IgnoreBody: true, OverrideExternal: true, External: coro.ExternalKnown,
					Exec: coro.IRQUnsafe, ForeignNoBlockCertificate: certificate.ID,
				}, nil
			}
			return coro.SSAFunctionPolicy{
				IgnoreBody: true, OverrideExternal: true, External: coro.ExternalUnknownForeign,
				Exec: coro.BlockForeign | coro.IRQUnsafe,
			}, nil
		},
		ClassifyElidedCall: func(_ *ssa.Function, call ssa.CallInstruction) (bool, error) {
			callee := call.Common().StaticCallee()
			if callee != nil && callee.Pkg != nil && callee.Pkg.Pkg.Path() == "unsafe" && callee.Name() == "init" {
				return true, nil
			}
			semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(call)
			return intrinsic && semantics.ElidesManagedCall(), err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func coroFrameRetentionHeapAllocs(fn *ssa.Function) []*ssa.Alloc {
	var result []*ssa.Alloc
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			if alloc, ok := instruction.(*ssa.Alloc); ok && alloc.Heap {
				result = append(result, alloc)
			}
		}
	}
	return result
}
