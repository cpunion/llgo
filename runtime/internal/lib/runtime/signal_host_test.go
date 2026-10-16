//go:build coro_signal_host_test

package runtime

import "testing"

// Named-source host test; production throw remains the ordinary fatal runtime
// hook. No fallback or callback is added to the LLGo runtime implementation.
func throw(message string) { panic(message) }

func TestHostSignalHooksRejectUnsupportedCapability(t *testing.T) {
	for name, call := range map[string]func(){
		"enable":  func() { signal_enable(1) },
		"disable": func() { signal_disable(1) },
		"ignore":  func() { signal_ignore(1) },
		"ignored": func() { signal_ignored(1) },
		"receive": func() { signal_recv() },
		"idle":    signalWaitUntilIdle,
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if got := recover(); got != hostSignalUnavailable {
					t.Fatalf("hook result = %v, want explicit unsupported capability", got)
				}
			}()
			call()
			t.Fatal("unsupported signal operation returned normally")
		})
	}
}
