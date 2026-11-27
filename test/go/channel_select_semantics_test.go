package gotest

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNilChannelSendRecvBlock(t *testing.T) {
	done := make(chan string, 2)
	var ch chan int

	go func() {
		ch <- 1
		done <- "send"
	}()
	go func() {
		<-ch
		done <- "recv"
	}()

	time.Sleep(20 * time.Millisecond)
	select {
	case op := <-done:
		t.Fatalf("nil channel %s completed, want block", op)
	default:
	}
}

func TestNilChannelSelectCasesUseDefault(t *testing.T) {
	var ch chan int

	select {
	case ch <- 1:
		t.Fatal("nil channel send case selected")
	case <-ch:
		t.Fatal("nil channel receive case selected")
	default:
	}
}

func TestClosedChannelSendPanics(t *testing.T) {
	ch := make(chan int)
	close(ch)

	expectChannelPanicContaining(t, "send on closed channel", func() {
		ch <- 1
	})
}

func TestClosedChannelSelectSendPanics(t *testing.T) {
	ch := make(chan int)
	close(ch)

	expectChannelPanicContaining(t, "send on closed channel", func() {
		select {
		case ch <- 1:
		default:
			t.Fatal("closed channel send selected default")
		}
	})
}

func TestClosedChannelSelectRecvReturnsZero(t *testing.T) {
	ch := make(chan int)
	close(ch)

	select {
	case v, ok := <-ch:
		if v != 0 || ok {
			t.Fatalf("closed channel receive = %d, %v; want 0, false", v, ok)
		}
	default:
		t.Fatal("closed channel receive did not select")
	}
}

func TestHighFrequencySelectRecvHandshake(t *testing.T) {
	const n = 100000
	ch := make(chan int)
	dummy := make(chan int)
	done := make(chan struct{})

	go func() {
		for i := 0; i < n; i++ {
			ch <- i
		}
		close(done)
	}()

	for i := 0; i < n; i++ {
		select {
		case v := <-ch:
			if v != i {
				t.Fatalf("receive %d = %d", i, v)
			}
		case <-dummy:
			t.Fatal("dummy channel selected")
		}
	}

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("sender did not finish")
	}
}

func TestSelectTimeoutCancellationAndWakeupUnderTraffic(t *testing.T) {
	stop := make(chan struct{})
	var trafficOps atomic.Int64
	var wg sync.WaitGroup

	// Keep continuous multi-case select handshakes busy so P1/P4 cannot starve
	// timer, context cancellation, or mutex-backed wakeups.
	for i := 0; i < 4; i++ {
		a := make(chan int)
		b := make(chan int)
		wg.Add(2)
		go func() {
			defer wg.Done()
			for v := 0; ; v++ {
				select {
				case a <- v:
				case b <- v:
				case <-stop:
					return
				}
			}
		}()
		go func() {
			defer wg.Done()
			for {
				select {
				case <-a:
					trafficOps.Add(1)
				case <-b:
					trafficOps.Add(1)
				case <-stop:
					return
				}
			}
		}()
	}

	defer func() {
		close(stop)
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("traffic goroutines did not exit after stop")
		}
	}()

	idleA := make(chan int)
	idleB := make(chan int)

	// 1. time.After wakeup inside a multi-case blocking select under traffic.
	t0 := time.Now()
	select {
	case <-idleA:
		t.Fatal("idleA unexpectedly selected")
	case <-idleB:
		t.Fatal("idleB unexpectedly selected")
	case <-time.After(15 * time.Millisecond):
	}
	if d := time.Since(t0); d > 2*time.Second {
		t.Fatalf("time.After starved under select traffic: took %v", d)
	}

	// 2. context.WithTimeout wakeup under traffic.
	ctxTimeout, cancelTimeout := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancelTimeout()
	t0 = time.Now()
	select {
	case <-idleA:
		t.Fatal("idleA unexpectedly selected")
	case <-ctxTimeout.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("context.WithTimeout did not wake select")
	}
	if err := ctxTimeout.Err(); err != context.DeadlineExceeded {
		t.Fatalf("ctxTimeout.Err() = %v, want DeadlineExceeded", err)
	}
	if d := time.Since(t0); d > 2*time.Second {
		t.Fatalf("context.WithTimeout starved: took %v", d)
	}

	// 3. context.WithCancel triggered from a sibling goroutine under traffic.
	ctxCancel, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	select {
	case <-idleA:
		t.Fatal("idleA unexpectedly selected")
	case <-idleB:
		t.Fatal("idleB unexpectedly selected")
	case <-ctxCancel.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("context.WithCancel did not wake select")
	}
	if err := ctxCancel.Err(); err != context.Canceled {
		t.Fatalf("ctxCancel.Err() = %v, want Canceled", err)
	}

	// 4. Repeated timer Reset + Stop + channel wakeup alternation.
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	wake := make(chan int, 1)
	for i := 0; i < 6; i++ {
		if i%2 == 0 {
			timer.Reset(5 * time.Millisecond)
			select {
			case <-idleA:
				t.Fatal("idleA unexpectedly selected")
			case <-timer.C:
			case <-time.After(2 * time.Second):
				t.Fatalf("reset timer iter %d did not fire", i)
			}
		} else {
			timer.Reset(time.Hour)
			go func(v int) {
				wake <- v
			}(i)
			select {
			case <-idleA:
				t.Fatal("idleA unexpectedly selected")
			case <-timer.C:
				t.Fatalf("1h timer fired unexpectedly on iter %d", i)
			case v := <-wake:
				if v != i {
					t.Fatalf("wake value = %d, want %d", v, i)
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("channel wakeup iter %d timed out", i)
			}
			timer.Stop()
		}
	}

	if trafficOps.Load() == 0 {
		t.Fatal("expected sibling select traffic to make progress")
	}
}

func TestSelectConcurrentMultiProducerAndCloseRace(t *testing.T) {
	for _, capSize := range []int{0, 1} {
		const perProducer = 500
		c0 := make(chan int, capSize)
		c1 := make(chan int, capSize)
		c2 := make(chan int, capSize)
		c3 := make(chan int, capSize)
		chs := [4]chan int{c0, c1, c2, c3}

		var wantSum int64
		var wg sync.WaitGroup
		for p := 0; p < 4; p++ {
			base := p * 10000
			for i := 1; i <= perProducer; i++ {
				wantSum += int64(base + i)
			}
			ch := chs[p]
			wg.Add(1)
			go func(base int, ch chan int) {
				defer wg.Done()
				for i := 1; i <= perProducer; i++ {
					ch <- base + i
				}
			}(base, ch)
		}

		var gotSum int64
		idle := make(chan struct{})
		total := 4 * perProducer
		for i := 0; i < total; i++ {
			select {
			case v := <-c0:
				gotSum += int64(v)
			case v := <-c1:
				gotSum += int64(v)
			case v := <-c2:
				gotSum += int64(v)
			case v := <-c3:
				gotSum += int64(v)
			case <-idle:
				t.Fatal("idle channel selected")
			}
		}
		wg.Wait()
		if gotSum != wantSum {
			t.Fatalf("cap=%d sum = %d, want %d", capSize, gotSum, wantSum)
		}
	}

	// Broadcast close wakes multiple goroutines parked in multi-case select.
	const waiters = 16
	work := make(chan int)
	broadcast := make(chan struct{})
	var woken atomic.Int32
	var wg sync.WaitGroup
	wg.Add(waiters)
	for i := 0; i < waiters; i++ {
		go func() {
			defer wg.Done()
			select {
			case <-work:
				t.Errorf("unexpected receive on work channel")
			case <-broadcast:
				woken.Add(1)
			}
		}()
	}
	time.Sleep(5 * time.Millisecond)
	close(broadcast)
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("broadcast close did not wake all select waiters")
	}
	if got := woken.Load(); got != waiters {
		t.Fatalf("woken = %d, want %d", got, waiters)
	}

	// Closing a channel while a goroutine is parked on a select send case must panic.
	sendCh := make(chan int)
	other := make(chan int)
	ready := make(chan struct{})
	go func() {
		<-ready
		time.Sleep(5 * time.Millisecond)
		close(sendCh)
	}()
	expectChannelPanicContaining(t, "send on closed channel", func() {
		close(ready)
		select {
		case sendCh <- 42:
		case <-other:
			t.Fatal("other channel unexpectedly selected")
		}
	})
}

func TestSelectDuplicateAndNilChannelSemantics(t *testing.T) {
	// Duplicate receive cases on the same channel plus nil cases in a reused loop.
	ch := make(chan int)
	var nilRecv chan int
	var nilSend chan int
	const n = 200
	go func() {
		for i := 0; i < n; i++ {
			ch <- i
		}
	}()
	var sum int
	for i := 0; i < n; i++ {
		select {
		case v := <-ch:
			sum += v
		case v := <-ch:
			sum += v
		case <-nilRecv:
			t.Fatal("nil recv selected")
		case nilSend <- i:
			t.Fatal("nil send selected")
		}
	}
	want := n * (n - 1) / 2
	if sum != want {
		t.Fatalf("duplicate recv sum = %d, want %d", sum, want)
	}

	// Duplicate send + recv on the same unbuffered channel in one select.
	dup := make(chan int)
	done := make(chan int, 1)
	go func() {
		select {
		case dup <- 77:
			done <- 1
		case v := <-dup:
			done <- v
		case <-nilRecv:
			done <- -1
		}
	}()
	// Either receive the 77 or send 99; here receive from dup to match send case.
	if v := <-dup; v != 77 {
		t.Fatalf("dup recv = %d, want 77", v)
	}
	if res := <-done; res != 1 {
		t.Fatalf("dup select result = %d, want 1", res)
	}
}

func expectChannelPanicContaining(t *testing.T, want string, f func()) {
	t.Helper()
	defer func() {
		err := recover()
		if err == nil {
			t.Fatalf("expected panic containing %q", want)
		}
		if got := fmt.Sprint(err); !strings.Contains(got, want) {
			t.Fatalf("panic = %q, want contains %q", got, want)
		}
	}()
	f()
}
