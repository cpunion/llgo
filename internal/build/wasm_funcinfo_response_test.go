//go:build !llgo

package build

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/clang"
	"github.com/xgo-dev/llgo/internal/crosscompile"
	llssa "github.com/xgo-dev/llgo/ssa"
	llvm "github.com/xgo-dev/llvm"
)

func TestWasmLinkNeedsStdoutProbe(t *testing.T) {
	for _, test := range []struct {
		args []string
		want bool
	}{
		{},
		{args: []string{"plain.o", "objects@user.o", "objects,@user.o", "-Wl,--Map=user map.txt"}},
		{args: []string{"@user flags.rsp"}, want: true},
		{args: []string{"-Wl,@user flags.rsp"}, want: true},
		{args: []string{"-Wl,--gc-sections,@user flags.rsp"}, want: true},
		{args: []string{"-Xlinker", "@user flags.rsp"}, want: true},
		{args: []string{"-Wl,--print-map"}, want: true},
		{args: []string{"-Xlinker", "--print-map"}, want: true},
	} {
		if got := wasmLinkNeedsStdoutProbe(test.args); got != test.want {
			t.Errorf("stdout probe for %q = %t, want %t", test.args, got, test.want)
		}
	}
}

func TestWasmMapProbeClosesAndRestoresStdout(t *testing.T) {
	t.Setenv("CCFLAGS", "")
	t.Setenv("LDFLAGS", "")
	closeFailure := errors.New("close failed")
	for _, mode := range []string{"write", "fail"} {
		for _, closeErr := range []error{nil, closeFailure} {
			t.Setenv("LLGO_TEST_LINKER_HELPER", mode)
			cmd := clang.New(os.Args[0], clang.Config{})
			var original bytes.Buffer
			cmd.Stdout = &original
			output := &wasmProbeTestOutput{closeErr: closeErr}
			err := linkWasmMapProbe(cmd, output, []string{"-o", filepath.Join(t.TempDir(), "out.wasm")})
			if !output.closed || cmd.Stdout != &original {
				t.Fatalf("probe leaked its output (mode=%s): closed=%t stdout=%T", mode, output.closed, cmd.Stdout)
			}
			if errors.Is(err, closeFailure) != (closeErr != nil) {
				t.Fatalf("close error was lost: %v", err)
			}
			if mode == "fail" && (err == nil || !strings.Contains(err.Error(), "exit status")) {
				t.Fatalf("link error was lost: %v", err)
			}
			if mode == "write" && closeErr == nil && err != nil {
				t.Fatal(err)
			}
		}
	}
	plan := &wasmFuncInfoRelink{mapPath: t.TempDir(), stdoutProbe: true}
	if err := plan.linkProbe(clang.New(os.Args[0], clang.Config{}), nil); err == nil || !strings.Contains(err.Error(), "open WebAssembly probe stdout map") {
		t.Fatalf("invalid capture path was accepted: %v", err)
	}
}

func TestWasmResponseProbeUsesEffectiveArguments(t *testing.T) {
	t.Setenv("CCFLAGS", "")
	t.Setenv("LDFLAGS", "")
	prog := llssa.NewProgram(&llssa.Target{GOOS: "js", GOARCH: "wasm"})
	defer prog.Dispose()
	prog.EnableFuncInfoSites(true)
	for _, source := range []string{"prefix", "config", "environment"} {
		t.Run(source, func(t *testing.T) {
			ctx := &context{prog: prog, buildConf: &Config{Goarch: "wasm", BuildMode: BuildModeExe}}
			ctx.commands.dir = t.TempDir()
			switch source {
			case "prefix":
				ctx.crossCompile.Linker = "clang"
				ctx.crossCompile.LinkerArgs = []string{"@unread response file.rsp"}
			case "config":
				ctx.crossCompile.LDFLAGS = []string{"-Xlinker", "@unread response file.rsp"}
			case "environment":
				t.Setenv("LDFLAGS", "-Wl,@unread response file.rsp")
			}
			p, err := prepareWasmFuncInfoRelink(ctx, filepath.Join(ctx.commands.dir, "app.wasm"), nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer p.cleanup()
			if !p.stdoutProbe || p.userMap || !slices.Equal(p.probeArgs(), []string{"-Xlinker", "--print-map"}) {
				t.Fatalf("effective response argument was ignored: %+v", p)
			}
		})
	}
}

type wasmProbeTestOutput struct {
	bytes.Buffer
	closeErr error
	closed   bool
}

func (o *wasmProbeTestOutput) Close() error {
	o.closed = true
	return o.closeErr
}

// Exercise the real LLVM driver/linker, without emcc, Node or a Go runtime.
// The response parser remains the driver's, including nested cwd-relative
// filenames and map destinations containing spaces. A dead address-taken
// function has an unresolved import, so retaining all entry rows would fail.
func TestWasmFuncInfoResponseMaps(t *testing.T) {
	t.Setenv("CCFLAGS", "")
	t.Setenv("LDFLAGS", "")
	compiler, err := exec.LookPath("clang")
	if err != nil {
		t.Fatalf("clang is required: %v", err)
	}
	linker, err := exec.LookPath("wasm-ld")
	if err != nil {
		t.Fatalf("wasm-ld is required: %v", err)
	}
	prog := llssa.NewProgram(&llssa.Target{GOOS: "js", GOARCH: "wasm"})
	defer prog.Dispose()
	prog.EnableFuncInfoSites(true)
	object := wasmResponseEntryFixture(t, prog, compiler)
	for _, consumer := range []bool{false, true} {
		for _, spelling := range []string{"driver", "wl", "xlinker"} {
			name := spelling + map[bool]string{false: "/no-consumer", true: "/funcforpc"}[consumer]
			t.Run(name, func(t *testing.T) {
				dir := t.TempDir()
				if err := os.Mkdir(filepath.Join(dir, "nested dir"), 0o700); err != nil {
					t.Fatal(err)
				}
				writeWasmResponseTestFile(t, filepath.Join(dir, "outer flags.rsp"), "@\"nested dir/inner flags.rsp\"\n")
				mapOption := "--Map=user map.txt"
				if spelling == "driver" {
					mapOption = "-Wl," + mapOption
				}
				writeWasmResponseTestFile(t, filepath.Join(dir, "nested dir", "inner flags.rsp"), "\""+mapOption+"\"\n")
				args := []string{"-Wl,--no-entry,--gc-sections"}
				if consumer {
					args = append(args, "-Wl,--export=runtime.FuncForPC")
				} else {
					args = append(args, "-Wl,--export=live")
				}
				switch spelling {
				case "driver":
					args = append(args, "@outer flags.rsp")
				case "wl":
					args = append(args, "-Wl,@outer flags.rsp")
				case "xlinker":
					args = append(args, "-Xlinker", "@outer flags.rsp")
				}
				ctx := &context{
					prog: prog,
					buildConf: &Config{Goos: "js", Goarch: "wasm", BuildMode: BuildModeExe,
						LinkOptions: LinkOptions{DWARF: DWARFOmit}},
					crossCompile: crosscompile.Export{Linker: compiler,
						LinkerArgs: []string{"--target=wasm32-unknown-unknown", "-nostdlib", "-fuse-ld=" + linker}},
				}
				ctx.commands.dir = dir
				before := slices.Clone(args)
				if err := linkObjFiles(ctx, filepath.Join(dir, "app.wasm"), []string{object}, args, false); err != nil {
					t.Fatal(err)
				}
				if !slices.Equal(before, args) {
					t.Fatal("linking changed user arguments")
				}
				data, err := os.ReadFile(filepath.Join(dir, "user map.txt"))
				if err != nil {
					t.Fatalf("hidden user map was not produced: %v", err)
				}
				linkMap := string(data)
				if !wasmLinkMapHasSymbol(linkMap, "live") || strings.Contains(linkMap, "missing_from_dead") {
					t.Fatalf("incorrect final liveness:\n%s", linkMap)
				}
				if got := strings.Contains(linkMap, wasmFuncInfoEntryPrefix+"live"); got != consumer {
					t.Fatalf("final user map has entry row = %t, want %t:\n%s", got, consumer, linkMap)
				}
				if files, _ := filepath.Glob(filepath.Join(dir, ".llgo-wasm-funcinfo-*.map")); len(files) != 0 {
					t.Fatalf("private maps were not cleaned up: %q", files)
				}
			})
		}
	}
	for _, spelling := range []string{"response-stdout", "explicit-print-map"} {
		t.Run(spelling, func(t *testing.T) {
			dir := t.TempDir()
			ctx := &context{
				prog: prog,
				buildConf: &Config{Goos: "js", Goarch: "wasm", BuildMode: BuildModeExe,
					LinkOptions: LinkOptions{DWARF: DWARFOmit}},
				crossCompile: crosscompile.Export{Linker: compiler,
					LinkerArgs: []string{"--target=wasm32-unknown-unknown", "-nostdlib", "-fuse-ld=" + linker}},
			}
			ctx.commands.dir = dir
			app := filepath.Join(dir, "app.wasm")
			args := []string{"-o", app, "-Wl,--no-entry,--gc-sections,--export=live", object}
			if spelling == "response-stdout" {
				writeWasmResponseTestFile(t, filepath.Join(dir, "stdout flags.rsp"), "-Wl,--Map=-\n")
				args = append(args, "@stdout flags.rsp")
			} else {
				args = append(args, "-Wl,--Map=unused.map,--print-map")
			}
			p, err := prepareWasmFuncInfoRelink(ctx, app, []string{object}, args)
			if err != nil {
				t.Fatal(err)
			}
			defer p.cleanup()
			var output bytes.Buffer
			cmd := ctx.linker()
			cmd.Stdout = &output
			if err := p.linkProbe(cmd, args); err != nil {
				t.Fatal(err)
			}
			if output.Len() != 0 || cmd.Stdout != &output {
				t.Fatal("private probe leaked to or replaced the user's stdout")
			}
			if root, err := p.liveEntryObject(ctx); err != nil || root != "" {
				t.Fatalf("non-consumer probe: root=%q err=%v", root, err)
			}
			if err := cmd.Link(args...); err != nil {
				t.Fatal(err)
			}
			if strings.Count(output.String(), "Addr") != 1 || !wasmLinkMapHasSymbol(output.String(), "live") {
				t.Fatalf("expected exactly the final stdout map:\n%s", output.String())
			}
			if _, err := os.Stat(filepath.Join(dir, "unused.map")); !os.IsNotExist(err) {
				t.Fatalf("--print-map did not retain its driver precedence: %v", err)
			}
		})
	}
}

func wasmResponseEntryFixture(t *testing.T, prog llssa.Program, compiler string) string {
	t.Helper()
	pkg := prog.NewPackage("response-entries", "response-entries")
	mod := pkg.Module()
	ctx := mod.Context()
	fnType := llvm.FunctionType(ctx.VoidType(), nil, false)
	live := llvm.AddFunction(mod, "live", fnType)
	dead := llvm.AddFunction(mod, "dead", fnType)
	consumer := llvm.AddFunction(mod, "runtime.FuncForPC", fnType)
	missing := llvm.AddFunction(mod, "missing_from_dead", fnType)
	builder := ctx.NewBuilder()
	defer builder.Dispose()
	for _, pair := range []struct{ fn, call llvm.Value }{{live, llvm.Value{}}, {dead, missing}, {consumer, live}} {
		pair.fn.SetSection(".text." + pair.fn.Name())
		builder.SetInsertPointAtEnd(ctx.AddBasicBlock(pair.fn, "entry"))
		if !pair.call.IsNil() {
			llvm.CreateCall(builder, fnType, pair.call, nil)
		}
		builder.CreateRetVoid()
	}
	addresses := llvm.ConstArray(live.Type(), []llvm.Value{live, dead})
	unused := llvm.AddGlobal(mod, addresses.Type(), "unused_addresses")
	unused.SetInitializer(addresses)
	unused.SetLinkage(llvm.InternalLinkage)
	emitWasmFuncInfoEntrySites(mod, map[string]uint64{"live": 1, "dead": 2})
	dir := t.TempDir()
	ir := filepath.Join(dir, "entries.ll")
	object := filepath.Join(dir, "entries.o")
	writeWasmResponseTestFile(t, ir, mod.String())
	if output, err := exec.Command(compiler, "--target=wasm32-unknown-unknown", "-Wno-override-module", "-c", ir, "-o", object).CombinedOutput(); err != nil {
		t.Fatalf("compile response-file entry fixture: %v\n%s", err, output)
	}
	return object
}

func writeWasmResponseTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
