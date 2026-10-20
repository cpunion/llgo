//go:build !llgo && (darwin || linux)

package runtime

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Each process initializes the real collector through the production wrapper.
// A fresh process matters: marker configuration is immutable after GC_init.
func TestBDWGCInitialMarkerQuota(t *testing.T) {
	flags, err := exec.Command("pkg-config", "--cflags", "--libs", "bdw-gc").Output()
	if err != nil {
		t.Skipf("BDWGC development files unavailable: %v", err)
	}
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("C compiler unavailable")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "marker_quota.c")
	program := `
#if defined(__APPLE__) && !defined(_DARWIN_C_SOURCE)
#define _DARWIN_C_SOURCE 1
#endif
#define GC_THREADS 1
#include <gc/gc.h>
#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>
extern void __llgo_coro_gc_init_v1(void);
int main(int argc, char **argv) {
    if (argc > 1) {
        GC_set_markers_count(3);
        GC_init();
    }
    __llgo_coro_gc_init_v1();
    GC_start_mark_threads();
    unsigned before = (unsigned)GC_get_parallel() + 1;
    setenv("GOMAXPROCS", "4", 1);
    __llgo_coro_gc_init_v1();
    unsigned after = (unsigned)GC_get_parallel() + 1;
    printf("%u %u %ld\n", before, after, sysconf(_SC_NPROCESSORS_ONLN));
    return before == after ? 0 : 1;
}
`
	if err := os.WriteFile(source, []byte(program), 0600); err != nil {
		t.Fatal(err)
	}
	image := filepath.Join(dir, "marker_quota")
	args := []string{"-std=c11", "-D_POSIX_C_SOURCE=200809L", source,
		"internal/clite/bdwgc/_wrap/coro_init.c", "-o", image}
	args = append(args, strings.Fields(string(flags))...)
	if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("compile collector quota fixture: %v\n%s", err, out)
	}
	base := []string{}
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if name != "GOMAXPROCS" && name != "GC_MARKERS" && name != "GC_NPROCS" {
			base = append(base, entry)
		}
	}
	run := func(t *testing.T, settings []string, preinit bool) (count, online int) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, image)
		if preinit {
			cmd.Args = append(cmd.Args, "preinitialized")
		}
		cmd.Env = append(append([]string{}, base...), settings...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("collector startup %v: %v\n%s", settings, err, out)
		}
		var after int
		if n, err := fmt.Sscan(string(out), &count, &after, &online); n != 3 || err != nil || count != after || count < 1 || online < 1 {
			t.Fatalf("invalid collector result: %q (%v)", out, err)
		}
		return
	}
	defaultCount, _ := run(t, nil, false)
	for _, tc := range []struct {
		name, quota string
	}{
		{"one", "1"}, {"two", "2"}, {"four", "4"}, {"leading-zero", "0002"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, online := run(t, []string{"GOMAXPROCS=" + tc.quota}, false)
			var want int
			fmt.Sscan(tc.quota, &want)
			if want > online {
				want = online
			}
			if got != want {
				t.Fatalf("marker count = %d, want %d", got, want)
			}
		})
	}
	for _, invalid := range []string{"", "0", "-1", "+2", "2x", " 2", "2 "} {
		t.Run("invalid/"+invalid, func(t *testing.T) {
			if got, _ := run(t, []string{"GOMAXPROCS=" + invalid}, false); got != defaultCount {
				t.Fatalf("invalid quota changed collector default: %d != %d", got, defaultCount)
			}
		})
	}
	for _, override := range []string{"GC_MARKERS=3", "GC_NPROCS=3"} {
		t.Run(override, func(t *testing.T) {
			if got, _ := run(t, []string{"GOMAXPROCS=1", override}, false); got != 3 {
				t.Fatalf("explicit collector setting lost: %d", got)
			}
		})
	}
	if got, _ := run(t, []string{"GOMAXPROCS=1"}, true); got != 3 {
		t.Fatalf("embedder marker setting lost: %d", got)
	}
	if got, online := run(t, []string{"GOMAXPROCS=99999999999999999999999999999999999999999"}, false); got > online {
		t.Fatalf("overflowing quota exceeded CPU count: %d > %d", got, online)
	}
}
