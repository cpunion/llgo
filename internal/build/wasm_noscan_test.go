package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
)

func TestFuncInfoNoScanSections(t *testing.T) {
	for _, goos := range []string{"js", "wasip1", "linux"} {
		t.Run(goos, func(t *testing.T) {
			goarch := "wasm"
			if goos == "linux" {
				goarch = "amd64"
			}
			prog := llssa.NewProgram(&llssa.Target{GOOS: goos, GOARCH: goarch})
			defer prog.Dispose()
			prog.EnableFuncInfoMetadata(true)
			pkg := prog.NewPackage("tables", "tables")
			pkg.EmitFuncInfo("tables.live", "tables.Live", "live.go", 17, 3)
			ctx := &context{prog: prog, buildConf: &Config{Goos: goos, Goarch: goarch}}
			emitFuncInfoTable(ctx, pkg, collectFuncInfo([]Package{{LPkg: pkg}}), nil)
			for _, name := range []string{"__llgo_funcinfo_hash$data", "__llgo_funcinfo_symbol_index$data"} {
				data := pkg.Module().NamedGlobal(name)
				if data.IsNil() {
					t.Fatalf("missing metadata table %s", name)
				}
				want := "llgo_gc_noscan"
				if goarch != "wasm" {
					want = ""
				}
				if got := data.Section(); got != want {
					t.Fatalf("%s section = %q, want %q", name, got, want)
				}
			}
		})
	}
}

func TestPrepareWasmStaticRootMarker(t *testing.T) {
	path, cleanup, err := prepareWasmStaticRootMarker(nil, filepath.Join(t.TempDir(), "app.wasm"))
	if err != nil || path != "" || cleanup == nil {
		t.Fatalf("nil context marker = %q/%v, cleanup nil=%t", path, err, cleanup == nil)
	}
	cleanup()

	prog := llssa.NewProgram(&llssa.Target{
		GOOS: "wasip1", GOARCH: "wasm", LLVMTarget: "wasm32-unknown-unknown",
	})
	defer prog.Dispose()
	ctx := &context{prog: prog, buildConf: &Config{Goos: "wasip1", Goarch: "wasm", BuildMode: BuildModeExe}}
	dir := t.TempDir()
	path, cleanup, err = prepareWasmStaticRootMarker(ctx, filepath.Join(dir, "app.wasm"))
	if err != nil || path != "" || cleanup == nil {
		t.Fatalf("disabled GC marker = %q/%v, cleanup nil=%t", path, err, cleanup == nil)
	}
	cleanup()

	prog.EnableGCRoots(true)
	path, cleanup, err = prepareWasmStaticRootMarker(ctx, filepath.Join(dir, "app.wasm"))
	if err != nil {
		t.Fatal(err)
	}
	if path == "" || cleanup == nil {
		t.Fatalf("enabled GC marker = %q, cleanup nil=%t", path, cleanup == nil)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("static-root marker was not written: %v", err)
	}
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("static-root marker cleanup error = %v", err)
	}
}

func TestWasmFuncInfoNoScanLinking(t *testing.T) {
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Fatalf("clang is required: %v", err)
	}
	linker, err := exec.LookPath("wasm-ld")
	if err != nil {
		t.Fatalf("wasm-ld is required: %v", err)
	}
	nm, err := exec.LookPath("llvm-nm")
	if err != nil {
		t.Fatalf("llvm-nm is required: %v", err)
	}
	for _, config := range []struct {
		triple string
		lto    bool
	}{
		{"wasm32-unknown-unknown", false}, {"wasm64-unknown-unknown", false},
		{"wasm32-unknown-unknown", true}, {"wasm64-unknown-unknown", true},
	} {
		t.Run(config.triple+"/lto="+strconv.FormatBool(config.lto), func(t *testing.T) {
			triple := config.triple
			prog := llssa.NewProgram(&llssa.Target{GOOS: "wasip1", GOARCH: "wasm", LLVMTarget: triple})
			defer prog.Dispose()
			ctx := &context{prog: prog, buildConf: &Config{Goos: "wasip1", Goarch: "wasm"}}
			pkg := prog.NewPackage("tables", "tables")
			mod := pkg.Module()
			mod.SetTarget(triple)
			mod.SetDataLayout(prog.DataLayout())
			i32 := mod.Context().Int32Type()
			for _, name := range []string{"live_noscan_table", "dead_noscan_table"} {
				g := llvm.AddGlobal(mod, i32, name)
				g.SetInitializer(llvm.ConstInt(i32, 0x12345678, false))
				g.SetGlobalConstant(true)
				g.SetLinkage(llvm.PrivateLinkage)
				setWasmFuncInfoNoScan(ctx, mod, g)
				// Use a public pointer to select either table without retaining the other.
				p := llvm.AddGlobal(mod, g.Type(), "keep_"+name)
				p.SetInitializer(g)
			}
			if err := llvm.VerifyModule(mod, llvm.ReturnStatusAction); err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			marker := filepath.Join(dir, "static-roots.o")
			if err := writeWasmStaticRootMarkerObject(ctx, marker); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command(nm, "--defined-only", marker).CombinedOutput(); err != nil ||
				!strings.Contains(string(out), "llgo_gc_globals_start_marker") ||
				!strings.Contains(string(out), "llgo_gc_tls_start_marker") ||
				!strings.Contains(string(out), "llgo_gc_globals_start") {
				t.Fatalf("static-root marker object is invalid: %v\n%s", err, out)
			}
			ir, object := filepath.Join(dir, "tables.ll"), filepath.Join(dir, "tables.o")
			if err := os.WriteFile(ir, []byte(pkg.String()), 0600); err != nil {
				t.Fatal(err)
			}
			compileArgs := []string{"--target=" + triple, "-c", ir, "-o", object}
			if config.lto {
				compileArgs = append(compileArgs, "-flto")
			}
			if out, err := exec.Command(clang, compileArgs...).CombinedOutput(); err != nil {
				t.Fatalf("compile tables: %v\n%s", err, out)
			}
			// Use the actual runtime C sentinel, independently of the metadata object.
			sentinel := filepath.Join(dir, "gc.o")
			cfile := filepath.Join("..", "..", "runtime", "internal", "runtime", "tinygogc", "_wrap", "gc_wasm.c")
			compileArgs = []string{"--target=" + triple, "-O2", "-c", cfile, "-o", sentinel}
			if config.lto {
				compileArgs = append(compileArgs, "-flto")
			}
			if out, err := exec.Command(clang, compileArgs...).CombinedOutput(); err != nil {
				t.Fatalf("compile collector boundary: %v\n%s", err, out)
			}
			// A wasm c-archive is consumed by an external final link and cannot
			// guarantee that an LLGo marker is its first input. Its weak fallback
			// must therefore remain linkable without the generated object.
			fallback := filepath.Join(dir, "archive-consumer.wasm")
			fallbackArgs := []string{"--no-entry", "--gc-sections", "--export=llgo_gc_globals_start", "-o", fallback, sentinel}
			if strings.HasPrefix(triple, "wasm64") {
				fallbackArgs = append(fallbackArgs, "-mwasm64")
			}
			if out, err := exec.Command(linker, fallbackArgs...).CombinedOutput(); err != nil {
				t.Fatalf("link c-archive fallback: %v\n%s", err, out)
			}
			for _, live := range []bool{false, true} {
				name := "empty"
				if live {
					name = "one-live"
				}
				linkMap := filepath.Join(dir, name+".map")
				wasm := filepath.Join(dir, name+".wasm")
				args := []string{"--no-entry", "--gc-sections", "--export=llgo_gc_globals_start", "--export=llgo_gc_noscan_start", "--export=llgo_gc_noscan_end", "--Map=" + linkMap, "-o", wasm, marker, object, sentinel}
				if strings.HasPrefix(triple, "wasm64") {
					args = append(args, "-mwasm64")
				}
				if live {
					args = append(args, "--export=keep_live_noscan_table")
				}
				if out, err := exec.Command(linker, args...).CombinedOutput(); err != nil {
					t.Fatalf("link %s: %v\n%s", name, err, out)
				}
				data, err := os.ReadFile(linkMap)
				if err != nil {
					t.Fatal(err)
				}
				contents := string(data)
				if !strings.Contains(contents, "llgo_gc_noscan_sentinel") {
					t.Fatalf("missing linker boundaries/sentinel:\n%s", contents)
				}
				if strings.Contains(contents, "dead_noscan_table") || strings.Contains(contents, "live_noscan_table") != live {
					t.Fatalf("noscan range changed table DCE, live=%t:\n%s", live, contents)
				}
				tlsStart, dataStart := uint64(0), uint64(0)
				dataMarkerStart, tlsMarkerStart := uint64(0), uint64(0)
				foundTLS, foundData := false, false
				foundDataMarker, foundTLSMarker := false, false
				for _, line := range strings.Split(contents, "\n") {
					fields := strings.Fields(line)
					if len(fields) != 4 {
						continue
					}
					address, parseErr := strconv.ParseUint(fields[0], 16, 64)
					if parseErr != nil {
						continue
					}
					switch fields[3] {
					case ".tdata":
						tlsStart, foundTLS = address, true
					case ".data":
						dataStart, foundData = address, true
					}
					if strings.Contains(fields[3], "(.data.llgo_gc_start)") {
						dataMarkerStart, foundDataMarker = address, true
					}
					if strings.Contains(fields[3], "(.tdata.llgo_gc_start)") {
						tlsMarkerStart, foundTLSMarker = address, true
					}
				}
				if !foundData || !foundTLS || !foundDataMarker || !foundTLSMarker ||
					dataStart != dataMarkerStart || tlsStart != tlsMarkerStart {
					t.Fatalf("static-root markers are not mutable segment boundaries: data=%#x/%t marker=%#x/%t tls=%#x/%t marker=%#x/%t\n%s",
						dataStart, foundData, dataMarkerStart, foundDataMarker,
						tlsStart, foundTLS, tlsMarkerStart, foundTLSMarker, contents)
				}
				wantSize := 8
				if live {
					wantSize = 16
				}
				// Strict linking resolves both exported boundary getters. Inspect
				// the resulting segment without requiring a Memory64-capable
				// JavaScript engine in every native compiler unit-test job.
				found := false
				for _, line := range strings.Split(contents, "\n") {
					fields := strings.Fields(line)
					if len(fields) != 4 || fields[3] != "llgo_gc_noscan" {
						continue
					}
					start, startErr := strconv.ParseUint(fields[0], 16, 64)
					size, sizeErr := strconv.ParseUint(fields[2], 16, 64)
					if startErr != nil || sizeErr != nil || start%8 != 0 || size != uint64(wantSize) {
						t.Fatalf("invalid noscan boundaries: %s", line)
					}
					found = true
				}
				if !found {
					t.Fatalf("missing noscan output segment:\n%s", contents)
				}
			}
		})
	}
}
