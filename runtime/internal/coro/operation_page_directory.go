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

// Every current operation catalog uses 64-slot pages. The two-word
// OperationID reserves 15 source-local bits, so 511 complete pages (32,704
// slots) are representable without changing the producer ABI. The remaining
// 63 encodings stay invalid rather than creating a partial final page.
const (
	operationCatalogPageCapacity     = uint32(64)
	operationCatalogMaximumPageCount = operationLocalMask / operationCatalogPageCapacity
	operationDynamicPageCapacity     = operationCatalogMaximumPageCount - 1
)

// operationDynamicPageDirectory is the target-neutral monotonic extension
// point for stable source pages. A source keeps its inline and statically
// configured pages outside this directory; targets may publish additional
// heap, arena, or static pages while the source is bound.
//
// pages is pointer-typed so a tracing runtime keeps target-allocated pages
// rooted. The owner writes one new entry before release-publishing count.
// Producers acquire count before reading that immutable entry; existing
// entries and page addresses are never replaced or removed.
type operationDynamicPageDirectory struct {
	pages [operationDynamicPageCapacity]unsafe.Pointer
	count uint32
}

func (directory *operationDynamicPageDirectory) published() uint32 {
	if directory == nil {
		return 0
	}
	return preemptLoad(&directory.count)
}

func (directory *operationDynamicPageDirectory) page(index uint32) unsafe.Pointer {
	if directory == nil || index >= directory.published() {
		return nil
	}
	return directory.pages[index]
}

func (directory *operationDynamicPageDirectory) publish(page unsafe.Pointer) bool {
	if directory == nil || page == nil {
		return false
	}
	count := directory.published()
	if count >= uint32(len(directory.pages)) || directory.pages[count] != nil {
		return false
	}
	directory.pages[count] = page
	preemptStore(&directory.count, count+1)
	return true
}
