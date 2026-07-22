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

// PrepareNotifyWait publishes one sync.Cond ticket in the shared keyed-wait
// catalog. The caller must recheck the notify counter after this succeeds and
// exact-post handle when the ticket was notified before publication completed.
func PrepareNotifyWait(
	p *P,
	waits *WaitRegistrationTable,
	catalog *KeyedWaitCatalog,
	token *WaitToken,
	key uintptr,
	logicalTicket uint32,
) (WaitTicket, KeyedWaitHandle, KeyedWaitPrepareResult) {
	return PrepareKeyedWait(
		p, waits, catalog, token,
		KeyedWaitNamespaceNotifyList, key, logicalTicket,
	)
}

// PostPreparedNotifyWait is the exact repair edge for Notify-before-park.
func PostPreparedNotifyWait(
	catalog *KeyedWaitCatalog,
	waits *WaitRegistrationTable,
	handle KeyedWaitHandle,
	key uintptr,
	logicalTicket uint32,
) KeyedWaitPostResult {
	return PostPreparedKeyedWait(
		catalog, waits, handle,
		KeyedWaitNamespaceNotifyList, key, logicalTicket,
	)
}

// PostNotifyWaitOne wakes the exact next notify ticket. Registration order is
// intentionally irrelevant: notifyListAdd tickets define FIFO order.
func PostNotifyWaitOne(
	catalog *KeyedWaitCatalog,
	waits *WaitRegistrationTable,
	key uintptr,
	logicalTicket uint32,
) KeyedWaitPostResult {
	return PostExactKeyedWait(
		catalog, waits, KeyedWaitNamespaceNotifyList, key, logicalTicket,
	)
}

// PostNotifyWaitAll wakes the tickets covered by the notify counter movement
// from first to next. Wrapped counters use the Go runtime bounded comparison.
func PostNotifyWaitAll(
	catalog *KeyedWaitCatalog,
	waits *WaitRegistrationTable,
	key uintptr,
	first, next uint32,
) (uint32, bool) {
	return PostKeyedWaitTicketRange(
		catalog, waits, KeyedWaitNamespaceNotifyList, key, first, next,
	)
}

func RetireCompletedNotifyWait(
	catalog *KeyedWaitCatalog,
	waits *WaitRegistrationTable,
	handle KeyedWaitHandle,
	token *WaitToken,
	ticket WaitTicket,
) bool {
	slot, ok := keyedWaitSlotAt(catalog, handle)
	if !ok || slot.namespace != KeyedWaitNamespaceNotifyList {
		return false
	}
	return RetireCompletedKeyedWait(
		catalog, waits, handle, token, ticket,
		KeyedWaitNamespaceNotifyList, slot.key, slot.ticket,
	)
}
