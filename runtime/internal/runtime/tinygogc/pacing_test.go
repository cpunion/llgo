package tinygogc

import "testing"

func TestParseGCPercent(t *testing.T) {
	for _, test := range []struct {
		input string
		want  int32
	}{
		{"", 100}, {"off", -1}, {"OFF", 100}, {"0", 0}, {"-0", 0},
		{"1", 1}, {"+1", 1}, {"100", 100}, {"001", 1}, {"-1", -1},
		{"-123", -1}, {"2147483647", 2147483647}, {"2147483648", 100},
		{"-2147483648", -1}, {"-2147483649", 100}, {"9999999999999", 100},
		{" 1", 100}, {"1 ", 100}, {"1_0", 100}, {"0x1", 100},
		{"+", 100}, {"-", 100}, {"--1", 100}, {"/", 100},
	} {
		t.Run(test.input, func(t *testing.T) {
			if got := ParseGCPercent(test.input); got != test.want {
				t.Fatalf("ParseGCPercent(%q) = %d, want %d", test.input, got, test.want)
			}
		})
	}
}

func TestGCHeapGoal(t *testing.T) {
	for _, test := range []struct {
		live    uint64
		percent int32
		want    uint64
	}{
		{0, 100, 4 << 20}, {5 << 20, 100, 10 << 20},
		{5 << 20, 1, 5<<20 + 64<<10}, {10 << 20, 1, 10<<20 + (10<<20)/100},
		{5 << 20, 0, 5<<20 + 64<<10}, {0, 0, 64 << 10},
		{5 << 20, -1, disabledGCGoal}, {0, -123, disabledGCGoal},
		{disabledGCGoal, 100, disabledGCGoal}, {disabledGCGoal - 1, 0, disabledGCGoal},
		{disabledGCGoal, 2147483647, disabledGCGoal},
		{100, 2147483647, (4 << 20) * uint64(2147483647) / 100},
	} {
		if got := gcHeapGoal(test.live, test.percent); got != test.want {
			t.Errorf("goal(%d, %d) = %d, want %d", test.live, test.percent, got, test.want)
		}
	}
}

func TestGCPacingInitialization(t *testing.T) {
	var p gcPacing
	if p.automatic() || p.shouldCollect(disabledGCGoal, 1) || p.nextGC() != disabledGCGoal {
		t.Fatal("bootstrap must allocate/grow without automatic collection")
	}
	p.collected(8 << 20)
	if p.initialized {
		t.Fatal("explicit bootstrap collection initialized environment policy")
	}
	p.init(1, 5<<20)
	if p.percent != 1 || p.live != 5<<20 || p.nextGC() != 5<<20+64<<10 {
		t.Fatalf("initial live stack was not included: %+v", p)
	}
	p.init(100, 123)
	if p.percent != 1 || p.live != 5<<20 {
		t.Fatal("reinitialization overwrote configuration")
	}
	if old := p.setPercent(-123, 42); old != 1 {
		t.Fatalf("old = %d", old)
	}
	if p.automatic() || p.nextGC() != disabledGCGoal {
		t.Fatal("negative percent did not disable automatic GC")
	}
	if old := p.setPercent(0, 42); old != -1 {
		t.Fatalf("negative old value not normalized: %d", old)
	}
	if old := p.setPercent(100, 42); old != 0 {
		t.Fatalf("old = %d", old)
	}

	var early gcPacing
	if old := early.setPercent(-2, 7<<20); old != 100 {
		t.Fatalf("pre-init old = %d", old)
	}
	early.init(1, 8<<20)
	if early.percent != -1 || early.live != 7<<20 {
		t.Fatal("startup overwrote early setter")
	}
}

func TestGCPacingProgressAndCollection(t *testing.T) {
	var p gcPacing
	p.init(0, 5<<20)
	if p.shouldCollect(5<<20, 16) || p.shouldCollect(5<<20, minimumGCProgress-1) {
		t.Fatal("GOGC=0 did not permit minimum allocation progress")
	}
	if !p.shouldCollect(5<<20, minimumGCProgress) || !p.shouldCollect(p.goal, 1) {
		t.Fatal("reaching the goal did not trigger collection")
	}
	p.collected(3 << 20)
	if p.nextGC() != 3<<20+minimumGCProgress {
		t.Fatal("goal was not based on post-sweep live bytes")
	}
	p.setPercent(1, 99)
	low := p.nextGC()
	p.setPercent(100, 99)
	if low >= p.nextGC() {
		t.Fatal("GOGC=1 silently clamped to the default")
	}
	p.setPercent(-1, 99)
	p.collected(1 << 20)
	if p.nextGC() != disabledGCGoal || p.shouldCollect(disabledGCGoal, disabledGCGoal) {
		t.Fatal("manual collection reenabled automatic GC")
	}
	p.setPercent(100, 99)
	if p.nextGC() != 4<<20 {
		t.Fatal("manual collection did not update live baseline while disabled")
	}
	if !p.shouldCollect(p.goal-1, disabledGCGoal) {
		t.Fatal("allocation addition overflowed")
	}
}

func TestGCPacingRuntimeRootsPreserveHeapBudget(t *testing.T) {
	const initialLive = uint64(10 << 20)
	const stackSize = uint64(128 << 10)
	for _, percent := range []int32{0, 1, 100} {
		var p gcPacing
		p.init(percent, initialLive)
		initialGoal := p.nextGC()
		budget := initialGoal - initialLive
		live := initialLive
		for i := 0; i < 100; i++ {
			p.rootAllocated(stackSize)
			live += stackSize
			if p.live != live || p.nextGC()-live != budget {
				t.Fatalf("GOGC=%d: root allocation changed heap budget: %+v", percent, p)
			}
			if p.shouldCollect(live, budget-1) || !p.shouldCollect(live, budget) {
				t.Fatalf("GOGC=%d: Go allocations no longer obey their budget", percent)
			}
		}
		for i := 0; i < 100; i++ {
			p.rootFreed(stackSize)
			live -= stackSize
			if p.live != live || p.nextGC()-live != budget {
				t.Fatalf("GOGC=%d: root release changed heap budget: %+v", percent, p)
			}
		}
		if p.nextGC() != initialGoal {
			t.Fatalf("GOGC=%d: releasing roots did not restore original goal", percent)
		}
	}
}

func TestGCPacingRuntimeRootsLifecycle(t *testing.T) {
	var p gcPacing
	p.rootAllocated(128 << 10)
	p.rootFreed(64 << 10)
	if p.initialized || p.live != 0 || p.nextGC() != disabledGCGoal {
		t.Fatal("bootstrap root storage initialized pacing")
	}
	p.init(-1, 10<<20)
	p.rootAllocated(128 << 10)
	p.rootFreed(64 << 10)
	if p.live != 10<<20+64<<10 || p.nextGC() != disabledGCGoal || p.automatic() {
		t.Fatalf("root storage changed GOGC=off: %+v", p)
	}
	p.setPercent(1, 0)
	if p.nextGC() != gcHeapGoal(10<<20+64<<10, 1) {
		t.Fatal("reenabling GC did not include current root storage")
	}
	p.collected(9 << 20)
	if p.live != 9<<20 || p.nextGC() != gcHeapGoal(9<<20, 1) {
		t.Fatal("collection did not reset the adjusted live baseline")
	}
	p.rootFreed(64 << 10)
	if p.live != 9<<20-64<<10 || p.nextGC() != gcHeapGoal(9<<20, 1)-64<<10 {
		t.Fatal("root release after collection did not adjust the new baseline")
	}
	p.rootAllocated(0)
	p.rootFreed(0)
	if p.live != 9<<20-64<<10 {
		t.Fatal("zero-sized root changed pacing")
	}
}

func TestGCPacingRuntimeRootsSaturation(t *testing.T) {
	var p gcPacing
	p.init(1, disabledGCGoal-32)
	p.rootAllocated(64)
	if p.live != disabledGCGoal || p.nextGC() != disabledGCGoal {
		t.Fatalf("root allocation overflowed: %+v", p)
	}
	p.rootFreed(64)
	if p.live != disabledGCGoal-64 || p.nextGC() != disabledGCGoal-64 {
		t.Fatalf("saturated root release overflowed: %+v", p)
	}
	p.collected(32)
	p.rootFreed(64)
	if p.live != 0 || p.nextGC() != gcHeapGoal(32, 1)-32 {
		t.Fatalf("root release underflowed: %+v", p)
	}
}
