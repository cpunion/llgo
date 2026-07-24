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
	"go/token"
	"go/types"
	"sort"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	llssa "github.com/goplus/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

const coroFrameExactRootsFixture = `package foo

import "unsafe"

type Box struct { value byte }

func Child(receiver *Box, bytes []byte, pointer *byte) {}

//go:linkname raw llgo.syscall
func raw(fn, a0 uintptr) (uintptr, uintptr, uintptr)

//go:linkname funcPCABI0 llgo.funcPCABI0
func funcPCABI0(fn any) uintptr

//llgo:coro workeraddr 1
func libc_frame_root_v1_trampoline()

func (receiver *Box) Method(pointer *byte, bytes []byte) uintptr {
	if receiver != nil && pointer != nil && len(bytes) > 0 {
		receiver.value = *pointer
		Child(receiver, bytes, pointer)
		word := uintptr(unsafe.Pointer(&bytes[0]))
		result, _, _ := raw(funcPCABI0(libc_frame_root_v1_trampoline), word)
		return result
	}
	return 0
}
`

func TestCoroFrameExactRootsAndUintptrKeepaliveAreFrozen(t *testing.T) {
	digest := ""
	for iteration := 0; iteration < 2; iteration++ {
		prog, _, universe, method, audit, proof := prepareCoroFrameRootAudit(
			t, coroFrameExactRootsFixture, "Method", EmissionUniverseOptions{CoroTargetCapabilities: CoroNativeTargetCapabilities()},
		)
		if got := proof.exactRootCapabilityProfile(); got != coroFrameRetentionExactRootProfileV2 {
			prog.Dispose()
			t.Fatalf("exact-root capability profile = %q", got)
		}
		if got := proof.exactRootCapabilityDigest(); len(got) != 64 {
			prog.Dispose()
			t.Fatalf("exact-root digest = %q, want one SHA-256 identity", got)
		} else if iteration == 0 {
			digest = got
		} else if got != digest {
			prog.Dispose()
			t.Fatalf("same immutable SSA rebuilt digest %q, want %q", got, digest)
		}

		roots := make(map[string]coroFrameRetentionRootKind)
		for _, value := range proof.exactRetainedRoots() {
			roots[value.Name()] = proof.exactRoots[value].kind
		}
		for name, kind := range map[string]coroFrameRetentionRootKind{
			"receiver": coroFrameRetentionRootReceiver,
			"pointer":  coroFrameRetentionRootPointerParameter,
			"bytes":    coroFrameRetentionRootSliceParameter,
		} {
			if roots[name] != kind {
				prog.Dispose()
				t.Fatalf("exact root %q kind = %d, want %d; roots=%v", name, roots[name], kind, roots)
			}
		}

		var childCall, workerCall *ssa.Call
		var sliceAddress *ssa.IndexAddr
		var pointerWord *ssa.Convert
		for _, block := range method.Blocks {
			for _, instruction := range block.Instrs {
				if handled, reason := audit.validate(instruction); handled && reason != "" {
					prog.Dispose()
					t.Fatalf("certified instruction %T %q rejected: %s", instruction, instruction, reason)
				}
				switch instruction := instruction.(type) {
				case *ssa.Call:
					callee := instruction.Common().StaticCallee()
					if callee != nil && callee.Name() == "Child" {
						childCall = instruction
					}
					semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(instruction)
					if err == nil && intrinsic && semantics == CoroIntrinsicCallInlineSuspend {
						workerCall = instruction
					}
				case *ssa.IndexAddr:
					if _, slice := instruction.X.Type().Underlying().(*types.Slice); slice {
						sliceAddress = instruction
					}
				case *ssa.Convert:
					if coroFrameRetentionPointerToUintptr(instruction) {
						pointerWord = instruction
					}
				}
			}
		}
		if childCall == nil || workerCall == nil || sliceAddress == nil || pointerWord == nil {
			prog.Dispose()
			t.Fatalf("fixture facts child=%v worker=%v slice=%v uintptr=%v", childCall, workerCall, sliceAddress, pointerWord)
		}
		if !proof.provesDominatedStableAddress(sliceAddress, sliceAddress) || !proof.provesTraceableUintptr(pointerWord) {
			prog.Dispose()
			t.Fatal("dominated &bytes[0] or pointer->uintptr provenance was not frozen")
		}
		if got := rootNames(proof.exactCallKeepaliveRoots(childCall)); strings.Join(got, ",") != "bytes,pointer,receiver" {
			prog.Dispose()
			t.Fatalf("child keepalive roots = %v", got)
		}
		if got := rootNames(proof.exactCallKeepaliveRoots(workerCall)); strings.Join(got, ",") != "bytes" {
			prog.Dispose()
			t.Fatalf("worker keepalive roots = %v", got)
		}
		if sources := proof.exactCallKeepaliveSources(workerCall); len(sources) != 1 || sources[0] != pointerWord {
			prog.Dispose()
			t.Fatalf("worker keepalive sources = %v, want only exact call argument %v", sources, pointerWord)
		}
		prog.Dispose()
	}
}

func TestCoroFrameExactRootsRemainFailClosed(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		options EmissionUniverseOptions
		want    string
	}{
		{
			name: "unproved nil pointer",
			source: `package foo
type Box struct { value byte }
func Root(box *Box) byte { return box.value }
`,
			want: "no exact non-nil frame-retention proof",
		},
		{
			name: "unproved empty slice",
			source: `package foo
import "unsafe"
func Child(uintptr) {}
func Root(bytes []byte) { Child(uintptr(unsafe.Pointer(&bytes[0]))) }
`,
			want: "index base is not a fixed-array pointer",
		},
		{
			name: "non-positive dominance",
			source: `package foo
import "unsafe"
func Child(uintptr) {}
func Root(bytes []byte) { if len(bytes) >= 0 { Child(uintptr(unsafe.Pointer(&bytes[0]))) } }
`,
			want: "index base is not a fixed-array pointer",
		},
		{
			name: "index one only proves nonempty",
			source: `package foo
import "unsafe"
func Child(uintptr) {}
func Root(bytes []byte) { if len(bytes) > 0 { Child(uintptr(unsafe.Pointer(&bytes[1]))) } }
`,
			want: "index base is not a fixed-array pointer",
		},
		{
			name: "returned pointer word escapes bounded lifetime",
			source: `package foo
import "unsafe"
func Root(pointer *byte) uintptr { return uintptr(unsafe.Pointer(pointer)) }
`,
			want: "not bound to an exact managed-child/worker uintptrkeepalive source",
		},
		{
			name: "foreign pointer word escape",
			source: `package foo
import "unsafe"
//go:linkname foreign C.foreign
func foreign(uintptr)
func Root(pointer *byte) { foreign(uintptr(unsafe.Pointer(pointer))) }
`,
			want: "not bound to an exact managed-child/worker uintptrkeepalive source",
		},
		{
			name: "untraceable uintptr to pointer",
			source: `package foo
import "unsafe"
func Root(word uintptr) unsafe.Pointer { return unsafe.Pointer(word) }
`,
			want: "has no traceable exact pointer provenance",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prog, _, _, root, audit, _ := prepareCoroFrameRootAudit(t, test.source, "Root", test.options)
			defer prog.Dispose()
			var got string
			for _, block := range root.Blocks {
				for _, instruction := range block.Instrs {
					if handled, reason := audit.validate(instruction); handled && reason != "" {
						got = reason
						break
					}
				}
				if got != "" {
					break
				}
			}
			if !strings.Contains(got, test.want) {
				t.Fatalf("first pure-SSA rejection = %q, want %q", got, test.want)
			}
		})
	}
}

func TestCoroFrameExactRootsAcceptCanonicalSliceRangeIndex(t *testing.T) {
	prog, _, _, root, audit, proof := prepareCoroFrameRootAudit(t, `package foo
func Root(bytes []byte) byte {
	if len(bytes) == 0 { return 0 }
	sum := bytes[len(bytes)-1]
	for _, value := range bytes {
		if value == 0 { continue }
		sum ^= value
	}
	return sum
}
`, "Root", EmissionUniverseOptions{})
	defer prog.Dispose()
	var address *ssa.IndexAddr
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			if handled, reason := audit.validate(instruction); handled && reason != "" {
				t.Fatalf("canonical range instruction %T %q rejected: %s", instruction, instruction, reason)
			}
			if index, ok := instruction.(*ssa.IndexAddr); ok {
				address = index
			}
		}
	}
	if address == nil || !proof.provesDominatedStableAddress(address, address) {
		t.Fatal("canonical range IndexAddr has no exact dominating bounds/root proof")
	}
}

func TestCoroFrameExactRootsAcceptCanonicalFixedArrayRangeIndex(t *testing.T) {
	prog, _, _, root, audit, proof := prepareCoroFrameRootAudit(t, `package foo
func Root() byte {
	var values [16]byte
	for index := 0; index < len(values); index++ {
		values[index] = byte(index)
	}
	return values[15]
}
`, "Root", EmissionUniverseOptions{})
	defer prog.Dispose()
	var dynamicAddress *ssa.IndexAddr
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			if handled, reason := audit.validate(instruction); handled && reason != "" {
				t.Fatalf("canonical fixed-array range instruction %T %q rejected: %s", instruction, instruction, reason)
			}
			if index, ok := instruction.(*ssa.IndexAddr); ok {
				if _, constant := index.Index.(*ssa.Const); !constant {
					dynamicAddress = index
				}
			}
		}
	}
	if dynamicAddress == nil || !proof.provesDominatedStableAddress(dynamicAddress, dynamicAddress) {
		t.Fatal("canonical fixed-array range IndexAddr has no exact dominating bounds/root proof")
	}
}

func TestCoroFrameExactRootsAcceptBoundedFixedArrayStepIndex(t *testing.T) {
	prog, _, _, root, audit, proof := prepareCoroFrameRootAudit(t, `package foo
func Root() byte {
	var values [64]byte
	for index := 0; index < len(values); index += 8 {
		values[index] = byte(index)
	}
	return values[56]
}
`, "Root", EmissionUniverseOptions{})
	defer prog.Dispose()
	var dynamicAddress *ssa.IndexAddr
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			if handled, reason := audit.validate(instruction); handled && reason != "" {
				t.Fatalf("bounded step instruction %T %q rejected: %s", instruction, instruction, reason)
			}
			if index, ok := instruction.(*ssa.IndexAddr); ok {
				if _, constant := index.Index.(*ssa.Const); !constant {
					dynamicAddress = index
				}
			}
		}
	}
	if dynamicAddress == nil || !proof.provesDominatedStableAddress(dynamicAddress, dynamicAddress) {
		t.Fatal("bounded fixed-array step IndexAddr has no exact CFG/bounds proof")
	}
}

func TestCoroFrameExactRootsAcceptNestedSliceAndArrayIndexes(t *testing.T) {
	prog, _, _, root, audit, proof := prepareCoroFrameRootAudit(t, `package foo
type bucket struct { values [16]uint16 }
func Root(buckets []bucket) []bucket {
	for b := 0; b < len(buckets); b++ {
		for s := 0; s < 16; s++ {
			buckets[b].values[s] = uint16(b + s)
		}
	}
	return buckets
}
`, "Root", EmissionUniverseOptions{})
	defer prog.Dispose()
	proved := 0
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			if handled, reason := audit.validate(instruction); handled && reason != "" {
				var dump bytes.Buffer
				ssa.WriteFunction(&dump, root)
				t.Fatalf("nested index instruction %T %q rejected in block %d: %s\n%s", instruction, instruction, block.Index, reason, dump.String())
			}
			if index, ok := instruction.(*ssa.IndexAddr); ok && proof.provesDominatedStableAddress(index, index) {
				proved++
			}
		}
	}
	if proved < 2 {
		t.Fatalf("nested slice/array fixture proved %d IndexAddr values, want both levels", proved)
	}
}

func TestCoroFrameExactRootsRejectSignedOverflowReentryIndex(t *testing.T) {
	prog, _, _, root, audit, _ := prepareCoroFrameRootAudit(t, `package foo
func Root() byte {
	var values [16]byte
	var index int8
	for {
		if index < 16 {
			if index < 0 {
				return values[index]
			}
		}
		index++
	}
}
`, "Root", EmissionUniverseOptions{})
	defer prog.Dispose()
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			index, ok := instruction.(*ssa.IndexAddr)
			if !ok {
				continue
			}
			handled, reason := audit.validate(index)
			if !handled || reason == "" {
				t.Fatalf("signed-overflow reentry IndexAddr unexpectedly accepted: handled=%v reason=%q", handled, reason)
			}
			return
		}
	}
	t.Fatal("signed-overflow fixture has no IndexAddr")
}

func TestCoroFrameExactRootsAcceptGuardedUnsafeAddDereference(t *testing.T) {
	prog, _, _, root, audit, proof := prepareCoroFrameRootAudit(t, `package foo
import "unsafe"
func Root(base *byte, offset uintptr) byte {
	if base == nil {
		return 0
	}
	address := unsafe.Add(unsafe.Pointer(base), offset)
	return *(*byte)(address)
}
`, "Root", EmissionUniverseOptions{})
	defer prog.Dispose()
	var dereference *ssa.UnOp
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			if handled, reason := audit.validate(instruction); handled && reason != "" {
				t.Fatalf("guarded unsafe.Add instruction %T %q rejected: %s", instruction, instruction, reason)
			}
			if load, ok := instruction.(*ssa.UnOp); ok && load.Op == token.MUL {
				dereference = load
			}
		}
	}
	if dereference == nil || !proof.provesDominatedStableAddress(dereference.X, dereference) {
		t.Fatal("guarded unsafe.Add dereference has no exact address-retention proof")
	}
}

func TestCoroFrameExactRootsAcceptGuardedMergedPointer(t *testing.T) {
	prog, _, _, root, audit, proof := prepareCoroFrameRootAudit(t, `package foo
type Box struct { value byte }
func Root(first, second *Box, choose bool) byte {
	selected := first
	if choose { selected = second }
	if selected == nil { return 0 }
	return selected.value
}
`, "Root", EmissionUniverseOptions{})
	defer prog.Dispose()
	var field *ssa.FieldAddr
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			if handled, reason := audit.validate(instruction); handled && reason != "" {
				var dump bytes.Buffer
				ssa.WriteFunction(&dump, root)
				t.Fatalf("guarded merged-pointer instruction %T %q rejected: %s\n%s", instruction, instruction, reason, dump.String())
			}
			if candidate, ok := instruction.(*ssa.FieldAddr); ok {
				field = candidate
			}
		}
	}
	if field == nil || !proof.provesDominatedStableAddress(field, field) {
		t.Fatal("guarded merged pointer field has no exact non-nil retention proof")
	}
}

func TestCoroFrameExactRootsAcceptGuardedLoopCarriedPointer(t *testing.T) {
	prog, _, _, root, audit, proof := prepareCoroFrameRootAudit(t, `package foo
type Box struct { value byte }
func Root(boxes []*Box, match byte) byte {
	var selected *Box
	for index := 0; index < len(boxes); index++ {
		candidate := boxes[index]
		if candidate != nil && candidate.value == match {
			if selected != nil { return 0 }
			selected = candidate
		}
	}
	if selected == nil { return 0 }
	return selected.value
}
`, "Root", EmissionUniverseOptions{})
	defer prog.Dispose()
	var guardedField *ssa.FieldAddr
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			if handled, reason := audit.validate(instruction); handled && reason != "" {
				var dump bytes.Buffer
				ssa.WriteFunction(&dump, root)
				t.Fatalf("guarded loop-carried instruction %T %q rejected: %s\n%s", instruction, instruction, reason, dump.String())
			}
			field, ok := instruction.(*ssa.FieldAddr)
			if ok {
				guardedField = field
			}
		}
	}
	if guardedField == nil || !proof.provesDominatedStableAddress(guardedField, guardedField) {
		t.Fatal("guarded loop-carried pointer field has no exact non-nil retention proof")
	}
}

func TestCoroFrameExactRootsAcceptUnsafePointerPhiWithNilSeed(t *testing.T) {
	prog, _, _, root, audit, proof := prepareCoroFrameRootAudit(t, `package foo
import "unsafe"
func Root(pointer unsafe.Pointer, choose bool) {
	var address unsafe.Pointer
	if choose { address = pointer }
	*(*unsafe.Pointer)(address) = pointer
}
`, "Root", EmissionUniverseOptions{})
	defer prog.Dispose()
	audit.allowImplicitNilFault = true
	var store *ssa.Store
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			candidate, ok := instruction.(*ssa.Store)
			if !ok {
				continue
			}
			store = candidate
			if reason := audit.validateStore(candidate); reason != "" {
				var dump bytes.Buffer
				ssa.WriteFunction(&dump, root)
				t.Fatalf("unsafe.Pointer phi store rejected: %s\n%s", reason, dump.String())
			}
		}
	}
	if store == nil || !proof.provesGuardableStableAddress(store.Addr, store) {
		t.Fatal("unsafe.Pointer phi with a nil seed has no exact guardable address proof")
	}
}

func TestCoroFrameExactUintptrRoundtripWithInterveningChildIsFrozen(t *testing.T) {
	prog, _, _, root, audit, proof := prepareCoroFrameRootAudit(t, `package foo
import "unsafe"
type Header struct { length uintptr }
type AddressWord uintptr
func Align(size int) int { return (size + 7) &^ 7 }
func Root(header *Header, offset uintptr) unsafe.Pointer {
	return unsafe.Pointer(uintptr(AddressWord(uintptr(unsafe.Pointer(header)))) + uintptr(Align(16)) + offset)
}
`, "Root", EmissionUniverseOptions{})
	defer prog.Dispose()
	pointerWords := 0
	reconstruction := false
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			if handled, reason := audit.validate(instruction); handled && reason != "" {
				var dump bytes.Buffer
				ssa.WriteFunction(&dump, root)
				t.Fatalf("roundtrip instruction %T %q rejected: %s\n%s", instruction, instruction, reason, dump.String())
			}
			conversion, ok := instruction.(*ssa.Convert)
			if !ok {
				continue
			}
			if coroFrameRetentionPointerToUintptr(conversion) {
				pointerWords++
				if !proof.provesTraceableUintptr(conversion) {
					t.Fatalf("pointer word %q has no exact roundtrip provenance", conversion)
				}
			}
			if coroFrameRetentionUintptrLike(conversion.X.Type()) && coroFrameRetentionPointerLike(conversion.Type()) {
				reconstruction = true
				if !proof.provesTraceableUintptr(conversion.X) {
					t.Fatalf("pointer reconstruction %q has no exact source provenance", conversion)
				}
			}
		}
	}
	if pointerWords != 1 || !reconstruction {
		t.Fatalf("roundtrip facts pointer words=%d reconstruction=%t, want 1/true", pointerWords, reconstruction)
	}
}

func TestCoroFramePointerDistanceIsAnExactScalarTerminal(t *testing.T) {
	prog, _, _, root, audit, proof := prepareCoroFrameRootAudit(t, `package foo
import "unsafe"
func Root(start, end unsafe.Pointer) uintptr {
	distance := uintptr(end) - uintptr(start)
	if distance > 1<<20 { return 0 }
	return distance
}
`, "Root", EmissionUniverseOptions{})
	defer prog.Dispose()
	pointerWords := 0
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			if conversion, ok := instruction.(*ssa.Convert); ok && coroFrameRetentionPointerToUintptr(conversion) {
				pointerWords++
				if proof.provesTraceableUintptr(conversion) {
					t.Fatalf("scalar-only pointer word %q unexpectedly received reconstructable pointer provenance", conversion)
				}
				if !coroPointerUintptrScalarTerminal(conversion) {
					t.Fatalf("pointer-distance word %q lacks the exact structural scalar terminal", conversion)
				}
				continue
			}
			if handled, reason := audit.validate(instruction); handled && reason != "" {
				var dump bytes.Buffer
				ssa.WriteFunction(&dump, root)
				t.Fatalf("pointer-distance instruction %T %q rejected: %s\n%s", instruction, instruction, reason, dump.String())
			}
		}
	}
	if pointerWords != 2 {
		t.Fatalf("pointer-distance conversions = %d, want 2", pointerWords)
	}
}

func TestCoroFrameExactUintptrRoundtripChildAwaitNativeAndWasm(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	const source = `package foo
import "unsafe"
type Header struct { length uintptr }
func Align(size int) int { return (size + 7) &^ 7 }
func Root(header *Header, offset uintptr) unsafe.Pointer {
	return unsafe.Pointer(uintptr(unsafe.Pointer(header)) + uintptr(Align(16)) + offset)
}
`
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ssaPkg, _, files := buildGoSSAPkg(t, source)
			var prog llssa.Program
			if test.target == nil {
				prog = newLLSSAProg(t)
			} else {
				prog = newLLSSAProgForTarget(t, test.target)
			}
			defer prog.Dispose()
			universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
			if err != nil {
				t.Fatal(err)
			}
			ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
			if err != nil {
				t.Fatal(err)
			}
			functionIDs := universe.FunctionIDConfig()
			functionIDs.CoroABI = coro.PhysicalABIV1
			functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
			functionIDs.ArchiveReady = true
			root, align := ssaPkg.Func("Root"), ssaPkg.Func("Align")
			plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
				EmissionUniverse:     ssaUniverse,
				FunctionIDs:          functionIDs,
				MaxPlainInstructions: 1,
				ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
					if fn == align {
						return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
					}
					return coro.SSAFunctionPolicy{}, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			rootPlan, ok := plan.FunctionPlan(root)
			if !ok || rootPlan.Emission != coro.EmitCoroutine || rootPlan.Primary != coro.PrimaryCoroutine ||
				!rootPlan.Effect.Contains(coro.AwaitStructured) {
				t.Fatalf("Root plan = %+v, present=%t; want structured child-await coroutine", rootPlan, ok)
			}
			compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
			enableCoroPreemptCompilation(compilation)
			pkg, _, err := NewPackageExWithEmbedOptions(
				prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{}, PackageOptions{Compilation: compilation},
			)
			if err != nil {
				t.Fatal(err)
			}
			module := pkg.Module()
			defer module.Dispose()
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify uintptr roundtrip before CoroSplit: %v\n%s", err, module.String())
			}
			body := requireCoroPhysicalFunction(t, module, "foo.Root").String()
			for _, required := range []string{"ptrtoint", "inttoptr", "foo.Align$coro", "call void @" + coroAwaitPrepareHookV1} {
				if !strings.Contains(body, required) {
					t.Fatalf("uintptr roundtrip child-await coroutine lacks %q:\n%s", required, body)
				}
			}
			runCoroABITestPipeline(t, prog, module)
			if resume := module.NamedFunction("foo.Root$coro.resume"); resume.IsNil() {
				t.Fatalf("CoroSplit did not materialize Root resume entry:\n%s", module.String())
			}
			object, err := prog.TargetMachine().EmitToMemoryBuffer(module, llvm.ObjectFile)
			if err != nil {
				t.Fatalf("emit uintptr roundtrip object: %v\n%s", err, module.String())
			}
			defer object.Dispose()
			if len(object.Bytes()) == 0 {
				t.Fatal("uintptr roundtrip emitted an empty object")
			}
		})
	}
}

func TestCoroFrameExactUintptrRoundtripRemainsFailClosed(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{
			name: "word return escape",
			source: `package foo
import "unsafe"
func Root(pointer *byte, offset uintptr) uintptr { return uintptr(unsafe.Pointer(pointer)) + offset }
`,
		},
		{
			name: "converted integer return escape",
			source: `package foo
import "unsafe"
func Root(pointer *byte) int64 { return int64(uintptr(unsafe.Pointer(pointer))) }
`,
		},
		{
			name: "word store escape",
			source: `package foo
import "unsafe"
var escaped uintptr
func Root(pointer *byte) { escaped = uintptr(unsafe.Pointer(pointer)) }
`,
		},
		{
			name: "converted integer store escape",
			source: `package foo
import "unsafe"
var escaped int64
func Root(pointer *byte) { escaped = int64(uintptr(unsafe.Pointer(pointer))) }
`,
		},
		{
			name: "foreign word escape",
			source: `package foo
import "unsafe"
//go:linkname foreign C.foreign
func foreign(uintptr)
func Root(pointer *byte) { foreign(uintptr(unsafe.Pointer(pointer))) }
`,
		},
		{
			name: "converted integer foreign escape",
			source: `package foo
import "unsafe"
//go:linkname foreign C.foreign
func foreign(int64)
func Root(pointer *byte) { foreign(int64(uintptr(unsafe.Pointer(pointer)))) }
`,
		},
		{
			name: "converted integer arithmetic escape",
			source: `package foo
import "unsafe"
func Child(int64) {}
func Root(pointer *byte) { Child(int64(uintptr(unsafe.Pointer(pointer))) + 1) }
`,
		},
		{
			name: "multiplication loses address provenance",
			source: `package foo
import "unsafe"
func Root(pointer *byte, scale uintptr) unsafe.Pointer {
	return unsafe.Pointer(uintptr(unsafe.Pointer(pointer)) * scale)
}
`,
		},
		{
			name: "two pointer words are ambiguous",
			source: `package foo
import "unsafe"
func Root(left, right *byte) unsafe.Pointer {
	return unsafe.Pointer(uintptr(unsafe.Pointer(left)) + uintptr(unsafe.Pointer(right)))
}
`,
		},
		{
			name: "partial control-flow reconstruction",
			source: `package foo
import "unsafe"
func Root(pointer *byte, offset uintptr, reconstruct bool) unsafe.Pointer {
	word := uintptr(unsafe.Pointer(pointer))
	if reconstruct { return unsafe.Pointer(word + offset) }
	return nil
}
`,
		},
		{
			name: "phi address ambiguity",
			source: `package foo
import "unsafe"
func Root(pointer *byte, offset uintptr, adjust bool) unsafe.Pointer {
	word := uintptr(unsafe.Pointer(pointer))
	if adjust { word += offset }
	return unsafe.Pointer(word)
}
`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prog, _, _, root, audit, proof := prepareCoroFrameRootAudit(t, test.source, "Root", EmissionUniverseOptions{})
			defer prog.Dispose()
			pointerWords := 0
			rejection := ""
			for _, block := range root.Blocks {
				for _, instruction := range block.Instrs {
					if conversion, ok := instruction.(*ssa.Convert); ok && coroFrameRetentionPointerToUintptr(conversion) {
						pointerWords++
						if proof.provesTraceableUintptr(conversion) {
							t.Fatalf("unsafe address word %q unexpectedly received exact provenance", conversion)
						}
					}
					if handled, reason := audit.validate(instruction); handled && reason != "" && rejection == "" {
						rejection = reason
					}
				}
			}
			if pointerWords == 0 {
				t.Fatal("negative fixture has no pointer-to-uintptr conversion")
			}
			if rejection == "" || (!strings.Contains(rejection, "not bound to an exact managed-child/worker") &&
				!strings.Contains(rejection, "has no traceable exact pointer provenance")) {
				var dump bytes.Buffer
				ssa.WriteFunction(&dump, root)
				t.Fatalf("first rejection = %q, want exact uintptr provenance failure\n%s", rejection, dump.String())
			}
		})
	}
}

func TestCoroPointerUintptrAlignmentObservationIsScalarTerminal(t *testing.T) {
	for _, test := range []struct {
		name       string
		expression string
		wantSafe   bool
	}{
		{name: "comparison", expression: "return uintptr(unsafe.Pointer(pointer))%8 == 0", wantSafe: true},
		{name: "returned remainder", expression: "return uintptr(unsafe.Pointer(pointer)) % 8"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := "bool"
			if !test.wantSafe {
				result = "uintptr"
			}
			source := "package foo\nimport \"unsafe\"\nfunc Root(pointer *byte) " + result + " { " + test.expression + " }\n"
			prog, _, _, root, audit, _ := prepareCoroFrameRootAudit(t, source, "Root", EmissionUniverseOptions{})
			defer prog.Dispose()
			found := false
			for _, block := range root.Blocks {
				for _, instruction := range block.Instrs {
					conversion, ok := instruction.(*ssa.Convert)
					if !ok || !coroFrameRetentionPointerToUintptr(conversion) {
						continue
					}
					found = true
					reason := audit.validateConvert(conversion)
					if test.wantSafe && !coroPointerUintptrScalarTerminal(conversion) {
						t.Fatalf("alignment comparison lacks the structural scalar-terminal proof: %s", reason)
					}
					if !test.wantSafe && !strings.Contains(reason, "not bound to an exact managed-child/worker") {
						t.Fatalf("returned remainder rejection = %q", reason)
					}
				}
			}
			if !found {
				t.Fatal("fixture has no pointer-to-uintptr conversion")
			}
		})
	}
}

func TestCoroPointerUintptrReflectHeaderStoreTerminal(t *testing.T) {
	pkg, _, _ := buildGoSSAPkg(t, `package foo
import (
	"reflect"
	"unsafe"
)
type Header struct { Data uintptr }
func observe() {}
func String(header *reflect.StringHeader, bytes []byte) {
	header.Data = uintptr(unsafe.Pointer(&bytes[0]))
}
func Slice(header *reflect.SliceHeader, bytes []byte) {
	header.Data = uintptr(unsafe.Pointer(&bytes[0]))
}
func Fake(header *Header, bytes []byte) {
	header.Data = uintptr(unsafe.Pointer(&bytes[0]))
}
func Gap(header *reflect.StringHeader, bytes []byte) {
	word := uintptr(unsafe.Pointer(&bytes[0]))
	observe()
	header.Data = word
}
`)
	for _, test := range []struct {
		name string
		want bool
	}{
		{name: "String", want: true},
		{name: "Slice", want: true},
		{name: "Fake"},
		{name: "Gap"},
	} {
		t.Run(test.name, func(t *testing.T) {
			function := pkg.Func(test.name)
			var conversion *ssa.Convert
			for _, block := range function.Blocks {
				for _, instruction := range block.Instrs {
					candidate, ok := instruction.(*ssa.Convert)
					if !ok || !coroFrameRetentionPointerToUintptr(candidate) {
						continue
					}
					if conversion != nil {
						t.Fatalf("%s has more than one pointer-to-uintptr conversion", test.name)
					}
					conversion = candidate
				}
			}
			if conversion == nil {
				t.Fatalf("%s has no pointer-to-uintptr conversion", test.name)
			}
			if got := coroPointerUintptrReflectHeaderStoreTerminal(conversion); got != test.want {
				var dump bytes.Buffer
				ssa.WriteFunction(&dump, function)
				t.Fatalf("%s reflect-header terminal = %t, want %t\n%s", test.name, got, test.want, dump.String())
			}
		})
	}
}

func TestCoroConstantUintptrAddress(t *testing.T) {
	pkg, _, _ := buildGoSSAPkg(t, `package foo
import "unsafe"
func Captured() (unsafe.Pointer, func() uintptr) {
	value := ^uintptr(0)
	pointer := unsafe.Pointer(value)
	return pointer, func() uintptr { return value }
}
func Mutable(word uintptr) (unsafe.Pointer, func() uintptr) {
	value := ^uintptr(0)
	value = word
	pointer := unsafe.Pointer(value)
	return pointer, func() uintptr { return value }
}
`)
	for _, test := range []struct {
		name string
		want bool
	}{
		{name: "Captured", want: true},
		{name: "Mutable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			function := pkg.Func(test.name)
			var conversion *ssa.Convert
			for _, block := range function.Blocks {
				for _, instruction := range block.Instrs {
					candidate, ok := instruction.(*ssa.Convert)
					if !ok || !coroFrameRetentionUintptrLike(candidate.X.Type()) ||
						!coroFrameRetentionPointerLike(candidate.Type()) {
						continue
					}
					conversion = candidate
					break
				}
			}
			if conversion == nil {
				t.Fatalf("%s has no uintptr-to-pointer conversion", test.name)
			}
			if got := coroConstantUintptrAddress(conversion.X); got != test.want {
				var dump bytes.Buffer
				ssa.WriteFunction(&dump, function)
				t.Fatalf("%s constant-address proof = %t, want %t\n%s", test.name, got, test.want, dump.String())
			}
		})
	}
}

func TestCoroFrameExactRootsRejectPreciseShadowProfile(t *testing.T) {
	old := emitShadowStackInstrumentation
	emitShadowStackInstrumentation = true
	defer func() { emitShadowStackInstrumentation = old }()
	prog, _, _, _, _, proof := prepareCoroFrameRootAudit(
		t, coroFrameExactRootsFixture, "Method", EmissionUniverseOptions{CoroTargetCapabilities: CoroNativeTargetCapabilities()},
	)
	defer prog.Dispose()
	if proof.exactRootCapabilityProfile() != "" || proof.exactRootCapabilityDigest() != "" || len(proof.exactRetainedRoots()) != 0 {
		t.Fatalf("precise/shadow profile received exact-root capability: profile=%q digest=%q roots=%d",
			proof.exactRootCapabilityProfile(), proof.exactRootCapabilityDigest(), len(proof.exactRetainedRoots()))
	}
}

func prepareCoroFrameRootAudit(t *testing.T, source, function string, options EmissionUniverseOptions) (
	llssa.Program, *ssa.Package, *EmissionUniverse, *ssa.Function, *coroPhysicalPureSSAAudit, *coroFrameRetentionProof,
) {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	prog := newLLSSAProg(t)
	universe, err := prepareStacklessEmissionUniverseWithOptions(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}}, options)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	var target *ssa.Function
	if direct := ssaPkg.Func(function); direct != nil {
		target = direct
	} else {
		for _, candidate := range universe.Functions() {
			if candidate != nil && candidate.Name() == function && candidate.Signature != nil && candidate.Signature.Recv() != nil {
				target = candidate
				break
			}
		}
	}
	if target == nil {
		prog.Dispose()
		t.Fatalf("function %q not found", function)
	}
	audit, err := newCoroPhysicalPureSSAAudit(universe, nil, target, "")
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	return prog, ssaPkg, universe, target, audit, audit.currentFrameRetentionProof()
}

func rootNames(values []ssa.Value) []string {
	names := make([]string, len(values))
	for index, value := range values {
		names[index] = value.Name()
	}
	sort.Strings(names)
	return names
}
