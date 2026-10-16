package coro

// GCObservation is one process-wide, retained collection notification. It
// carries no heap pointer and adds no state to a G, frame, timer, or weak cell.
// Collection only publishes; an executor owner performs the actual observation
// outside collector locks. Failed/busy observations must not acknowledge.
type GCObservation struct {
	enabled uint32
	epoch   uint32
	seen    uint32
}

func (o *GCObservation) Enable() { preemptStore(&o.enabled, 1) }

func (o *GCObservation) Enabled() bool { return preemptLoad(&o.enabled) != 0 }

// EpochWord is stable process-lifetime storage. A native collector may update
// it with a release atomic increment, but must never call Go from its GC hook.
func (o *GCObservation) EpochWord() *uint32 { return &o.epoch }

// Complete publishes from collector code, where an absent subscription is a
// no-op. The explicit nil guard also keeps this tail infallible without a
// trusted no-unwind annotation or a managed child frame inside the collector.
func (o *GCObservation) Complete() {
	if o != nil && preemptLoad(&o.enabled) != 0 {
		preemptAdd(&o.epoch, 1)
	}
}

func (o *GCObservation) Pending() (epoch, seen uint32, pending bool) {
	if preemptLoad(&o.enabled) == 0 {
		return 0, 0, false
	}
	seen = preemptLoad(&o.seen)
	epoch = preemptLoad(&o.epoch)
	return epoch, seen, epoch != seen
}

// A concurrent owner must not overwrite a newer acknowledgement with an older
// scan. Failure is harmless: the unacknowledged epoch remains pending.
func (o *GCObservation) Acknowledge(epoch, seen uint32) {
	preemptCompareAndSwap(&o.seen, seen, epoch)
}
