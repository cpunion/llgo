//go:build !llgo && (darwin || linux) && !baremetal

package corodoorbell

import "testing"

func TestWaitPollSetCombinesDoorbellAndReadiness(t *testing.T) {
	var pipe Pipe
	if !pipe.Open() {
		t.Fatal("open retained pipe")
	}
	defer func() {
		if !pipe.Close() {
			t.Fatal("close retained pipe")
		}
	}()

	entries := [PollSetCapacity]PollFD{
		{FD: pipe.readFD, Events: PollRead},
		{FD: pipe.writeFD, Events: PollWrite},
	}
	if ready, errno := WaitPollSet(&entries[0], 2, 0); ready != 1 || errno != 0 || entries[0].Revents != 0 || entries[1].Revents&PollWrite == 0 {
		t.Fatalf("initial poll = ready:%d errno:%d entries:%+v", ready, errno, entries[:2])
	}
	if !pipe.Ring() {
		t.Fatal("ring retained pipe")
	}
	entries[0].Revents, entries[1].Revents = 0, 0
	if ready, errno := WaitPollSet(&entries[0], 2, 100); ready != 2 || errno != 0 || entries[0].Revents&PollRead == 0 || entries[1].Revents&PollWrite == 0 {
		t.Fatalf("ready poll = ready:%d errno:%d entries:%+v", ready, errno, entries[:2])
	}
	if !pipe.Drain() {
		t.Fatal("drain retained pipe")
	}
}

func TestWaitPollSetRejectsInvalidPhysicalShape(t *testing.T) {
	var entry PollFD
	for _, test := range []struct {
		first   *PollFD
		count   uint32
		timeout int32
	}{
		{nil, 1, 0},
		{&entry, 0, 0},
		{&entry, PollSetCapacity + 1, 0},
		{&entry, 1, -1},
		{&entry, 1, -2},
		{&entry, 1, physicalPollMaxMS + 1},
	} {
		if ready, errno := WaitPollSet(test.first, test.count, test.timeout); ready != -1 || errno != -1 {
			t.Fatalf("invalid poll shape returned ready:%d errno:%d", ready, errno)
		}
	}
}

func TestWaitPollSetCarriesMoreThanOneLogicalPage(t *testing.T) {
	const count = 80
	var pipes [count]Pipe
	var entries [PollSetCapacity]PollFD
	for index := range pipes {
		if !pipes[index].Open() {
			t.Fatalf("open pipe %d", index)
		}
		defer func(pipe *Pipe) {
			if !pipe.Close() {
				t.Errorf("close retained pipe")
			}
		}(&pipes[index])
		entries[index] = PollFD{FD: pipes[index].readFD, Events: PollRead}
	}
	if !pipes[count-1].Ring() {
		t.Fatal("ring readiness source beyond first logical page")
	}
	ready, errno := WaitPollSet(&entries[0], count, 100)
	if ready != 1 || errno != 0 {
		t.Fatalf("six-source poll = ready:%d errno:%d", ready, errno)
	}
	for index := range pipes {
		want := int16(0)
		if index == count-1 {
			want = PollRead
		}
		if entries[index].Revents&PollRead != want {
			t.Fatalf("entry %d revents = %#x, want read=%#x", index, entries[index].Revents, want)
		}
	}
}

func TestWaitPollSetCarriesNativeConfiguredMaximum(t *testing.T) {
	var pipe Pipe
	if !pipe.Open() {
		t.Fatal("open maximum poll pipe")
	}
	defer func() {
		if !pipe.Close() {
			t.Error("close maximum poll pipe")
		}
	}()
	var entries [PollSetCapacity]PollFD
	for index := range entries {
		// POSIX poll ignores negative descriptors. Placing the only live fd at
		// the final index proves the complete configured span reached the kernel
		// without consuming 1025 process descriptors in the test.
		entries[index] = PollFD{FD: -1, Events: PollRead}
	}
	entries[PollSetCapacity-1].FD = pipe.readFD
	if !pipe.Ring() {
		t.Fatal("ring maximum poll pipe")
	}
	ready, errno := WaitPollSet(&entries[0], PollSetCapacity, 100)
	if ready != 1 || errno != 0 {
		t.Fatalf("maximum poll = ready:%d errno:%d, want one final entry", ready, errno)
	}
	if entries[PollSetCapacity-1].Revents&PollRead == 0 {
		t.Fatal("maximum poll did not inspect the final entry")
	}
}
