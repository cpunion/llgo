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

import "testing"

func TestOperationReadyPageAndSummaryBoundaries(t *testing.T) {
	var page operationReadyPage
	for _, local := range []uint32{0, 31, 32, 63} {
		if !page.mark(local) {
			t.Fatalf("mark ready leaf %d", local)
		}
	}
	cursor := uint32(0)
	for _, want := range []uint32{0, 31, 32, 63} {
		got, marked, ok := page.next(cursor, operationCatalogPageCapacity)
		if !ok || !marked || got != want || !page.clear(got) {
			t.Fatalf("take ready leaf from %d = (%d,%t,%t), want %d", cursor, got, marked, ok, want)
		}
		cursor = got + 1
	}
	if _, marked, ok := page.next(0, operationCatalogPageCapacity); !ok || marked || !page.empty() {
		t.Fatalf("drained ready leaf = (marked:%t ok:%t empty:%t)", marked, ok, page.empty())
	}

	var index operationReadyPageIndex
	for _, page := range []uint32{0, 31, 32, operationCatalogMaximumPageCount - 1} {
		if !index.mark(page) {
			t.Fatalf("mark ready page %d", page)
		}
	}
	cursor = 0
	for _, want := range []uint32{0, 31, 32, operationCatalogMaximumPageCount - 1} {
		got, marked, ok := index.takeNext(cursor, operationCatalogMaximumPageCount)
		if !ok || !marked || got != want {
			t.Fatalf("take ready page from %d = (%d,%t,%t), want %d", cursor, got, marked, ok, want)
		}
		cursor = got + 1
	}
	if _, marked, ok := index.takeNext(0, operationCatalogMaximumPageCount); !ok || marked || !index.empty() {
		t.Fatalf("drained ready summary = (marked:%t ok:%t empty:%t)", marked, ok, index.empty())
	}
}

func TestOperationReadyIndexMasksUnrepresentableTailPage(t *testing.T) {
	var index operationReadyPageIndex
	invalidPage := operationCatalogMaximumPageCount
	word := &index.words[invalidPage/operationReadyWordBits]
	preemptStore(word, uint32(1)<<(invalidPage%operationReadyWordBits))
	if page, marked, ok := index.takeNext(0, operationCatalogMaximumPageCount); !ok || marked || page != 0 {
		t.Fatalf("unrepresentable summary tail became visible = (%d,%t,%t)", page, marked, ok)
	}
	preemptStore(word, 0)
}
