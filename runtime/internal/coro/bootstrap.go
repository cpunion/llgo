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

// The v1 bootstrap ABI is deliberately pointer-size neutral. These structures
// mirror compiler-emitted LLVM constants; keep uintptr and pointer fields in
// the same order so the layouts also match wasm32, embedded, and bare-metal
// targets. Non-null pointers come from the linked program image and therefore
// must denote readable constants; structural validation can reject alignment,
// count, and address overflow, but cannot safely probe an arbitrary unmapped
// address supplied by untrusted native memory.
const (
	ProgramManifestVersionV1   uint32 = 1
	ProgramBootstrapVersionV1  uint32 = 1
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

const (
	ProgramStepFlagInitV1 uint32 = 1 << iota
	ProgramStepFlagMainV1
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
		size == 0 || count > ^uintptr(0)/size {
		return programArrayAddressV1
	}
	span := count * size
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
