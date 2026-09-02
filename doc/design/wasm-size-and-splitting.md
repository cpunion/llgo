# WebAssembly size optimization and module splitting

Status: proposal.

## Summary

LLGo should treat WebAssembly size reduction and module splitting as two
separate products:

1. A production-size pipeline should remove unreachable code, make runtime
   symbolization metadata optional, minimize the JS/Wasm interface together,
   run a pinned post-link optimizer, and publish precompressed artifacts.
2. A later, experimental Emscripten pipeline should split an already linked
   program from a workload profile. It must report both initial and total
   payload, and it must not imply that a Go package is an independently
   loadable runtime unit.

The first work is lower risk and has measured value. In the `fmt.Printf`
fixture, `-pclntab=none` removes 338--343 KB raw and 123--125 KB after Brotli
from current LLGo Wasm modules. Emscripten import/export name minification, by
contrast, saves less than 2 KB raw and less than 1 KB after Brotli. Reducing the
export surface and runtime metadata therefore comes before short symbol names.

Core WebAssembly supports multiple modules and imports, but it does not define
package chunks, a loader manifest, symbol relocation, lazy fetching, or
`dlopen`. A single module remains one validation and instantiation unit. Any
parallel or lazy loading policy belongs to the toolchain and host. The
[core module model](https://webassembly.github.io/spec/core/syntax/modules.html),
[Web API](https://webassembly.github.io/spec/web-api/), and
[Emscripten module splitting documentation](https://emscripten.org/docs/optimizing/Module-Splitting.html)
are the relevant boundaries.

## Goals

- Establish a reproducible size matrix for Go, LLGo, and TinyGo.
- Cover browser JS and WASI Preview 1, and LLGo's wasm32 and memory64
  Emscripten profiles.
- Report raw, gzip, and Brotli size without conflating a Wasm module with its
  JS loader.
- Preserve explicit choices for runtime symbolization and debugging.
- Define an implementable path for optional Wasm PCLN data.
- Define a safe, profile-guided Emscripten splitting experiment.
- Measure initial payload, total payload, startup time, and correctness before
  enabling any optimization by default.

## Non-goals

- Treating symbol minification as security or obfuscation.
- Claiming TinyGo's standard library and runtime behavior are identical to Go.
- Making arbitrary Go packages dynamically linkable.
- Defining a new general-purpose Wasm dynamic-linking ABI.
- Enabling code splitting for the Go-platform ABI or WASI Preview 1 before a
  runtime and loader contract exists.

## Terminology and size accounting

This proposal uses the following terms:

- **Wasm bytes** are bytes in the `.wasm` file.
- **Glue bytes** are bytes in the JS loader. Go and TinyGo have a reusable
  loader; the current LLGo Emscripten loader is generated per program.
- **Initial payload** is the sum of independently compressed files required
  before the entry point can run.
- **Deferred payload** is fetched only after initial execution.
- **Total payload** is initial plus every deferred module and sidecar.
- **Release variant** means Go `-s -w -buildid=`, LLGo
  `-pclntab=none`, or TinyGo `-no-debug` in the tables below. These variants
  do not have identical symbolization semantics, so the option name is always
  shown.

The size notation in the artifact tables is:

```text
raw / gzip-9 / Brotli-11 bytes
```

Web assets are compressed separately before their sizes are added. This
matches HTTP deployment and preserves independent caching.

## Benchmark definition

The measurements were collected on 2026-09-02 from:

- Go 1.27.0 on Darwin arm64;
- LLGo R1 commit
  [`7467046b`](https://github.com/xgo-dev/llgo/pull/2472/commits/7467046bac9a59ea75a95ec7b6770f2dc90be63f),
  built with Go 1.27.0;
- TinyGo 0.39.0, LLVM 19.1.2, and Go 1.25.14, because TinyGo 0.39.0
  rejects Go 1.27;
- Emscripten 6.0.8-git and Binaryen 132;
- Node 26.8.1 and Wasmtime 48.0.1.

Every compiler used size optimization and `-trimpath` where supported. LLGo
used its default `-Oz`, `LLGO_BUILD_CACHE=off`, and `GOMAXPROCS=2`. TinyGo used
`-opt=z`; its Wasm builder always invokes `wasm-opt`, as shown by the
[TinyGo 0.39 builder](https://github.com/tinygo-org/tinygo/blob/v0.39.0/builder/build.go#L781-L918).

The cases are:

- `cprintf`: LLGo calls `github.com/goplus/lib/c.Printf`; TinyGo calls C
  `printf` through an inline-C wrapper. The standard Go Wasm ports do not
  support cgo, so this cell is not available for Go.
- `println`: `println("Hello, world")`.
- `fmtprintf`: `fmt.Printf("Hello, world\n")`.

`println` and `fmtprintf` use identical source with all three compilers.
`cprintf` compares the lowest available C `printf` path, not identical Go
source.

Every produced module passed `wasm-tools validate`. Every runnable release
variant printed `Hello, world` under its matching runner. The LLGo raw
`GOOS=js GOARCH=wasm` compatibility path validates, but the official Go
`wasm_exec.js` rejects it because it currently imports Emscripten `env`
functions. It is listed as compile-only and must not yet be described as ABI
compatible. Raw LLGo `wasip1/wasm`, named LLGo WASI, Go, TinyGo, and both
LLGo Emscripten profiles ran successfully.

## Release Wasm size

### JS API

These are Wasm-only sizes. JS glue is accounted for separately below.

| Compiler/profile | `cprintf` | `println` | `fmtprintf` |
| --- | ---: | ---: | ---: |
| Go `js/wasm`, stripped | N/A | 1,855,428 / 546,627 / 422,302 | 2,473,792 / 712,219 / 544,958 |
| LLGo Go-platform path, PCLN none | 66,554 / 25,665 / 21,829 | 65,885 / 25,535 / 21,831 | 1,958,314 / 574,014 / 398,900 |
| LLGo Emscripten wasm32, PCLN none | 74,876 / 28,091 / 23,898 | 74,215 / 27,934 / 23,804 | 1,966,643 / 574,729 / 398,779 |
| LLGo Emscripten memory64, PCLN none | 79,602 / 30,561 / 25,900 | 78,934 / 30,282 / 25,714 | 2,208,011 / 627,448 / 432,815 |
| TinyGo `wasm`, no debug | 23,760 / 8,799 / 7,613 | 18,052 / 6,954 / 6,023 | 151,088 / 57,132 / 45,015 |

The LLGo Go-platform row has no compatible JS glue today and is not a usable
web payload. It is retained to track convergence with the official ABI.

### WASI Preview 1 API

| Compiler/profile | `cprintf` | `println` | `fmtprintf` |
| --- | ---: | ---: | ---: |
| Go `wasip1/wasm`, stripped | N/A | 1,869,853 / 551,001 / 425,876 | 2,447,553 / 707,341 / 540,512 |
| LLGo Go-platform path, PCLN none | 93,215 / 33,343 / 27,858 | 92,465 / 33,173 / 27,714 | 2,010,612 / 571,638 / 396,934 |
| LLGo WASI C ABI, PCLN none | 98,324 / 35,459 / 29,530 | 97,152 / 35,230 / 29,220 | 1,997,577 / 574,326 / 399,686 |
| TinyGo `wasip1`, no debug | 22,379 / 8,629 / 7,532 | 16,961 / 6,814 / 6,009 | 72,950 / 28,611 / 24,161 |

There is no WASI Preview 1 memory64 row. LLGo's current memory64 profile is an
Emscripten JS profile; inventing a WASI counterpart would not be a like-for-like
measurement.

### JS glue and initial web payload

The release glue sizes are:

| Loader | `cprintf` | `println` | `fmtprintf` |
| --- | ---: | ---: | ---: |
| Go `wasm_exec.js`, reusable | N/A | 16,992 / 4,365 / 3,753 | 16,992 / 4,365 / 3,753 |
| LLGo Emscripten wasm32 `.mjs` | 70,731 / 19,878 / 17,621 | 70,731 / 19,878 / 17,582 | 97,763 / 25,885 / 22,766 |
| LLGo Emscripten memory64 `.mjs` | 74,048 / 20,509 / 18,124 | 74,048 / 20,509 / 18,129 | 109,087 / 27,359 / 23,973 |
| TinyGo `wasm_exec.js`, reusable | 16,481 / 4,734 / 4,155 | 16,481 / 4,734 / 4,155 | 16,481 / 4,734 / 4,155 |

The resulting independently compressed initial payloads are shown as
`raw / Brotli-11`:

| Runnable web profile | `cprintf` | `println` | `fmtprintf` |
| --- | ---: | ---: | ---: |
| Go `js/wasm` | N/A | 1,872,420 / 426,055 | 2,490,784 / 548,711 |
| LLGo Emscripten wasm32 | 145,607 / 41,519 | 144,946 / 41,386 | 2,064,406 / 421,545 |
| LLGo Emscripten memory64 | 153,650 / 44,024 | 152,982 / 43,843 | 2,317,098 / 456,788 |
| TinyGo `wasm` | 40,241 / 11,768 | 34,533 / 10,178 | 167,569 / 49,170 |

Go and TinyGo glue can be cached across programs. Current Emscripten glue is
program-specific, so a multi-application comparison shifts further toward a
reusable loader.

## What the size data means

TinyGo is smallest in these fixtures, but it is not a size result for complete
Go compatibility. Its Wasm targets use a small precise-GC runtime and Asyncify,
and several symbolization APIs are stubs: `runtime.Caller`, `runtime.Stack`,
and `runtime.FuncForPC` return no data in
[`src/runtime/stack.go`](https://github.com/tinygo-org/tinygo/blob/v0.39.0/src/runtime/stack.go).
TinyGo also replaces parts of the standard library. The matrix proves the size
of these runnable workloads; it does not prove semantic equivalence for every
program.

LLGo's small `cprintf` and `println` outputs show that its base runtime can stay
compact. `fmtprintf` is dominated by linked Go formatting, reflection, type,
and runtime metadata. On Emscripten wasm32 it grows from 128 defined functions
for `println` to 1,761 for `fmtprintf`; code grows from about 68 KB to 1.73 MB
before PCLN is counted. This dependency boundary is a higher-value target than
renaming ABI strings.

Memory64 costs 6--12% raw and about 8% Brotli in these fixtures relative to
Emscripten wasm32. It should be selected for its address-space requirement,
not as a general web default.

## Debug and PCLN metadata

The table below reports bytes removed as `raw (Brotli)` when changing from the
compiler's default metadata mode to the release variant:

| Compiler/profile and option | `cprintf` | `println` | `fmtprintf` |
| --- | ---: | ---: | ---: |
| Go JS, `-s -w -buildid=` | N/A | 35,221 (6,195) | 45,536 (8,005) |
| Go WASI, `-s -w -buildid=` | N/A | 35,285 (4,845) | 44,989 (7,399) |
| LLGo Go-platform JS, `-pclntab=none` | 0 (0) | 0 (0) | 342,368 (124,411) |
| LLGo Emscripten wasm32, `-pclntab=none` | 0 (0) | 0 (0) | 342,719 (125,143) |
| LLGo Emscripten memory64, `-pclntab=none` | 0 (0) | 0 (0) | 342,932 (124,636) |
| LLGo Go-platform WASI, `-pclntab=none` | 0 (0) | 0 (0) | 338,370 (123,937) |
| LLGo WASI C ABI, `-pclntab=none` | 0 (0) | 0 (0) | 338,370 (122,729) |
| TinyGo JS, `-no-debug` | 107,112 (30,840) | 78,513 (23,996) | 238,162 (65,421) |
| TinyGo WASI, `-no-debug` | 107,687 (30,879) | 79,019 (24,100) | 264,780 (72,952) |

Go's `-s` removes its optional Wasm `name` section, but Go runtime function
names remain in runtime data for stack traces. The Go linker writes the name
section only when `-s` is not set, as seen in the
[Go 1.27 Wasm linker](https://github.com/golang/go/blob/go1.27.0/src/cmd/link/internal/wasm/asm.go),
and the [linker documentation](https://pkg.go.dev/cmd/link) defines `-s` and
`-w`. This is stripping, not obfuscation.

TinyGo `-no-debug` removes DWARF at link time. Its subsequent `wasm-opt -g`
still retains a small `name` section. TinyGo has no PCLN-equivalent runtime
symbolization sidecar because the corresponding runtime APIs are not
implemented.

LLGo `-pclntab=none` is not merely a debug strip: it disables LLGo runtime
symbolization metadata and related behavior. The zero savings for the two
small cases show that reachability already removes the metadata when nothing
needs it. The large `fmtprintf` result makes the mode valuable for explicit
production builds, but it must not silently become the default.

### Current `-pclntab=external` limitation

`-pclntab=external` was tested and is not currently a Wasm feature. The R1
compiler rejects named Wasm targets with, for example:

```text
external PCLN metadata is not supported for target "emscripten"
```

It also rejects raw `js/wasm` by GOOS. The current design intentionally limits
the sidecar to native Darwin/Linux amd64/arm64 executables. Therefore this
proposal does not report a fabricated Wasm sidecar size. The embedded-to-none
delta above is an upper-bound estimate of detachable input, not the size of a
real sidecar; the external loader, identity, indexes, and sidecar encoding will
change both numbers.

## Compression comparison

### Existing compiler behavior

- Go performs reachability-based dead-code elimination, deduplicates Wasm
  function types, omits profitable zero-filled data runs, and optionally omits
  the name section. Internal calls already use numeric function indexes, so
  changing internal Go names to one character would not reduce call sites.
- TinyGo uses LLVM size optimization, LTO, `wasm-ld`, and an automatic Binaryen
  pass for wasm32. `-no-debug` strips DWARF before Binaryen.
- LLGo uses LLVM and the selected ecosystem linker. Emscripten performs its own
  JS/Wasm processing; LLGo's WASI path invokes Binaryen for Asyncify.

Go does not expose a Binaryen optimizer, import/export minifier, or module
splitter. TinyGo exposes optimization, debug, GC, scheduler, and component
options, but no automatic core-module splitter. TinyGo's component embedding
is component packaging, not lazy division of one Go runtime.

### Additional post-link Binaryen pass

A second pass was tested on every release module with Binaryen 132:

```text
wasm-opt <explicit feature allowlist> -Oz --strip-debug --strip-producers
```

All resulting modules validated and retained the same observed output. The
range across the three cases was:

| Compiler/profile | Raw reduction | Brotli reduction |
| --- | ---: | ---: |
| Go JS | 5.26--5.51% | -0.02--0.06% |
| Go WASI | 5.26--5.47% | -0.14--0.24% |
| LLGo Emscripten wasm32 | 2.23--3.64% | -0.22--1.12% |
| LLGo Emscripten memory64 | 2.64--3.44% | 0.43--1.11% |
| LLGo WASI C ABI | 0.02--0.20% | 0.24--0.81% |
| TinyGo JS | 3.00--6.59% | 3.26--8.67% |
| TinyGo WASI | 3.00--5.78% | 4.43--9.05% |

A negative Brotli reduction means the raw module became smaller but the
compressed response became larger. Raw size alone is therefore not an
acceptance criterion.

The experiment also found that `wasm-opt --all-features` is unsafe as a release
shortcut with Binaryen 132: it emitted compact-import encodings that Node and
Wasmtime rejected even though validation with every proposal enabled passed.
LLGo must derive and pin the feature allowlist from its target profile, and
test output with production runtimes.

### Import and export minification

Binaryen can rewrite ABI strings and emit a mapping. Direct application to the
LLGo Emscripten release modules produced these theoretical Wasm-only savings:

| Profile | `cprintf` | `println` | `fmtprintf` |
| --- | ---: | ---: | ---: |
| Emscripten wasm32 raw | 613 | 613 | 1,839 |
| Emscripten wasm32 Brotli | 187 | 320 | 732 |
| Emscripten memory64 raw | 632 | 633 | 1,986 |
| Emscripten memory64 Brotli | 162 | 171 | 589 |

These direct outputs are not runnable with the old JS glue. The supported form
must use Emscripten's paired JS/Wasm rewriting. Current R1 also links with
`-sEXPORT_ALL=1`, resulting in 26 exports for wasm32 `println` and 62 for
`fmtprintf`, compared with four exports in the tested Go JS modules. An exact
export allowlist and JS/Wasm metadce can remove roots and glue, while short
names only shorten strings. Export reduction has priority.

### Transport compression

Release artifacts should be precompressed with Brotli and gzip and served with
`Content-Type: application/wasm` plus the matching `Content-Encoding`.
`WebAssembly.instantiateStreaming` can then overlap download, decompression,
validation, and compilation. Transport compression is independent of code
splitting and should ship first.

Splitting can reduce compression efficiency because each file loses dictionary
context and adds headers and request overhead. A split is accepted only when a
measured latency or on-demand-byte benefit outweighs any increase in total
Brotli bytes.

## Module splitting comparison

| Compiler/profile | Multiple modules | Automatic splitting | Lazy-loading support |
| --- | --- | --- | --- |
| Go JS | Host can instantiate independent programs | No | No Go runtime contract |
| Go WASI Preview 1 | `c-shared` reactor exists since Go 1.24 | No | No standard loader |
| LLGo Go-platform paths | Future ABI-compatibility path | No | No |
| LLGo Emscripten wasm32/memory64 | Emscripten mechanisms are available underneath | Not integrated | Not integrated |
| LLGo WASI Preview 1 | Host-managed independent modules | No | No standard loader |
| TinyGo JS/WASI | Independent modules; WASI Preview 2 components also exist | No core-module splitter | Host/component composition only |

Go supports `wasip1/wasm -buildmode=c-shared`, but it is a reactor containing
one Go runtime rather than a split package. Go still does not support
`buildmode=shared` or `plugin` on Wasm; see
[Go 1.24 release notes](https://go.dev/doc/go1.24) and
[the platform build-mode table](https://github.com/golang/go/blob/go1.27.0/src/internal/platform/supported.go).

Emscripten provides both a main/side-module dynamic-linking ABI and Binaryen
`wasm-split`. Both are tool conventions rather than core WebAssembly. Dynamic
linking can increase code size because system libraries and retained symbols
must be coordinated. `wasm-split` instruments a whole module, records executed
functions, and produces primary and secondary modules from that profile. See
the [dynamic-linking documentation](https://emscripten.org/docs/compiling/Dynamic-Linking.html)
and [Binaryen tools](https://github.com/WebAssembly/binaryen).

The three micro-cases execute their only feature immediately, so moving that
feature to a deferred module cannot improve time to first output. No split-size
number is claimed from them. A valid split benchmark needs a cold feature that
is not called during startup and a real loader. Running `wasm-split` on the
current final file without Emscripten's `SPLIT_MODULE` preparation can create
two validating files, but not a supported runnable application; counting such
files would be misleading.

The current LLGo command also does not forward Go `-extldflags` to named Wasm
profiles, so `-sSPLIT_MODULE=1` cannot be enabled as a user linker escape hatch.
Splitting should be a typed LLGo pipeline option because it creates multiple
artifacts and changes the JS loader, not an opaque linker string.

## Proposed implementation

### Phase 0: reproducible measurement

Add an opt-in benchmark mode around the three fixtures that records:

- compiler version, Go version, LLVM/Binaryen/Emscripten version, and commit;
- target ABI and enabled Wasm features;
- Wasm, glue, PCLN sidecar, primary, and secondary sizes independently;
- raw, gzip-9, and Brotli-11 bytes;
- type, import, function, export, code, and data section counts/sizes;
- validation and execution status;
- initial and total payload;
- download, compile, instantiate, first-output, and deferred-load latency for
  split browser cases.

The benchmark should be manual or scheduled while the design is experimental.
Ordinary changes need only a small compile/run smoke test. Results must name
the metadata mode and must never compare a Wasm-only row with a Wasm-plus-glue
row.

### Phase 1: production-size pipeline

Add a typed Wasm optimization policy rather than forwarding arbitrary options:

```text
-wasm-opt=off|size
-wasm-strip=none|debug|all
```

The exact spelling can change during implementation, but the policy must:

1. preserve the existing default behavior;
2. keep `-pclntab=embedded|external|none` independent;
3. run the optimizer after Asyncify and final linking;
4. use an explicit, target-derived feature allowlist;
5. preserve required export names unless the JS glue is rewritten in the same
   transaction;
6. validate and execute the final artifact, not only an intermediate file;
7. produce deterministic bytes with pinned tool versions.

For Emscripten, replace `EXPORT_ALL=1` with explicit roots needed by LLGo,
Asyncify, embind/emval, malloc/free, the table, and public user exports. Enable
Emscripten's combined JS/Wasm metadce and import/export minification only after
that allowlist passes ABI tests.

In parallel, analyze why `fmt` retains roughly 1.7--1.9 MB of code in LLGo.
The useful outputs are retained-package, retained-symbol, reflection/type-data,
and PCLN attribution reports. Size-sensitive applications should not be told
that symbol minification solves a formatting dependency problem.

### Phase 2: Wasm PCLN packaging

Extend `-pclntab=external` to Wasm as a separate deliverable before code
splitting.

Expected outputs are:

```text
app.wasm
app.wasm.pclntab
app.mjs                 # Emscripten only
app.wasm-manifest.json
```

The existing PCLN version, target tuple, ABI version, checksum, and executable
identity checks should be retained. Addresses must be module-relative, work for
wasm32 and memory64, and survive final Binaryen transformations. The build ID
must also support server-side symbolization when no client sidecar is shipped.

Browser loading cannot synchronously fetch a sidecar from `runtime.Caller` or
a panic. The JS API should therefore expose an explicit Promise-based preload,
for example `loadPCLN(url)`. Before it completes, symbolization returns the
documented unavailable/numeric-PC fallback. Deployments can choose:

- no fetch and server-side symbolization;
- background fetch after startup;
- required preload before calling the Go entry point.

WASI may read a preopened sidecar synchronously on safe paths, but missing path
capabilities must be a cached soft failure. Fatal signal or trap paths must
never initiate I/O or wait for a loader.

Acceptance requires reporting main bytes, sidecar bytes, total bytes, and
initial bytes. Moving 340 KB out of the main module is not a 340 KB total-size
reduction.

### Phase 3: profile-guided Emscripten split

Introduce an explicitly experimental pipeline, initially only for named
Emscripten targets:

```text
-wasm-split=instrument
-wasm-split=profile:<path>
```

The instrument build should preserve the original module, emit the profile
writer and compatible JS loader, and identify the exact module version. The
profile build should emit:

```text
app.wasm                    # primary
app.deferred.wasm           # secondary
app.mjs                     # matching loader
app.wasm-manifest.json
```

Implementation should use Emscripten `SPLIT_MODULE` and the matching Binaryen
`wasm-split`, not split LLVM objects by Go package. Profiles from representative
startup workloads should be merged. Functions required by startup, allocation,
GC, scheduling, Asyncify transitions, callbacks, table initialization, traps,
and loader failure handling must remain primary until tests prove otherwise.

Roll out in this order:

1. **Eager split:** fetch and compile both files in parallel, instantiate both
   before entry, and prove the cross-module ABI.
2. **Worker lazy split:** defer the secondary fetch on a worker where blocking
   and event-loop constraints are controlled.
3. **Main-thread lazy split:** only with a tested JSPI/Promise-aware export or
   another explicit suspension contract. Existing Asyncify use alone does not
   make an arbitrary synchronous placeholder safe.

WASI Preview 1 and the Go-platform paths remain out of scope for this phase.
For feature plugins, separately compiled components with an explicit byte- or
C-shaped ABI are safer than transparent package splitting. The
[Component Model linking design](https://github.com/WebAssembly/component-model/blob/main/design/mvp/Linking.md)
can inform that later work, but it does not automatically share one LLGo heap,
GC, scheduler, or type identity.

## Cross-module invariants

A code split is correct only if all of these remain coherent:

- one linear memory and allocator;
- one function table and stable table slots;
- mutable globals, stack pointer, Asyncify state, and exception tags;
- one LLGo scheduler, timer subsystem, and GC;
- function values, interfaces, itabs, type descriptors, and reflection data;
- panic/recover and stack unwinding across the boundary;
- JS callbacks and re-entry while a deferred load is in progress;
- PCLN lookup after functions move to a secondary module;
- module identity and cache invalidation.

Compiling two Go packages as independent Wasm programs duplicates runtime
state and does not satisfy these invariants. Cross-module Go pointers,
interfaces, and function values are not a stable public ABI.

## Testing and acceptance

Each optimization mode must cover Emscripten wasm32, Emscripten memory64, and
WASI Preview 1 where applicable. Tests include:

- the three size fixtures;
- validation with the production feature set, not `--all-features`;
- Node, browser main thread, browser worker, and Wasmtime execution;
- goroutines, timers, channels, GC, finalizers, panic/recover, reflection,
  function values, and JS callbacks;
- PCLN present, absent, corrupt, mismatched, late-loaded, and concurrently
  loaded states;
- primary-only startup, secondary success, secondary fetch failure, and stale
  profile behavior;
- raw and compressed size regression thresholds;
- deterministic rebuild and manifest/hash checks.

A split is accepted only when:

- initial Brotli bytes or measured startup latency improve materially;
- total Brotli bytes and peak memory stay within an agreed budget;
- the output is runnable through the supported loader;
- unsampled but valid control flow loads the secondary safely;
- no Go behavior is silently removed.

## Recommended order

1. Land the reproducible matrix and section attribution.
2. Remove `EXPORT_ALL=1` in favor of an ABI-tested allowlist.
3. Add the pinned final size/strip pipeline with target feature allowlists.
4. Implement Wasm external PCLN and server-side symbolization support.
5. Investigate retained `fmt` code and metadata.
6. Prototype eager Emscripten split, then worker lazy split.
7. Consider main-thread lazy split only after the suspension contract is
   explicit and tested.

This order captures most measured size savings without first taking on a new
dynamic-linking and scheduler boundary.
