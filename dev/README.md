# Dev tooling

This directory contains scripts for running LLGo locally and inside reusable Linux dev containers.

## Prerequisites

- Docker installed and running
- Docker Compose v2 (`docker compose`, not `docker-compose`)

## 1) Start a Linux container, then run `dev/llgo.sh` / `dev/llgo_wasm.sh`

Start an interactive shell (pick one):

```bash
./dev/docker.sh amd64
./dev/docker.sh arm64
./dev/docker.sh i386
```

Notes:
- `amd64` uses the `pydeps` image target (includes extra Python demo deps like `numpy`/`torch`).
- `arm64` and `i386` use the smaller `base` target (no extra Python ML deps).

Inside the container, run tests/builds using the repo scripts:

```bash
./dev/llgo.sh test ./...
./dev/llgo.sh test ./test

# WASI/WASM (wasip1/wasm)
./dev/llgo_wasm.sh build ./...
```

Notes:
- `dev/docker.sh` starts in the same repo subdirectory you launched it from.
- `dev/llgo.sh` and `dev/llgo_wasm.sh` must be run from within `LLGO_ROOT` (the repo) and will error otherwise.

## 2) Start a Linux container, run one command, then exit

```bash
./dev/docker.sh amd64 bash -lc './dev/llgo.sh test ./test'
```

## 3) Run on the host (no container)

From anywhere inside the repo:

```bash
./dev/llgo.sh test ./test
./dev/llgo_wasm.sh build ./...
```

## 4) Run local CI (covers most checks)

```bash
./dev/local_ci.sh
```

This script creates a temporary workspace, runs formatting/build/tests, runs `llgo test`, and then runs demo checks.
You can control demo parallelism via `LLGO_DEMO_JOBS` (defaults to up to 4 jobs).

## 5) `dev/docker.sh` (composition-friendly)

`dev/docker.sh` is a thin wrapper around `docker compose`:

```bash
./dev/docker.sh <arch> [command...]
```

- `<arch>` must be `amd64`, `arm64`, or `i386`.
- If `[command...]` is omitted, it starts an interactive `bash`.
- If `[command...]` is provided, it runs that command and exits.
- You must run it from within the repo (within `LLGO_ROOT`), and it will start in the matching repo subdirectory inside the container.

## Platform and target validation

Cross-compilation proves that an artifact was produced; run it on a matching host, container, or emulator when behavior changes.

| Development host | Practical local coverage |
| --- | --- |
| macOS arm64 | Native macOS arm64; macOS amd64 with Rosetta and an x86_64 toolchain; Linux amd64/arm64 with Docker Desktop or OrbStack |
| macOS amd64 | Native macOS amd64; Linux amd64/arm64 with Docker Desktop or OrbStack; use a remote arm64 Mac for native macOS arm64 behavior |
| Linux amd64/arm64 | Native matching Linux architecture; the other Linux architecture with containers plus QEMU/binfmt |
| Windows amd64/arm64 | Linux through WSL2 or Docker; native Windows is not currently supported |

Use the same container commands with Docker Desktop or OrbStack on macOS. Linux hosts must enable QEMU/binfmt before running the other architecture:

```bash
./dev/docker.sh amd64 bash -lc './dev/llgo.sh test ./test/...'
./dev/docker.sh arm64 bash -lc './dev/llgo.sh test ./test/...'
```

The primary CI source-build/test lanes cover macOS arm64 and Linux amd64. Release artifact smoke tests additionally cover macOS amd64 and Linux arm64. Linux and Windows hosts cannot validate native macOS behavior.

### Official Go compatibility

Run the current GOROOT with the CI directive set:

```bash
bash ./dev/test_goroot.sh -- -directive-mode ci
```

Pass GOROOT paths before `--` for multiple Go versions. See [`test/goroot/README.md`](../test/goroot/README.md) for case filters, full coverage, resource limits, and sharding.

### WebAssembly

Run the WASI build and WAMR execution smoke test:

```bash
./dev/test_wasm.sh
```

The script builds the pinned WAMR runner through `dev/build_iwasm.sh` when it is not cached. Changes specific to `GOOS=js` still need an explicit `GOOS=js GOARCH=wasm` build and an appropriate Node or browser test.

### Embedded

After installing SDL2 and libslirp as shown in [the CI setup action](../.github/actions/setup-embed-deps/action.yml), run:

```bash
./dev/test_embed.sh
```

The script caches the pinned ESP QEMU binaries outside the worktree, then builds and runs the ESP32/ESP32-C3 serial smoke tests. Target-table changes should also run `(cd _demo/embed/targetsbuild && bash build.sh empty)`. Startup/linker changes should additionally run `_demo/embed/test_esp32c3_startup.sh` with `esptool==5.1.0`. If a target has no emulator, report build-only validation explicitly.

## 6) Refresh test goldens

Use source-embedded `// LITTEST` checks and `litgen` for new and migrated IR tests. The remaining `out.ll` cases use `llgen`; `gentests` exists only for intentional legacy batch maintenance.

- `litgen` is the default for source-embedded FileCheck directives.
- `llgen` refreshes an individual legacy `out.ll` case.
- `gentests` batch-refreshes legacy `out.ll` and `expect.txt`; do not use it for routine focused changes.

### `gentests` (legacy batch only)

Run:

```bash
go run ./chore/gentests
```

Behavior:

- Refreshes `out.ll` for the built-in test suites under `cl/_testlibc`, `cl/_testlibgo`, `cl/_testrt`, `cl/_testgo`, `cl/_testpy`, and `cl/_testdata`.
- Refreshes `expect.txt` for the same directories using the existing runtime execution flow.
- Preserves the existing skip convention where `out.ll` or `expect.txt` containing only `;` means "do not refresh".
- New behavior: if a test case directory contains a non-test Go source file whose first line is exactly `// LITTEST`, `gentests` skips `llgen` for that directory and does not regenerate `out.ll` there.

Use `gentests` only when an intentional change requires a repository-wide refresh of legacy `out.ll` or `expect.txt`. For one `out.ll` case, use `go run ./chore/llgen path/to/case` instead.

### `litgen`

Run on a single marked file:

```bash
go run ./chore/litgen path/to/in.go
```

Run on a directory tree:

```bash
go run ./chore/litgen cl/_testrt/litdemo
go run ./chore/litgen cl/_testdata
```

Behavior:

- Accepts one or more paths.
- If the path is a `.go` file, it refreshes only that file. The file must start with `// LITTEST`.
- If the path is a directory, it walks that directory recursively, finds marked source files, and refreshes each marked test in place.
- Rewrites embedded `CHECK-LABEL`, `CHECK-NEXT`, `CHECK-EMPTY`, and referenced constant `CHECK-LINE` directives from the current generated IR.
- Does not update `expect.txt` and does not write `out.ll`.

Use `litgen` when the test case stores its IR expectations directly in the Go source instead of `out.ll`.

### Marker convention

Source-embedded IR checks are enabled by putting this marker on the first line of the source file:

```go
// LITTEST
```

The generated directives are consumed by the existing `littest`/FileCheck path in the compiler tests.

Example:

- [cl/_testrt/litdemo/in.go](../cl/_testrt/litdemo/in.go) is a minimal `_testrt` case that demonstrates `litgen` output.
