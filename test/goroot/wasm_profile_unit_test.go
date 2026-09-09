package goroot

import (
	"path/filepath"
	"reflect"
	"testing"
)

func withGOROOTWasmProfile(t *testing.T, name string) {
	t.Helper()
	old := *flagWasmProfile
	*flagWasmProfile = name
	t.Cleanup(func() { *flagWasmProfile = old })
}

func TestGOROOTWasmProfiles(t *testing.T) {
	want := map[string]struct{ target, goos, suffix, runner string }{
		"EC32":  {"emscripten", "js", ".mjs", "emscripten-runner.mjs"},
		"EC64":  {"emscripten-memory64", "js", ".mjs", "emscripten-memory64-runner.mjs"},
		"WC32":  {"wasi", "wasip1", ".wasm", "wasmtime"},
		"GJS":   {"", "js", ".mjs", "emscripten-runner.mjs"},
		"GWASI": {"", "wasip1", ".wasm", "go_wasip1_wasm_exec"},
	}
	for name, expected := range want {
		got, ok, err := selectGOROOTWasmProfile(name)
		if err != nil || !ok || got.target != expected.target || got.goos != expected.goos || got.llgoSuffix != expected.suffix || got.runner != expected.runner {
			t.Fatalf("%s: got %+v, %v, %v; want %+v", name, got, ok, err, expected)
		}
	}
	if _, ok, err := selectGOROOTWasmProfile(""); err != nil || ok {
		t.Fatalf("empty profile: ok=%v err=%v", ok, err)
	}
	if _, _, err := selectGOROOTWasmProfile("bad"); err == nil {
		t.Fatal("unknown profile accepted")
	}
}

func TestGOROOTWasmBuildAndRunCommands(t *testing.T) {
	withGOROOTWasmProfile(t, "EC64")
	env := []string{"GOROOT=/go", "LLGO_ROOT=/llgo", "GOOS=linux", "GOARCH=amd64"}
	if got := gorootArtifactPath("/tmp", "llgo", true); got != filepath.Join("/tmp", "llgo.mjs") {
		t.Fatal(got)
	}
	wantBuild := []string{"build", "-target", "emscripten-memory64", "-tags=x", "-o", "out.mjs", "."}
	if got := gorootBuildArgs(true, []string{"-tags=x"}, "out.mjs", "."); !reflect.DeepEqual(got, wantBuild) {
		t.Fatalf("build args: %v", got)
	}
	app, args, targetEnv, err := gorootArtifactCommand("/work", "out.mjs", true, env, "one")
	if err != nil || app != "node" || !reflect.DeepEqual(args, []string{filepath.Join("/llgo", "targets", "emscripten-memory64-runner.mjs"), "out.mjs", "one"}) {
		t.Fatalf("LLGo command: %q %v %v", app, args, err)
	}
	if envEntry(targetEnv, "GOOS") != "js" || envEntry(targetEnv, "GOARCH") != "wasm" || envEntry(targetEnv, "CGO_ENABLED") != "0" || envEntry(targetEnv, "GOMAXPROCS") != "1" {
		t.Fatalf("target env: %v", targetEnv)
	}
	app, args, _, err = gorootArtifactCommand("/work", "go.wasm", false, env, "two")
	if err != nil || app != filepath.Join("/go", "lib", "wasm", "go_js_wasm_exec") || !reflect.DeepEqual(args, []string{"go.wasm", "two"}) {
		t.Fatalf("Go command: %q %v %v", app, args, err)
	}
}

func TestGOROOTWasiRunCommand(t *testing.T) {
	withGOROOTWasmProfile(t, "GWASI")
	env := []string{"GOROOT=/go", "LLGO_ROOT=/llgo"}
	app, args, targetEnv, err := gorootArtifactCommand("/work", "out.wasm", true, env, "arg")
	want := []string{"out.wasm", "arg"}
	if err != nil || app != filepath.Join("/go", "lib", "wasm", "go_wasip1_wasm_exec") || !reflect.DeepEqual(args, want) || envEntry(targetEnv, "GOWASIRUNTIME") != "wasmtime" || envEntry(targetEnv, "GOWASIRUNTIMEARGS") != "-W exceptions=y -W multi-memory=y" {
		t.Fatalf("WASI command: %q %v %v %v", app, args, targetEnv, err)
	}
}

func TestGOROOTWasiCRunCommand(t *testing.T) {
	withGOROOTWasmProfile(t, "WC32")
	env := []string{"GOROOT=/go", "LLGO_ROOT=/llgo", "PATH=/bin", "PWD=/work"}
	app, args, targetEnv, err := gorootArtifactCommand("/work", "out.wasm", true, env, "arg")
	want := []string{"run", "--dir=/", "--env", "PWD", "--env", "PATH", "-W", "exceptions=y", "-W", "max-wasm-stack=8388608", "out.wasm", "arg"}
	if err != nil || app != "wasmtime" || !reflect.DeepEqual(args, want) {
		t.Fatalf("WASI C command: %q %v %v %v", app, args, targetEnv, err)
	}
	if envEntry(targetEnv, "PATH") != "/bin" || envEntry(targetEnv, "PWD") != "/work" || envEntry(targetEnv, "GOMAXPROCS") != "1" {
		t.Fatalf("WASI C environment: %v", targetEnv)
	}
}

func TestGOROOTWasmRuntimeEnvironmentExcludesHostControls(t *testing.T) {
	base := []string{"PATH=/bin", "GOROOT=/go", "GOGC=1", "GODEBUG=checkptr=1", "USER_CASE=kept",
		"ACTIONS_RUNTIME_TOKEN=host-only", "GITHUB_ENV=/host/environment", "RUNNER_TEMP=/host/tmp",
		"LLGO_DIAG_EVIDENCE=/diagnostics", "LLGO_R4_LLVM_TRACE=/bitcode", "GOMAXPROCS=2"}
	before := append([]string(nil), base...)
	for _, profile := range []string{"EC32", "EC64", "WC32", "GJS", "GWASI", ""} {
		t.Run(profile, func(t *testing.T) {
			withGOROOTWasmProfile(t, profile)
			buildEnv := gorootTargetEnv(base)
			runEnv := gorootRuntimeEnv(base)
			if !reflect.DeepEqual(base, before) {
				t.Fatal("runtime environment filtering mutated the caller's environment")
			}
			if profile == "" {
				if !reflect.DeepEqual(runEnv, base) {
					t.Fatal("native test environment changed")
				}
				return
			}
			for _, key := range []string{"ACTIONS_RUNTIME_TOKEN", "GITHUB_ENV", "RUNNER_TEMP", "LLGO_DIAG_EVIDENCE", "LLGO_R4_LLVM_TRACE"} {
				if envEntry(runEnv, key) != "" || envEntry(buildEnv, key) == "" {
					t.Fatalf("%s must remain available only to the host build", key)
				}
			}
			for _, key := range []string{"PATH", "GOROOT", "GOGC", "GODEBUG", "USER_CASE"} {
				if envEntry(runEnv, key) != envEntry(base, key) {
					t.Fatalf("lost program environment variable %s", key)
				}
			}
			if envEntry(runEnv, "GOMAXPROCS") != "1" {
				t.Fatal("lost the official single-worker contract")
			}
		})
	}
}
