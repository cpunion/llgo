//go:build llgo && wasip1 && wasm

package wasmtest

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"
	_ "unsafe"
)

var wasiPollStdin = flag.Bool("llgo.wasi-poll-stdin", false, "test waiting on a host-held empty stdin pipe")

// Exercise the actual host poll_oneoff call: ordinary file reads can succeed
// without ever waiting for readiness in internal/poll.
//
//go:linkname wasiTestPollOpen internal/poll.runtime_pollOpen
func wasiTestPollOpen(fd uintptr) (uintptr, int)

//go:linkname wasiTestPollWait internal/poll.runtime_pollWait
func wasiTestPollWait(ctx uintptr, mode int) int

//go:linkname wasiTestPollDeadline internal/poll.runtime_pollSetDeadline
func wasiTestPollDeadline(ctx uintptr, d int64, mode int)

//go:linkname wasiTestPollUnblock internal/poll.runtime_pollUnblock
func wasiTestPollUnblock(ctx uintptr)

//go:linkname wasiTestPollClose internal/poll.runtime_pollClose
func wasiTestPollClose(ctx uintptr)

func TestWASIPollDescriptorLifecycle(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "poll"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ctx, errno := wasiTestPollOpen(f.Fd())
	if errno != 0 || ctx == 0 {
		t.Fatalf("pollOpen = %d, %d", ctx, errno)
	}
	defer wasiTestPollClose(ctx)
	for _, mode := range []int{'r', 'w'} {
		if got := wasiTestPollWait(ctx, mode); got != 0 {
			t.Fatalf("ready %c: %d", mode, got)
		}
		wasiTestPollDeadline(ctx, -1, mode)
		if got := wasiTestPollWait(ctx, mode); got != 2 {
			t.Fatalf("expired %c: %d", mode, got)
		}
		wasiTestPollDeadline(ctx, 0, mode)
		if got := wasiTestPollWait(ctx, mode); got != 0 {
			t.Fatalf("reset %c: %d", mode, got)
		}
	}
	wasiTestPollUnblock(ctx)
	if got := wasiTestPollWait(ctx, 'r'); got != 1 {
		t.Fatalf("unblocked read: %d", got)
	}
}

func TestWASIPollBlockedWait(t *testing.T) {
	if !*wasiPollStdin {
		return // The host CI driver keeps stdin open without supplying data.
	}
	for _, action := range []string{"timeout", "changed deadline", "close"} {
		t.Run(action, func(t *testing.T) {
			ctx, errno := wasiTestPollOpen(0)
			if errno != 0 || ctx == 0 {
				t.Fatalf("pollOpen = %d, %d", ctx, errno)
			}
			defer wasiTestPollClose(ctx)
			if action == "timeout" {
				wasiTestPollDeadline(ctx, int64(20*time.Millisecond), 'r')
			}
			progress := make(chan struct{})
			go func() {
				time.Sleep(10 * time.Millisecond)
				switch action {
				case "changed deadline":
					wasiTestPollDeadline(ctx, -1, 'r')
				case "close":
					wasiTestPollUnblock(ctx)
				}
				close(progress)
			}()
			want := 2 // pollErrTimeout
			if action == "close" {
				want = 1 // pollErrClosing
			}
			if got := wasiTestPollWait(ctx, 'r'); got != want {
				t.Fatalf("blocked wait = %d, want %d (host stdin must remain empty and open)", got, want)
			}
			select {
			case <-progress:
			default:
				t.Fatal("poll wait prevented another goroutine from running")
			}
		})
	}
}
