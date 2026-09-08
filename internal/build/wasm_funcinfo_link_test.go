//go:build !llgo

package build

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	llssa "github.com/xgo-dev/llgo/ssa"
)

func TestWasmLinkMapOutput(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"none", nil, ""},
		{"unrelated", []string{"-o", "map.wasm", "-Wl,--gc-sections", "-Xlinker"}, ""},
		{"wl equals", []string{"-Wl,--Map=out.map"}, "out.map"},
		{"wl separate", []string{"-Wl,-Map,out with spaces.map,--gc-sections"}, "out with spaces.map"},
		{"xlinker equals", []string{"-Xlinker", "-Map=out.map"}, "out.map"},
		{"xlinker separate", []string{"-Xlinker", "--Map", "-Xlinker", "out with spaces.map"}, "out with spaces.map"},
		{"mixed", []string{"-Wl,-Map", "-Xlinker", "out.map"}, "out.map"},
		{"last wins", []string{"-Wl,-Map,old.map", "-Xlinker", "--Map=new.map"}, "new.map"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := wasmLinkMapOutput(tt.args); got != tt.want {
				t.Fatalf("map = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWasmFuncInfoRelinkPreservesUserMap(t *testing.T) {
	prog := llssa.NewProgram(&llssa.Target{GOOS: "wasip1", GOARCH: "wasm"})
	defer prog.Dispose()
	prog.EnableFuncInfoSites(true)
	for _, userMap := range []bool{false, true} {
		t.Run(map[bool]string{false: "private", true: "user"}[userMap], func(t *testing.T) {
			dir := t.TempDir()
			ctx := &context{prog: prog, buildConf: &Config{Goarch: "wasm", BuildMode: BuildModeExe}}
			ctx.commands.dir = dir
			if userMap {
				ctx.crossCompile.LDFLAGS = []string{"-Wl,-Map,user map.txt"}
			}
			inputs := []string{"input.o"}
			p, err := prepareWasmFuncInfoRelink(ctx, filepath.Join(dir, "out.wasm"), inputs, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer p.cleanup()
			if p == nil || p.userMap != userMap || !slices.Equal(p.inputs, inputs) {
				t.Fatalf("unexpected plan: %+v", p)
			}
			inputs[0] = "changed.o"
			if p.inputs[0] != "input.o" {
				t.Fatal("plan aliases caller's inputs")
			}
			if userMap {
				if p.mapPath != filepath.Join(dir, "user map.txt") || len(p.probeArgs()) != 0 {
					t.Fatalf("user map overridden: %+v, args=%q", p, p.probeArgs())
				}
			} else if !slices.Equal(p.probeArgs(), []string{"-Xlinker", "--Map=" + p.mapPath}) {
				t.Fatalf("private probe flags = %q", p.probeArgs())
			}
			if err := os.WriteFile(p.mapPath, []byte("no live FuncForPC\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if root, err := p.liveEntryObject(ctx); err != nil || root != "" {
				t.Fatalf("unexpected relink: %q, %v", root, err)
			}
			p.cleanup()
			_, err = os.Stat(p.mapPath)
			if userMap && err != nil || !userMap && !os.IsNotExist(err) {
				t.Fatalf("map cleanup (user=%v): %v", userMap, err)
			}
		})
	}
}
