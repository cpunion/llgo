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

import "unsafe"

// The bootstrap ABI layouts are deliberately pointer-size neutral. These
// structures mirror compiler-emitted LLVM constants; keep uintptr and pointer
// fields in the same order so the layouts also match wasm32, embedded, and
// bare-metal targets. Non-null pointers come from the linked program image and
// therefore must denote readable constants; structural validation can reject
// alignment, count, and address overflow, but cannot safely probe an arbitrary
// unmapped address supplied by untrusted native memory.
const (
	ProgramManifestVersionV1   uint32 = 1
	ProgramBootstrapVersionV1  uint32 = 1
	ProgramBootstrapVersionV2  uint32 = 2
	RootPackageAnchorVersionV1 uint32 = 1
	RootFactoryVersionV1       uint32 = 1
)

// ProgramStepKindV1 identifies how one compiler-emitted bootstrap step must be
// entered. It is data only: this package never invokes either pointer kind.
type ProgramStepKindV1 uint32

const (
	ProgramStepDirectPlainV1 ProgramStepKindV1 = 1
	ProgramStepCoroRootV1    ProgramStepKindV1 = 2
)

// Version two deliberately reuses the version-one step representation and
// kind numbers. These aliases let startup-driver code remain version-explicit
// without defining a second physical layout.
type ProgramStepKindV2 = ProgramStepKindV1

const (
	ProgramStepDirectPlainV2 ProgramStepKindV2 = ProgramStepDirectPlainV1
	ProgramStepCoroRootV2    ProgramStepKindV2 = ProgramStepCoroRootV1
)

const (
	ProgramStepFlagInitV1 uint32 = 1 << iota
	ProgramStepFlagMainV1
)

// Version-two roles describe the complete heterogeneous startup sequence.
// Their bit values are scoped by ProgramBootstrapVersionV2 and therefore may
// overlap the version-one roles. Every table entry must contain exactly the
// role at its canonical position.
const (
	ProgramStepFlagInternalRuntimeInitV2 uint32 = 1 << iota
	ProgramStepFlagCompilerABIInitV2
	ProgramStepFlagPublicRuntimeInitV2
	ProgramStepFlagMainPackageInitV2
	ProgramStepFlagMainV2
)

// ProgramManifestV1 is the runtime view of
// __llgo_coro_program_manifest_v1.
type ProgramManifestV1 struct {
	Version      uint32
	Flags        uint32
	HashLo       uint64
	HashHi       uint64
	PackageCount uintptr
	Packages     unsafe.Pointer
	Bootstrap    unsafe.Pointer
}

// ProgramBootstrapV1 describes the complete, ordered startup program. Factory
// is allowed to be nil only while validating a static Phase13-A descriptor;
// runnable validation rejects it.
type ProgramBootstrapV1 struct {
	Version   uint32
	Flags     uint32
	HashLo    uint64
	HashHi    uint64
	StepCount uintptr
	Steps     unsafe.Pointer
	Factory   unsafe.Pointer
}

// ProgramStepV1 has one of two fixed representations:
//
//   - DirectPlain: Target is a void() C-ABI function and Aux is zero.
//   - CoroRoot: Target is a package anchor and Aux is a descriptor index.
type ProgramStepV1 struct {
	Kind   uint32
	Flags  uint32
	Target unsafe.Pointer
	Aux    uintptr
}

// ProgramBootstrapV2 and ProgramStepV2 reuse the pointer-size-neutral v1
// physical layouts. ProgramBootstrapV2 is distinguished by Version == 2 and
// by its exact five-role step program.
type ProgramBootstrapV2 = ProgramBootstrapV1
type ProgramStepV2 = ProgramStepV1

// RootPackageAnchorV1 mirrors the package registry emitted by cl.
type RootPackageAnchorV1 struct {
	Version uint32
	Flags   uint32
	HashLo  uint64
	HashHi  uint64
	Count   uintptr
	Entries unsafe.Pointer
}

// RootFactoryDescriptorV1 mirrors one typed coroutine root descriptor.
type RootFactoryDescriptorV1 struct {
	Version      uint32
	Flags        uint32
	HashLo       uint64
	HashHi       uint64
	Factory      unsafe.Pointer
	StartupSize  uintptr
	StartupAlign uintptr
	ResultSize   uintptr
	ResultAlign  uintptr
}

// ProgramValidationCodeV1 is an allocation-free integer result code. It does
// not implement error because this target-neutral layer must not introduce an
// interface, formatting, or allocation dependency.
type ProgramValidationCodeV1 uint32

const (
	ProgramValidationOKV1 ProgramValidationCodeV1 = iota
	ProgramValidationNilManifestV1
	ProgramValidationManifestAddressV1
	ProgramValidationManifestVersionV1
	ProgramValidationManifestFlagsV1
	ProgramValidationPackageCountPointerV1
	ProgramValidationPackageTableAddressV1
	ProgramValidationNilBootstrapV1
	ProgramValidationBootstrapAddressV1
	ProgramValidationBootstrapVersionV1
	ProgramValidationBootstrapFlagsV1
	ProgramValidationBootstrapHashV1
	ProgramValidationStepCountV1
	ProgramValidationStepCountPointerV1
	ProgramValidationStepTableAddressV1
	ProgramValidationBootstrapFactoryV1
	ProgramValidationNilPackageAnchorV1
	ProgramValidationPackageAnchorAddressV1
	ProgramValidationDuplicatePackageAnchorV1
	ProgramValidationPackageAnchorVersionV1
	ProgramValidationPackageAnchorFlagsV1
	ProgramValidationEmptyPackageAnchorV1
	ProgramValidationDescriptorCountPointerV1
	ProgramValidationDescriptorTableAddressV1
	ProgramValidationNilRootDescriptorV1
	ProgramValidationRootDescriptorAddressV1
	ProgramValidationDuplicateRootDescriptorV1
	ProgramValidationRootDescriptorVersionV1
	ProgramValidationRootDescriptorFlagsV1
	ProgramValidationRootDescriptorFactoryV1
	ProgramValidationRootStartupLayoutV1
	ProgramValidationRootResultLayoutV1
	ProgramValidationStepInitFlagsV1
	ProgramValidationStepMainFlagsV1
	ProgramValidationStepKindV1
	ProgramValidationStepTargetV1
	ProgramValidationStepAuxV1
	ProgramValidationStepAnchorV1
	ProgramValidationStepDescriptorIndexV1
	ProgramValidationStepPayloadV1
	ProgramValidationInvalidViewV1
	ProgramValidationStepIndexV1
	ProgramValidationBootstrapFactoryIdentityV1
	ProgramValidationRunnableStepKindV1
)

// ResolvedProgramStepV1 is a data-only action. Exactly one representation is
// populated: Plain for DirectPlain, or Descriptor and Factory for CoroRoot.
type ResolvedProgramStepV1 struct {
	Kind       ProgramStepKindV1
	Flags      uint32
	Plain      unsafe.Pointer
	Descriptor *RootFactoryDescriptorV1
	Factory    unsafe.Pointer
}

const validatedProgramMagicV1 uint32 = 0x42535431 // "BST1"

// ProgramViewV1 is opaque despite being a public hand-off type: callers can
// only obtain a valid value from validation and cannot construct or mutate its
// private contents. Copying the two resolved actions also prevents later table
// mutation from changing an already validated startup plan.
type ProgramViewV1 struct {
	magic   uint32
	factory unsafe.Pointer
	init    ResolvedProgramStepV1
	main    ResolvedProgramStepV1
}

type programArrayStateV1 uint8

const (
	programArrayOKV1 programArrayStateV1 = iota
	programArrayCountPointerV1
	programArrayAddressV1
)

// checkedProgramSpanV1 performs a full-width uintptr multiplication without
// division. Division-by-zero guards in compiler-owned runtime validation would
// otherwise introduce an async panic helper into the synchronous process-entry
// ABI, even though checkedProgramArrayV1 rejects a zero element size first.
func checkedProgramSpanV1(count, size uintptr) (uintptr, bool) {
	const mask32 uint64 = 1<<32 - 1
	x := uint64(count)
	y := uint64(size)
	x0 := x & mask32
	x1 := x >> 32
	y0 := y & mask32
	y1 := y >> 32
	w0 := x0 * y0
	t := x1*y0 + w0>>32
	w1 := t & mask32
	w2 := t >> 32
	w1 += x0 * y1
	hi := x1*y1 + w2 + w1>>32
	lo := x * y
	if hi != 0 || lo > uint64(^uintptr(0)) {
		return 0, false
	}
	return uintptr(lo), true
}

func checkedProgramArrayV1(base unsafe.Pointer, count, size, align uintptr) programArrayStateV1 {
	if count == 0 {
		if base != nil {
			return programArrayCountPointerV1
		}
		return programArrayOKV1
	}
	if base == nil {
		return programArrayCountPointerV1
	}
	address := uintptr(base)
	if align == 0 || align&(align-1) != 0 || address&(align-1) != 0 ||
		size == 0 {
		return programArrayAddressV1
	}
	span, ok := checkedProgramSpanV1(count, size)
	if !ok {
		return programArrayAddressV1
	}
	if address > ^uintptr(0)-(span-1) {
		return programArrayAddressV1
	}
	return programArrayOKV1
}

func checkedProgramObjectV1(object unsafe.Pointer, size, align uintptr) bool {
	return checkedProgramArrayV1(object, 1, size, align) == programArrayOKV1
}

func programPointerAtV1(base unsafe.Pointer, index uintptr) unsafe.Pointer {
	offset := index * unsafe.Sizeof(unsafe.Pointer(nil))
	return *(*unsafe.Pointer)(unsafe.Add(base, offset))
}

func programStepAtV1(base unsafe.Pointer, index uintptr) *ProgramStepV1 {
	offset := index * unsafe.Sizeof(ProgramStepV1{})
	return (*ProgramStepV1)(unsafe.Add(base, offset))
}

func rootDescriptorAtV1(anchor *RootPackageAnchorV1, index uintptr) *RootFactoryDescriptorV1 {
	return (*RootFactoryDescriptorV1)(programPointerAtV1(anchor.Entries, index))
}

func validProgramPayloadLayoutV1(size, align uintptr) bool {
	return align != 0 && align&(align-1) == 0 && size&(align-1) == 0
}

func programPackageAtV1(manifest *ProgramManifestV1, index uintptr) *RootPackageAnchorV1 {
	return (*RootPackageAnchorV1)(programPointerAtV1(manifest.Packages, index))
}

func duplicateProgramPackageV1(manifest *ProgramManifestV1, index uintptr, anchor *RootPackageAnchorV1) bool {
	for previous := uintptr(0); previous < index; previous++ {
		if programPackageAtV1(manifest, previous) == anchor {
			return true
		}
	}
	return false
}

func duplicateRootDescriptorV1(
	manifest *ProgramManifestV1, packageIndex, descriptorIndex uintptr, descriptor *RootFactoryDescriptorV1,
) bool {
	for previousPackage := uintptr(0); previousPackage <= packageIndex; previousPackage++ {
		anchor := programPackageAtV1(manifest, previousPackage)
		limit := anchor.Count
		if previousPackage == packageIndex {
			limit = descriptorIndex
		}
		for previousDescriptor := uintptr(0); previousDescriptor < limit; previousDescriptor++ {
			if rootDescriptorAtV1(anchor, previousDescriptor) == descriptor {
				return true
			}
		}
	}
	return false
}

func validateProgramCatalogV1(manifest *ProgramManifestV1) ProgramValidationCodeV1 {
	for packageIndex := uintptr(0); packageIndex < manifest.PackageCount; packageIndex++ {
		anchorPointer := programPointerAtV1(manifest.Packages, packageIndex)
		if anchorPointer == nil {
			return ProgramValidationNilPackageAnchorV1
		}
		if !checkedProgramObjectV1(anchorPointer, unsafe.Sizeof(RootPackageAnchorV1{}), unsafe.Alignof(RootPackageAnchorV1{})) {
			return ProgramValidationPackageAnchorAddressV1
		}
		anchor := (*RootPackageAnchorV1)(anchorPointer)
		if duplicateProgramPackageV1(manifest, packageIndex, anchor) {
			return ProgramValidationDuplicatePackageAnchorV1
		}
		if anchor.Version != RootPackageAnchorVersionV1 {
			return ProgramValidationPackageAnchorVersionV1
		}
		if anchor.Flags != 0 {
			return ProgramValidationPackageAnchorFlagsV1
		}
		if anchor.Count == 0 {
			if anchor.Entries != nil {
				return ProgramValidationDescriptorCountPointerV1
			}
			return ProgramValidationEmptyPackageAnchorV1
		}
		switch checkedProgramArrayV1(
			anchor.Entries,
			anchor.Count,
			unsafe.Sizeof(unsafe.Pointer(nil)),
			unsafe.Alignof(unsafe.Pointer(nil)),
		) {
		case programArrayCountPointerV1:
			return ProgramValidationDescriptorCountPointerV1
		case programArrayAddressV1:
			return ProgramValidationDescriptorTableAddressV1
		}
		for descriptorIndex := uintptr(0); descriptorIndex < anchor.Count; descriptorIndex++ {
			descriptorPointer := programPointerAtV1(anchor.Entries, descriptorIndex)
			if descriptorPointer == nil {
				return ProgramValidationNilRootDescriptorV1
			}
			if !checkedProgramObjectV1(descriptorPointer, unsafe.Sizeof(RootFactoryDescriptorV1{}), unsafe.Alignof(RootFactoryDescriptorV1{})) {
				return ProgramValidationRootDescriptorAddressV1
			}
			descriptor := (*RootFactoryDescriptorV1)(descriptorPointer)
			if duplicateRootDescriptorV1(manifest, packageIndex, descriptorIndex, descriptor) {
				return ProgramValidationDuplicateRootDescriptorV1
			}
			if descriptor.Version != RootFactoryVersionV1 {
				return ProgramValidationRootDescriptorVersionV1
			}
			if descriptor.Flags != 0 {
				return ProgramValidationRootDescriptorFlagsV1
			}
			if descriptor.Factory == nil {
				return ProgramValidationRootDescriptorFactoryV1
			}
			if !validProgramPayloadLayoutV1(descriptor.StartupSize, descriptor.StartupAlign) {
				return ProgramValidationRootStartupLayoutV1
			}
			if !validProgramPayloadLayoutV1(descriptor.ResultSize, descriptor.ResultAlign) {
				return ProgramValidationRootResultLayoutV1
			}
		}
	}
	return ProgramValidationOKV1
}

func findProgramPackageV1(manifest *ProgramManifestV1, target unsafe.Pointer) *RootPackageAnchorV1 {
	for index := uintptr(0); index < manifest.PackageCount; index++ {
		anchor := programPackageAtV1(manifest, index)
		if unsafe.Pointer(anchor) == target {
			return anchor
		}
	}
	return nil
}

func resolveValidatedProgramStepV1(
	manifest *ProgramManifestV1, step *ProgramStepV1, expectedFlags uint32,
) (ResolvedProgramStepV1, ProgramValidationCodeV1) {
	if step.Flags != expectedFlags {
		if expectedFlags == ProgramStepFlagInitV1 {
			return ResolvedProgramStepV1{}, ProgramValidationStepInitFlagsV1
		}
		return ResolvedProgramStepV1{}, ProgramValidationStepMainFlagsV1
	}
	if step.Target == nil {
		return ResolvedProgramStepV1{}, ProgramValidationStepTargetV1
	}
	switch ProgramStepKindV1(step.Kind) {
	case ProgramStepDirectPlainV1:
		if step.Aux != 0 {
			return ResolvedProgramStepV1{}, ProgramValidationStepAuxV1
		}
		return ResolvedProgramStepV1{
			Kind:  ProgramStepDirectPlainV1,
			Flags: step.Flags,
			Plain: step.Target,
		}, ProgramValidationOKV1
	case ProgramStepCoroRootV1:
		anchor := findProgramPackageV1(manifest, step.Target)
		if anchor == nil {
			return ResolvedProgramStepV1{}, ProgramValidationStepAnchorV1
		}
		if step.Aux >= anchor.Count {
			return ResolvedProgramStepV1{}, ProgramValidationStepDescriptorIndexV1
		}
		descriptor := rootDescriptorAtV1(anchor, step.Aux)
		if descriptor.StartupSize != 0 || descriptor.StartupAlign != 1 ||
			descriptor.ResultSize != 0 || descriptor.ResultAlign != 1 {
			return ResolvedProgramStepV1{}, ProgramValidationStepPayloadV1
		}
		return ResolvedProgramStepV1{
			Kind:       ProgramStepCoroRootV1,
			Flags:      step.Flags,
			Descriptor: descriptor,
			Factory:    descriptor.Factory,
		}, ProgramValidationOKV1
	default:
		return ResolvedProgramStepV1{}, ProgramValidationStepKindV1
	}
}

func validateProgramV1(manifest *ProgramManifestV1, requireFactory bool) (ProgramViewV1, ProgramValidationCodeV1) {
	if manifest == nil {
		return ProgramViewV1{}, ProgramValidationNilManifestV1
	}
	if !checkedProgramObjectV1(unsafe.Pointer(manifest), unsafe.Sizeof(ProgramManifestV1{}), unsafe.Alignof(ProgramManifestV1{})) {
		return ProgramViewV1{}, ProgramValidationManifestAddressV1
	}
	if manifest.Version != ProgramManifestVersionV1 {
		return ProgramViewV1{}, ProgramValidationManifestVersionV1
	}
	if manifest.Flags != 0 {
		return ProgramViewV1{}, ProgramValidationManifestFlagsV1
	}
	switch checkedProgramArrayV1(
		manifest.Packages,
		manifest.PackageCount,
		unsafe.Sizeof(unsafe.Pointer(nil)),
		unsafe.Alignof(unsafe.Pointer(nil)),
	) {
	case programArrayCountPointerV1:
		return ProgramViewV1{}, ProgramValidationPackageCountPointerV1
	case programArrayAddressV1:
		return ProgramViewV1{}, ProgramValidationPackageTableAddressV1
	}
	if manifest.Bootstrap == nil {
		return ProgramViewV1{}, ProgramValidationNilBootstrapV1
	}
	if !checkedProgramObjectV1(manifest.Bootstrap, unsafe.Sizeof(ProgramBootstrapV1{}), unsafe.Alignof(ProgramBootstrapV1{})) {
		return ProgramViewV1{}, ProgramValidationBootstrapAddressV1
	}
	bootstrap := (*ProgramBootstrapV1)(manifest.Bootstrap)
	if bootstrap.Version != ProgramBootstrapVersionV1 {
		return ProgramViewV1{}, ProgramValidationBootstrapVersionV1
	}
	if bootstrap.Flags != 0 {
		return ProgramViewV1{}, ProgramValidationBootstrapFlagsV1
	}
	if bootstrap.HashLo != manifest.HashLo || bootstrap.HashHi != manifest.HashHi {
		return ProgramViewV1{}, ProgramValidationBootstrapHashV1
	}
	if bootstrap.StepCount != 2 {
		return ProgramViewV1{}, ProgramValidationStepCountV1
	}
	switch checkedProgramArrayV1(
		bootstrap.Steps,
		bootstrap.StepCount,
		unsafe.Sizeof(ProgramStepV1{}),
		unsafe.Alignof(ProgramStepV1{}),
	) {
	case programArrayCountPointerV1:
		return ProgramViewV1{}, ProgramValidationStepCountPointerV1
	case programArrayAddressV1:
		return ProgramViewV1{}, ProgramValidationStepTableAddressV1
	}
	if catalogError := validateProgramCatalogV1(manifest); catalogError != ProgramValidationOKV1 {
		return ProgramViewV1{}, catalogError
	}
	init, initError := resolveValidatedProgramStepV1(
		manifest,
		programStepAtV1(bootstrap.Steps, 0),
		ProgramStepFlagInitV1,
	)
	if initError != ProgramValidationOKV1 {
		return ProgramViewV1{}, initError
	}
	main, mainError := resolveValidatedProgramStepV1(
		manifest,
		programStepAtV1(bootstrap.Steps, 1),
		ProgramStepFlagMainV1,
	)
	if mainError != ProgramValidationOKV1 {
		return ProgramViewV1{}, mainError
	}
	if requireFactory && bootstrap.Factory == nil {
		return ProgramViewV1{}, ProgramValidationBootstrapFactoryV1
	}
	return ProgramViewV1{
		magic:   validatedProgramMagicV1,
		factory: bootstrap.Factory,
		init:    init,
		main:    main,
	}, ProgramValidationOKV1
}

// ValidateProgramDescriptorV1 validates the complete manifest, package
// catalog, descriptor catalog, and exact Init -> Main step program. It allows a
// nil bootstrap factory so the compiler can publish a Phase13-A static
// descriptor before the production entry switches to the coroutine driver.
func ValidateProgramDescriptorV1(manifest *ProgramManifestV1) (ProgramViewV1, ProgramValidationCodeV1) {
	return validateProgramV1(manifest, false)
}

// ValidateProgramV1 validates a runnable program and therefore also requires
// a non-nil bootstrap coroutine factory.
func ValidateProgramV1(manifest *ProgramManifestV1) (ProgramViewV1, ProgramValidationCodeV1) {
	return validateProgramV1(manifest, true)
}

// ValidateRunnableProgramV1 is the explicit spelling used by runtime startup.
func ValidateRunnableProgramV1(manifest *ProgramManifestV1) (ProgramViewV1, ProgramValidationCodeV1) {
	return validateProgramV1(manifest, true)
}

// ValidateRunnableDirectProgramV1 validates the first production bootstrap
// boundary. In addition to the complete manifest checks performed by
// ValidateRunnableProgramV1, it binds the descriptor to the exact factory the
// compiler will call directly and accepts only the fixed DirectPlain
// Init -> Main program supported by that factory.
//
// This function compares factory pointers as data. It never invokes the
// bootstrap factory or either program step.
func ValidateRunnableDirectProgramV1(
	manifest *ProgramManifestV1, expectedFactory unsafe.Pointer,
) (ProgramViewV1, ProgramValidationCodeV1) {
	program, code := validateProgramV1(manifest, true)
	if code != ProgramValidationOKV1 {
		return ProgramViewV1{}, code
	}
	if expectedFactory == nil || program.factory != expectedFactory {
		return ProgramViewV1{}, ProgramValidationBootstrapFactoryIdentityV1
	}
	if program.init.Kind != ProgramStepDirectPlainV1 || program.main.Kind != ProgramStepDirectPlainV1 {
		return ProgramViewV1{}, ProgramValidationRunnableStepKindV1
	}
	return program, ProgramValidationOKV1
}

// ResolveProgramStepV1 returns one action from an opaque validated view. It
// never calls the plain target or coroutine factory.
func ResolveProgramStepV1(program ProgramViewV1, index uintptr) (ResolvedProgramStepV1, ProgramValidationCodeV1) {
	if program.magic != validatedProgramMagicV1 {
		return ResolvedProgramStepV1{}, ProgramValidationInvalidViewV1
	}
	switch index {
	case 0:
		return program.init, ProgramValidationOKV1
	case 1:
		return program.main, ProgramValidationOKV1
	default:
		return ResolvedProgramStepV1{}, ProgramValidationStepIndexV1
	}
}

// ProgramValidationCodeV2 is the allocation-free result of validating the
// version-two heterogeneous startup table. It is deliberately independent of
// ProgramValidationCodeV1 even where both ABIs reject the same physical field.
type ProgramValidationCodeV2 uint32

const (
	ProgramValidationOKV2 ProgramValidationCodeV2 = iota
	ProgramValidationNilManifestV2
	ProgramValidationManifestAddressV2
	ProgramValidationManifestVersionV2
	ProgramValidationManifestFlagsV2
	ProgramValidationPackageCountPointerV2
	ProgramValidationPackageTableAddressV2
	ProgramValidationNilBootstrapV2
	ProgramValidationBootstrapAddressV2
	ProgramValidationBootstrapVersionV2
	ProgramValidationBootstrapFlagsV2
	ProgramValidationBootstrapHashV2
	ProgramValidationStepCountV2
	ProgramValidationStepCountPointerV2
	ProgramValidationStepTableAddressV2
	ProgramValidationBootstrapFactoryV2
	ProgramValidationNilPackageAnchorV2
	ProgramValidationPackageAnchorAddressV2
	ProgramValidationDuplicatePackageAnchorV2
	ProgramValidationPackageAnchorVersionV2
	ProgramValidationPackageAnchorFlagsV2
	ProgramValidationEmptyPackageAnchorV2
	ProgramValidationDescriptorCountPointerV2
	ProgramValidationDescriptorTableAddressV2
	ProgramValidationNilRootDescriptorV2
	ProgramValidationRootDescriptorAddressV2
	ProgramValidationDuplicateRootDescriptorV2
	ProgramValidationRootDescriptorVersionV2
	ProgramValidationRootDescriptorFlagsV2
	ProgramValidationRootDescriptorFactoryV2
	ProgramValidationRootStartupLayoutV2
	ProgramValidationRootResultLayoutV2
	ProgramValidationStepRoleV2
	ProgramValidationStepKindV2
	ProgramValidationStepTargetV2
	ProgramValidationStepAuxV2
	ProgramValidationStepAnchorV2
	ProgramValidationStepDescriptorIndexV2
	ProgramValidationStepPayloadV2
	ProgramValidationInvalidViewV2
	ProgramValidationStepIndexV2
	ProgramValidationBootstrapFactoryIdentityV2
)

// ResolvedProgramStepV2 is one validated heterogeneous startup action. Exactly
// one representation is populated: Plain for DirectPlain, or Descriptor and
// Factory for CoroRoot. Resolving a step never invokes either target.
type ResolvedProgramStepV2 struct {
	Kind       ProgramStepKindV2
	Flags      uint32
	Plain      unsafe.Pointer
	Descriptor *RootFactoryDescriptorV1
	Factory    unsafe.Pointer
}

const validatedProgramMagicV2 uint32 = 0x42535432 // "BST2"

// ProgramViewV2 is an immutable, allocation-free snapshot of the five startup
// actions. Its contents are private so only successful validation can produce
// a resolvable value.
type ProgramViewV2 struct {
	magic               uint32
	factory             unsafe.Pointer
	internalRuntimeInit ResolvedProgramStepV2
	compilerABIInit     ResolvedProgramStepV2
	publicRuntimeInit   ResolvedProgramStepV2
	mainPackageInit     ResolvedProgramStepV2
	main                ResolvedProgramStepV2
}

const programStepCountV2 uintptr = 5

// programCatalogValidationV2 translates validation of the shared v1 physical
// package/descriptor catalog into the independent v2 result namespace.
func programCatalogValidationV2(code ProgramValidationCodeV1) ProgramValidationCodeV2 {
	switch code {
	case ProgramValidationOKV1:
		return ProgramValidationOKV2
	case ProgramValidationNilPackageAnchorV1:
		return ProgramValidationNilPackageAnchorV2
	case ProgramValidationPackageAnchorAddressV1:
		return ProgramValidationPackageAnchorAddressV2
	case ProgramValidationDuplicatePackageAnchorV1:
		return ProgramValidationDuplicatePackageAnchorV2
	case ProgramValidationPackageAnchorVersionV1:
		return ProgramValidationPackageAnchorVersionV2
	case ProgramValidationPackageAnchorFlagsV1:
		return ProgramValidationPackageAnchorFlagsV2
	case ProgramValidationEmptyPackageAnchorV1:
		return ProgramValidationEmptyPackageAnchorV2
	case ProgramValidationDescriptorCountPointerV1:
		return ProgramValidationDescriptorCountPointerV2
	case ProgramValidationDescriptorTableAddressV1:
		return ProgramValidationDescriptorTableAddressV2
	case ProgramValidationNilRootDescriptorV1:
		return ProgramValidationNilRootDescriptorV2
	case ProgramValidationRootDescriptorAddressV1:
		return ProgramValidationRootDescriptorAddressV2
	case ProgramValidationDuplicateRootDescriptorV1:
		return ProgramValidationDuplicateRootDescriptorV2
	case ProgramValidationRootDescriptorVersionV1:
		return ProgramValidationRootDescriptorVersionV2
	case ProgramValidationRootDescriptorFlagsV1:
		return ProgramValidationRootDescriptorFlagsV2
	case ProgramValidationRootDescriptorFactoryV1:
		return ProgramValidationRootDescriptorFactoryV2
	case ProgramValidationRootStartupLayoutV1:
		return ProgramValidationRootStartupLayoutV2
	case ProgramValidationRootResultLayoutV1:
		return ProgramValidationRootResultLayoutV2
	default:
		// validateProgramCatalogV1 can only return the cases above. Keep this
		// fail closed if that implementation gains a new result.
		return ProgramValidationPackageTableAddressV2
	}
}

func resolveValidatedProgramStepV2(
	manifest *ProgramManifestV1, step *ProgramStepV1, expectedRole uint32,
) (ResolvedProgramStepV2, ProgramValidationCodeV2) {
	if step.Flags != expectedRole {
		return ResolvedProgramStepV2{}, ProgramValidationStepRoleV2
	}
	if step.Target == nil {
		return ResolvedProgramStepV2{}, ProgramValidationStepTargetV2
	}
	switch ProgramStepKindV2(step.Kind) {
	case ProgramStepDirectPlainV2:
		if step.Aux != 0 {
			return ResolvedProgramStepV2{}, ProgramValidationStepAuxV2
		}
		return ResolvedProgramStepV2{
			Kind:  ProgramStepDirectPlainV2,
			Flags: step.Flags,
			Plain: step.Target,
		}, ProgramValidationOKV2
	case ProgramStepCoroRootV2:
		anchor := findProgramPackageV1(manifest, step.Target)
		if anchor == nil {
			return ResolvedProgramStepV2{}, ProgramValidationStepAnchorV2
		}
		if step.Aux >= anchor.Count {
			return ResolvedProgramStepV2{}, ProgramValidationStepDescriptorIndexV2
		}
		descriptor := rootDescriptorAtV1(anchor, step.Aux)
		if descriptor.StartupSize != 0 || descriptor.StartupAlign != 1 ||
			descriptor.ResultSize != 0 || descriptor.ResultAlign != 1 {
			return ResolvedProgramStepV2{}, ProgramValidationStepPayloadV2
		}
		return ResolvedProgramStepV2{
			Kind:       ProgramStepCoroRootV2,
			Flags:      step.Flags,
			Descriptor: descriptor,
			Factory:    descriptor.Factory,
		}, ProgramValidationOKV2
	default:
		return ResolvedProgramStepV2{}, ProgramValidationStepKindV2
	}
}

// ValidateRunnableProgramV2 validates the shared manifest and package catalog,
// then the exact five-role heterogeneous startup program. It binds the table to
// expectedFactory by pointer identity and snapshots every resolved action.
// Validation performs no allocation and never invokes a target or factory.
func ValidateRunnableProgramV2(
	manifest *ProgramManifestV1, expectedFactory unsafe.Pointer,
) (ProgramViewV2, ProgramValidationCodeV2) {
	if manifest == nil {
		return ProgramViewV2{}, ProgramValidationNilManifestV2
	}
	if !checkedProgramObjectV1(
		unsafe.Pointer(manifest), unsafe.Sizeof(ProgramManifestV1{}), unsafe.Alignof(ProgramManifestV1{}),
	) {
		return ProgramViewV2{}, ProgramValidationManifestAddressV2
	}
	if manifest.Version != ProgramManifestVersionV1 {
		return ProgramViewV2{}, ProgramValidationManifestVersionV2
	}
	if manifest.Flags != 0 {
		return ProgramViewV2{}, ProgramValidationManifestFlagsV2
	}
	switch checkedProgramArrayV1(
		manifest.Packages,
		manifest.PackageCount,
		unsafe.Sizeof(unsafe.Pointer(nil)),
		unsafe.Alignof(unsafe.Pointer(nil)),
	) {
	case programArrayCountPointerV1:
		return ProgramViewV2{}, ProgramValidationPackageCountPointerV2
	case programArrayAddressV1:
		return ProgramViewV2{}, ProgramValidationPackageTableAddressV2
	}
	if manifest.Bootstrap == nil {
		return ProgramViewV2{}, ProgramValidationNilBootstrapV2
	}
	if !checkedProgramObjectV1(
		manifest.Bootstrap, unsafe.Sizeof(ProgramBootstrapV1{}), unsafe.Alignof(ProgramBootstrapV1{}),
	) {
		return ProgramViewV2{}, ProgramValidationBootstrapAddressV2
	}
	bootstrap := (*ProgramBootstrapV1)(manifest.Bootstrap)
	if bootstrap.Version != ProgramBootstrapVersionV2 {
		return ProgramViewV2{}, ProgramValidationBootstrapVersionV2
	}
	if bootstrap.Flags != 0 {
		return ProgramViewV2{}, ProgramValidationBootstrapFlagsV2
	}
	if bootstrap.HashLo != manifest.HashLo || bootstrap.HashHi != manifest.HashHi {
		return ProgramViewV2{}, ProgramValidationBootstrapHashV2
	}
	if bootstrap.StepCount != programStepCountV2 {
		return ProgramViewV2{}, ProgramValidationStepCountV2
	}
	switch checkedProgramArrayV1(
		bootstrap.Steps,
		bootstrap.StepCount,
		unsafe.Sizeof(ProgramStepV1{}),
		unsafe.Alignof(ProgramStepV1{}),
	) {
	case programArrayCountPointerV1:
		return ProgramViewV2{}, ProgramValidationStepCountPointerV2
	case programArrayAddressV1:
		return ProgramViewV2{}, ProgramValidationStepTableAddressV2
	}
	if catalogCode := programCatalogValidationV2(validateProgramCatalogV1(manifest)); catalogCode != ProgramValidationOKV2 {
		return ProgramViewV2{}, catalogCode
	}
	if bootstrap.Factory == nil {
		return ProgramViewV2{}, ProgramValidationBootstrapFactoryV2
	}
	if expectedFactory == nil || bootstrap.Factory != expectedFactory {
		return ProgramViewV2{}, ProgramValidationBootstrapFactoryIdentityV2
	}

	program := ProgramViewV2{
		magic:   validatedProgramMagicV2,
		factory: bootstrap.Factory,
	}
	var code ProgramValidationCodeV2
	program.internalRuntimeInit, code = resolveValidatedProgramStepV2(
		manifest, programStepAtV1(bootstrap.Steps, 0), ProgramStepFlagInternalRuntimeInitV2,
	)
	if code != ProgramValidationOKV2 {
		return ProgramViewV2{}, code
	}
	program.compilerABIInit, code = resolveValidatedProgramStepV2(
		manifest, programStepAtV1(bootstrap.Steps, 1), ProgramStepFlagCompilerABIInitV2,
	)
	if code != ProgramValidationOKV2 {
		return ProgramViewV2{}, code
	}
	program.publicRuntimeInit, code = resolveValidatedProgramStepV2(
		manifest, programStepAtV1(bootstrap.Steps, 2), ProgramStepFlagPublicRuntimeInitV2,
	)
	if code != ProgramValidationOKV2 {
		return ProgramViewV2{}, code
	}
	program.mainPackageInit, code = resolveValidatedProgramStepV2(
		manifest, programStepAtV1(bootstrap.Steps, 3), ProgramStepFlagMainPackageInitV2,
	)
	if code != ProgramValidationOKV2 {
		return ProgramViewV2{}, code
	}
	program.main, code = resolveValidatedProgramStepV2(
		manifest, programStepAtV1(bootstrap.Steps, 4), ProgramStepFlagMainV2,
	)
	if code != ProgramValidationOKV2 {
		return ProgramViewV2{}, code
	}
	return program, ProgramValidationOKV2
}

// ResolveProgramStepV2 returns one action from an opaque validated view. It
// never calls the plain target or coroutine factory.
func ResolveProgramStepV2(program ProgramViewV2, index uintptr) (ResolvedProgramStepV2, ProgramValidationCodeV2) {
	if program.magic != validatedProgramMagicV2 {
		return ResolvedProgramStepV2{}, ProgramValidationInvalidViewV2
	}
	switch index {
	case 0:
		return program.internalRuntimeInit, ProgramValidationOKV2
	case 1:
		return program.compilerABIInit, ProgramValidationOKV2
	case 2:
		return program.publicRuntimeInit, ProgramValidationOKV2
	case 3:
		return program.mainPackageInit, ProgramValidationOKV2
	case 4:
		return program.main, ProgramValidationOKV2
	default:
		return ResolvedProgramStepV2{}, ProgramValidationStepIndexV2
	}
}
