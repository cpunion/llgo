# R4 WebAssembly Compatibility Proposal

Status: draft. Parent proposal: [xgo-dev/llgo#2152](https://github.com/xgo-dev/llgo/issues/2152).

R4 completes the single-worker WebAssembly compatibility layer before later work on WasmGC, threads, multiple workers, and parallel goroutines. Its primary acceptance contract is documented in [`dev/wasmstdlib/README.md`](../dev/wasmstdlib/README.md).

The design is split into independently reviewable subproposals:

1. [Compiler-generated reflection bridges](wasm-reflection-bridges-proposal.md), tracked by [#2556](https://github.com/xgo-dev/llgo/issues/2556), replace the WebAssembly libffi backend while preserving LLGo's target C ABI. The document also separates that ABI from the Component Model's WIT Canonical ABI and defines a future integration boundary.
2. [Emscripten output and Go host compatibility](wasm-emscripten-host-proposal.md), tracked by [#2557](https://github.com/xgo-dev/llgo/issues/2557), defines generated artifacts, the browser filesystem adapter, nested Go/JavaScript callback behavior, and process termination. It also records how the overlapping work in xgo-dev/llgo#2539 is to be reconciled.

These documents are separate because the first is a compiler/runtime calling convention and the second is a host and packaging contract. They may be implemented and reviewed independently, but R4 acceptance tests exercise both.

## Profiles

The subproposals apply to these R4 profiles where relevant:

| Name | Source/API profile | Core Wasm environment | Pointer width |
| --- | --- | --- | --- |
| GJS | Go `js/wasm` | Emscripten host glue | 32-bit |
| GWASI | Go `wasip1/wasm` | WASI Preview 1 | 32-bit |
| EC32 | LLGo Emscripten C profile | Emscripten | 32-bit |
| EC64 | LLGo Emscripten C profile | Emscripten Memory64 | 64-bit |
| WC32 | LLGo WASI C profile | WASI Preview 1 | 32-bit |

GJS and GWASI are source, API, and host-profile compatibility goals. They do not promise binary compatibility with objects emitted by the official Go compiler. EC32, EC64, and WC32 expose the corresponding C ecosystem ABI.

The parent proposal defines nine public IDs. `L32` (`-target wasm`) aliases EC32 and `LW32` (`-target wasip1`) aliases WC32, leaving seven distinct profiles. R4 runs the full hosted compatibility matrix on the five profiles above. The aliases receive target-equivalence and command-path coverage instead of duplicating the full matrix. The remaining independent profiles are `F32` (`-target wasm-unknown`), a preserved freestanding/library profile with no required host, and `P2` (`-target wasip2`), an optional WASI 0.2 Component Model profile; neither is part of R4's hosted Go/standard-library completeness claim.

The five-profile matrix has two independent axes rather than a single nearest-neighbor order. By host and standard-library semantics, WC32 is closest to GWASI because both use WASI Preview 1, the `wasip1 && wasm` source environment, and the WASI import surface. By compiler ABI and runtime implementation, WC32 is closest to EC32 because both are LLGo wasm32 C profiles using the wasm32 C data layout and `internal/cabi`. WC32 is therefore not an alias of either profile: WASI host and polling tests pair it with GWASI, while C ABI, reflection-bridge, and wasm32 layout tests pair it with EC32.

| Host family | Official-Go-compatible ABI | LLGo C ABI |
| --- | --- | --- |
| JavaScript/Emscripten | GJS | EC32; EC64 is its Memory64/LP64 variant |
| WASI Preview 1 | GWASI | WC32 |

## Common acceptance rules

- A feature is not considered implemented unless CI executes it on every applicable profile.
- Host-inapplicable tests must be classified explicitly; absence of selected source files is not a pass.
- Expected failures are still executed. A stale expected failure must fail closed so that it can be retired.
- Compatibility checks include runtime behavior, generated artifact shape, code size, and regressions involving suspension and garbage collection.
- The final R4 change set must be split into reviewable PRs with no diagnostic probes or superseded experiments left in the diff.

---

# R4 WebAssembly 兼容性提案

状态：草案。父提案：[xgo-dev/llgo#2152](https://github.com/xgo-dev/llgo/issues/2152)。

R4 在后续 WasmGC、threads、多 worker 和并行 goroutine 之前，先完成单 worker WebAssembly 兼容层。主要验收约定见 [`dev/wasmstdlib/README.md`](../dev/wasmstdlib/README.md)。

设计拆分为两个可独立 review 的子提案：

1. [编译器生成的反射桥](wasm-reflection-bridges-proposal.md)由 [#2556](https://github.com/xgo-dev/llgo/issues/2556) 跟踪，在保持 LLGo 目标 C ABI 的前提下替换 WebAssembly libffi 后端，并明确该 ABI 与 Component Model WIT Canonical ABI 的边界及未来集成方向。
2. [Emscripten 输出与 Go Host 兼容](wasm-emscripten-host-proposal.md)由 [#2557](https://github.com/xgo-dev/llgo/issues/2557) 跟踪，定义生成产物、浏览器文件系统适配、Go/JavaScript 嵌套回调和进程退出，并记录与 xgo-dev/llgo#2539 重叠部分的合并策略。

二者分开是因为前者属于编译器/运行时调用约定，后者属于 host 和打包约定；它们可以独立实现和 review，但 R4 验收会同时覆盖二者。

## Profiles

两个子提案按适用范围覆盖以下 R4 profile：

| 名称 | 源码/API profile | Core Wasm 环境 | 指针宽度 |
| --- | --- | --- | --- |
| GJS | Go `js/wasm` | Emscripten host glue | 32-bit |
| GWASI | Go `wasip1/wasm` | WASI Preview 1 | 32-bit |
| EC32 | LLGo Emscripten C profile | Emscripten | 32-bit |
| EC64 | LLGo Emscripten C profile | Emscripten Memory64 | 64-bit |
| WC32 | LLGo WASI C profile | WASI Preview 1 | 32-bit |

GJS 和 GWASI 的目标是源码、API 和 host profile 兼容，不承诺与官方 Go 编译器生成 object 的二进制兼容。EC32、EC64 和 WC32 暴露对应的 C 生态 ABI。

父提案定义了九个公共 ID。`L32`（`-target wasm`）是 EC32 的 alias，`LW32`（`-target wasip1`）是 WC32 的 alias，去重后共有七个独立 profile。R4 对上表五个 profile 运行完整 hosted 兼容矩阵；两个 alias 只做 target 等价性与命令路径覆盖，不重复整套矩阵。其余两个独立 profile 是 `F32`（`-target wasm-unknown`，保留的 freestanding/library profile，无必需 host）和 `P2`（`-target wasip2`，可选 WASI 0.2 Component Model profile）；二者都不属于 R4 对 hosted Go/标准库完整性的承诺。

五 profile 矩阵包含两个独立维度，不能只按一个最近邻顺序理解。按 host 和标准库语义，WC32 最接近 GWASI：二者都使用 WASI Preview 1、`wasip1 && wasm` 源码环境和 WASI import surface。按编译器 ABI 和 runtime 实现，WC32 最接近 EC32：二者都是 LLGo wasm32 C profile，使用 wasm32 C 数据布局和 `internal/cabi`。因此 WC32 不是其中任何一个的 alias：WASI host/poll 测试将它与 GWASI 成对验证，C ABI、反射桥和 wasm32 layout 测试则将它与 EC32 成对验证。

| Host 家族 | 官方 Go 兼容 ABI | LLGo C ABI |
| --- | --- | --- |
| JavaScript/Emscripten | GJS | EC32；EC64 是其 Memory64/LP64 变体 |
| WASI Preview 1 | GWASI | WC32 |

## 通用验收规则

- 功能只有在 CI 对所有适用 profile 实际执行后才视为已实现。
- Host 不适用测试必须显式分类；没有选中源码不算通过。
- Xfail 仍要执行，过期 xfail 必须 fail closed 并及时移除。
- 兼容检查包括运行时行为、产物形态、代码体积，以及挂起和 GC 回归。
- 最终 R4 变更必须拆成可 review 的 PR，不能残留诊断探针或已被取代的探索实现。
