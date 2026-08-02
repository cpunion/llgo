# Cross-platform debugger acceptance

This document is the completion gate for
[xgo-dev/llgo#2164](https://github.com/xgo-dev/llgo/issues/2164). A frontend is
not considered covered merely because it can open an artifact: its checked
lane must exercise the final linked artifact, execution transport, and the
common LLGo debugger ABI independently.

## Supported tool matrix

| Frontend | Minimum | Automated lane |
| --- | --- | --- |
| Native LLDB | LLDB 18 with Python | LLVM/LLDB 19 on Darwin and Linux |
| Embedded GDB Remote | GDB 12 with Python; target probe or emulator | `gdb-multiarch`, LLDB 19, and QEMU Cortex-M |
| WASI guest debug | Wasmtime 44; LLDB 22 with `process/wasm` | Wasmtime 47.0.3 and wasi-sdk 33 LLDB |
| Browser Wasm | Chromium 123 | Chrome for Testing 151.0.7922.71 |

LLVM 19 is the oldest producer/validator lane. Older LLVM versions are not a
compatibility target. Exact commands live in these focused fixtures:

- native: `cmd/llgo/lldbtest` and `cmd/internal/gdb`;
- emulated embedded: `cmd/llgo/debugtest/embedded`;
- opt-in physical probe: `cmd/llgo/debugtest/hardware`;
- WASI: `cmd/llgo/debugtest/wasi`;
- browser: `cmd/internal/browser` and `internal/browserdebug`.

## CI baseline

| Gate | Workflow | What must be observed |
| --- | --- | --- |
| Native Darwin/Linux | `llgo.yml` | source breakpoints, scopes, locals/globals, runtime values, goroutine ownership/stacks, panic/fault source locations, optimized inline stepping, Go/C/Go frames, and raw non-LLGo fallback |
| Emulated embedded | `targets.yml` | host ELF, GDB Remote and LLDB `gdb-remote`, source values/backtrace, identical flashed bytes with/without host DWARF, and verified retained DWARF |
| WASI | `wasi-debug.yml` | final Wasm DWARF, Wasmtime guest stub, source breakpoint, parameter/local, call stack, and clean exit |
| Browser | `browser-debug.yml` | embedded and external DWARF, standard build identity, escaped sidecar URL, source remapping, all common runtime-layout categories, real LLGo Wasm registration, and no-extension fallback |

Physical hardware is deliberately outside required PR CI. Its guarded script
must be run for a representative probe/target before declaring a release's
hardware path validated.

## Acceptance coverage and limits

The checked lanes cover source/line mapping, breakpoints, call stacks,
parameters, locals, globals, lexical shadowing, aggregate and recursive DWARF
types, marker/schema rejection, and the common string, slice, interface,
function, map, channel, and goroutine layout contract where the transport can
read memory.

Platform-specific limits remain explicit:

- `llgo debug` defaults to `-O0`. A variable with no valid DWARF location at a
  stop is omitted as optimized out; adapters never manufacture a zero value.
- Wasmtime 47 cannot expose LLGo's shared guest memory through its current
  guest-debug RSP map. Source breakpoints, Wasm locals, and stacks work, while
  memory-backed runtime summaries remain gated by Wasmtime issue 14062.
- Browser expression evaluation supports variable names, qualified global
  names, field selection, pointers, arrays, aggregates, and schema-backed Go
  runtime values. It is not a general Go expression interpreter.
- A missing external Wasm sidecar and a mismatched `build_id` fail before the
  browser launches. A consumer without the LLGo extension keeps raw/name
  section symbolication but does not get Go runtime presentation.
- The current Asyncify-transformed browser runtime is accepted by Go's DWARF
  reader and Chrome, but LLVM's final `llvm-dwarfdump --verify` still reports
  overlapping/range-containment diagnostics. With Emscripten 4.0.21 and
  Binaryen 125, relinking the exact same objects without Asyncify passes the
  verifier across all compile units; Asyncify alone introduces invalid ranges
  in both LLGo and Emscripten C-library DIEs. This matches Binaryen issue
  [#6406](https://github.com/WebAssembly/binaryen/issues/6406). A source map
  can recover source lines but cannot repair Wasm-local variable locations, so
  it is not a substitute for this gate. This is a final-artifact blocker, not a
  condition that the acceptance lane may suppress.

The native lane additionally stops explicit panic, integer division by zero,
invalid memory, and a real host trap at their exact source locations. Its
optimized fixture must preserve nested LLVM inline frames and step back to the
physical caller, while its callback fixture preserves ordered Go/C/Go frames.
Equivalent boundary coverage remains platform-specific. Clean final DWARF
after every LTO/post-link transform is still required before the umbrella
issue itself can be closed; keep transform failures visible in focused
fixtures rather than weakening the validators.

## Artifact-size accounting

Every build reports artifacts independently as:

```text
llgo: artifact role=<debug|deployment|debug+deployment> format=<format> size=<bytes> path=<path>
```

For embedded targets, the host debug ELF and derived flash image are separate
roles, and CI verifies that host DWARF does not change loadable bytes. For
external Wasm, the deployable module and `.debug.wasm` sidecar have separate
sizes and share one `build_id`. Size regressions must compare like roles; the
combined checkout size is not a deployment-size metric.
