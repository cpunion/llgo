# Proposal: Compiler-Generated WebAssembly Reflection Bridges

Status: draft R4 subproposal. Tracking issue: [xgo-dev/llgo#2556](https://github.com/xgo-dev/llgo/issues/2556). Parent proposal: [xgo-dev/llgo#2152](https://github.com/xgo-dev/llgo/issues/2152).

## Summary

WebAssembly reflection uses compiler-generated, signature-specific call and `reflect.MakeFunc` bridges instead of libffi. Reflection still supplies values dynamically, but the final call edge has an exact LLVM function type and is lowered by the same WebAssembly C ABI transformer as an ordinary LLGo call.

This design removes the WebAssembly libffi implementation and its executable closure machinery. Native targets continue to use libffi; direct C calls and cgo interoperability are unchanged.

This document uses **C ABI** for LLGo/LLVM's Core WebAssembly calling convention and **Canonical ABI** only for the WIT/Component Model boundary. They are different protocols.

## Motivation

`reflect.Value.Call` and `reflect.MakeFunc` know a function's types only through runtime metadata. Native LLGo delegates the resulting dynamic call layout and closure entry to libffi. That is a poor fit for WebAssembly:

- Indirect Core Wasm calls are type checked and trap if the table entry's exact function type does not match.
- Portable runtime generation of a new typed code/table entry is unavailable.
- A libffi continuation crossing Asyncify, a goroutine suspension, or a GC safepoint adds a second stack and lifetime protocol.
- Vendoring separate wasm32 and wasm64 libffi backends increases maintenance and binary-size risk while duplicating information already known by the compiler.

LLGo already emits runtime type descriptors for every statically reachable Go function signature. Those descriptors can carry the two exact adapters needed by reflection.

## Goals

- Support `reflect.Value.Call`, `CallSlice`, method calls, method values, and `reflect.MakeFunc` on all five R4 WebAssembly profiles.
- Preserve Go value semantics for scalars, aggregate values, zero-sized values, multiple results, variadic functions, interfaces, function values, and complex numbers.
- Preserve panic, recover, defer, suspension, and GC-root behavior across the bridge.
- Use one compiler/runtime design for wasm32 and wasm64.
- Generate bridges only for reachable function types and let the linker remove unused bridges.
- Keep native reflection on its existing libffi implementation.

## Non-goals

- Replacing libffi on native targets.
- Replacing direct C/cgo calls with reflection bridges.
- Providing a general runtime JIT or allocating executable WebAssembly code.
- Making LLGo binaries link-compatible with objects produced by Go's `cmd/compile`.
- Implementing WIT bindings or the Component Model Canonical ABI in R4.
- Supporting arbitrary C variadic calls through Go reflection.

## Runtime type metadata

On WebAssembly, each statically emitted `abi.FuncType` additionally carries:

```go
type FuncType struct {
    Type
    In, Out []*Type
    Call_   unsafe.Pointer
    Make_   unsafe.Pointer
}
```

`Call_` points to the call adapter for the semantic signature. `Make_` points to an environment-bearing entry with that signature. The compiler emits both with `linkonce_odr` linkage and a COMDAT keyed by the canonical type name, so identical signatures coalesce across packages.

Reflection-created function descriptors first look for an identical static descriptor and reuse its bridge pointers. A descriptor whose complete signature exists only at runtime has no new typed direct-call entry.

## Call bridge

`reflect.Value.Call` performs these steps:

1. Validate and convert each `reflect.Value` as required by the public reflect API.
2. Allocate one typed slot per semantic argument and result, then construct two private arrays containing pointers to those slots.
3. Invoke `Call_` with the target code pointer, optional closure environment, the plain-versus-environment-bearing selector, and both slot arrays.
4. Load every argument using its real static type and emit one of two exact call edges:

```text
result = fn(arg0, arg1, ...)
result = fn(env, arg0, arg1, ...)
```

5. Store each typed result into its result slot. Reflection reconstructs the returned `[]reflect.Value` and resolves indirect result representations.

The address arrays are a private reflect-to-bridge protocol. They are not an external ABI and are never used as the actual target function signature.

## `reflect.MakeFunc` bridge

`Make_` is a normal environment-bearing entry with the exact requested Go signature. Its environment retains the user callback and reflection metadata. On entry it:

1. Roots the environment and all pointer-bearing arguments.
2. Copies arguments into independently owned typed slots because `reflect.Value` arguments may escape the callback.
3. Invokes the generic reflection callback with argument/result slot arrays.
4. Validates and copies returned values into the exact static results.

No executable closure allocation or finite trampoline pool is needed. The existing two-word LLGo function value, `{code, env}`, remains unchanged.

## Closure and method environment

WebAssembly has no hidden static-chain register compatible with LLGo's native `nest`/`swiftself` conventions. An environment-bearing entry therefore has an ordinary leading pointer parameter:

```text
semantic: R func(A, B)
plain:    R entry(A, B)
closure:  R entry(env, A, B)
```

The two Core Wasm function types are different. A dynamic call must branch on the known function-value representation and issue the matching typed call; it must not bitcast one form to the other. Bound methods use the same explicit environment mechanism.

## Core WebAssembly C ABI lowering

After the bridge has produced an exact LLVM signature, LLGo applies its normal `internal/cabi` WebAssembly transformation to both the bridge and the target call. The current contract is:

| Source shape | Lowered Core Wasm C ABI |
| --- | --- |
| Empty parameter | omitted |
| Scalar | direct `i32`, `i64`, `f32`, `f64`, or pointer-width value |
| Struct/array with one scalar leaf | that scalar value |
| Struct/array with two or more leaves, as a parameter | pointer with LLVM `byval` |
| Struct/array with two or more leaves, as a result | hidden first pointer with LLVM `sret`; physical result is `void` |

Multiple Go results form an LLVM aggregate before this transformation and therefore follow the aggregate result rule. Strings, slices, interfaces, and function values retain their LLGo aggregate layout; complex values retain their two-component layout. wasm32 and wasm64 use their respective pointer size and target data layout.

These rules must agree with Clang/LLVM for the resolved target triple. The compiler transforms declarations and every corresponding call site together; pre-lowered `sret`, closure environment attributes, and alignment must be preserved.

## WIT and the Component Model Canonical ABI

The WIT Canonical ABI solves a different problem: transferring typed values across a shared-nothing boundary between WebAssembly components, potentially written in different languages. WIT describes interfaces and worlds with language-neutral types such as records, variants, options, results, strings, lists, flags, and owned or borrowed resources.

The Component Model creates adapters in two directions:

- `canon lift` wraps a Core Wasm function as a component-level function.
- `canon lower` wraps a component-level function as a Core Wasm function that a core module can import.

Canonical lowering first flattens WIT values to Core Wasm scalar values. In the current synchronous ABI, at most 16 flattened parameters and one flattened result are passed directly. Values exceeding those limits use linear-memory parameter/result areas. Variable-length strings and lists carry an address and length and require the selected memory; lowering that allocates storage also requires the selected `realloc`. A lifted export may specify `post-return` so the callee can release temporary result storage after the caller has consumed it. String encoding is an explicit canonical option (`utf8`, `utf16`, or `latin1+utf16`). Variants use a discriminant plus a joined payload layout, and resources cross the boundary as checked handles with ownership rules rather than raw Go pointers.

This protocol is not LLGo's `internal/cabi`:

| Property | LLGo wasm C ABI | WIT Canonical ABI |
| --- | --- | --- |
| Boundary | calls within or imported by one Core Wasm module | calls across component interfaces |
| Source types | LLVM/LLGo physical types | WIT interface types |
| Compound values | C aggregate lowering (`byval`, `sret`, scalarization) | flattening or canonical linear-memory layout |
| Strings/lists | LLGo pointer/length/capacity layouts | encoding-selected address/length representation |
| Ownership | Go runtime and GC rules | Canonical ABI copy, borrow, resource, and `post-return` rules |
| Adapter | compiler C-ABI transform | Component Model `canon lift` / `canon lower` |

A future LLGo component target should layer generated WIT bindings around the Core Wasm module:

```text
WIT value
  -> canon lower/lift adapter
  -> generated Go/WIT binding
  -> ordinary typed LLGo function
  -> LLGo wasm C ABI inside the core module
```

The reflection bridge remains useful inside that module, but must not be reused as the Canonical ABI adapter. Component support needs a separate proposal for WIT-to-Go type mapping, canonical allocation, resources, async interfaces, error mapping, and component packaging. Memory64, threads, and the evolving async Canonical ABI must be feature-gated by actual engine support rather than silently changing R4 profiles.

Normative upstream references:

- [WebAssembly Component Model Canonical ABI](https://github.com/WebAssembly/component-model/blob/main/design/mvp/CanonicalABI.md)
- [WebAssembly Interface Types (WIT)](https://github.com/WebAssembly/component-model/blob/main/design/mvp/WIT.md)
- [Component Model explainer](https://github.com/WebAssembly/component-model/blob/main/design/mvp/Explainer.md)

## Dynamic-only signatures

WebAssembly cannot synthesize a new typed entry point merely because `reflect.FuncOf` assembled a new descriptor at runtime.

- If an identical static function type exists in the linked program, `FuncOf` reuses its `Call_` and `Make_` entries.
- A runtime-only `MakeFunc` value may be invoked through reflection's private fallback because both sides use the slot-array protocol.
- Converting such a value into an arbitrary statically callable Go function, exporting it as a new C callback, or calling it through an unrelated wasm table type is unsupported and must fail explicitly rather than trap through a mismatched `call_indirect`.

This limitation does not affect ordinary Go programs: every function type used by a typed expression, interface conversion, method expression, or direct function-value call is known during compilation.

## GC, suspension, panic, and recover

- Typed argument storage that survives a callback must be independently owned; it cannot alias a bridge stack slot after return.
- Pointer-bearing arguments, environments, and result storage must be visible to the selected WebAssembly collector before any allocation, host call, or scheduler suspension.
- Asyncify or a scheduler yield may occur inside a reflected target without crossing an untracked libffi continuation.
- Generated adapters are transparent runtime frames for direct deferred `recover` semantics, while an additional user wrapper remains an ordinary indirect call boundary.
- Missing bridge metadata must produce a deterministic Go panic identifying the function type; a raw WebAssembly signature-mismatch trap is not accepted.

## Size and performance

The design trades one general interpreter/backend for small per-signature adapters. To control the cost:

- Call and make bridges are deduplicated by signature with COMDAT.
- Type metadata retains bridge symbols only when the corresponding type is reachable.
- Global DCE and wasm section GC remove unreferenced adapters.
- Bridge bodies use typed loads, calls, and stores that LLVM can inline or simplify.
- Binaries that do not use reflection must not retain the generic reflect callback implementation merely because an unrelated function type exists.

R4 acceptance must compare `cprintf`, `println`, `fmtprintf`, and reflection fixtures with the baseline. A large fixed cost in non-reflection binaries or an unbounded per-type increase blocks adoption.

## Validation

Compiler tests must verify generated LLVM types, both plain and env-bearing call edges, wasm32/wasm64 layouts, `sret`/`byval`, COMDAT deduplication, and missing-bridge diagnostics.

Runtime tests must cover:

- Zero, one, and multiple arguments/results.
- Scalars, pointers, strings, slices, interfaces, functions, arrays, structs, zero-sized types, and `complex64`/`complex128`.
- `Call`, `CallSlice`, `MakeFunc`, methods, method expressions, method values, and runtime-created `FuncOf` descriptors.
- Variadic calls and nil values.
- GC during calls and after captured argument escape.
- Goroutine suspension, timer waits, defer, panic, and recover.

The applicable suite must execute on GJS, GWASI, EC32, EC64, and WC32. A compile-only check or a test that links the wasm libffi stub is not acceptance.

## Alternatives

### Vendor WebAssembly libffi

This provides a familiar dynamic interface but requires separate wasm32 and wasm64 maintenance, closure-table integration, precise aggregate ABI mapping, and safe continuation handling. It duplicates compiler type knowledge and is not selected for R4.

### One untyped universal `call_indirect`

Core Wasm validates indirect calls against the exact function type, so a universal pointer-array signature cannot directly call arbitrary typed entries. Bitcasting the table entry only postpones the mismatch to a runtime trap.

### Runtime-generated trampoline pool

A finite pool needs an entry for every possible signature and does not solve arbitrary runtime-created types. Runtime code generation is not portable across R4 hosts. Compiler-generated reachable bridges are deterministic and removable.

---

# 子提案：编译器生成的 WebAssembly 反射桥

状态：R4 子提案草案。跟踪 issue：[xgo-dev/llgo#2556](https://github.com/xgo-dev/llgo/issues/2556)。父提案：[xgo-dev/llgo#2152](https://github.com/xgo-dev/llgo/issues/2152)。

## 摘要

WebAssembly 上的反射调用不再依赖 libffi，而是使用编译器按函数签名生成的 `Call` 和 `reflect.MakeFunc` 桥。反射层仍在运行时提供值，但最终调用边具有精确的 LLVM 函数类型，并与普通 LLGo 调用一样经过 WebAssembly C ABI 转换。

本方案移除 WebAssembly libffi 实现及其可执行闭包机制。原生平台继续使用 libffi；直接 C 调用和 cgo 互操作不受影响。

本文用 **C ABI** 表示 LLGo/LLVM 的 Core WebAssembly 调用约定，只用 **Canonical ABI** 表示 WIT/Component Model 边界；二者不是同一个协议。

## 动机

`reflect.Value.Call` 和 `reflect.MakeFunc` 只能通过运行时元数据获知函数类型。原生 LLGo 把动态调用布局和闭包入口交给 libffi，但这并不适合 WebAssembly：

- Core Wasm 间接调用会检查精确函数类型，table entry 类型不匹配就会 trap。
- WebAssembly 没有可移植的运行时 typed code/table entry 生成方式。
- libffi continuation 跨越 Asyncify、goroutine 挂起或 GC safepoint 时，会引入第二套栈和生命周期协议。
- 分别维护 wasm32 和 wasm64 libffi 后端会增加维护与体积风险，同时重复编译器已经掌握的类型信息。

LLGo 已经为每个静态可达的 Go 函数签名生成运行时类型描述符，这些描述符可以直接携带反射所需的两个精确适配器。

## 目标

- 在五个 R4 WebAssembly profile 上支持 `reflect.Value.Call`、`CallSlice`、方法调用、方法值和 `reflect.MakeFunc`。
- 保持标量、聚合值、零尺寸值、多返回值、可变参数、接口、函数值和复数的 Go 语义。
- 保持跨桥的 panic、recover、defer、挂起和 GC root 行为。
- wasm32 与 wasm64 使用同一套编译器/运行时设计。
- 只为可达函数类型生成桥，并允许链接器移除未使用的桥。
- 原生平台反射继续使用现有 libffi 实现。

## 非目标

- 替换原生平台上的 libffi。
- 用反射桥替换直接 C/cgo 调用。
- 提供通用运行时 JIT 或分配可执行 WebAssembly 代码。
- 让 LLGo 二进制与 Go `cmd/compile` 生成的 object 实现链接级 ABI 兼容。
- 在 R4 中实现 WIT binding 或 Component Model Canonical ABI。
- 通过 Go 反射支持任意 C variadic 调用。

## 运行时类型元数据

在 WebAssembly 上，每个静态生成的 `abi.FuncType` 额外携带：

```go
type FuncType struct {
    Type
    In, Out []*Type
    Call_   unsafe.Pointer
    Make_   unsafe.Pointer
}
```

`Call_` 指向该语义签名的调用适配器，`Make_` 指向具有同一签名且携带环境的入口。编译器以规范化类型名为 key，使用 `linkonce_odr` 和 COMDAT 生成二者，因此跨包的相同签名会合并。

反射创建函数描述符时先查找相同的静态描述符并复用桥指针。完整签名只在运行时出现的描述符不会凭空获得新的 typed direct-call 入口。

## Call 桥

`reflect.Value.Call` 执行以下步骤：

1. 按公开 reflect API 的要求验证并转换每个 `reflect.Value`。
2. 为每个语义参数和结果分配 typed slot，然后构造两个仅包含 slot 地址的私有数组。
3. 用目标代码指针、可选闭包环境、普通入口/带环境入口选择值以及两个 slot 数组调用 `Call_`。
4. 按真实静态类型加载每个参数，并生成两条精确调用边之一：

```text
result = fn(arg0, arg1, ...)
result = fn(env, arg0, arg1, ...)
```

5. 把每个 typed result 写回结果 slot，由反射层重建返回的 `[]reflect.Value` 并解析间接结果表示。

地址数组只是 reflect 到 bridge 的内部协议，不是外部 ABI，也不会成为目标函数的真实签名。

## `reflect.MakeFunc` 桥

`Make_` 是具有目标 Go 精确签名的普通带环境入口，其环境持有用户回调和反射元数据。进入时它会：

1. 把环境和所有含指针参数登记为 root。
2. 把参数复制到独立拥有的 typed slot，因为传给回调的 `reflect.Value` 可能逃逸。
3. 通过参数/结果 slot 数组调用通用反射回调。
4. 验证返回值并复制到精确的静态结果中。

不需要分配可执行闭包，也不需要容量有限的 trampoline pool。LLGo 现有的双字函数值 `{code, env}` 保持不变。

## 闭包与方法环境

WebAssembly 没有与 LLGo 原生 `nest`/`swiftself` 约定兼容的隐藏静态链寄存器，因此带环境入口使用普通的首个指针参数：

```text
semantic: R func(A, B)
plain:    R entry(A, B)
closure:  R entry(env, A, B)
```

两种入口对应不同的 Core Wasm 函数类型。动态调用必须根据已知的函数值表示分支，并发出匹配的 typed call，不能把一种入口 bitcast 成另一种入口。绑定方法复用同一套显式环境机制。

## Core WebAssembly C ABI 转换

bridge 生成精确 LLVM 签名后，LLGo 对 bridge 和目标调用统一执行常规的 `internal/cabi` WebAssembly 转换。当前约定如下：

| 源类型形状 | 转换后的 Core Wasm C ABI |
| --- | --- |
| 空参数 | 省略 |
| 标量 | 直接使用 `i32`、`i64`、`f32`、`f64` 或目标指针宽度值 |
| 只有一个标量叶子的 struct/array | 标量化为该叶子 |
| 含两个及以上叶子的 struct/array 参数 | 带 LLVM `byval` 的指针 |
| 含两个及以上叶子的 struct/array 返回值 | 使用隐藏的首个 LLVM `sret` 指针，物理返回值为 `void` |

多个 Go 返回值会先形成 LLVM 聚合，再按聚合返回规则转换。string、slice、interface 和函数值保留 LLGo 的聚合布局，complex 保留两个分量的布局。wasm32 和 wasm64 分别使用各自的指针宽度和目标 data layout。

这些规则必须与解析后的 target triple 所对应的 Clang/LLVM 规则一致。编译器必须同时转换声明与所有对应调用点，并保留已有的 `sret`、闭包环境属性和对齐信息。

## WIT 与 Component Model Canonical ABI

WIT Canonical ABI 解决的是另一个问题：在可能由不同语言编写的 WebAssembly component 之间，跨 shared-nothing 边界传递类型化值。WIT 用与语言无关的 record、variant、option、result、string、list、flags、owned/borrowed resource 等类型描述 interface 和 world。

Component Model 提供两个方向的适配：

- `canon lift` 把 Core Wasm 函数包装成 component-level 函数。
- `canon lower` 把 component-level 函数包装成 core module 可导入调用的 Core Wasm 函数。

Canonical lowering 会先把 WIT 值扁平化为 Core Wasm 标量。当前同步 ABI 最多直接传递 16 个扁平参数和 1 个扁平结果，超过限制时使用线性内存中的参数/结果区域。变长 string 和 list 以地址加长度表示并要求指定 memory；lowering 需要分配存储时还必须指定 `realloc`。lifted export 可指定 `post-return`，让被调用方在调用方消费完结果后释放临时结果存储。字符串编码是显式 canonical option，可为 `utf8`、`utf16` 或 `latin1+utf16`。variant 使用判别值和合并后的 payload 布局，resource 则通过带所有权规则和检查的 handle 过边界，而不是传递原始 Go 指针。

该协议不是 LLGo 的 `internal/cabi`：

| 属性 | LLGo wasm C ABI | WIT Canonical ABI |
| --- | --- | --- |
| 边界 | 单个 Core Wasm module 内部或其 import 调用 | component interface 之间的调用 |
| 源类型 | LLVM/LLGo 物理类型 | WIT interface 类型 |
| 复合值 | C 聚合转换（`byval`、`sret`、标量化） | 扁平化或 canonical 线性内存布局 |
| string/list | LLGo 的 pointer/length/capacity 布局 | 由编码选项决定的 address/length 表示 |
| 所有权 | Go runtime 和 GC 规则 | Canonical ABI copy、borrow、resource 与 `post-return` 规则 |
| 适配器 | 编译器 C ABI transform | Component Model `canon lift` / `canon lower` |

未来的 LLGo component target 应在线性 Core Wasm module 外再生成一层 WIT binding：

```text
WIT value
  -> canon lower/lift adapter
  -> generated Go/WIT binding
  -> ordinary typed LLGo function
  -> LLGo wasm C ABI inside the core module
```

反射 bridge 在 core module 内仍然有用，但不能被当作 Canonical ABI adapter 复用。Component 支持需要另立提案，定义 WIT 到 Go 的类型映射、canonical allocation、resource、async interface、错误映射和 component 打包。Memory64、threads 以及仍在演进的异步 Canonical ABI 必须根据引擎实际能力做 feature gate，不能暗中改变 R4 profile。

上游规范引用：

- [WebAssembly Component Model Canonical ABI](https://github.com/WebAssembly/component-model/blob/main/design/mvp/CanonicalABI.md)
- [WebAssembly Interface Types (WIT)](https://github.com/WebAssembly/component-model/blob/main/design/mvp/WIT.md)
- [Component Model explainer](https://github.com/WebAssembly/component-model/blob/main/design/mvp/Explainer.md)

## 仅运行时存在的动态签名

WebAssembly 不能仅因为 `reflect.FuncOf` 在运行时组装出一个新描述符，就合成新的 typed entry point。

- 如果链接程序中存在相同的静态函数类型，`FuncOf` 复用其 `Call_` 和 `Make_` 入口。
- 仅运行时存在的 `MakeFunc` 值可以通过反射私有 fallback 调用，因为两端都使用 slot-array 协议。
- 把这种值转成任意静态可调用 Go 函数、导出为新的 C callback，或通过无关的 wasm table type 调用均不支持；必须显式失败，不能让错误变成 `call_indirect` 类型不匹配 trap。

此限制不影响普通 Go 程序：typed expression、interface conversion、method expression 或直接函数值调用所使用的每个函数类型，在编译时都是已知的。

## GC、挂起、panic 与 recover

- Callback 返回后仍可能存活的 typed argument storage 必须独立拥有，不能继续引用 bridge stack slot。
- 所有含指针的参数、环境和结果存储，在任何分配、host call 或 scheduler suspension 之前都必须对当前 WebAssembly GC 可见。
- 反射目标内部可以发生 Asyncify 或 scheduler yield，不再跨越未跟踪的 libffi continuation。
- 直接 defer 调用时，生成的 adapter 对 `recover` 语义是透明 runtime frame；额外用户 wrapper 仍是普通间接调用边界。
- 缺少 bridge 元数据时必须产生包含函数类型的确定性 Go panic，不能接受原始 WebAssembly signature mismatch trap。

## 体积与性能

本方案用每签名的小型 adapter 替代通用解释器/后端。体积控制规则如下：

- Call/Make bridge 通过 COMDAT 按签名去重。
- 只有对应类型可达时，类型元数据才保留 bridge symbol。
- Global DCE 和 wasm section GC 移除未引用 adapter。
- Bridge body 只包含 typed load、call 和 store，便于 LLVM inline 或化简。
- 不使用反射的二进制不能因为出现无关函数类型就保留通用 reflect callback 实现。

R4 验收必须把 `cprintf`、`println`、`fmtprintf` 和反射 fixture 与基线比较。非反射程序出现较大固定开销，或体积随类型数量无界增长，都会阻止采用本方案。

## 验证

编译器测试必须验证生成的 LLVM 类型、普通/带环境两条调用边、wasm32/wasm64 布局、`sret`/`byval`、COMDAT 去重和缺失 bridge 的诊断。

运行时测试必须覆盖：

- 零个、一个和多个参数/结果。
- 标量、指针、string、slice、interface、函数、array、struct、零尺寸类型以及 `complex64`/`complex128`。
- `Call`、`CallSlice`、`MakeFunc`、方法、方法表达式、方法值和运行时创建的 `FuncOf` 描述符。
- 可变参数和 nil 值。
- 调用期间 GC，以及捕获参数逃逸后的 GC。
- goroutine 挂起、timer wait、defer、panic 和 recover。

适用测试必须在 GJS、GWASI、EC32、EC64 和 WC32 上执行。仅编译通过，或只链接 wasm libffi stub，均不算验收。

## 备选方案

### 内置 WebAssembly libffi

它提供熟悉的动态接口，但需要分别维护 wasm32/wasm64、closure table、精确聚合 ABI 映射和安全 continuation。它重复编译器已有类型信息，因此 R4 不采用。

### 单个无类型通用 `call_indirect`

Core Wasm 会按精确函数类型验证间接调用，因此 pointer-array 通用签名不能直接调用任意 typed entry。对 table entry 做 bitcast 只会把错误推迟到运行时 trap。

### 运行时生成 trampoline pool

有限 pool 需要预留所有可能签名，仍无法解决任意运行时创建类型。运行时生成代码也不能跨 R4 host 移植。编译器为可达类型生成 bridge 更确定，并可被链接器移除。
