//go:build !llgo

package build

import (
	"bytes"
	stdcontext "context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/xgo-dev/llvm"
)

func TestCoroReflectResourceAcceptance(t *testing.T) {
	if os.Getenv("LLGO_CORO_RESOURCE_ACCEPTANCE") != "1" {
		t.Skip("set LLGO_CORO_RESOURCE_ACCEPTANCE=1 for physical reflected resource acceptance")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("reflected resources require POSIX descriptors")
	}
	compiler := os.Getenv("LLGO_TEST_LLGO")
	if compiler == "" {
		t.Fatal("LLGO_TEST_LLGO must identify the compiler being accepted")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	image := coroResourceAcceptanceImage(t, "coro_reflect_delivery")
	ctx, cancel := stdcontext.WithTimeout(stdcontext.Background(), 10*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, compiler, "build", "-p=1", "-o", image, "./_testgo/coro_reflect_delivery")
	build.Dir = filepath.Join(root, "runtime")
	build.Env = append(os.Environ(), "LLGO_ROOT="+root, "LLGO_BUILD_CACHE=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("reflected resource build: %v\n%s", err, output)
	}
	for _, mode := range []int{12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 26, 27, 28, 29, 31, 32, 33} {
		testCoroReflectResourceRuns(t, image, mode, false)
	}
}

// Delay after the real managed native invocation and State transfer, before
// reflect boxes results. The hook exists only in this invocation's source
// overlay. The negative control changes only the transfer, retaining every
// acquisition, initializer, guard and cleanup invocation.
func TestCoroReflectPostNativeResourceAcceptance(t *testing.T) {
	if os.Getenv("LLGO_CORO_RESOURCE_ACCEPTANCE") != "1" {
		t.Skip("set LLGO_CORO_RESOURCE_ACCEPTANCE=1 for post-native reflection ownership acceptance")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("reflected resources require POSIX descriptors")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLGO_ROOT", root)
	t.Setenv(llgoBuildCache, "off")
	read := func(relative string) []byte {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(root, relative))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	valueFile := filepath.Join(root, "runtime/internal/lib/reflect/value.go")
	callFile := filepath.Join(root, "runtime/internal/lib/reflect/call_delivery.go")
	ffiFile := filepath.Join(root, "runtime/internal/ffi/delivery.go")
	value := read("runtime/internal/lib/reflect/value.go")
	needle := []byte("state.native.FinishInvocation()")
	if bytes.Count(value, needle) != 1 {
		t.Fatal("post-native insertion must name the exact real invocation")
	}
	value = bytes.Replace(value, needle, []byte("state.native.FinishInvocation(); postNativeDeliveryTestWait()"), 1)
	call := append(read("runtime/internal/lib/reflect/call_delivery.go"), []byte("\n//go:linkname postNativeDeliveryTestWait C.llgo_reflect_post_native_wait\nfunc postNativeDeliveryTestWait()\n")...)
	for _, negative := range []bool{false, true} {
		name := "post-native"
		if negative {
			name = "post-native-without-state-transfer"
		}
		t.Run(name, func(t *testing.T) {
			conf := NewDefaultConf(ModeBuild)
			conf.BuildParallelism = 1
			conf.OutFile = coroResourceAcceptanceImage(t, "coro_reflect_"+name)
			conf.Overlay = map[string][]byte{valueFile: value, callFile: call}
			if negative {
				body := read("runtime/internal/ffi/delivery.go")
				if bytes.Count(body, []byte("s.nativeState = state")) != 1 {
					t.Fatal("negative control must omit exactly one transfer")
				}
				conf.Overlay[ffiFile] = bytes.Replace(body, []byte("s.nativeState = state"), []byte("s.nativeState = nil"), 1)
			}
			if _, err := Build(Invocation{Args: []string{"./_testgo/coro_reflect_delivery"}, Config: conf, Dir: filepath.Join(root, "runtime")}); err != nil {
				t.Fatal(err)
			}
			modes := []int{24, 25, 30}
			if negative {
				modes = []int{25, 30}
			}
			for _, mode := range modes {
				testCoroReflectResourceRuns(t, conf.OutFile, mode, negative)
			}
		})
	}
}

func testCoroReflectResourceRuns(t *testing.T, image string, mode int, negative bool) {
	t.Helper()
	for _, owners := range []string{"1", "2", "4"} {
		for attempt := 0; attempt < 3; attempt++ {
			ctx, cancel := stdcontext.WithTimeout(stdcontext.Background(), 30*time.Second)
			run := exec.CommandContext(ctx, image)
			run.Env = append(os.Environ(), "GOMAXPROCS="+owners, fmt.Sprintf("LLGO_DELIVERY_FIXTURE_MODE=%d", mode))
			output, err := run.CombinedOutput()
			cancel()
			if negative {
				exit, ok := err.(*exec.ExitError)
				if !ok || exit.ExitCode() != 86 || !bytes.Contains(output, []byte("open=3 close=2 waiting=1 close_waiting=0 released=1")) {
					t.Fatalf("missing-state control mode=%d P=%s run=%d: %v\n%s", mode, owners, attempt, err, output)
				}
			} else if err != nil || strings.TrimSpace(string(output)) != "typed resource delivery normal, discarded and canceled passed" {
				t.Fatalf("reflected resource mode=%d P=%s run=%d: %v\n%s", mode, owners, attempt, err, output)
			}
		}
	}
}

// Both the producer and the consumer use the production frontend and runtime.
// The second invocation sees only public declarations, an actual native
// archive, and that archive's effect records. No private helper body survives.
func TestCoroReflectArchiveResourceAcceptance(t *testing.T) {
	if os.Getenv("LLGO_CORO_RESOURCE_ACCEPTANCE") != "1" {
		t.Skip("set LLGO_CORO_RESOURCE_ACCEPTANCE=1 for linked reflection/archive ownership acceptance")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("reflection/archive fixture requires POSIX descriptors")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.MkdirTemp(filepath.Join(root, "runtime"), ".coro-reflect-archive-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(source) })
	write := func(path, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	libraryDir := filepath.Join(source, "library")
	libraryPath := "github.com/xgo-dev/llgo/runtime/" + filepath.Base(source) + "/library"
	declarations := `type Receiver struct { Token uint32 }
type Payload struct { Bytes [131073]byte; Keep *uint32; Check func() uint32 }
`
	producer := "package library\nimport \"unsafe\"\n" + declarations + `
type result = struct { R0 int32 }
//go:linkname borrow llgo.coroBorrowResult
func borrow(func(*result), func(*result, unsafe.Pointer, uint32, uint32)) *result
//go:linkname open C.llgo_delivery_open
func open(*result, uint32)
//go:linkname closeFD C.llgo_delivery_close
func closeFD(int32)
//go:linkname escaped C.llgo_delivery_escaped
func escaped()
func initialize(out *result) { out.R0 = -1 }
func drop(out *result, _ unsafe.Pointer, token, _ uint32) {
 if token != 17 { escaped() }; fd := out.R0; out.R0 = -1; if fd >= 0 { closeFD(fd) }
}
func acquire(token, mode uint32) int32 {
 out := borrow(initialize, drop); open(out, mode); return out.R0
}
func (r Receiver) Acquire(mode uint32) int32 { return acquire(r.Token, mode) }
func Small() func(*Receiver, uint32) int32 { return (*Receiver).Acquire }
type largeResult = struct { R0 int32; R1 Payload }
//go:linkname borrowLarge llgo.coroBorrowResult
func borrowLarge(func(*largeResult), func(*largeResult, unsafe.Pointer, uint32, uint32)) *largeResult
//go:linkname openLarge C.llgo_delivery_open_large
func openLarge(*largeResult, uint32)
func initializeLarge(out *largeResult) { out.R0 = -1 }
func dropLarge(out *largeResult, _ unsafe.Pointer, token, _ uint32) {
 fd := out.R0; out.R0 = -1
 if fd >= 0 {
  if token != 17 || out.R1.Keep == nil || *out.R1.Keep != 37 || out.R1.Check() != 41 || out.R1.Bytes[131072] != 43 { escaped() }
  closeFD(fd)
 }
}
func acquireLarge(token, mode uint32) (int32, Payload) {
 out := borrowLarge(initializeLarge, dropLarge)
 keep := new(uint32); *keep = 37; out.R1.Keep = keep
 out.R1.Check = func() uint32 { return *keep + 4 }; out.R1.Bytes[131072] = 43
 openLarge(out, mode); return out.R0, out.R1
}
func (r Receiver) AcquireLarge(mode uint32) (int32, Payload) { return acquireLarge(r.Token, mode) }
func Large() func(*Receiver, uint32) (int32, Payload) { return (*Receiver).AcquireLarge }
`
	write(filepath.Join(libraryDir, "library.go"), producer)
	t.Setenv("LLGO_ROOT", root)
	t.Setenv(llgoBuildCache, "off")
	conf := NewDefaultConf(ModeGen)
	conf.BuildParallelism = 1
	packages, err := Build(Invocation{Args: []string{"."}, Config: conf, Dir: libraryDir})
	if err != nil {
		t.Fatal("independent producer: ", err)
	}
	if len(packages) != 1 || packages[0].LPkg == nil || len(packages[0].CoroLibraryEffectRecords) == 0 {
		t.Fatal("producer has no module/effect records")
	}
	pkg := packages[0]
	prog := pkg.LPkg.Prog
	defer prog.Dispose()
	module := pkg.LPkg.Module()
	runCoroSpawnNativeE2EPasses(t, prog, module)
	object, err := prog.TargetMachine().EmitToMemoryBuffer(module, llvm.ObjectFile)
	if err != nil {
		t.Fatal(err)
	}
	defer object.Dispose()
	archive := filepath.Join(source, "libresource.a")
	ctx := &context{buildConf: conf, prog: prog}
	if err := ctx.createPackageArchiveFile(archive, &aPackage{ObjBuffers: []packageArchiveBuffer{{name: "resource.o", buffer: object}}, CoroLibraryEffectRecords: pkg.CoroLibraryEffectRecords}, false); err != nil {
		t.Fatal(err)
	}
	importcfg := filepath.Join(source, "importcfg")
	write(importcfg, "packagefile "+libraryPath+"="+archive+"\n")
	write(filepath.Join(libraryDir, "library.go"), "package library\n"+declarations+`
const LLGoPackage = "decl"
func Small() func(*Receiver, uint32) int32
func Large() func(*Receiver, uint32) (int32, Payload)
`)
	// An assembly declaration unit makes bodyless public Go declarations
	// valid to cmd/go too. PkgDeclOnly emits neither Go bodies nor assembly.
	write(filepath.Join(libraryDir, "declarations.s"), "// Declarations are implemented by the independently compiled resource.a.\n")
	// LLGoFiles compiles source units; supply a native archive through the
	// ordinary external-link package contract instead. Force extraction also
	// keeps this test independent of a host linker's archive rescan policy.
	linkArgs := "-Wl,--whole-archive," + archive + ",--no-whole-archive"
	if runtime.GOOS == "darwin" {
		linkArgs = "-Wl,-force_load," + archive
	}
	write(filepath.Join(source, "native", "native.go"), fmt.Sprintf("package native\nconst LLGoPackage = %q\n", "link: "+linkArgs))
	consumerDir := filepath.Join(source, "consumer")
	fdSource, err := os.ReadFile(filepath.Join(root, "runtime/_testgo/coro_resource_delivery_slot/wrap/delivery.c"))
	if err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(source, "delivery.c"), string(fdSource))
	for _, entry := range []string{"Small", "Large"} {
		t.Run(entry, func(t *testing.T) {
			write(filepath.Join(consumerDir, "main.go"), fmt.Sprintf(`package main
import (
 "reflect"
 "runtime"
 "unsafe"
 library %q
 _ %q
)
const LLGoFiles = "../delivery.c"
//go:linkname install C.llgo_delivery_install
func install()
//go:linkname checkAlive C.llgo_delivery_check_alive
func checkAlive(int32)
//go:linkname closeFD C.llgo_delivery_close
func closeFD(int32)
//go:linkname blocked C.llgo_delivery_blocked
func blocked() int32
//go:linkname release C.llgo_delivery_release
func release()
//go:linkname escaped C.llgo_delivery_escaped
func escaped()
var _ unsafe.Pointer
var receiver = &library.Receiver{Token: 17}
var function = reflect.ValueOf(library.%s())
func invoke(mode uint32) int32 {
 values := function.Call([]reflect.Value{reflect.ValueOf(receiver), reflect.ValueOf(mode)})
 if len(values) == 2 {
  data := values[1].Interface().(library.Payload); runtime.GC()
  if data.Keep == nil || *data.Keep != 37 || data.Check() != 41 || data.Bytes[131072] != 43 { escaped() }
 }
 return int32(values[0].Int())
}
func sentinel(ready chan struct{}) { defer release(); close(ready); var never chan struct{}; <-never }
func owner() { invoke(1); escaped() }
func main() {
 install()
 for i := 0; i < 2; i++ { fd := invoke(0); runtime.GC(); checkAlive(fd); closeFD(fd) }
 ready := make(chan struct{}); go sentinel(ready); <-ready
 go owner(); for blocked() == 0 { runtime.Gosched() }
 receiver.Token = 99; function = reflect.Value{}; runtime.GC(); runtime.GC()
}
`, libraryPath, "github.com/xgo-dev/llgo/runtime/"+filepath.Base(source)+"/native", entry))
			image := coroResourceAcceptanceImage(t, "coro_reflect_archive_"+strings.ToLower(entry))
			if os.Getenv("LLGO_CORO_RESOURCE_OUTPUT") != "" {
				for name, path := range map[string]string{"libresource.a": archive, "consumer.go": filepath.Join(consumerDir, "main.go"), "declarations.go": filepath.Join(libraryDir, "library.go"), "importcfg": importcfg} {
					data, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(filepath.Dir(image), name), data, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				write(filepath.Join(filepath.Dir(image), "producer.go"), producer)
			}
			conf := NewDefaultConf(ModeBuild)
			conf.BuildParallelism, conf.ImportCfg, conf.OutFile = 1, importcfg, image
			if _, err := Build(Invocation{Args: []string{"."}, Config: conf, Dir: consumerDir}); err != nil {
				t.Fatal("archive-only consumer: ", err)
			}
			for _, owners := range []string{"1", "2", "4"} {
				for attempt := 0; attempt < 3; attempt++ {
					runCtx, cancel := stdcontext.WithTimeout(stdcontext.Background(), 30*time.Second)
					run := exec.CommandContext(runCtx, image)
					run.Env = append(os.Environ(), "GOMAXPROCS="+owners, "LLGO_DELIVERY_FIXTURE_MODE=12")
					output, err := run.CombinedOutput()
					cancel()
					if err != nil || strings.TrimSpace(string(output)) != "typed resource delivery normal, discarded and canceled passed" {
						t.Fatalf("archive reflection P=%s run=%d: %v\n%s", owners, attempt, err, output)
					}
				}
			}
		})
	}
}
