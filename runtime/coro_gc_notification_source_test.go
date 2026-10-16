package runtime

import (
	"os"
	"strings"
	"testing"
)

func TestCoroGCNotificationCollectorAndOwnerBoundary(t *testing.T) {
	read := func(path string) string {
		t.Helper()
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(source)
	}
	native := read("internal/clite/bdwgc/_wrap/coro_gc_notify.c")
	start := strings.Index(native, "static void GC_CALLBACK llgo_gc_collection_event(")
	end := strings.Index(native, "/* Called once during")
	if start < 0 || end <= start {
		t.Fatal("missing collector-native notification hook")
	}
	hook := native[start:end]
	for _, forbidden := range []string{"malloc(", "GC_call_with_alloc_lock", "pthread_mutex", "__llgo_coro_gc_observe_v1"} {
		if strings.Contains(hook, forbidden) {
			t.Errorf("collector callback performs forbidden operation %s", forbidden)
		}
	}
	for _, required := range []string{"event == GC_EVENT_END", "__atomic_fetch_add", "attempt < 4", "write(llgo_gc_wake_fd", "llgo_gc_previous_event(event)"} {
		if !strings.Contains(hook, required) {
			t.Errorf("collector notification lacks %s", required)
		}
	}
	tiny := read("internal/runtime/tinygogc/gc_tinygo.go")
	clear, sweep, notify := strings.Index(tiny, "\n\tclearUnmarkedWeakReferences()"), strings.Index(tiny, "\n\tfreeBytes = sweep()"), strings.Index(tiny, "\n\tgcCompleted()")
	if clear < 0 || sweep <= clear || notify <= sweep {
		t.Fatal("tinygc notification must follow weak clearing and sweep")
	}
	for _, path := range []string{"internal/runtime/coro_sched.go", "internal/runtime/coro_run_slice.go", "internal/runtime/coro_target_host_llgo.go"} {
		if !strings.Contains(read(path), "coroObserveGCV1()") {
			t.Errorf("%s lacks a retained GC observation point", path)
		}
	}
	for _, path := range []string{"internal/runtime/coro_executor_driver_timer_llgo.go", "internal/runtime/coro_executor_driver_host_llgo.go", "internal/runtime/coro_native_fleet.go"} {
		if !strings.Contains(read(path), "if coroObserveGCV1() {") {
			t.Errorf("%s may sleep after a busy/unacknowledged GC observation", path)
		}
	}
	lifecycle := read("internal/runtime/coro_native_fleet.go")
	start = strings.Index(lifecycle, "func coroNativeFleetRetireBackendV1(")
	if start < 0 {
		t.Fatal("missing native backend retirement")
	}
	lifecycle = lifecycle[start:]
	detach, close := strings.Index(lifecycle, "coroDetachGCNotificationV1(fd)"), strings.Index(lifecycle, "domain.doorbell.Close()")
	if detach < 0 || close <= detach {
		t.Fatal("collector hook must strongly detach before doorbell descriptor close")
	}
}

func TestCoroGCNotificationIsGuardedByFrozenProgramDemand(t *testing.T) {
	// The actual scheduler/bootstrap edges must disappear before effect
	// analysis. Go-only observer/completion functions must not become exported
	// C roots which retain dead work or invent a native context boundary.
	for _, path := range []string{
		"internal/coroalloc/backend_gc.go",
		"internal/runtime/coro_gc_observer_llgo.go",
		"internal/runtime/coro_gc_observer_bdwgc_llgo.go",
	} {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(source), "coro.GCObservationEnabled()") {
			t.Errorf("%s retains an unconditional post-GC observation root", path)
		}
	}
	for _, entry := range []struct{ path, alias string }{
		{"internal/runtime/coro_gc_observer_llgo.go", "//go:linkname coroGCObserveV1 runtime.coroTimerGCObserveV1"},
		{"internal/runtime/tinygogc/gc_notify_llgo.go", "//go:linkname gcCompleted github.com/xgo-dev/llgo/runtime/internal/runtime.coroGCCompletedV1"},
		{"internal/lib/runtime/time_coro_gc_llgo.go", "func coroTimerGCObserveV1() bool"},
	} {
		source, err := os.ReadFile(entry.path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(source)
		if !strings.Contains(text, entry.alias) || strings.Contains(text, "//export ") || strings.Contains(text, "C.__llgo_coro_gc_") {
			t.Errorf("%s must preserve its exact Go-only notification boundary", entry.path)
		}
	}
}
