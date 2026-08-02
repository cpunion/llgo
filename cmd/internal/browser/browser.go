// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package browser launches LLGo WebAssembly debug sessions in Chromium.
package browser

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/goplus/llgo/internal/browserdebug"
	"github.com/goplus/llgo/internal/debugabi"
	"github.com/goplus/llgo/internal/env"
	"github.com/goplus/llgo/internal/wasmdebug"
)

const MinimumChromeMajor = 123

//go:embed extension/manifest.json
var extensionManifest []byte

//go:embed extension/devtools.html
var extensionPage []byte

//go:embed extension/plugin.js
var extensionPlugin []byte

var chromeVersionPattern = regexp.MustCompile(`(?:Chrome(?: for Testing| Canary)?|Chromium)\s+(\d+)\.`)

type Options struct {
	Chrome       string
	ChromeArgs   []string
	SourceMaps   []browserdebug.PathMapping
	KeepProfile  bool
	ProfilePath  string
	DisableTools bool
}

// Run validates artifact, starts a loopback-only debug server, installs the
// LLGo extension in an isolated profile, and waits for Chromium to exit.
func Run(artifact string, options Options, stdin io.Reader, stdout, stderr io.Writer) error {
	path, version, err := Find(options.Chrome)
	if err != nil {
		return err
	}
	session, err := StartSession(artifact, options.SourceMaps)
	if err != nil {
		return fmt.Errorf("llgo debug: %w", err)
	}
	defer session.Close()

	profile, profileCleanup, err := prepareProfile(options)
	if err != nil {
		return err
	}
	defer profileCleanup()
	extensionPath := filepath.Join(profile, "llgo-extension")
	if err := WriteExtension(extensionPath); err != nil {
		return fmt.Errorf("llgo debug: prepare browser extension: %w", err)
	}

	args := []string{
		"--user-data-dir=" + profile,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-background-networking",
		"--disable-breakpad",
		"--disable-default-apps",
		"--password-store=basic",
		"--disable-extensions-except=" + extensionPath,
		"--load-extension=" + extensionPath,
	}
	if runtime.GOOS == "darwin" {
		// An isolated profile must not wait for a system Keychain prompt before
		// loading its command-line extension and first navigation.
		args = append(args, "--use-mock-keychain")
	}
	if !options.DisableTools {
		args = append(args, "--auto-open-devtools-for-tabs")
	}
	args = append(args, options.ChromeArgs...)
	sessionURL := session.URL
	if options.DisableTools {
		sessionURL += "?llgo-devtools=disabled"
	}
	args = append(args, sessionURL)
	fmt.Fprintf(stderr, "llgo debug: Chromium %d; browser session %s\n", version, session.URL)
	command := exec.Command(path, args...)
	command.Stdin = stdin
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("llgo debug: Chromium session: %w", err)
	}
	return nil
}

func prepareProfile(options Options) (string, func(), error) {
	if options.ProfilePath != "" {
		path, err := filepath.Abs(options.ProfilePath)
		if err != nil {
			return "", func() {}, err
		}
		if err := os.MkdirAll(path, 0o700); err != nil {
			return "", func() {}, err
		}
		return path, func() {}, nil
	}
	path, err := os.MkdirTemp("", "llgo-browser-debug-")
	if err != nil {
		return "", func() {}, fmt.Errorf("llgo debug: create Chromium profile: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(path) }
	if options.KeepProfile {
		cleanup = func() {}
	}
	return path, cleanup, nil
}

// Find resolves and validates a Chromium-family executable.
func Find(configured string) (string, int, error) {
	candidates := []string{configured, os.Getenv("LLGO_CHROME")}
	switch runtime.GOOS {
	case "darwin":
		candidates = append(candidates,
			"/Applications/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary",
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		)
	case "windows":
		candidates = append(candidates, "chrome.exe", "chromium.exe")
	default:
		candidates = append(candidates, "chromium", "chromium-browser", "google-chrome", "google-chrome-stable")
	}
	seen := make(map[string]bool)
	var failures []string
	for _, candidate := range candidates {
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		path, err := exec.LookPath(candidate)
		if err != nil {
			continue
		}
		output, err := exec.Command(path, "--version").CombinedOutput()
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		match := chromeVersionPattern.FindStringSubmatch(string(output))
		if len(match) != 2 {
			failures = append(failures, fmt.Sprintf("%s: cannot parse %q", path, strings.TrimSpace(string(output))))
			continue
		}
		major, _ := strconv.Atoi(match[1])
		if major < MinimumChromeMajor {
			failures = append(failures, fmt.Sprintf("%s: version %d is older than %d", path, major, MinimumChromeMajor))
			continue
		}
		return path, major, nil
	}
	detail := ""
	if len(failures) != 0 {
		detail = ": " + strings.Join(failures, "; ")
	}
	return "", 0, fmt.Errorf("llgo debug: Chromium %d or newer is required; use -chrome or LLGO_CHROME%s", MinimumChromeMajor, detail)
}

// WriteExtension materializes the embedded unpacked extension.
func WriteExtension(directory string) error {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	for name, data := range map[string][]byte{
		"manifest.json": extensionManifest,
		"devtools.html": extensionPage,
		"plugin.js":     extensionPlugin,
	} {
		if err := os.WriteFile(filepath.Join(directory, name), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

type Session struct {
	URL            string
	Listener       net.Listener
	Server         *http.Server
	Bundle         *browserdebug.Bundle
	pluginRequests atomic.Uint64
	pluginReady    atomic.Uint64
	runtimeReady   atomic.Uint64
}

// StartSession starts the loopback HTTP portion of a browser debug session.
// It is exported so headless acceptance tests can exercise exactly the same
// artifact, sidecar, source, schema, and page routes as the interactive path.
func StartSession(artifact string, mappings []browserdebug.PathMapping) (*Session, error) {
	bundle, err := browserdebug.Load(artifact, mappings)
	if err != nil {
		return nil, err
	}
	wasmExec, err := os.ReadFile(filepath.Join(env.LLGoROOT(), "targets", "wasm_exec.js"))
	if err != nil {
		return nil, fmt.Errorf("read browser runtime: %w", err)
	}
	main, err := os.ReadFile(bundle.MainPath)
	if err != nil {
		return nil, err
	}
	indexJSON, err := json.Marshal(bundle.Index)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("start browser debug server: %w", err)
	}
	origin := "http://" + listener.Addr().String()
	mainRoute := "/" + filepath.Base(bundle.MainPath)
	mainURLPath := "/" + url.PathEscape(filepath.Base(bundle.MainPath))
	session := &Session{URL: origin + "/", Listener: listener, Bundle: bundle}

	files := map[string]servedFile{
		mainRoute:       {data: main, contentType: "application/wasm"},
		"/wasm_exec.js": {data: wasmExec, contentType: "text/javascript; charset=utf-8"},
		"/favicon.ico":  {data: nil, contentType: "image/x-icon"},
	}
	if bundle.SymbolsPath != bundle.MainPath {
		reference, ok, err := wasmdebug.ExternalURL(main)
		if err != nil || !ok {
			listener.Close()
			return nil, fmt.Errorf("read external WebAssembly DWARF URL: %w", err)
		}
		parsed, _ := url.Parse(reference)
		sidecarRoute, err := url.PathUnescape("/" + parsed.EscapedPath())
		if err != nil {
			listener.Close()
			return nil, err
		}
		sidecar, err := os.ReadFile(bundle.SymbolsPath)
		if err != nil {
			listener.Close()
			return nil, err
		}
		files[sidecarRoute] = servedFile{data: sidecar, contentType: "application/wasm"}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(response http.ResponseWriter, request *http.Request) {
		setDebugHeaders(response)
		if request.URL.Path == "/" {
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(response, debugPage(mainURLPath))
			return
		}
		if file, ok := files[request.URL.Path]; ok {
			response.Header().Set("Content-Type", file.contentType)
			response.Header().Set("Content-Length", strconv.Itoa(len(file.data)))
			_, _ = response.Write(file.data)
			return
		}
		http.NotFound(response, request)
	})
	mux.HandleFunc("/__llgo/debug-index.json", func(response http.ResponseWriter, _ *http.Request) {
		session.pluginRequests.Add(1)
		setDebugHeaders(response)
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write(indexJSON)
	})
	mux.HandleFunc("/__llgo/debug-schema.json", func(response http.ResponseWriter, _ *http.Request) {
		setDebugHeaders(response)
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write(debugabi.SchemaV1())
	})
	mux.HandleFunc("/__llgo/plugin-ready", func(response http.ResponseWriter, request *http.Request) {
		setDebugHeaders(response)
		if request.Method != http.MethodGet {
			response.Header().Set("Allow", http.MethodGet)
			http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		session.pluginReady.Add(1)
		response.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/__llgo/runtime-ready", func(response http.ResponseWriter, request *http.Request) {
		setDebugHeaders(response)
		if request.Method != http.MethodGet {
			response.Header().Set("Allow", http.MethodGet)
			http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		session.runtimeReady.Add(1)
		response.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/__llgo/source/", func(response http.ResponseWriter, request *http.Request) {
		setDebugHeaders(response)
		id := strings.TrimPrefix(request.URL.Path, "/__llgo/source/")
		path, ok := bundle.SourceFiles[id]
		if !ok {
			http.NotFound(response, request)
			return
		}
		response.Header().Set("Content-Type", "text/plain; charset=utf-8")
		http.ServeFile(response, request, path)
	})

	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	session.Server = server
	go func() {
		_ = server.Serve(listener)
	}()
	return session, nil
}

// PluginRequests reports how often Chrome's Language Extension requested the
// session index. PluginReady is the stronger end-to-end readiness signal.
func (s *Session) PluginRequests() uint64 {
	if s == nil {
		return 0
	}
	return s.pluginRequests.Load()
}

// PluginReady reports how often Chrome's Language Extension completed all
// module, index, build-identity, and debugger-schema validation.
func (s *Session) PluginReady() uint64 {
	if s == nil {
		return 0
	}
	return s.pluginReady.Load()
}

// RuntimeReady reports how often the inspected page completed WebAssembly
// instantiation. It is independent of whether DevTools or the extension ran.
func (s *Session) RuntimeReady() uint64 {
	if s == nil {
		return 0
	}
	return s.runtimeReady.Load()
}

type servedFile struct {
	data        []byte
	contentType string
}

func setDebugHeaders(response http.ResponseWriter) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
	response.Header().Set("Cross-Origin-Embedder-Policy", "require-corp")
	response.Header().Set("Access-Control-Allow-Origin", "*")
}

func debugPage(modulePath string) string {
	quoted, _ := json.Marshal(modulePath)
	return `<!doctype html>
<meta charset="utf-8">
<title>LLGo WebAssembly Debug Session</title>
<pre id="status">loading</pre>
<button id="run" type="button" disabled>Run</button>
<script src="/wasm_exec.js"></script>
<script>
(async () => {
  const status = document.getElementById('status');
  const runButton = document.getElementById('run');
  try {
    const devToolsMode = new URLSearchParams(location.search).get('llgo-devtools');
    if (devToolsMode !== 'disabled' &&
        !globalThis.__llgoLanguageExtensionReady) {
      status.textContent = 'Waiting for the LLGo DevTools extension.';
      await new Promise(resolve => {
        const timer = setTimeout(resolve, 5000);
        globalThis.addEventListener('__llgoLanguageExtensionReady', () => {
          clearTimeout(timer);
          resolve();
        }, {once: true});
      });
    }
    const go = new Go();
    const memory = () => new DataView(go._inst.exports.memory.buffer);
    Object.assign(go.importObject.env, {
      emscripten_asm_const_int: () => 0,
      emscripten_notify_memory_growth: () => {},
      _emscripten_throw_longjmp: () => { throw new Error('LLGo longjmp'); },
    });
    Object.assign(go.importObject.wasi_snapshot_preview1, {
      args_sizes_get: (argc, argvSize) => {
        memory().setUint32(argc, 0, true);
        memory().setUint32(argvSize, 0, true);
        return 0;
      },
      args_get: () => 0,
      clock_time_get: (_clock, _precision, result) => {
        memory().setBigUint64(result, BigInt(Date.now()) * 1000000n, true);
        return 0;
      },
    });
    const response = await fetch(` + string(quoted) + `, {cache: 'no-store'});
    const result = await WebAssembly.instantiateStreaming(response, go.importObject);
    globalThis.__llgoDebugInstance = result.instance;
    globalThis.__llgoDebugStatus = {state: 'ready'};
    status.textContent = 'LLGo WebAssembly ready. Set Go breakpoints in Sources, then click Run.';
    fetch('/__llgo/runtime-ready', {cache: 'no-store'}).catch(error => console.error(error));
    globalThis.__llgoDebugRun = async () => {
      if (globalThis.__llgoDebugStatus.state !== 'ready') return;
      globalThis.__llgoDebugStatus = {state: 'running'};
      runButton.disabled = true;
      status.textContent = 'LLGo WebAssembly running.';
      try {
        await go.run(result.instance);
        globalThis.__llgoDebugStatus = {state: 'exited', code: go.exitCode};
        status.textContent = 'LLGo WebAssembly exited with code ' + go.exitCode;
      } catch (error) {
        globalThis.__llgoDebugStatus = {state: 'error', message: String(error && error.stack || error)};
        status.textContent = globalThis.__llgoDebugStatus.message;
        console.error(error);
      }
    };
    runButton.addEventListener('click', globalThis.__llgoDebugRun);
    runButton.disabled = false;
  } catch (error) {
    globalThis.__llgoDebugStatus = {state: 'error', message: String(error && error.stack || error)};
    status.textContent = globalThis.__llgoDebugStatus.message;
    console.error(error);
  }
})();
</script>
`
}

func (s *Session) Close() error {
	if s == nil || s.Server == nil {
		return nil
	}
	context, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := s.Server.Shutdown(context)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
