# LLGo Coroutine 语义标准化 IR 与统一 Lowering 设计

状态：设计与代码审查结论；Phase A 与 Phase B 的首个生产切片已落地：稀疏 LoweringFacts schema、canonical verifier、owner-scoped snapshot 及其 build/cache/manifest identity 已闭环；集中 emission observer、PrimitiveCatalog、CoroOverlay 与生产 emitter 切换尚未完成

更新：2026-07-20

审查基线：`897d251f8`（`cpunion/llgo:llvm-coro`，已包含 Phase 35 / PR #42）

关联总体设计：[`llvm-coro-runtime-design.md`](./llvm-coro-runtime-design.md)

统一异步核心契约：[`coro-async-core-contract.md`](./coro-async-core-contract.md)

Callable、调用点与 foreign boundary 契约：[`coro-callable-contract.md`](./coro-callable-contract.md)

## 1. 结论

方案可行，但不应实现成“在现有全局计划之后，再复制一份完整 Go SSA”。推荐采用两个构建时点、四层窄职责数据，而不是两份可执行 IR：稀疏 `LoweringFacts ledger`、现有 `SSAPlan`、引用原 Go SSA 的 `CoroOverlay` 与 target-configured `VirtualStoragePlan`。

1. 在 emission closure 构建期间，同时生成稀疏 `LoweringFacts ledger`。它冻结有效 Go SSA site 的求值约束、隐藏 runtime helper、调用边、panic/unwind、函数值用途、intrinsic、backend footprint 和地址生命周期事实，但不复制 Phi、普通 value/result、terminator 或完整 CFG。
2. 继续复用现有 `Effect / Exec / Demand / FuncRep / FunctionPlan / CallPlan` 固定点；这些分析的分层是正确的，不应重写。
3. 固定点完成后，把 `LoweringFacts + SSAPlan` 收敛成 `CoroOverlay + VirtualStoragePlan`。普通连续区间只引用原 SSA span；overlay只显式表示 control cut、唯一 continuation、outcome、source-edge mapping 和显式跨层 slot，不预展开标准协议的 LLVM blocks。
4. 由一个 coroutine emitter 按封闭的 protocol template 从 overlay 生成 LLSSA/LLVM IR，继续复用现有 `ssa.CoroBuilder` 和 LLVM `CoroSplit`。
5. 普通 `NoSuspend` 函数在迁移期继续走现有 plain compiler；新 IR 不是第二套通用编译器。

最重要的代码审查修正是：不能采用简单的

```text
EmissionUniverse -> Normalized IR -> AnalyzeSSA
```

因为当前 `EmissionUniverse` 并不是先独立冻结，再发现 hidden helper。`PrepareEmissionUniverseWithOptions` 的 materialization worklist 会扫描函数指令，`materializeLoweredRuntimeHelpers` 会因此加入新的 runtime helper 和可达函数；只有这个闭包稳定后，universe 和 FunctionID 才被冻结。正确结构应是：

```text
package/patch/ABI seeds
        |
        v
ProgramModelBuilder fixed point
  - select effective definitions
  - normalize one owner-scoped function instance
  - discover calls/helpers/children/type demands
  - add newly reached instances and repeat
        |
        +--> frozen EmissionUniverse
        +--> frozen LoweringFacts ledger
                    |
                    v
              existing SSAPlan
                    |
                    v
         CoroOverlay + VirtualStoragePlan
                    |
                    v
          verifier -> LLVM emitter
                    |
                    v
       existing CoroBuilder -> CoroSplit
```

综合审查结论：

- 当前核心方向是正确的：无栈、单 primary body、静态调用透明 await、动态表示与 effect 分离、统一 operation/select/cancel runtime 都应保留。
- 当前代码量偏大的主因不是 LLVM coroutine，也不是 timer 本身，而是 frontend lowering 的同一语义在 helper 预测、effect 分析、physical ABI preflight、frame retention proof 和最终 codegen 中重复解释。
- 只增加 post-plan overlay 能解决 physical CFG 拼装，但不能消除 hidden helper 和 effect 事实的重复提取，收益只有一半。
- 重写 runtime 或 `internal/coro` 固定点不会解决上述问题，风险反而更大。
- 新设计可以成为后续 defer/panic/recover、dynamic coroutine descriptor、syscall/IO、精确 GC metadata 和多平台 adapter 的公共编译器底座；但它本身不会自动补齐这些尚未实现的能力。
- 当前代码仍是受限的 native Linux/Darwin single-P vertical slice，但普通Go 1.26标准库源码风格的`time.Sleep`、冻结timer语义、固定syscall文件回环、高层`os.File`回环和loopback TCP探针已全部compile-link-run通过。这仍不能结论为“完整Go标准库和所有平台已经基本都可落地”：panic/unwind全矩阵、timer GC/synctest、tooling、cgo reentry、affinity、multi-P和平台driver仍需原型或生产验证，见第9、11节及《统一异步核心契约》第9.1节。

## 2. 目标与非目标

### 2.1 目标

- 保持 Go 标准库和用户代码的同步调用风格，不引入源码级 `async/await`、Future 或 Task。
- 保持 LLVM coroutine 无栈约束；suspend 后不得保留 native Go activation。
- 继续坚持一个 source function 只有一个 primary body。`BothDemand` 不得成为复制函数体的理由。
- 让 `Syscall*`、`RawSyscall*`、timer、netpoll 或其他底层 wrapper 的 suspend effect 自动传播到普通 Go caller。
- 把 source evaluation、hidden lowering、全局计划、physical control flow 和 runtime operation lifetime 分成可验证的层。
- 让 timer、文件、网络、host Promise、worker、RTOS notification、IRQ 等扩展复用同一 `Park/Operation` 模型，而不是新增 compiler semantic family。
- 把 select 多候选和执行取消作为公共底层语义，保证结果 lease、loser detach 和 cleanup 次序。
- 降低新增语言特性或 event source 时同时修改多个 compiler 模块的概率。
- 保留当前 LLVM 19–22 支持范围；不为 LLVM 19 以下版本增加设计负担。

### 2.2 非目标

- 不建立第二个包含所有 Go 类型、值优化和普通指令语义的通用 SSA。
- 不在本次迁移中重写现有 `Effect / Exec / Demand / FuncRep` fixed point。
- 不用 IR 重构代替 scheduler、operation、select/cancel、GC 或平台 adapter 的实现工作。
- 不要求新旧 backend 产生 byte-identical object；要求语义、ABI、计划和关键结构等价。
- 不把所有 runtime 校验删掉来追求行数。并发生命周期的 fail-closed 证明不是编译器重复代码。
- 不先加入新的 timer、netpoll 或 defer 功能再验证 IR 架构；迁移前几阶段只做等价重构。

## 3. 当前实现审查

### 3.1 当前流水线

当前生产路径可以概括为：

```text
x/tools Go SSA
  -> cl.EmissionUniverse（patch/owner/symbol/hidden helper closure）
  -> internal/coro.AnalyzeSSA（Effect/Exec/flow/Demand/FuncRep/CallPlan）
  -> cl physical preflight（支持子集、pure SSA、frame retention）
  -> cl.compileInstr/compileValue + coroutine feature lowering
  -> ssa.CoroBuilder
  -> LLVM CoroSplit
  -> runtime scheduler/operation/source/target adapter
```

这个分层已经建立了几个必须保留的正确决定：

- `Effect`、`ExecFlags`、`Demand`、`FuncRep` 和 `Emission` 相互独立。
- 静态 managed call 根据计划选择 direct plain 或 DirectCoro structured await。
- `go` target 是新的 scheduler root，不把 child 的等待 effect 错误传播成 parent 的同步等待。
- 函数值只有进入开放存储、interface、reflect 或 ABI boundary 时才需要 descriptor/dispatch。
- `FuncRep == Dispatch` 不允许产生第二份 source body。
- `G`、frame chain、`P`、`RunDecision`、operation 和 source lifetime 已与裸 LLVM handle 分离。
- producer ABI 使用 pointer-free ID，不把 G、P、frame 或 coroutine handle 暴露给 callback/IRQ。

因此新方案是收敛 lowering，不是推倒重来。

### 3.2 重复解释的代码证据

同一 Go SSA instruction 目前至少在以下位置被重新解释：

- `cl/emission_runtime_helpers.go` 的 `loweredRuntimeHelpers` 根据 instruction、patched type 和 LLSSA lowering 预测隐藏 runtime helper。
- `cl/emission_universe.go` 的 `materializeFunctionForOwner` 再扫描 instruction，扩展 helper、call root、operand function 和 ABI type closure。
- `internal/coro/ssa_plan.go` 与 `internal/coro/func_flow.go` 多轮扫描 body，分别提取 call、value-flow、unknown target、elided call、raw function address 和局部 effect/exec 事实。
- `cl/coro_abi.go` 的 physical preflight 再按 instruction allowlist 计算 await、park、spawn、panic 和 preemption 条件。
- `cl/coro_pure_ssa.go` 为了证明“最终 lowering 不会隐藏调用或 panic”，手工镜像普通 compiler lowering。
- `cl/coro_frame_retention.go` 从特定 prepare/park/retire SSA 形状重新推导 slot、alias、no-preempt span 和 lifetime end。
- `cl/compile.go`、`cl/coro_await.go`、`cl/coro_channel.go`、`cl/coro_spawn.go` 和 `cl/coro_panic.go` 最后再次判断并直接拼装 physical CFG。

多次遍历本身不一定错误；数据流分析本来就可能需要多 pass。真正的问题是多个模块各自维护“这条指令最终会发出什么调用、是否会 panic、在哪里 suspend、怎样进入 continuation”的语义判断。任何一处变化都可能让预测、计划、preflight 和 emission 失配。

当前代码已经用大量 fail-closed 校验阻止静默错编，这说明问题被认真处理了；但这些校验也证明缺少一个单一、不可变的 lowering fact source。

### 3.3 physical CFG 直接拼装的风险

`cl.context.currentCoro` 已进入普通 `compileBlock`、`compileInstrOrValue`、`compileInstr`、allocation、return、panic、channel 和 call 路径。`compileBlock` 同时负责：

- source instruction 顺序；
- preemption budget；
- frame-retention no-preempt span；
- source logical block 到 physical LLVM tail 的映射；
- 普通 compiler 的 cgo、debug、init 和 patch 行为。

`ssa.CoroBuilder.SuspendCurrentBlock*` 为保留 Phi 的 logical predecessor，会修改 logical block 的 physical tail。该接口是合理的 backend primitive，但 frontend 每增加一种 fast-path/park/resume 协议，就必须手工选择 callback、dispatch block 和 join block。

Phase 35 修复过 direct receive resume status 跳回 logical block 首部、重放 receive 前副作用的问题。这个错误不是 channel 算法本身造成的，而是“source logical block”与“suspend 后唯一 physical continuation”没有成为显式、可验证的 frontend 对象。`CoroOverlay` 应直接表达两者，emitter 不再猜 logical tail。

### 3.4 helper closure 与 normalization 的先后关系

`PrepareEmissionUniverseWithOptions` 当前先选择 package/patch definitions，再循环执行：

1. 取稳定排序的 required functions；
2. 对尚未 materialize 的 `(function, use owner)` 调用 `materializeFunctionForOwner`；
3. 扫描 body 并通过 `materializeLoweredRuntimeHelpers` 加入 compiler-inserted runtime calls；
4. 加入 call roots、anonymous children、function operands 和 ABI type demands；
5. 若有新函数则重复；
6. closure 稳定后才排序 functions、冻结 FunctionID 和 foreign certificate。

所以“先完成 EmissionUniverse，再构造 normalized facts”会要求 EmissionUniverse 继续保留一套 hidden-lowering 解释器。推荐让 `ProgramModelBuilder` 接管这个 fixed point，或者先在现有 worklist 内缓存每个相关site的facts，再逐步把下游改成只消费缓存。

### 3.5 owner-scoped 物理上下文

当前 universe 允许同一 canonical SSA function 被多个 use owner materialize。patch 类型、local generic provenance、intrinsic wrapper、physical name 和 link-once ABI 都可能依赖 owner。

因此新模型至少需要两个 identity：

- `FunctionID`：用于 effect、demand、call graph 和逻辑 Go 函数身份；
- `EmissionInstanceID`：`FunctionID + owner identity + patch state + effective ABI/type context hash`，用于 normalization 和 physical emission。

如果多个 instance 对同一 FunctionID 得出的 local effect、hidden managed edge 或函数值 schema 不同，builder 必须：

1. 按语义规则保守 join；或
2. 证明差异只影响物理 name/layout；或
3. 把它们分成不同的逻辑 identity；或
4. 以 fail-closed 方式拒绝。

不能把任意一个 owner 的结果当作全局事实。迁移第一版采用第4项；在CallPlan和physical consumer有明确instance模型前不实现保守join。

### 3.6 frame retention 是通用契约缺失的信号

`cl/coro_frame_retention.go` 当前精确识别两个 native timer symbol、四个 local allocation、prepare/park/retire 的同 block 顺序、alias closure 和只读 span。这个实现作为第一条安全 vertical slice 是合理的，但新增文件、网络或 host operation 若复制这套证明会继续放大 compiler。

应把它抽象为版本化 `SuspendRegionContract`：

- begin/park/end role；
- 可跨 suspend 保留的 slot；
- address/alias closure；
- owner（frame、operation、G）；
- GC policy；
- no-preempt/no-suspend/no-uncontrolled-panic region；
- terminal、rollback 和 lifetime end。

优先方案仍是把 producer 需要长期访问的数据放到稳定 `OperationRecord`，避免借用 caller frame。只有确实需要零分配 frame borrow 时才使用该 contract。

### 3.7 构建、缓存和 ABI

当前 build driver 的顺序是正确的：prepare universe、运行 `CoroPlanBuilder`、验证 plan、生成 `CoroPlanDigest`，再把相同 plan/universe/ABI/target metadata 安装到所有 package compilation 和 cache fingerprint。active lowering 在缺少 canonical digest 时禁止 package cache。

新 IR 必须延续这条 fail-closed 边界。不能只给 in-memory object 增加字段而不更新 cache identity、archive summary 和 descriptor ABI。

### 3.8 代码量事实

相对 merge-base `2c9d1897`，当前基线的相关 production physical diff 约为：

| 模块 | 净新增行 | 判断 |
| --- | ---: | --- |
| `internal/coro` | 6,753 | 大部分是应保留的全局分析、identity 和 digest |
| `cl` | 11,547 | 包含主要的重复 frontend/lowering 边界 |
| `ssa` | 2,361 | 大部分是可复用 LLVM coroutine builder、descriptor 和 metadata |
| `internal/build` | 约 5,000 | 大部分是 whole-program、cache、registry 和 bootstrap 集成 |
| `runtime` | 22,356 | scheduler/operation/source/platform 实现，不会因 compiler IR 自动消失 |

直接的 coroutine ABI/pure-SSA/frame-retention/channel/await/spawn/panic lowering 文件约 3,549 行。这是新 emitter 最可能替换或显著缩小的区域；不能据此承诺删除全部 `cl`、runtime 或 analysis 增量。

## 4. 方案比较

| 方案 | 优点 | 主要问题 | 结论 |
| --- | --- | --- | --- |
| 维持当前 raw SSA 直接 lowering | 无迁移成本；已能运行受限原型 | 每个新特性继续扩展 preflight、helper 预测和 CFG 分支；长期一致性成本高 | 只适合作为迁移参照 |
| 只增加 side-table facts | 改动最小；可先消除 helper/effect 重复判断 | physical continuation、outcome、slot 和 cleanup 仍散落在 emitter | 推荐作为第一迁移阶段，不是终态 |
| 只在 `SSAPlan` 后增加 `CoroOverlay` | 能统一 suspend CFG 和 emitter；迁移较直接 | EmissionUniverse/helper closure 和 AnalyzeSSA 仍需重新解释 raw SSA | 有价值但收益不完整 |
| 稀疏 `LoweringFacts -> SSAPlan -> CoroOverlay` | 单一 semantic fact source；全局分析和 physical lowering 各有清晰输入；为后续 Go control semantics 预留统一位置 | 峰值代码量增加；需处理 owner instance 和 cache schema | 推荐方案 |
| 把 x/tools SSA 改造成 async SSA/CPS | 所有 continuation 都在一层 | 侵入上游 SSA；普通优化、debug、generic 和现有 compiler 全受影响；迁移风险最高 | 不推荐 |
| 新建完整通用 SSA/MIR | 理论上最整齐，可做自有优化 | 重复 Go SSA 的类型、值、内存、debug 和普通 codegen；远超当前问题规模 | 不推荐 |
| 在 LLVM IR pass 中识别调用并插 suspend | 接近 CoroSplit，frontend 改动看似少 | 已丢失 Go 求值顺序、function-value flow、panic/defer、source CFG 和 runtime ownership；跨包 effect 太晚 | 不可作为语义方案 |
| Go 源码到源码 async 改写 | 容易观察生成代码 | 会改变 API/函数类型/标准库调用风格，且无法自然保存 Go panic/defer/reflect ABI | 不符合目标 |

推荐方案不是“越多 IR 越好”，而是只在 raw Go SSA 和 LLVM builder 之间增加目前缺失的两类事实：

- lowering 之前就必须全局可见的稀疏事实清单 `LoweringFacts ledger`；
- fixed point 之后才能确定的 coroutine 控制覆盖层 `CoroOverlay` 与显式 `VirtualStoragePlan`。

## 5. 推荐总体架构

### 5.1 分层

```text
Layer 0  Go SSA / AST directives / patch packages / target layout
Layer 1  ProgramModelBuilder + PrimitiveCatalog
         -> EmissionUniverse + LoweringFacts ledger
Layer 2  existing global analysis
         -> SSAPlan (Effect/Exec/Demand/FuncRep/CallPlan)
Layer 3  CoroPlanner
         -> CoroOverlay + target VirtualStoragePlan
Layer 4  CoroVerifier
Layer 5  LLSSA emitter
         -> existing CoroBuilder / descriptor builders
Layer 6  LLVM CoroSplit and target codegen
Layer 7  runtime scheduler / operation / source / target adapter
```

每层只能增加自己拥有的事实：

- Layer 1 不决定一个函数最终是 plain 还是 coroutine；它只记录局部语义和真实 lowering edges。
- Layer 2 不生成 CFG；它只做全局 fixed point 和表示选择。
- Layer 3 不重新扫描 raw SSA 来发现 helper；它只把已冻结事实和 plan 组合成 physical control。
- Layer 5 不新增 hidden managed call、suspend edge 或 cleanup outcome；发现缺失事实即报 verifier/compiler error。
- runtime 不理解 Go SSA 或 FunctionPlan；它只实现 versioned physical ABI。

### 5.2 target-neutral 与 target-configured

不能把整个 facts ledger 声称为完全 target-neutral。当前 hidden helper、patched physical type、pointer width、ABI alignment、C declaration 和 intrinsic mapping 确实依赖目标及 frontend 配置。

应明确分成：

- 语言/异步语义：call、spawn、panic、defer、park、select、cancel、evaluation order，目标无关；
- lowering facts：effective type、helper target、ABI signature、layout class、frame-borrow policy，按 build target 配置；
- LLVM emission：block、instruction 和 intrinsic 的具体生成，backend-specific。

`LoweringFacts` 可以是一次 target build 的产物，但不包含 LLVM value、LLVM block 或 CoroSplit 后结构。若需要跨 target 比较，只比较明确标注为 language-intent 的 projection，不能假设完整 ledger 或 CoroOverlay 相同。

### 5.3 ProgramModelBuilder fixed point

建议算法：

```text
seed package definitions, roots, runtime ABI declarations and owner contexts

while work queue is not empty:
    take ProvisionalInstanceKey
    select its exact effective body/declaration
    build or fetch sparse LoweringFacts for that instance
    register local effects, calls, values, helpers, ABI/type demands
    add every newly reached function/instance/helper/anonymous child
validate projections of all instances sharing one canonical SSA identity
freeze deterministic logical function/instance order and FunctionIDs
map provisional keys to pointer-free EmissionInstanceID / PrimitiveID
canonicalize references and reject unresolved keys
freeze EmissionUniverse and LoweringFacts together
```

closure 不能用尚未冻结的 FunctionID 构造 `EmissionInstanceID`，否则 identity 存在循环。`ProvisionalInstanceKey` 直接复用当前 `emissionFunctionOwnerKey{function *ssa.Function, owner *preparedEmissionPackage}`，必要时附加 patch/effective-context generation；它只在进程内存在。最终 ID 也不得包含自己的 plan/digest。

迁移初期不必马上重写 `EmissionUniverse`：可以在现有 `materializeFunctionForOwner` 内建立 `LoweringFacts` cache，使现有 closure 仍负责 worklist，但 helper materialization、AnalyzeSSA callback 和 codegen audit 都读取同一份 facts。第一版若同一 FunctionID 的不同 owner 得出不同 local effect、managed edge 或 function-value schema，直接以 fail-closed 方式拒绝；在有明确消费模型前不要先做保守 join。等行为等价后，再把 worklist 抽成 `ProgramModelBuilder`。

Demand、dynamic dispatch 或 host boundary 在 plan 后才确定，但其 thunk/boundary driver 不能在 closure 冻结后突然引入 managed edge。推荐让封闭 `EntryTemplateCatalog` 预先声明每种可能入口的 helper/primitive footprint，并在 closure 阶段按 root、function-value use 和 target capability 保守 materialize 候选；plan 只选择子集。若某类 target driver 无法满足这个约束，必须把 `closure -> plan -> entry footprint` 放入外层单调 fixed point，直到没有新增 instance/helper 后才分配最终 FunctionID 和 digest。

### 5.4 Body、entry 与 layout 必须分离

`BothDemand`、开放函数值和 host/export boundary 可能要求同一个逻辑函数有多个入口，但不能因此产生两份 source body。计划结构应明确分成：

```go
type FunctionArtifactPlan struct {
    Function    FunctionID
    Body        BodyArtifactPlan       // 每个 target link unit 最多一个 defining body
    Thunks      []ThinThunkPlan        // 只做marshal/context装载/跳转
    Boundaries  []BoundaryDriverPlan   // native/host/RTOS/baremetal执行边界
    Descriptors []DescriptorPlan       // 纯数据，不是 entry/body
    Storage     VirtualStoragePlan
}

type VirtualStoragePlan struct {
    Instance   EmissionInstanceID
    TargetABI  TargetABIIdentity
    Signature  PhysicalSignature
    Slots      []VirtualSlot
    Descriptor DescriptorLayout
    ABIDigest  Digest
}
```

`ThinThunkPlan` 只允许参数/结果封送、descriptor context 装载和 primary body 跳转。hard-sync/host crossing 不能错误地都称为“薄adapter”：`BoundaryDriverPlan` 可能需要创建G/frame、驱动或阻塞executor、传播result/panic、处理reentry与线程亲和。它仍是封闭runtime模板，不复制普通source CFG，但实现和平台契约并不轻。Native可以有明确的block-and-drive边界；WASM内部若等待未来Promise，通常必须是Promise/continuation export或声明JSPI/Asyncify能力，不能仅靠同步host ABI等待。RTOS task entry与baremetal main-loop也各有独立driver模板。Go源码保持同步调用风格，不等于所有外部ABI都保持同步。

`DescriptorPlan` 是 capability/ABI 数据，LLVM CoroSplit 自动生成的 ramp/resume/destroy 也不是第二个 Go entry。

单 primary 是 `FunctionID` 级约束，不是 owner instance 级约束。多个 `EmissionInstanceID` 只参与 normalization 和 layout projection；一个 target link unit 中只能有一个 instance 成为 defining `BodyArtifact`。若不同 owner 导致主体语义或 ABI 不同，builder 必须证明可合并、拆成不同逻辑 FunctionID，或以 fail-closed 方式拒绝，不能只 join effect 后各生成一份 body。

`VirtualStoragePlan` 只决定显式 compiler/runtime slot、签名、对齐和 descriptor 物理形式，不预先固定由 LLVM CoroSplit 决定的普通 value frame offset。需要 runtime 取址的 frame-owned slot 通过稳定 alloca/metadata 进入 CoroSplit；split 后再机械产生只读 `FinalFrameLayout/DescriptorMap` 并校验 target layout。这样 32/64 位、native/WASM 和不同 GC profile 的物理差异不会渗入 CoroOverlay 控制规则。

## 6. 稀疏 `LoweringFacts ledger`

### 6.1 最小数据模型

以下是语义示意，不是立即冻结的 Go API：

```go
type LoweringLedger struct {
    Schema    string
    Functions []FunctionLoweringFacts
}

type FunctionLoweringFacts struct {
    ID          EmissionInstanceID
    FunctionID  FunctionID
    Owner       OwnerID
    Source      *ssa.Function       // 仅进程内定位
    Signature   EffectiveSignature
    Sites       []LoweringFact       // 仅有需冻结事实的稀疏site
    LocalEffect Effect
    LocalExec   ExecFlags
    AtomicCost  LocalAtomicCost
    Calls       []CallFact
    Values      []FunctionValueFact
    Regions     []SuspendRegionContract
}

type LoweringFact struct {
    Site        EmissionSiteID
    Source      ssa.Instruction     // 仅进程内定位
    Class       OpClass
    Recipe      SemanticRecipeID
    OperandUse  []OperandConstraint // 只冻结求值/消费约束，不复制value graph
    Helpers     []ManagedEdge
    Panic       PanicFact
    FunctionUse []FunctionValueUse
    Footprint   BackendFootprint
    Contract    ContractID
}
```

上面的 `EmissionInstanceID/EmissionSiteID` 是freeze后的不可变结构。closure期间builder使用 `ProvisionalInstanceKey/ProvisionalSiteKey`；只有FunctionID分配完成并canonicalize全部引用后才构造 `LoweringLedger`，不能在work queue中伪造最终ID。

canonical dump 和 digest 不保存 Go pointer。site identity 分成两层：

```text
SourceSiteID   = FunctionID + source block index
               + non-debug semantic instruction ordinal + subsite/outcome ordinal
EmissionSiteID = EmissionInstanceID + SourceSiteID
```

使用“非 debug 语义指令序号”可避免仅增删调试指令导致全部 site 漂移，但它只保证同一 frontend/CFG schema 下的 deterministic identity，不承诺跨 x/tools 版本或 CFG 重构永久稳定。subsite 优先使用 typed role，例如 `NilCheck`、`FastTry`、`Park`、`Resume`、`Outcome(Panic)`；同 role 重复时才加 ordinal。compiler 插入的 poll 使用 source edge/backedge/path anchor，`SuspendSiteID` 使用 `EmissionSiteID + typed suspend role`，不能依赖 map 遍历、LLVM/planned block 编号或 pointer 地址。非 instruction value 使用结构化 value kind 与同类 ordinal。

这不是一份可独立执行的 CFG。普通 source CFG、Phi、terminator、value definition/result 和 liveness 仍只由原 Go SSA 拥有；ledger 通过 site anchor 引用它们。这里使用“normalized”一词只表示 lowering facts 已冻结，不表示重建 instruction graph。

### 6.2 OpClass

OpClass 应少而稳定：

- `Pure`：`NoSuspend + NoUnwind + NoManagedCall`，并且 backend expansion、allocation/barrier/root 和 atomic retry footprint 已知且满足当前 profile；
- `Lowered`：普通 Go operation，但有已冻结 helper/panic/ABI recipe；
- `Call`：direct plain、direct coroutine candidate、dynamic managed、foreign 或 host call；
- `Intrinsic`：被 frontend 消除或替换的精确 compiler primitive；
- `Spawn`：新的 G root；
- `Channel` / `Select`：需要保持 Go 求值和 commit 语义的语言 operation；
- `Control`：return、defer、panic、recover、Goexit、abort、shutdown；
- `Debug`：source/debug metadata，不影响 effect。

Timer、fd read、socket write、worker job 或某个 syscall number不是新的 OpClass。它们应通过普通 wrapper、generic operation record 和 `Park/ForeignOp/HostOp` primitive 表达。

`Pure` 不能只表示“frontend 没看到 helper”。memcpy、compiler-rt、原子重试、target intrinsic 和 assembly loop 仍可能破坏 bounded-cost 或 GC 假设；recipe 必须提供可信 backend footprint，无法证明时降级为 `Lowered/Call`、增加 poll/offload，或由 post-codegen verifier 执行 fail-closed 拒绝。

### 6.3 LoweringRecipe

仅记录 `ssa.Instruction` 引用还不够，否则 emitter 仍会重新判断 helper。`LoweringRecipe` 至少冻结：

- effective operand/result type；
- operand consumption order；
- exact runtime helper FunctionID 和 logical role；
- 是否有 implicit nil/bounds/divide/type-assert panic；
- call 是否被 elide、inline 或替换；
- ABI/background；
- function-valued definition/use/escape、候选target和边界需求；
- 是否要求 source location、write barrier、GC root 或 no-preempt contract。

迁移期可以由现有普通 lowering 执行 recipe，但必须安装 emission ledger：最终发出的 managed helper、suspend 和 panic edge必须与 recipe 精确相等。之后再逐类把 `PlanX + EmitX(plan)` 从当前 `compileValue` 中抽出。

`RecipeCatalog` 更适合是一组共享实现入口，而不是可编程的大型数据 IR。同一 recipe 实现 `Footprint/Plan/Emit/Verify`：planner 固化其只读结果，emitter 只能消费该结果，verifier 再对真实 emission ledger 核对。这样避免 planner 与 emitter 各写一份 classification，也避免 recipe 字段逐渐演化成另一套字节码。

pre-plan ledger只能冻结 `SemanticRecipe`，不能提前选择 direct plain、DirectCoro、Dispatch或descriptor transport；这些是Effect/Demand/FuncRep/value-flow fixed point的结果。`CoroPlanner` 在SSAPlan之后把semantic facts收敛为 `PhysicalRecipe`，其中才包含最终call mode、physical signature、descriptor和adapter选择。这个拆分避免Layer 1反向依赖Layer 2。

### 6.4 PrimitiveCatalog

当前 hook symbol、capability flag 和 intrinsic semantic 分散在 build、cl 与 runtime ABI glue。推荐增加 compilation-scoped、版本化 `PrimitiveCatalog`：

```text
PrimitiveID
provisional PrimitiveRef -> frozen exact FunctionID / declaration
signature and ABI version
local Effect / Exec seed
lowering recipe
suspend-region contract, if any
required runtime capability
```

catalog 由 exact SSA declaration、link metadata 和 target profile 构建，不能在下游按显示名猜测。closure 期间使用指向exact declaration的 `PrimitiveRef`，FunctionID冻结后才 canonicalize 为 pointer-free `PrimitiveID`。它不意味着所有 runtime API 都变成 compiler intrinsic；绝大多数 wrapper 仍是普通 Go call。只有必须在 caller physical frame 内 stack-cut 的少数 primitive进入 catalog。

## 7. `CoroOverlay / VirtualStoragePlan`

### 7.1 最小数据模型

```go
type FunctionOverlay struct {
    FunctionID  FunctionID
    Body        BodyArtifactID
    Plan        FunctionPlan
    Segments    []SourceSpan
    Cuts        []ControlCut
    Slots       []VirtualSlot
    SourceEdges []SourceEdgeMapping
    Cleanup     CleanupPlan
}

type SourceSpan struct {
    Block          int
    FirstInstr     int
    LastInstr      int
    Materialization MaterializationLedgerID
}

type ControlCut struct {
    ID           SuspendSiteID
    Anchor       SourceAnchor
    Kind         ControlCutKind
    Protocol     ProtocolTemplateID
    Continuation ContinuationID
    Outcomes     []OutcomeEdge
    Slots        []SlotID
    Region       ContractID
}

type VirtualSlot struct {
    ID       SlotID
    Type     EffectiveType
    Owner    SlotOwner       // Frame / Operation / G / Boundary
    GC       GCPolicy        // Scalar / Scanned / Pinned / Opaque
    Lifetime Lifetime
}
```

普通 SSA value 仍由 LLVM SSA 和 CoroSplit 处理。`VirtualSlot` 只描述 compiler/runtime 必须共同理解的显式存储，例如 result slot、wait/select record、completion record、cleanup state、frame-borrow storage 和 boundary packet。不要提前重做 LLVM 的全部 liveness 与 frame layout。

`SourceSpan` 必须引用确定的 source instruction 范围，而不能只有模糊的 first/last value。现有 `compileValue` 可能递归 materialize producer，因此迁移期的 `MaterializationLedger` 必须证明 emitter 不会越过 span 边界发出有副作用 producer。只有 call mode、poll、await、spawn、protocol cut、cleanup 和 terminal control 成为 overlay 节点；map/interface/cgo/debug 等普通 lowering 继续走成熟路径。

overlay 不保存 `FastPath/Prepare/ResumeGate/Reconcile` 等一组 `PhysicalBlockID`，也不复制普通 `PlannedValue`。固定 protocol template 负责展开标准 blocks；overlay只冻结 source anchor、唯一 continuation、outcome、显式 slot 和 logical-to-generated edge mapping。这样可以修复 continuation 重放问题，又不把物理 CFG 在 planner 中完整复制一遍。

### 7.2 physical continuation

每个 suspend site 必须有显式 continuation identity。它解决：

- source logical predecessor 与 CoroSplit 前多个 physical block 的映射；
- conditional fast path是否经过 resume gate；
- resume status读取和 result reconciliation只执行一次；
- channel/select 前置副作用不会因跳回 logical block 首部而重放；
- cleanup edge 与 normal edge不会由 callback 隐式夺取 builder insertion point。

`ContinuationID` 和 `SourceEdgeMapping` 在生成 block 前就固定，standard template 生成的 fast/prepare/resume/reconcile blocks 都必须回填到该 mapping。`ssa.CoroBuilder` 继续负责合法的 LLVM switched-resume shape，但调用它的是统一 emitter，而不是每个 feature 文件各自维护 block tail。

### 7.3 outcome

`OutcomeEdge` 至少覆盖：

- normal return；
- selected operation result；
- operation cancel；
- task abort；
- shutdown；
- panic；
- Goexit；
- fail-closed trap。

resume gate 只取得 transient `RunDecision`；site-local reconciliation 必须先完成 exact ticket、winner lease、payload take/discard、loser detach 或 child `CompletionRecord` 消费，然后才可进入共享 cleanup。这个顺序应由 IR verifier 检查，而不是依赖 emitter review。

### 7.4 OperationRecipe

Timer、fd、socket、worker、host Promise、RTOS notification 和 IRQ source 不应扩展一组平行的 IR opcode，也不应允许任意 hook 列表演化成异步字节码。推荐只有少量封闭 protocol family：

```text
DirectWait             已拥有稳定内部状态的直接等待
RegisteredEventWait    timer/fd/IRQ等可注册外部事件
ForeignWait            worker/offload或foreign completion
HostWait               Promise/JSPI/Asyncify等host边界
WaitSetSelect          多候选claim/commit/cancel/detach
```

`OperationRecipe` 只选择 family、绑定该 family 的固定角色并声明 contract：

```go
type OperationRecipe struct {
    ID          OperationRecipeID
    Family      ProtocolFamily
    Capability  RuntimeCapability
    Bindings    ProtocolPrimitiveBindings
    Commit      CommitModel
    Completion  EarlyCompletionPolicy
    Cancel      CancelStrength
    Result      ResultLeasePolicy
    Quiescence  QuiescencePolicy
    Affinity    AffinityPolicy
    Outcomes    []OutcomeMapping
    Slots       []SlotRequirement
    Region      ContractID
}
```

每个 family 的合法角色和次序由 schema 固定；例如 producer-side publish 不是 coroutine emitter 随意插入的步骤。多候选还必须使用独立 `WaitSetRecipe` 描述伪随机 ready-case 选择、双端 commit、winner lease 和 loser barrier，不能把若干独立 single-wait recipe 简单拼接。feature translator 只能选择 recipe 并绑定参数，不能直接调用 `CoroBuilder` 或临时插入新的 runtime hook。新增 source 若只改变提交、轮询和 payload adapter，编译器 schema 不变。

## 8. Verifier

### 8.1 LoweringFacts 不变量

1. 每个 materialized fact/control cut 有且只有一个 stable site identity；未物化的 source instruction 也能按相同 schema 推导 anchor。
2. operand 按 Go/x-tools SSA 定义的顺序求值和消费一次；recipe 不得重新求值有副作用的 producer。
3. 每个 hidden managed helper、elided call、intrinsic replacement 和 implicit panic 都已冻结。
4. backend 不得从 `Pure` op 发出未登记 managed call、suspend、panic/unwind、未知 allocation/barrier 或无界 backend loop。
5. helper/call target 必须属于共同冻结的 emission closure。
6. provisional instance/primitive ref 在 freeze 后全部解析为 canonical ID；digest 不包含 provisional pointer 或自引用 identity。
7. 同一 logical helper identity 在一个 owner 中只能解析到一个 exact target；不同 owner 的语义差异在第一版以 fail-closed 方式拒绝。
8. local effect/exec 是 op facts 的保守 join。
9. function-value definition/use/escape projection与后续 FuncRep flow输入一致。
10. `SuspendRegionContract` 的 begin/end、slot、alias 和 forbidden operation完整闭合。
11. canonical dump、schema 和 target-configured ABI facts可确定排序。

### 8.2 CoroOverlay / storage 不变量

1. `FunctionPlan`、entry symbol、physical signature和 primary kind一致。
2. 每个 source function最多一个 primary body；thin thunk不复制source CFG，boundary driver来自versioned模板，descriptor只能是data。
3. direct plain、DirectCoro、Dispatch 和 foreign/host call均与 exact `CallPlan`一致。
4. 每个 suspend site只有一个显式 normal continuation；其ID不依赖generated block编号。
5. conditional fast path不经过只属于真实 resume 的 reconciliation。
6. 所有真实 resume先通过正确的 zero-ticket或exact-ticket gate。
7. outcome集合对该 site 完备，未知状态只能到 fail-closed edge。
8. operation/child结果的 take或discard发生在 task cancel cleanup之前。
9. select protocol静态上只暴露一个 selected continuation；loser完成cancel/detach barrier后才允许 ready。
10. runtime在 suspend期间可访问的地址只来自稳定 frame、operation、G或boundary storage。
11. prepare/no-preempt region内没有 suspend、spawn、未登记call或 uncontrolled panic。
12. Phi incoming 使用 source-edge mapping 到 generated continuation 的显式映射。
13. defer参数求值一次，cleanup cursor保证LIFO且 deferred call park后不重复执行。
14. return、panic、Goexit、abort和shutdown保留独立 control kind。
15. 每条静态terminal path对final suspend、completion publication、destroy和frame free各有唯一合法调用位置。
16. slot的GC policy覆盖其完整 lifetime；pointer slot不得被标成scalar。
17. 每个 FunctionID 最多一个 defining `BodyArtifact`；thin thunk不得拥有source body副本，boundary driver必须来自versioned platform模板，descriptor只包含data。
18. `VirtualStoragePlan` 只布局显式 slot 和 ABI 对象；`FinalFrameLayout` 只能在 CoroSplit 后机械产生并校验。
19. 一个函数的整个 coroutine body 只能由一个 backend 拥有，禁止旧/新 emitter 混拼 CFG。
20. runtime ABI、layout hash、primitive schema和IR schema全部进入cache identity。

静态 verifier 只能证明primitive调用次序、ticket/slot传递、continuation/outcome完备和contract覆盖，不能单独证明runtime状态机满足 exactly-once。result lease、detach、quiescence、resume/destroy和并发linearizability仍必须由runtime invariant审计、模型测试、race/shuffle和压力测试证明；两层证据缺一不可。

## 9. Go 语言与标准库能力审查

| 能力 | IR 表达判断（非完成度） | 当前限制与主要工作 |
| --- | --- | --- |
| 普通 direct call | 当前plain/coroutine subset可表示 | explicit-status下managed plain与MayUnwind plain edge未闭环；需第9.3节协议 |
| 递归/SCC | overlay可表示，需原型证明 | 当前physical preflight拒绝recursive lowering；需frame/resource/poll闭环 |
| `go f(args)` | 当前仅受限静态target slice | closure/method/dynamic descriptor、argument transport |
| channel send/recv | 当前已有direct vertical slice | channel operation 默认page为64槽，native profile为16页/1024槽；仍需typed payload GC、完整hchan接线和multi-P |
| 多 case `select` | wait-set overlay适合表示 | native 1024-slot profile下的完整dynamic case、reflect.Select、uniform selection、typed result和P-neutral result packet |
| timer/Sleep | native Sleep source/owner 与 Go 1.26 controlled-timer linkname 原型可表示 | 标准库 E2E 未通过；Timer channel 的 sync-visible `len/cap`、GC、`asynctimerchan`、synctest，dynamic/sharded source |
| 文件/网络 | native Poll/Worker source、deadline/closing 和 scalar result 已接入统一 operation；冻结的regular-file与TCP标准库探针已native single-P E2E通过 | 探针之外的cancel/quiescence竞态、payload/GC、multi-P、完整net/resolver矩阵和其他platform backend尚未闭环 |
| `Syscall*` | Linux/Darwin 固定 wrapper 可经 worker park 自动染色，仍需逐族 contract | 已有 bounded 4-thread/1024-job native worker 与 `{r1,r2,errno}` result cell；全 syscall family、cancel-before-start/背压、pointer GC lifetime 和 E2E 尚未完成 |
| `RawSyscall*` | 不能统一“一律异步” | 逐ABI保留raw、signal-safe、locked-thread或不可重启语义；有contract才stack-cut/offload |
| defer | overlay可表示；当前有静态、无环cleanup子集 | 动态defer栈、完整控制流、递归/SCC和语言矩阵仍未闭环 |
| panic/recover | logical outcome已有task-stop相邻原型 | `CompletionRecord`已覆盖`Return/Panic/Abort/Shutdown`与`ReturnRecovered`提交，但完整panic/recover、`Goexit`和plain/root boundary仍未闭环 |
| Goexit | 可预留独立control kind | defer drain、parent/root传播 |
| closure/method value | descriptor模型可预留 | 当前physical ABI仍拒绝多类closure/method；需capture lifetime与ABI hash |
| interface/function value | value-flow方向可用，dynamic能力受限 | async descriptor、multi-target dispatch、nil check；当前dispatch subset很窄 |
| generics/variadic | identity/schema可预留 | 当前physical ABI仍拒绝部分instance/variadic；需physical lowering与summary冻结 |
| reflect.Call/MakeFunc | 尚未发现模型冲突，必须原型 | runtime type-driven marshal、dynamic descriptor和boundary driver |
| cgo/assembly/linkname | 无统一自动转换 | foreign boundary、stack cut、callback/reentry、unwind和summary contract |
| unsafe pointer | 稳定无栈frame提供基础，必须验证 | 跨suspend address、pin/root/barrier和foreign lifetime |
| GC | IR有助于表达，当前未闭环 | CoroSplit后frame root map、write barrier、STW和collector adapter |
| race detector | 必须原型验证工具ABI | scheduler/channel/source happens-before instrumentation |
| runtime.Caller/Stack/pprof | 必须原型验证logical/native stack合成 | logical frame descriptor和post-CoroSplit debug metadata |
| LockOSThread/entersyscall | 必须原型验证M/P/affinity | multi-M/P、P handoff和worker compensation |
| init/main/host export | 当前仅受限bootstrap | versioned root/boundary lifecycle与各平台driver |

这张表只说明窄 IR 有位置表达问题，不能证明所有语言特性最终可行。当前没有发现“无栈 coroutine + Go 源码同步调用风格”本身的否定证据，但 panic/unwind、logical stack/tooling、GC、cgo reentry 和 affinity 必须由独立原型验证。最重的剩余工作集中在：

- 可挂起 defer/panic/recover/Goexit；
- 完整动态 function descriptor、interface 和 reflect；
- 精确 GC frame metadata；
- syscall/netpoll/worker 组件已有 native vertical slice，但仍需通过 `os`/`net` 标准库全链 E2E、容量/取消/GC 合同和 multi-P；
- multi-P、P-neutral resume packet 和 affinity；
- WASM/WASI/RTOS/baremetal production host adapter。

### 9.1 syscall 自动染色

用户代码无需写 await。若一个 exact syscall wrapper 被 PrimitiveCatalog 或普通 helper call graph分类为 `WaitForeign / MayPark`：

```text
Syscall wrapper effect
    -> caller managed call becomes AwaitStructured
    -> caller function becomes coroutine primary
    -> 继续沿普通静态调用链传播
```

这可以复用保持同步签名的上层 pure-Go 标准库调用链；syscall、runtime、assembly、linkname、cgo和host leaf仍需patch、primitive或backend contract，不能直接表述为“完整标准库无需适配”。需要避免两个错误：

- 不能把所有 syscall 都改成 readiness wait；只有 `internal/poll` 一类有明确 wait+retry contract 的路径才适合 readiness token。
- 不能在普通同步 helper 内执行 `llvm.coro.suspend`；stack cut 必须位于当前 physical coroutine frame，或将完整操作放到 worker/host operation 后 park。

### 9.2 抢占上界不能只统计当前函数

当前 `MaxPlainInstructions` 和 coroutine body中的block/指令budget能发现本地循环与长block，但它不是跨普通调用闭包的 `MaxAtomicCost` 证明。一个coroutine可以连续调用很深的无环plain函数链；每个callee单独很短，合计仍可能长期没有poll。外部C函数即使被证明nonblocking，也不等于执行时间有界。

等价迁移完成后，应在 `LoweringFacts` 上增加 whole-program 单调分析。它不是简单把一个全函数最大值相加，而是在 CFG 上计算“从上一个 cut 到下一个 cut”的最长路径；每个 direct plain call site 用该 callee 的无 cut cost 替换，并按路径出现次数累计：

```text
AtomicCost(fn) = longest weighted no-cut path over CFG
call-site weight = direct plain callee AtomicCost

poll / await / park / host return = cut
recursive plain SCC / unknown cost / overflow = unbounded
```

规则：

- 本地CFG环若没有cut，函数需要 `NeedsPreempt`；
- recursive plain SCC视为unbounded，除非是显式trusted bounded runtime island；
- direct plain callee的cost计入caller最长atomic path；
- dynamic/open call没有可验证summary时视为unbounded；
- foreign no-block certificate若要保留在managed plain closure，还必须携带bounded-cost或opaque/offload结论；
- 超过target/profile budget时，函数单调提升为 `NeedsPreempt` coroutine primary，或在合法边界增加poll；本次plan中promotion只能增加、不能因插入poll后cost下降而撤销；
- 在未插入新poll的source/call closure上先求潜在cost并冻结promotion，再生成poll并验证实际gap，避免plain/coroutine振荡；
- primary/call mode变化后重新求解，直到Effect、Exec、Demand和AtomicCost共同稳定。

`CoroOverlay` 随后验证所有环和最长无 poll 路径。这样“抢占式”才具有可审计的 safepoint 延迟上界，而不只是函数内插点启发式。Phase B–F 为保证计划/ABI等价，只做 report-only 计算；真正参与 `NeedsPreempt` 的变化属于后续独立功能阶段。

还需要两层cost proof：user CoroOverlay证明managed source path的no-poll gap；runtime hook/executor证明单次 `findFrame`、park-link/select扫描、source drain和reduction budget有界。仅把caller提升为coroutine并不能中断已经进入的超预算plain callee；每条plain edge都必须证明callee cost不超过剩余budget，否则实际callee/SCC也要变成可poll coroutine、offload，或被拒绝。post-LLVM footprint与runtime cost certificate因此仍是完整抢占上界的必要条件。

### 9.3 plain call 与 panic/unwind 协议

这是当前方案最重要的独立实现障碍之一。baseline中defined body被保守赋予 `MayUnwind`；explicit-status production path拒绝managed plain emission，也拒绝coroutine内没有hidden-outcome contract的direct plain call。正常build driver仍把explicit-status panic视为identity-only并报错；现有panic lowering是focused/manual vertical slice，不代表production已闭环。

推荐的终态是managed logical outcome ABI，而不是让native unwind穿过已切栈的coroutine边界：

1. `SemanticRecipe`冻结implicit/explicit panic与defer/recover事实，但不选择物理call mode。
2. 能证明 `NoUnwind` 的短plain island继续普通调用。
3. 可能unwind的managed primary使用隐藏 `Outcome/CompletionRecord` 物理协议；它仍只有一个source body，不为同步/异步consumer各复制一份。
4. coroutine caller在site-local reconciliation处理return/panic/Goexit；defer cursor按LIFO执行并允许deferred managed call挂起。
5. hard-sync/native/host `BoundaryDriverPlan` 把logical outcome转换成该边界允许的panic/error/trap/Promise rejection；foreign LLVM EH只在有明确personality和reentry contract的边界使用。

在status-return primary、统一cleanup ABI或可靠LLVM EH bridge至少有一个原型通过前，不能同时承诺“任意managed plain call保持现状”和“完整panic/recover兼容”。这属于Phase G功能，不应混入LoweringFacts等价迁移。

## 10. Runtime 全面审查与边界

### 10.1 应保留的核心

以下 production 代码不是 compiler IR 重复，应作为稳定 runtime contract继续演进：

- `G`、`P`、frame chain、ready/wait ownership；
- `Action`、`RunDecision`、resume/destroy exactly-once protocol；
- `OperationID` 的 pointer-free source/route/local/generation identity；
- `OperationRecord` 的 completion、cancel、detach、quiescence和result lease；
- G-owned `ParkState`、frame-local `WaitSetRecord` 和 affected FIFO；
- multi-candidate select claim、winner、loser cancel/detach barrier；
- task abort/shutdown sticky cancellation；
- bounded executor/source cursor、A/ack/B 防丢唤醒和idle transaction；
- target ingress seal/join和producer admission。

这些状态看起来繁重，是因为 completion、cancel、selected result、late callback、shutdown 和 physical quiescence确实是相互独立的事实。把它们折叠成一个 `done` bool 会重新引入 use-after-free、lost wake 或重复消费。

取消必须区分四层：`context`取消是库级值传播；operation cancel撤销一个外部注册；task abort是scheduler在安全点观察的cooperative stop；shutdown是executor/root生命周期。当前 `TaskCancel` 不是任意goroutine强杀，也不等同于context/operation cancel。compiler cancel gate现已对zero-ticket和Channel/Worker/Timer/Poll恢复保留精确`Abort/Shutdown`，进入当前静态、无环cleanup子集，并经parent-owned `CompletionRecord`跨child destroy逐层传播；这仍不能外推为动态defer、完整panic/recover或`Goexit`。终态必须继续声明观察safepoint、不可取消区、irreversible effect/result lease、defer drain和destroy barrier；没有这些协议时，不能承诺任意执行取消。

### 10.2 可进一步收敛的部分

- `WaitToken/WaitRegistration` 与 V2 `ParkState/OperationID` 当前并存，但 `WaitRegistration` 仍承担 ExecutorDriver 的平台 wait/idle ingress，不能把所有带 V1 名字的机制统一视为 legacy。先建立 symbol/caller/replacement matrix，只有某条 producer、timer/wait 或 whole-episode compatibility path 已有逐项替代且无调用者后才删除。
- `ExecutorSourceSet` 已有统一协议，但当前手写 `if source != nil` catalog。按既有设计应由 target profile生成静态 direct-call catalog，避免每加一个source手改executor，又不引入Go interface dispatch。
- Primitive/hook ABI应由 versioned catalog统一生成 compiler declaration、runtime export、signature validation和digest identity。
- 完整结构审计应保留在构造、debug、test和terminal边界；热路径只做已认证的O(1) header/local-link校验，继续遵守现有cost certificate方向。
- fixed small source capacity适合prototype；target-neutral wait/timer/poll/worker/channel/keyed-wait 的默认 page 是64槽，当前 native Timer 独立配置64页即4096槽，其余 common source 配置16页即1024槽，worker 使用4个物理pthread与1024-job ring，task-control仍为8槽。timer/poll当前仍会扫描配置容量；后续native可用heap/ready index/sharded catalog，embedded/baremetal用显式静态容量和固定heap/ring。容量策略不应改变compiler IR。
- source-specific payload处理应落在 source/operation adapter；compiler只理解slot ownership和reconciliation contract。

### 10.3 新 IR 与 runtime 的唯一接口

新 IR 不直接操作 scheduler fields，只生成 versioned primitive calls和显式slots：

```text
frame create/publish
spawn begin/commit
await prepare
operation prepare/park
run-decision take
result reconcile/take/discard
complete/panic/goexit/cleanup publish
frame final suspend/free
```

新增 timer、poll、host 或 worker source不应要求增加 IR opcode；只有出现新的语言控制语义或跨层ownership contract时才扩展 IR schema。

compiler/runtime内部ABI可以进一步直接传稳定 `Frame*` 或等价 `FrameRef`。当前frame allocation已经在LLVM storage前保存back-pointer，但 `PrepareAwait/Complete/Yield/Park/ParkSet` 仍按handle在线性frame链中执行 `findFrame`。深structured-await链会把本应O(1)的transition变成线性查找。

这不是一个无版本替换：当前 header 没有 `Frame*`，compiler主要持有storage/handle。需要独立 runtime ABI 提案，明确 header/alloc 版本、`FrameFromStorage` 或显式 ref 取得方式、destroy validation、GC稳定性和旧新调用面。`FrameRef` 只在受控compiler/runtime调用内使用；外部producer仍只能持有pointer-free `OperationID`，两者不能混淆。

### 10.4 更轻量 Runtime V3 候选

代码审查还提出了一个更激进的候选：

```text
Task/G + Frame chain
Executor(local deque + MPSC injection + timer queue + doorbell)
Park(atomic winner/cancel/once-enqueue)
Op(frame-spilled for internal waits, registry-backed for external callbacks)
```

其中有几项可以较确定地独立推进：

- 按replacement matrix逐条删除已确认无调用者的legacy path，不按V1/V2名字批量删除；
- channel queue node与pthread waiter分离。当前coroutine waiter也携带未使用的pthread mutex/cond，每个select case会不必要地放大coroutine frame；
- native timer从固定容量全表扫描改为heap或sharded queue，静态target使用固定heap/ring；
- 内部channel waiter直接使用frame-spilled稳定op，外部callback才使用versioned registry；
- native多P增加MPSC/global injection，WASM/embedded/baremetal映射为各自doorbell或IRQ ring。

“让producer直接CAS Park winner并enqueue”有潜在减码价值，但目前不能判定优于现有owner-sidepublished-epoch resolver。至少需要证明：

- select进入时对已ready cases保持Go伪随机选择，而不是被source扫描或callback先后顺序永久偏置；
- channel-to-channel/select-to-select的双端物理提交不会出现只赢一端、effect后回滚或两个Park半提交；
- cancellation覆盖selected continuation时，已发生的物理effect和result lease仍按 exactly-once 规则 discard；
- 所有loser从hchan/source摘除且producer strong-quiesced之后才允许frame destroy；
- producer跨线程访问的Park/Op字段具备稳定地址、GC root和正确memory ordering；
- producer不持有裸G/P/frame pointer，而是通过稳定registry lease取得P-neutral `ResumePacket`；packet在multi-P迁移、GC barrier和frame teardown期间保持有效；
- native global/MPSC injection、registry pin和目标P选择在producer可直接enqueue前已经闭环；
- idle arm、request coalescing和once-enqueue不会丢唤醒或形成ABA。

因此建议把Park V3作为IR等价迁移后的独立feature-flag实验：只在deterministic fake source下用旧resolver比较允许outcome；真实并发trace不要求逐步相同，而用mixed channel/timer/select/cancel、双select pairing和frame teardown压力测试验证linearizability、exactly-once和quiescence不变量。没有这些证据前，不应为了行数直接删除A/ack/B和detach/quiescence层。

## 11. LLVM backend 与平台兼容性

### 11.1 LLVM

`ssa.CoroBuilder` 已经正确封装：

- frame alloc/free和alignment；
- initial/final suspend；
- conditional suspend；
- resume dispatch gate；
- logical block physical tail；
- `coro.done/resume/destroy/promise`；
- descriptor和root metadata。

它应保留为 LLVM backend，不应把 Go effect、select或cleanup语义塞入其中。新 emitter可减少 feature callback数量，但无需重写 `CoroBuilder`。

LLVM CoroSplit继续负责普通 SSA liveness和frame materialization。精确 GC需要在CoroSplit后取得可靠frame layout/root metadata，或在显式slot层为GC-managed值提供自己的descriptor；这需要LLVM 19–22分别验证，不能仅凭pre-split IR推断最终offset。

抢占仍是 compiler safepoint preemption，不是任意PC抢占。CoroOverlay可以更可靠地证明循环、递归SCC和长block的poll上界，但LLVM stackless coroutine不能在signal/ISR中保存普通native activation。

### 11.2 平台现状与兼容性

| 平台 | 核心模型判断 | 当前实现现实 |
| --- | --- | --- |
| Native Linux/Darwin | layout/ownership无已知冲突 | 已有single-P pipe doorbell/POSIX `poll`、monotonic timer、bounded worker、semaphore/notify、channel/select vertical slice和native runner；fleet Timer/Poll exact route及Poll callback ingress已闭环，五个冻结标准库探针已E2E通过，但尚无程序级multi-P、双domain reactor arm、完整GC/cleanup与GOROOT矩阵 |
| 其他native OS | 尚未审查 | Windows/BSD/mobile production adapter、thread/IO/ABI均未验证 |
| JS/WASM | layout/ownership无已知冲突，可映射为1P host `RunSlice` | 32-bit layout、pre/post-CoroSplit/object和test adapter有覆盖；production queued run/timer/Promise/IO adapter未实现，仍走fail-closed fallback |
| WASI | operation模型可映射，未验证 | pollable/poll_oneoff、filesystem/socket/clock production adapter未完成 |
| RTOS/embedded | 静态执行模型可映射，未验证 | HAL clock/notification/ISR ingress、boundary driver和容量证明都未实现 |
| baremetal | event-loop模型可映射，未验证 | main loop、IRQ mailbox、WFI/WFE、static/tinygc frame和production adapter都未实现 |

架构不要求每G native stack、libuv、BDWGC或pthread，但这只是兼容候选，不是平台完成度。当前production target adapter实际只有llgo native Linux/Darwin single-P，其bounded blocking worker使用固定pthread pool；`coro_target_none.go` 对queued host run与retained wait采用fail-closed行为。双native domain已与该single-P entry共用唯一物理run-step reducer，并可执行/迁移P-neutral yield任务，但尚未取代program entry或提供并行M owner。Worker也已保持单物理池并用每job的既有`OperationID.Route`支持fleet completion，不做函数地址反查；compiler-reserved profile将program/fleet回调静态分开，两者均已通过真实LLGo raw-plain plan验证。它尚未由program-level fleet coordinator启动，不改变当前single-P完成度结论。缺少filesystem、process、socket或host async能力的平台仍按target capability决定可用package。LLVM支持范围只是19–22，不考虑19以下版本。

## 12. Cache、archive 与 summary

建议增加：

- `LoweringFactsSchema`；
- `CoroOverlaySchema`；
- `VirtualStorageSchema`；
- `PrimitiveCatalogSchema`；
- 必要时 `ProgramModelDigest` 或扩展现有 `PlanDigestSchema`。

cache identity必须覆盖所有会改变物理IR的事实：

- canonical FunctionID/EmissionInstanceID；
- normalized helper、elided call、panic和function-value sites；
- FunctionPlan/CallPlan；
- suspend site kind、contract、slot/GC policy和outcome；
- target triple/CPU/features/ABI/data layout；
- coroutine、scheduler、panic、FuncRep和runtime primitive ABI。

迁移期可以继续使用全局 `CoroPlanDigest`，把新 schema 和 canonical facts 纳入同一 document，但内部应保留两个可单独诊断的 projection：

- `LoweringSemanticDigest`：本次 target build 的 source site、call/effect/demand/representation、CoroOverlay、outcome 和 contract identity；
- `TargetLayoutDigest`：target triple/data layout、effective signature、owner/patch resolution、explicit slot layout、descriptor/primitive ABI、GC profile 和 LLVM compatibility profile。

最终 cache key 组合 schema 版本与两个 digest。target-configured helper 或 intrinsic 若会改变 semantic projection 和 layout projection，则同时进入两者，不能为了跨 target 复用而丢失事实。当前FunctionID本身包含coroutine/scheduler ABI与最终link identity；若需要跨target比较，必须另建不含这些字段的 `SourceFunctionKey`，并生成只含logical primitive/contract的诊断 projection，它不参与artifact复用。这样的拆分首先用于定位“计划变化”还是“物理布局变化”，不是承诺不同 target 必然共享 artifact。

**历史基线说明**：本迁移方案最初成文时 `PlanDigestSchema` 为 v8，文中原定的v9/v10只是
当时为LoweringFacts与overlay预计的相对里程碑，不再是当前版本路线。2026-07-22接入前代码中
`PlanDigestSchema` 已是 `llgo.coro.plan-digest.v25`；其中已包含现有FunctionPlan/CallPlan事实、
managed-required exact C declaration的total `CallableIdentityCertificate`、content-addressed
`CallableContractCertificate` 和exact TrustedInline invocation certificate等后续已落地信息。
identity inventory与execution policy保持正交：未标注/legacy declaration的content-addressed unknown
behavior只进入facts/catalog，不额外改变其调度策略。本轮把当前已覆盖的canonical LoweringFacts
schema与digest接入production plan、Compilation、package fingerprint和manifest后，schema升级为
`llgo.coro.plan-digest.v26`。v26仍不表示集中EmissionLedger observer、PrimitiveCatalog、CoroOverlay或
VirtualStoragePlan已经实现。

后续不预留空v22/v23字段，也不沿用历史v9/v10标号。只在某一层的canonical事实真正进入
production plan/cache identity时，从届时当前schema递增一次。每次升级都验证：任一相关fact
mutation改变cache key、source compile与cache registration使用同一digest、旧schema只产生
cache miss而不是被接受。详细JSON dump按诊断开关生成；cache key使用per-function canonical
digest/Merkle汇总，避免把所有普通operand/type再次序列化进全局document。

长期若全局 digest 导致任意函数变化使所有 package cache 失效，可进一步拆成：

- archive producer summary：exported/address-taken函数的effect、exec、FuncRep schema、ABI和primitive依赖；
- package-local IR digest；
- link-wide root/closed-world plan digest。

不能用仅供诊断的 summary代替独立archive ABI，也不能让linker重新解释未知producer的function-value物理布局。

## 13. 代码迁移映射

| 当前位置 | 迁移后职责 |
| --- | --- |
| `cl/emission_universe.go` | 保留package/patch/owner/symbol选择；worklist逐步迁入ProgramModelBuilder |
| `cl/emission_runtime_helpers.go` | helper预测变成LoweringRecipe planner；不再由preflight/codegen各自镜像 |
| `internal/coro/ssa_plan.go` / `func_flow.go` | 保留fixed point；逐步从LoweringFacts projection读取call/value/local facts |
| `cl/coro_abi.go` | ABI descriptor、签名和少量边界保留；instruction allowlist和计数迁入verifier/planner |
| `cl/coro_pure_ssa.go` | 被recipe ledger和LoweringFacts verifier替换 |
| `cl/coro_frame_retention.go` | 精确timer特例迁成通用SuspendRegionContract planner/verifier |
| `cl/coro_await.go` / `channel.go` / `spawn.go` / `panic.go` | 转成CoroOverlay construction规则；LLVM block拼装移入统一emitter |
| `cl/compile.go` | plain path保留；移除分散的`currentCoro`分支，coroutine body交给新emitter |
| `ssa/coro.go` | 保留LLVM builder/descriptor backend |
| `internal/build` | 安装ProgramModel/plan/digest/schema并维护cache/registry/bootstrap |
| `runtime/internal/coro` | 不因IR迁移重写；按独立计划删除legacy并扩展source/multi-P |

建议新增的包/文件边界：

```text
internal/coro/ir/          schema, IDs, dump, verifier
cl/coro_model_builder.go   SSA + frontend context -> LoweringFacts
cl/coro_planner.go         LoweringFacts + SSAPlan -> CoroOverlay/VirtualStoragePlan
cl/coro_emit.go            CoroOverlay -> LLSSA
cl/coro_recipe_*.go        ordinary lowering recipe planning/emission pairs
```

`internal/coro/ir` 可以在进程内持有 x/tools SSA引用，但不得依赖 LLSSA/LLVM。需要canonical dump的结构使用pointer-free site ID。稀疏ledger节点数、bytes/function和peak RSS必须作为硬观测，防止它逐渐复制完整SSA。

## 14. 迁移计划

### Phase A：冻结基线与观测

- 以 `897d251f8` 为迁移基线，不混入新runtime功能。
- 先实现plan/semantic CFG canonicalizer；只为小型代表fixture保存plain/await/preempt/park/timer/channel/select/spawn/panic投影，不保存整个支持subset或完整post-CoroSplit文本；panic标注为focused/manual fixture，不冒充production build path。
- 各LLVM版本分别做module verify和结构断言；frame/object size记录版本内基线与阈值，不要求LLVM 19–22文本或精确size相同。
- 定义固定fixture/target、warm cache、重复次数/中位数、alloc和peak RSS采集方式；先报告compile wall、node/bytes、block/instruction、frame和object size，取得噪声后再冻结回退阈值。

验收：不改生成IR。

### Phase B：LoweringFacts ledger

- 在现有 EmissionUniverse materialization中只为Lowered/Call/Intrinsic/Control、function-value、implicit panic和SuspendRegion等owner-scoped site生成稀疏facts；普通Pure span仅存range与recipe/footprint hash。
- helper、intrinsic、panic、function use和frame-region proof全部进入稳定dump。
- 先增加集中 `EmissionLedger`：编译source instruction前安装 `EmissionSiteID`，managed helper resolver、explicit coroutine feature、panic/suspend都通过统一record API；若LLSSA调用无法集中观测，则增加call/control observer。未接入observer的类别只能标为尚未覆盖，不能宣称全量精确比较。
- 将 canonical LoweringFacts/PrimitiveCatalog digest接入 `SSAPlan.CoroPlanDigest`、
  `internal/build.buildCoroPlan`、`cl.Compilation`、fingerprint和manifest；在事实真正进入
  cache identity时从届时当前schema升级，不使用历史预留的v9号。

验收：FunctionPlan、可执行LLVM CFG、runtime ABI和运行行为不变；cache/manifest digest与相关
metadata按当次新schema预期变化；已接入observer的预测/实际差异触发fail-closed拒绝；
fact mutation/cache schema测试通过。

### Phase C：analysis只消费facts

- 先替换 `ClassifyLoweredCalls`、`ClassifyElidedCall`、intrinsic和local effect输入。
- 再逐步替换call/value-flow扫描的重复classification；必要的数据流pass仍保留。
- report-only计算跨plain调用闭包的MaxAtomicCost，记录与当前instruction budget的差异，但不改变NeedsPreempt、primary或poll。

验收：旧/新 plan、roots、FunctionID、CallPlan、ValuePlan和digest projection一致。

### Phase D：生成CoroOverlay

- 仅覆盖当前preflight已接受的函数。
- 显式生成poll、await、park、channel/select、spawn、return/panic的control cut、continuation、outcome和virtual slot；不预展开physical blocks。
- 新 verifier独立运行；生产仍使用旧emitter。
- overlay/storage真正进入digest时，再从届时当前schema升级；不为尚不存在的层
  提前放空字段，不使用历史预留的v10号。

验收：每个旧支持函数都能产生合法、稳定的overlay dump；旧拒绝用例继续拒绝；除当次
digest schema/metadata变化外，可执行LLVM CFG不变。

### Phase E：双backend对照

- 新 coroutine emitter使用现有 CoroBuilder。
- plain function继续旧路径。
- 对同一fixture执行两个独立compile/module invocation：legacy读取raw SSA，新backend读取overlay，避免同名symbol在一个module双发。
- 比较canonical semantic projection、suspend/continuation/helper/descriptor、post-CoroSplit verify、frame阈值和运行结果，不要求physical CFG同构。

验收：native+nogc E2E、host race/shuffle、JS/WASM test adapter、native64/wasm32、LLVM 19–22全部通过。

### Phase F：按完整函数切换并删除重复实现

- 按whole-function eligibility cohort依次切换pure-only、preempt、await/spawn、park/channel/select；一个函数的全部SuspendKind均被新backend支持后才切换。
- 禁止同一coroutine physical body按op/feature混用两套emitter，否则Phi、continuation和logical tail没有唯一owner。
- 删除旧instruction allowlist、pure SSA镜像、直接CFG callback和timer专用proof。
- 将 `currentCoro` 收缩到新emitter内部。

验收：production只存在一条coroutine physical emission路径。

### Phase G：在新IR上补语言能力

优先顺序建议：

1. 让MaxAtomicCost真正参与单调NeedsPreempt/poll计划；
2. generic operation reconciliation和CompletionRecord；
3. defer/panic/recover/Goexit cleanup；
4. dynamic coroutine descriptor、closure/method/interface；
5. syscall/netpoll/worker/host sources；
6. precise GC/debug metadata；
7. P-neutral packet、多P/affinity；
8. reflect和完整平台adapter。

Phase B–F严格保持plan、runtime ABI和可观察行为不变；这部分才是新功能开发，不应混为一个巨大PR。PrimitiveCatalog生成器重构和Runtime V3也分别立项，不塞入等价迁移。

## 15. 成本、收益与性能

### 15.1 迁移代码量估计

基于当前文件分布的保守估计：

- schema、dump、verifier：新增约 1.5–2.5k production LOC；
- current subset translator/planner：新增约 1.0–1.8k；
- unified emitter：新增约 1.2–2.0k；
- 迁移峰值新旧并存：三项算术合计约 +3.7–6.3k production LOC；
- 稳定后删除/收缩旧audit和direct lowering：约 -3.0–4.7k；
- 稳定态相对当前只能粗估为 -1.0k 至 +3.3k production LOC，另有2–4k测试。

这个区间尚未计入完整ProgramModelBuilder worklist重构、catalog生成器、digest分层和MaxAtomicCost新功能；它们可能与已有代码替换重叠，也可能净新增，因此不能用单点数字承诺最终行数。更轻的sparse ledger/control-cut overlay正是为了把峰值和稳定维护面压在这个量级，而不是再增加一份完整SSA/physical CFG。

因此目标不应写成“立即显著减少总行数”，而应是：每个后续能力只增加自己的语义和runtime adapter，不再复制整套compiler proof/CFG。

### 15.2 预期收益

- hidden helper/effect与真实emission一致，可机器验证；
- suspend/resume/cleanup CFG有稳定dump，review不必从LLVM block反推语义；
- 新operation source通常不修改compiler；
- defer/panic/select/cancel的ordering有统一verifier；
- unsupported诊断从巨大allowlist变成具体缺失recipe/contract；
- cache、summary、ABI有明确版本入口；
- 可在LLVM emission前做frame slot、poll和continuation优化。

### 15.3 性能

第一阶段应产生与当前等价的LLVM IR，runtime性能预期中性。可能的后续收益包括：

- 更精确的跨suspend live set和显式slot lifetime；
- 合并冗余poll或连续resume gate；
- 减少无效child frame，例如条件上必不等待的fast path；
- 更稳定的CFG有利于CoroSplit和后续优化。

风险包括：

- 额外in-memory IR增加编译期内存；
- recipe过细会形成第二个SSA；
- recipe过粗则emitter继续重新推导语义；
- canonical dump/digest若包含不稳定pointer或遍历顺序会破坏cache；
- 提前自行分配所有frame value会与CoroSplit重复并可能降低优化质量。

必须测量而不是预先承诺性能提升。

## 16. 验证与CI

### 16.1 编译器差分

- 相同EmissionUniverse closure和owner instance集合；
- 相同FunctionID、root、Effect/Exec/Demand/FuncRep；
- 相同CallPlan/ValuePlan/elided/lowered helper集合；
- stable LoweringFacts/CoroOverlay dump；
- canonical semantic projection中的suspend site、normal continuation、outcome和helper call一致；physical CFG只要求满足模板不变量，不要求同构；
- post-CoroSplit module verify、resume/destroy symbol、descriptor和frame size一致或有解释的版本变化；
- object emission和最终链接通过。

### 16.2 runtime与语义

- argument/side effect只执行一次；
- conditional fast path不执行resume-only逻辑；
- child result/panic/cancel exact once；
- select winner/loser、closed send panic、default和task cancel竞态；
- result lease Take/Discard；
- main return、panic和shutdown destroy顺序；
- preemption公平和有界source service。

### 16.3 target矩阵

当前feature PR必跑层按现有 `coroutine.yml`：Ubuntu 22.04；Go 1.24.2上的LLVM 19/20/21/22和Go 1.26.5上的LLVM 19；host runtime core `-race -shuffle`、JS/WASM test adapter、native timer/time.Sleep focused E2E、arm/riscv/WASM/baremetal compile/link检查。双backend阶段把快速structural/verify矩阵与LLVM 19完整E2E拆开，避免五个矩阵重复重runtime而超过当前15分钟job预算。

upstream cutover gate再要求：新增macOS native执行；恢复当前workflow注释中暂时关闭的full Go与cache workflow；验证native arm64/riscv64等cross compile、wasm32实际production adapter、baremetal/embedded无host依赖，以及目标支持的nogc/BDWGC/tinygc profile。未落地production adapter的平台不能用compile-only冒充运行兼容。

### 16.4 编译性能

记录：

- whole-program build wall time和peak RSS；
- ProgramModel/LoweringFacts/CoroOverlay对象数与bytes；
- 每函数raw SSA instruction、materialized fact、source span、control cut和suspend site数量；
- pre/post-CoroSplit block/instruction数；
- object size、frame size；
- resume/channel/select/timer microbenchmark。

Phase A先报告多次运行中位数和离散度；取得稳定噪声后，再分别给wall time、peak RSS、code/frame/object size设置数值阈值。“无明显回退”本身不是可执行验收条件。

## 17. 最终可行性判断

### 17.1 可以确定的结论

- 稀疏facts ledger + 现有SSAPlan + control-cut overlay + virtual storage在现有代码结构上可实现，不要求改变Go语法、标准库API或LLVM coroutine基本模型。
- 最安全的起点是现有 `EmissionUniverse` fixed point内的facts cache，而不是新建独立、事后扫描的translator。
- 现有全局计划和runtime operation核心足以作为迁移基线，不需要先重写。
- 当前所有已支持vertical slice都能自然映射到CoroOverlay。
- 新结构为后续Go control semantics提供了目前缺失的显式位置，尤其是continuation、cleanup、result reconciliation和frame lifetime；这不是这些语义已经实现的证明。

### 17.2 不能过度承诺的结论

- 新IR不会让22k行runtime消失，也不会把复杂select/cancel状态机变成几十行。
- 它不会自动完成dynamic descriptor、defer/recover、precise GC、多P或各平台adapter。
- stable state的production LOC未必立即低于当前；收益主要体现在后续扩展斜率和correctness proof。
- 当前prototype的运行成功不能外推成所有Go语言特性或所有平台兼容已经完成。
- hard-sync boundary、panic/unwind、logical stack/tooling、GC、cgo reentry和affinity仍可能迫使局部ABI/方案调整，必须先做原型。

### 17.3 比最初提议更好的具体调整

1. 使用“closure + facts共同fixed point”，并用现有owner key作provisional identity，freeze后才分配pointer-free ID。
2. 区分FunctionID与owner-scoped EmissionInstanceID；单primary在FunctionID层强制。
3. LoweringFacts是稀疏ledger，不复制Phi、普通value/result、terminator或完整CFG。
4. pre-plan `SemanticRecipe` 与 post-plan `PhysicalRecipe` 分离，避免表示选择循环依赖。
5. CoroOverlay只存source span、control cut、continuation、outcome和edge mapping，不预展开physical blocks。
6. VirtualStoragePlan与post-CoroSplit FinalFrameLayout分离；普通value liveness继续交给LLVM。
7. operation使用封闭protocol family和独立WaitSetRecipe，不允许任意hook列表演化为字节码。
8. frame retention改成通用SuspendRegionContract，优先使用稳定OperationRecord。
9. runtime source catalog由target profile生成direct calls，不使用interface，也不让每个source复制executor。
10. LoweringFacts和overlay/storage只在真正存在并进入production cache identity时各升级一次
    届时当前schema；历史v9/v10不再作为版本目标。
11. 新旧backend按完整函数cohort切换，绝不在一个coroutine body内混拼CFG。
12. hard-sync/host入口区分thin thunk与有状态BoundaryDriver；Go源码同步风格不强迫所有host ABI同步。
13. panic/unwind采用logical outcome方向，但先以NoUnwind plain island和focused原型证明。
14. whole-program MaxAtomicCost在等价迁移后单独启用，并补runtime/post-LLVM cost certificate。
15. runtime确定性瘦身与Park V3实验独立于compiler IR迁移，只删replacement matrix已证明可删的路径。

## 18. 建议的下一步

Phase A 与 Phase B 的第一段已经完成：`internal/coro/lowering_facts.go`提供pointer-free site/instance
identity、稀疏LoweringFacts、canonical dump/digest与verifier；`cl`从冻结的EmissionUniverse和SSAPlan
生成owner-scoped snapshot；build在任何package codegen前把该snapshot装入`CoroPlanDigest v26`、
`cl.Compilation`、package fingerprint与manifest，source/cache registration都会验证内容和digest一致。

Phase B剩余工作按以下顺序推进：

- 增加集中EmissionLedger observer，先覆盖managed helper、explicit coroutine feature、panic和suspend；
- 让现有lowered helper、intrinsic和frame retention路径读取或精确对照这些facts；
- 为尚未接入observer的site显式记录coverage，而不是把当前稀疏snapshot宣称成全量emission证明；
- 实现实际被使用的PrimitiveCatalog条目后再将其digest接入，不提前增加空schema字段；
- 记录LoweringFacts对象数、bytes和compile wall基线，避免新事实层长期只增不减。

完成上述预测/实际闭环后进入Phase C，让analysis逐项只消费facts；再生成CoroOverlay并开始新旧backend
对照。除预期schema/cache identity变化外，Phase B不改变FunctionPlan、LLVM CFG、runtime ABI和运行行为。

## 附录 A：关键代码定位

- emission closure：`cl.PrepareEmissionUniverseWithOptions`、`EmissionUniverse.materializeFunctionForOwner`
- provisional owner key：`cl.emissionFunctionOwnerKey`
- hidden helper：`EmissionUniverse.materializeLoweredRuntimeHelpers`、`EmissionUniverse.loweredRuntimeHelpers`
- global plan：`internal/coro.AnalyzeSSA`
- function value flow：`internal/coro.analyzeSSAFunctionFlow`
- physical preflight：`cl.validateCoroPhysicalABIWithUniverseCapabilitiesFrameRetentionAndChannel`
- managed plain/unwind boundary：`cl.plannedFunctionSymbol.checkSupported`、`cl.validateCoroPhysicalABIWithUniverseCapabilitiesFrameRetentionAndChannel`、`internal/build.buildCoroPlan`
- pure lowering audit：`cl.coroPhysicalPureSSAAudit`
- timer frame proof：`cl.coroFrameRetentionProof`
- current physical body：`cl.compileCoroPhysicalBody`
- await/channel/spawn：`cl.compileCoroTargetAwait`、`cl.compileCoroChan*`、`cl.tryCompileCoroClosedStaticSpawn`
- LLVM backend：`ssa.CoroBuilder`
- scheduler：`runtime/internal/coro.G`、`P`、`Action`、`RunDecision`
- operation/select：`OperationID`、`OperationRecord`、`ParkState`、`WaitSetRecord`
- cancellation：`runtime/internal/coro/task_cancel.go`、`cl.coroBodyContext.bindCancellationCompletion`
- executor/source：`ExecutorDriver`、`ExecutorSourceSet`、`executorRunCursor`
- target fallback：`runtime/internal/runtime/coro_target_none.go`
- build/cache：`internal/build.buildCoroPlan`、`coro.SSAPlan.CoroPlanDigest`

## 附录 B：术语

- `LoweringFacts ledger`：全局fixed point期间、owner-scoped、冻结frontend lowering事实的稀疏side table。
- `SSAPlan`：现有Effect/Exec/Demand/FuncRep/CallPlan全局结果。
- `CoroOverlay`：fixed point之后、显式control cut、continuation、outcome和runtime contract的稀疏覆盖层。
- `SemanticRecipe / PhysicalRecipe`：同一source site在plan前后的语义事实与确定性emission计划。
- `OperationRecipe`：把一种event source绑定到封闭protocol family和声明式lifetime contract的配方。
- `SuspendRegionContract`：prepare/park/reconcile/end期间的slot、alias、GC和no-preempt契约。
- `VirtualStoragePlan`：按target配置的显式slot、physical signature和descriptor布局；不复制LLVM普通value liveness。
- `FinalFrameLayout`：CoroSplit后机械取得并校验的最终frame/root descriptor视图。
- `EmissionInstanceID`：同一FunctionID在一个exact owner/patch/ABI上下文中的物理实例identity。
