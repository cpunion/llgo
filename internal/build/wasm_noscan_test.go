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

func TestWasmFuncInfoNoScanLinking(t *testing.T) {
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Fatalf("clang is required: %v", err)
	}
	linker, err := exec.LookPath("wasm-ld")
	if err != nil {
		t.Fatalf("wasm-ld is required: %v", err)
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
			for _, live := range []bool{false, true} {
				name := "empty"
				if live {
					name = "one-live"
				}
				linkMap := filepath.Join(dir, name+".map")
				wasm := filepath.Join(dir, name+".wasm")
				args := []string{"--no-entry", "--gc-sections", "--export=llgo_gc_noscan_start", "--export=llgo_gc_noscan_end", "--Map=" + linkMap, "-o", wasm, object, sentinel}
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
