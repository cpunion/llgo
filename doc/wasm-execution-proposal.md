# LLGo WebAssembly Execution Proposal

Status: draft. This proposal extends [the WebAssembly profile proposal](wasm-proposal.md). It changes execution backends and runtime scheduling, but does not change the J32/J64/W32 data model defined there.

## Decision

LLGo will use one scheduler contract with target-specific execution backends:

| Target | Goroutine execution | M provider | Host async |
| --- | --- | --- | --- |
| Browser JavaScript | LLGo `wasmresume` stackless frames | bounded Web Worker pool, initially one worker | JSPI at the C/JavaScript boundary |
| WASI Preview 1 | pthread stacks | WASI threads, with WAMR as the first supported runtime | blocking Preview 1 APIs |
| non-WebAssembly | existing pthread/thread backend | existing host threads | existing host APIs |
| future WASI 0.3 components | backend selected behind the same scheduler contract | Component Model runtime | `async func`, `future<T>`, and `stream<T>` adapters |

The single-thread WASI/Asyncify profile will be removed after threaded GC is ready. The native pthread backend remains unchanged: creating a goroutine continues to create a host thread. Separating G from M outside WebAssembly is out of scope.

Emscripten remains LLGo's C/C++ SDK, sysroot, linker, ports, and JavaScript glue provider. It no longer defines the Go scheduler or continuation ABI. Binaryen is removed from the required build path once the replacement backends cover both browser and WASI builds.

## Problem

The current browser and single-thread WASI paths use Emscripten Fiber and Binaryen Asyncify. The build then invokes `wasm-opt --asyncify --translate-to-exnref`, optionally with another optimization pass. This couples goroutine suspension, exception encoding, optimization, and debug-information repair to a whole-module post-link transform.

That design has three practical limits:

1. Binaryen transforms code after LLVM has emitted DWARF. [Binaryen PR #8964](https://github.com/WebAssembly/binaryen/pull/8964) repairs invalid scope ranges, but relying on that repair leaves LLGo's debug correctness dependent on a post-link rewrite.
2. Asyncify treats a Wasm computation as the suspension unit. It does not provide the G/M separation needed to run many goroutines over a bounded number of browser workers.
3. C compatibility and goroutine scheduling have different requirements. Existing synchronous C code needs a normal C stack, while Go goroutines need cheap suspension. Applying one stack transform to the complete program makes both paths harder to reason about.

The replacement should remove those couplings without requiring annotations that classify every C function as blocking, nonblocking, synchronous, or asynchronous.

## Stable scheduler contract

All WebAssembly continuation implementations must adapt to this logical interface:

```text
Resume(g) -> Action

Action = Yield
       | Park(token)
       | Done
       | Panic(payload)

Wake(token)
```

`wasmresume` is an internal, versioned ABI between LLGo-generated Go code and the LLGo runtime. It must not appear at a public C boundary, package archive boundary without an ABI marker, or Component Model boundary.

A suspended Go frame contains a resume ID, state, scalar values, linear-memory addresses, and GC roots. Resume IDs are numeric. Shared scheduler state must not contain JavaScript objects or realm-local `funcref` values; each worker resolves an ID in its own instance or table view.

The build and archive identity must include at least:

- continuation backend and ABI version;
- memory ABI and pointer width;
- exception encoding;
- shared-memory and thread capability;
- host provider and host-async mode.

The linker must reject incompatible objects and archives instead of silently combining them.

## Browser execution

### Go continuations

The primary browser backend is the already prototyped `wasmresume` stackless ABI. The compiler lowers suspension points into explicit frames and returns an `Action` to the scheduler. The initial runtime has one M, but all current-G, root, and foreign-call state is owned by that M rather than process globals.

LLVM CoroSplit is an optional Wasm-only fallback. It must never replace the pthread/thread implementation on other platforms. Before CoroSplit, LLGo must reject resumable IR containing `invoke`, `landingpad`, `catchswitch`, `catchpad`, `cleanuppad`, `cleanupret`, `catchret`, or `resume`. This prevents a C++ exception or SjLj edge from being split across a Go continuation frame.

The fallback is not on the critical path. Local tests show that simple direct/child await, cleanup, panic, and recover cases can work when the coroutine uses explicit status returns. They also reproduce [LLVM issue #208409](https://github.com/llvm/llvm-project/issues/208409): current WebAssembly coroutine and exception lowering is unsafe when EH pads enter the coroutine pipeline.

### C and JavaScript boundary

Every Go-to-C call follows cgo-style ownership rules. The G is pinned to its M until C returns. A synchronous C call needs no annotation. A CPU-blocking call occupies that M. A C call that reaches asynchronous JavaScript uses [JSPI](https://github.com/WebAssembly/js-promise-integration/blob/main/proposals/js-promise-integration/Overview.md), which suspends the complete Wasm computation for that M.

Other goroutines can run on other Ms when the worker pool has more than one member. With one M, a long-running C call or JSPI suspension stalls all goroutines. This limitation is explicit in the initial profile: transparent synchronous C compatibility, one physical M, and continued execution of other goroutines cannot all be provided without transforming the C stack.

C-to-Go callbacks re-enter on the current M. An asynchronous callback becomes a scheduler event and wakes a token. Main-thread-only browser APIs continue to use Emscripten proxying.

This boundary avoids per-function blocking annotations. It also preserves the reason LLGo selects Emscripten: existing C/C++ headers, libc/libc++, ports, link behavior, and JavaScript integration remain available.

### Worker pool

The browser runtime grows from one M to a bounded M pool. It must not create one Web Worker for every G. The pool size is a runtime policy constrained by browser resources and cross-origin isolation requirements.

The multi-worker phase requires:

- a concurrent run queue and wakeup path;
- per-M current-G, root, callback, and foreign-call state;
- a shared-memory-safe frame representation;
- stop-the-world coordination for the linear-memory collector;
- deterministic worker startup, shutdown, and failure handling;
- executable tests with the required COOP/COEP headers.

The one-M implementation must use the same data structures with a pool size of one so that adding workers does not change the continuation ABI.

## WASI Preview 1

W32 will require the legacy [WASI threads](https://github.com/WebAssembly/wasi-threads) ABI and use the existing pthread runtime path. WAMR is the first supported runtime because it has explicit [`WAMR_BUILD_LIB_WASI_THREADS`](https://github.com/bytecodealliance/wasm-micro-runtime/blob/main/doc/build_wamr.md#lib-wasi-threads) support. This choice is tactical: Wasmtime removed its legacy wasi-threads implementation in [wasmtime#13558](https://github.com/bytecodealliance/wasmtime/pull/13558), and future portable WASI concurrency is expected to use newer Component Model facilities.

Removing single-thread WASI is gated on correct threaded GC. The current linear collector rejects `llgo.wasi_threads`; changing the target default before a stop-the-world or equivalent thread-safe collector exists would create a profile that only works with `nogc`. The migration therefore completes threaded GC, WAMR execution tests, pthread link compatibility, and runtime shutdown before deleting the Asyncify path.

WASI Preview 1 does not use `wasmresume`. Its G/M mapping remains the current pthread model. This avoids introducing a second scheduler rewrite while establishing a useful runtime baseline.

## Exceptions and panic

Go panic, defer, and recover use explicit status propagation across resumable Go frames. A Go panic does not depend on WebAssembly EH.

C++ exceptions and C SjLj stay inside the foreign C boundary. A bridge must translate a caught foreign failure into an explicit Go result or terminate according to the called API. Throwing or `longjmp` across a Go stackless frame is unsupported and must fail deterministically.

The first direct C/C++ EH profile uses LLVM's legacy Wasm EH encoding for compatibility. [Standard `exnref` EH](https://github.com/WebAssembly/spec/blob/main/proposals/exception-handling/Exceptions.md) becomes an opt-in and later the default after the LLVM pipeline is validated. LLGo will not use Binaryen's `--translate-to-exnref` as an ABI conversion step.

The acceptance suite must compare:

- Go panic/recover across yield and park points;
- a C++ exception caught wholly inside C++;
- a foreign exception translated at a C wrapper;
- rejection of an exception or SjLj edge that crosses Go;
- direct legacy EH and direct standard EH at supported optimization levels;
- the optional CoroSplit backend with and without EH constructs.

## Binaryen removal

LLGo does not need to reimplement Binaryen. It needs to replace the three jobs currently assigned to it:

| Current Binaryen job | Owner after migration |
| --- | --- |
| Asyncify Go and C stacks | `wasmresume` for Go; JSPI for synchronous C calling asynchronous JavaScript |
| translate legacy EH to `exnref` | LLVM emits the selected EH encoding directly |
| post-link optimization | LLVM/LTO optimizes before the final link |

The validated link shape is optimized LLVM objects followed by an Emscripten `-O1` final link with `-sERROR_ON_WASM_CHANGES_AFTER_LINK=1`. The build must not pass `-sASYNCIFY=0` after enabling `-sJSPI`, because that can overwrite Emscripten's JSPI configuration. Higher final-link optimization levels are enabled only when Emscripten can honor the no-post-link-change gate.

Once no supported profile requests Asyncify, LLGo removes `wasm_postlink.go`, the required `wasm-opt` dependency, Asyncify stack storage, Fiber scheduling hooks, and `--translate-to-exnref`. A build test must prove that the final link succeeds when `wasm-opt` is absent.

The scope is deliberately smaller than a general Wasm binary rewriter. If a future isolated transform is necessary, it should operate on LLGo IR before DWARF emission or update all affected debug metadata with a focused, tested implementation.

## GC and WasmGC

The first browser and WAMR implementations retain LLGo's linear-memory heap. Suspended stackless frames are visible to the collector through typed root maps.

Frame storage is split conceptually into:

- control slots: resume ID, state, scalars, and linear-memory offsets;
- root slots: values traced through a `RootStorage` interface.

The initial `RootStorage` is a linear-memory root table. A future [WasmGC](https://github.com/WebAssembly/gc/blob/main/proposals/gc/Overview.md) backend may store managed references in a reference array or table. WasmGC references cannot be represented as integer slots, so this proposal preserves a migration boundary rather than claiming automatic WasmGC support. Moving the Go heap to WasmGC remains a separate project.

## Compatibility with WebAssembly evolution

| Direction | Compatibility rule |
| --- | --- |
| JavaScript hosts | Go scheduling uses `wasmresume`; JSPI is only the foreign async bridge. GoJS and Emscripten providers share the scheduler contract. |
| WASI | Preview 1 uses WAMR threads now. [WASI 0.3](https://wasi.dev/releases/wasi-p3) `async func`, `future<T>`, and `stream<T>` map to `Park(token)` and `Wake(token)` through a future component adapter. |
| Threads | The M-provider interface hides Web Workers and WASI thread creation. Numeric resume IDs and linear-memory state remain usable with [shared-everything threads](https://github.com/WebAssembly/shared-everything-threads/blob/main/proposals/shared-everything-threads/Overview.md). |
| Stack switching | A future native [stack-switching](https://github.com/WebAssembly/stack-switching/blob/main/proposals/stack-switching/Explainer.md) backend can implement `Resume` without changing scheduler semantics or public C ABI. |
| WasmGC | `RootStorage` separates managed roots from integer control state; heap and object ABI migration remains independent. |
| Exception handling | Explicit Go status propagation is independent of legacy or standard Wasm EH. Foreign EH is contained at C boundaries. |
| Multiple workers | Per-M state, concurrent queues, numeric resume IDs, and shared-safe frames permit a bounded pool without assigning a worker to each G. |
| Memory64 | Frame offsets and pointer width are part of the ABI identity; J32 and J64 use the same logical scheduler contract. |

This consistency is semantic rather than mechanical. Native and WASI pthread stacks, browser stackless frames, and future native Wasm continuations may have different representations while producing the same scheduler actions.

## PR plan

Each PR must preserve a working default configuration and carry executable tests for the behavior it changes.

### E0: proposal

Record the decisions, invariants, migration gates, and PR dependencies. No runtime behavior changes.

### E1: execution capability and ABI identity

Add explicit continuation, host-async, M-provider, EH, shared-memory, and ABI-version capabilities. Include them in target configuration, archives, and caches. Reject incompatible link inputs. Existing execution remains the default.

### E2: scheduler interfaces and per-M state

Introduce `Resume`/`Action`/`Wake`, `RootStorage`, and an M-owned current-G/root/foreign context. Adapt the existing one-worker path without changing behavior. This PR removes singleton assumptions before a new continuation backend depends on them.

### E3: `wasmresume` compiler lowering

Implement liveness, frame layout, resume dispatch, direct and indirect calls, panic status, and typed root maps behind an experimental Wasm-only capability. Add focused IR tests and interpreter-level state-machine tests. Do not select it for non-Wasm targets.

### E4: one-M browser integration

Run channels, select, synchronization, timers, callbacks, defer/panic/recover, and GC over `wasmresume` with a worker-pool size of one. Keep the existing browser backend available as a temporary comparison path.

### E5: C boundary and JSPI

Implement G-to-M pinning, reentrant callbacks, async callback wakeups, JSPI imports/exports, and Emscripten main-thread proxying. Test synchronous C, blocking-C behavior, C calling Promise-based JavaScript, C++ exceptions contained in C++, and nested C/Go/JS callbacks in Node and a browser.

### E6: concurrent scheduler foundation

Make run queues, timers, wakeups, and lifecycle handling safe for multiple Ms without enabling another M by default. Exercise ownership and race invariants with deterministic runtime stress tests.

### E7: threaded linear GC

Make root publication, allocation, safepoints, and stop-the-world coordination safe for multiple Ms. Test collection while goroutines allocate, park, exit, enter C, and publish or retire roots. This is the largest implementation gate and is shared by WAMR threads and the browser worker pool.

### E8: WAMR WASI-threads profile

Make W32 use WASI threads and the pthread backend, add a WAMR CI runner, resolve pthread startup/exit and required C symbols, and run GC, channels, timers, panic/recover, and C interoperability tests. Then remove the single-thread WASI target and its Asyncify runtime files.

### E9: make browser stackless and remove Binaryen

Make `wasmresume` plus JSPI the default browser execution path. Remove Emscripten Fiber, remaining Asyncify state, the Binaryen post-link pipeline, and the required Binaryen setup. Validate direct EH, no-post-link-change linking, DWARF, size, startup time, and runtime benchmarks.

### E10: bounded browser worker pool

Enable a configurable, bounded number of Web Workers over shared memory. Add worker lifecycle, concurrent scheduling, GC stop-the-world, shutdown, and COOP/COEP browser tests. Keep one worker as a supported policy setting.

The browser chain is `E1 -> E2 -> E3 -> E4 -> E5 -> E9`. The concurrency chain is `E2 -> E6 -> E7 -> E8`; E8 also gates E9 because removing single-thread WASI eliminates the last required Asyncify user. E10 depends on E7 and E9. CoroSplit, native stack switching, WASI 0.3 components, standard-EH defaulting, and a WasmGC heap are follow-up experiments rather than prerequisites.

## Acceptance gates

- No supported default build invokes `wasm-opt` or emits Asyncify runtime state.
- Browser tests execute on current Chrome and Node with JSPI, plus a clear capability error on unsupported engines.
- Browser one-M and multi-M runs produce the same Go-visible scheduling, panic, callback, and lifecycle behavior.
- W32 runs under WAMR with WASI threads and GC enabled; `nogc` is not the only supported configuration.
- C calls require no blocking/async annotation. Tests document that a blocked C call occupies one M.
- Go panic never relies on Wasm EH, and foreign EH never crosses a Go resumable frame.
- Debug builds pass Wasm validation and debugger/DWARF smoke tests without a post-link code transform.
- Mixed continuation, EH, memory, or shared-thread ABIs fail at link time.
- Native and embedded test suites show no scheduler or pthread behavior change.

## Evidence behind the choices

Local experiments performed while preparing this proposal found:

- Emscripten 6.0.8 can build and run `emscripten_sleep` through JSPI in Node and Chrome without `wasm-opt`, using an optimized object, an `-O1` final link, and `-sERROR_ON_WASM_CHANGES_AFTER_LINK=1`.
- A higher Emscripten final-link optimization level requests a post-link Wasm change and is correctly rejected by that gate.
- LLVM 22.1.8 WebAssembly EH plus coroutine cases reproduce invalid legacy-EH output or a standard-EH compiler failure tracked by LLVM issue #208409.
- LLGo's focused CoroSplit prototype passes direct/child suspension, cleanup, panic, and recover cases because it uses explicit status propagation and keeps EH pads out of coroutine functions.

These observations justify the primary `wasmresume` choice and the EH containment rule. They are initial toolchain evidence; each implementation PR must convert the applicable claim into a checked regression or CI probe.

## Alternatives considered

- Keeping Asyncify retains the post-link DWARF dependency and does not create a bounded G/M model.
- Reimplementing Binaryen would reproduce a general Wasm optimizer and rewriter when LLGo only needs continuation lowering, direct EH selection, and pre-link optimization.
- Making CoroSplit the default would put the known LLVM coroutine/EH failures on the critical path. It remains useful as a constrained Wasm experiment.
- Creating one Web Worker per G has unacceptable startup, memory, browser-resource, and scheduling costs.
- Annotating every C function as blocking or asynchronous would break transparent use of existing C libraries and would still be wrong for indirect calls.
- Replacing Emscripten with a direct Clang/WASI SDK link does not provide an equivalent browser C/C++ ports and JavaScript integration ecosystem. The provider boundary remains explicit so a better complete alternative can be added later.
