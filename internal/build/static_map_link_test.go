//go:build !llgo

package build

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/cl"
)

func init() {
	if os.Getenv("LLGO_STATIC_MAP_ARGV0_HELPER") == "1" {
		fmt.Print(os.Args[0])
		os.Exit(0)
	}
}

func TestStaticMapInspectionPreservesDispatcherName(t *testing.T) {
	command := exec.Command(os.Args[0])
	command.Args[0] = "/logical/ld64.lld"
	command.Dir = t.TempDir()
	command.Env = append(os.Environ(), "LLGO_STATIC_MAP_ARGV0_HELPER=1")
	output, err := staticMapCommandOutput(command, nil, 4096)
	if err != nil || output != command.Args[0] {
		t.Fatalf("dispatcher name changed: %q %v", output, err)
	}
}

func TestStaticMapOmissionRequiresCurrentConsumerAndExactProducer(t *testing.T) {
	producer := staticMapProducerFixture("example.com/consumer")
	fact := cl.StaticMapInitializer{PackageIdentity: producer.PackageIdentity, PhysicalSymbol: producer.PhysicalSymbol,
		ConstructorID: producer.ConstructorID, GateSymbol: producer.GateSymbol, Entries: producer.Entries,
		GoUnread: true, ValueCellsDefined: true}
	for _, name := range []string{"unread", "read", "value-unknown", "missing-producer", "different-constructor", "different-owner", "raw-reference", "macho-reference", "gate-reference"} {
		t.Run(name, func(t *testing.T) {
			current := fact
			producers := map[string]cl.StaticMapInitProducer{producer.GateSymbol: producer}
			mentions := map[string]bool{}
			switch name {
			case "read":
				current.GoUnread = false
			case "value-unknown":
				current.ValueCellsDefined = false
			case "missing-producer":
				delete(producers, producer.GateSymbol)
			case "different-constructor":
				current.Entries++
			case "different-owner":
				current.PackageIdentity += "/other"
			case "raw-reference":
				mentions[current.PhysicalSymbol] = true
			case "macho-reference":
				mentions["_"+current.PhysicalSymbol] = true
			case "gate-reference":
				mentions["_"+current.GateSymbol] = true
			}
			got := staticMapOmitSymbols([]cl.StaticMapInitializer{current}, producers, mentions)
			if name == "unread" {
				if !slices.Equal(got, []string{producer.GateSymbol}) {
					t.Fatalf("eligible current producer: %v", got)
				}
			} else if len(got) != 0 {
				t.Fatal("unproved constructor acquired an omit symbol")
			}
		})
	}
}

func staticMapLDPlanFixture() ([]string, map[string]bool) {
	return []string{"-demangle", "-dynamic", "-arch", "arm64", "-platform_version", "macos", "28.0.0", "26.5",
			"-syslibroot", "/sdk", "-o", "/output", "-no_exported_symbols", "-L/libraries", "-lfoo", "/entry.o", "/application.a", "/compiler_rt.a"},
		map[string]bool{"/entry.o": true, "/application.a": true}
}

func TestStaticMapFinalLDPlanAndPinnedResolution(t *testing.T) {
	args, sources := staticMapLDPlanFixture()
	plan, err := staticMapParseLDPlan(args, sources)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(plan.probeArgs, "/entry.o") || slices.Contains(plan.probeArgs, "/application.a") ||
		!slices.Contains(plan.probeArgs, "/compiler_rt.a") || slices.Contains(plan.probeArgs, "/output") {
		t.Fatal("library-only inspection has the wrong inputs")
	}
	files := map[string]staticMapFileSnapshot{"/libraries/libfoo.dylib": {Path: "/libraries/libfoo.dylib", RealPath: "/exact/libfoo.1.dylib", SHA256: "content"}}
	pinned, err := staticMapPinLibraries(plan, files)
	if err != nil || slices.Contains(pinned, "-lfoo") || !slices.Contains(pinned, "/exact/libfoo.1.dylib") {
		t.Fatalf("unfrozen library search: %v %v", pinned, err)
	}
	if frozen, err := staticMapParseLDPlan(pinned, sources); err != nil || len(frozen.libraries) != 0 {
		t.Fatalf("pinned command still searches for libraries: %+v %v", frozen, err)
	}
	for _, name := range []string{"missing", "ambiguous"} {
		t.Run(name, func(t *testing.T) {
			other := map[string]staticMapFileSnapshot{}
			if name == "ambiguous" {
				other["/one/libfoo.dylib"] = staticMapFileSnapshot{RealPath: "/one/libfoo.dylib"}
				other["/two/libfoo.dylib"] = staticMapFileSnapshot{RealPath: "/two/libfoo.dylib"}
			}
			if _, err := staticMapPinLibraries(plan, other); err == nil {
				t.Fatal("unproved library resolution was accepted")
			}
		})
	}
	for _, name := range []string{"exports", "no-policy", "duplicate-policy", "foreign-arch", "foreign-platform", "missing-source", "duplicate-source", "response", "automatic-framework", "unknown-llvm-option"} {
		t.Run(name, func(t *testing.T) {
			changed := slices.Clone(args)
			switch name {
			case "exports":
				changed = append(changed, "-export_dynamic")
			case "no-policy":
				changed = slices.Delete(changed, 12, 13)
			case "duplicate-policy":
				changed = append(changed, "-no_exported_symbols")
			case "foreign-arch":
				changed[3] = "x86_64"
			case "foreign-platform":
				changed[5] = "ios"
			case "missing-source":
				changed = slices.Delete(changed, 15, 16)
			case "duplicate-source":
				changed = append(changed, "/entry.o")
			case "response":
				changed = append(changed, "@hidden.args")
			case "automatic-framework":
				changed = append(changed, "-framework", "Unknown")
			case "unknown-llvm-option":
				changed = append(changed, "-mllvm", "-unknown-option")
			}
			if _, err := staticMapParseLDPlan(changed, sources); err == nil {
				t.Fatal("unproved final link plan was accepted")
			}
		})
	}
}

func TestStaticMapDriverJobQuotedGrammar(t *testing.T) {
	want := []string{"/tools/ld64.lld", "/path with spaces/object.o", `literal$()\value`, ""}
	var quoted []string
	for _, value := range want {
		quoted = append(quoted, strconv.Quote(value))
	}
	got, err := staticMapParseQuotedJob(strings.Join(quoted, " "))
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("literal driver arguments: %q %v", got, err)
	}
	for _, text := range []string{`unquoted`, `"unterminated`, `"one""two"`, `"@response"`, `"\u0000"`, `"one" $(command)`} {
		if _, err := staticMapParseQuotedJob(text); err == nil {
			t.Fatalf("accepted unsupported job grammar: %s", text)
		}
	}
}

func TestStaticMapCertificateRejectsChangedFilesAndCommand(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "input.a")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := staticMapSnapshotFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"unchanged", "contents", "arguments", "environment", "directory", "tool"} {
		t.Run(name, func(t *testing.T) {
			command := exec.Command("/bin/echo", "original")
			command.Dir, command.Env = dir, []string{"BOUND=original"}
			certificate := staticMapLinkCertificate{commandSHA256: staticMapCommandDigest(command), files: []staticMapFileSnapshot{snapshot}}
			switch name {
			case "contents":
				if err := os.WriteFile(path, []byte("modified"), 0600); err != nil {
					t.Fatal(err)
				}
				defer os.WriteFile(path, []byte("original"), 0600)
			case "arguments":
				command.Args = append(command.Args, "extra")
			case "environment":
				command.Env[0] = "BOUND=changed"
			case "directory":
				command.Dir += "/other"
			case "tool":
				command.Path = "/other/tool"
			}
			if err := certificate.verify(command); (err == nil) != (name == "unchanged") {
				t.Fatalf("certificate outcome: %v", err)
			}
		})
	}
}

func TestStaticMapDynamicInputRejectsDisguisedNativeCode(t *testing.T) {
	object := staticMapNativeObjectFixture(t, "_foreign", true)
	if staticMapDynamicInput(object, ".dylib") || staticMapDynamicInput(object, ".tbd") {
		t.Fatal("native object code was mistaken for a dynamic library")
	}
	if !staticMapDynamicInput([]byte("--- !tapi-tbd\ntbd-version: 4\n"), ".tbd") ||
		!staticMapDynamicInput([]byte(`{"tapi_tbd_version":5,"main_library":{}}`), ".tbd") ||
		staticMapDynamicInput([]byte(`{"tapi_tbd_version":99,"main_library":{}}`), ".tbd") {
		t.Fatal("incorrect TextAPI input classification")
	}
}
