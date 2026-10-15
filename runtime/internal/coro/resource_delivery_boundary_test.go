//go:build darwin || linux

package coro

import (
	"fmt"
	"syscall"
	"testing"
)

// This is a design-level oracle, not compiler or public-Open acceptance.
// The test deliberately supplies the parent cleanup absent from production
// scalar resource returns. The negative control proves Return alone cannot
// replace that cleanup. All scheduler transitions use the existing core.
func TestResourceDeliveryBoundary(t *testing.T) {
	// Inject only at the real owner decision boundary, after the physical
	// destroy transaction. A stop injected inside an executing child resume
	// or between its terminal publication and physical return is not this
	// gate's modeled scheduling boundary.
	for _, stage := range []string{"normal", "after-destroy"} {
		for _, guarded := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/guard=%t", stage, guarded), func(t *testing.T) {
				fixture := newAwaitCompletionFixture(t)
				defer fixture.keepAlive()
				// This storage belongs to the parent, not the child frame.
				output, originalFD, closeCount := -1, -1, 0
				defer func() {
					if originalFD >= 0 {
						_ = syscall.Close(originalFD)
					}
				}()
				var delivered int
				func() {
					adopted := false
					defer func() {
						if !guarded || adopted || output < 0 {
							return
						}
						if findFrame(fixture.g, fixture.child.handle) != nil {
							t.Fatal("cleanup ran before child strong join")
						}
						fd := output
						output = -1
						closeCount++
						if err := syscall.Close(fd); err != nil {
							t.Fatal(err)
						}
					}()
					var err error
					output, err = syscall.Open("/dev/null", syscall.O_RDONLY, 0)
					if err != nil {
						t.Fatal(err)
					}
					originalFD = output
					request := func(point string) {
						if stage == point && !RequestTaskCancellation(fixture.p, fixture.g, TaskCancelAbort) {
							t.Fatalf("request at %s", point)
						}
					}
					fixture.child.header.SuspendReason = uint16(SuspendFrameComplete)
					fixture.child.header.Lifecycle = uint16(FrameFinalSuspended)
					if !PrepareComplete(fixture.g, fixture.child.handle, fixture.child.header) {
						t.Fatal("publish child Return")
					}
					if snapshot, ok := ConsumeAwaitCompletion(fixture.g, fixture.parent.handle); ok || snapshot != (CompletionSnapshot{}) {
						t.Fatal("consumed before physical child destroy")
					}
					action, ok := Resumed(fixture.p, fixture.g, fixture.action)
					if !ok || action.Kind != ActionCheckDestroy {
						t.Fatal("child destroy selection")
					}
					action, ok = checkedTestAction(fixture.p, fixture.g, action, true)
					if !ok || action.Kind != ActionDestroy {
						t.Fatal("child destroy gate")
					}
					releaseTestFrame(t, fixture.g, fixture.child)
					action, ok = Destroyed(fixture.p, fixture.g, action)
					if !ok || action.Kind != ActionCheckResume || action.Handle != fixture.parent.handle {
						t.Fatal("child destroy commit")
					}
					request("after-destroy")
					action, ok = Checked(fixture.p, fixture.g, action, false)
					if !ok || action.Kind != ActionResume {
						t.Fatal("parent resume gate")
					}
					_, _, _, canceled, ok := TakeRunDecision(fixture.g, ParkTicket{})
					if !ok || (canceled == TaskCancelAbort) != (stage != "normal") {
						t.Fatalf("decision cancel=%d valid=%t", canceled, ok)
					}
					fixture.parent.header.SuspendReason = uint16(SuspendNone)
					fixture.parent.header.Lifecycle = uint16(FrameActive)
					snapshot, ok := ConsumeAwaitCompletion(fixture.g, fixture.parent.handle)
					if !ok || snapshot != (CompletionSnapshot{Status: CompletionReturn}) {
						t.Fatalf("same Return record on both lanes: %+v valid=%t", snapshot, ok)
					}
					if _, duplicate := ConsumeAwaitCompletion(fixture.g, fixture.parent.handle); duplicate {
						t.Fatal("completion replay")
					}
					if canceled != TaskCancelNone {
						return // Shared cleanup, never the user continuation.
					}
					delivered = output // No safepoint between load and disarm.
					adopted = true
				}()
				var stat syscall.Stat_t
				err := syscall.Fstat(originalFD, &stat)
				if stage != "normal" && guarded {
					if closeCount != 1 || err != syscall.EBADF || output != -1 {
						t.Fatalf("drop: close=%d stat=%v output=%d", closeCount, err, output)
					}
					originalFD = -1
				} else {
					// The unguarded canceled case intentionally reproduces the
					// current leak window, then the outer test closes the fd.
					if closeCount != 0 || err != nil || stage == "normal" && delivered != originalFD {
						t.Fatalf("normal/control: close=%d stat=%v delivered=%d", closeCount, err, delivered)
					}
				}
			})
		}
	}
}
