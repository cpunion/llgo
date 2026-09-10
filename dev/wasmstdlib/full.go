package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Full auditing deliberately has no compatibility allowlist. Source exclusions
// and failures remain unresolved until reviewed individually; independently
// executable host-side suites are named explicitly in the report.
type fullPackage struct {
	Package    string          `json:"package"`
	Status     string          `json:"status"`
	Reason     string          `json:"reason,omitempty"`
	Tests      int             `json:"passed_top_level_tests"`
	HostChecks []fullHostCheck `json:"host_checks,omitempty"`
}

type fullHostCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type selectedPackage struct {
	Dir                       string
	TestGoFiles, XTestGoFiles []string
	Error                     *struct{ Err string }
}

const wasmTimerStressPackage = "test/_stress/runtime/timer"

func discoverFull(root string) ([]string, error) {
	seen := map[string]bool{}
	err := filepath.WalkDir(filepath.Join(root, "test"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" || d.Name() == "vendor" || d.Name() == "_manualtest" || strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), "_test.go") {
			rel, err := filepath.Rel(root, filepath.Dir(path))
			if err != nil {
				return err
			}
			seen[filepath.ToSlash(rel)] = true
		}
		return nil
	})
	var names []string
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, err
}

func fullProfile(name string) (profile, error) {
	switch name {
	case "GJS":
		return profile{Name: name, GOOS: "js"}, nil
	case "GWASI":
		return profile{Name: name, GOOS: "wasip1"}, nil
	default:
		return selectProfile(name)
	}
}

func fullCommand(p profile, goCmd, llgo, goRoot, pkg string) command {
	args := []string{"test", "-v", "-count=1", "-timeout=" + fullTestTimeout(pkg)}
	env := map[string]string{}
	program := llgo
	if p.Reference {
		program = goCmd
		args = append(args, "-exec="+strconv.Quote(filepath.Join(goRoot, "lib", "wasm", "go_"+p.GOOS+"_wasm_exec")))
		env["GOWASIRUNTIME"] = "wasmtime"
		// Match the official Go wasm execution contract. The current js/wasm
		// and wasip1/wasm runtimes do not create operating-system threads, so
		// inheriting a wider CI GOMAXPROCS makes the reference runtime call
		// newosproc and abort before package initialization.
		env["GOMAXPROCS"] = "1"
	}
	if p.Target != "" {
		args = append(args, "-target", p.Target, "-emulator")
	} else {
		env["GOOS"], env["GOARCH"], env["CGO_ENABLED"] = p.GOOS, "wasm", "0"
	}
	if !p.Reference && p.GOOS == "js" {
		// Catch fixed-size Fiber exhaustion at the offending frame, before it
		// corrupts another heap allocation and surfaces as an unrelated failure.
		// Exercise LLGo's external-linker flag forwarding at the same time.
		args = append(args, "-ldflags=-extldflags=-sSTACK_OVERFLOW_CHECK=2")
	}
	if !p.Reference && !fullNeedsPCLN(pkg) {
		// Embedded symbolization requires a second final link after function
		// addresses are known. Most packages test unrelated language/library
		// behavior; keep that costly path in the suites that inspect callers,
		// tracebacks, profiling, or runtime function metadata.
		args = append(args, "-pclntab=none")
	}
	packageArg := "./" + pkg
	if pkg == wasmTimerStressPackage {
		// The Go command excludes underscore directories from package patterns,
		// but it accepts this reviewed stress package as an explicit file.
		packageArg += "/timer_stress_test.go"
	}
	args = append(args, packageArg)
	// Keep the LLGo package cache local to this job. Repeated stdlib builds can
	// reuse compilation while every test binary still executes with count=1.
	if !p.Reference {
		env["LLGO_BUILD_CACHE"] = "on"
	}
	if strings.HasPrefix(pkg, "test/_stress/") {
		env["LLGO_STRESS_PROFILE"] = "quick"
	}
	// GNU timeout bounds compilation as well as execution, including children.
	return command{"timeout", append([]string{"--kill-after=10s", "5m", program}, args...), env}
}

func fullNeedsPCLN(pkg string) bool {
	for _, prefix := range []string{
		"test/go",
		"test/llgoext",
		"test/std/runtime",
		"test/std/net/http/pprof",
		"test/std/log/slog",
	} {
		if pkg == prefix || strings.HasPrefix(pkg, prefix+"/") {
			return true
		}
	}
	return pkg == "test"
}

func fullTestTimeout(pkg string) string {
	// Keep the global default strict while allowing reviewed, finite wasm work
	// enough time to finish. These packages either generate cryptographic keys,
	// walk a broad API surface, or collect a complete heap profile.
	switch pkg {
	case "test/std/crypto/dsa", "test/std/crypto/rsa", "test/std/go/types", "test/std/os", "test/std/runtime/pprof":
		return "3m"
	}
	if strings.HasPrefix(pkg, "test/_stress/") {
		return "3m"
	}
	return "60s"
}

func fullSourceContext(p profile) (tags string, cgo string) {
	if p.Reference {
		return "", "0"
	}
	tags = "llgo,osusergo"
	if p.Target == "" {
		return tags + ",llgo.wasm.gc.linear", "0"
	}
	tags += ",llgo.wasm.gc.linear"
	switch p.Target {
	case "emscripten":
		tags += ",llgo.wasm.emscripten"
	case "emscripten-memory64":
		tags += ",llgo.wasm.emscripten,llgo.wasm.emscripten.memory64"
	case "wasi":
		tags += ",llgo.wasm.wasi"
	}
	return tags, "1"
}

// fullSourceExclusion classifies packages whose own build constraints define
// them outside every wasm source context. Keep this list narrow and explicit:
// an unrecognized source exclusion remains a failing acceptance result.
func fullSourceExclusion(p profile, pkg string) (string, bool) {
	switch pkg {
	case "test/_stress/runtime/cpuprof":
		return "SIGPROF stress requires a native Darwin or Linux process", true
	case "test/_stress/runtime/finalizer":
		return "BDWGC finalizer stress is native-only; linear-GC finalizers are covered by target runtime tests", true
	case "test/_stress/runtime/signal":
		return "POSIX signal stress is excluded by its native-only build contract", true
	case "test/std/plugin":
		return "tests select only darwin, linux, or windows; wasm has no dynamic plugin loader", true
	case "test/std/syscall":
		return "tests select Unix or Windows syscall surfaces, neither of which is the js/wasm or wasip1/wasm ABI", true
	case "test/windows":
		return "Windows-only integration suite", true
	case "test/cgo":
		return "the Go cgo frontend does not support GOARCH=wasm; Emscripten and WASI C interoperability is covered by dedicated LLGo target tests", true
	case "test/std/runtime/cgo":
		return "the standard runtime/cgo package requires the unsupported GOARCH=wasm cgo frontend; LLGo handle and host-boundary behavior is covered by dedicated target tests", true
	case "test/llgoext", "test/llgoext/localitymulti":
		if p.Reference {
			return "LLGo extension tests require the llgo build tag and LLGo-only runtime APIs; all LLGo profiles still execute them", true
		}
	}
	return "", false
}

func testWitness(pkg selectedPackage) (string, error) {
	for _, file := range append(append([]string{}, pkg.TestGoFiles...), pkg.XTestGoFiles...) {
		f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(pkg.Dir, file), nil, 0)
		if err != nil {
			return "", err
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Recv == nil && strings.HasPrefix(fn.Name.Name, "Test") && fn.Name.Name != "TestMain" && fn.Type.Params.NumFields() == 1 {
				return fn.Name.Name, nil
			}
		}
	}
	return "", errors.New("no top-level test witness; examples/benchmarks require separate accounting")
}

func runFull(name, reportPath, goCmd, llgo string, shard, shards int) error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	return runFullAt(root, name, reportPath, goCmd, llgo, shard, shards, executeStructured, execute)
}

func runFullAt(root, name, reportPath, goCmd, llgo string, shard, shards int, structured, run func(string, command) ([]byte, error)) error {
	if reportPath == "" || shards < 1 || shard < 0 || shard >= shards {
		return errors.New("full audit requires report and valid shard/shards")
	}
	p, err := fullProfile(name)
	if err != nil {
		return err
	}
	names, err := discoverFull(root)
	if err != nil {
		return err
	}
	entries := make([]fullPackage, len(names))
	for i, pkg := range names {
		entries[i] = fullPackage{Package: pkg, Status: "not-run"}
		if i%shards != shard {
			entries[i].Status = "other-shard"
		}
	}
	result := "incomplete"
	save := func() error {
		data, err := json.MarshalIndent(struct {
			Profile  string        `json:"profile"`
			Result   string        `json:"result"`
			Shard    int           `json:"shard"`
			Shards   int           `json:"shards"`
			Packages []fullPackage `json:"packages"`
		}{name, result, shard, shards, entries}, "", "  ")
		if err != nil {
			return err
		}
		return os.WriteFile(reportPath, append(data, '\n'), 0644)
	}
	if err := save(); err != nil {
		return err
	}
	data, err := structured(root, command{goCmd, []string{"env", "GOROOT"}, nil})
	if err != nil {
		return err
	}
	goRoot := strings.TrimSpace(string(data))
	listArgs := []string{"list", "-e", "-json"}
	tags, cgo := fullSourceContext(p)
	if tags != "" {
		listArgs = append(listArgs, "-tags="+tags)
	}
	// Go package patterns intentionally ignore directories beginning with an
	// underscore. The reviewed wasm timer stress package is selected explicitly
	// below so it cannot disappear from the acceptance inventory.
	listArgs = append(listArgs, "./test/...")
	data, err = structured(root, command{goCmd, listArgs, map[string]string{"GOOS": p.GOOS, "GOARCH": "wasm", "CGO_ENABLED": cgo}})
	if err != nil {
		return err
	}
	selected := map[string]selectedPackage{}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	for {
		var pkg selectedPackage
		if err := decoder.Decode(&pkg); err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, pkg.Dir)
		if err != nil {
			return err
		}
		selected[filepath.ToSlash(rel)] = pkg
	}
	stressDir := filepath.Join(root, filepath.FromSlash(wasmTimerStressPackage))
	selected[wasmTimerStressPackage] = selectedPackage{
		Dir:         stressDir,
		TestGoFiles: []string{"timer_stress_test.go"},
	}
	if err := os.MkdirAll(reportPath+".logs", 0755); err != nil {
		return err
	}
	failures := 0
	for i := range entries {
		e := &entries[i]
		if e.Status == "other-shard" {
			continue
		}
		if reason, classified := fullSourceExclusion(p, e.Package); classified {
			e.Status, e.Reason = "not-applicable", reason
			continue
		}
		pkg, exists := selected[e.Package]
		switch {
		case e.Package == "test/goroot":
			e.Status, e.Reason = "separate-suite", "host-side target runner is executed by the wasm GOROOT acceptance jobs"
		case e.Package == "test/cmd/llgo":
			e.Status, e.Reason = "separate-suite", "compiler-driver subprocess tests execute in the regular host CI suite"
		case !exists || len(pkg.TestGoFiles)+len(pkg.XTestGoFiles) == 0:
			e.Status, e.Reason = "source-excluded", "no tests selected by source context; applicability not yet established"
		case pkg.Error != nil:
			e.Status, e.Reason = "fail", pkg.Error.Err
		default:
			witness, err := testWitness(pkg)
			if err != nil {
				e.Status, e.Reason = "unresolved", err.Error()
				break
			}
			fmt.Printf("%s %s\n", name, e.Package)
			cmd := fullCommand(p, goCmd, llgo, goRoot, e.Package)
			var hostArtifact string
			if e.Package == "test/go" || e.Package == "test" {
				dir, err := os.MkdirTemp("", "llgo-wasm-test-go-")
				if err != nil {
					return err
				}
				defer os.RemoveAll(dir)
				hostArtifact = filepath.Join(dir, strings.ReplaceAll(e.Package, "/", "-")+".wasm")
				if p.GOOS == "js" && !p.Reference {
					hostArtifact = filepath.Join(dir, strings.ReplaceAll(e.Package, "/", "-")+".mjs")
				}
				cmd.Args = append(cmd.Args[:len(cmd.Args)-1], "-o", hostArtifact, cmd.Args[len(cmd.Args)-1])
			}
			out, runErr := run(root, cmd)
			if err := os.WriteFile(filepath.Join(reportPath+".logs", strings.ReplaceAll(e.Package, "/", "_")+".log"), out, 0644); err != nil {
				return err
			}
			if runErr == nil {
				e.Tests, runErr = validateOutput(out, witness)
			}
			if e.Package == "test/go" && hostArtifact != "" {
				childOut, childErr := run(root, fullPanicCommand(p, root, goRoot, hostArtifact))
				if err := os.WriteFile(filepath.Join(reportPath+".logs", "test_go_panic_child.log"), childOut, 0644); err != nil {
					return err
				}
				check := fullHostCheck{Name: "unrecovered init panic traceback", Status: "pass"}
				if err := validateFullPanic(root, childOut, childErr); err != nil {
					check.Status, check.Reason = "fail", err.Error()
					runErr = errors.Join(runErr, err)
					writeFullFailureOutput(os.Stdout, name, e.Package+" panic child", childOut)
				}
				e.HostChecks = append(e.HostChecks, check)
				for _, name := range fullFinalizerInvalidCases {
					childOut, childErr = run(root, fullFinalizerInvalidCommand(p, root, goRoot, hostArtifact, name))
					logName := "test_go_finalizer_invalid_" + strings.ReplaceAll(name, " ", "_") + ".log"
					if err := os.WriteFile(filepath.Join(reportPath+".logs", logName), childOut, 0644); err != nil {
						return err
					}
					check = fullHostCheck{Name: "SetFinalizer rejects " + name, Status: "pass"}
					if err := validateFullFinalizerInvalid(childOut, childErr, name); err != nil {
						check.Status, check.Reason = "fail", err.Error()
						runErr = errors.Join(runErr, err)
						writeFullFailureOutput(os.Stdout, name, e.Package+" finalizer child", childOut)
					}
					e.HostChecks = append(e.HostChecks, check)
				}
			}
			if e.Package == "test" && hostArtifact != "" {
				childOut, childErr := run(root, fullBuiltinPrintCommand(p, root, goRoot, hostArtifact))
				if err := os.WriteFile(filepath.Join(reportPath+".logs", "test_builtin_print_child.log"), childOut, 0644); err != nil {
					return err
				}
				check := fullHostCheck{Name: "Go 1.26 builtin print formatting", Status: "pass"}
				if err := validateFullBuiltinPrint(childOut, childErr); err != nil {
					check.Status, check.Reason = "fail", err.Error()
					runErr = errors.Join(runErr, err)
					writeFullFailureOutput(os.Stdout, name, e.Package+" builtin print child", childOut)
				}
				e.HostChecks = append(e.HostChecks, check)
			}
			e.Status = "pass"
			if runErr != nil {
				e.Status, e.Reason = "fail", runErr.Error()
				writeFullFailureOutput(os.Stdout, name, e.Package, out)
			}
		}
		if e.Status != "pass" && e.Status != "separate-suite" && e.Status != "not-applicable" {
			failures++
		}
		fmt.Printf("%s %s: %s %s\n", name, e.Package, e.Status, e.Reason)
		if err := save(); err != nil {
			return err
		}
	}
	result = "pass"
	if failures != 0 {
		result = "fail"
	}
	if err := save(); err != nil {
		return err
	}
	if failures != 0 {
		return fmt.Errorf("%s shard %d/%d: %d unresolved/failed packages", name, shard, shards, failures)
	}
	return nil
}

// Show a failing package's diagnostics before the rest of a long shard finishes.
// Keep the original artifact intact, and prefix streamed lines so test output
// cannot be interpreted as GitHub Actions workflow commands.
func writeFullFailureOutput(w io.Writer, profile, pkg string, out []byte) {
	if len(out) == 0 {
		return
	}
	fmt.Fprintf(w, "\n--- %s %s failure output ---\n", profile, pkg)
	for _, line := range strings.Split(strings.TrimSuffix(string(out), "\n"), "\n") {
		fmt.Fprintf(w, "| %s\n", line)
	}
	fmt.Fprintln(w, "--- end failure output ---")
}
