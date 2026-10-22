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

// PhysicalOwnerRetirement joins a physical M's departure, its original
// parent's lineage acknowledgement, and temporary metadata readers. The last
// releaser receives the sole permission to clear and reuse the native slot.
// It contains no G/P pointer and does not change coroutine frame storage.
type PhysicalOwnerRetirement struct {
	noCopy physicalOwnerRetirementNoCopy
	state  uint32
}

type physicalOwnerRetirementNoCopy struct{}

func (*physicalOwnerRetirementNoCopy) Lock()   {}
func (*physicalOwnerRetirementNoCopy) Unlock() {}

const (
	physicalOwnerLeft     uint32 = 1
	physicalOwnerReleased uint32 = 2
	physicalOwnerClosed   uint32 = 4
	physicalOwnerDormant  uint32 = 8
	physicalOwnerClaimed  uint32 = 16
	physicalOwnerReader   uint32 = 32
	physicalOwnerFlagMask uint32 = 31
	physicalOwnerComplete        = physicalOwnerLeft | physicalOwnerReleased | physicalOwnerClosed
	physicalOwnerClaim           = physicalOwnerComplete | physicalOwnerClaimed
)

func validPhysicalOwnerRetirement(word uint32) bool {
	flags := word & physicalOwnerFlagMask
	if flags&physicalOwnerDormant != 0 {
		return word == physicalOwnerDormant
	}
	if flags&physicalOwnerClaimed != 0 {
		return word == physicalOwnerClaim
	}
	if flags&(physicalOwnerLeft|physicalOwnerReleased) != 0 && flags&physicalOwnerClosed == 0 {
		return false
	}
	return word != physicalOwnerComplete
}

// Open publishes a newly initialized native record. Reused records remain
// dormant until their target-owned fields have all been initialized.
func (retirement *PhysicalOwnerRetirement) Open() bool {
	if retirement == nil {
		return false
	}
	word := preemptLoad(&retirement.state)
	return word == 0 || word == physicalOwnerDormant &&
		preemptCompareAndSwap(&retirement.state, word, 0)
}

func (retirement *PhysicalOwnerRetirement) AcquireMetadata() bool {
	if retirement == nil {
		return false
	}
	for {
		word := preemptLoad(&retirement.state)
		if !validPhysicalOwnerRetirement(word) || word&(physicalOwnerDormant|physicalOwnerClaimed) != 0 ||
			word > ^uint32(0)-physicalOwnerReader {
			return false
		}
		if preemptCompareAndSwap(&retirement.state, word, word+physicalOwnerReader) {
			return true
		}
	}
}

func (retirement *PhysicalOwnerRetirement) Close(lineagePinned bool) bool {
	if retirement == nil {
		return false
	}
	for {
		word := preemptLoad(&retirement.state)
		if !validPhysicalOwnerRetirement(word) || word&physicalOwnerFlagMask != 0 {
			return false
		}
		next := word | physicalOwnerClosed
		if !lineagePinned {
			next |= physicalOwnerReleased
		}
		if preemptCompareAndSwap(&retirement.state, word, next) {
			return true
		}
	}
}

func (retirement *PhysicalOwnerRetirement) release(flag uint32) (reclaim, ok bool) {
	if retirement == nil {
		return false, false
	}
	for {
		word := preemptLoad(&retirement.state)
		if !validPhysicalOwnerRetirement(word) || word&(physicalOwnerDormant|physicalOwnerClaimed) != 0 {
			return false, false
		}
		next := word
		if flag == physicalOwnerReader {
			if word&^physicalOwnerFlagMask == 0 {
				return false, false
			}
			next -= physicalOwnerReader
		} else {
			if word&physicalOwnerClosed == 0 || word&flag != 0 {
				return false, false
			}
			next |= flag
		}
		reclaim = next == physicalOwnerComplete
		if reclaim {
			next |= physicalOwnerClaimed
		}
		if preemptCompareAndSwap(&retirement.state, word, next) {
			// Do not inspect storage after publishing this release: another
			// last party can already have cleared and reopened the native slot.
			return reclaim, true
		}
	}
}

func (retirement *PhysicalOwnerRetirement) ReleaseMetadata() (bool, bool) {
	return retirement.release(physicalOwnerReader)
}

func (retirement *PhysicalOwnerRetirement) ReleasePhysical() (bool, bool) {
	return retirement.release(physicalOwnerLeft)
}

func (retirement *PhysicalOwnerRetirement) ReleaseLineage() (bool, bool) {
	return retirement.release(physicalOwnerReleased)
}

// Reset is target-owner-only after an ordinary returned record, or after this
// protocol granted its unique retired-record reclaim permission.
func (retirement *PhysicalOwnerRetirement) Reset() bool {
	if retirement == nil {
		return false
	}
	word := preemptLoad(&retirement.state)
	return (word == 0 || word == physicalOwnerClaim) &&
		preemptCompareAndSwap(&retirement.state, word, physicalOwnerDormant)
}
