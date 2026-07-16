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
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"
)

func alignProgramTestSizeV1(size, align uintptr) uintptr {
	return (size + align - 1) &^ (align - 1)
}

func TestProgramBootstrapV1TargetNeutralLayout(t *testing.T) {
	pointerSize := unsafe.Sizeof(uintptr(0))
	structAlign := unsafe.Alignof(uint64(0))

	manifest := ProgramManifestV1{}
	bootstrap := ProgramBootstrapV1{}
	step := ProgramStepV1{}
	anchor := RootPackageAnchorV1{}
	descriptor := RootFactoryDescriptorV1{}
	wants := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"manifest.version", unsafe.Offsetof(manifest.Version), 0},
		{"manifest.flags", unsafe.Offsetof(manifest.Flags), 4},
		{"manifest.hashLo", unsafe.Offsetof(manifest.HashLo), 8},
		{"manifest.hashHi", unsafe.Offsetof(manifest.HashHi), 16},
		{"manifest.packageCount", unsafe.Offsetof(manifest.PackageCount), 24},
		{"manifest.packages", unsafe.Offsetof(manifest.Packages), 24 + pointerSize},
		{"manifest.bootstrap", unsafe.Offsetof(manifest.Bootstrap), 24 + 2*pointerSize},
		{"bootstrap.version", unsafe.Offsetof(bootstrap.Version), 0},
		{"bootstrap.flags", unsafe.Offsetof(bootstrap.Flags), 4},
		{"bootstrap.hashLo", unsafe.Offsetof(bootstrap.HashLo), 8},
		{"bootstrap.hashHi", unsafe.Offsetof(bootstrap.HashHi), 16},
		{"bootstrap.stepCount", unsafe.Offsetof(bootstrap.StepCount), 24},
		{"bootstrap.steps", unsafe.Offsetof(bootstrap.Steps), 24 + pointerSize},
		{"bootstrap.factory", unsafe.Offsetof(bootstrap.Factory), 24 + 2*pointerSize},
		{"step.kind", unsafe.Offsetof(step.Kind), 0},
		{"step.flags", unsafe.Offsetof(step.Flags), 4},
		{"step.target", unsafe.Offsetof(step.Target), 8},
		{"step.aux", unsafe.Offsetof(step.Aux), 8 + pointerSize},
		{"anchor.version", unsafe.Offsetof(anchor.Version), 0},
		{"anchor.flags", unsafe.Offsetof(anchor.Flags), 4},
		{"anchor.hashLo", unsafe.Offsetof(anchor.HashLo), 8},
		{"anchor.hashHi", unsafe.Offsetof(anchor.HashHi), 16},
		{"anchor.count", unsafe.Offsetof(anchor.Count), 24},
		{"anchor.entries", unsafe.Offsetof(anchor.Entries), 24 + pointerSize},
		{"descriptor.version", unsafe.Offsetof(descriptor.Version), 0},
		{"descriptor.flags", unsafe.Offsetof(descriptor.Flags), 4},
		{"descriptor.hashLo", unsafe.Offsetof(descriptor.HashLo), 8},
		{"descriptor.hashHi", unsafe.Offsetof(descriptor.HashHi), 16},
		{"descriptor.factory", unsafe.Offsetof(descriptor.Factory), 24},
		{"descriptor.startupSize", unsafe.Offsetof(descriptor.StartupSize), 24 + pointerSize},
		{"descriptor.startupAlign", unsafe.Offsetof(descriptor.StartupAlign), 24 + 2*pointerSize},
		{"descriptor.resultSize", unsafe.Offsetof(descriptor.ResultSize), 24 + 3*pointerSize},
		{"descriptor.resultAlign", unsafe.Offsetof(descriptor.ResultAlign), 24 + 4*pointerSize},
	}
	for _, field := range wants {
		if field.got != field.want {
			t.Errorf("%s offset = %d, want %d", field.name, field.got, field.want)
		}
	}
	sizes := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"manifest", unsafe.Sizeof(manifest), alignProgramTestSizeV1(24+3*pointerSize, structAlign)},
		{"bootstrap", unsafe.Sizeof(bootstrap), alignProgramTestSizeV1(24+3*pointerSize, structAlign)},
		{"step", unsafe.Sizeof(step), alignProgramTestSizeV1(8+2*pointerSize, pointerSize)},
		{"anchor", unsafe.Sizeof(anchor), alignProgramTestSizeV1(24+2*pointerSize, structAlign)},
		{"descriptor", unsafe.Sizeof(descriptor), alignProgramTestSizeV1(24+5*pointerSize, structAlign)},
	}
	for _, size := range sizes {
		if size.got != size.want {
			t.Errorf("%s size = %d, want %d", size.name, size.got, size.want)
		}
	}
}

type programBootstrapTestFixtureV1 struct {
	plainTargets     [2]byte
	bootstrapFactory byte
	rootFactories    [3]byte
	descriptors      [3]RootFactoryDescriptorV1
	anchorEntriesA   [2]unsafe.Pointer
	anchorEntriesB   [1]unsafe.Pointer
	anchors          [2]RootPackageAnchorV1
	packages         [2]unsafe.Pointer
	steps            [2]ProgramStepV1
	bootstrap        ProgramBootstrapV1
	manifest         ProgramManifestV1
	scratch          [256]byte
}

func newProgramBootstrapTestFixtureV1() *programBootstrapTestFixtureV1 {
	f := new(programBootstrapTestFixtureV1)
	f.plainTargets = [2]byte{0x31, 0x32}
	f.bootstrapFactory = 0x41
	f.rootFactories = [3]byte{0x51, 0x52, 0x53}
	f.descriptors[0] = RootFactoryDescriptorV1{
		Version: RootFactoryVersionV1, HashLo: 0x101, HashHi: 0x102,
		Factory:     unsafe.Pointer(&f.rootFactories[0]),
		StartupSize: 8, StartupAlign: 8, ResultSize: 4, ResultAlign: 4,
	}
	f.descriptors[1] = RootFactoryDescriptorV1{
		Version: RootFactoryVersionV1, HashLo: 0x201, HashHi: 0x202,
		Factory:      unsafe.Pointer(&f.rootFactories[1]),
		StartupAlign: 1, ResultAlign: 1,
	}
	f.descriptors[2] = RootFactoryDescriptorV1{
		Version: RootFactoryVersionV1, HashLo: 0x301, HashHi: 0x302,
		Factory:     unsafe.Pointer(&f.rootFactories[2]),
		StartupSize: 2, StartupAlign: 2, ResultSize: 2, ResultAlign: 2,
	}
	f.anchorEntriesA = [2]unsafe.Pointer{
		unsafe.Pointer(&f.descriptors[0]),
		unsafe.Pointer(&f.descriptors[1]),
	}
	f.anchorEntriesB = [1]unsafe.Pointer{unsafe.Pointer(&f.descriptors[2])}
	f.anchors[0] = RootPackageAnchorV1{
		Version: RootPackageAnchorVersionV1, HashLo: 0xa01, HashHi: 0xa02,
		Count: uintptr(len(f.anchorEntriesA)), Entries: unsafe.Pointer(&f.anchorEntriesA[0]),
	}
	f.anchors[1] = RootPackageAnchorV1{
		Version: RootPackageAnchorVersionV1, HashLo: 0xb01, HashHi: 0xb02,
		Count: uintptr(len(f.anchorEntriesB)), Entries: unsafe.Pointer(&f.anchorEntriesB[0]),
	}
	f.packages = [2]unsafe.Pointer{unsafe.Pointer(&f.anchors[0]), unsafe.Pointer(&f.anchors[1])}
	f.steps[0] = ProgramStepV1{
		Kind: uint32(ProgramStepDirectPlainV1), Flags: ProgramStepFlagInitV1,
		Target: unsafe.Pointer(&f.plainTargets[0]),
	}
	f.steps[1] = ProgramStepV1{
		Kind: uint32(ProgramStepCoroRootV1), Flags: ProgramStepFlagMainV1,
		Target: unsafe.Pointer(&f.anchors[0]), Aux: 1,
	}
	f.bootstrap = ProgramBootstrapV1{
		Version:   ProgramBootstrapVersionV1,
		HashLo:    0x1234567890abcdef,
		HashHi:    0xfedcba0987654321,
		StepCount: uintptr(len(f.steps)),
		Steps:     unsafe.Pointer(&f.steps[0]),
		Factory:   unsafe.Pointer(&f.bootstrapFactory),
	}
	f.manifest = ProgramManifestV1{
		Version:      ProgramManifestVersionV1,
		HashLo:       f.bootstrap.HashLo,
		HashHi:       f.bootstrap.HashHi,
		PackageCount: uintptr(len(f.packages)),
		Packages:     unsafe.Pointer(&f.packages[0]),
		Bootstrap:    unsafe.Pointer(&f.bootstrap),
	}
	return f
}

func (f *programBootstrapTestFixtureV1) misaligned(align uintptr) unsafe.Pointer {
	base := uintptr(unsafe.Pointer(&f.scratch[0]))
	offset := -base & (align - 1)
	return unsafe.Add(unsafe.Pointer(&f.scratch[0]), offset+1)
}

func makeDirectProgramBootstrapTestFixtureV1() *programBootstrapTestFixtureV1 {
	f := newProgramBootstrapTestFixtureV1()
	f.manifest.PackageCount = 0
	f.manifest.Packages = nil
	f.steps[1] = ProgramStepV1{
		Kind: uint32(ProgramStepDirectPlainV1), Flags: ProgramStepFlagMainV1,
		Target: unsafe.Pointer(&f.plainTargets[1]),
	}
	return f
}

func makeCoroProgramBootstrapTestFixtureV1() *programBootstrapTestFixtureV1 {
	f := newProgramBootstrapTestFixtureV1()
	f.descriptors[2].StartupSize = 0
	f.descriptors[2].StartupAlign = 1
	f.descriptors[2].ResultSize = 0
	f.descriptors[2].ResultAlign = 1
	f.steps[0] = ProgramStepV1{
		Kind: uint32(ProgramStepCoroRootV1), Flags: ProgramStepFlagInitV1,
		Target: unsafe.Pointer(&f.anchors[0]), Aux: 1,
	}
	f.steps[1] = ProgramStepV1{
		Kind: uint32(ProgramStepCoroRootV1), Flags: ProgramStepFlagMainV1,
		Target: unsafe.Pointer(&f.anchors[1]), Aux: 0,
	}
	return f
}

func requireProgramViewV1(t *testing.T, manifest *ProgramManifestV1) ProgramViewV1 {
	t.Helper()
	view, code := ValidateProgramV1(manifest)
	if code != ProgramValidationOKV1 {
		t.Fatalf("ValidateProgramV1 code = %d, want success", code)
	}
	return view
}

func TestValidateAndResolveDirectProgramV1(t *testing.T) {
	f := makeDirectProgramBootstrapTestFixtureV1()
	view := requireProgramViewV1(t, &f.manifest)
	for index, want := range []unsafe.Pointer{
		unsafe.Pointer(&f.plainTargets[0]),
		unsafe.Pointer(&f.plainTargets[1]),
	} {
		step, code := ResolveProgramStepV1(view, uintptr(index))
		if code != ProgramValidationOKV1 || step.Kind != ProgramStepDirectPlainV1 ||
			step.Plain != want || step.Descriptor != nil || step.Factory != nil {
			t.Fatalf("step %d = (%+v, %d), want direct target %p", index, step, code, want)
		}
	}
	if f.plainTargets != [2]byte{0x31, 0x32} || f.bootstrapFactory != 0x41 {
		t.Fatal("validation or resolution invoked a target/factory")
	}
}

func TestValidateAndResolveCoroRootProgramV1(t *testing.T) {
	f := makeCoroProgramBootstrapTestFixtureV1()
	view := requireProgramViewV1(t, &f.manifest)
	wants := []*RootFactoryDescriptorV1{&f.descriptors[1], &f.descriptors[2]}
	for index, want := range wants {
		step, code := ResolveProgramStepV1(view, uintptr(index))
		if code != ProgramValidationOKV1 || step.Kind != ProgramStepCoroRootV1 ||
			step.Plain != nil || step.Descriptor != want || step.Factory != want.Factory {
			t.Fatalf("step %d = (%+v, %d), want coroutine descriptor %p", index, step, code, want)
		}
	}
	if f.rootFactories != [3]byte{0x51, 0x52, 0x53} || f.bootstrapFactory != 0x41 {
		t.Fatal("validation or resolution invoked a target/factory")
	}
}

func TestProgramDescriptorAllowsMissingBootstrapFactoryV1(t *testing.T) {
	f := newProgramBootstrapTestFixtureV1()
	f.bootstrap.Factory = nil
	view, code := ValidateProgramDescriptorV1(&f.manifest)
	if code != ProgramValidationOKV1 {
		t.Fatalf("descriptor validation code = %d, want success", code)
	}
	if _, code = ResolveProgramStepV1(view, 0); code != ProgramValidationOKV1 {
		t.Fatalf("descriptor view resolution code = %d, want success", code)
	}
	if _, code = ValidateProgramV1(&f.manifest); code != ProgramValidationBootstrapFactoryV1 {
		t.Fatalf("program validation code = %d, want missing factory", code)
	}
	if _, code = ValidateRunnableProgramV1(&f.manifest); code != ProgramValidationBootstrapFactoryV1 {
		t.Fatalf("runnable validation code = %d, want missing factory", code)
	}
}

func TestResolvedProgramViewV1IsImmutableSnapshot(t *testing.T) {
	f := newProgramBootstrapTestFixtureV1()
	view := requireProgramViewV1(t, &f.manifest)
	wantPlain := f.steps[0].Target
	wantDescriptor := &f.descriptors[1]
	wantFactory := f.descriptors[1].Factory
	f.steps = [2]ProgramStepV1{}
	f.descriptors[1].Factory = nil

	init, initCode := ResolveProgramStepV1(view, 0)
	main, mainCode := ResolveProgramStepV1(view, 1)
	if initCode != ProgramValidationOKV1 || init.Plain != wantPlain ||
		mainCode != ProgramValidationOKV1 || main.Descriptor != wantDescriptor || main.Factory != wantFactory {
		t.Fatalf("snapshot changed: init=(%+v,%d) main=(%+v,%d)", init, initCode, main, mainCode)
	}
	if _, code := ResolveProgramStepV1(ProgramViewV1{}, 0); code != ProgramValidationInvalidViewV1 {
		t.Fatalf("zero view code = %d, want invalid view", code)
	}
	if _, code := ResolveProgramStepV1(view, 2); code != ProgramValidationStepIndexV1 {
		t.Fatalf("out-of-range code = %d, want step index", code)
	}
}

func TestCheckedProgramArrayV1RejectsCountAndAddressOverflow(t *testing.T) {
	entries := [2]unsafe.Pointer{unsafe.Pointer(new(byte)), unsafe.Pointer(new(byte))}
	base := unsafe.Pointer(&entries[0])
	size := unsafe.Sizeof(entries[0])
	align := unsafe.Alignof(entries[0])
	if got := checkedProgramArrayV1(nil, 0, size, align); got != programArrayOKV1 {
		t.Fatalf("empty array state = %d, want ok", got)
	}
	if got := checkedProgramArrayV1(base, 0, size, align); got != programArrayCountPointerV1 {
		t.Fatalf("zero count/non-nil pointer state = %d", got)
	}
	if got := checkedProgramArrayV1(nil, 1, size, align); got != programArrayCountPointerV1 {
		t.Fatalf("nonzero count/nil pointer state = %d", got)
	}
	if got := checkedProgramArrayV1(base, ^uintptr(0)/size+1, size, align); got != programArrayAddressV1 {
		t.Fatalf("multiplication overflow state = %d", got)
	}
	if got := checkedProgramArrayV1(base, ^uintptr(0)/size, size, align); got != programArrayAddressV1 {
		t.Fatalf("end-address overflow state = %d", got)
	}
}

func TestValidateProgramV1FailsClosed(t *testing.T) {
	if _, code := ValidateProgramDescriptorV1(nil); code != ProgramValidationNilManifestV1 {
		t.Fatalf("nil manifest code = %d", code)
	}

	tests := []struct {
		name     string
		want     ProgramValidationCodeV1
		runnable bool
		mutate   func(*programBootstrapTestFixtureV1)
	}{
		{"manifest version", ProgramValidationManifestVersionV1, false, func(f *programBootstrapTestFixtureV1) { f.manifest.Version = 2 }},
		{"manifest flags", ProgramValidationManifestFlagsV1, false, func(f *programBootstrapTestFixtureV1) { f.manifest.Flags = 1 }},
		{"packages without count", ProgramValidationPackageCountPointerV1, false, func(f *programBootstrapTestFixtureV1) { f.manifest.PackageCount = 0 }},
		{"count without packages", ProgramValidationPackageCountPointerV1, false, func(f *programBootstrapTestFixtureV1) { f.manifest.Packages = nil }},
		{"package table misaligned", ProgramValidationPackageTableAddressV1, false, func(f *programBootstrapTestFixtureV1) {
			f.manifest.Packages = f.misaligned(unsafe.Alignof(unsafe.Pointer(nil)))
		}},
		{"package table multiplication overflow", ProgramValidationPackageTableAddressV1, false, func(f *programBootstrapTestFixtureV1) {
			f.manifest.PackageCount = ^uintptr(0)/unsafe.Sizeof(unsafe.Pointer(nil)) + 1
		}},
		{"nil bootstrap", ProgramValidationNilBootstrapV1, false, func(f *programBootstrapTestFixtureV1) { f.manifest.Bootstrap = nil }},
		{"bootstrap misaligned", ProgramValidationBootstrapAddressV1, false, func(f *programBootstrapTestFixtureV1) {
			f.manifest.Bootstrap = f.misaligned(unsafe.Alignof(ProgramBootstrapV1{}))
		}},
		{"bootstrap version", ProgramValidationBootstrapVersionV1, false, func(f *programBootstrapTestFixtureV1) { f.bootstrap.Version = 2 }},
		{"bootstrap flags", ProgramValidationBootstrapFlagsV1, false, func(f *programBootstrapTestFixtureV1) { f.bootstrap.Flags = 1 }},
		{"bootstrap low hash", ProgramValidationBootstrapHashV1, false, func(f *programBootstrapTestFixtureV1) { f.bootstrap.HashLo++ }},
		{"bootstrap high hash", ProgramValidationBootstrapHashV1, false, func(f *programBootstrapTestFixtureV1) { f.bootstrap.HashHi++ }},
		{"one step", ProgramValidationStepCountV1, false, func(f *programBootstrapTestFixtureV1) { f.bootstrap.StepCount = 1 }},
		{"three steps", ProgramValidationStepCountV1, false, func(f *programBootstrapTestFixtureV1) { f.bootstrap.StepCount = 3 }},
		{"nil steps", ProgramValidationStepCountPointerV1, false, func(f *programBootstrapTestFixtureV1) { f.bootstrap.Steps = nil }},
		{"steps misaligned", ProgramValidationStepTableAddressV1, false, func(f *programBootstrapTestFixtureV1) {
			f.bootstrap.Steps = f.misaligned(unsafe.Alignof(ProgramStepV1{}))
		}},
		{"nil bootstrap factory", ProgramValidationBootstrapFactoryV1, true, func(f *programBootstrapTestFixtureV1) { f.bootstrap.Factory = nil }},
		{"nil package anchor", ProgramValidationNilPackageAnchorV1, false, func(f *programBootstrapTestFixtureV1) { f.packages[0] = nil }},
		{"package anchor misaligned", ProgramValidationPackageAnchorAddressV1, false, func(f *programBootstrapTestFixtureV1) {
			f.packages[0] = f.misaligned(unsafe.Alignof(RootPackageAnchorV1{}))
		}},
		{"duplicate package anchor", ProgramValidationDuplicatePackageAnchorV1, false, func(f *programBootstrapTestFixtureV1) { f.packages[1] = f.packages[0] }},
		{"package anchor version", ProgramValidationPackageAnchorVersionV1, false, func(f *programBootstrapTestFixtureV1) { f.anchors[0].Version = 2 }},
		{"package anchor flags", ProgramValidationPackageAnchorFlagsV1, false, func(f *programBootstrapTestFixtureV1) { f.anchors[0].Flags = 1 }},
		{"empty package anchor", ProgramValidationEmptyPackageAnchorV1, false, func(f *programBootstrapTestFixtureV1) { f.anchors[0].Count = 0; f.anchors[0].Entries = nil }},
		{"entries without count", ProgramValidationDescriptorCountPointerV1, false, func(f *programBootstrapTestFixtureV1) { f.anchors[0].Count = 0 }},
		{"count without entries", ProgramValidationDescriptorCountPointerV1, false, func(f *programBootstrapTestFixtureV1) { f.anchors[0].Entries = nil }},
		{"descriptor table misaligned", ProgramValidationDescriptorTableAddressV1, false, func(f *programBootstrapTestFixtureV1) {
			f.anchors[0].Entries = f.misaligned(unsafe.Alignof(unsafe.Pointer(nil)))
		}},
		{"descriptor table multiplication overflow", ProgramValidationDescriptorTableAddressV1, false, func(f *programBootstrapTestFixtureV1) {
			f.anchors[0].Count = ^uintptr(0)/unsafe.Sizeof(unsafe.Pointer(nil)) + 1
		}},
		{"nil root descriptor", ProgramValidationNilRootDescriptorV1, false, func(f *programBootstrapTestFixtureV1) { f.anchorEntriesA[0] = nil }},
		{"root descriptor misaligned", ProgramValidationRootDescriptorAddressV1, false, func(f *programBootstrapTestFixtureV1) {
			f.anchorEntriesA[0] = f.misaligned(unsafe.Alignof(RootFactoryDescriptorV1{}))
		}},
		{"duplicate root descriptor in anchor", ProgramValidationDuplicateRootDescriptorV1, false, func(f *programBootstrapTestFixtureV1) { f.anchorEntriesA[1] = f.anchorEntriesA[0] }},
		{"duplicate root descriptor across anchors", ProgramValidationDuplicateRootDescriptorV1, false, func(f *programBootstrapTestFixtureV1) { f.anchorEntriesB[0] = f.anchorEntriesA[0] }},
		{"unused root descriptor version", ProgramValidationRootDescriptorVersionV1, false, func(f *programBootstrapTestFixtureV1) { f.descriptors[0].Version = 2 }},
		{"root descriptor flags", ProgramValidationRootDescriptorFlagsV1, false, func(f *programBootstrapTestFixtureV1) { f.descriptors[0].Flags = 1 }},
		{"root descriptor factory", ProgramValidationRootDescriptorFactoryV1, false, func(f *programBootstrapTestFixtureV1) { f.descriptors[0].Factory = nil }},
		{"startup zero alignment", ProgramValidationRootStartupLayoutV1, false, func(f *programBootstrapTestFixtureV1) { f.descriptors[0].StartupAlign = 0 }},
		{"startup non-power alignment", ProgramValidationRootStartupLayoutV1, false, func(f *programBootstrapTestFixtureV1) { f.descriptors[0].StartupAlign = 3 }},
		{"startup size alignment mismatch", ProgramValidationRootStartupLayoutV1, false, func(f *programBootstrapTestFixtureV1) {
			f.descriptors[0].StartupSize = 3
			f.descriptors[0].StartupAlign = 2
		}},
		{"result zero alignment", ProgramValidationRootResultLayoutV1, false, func(f *programBootstrapTestFixtureV1) { f.descriptors[0].ResultAlign = 0 }},
		{"result non-power alignment", ProgramValidationRootResultLayoutV1, false, func(f *programBootstrapTestFixtureV1) { f.descriptors[0].ResultAlign = 3 }},
		{"result size alignment mismatch", ProgramValidationRootResultLayoutV1, false, func(f *programBootstrapTestFixtureV1) {
			f.descriptors[0].ResultSize = 3
			f.descriptors[0].ResultAlign = 2
		}},
		{"init flags", ProgramValidationStepInitFlagsV1, false, func(f *programBootstrapTestFixtureV1) { f.steps[0].Flags = 0 }},
		{"main flags", ProgramValidationStepMainFlagsV1, false, func(f *programBootstrapTestFixtureV1) { f.steps[1].Flags = ProgramStepFlagInitV1 }},
		{"step kind", ProgramValidationStepKindV1, false, func(f *programBootstrapTestFixtureV1) { f.steps[0].Kind = 3 }},
		{"step target", ProgramValidationStepTargetV1, false, func(f *programBootstrapTestFixtureV1) { f.steps[0].Target = nil }},
		{"direct aux", ProgramValidationStepAuxV1, false, func(f *programBootstrapTestFixtureV1) { f.steps[0].Aux = 1 }},
		{"coro anchor membership", ProgramValidationStepAnchorV1, false, func(f *programBootstrapTestFixtureV1) { f.steps[1].Target = unsafe.Pointer(&f.plainTargets[1]) }},
		{"coro descriptor index", ProgramValidationStepDescriptorIndexV1, false, func(f *programBootstrapTestFixtureV1) { f.steps[1].Aux = f.anchors[0].Count }},
		{"coro startup size", ProgramValidationStepPayloadV1, false, func(f *programBootstrapTestFixtureV1) { f.descriptors[1].StartupSize = 1 }},
		{"coro startup alignment", ProgramValidationStepPayloadV1, false, func(f *programBootstrapTestFixtureV1) { f.descriptors[1].StartupAlign = 2 }},
		{"coro result size", ProgramValidationStepPayloadV1, false, func(f *programBootstrapTestFixtureV1) { f.descriptors[1].ResultSize = 1 }},
		{"coro result alignment", ProgramValidationStepPayloadV1, false, func(f *programBootstrapTestFixtureV1) { f.descriptors[1].ResultAlign = 2 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newProgramBootstrapTestFixtureV1()
			test.mutate(f)
			var code ProgramValidationCodeV1
			if test.runnable {
				_, code = ValidateProgramV1(&f.manifest)
			} else {
				_, code = ValidateProgramDescriptorV1(&f.manifest)
			}
			if code != test.want {
				t.Fatalf("validation code = %d, want %d", code, test.want)
			}
		})
	}
}

var (
	programViewSinkV1 ProgramViewV1
	programStepSinkV1 ResolvedProgramStepV1
	programCodeSinkV1 ProgramValidationCodeV1
)

func TestValidateAndResolveProgramV1AllocateNothing(t *testing.T) {
	f := makeCoroProgramBootstrapTestFixtureV1()
	allocations := testing.AllocsPerRun(1000, func() {
		programViewSinkV1, programCodeSinkV1 = ValidateProgramV1(&f.manifest)
		programStepSinkV1, programCodeSinkV1 = ResolveProgramStepV1(programViewSinkV1, 1)
	})
	if allocations != 0 {
		t.Fatalf("validation and resolution allocations = %v, want 0", allocations)
	}
	if programCodeSinkV1 != ProgramValidationOKV1 || programStepSinkV1.Descriptor != &f.descriptors[2] {
		t.Fatal("allocation run did not preserve the resolved main step")
	}
}

func TestValidatedProgramV1ConcurrentRead(t *testing.T) {
	f := makeCoroProgramBootstrapTestFixtureV1()
	view := requireProgramViewV1(t, &f.manifest)
	const (
		workers    = 16
		iterations = 1000
	)
	var failed atomic.Bool
	var wg sync.WaitGroup
	wg.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func() {
			defer wg.Done()
			for iteration := 0; iteration < iterations; iteration++ {
				for index := uintptr(0); index < 2; index++ {
					step, code := ResolveProgramStepV1(view, index)
					if code != ProgramValidationOKV1 || step.Kind != ProgramStepCoroRootV1 ||
						step.Descriptor == nil || step.Factory == nil {
						failed.Store(true)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	if failed.Load() {
		t.Fatal("concurrent validated-view resolution failed")
	}
}
