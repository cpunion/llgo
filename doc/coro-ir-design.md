# LLGo Coroutine 语义标准化 IR 与统一 Lowering 设计

状态：2026-07-22架构复审结论；可运行纵向基线已冻结，暂停新增能力。hidden runtime helper、intrinsic/call-elision、physical proof/implicit-fault、physical emission session/ordinary-compiler isolation、single-event Park protocol、channel/WaitSet Park envelope、ordinary semantic recipe/local Effect-Exec、await/spawn physical control choice、channel/select physical operation choice以及panic/outcome/cleanup choice十个封闭cohort已完成硬切换。其他LoweringFacts仍主要是report/cache identity，尚未成为production lowering的唯一事实源。在完成单一ProgramIR、单一emitter及runtime hard cutover前，不继续叠加语言或平台功能

更新：2026-07-22

当前硬切换基线：`d940fb3dd`（`cpunion/llgo:llvm-coro`，已合并 PR #44）

关联总体设计：[`llvm-coro-runtime-design.md`](./llvm-coro-runtime-design.md)

统一异步核心契约：[`coro-async-core-contract.md`](./coro-async-core-contract.md)

Callable、调用点与 foreign boundary 契约：[`coro-callable-contract.md`](./coro-callable-contract.md)

## 1. 结论

方案可行，但不应实现成“在现有全局计划之后，再复制一份完整 Go SSA”。语义上仍需要稀疏site facts、现有全局fixed point、physical control overlay和target storage plan四种视图；实现上它们必须由同一个`ProgramModelBuilder`产生并冻结为一个`CoroProgramIR`，共享identity、索引、verifier和cache digest，而不是四份独立版本化、各自canonical化的长期文档。

1. 在 emission closure 构建期间，为每个owner-scoped function instance生成一次稀疏`SitePlan`。它冻结有效 Go SSA site 的求值约束、隐藏runtime helper、调用边、panic/unwind、函数值用途、intrinsic、backend footprint和地址生命周期事实，但不复制Phi、普通value/result、terminator或完整CFG；当前`LoweringFacts`wire projection可作为迁移输入，不再继续扩成第二套IR。
2. 继续复用现有 `Effect / Exec / Demand / FuncRep / FunctionPlan / CallPlan` 固定点；这些分析的分层是正确的，不应重写。
3. 固定点完成后，在同一function model上冻结physical fields：普通连续区间只引用原SSA span；control plan只显式表示control cut、唯一continuation、outcome、source-edge mapping和显式跨层slot，不预展开标准协议的LLVM blocks。`CoroOverlay`可保留为诊断视图，但不拥有第二套site identity或独立cache schema。
4. 由一个coroutine emitter只读取冻结的`CoroProgramIR`，按封闭protocol template生成LLSSA/LLVM IR，继续复用现有`ssa.CoroBuilder`和LLVM `CoroSplit`；emitter不得重新分类raw SSA、发现hidden helper或重建frame proof。
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
        +--> provisional site plans + frozen EmissionUniverse
                    |
                    v
              existing SSAPlan fixed point
                    |
                    v
         freeze one CoroProgramIR
         - site semantic plans
         - function/call/value summaries
         - control regions/continuations/outcomes
         - target virtual storage
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
- 当前代码已从native single-P vertical slice推进到受限的Linux/Darwin双owner fleet：command M与peer pthread M各自驱动一个P/source shard，使用exact route、route-local poll/timer/doorbell和共享bounded worker；loopback TCP已在该profile fresh compile-link-run并完成10,000次压力。普通Go 1.26标准库源码风格的`time.Sleep`、冻结timer语义、固定syscall文件回环、高层`os.File`回环和loopback TCP又在同一fleet acceptance中全部通过；整组耗时1102.23s，各项依次为246.60s、375.42s、134.39s、109.01s、236.80s。这不能结论为“完整Go标准库和所有平台已经基本都可落地”：panic/unwind全矩阵、timer GC/synctest、tooling、cgo reentry、precise GC、动态P/affinity、parked-result迁移和其他平台driver仍需实现或生产验证，见第9、11节及《统一异步核心契约》第9.1节。

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

相对当前PR基线`897d251f8`，2026-07-22 worktree共有64个提交、530个tracked变更文件，约
`+114,214/-3,946`，另有3个共191行的新runtime文件。测试约`+53,432/-464`，文档约
`+2,475/-33`；排除测试和文档后，production约`+58,498/-3,449`，净增加约55,049行。

| 模块 | production净新增行 | 判断 |
| --- | ---: | --- |
| `cl` | 23,146 | 最大的可收敛热点；同一SSA语义在closure、preflight、proof和emission重复解释 |
| `internal/coro` | 7,139 | fixed point、identity和并发无关的分析大多应保留；多个独立wire schema可合并 |
| `internal/build` | 4,468 | whole-program、cache、registry和bootstrap；11个阶段性能力开关放大组合面 |
| `ssa` | 1,059 | 可复用LLVM coroutine builder、descriptor和metadata，非主要臃肿来源 |
| runtime core | 8,064 | operation/select/cancel/quiescence多数为必要并发语义 |
| runtime/platform adapter | 10,963 | V1/V2 logical wait、single-P/fleet和手写source catalog存在明显迁移重复 |
| 其他 | 210 | 非主要项 |

测试占新增行约47%，说明PR显示的总行数不能直接等同于runtime重量；但约55k净production增量仍然过大，不能用测试充分性解释。`cl/coro_pure_ssa.go`、`cl/coro_frame_retention.go`、`cl/coro_abi.go`和分散feature lowerer是统一planner/emitter最可能替换或显著缩小的区域；runtime的operation状态机不会因compiler IR自动消失。

### 3.9 2026-07-22耦合审计与停止线

当前实现已越过“只是原型代码多”的范围，存在可量化的横向耦合：

- 旧`currentCoro`和六个可独立安装的physical emission字段已从production归零；`compile.go`与`instr.go`不再读取完整physical body。但协程专用lowerer仍在24个文件中保留47处`coroBody()`能力访问，尚未收敛成最终单一emitter。
- `CoroPlan/EmissionUniverse`直接读取已由精确gate从412降到392处，但距离窄的function/site plan消费边界仍有明显差距。
- `EnableCoro*`阶段开关的精确引用已从330降到316处；合法组合仍主要靠分散gate维持。
- `ExecutorSourceSet`一个文件约767行，对7类source有153个typed field/case引用，bind失败回滚、scan、apply、deadline、empty、terminal close和unbind均手写展开。
- native single-P与fleet选择/全局状态涉及29个production runtime文件、362处引用；fleet route 1通过“收养”旧program P/driver/source进入新模型，而不是由唯一fleet profile直接创建domain 0。
- `WaitToken/WaitRegistration`逻辑队列与`ParkState/OperationID`同时存在；Timer和Poll还各自维护V1/V2 mode及共享generation兼容规则。
- 当前`LoweringFacts`已经canonical化并进入cache identity，但production analysis/preflight/emitter几乎不消费它；`CoroOverlay`只有schema/verifier，没有production planner/emitter调用。继续直接实现Overlay会先增加第三条解释路径。

这形成三条明确停止线：

1. 在site plan成为helper discovery、analysis、preflight和emission的唯一事实源前，不新增Go语言lowering。
2. 在native fleet成为唯一native coroutine target、旧logical wait迁到Park/Operation前，不增加动态P、steal或新event source。
3. 新抽象必须在同一迁移cohort删除旧consumer；不再接受“先report-only覆盖全量、以后再切换”的长期双轨。

需要保留的复杂度也同样明确：`OperationRecord`、`ParkState`、`WaitSetRecord`、result lease、cancel/detach/quiescence、producer admission和A/ack/B防丢唤醒分别表示独立并发事实。没有linearizability证据时，不能为了行数把它们折叠为一个`done`位或让producer直接持有G/P/frame指针。

## 4. 方案比较

| 方案 | 优点 | 主要问题 | 结论 |
| --- | --- | --- | --- |
| 维持当前 raw SSA 直接 lowering | 无迁移成本；已能运行受限原型 | 每个新特性继续扩展 preflight、helper 预测和 CFG 分支；长期一致性成本高 | 只适合作为迁移参照 |
| 只增加 side-table facts | 改动最小；可先消除 helper/effect 重复判断 | physical continuation、outcome、slot 和 cleanup 仍散落在 emitter | 推荐作为第一迁移阶段，不是终态 |
| 只在 `SSAPlan` 后增加 `CoroOverlay` | 能统一 suspend CFG 和 emitter；迁移较直接 | EmissionUniverse/helper closure 和 AnalyzeSSA 仍需重新解释 raw SSA | 有价值但收益不完整 |
| 单一`CoroProgramIR`内的`SitePlan -> SSAPlan summary -> PhysicalPlan` | 一个builder、identity、verifier和digest；阶段职责仍清楚；可直接成为emitter唯一输入 | 必须按cohort替换旧consumer，不能长期只做report | 推荐终态 |
| 把 x/tools SSA 改造成 async SSA/CPS | 所有 continuation 都在一层 | 侵入上游 SSA；普通优化、debug、generic 和现有 compiler 全受影响；迁移风险最高 | 不推荐 |
| 新建完整通用 SSA/MIR | 理论上最整齐，可做自有优化 | 重复 Go SSA 的类型、值、内存、debug 和普通 codegen；远超当前问题规模 | 不推荐 |
| 在 LLVM IR pass 中识别调用并插 suspend | 接近 CoroSplit，frontend 改动看似少 | 已丢失 Go 求值顺序、function-value flow、panic/defer、source CFG 和 runtime ownership；跨包 effect 太晚 | 不可作为语义方案 |
| Go 源码到源码 async 改写 | 容易观察生成代码 | 会改变 API/函数类型/标准库调用风格，且无法自然保存 Go panic/defer/reflect ABI | 不符合目标 |

推荐方案不是“越多 IR 越好”，而是只在raw Go SSA和LLVM builder之间增加目前缺失的两类视图，并把它们冻结在同一个program artifact中：

- lowering之前就必须全局可见的稀疏`SitePlan`；
- fixed point之后才能确定的coroutine `PhysicalPlan`与显式`VirtualStoragePlan`。

现有`LoweringFacts`和`CoroOverlay`类型是迁移原型，可以作为这两个view的字段来源；它们不再各自演化独立schema、digest和consumer。下一版cache identity只绑定一个`CoroProgramIR`schema/digest。

## 5. 推荐总体架构

### 5.1 分层

```text
Layer 0  Go SSA / AST directives / patch packages / target layout
Layer 1  ProgramModelBuilder + PrimitiveCatalog
         -> provisional FunctionModel/SitePlan + EmissionUniverse
Layer 2  existing global analysis
         -> Function/Call/Value summary (复用SSAPlan算法)
Layer 3  CoroPlanner
         -> FunctionModel.Physical + target VirtualStoragePlan
Layer 4  freeze one CoroProgramIR + CoroVerifier
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

这些是builder内部阶段，不是四个长期可独立缺失的production组件。freeze成功后，package compiler只接收一个只读`CoroProgramIR`和当前function的`FunctionIR`；缺任何site/region/storage decision即失败。诊断JSON是该对象的projection，不能反过来成为另一份运行时真相。

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
| channel send/recv | typed hchan与route-aware operation已有direct vertical slice | channel operation 默认page为64槽，native profile为16页/1024槽；仍需typed payload precise-GC、完整语言/race矩阵和parked-result跨P物化 |
| 多 case `select` | wait-set overlay适合表示 | native 1024-slot profile下的完整dynamic case、reflect.Select、uniform selection、typed result和P-neutral result packet |
| timer/Sleep | native Sleep source/owner 与 Go 1.26 controlled-timer linkname 已接入 | 冻结标准库E2E已通过；Timer channel完整lazy语义、GC、`asynctimerchan`、synctest、完整race矩阵和dynamic/sharded source仍待完成 |
| 文件/网络 | native Poll/Worker source、deadline/closing 和 scalar result 已接入统一 operation；冻结regular-file探针已有E2E，TCP已在双owner fleet E2E/压力通过 | 探针之外的cancel/quiescence矩阵、payload/precise GC、parked-result迁移、完整net/resolver矩阵和其他platform backend尚未闭环 |
| `Syscall*` | Linux/Darwin 固定 wrapper 可经 worker park 自动染色，仍需逐族 contract | 已有 bounded 4-thread/1024-job native worker 与 `{r1,r2,errno}` result cell；全 syscall family、cancel-before-start/背压、pointer GC lifetime 和 E2E 尚未完成 |
| `RawSyscall*` | 不能统一“一律异步” | 逐ABI保留raw、signal-safe、locked-thread或不可重启语义；有contract才stack-cut/offload |
| defer | overlay可表示；当前已有静态cleanup及frame-rooted异构动态defer LIFO子集 | range-over-func、Goexit、完整控制流、递归/SCC和语言矩阵仍未闭环 |
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
- syscall/netpoll/worker已在native双owner TCP链闭环，仍需更广`os`/`net`/syscall、容量/取消/GC合同和高连接数backend；
- P-neutral parked-result packet、通用global injection/steal、动态P数量和affinity；
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
| Native Linux/Darwin | layout/ownership无已知冲突 | opt-in双owner fleet已接入程序target：两个真实M/P、独立doorbell/POSIX `poll`/timer shard、Timer/Poll/Worker/Channel exact route、共享bounded worker及start/stop/join已闭环；TCP标准库探针fresh E2E与10,000次压力通过。仍缺动态P、通用steal、parked-result迁移、完整GC/cleanup与GOROOT矩阵 |
| 其他native OS | 尚未审查 | Windows/BSD/mobile production adapter、thread/IO/ABI均未验证 |
| JS/WASM | layout/ownership无已知冲突，可映射为1P host `RunSlice` | 32-bit layout、pre/post-CoroSplit/object和test adapter有覆盖；production queued run/timer/Promise/IO adapter未实现，仍走fail-closed fallback |
| WASI | operation模型可映射，未验证 | pollable/poll_oneoff、filesystem/socket/clock production adapter未完成 |
| RTOS/embedded | 静态执行模型可映射，未验证 | HAL clock/notification/ISR ingress、boundary driver和容量证明都未实现 |
| baremetal | event-loop模型可映射，未验证 | main loop、IRQ mailbox、WFI/WFE、static/tinygc frame和production adapter都未实现 |

架构不要求每G native stack、libuv、BDWGC或pthread，但这只是兼容候选，不是平台完成度。当前production target adapter中，llgo native Linux/Darwin已有opt-in双owner fleet：route 1原位收养program executor，route 2由固定pthread M拥有；两个domain共用唯一物理run-step reducer，各自通过exact idle gate和route-local poll set等待doorbell/fd/deadline。Program target负责peer与共享worker pool的start/stop/join及route/backend/driver强关闭。Worker保持单物理池并用每job的`OperationID.Route`支持fleet completion，不做函数地址反查；C11 ring支持多个owner并发reservation。仍未提供动态P/GOMAXPROCS、通用steal、已park G跨P结果物化或完整affinity。缺少filesystem、process、socket或host async能力的平台仍按target capability决定可用package。LLVM支持范围只是19–22，不考虑19以下版本。

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

当前report/cache slice只算迁移准备，不算Phase B完成。后续按recipe cohort推进；每个cohort必须同时：

1. 让closure/helper discovery和analysis读取同一个in-memory `SitePlan`；
2. 让实际emission observer验证同一个decision；
3. 删除该cohort原来的helper预测、analysis classifier和preflight镜像；
4. 增加静态gate，禁止旧classifier符号或新的raw-SSA重分类入口再次出现。

验收：FunctionPlan、可执行LLVM CFG、runtime ABI和运行行为不变；cache/manifest只绑定统一
ProgramIR identity；已迁移cohort不存在第二个consumer事实源。仅生成report或比较日志不算通过。

#### Phase B.1：hidden runtime helper cohort（已完成）

2026-07-22已完成第一条production replacement cohort，而不是保留双轨比较：

- `EmissionUniverse`在owner-scoped materialization期间建立唯一`coroProgramIR`，每条有效SSA
  instruction只由`classifyCoroRuntimeHelpers`/`classifyPlainRuntimeHelpers`分类一次；raw classifier仅允许
  存在于`cl/emission_runtime_helpers.go`的builder边界。
- closure扩展、LoweringFacts、physical preflight与pure-SSA audit都读取冻结SitePlan；production中的旧
  `loweredRuntimeHelpers`和`plainRepresentationRuntimeHelpers`consumer已删除，静态gate要求其数量恒为0。
- managed helper同时冻结source、prologue或cleanup物理placement。named-result `AllocZ`与动态defer
  `FreeDeferNode`不再依靠codegen后反推或临时抵消表；每个动态defer的`AllocU`/`FreeDeferNode`位置在
  ProgramIR构建期确定。
- `compileInstr`安装source emission ledger；prologue和cleanup使用显式relocated ledger；统一runtime
  helper resolver以及动态cleanup的直接调用都必须上报实际helper。计划外发射、计划内漏发射和缺少
  frozen owner均立即失败。
- architecture gate精确锁定raw planner数量/文件、旧consumer为0、source/relocated ledger入口以及
  helper observation调用点；这些值没有可供旧路径反弹的上限余量。
- raw helper planner不再通过`context.type_`物化LLSSA runtime类型；interface/heap zero-size判断只读取
  patched Go type与target pointer size，因此report/identity universe无需伪造runtime package。静态gate
  要求`cl/emission_runtime_helpers.go`中的physical-type dependency恒为0，原integration崩溃夹具已回归。

该完成标记只覆盖hidden runtime helper集合和它们当前三种物理placement，不代表panic、suspend、
frame lifetime或统一emitter已经迁移。下一cohort必须继续以“新权威接管、旧判断删除、负向测试、
静态gate”四项同时完成作为验收条件。

#### Phase B.2：intrinsic与call-elision cohort（已完成）

2026-07-22已完成第二条production replacement cohort：

- `coroProgramIR.freezeCallSites`在helper closure、patch redirect、physical identity及worker capability全部
  冻结之后，对每个owner-scoped call occurrence执行唯一一次raw分类，并把intrinsic语义、private opcode、
  no-init/patch/intrinsic三类互斥elision以及可选certificate写入同一个SitePlan。`CoroCallSitePlan`只保留
  `Elision`枚举，不再同时保存可矛盾的`Elided bool`。
- build analysis的intrinsic local effect、`ClassifyElidedCall`、elision certificate及raw-plain intrinsic closure
  全部读取同一个`callSitePlan` projection；旧`intrinsicCallSemantics`、`patchInitRedirect`和
  `elidedCallCertificate` production输入已删除并由静态gate要求恒为0。build不再先用callee membership
  特判no-init或重新解释raw SSA。
- physical ABI preflight、critical-region proof、raw/static function-address用途以及现有pure/frame/defer
  consumer只读取冻结call SitePlan；`CoroIntrinsicCallSiteSemantics`保留为纯projection兼容入口，不再
  执行opcode或operand分类。
- worker syscall的完整certificate、owner set和incoming edge proof也被复制进冻结call SitePlan；旧
  `workerSyscalls/workerSyscallOwners/workerSyscallIncoming`只在builder窗口可写可读，并在ProgramIR freeze
  成功后立即置空。patch redirect payload同样进入SitePlan，旧`patchInitRedirects` builder scratch随后置空。
  production validation、pointer-result proof、patch emission和测试查询均从ProgramIR读取。
- source emission ledger要求实际no-init、patch redirect及intrinsic replacement分别上报精确elision；
  intrinsic还必须上报实际opcode与recipe。physical no-init和worker选择直接读取冻结SitePlan，旧前端判断
  只作为非coroutine/incomplete-universe兼容路径。计划外、类型不符、重复或漏发均fail closed。负向测试
  覆盖opcode/recipe不匹配、recipe缺失及elision类型不匹配。
- architecture gate以精确数量和精确文件集合锁定raw no-init分类、intrinsic planner/opcode/shape、worker
  builder scratch、patch redirect lookup、freeze入口和三类actual-emission observation；没有增长余量。

该完成标记只覆盖call occurrence的intrinsic/elision/capability事实及其现有consumer，不代表pure
instruction、implicit fault、panic/outcome、control overlay、frame storage或统一emitter已经迁移。

#### Phase B.3：physical proof与implicit-fault cohort（已完成）

2026-07-22已完成第三条production replacement cohort；其边界是“physical preflight已接受的函数证明和
FieldAddr/Deref/Index/IndexAddr/Slice/SliceToArrayPointer/`ssa:wrapnilchk`控制流选择”，不把尚未统一的普通
pure instruction emitter冒充完成：

- `Compilation.preflightCoroPlan`为每个精确`(function, emission owner)`构造同一ProgramIR中的post-plan
  `coroPhysicalFunctionPlan`，一次性冻结frame-retention、critical-region、static-cleanup证明及每条source
  instruction的physical recipe。owner不是caller package：符号引用读取body owner证明，真正multi-owner body
  emission则读取自己的exact owner projection。
- freeze采用事务式stage；所有function、raw/plain consumer及dispatch验证全部成功后才原子commit并seal。
  失败preflight不会把半份projection留在共享EmissionUniverse，重复freeze、缺owner、缺instruction、集合不精确
  或第二次commit均fail closed。
- `compileCoroPhysicalBody`只读取sealed physical plan；production codegen中
  `newCoroPhysicalPureSSAAudit`、`proveCoroCriticalRegions`和`prepareCoroStaticCleanupPlan`重建调用为0。
  frame-retention、critical和cleanup不再由preflight/codegen各算一遍。
- implicit-fault recipe同时冻结container kind、array bound、nil guard与bounds guard；safe fixed-array proof只在
  planner核对一次。explicit-status PhysicalABIV1 codegen不再从patched type、frame proof或raw SSA重新选择FieldAddr/Deref、
  Index/IndexAddr、Slice、slice-to-array及wrapper nil分支；旧selector和旧`*Guarded`入口已删除且静态gate为0。
- source emission ledger要求上述每个非ordinary physical recipe精确上报一次。漏发、错recipe、重复发射、
  未sealed plan或codegen缺plan均立即失败；负向测试覆盖事务污染、重复commit、缺失projection以及recipe
  missing/mismatch。
- architecture gate锁定builder=2、freeze=1、commit=1、lookup=2、recipe selection=7、recipe observation=7和
  nil/bounds guard observation=10，
  并分别锁定精确文件集合；codegen proof rebuild和legacy physical selector均为0，没有回弹余量。

该完成标记不包含普通pure instruction的统一LLVM recipe、await/spawn/park/channel/select、panic/outcome、
continuation overlay、virtual storage或单一emitter。后续迁移必须扩展同一个physical plan，不能另建overlay
权威或恢复codegen现场判断。

#### Phase B.4：physical emission session与ordinary compiler isolation（已完成）

2026-07-22已完成第四条production replacement cohort；其闭合边界是“一个physical body的临时编译状态
生命周期，以及普通SSA emitter对该状态的访问权”，不把协程专用feature lowerer尚存的body访问冒充
单一emitter完成：

- `context`原有`currentCoro/currentCoroSite/coroPhysicalPlan/coroPhysicalEmission/
  coroExplicitStatus/coroSourceBlocks/sourceParamBase`不再作为可独立安装的字段；plan、body、nested SitePlan
  observer、source-block projection、hidden parameter base和explicit-status capability由唯一
  `coroPhysicalEmissionSession`共同拥有。
- session只有`prologue -> body -> complete`三阶段。prologue可供CoroBuilder初始化回调消费冻结plan，但完整
  body不可见；body与source-block projection必须一次性bind；正常关闭必须已经complete且没有活动SitePlan。
  panic关闭先清除context中的session再原样传播失败，不遗留半安装状态；同一context禁止嵌套physical emission。
- `compileCoroPhysicalBody`是production唯一session begin、body bind和body complete入口，三者各恰好一次。
  `compile.go`与`instr.go`只通过`coro_emitter_adapter.go`中的语义操作处理instruction boundary、allocation、
  return、defer、synthetic select panic、channel和builtin capability，不再取得`coroBodyContext`。
- 单元测试覆盖完整commit、重复bind、嵌套session、未完成关闭和panic清理；静态架构gate锁定旧
  `currentCoro`为0、其余旧split字段为0、begin/bind/complete各1、session字段只能位于context声明与
  session实现、session结构只能保留上述七个正交字段，同时精确禁止普通emitter重新调用`coroBody()`。
- 该切换还将`EnableCoro*`散布引用由322降至316；重复feature gate只能继续减少，不能借适配器回弹。

该完成标记仅覆盖session原子性和ordinary compiler isolation。协程专用lowerer仍有47处`coroBody()`
能力访问，分布在24个production文件；下一阶段必须按await/control、terminal/cleanup、operation等完整职责
域迁入统一emitter，并在每一cohort中同时清零旧访问文件/入口。只把访问器换名或把全部字段透传到一个
generic facade不算完成。

#### Phase B.5：single-event Park protocol（已完成）

2026-07-22已完成第五条production replacement cohort；其精确边界是timer Sleep、controlled timer wait、
poll wait和bounded worker wait四个单事件等待点，不把channel/select的多候选winner reconciliation冒充成
相同协议：

- 唯一`emitCoroParkOperation`模板拥有state ID分配、instruction budget复位、Park/Suspended发布、一次
  suspend/resume continuation、normal/abort/shutdown fail-closed分派以及join后的body activation。feature lowerer
  只能绑定typed park hook、resume hook、封闭status vocabulary和各自payload/liveness处理。
- status vocabulary在发射前验证：normal集合不能为空且不能重复，abort不得与normal重合，shutdown不得与
  normal或abort重合；resume hook缺少status同样立即失败。status描述保留现有`uint64`常量表示，物化LLVM
  switch时显式使用现有`uint32`runtime ABI，不引入ABI变化。
- timer、controlled timer、poll和worker的旧CFG拼装已同时删除；不存在保留在feature flag后的第二条
  production路径。controller/control和worker owner的跨suspend KeepAlive仍留在对应feature lowerer，因为
  它们是typed lifetime事实而不是通用Park协议步骤。
- architecture gate在B.5边界要求低层`suspend`模板入口恰好1处、模板调用恰好4处且只能来自上述3个feature文件；
  这些文件直接调用`suspend/publish/cancellation-target/activate`四类旧协议步骤必须恒为0。模板结构精确
  锁定为`shouldSuspend/park/resume/normal/abort/shutdown`六个字段；下面B.6只通过同一gate受控增加声明式
  fault route，仍禁止通过增加任意hook扩张为隐式字节码。

该完成标记不覆盖channel/select：它们需要一个WaitSet级协议统一完成candidate registration、winner claim、
loser detach、closed-send panic、cancel和result reconciliation，不能拆成多个独立Park。也不表示所有await、
panic/outcome或runtime legacy wait已经迁移；这些必须各自形成同样有旧路径归零gate的封闭cohort。

#### Phase B.6：channel/WaitSet Park envelope（已完成）

2026-07-22已完成第六条production replacement cohort。B.5排除的是channel/select内部多候选对账；本cohort
只统一它们外层“一次逻辑等待对应一次physical suspend”的envelope，两者不能混淆：

- blocking send、blocking receive和blocking select全部改用同一个`emitCoroParkOperation`。各自仍由typed
  channel helper完成single-channel或WaitSet的candidate registration、commit、winner/loser detach和result
  lease；通用emitter看不到候选数组，也不会把select拆成多个Park。
- Park recipe新增的唯一扩展是声明式`coroParkFaultRoute{status, kind}`。emitter为每条route创建canonical
  terminal-fault continuation并调用统一fault lowering，然后恢复joined continuation；feature不能提供任意
  target或CFG callback。send-on-closed及select closed-send因此继续经过相同defer/panic cleanup路径。
- receive的`recvOK`在resume hook后按同一个status SSA值作直线投影，两个成功status仍由统一switch进入normal
  continuation。结构测试验证hook返回值与switch operand identity一致，并禁止二者之间出现branch、第二个
  switch、return或unreachable，不能以“允许中间指令”为由弱化exact-ticket dispatch。
- architecture gate现在要求全production的conditional suspend入口恰好1处、Park recipe恰好7个且分别位于
  精确的7个函数/4个feature文件；这些文件直接访问state ID/budget/park-state字段或直接调用publish/cancel
  protocol必须为0，7个已迁移函数直接activate也必须为0。非阻塞close/try-select不是Park，保留各自normal
  continuation activation，不被错误计入。
- recipe结构被精确锁定为`shouldSuspend/park/resume/normal/faults/abort/shutdown`七个字段，fault route只能
  有`status/kind`两个字段；status集合、`uint32`ABI范围、fault kind以及normal/fault/abort/shutdown互斥都在
  发射前fail closed。

该完成标记覆盖现有7个physical Park envelope，不代表WaitSet内部逻辑已成为ProgramIR OperationRecipe，也
不删除runtime的WaitSetRecord、result lease、cancel/detach/quiescence事实。下一cohort若迁移这些逻辑，必须
以typed recipe/verifier替换现有builder事实，不能把候选状态机复制进compiler generic emitter。

#### Phase B.7：ordinary semantic recipe与local Effect/Exec（已完成）

- `planCoroSemanticInstruction`是唯一raw Go SSA instruction语义分类入口。它在EmissionUniverse worklist窗口
  为每个精确owner的每条source instruction生成稳定recipe、local Effect/Exec、materialization和debug事实；
  ordinary instruction不进入稀疏wire facts，但仍拥有内部recipe，因此不会为了report复制完整Go SSA。
- ProgramIR在判断物理frontend kind之前冻结局部语义。普通Go body、intrinsic泛型实例和保留SSA stub的
  foreign declaration都进入同一闭包；`Advance`、`atomic.Load/Store`等不生成普通Go body的实例不会在
  AnalyzeSSA阶段重新扫描stub。`freezeSiteOwner`同时验证所有source instruction均有recipe，并拒绝同一
  function在不同owner下得到不同的local body事实。
- ProgramIR从recipe聚合`SSAFunctionBodyFacts{Effect, Exec, InstructionCount, HasCycle}`。production
  `CoroPlanInput.Analyze`只允许安装这一callback，并拒绝builder覆盖；instruction budget与cycle对应的
  `NeedsPreempt`仍由现有AnalyzeSSA策略施加。standalone analyzer在callback为nil时保留一个明确的raw scanner，
  但production build没有回退路径。
- physical instruction plan携带同一semantic recipe；`compileInstr`对每条source instruction必须精确消费一次，
  emission ledger会拒绝漏报和重复。旧`coroSourceInstructionFact`已删除，production公开的
  `CoroIntrinsicCallSiteSemantics`兼容投影也已删除，所有consumer直接读取完整`CoroCallSitePlan`。
- architecture gate精确锁定raw local-body scanner仅在`internal/coro/ssa_plan.go`出现2次（定义与standalone
  fallback调用）、ProgramIR local-body authority仅在2个production边界、semantic planner仅在3个文件、
  semantic observation仅在2个文件。负向测试证明即使raw body包含send和loop，注入的冻结facts也不会被
  analysis覆盖；真实native/runtime构建测试覆盖intrinsic泛型实例闭包。

该完成标记只说明ordinary recipe与local Effect/Exec已有唯一production权威，普通value/CFG仍由成熟compiler
lowering；它本身不表示await/spawn、panic/outcome或whole-function unified emitter已经完成。Phase B.8继续
扩展同一physical plan并删除await/spawn feature emitter的重复control分类，没有新建第二个recipe表。

#### Phase B.8：await/spawn physical control choice（已完成）

- 同一`coroPhysicalInstructionPlan`新增正交的control recipe，不复制value/fault recipe。当前封闭集合覆盖
  direct await、managed descriptor await、closed/managed interface await、synchronous descriptor dispatch、
  direct spawn和descriptor spawn；ordinary call保持零control recipe。
- 唯一`planCoroPhysicalControlInstruction`在post-analysis physical-plan stage读取冻结CallPlan/ValuePlan、
  interface-family proof、owner与target ABI，保存精确target、FunctionID、interface candidate plan或source
  signature。识别到await/spawn后的ABI错误是hard failure，不能退回plain call；未识别direct await仍可由
  既有direct-plain证明处理。
- physical preflight对这些site只统计/验证冻结control recipe，不再第二次执行static/dynamic/interface/spawn
  selector。对应source codegen也只调用`plannedCoroPhysicalControl`，并以
  `observeCoroPhysicalControl`精确上报；漏报、错recipe和重复上报均在source site关闭时失败。
- direct/descriptor spawn codegen不再读取CallPlan或重跑`ResolveManagedDispatchSpawn`；static、dynamic及
  interface await不再在emitter中重跑resolver/shape classifier。plain body仍保留自己的同步descriptor路径，
  不被错误强制依赖physical plan。
- architecture gate锁定control planner恰好2个标识出现（定义与唯一调用）、selection 7处、observation 8处及
  精确文件集合；同时`CoroPlan/EmissionUniverse`直接权威引用由392降至377，`EnableCoro*`引用由316降至315。
  全部`TestCoro*`、native/runtime真实构建及observer负向测试已覆盖该切换。

该完成标记覆盖source call/spawn的最终control选择与emission ledger，不表示child completion、panic/outcome、
WaitSet reconciliation或whole-function CFG已经由单一emitter拥有。后续必须把这些protocol region继续冻结进
同一FunctionIR，不能让feature emitter重新读取CallPlan来派生另一条control choice。

#### Phase B.9：channel/select physical operation choice（已完成）

- 同一`coroPhysicalInstructionPlan`新增与value/fault、control正交的operation recipe，封闭覆盖send、receive、
  close、blocking select Park及try-select。唯一`planCoroPhysicalOperationInstruction`在physical-plan stage验证
  channel capability、精确channel type及select state，并冻结blocking/try选择；ordinary instruction保持零recipe。
- physical preflight只读取冻结operation recipe统计Park和panic，不再重复读取`EnableCoroChannel`、channel type、
  select states或`Select.Blocking`。source codegen只经`plannedCoroPhysicalOperation`选择，并通过
  `observeCoroPhysicalOperation`精确上报；漏报、错recipe及重复上报均在source site关闭时失败。
- `coroChannelLoweringEnabled`已删除，channel emitter不再回读阶段开关。实际send/receive/select挂起仍复用
  B.5/B.6的唯一`emitCoroParkOperation`模板；本cohort没有复制Park CFG，也没有改写runtime的WaitSet result
  lease、loser detach、cancel或quiescence状态机。
- architecture gate锁定operation planner恰好2个标识出现、selection 5处、observation 6处及精确文件集合；
  `EnableCoro*`production引用由315降至313。production净增175行，属于单一配方与ledger替换，而不是第二套
  channel backend。channel native/wasm32、zero-sized channel、capability fail-closed及observer负向测试已覆盖。

该完成标记只覆盖source channel/select的最终物理操作选择。WaitSet runtime reconciliation及跨source取消仍是
现有统一runtime core的权威实现；后续若迁移其表示，必须在同一cohort删除对应legacy WaitToken consumer。

#### Phase B.10：panic/outcome/cleanup physical choice（已完成）

- 同一instruction plan新增第四条正交outcome recipe，封闭覆盖physical return、defer registration、RunDefers、
  explicit-status panic、recover以及blocking-select的synthetic invariant trap。planner同时验证panic payload、精确
  cleanup site及explicit-status capability；preflight只读取冻结recipe，不再重复识别这些SSA终结形状。
- source emitter经唯一`plannedCoroPhysicalOutcome`选择并由`observeCoroPhysicalOutcome`逐site上报；缺失、错配及
  重复消费都会在编译期失败。原先返回bool并在ordinary compiler中试探的`tryCompileCoroReturn/Defer/
  RunDefers/ExplicitStatusPanic`入口已删除，physical recipe不匹配不能退回legacy native-stack panic/defer。
- synthetic select message boxing成为现有value/fault recipe中的显式elision；unsafe.String和unsafe.Slice也由冻结
  physical recipe选择。panic、recover、implicit fault、unsafe及slice-to-array emitter不再读取全局
  `EnableCoroExplicitStatusPanicABI`，只校验已绑定physical session的ABI identity。
- architecture gate锁定outcome planner 2处、selection 6处、observation 6处及精确文件集合；本cohort把
  `EnableCoro*`production引用由313降至303。production净增200行，主要是typed recipe和ledger，同时删除
  74行旧试探/feature判断。全部`TestCoro*`及panic/recover/defer/fault native+wasm32 focused矩阵通过。

该完成标记说明source outcome的选择权已唯一化；cleanup drainer、child completion reconciliation和终态CFG模板
仍由现有physical emitter实现。下一阶段收拢remaining value/call selection及whole-function emitter入口。

### Phase C：analysis只消费facts

- hidden lowered helper、`ClassifyElidedCall`、intrinsic site/local effect及physical implicit-fault选择已由
  Phase B.1/B.2/B.3替换；ordinary instruction recipe及local effect/exec输入已由Phase B.7替换，source
  await/spawn control choice已由Phase B.8替换，channel/select operation choice已由Phase B.9替换，
  panic/outcome/cleanup choice已由Phase B.10替换；继续迁移WaitSet runtime表示及剩余call/value-flow classification。
- 再逐步替换call/value-flow扫描的重复classification；必要的数据流pass仍保留。
- report-only计算跨plain调用闭包的MaxAtomicCost，记录与当前instruction budget的差异，但不改变NeedsPreempt、primary或poll。

验收：plan、roots、FunctionID、CallPlan和ValuePlan与冻结基线一致；production `AnalyzeSSA`对已迁移
site只接受ProgramIR projection。旧callback/classifier在同一提交删除，不能由feature flag保留。

### Phase D：生成CoroOverlay

- 仅覆盖当前preflight已接受的函数。
- 显式生成poll、await、park、channel/select、spawn、return/panic的control cut、continuation、outcome和virtual slot；不预展开physical blocks。
- physical fields直接冻结进同一`FunctionIR`；诊断层可投影为CoroOverlay，但不增加独立schema/digest。
- 新verifier先在单个whole-function cohort通过，随后该cohort立即交给新emitter；不允许把全程序
  report-only overlay作为一个长期阶段。

验收：每个迁移function都只有一份合法physical plan和一个production emitter owner；旧拒绝用例继续
拒绝。没有“生成overlay但production仍从raw SSA重建控制流”的已完成状态。

### Phase E：双backend对照

- 新 coroutine emitter使用现有 CoroBuilder。
- plain function继续旧路径。
- 对同一fixture执行两个独立test-only compile/module invocation：legacy读取raw SSA，新backend读取FunctionIR，避免同名symbol在一个module双发；production config没有双backend开关。
- 比较canonical semantic projection、suspend/continuation/helper/descriptor、post-CoroSplit verify、frame阈值和运行结果，不要求physical CFG同构。

验收：native+nogc E2E、host race/shuffle、JS/WASM test adapter、native64/wasm32、LLVM 19–22全部通过后，在同一cohort cutover并删除legacy emitter。只完成对照而未删除旧路径不算Phase E完成。

### Phase F：按完整函数切换并删除重复实现

- 按whole-function eligibility cohort依次切换pure-only、preempt、await/spawn、park/channel/select；一个函数的全部SuspendKind均被新backend支持后才切换。
- 禁止同一coroutine physical body按op/feature混用两套emitter，否则Phi、continuation和logical tail没有唯一owner。
- 删除旧instruction allowlist、pure SSA镜像、直接CFG callback和timer专用proof。
- 将 `currentCoro` 收缩到新emitter内部。

验收：production只存在一条coroutine physical emission路径。

### Phase G：在新IR上补语言能力（Phase R全部通过后才开始）

优先顺序建议：

1. 让MaxAtomicCost真正参与单调NeedsPreempt/poll计划；
2. generic operation reconciliation和CompletionRecord；
3. defer/panic/recover/Goexit cleanup；
4. dynamic coroutine descriptor、closure/method/interface；
5. syscall/netpoll/worker/host sources；
6. precise GC/debug metadata；
7. P-neutral packet、多P/affinity；
8. reflect和完整平台adapter。

Phase B–F严格保持plan、runtime ABI和可观察行为不变；Phase G才是新功能开发，并且必须等待下面Phase R的四个hard-cutover gate全部通过，不能混为一个巨大PR。PrimitiveCatalog生成器重构和Runtime V3也分别立项，不塞入等价迁移。

### Phase R：runtime hard cutover（新增功能前必须完成）

Phase R与compiler cohort可独立提交，但四个gate全部通过前不进入Phase G：

1. **唯一native target**：native coroutine command直接创建fleet domain 0；删除program-state adoption、single-P target、default/fleet poll route、worker completion和ready distribution双实现。domain数量仍可先固定为2，但storage/lifecycle只存在一套。
2. **唯一logical wait**：semaphore、notify、Sleep及剩余legacy park全部迁到`ParkState/OperationID`；删除G/P中的legacy WaitToken queue、Timer/Poll V1 mode和跨V1/V2 generation兼容分支。若平台idle ingress仍需要小型registration，必须是只持POD ID的物理mailbox，不能再次拥有逻辑G wait状态。
3. **唯一source dispatcher**：用`SourceKind +`静态direct switch驱动统一bind/rollback/scan/apply/deadline/close/unbind循环；source-specific模块只保留payload、physical commit/cancel和强quiescence。不得使用Go interface、closure或producer持有Go pointer。
4. **唯一profile**：production只保留一个coroutine enable/profile选择；PhysicalABI、ChildAwait、Bootstrap、Channel、Worker、NativeFleet等阶段开关不再组成配置笛卡尔积。target capability由冻结profile/catalog派生，测试特例留在test-only builder。

runtime hard cutover不删除`OperationRecord/ParkState/WaitSetRecord/result lease/cancel/detach/quiescence/producer admission`。这些是正确性的正交事实，不是legacy层。

### 14.1 不可回退的架构gate

每个cutover提交都必须新增或更新机器gate；最终至少满足：

- production compiler中`currentCoro`只允许存在于统一coroutine emitter边界，普通instruction文件不得直接读取；
- helper、effect、panic、suspend、frame lifetime的production decision均可追溯到一个`SitePlan`，不存在第二个raw-SSA classifier；
- cache/archive/manifest只接受一个`CoroProgramIR`schema/digest；旧LoweringFacts/Overlay独立identity被删除；
- native production source tree不存在single-P/fleet互斥build tag和两套route/completion/ready实现；
- `G`/`P`及production target不存在`WaitToken`logical queue，Timer/Poll不存在V1/V2 mode；
- production config不再含多个阶段性`EnableCoro*`布尔字段；
- 新增一种event source只修改source adapter、profile catalog和测试，不修改compiler opcode/feature lowerer；
- `go test -race` runtime core、LLVM 19/20/21/22结构门和五项fresh fleet标准库E2E全部通过。

gate应解析Go AST/build constraints或检查冻结catalog，不依赖容易被改名绕过的单一字符串；同时保留少量明确的forbidden-symbol检查，防止旧路径重新出现。任何一项未满足，架构优化状态就是`incomplete`，不得开始后续功能PR。

## 15. 成本、收益与性能

### 15.1 迁移代码量估计

不再接受“先新增完整新backend、再等待未来删除旧backend”的峰值模型。每个replacement cohort只允许短期加入该cohort所需的`SitePlan`字段、verifier和emitter模板，并在同一提交删除对应helper预测、classifier、preflight或旧CFG拼装；production双轨不能跨cohort存在。

因此代码量按cohort而不是整个迁移估算：单个cohort原则上应是数百行级净变更；若需要新增超过约1k production LOC，必须先证明它没有复制raw SSA、fixed point或LLVM CFG，并在PR中列出同步删除量。总迁移可能仍需数千行新schema/planner/emitter，但稳定态目标是明显低于当前约55k production净增量，且`cl`、runtime adapter和配置面的架构债务gate持续下降。完整ProgramModelBuilder worklist重构、catalog生成器、digest分层和MaxAtomicCost新功能另行计量，不能混入等价迁移来掩盖净增长。

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
10. LoweringFacts、overlay和storage只作为同一个`CoroProgramIR`的projection；production cache、
    archive和manifest只绑定一个schema/digest，历史v9/v10不再作为独立版本目标。
11. 新旧backend按完整函数cohort切换，绝不在一个coroutine body内混拼CFG。
12. hard-sync/host入口区分thin thunk与有状态BoundaryDriver；Go源码同步风格不强迫所有host ABI同步。
13. panic/unwind采用logical outcome方向，但先以NoUnwind plain island和focused原型证明。
14. whole-program MaxAtomicCost在等价迁移后单独启用，并补runtime/post-LLVM cost certificate。
15. runtime确定性瘦身与Park V3实验独立于compiler IR迁移，只删replacement matrix已证明可删的路径。

## 18. 建议的下一步

Phase A 与 Phase B 的十个replacement cohort已经完成：`internal/coro/lowering_facts.go`提供pointer-free site/instance
identity、稀疏LoweringFacts、canonical dump/digest与verifier；`cl`从冻结的EmissionUniverse和SSAPlan
生成owner-scoped snapshot；build在任何package codegen前把该snapshot装入`CoroPlanDigest v26`、
`cl.Compilation`、package fingerprint与manifest，source/cache registration都会验证内容和digest一致。

2026-07-22复审最初把LoweringFacts定义为“已建立观测点”，而不是已完成架构层。随后hidden runtime
helper、intrinsic/call-elision、physical proof/implicit-fault、physical emission session、single-event Park及
channel/WaitSet Park envelope、ordinary semantic recipe/local Effect-Exec、await/spawn physical control choice、
channel/select physical operation choice及panic/outcome/cleanup choice cohort已完成production切换，但其余facts仍未替换production classifier/emitter；继续直接
增加完整Overlay仍会扩大双轨。后续严格按replacement cohort推进：

1. 先提交当前双owner fleet可运行基线及五项fresh E2E结果，不再混入新能力。
2. architecture gate test已经冻结当前债务的精确AST/build-constraint快照；每个cohort必须在删除旧路径的
   同一提交下调数字和白名单，禁止留下可反弹额度，也禁止新增`EnableCoro*`、raw-SSA classifier、
   single-P/fleet分支和logical WaitToken consumer。
3. hidden helper、intrinsic/call-elision、physical proof/implicit-fault、emission session、single-event Park、
   channel/WaitSet Park envelope、ordinary semantic recipe/local Effect-Exec、await/spawn physical control
   choice、channel/select physical operation choice及panic/outcome/cleanup choice cohort已按上述gate完成；
   下一步迁移remaining value/call selection和WaitSet runtime表示，不能重新引入raw SSA helper、
   intrinsic、local body scanner、feature-local control selector、fault selector或codegen proof rebuild。
4. 随后迁移panic/outcome；每个完整
   function cohort由统一emitter接管后立即删除旧CFG拼装，最终清除普通compiler中的`currentCoro`分支。
5. 并行完成runtime Phase R：fleet唯一target、Park/Operation唯一logical wait、统一source dispatcher和
   单一profile；每一项都以旧production符号为零作为完成条件。
6. 运行runtime race、LLVM 19–22、native/wasm32结构验证和五项fresh stdlib E2E。只有全部architecture
   gate通过，才恢复P-neutral result、dynamic P、GC、panic/Goexit和平台adapter等功能开发。

迁移过程可以用独立test invocation比较旧/新输出，但production永远只有一个被选择的consumer；临时双轨
不得跨cohort保留，也不得以feature flag形式进入下一阶段。最终目标不是“文档中有新架构”，而是旧架构
的production调用者和配置入口已经物理删除。

## 附录 A：关键代码定位

- emission closure：`cl.PrepareEmissionUniverseWithOptions`、`EmissionUniverse.materializeFunctionForOwner`
- provisional owner key：`cl.emissionFunctionOwnerKey`
- hidden helper builder：`EmissionUniverse.materializeLoweredRuntimeHelpers`、`EmissionUniverse.classifyCoroRuntimeHelpers`
- hidden helper authority/ledger：`coroProgramIR.sitePlan`、`context.beginCoroSiteEmission`、`context.observeCoroSiteRuntimeHelper`
- call-site builder/authority：`coroProgramIR.freezeCallSites`、`EmissionUniverse.CoroCallSitePlan`
- call-site emission ledger：`context.observeCoroCallElision`、`context.observeCoroIntrinsicCallEmission`
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

- `CoroProgramIR`：由一个ProgramModelBuilder冻结的唯一production program artifact；包含site、全局summary、physical control和storage视图，并拥有唯一schema/digest。
- `LoweringFacts ledger`：全局fixed point期间、owner-scoped、冻结frontend lowering事实的稀疏side table。
- `SSAPlan`：现有Effect/Exec/Demand/FuncRep/CallPlan全局结果。
- `CoroOverlay`：fixed point之后、显式control cut、continuation、outcome和runtime contract的稀疏覆盖层。
- `SemanticRecipe / PhysicalRecipe`：同一source site在plan前后的语义事实与确定性emission计划。
- `OperationRecipe`：把一种event source绑定到封闭protocol family和声明式lifetime contract的配方。
- `SuspendRegionContract`：prepare/park/reconcile/end期间的slot、alias、GC和no-preempt契约。
- `VirtualStoragePlan`：按target配置的显式slot、physical signature和descriptor布局；不复制LLVM普通value liveness。
- `FinalFrameLayout`：CoroSplit后机械取得并校验的最终frame/root descriptor视图。
- `EmissionInstanceID`：同一FunctionID在一个exact owner/patch/ABI上下文中的物理实例identity。
