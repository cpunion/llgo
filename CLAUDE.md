# LLGo Contributor and AI Assistant Guide

This document provides essential information for contributors and AI assistants fixing bugs or implementing features in the LLGo project.

## About LLGo

LLGo is a Go compiler based on LLVM designed to better integrate Go with the C ecosystem, including Python and JavaScript. It's a subproject of the XGo project that aims to expand the boundaries of Go/XGo for game development, AI and data science, WebAssembly, and embedded development.

## Project Structure

- `cmd/llgo` - Main llgo compiler command (usage similar to `go` command)
- `cl/` - Core compiler logic that converts Go packages to LLVM IR
- `ssa/` - LLVM IR file generation using Go SSA semantics
- `internal/build/` - Build process orchestration
- `runtime/` - LLGo runtime library
- `chore/` - Development tools (llgen, llpyg, ssadump, etc.)
- `_demo/` - Example programs demonstrating C/C++ interop (`c/hello`, `c/qsort`) and Python integration (`py/callpy`, `py/numpy`)
- `_cmptest/` - Comparison tests to verify the same program gets the same output with Go and LLGo

## Development Environment

For detailed dependency requirements and installation instructions, see the [Dependencies](README.md#dependencies) and [How to install](README.md#how-to-install) sections in the README.

CI uses LLVM 19 and pins exact Go patch releases; check [`.github/workflows/llgo.yml`](.github/workflows/llgo.yml) and [`.github/workflows/goroot.yml`](.github/workflows/goroot.yml) instead of guessing versions. Native development is supported on macOS and Linux. Native Windows support is still TODO; use WSL2 or Linux containers on Windows.

## Repository and GitHub Safety

- Treat `xgo-dev/*` as upstream. Do not push branches or tags directly to an `xgo-dev` repository, and do not merge its pull requests.
- Push code to your fork, then create or update a pull request against `xgo-dev/llgo:main`. Bug reports and proposals may be submitted as upstream issues.
- Do not publish upstream releases or change upstream repository settings. Inspect remotes before any write operation when ownership is unclear.
- Prefer `gh issue view`, `gh pr view`, and `gh pr checks` for GitHub state. Use `gh api` for review threads, inline comments, check-run details, or fields not exposed by the higher-level commands; avoid scraping the website.
- Keep changes scoped, preserve unrelated worktree edits, and review the complete diff against upstream before submission. Diagnose baseline failures instead of hiding them with skips, exclusions, or weakened checks.

## Testing & Validation

The following commands and workflows are essential when fixing bugs or implementing features in the LLGo project:

### Run all tests
```bash
go test ./...
```

**Note:** Some tests may fail if optional dependencies (like Python) are not properly configured. The test suite includes comprehensive tests for:
- Compiler functionality
- SSA generation
- C interop
- Python integration (requires Python development headers)

The root command does not enter the nested `runtime` Go module. Test it separately when runtime code changes:

```bash
(cd runtime && go test ./...)
```

Prefer the development wrapper for LLGo execution tests; it builds the current checkout and sets `LLGO_ROOT`:

```bash
./dev/llgo.sh test ./path/to/package
```

After focused tests pass, `./dev/local_ci.sh` runs the main local build, test, LLGo, demo, target-build, and cache checks when the optional dependencies are available. See [`dev/README.md`](dev/README.md) for the maintained commands.

### Coverage

- The Codecov patch check must pass; new deterministic logic and error paths should normally be fully covered.
- From the module containing the target package, check focused coverage with `go test -coverprofile=coverage.out ./path/to/package` and `go tool cover -func=coverage.out`.
- Coverage from Linux and macOS is combined because each has platform-specific paths. Validate host-specific changes on the matching host when possible.
- [`.github/codecov.yml`](.github/codecov.yml) lists paths excluded from coverage. Add an exclusion only for generated, tooling, fixture, or otherwise non-meaningful code; never exclude production logic merely to make a PR pass, and explain every ignore change in the PR.

### Write and run tests for your changes

When adding new functionality or fixing bugs, create appropriate test cases:

```bash
# Add your test to the relevant package's *_test.go file
# Then run tests for that package
go test ./path/to/package

# Or run all tests
go test ./...
```

**Important:** The `LLGO_ROOT` environment variable must be set to the repository root when running llgo commands during development.

### Update IR test expectations

When `ssa/` or `cl/` changes generated IR, refresh only the affected expectations and review every generated diff:

```bash
go run ./chore/litgen path/to/LITTEST/in.go  # default
go run ./chore/llgen path/to/legacy/case     # remaining out.ll cases only
```

Do not regenerate unrelated output. The legacy batch tooling, supported scopes, and marker format are documented in [`dev/README.md`](dev/README.md#6-refresh-test-goldens).

### Compatibility and target validation

- Go compatibility covers source and observable behavior, not the gc compiler's internal ABI. Run standard-library tests with both `go test ./test/std/...` and `./dev/llgo.sh test ./test/std/...`.
- Run official Go cases with `bash ./dev/test_goroot.sh -- -directive-mode ci`; see [`test/goroot/README.md`](test/goroot/README.md) for filtering, multiple toolchains, full coverage, and sharding.
- Run native tests on the matching host. Use `dev/docker.sh` for Linux amd64/arm64 validation, `dev/test_wasm.sh` for Wasm, and `dev/test_embed.sh` for embedded build plus emulator smoke.
- Cross-compilation alone is not execution validation. Do not weaken or reclassify failures to make a change pass, and state any target that could not be run.

The host matrix, CI coverage, dependencies, and target-specific follow-up commands are in [`dev/README.md`](dev/README.md#platform-and-target-validation).

## Code Quality

Before submitting any code updates, you must run the following formatting and validation commands:

### Format code
```bash
gofmt -w path/to/changed.go
```

**Important:** Format every changed Go file before committing, but do not rewrite unrelated files in a shared or dirty worktree.

### Run static analysis
```bash
go vet ./...
```

**Note:** Currently reports some issues related to lock passing by value in `ssa/type_cvt.go` and a possible unsafe.Pointer misuse in `cl/builtin_test.go`. These are known issues.


## Common Development Tasks

### Build the entire project
```bash
go build -v ./...
```

### Build llgo command specifically
```bash
go build -o llgo ./cmd/llgo
```

### Check llgo version
```bash
llgo version
```

### Install llgo for system-wide use
```bash
./install.sh
```

### Build development tools
```bash
go install -v ./cmd/...
go install -v ./chore/...
```

## Key Modules for Understanding

- `ssa` - Generates LLVM IR using Go SSA semantics
- `cl` - Core compiler converting Go to LLVM IR
- `internal/build` - Orchestrates the compilation process

## Debugging

### Disable Garbage Collection
For testing purposes, you can disable GC:
```bash
LLGO_ROOT=/path/to/llgo llgo run -tags nogc .
```

## LLGO_ROOT Environment Variable

**CRITICAL:** Always set `LLGO_ROOT` to the repository root when running llgo during development:

```bash
export LLGO_ROOT=/path/to/llgo
# or
LLGO_ROOT=/path/to/llgo llgo run .
```

## Important Notes

1. **Testing Requirement:** All bug fixes and features MUST include tests
2. **Demo Directory:** Examples in `_demo` are prefixed with `_` to prevent standard `go` command from trying to compile them
3. **Defer in Loops:** LLGo now supports `defer` within loops, matching Go's semantics of executing defers in LIFO order for every iteration. Be mindful of loop-heavy defer usage as it allocates per iteration.
4. **C Ecosystem Integration:** LLGo uses `go:linkname` directive to link external symbols through ABI
5. **Python Integration:** Third-party Python libraries require separate installation of library files
