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
	operationCatalogPageCapacity        = uint32(64)
	operationCatalogMaximumPageCount    = operationLocalMask / operationCatalogPageCapacity
	operationDynamicPageCapacity        = operationCatalogMaximumPageCount - 1
	operationPageDirectoryBlockCapacity = uint32(64)
	operationPageDirectoryBlockCount    = (operationDynamicPageCapacity + operationPageDirectoryBlockCapacity - 1) /
		operationPageDirectoryBlockCapacity
)

// OperationPageDirectoryBlock is stable target-owned storage for 64 dynamic
// page pointers. Targets supply a pristine block only when an Attach operation
// crosses a block boundary. Its fields remain private so only the monotonic
// directory publication protocol can mutate them.
type OperationPageDirectoryBlock struct {
	pages [operationPageDirectoryBlockCapacity]unsafe.Pointer
}

// operationDynamicPageDirectory is the target-neutral monotonic extension
// point for stable source pages. A source keeps its inline and statically
// configured pages outside this directory; targets may publish additional
// heap, arena, or static pages while the source is bound.
//
// blocks and their pages are pointer-typed so a tracing runtime keeps target-
// allocated storage rooted. The source pays for only eight root pointers;
// targets attach one block per 64 dynamic pages instead of every source
// embedding the full 510-pointer representable catalog. The owner writes a
// complete block/page path before release-publishing count. Producers acquire
// count before reading those immutable pointers.
type operationDynamicPageDirectory struct {
	blocks [operationPageDirectoryBlockCount]*OperationPageDirectoryBlock
	count  uint32
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
	block := directory.blocks[index/operationPageDirectoryBlockCapacity]
	if block == nil {
		return nil
	}
	return block.pages[index%operationPageDirectoryBlockCapacity]
}

func operationPageDirectoryBlockEmpty(block *OperationPageDirectoryBlock) bool {
	if block == nil {
		return false
	}
	for index := range block.pages {
		if block.pages[index] != nil {
			return false
		}
	}
	return true
}

// publish accepts a directory block only for the first page in that block.
// Passing nil at such a boundary is a mutation-free capacity probe: targets
// may retry with newly allocated stable block storage without a separate
// source-specific query API.
func (directory *operationDynamicPageDirectory) publish(
	page unsafe.Pointer,
	newBlock *OperationPageDirectoryBlock,
) bool {
	if directory == nil || page == nil {
		return false
	}
	count := directory.published()
	if count >= operationDynamicPageCapacity {
		return false
	}
	for index := uint32(0); index < count; index++ {
		if directory.page(index) == page {
			return false
		}
	}
	blockIndex := count / operationPageDirectoryBlockCapacity
	offset := count % operationPageDirectoryBlockCapacity
	block := directory.blocks[blockIndex]
	if offset == 0 {
		if block != nil || !operationPageDirectoryBlockEmpty(newBlock) {
			return false
		}
		block = newBlock
		directory.blocks[blockIndex] = block
	} else if block == nil || newBlock != nil {
		return false
	}
	if block.pages[offset] != nil {
		return false
	}
	block.pages[offset] = page
	preemptStore(&directory.count, count+1)
	return true
}
