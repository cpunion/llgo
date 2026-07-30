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

// operationReadyPage and operationReadyPageIndex are the allocation-free,
// target-neutral readiness index shared by paged operation catalogs. A leaf
// covers one 64-slot page; the summary covers every complete page representable
// by OperationID's 15-bit local field. Both use only the runtime core's required
// 32-bit atomic load/store/CAS contract.
const (
	operationReadyWordBits     = uint32(32)
	operationReadyWordsPerPage = operationCatalogPageCapacity / operationReadyWordBits
	operationReadyIndexWords   = (operationCatalogMaximumPageCount + operationReadyWordBits - 1) /
		operationReadyWordBits
)

type operationReadyPage struct {
	words [operationReadyWordsPerPage]uint32
}

type operationReadyPageIndex struct {
	words [operationReadyIndexWords]uint32
}

func operationReadyTrailingZeros(value uint32) uint32 {
	if value == 0 {
		return operationReadyWordBits
	}
	count := uint32(0)
	if value&0xffff == 0 {
		value >>= 16
		count += 16
	}
	if value&0xff == 0 {
		value >>= 8
		count += 8
	}
	if value&0xf == 0 {
		value >>= 4
		count += 4
	}
	if value&0x3 == 0 {
		value >>= 2
		count += 2
	}
	if value&0x1 == 0 {
		count++
	}
	return count
}

func operationReadyMark(word *uint32, mask uint32) bool {
	if word == nil || mask == 0 {
		return false
	}
	for {
		value := preemptLoad(word)
		if value&mask != 0 {
			return true
		}
		if preemptCompareAndSwap(word, value, value|mask) {
			return true
		}
	}
}

func operationReadyClear(word *uint32, mask uint32) bool {
	if word == nil || mask == 0 {
		return false
	}
	for {
		value := preemptLoad(word)
		if value&mask == 0 {
			return true
		}
		if preemptCompareAndSwap(word, value, value&^mask) {
			return true
		}
	}
}

func (page *operationReadyPage) marked(local uint32) (bool, bool) {
	if page == nil || local >= operationCatalogPageCapacity {
		return false, false
	}
	word, mask := &page.words[local/operationReadyWordBits],
		uint32(1)<<(local%operationReadyWordBits)
	return preemptLoad(word)&mask != 0, true
}

func (page *operationReadyPage) mark(local uint32) bool {
	if page == nil || local >= operationCatalogPageCapacity {
		return false
	}
	return operationReadyMark(
		&page.words[local/operationReadyWordBits],
		uint32(1)<<(local%operationReadyWordBits),
	)
}

func (page *operationReadyPage) clear(local uint32) bool {
	if page == nil || local >= operationCatalogPageCapacity {
		return false
	}
	return operationReadyClear(
		&page.words[local/operationReadyWordBits],
		uint32(1)<<(local%operationReadyWordBits),
	)
}

func (page *operationReadyPage) empty() bool {
	if page == nil {
		return false
	}
	for index := range page.words {
		if preemptLoad(&page.words[index]) != 0 {
			return false
		}
	}
	return true
}

func (page *operationReadyPage) next(start, limit uint32) (uint32, bool, bool) {
	if page == nil || start > limit || limit > operationCatalogPageCapacity {
		return 0, false, false
	}
	for wordIndex := start / operationReadyWordBits; wordIndex < operationReadyWordsPerPage && wordIndex*operationReadyWordBits < limit; wordIndex++ {
		bitOffset := uint32(0)
		if wordIndex == start/operationReadyWordBits {
			bitOffset = start % operationReadyWordBits
		}
		value := preemptLoad(&page.words[wordIndex]) & (^uint32(0) << bitOffset)
		if end := limit - wordIndex*operationReadyWordBits; end < operationReadyWordBits {
			value &= (uint32(1) << end) - 1
		}
		if value != 0 {
			return wordIndex*operationReadyWordBits + operationReadyTrailingZeros(value), true, true
		}
	}
	return 0, false, true
}

func (index *operationReadyPageIndex) mark(page uint32) bool {
	if index == nil || page >= operationCatalogMaximumPageCount {
		return false
	}
	return operationReadyMark(
		&index.words[page/operationReadyWordBits],
		uint32(1)<<(page%operationReadyWordBits),
	)
}

func (index *operationReadyPageIndex) take(page uint32) bool {
	if index == nil || page >= operationCatalogMaximumPageCount {
		return false
	}
	word, mask := &index.words[page/operationReadyWordBits],
		uint32(1)<<(page%operationReadyWordBits)
	for {
		value := preemptLoad(word)
		if value&mask == 0 {
			return false
		}
		if preemptCompareAndSwap(word, value, value&^mask) {
			return true
		}
	}
}

func (index *operationReadyPageIndex) empty() bool {
	if index == nil {
		return false
	}
	for word := range index.words {
		if preemptLoad(&index.words[word]) != 0 {
			return false
		}
	}
	return true
}

func (index *operationReadyPageIndex) takeNext(start, limit uint32) (uint32, bool, bool) {
	if index == nil || start > limit || limit > operationCatalogMaximumPageCount {
		return 0, false, false
	}
	for page := start; page < limit; {
		wordIndex := page / operationReadyWordBits
		bitOffset := page % operationReadyWordBits
		value := preemptLoad(&index.words[wordIndex]) & (^uint32(0) << bitOffset)
		if end := limit - wordIndex*operationReadyWordBits; end < operationReadyWordBits {
			value &= (uint32(1) << end) - 1
		}
		if value == 0 {
			page = (wordIndex + 1) * operationReadyWordBits
			continue
		}
		candidate := wordIndex*operationReadyWordBits + operationReadyTrailingZeros(value)
		if candidate >= limit {
			return 0, false, true
		}
		if index.take(candidate) {
			return candidate, true, true
		}
		page = candidate
	}
	return 0, false, true
}
