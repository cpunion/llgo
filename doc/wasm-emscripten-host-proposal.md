# Proposal: Emscripten Output and Go Host Compatibility

Status: draft R4 subproposal. Tracking issue: [xgo-dev/llgo#2557](https://github.com/xgo-dev/llgo/issues/2557). Parent proposal: [xgo-dev/llgo#2152](https://github.com/xgo-dev/llgo/issues/2152).

## Summary

LLGo's Emscripten profiles must produce self-consistent artifacts and provide the JavaScript host surface expected by the selected Go `js/wasm` standard library. This proposal defines output suffix and sidecar behavior, module initialization and browser filesystem attachment, the `fs`/`process`/`path` compatibility surface, synchronous nested Go/JavaScript callbacks, external asynchronous event dispatch, and process termination.

The proposal reconciles the direct overlap between R4 and [xgo-dev/llgo#2539](https://github.com/xgo-dev/llgo/pull/2539). R4's generic nested-callback mechanism is the selected callback design; #2539's more complete filesystem and output handling should be adopted instead of keeping parallel implementations.

## Goals

- Make `.html`, `.js`, `.mjs`, and `.wasm` requests produce predictable, runnable Emscripten artifacts without stale sibling glue.
- Run the selected GOROOT's Go `js/wasm` filesystem code instead of maintaining an LLGo fork of its public semantics.
- Supply the minimum Node-like browser globals that code requires on an Emscripten module instance.
- Preserve synchronous callback results and JavaScript side-effect ordering for nested calls.
- Queue genuinely external host events without re-entering an inactive Go continuation.
- Preserve nonzero exit status and distinguish success from aborts, panics, rejected module initialization, and late asynchronous failures.

## Non-goals

- Implementing Node's complete `fs`, `process`, or `path` APIs.
- Making an Emscripten C-profile binary use the official Go binary ABI.
- Adding pthread workers or parallel goroutines.
- Hiding unsupported filesystem operations by reporting success.
- Maintaining two callback mechanisms, one generic and one special-cased for filesystem methods.

## Output contract

The requested output suffix selects the public artifact contract:

| Requested output | Required result |
| --- | --- |
| `.html` | runnable HTML plus the generated module and required host sidecars |
| `.js` | JavaScript glue using the requested name, plus external `.wasm` when not single-file |
| `.mjs` | ES module glue using the requested name, plus external `.wasm` when not single-file |
| `.wasm` | preserve the raw wasm artifact and publish the JavaScript glue needed to instantiate it |

The builder must publish `wasm_fs.js` only for browser-facing Emscripten outputs that need the Go host compatibility surface. Publication is atomic. When switching an output basename between `.js` and `.mjs`, stale generated glue must be removed so a previous build cannot be mistaken for the current artifact. Generated HTML must load the filesystem shim before importing or starting the module, exactly once.

## Module attachment

Modularized ES output does not guarantee a stable global `Module`. The shim therefore exposes an explicit idempotent attachment function:

```js
const options = {};
globalThis.llgoAttachWasmFS(options);
await initModule(options);
```

Auto-generated non-modularized HTML may attach an existing global module. All filesystem operations resolve the currently attached instance at call time; they must not capture a stale module or a typed-array view that can be detached when WebAssembly memory grows.

Installing the shim and callback dispatcher must be idempotent. Reinstallation may refresh the active module or handler, but must not wrap functions repeatedly or leak the prior handler.

## Go-compatible host surface

The compatibility layer supplies the portions of `fs`, `process`, and `path` used by Go's `js/wasm` standard library.

Required filesystem behavior includes asynchronous and synchronous forms of open, close, read, write, stat, directory, link, rename, truncate, permission, ownership, timestamp, and synchronization operations where Emscripten FS has a matching primitive. Unsupported operations report a stable Node-style error; they do not silently succeed.

Additional requirements:

- Translate Emscripten errno values to the Node-style `error.code` strings consumed by Go.
- Preserve file descriptor position rules, including `null`/unspecified positions.
- Remove `.` and `..` from directory results where Go's host contract expects entries only.
- Resolve paths relative to the attached Emscripten FS working directory.
- Expose deterministic browser process identity, groups, umask, cwd, and chdir.
- Route stdout and stderr to the module's `print`/`printErr` hooks when present.
- Incrementally decode UTF-8 across writes, bound unterminated output buffering, and flush pending output on close, fsync, and exit.
- Copy data away from resizable WebAssembly views before an operation that may grow memory or retain the bytes.

The shim is a compatibility adapter, not a replacement filesystem. Persistent or network-backed storage remains an Emscripten configuration choice.

## Callback semantics

There are two entry classes.

### Nested synchronous callback

When Go calls JavaScript through `syscall/js` and JavaScript invokes a Go `js.FuncOf` callback before that call returns, the callback is part of the same logical stack:

```text
Go G -> JavaScript -> Go callback -> JavaScript result -> original Go G
```

The callback must run before `Value.Call` or `Value.Invoke` returns, and its return value and side effects must be visible synchronously. The generic host dispatcher detects this nested entry, temporarily delimits the active Asyncify/Fiber continuation, runs the callback on the invoking G, and restores the caller's host state in a `finally` path.

Filesystem `*Sync` methods rely on this general rule. They must not introduce a separate private callback path. This is the point where R4 supersedes #2539's filesystem-specific asynchronous workaround.

A synchronously nested callback may run Go code and schedule work under the runtime's host-event contract. Operations that require an unrelated future host event remain subject to the same deadlock limitation documented for Go's `syscall/js.FuncOf`; the shim must not replay the surrounding JavaScript call because that could duplicate arbitrary side effects.

### External asynchronous event

A timer, browser event, or promise continuation arriving when no Go-to-JS call owns the stack is queued. The host sets a pending flag and wakes the single-worker scheduler. Go takes the event and dispatches it on a runnable goroutine. The JavaScript invocation has no synchronous Go result in this case.

The queue must preserve event identity and exactly-once dispatch. Callback release or replacement during a nested invocation must not free the active handle early or invoke a stale replacement.

## Exit and error semantics

Emscripten represents process termination with an `ExitStatus` object that can surface as a rejected module factory, an uncaught exception, or an unhandled rejection after Asyncify starts unwinding.

- `os.Exit(n)` must terminate with status `n`, including when called inside a nested `js.FuncOf` callback.
- An `ExitStatus` must pass through JS-call adapters to the module runner; it must not be converted into a `syscall/js.Error` panic.
- A later normal unwind must not overwrite an earlier nonzero status.
- Ordinary JavaScript exceptions remain `syscall/js.Error` values.
- Panics, traps, aborts, module initialization failures, and late asynchronous failures remain failures even if a success marker was already printed.
- Host handlers installed to observe Emscripten termination must not swallow unrelated exceptions.

## Reconciliation with #2539

The final implementation should use one owner for each concern:

| Concern | Selected direction |
| --- | --- |
| Nested callback semantics | R4 generic Go/JS host dispatcher |
| `fs.*Sync` compatibility | implement on top of the generic synchronous callback contract |
| Browser FS implementation | adopt #2539's fuller module-attached shim, including safe stdio and path handling |
| Install-once behavior | one idempotent bridge for module and callback state |
| Output suffixes and stale glue | adopt #2539's complete output contract |
| Existing R4 shim fragments | drop when equivalent to or weaker than the selected #2539 implementation |

Before integration, compare the two branches by behavior rather than commit identity. Adapted patches may have different hashes while still duplicating the same mechanism.

## Validation

Builder tests must cover all output suffixes, sidecar placement, HTML injection, atomic replacement, repeated builds, missing source files, and stale glue removal.

JavaScript unit tests must run the shim against a fake module and exercise errno mapping, full and partial reads/writes, position handling, path normalization, UTF-8 split across writes, bounded stdout/stderr, memory-view replacement, synchronous wrappers, module replacement, and idempotent install.

Real integration tests must also run in Chrome; a fake-module unit test alone does not prove the browser sidecar is live. The browser fixture must perform real Go file I/O through `wasm_fs.js`, then exercise nested callbacks, timers, process state, success exit, nonzero exit, and a delayed failure after the success marker.

Node acceptance must cover GJS, EC32, and EC64, including synchronous return values and exactly-once nested side effects, buffered and blocking callback behavior allowed by the documented contract, callback release and replacement, JavaScript exceptions, `os.Exit` from ordinary Go code and from a nested callback, and memory growth between host operations.

CI must inspect both status and terminal output. Merely building the shim, copying it beside an artifact, or unit-testing it without loading the generated module does not count as coverage.

## Rollout

1. Rebase the selected #2539 output/FS changes onto the current R4 base.
2. Retain R4's generic callback dispatcher and adapt synchronous FS operations to it.
3. Remove duplicate shims and callback special cases.
4. Add real browser FS execution before claiming the sidecar is supported.
5. Run GJS, EC32, and EC64 Node/browser acceptance and compare artifact size with the pre-change baseline.
6. Submit the host/output work separately from the reflection bridge PR.

---

# 子提案：Emscripten 输出与 Go Host 兼容

状态：R4 子提案草案。跟踪 issue：[xgo-dev/llgo#2557](https://github.com/xgo-dev/llgo/issues/2557)。父提案：[xgo-dev/llgo#2152](https://github.com/xgo-dev/llgo/issues/2152)。

## 摘要

LLGo 的 Emscripten profile 必须生成自洽的产物，并提供所选 Go `js/wasm` 标准库所期望的 JavaScript host surface。本提案定义输出后缀与 sidecar、模块初始化与浏览器文件系统绑定、`fs`/`process`/`path` 兼容层、Go/JavaScript 嵌套同步回调、外部异步事件分发和进程退出语义。

本提案用于解决 R4 与 [xgo-dev/llgo#2539](https://github.com/xgo-dev/llgo/pull/2539) 的直接重叠。回调部分选择 R4 的通用嵌套回调机制；文件系统和输出处理则应吸收 #2539 更完整的实现，不能长期保留两套并行方案。

## 目标

- 让 `.html`、`.js`、`.mjs` 和 `.wasm` 输出请求生成可预测、可运行且没有陈旧 sibling glue 的 Emscripten 产物。
- 复用所选 GOROOT 的 Go `js/wasm` 文件系统代码，而不是由 LLGo 分叉维护公开语义。
- 在 Emscripten module instance 上提供这些代码所需的最小 Node-like 浏览器全局对象。
- 保持嵌套调用的同步回调结果和 JavaScript side-effect 顺序。
- 对真正来自外部的 host event 排队，不能重入一个已经不活跃的 Go continuation。
- 保留非零退出状态，并区分正常成功、abort、panic、module 初始化拒绝和延迟异步失败。

## 非目标

- 实现 Node 完整的 `fs`、`process` 或 `path` API。
- 让 Emscripten C profile 二进制采用官方 Go 二进制 ABI。
- 增加 pthread worker 或并行 goroutine。
- 把不支持的文件系统操作伪装成成功。
- 同时维护一套通用回调机制和一套只为文件系统特殊处理的回调机制。

## 输出约定

请求的输出后缀决定公开产物约定：

| 请求输出 | 必须生成的结果 |
| --- | --- |
| `.html` | 可运行 HTML、生成模块及所需 host sidecar |
| `.js` | 使用请求文件名的 JavaScript glue；非 single-file 时还包括外部 `.wasm` |
| `.mjs` | 使用请求文件名的 ES module glue；非 single-file 时还包括外部 `.wasm` |
| `.wasm` | 保留原始 wasm 产物，并发布实例化它所需的 JavaScript glue |

builder 只能为确实需要 Go host 兼容层的浏览器 Emscripten 输出发布 `wasm_fs.js`，且发布必须是原子的。同一 basename 在 `.js` 与 `.mjs` 之间切换时必须移除陈旧 glue，避免旧构建被误认为当前产物。生成的 HTML 必须恰好一次地在导入或启动模块前加载文件系统 shim。

## 模块绑定

Modularized ES 输出不保证存在稳定的全局 `Module`，因此 shim 提供显式且幂等的绑定函数：

```js
const options = {};
globalThis.llgoAttachWasmFS(options);
await initModule(options);
```

自动生成的非 modularized HTML 可以绑定已有全局 module。每次文件系统操作都必须在调用时解析当前绑定的 instance，不能捕获过期 module，也不能长期持有可能在 WebAssembly memory growth 后失效的 typed-array view。

shim 和 callback dispatcher 的安装都必须幂等。再次安装可以刷新当前 module 或 handler，但不能重复包装函数或泄漏旧 handler。

## Go 兼容的 host surface

兼容层提供 Go `js/wasm` 标准库所使用的 `fs`、`process` 和 `path` 子集。

只要 Emscripten FS 有对应 primitive，就需要提供 open、close、read、write、stat、目录、链接、rename、truncate、权限、所有权、时间戳和同步操作的异步与同步形式。不支持的操作必须返回稳定的 Node-style error，不能静默成功。

其他要求包括：

- 把 Emscripten errno 转换为 Go 所消费的 Node-style `error.code` 字符串。
- 保持文件描述符 position 规则，包括 `null`/未指定位置。
- 当 Go host contract 只需要目录项时，从结果中移除 `.` 和 `..`。
- 相对 attached Emscripten FS 的当前工作目录解析路径。
- 提供确定性的浏览器 process identity、groups、umask、cwd 和 chdir。
- 存在 module `print`/`printErr` hook 时，把 stdout/stderr 路由到这些 hook。
- 跨多次 write 增量解码 UTF-8，限制无换行输出缓冲，并在 close、fsync 和 exit 时 flush。
- 在可能触发 memory growth 或保留字节的操作前，把数据复制出可扩容 WebAssembly view。

shim 是兼容适配器，不是替代文件系统。持久化或网络存储仍由 Emscripten 配置决定。

## 回调语义

回调入口分为两类。

### 嵌套同步回调

Go 通过 `syscall/js` 调用 JavaScript，而 JavaScript 在该调用返回前又调用 Go `js.FuncOf` callback 时，该 callback 属于同一个逻辑栈：

```text
Go G -> JavaScript -> Go callback -> JavaScript result -> original Go G
```

callback 必须在 `Value.Call` 或 `Value.Invoke` 返回前执行，其返回值和 side effect 必须同步可见。通用 host dispatcher 识别该嵌套入口，暂时划分当前 Asyncify/Fiber continuation，在调用方 G 上运行 callback，并在 `finally` 路径恢复调用方 host state。

文件系统 `*Sync` 方法应建立在这条通用规则之上，不能再引入私有回调路径。这里由 R4 取代 #2539 针对文件系统的异步 workaround。

同步嵌套 callback 可以在 runtime host-event contract 范围内运行 Go 代码并调度工作。必须等待无关未来 host event 的操作仍受 Go `syscall/js.FuncOf` 文档中的 deadlock 限制；shim 不能 replay 外层 JavaScript 调用，因为那可能重复任意 side effect。

### 外部异步事件

如果 timer、浏览器事件或 promise continuation 到达时没有 Go-to-JS 调用持有当前栈，则 host 把事件入队、设置 pending flag 并唤醒单 worker scheduler。Go 取出事件后在可运行 goroutine 上分发；此时 JavaScript 调用没有同步 Go 返回值。

队列必须保持事件 identity 并保证 exactly-once dispatch。在嵌套调用中释放或替换 callback 时，不能过早释放活动 handle，也不能调用陈旧 replacement。

## 退出与错误语义

Emscripten 用 `ExitStatus` 对象表示进程退出。Asyncify 开始 unwind 后，它可能表现为 module factory rejection、uncaught exception 或 unhandled rejection。

- `os.Exit(n)` 必须以状态 `n` 结束，包括在嵌套 `js.FuncOf` callback 内调用时。
- `ExitStatus` 必须穿过 JS-call adapter 交给 module runner，不能转换成 `syscall/js.Error` panic。
- 后续正常 unwind 不能覆盖此前非零状态。
- 普通 JavaScript exception 仍转换为 `syscall/js.Error`。
- 即使已经打印 success marker，panic、trap、abort、module 初始化失败和延迟异步失败仍必须判为失败。
- 为观察 Emscripten 退出而安装的 host handler 不能吞掉无关 exception。

## 与 #2539 的合并策略

最终实现对每项职责只保留一个 owner：

| 职责 | 选择方向 |
| --- | --- |
| 嵌套 callback 语义 | R4 通用 Go/JS host dispatcher |
| `fs.*Sync` 兼容 | 建立在通用同步 callback contract 上 |
| 浏览器 FS 实现 | 吸收 #2539 更完整的 module-attached shim，包括安全 stdio 和 path 处理 |
| Install-once 行为 | module 和 callback state 共用一个幂等 bridge |
| 输出后缀和陈旧 glue | 吸收 #2539 的完整 output contract |
| R4 现有 shim 片段 | 与选定 #2539 实现等价或更弱时删除 |

集成前应按行为比较两个分支，而不是按 commit identity 比较；经过改写的 patch 即使 hash 不同，仍可能重复同一机制。

## 验证

builder 测试必须覆盖所有输出后缀、sidecar 位置、HTML 注入、原子替换、重复构建、缺失源文件和陈旧 glue 清理。

JavaScript 单元测试必须用 fake module 运行 shim，并覆盖 errno mapping、完整和部分 read/write、position、路径规范化、跨 write 的 UTF-8、受限 stdout/stderr、memory view 替换、同步 wrapper、module 替换和幂等安装。

还必须在 Chrome 中运行真实集成测试；只用 fake module 的单元测试不能证明 browser sidecar 是活代码。浏览器 fixture 必须通过 `wasm_fs.js` 执行真实 Go 文件 I/O，然后覆盖嵌套 callback、timer、process state、正常退出、非零退出以及 success marker 之后的延迟失败。

Node 验收必须覆盖 GJS、EC32 和 EC64，包括同步返回值与 exactly-once 嵌套 side effect、文档允许的 buffered/blocking callback 行为、callback 释放和替换、JavaScript exception、普通 Go 代码和嵌套 callback 中的 `os.Exit`，以及 host operation 之间的 memory growth。

CI 必须同时检查状态码和终止输出。只构建 shim、只把它复制到产物旁边，或只做没有加载真实生成模块的单元测试，都不算覆盖。

## 落地顺序

1. 把选定的 #2539 output/FS 变更 rebase 到当前 R4 基线。
2. 保留 R4 通用 callback dispatcher，并让同步 FS 操作适配它。
3. 删除重复 shim 和 callback 特例。
4. 增加真实浏览器 FS 执行，之后才能声称 sidecar 已受支持。
5. 运行 GJS、EC32 和 EC64 Node/browser 验收，并与变更前基线比较产物体积。
6. Host/output 工作与反射 bridge 分别提交 PR。
