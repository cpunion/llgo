//go:build !llgo

package build

import (
	"slices"
	"testing"

	"github.com/xgo-dev/llgo/internal/crosscompile"
)

func TestDefaultWASIHeapArgs(t *testing.T) {
	for _, test := range []struct {
		name     string
		goos     string
		goarch   string
		args     []string
		config   []string
		prefix   []string
		ccflags  string
		ldflags  string
		wantHeap bool
	}{
		{name: "default", goos: "wasip1", goarch: "wasm", wantHeap: true},
		{name: "named WASI flags", goos: "wasip1", goarch: "wasm", config: []string{"-target", "wasm32-unknown-wasip1", "-Wl,--stack-first"}, wantHeap: true},
		{name: "native", goos: "linux", goarch: "amd64"},
		{name: "non-wasm architecture", goos: "wasip1", goarch: "amd64"},
		{name: "emscripten", goos: "js", goarch: "wasm"},
		{name: "package memory", goos: "wasip1", goarch: "wasm", args: []string{"-Wl,--initial-memory=33554432"}},
		{name: "package heap", goos: "wasip1", goarch: "wasm", args: []string{"-Wl,--initial-heap=1048576"}},
		{name: "separate driver option", goos: "wasip1", goarch: "wasm", args: []string{"-Wl,--initial-memory,33554432"}},
		{name: "xlinker separate", goos: "wasip1", goarch: "wasm", args: []string{"-Xlinker", "--initial-heap", "-Xlinker", "0"}},
		{name: "xlinker equals", goos: "wasip1", goarch: "wasm", args: []string{"-Xlinker", "--initial-memory=33554432"}},
		{name: "direct linker option", goos: "wasip1", goarch: "wasm", args: []string{"--initial-memory", "33554432"}},
		{name: "config or extldflags", goos: "wasip1", goarch: "wasm", config: []string{"-Wl,--initial-memory=33554432"}},
		{name: "linker prefix", goos: "wasip1", goarch: "wasm", prefix: []string{"-Wl,--initial-heap=1048576"}},
		{name: "CCFLAGS", goos: "wasip1", goarch: "wasm", ccflags: "-Wl,--initial-memory=33554432"},
		{name: "LDFLAGS", goos: "wasip1", goarch: "wasm", ldflags: "-Xlinker --initial-heap=1048576"},
		{name: "maximum unchanged", goos: "wasip1", goarch: "wasm", args: []string{"-Wl,--max-memory=268435456"}, wantHeap: true},
		{name: "unrelated names", goos: "wasip1", goarch: "wasm", args: []string{"data-initial-memory.o", "-Wl,-Map,initial-heap.map"}, wantHeap: true},
		{name: "response contents not expanded", goos: "wasip1", goarch: "wasm", args: []string{"@initial-memory.rsp"}, wantHeap: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("CCFLAGS", test.ccflags)
			t.Setenv("LDFLAGS", test.ldflags)
			ctx := &context{
				buildConf: &Config{Goos: test.goos, Goarch: test.goarch},
				crossCompile: crosscompile.Export{
					Linker: "clang", LinkerArgs: test.prefix, LDFLAGS: test.config,
				},
			}
			before := ctx.linker().LinkArguments(test.args...)
			got := defaultWASIHeapArgs(ctx, test.args)
			var want []string
			if test.wantHeap {
				want = []string{defaultWASIHeapFlag}
			}
			if !slices.Equal(got, want) {
				t.Fatalf("flags = %q, want %q", got, want)
			}
			if after := ctx.linker().LinkArguments(test.args...); !slices.Equal(before, after) {
				t.Fatalf("explicit arguments changed: before %q, after %q", before, after)
			}
		})
	}
	if got := defaultWASIHeapArgs(nil, nil); len(got) != 0 {
		t.Fatalf("nil context: %q", got)
	}
	if got := defaultWASIHeapArgs(&context{}, nil); len(got) != 0 {
		t.Fatalf("missing config: %q", got)
	}
}
