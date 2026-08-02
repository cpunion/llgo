package wasmevent

import "testing"

func TestDispatcherRejectsStaleWakeups(t *testing.T) {
	var d dispatcher
	first := d.schedule()
	second := d.schedule()
	if first == 0 || second == 0 || first == second {
		t.Fatalf("invalid generations: first=%d second=%d", first, second)
	}

	if d.consume(first) {
		t.Fatal("stale wakeup was consumed")
	}
	if !d.consume(second) {
		t.Fatal("current wakeup was not consumed")
	}
	if d.consume(second) {
		t.Fatal("wakeup was consumed twice")
	}
}

func TestDispatcherCancel(t *testing.T) {
	var d dispatcher
	generation := d.schedule()
	d.cancel()
	if d.consume(generation) {
		t.Fatal("canceled wakeup was consumed")
	}
}

func TestDispatcherSkipsZeroGenerationAfterWrap(t *testing.T) {
	d := dispatcher{generation: ^uintptr(0)}
	if generation := d.schedule(); generation != 1 {
		t.Fatalf("generation after wrap = %d, want 1", generation)
	}
}
