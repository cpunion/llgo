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
	ProgramBootstrapVersionV2  uint32 = 2
	RootPackageAnchorVersionV1 uint32 = 1
	RootFactoryVersionV1       uint32 = 1
)

// ProgramCapabilitiesV2 is the compiler-proven set of optional physical
// services used by the final program. It is carried in ProgramBootstrapV2.Flags
// and is deliberately independent of the target's larger capability set.
type ProgramCapabilitiesV2 uint32

const (
	ProgramCapabilityWorkerV2 ProgramCapabilitiesV2 = 1 << iota
)

func (capabilities ProgramCapabilitiesV2) Valid() bool {
	const known = ProgramCapabilityWorkerV2
	return capabilities&^known == 0
}

func (capabilities ProgramCapabilitiesV2) Worker() bool {
	return capabilities&ProgramCapabilityWorkerV2 != 0
}

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

// Version-two roles describe the complete heterogeneous startup sequence.
// Their bit values are scoped by ProgramBootstrapVersionV2 and therefore may
// overlap the version-one roles. Every table entry must contain exactly the
// role at its canonical position.
const (
	ProgramStepFlagInternalRuntimeInitV2 uint32 = 1 << iota
	ProgramStepFlagCompilerABIInitV2
	ProgramStepFlagPublicRuntimeInitV2
	ProgramStepFlagPackageInitV2
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

// ProgramBootstrapV1 is the pointer-size-neutral physical startup-table layout.
// The stackless runtime accepts only ProgramBootstrapVersionV2.
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
// by its ordered startup program.
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

// ProgramValidationCodeV1 is the allocation-free result of validating the
// shared physical package/descriptor catalog. The V1 suffix describes that
// retained layout, not an accepted version-one startup program.
type ProgramValidationCodeV1 uint32

const (
	ProgramValidationOKV1 ProgramValidationCodeV1 = iota
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
)

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

// ProgramViewV2 is an allocation-free identity and digest of a validated
// startup program. Compiler-emitted tables are immutable. Resolve nevertheless
// revalidates and compares the digest so a synthetic mutable table fails closed
// instead of silently changing the action represented by an existing view.
type ProgramViewV2 struct {
	magic     uint32
	manifest  *ProgramManifestV1
	bootstrap *ProgramBootstrapV1
	factory   unsafe.Pointer
	steps     unsafe.Pointer
	stepCount uintptr
	digestLo  uint64
	digestHi  uint64
}

const programStepMinimumV2 uintptr = 5

func programStepRoleV2(index, count uintptr) (uint32, bool) {
	if count < programStepMinimumV2 || index >= count {
		return 0, false
	}
	switch index {
	case 0:
		return ProgramStepFlagInternalRuntimeInitV2, true
	case 1:
		return ProgramStepFlagCompilerABIInitV2, true
	case 2:
		return ProgramStepFlagPublicRuntimeInitV2, true
	case count - 1:
		return ProgramStepFlagMainV2, true
	default:
		return ProgramStepFlagPackageInitV2, true
	}
}

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

// mixProgramDigestV2 is a small non-cryptographic mixer for compiler-owned
// constant startup data. The two independent words make accidental equality
// across a changed raw step or resolved descriptor extremely unlikely without
// adding a hash dependency or allocation to process entry.
func mixProgramDigestV2(lo, hi, value uint64) (uint64, uint64) {
	lo ^= value
	lo *= 1099511628211
	hi ^= value + 0x9e3779b97f4a7c15 + hi<<6 + hi>>2
	hi = hi<<31 | hi>>33
	hi *= 0x9ddfea08eb382d69
	return lo, hi
}

func programStepsDigestV2(
	manifest *ProgramManifestV1, bootstrap *ProgramBootstrapV1,
) (uint64, uint64, ProgramValidationCodeV2) {
	lo := uint64(14695981039346656037)
	hi := uint64(0x6eed0e9da4d94a4f)
	lo, hi = mixProgramDigestV2(lo, hi, uint64(bootstrap.Flags))
	lo, hi = mixProgramDigestV2(lo, hi, uint64(bootstrap.StepCount))
	lo, hi = mixProgramDigestV2(lo, hi, bootstrap.HashLo)
	lo, hi = mixProgramDigestV2(lo, hi, bootstrap.HashHi)
	lo, hi = mixProgramDigestV2(lo, hi, uint64(uintptr(bootstrap.Factory)))
	for index := uintptr(0); index < bootstrap.StepCount; index++ {
		role, ok := programStepRoleV2(index, bootstrap.StepCount)
		if !ok {
			return 0, 0, ProgramValidationStepCountV2
		}
		raw := programStepAtV1(bootstrap.Steps, index)
		resolved, code := resolveValidatedProgramStepV2(manifest, raw, role)
		if code != ProgramValidationOKV2 {
			return 0, 0, code
		}
		values := [...]uint64{
			uint64(index),
			uint64(raw.Kind),
			uint64(raw.Flags),
			uint64(uintptr(raw.Target)),
			uint64(raw.Aux),
			uint64(resolved.Kind),
			uint64(resolved.Flags),
			uint64(uintptr(resolved.Plain)),
			uint64(uintptr(unsafe.Pointer(resolved.Descriptor))),
			uint64(uintptr(resolved.Factory)),
		}
		for _, value := range values {
			lo, hi = mixProgramDigestV2(lo, hi, value)
		}
	}
	return lo, hi, ProgramValidationOKV2
}

// ValidateRunnableProgramV2 validates the shared manifest and package catalog,
// then the variable-length heterogeneous startup program. It binds the table
// to expectedFactory by pointer identity and records its resolved digest.
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
	if !ProgramCapabilitiesV2(bootstrap.Flags).Valid() {
		return ProgramViewV2{}, ProgramValidationBootstrapFlagsV2
	}
	if bootstrap.HashLo != manifest.HashLo || bootstrap.HashHi != manifest.HashHi {
		return ProgramViewV2{}, ProgramValidationBootstrapHashV2
	}
	if bootstrap.StepCount < programStepMinimumV2 {
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

	digestLo, digestHi, code := programStepsDigestV2(manifest, bootstrap)
	if code != ProgramValidationOKV2 {
		return ProgramViewV2{}, code
	}
	return ProgramViewV2{
		magic:     validatedProgramMagicV2,
		manifest:  manifest,
		bootstrap: bootstrap,
		factory:   bootstrap.Factory,
		steps:     bootstrap.Steps,
		stepCount: bootstrap.StepCount,
		digestLo:  digestLo,
		digestHi:  digestHi,
	}, ProgramValidationOKV2
}

// ResolveProgramCapabilitiesV2 returns the optional-service demand from an
// opaque validated view. Like ResolveProgramStepV2 it revalidates mutable test
// storage and compares the complete digest before publishing any fact.
func ResolveProgramCapabilitiesV2(
	program ProgramViewV2,
) (ProgramCapabilitiesV2, ProgramValidationCodeV2) {
	if program.magic != validatedProgramMagicV2 {
		return 0, ProgramValidationInvalidViewV2
	}
	current, code := ValidateRunnableProgramV2(program.manifest, program.factory)
	if code != ProgramValidationOKV2 || current.bootstrap != program.bootstrap ||
		current.steps != program.steps || current.stepCount != program.stepCount ||
		current.digestLo != program.digestLo || current.digestHi != program.digestHi {
		return 0, ProgramValidationInvalidViewV2
	}
	capabilities := ProgramCapabilitiesV2(current.bootstrap.Flags)
	if !capabilities.Valid() {
		return 0, ProgramValidationBootstrapFlagsV2
	}
	return capabilities, ProgramValidationOKV2
}

// ResolveProgramStepV2 returns one action from an opaque validated view. It
// never calls the plain target or coroutine factory.
func ResolveProgramStepV2(program ProgramViewV2, index uintptr) (ResolvedProgramStepV2, ProgramValidationCodeV2) {
	if program.magic != validatedProgramMagicV2 {
		return ResolvedProgramStepV2{}, ProgramValidationInvalidViewV2
	}
	if index >= program.stepCount {
		return ResolvedProgramStepV2{}, ProgramValidationStepIndexV2
	}
	current, code := ValidateRunnableProgramV2(program.manifest, program.factory)
	if code != ProgramValidationOKV2 || current.bootstrap != program.bootstrap ||
		current.steps != program.steps || current.stepCount != program.stepCount ||
		current.digestLo != program.digestLo || current.digestHi != program.digestHi {
		return ResolvedProgramStepV2{}, ProgramValidationInvalidViewV2
	}
	role, ok := programStepRoleV2(index, program.stepCount)
	if !ok {
		return ResolvedProgramStepV2{}, ProgramValidationInvalidViewV2
	}
	return resolveValidatedProgramStepV2(
		program.manifest, programStepAtV1(program.steps, index), role,
	)
}
