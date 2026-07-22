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

type programBootstrapTestFixtureV2 struct {
	plainTargets     [5]byte
	bootstrapFactory byte
	rootFactories    [5]byte
	descriptors      [5]RootFactoryDescriptorV1
	anchorEntries    [5]unsafe.Pointer
	anchor           RootPackageAnchorV1
	packages         [1]unsafe.Pointer
	steps            [5]ProgramStepV1
	bootstrap        ProgramBootstrapV1
	manifest         ProgramManifestV1
}

var programStepRolesForTestV2 = [5]uint32{
	ProgramStepFlagInternalRuntimeInitV2,
	ProgramStepFlagCompilerABIInitV2,
	ProgramStepFlagPublicRuntimeInitV2,
	ProgramStepFlagMainPackageInitV2,
	ProgramStepFlagMainV2,
}

func newProgramBootstrapTestFixtureV2(coroMask uint32) *programBootstrapTestFixtureV2 {
	f := new(programBootstrapTestFixtureV2)
	f.plainTargets = [5]byte{0x31, 0x32, 0x33, 0x34, 0x35}
	f.bootstrapFactory = 0x41
	f.rootFactories = [5]byte{0x51, 0x52, 0x53, 0x54, 0x55}
	for index := range f.descriptors {
		f.descriptors[index] = RootFactoryDescriptorV1{
			Version:      RootFactoryVersionV1,
			HashLo:       uint64(0x100 + index),
			HashHi:       uint64(0x200 + index),
			Factory:      unsafe.Pointer(&f.rootFactories[index]),
			StartupAlign: 1,
			ResultAlign:  1,
		}
		f.anchorEntries[index] = unsafe.Pointer(&f.descriptors[index])
	}
	f.anchor = RootPackageAnchorV1{
		Version: RootPackageAnchorVersionV1,
		HashLo:  0xa01,
		HashHi:  0xa02,
		Count:   uintptr(len(f.anchorEntries)),
		Entries: unsafe.Pointer(&f.anchorEntries[0]),
	}
	f.packages[0] = unsafe.Pointer(&f.anchor)
	for index, role := range programStepRolesForTestV2 {
		if coroMask&(uint32(1)<<index) != 0 {
			f.steps[index] = ProgramStepV1{
				Kind:   uint32(ProgramStepCoroRootV1),
				Flags:  role,
				Target: unsafe.Pointer(&f.anchor),
				Aux:    uintptr(index),
			}
		} else {
			f.steps[index] = ProgramStepV1{
				Kind:   uint32(ProgramStepDirectPlainV1),
				Flags:  role,
				Target: unsafe.Pointer(&f.plainTargets[index]),
			}
		}
	}
	f.bootstrap = ProgramBootstrapV1{
		Version:   ProgramBootstrapVersionV2,
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

func requireProgramViewV2(t *testing.T, f *programBootstrapTestFixtureV2) ProgramViewV2 {
	t.Helper()
	view, code := ValidateRunnableProgramV2(&f.manifest, unsafe.Pointer(&f.bootstrapFactory))
	if code != ProgramValidationOKV2 {
		t.Fatalf("ValidateRunnableProgramV2 code = %d, want success", code)
	}
	return view
}

func TestValidateAndResolveEveryHeterogeneousProgramV2(t *testing.T) {
	for mask := uint32(0); mask < 1<<5; mask++ {
		f := newProgramBootstrapTestFixtureV2(mask)
		view := requireProgramViewV2(t, f)
		for index, role := range programStepRolesForTestV2 {
			step, code := ResolveProgramStepV2(view, uintptr(index))
			if code != ProgramValidationOKV2 || step.Flags != role {
				t.Fatalf("mask %#x step %d = (%+v, %d), want role %#x", mask, index, step, code, role)
			}
			if mask&(uint32(1)<<index) == 0 {
				want := unsafe.Pointer(&f.plainTargets[index])
				if step.Kind != ProgramStepDirectPlainV2 || step.Plain != want ||
					step.Descriptor != nil || step.Factory != nil {
					t.Fatalf("mask %#x step %d = %+v, want direct target %p", mask, index, step, want)
				}
			} else {
				want := &f.descriptors[index]
				if step.Kind != ProgramStepCoroRootV2 || step.Plain != nil ||
					step.Descriptor != want || step.Factory != want.Factory {
					t.Fatalf("mask %#x step %d = %+v, want coroutine descriptor %p", mask, index, step, want)
				}
			}
		}
		if f.plainTargets != [5]byte{0x31, 0x32, 0x33, 0x34, 0x35} ||
			f.rootFactories != [5]byte{0x51, 0x52, 0x53, 0x54, 0x55} || f.bootstrapFactory != 0x41 {
			t.Fatalf("mask %#x validation or resolution invoked a target/factory", mask)
		}
	}
}

func TestValidateRunnableProgramV2RejectsMalformedTable(t *testing.T) {
	tests := []struct {
		name   string
		want   ProgramValidationCodeV2
		mutate func(*programBootstrapTestFixtureV2)
	}{
		{"manifest version", ProgramValidationManifestVersionV2, func(f *programBootstrapTestFixtureV2) { f.manifest.Version = 2 }},
		{"manifest flags", ProgramValidationManifestFlagsV2, func(f *programBootstrapTestFixtureV2) { f.manifest.Flags = 1 }},
		{"nil bootstrap", ProgramValidationNilBootstrapV2, func(f *programBootstrapTestFixtureV2) { f.manifest.Bootstrap = nil }},
		{"bootstrap v1", ProgramValidationBootstrapVersionV2, func(f *programBootstrapTestFixtureV2) { f.bootstrap.Version = uint32(1) }},
		{"unknown bootstrap version", ProgramValidationBootstrapVersionV2, func(f *programBootstrapTestFixtureV2) { f.bootstrap.Version = 3 }},
		{"bootstrap flags", ProgramValidationBootstrapFlagsV2, func(f *programBootstrapTestFixtureV2) { f.bootstrap.Flags = 1 }},
		{"bootstrap hash", ProgramValidationBootstrapHashV2, func(f *programBootstrapTestFixtureV2) { f.bootstrap.HashHi++ }},
		{"four steps", ProgramValidationStepCountV2, func(f *programBootstrapTestFixtureV2) { f.bootstrap.StepCount = 4 }},
		{"six steps", ProgramValidationStepCountV2, func(f *programBootstrapTestFixtureV2) { f.bootstrap.StepCount = 6 }},
		{"nil steps", ProgramValidationStepCountPointerV2, func(f *programBootstrapTestFixtureV2) { f.bootstrap.Steps = nil }},
		{"nil package anchor", ProgramValidationNilPackageAnchorV2, func(f *programBootstrapTestFixtureV2) { f.packages[0] = nil }},
		{"anchor version", ProgramValidationPackageAnchorVersionV2, func(f *programBootstrapTestFixtureV2) { f.anchor.Version = 2 }},
		{"anchor flags", ProgramValidationPackageAnchorFlagsV2, func(f *programBootstrapTestFixtureV2) { f.anchor.Flags = 1 }},
		{"nil descriptor", ProgramValidationNilRootDescriptorV2, func(f *programBootstrapTestFixtureV2) { f.anchorEntries[0] = nil }},
		{"descriptor version", ProgramValidationRootDescriptorVersionV2, func(f *programBootstrapTestFixtureV2) { f.descriptors[0].Version = 2 }},
		{"descriptor flags", ProgramValidationRootDescriptorFlagsV2, func(f *programBootstrapTestFixtureV2) { f.descriptors[0].Flags = 1 }},
		{"descriptor factory", ProgramValidationRootDescriptorFactoryV2, func(f *programBootstrapTestFixtureV2) { f.descriptors[0].Factory = nil }},
		{"descriptor startup layout", ProgramValidationRootStartupLayoutV2, func(f *programBootstrapTestFixtureV2) { f.descriptors[0].StartupAlign = 0 }},
		{"descriptor result layout", ProgramValidationRootResultLayoutV2, func(f *programBootstrapTestFixtureV2) { f.descriptors[0].ResultAlign = 0 }},
		{"step kind zero", ProgramValidationStepKindV2, func(f *programBootstrapTestFixtureV2) { f.steps[0].Kind = 0 }},
		{"step kind unknown", ProgramValidationStepKindV2, func(f *programBootstrapTestFixtureV2) { f.steps[0].Kind = 3 }},
		{"step target", ProgramValidationStepTargetV2, func(f *programBootstrapTestFixtureV2) { f.steps[0].Target = nil }},
		{"direct aux", ProgramValidationStepAuxV2, func(f *programBootstrapTestFixtureV2) { f.steps[0].Aux = 1 }},
		{"coro anchor", ProgramValidationStepAnchorV2, func(f *programBootstrapTestFixtureV2) {
			f.steps[1].Target = unsafe.Pointer(&f.plainTargets[1])
		}},
		{"coro descriptor index", ProgramValidationStepDescriptorIndexV2, func(f *programBootstrapTestFixtureV2) { f.steps[1].Aux = f.anchor.Count }},
		{"coro startup payload", ProgramValidationStepPayloadV2, func(f *programBootstrapTestFixtureV2) { f.descriptors[1].StartupSize = 1 }},
		{"coro result payload", ProgramValidationStepPayloadV2, func(f *programBootstrapTestFixtureV2) { f.descriptors[1].ResultSize = 1 }},
		{"bootstrap factory", ProgramValidationBootstrapFactoryV2, func(f *programBootstrapTestFixtureV2) { f.bootstrap.Factory = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Bits 1 and 3 select coroutine steps while leaving direct steps for
			// the kind-specific mutations above.
			f := newProgramBootstrapTestFixtureV2(1<<1 | 1<<3)
			test.mutate(f)
			_, code := ValidateRunnableProgramV2(&f.manifest, unsafe.Pointer(&f.bootstrapFactory))
			if code != test.want {
				t.Fatalf("validation code = %d, want %d", code, test.want)
			}
		})
	}

	for index := range programStepRolesForTestV2 {
		for name, role := range map[string]uint32{
			"zero":     0,
			"wrong":    programStepRolesForTestV2[(index+1)%len(programStepRolesForTestV2)],
			"multiple": programStepRolesForTestV2[index] | programStepRolesForTestV2[(index+1)%len(programStepRolesForTestV2)],
			"unknown":  1 << 12,
		} {
			t.Run(name+" role", func(t *testing.T) {
				f := newProgramBootstrapTestFixtureV2(1 << 1)
				f.steps[index].Flags = role
				_, code := ValidateRunnableProgramV2(&f.manifest, unsafe.Pointer(&f.bootstrapFactory))
				if code != ProgramValidationStepRoleV2 {
					t.Fatalf("step %d validation code = %d, want role failure", index, code)
				}
			})
		}
	}
}

func TestValidateRunnableProgramV2BindsExactFactory(t *testing.T) {
	f := newProgramBootstrapTestFixtureV2(0b01010)
	expected := unsafe.Pointer(&f.bootstrapFactory)
	if _, code := ValidateRunnableProgramV2(&f.manifest, expected); code != ProgramValidationOKV2 {
		t.Fatalf("exact factory validation code = %d, want success", code)
	}
	if _, code := ValidateRunnableProgramV2(&f.manifest, nil); code != ProgramValidationBootstrapFactoryIdentityV2 {
		t.Fatalf("nil expected factory code = %d, want identity failure", code)
	}
	other := byte(0x42)
	if _, code := ValidateRunnableProgramV2(&f.manifest, unsafe.Pointer(&other)); code != ProgramValidationBootstrapFactoryIdentityV2 {
		t.Fatalf("different expected factory code = %d, want identity failure", code)
	}
}

func TestResolvedProgramViewV2IsImmutableSnapshot(t *testing.T) {
	f := newProgramBootstrapTestFixtureV2(0b01010)
	view := requireProgramViewV2(t, f)
	wantPlain := f.steps[0].Target
	wantDescriptor := &f.descriptors[1]
	wantFactory := f.descriptors[1].Factory
	f.steps = [5]ProgramStepV1{}
	f.descriptors[1].Factory = nil

	plain, plainCode := ResolveProgramStepV2(view, 0)
	root, rootCode := ResolveProgramStepV2(view, 1)
	if plainCode != ProgramValidationOKV2 || plain.Plain != wantPlain ||
		rootCode != ProgramValidationOKV2 || root.Descriptor != wantDescriptor || root.Factory != wantFactory {
		t.Fatalf("snapshot changed: plain=(%+v,%d) root=(%+v,%d)", plain, plainCode, root, rootCode)
	}
	if _, code := ResolveProgramStepV2(ProgramViewV2{}, 0); code != ProgramValidationInvalidViewV2 {
		t.Fatalf("zero view code = %d, want invalid view", code)
	}
	if _, code := ResolveProgramStepV2(view, 5); code != ProgramValidationStepIndexV2 {
		t.Fatalf("out-of-range code = %d, want step index", code)
	}
}

var (
	programViewSinkV2 ProgramViewV2
	programStepSinkV2 ResolvedProgramStepV2
	programCodeSinkV2 ProgramValidationCodeV2
)

func TestValidateAndResolveProgramV2AllocateNothing(t *testing.T) {
	f := newProgramBootstrapTestFixtureV2(0b01010)
	expected := unsafe.Pointer(&f.bootstrapFactory)
	allocations := testing.AllocsPerRun(1000, func() {
		programViewSinkV2, programCodeSinkV2 = ValidateRunnableProgramV2(&f.manifest, expected)
		programStepSinkV2, programCodeSinkV2 = ResolveProgramStepV2(programViewSinkV2, 3)
	})
	if allocations != 0 {
		t.Fatalf("v2 validation and resolution allocations = %v, want 0", allocations)
	}
	if programCodeSinkV2 != ProgramValidationOKV2 || programStepSinkV2.Descriptor != &f.descriptors[3] {
		t.Fatal("allocation run did not preserve the resolved coroutine step")
	}
}
