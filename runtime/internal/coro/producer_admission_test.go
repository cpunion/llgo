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

func TestProducerAdmissionLifecycle(t *testing.T) {
	if producerAdmissionAcquire(nil) || producerAdmissionSeal(nil) ||
		producerAdmissionQuiesced(nil) || producerAdmissionReopen(nil) ||
		producerAdmissionReleaseChecked(nil) {
		t.Fatal("nil admission word accepted")
	}
	producerAdmissionRelease(nil)

	var word uint32
	producerAdmissionRelease(&word)
	if producerAdmissionReleaseChecked(&word) {
		t.Fatal("checked release accepted open zero")
	}
	if preemptLoad(&word) != 0 || !producerAdmissionAcquire(&word) ||
		!producerAdmissionAcquire(&word) || preemptLoad(&word) != 2 {
		t.Fatalf("open admission = %#x", preemptLoad(&word))
	}
	if !producerAdmissionSeal(&word) || preemptLoad(&word) != producerAdmissionClosed|2 ||
		producerAdmissionAcquire(&word) || producerAdmissionQuiesced(&word) ||
		producerAdmissionReopen(&word) {
		t.Fatalf("sealed live admission = %#x", preemptLoad(&word))
	}
	if !producerAdmissionReleaseChecked(&word) || !producerAdmissionReleaseChecked(&word) ||
		producerAdmissionReleaseChecked(&word) {
		t.Fatal("checked release did not reject sealed zero")
	}
	if !producerAdmissionQuiesced(&word) || !producerAdmissionSeal(&word) ||
		!producerAdmissionReopen(&word) || preemptLoad(&word) != 0 {
		t.Fatalf("quiesced admission = %#x", preemptLoad(&word))
	}

	preemptStore(&word, producerAdmissionCountMask)
	if producerAdmissionAcquire(&word) || !producerAdmissionSeal(&word) ||
		preemptLoad(&word) != producerAdmissionClosed|producerAdmissionCountMask {
		t.Fatalf("saturated admission = %#x", preemptLoad(&word))
	}
}

func TestProducerAdmissionSealRaceJoinsEveryAcceptedProducer(t *testing.T) {
	const producerCount = 64

	var word uint32
	if !producerAdmissionAcquire(&word) {
		t.Fatal("admit producer before seal race")
	}
	start := make(chan struct{})
	accepted := make(chan bool, producerCount)
	release := make(chan struct{})
	finished := make(chan struct{}, producerCount)
	for index := 0; index < producerCount; index++ {
		go func() {
			<-start
			entered := producerAdmissionAcquire(&word)
			accepted <- entered
			if entered {
				<-release
				producerAdmissionRelease(&word)
			}
			finished <- struct{}{}
		}()
	}
	close(start)
	if !producerAdmissionSeal(&word) || producerAdmissionAcquire(&word) {
		t.Fatal("seal did not withdraw producer admission")
	}

	acceptedCount := uint32(1)
	for index := 0; index < producerCount; index++ {
		if <-accepted {
			acceptedCount++
		}
	}
	if got := preemptLoad(&word); got != producerAdmissionClosed|acceptedCount {
		t.Fatalf("sealed count = %#x, want %#x", got, producerAdmissionClosed|acceptedCount)
	}
	close(release)
	producerAdmissionRelease(&word)
	for index := 0; index < producerCount; index++ {
		<-finished
	}
	if !producerAdmissionQuiesced(&word) {
		t.Fatalf("joined admission = %#x", preemptLoad(&word))
	}
	if !producerAdmissionReopen(&word) || !producerAdmissionAcquire(&word) {
		t.Fatal("joined admission did not reopen")
	}
	producerAdmissionRelease(&word)
}
