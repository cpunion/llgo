package coro

import (
	"sync"
	"testing"
)

func TestGCObservationAbsentSubscription(t *testing.T) {
	var absent *GCObservation
	absent.Complete()
	var disabled GCObservation
	disabled.Complete()
	if disabled.epoch != 0 || disabled.seen != 0 || disabled.Enabled() {
		t.Fatal("absent or disabled subscription published a collection")
	}
}

func TestGCObservationRetainsBusyAndConcurrentCollection(t *testing.T) {
	var state GCObservation
	state.Complete()
	if _, _, pending := state.Pending(); pending {
		t.Fatal("disabled observer published a collection")
	}
	state.Enable()
	state.Complete()
	epoch, seen, pending := state.Pending()
	if !pending || epoch != 1 || seen != 0 {
		t.Fatalf("first observation = %d,%d,%v", epoch, seen, pending)
	}
	// A failed TryLock does not acknowledge. The same obligation survives an
	// owner return, a host return, or a drained physical doorbell byte.
	if next, _, retry := state.Pending(); !retry || next != epoch {
		t.Fatal("busy observation lost its retained retry")
	}
	state.Complete() // collection while the first epoch is being scanned
	state.Acknowledge(epoch, seen)
	newEpoch, newSeen, pending := state.Pending()
	if !pending || newEpoch != 2 || newSeen != 1 {
		t.Fatal("collection during observation was incorrectly acknowledged")
	}
	state.Acknowledge(newEpoch, newSeen)
	state.Acknowledge(epoch, seen) // stale owner cannot regress acknowledgement
	if _, _, pending := state.Pending(); pending {
		t.Fatal("stale owner regressed the completed observation")
	}
}

func TestGCObservationConcurrentPublication(t *testing.T) {
	var state GCObservation
	state.Enable()
	var producers sync.WaitGroup
	for i := 0; i < 8; i++ {
		producers.Add(1)
		go func() {
			defer producers.Done()
			for n := 0; n < 1000; n++ {
				state.Complete()
			}
		}()
	}
	producers.Wait()
	epoch, seen, pending := state.Pending()
	if !pending || epoch != 8000 || seen != 0 {
		t.Fatalf("concurrent publications = %d,%d,%v", epoch, seen, pending)
	}
	state.Acknowledge(epoch, seen)
	if _, _, pending := state.Pending(); pending {
		t.Fatal("final collection still pending")
	}
}
