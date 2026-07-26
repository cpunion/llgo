# LLGo 基于 LLVM Coroutine 的运行时与抢占调度器总体设计

状态：实现中（可验证无栈原型；尚非完整 Go runtime）

更新：2026-07-26

当前审查基线：`cpunion/llgo:llvm-coro` @ `5e80e4064`

集成状态：Phase 36 hard-cutover、P-neutral result materialization 与 demand-driven
work sharing、native 固定有界物理 topology、动态 managed-execution quota 和标准
`runtime.GOMAXPROCS` 接口、generation-bound ownership baton及target-neutral
active resume detach/restore core已合并；当前实现分支已接通native replacement
M目录、同P ready/source驱动、嵌套handoff以及channel/timer/poll linked E2E，并以
process-lifetime clean thread factory隔离replacement创建和已锁M的可继承线程状态

关联提案：[Issue #1546](https://github.com/xgo-dev/llgo/issues/1546)

历史原型：[PR #1532](https://github.com/xgo-dev/llgo/pull/1532)

统一异步核心与扩展成本契约：[`coro-async-core-contract.md`](./coro-async-core-contract.md)

编译器语义标准化 IR 与统一 lowering 审查：[`coro-ir-design.md`](./coro-ir-design.md)

## 1. 结论与核心决策

本设计以 LLVM stackless coroutine 作为可挂起 Go 调用帧的唯一底层机制，重新设计编译器分析、函数 ABI、逻辑 goroutine、抢占调度、GC、同步原语和平台事件驱动。无栈不是可选优化，而是跨 Native、WASM、RTOS 和 baremetal 共用同一调度模型的硬性架构约束。PR #1532 仅作为 LLVM intrinsic 与 IR 结构参考，不在其调度器和“所有函数双版本”模型上继续演进。

核心决策如下。

1. 不为所有函数生成同步、协程两个完整版本。
2. 每个 `G` 都是无栈协程：没有私有、可增长、可复制或长期保留的 native stack；所有跨 suspend 存活的控制状态和值只存在于 LLVM coroutine frame 和显式 runtime 对象中。
3. 明确同步的短小函数只生成普通同步实现；明确异步或需要抢占的函数只生成 coroutine 实现。
4. 静态可判定调用全部在编译期选择入口。只有真正的 hard sync ABI 调用 coroutine 时使用薄 `blockOn` 边界，不复制函数体；普通 managed caller 会被 effect 传播为 coroutine 并透明 await。
5. 只有函数值流入开放存储或动态调用边界，例如func value、`any`、interface、reflect、未知包或C回调，才生成运行时描述符。Descriptor只发布唯一primary的plain或coro capability；hard-sync crossing由consumer生成薄root adapter，不复制函数体。
6. 不使用 R12、TLS mode flag 或全局 `coroDepth` 判断当前调用模式。调用模式是编译计划和调用点的显式属性。
7. 调度对象是逻辑 goroutine `G`，不是裸 LLVM coroutine handle。一个 `G` 可拥有由普通异步调用形成的 coroutine frame chain。
8. 调度器采用编译器辅助的安全点抢占：时钟、线程或中断异步提出抢占请求，编译器插入的 poll 在安全点执行 `llvm.coro.suspend`。用户代码不需要显式 yield。
9. 有界抢占是硬性验收要求。所有可能无限执行的 managed 路径必须经过可挂起 poll；循环、递归 SCC 和超长基本块会成为 coroutine lowering 的 seed。
10. channel、select、runtime semaphore、timer、poll 等阻塞路径必须改成 scheduler-aware 的 park/wake，不能继续在 coroutine executor 上阻塞 pthread cond 或 libc `poll`。
11. coroutine frame 必须由可扫描的 runtime allocator 管理，不能使用不受 GC 管理的普通 C `malloc`。
12. Native 使用 M/P/G 形式的多 executor 调度；JS/WASM、WASI 初期、RTOS 初期和 baremetal 使用同一抽象的单 P 形态。
13. JS/WASM scheduler必须按slice返回host；Sync export不能执行未证明可在当前同步任务闭包内完成的park，更不能等待未来Promise/timer。只有ABI已声明Async/Dual时才生成Promise wrapper，否则必须启用声明的JSPI/Asyncify边界能力或诊断。
14. 借鉴其他语言只采纳operation lifetime、commit、cooperative cancellation、reduction和executor ownership等底层机制；不把`Future`、`Task/Job`、`Promise`、sender/receiver对象图、STM retry log或每G mailbox引入Go源码、Go ABI或基础G常驻布局。

这里的“抢占式”指对 Go 用户透明、由异步请求触发并在编译器安全点完成的抢占。LLVM stackless coroutine 不能在任意机器指令、POSIX signal handler 或 ISR 中保存普通 native 调用栈，因此本设计不承诺任意 PC 硬抢占。

## 2. 背景与当前基线

当前 main 的并发运行时仍以 pthread 为中心：

- `ssa/goroutine.go` 中每个 `go` 语句创建 detached pthread。
- channel、select 和 runtime semaphore 的等待路径使用 pthread mutex/cond。
- Native timer 由 libuv loop 和独立线程驱动。
- Native poll wait 由调用线程执行阻塞式 libc `poll`。
- Baremetal timer、monotonic clock 仍是链接占位实现。
- panic、defer、Goexit 和部分 goroutine-local 状态依赖 native stack、TLS 或全局变量。
- `runtime.Gosched`、`LockOSThread` 基本为空，`entersyscall/exitsyscall` 尚未建立 P handoff。
- `GOMAXPROCS` 没有真实控制 executor，`NumGoroutine` 仍不能反映逻辑 G。
- `procPin` 由全局 pthread mutex 模拟并固定到 P0，trace/pprof/synctest 也没有真实 G/P 状态。

历史 PR #1532 已证明 LLGo 能生成并由 LLVM 19 降级基本 coroutine IR，但其 runtime 有以下结构性问题：

- 几乎所有 tainted 函数生成完整双版本。
- 用 R12/TLS/global mode 判断调用上下文。
- 调度队列保存裸 handle，没有逻辑 G。
- 全局无锁 ready queue、current handle、depth 和 panic map。
- `CoroYield` 未实现。
- 没有 timer、netpoll、idle wait、抢占、多核和真正的 channel 集成。
- 队列为空时直接 resume 尚未满足等待条件的 handle。
- frame 使用 C `malloc`，Promise alignment 固定为 8。
- main 返回后继续 drain goroutine，违反 Go 的退出语义。
- 污点分析遇递归环时暂定 clean，可能漏标整个 SCC。

因此新方案从 Task 状态机和编译计划重新开始，只复用经过验证的 LLVM coroutine IR 生成知识。

### 2.1 不可协商的兼容性前提：Go 源码始终是同步调用风格

本设计的首要前提不是提供一套新的 async API，而是让未经异步改写的 Go 标准库和用户代码按原有方式工作：

    data, err := conn.Read(buf)
    time.Sleep(time.Second)
    wg.Wait()
    value := <-ch
    err = http.ListenAndServe(addr, handler)

源码、函数签名、interface method set、错误返回、defer 和 panic 传播都不出现 Future、Promise 或显式 `await`。编译器在内部决定一次调用是：

- 普通同步 direct call。
- 创建同一 G 的 child coroutine frame 并透明 await。
- 通过动态 descriptor选择plain/coro entry；hard-sync ABI统一经过root boundary adapter。
- 仅在 C/host 等 hard sync boundary 执行 `blockOn`。

因此本文后续的 “Sync function” 和 “Coroutine function” 都是 codegen/ABI 分类，不是两种 Go 语言函数，也不改变调用者看到的 API。

### 2.2 Transparent await

源代码：

    func serve(c net.Conn) error {
        n, err := c.Read(buf)
        if err != nil {
            return err
        }
        return process(buf[:n])
    }

若 `c.Read` 的动态实现可能 park，编译器内部等价地生成：

    child := dispatchCoro(c.Read, buf)
    suspendCurrentFrameAwaiting(child)
    n, err := loadResult(child)

但 Go 类型仍是 `Read([]byte) (int, error)`。调用链中的 `serve`、上层 handler 和 server loop 会由 Effect fixed point 自动 lower 成 coroutine primary；用户无需逐层修改签名。

若具体 receiver 是 `*bytes.Buffer`，分析证明 `Read` 为 bounded `NoSuspend`，调用可以去虚拟化成普通 direct call。若 receiver 是 `net.Conn`，interface descriptor 在运行时选择对应实现。这个差异只存在于编译产物中。

### 2.3 标准库兼容的直接含义

为了兼容完整标准库使用风格，以下行为是设计要求：

- `sync.Mutex.Lock`、`WaitGroup.Wait`、`Cond.Wait` 的 slow path park G。
- `time.Sleep`、Timer、Ticker 使用 scheduler timer heap。
- `internal/poll.runtime_pollWait` park G，net/http/tls 等源码无需异步改写。
- Regular file、DNS、process wait 等不可统一异步化的 OS 操作先形成 `ForeignOp` 并 stack-cut，再由 native blocking worker 或指定 M 的干净 thunk 执行。
- `syscall.Syscall*`、`RawSyscall*`及internal syscall wrapper保持原同步函数签名，但在coro模式下是effect-aware intrinsic：公开primitive仍执行一次原kernel op，潜在阻塞时以ForeignOp异步承载；只有带明确wait+retry契约的`internal/poll`层才转readiness token。Effect自动把调用它们的标准库/用户caller提升为coroutine primary。
- `io.Reader`、`io.Writer`、`http.Handler`、`error`、`fmt.Stringer`、`sort` comparator 等高阶或 interface callback 支持动态 coroutine entry。
- `context` 继续使用 channel、timer 和 goroutine；底层能力完成后无需改变 public API。
- `reflect.Value.Call/MakeFunc` 能表示并调用 coroutine function。
- Finalizer、Cleanup、timer callback 和 signal delivery 都作为 G 调度，不在 driver/ISR 中直接运行。
- `runtime.Caller/Stack`、panic traceback 和 pprof 最终展示 logical coroutine frame chain。

标准库中“看起来同步但实现可能等待”的函数不能在内部递归 `blockOn`。它们必须通过编译器 effect 传播成为 coroutine primary，并在普通 Go 调用点透明 await。`blockOn` 只解决最外层 hard sync boundary。

### 2.4 兼容目标与平台能力必须分开

语言/标准库调用风格兼容，不代表所有硬件都拥有同样的 OS 服务：

- Native POSIX 的目标是完整标准库和完整并发语义。
- JS/WASM、WASI 的可用包范围至少与对应 Go target/host capability 对齐。
- RTOS/baremetal 没有 process、filesystem、signal 或 socket 时，相应 package 可按 build tag/HAL 缺失；这不是 coroutine 模型本身的语言障碍。
- 在一个平台声明 package 可用后，其阻塞 API必须保持普通同步 Go 风格，不能暴露平台专用 await。

文档把“缺少平台服务”和“coroutine 无法保持 Go 语义”分别列出，避免把两者混为一谈。

### 2.5 不可协商的无栈前提

本文所称 stackless 必须同时满足以下条件，而不只是 IR 中出现了 `llvm.coro.*`：

- 每个 `G` 不分配独占 pthread stack、分段栈、复制栈或 Asyncify shadow stack。
- 跨 `llvm.coro.suspend` 仍存活的局部值、返回槽、defer/panic 状态和 program counter 都位于显式 coroutine frame；handle 不是 native stack pointer。
- 一次 resume episode 可以暂时使用当前 `M` 的 native/host stack，但 suspend 后必须逐层返回 scheduler，不能留下任何引用该栈帧的 continuation。
- 普通 plain helper只能处于有界同步调用区域。它可以继续调用其他plain函数并使用executor stack，但整个plain call closure不得跨suspend，也不能形成未证明有界的递归或循环。
- 一个 `M` 只有一份由平台配置的 executor stack；它被多个 `G` 分时共享。RTOS 是每个 scheduler task 一份，baremetal 是 main/exception stack，WASM 是当前 host entry stack，而不是每个 G 一份。
- C、汇编、ISR 和 host callback 的活动栈不属于 coroutine frame，不能被捕获为managed continuation。普通外部调用必须返回、offload或投递token；同步ForeignReentry/HostReentry特例只允许child LLVM coroutine stack-cut回受控boundary loop，保留的有界外部ABI stack归ForeignOp/HostOp所有且不保存Go frame地址。
- 逻辑 Go 栈由 `G + FrameDescriptor + frame parent` 重建，不以保留 native stack 作为 traceback、panic 或 recover 的正确性条件。

Stackless 不等于零内存。每个 suspended call 仍需要一个显式 frame，深递归仍消耗与逻辑深度相关的 frame 内存。区别是这些对象可由 GC、arena、slab 或静态 pool 管理，可单独回收和限制，不要求目标具备虚拟内存、guard page、线程栈增长或可执行堆。

这正是跨环境兼容的基础：

| 环境 | 共享执行栈 | Suspended state | 不依赖的机制 |
|---|---|---|---|
| Native | 每个 M 的 OS stack | GC-visible coroutine frames | 每 G pthread/stack copying |
| JS/WASM | 每次 host re-entry 的 Wasm stack | linear-memory frames | JS stack retention/Asyncify |
| WASI | scheduler command/reactor stack | linear-memory frames | host thread per G |
| RTOS | 每个 scheduler task 的固定 stack | heap/slab frames | RTOS task per G |
| Baremetal | main stack和独立 IRQ stack | static pool/tinygogc frames | OS thread、VM、guard page |

若某个 lowering、runtime primitive 或第三方 pass 需要在 suspend 后保留 native stack 地址，它违反本设计，即使功能测试暂时能运行也不得合入 coroutine scheduler。

### 2.6 主要收益与代价

收益：

- G数量不再受OS thread/RTOS task stack成本限制，高并发内存只支付实际live frame。
- 同一frame/scheduler ABI可落到Native heap、WASM linear memory、RTOS slab和baremetal static pool。
- 用户和标准库继续使用同步Go API；平台异步性被限制在compiler/runtime内部。
- Primary body选择性生成，避免全函数双版本的代码体积。
- Suspended state显式可枚举，便于GC root、debug、resource limit和确定性测试。

代价：

- Effect/value-flow、跨包summary、dynamic descriptor和post-CoroSplit metadata是编译器正确性关键路径。
- 每次可挂起调用可能分配frame；需要pool、tail/inline优化和frame-size预算。
- 抢占只能发生在compiler safepoint，延迟上界依赖plain call region和foreign region审计。
- Panic、defer、logical stack、reflect和cgo不能继续依赖native stack/TLS的既有实现。
- 无host服务的平台仍无法提供process/POSIX signal/raw socket；无栈调度器不能创造硬件或OS能力。

## 3. 目标

### 3.1 语言与兼容性目标

- 保持普通 Go 源码，不增加 `async/await` 语法。
- 以 Go 1.26 标准库的同步 public API 和 runtime linkname contract 为兼容基线。
- 标准库和用户包不需要维护 async fork。
- `go f()` 创建独立逻辑 goroutine。
- 普通函数调用保持同步结果语义；在 managed task 中由编译器自动 await。
- 支持 closure、method value、interface、`any`、泛型和 reflect 的动态调用。
- 支持 defer、panic/recover、Goexit 跨 coroutine frame 传播。
- Command模式的`main.main`返回时立即退出、不等待其他goroutine；Reactor/Embedded由显式host lifecycle管理。
- 阻塞的 Go 同步原语 park 当前 G，而不是阻塞整个 executor。

### 3.2 调度目标

- 无需用户显式 yield，纯计算循环也可被调度器抢占。
- 单 P 和多 P 使用同一 G 状态机。
- Native 支持 M:N、多核、work stealing，以及 blocking foreign operation 的有界 worker/locked-M 补偿。
- timer、channel、select、I/O 使用统一 park/wake。
- 能检测 lost wake、重复入队、同一 handle 并发 resume 等错误。
- 支持 deterministic fake platform，以便重放状态机和竞态测试。
- 在任意普通未pin G数量下，native/RTOS stack数不随G增长；显式LockOSThread、foreign和driver worker只按独立硬预算增加M/task。

### 3.3 平台目标

- Native POSIX：Linux、Darwin，后续扩展其他 OS。
- JS/WASM：单线程 event loop，后续可选 wasm threads。
- WASI：`poll_oneoff` 驱动。
- 嵌入式 RTOS：一个或多个 scheduler task。
- Baremetal：main loop + hardware timer/IRQ + WFI/WFE。

### 3.4 性能目标

- 纯同步、无动态逃逸的代码不引入 coroutine frame 和动态分派开销。
- 每个可挂起函数只有一个主实现。
- ready queue、普通 await、timer Sleep 不因每次切换分配节点。
- coroutine frame 大小只包含跨 suspend 点仍存活的值及必要头部。
- 高并发内存按 `O(G header + live coroutine frames + wait nodes)` 增长，不含 `O(G × reserved native stack)`。
- 单 P 基础正确后，再以本地 deque、批量 stealing、缓存 frame 等方式优化。

## 4. 非目标与明确限制

- 不支持在任意机器指令处异步捕获 native stack。
- 不追求“完全不使用机器栈”；scheduler、resume episode 和有界 plain call region 仍使用每个 M 的共享 executor stack。
- 不使用 split-stack、stack copying、setjmp/longjmp 保存栈或全程序 Asyncify 来实现 managed Go continuation。
- 不允许 signal handler、JS callback 或 ISR 直接 resume/destroy coroutine。
- 不允许把活动C/host frame捕获进coroutine continuation；ForeignReentry/HostReentry只能按13.1/13.2的受控boundary-loop协议stack-cut。
- 第一阶段不支持把现有 pthread goroutine 和新 coroutine goroutine 混在同一 Go runtime 中；一个 binary 选择一种 scheduler mode。
- 第一阶段不承诺 precise coroutine-frame GC map；使用保守可扫描 frame。
- 第一阶段不承诺 wasm threads、MCU SMP 和插件式 open-world 动态加载。
- 同步JS/WASM导出若包含未证明可完成的MayPark或依赖未来host event，不尝试用busy loop伪造阻塞。

## 5. 术语

| 术语 | 含义 |
|---|---|
| Synchronous call style | Go 源码层普通调用并等待结果；本设计对所有 Go API 保持这种风格 |
| Plain/Sync implementation | 普通内部 ABI 函数，执行过程中不能 suspend |
| Coroutine implementation | 同一 Go 函数的 LLVM presplit coroutine lowering，ramp 返回 handle，结果写入 result slot |
| Stackless / 无栈 | 每个 G 不拥有可跨 suspend 保留的机器栈；continuation 全部位于显式 frame |
| Resume episode | Scheduler 恢复一个 frame，直到它再次 suspend/complete 并返回 scheduler 的一次有界执行片段 |
| Primary body | 一个源函数唯一的主要实现；同步或 coroutine 二选一 |
| Adapter | ABI边界的薄包装，例如 `blockOn(newG(rootFactory, record))` |
| Dynamic descriptor | 开放调用边界保存 sync/coro 入口能力的描述符 |
| G / Task | 一个 Go 语言层 goroutine |
| Frame | 一次 coroutine 函数调用的 LLVM heap frame |
| Frame chain | 一个 G 内由普通 async call/await 形成的父子 frame 链 |
| P / Processor | 执行 managed Go 代码的调度许可和本地 shard |
| M / Executor | 执行 scheduler 和 Go 代码的 OS thread、RTOS task 或 host execution context |
| Safepoint | 编译器保证可检查抢占、GC 或调度请求的位置 |
| Park | 因 channel、timer、I/O、semaphore 等等待而挂起 G |
| Preempt | 时间片到期后在 safepoint 把 Running G 变回 Runnable |
| Hard sync boundary | C ABI、同步 export、同步 reflect 或 host 要求立即返回的边界 |

## 6. 总体架构

    Go SSA program
          |
          v
    Effect / Demand / Value-flow analysis
          |
          v
    Per-function and per-callsite CoroPlan
          |
          +--------------------+
          |                    |
          v                    v
    normal LLVM function   LLVM presplit coroutine
          |                    |
          +---------+----------+
                    v
          dynamic adapters/descriptors
                    |
                    v
              LLVM CoroSplit
                    |
                    v
      runtime Task / Frame / Scheduler ABI
                    |
          +---------+---------+-----------+
          |                   |           |
          v                   v           v
      ready queues       timer/netpoll   GC/debug
          |
          v
      platform driver
      native / JS / WASI / RTOS / baremetal

编译器负责决定哪些函数可以保持 sync、哪些必须成为 coroutine、哪些值必须 canonicalize 为动态表示。Runtime 不通过环境 mode 猜测调用方式，只执行编译器明确生成的操作。

## 7. 编译器分析模型

### 7.1 三个正交维度

每个函数和调用点分别分析三个维度，不能用一个“tainted”布尔值代替。

#### Effect

Suspend effect和执行约束必须分开：

    SuspendEffect =
        NoSuspend
        | YieldOnly
        | AwaitStructured
        | MayPark
        | WaitPlatform
        | WaitHost
        | WaitForeign
        | OpaqueSuspend

非NoSuspend项可组合并按集合join；`WaitHost -> WaitPlatform`。`MaySuspend` 是这些suspend effect的统称，不包含线程亲和、IRQ或普通控制流属性。

- `YieldOnly`：preempt/Gosched后当前G本身仍Runnable，不依赖其他G或外部事件。
- `AwaitStructured`：等待一个由当前调用创建、effect已知的child frame完成；child effect继续向caller传播。
- `MayPark`：在channel、mutex、WaitGroup、select等对象上等待，完成条件可能来自动态的另一个G。
- `WaitPlatform`：需要 timer、fd、host Promise、IRQ 等未来外部事件。
- `WaitHost`：`WaitPlatform` 的子类；必须把执行权还给同线程 host event loop，例如 JS Promise/setTimeout/fetch。
- `WaitForeign`：caller已stack-cut并等待ForeignOp完成。
- `OpaqueSuspend`：未知动态代码，保守包含全部suspend capability。

正交 `ExecFlags`：

- `BlockForeign`：callee可能阻塞在C/syscall；CallPlan把它lower为caller的WaitForeign，callee内部不suspend。
- `ThreadAffine`：依赖当前 M/OS thread，例如 LockOSThread 或 C TLS。
- `IRQUnsafe`：可能分配、加锁、park或调用非中断安全代码。
- `NeedsPreempt`：managed上下文需要suspendable poll，因此为primary加入YieldOnly seed。
- `MayUnwind/NeedsCleanupFrame`：可能panic/Goexit或有plain defer cleanup；选择PanicABI landing/status，但本身不触发coroutine化。
- `NoReturn`、`PanicOnly` 等已有控制流属性。

`MaySuspend` 的 seed 包括：

- channel send/recv、可能阻塞的select和scheduler-aware semaphore/mutex/Cond/WaitGroup：`MayPark`。
- Sleep、timer wait、netpoll wait：`WaitPlatform`；JS host实现同时标 `WaitHost`。
- 未证明bounded + no-callback的foreign call：callee标BlockForeign，caller产生 `WaitForeign`。
- 公开`Syscall*`/`RawSyscall*`通常由target metadata产生`WaitForeign`；只有带PollWait/ExactAsync契约的上层wrapper产生`WaitPlatform/WaitHost`，runtime启动、signal/after-fork等显式RawCritical路径才可在验证后保持NoSuspend。
- Gosched和抢占poll：`YieldOnly`。
- 普通transparent await：`AwaitStructured`并合并child effect。
- coroutine dynamic call：候选effect的join；未知候选为OpaqueSuspend。

panic、recover 本身不是 suspend seed。它们需要 coroutine-aware unwind，但不应像 #1532 那样无条件把整个调用图标成 async。

这些 capability 参与边界验证：

- JS sync export可运行NoSuspend/`YieldOnly`，也可运行已证明不含MayPark/WaitHost的structured await tree。
- JS sync export默认拒绝 `MayPark`，即使该函数本身没有WaitHost：wait-for graph可能指向另一个随后Sleep/fetch的G。只有closed-world completion proof证明全部wait edge都由当前同步任务闭包内、无WaitHost的producer满足时才可放行；V1不做该证明时一律生成Promise/JSPI adapter或报错。
- `go f()` 的effect通常不传播给caller，但JS sync-export completion proof必须把当前boundary内spawn的G和动态wait edge纳入依赖图，不能只查普通direct call graph。
- WASI `poll_oneoff` 可在host import中同步等待，因此 `WaitPlatform` 不等于JS的 `WaitHost`。
- Interrupt入口的整个可达图必须证明 `!IRQUnsafe` 且不包含任何Suspend/BlockForeign。
- ThreadAffine G只能在绑定M上恢复。
- C/assembly默认 `BlockForeign + IRQUnsafe`，由可信annotation收窄。

#### Demand

    None | Sync | Async | Both

- C export、同步host/C callback等hard sync root产生Sync demand；main/init和所有G root产生Async demand。
- `go f()` 对 target 产生 Async demand，但不使 caller 自身 suspend。
- managed function 普通调用 MaySuspend callee 时，对 callee 产生 Async demand，并使 caller 可 suspend。
- `defer f()` 在当前 G 内执行，callee effect 必须传播到当前函数。
- 动态调用按该 callsite 所在模式传播 demand。

`Both` 只表示同一个primary同时被managed调用点和hard-sync consumer需要，不授权生成两份完整函数体；后者通过typed root adapter满足Sync demand。

#### FuncRep

    DirectPlain | DirectCoro | Dispatch

- 纯 SSA 局部、候选唯一且上下文封闭的 function value 可以保持 direct。
- 流入 global、heap field、map、channel、未知 memory、`any`、interface、reflect、unsafe、未知包或 C 的 function value canonicalize 为 Dispatch；该规则递归作用于struct/array/slice等aggregate中的func叶子。
- 出现在 exported 参数、返回值、变量或独立 archive ABI 中的 function value及包含它的aggregate默认使用 canonical Dispatch。只有 summary 能证明整个边界封闭且sync-only，或调用者与callee位于同一LTO单元且aggregate始终SSA-scalarized时，才允许内部降级成Direct。
- Phi、参数、返回值和 storage slot 的所有 incoming 必须统一表示，不能在运行时猜测两字值里装的是 code 还是 descriptor。

### 7.2 抢占对 Effect 的影响

LLVM coroutine 只能在已 lower 成 coroutine 的函数中执行真正 suspend。普通 sync callee 即使插入一个 poll，也无法保存其 native 调用栈。

因此 managed 可达函数若满足以下任一条件，必须设置 `NeedsPreempt`，成为 coroutine lowering seed：

- CFG 有循环回边。
- 位于递归 SCC，或可形成无界递归调用链。
- 单个基本块或直线路径的静态 cost 超过阈值。
- 调用未知耗时的 Go function value。
- 编译器无法证明在抢占上界内返回。

短小、无环、无 suspend、执行成本有界的 sync helper 可由 coroutine 直接调用，并被视作一个原子执行片段。

这条规则保证：任何可能无限执行的 managed 路径都经过真正可 suspend 的 safepoint，而不是在 sync helper 内做无效检查。

### 7.3 调用图和不动点

分析使用完整 SSA program，步骤如下。

1. 为每个函数建立稳定 FunctionID。
2. 扫描 Call、Defer、Go、MakeClosure、interface invoke、channel/select 和 CFG backedge。
3. 用 CHA 建立保守初始调用图，可用 VTA 精化 function value 和 interface 候选。
4. 对 direct call graph 计算 SCC condensation graph。
5. 用 worklist 求 Effect 最小不动点。
6. 在 `(Function, Mode)` 上传播 Demand。
7. 对 function value storage 做 value-flow join，得到 FuncRep。
8. 生成 per-function、per-callsite、per-value CoroPlan。
9. Codegen 后运行 plan verifier，确保每个调用点需要的入口存在。

不能使用“递归 DFS 遇到 analyzing 就返回 clean”的算法。SCC 中任一成员出现 suspend/preempt seed，相关可达 caller 必须按边类型传播。

FunctionID 不能只使用 `PkgPath + Name`。它必须包含：

- 最终 linkname/patch 后的符号身份。
- receiver 类型和 pointer/value 形态。
- 泛型实例 type arguments。
- nested function/closure 的稳定 lexical identity。
- ABI 和 scheduler 版本。

### 7.4 边的传播规则

| SSA 边 | 对 caller 的影响 | 对 callee 的 demand |
|---|---|---|
| Direct Call | MaySuspend callee 使 managed caller MaySuspend | 当前模式 |
| Go | caller 不因 spawn 而 suspend | Async |
| Defer | defer 在当前 G 内执行，effect 传播 | 当前 managed 模式 |
| Direct bounded plain helper | 不传播 suspend | Plain entry |
| Interface/func dynamic call | caller 必须包含动态 async 分支 | callsite mode |
| Foreign call | callee BlockForeign使caller产生WaitForeign并stack-cut | Plain foreign thunk ABI |

### 7.5 生成矩阵

| Effect / 使用方式 | 主实现 | Adapter / descriptor |
|---|---|---|
| NoSuspend，仅静态调用 | `F` | 无 |
| NoSuspend，从 coroutine 调用 | `F` | 无；直接调用 |
| NoSuspend，动态逃逸 | `F` | plain-only descriptor；无 `F$coro` |
| NoSuspend，hard-sync Go entry | `F` | typed sync wrapper创建LLVM-coro root trampoline；不复制 `F` |
| MaySuspend，仅 managed/async | `F$coro` | 无同步函数体 |
| MaySuspend，同时有hard-sync边界 | `F$coro` | typed wrapper创建root G并 `blockOn`；无sync主体 |
| MaySuspend，动态跨上下文 | `F$coro` | Dispatch descriptor；hard-sync crossing由consumer生成root adapter |

默认政策是每个源函数只有一个 primary body。Hard sync root调用 coroutine 时：

    result = runtime.blockOn(
        runtime.newG(typedRootFactory(F$coro), evaluatedArgs))

这里的 hard sync root仅指C export、同步host callback等外部ABI边界。NoSuspend target也必须经通用root trampoline建立G，再在其中调用 `F`；这不是复制主体。普通Go managed caller一旦可达MaySuspend callee就不能保持NoSuspend；它会成为coroutine并直接创建child frame await，禁止在managed frame内嵌套 `blockOn`。

V1禁止以性能或调用上下文为理由复制sync/coro两个主体。未来若研究whole-program specialization，也只能作为可关闭且语义等价的优化，不能改变descriptor ABI、正确性或本设计的单primary验收门槛。

### 7.6 示例

    func add(a, b int) int {
        return a + b
    }

只生成 `add`。从 coroutine 调用时也直接调用，不生成 `add$coro`。

    func worker() {
        for {
            doOneUnit()
        }
    }

`worker` 在 managed task 中包含无界循环，因此只生成 `worker$coro`，循环回边包含抢占 poll。若同步 C export 需要调用它，只生成薄同步 wrapper。

    var x any = worker

在box到 `any` 时materialize Dispatch descriptor，`coroEntry` 指向 `worker$coro`。若以后流入hard-sync crossing，由实际consumer按静态func type生成typed root + `blockOn` adapter；producer/archive不需要预知该demand。

### 7.7 泛型

LLGo 当前会实例化泛型。分析必须按每个 instantiated `*ssa.Function` 进行，不按 generic origin 一刀切。

- 不同 type arguments 可产生不同 method target、循环形态和 effect。
- Generic linkonce body、descriptor 和 method dispatch metadata 使用稳定实例 ID。
- COMDAT 中重复实例必须生成完全一致的 plan digest 和 initializer。
- 高阶泛型摘要需要表达 effect constraint，例如 “`Apply(f)` 的 effect 依赖参数 0”。

### 7.8 跨包摘要和构建缓存

当前全源构建可在所有 `buildSSAPkgs` 完成后、各包 codegen 前做一次全程序分析。`llgo tool compile`、预编译标准库和 archive 模式还需要跨包摘要。

摘要至少包含：

- Coro ABI、scheduler ABI和target-wide `PanicABI`版本。
- target triple、pointer size、endianness。
- FunctionID。
- SuspendEffect、ExecFlags、Demand capability、可用entry及syscall/host-import effect metadata digest。
- function参数、返回值以及嵌套aggregate func叶子的FuncRep map/layout hash。
- hard sync/export 边界。
- method dispatch descriptor。
- 高阶参数 effect constraint，例如 `effect(Apply) = localEffect ∪ effect(param0)`。
- plan digest。

未知摘要或 ABI 版本不匹配时：

- 可证明不涉及 coroutine 的 C/同步声明按 Sync 处理。
- Go动态调用按OpaqueSuspend + unknown ExecFlags + Dispatch保守处理。
- 无法安全生成 bridge 时在编译期报错，不能静默调用错误 ABI。

分析结果受反向 caller 和最终程序影响，因此每包 cache fingerprint 必须加入稳定 `CoroPlanDigest`。否则同一包在两个应用中得到不同 Sync/Async/Dispatch 计划时可能错误复用 archive。

正确性不能依赖最终链接程序重新解释预编译archive中的function-value或嵌套aggregate布局。所有ABI-visible/open package boundary递归使用稳定canonical Dispatch，只发布producer的plain/coro primary capability；未知未来hard consumer在实际crossing处生成CallbackHandle/typed root adapter。`CoroPlanDigest` 只允许驱动包内entry pruning、devirtualization和cache校验，不能改变已经发布的字段、参数或返回值物理表示。这样 `llgo tool compile` 生成的标准库archive可被未知后续caller安全复用。

### 7.9 编译器指令

建议支持但不依赖用户标注：

- `//llgo:async`：强制 coroutine primary。
- `//llgo:nosuspend`：声明函数不能产生语义 suspend。
- `//llgo:nopreempt`：runtime 短临界函数，不插入抢占点。
- `//llgo:nounwind`：runtime 不变量函数不能发起 Go panic/unwind；显式 panic/defer、开放调用和会 unwind 的静态 callee 仍由 verifier 拒绝。
- `//llgo:noblock`：已知短小、不阻塞的 C 调用。
- `//llgo:blocking`：foreign call 需要 executor compensation。
- `//llgo:interrupt`：声明IRQ入口；整个可达图必须验证为NoSuspend、NoBlock、NoAlloc和IRQ-safe。

`nosuspend/nopreempt/nounwind/noblock/interrupt` 都必须由 verifier 验证。`nopreempt` 中出现未知调用、blocking call 或 coroutine intrinsic 应直接报错；原型阶段仅对显式 runtime 证书容忍计算循环，计入后续 M/P/G 解耦债务。`nounwind` 不得掩盖显式 panic/defer、开放调用或未证明的 callee；interrupt可达图中出现分配、GC、锁、park或非IRQ-safe call也必须报错。

AST directive 必须在全程序分析前收集。不能等到 codegen 才发现 `//export` 或 linkname，否则会遗漏 hard sync boundary。

### 7.10 求值顺序与 effect lowering 不变量

Transparent await不能改变Go规定的求值时机。Codegen必须先生成一个共同的 evaluation prefix，再分支到sync/coro/dispatch路径：

- 普通调用的callee、receiver和全部参数只求值一次，再选择entry。
- Variadic slice在entry选择前构造。
- Method value的receiver在method value表达式求值时固定。
- `go f(args...)` 在parent G中完成function value和参数求值；求值panic时不创建G。
- `defer f(args...)` 先完整求值，再安装defer record。若求值过程park或panic，尚未安装该defer。
- Return expression先写named result，再运行LIFO defer，最后从result slot publish。
- Select先按源码顺序求值所有recv channel、send channel和send RHS，再probe/register；recv case的LHS只在该case选中后求值。
- Dynamic dispatch的sync/coro两条分支只能消费共同prefix产生的temporaries，不能各自重复表达式。

Plan verifier应检查dispatch block的incoming operands来自同一evaluation prefix，并为Go、Defer、Select和return建立源码级语义测试。

## 8. 编译计划在代码结构中的位置

高层计划引用 `go/ssa.Function`、CallInstruction 和 SSA Value，不应放进低层 `llgo/ssa.Program`，否则低层 LLVM builder 会反向依赖 x/tools/go/ssa。

建议新增 compilation-scoped：

    internal/coro.Plan
      FunctionPlan[*ssa.Function]
      CallPlan[*ssa.CallInstruction]
      ValuePlan[ssa.Value]
      PackageDigest[pkgPath]

`internal/build.context` 持有 Plan，并通过统一 `cl.Compilation` 参数传入 `cl.context`。`cl` 再调用低层显式 API：

- `MakeDirectClosure`
- `MakeDispatchClosure`
- `CallSync`
- `CallCoro`
- `CallDispatch`
- `EmitSuspend`
- `EmitPreemptPoll`

当 Plan 为 nil 时保留现有 pthread/sync lowering，便于 feature flag、现有单测和回滚。

优化管线必须保持Plan不变量：bounded plain region可inline进coroutine；coroutine body不得inline进plain caller；devirtualization后可把Dispatch降为Direct；loop rotation/unroll等变换必须保留safepoint coverage。任何在Plan之后新增的call edge都要更新summary或被post-optimization verifier拒绝。

## 9. 函数 ABI 与动态分派

### 9.1 同步 ABI

同步函数保持Go源码层签名；物理managed ABI由整个link unit统一选择的`PanicABI`决定：

    R F(ctx?, args...)

`NativeEH`/`WasmEH`/`EpisodeSJLJ`可保持上述直接返回形态；`ExplicitStatus`为可能panic/Goexit的plain调用增加隐藏outcome并让callsite走cleanup edge。该选择必须进入跨包summary、symbol ABI hash和link compatibility check，不能让caller/callee各自猜测。无论哪种PanicABI，plain函数都没有coroutine handle且不允许suspend。

### 9.2 Coroutine ABI

Coroutine ramp 使用逻辑 ABI：

    CoroHandle F$coro(Task *g, ResultSlot *out, ctx?, args...)

具体 LLVM IR 仍遵循 `llvm.coro.id/begin/suspend/end/free` 模式。结果放在 caller/task 管理的 result slot，不要求 waiter 在 frame destroy 后继续读取 Promise。

Coroutine 在 initial suspend 后由 scheduler 管理。普通 async call 不创建新 G，只创建同一 G 的 child frame。

ABI 明确禁止额外的 stack pointer、saved register stack image 或 longjmp target。Ramp 只创建显式 frame；resume/destroy 只接受 handle。CoroSplit 必须使用 LLGo 的 `coroFrameAlloc/coroFrameFree`，不得因为 target 不支持默认 heap 而回退为跨 suspend 的 `alloca`。

### 9.3 Dispatch ABI

现有普通 closure 的物理布局是两字 `{code, env}`。为避免所有 function value 扩成三字，保留 direct 两字布局，并为动态值使用计划内的两字 Dispatch 形式：

    DirectFuncValue {
        code
        env
    }

    DispatchFuncValue {
        descriptor *FuncDispatch
        env
    }

    FuncDispatch {
        plainEntry
        coroEntry
        flags
        abiHash
        resultLayout
    }

`abiHash`覆盖Go func signature、receiver/invoke convention、pointer width、PanicABI、argument/result layout和递归FuncRep map；任何一项不匹配都在call前诊断，不能仅比较源码类型字符串。

成为Dispatch不等于生成双版本。NoSuspend值只有 `plainEntry`；MaySuspend值只有 `coroEntry`。两种managed上下文都可从这两个互斥primary slot调用；hard-sync adapter属于crossing consumer，不是producer的第三版本或descriptor正确性前提。

表示种类由 CoroPlan 和跨包摘要决定，不使用 code pointer 低位 tag。低位 tag 对 wasm、CHERI、函数地址对齐和部分 baremetal ABI 不可靠。

合流到 Dispatch slot 时，所有 direct incoming 在 store/phi/return 前显式转换。Nil function value 保持 `{nil, nil}`，调用前统一 nil check。

FuncRep规划递归覆盖aggregate。任何被物化到内存、跨包/导出边界、进入reflect/unsafe或可能bulk-copy的struct/array/slice/map/channel元素，其全部func叶子都使用canonical Dispatch；Direct只允许保留在封闭、未取址且始终SSA-scalarized的值中。Direct与Dispatch虽然都是两字，但不得靠位模式猜测：type/field metadata携带`FuncRepMap + layoutHash`，insert/extract/store/return前执行显式转换，`memmove`只复制已canonicalize的字节，reflect按metadata装载。Verifier拒绝把raw code pointer aggregate解释为descriptor aggregate。

Async callsite：

1. `coroEntry != nil`：创建 child frame并 await。
2. 否则直接调用`plainEntry`，作为bounded plain call region的一部分。

Hard-sync callsite：

以下协议只适用于没有现存ForeignOp/HostOp owner G的外部首次进入或普通hard-sync export；Go→C/host→Go同步重入必须走13.1的`ForeignReentry/HostReentry` special child，不创建新G。

外部thread/host entry先attach或定位M、注册GC stack/root、完成STW handshake；`blockOn`只在每次managed resume前获取P并设置currentG，resume返回后立即清除并按平台协议释放P。Terminal ack后，临时attach的thread才能detach。任何wrapper都不能在无P状态运行用户Go。

1. Crossing consumer按静态func type生成/缓存typed wrapper；动态callback同时创建 `CallbackHandle{descriptor, env, roots, abiHash, boundaryPolicy, generation}`。需要裸closure code pointer的平台不能要求producer archive预生成每个实例。
2. Wrapper按Go规则求值参数，将它们复制到GC-visible runtime object：

       BoundaryRecord {
           argumentStorage
           resultStorage
           completion
           panicRecord
           gcRoots
           boundaryPolicy
       }

3. Wrapper调用 `newG(typedRootFactory, BoundaryRecord)`。Root trampoline在G内选择 `plainEntry` 或 `coroEntry`，result slot始终指向BoundaryRecord。
4. 外层`blockOn`等待root完成DestroyPending/unregister后的terminal ack。`Return`才把result复制回foreign ABI；`Panic/Goexit/Abort/Shutdown`必须已运行Go defer并冻结logical trace，再按ABI声明的boundaryPolicy处理。同步C/host export默认不得language-unwind或伪造零值返回，只能采用与cgo兼容的fatal/abort；显式支持error outcome的embedding ABI可返回该outcome，Promise风格异步边界可reject。Record只在terminal ack被consumer确认后释放。

动态callback trampoline分三类：

- 外部ABI显式带userdata：共享的signature-specific static trampoline从userdata取得CallbackHandle。
- 裸函数指针且无userdata：需要libffi/JIT closure，或有硬上限的预生成slot trampoline registry；每个code address只定位一个handle lifetime，调用本身不携带generation。
- target两者都不具备：`ffiClosure=Unavailable`，编译/注册时给出capability error，不能让单个static trampoline猜env。

CallbackHandle注册后由runtime registry强保根；显式Release/注销先关闭新调用并等待runtime已知的并发reentry引用归零。只有userdata/token实际携带generation的ABI才能靠generation拒绝迟到调用并安全复用registry index。

无userdata的裸C函数指针若复用同一code address，旧指针与新callback不可区分。因此slot/libffi closure只有在外部ABI明确确认quiescence、保证不再调用旧指针后才能回收/复用；无法确认时保留可拒绝调用的tombstone并永久retire该address，有限pool耗尽即capability/resource error。不能释放code后承诺拦截stale pointer，也不能靠递增runtime generation使同一裸地址安全。C无限期保存callback而不提供quiescence时，对应handle/root或tombstone按C ownership继续存活，这是显式资源生命周期而不是GC可推断的逃逸。

Compiler生成的root frame、result slot和argument storage不得引用outer C/host stack临时地址。显式传入的opaque C pointer仍按cgo lifetime/pinning规则处理，但不能把wrapper自己的 `alloca` 当continuation storage。Stack-cut verifier用多次suspend boundary tests检查这一点。

普通managed动态callsite不是这里的hard-sync callsite；若候选包含coro entry，其caller由effect分析提升并走async规则。大多数direct call不读取descriptor。

Plain managed dynamic callsite只有在CoroPlan证明候选全集都是plain-only且ABI hash一致时才可加载`plainEntry`直接调用。任何候选可能包含coro entry或unknown时，caller必须在编译期成为coroutine并使用上述async算法；runtime发现coro后递归`blockOn`不是合法fallback。

### 9.4 Interface 方法

当前itab每个method slot只有一个code pointer，不足以表达可选plain/coro entry。新ABI使用method descriptor：

    MethodInvoke {
        plainEntry
        coroEntry
        flags
        abiHash
    }

Itab method slot 保存 `*MethodInvoke`。Concrete method 的 primary body仍遵循选择性生成；descriptor 不意味着复制两个函数体。

每个slot入口是signature-specific invoke thunk，使用统一interface receiver ABI，再适配value receiver copy、pointer receiver和nil receiver检查后调用唯一concrete primary。Thunk只做ABI/receiver适配，不复制方法主体。

- 去虚拟化成功的 singleton interface call 直接使用静态入口，不经过 descriptor。
- 动态managed interface call优先coro entry，否则直调plain entry。
- Hard-sync boundary先创建BoundaryRecord/root G，root再按同一规则调用；普通managed caller不blockOn。
- Method value创建真正的`FuncDispatch`，env保存已按Go语义求值/复制的receiver与`*MethodInvoke`；不能把MethodInvoke指针直接重解释为FuncDispatch。

这需要统一更新 compiler itab layout、runtime `abi.Method`、`NewItab`、reflect method metadata 和 Go global DCE 的方法 capability metadata。

### 9.5 `any` 和 reflect

- Function value box 到开放 `any` 时必须是 Dispatch。
- Type assert 回 func 后保持 Dispatch，除非优化器证明 box/assert 封闭并消除。
- `reflect.Value.Call`、`Method`、`MakeFunc` 是 open-world 动态边界。
- Managed lowering的reflect call读取coro/plain entry；hard-sync lowering创建BoundaryRecord/root G后blockOn。
- libffi 只能调用对应 ABI。不能把 coroutine ramp 当作普通返回值函数。
- `MakeFunc` 不应依赖运行时生成可执行代码作为唯一方案。WASM、Harvard 架构 MCU 和无可执行 RAM 目标对链接时已知 signature 使用编译器预生成的 trampoline，运行时只创建 `{descriptor, env}` 数据。
- `reflect.FuncOf` 可构造链接时未知签名。Native 可使用 libffi/架构 universal trampoline；AOT target 只有在存在 universal packed ABI 时才能完全支持，否则必须限制为已注册 signature 并报告 capability error，不能假设静态 trampoline 能覆盖任意运行时类型。
- 第一阶段若尚未实现 async reflect，编译器必须对可能 async 的 reflect call 给出明确诊断，而不是静默调用 sync pointer。

## 10. Coroutine frame 与逻辑 G

### 10.1 G 而不是 raw handle

调度队列、timer、channel 和 I/O registry 都保存 `*G` 或整数 token，不保存无 owner 的裸 handle。

    G
    └── root frame
        └── awaited child frame
            └── active frame

一个 G 只有最深层 active frame 可被 resume。同一 handle 绝不能被两个 M 并发 resume。

所有G只有一个创建入口：

    newG(coroRootFactory, evaluatedArgs, origin) -> *G

`main/init` bootstrap、`go`、外部C/host thread首次进入Go、`AfterFunc`、signal delivery、finalizer、每个 `AddCleanup`、testing worker、runtime background task和 `runtime.newcoro` 都必须经过该入口。目标函数即使是bounded sync-only，也由通用LLVM-coro root trampoline调用；目标本身不因此复制coro版本。

Go→foreign/host→Go的同步重入是唯一一类不创建新G的入口：C callback使用`ForeignReentry`，JS/WASM host callback使用`HostReentry`，都在原owner G上建立special child frame以保持goroutine identity、LockOSThread和callback语义；原outbound continuation持续suspend，直到外部调用最终返回。没有现存owner op的外部thread/host首次进入仍使用`newG`。Poller、ISR和GC callback不是G，只能投递token/record，不能在driver stack直接运行Go callback。

### 10.2 普通 async call

1. Parent 调用 child ramp，得到 initial-suspended child handle。
2. Parent 把 child 的 parent 设为自己，设置 suspend reason 为 `Call`。
3. Parent 执行 `llvm.coro.suspend`。
4. Resume 返回 scheduler 后，scheduler 把 `g.activeFrame` 切换到 child。
5. Child 继续执行、park、preempt 或完成。
6. Child 完成后，结果已写入 parent-owned result slot。
7. Scheduler acquire CompletionRecord，把 `g.activeFrame` 和parent chain先原子切回parent，同时把child移入scheduler-owned `DestroyPending` root。
8. 执行一次destroy/free；该路径不得调用Go callback或suspend。完成后移除DestroyPending，再resume parent消费结果。

普通函数调用只有一个 parent，不需要 #1532 的 push waiter 链。Waiter queue 只用于跨 G 的 channel、select、timer、I/O 和 future。

### 10.3 公共 frame header

每个coroutine通过 `llvm.coro.id` 关联一个ABI固定的LLGo promise/header region：

    CoroHeader {
        g              *G
        parent         CoroHandle
        descriptor     *FrameDescriptor
        allocationBase unsafe.Pointer
        resultSlot     unsafe.Pointer
        suspendReason  uint16
        lifecycleState uint16
        stateID         uint32
        flags          uint32
    }

`CoroHandle`、allocation base和promise地址不保证相同。Compiler生成 `coroHeader(handle)` accessor：pre-split语义使用 `llvm.coro.promise`，post-split pass把它固定为该target/frame layout的正确映射；runtime绝不把handle直接cast成header，也不读取LLVM frame第一个机器字判断resume/destroy状态。

每个suspend edge在publish状态前写入稳定 `stateID`。Pre/post-CoroSplit metadata都维护 `FunctionID + stateID -> source PC/GC map`，verifier检查每个可达suspend state恰有一个映射。Trailing storage由具体函数决定。

`FrameDescriptor` 至少包含：

- FunctionID 和 ABI version。
- frame size/alignment 获取方式。
- logical stack/debug state map。
- `scanMode = ConservativeWholeFrame | PrecisePerState`；前者给出可扫描range，后者才要求per-state live pointer map。
- result layout。
- panic/defer cleanup metadata。

### 10.4 Frame 分配

统一调用：

    coroFrameAlloc(size, align, descriptor) -> aligned frame
    coroFrameFree(frame, size, align, descriptor)

- Size 使用目标 pointer-width 的 `llvm.coro.size`。
- Alignment 来自 LLVM DataLayout 和 CoroSplit 后 frame 信息；不能硬编码为 8。
- 若 LLVM 版本不能直接暴露 frame alignment，LLGo post-CoroSplit pass 生成 descriptor，allocator 按 descriptor 对齐。
- Over-aligned allocation 保存真实 allocation base，destroy 后用 base 释放。
- Frame 从创建到注册进 G root graph 之间不能触发可见 GC 窗口。
- V1 frame地址从 `coro.begin` 到destroy保持稳定；moving GC必须pin frame或使用稳定handle indirection，不能搬移LLVM仍在引用的frame。
- Native 由 GC-visible allocator/arena 提供；WASM/WASI 使用 linear-memory size class；RTOS/baremetal 使用可配置 slab/static pool，均共享同一分配 ABI。
- 分配失败走 target-defined runtime OOM/fatal path。不能隐式退化为为该 G 创建线程栈，也不能在 ISR 中扩容。

Allocator必须维护live-frame root registry。GC按registry中的FrameRef + descriptor扫描，不能盲扫整个slab/pool；destroy先从所有wait/queue和G chain unlink，在GC handshake下注销live range，再按descriptor清零pointer words后复用slot。这样free slot的陈旧pointer不会永久保活对象，GC heap外的static slab也不会漏扫live frame。

### 10.5 生命周期

    Allocated
      -> InitialSuspended
      -> Active
      -> Suspended
      -> FinalSuspended
      -> DestroyPending
      -> Destroyed

不变量：

- 每个 frame 正好 destroy 一次。
- FinalSuspended 后不能再次 resume。
- Destroyed handle 不留在 ready、timer、wait 或 token registry。
- Destroy期间 `g.activeFrame` 不指向child；若allocator/GC可能观察它，child由DestroyPending registry临时保根。
- 结果在 destroy 前已复制到 frame 外的 result slot。
- Panic/Goexit outcome 在 destroy 前 release-publish 到 parent/G-owned `CompletionRecord`；frame header 的本地生命周期态在 destroy 后不可读取。
- 取消只设置 `CompletionRecord`/unwind请求并恢复该G执行显式cleanup；不能直接destroy仍含defer的frame chain。
- Root frame同样执行完整终结协议：先把outcome/trace复制到G或BoundaryRecord，原子清除`activeFrame/rootFrame`并移入DestroyPending registry，destroy/unregister后才发布terminal ack、把G转Dead。`blockOn`和外部consumer只能在terminal ack后释放BoundaryRecord/CallbackHandle引用。

### 10.6 无栈 lowering 与 executor stack 上界

每个 suspend edge 都必须满足 “stack cut”：

1. CoroSplit 把跨边 live 的 SSA value、addressable local、defer state 和 resume state 存入 frame。
2. `llvm.coro.suspend` 后当前 resume 函数返回 scheduler。
3. Scheduler 在自己的循环中选择下一个 `G`，不会从 waiter/IRQ/host callback 直接嵌套 resume。
4. 再次 resume 时只从 handle 和显式 `G` 状态恢复，不读取上一次 episode 的 native SP/FP。

编译器和 post-CoroSplit verifier 必须拒绝：

- 跨 suspend 存活且仍指向 executor stack 的 pointer。
- 跨 suspend 的 dynamic alloca、`stacksave/stackrestore` 或 setjmp/longjmp state；应改为 frame/heap object或给出诊断。
- 把 native frame address、return address或 callee-saved stack image写入 continuation。
- 在普通 plain函数中隐藏 `llvm.coro.suspend`。
- Scheduler、waker、ISR或host callback对正在运行/已嵌套handle直接resume。

固定大小 local 若跨 suspend，必须进入 coroutine frame；不跨 suspend 的 temporary 可留在共享 executor stack。Go `make`、逃逸对象和动态大对象继续走 heap。无法证明有界的递归 SCC 必须 coroutine 化，使递归深度消耗显式 child frames而不是递增保留 native stack。

每个 target 还声明 `executorStackBytes`、`maxPlainStackBytes`、`foreignBoundaryStackBytes`和`maxForeignDepth`。Compiler生成plain call-region的保守stack-cost summary；超过阈值的函数要拆分、减少local、转coroutine boundary或在strict embedded profile下拒绝。该summary不是实现goroutine stack growth，而是保证每次resume episode对共享机器栈的使用有界。

Summary分两阶段生成：IR阶段计算call graph/atomic cost，LLVM codegen后从MachineFrameInfo/stack-size metadata取得每个plain symbol以及post-CoroSplit ramp/resume/destroy symbol的最终frame bytes；linker在无环plain call graph上求最长路径。间接call使用descriptor候选最大值，未知archive/C/asm必须提供可信上界或在strict embedded profile报错。

每个平台最终验证：

    MaxEpisodeStack =
        platformSchedulerBase
        + max over {root/ramp/resume/destroy}(
              splitSymbolMachineFrame
              + reachablePlainDAG)
        + targetABIRedZone
        + interruptReserve

`MaxEpisodeStack <= executorStackBytes`是link条件，不能只校验plain DAG而漏掉coro resume中不跨suspend的大fixed local、寄存器spill、scheduler trampoline和IRQ嵌套。Foreign/host boundary stack不计入G continuation，但必须单独满足`foreignBoundaryStackBytes × maxForeignDepth`及permit预算。

Runtime 可选配置每 G 的 `maxFrameDepth/maxFrameBytes`，用于资源受限 target 在 frame pool耗尽前给出确定性 fatal/resource diagnostic。Native 默认可动态增长 frame graph；baremetal 可静态预算。无论策略如何，G数量都不增加机器栈数量。

## 11. Scheduler 模型

### 11.1 M/P/G

#### G

    G {
        id
        atomic state
        rootFrame
        activeFrame
        ownerP
        lockedM
        waitReason
        parkState       ParkState
        resumePermitEpoch
        taskCancelState
        preemptRequested
        preemptDisable
        pendingRequest[RequestKind]
        seenEpoch[RequestKind]
        pollBudget
        quantumDeadline
        panicState
        intrusive ready link
    }

#### P

    P {
        id
        localRunQueue
        timerHeap
        sourceSet
        runSliceCursor
        currentG
        seenEpoch[RequestKind]
        allocatorCache
        status
    }

#### M

    M {
        id
        currentP
        currentG
        platformHandle
        schedulerStack
        foreignDepth
        seenEpoch[RequestKind]
        pinnedQueue
    }

Native 上 M 是 pthread，P 数量通常受 GOMAXPROCS 控制。JS/WASM、单线程 WASI 和 baremetal 初期折叠为一个 M、一个 P、多个 G。

这里的结构只表达ownership，不冻结字段排列。V2等待由稳定G内嵌的`ParkState`、直接park frame拥有的`WaitSetRecord`和source-owned `OperationRecord/ParkLink`共同表示；旧`parkGeneration + wakePending`只属于legacy单等待迁移层，不能继续作为channel、select、timer或I/O的新契约。

#### Execution-domain handoff

`LockOSThread`绑定的是G到物理M，不是把P永久绑定到同一M。一个thread-affine foreign call
不可避免地阻塞locked M时，active LLVM frame和本次native调用栈仍留在locked M；P、driver、
ready queue和source catalog则由replacement M临时服务。这个边界不能复用普通runnable
transfer：被阻塞G仍是`Running/FrameActive`，既不是可迁移runnable，也没有产生park result。

target-neutral `ExecutionDomainHandoff`为此冻结一个8-byte、无指针baton。handle只包含
`{generation, ownerEpoch}`，状态转换为：

```text
Idle
  -> Publishing -> Released
       Released -- Claim --> Claimed -- RequestReturn --> ReturnRequested
       Released -- RequestReturn ------------------------> Returned
       ReturnRequested -- FinishReturn ------------------> Returned
       Returned -> Completing -> Idle
       Idle -> Retired
```

- `Publishing`在发布epoch前先独占下一generation，claimant不能观察半发布handle；
- `Claim`与`RequestReturn`在线性化的同一32-bit状态字上竞争。未claim的release可直接撤回；
  已claim的replacement必须在不再接触任何P/driver/source字段的稳定边界执行`FinishReturn`；
- `Completing`先清除owner epoch再发布`Idle`，复制或延迟的complete不能跨reuse污染下一次调用；
- generation耗尽永久`Retired`，不允许ABA回绕；
- gate归属物理M/execution-domain slot，而不是P route；不包含G、P、driver、handle pointer或
  callback。Native pthread、RTOS task、host realm与baremetal core按稳定M/domain slot找到
  gate，再携带POD handle；
- replacement M若再次进入thread-affine blocking call，使用自己的gate把同一P继续交给下一
  replacement。由此多个blocked M可形成受`maxThreads`约束的链，而不是让一个P只能保存一个
  detached resume；每个blocked active resume仍只由其M上的显式record保根；
- doorbell、等待和replacement容量属于target adapter。core不自旋、不创建线程，也不引入
  第二套scheduler。

active resume core现已用M-local `ExecutorResumeHandoff`完成baton两侧的逻辑切换。Detach只接受
compiler prologue已经消费run decision、`driver.run.issued=ActionCheckResume`、active frame仍为
`FrameActive/SuspendNone`且当前G精确持有`P.osThreadLockOwner`的窗口；它保存
`{driver,G,ActionResume,budget}`，清除
`P.current/inResume/action/runDecisionTaken/servicePreemptBudget/osThreadLockOwner`与
`driver.run.issued`，并把G置为不入任何queue的`GForeignWaiting`。`G.runP`继续绑定原P，使
`TaskControlSource`可在replacement owner上把Abort/Shutdown保持为sticky请求。

replacement仍只能运行现有bounded reducer。`ExecutorResumeHandoffReturnable`要求没有issued
physical action、poll A/ack/B transaction已经回到Idle、P没有current/resume/action/lock owner；
ready queue和durable source request可以保留。Target在该边界`FinishReturn`并strong-join后，
原M先重获managed-execution permit，再由`RestoreExecutorResume`精确恢复原action、预算、
driver receipt和LockOSThread owner。record成功restore后立即清零；重复或结构不匹配的restore
fail closed。record归属M而不是P，因此replacement再次阻塞时使用自己的record，仍支持有界链。

Native已经把该core接到完整物理路径：固定10,000槽BSS M目录、每route active-M slot、
scalar pthread入口、create/claim/return/join、`borrowedWait` publication及公共
doorbell/timer/poll physical-wait primitive均已落地。replacement保持logical driver Active，
只借用原P/source和公共reducer；同route channel、timer、socket poll、最后可见子G完成以及
M0→M1→M2 nested handoff的linked E2E已通过。所有managed-start后的owner pthread都由
启动期创建、永不执行用户代码的clean factory串行创建；locked M只提交scalar slot并同步等待，
不会把其signal mask、namespace、cwd/fs view、credentials等可继承状态传播给replacement。
factory在所有owner strong-join后才停止并自join；原生C并发测试以16个已污染请求线程验证
隔离和restart/stop线性化。剩余生产化项是把per-call create/join升级为有界standby-M缓存、
与`runtime/debug.SetMaxThreads`联动，并覆盖blocked parent遇到command shutdown/fatal、
callback/reentry和异常退出的完整矩阵；这些不能由当前成功路径外推。

### 11.2 G 状态机

    New -> Runnable -> Running -> Dispatching
             ^          ^             |
             |          |             +-> Runnable       (preempt/yield)
             |          |             +-> Parking -> Waiting
             |          |             +-> GCStopped
             |          |             +-> ForeignWait
             |          |             +-> HostWait
             |          |             +-> CoroWaiting
             |          +---------------- direct frame/baton handoff
             +--------------------------- wake/foreign completion/GC resume

    Running/Dispatching -> RootDestroying -> Dead

`Dispatching` 表示resume episode已经返回scheduler、M暂时拥有G但尚未决定direct resume还是入队。任何suspend/final completion先执行 `Running -> Dispatching`，再按reason提交：

| Suspend/事件 | Frame/record动作 | 提交后的G状态与位置 | 下一owner |
|---|---|---|---|
| `Call` / structured await | parent suspended，activeFrame切child | Dispatching；若quantum到期可转Runnable | 当前M direct或ready queue |
| `FrameComplete` | publish completion，activeFrame切parent，child入DestroyPending后销毁 | Dispatching | 当前M direct或ready queue |
| `Preempt/Yield` | activeFrame不变 | Runnable，exactly-once入队；preempt放队尾 | 任意允许的M |
| `Park` | exact ParkTicket已seal，candidate operation已release publish | Parking；owner提交后按sticky fact变Waiting或进入resolution/detach | wait owner / source-affine ready queue |
| `GCStop` | 发布stateID和stack/root状态 | GCStopped，STW list | GC恢复后原/任意M |
| `ForeignCall` | publish ForeignOp | ForeignWait，foreign-op registry | foreign worker/targetM；完成后ready |
| locked active foreign call | M-local `ExecutorResumeHandoff`保根，P/source交给replacement | GForeignWaiting，不入queue且active frame留在原M | replacement服务P；return/join后原M恢复同一resume |
| `ForeignReentry start` | 在owner G push special child | ForeignWait -> Dispatching -> Running | 持有C boundary的M |
| `ForeignReentry return` | publishcallback result并pop child | Running -> Dispatching -> ForeignWait | 返回同一C thunk |
| `HostCall/Reentry` | publish HostOp/ReentryRecord；push/pop special child | HostWait与Dispatching/Running间受控切换 | 持有host boundary的M/entry |
| `CoroSwitch` | 当前G转CoroWaiting，对端G取baton | 两个G状态/owner一次原子提交，不入普通queue | 当前M direct |
| `RootComplete` | publish最终record/trace，清active/root，root入DestroyPending并destroy/unregister | RootDestroying；完成后发布terminal ack并转Dead | runtime terminal consumer |

ForeignReentry/HostReentry child若park，使用普通Parking/Waiting协议但pin到持有该external boundary的M/entry；ready后只能由该boundary恢复。Call/frame-complete等direct transition也必须经过Dispatching提交activeFrame，不能在callee、waker或callback stack里嵌套resume。

所有转换由集中函数执行并在debug build校验。Direct handoff不入queue；其余Runnable转换必须release CAS后exactly-once enqueue，M取得Running时acquire并设置 `M.currentG/P.currentG`。任意时刻ready queue最多包含一个G实例。

### 11.3 Ready queue

第一阶段使用锁保护队列验证正确性。Native 多 P 阶段：

- 每个 P 有 owner-fast local deque。
- 本地 enqueue/dequeue 优先。
- 外部线程、timer poller 和跨 P wake 写 global injection queue。
- 本地溢出时批量转移到 global。
- 空闲 P 随机选择 victim，偷取约一半 runnable G。
- 每执行固定数量本地任务检查 global queue，防止全局饥饿。
- Preempted G 放队尾。
- `runnext` 只能有限使用，避免 ping-pong 饿死其他任务。

Ready 和 wait link 内嵌在 G/等待对象中，普通切换不分配 queue node。

等待完成后的G先是`source-affine ready`，不自动等于可偷取的Runnable：winner record、result lease或cleanup disposition仍属于原P时，只能留在原owner队列。原P通过source-specific静态hook把exact ticket、outcome、case和typed payload物化到compiler提供的frame-local `ResumePacket/ResultCell`，对被prompt cancellation压制的结果执行Discard，并结束所有需要跨P读取的source lease；release-publish packet后，G才成为P-neutral runnable并可进入global injection或work stealing。新P acquire packet后只能选择normal continuation或cleanup，不能回访旧route/source。packet未物化、ticket不匹配、重复消费或仍带source lease时一律fail closed。

### 11.4 Park/Wake handshake

必须正确处理 wake-before-park。

Park preparation由owner P执行短小事务：`BeginParkSet -> Attach* -> Seal -> Prepare/Commit`。`ParkTicket`标识本次G的logical wait；每个candidate另有带source/route/local/generation的`OperationID`，两者不能合并。事务seal前失败必须同步abort并撤销已准入registration；seal后active frame执行suspend并回到scheduler，其他M、producer、host callback或ISR都不能直接resume handle。

Producer只向exact `OperationID` release-publish sticky fact并请求可合并doorbell。early fact可以发生在G仍为`Parking`时，但事实保存在source-owned record；owner P完成整个SourceSet publication barrier后才扫描受影响`WaitSetRecord`、决定logical outcome并启动loser detach。winner result lease必须已有exact owner，且所有loser都达到Detached或pointer-free tombstone，G才进入source-affine ready；Take/Discard在resume gate或P-neutral packet物化时完成。物理backend的Quiesced与slot Recycle仍是更晚、独立的阶段。

`OperationRecord`用一个byte表示完整result lifetime：winner为`Empty -> Owned -> Leased -> Taken|Discarded`；有物理result的loser在source完成真实cancel/rollback之后执行`Owned -> Discarded`。Irreversible/Reservable只有成功publication建立`Owned`；Ready hint保持`Empty`，成功`TryCommit`必须在exact pre-effect gate之后、同一个owner-serialized且不可重入的静态dispatch握手中调用唯一bind入口，bind成功才产生Succeeded attempt。Ack拒绝仍为`Owned`的loser，Consume拒绝非`Owned` winner，Recycle拒绝winner的`Owned/Leased`和loser的`Owned/Leased/Taken`。因此Take与Discard不是同一个“已消费”bit，重复或stale capability不能改变terminal intent；该状态机不携带interface、func value或allocation，也不改变`OperationRecord`的80/60-byte跨目标布局。

固定标量结果使用source slot可选内嵌的`ScalarResultCell`，不进入`OperationRecord/G/P/ParkState/WaitSetRecord`。V1 payload固定为28-byte/align-4 POD：`Meta uint32 + Words[6]uint32`；Meta五段依次为8-bit version、8-bit kind、4-bit逻辑scalar数、4-bit物理word数和8-bit flags，V1只接受`kind=Words`、0..3个逻辑`uint64`且word数必须为其两倍。每个值显式按`low32, high32`保存，未用word为零，因而32-bit、WASM和不同endianness都不依赖原生`uint64`内存布局。36-byte cell另存exact两字`OperationID`，stale generation不能读取或清除新payload；winner还必须用`OperationResultLease`的logical ticket验证。异步producer仍先写source-specific atomic mailbox并release-publish，owner acquire-drain后才把POD复制进普通cell；cell自身不是跨线程mailbox。Irreversible/Reservable在publication前stage，Ready在物理effect后与exact request同一握手bind；Take先复制再结束lease，Discard和loser cleanup先清cell再改变ownership，失败路径事务性保留原cell。无payload Timer不内嵌cell，因此没有额外slot成本。

Stale generation、duplicate fact和已经terminal的operation静默拒绝或按debug策略fail closed，绝不能重复enqueue。等待对象只发布事实，不拥有G frame；同一suspension epoch的`ResumePermit(frame, epoch)`只能被scheduler消费一次。

内存序要求：

- waiter、result record和candidate link初始化后release publish。
- producer在验证exact generation并取得admission lease后写payload，再release发布terminal fact；owner acquire后才读取payload。
- logical terminal、detach、quiescence和recycle各自有独立状态/ack，后者不能倒推或覆盖前者。
- `ResumePacket`/completion先写typed result和control kind，再release发布ready；queue pop或`Runnable -> Running`后acquire读取。
- parent恢复前acquire `CompletionRecord`；只有`Return`读取普通结果，其他kind进入cleanup。

初期可使用锁或 seq-cst 原子；状态机稳定后再细化 acquire/release。

## 12. 抢占式调度

### 12.1 能力边界

POSIX signal、RTOS tick、baremetal SysTick 和 JS timer 都不能在任意 PC 调用 `llvm.coro.suspend`。抢占分两步：

    platform tick / epoch / budget
              |
              v
       preempt requested
              |
              v
       compiler safepoint poll
              |
              v
       active frame suspend
              |
              v
       G requeued at tail

请求可以异步发生，完成切换必须位于当前 coroutine 的显式 suspend 点。

### 12.2 Poll 插入点

- 每个 loop latch/backedge。
- 递归函数入口或递归 SCC 的调用边。
- 超过静态 cost 阈值的长基本块。
- 未知/间接 Go 调用前后。
- blocking foreign call 返回后的强制 safepoint。
- 已有 channel/timer/netpoll suspend 点。

优化和 CoroSplit 后运行 verifier：除证明所有 cyclic control-flow path 经过 poll，还要按 target-machine cost 对任意相邻poll之间的最大加权路径求上界，包括长但无环的路径以及内联plain helper摘要。Poll 使用具有可观察内存副作用的 runtime/atomic 读取，并按 LLVM 需要标记，防止被删除、合并或 hoist 到循环外。

Safepoint分两类：`SuspendSafepoint` 只存在于coroutine frame，可preempt/park并返回scheduler；`StopSafepoint` 可存在于plain activation，只允许当前M在原native activation上参加STW/同步GC，绝不切换G。Allocation slow path是StopSafepoint：它发布当前M的stack map/保守stack range，等待或作为initiator执行GC，GC结束后原调用原地继续。不能在plain函数里隐藏 `llvm.coro.suspend`，也不能通过重试整个plain函数破坏已发生的副作用。

### 12.3 Budget + epoch

请求按kind拥有独立slot，target object也拥有独立ack generation；不能用一个`P.seenEpoch`代表迁移中的G：

    RequestSlot[kind] {
        activeEpoch
        targets       // explicit G/P/M/world target set
        ackSet
        nextTargets   // requests arriving before activeEpoch is fully acked
    }

    kind = Preempt | GCStop | Profile

同kind请求在前一generation未ack完时只能合并到明确的`nextTargets`或排入有界next generation，不能覆盖active request；不同kind互不覆盖。G-target比较/更新`g.seenEpoch[kind]`，P-target使用`p.seenEpoch[kind]`，foreign-M/STW target使用`m.seenEpoch[kind]`。非target对象绝不能替target推进seen；G迁移到另一个P后仍携带自己的pending/seen状态。

Fast path：

    g.pollBudget -= staticCost
    snapshot = acquireLoad(requestSummary)
    if g.pollBudget > 0 && !pendingFor(g, p, m, snapshot) {
        continue
    }

Slow path按固定优先级处理所有pending kind，而不是只读取最后一个request：

    pending = collectTargetedRequests(g, p, m)
    if g.preemptDisable != 0 {
        g.pendingRequest |= pending
        continue
    }
    if pending has GCStop {
        reason = GCStop
        llvm.coro.suspend
        // scheduler commits GCStopped, then advances the actual target's seenEpoch and acks
    }
    if pending has Profile {
        publishLogicalSample(g)
        // scheduler/profile owner acks only after sample state is stable
    }
    if pending has Preempt || quantum expired {
        reason = Preempt
        llvm.coro.suspend
        // scheduler enqueues, then advances g/P target seenEpoch and acks
    }

`requestSummary`只是“可能有未处理请求”的fast-path hint，不承担ownership；即使发生false positive也进入slow path重新读取各slot。Scheduler只在对应handoff/sample/stop提交成功后清除该target的pending并推进seen。所有target ack后才关闭activeEpoch并发布next generation。

请求发布时已Waiting/GCStopped的G，其frame状态已稳定：GCStop可由controller枚举root后ack，Preempt可记为无需切换并更新该G generation；Runnable G只打pending标记，直到某M取得它并完成handoff。Dead target从target set安全移除。任何这些操作都更新实际G/M/P的seen，而不是借当前P代ack。

- Native sysmon/timer thread更新 epoch，并唤醒 idle executor。
- RTOS/baremetal tick ISR 只更新预分配 flag/epoch。
- JS/WASM 连续执行时 host timer 无法运行，因此以编译预算为主；scheduler slice 到期必须返回 host。
- WASI 可在调度边界读取 monotonic clock，循环内部仍以 budget 降低开销。

Scheduler只在完成对应handoff后清除pending request并重置budget。时间片属于G，不在每次进入child frame时重置，否则深调用可以逃避抢占。

`runtime.Gosched` 是显式 `SuspendYield`：当前active frame在安全点suspend，G进入当前P队尾，不等待timer/host event，也不创建新frame。

上述`g.pollBudget`只约束managed resume episode，不能代替executor的service budget。每次executor entry还接受统一reduction budget，并以同一账本计费source slot/fact、affected wait-set、candidate apply/detach、G dequeue/resume/destroy、inline-ready wrapper和连续child await；可拆工作保存cursor，select winner决策只允许在声明的`MaxSelectCases`内有界overshoot。`RunSlice`返回`{status, used, more, blocked, nextDeadline}`：`RetryBudget`设置`more`并安排下一次外层迭代或host entry，`AwaitExternalFact`设置`blocked`且不能因同一operation同时设置`more`。callback、ISR、同步`requestRun`和doorbell都只publish fact，永不因`more`递归进入executor。timer publication epoch在入口冻结一个monotonic `now`，不能因分片扫描让同一epoch的不同slot观察不同时间基准。

### 12.4 有界抢占条件

硬保证首先定义为world-running CPU时间：目标G所在executor实际获得CPU、world未因GC停止时，从request到G交还scheduler的时间。

    T_preempt_cpu <=
        T_max_poll_gap
        + T_poll_slowpath
        + T_max_runtime_critical
        + T_max_plain_atomic_region

Wall-clock观测还包含runtime无法在所有host上硬约束的外部项：

    T_preempt_wall <= T_preempt_cpu + T_STW + T_OS_deschedule + T_host_reentry

Native BDWGC pause和OS调度只能独立测量/SLO，不能伪称严格wall-clock bound。RTOS/baremetal若要声明realtime wall-clock bound，target manifest必须同时给出GC heap扫描、关中断、最高优先级任务和host re-entry上界。

必须同时满足：

- 每条无限 managed 路径包含 suspendable poll。
- sync leaf 的最大执行 cost 低于配置上界。
- `nopreempt` 区域短小并经过 verifier。
- foreign blocking call通过ForeignOp stack-cut和有界worker/targetM处理。
- 平台 event loop 能及时重新进入 scheduler。

Verifier不能只看Go/LLVM IR CFG。Backend必须审计或提供target-machine proof，覆盖变量长度memcpy/memmove、hash/crypto helper、compiler-rt libcall、LL/SC retry和汇编循环。输入相关操作要切块、lower成可挂起实现或ForeignOp；LL/SC使用有界retry + scheduler-aware slow path。

Strict和release coroutine构建要求 `unboundedRegions == 0`。未知/输入相关 `MaxAtomicCost`、无summary archive/C/asm或backend新引入循环都是link error，不能只打印warning后仍宣称有界抢占；实验profile可显式opt-out，但capability必须降级且CI不得计为通过。

### 12.5 抢占禁止区

Runtime 提供 nesting counter：

    preemptDisable++
    critical operation
    preemptDisable--
    if pending && preemptDisable == 0 {
        immediate poll
    }

适用范围：

- scheduler/run queue 的短临界区。
- frame 和 G 状态提交。
- GC allocator/write barrier 的关键区。
- channel/select waiter 注册的不可分割阶段。
- C ABI attach/detach 过渡。
- `procPin`。

用户 `sync.Mutex` 不自动禁止抢占；竞争者应 park。`runtime.LockOSThread` 只 pin G 到 M，不等于禁止抢占。

### 12.6 LockOSThread

- Running G 调用 LockOSThread 后设置 `lockedM`。
- 该 G park/preempt 后进入对应 M 的 pinned queue，不能被其他 M steal。
- Locked G park时，其M释放P并等待该G，不执行其他G；scheduler可唤醒replacement M保持P的并行度。
- Locked G ready后唤醒对应M，由该M重新获取P再resume。
- UnlockOSThread 清除绑定后，G 可在下一 safepoint 迁移。

当前production的locked foreign子路径已经在同M C调用前detach active resume，通过
`ExecutionDomainHandoff`和active-M slot把原P/source交给replacement，释放/重获process
managed-execution quota，并在strong join后恢复exact resume。`GOMAXPROCS=1`同route
channel、timer、socket poll及nested replacement的linked对抗测试已通过。这里仍未完成的是
任意locked G普通park/preempt后的pinned queue、locked G异常退出/command shutdown时的M退休、
standby M缓存和完整`entersyscall/exitsyscall`矩阵，因此不能把foreign子路径等同于全部
`LockOSThread`语义。

Full语义要求locked G使用固定M，且该M在G park/preempt期间不运行其他G；因此还需要replacement M维持其他P进展。单M JS/WASM、WASI和baremetal不能同时满足“该线程不运行其他G”和“locked G park时其他G继续”：

- `threadAffinity=Full`：Native、多worker Wasm threads或多task RTOS按上述模型。
- `Degraded` 单M契约：只支持bounded + NoSuspend且unlock前不触发preempt的短locked region；期间不调度其他G，因此仍保持exclusivity。若动态路径尝试park/yield/preempt，runtime在suspend前给出明确capability fatal，不能静默让其他G使用该M。
- Strict profile要求编译期证明上述restricted region，否则对LockOSThread报target capability错误；`Unavailable` target在链接时拒绝。

调度器不能通过禁用所有抢占来静默“支持”长locked region；这会违反高并发有界抢占目标。

## 13. 阻塞 C 和 syscall

LLVM coroutine不能捕获活动C/host frame。发起可能阻塞或同步回调Go的外部调用前，Go continuation必须先stack-cut；Go→C/host→Go的同步callback仅能按下述ForeignReentry/HostReentry协议把child coroutine suspend回仍在等待同步返回的受控boundary loop，不能从外部栈内部恢复原caller continuation。

### 13.1 Native

默认把未知 foreign call 视为可能阻塞，除非有 `//llgo:noblock` 或可信 runtime metadata。

以下优先级针对具有已知高层语义的外部operation；不能只看raw syscall number就自动加入等待或重试：

1. 可表示为fd readiness/completion的操作接入netpoll，当前G直接park。
2. 平台有真正async API时提交token，当前G park。
3. 只有compiler证明bounded + nonblocking + no-callback的短C/intrinsic才允许在当前resume episode内inline调用。
4. 其余C/syscall一律lower成显式 `ForeignOp`，包括ThreadAffine调用。

#### Syscall family的透明异步化

在`-scheduler=coro`下，compiler/runtime把`syscall.Syscall`、`Syscall6`、`RawSyscall`、`RawSyscall6`、`RawSyscallNoError`及target/internal变体识别为特殊调用族，而不是不可分析的普通汇编叶子。Go源码签名、参数求值、单次kernel invocation、trap/result/errno、`EINTR`、short result和错误返回风格完全不变；lowering结合callsite contract、常量syscall number与target metadata选择：

1. 只有`internal/poll`等wrapper明确声明`PollWait/Retry`契约时，才可在收到`EAGAIN`后注册readiness token并由wrapper按原逻辑重试。公开Syscall/RawSyscall primitive本身绝不隐式wait/retry，O_NONBLOCK fd必须立即返回EAGAIN。
2. 平台completion/host async API若能证明与一次kernel operation的result/cancellation完全等价，可提交event token；否则潜在阻塞primitive在ForeignOp thunk中原样调用一次。
3. 经证明bounded、nonblocking、no-callback的调用可在当前episode直接执行，仍计入MaxAtomicCost。
4. `ThreadScoped`调用，例如gettid、signal mask、TLS相关操作，stack-cut后绑定调用时M的干净thunk，不能搬到任意worker。
5. Fork/exec/exit/thread-create等`ProcessControl/NoReturn`调用使用专门runtime协议，不能伪装成普通可返回ForeignOp。
6. Number动态未知时默认绑定调用时M以保留thread observable；strict profile若没有scope/no-return/blocking metadata则拒绝，显式compat-degraded模式才允许按有界foreign permit执行并报告其限制，绝不默认交给普通worker。

`WaitPlatform/WaitHost/WaitForeign`进入跨包effect summary并沿SSA call graph求不动点。因此标准库中继续写`r1, r2, errno := syscall.RawSyscall(...)`即可；所有可能等待的上层函数自动成为coroutine primary并透明await，无需为`os`、`net`、`internal/poll`、DNS或driver维护async源码分叉。`Raw`在这里保留低级调用ABI、errno和thread-affinity约束，但不强制把潜在阻塞OS调用留在活跃Go native stack上。

跨suspend传给syscall的Go对象必须由argumentRecord强保根并按需要pin。对于`//go:uintptrescapes`或compiler-known pointer→uintptr provenance，compiler必须在整数化丢失类型前把源Go object加入`gcRoots`并pin到ForeignOp terminal ack；runtime不能事后从uintptr数值猜回root。原本可能指向executor stack的local必须先spill到稳定LLVM frame/heap，verifier拒绝把临时alloca地址交给异步thunk。Kernel/libc result及TLS errno必须在同一thunk内立即复制进resultRecord。

Runtime初始化早期、signal handler、fork child exec前、IRQ以及已经持有不可重入runtime锁的路径不能启动scheduler。这些调用必须使用私有`RawCritical` plan：编译器证明bounded/NoSuspend/NoAlloc/NoCallback并计入target cost，否则strict构建拒绝。它们是窄化的runtime边界，不应迫使普通标准库RawSyscall保持同步阻塞。

    ForeignOp {
        ownerG
        typedThunk
        argumentRecord
        resultRecord
        gcRoots
        targetM
        ancestorForeignOp
        permitClass
        generation
        state
    }

ForeignOp协议：

1. Caller在active frame中按Go顺序求值参数，把跨边界值复制/固定到GC-visible operation record。
2. 取得有界foreign permit；普通top-level op没有permit时以`ForeignCapacity`原因无栈park。若请求来自ForeignReentry child，必须记录`ancestorForeignOp`并使用独立reserved reentry permit；不得等待祖先链正占有的普通permit。Reserved配额或`maxForeignDepth`耗尽时在进入新C call前确定性resource-fatal/返回显式资源错误，不能形成“C等callback、callback等祖先permit”的死锁。
3. 发布op，将G置为 `ForeignWait`，执行 `llvm.coro.suspend` 并完全返回scheduler；此时owner G的continuation只在LLVM frame。
4. Scheduler从干净的M/scheduler stack调用typed foreign thunk。普通op可放有界worker；ThreadAffine/LockOSThread op投递到指定locked M，该M不运行其他G，只执行这个thunk。
5. 执行C前释放P。其他M取得P继续managed工作，M/worker总数受target/thread limit约束。
6. C返回后必须立即进入compiler/runtime `foreignComplete` intrinsic：先release-publish result/completion并把owner G提交到pinned/global runnable队列，再释放foreign permit。其间不得执行用户Go指令、allocation、defer、普通barrier或在无P状态继续Go。
7. G以后由正常scheduler获取P并resume，在frame中读取结果、解除临时pin/root。

Go→C→Go重入callback不恢复原foreign-call continuation。External C thread首次进入Go仍通过`newG + BoundaryRecord`创建root；已有ForeignOp的同步重入则使用：

    ReentryRecord {
        ownerForeignOp
        argumentStorage
        resultStorage
        completion
        panicRecord
        gcRoots
        generation
    }

Wrapper先在ForeignOp/boundary registry中分配并保根ReentryRecord，把callback参数、aggregate临时量和result slot全部复制/指向该record，再在同一owner G上建立`ForeignReentry` special child frame和logical boundary marker。显式C pointer仍遵守cgo lifetime/pinning，但child绝不能引用wrapper alloca。Nested reentry深度和foreign boundary stack均有硬上限。

每次进入或恢复managed callback前，boundary M必须attach/register runtime、完成当前STW handshake并获取P；然后才设置`M.currentG/P.currentG`并resume这个pinned child。Child可以透明park/preempt，但每次suspend都返回受控boundary loop，先清currentG并释放P；ready后同一boundary M重新获取P，只恢复该child。不能在无P状态执行用户Go、allocation、barrier或defer。

ForeignReentry completion固定为：

- `Return`：release-publish到ReentryRecord，pop并按DestroyPending协议销毁child，之后才把result复制回C ABI、释放record、恢复ForeignWait并返回C。
- `Panic`：先运行全部Go defer并冻结trace，绝不language-unwind穿过C。V1默认process-fatal；只有外部ABI明确提供cooperative abort/错误outcome且C已确认退出时，才可把整个ForeignOp提交为非Return终态。
- `Goexit/Abort/Shutdown`：同样先运行defer；默认process-fatal。不能在C仍执行时先唤醒owner G、释放record/permit或伪造正常callback返回。

若boundaryPolicy支持cooperative nonReturn，整个ForeignOp只能在C确认退出后提交一次terminal completion；默认process-fatal路径绝不恢复owner G。专项测试覆盖nested callback panic/Goexit、LockOSThread和permit回收。

Foreign thunk可能永久占用一个M stack，但它下面没有active Go resume frame，owner G也不依赖该stack恢复。并发数量由permit严格限制，额外caller仍以LLVM frame park，因此不会退化为per-G native stack。

`ForeignWait` 不能被普通cancel直接wake/destroy。Cancellation只原子设置 `cancelRequested` 并调用可选OS/API cancel hook；`ForeignOp`、argument/result storage、gcRoots和owner frame至少保留到thunk确认退出并发布唯一completion。Generation用于拒绝重复/过期完成，不能代替lifetime ownership。V1若C永不返回且无法cancel，graceful shutdown可报告stuck foreign op，但不能制造UAF或并发resume。

### 13.2 JS/WASM

非线程 wasm 中 blocking C/JS 会阻塞整个实例。只能使用：

- Promise/event token + host re-entry。
- Worker/wasm threads。
- JSPI 或显式 opt-in Asyncify。
- 编译期拒绝不兼容的同步阻塞边界。

即使启用JSPI/Asyncify，managed G也必须先stack-cut并发布ForeignOp/BoundaryRecord。Transform set只能覆盖outer host ABI或从干净scheduler stack调用的foreign thunk，必须排除LLVM-coro ramp/resume和普通managed call graph；shadow foreign operation数量受maxForeignM/host token预算限制并由link verifier检查。

`syscall/js.Value.Call/Invoke/New`、`wasmimport`和其他可能同步回调Go的host call按`HostOp`处理：

1. Compiler依据host import metadata标记`MayCallback/WaitHost/ThreadAffine`；只有证明bounded且no-callback的调用可在当前episode direct。
2. 其余调用先把参数/result/root复制到GC-visible HostOp，owner G进入HostWait并stack-cut，host thunk从干净boundary stack调用JS。
3. 若JS在该调用中同步调用`syscall/js.FuncOf`产生的Go callback，建立同owner G的HostReentry child和ReentryRecord；attach/STW/P/currentG、park/resume以及nonReturn规则与13.1一致。
4. Host thunk返回后先publish result/exception，再由正常scheduler恢复owner G；原outbound continuation绝不在JS callback stack中直接恢复。

没有active HostOp的JS外部事件首次调用Go callback时用`newG + BoundaryRecord`。`syscall/js.FuncOf`创建CallbackHandle；`Func.Release`按9.3的close/refcount/generation协议注销，迟到调用必须拒绝。同步HostReentry child若可能WaitHost，会因同一JS event loop尚未获得控制而死锁，因此只允许NoSuspend/YieldOnly或通过closed-world completion proof的本地structured wait；否则要求JSPI/async contract或在进入park前诊断。

### 13.3 RTOS 与 baremetal

- RTOS blocking driver 放在专用 task，通过 token 唤醒 G。
- Baremetal driver 使用 interrupt/state machine。
- ISR 只写预分配 token ring 或 sticky flag。

### 13.4 `blockOn` 规则

`blockOn` 只允许在 `M.currentG == nil` 且没有active managed resume activation的最外层hard sync ABI。ForeignReentry/HostReentry boundary可保存owner G identity，但开始drive child前`currentG`仍必须为nil；child每次resume前必须取得P并临时设currentG，suspend返回干净boundary loop后立即清除并按协议释放P。它不是managed递归blockOn的例外。

Managed G及其DirectPlain activation内部不能递归启动scheduler；它们必须由effect传播后直接调用coro entry并await。CallPlan verifier和runtime assert双重检查这一点。

Native/WASI `blockOn` 可以 pump scheduler 和 platform wait。JS/WASM若遇到MayPark而没有closed-world completion proof，或等待未来host event，必须转换为async export/JSPI/Asyncify，否则立即报错，不能busy-loop。

RTOS普通task或baremetal main boundary只有在中断/event source仍可推进且port声明hostReentry时才可pump；ISR/exception context永远禁止 `blockOn`。

Boundary自身的C/host调用栈可以在最外层同步契约期间存在，但它不保存任何G continuation；每次G suspend仍返回同一个outer scheduler loop。Runtime限制 `blockOnDepth`，禁止managed递归blockOn，并对foreign callback嵌套给出deadlock/reentrancy诊断。这样保留的是有界外部ABI栈，而不是per-G stack。

## 14. Scheduler-aware 同步原语

现有 pthread cond 版本不能继续用于 coroutine 模式。若同一 executor 上 G1 持锁后被抢占，G2 再阻塞 pthread cond，该 executor 可能永久无法恢复 G1。

### 14.1 Semaphore

- 原子 fast path 尽量复用。
- Slow path 把当前 G 注册到 wait queue，执行 park。
- Release 将 waiter 变为 Runnable。
- Waiter node 从 G 或 per-P pool 获取，不在热路径任意 malloc。

### 14.2 Mutex、RWMutex、WaitGroup、Cond、Once

- 保留 Go 状态机和原子 fast path。
- `Semacquire`、`notifyListWait` 等底层等待统一 park G。
- `procPin/procUnpin` 绑定当前P并临时禁止抢占；`sync.Pool` 使用per-P local/victim并在GC周期执行cleanup，不能继续由全局pthread mutex模拟P0。
- Scheduler 自身使用独立的短临界 native mutex，函数标记 `nopreempt/nosuspend`。
- 不允许在 runtime scheduler lock 内执行 Go callback、分配或 suspend。

### 14.3 Channel

- Send/recv waiter 保存 `*G`、value slot、generation 和 select ticket。
- Buffer 操作和 wait registration 在 channel lock 下完成。
- 匹配后 release publish value，再 ready 对端 G。
- Close 以批量 wake 处理 send/recv waiter。
- 不直接从 waker resume handle。

### 14.4 Select

- 每次select创建一个logical `ParkTicket`和稳定`WaitSetRecord`；所有candidate分别保存exact `OperationID`、case index、预生成随机rank以及自己的readiness/reservation generation。source只发布ready fact，不能按扫描先后直接CAS成winner或resume G。
- 每种candidate静态声明一种commit contract：`ReadyThenTryCommit`在channel等自身同步域内用exact readiness generation执行`TryCommit`；失败或stale只消费该次hint，source要在状态再次可提交时发布新generation。`Reservable`先取得exact reservation，logical winner之后source仍须physical `Commit`，loser须`Rollback`；对应ack完成前仍计入promotion barrier。`IrreversibleCompletion`只允许结果可显式Discard的operation参加多路等待。
- Resolver在本轮完整sticky snapshot上按随机rank尝试candidate。多个ready case只有一个logical winner；ReadyThen失败后继续其他candidate，全部失败则重新probe/repark。带`default`时，只有本轮所有非nil candidate都已完成“当前不可提交”的exact probe，且不存在待确认reservation/commit时才能发布明确`Default` outcome，不能把没有ready通知或budget耗尽当成证明。
- `Shutdown/Abort`在发出source commit request前被claim时进入cleanup；commit成功后物理副作用不能撤销，late prompt cancellation只能压制normal continuation并Discard结果lease。普通operation cancellation与completion竞争同一terminal ownership，不能跳过尚可能成功的TryCommit而伪造cancel winner。
- Winner确定后所有loserregistration仍须cancel并达到Detached或pointer-free tombstone；reservation physical commit/rollback都ack后，G才可进入source-affine ready。winner result lease可随exact RunDecision保留到resume gate，但normal continuation或P-neutral迁移前必须Take或Discard；ticket、candidate generation不匹配以及重复commit/rollback/Take一律fail closed。

该协议保持Go channel select的一次求值、nil禁用、closed send/receive、uniform pseudo-random selection和default语义，但不引入STM日志、handler对象图或新的语言级async API。

## 15. Timer 与 I/O

### 15.1 公共 timer core

Runtime 维护 timer min-heap，平台只负责 monotonic clock、arm earliest deadline 和唤醒 scheduler。

    Sleep
      -> register embedded timer
      -> park G
      -> platform alarm/event
      -> scheduler drains due timers
      -> ready G

- 每个 P 可有本地 timer heap；单 P 即公共 heap。
- Stop/Reset/fire 使用 generation。
- 保持Go 1.23+同步timer channel语义：成功Stop/Reset后不能收到旧配置的stale value；`GODEBUG=asynctimerchan` 兼容路径单独测试。
- Period timer 按理论 deadline 推进，避免 callback 延迟造成永久漂移。
- Ticker在receiver跟不上时按标准语义丢tick，不积累无界callback/G。
- `AfterFunc` 必须经 `newG` 创建LLVM-coro root G；ISR/poller/JS callback只提交timer token，绝不直接运行用户callback。
- `Sleep` 不需要创建 channel。
- Wall clock 与 monotonic clock 分离。

Go 1.23+还要求未Stop且已不可达的channel Timer/Ticker可被GC回收。Timer heap因此不能用强引用永久保活`NewTimer/After/NewTicker/Tick`的user timer/channel：scheduler保存带generation的`TimerLease`弱handle；fire前在GC handshake下尝试提升为临时强root，GC sweep则CAS detach lease并向scheduler提交unlink token。Fire、Reset、Stop与GC-detach只有一个generation获胜，迟到事件不得触碰已回收对象。`Sleep`的当前G和`AfterFunc`的callback必须强保活到完成/成功Stop，因为二者语义上仍有待执行工作。Nogc target无法判断unreachable，capability report必须明确channel timer/ticker在Stop前不自动回收；不能把它宣称为完整GC语义。

这允许复用 Go runtime timer 的状态机思想，但不能直接复制依赖 per-P、gopark/goready、netpoll 和 hchan 的实现。

### 15.2 I/O

外部 driver 只提交整数 token：

    register(wait specification) -> token
    completion(token, generation)
    scheduler resolves token -> *G
    schedReady(g)

不能让 JS host、ISR 或任意 C driver 长期保存裸 Go heap pointer。

第一版稳定 registration 采用固定容量 one-shot mailbox table，平台可见 ABI 只有两个 `uint32`：

    WaitRegistrationHandle { slot, generation }

- table slot 的 producer-visible 前缀只有原子 `state/generation/inflight`；`*P`、`*WaitToken` 和 ticket 只在 scheduler-owned 后缀中，callback/ISR 不读取它们。
- `Post` 先取得 admission lease，再以 `Active -> Posting` 唯一认领；未来 result payload 只能由该赢家写入，最后 release-publish `Posted`。duplicate、closed 和旧 generation callback 静默丢弃。
- table 的 `pending` 只是 coalesced doorbell，`Posted` slot 才是事实源。scheduler 清 pending 后扫描固定表；与并发 Post 交错时至多多一次 doorbell，不会丢 event。通用 MPSC ring 仅作为后续加速，overflow 时仍必须回退全表扫描。
- close 先把 inflight word 的高位原子设为 admission sealed，低位保留已经进入的 callback 数；physical unregister 必须给出 strong quiescence ack：禁止新 callback，并 join 所有已进入 callback，包括尚未取得 slot lease 的入口窗口。只有 sealed 且低位为 0 时才能发布 logical cancellation。
- completion/cancellation 与 waiter claim 共享 exact-generation 8 状态 `WaitToken`。state 0 在 generation 0 表示初值，在非零 generation 表示 consumed-completed；独立 consumed-canceled 状态使 outcome 在下一次 Arm 前保持可查询，避免 scheduler consume 后把 `CompletionWon/AlreadyCanceled` 退化为未知。
- `Retire` 同时要求 physical quiescence 和 scheduler 已消费 matching outcome；Free slot 继续保持 admission sealed。复用时先 `Free -> Initializing`，发布 owner 和新 generation，最后重新开门并发布 Active，旧 callback 不可能把旧 handle 投递到新 token。
- 除 `Post/Pending` 外，table 控制 API 全部由同一 scheduler owner 串行调用。backend ack 只能投递给 owner，不能从 callback/ISR 直接执行 `ConfirmQuiesced`。
- 当前 slot 尚不携带 syscall result payload，真实 pipe/eventfd、JS microtask、WASI poll、RTOS notification 和 IRQ/WFI doorbell 也尚未接入；本阶段完成的是 target-neutral lifetime/ABA/cancel 基线。

Platform completion 还需要一个稳定的 executor request gate，不能在 callback/ISR 中保留 `*P` 或读取 `P.currentG`。第一版同样采用固定容量 registry，平台 ABI 为：

    ExecutorHandle { slot, generation }

每个 active generation 的 gate 只有以下合法组合：

    Running                    = 0
    Requested                  = 1
    IdleArmed                  = 2
    Requested | IdleArmed      = 3
    Closed                     = 4

- producer 必须先发布 durable source，再调用 `Request(executor)`。`Running -> Requested` 表示正在执行的 G 会在 compiler safepoint 观察请求，不需要平台 doorbell；`IdleArmed -> Requested|IdleArmed` 的唯一赢家返回 `IdleWake`，此时才必须 doorbell；已有 Requested 时只合并。
- running G 的 poll 只 acquire-observe Requested 并 yield，绝不清位。scheduler 取得所有权后先 drain timer/wait/channel/syscall 等事实源，再 `Acknowledge Requested -> Running`，随后无条件重扫所有事实源；第二个 producer 可能在 drain 与 ack 之间合并，不能把 gate 当作完成队列。
- idle 协议是 `ArmIdle(0 -> IdleArmed)`、重扫事实源、`CommitSleep(exact IdleArmed -> IdleArmed)`、进入 retained-doorbell wait。Request 若先赢则 commit 失败；commit 若先赢，后来的 Request 仍看到 IdleArmed 并响铃。retained wait 必须保存“commit 已成功但物理 block 尚未开始”窗口中的 wake，例如 pipe/eventfd 字节、latched host task、RTOS notification 或 IRQ pending bit；普通 edge-only callback 不满足契约。
- real/spurious wake 后 scheduler 先 `LeaveIdle`，只清 IdleArmed 并保留 Requested，再执行 drain/ack/recheck。`Acknowledge` 只接受精确 Running/Requested；`LeaveIdle`、`ArmIdle`、`CommitSleep` 和 close 对非法/Closed 组合 fail closed。
- close 通过精确 `Running -> Closed` 与 Request 竞争，再 seal producer admission。physical unregister/join 必须覆盖 callback 进入 registry lease 前以及 `Request` 返回到 doorbell 完成之间的整个 shim。因为 close 可能赢在 durable Post 与 Request 之间，join 后必须再做一次无条件 durable-source drain，才能确认 quiescence并复用 generation。
- Phase 17 的 `ExecutorDriver` 已把一个 exact-zero `ExecutorRegistry` generation 和一个空 `WaitRegistrationTable` 绑定到单个空闲 `P`。绑定按 scheduler-owned pointer first、原子 `executorMode` last 发布；绑定前调用者必须 strong-quiesce 所有仍可能持有 `*P` 的 legacy `RequestSchedule` source，因为 mode 只是 capability guard，不是旧 ABI 的 admission/refcount barrier。绑定后 legacy `RequestSchedule` fail closed，producer 必须使用稳定的 POD executor handle。
- 绑定后的 registration table 只接受同一 `P` owner，standalone `Drain` 被拒绝。平台 `Post` 仍只发布 durable slot；scheduler-owned driver 才能解引用 waiter、promote `G`，而 drain/cancel 不再写旧 `P.schedule`。
- 运行中 G 的 `PollPreempt` 只 observe stable Requested 并 yield，同一个请求可被多个 poll 重复观察；只有重新取得 `P` 的 `PollExecutor` 可以 ack。driver 每轮对当前已接入的事实源执行“registration table 全表扫描和 ready promotion → ack → 无条件全表重扫”，若第二次扫描产生工作、pending 仍在或新 Requested 可见则继续，覆盖 producer 在 drain 与 ack 之间合并的请求；timer/channel/syscall source set 接入后必须加入同一 transaction。
- `PrepareExecutorSleep` 只在无 runnable 且仍有 parked G 时 `ArmIdle`；随后不依赖 advisory pending，而是无条件扫描 `Posted` 事实源并检查 request/schedule，再选择 exact `CommitSleep`。若 request/事实源赢得 prepare 或 commit 竞争，只执行 `LeaveIdle` 并设置统一runner的`sourceMore`，不会在idle helper中嵌套legacy全量poll。成功 commit 只授权 target 进入 retained wait；真实或 spurious wake 必须先以`WakeExecutor[/At]`完成`LeaveIdle + sourceMore`，再由统一run-step reducer完成drain/ack、typed materialization与recheck。
- 常规 driver close 只允许 scheduler idle、无 parked G、无 live registration；ready G 可留给后续 command cancellation。`BeginExecutorClose` seal gate 后，target 必须 strong unregister/join 整个 ingress shim（包括 pre-lease entry 和 Request-to-doorbell tail），之后 `ConfirmExecutorClose` 才做 final source scan、retire generation、解绑 table 和 `P`。
- Phase 18 已为“root frame 已 destroy、当前 G 是队列中最后一个、executor 仍 bound”增加显式 terminal-close handoff。core 先执行 durable-source drain→executor ack→无条件重扫，再以 exact gate close 与 Request 竞争并 seal producer admission；成功后只在 driver 保留 `terminalKind`，清除已释放 frame 的 `g.root`，并把 `P.action` 切换为不携带 handle 的 `ActionTerminalExecutorClose`。已 destroy 的 LLVM handle 不进入持久 scheduler 状态，stale `ActionDestroy`/`ActionPanicDestroy` 也无法再通过 `expectedAction`。
- target 完成 strong unregister/join 后由 scheduler owner 调用 `ConfirmTerminalExecutorClose(driver)`。Confirm 不依赖原 caller stack，而是从稳定 driver/P 状态恢复 G、close marker 和 `terminalKind`；它在 join 后无条件执行 final source scan，随后 `ConfirmQuiesced`、`Retire`、解绑 registration table，按 `p.executor=nil`、driver zero、内部 commit token 恢复、`executorMode=Unbound` last 的顺序发布。最终 normal 或 panic terminal commit 只在 core 内调用 `Destroyed`/`PanicDestroyed` 重试，并只能向 adapter 返回 `ActionComplete` 或 `ActionPanicComplete`；该路径没有第二次 `llvm.coro.destroy` 操作。这个稳定状态交接允许 WASM/embedded 在异步 join 期间返回 host，不保留 managed continuation 或 native scheduler caller stack。
- Phase 19 把 adapter runner 从含混的 `bool` 结果改为显式 stop/drive 状态，并以静态direct dispatcher避免func value、interface或不必要的同步/异步双版本；Phase 31随后删除whole-G `coroRunActions`循环，改由bounded physical-action dispatcher驱动。runner 在任何需要等待target的边界都先返回调用者，不保留其Go/native/host activation。
- 第一版program runner静态拥有且只绑定一个`ExecutorRegistry + WaitRegistrationTable + ExecutorDriver`。绑定发生在root handle ingress前；target start只能接收稳定的`ExecutorHandle {slot,generation}`，不能接收`*P`、`*G`、`Action`或LLVM coroutine handle。terminal close确认后必须同时证明driver、registry和registration table已全部retire，才允许完成program lifecycle。
- retained target operation只在静态program state保存`Continuation {kind, epoch}`，其中kind当前为executor wake、terminal join或command join。平台异步完成后通过`__llgo_coro_program_continue_v1(epoch)`干净重入；重入先经过只含两个原子`uint32`的`DriveAdmission {Owned|Pending, epoch}`。精确epoch只有一个scheduler owner；在target Begin返回前或另一个drive期间到达的同epoch completion只合并Pending，由现owner在释放前claim并poll，因而既不递归drive也不丢早到事件。zero、mismatched和已经clear的late/duplicate epoch只被忽略，不读取或污染lifecycle；已取得精确owner后的kind、target或scheduler invariant不匹配才fail-stop。epoch到`MaxUint32`后拒绝继续发布，避免wrap产生ABA。该状态不包含caller stack、`G`、`Action`、LLVM handle或平台callback指针。
- normal main仍有ready child时，runner先完成generic executor close和target strong join，再调用`BeginCommandShutdown`、取消ready child并完成command shutdown；因此target callback不会与被取消的scheduler对象并发。last-G normal/panic继续使用Phase 18 terminal close。fatal panic仍有peer、main返回时存在parked/live registration等更一般的teardown尚未闭环，继续fail closed，不能提前宣称完整command/fatal shutdown。
- target API是编译期选择的静态函数集：start、begin close、poll close、begin wait和poll wake。`coro_target_none`没有任何外部ingress，因此只能对已经sealed且空的executor做“无物理callback可join”的同步确认；它不支持retained wait。测试adapter同时覆盖同步/异步terminal join、同步/异步ready-child command join、park→retained wait→wake→继续drive、panic、Begin内早到completion以及并发/stale/duplicate continuation，但它不是production backend。`DriveAdmission.CanRelease`也只允许在target strong join并确认没有ingress后作零状态断言，不能代替并发内存回收屏障。
- runnable runtime plan把`__llgo_coro_program_continue_v1`作为精确`func(uint32)` required root，并把symbol和签名纳入bootstrap driver hash。因为target callback是out-of-band入口，entry object还通过一个internal constant function-pointer anchor及一次volatile load形成真实链接relocation；最终链接测试证明独立archive member能在`--gc-sections`/`-dead_strip`下被抽取并保留，同时startup不会调用continue。
- 该层仍是target-neutral single-P runner。Native wake pipe/eventfd、WASM/JS `requestRun`、WASI poll、RTOS notification和baremetal IRQ/WFI retained-doorbell backend都尚未接入；允许返回`Suspended`的production target还必须同时接管platform entry lifetime/finalize，保证host不会在callback重入前销毁program。因而Phase 19证明的是无栈重入和生命周期协议，不是完整production platform executor。
- Phase 20 首个 Linux/Darwin native backend 在当前唯一 executor/main thread 上同步阻塞 `poll`，因此 `BeginExecutorWait` 只返回 `Complete`，不返回 `Pending`、不调用 `continue`，也不创建 per-G 或 per-executor pthread。它使用独立的 CLOEXEC/nonblocking pipe：`PrepareExecutorSleep` 已完成 `ArmIdle -> durable-source recheck -> CommitSleep`，其后到物理 `poll` 前到达的 `IdleWake` 先设置原子 latch 再写 byte，故该窗口由 retained byte/latch 闭合。EINTR 重试，EAGAIN/EWOULDBLOCK 视为已保留 wake，drain 读到 EAGAIN 才结束；异常 write 仍保留 latch，最长一次 bounded poll 后重查，异常 poll/read/EOF 则 fail-stop。
- native producer 的外部 ABI 是 `__llgo_coro_native_post_wait_v1(waitSlot, waitGeneration, executorSlot, executorGeneration) uint32`，只携带 POD generation handle；返回低字节 `WaitRegistrationPostResult` 和次低字节 `ExecutorRequestResult`。它既是required plain root，也由always-selected entry中的独立constant function-pointer anchor及volatile load形成最终链接relocation，独立archive member在 `--gc-sections`/`-dead_strip` 下仍会抽取且startup不会调用它。完整 shim 顺序固定为 `TargetIngress.Enter -> 精确 executor handle 验证 -> Post durable wait -> Request -> 仅 IdleWake 时 nonblocking pipe Ring -> TargetIngress.Leave`。`Leave` 是最后一次 target/FD 访问；close 先 `Seal`，通过单次 1ms poll 让步并反复检查所有已准入调用 `Quiesced`，strong join 后才 close FD、Retire ingress并返回 `Complete`，因此覆盖 pre-registry-lease 和 Request-to-doorbell tail，也没有 close/reuse 后写旧 FD 的窗口。整个 strong join 没有全局超时：若已准入 producer 永不执行 `Leave`，shutdown 会一直等待而不会冒险复用内存或 FD；持续 EINTR 也可能延长单次 poll 的实际墙钟时间。`TargetIngress` storage本身是永久static tombstone，Retire后只允许释放pipe等外部资源，不能释放、清零或复用该word。
- 同步 native wake 不得递归调用 `coroProgramDriveV1`；内部 `DriveAgain` 只交给外层迭代 pump。host fake 连续 2048 次 park/wake 的 `maxWaitBeginDepth=1`，证明 executor stack 深度不随历史 wake 增长。当前 producer ABI和门禁已经被native build plan作为 exact SyncDemand/DirectPlain root保留，但timer/syscall/netpoll source尚未调用它；`nogc` profile允许普通pthread调用，GC profile只允许由runtime/collector注册的producer thread（例如 `GC_pthread_create` 创建）进入。任意foreign thread、signal handler和ISR均尚未支持，后两者也没有async-signal-safe证明。
- Phase 21 增加 owner-side `wait_prepare/rollback/retire_completed` ABI。prepare 在当前 executor 正执行 `ActionResume` 的 frame 内一次完成 `ArmWait + Register`，只返回 owner ticket 与 producer 可复制的 `{waitSlot, waitGeneration, executorSlot, executorGeneration}`；Register 失败会把尚未发布的 armed ticket 转为已消费 cancellation，并保持 generation 单调，若该 CAS 异常失败则 runtime fail-stop，不能把 poisoned token 当作普通资源不足。外部 submit 在 callback 可达前失败时，只有已证明 source quiesced 的同一 resume owner 才能 rollback；完成路径也必须先 strong unregister/join，再由恢复后的同一 owner close/confirm/retire registration。producer 永远不接收 token、frame、P/G、table pointer 或 LLVM handle。
- Phase 21 的 Linux/Darwin `nogc` E2E 使用一个 entry 前创建的普通 pthread 作为真实 producer。线程只等待 C 静态四字 POD；test-only compiler capability 把 hook 放在 doorbell 已清 pending、即将调用真实 `poll` 的精确窗口，hook 释放 producer 并等待 `__llgo_coro_native_post_wait_v1` 完整返回，随后 production poll 消费已保留 pipe byte并恢复原 coroutine。恢复路径先 `pthread_join` 再 retire，最终断言 packed result `0x0201`、关闭后的 stale post `0x0403`、table/registry/ingress/pipe 全部终止，并从最终 binary 排除任意 `uv_`/`GC_` symbol。该测试证明真实 native ingress 与 retained poll 闭环，不代表 timer/syscall/netpoll 已经选择或提交这种 wait，也不引入 per-G pthread。

平台实现：

- Linux：初期统一 poll array + wake pipe，后续 epoll/eventfd。
- Darwin：kqueue 或统一 poll 过渡实现。
- WASI：`poll_oneoff` 同时等待 fd 和最近 clock deadline。
- JS/WASM：Promise completion 调用 wasm `notify(token, generation)`，再请求 `runSlice`。
- RTOS：task notification/event queue。
- Baremetal：IRQ 写预分配 SPSC/MPSC ring。

Token ring 满时设置 sticky overflow/rescan flag，不能静默丢事件。

## 16. GC 与 frame root

### 16.1 基本不变量

- 活跃 G 必须从 scheduler root registry 可达。
- G 在 Runnable、Running、Dispatching、Waiting、GCStopped、ForeignWait、HostWait、CoroWaiting 等全部非Dead状态都必须能到达完整root/active frame chain或明确的临时owner registry。
- Suspended frame 中的 Go pointer 必须被 GC 扫描。
- Timer、channel、select、I/O registry 持有 G root 或可解析 token。
- Frame unlink/destroy 后不得继续作为 root。
- Suspended G 不能依赖任何 M stack root；Running G 的 plain activation temporary由STW在该M到达safepoint后扫描executor stack/stack map。
- Frame、result slot和wait record写入Go pointer时遵守当前GC的publish/barrier协议。
- Plain activation中的allocation slow path可作为StopSafepoint保留当前activation，但该M不得调度其他G；GC必须扫描已发布的initiator stack range/map。

### 16.2 Native BDWGC

- G 和 coroutine frame 使用 scanned、uncollectable allocation，例如 `AllocRoot`。
- Destroy 后显式 `FreeRoot`。
- Executor 使用 GC-aware pthread 创建。
- M 的 current G 若在 TLS 中，沿用 GC-aware TLS root 注册。
- Foreign call 中的 pthread stack 仍由 BDWGC 注册和扫描。

普通 C `malloc` frame 不满足这些条件。

### 16.3 Nogc

- G/frame 使用 aligned malloc。
- Completion 后精确 destroy/free。
- 测试记录 create/destroy 和 byte count，已完成任务必须归零。
- 永久 blocked G 占用内存符合 goroutine 生命周期，但 stale timer/token 不得额外泄漏。
- 没有reachability collection时，`SetFinalizer`、`AddCleanup`、weak pointer和unique cleanup语义不可实现；strict profile必须在构建/链接时诊断，不能保留silent no-op。

### 16.4 Baremetal tinygogc

- `AllocRoot` 当前只是普通 tinygogc allocation，`FreeRoot` 是 no-op。
- Scheduler 全局 G registry、ready/timer/wait 链必须始终到达活跃 G。
- G再到达live FrameRef；tinygogc通过frame root registry按descriptor扫描，即使slot来自GC heap外static slab也不能漏掉。
- Unlink 后由后续 GC 回收。
- GC只能在scheduler或已注册StopSafepoint运行；plain allocator可用 `collectWithInitiatorStack` 同步收集，ISR不能触发分配/GC。
- 当前 tinygogc mutex 为空，第一阶段限制单 executor。
- Finalizer/Cleanup/weak需要新增registration、sweep queue和clear ordering；在实现前对应capability是 `Unavailable`，不是已有GC就自动支持。

### 16.5 未来 precise GC

CoroSplit 后 frame layout 由 LLVM 生成。未来精确 GC 可在 post-CoroSplit pass 生成：

- frame pointer bitmap，或
- 每个 suspension state 的 pointer map。

在此之前使用保守扫描。Target JSON 中现有但未被 Config 消费的 `gc: precise` 不能当作已有能力。

### 16.6 Write barrier

LLVM 自动生成的 spill store 可能绕过 LLGo 高层 write barrier。初期 GC 模式必须满足：

- BDWGC conservative scanned frame。
- tinygogc stop-the-world conservative scan。
- nogc 无 barrier。

若引入并发 precise GC，post-CoroSplit pass 必须识别 frame pointer stores 并插入 barrier，或者把 frame 放入每轮重新扫描的 root arena。

## 17. Stop-the-world

1. GC controller 增加 world epoch。
2. 对所有 P/G 设置 preempt request。
3. Running G在SuspendSafepoint suspend；位于plain allocation StopSafepoint的M保持原activation、发布stack root并ack，期间不运行其他G。
4. Idle/scheduler P 直接确认 stopped。
5. Waiting G 已经 suspended，其 heap frame 可直接扫描。
6. 枚举所有ForeignOp/M。释放P不等于M已停止：BDWGC必须由collector停住并扫描每个registered M；precise/moving模式必须等待foreign quiesce，或由target验证的pin + foreign-thread handshake保证C可见对象不移动且写入安全。
7. C/host callback重新进入Go或ForeignReentry/HostReentry前先参加STW handshake；未ack的foreign/host M不能执行managed callback。
8. 同时满足all-P ack和GC-mode要求的all-foreign-M ack/stop后，才能宣布STW完成并扫描G/frame/op root graph。
9. 恢复foreign M/world，并重新ready被GC suspend的G。

Debug watchdog 记录最长 `nopreempt`、foreign call 和 safepoint gap。STW 超时应打印 FunctionID、M/P/G 状态和最后 safepoint。

## 18. Panic、defer、recover 与 Goexit

Coroutine 迁移后不能继续把逻辑 goroutine 状态放在线程 TLS 或全局 handle map。

Frame completion 至少有：

    Return
    Panic
    Goexit
    Abort
    Shutdown

`Abort/Shutdown`是不可被`recover`捕获的runtime控制kind，不与普通operation返回的`context.Canceled`或I/O error混合；强度单调为`Shutdown > Abort`。每个非final resume先由compiler-owned gate取得exact `RunDecision`，再执行本地reconciliation：child await读取parent-owned`CompletionRecord`，park/select按ticket取得case与result lease，被prompt cancellation压制的winner也必须显式Discard。只有所有本地payload/physical disposition都已确定后，frame才进入normal continuation或共享cleanup入口；gate不能看到取消就直接destroy frame。

可能cleanup的frame使用可跨suspend的显式状态机`Idle -> Draining(cursor, control stack) -> AwaitingDeferredCall -> Draining -> PublishingCompletion -> FinalSuspended`。这允许defer自身park，同时保证LIFO节点exactly once；claim时冻结本次control cause，同一task cancellation在cleanup中保持sticky但不反复打断，后续request由terminal/shutdown policy记录而不能重入或跳过当前cleanup。

### 18.1 Panic 传播

1. Panic value 和 panic chain 记录在 G。
2. 当前 active frame 进入编译器生成的 async unwind/cleanup path。
3. 该 frame 依次执行 defer。
4. 若 recover 成功，清除对应 panic，当前 panicking function 按 Go 语义正常返回。
5. 若未recover，frame在final suspend前把 `CompletionRecord{kind, panicID, result}` 和该frame的logical trace segment复制到parent/G-owned storage，release publish；record不能位于即将destroy的child frame。
6. Scheduler acquire completion并保存nested-panic trace snapshot，先把activeFrame切回parent并将child放入DestroyPending root，再destroy child。
7. Parent await从稳定CompletionRecord观察Panic，进入自己的unwind path。
8. 逐 frame 传播到 root；未恢复 panic 打印 logical stack 后终止程序。

Recovered panic可释放对应snapshot；未恢复或defer中再次panic时，G-owned `PanicRecord` 保留各代panic chain和已销毁inner frame的trace segment，直到最终打印/终止。不能在destroy child后再从handle/header读取panic，也不能因为逐层destroy而丢失最内层栈。

Native `siglongjmp` buffer 不能跨 suspension 保存。Async frame 的 panic transport 必须满足“任何 jump/exception 只在一次 resume episode 内有效”。

统一 IR 语义固定为 `AsyncRaise -> frame cleanup -> completion`。后端可采用：

- `panicTransport=NativeEH`：目标ABI/LLVM unwinder。
- `WasmEH`：仅在engine、linker和所有module明确启用Wasm EH时使用，不能把JS exception当Go unwinder。
- `ExplicitStatus`：编译器隐藏outcome + cleanup edge，适用于无EH的WASM/WASI/baremetal。
- `EpisodeSJLJ`：只在当前resume episode内建立catcher，并保证每个plain defer frame都有landing/cleanup。

不允许从旧 native stack 直接 longjmp 到已 suspend 的 frame。

Panic本身不触发coroutine化，因此一次resume episode内仍可能存在 `plain A -> plain B -> panic`。独立 `PanicABI` 必须保证每个plain frame的defer/named result语义：

- 有LLVM EH/SJLJ unwinder的target为每个需要cleanup的plain frame生成landing pad；catcher只活在当前episode，不能跨suspend。
- 无可用unwinder的baremetal可把潜在panic/Goexit作为隐藏outcome沿managed internal ABI返回，并在每个callsite走显式cleanup edge；跨包summary/ABI hash记录该模式。
- 任何方案都不能只longjmp到active coroutine外层而跳过plain frame defer。Plain chain完成本地cleanup后，最外层coroutine frame才把Panic/Goexit转换为CompletionRecord。

Panic traceback的plain activation在unwind/destroy前写入per-G shadow/snapshot。Baremetal CI必须覆盖plain→plain多层defer/recover/Goexit，再跨child-await继续传播。

### 18.2 Defer

- Coroutine primary的static defer状态保存在coroutine frame；bounded plain activation的defer保存在本次executor-stack activation，并由target-wide PanicABI landing/status cleanup保证执行，绝不能跨suspend继续引用该栈。
- Dynamic defer node 由 G/frame root 可达。
- Deferred async function 在同一 G 中作为 child frame执行。
- 每次direct defer invocation创建动态 `RecoverToken{panicGeneration, ownerFrame}`，只注入该deferred callee的direct recover context。Token跨suspend保存在其frame中，但不传给callee再调用的helper；`recover` 原子验证/消费token，防止helper或重复recover成功。
- Frame destroy 前必须完成 defer 或明确处于 unrecoverable runtime abort。

### 18.3 Goexit

- Goexit 沿当前 G frame chain 执行所有 defer。
- Recover 不能捕获 Goexit。
- 非 main G 结束后变 Dead。
- Main G 调用 Goexit 后 scheduler 继续运行其他 G；若所有 G 永久等待且无未来 event，报告 deadlock。

## 19. Main、初始化和退出

平台entry建立runtime、P/M和一个bootstrap G。Bootstrap按依赖顺序执行package init，再按build mode执行`main.main`或embedding init entry。

- Sync init/main 可由 coroutine bootstrap 直接调用。
- MaySuspend init/main 使用 child coroutine await。
- `executionMode=Command`时，`main.main`正常返回立即请求进程退出，不drain其他G，保持普通Go命令语义。
- `Reactor/Embedded`时，bootstrap/main completion发布给host并`ReturnToHost`，runtime与export registry继续存活；未来host export首次进入仍可`newG`。何时保留或取消detached G、何时调用platformShutdown只由显式host shutdown和manifest `shutdownPolicy`决定，不能套用Command的隐式退出。
- 其他 G 的未恢复 panic：执行 defer/unwind 后终止程序。
- Command默认Go语义不变；embedding lifecycle是显式不同的build/host contract，不能静默改变同一artifact。

这取代 #1532 的“main 返回后调用 CoroSchedule 直到队列为空”。

## 20. 平台抽象

热路径使用固定 runtime symbols，不使用 Go interface 动态分派：

    platformInit(m)
    platformNanoTime() int64
    platformArmTimer(deadline)
    platformDisarmTimer()
    platformPollEvents(m, deadline) IdleResult
    platformWake(m)
    platformRequestPreempt(m)
    platformCriticalEnter() CriticalState
    platformCriticalExit(CriticalState)
    platformShutdown(code)

`IdleResult` 至少区分：

- `EventsReady`
- `DeadlineReached`
- `Interrupted`
- `ReturnToHost`

### 20.1 Target capability

`internal/targets.Config` 当前没有正式 GC/scheduler capability；部分 target JSON 字段会被忽略。新增显式、可验证配置：

    CapabilityState = Full | Adapter | Degraded | Unavailable

    TargetCapabilities {
        coroScheduler
        preemptSafepoint
        multiExecutor
        threadAffinity
        hostReentry
        blockingExport
        foreignBlockCompensation
        monotonicClock
        wallClock
        filesystem
        rawSocket
        process
        posixSignal
        dynamicLoader
        garbageCollector
        finalizer
        weakPointer
        reflectCall
        reflectMakeFunc
        ffiClosure
        wasmJSPI
        interruptBridge

        maxExecutors
        maxThreads
        maxLockedM
        maxForeignM
        maxWorkers
        maxG
        maxLiveFrames
        maxFrameDepth
        maxTimers
        maxWaitNodes
        maxHostOps
        maxCallbackSlots
        maxForeignDepth
        reservedReentryPermits
        eventRingEntries
        executorStackBytes
        maxPlainStackBytes
        foreignBoundaryStackBytes
        framePoolBytes
        outOfCapacityPolicy
        timerDriver
        eventDriver
        interruptModel
        executionMode       // Command | Reactor | Embedded
        quiescentPolicy
        shutdownPolicy
        panicTransport
        gcMode
        frameScanMode
    }

四态含义：

- `Full`：目标平台可提供声明的完整语义。
- `Adapter`：安装 host/board adapter 后提供完整语义。
- `Degraded`：API按该 target 的标准受限语义工作或返回标准错误。
- `Unavailable`：构建或调用时明确拒绝。

Target manifest 必须 strict decode，未知字段、互相矛盾的组合和缺失 runtime symbol 都是构建错误。JS/WASI/board port 可在启动时进一步协商动态 host capability，但不能把静态 `Unavailable` 升级为未经验证的 `Full`。

RTOS/baremetal/static-memory profile必须为G header、live frame/depth、timer、wait/select node、HostOp、callback slot、event ring、foreign depth和reserved reentry permit逐项给出有限容量。每次分配/注册先reserve再改变queue/root状态；容量满时按`outOfCapacityPolicy`返回该API允许的资源错误或确定性fatal，不能半入队、丢root、覆盖旧token或退化成新增thread/stack。Native也保留这些counter与可选limit用于压力测试。

构建时输出 package/API compatibility report；`--compat=hosted|sandbox|firmware|strict` 决定缺失 capability 是允许的标准裁剪、warning 还是 error。不能因为 target JSON 写了 `scheduler`、`cores` 或 `gc` 就宣称对应 runtime 已实现，也不能用永久等待或空 stub伪造支持。

### 20.2 平台矩阵

| 平台 | M/P | Timer/I/O | 抢占请求 | Frame/GC | 主要限制 |
|---|---|---|---|---|---|
| Native POSIX | N M / N P | 统一 poller + timer heap | sysmon/tick epoch | BDWGC root 或 nogc free | blocking C需ForeignOp worker/指定M干净thunk；数量有界 |
| JS/WASM 单线程 | host entry / 1 P | setTimeout + Promise token | 编译 budget 为主 | 初期nogc；完整档需linear-memory conservative/tiny GC | 必须返回JS；threadAffinity degraded |
| WASI 单线程 | 1 M / 1 P | poll_oneoff clock/fd | budget + clock | 初期nogc；完整档需frame-aware GC | pollable可阻塞；非poll host import需async/thread或降级 |
| RTOS | 1..N task / P | one-shot timer + notification | tick ISR flag | tinygogc/nogc | 初期单 executor |
| Baremetal | main loop / 1 P | compare IRQ + WFI | SysTick/预算 | tinygogc | 需HAL；threadAffinity degraded |
| WASM threads / MCU SMP | 多 executor | 跨 worker event | per-worker epoch | 并发 GC | 后续阶段 |
| AVR 等小 MCU | 1/1 | board timer | poll budget | tinygogc/static pool | frame/code size需单独评估 |

### 20.3 Native

- 初期单 P 验证状态机，之后启用 worker pool 和 work stealing。
- 多P producer identity不能沿用per-P local slot：V2 `OperationID`冻结为两字`source:8/route:9/local:15 + generation:32`，route在runtime instance内单调分配且退休后永久tombstone。Waiting/Running G不可偷；原owner必须先把winner payload物化到frame-local ResumePacket并结束source lease，才可把P-neutral Runnable G放进stealable deque。
- 每个 M 一份 OS stack，G数量不增加thread/stack；worker数量有硬上限。
- 每个同时parked的LockOSThread G需要保留M identity，但受 `maxLockedM/maxThreads` 限制；超限遵守 `SetMaxThreads` fatal语义。
- Poller 用 wake pipe/eventfd/kqueue 唤醒。
- Blocking foreign caller先stack-cut；worker或指定locked M从干净scheduler stack进入typed thunk，并在执行C前释放 P。
- LockOSThread pin G 到 M。
- Signal handler 只设置 epoch。

### 20.4 JS/WASM

Scheduler API 采用版本化 host protocol：

    runSlice(budget) -> { status, used, more, nextDeadline }
    notify(token, generation)
    requestRun()

流程：

1. JS 调用 `runSlice`。
2. Scheduler 执行到 budget 用完、无 runnable 或必须返回 host；source fact、affected wait、candidate apply和立即ready child同样扣reduction，不只计算loop扣预算。
3. 返回最近 deadline和`more`；`more`只要求安排新的host entry，当前entry必须先返回，callback/requestRun不得同步递归执行scheduler。
4. JS arm `setTimeout`/Promise。
5. Callback 调用 `notify`，再 queueMicrotask/requestRun。

所有 Go coroutine frame 位于 linear memory。一次 `runSlice` 返回时，Wasm operand/call stack 上不得保留 G continuation；managed runtime 不依赖 Asyncify。JSPI/Asyncify只允许包装明确的host/foreign边界。

启用这些adapter前也先把owner G suspend到LLVM frame；Asyncify transform list不得包含managed resume symbol，shadow stack只属于有界ForeignOp/BoundaryRecord。

仓库 `targets/wasm_exec.js` 已有 `runtime.sleepTicks`、`go_scheduler`、`resume` 的未接通脚手架，可借鉴生命周期，但新 ABI必须版本化并由 runtime 正式实现。

每个WASM export在ABI metadata中固定`exportMode = Sync | Async | Dual`。Compiler绝不能把已有Sync export静默改成Promise返回或更换symbol signature：Async由声明/host contract明确选择；Dual生成版本化的sync symbol与独立async companion。Sync函数若不满足下述completion proof，只能使用已声明JSPI能力或链接失败。

同步导出包含未证明可完成的park或等待未来host event时：

- 若exportMode允许Async/Dual，生成Promise-returning wrapper/companion。
- 可选 JSPI。
- Asyncify 只作为 opt-in C/host compatibility，不对全部 Go coroutine stack重复变换。
- 无能力时编译/链接报明确错误；dynamic fallback在runtime进入park前拒绝，不能等到event loop死锁。

### 20.5 WASI

- `platformPollEvents` 使用 `poll_oneoff`。
- 同时注册最近 timer deadline 和 fd subscription。
- Wasip1 command或host contract声明 `blockingExport=Full` 时，单线程scheduler可在无runnable时阻塞poll。
- Reactor/component同步export若其event依赖同一host loop，标记WaitHost并适用与JS相同的MayPark/completion-proof规则；没有async adapter/hostReentry时拒绝。
- Regular-file/path metadata等不可poll且可能阻塞的host import优先使用preview pollable/async接口、WASI threads或host worker。单线程host没有这些能力时，`foreignBlockCompensation=Degraded/Unavailable`：strict profile拒绝未知阻塞import；显式hosted-degraded build必须报告该段不能保证其他G前进或抢占，不能把`poll_oneoff`能力误报为覆盖全部I/O。
- 抢占主要由 poll budget 保证，调度边界校准 monotonic clock。

### 20.6 RTOS

- Scheduler M 映射为 RTOS task。
- RTOS task数量只由 `maxExecutors` 和有界driver workers决定，绝不为每个G创建task。
- One-shot timer 唤醒最近 deadline。
- Driver ISR 或 callback 提交 token/task notification。
- Blocking peripheral API 放独立 RTOS task。
- 多 scheduler task 前必须实现真正的 tinygogc STW 和线程安全。

### 20.7 Baremetal

- 主循环是唯一 M，单 P。
- G只分配frame slab/arena；main stack和IRQ stack按链接脚本静态预算，不随G数量增长。
- Hardware compare alarm 驱动最近 timer。
- 无 runnable 时 WFI/WFE。
- IRQ 只写预分配 ring/flag，主循环 drain 后 ready G。
- `runtimeNano()==0` 必须由 board HAL 替换。
- ISR 与主循环共享状态通过关中断临界区或真实原子实现。
- 初期只使用一个 core；RP2040 等多核能力不自动开启。
- Frame allocator提供 slab/size class 和可配置上限，减少碎片。

## 21. Deadlock、取消和资源所有权

Scheduler 在以下条件同时成立时报告 deadlock：

- 无 Running/Runnable G。
- 无未来 timer。
- 无注册 I/O/host token。
- 无尚可能完成的 `ForeignOp`、worker、host operation 或 syscall-like M。
- 无有效host-liveness lease，且executionMode要求以Go command语义判定deadlock。
- main G 尚未按正常返回终止进程。

Command模式保留Go程序“所有G均等待且无未来事件”deadlock。Reactor/Embedded模式可能由host在未来任意首次调用export，并不要求此刻已有token：已注册的WASM export、`syscall/js.FuncOf` CallbackHandle或embedding subscription持有host-liveness lease；scheduler quiescent时返回`ReturnToHost`而非panic。Func.Release/注销最后一个动态handle会减少lease，但静态reactor export是否永久保活由manifest决定。Lease只影响deadlock判断，不是G/frame root；generation和callback registry仍独立管理生命周期。

永久等待的 G 不自动取消。Runtime shutdown 时：

- Command的main正常返回直接终止，不要求逐G destroy；Reactor/Embedded的bootstrap返回不等于shutdown。
- Reactor/Embedded只有host显式shutdown才按`shutdownPolicy`选择立即终止或graceful cancellation/unwind；在此之前export和host-liveness lease仍可创建/唤醒G。
- 测试/embedded graceful shutdown可遍历G，标记runtime cancellation并wake，使其沿显式unwind运行defer；只有到FinalSuspended后才destroy。
- ForeignWait G只标cancel并等待ForeignOp completion/ack，不能提前unwind或释放C仍在使用的record/root；never-return foreign op单独报告。
- Stale token、timer 和 wait registration 必须按 generation 解注册。

取消不能压缩成一个boolean，而是`CancelRequested -> LogicalCanceled -> Detached -> Quiesced -> Recycled`的分层协议。Completion与取消竞争同一个exact-generation terminal ownership；已经发生的syscall、channel commit或其他物理副作用不能追溯撤销。`Detached`只保证source不能再访问G/frame/result pointer，`Quiesced`才保证旧backend callback不会再进入，只有后者完成且result lease已Take/Discard后才能复用slot generation。

Source admission同样必须无缝覆盖取消窗口：`reserve/attach -> observe sticky cancel -> backend Start -> recheck`，或由backend提供等价原子register。`Start`只允许一次，inline completion也只能publish terminal fact，不能递归resume或执行cleanup。固定容量source返回`Accepted | RetryBudget | AwaitCapacity | Unsupported`；`AwaitCapacity`依赖新的generation fact而不是本地自旋，并支持尚未Start的operation被cancel。worker/host/RTOS容量耗尽不能静默丢请求、无限创建线程或为每operation分配新的调度对象。

Coroutine frame ownership 始终属于一个 G；wait object 只借用 G reference，不拥有 frame。

## 22. Debug、Caller、trace 与 profiling

Native stack只能显示当前resume episode中的scheduler -> resume trampoline -> active plain/Go leaf chain，不能代表suspended parent。因此每个frame descriptor需要state-to-source metadata；suspended G完全不依赖native stack。

Logical traceback：

1. 从 G.activeFrame 开始。
2. 读取 FrameDescriptor 和 suspension state。
3. 映射到 Go function/file/line。
4. 沿 parent handle 到 root。
5. 若G正在Running，先展开完整active native/shadow Go chain，去掉scheduler/adapter frame，再接最深coroutine parent；若已suspend则不拼接M stack。

需要逐步支持：

- panic traceback。
- `runtime.Caller/Callers`。
- goroutine dump。
- scheduler trace：spawn、resume、park、wake、preempt、steal、destroy。
- block/mutex profile。
- CPU profile，把采样归因到 G 和 active frame state。
- race detector hooks：park/wake release/acquire、channel handoff、timer。

旧的 pclntab/caller 工作应通过 FrameDescriptor 接入，而不是依赖 LLVM resume 函数名字猜测源函数。

## 23. Build mode 与 ABI 版本

production不提供scheduler/profile切换：LLVM stackless coroutine是唯一架构，目标差异只通过冻结的`TargetCapabilities`和adapter catalog表达。不依赖仅在codegen中读取的环境变量切换ABI，也不允许以pthread fallback绕过缺失的平台能力。

Coroutine binary 导出：

    __llgo_coro_abi_v1
    __llgo_scheduler_abi_v1
    __llgo_panic_abi_<mode>_v1

Archive、package summary、runtime 和 linker 必须在coroutine、scheduler及PanicABI上完全匹配。Scheduler mode、panic transport、target capability 和 CoroPlanDigest 进入build cache fingerprint。

Pure sync library/archive不需要链接scheduler。Executable一旦选择 `-scheduler=coro`，仍由最小 `newG` LLVM-coro bootstrap运行init/main，以保持唯一G表示；若程序没有 `go`、MaySuspend或dynamic async，DCE可以移除timer/poller/work-stealing等大部分scheduler子系统，但不能把root退化为native-stack G，也不会为plain函数生成coro clone。

## 24. 建议代码布局

### 24.1 Compiler analysis

    internal/coro/
        effect.go
        graph.go
        flow.go
        plan.go
        summary.go
        verify.go

### 24.2 Build integration

- `internal/build/build.go`：在全 SSA program 构造完成后生成 Plan。
- `internal/build/collect.go`、`fingerprint.go`、cache manifest：保存 plan digest 和 summary。
- AST directive collector：在分析前统一收集 export/linkname/llgo directives。

### 24.3 Frontend lowering

- `cl/compile.go`：按 FunctionPlan 只生成 primary body。
- `cl/instr.go`：Call/Go/Defer/Invoke 的 sync/coro/dispatch lowering。
- `cl/expr.go`：function value representation conversion。
- 新的测试帮助器检查 symbol/descriptor absence。

### 24.4 LLVM builder

- `ssa/coro.go`：只封装 LLVM coroutine intrinsic 和结构化 suspend。
- `ssa/expr.go`、`closure_wrap.go`：Direct/Dispatch closure。
- `ssa/interface.go`、`abitype.go`、`type_cvt.go`：method descriptor ABI。
- `ssa/globaldce.go`：descriptor 后的方法 metadata。
- post-CoroSplit verifier/descriptor pass。

### 24.5 Runtime

    runtime/internal/runtime/
        sched.go
        sched_queue.go
        sched_park.go
        sched_preempt.go
        sched_timer.go
        sched_netpoll.go
        coro_frame.go
        coro_panic.go
        platform_*.go

并逐步替换：

- channel/select wait。
- sema/notifyList。
- time Sleep/timer。
- internal poll。
- goroutine-local defer/panic/Goexit/TLS。

## 25. 分阶段实现计划

### Phase 0：分析与 ABI 骨架

- 实现 Effect × Demand × FuncRep。
- SCC/worklist fixed point。
- 稳定 FunctionID、泛型实例、跨包 summary。
- 选择性 symbol emission。
- Direct/Dispatch closure 和 method descriptor ABI。
- Recursive aggregate FuncRep map、target-wide PanicABI和CallbackHandle trampoline registry ABI。
- `Syscall*`/`RawSyscall*`/host import effect metadata与RawCritical verifier。
- CoroPlanDigest/cache integration。
- MaxPlainDAGStack/MaxEpisodeStack/MaxAtomicCost summary、archive boundary canonical Dispatch。
- Pre-CoroSplit plan verifier检查NoSuspend closure/suspend coverage；post-CoroSplit IR verifier检查stack-address liveness、root/frame metadata；link verifier只查ABI/symbol/relocation和禁用runtime路径。

验收：纯 sync chain 只有 `F`；纯 async chain 只有 `F$coro`；动态 escape 才出现 descriptor/adapter；所有 `go` root和可挂起call都以LLVM-coro frame表示。

当前落地状态（2026-07-17，实验 physical ABI v0/v1；scheduler ABI 已扩展到 `llgo.coro.scheduler.program-bootstrap.v2.closed-static-spawn.v0`）：

- 全程序 SSA 的 Effect、Demand、FuncRep、稳定 FunctionID、精确 emission universe、单 primary symbol 选择和 `CoroPlanDigest` 已落地。明确 plain 或 coro 的函数仍只有一个主体；仅真正动态的 func/`any`/interface consumer 才进入 descriptor/dispatch。缺失、过期或目标布局不匹配的计划与 cache manifest 均 fail closed。
- LLGo 已固定使用 `cpunion/llvm` PR #5 的 LLVM 19–22 绑定。该分支吸收上游 LLVM 22 的完整 switch API 变更，并保留 LLGo 所需的 switched-resume builder/CoroSplit API；19、20、21、22 CI 均通过。LLGo 不再覆盖 LLVM 19 以下版本。
- closed static `CallDirect + DirectCoro` 已使用 caller-frame await：父 frame 按 Go 从左到右顺序求值参数、保存 typed result slot、创建 initial-suspended child，然后由 scheduler 独占 resume/done/destroy。值传输已覆盖 pointer、uintptr、function、string、slice、named struct、fixed array 和多返回值；不是仅支持 scalar。
- exact pure-SSA physical audit 已覆盖 stack alloc、local/global typed load/store、`FieldAddr`、static `IndexAddr`/`Index`、完整 fixed-array slice、`Field`/`Extract`/`Phi`、empty-interface direct value、受限 conversion/binop/unop、`len`/`cap`。heap escape、需要 allocation 的 interface box、slice 动态越界检查、pointer-containing global store、closure/type assertion/dynamic call 和任何隐藏 runtime helper 仍明确拒绝。
- `program-bootstrap.v2` 在 codegen 前冻结五阶段表：`[internal runtime.init, init$abitypes, public runtime.init, selected main-package init, main.main]`。managed Go 阶段根据唯一 primary 选择 `DirectPlain` 或 `CoroRoot`；public runtime init 若存在则必须使用其 exact managed body，不存在时才由 compiler 生成 no-op。Coro 表项只绑定 package anchor/descriptor index，不复制函数体，也不把 catalog 当启动列表。
- planner 已把 internal runtime init、selected package init 和 `main.main` 注入 managed demand。普通同步 Go/标准库调用风格不变，调用者根据精确 effect 自动被染成 coro；scheduler-stack hook closure 则是单独审计的 NoSuspend island，不能通过强改 demand 或放宽 trusted closure 绕过。
- frozen foreign `//llgo:coro noblock` certificate 当前只授予已审计的 `time`、`pthread_self`、`pthread_mutex_init` 和 `pthread_mutex_unlock`。证书只移除未知阻塞，`IRQUnsafe` 仍保留但允许在普通 G 上执行。真实 runtime init 仍被 `pthread_key_create`、`rand`/`srand`、`GC_malloc`、mutex lock、Memcpy/Memset 等未完成边界挡住。
- legacy PanicABI 仍是完整启动链的正式 blocker。exact proof 可追踪 `runtime.Panic → Rethrow → TracePanic → printany`，并在动态 `error.Error` 调用处停止；不能把该动态调用误标为 plain。新的 `llgo.coro.panic.explicit-status.v0` 已进入 digest、summary、cache、manifest 和 package/root ABI hash；`cl` 已有下述严格子集的 compiler lowering，但 `internal/build` 仍保留 target-wide 全局 gate，尚不能把它作为完整程序的 production PanicABI 开启。
- 多基本块 CFG、聚合值、PHI 和抢占 lowering 已完成。自然循环、循环入口及每 64 条有效指令的长直线块插入 poll；scheduler 的 P 级原子 request 只有在 slow path 才执行 publish/yield/`llvm.coro.suspend`，fast path 不切换。LLVM 19–22 上均有 native64/wasm32 pre-/post-CoroSplit 与 object 测试。
- 第一条 production `go` 路径已经落地：严格限定为 closed static、top-level、非捕获、非泛型、非变参、零返回的 `go f(args)`。编译器先按 Go 顺序完整求值参数，再以显式 parent G 执行 begin，调用 target 唯一的 `DirectCoro` primary 到 LLVM initial suspend，commit 后在 parent 上 poll/yield；runtime 不接收用户 callback，也不依赖 TLS。owner 与 target 都由精确 `YieldOnly` seed 进入 effect 传播，因此 target 即使当前很短也保留抢占点，普通同步 caller 则透明 await 同一主体。
- Command `main` 的正常 continuation 现在显式通知 runtime。main root 完成后，single-P shutdown 先整体校验 ready/wait/current/action 状态，再封闭调度 gate，按 FIFO 取 ready G、按 active-child 到 root 顺序直接 `llvm.coro.destroy`，最后每个 task storage 只释放一次。该 v1 路径只接收 `YieldOnly|AwaitStructured` target 且拒绝非空 wait set；panic/Goexit 不经过正常 main-return hook。
- terminal-only ExplicitStatus runtime core 已有 task-local 两字 `PanicRecord`、原子 once publication和无 TLS 的 `__llgo_coro_panic_prepare_v1(g, handle, header, typeWord, dataWord)`。compiler 对精确 cleanup-free PhysicalABIV1 body 生成 `SuspendPanic`/`FinalSuspended`，panic 与 normal return branch 到同一个 LLVM final suspend；active panic frame 先经过 `coro.done` 验证并 destroy，之后 suspended-await ancestor 不再 resume，而是从深到 root 直接 destroy，最终保留 record 并返回独立 `PanicComplete`。后续hard-cutover已把implicit language fault接入同一显式结果：无操作数的nil、bounds、channel和unsafe fault使用allocation-free V1静态payload；需要现场值的slice-to-array失败使用V2的两个target-width operand，由异常路径分配可跨LLVM frame destroy存活的GC可见`boundsError`，从而保持Go 1.26的动态类型、`runtime.Error`和逐值文本。V1/V2 hook均为required raw/plain runtime root并进入bootstrap/physical ABI identity；尚未定义的parameterized kind或不匹配的operand必须fail closed，不能静默退回无参数错误。更宽的dynamic explicit-panic payload、所有cleanup/recover/Goexit组合和最终进程报告仍分别由其专门验收矩阵约束，不能由本条fault ABI证据外推。
- park/wake handshake 已落地 32-bit 原子 `WaitToken`、generation ticket、early/late completion、park 前后 cancellation、唯一 waiter claim、ABA 范围校验及 terminal gate。完成/取消 outcome 在 scheduler consume 后仍保持到下一次 Arm，恢复后的同步风格 continuation 可用 exact ticket 查询赢家；精确 intrinsic `llgo.coroPark(token, ticket)` 被 Effect 分析识别为 `MayPark`，并在调用者当前 LLVM frame 中生成 park prepare、stateID、`coro.suspend` 和恢复路径，没有隐藏在普通同步 helper 中。channel/timer/syscall 的 submit/retry producer 尚未接入。
- 固定容量 `WaitRegistrationTable` 已实现 POD `{slot,generation}` handle、producer admission seal/refcount、`Active→Posting→Posted→Draining→Delivered` one-shot mailbox、scheduler-side Drain、strong unregister/quiescence handoff、cancel-vs-complete winner、silent late callback、generation reuse 和 capacity fail-closed。平台 Post 不接触 P/G/token/LLVM handle；Phase 17 绑定后 table 固定归一个 `P`，只有 driver drain 可解引用 waiter，standalone drain 和错误 owner registration 均 fail closed。race+shuffle 已覆盖 concurrent post、post-vs-close、旧 producer pin/reuse、pre-park cancel 和 exactly-once promotion。真实 backend 的 strong unregister/join 与 result payload 仍需各 target adapter 证明。
- 固定容量 `ExecutorRegistry` 已实现 POD `{slot,generation}` handle、`Requested|IdleArmed|Closed` gate、producer admission seal/refcount、exact idle commit、request/close 线性化、strong join/quiescence、generation reuse 和 capacity fail-closed。Phase 17 的 target-neutral `ExecutorDriver` 已把 gate 绑定单 P；当前统一语义是`PollPreempt` observe-only，scheduler run-step独占drain→ack→无条件重扫，idle执行ArmIdle→完整事实源重扫→exact CommitSleep，prepare/commit竞争失败与wake都只执行LeaveIdle→`sourceMore`，再返回同一run-step reducer，close执行seal→外部strong join→confirm/retire/unbind。旧 `P.schedule` 只保留给 unbound internal path，wait drain/cancel 不再发布 legacy request。
- Phase 17 host 验证已通过 `runtime/internal/coro` unit、`-race -shuffle=on -count=30`、focused `ExecutorDriver -race -count=100` 和 `go vet`；package cross-build 覆盖 `js/wasm`、`wasip1/wasm`、`linux/arm`、`linux/riscv64`，current-source LLGo package build 也通过。确定性交错包括 running poll 重复 observe 到 scheduler ack、Post-before-delayed-Request、300 次 Post×PrepareSleep race、wake-before-physical-block retained doorbell、spurious wake、错误 owner/direct drain、premature close 和 bound terminal fail-closed。这些验证只覆盖 target-neutral scheduler core，不代表 production `coroRun` 或真实 backend 已接线。
- Phase 18 已实现 handle-free `ActionTerminalExecutorClose` 和 `executorDriverTerminalClosing`。last-G root 在物理 destroy 后会先 settle request 并 seal executor，driver 只保留 normal/initial-panic `ActionDestroy` 或 ancestor-panic `ActionPanicDestroy` 的 `terminalKind`，清除 `g.root` 后发布 close marker。`ConfirmTerminalExecutorClose(driver)` 完全从稳定 driver/P 恢复必要状态，在 target 完成外部 join 后执行 final scan、confirm/retire/unbind、mode-last 发布和 core-only terminal commit；adapter 不会再收到已销毁 handle 或新的 destroy action。
- Phase 18 host 验证已通过 `runtime/internal/coro` unit、`-race -shuffle=on -count=30`、focused terminal executor `-race -count=100` 和 `go vet`；package cross-build 通过 `js/wasm`、`wasip1/wasm`、`linux/arm`、`linux/riscv64`。已覆盖 normal terminal、单帧 panic（`ActionDestroy`）、多帧 panic 的 root ancestor（`ActionPanicDestroy`）、stale destroy action 拒绝、错误 G/generic close 拒绝、executor request settle 和 producer lease 在 strong join 前阻止 Confirm。这些测试不能代替真实 target 对 pre-lease entry 及 Request-to-doorbell tail 的 join 证明。
- Phase 19 已把 production program runner 绑定到上述静态 driver，并把handle-free terminal close交给静态target dispatcher；其whole-G action循环已由Phase 31的bounded physical-action dispatcher替代。runner以显式drive status区分main return、retained sleep、terminal close和panic；`__llgo_coro_program_continue_v1(epoch)`通过`DriveAdmission`单owner重入，不保留caller stack、G/Action或LLVM handle。last-G normal/panic执行terminal strong join；main正常返回且仍有ready child时先执行generic executor close/join，再进入command cancellation。parked root的wait registration也已贯通Post/IdleWake→continue→`WakeExecutor`→统一source/materialization reducer→resume→consume/retire→terminal。
- Phase 19 host验证已通过DriveAdmission定向竞态、`runtime/internal/coro -race -shuffle`、program adapter `-race -shuffle`、`js/wasm`实际运行、native+nogc spawn/panic E2E、完整coroutine build integration与named-source vet；cross compile覆盖`js/wasm`、`wasip1/wasm`、`linux/arm`、`linux/riscv64`和cortexm。测试target覆盖同步/异步join、Begin返回前completion、并发/stale/duplicate continue和executor wake。production `coro_target_none`没有ingress，只能同步确认空executor，不能充当真实retained-doorbell backend。
- Phase 20 已加入只在 `llgo && llgo_coro && llgo_coro_native_pipe && (linux || darwin) && !baremetal` 选择的 production native pipe/poll backend；`llgo_coro_native_pipe` 是compiler-reserved capability，只由编译器对默认POSIX Linux/Darwin配置下发，`Config.Tags`、`GoBuildFlags`和named-target `BuildTags`均不能伪造。不能仅凭 `GOOS=linux` 推断该能力，因为部分embedded named target会借用Linux源码选择却没有process pipe/poll；普通host Go test、named target、WASM、baremetal和`coro_runtime_adapter_test`继续选择各自的非native target，避免runtime与planner/root/hash/anchor错配。验证覆盖pipe提前wake、并发coalesce、满管EAGAIN、TargetIngress Enter/Seal竞态与strong join、2048次同步wake迭代深度、真实runtime required-plain planner，以及Linux arm/arm64/riscv64和Darwin amd64/arm64静态交叉编译/target选择。native+nogc spawn/panic E2E按运行测试的host做最终链接执行：当前focused CI提供Linux执行覆盖，Darwin同一E2E仅在Darwin host运行时执行，尚无Darwin CI runner。该结果仍不表示timer/syscall source、blocking worker compensation或多P已经完成。
- Phase 21 已通过真实 `nogc` pthread producer E2E覆盖 `prepare -> publish POD -> llgo.coroPark -> CommitSleep -> pending-clear/poll窗口 post -> pipe wake -> scheduler drain/consume -> 原frame恢复 -> pthread_join -> registration retire -> terminal target close`。unit/race覆盖transactional prepare在nil owner与满64槽时回滚到新generation、pre-park rollback、只有当前resume owner可prepare/retire、以及永久retired ingress诊断；planner把三个owner ABI作为精确DirectPlain runtime roots并把完整签名纳入bootstrap hash。hook和终态audit只在compiler-reserved测试capability下存在，production默认IR常量消除hook调用。
- Phase 22 已接入第一个真实native monotonic timer source：64位Linux/Darwin使用绝对monotonic deadline和`pipe/poll` idle wait，`poll` timeout向上取整且在`EINTR`后重新取时计算；存在active timer时，唯一running G也有有界安全点预占，避免无ready peer时timer被纯计算任务无限拖延。timer table当前是64槽固定容量，slot+generation防ABA，prepare/rollback/cancel/retire保持显式交易。
- Phase 22 的编译器新增 `llgo.coro.frame-retention.timer.v1` 证书，只对frozen emission universe中精确void fail-stop prepare/retire C ABI、一个精确`{uint32}` pointer-free token、三个独立`uint32`输出和同一SSA basic block的`prepare -> llgo.coroPark -> retire`开放。完整address-use graph禁止store、escape、alias reuse、外部call和额外control transfer；证明成功后才把x/tools `Heap` alloc改降为LLVM coroutine-frame `alloca`。若函数需要抢占，编译器在prepare紧前poll，并从prepare返回到retire返回完全禁止普通budget poll/yield；未证明形状保持managed-allocation拒绝而不猜测。该ABI identity已进入plan digest、cache fingerprint、manifest和bootstrap hash；production builder只在runtime ABI暴露精确owner符号和签名时开启，fail-stop owner body另由源码结构测试锁定，并非compiler语义证明。
- Phase 23 的V2高并发promotion已使用直接park frame拥有的48/28-byte `WaitSetRecord`、独立active双链和per-P affected FIFO；completion与取消只合并标记受影响record，每个published epoch完成catalog pass后立即扫描其candidate snapshot，record-aware attach/detach/promotion均为O(1)邻接操作。active Poll固定执行epoch A、ack、无条件epoch B，B后不等待pending/request静默，因此连续producer不会饿死已经claim的wait-set。`ParkLink`的transient predecessor由同时parked operation支付，普通G布局不增加；1024-candidate测试通过破坏远端link证明fast detach没有退化成完整链审计。legacy WaitToken队列在迁移期独立保留。
- Phase 23 的跨线程执行取消使用固定容量`TaskControlSource`。只有显式host/export task handle分配两字`OperationID` generation endpoint；producer原子合并`Shutdown > Abort`并请求公共doorbell，owner P在SourceSet published epoch交付sticky task token。endpoint admission seal、late accepted fact、strong join、terminal late fact和generation reuse相互分离；G现有state后对齐空洞承载owner-only lease count，使普通G不增尺寸，同时阻止endpoint仍持有`*G`时提前回收task storage。
- TaskControl owner delivery现在按两级证明分流：公开`RequestTaskCancellation`/观察/claim API面对任意G时保留完整queue/ParkLink审计；`RegisterTaskControl`及park owner transition完成结构审计后，exact slot、source.owner和owner-only lease构成内部capability，published-epoch delivery只做O(1) scalar/local-head证明并进入唯一带proof mode的`requestTaskCancellationOwned` mutation core。Running/Dispatching要求exact `current/runP`，V2 Waiting要求exact active `WaitSetRecord`，Runnable/legacy Waiting要求完整本地字段并依赖当前single-P无迁移不变量；preparing/sealed park只复核header/head，ready winner还复核exact detached record/result ownership，设置sticky cancel时不扫描远端candidate，terminal request仍只discard。未来multi-P迁移必须先transfer lease locator或把请求forward到旧owner，不能把这个不变量静默带过迁移边界，也不为每个G增加P指针。
- Phase 23 已把monotonic timer迁入同一个Operation V2事务，同时保留现有V1 owner ABI：两种协议共享物理slot generation并由显式mode隔离；V2到期只publish sticky completion和affected wait，完整source epoch之后才统一resolve并按resolved candidate执行O(1) `ApplyOne`。winner结果lease未Take/Discard前不能recycle，task/shutdown取消可以压制selected continuation但不能泄漏结果所有权；Manual与Timer混合select的winner只由rank决定，不受静态source访问顺序影响。legacy WaitRegistration仍待迁移。
- Phase 26/27 已实现唯一的commit-capable select resolver：`ReadyThenTryCommit`的request精确绑定logical ticket、physical generation、record和readiness generation，失败从已排序链的下一link继续；`Reservable`与`IrreversibleCompletion`进入同一个逐candidate settle/finalize路径，ordinary/strong cancel与default也不再有旁路winner逻辑。兼容API只loop-drive该primitive。该阶段Channel/Poll/Host尚未在production `ExecutorSourceSet`中提供成功分支；后续Phase 32 C0已接入无payload Channel `TryCommit`和forced winner，但typed hchan、netpoll与完整Go select仍未完成。
- Phase 27 已把source catalog和common wait-set resolver变成可续的bounded transaction。A/ack/B的每个固定slot以及affected wait、candidate scan、Ready commit attempt、settle、`ApplyOne`、finish、promotion和legacy-G visit各消耗一个reduction；TaskControl slot过去隐藏的任意ready/wait/ParkLink扫描已由后续exact registered O(1) proof和header-only sticky mutation消除，candidate resolution仍由后续独立reductions承担。跨host entry的snapshot由不增加`ParkState`尺寸的owner-only `resolving`位冻结，热路径只验证O(1) scalar header和当前link邻接；`RetryBudget`与`AwaitExternalFact`严格分离。这里的成本认证仅覆盖当前静态catalog和common resolution：公开任意G取消审计、legacy Poll扫描、park candidate构造/排序、未来typed Channel/Poll/Host source及ready-G dequeue/resume/destroy、inline-ready wrapper、连续child await的wall-work仍须独立界定，不能把`budget=1`外推为完整`RunSlice`已经有界。
- Phase 29 已将operation result ownership落实为`Empty/Owned/Leased/Taken/Discarded`单字节状态，替换两个boolean且保持`OperationRecord`在64/32位分别为80/60 bytes。Irreversible/Reservable publication建立Owned，Ready publication不建立result，只有exact request bind能生成成功attempt；Manual、Timer和exact fake source在loser Ack前先完成source rollback/cleanup并Discard，Consume才把winner交成lease，Take/Discard是不同terminal action。late task cancellation、default/cancel、Ready失败重发、Reservable rollback、stale/duplicate lease与未绑定成功attempt均有定向覆盖。该阶段仍只承载无payload的Manual/Timer/fake结果标记，不能据此宣称typed channel/I/O payload、P-neutral `ResumePacket/ResultCell`、`CompletionRecord`或compiler reconciliation已经完成。
- Phase 30 已增加可复用的pointer-free scalar result cell：28-byte/align-4 V1 payload携带最多三个显式`low32/high32`逻辑`uint64`以及可验证Meta，36-byte cell用exact `OperationID`绑定source generation。公共API覆盖Irreversible/Reservable的stage-before-publication、Ready effect后的exact request bind、winner exact lease Take/Discard和loser clear-before-Discard/Ack；duplicate、lost publication和所有失败路径不覆写或误清已有generation。该cell只在需要固定标量结果的source slot中付费，不改变任何scheduler/operation公共布局，Manual producer wire ABI与无payloadTimer也未扩张。当前能力适合syscall worker、IOCP/io_uring、WASI、JS host和IRQ等整数状态/handle/count结果；它仍不承载typed Go pointer、channel receive值或普通Go `error`对象，也尚未提供frame-local `ResultCell/ResumePacket`、parent-child `CompletionRecord`或compiler reconciliation。
- Phase 31 已加入统一的普通single-P resumable runner。每个`ExecutorRunStep`只推进一个私有budget-one poll reduction、一次ready dequeue+dispatch、一个完整physical resume/destroy或返回稳定idle/terminal receipt；production runner不调用monolithic `PollExecutor/PollReady/NextRunnable`。公开兼容入口`PollExecutorSlice{At}`不能在`sourceMore/readyDebt/blocked/issued`非零时跨过cursor，只能由stable-idle `EnterExecutorRunCompatibility`显式清账。`CheckResume + done + Checked + llvm.coro.resume + Resumed`与对应destroy链在runtime adapter中不可拆，live continuation才可用G对齐空洞内的`runAction`重排；这里的physical action是不可返回边界，不等同于其内部wall-work已获常数成本证书。32/64位G仍为168/288 bytes。连续2048层同步child await精确产生2048个迭代resume action，普通resume/destroy/panic continuation和两个ready G都保持FIFO。非FIFO控制路径只覆盖compiler冻结的command bootstrap direct `CoroRoot` handoff和normal-main-return后的final root destroy：每个固定bootstrap表项最多保留一个direct-child destroy与一个exact-root resume，nested child仍在FIFO尾；main-return marker发布后只允许一个final root destroy，以保证Go main返回后不再启动用户G。已claim的A/ack/B先完整结束，hot source与ready physical action通过`readyDebt`交替。
- Phase 31b 已加入host-facing V2 slice ABI：`__llgo_coro_program_run_slice_v2`和`__llgo_coro_program_continue_slice_v2`只返回固定32-byte、8个`uint32`的POD result，包含status、used、exact executor tuple、epoch和deadline。首次entry冻结V1/V2模式；V2每次只推进给定budget，host request只能在runtime entry返回后非递归发出，同步回调统一折叠为`Repost`。native Linux/Darwin compiler entry使用一个固定机器栈循环和`budget=1024`，只接受canonical `Complete`或精确`Yielded + More|RequestInline`，畸形status/flags/tuple/deadline/reserved全部fail closed。WASM/WASI、embedded和baremetal在各自具有持久queued/blocked/deadline adapter前仍使用V1，因此本阶段不声称跨target requestRun已经完成。
- queued `CheckDestroy/PanicDestroy`若在dispatch前收到TaskControl sticky `Requested`，公开owner API不能把带非零`runAction`的G提前`Claim`成Cleanup；`BeginRunG`必须在任何frame/handle mutation前拒绝并让runner恢复原queue；`Checked(CheckDestroy)`在签发`ActionDestroy`前重复检查，覆盖owner在dispatch后、`done`返回前插入请求。`ActionDestroy`签发后owner API不再接受新token。`PanicDestroy`通过首门即进入`GPanicking`，该状态不接受owner task cancellation，source service又要求idle P，且runtime不在action/callback间返回host，所以无需额外preflight record。直到compiler cleanup lowering可消费该请求，token、target、frame和handle都保持sticky且可诊断，runtime不能通过先destroy或硬清请求伪造完成。
- Phase 31 的post-resume scheduler commit和bounded root commit只做O(1) header/local检查。final destroy后旧handle、`g.root`和`destroyTarget`都已清除，handle-free `ActionCommitDestroy`留在`P.current`而不进入ready queue；terminal close/legacy schedule race由明确的compatibility outer loop处理，且不伪造replacement handle。当前仍未覆盖physical resume内部的`findFrame`/`validPanicAncestry`、`PrepareParkSet` link scan和`SealParkSet`排序，idle prepare/wake、terminal/command close、shutdown、frame registry/Zero扫描、TaskControl delivery的legacy owner-membership队列扫描、select preparation cost certificate、非native target queued/blocked/deadline adapter、post-LLVM cost certificate和P-neutral packet/多P。Phase 31因此只证明source cursor、dispatch和resume后的scheduler commit有界可续，不宣称所有reduction或所有source路径已经strict cost-certified，也不能用于WASM/embedded完整wall-work声明。
- Phase 32 C0 已把第一个真实Channel commit-domain接入production `ExecutorSourceSet`，并复用共享`producerSourceSlot/routedProducerSource` lifecycle，但仍是固定4槽、无payload skeleton。每个多case select共享size/alignment均为4的`SelectClaim(Open/Acquiring/Committing/Claimed)`；common resolver先逐ParkLink发现统一commit domain，再在rank scan前持有claim。普通Ready走exact `TryCommit`；同步域暂不可进入或发现external `Acquiring/Committing`时，owner保留Ready hint/generation、abort exact commit snapshot、释放owner claim、恢复affected FIFO并完整结束epoch返回`more`，不会跨host钉住resolver而饿死持锁G。peer已经物理提交时，mailbox/claim/doorbell严格按release顺序发布，forced胜过rank/default/ordinary cancel；Abort/Shutdown只把continuation转为Canceled，source仍将Committed+Owned结果Discard而不Rollback。`Ready`读后被producer先CAS成`Forced`会由owner重读并直接drain Forced；`DrainingReady`上的并发forced则通过`ForcedBehindReadyDrain`保持sticky。forced落在A cursor后时，resolver在设置`ParkState.resolving`前O(1)恢复exact affected FIFO，让B或下一transaction发布forced，避免自等待和环。external producer使用无closure/interface/内部allocation的caller-owned fixed-layout pair transaction：调用方提供零值out storage，transaction以`self == out`绑定地址，所有effect/abort/commit/release入口在任何claim/slot解引用前验证self，因而任意按值副本都不能释放或提交原transaction；pair的`self/phase`是线性身份主证书，checked admission release只在aggregate count已为零时fail closed，不能单独识别某个lease。普通失败归零，Broken保留self与lease。Go gc escape输出显示self pointer会让普通caller local移到heap，所以C1 production wiring的硬门是compiler contract test同时证明pair落在不移动coroutine frame、无heap allocation，并证明整个hchan critical section NoSuspend/NoPanic；未证明时真实hchan不得调用该transaction。调用由hchan同步域串行化。pair保留原始endpoint映射，仅以stable slot address决定admission顺序，`BeginPair`取得两admission后只验证lifetime-stable的slot.claim/generation，再取得两claim，最后才在claim排他下集中验证owner-only record/link exact identity、Pending disposition、未Apply、pending-valid candidate与exact parked/non-resolving ticket；admission本身不与owner record mutation互斥。`BeginEffect`以两次Acquiring到Committing CAS取得共享、不可回退的effect permission；typed effect后`CommitPair`执行“两mailbox -> 两Claimed -> reverse-order checked release”，随后调用方才各自sticky request。pre-effect任一失败按“claims rollback -> admissions release”完整回退；任一步异常、第二次effect CAS异常或post-effect Duplicate/错误则保留lease fail closed。operation candidate仍只由owner访问，claim CAS负责排除并发logical resolution。任一claim访问都处于admission lifetime lease内，已admit publication接受Closing。Apply seal后若admission非closed-with-zero只能Retry；closed-with-zero却仍见非Claimed frame claim是corruption并Invalid，不能detach/清frame claim/promotion。generic route不能代替pair transaction。forced begin只验证scalar header与exact/local adjacency，后续每个settle reduction各验证一个link，不隐藏全链audit。ConfirmQuiesced/physical cleanup与result Take/Discard/Recycle独立。没有增加`G/P/ParkState/WaitSetRecord/OperationRecord`，Channel source值追加在Control之后；32-bit/WASM布局由编译期断言覆盖。
- Phase 32 C1 仍需typed hchan send/recv/buffer/close与payload ownership、claim-less单case external committing fence、两端route ingress、GC-visible send/result slot、uniform permutation、closed-send panic、reflect与compiler select lowering。C0的claim-backed external admission要求hchan先按稳定顺序取得两端source token，再在token仍held时取得或rollback两端claim；nil claim目前只支持owner-local Ready/TryCommit。因而这一阶段证明的是可跨native/WASM/RTOS/baremetal复用的无栈调度/claim核心，不是完整Go channel/select。
- 上一条是 Phase 32 当时的边界，后续状态由以下 Phase 33–35 记录取代。Phase 33/PR #40 已把typed hchan direct send/recv 接到真实 physical coroutine：同步fast path不切换，slow path使用compiler-frame内的`CoroChanParkV1`、exact `ParkTicket/OperationID/SelectClaim`和typed resume status；unbuffered pair、buffer、close、closed-send显式panic、task cancel及native/wasm object验证均已覆盖。该实现没有Future/Task对象，也不为每次操作创建pthread。
- Phase 34/PR #41 已把direct channel lowering放入真实native+nogc scheduler-island最终链接执行，覆盖三次unbuffered rendezvous、capacity-one buffer、child spawn以及main返回前的channel park drain。command shutdown会先让compiler resume gate消费并回收frame-local channel registration，再关闭executor，避免main返回后遗留hchan节点或source slot。LLVM 19–22 CI、review和linked E2E均通过并已合并到`llvm-coro`。
- 当前 Phase 35 已实现普通Go blocking/nonblocking多事件`select`的可编译运行版本。编译器按源码语义先求值并物化所有`ChanOp`，fast probe随机起点；blocking slow path在同一LLVM frame中保存typed candidate array和共享state，只创建一个logical wait/claim并在真正`suspend`前发布所有非nil case。runtime按channel地址用candidate内置heap-sort顺序加锁，不创建临时slice/接口对象；winner与所有loser经过exact detach、claim reset、result lease Take/Discard和slot recycle。receive/send、nil case、empty `select{}`取消、closed send显式panic、普通Try操作互操作、physical select与physical direct sender配对、select后立即再次park、TaskCancelAbort均有定向覆盖。nonblocking select只调用Try，不产生select park/resume。
- Phase 35 的native+nogc最终链接E2E实际执行两case select、后续direct rendezvous和buffer fast path，正常约3秒内完成；host adapter通过`-race -shuffle`，JS/Wasm adapter实际运行通过，native64/wasm32均通过pre/post-CoroSplit verify和object emission。开发中同时修复了direct receive resume status block错误跳回logical block首部、重放receive前副作用的问题；status现在进入compiler-owned physical continuation。
- Phase 35 仍是有界可运行原型而不是完整Go channel/select：`ChannelOperationSource`仍为每个single-P executor固定4个同时live physical slot，因此超过4个非nil case或高并发占满slot会fail-stop；尚无stable paged catalog、P-neutral packet/multi-P迁移、`reflect.Select`动态descriptor端到端、完整GC precise root或标准库`sync`slow path。本阶段冻结范围只保证当前direct channel与普通源码select可编译、链接、运行和确定性清理，不把这些后续能力混入同一PR。
- 当前Phase 36 / PR #45把实验性多入口路径收敛为唯一stackless profile和冻结的
  `ProgramIR -> physical operation recipe -> emitter`事实流。普通函数仍只有一个primary；
  只有进入func value、interface、reflect或其他开放动态边界的callable才发布descriptor。
  architecture gate持续限制直接读取plan、原始intrinsic opcode、后验语义重建和legacy
  fallback，当前hard-cutover没有扩大这些允许面。
- Phase 36已把闭包物理上下文统一为LLVM `nest`/`swiftself`，函数对象继续保持固定
  `{descriptor/function, env}`两字结构，不采用变长Go funcval。native
  `reflect.Value.Call/CallSlice/MakeFunc`经过`runtime/internal/ffi.CallLLGo`和
  `llgo.coroFFICall`进入同一descriptor；stock libffi只负责初次创建并挂起child，
  最终typed thunk才把普通`env`参数送入目标context寄存器，因此没有libffi/C栈跨越
  suspend。WASM、baremetal和无可执行内存环境的runtime-created MakeFunc签名仍是明确
  AOT capability缺口。
- ForeignWait已统一复用一个有界worker park/resume事务。编译器在冻结阶段从forward
  provenance生成typed record与exact certificate，不在runtime从函数地址反查语义。
  ordinary generated `_Cfunc_*`、C method、varargs和静态C pointer result已进入该路径；
  generated `_C2func_*`只把精确C调用与同线程`errno`快照放到同一worker transaction，
  `(result,error)`的Go构造在原coroutine恢复后完成。未证明ABI、callback/reentry、
  affinity、retention或pointer provenance的动态C调用继续fail closed。
- compiler runtime现独占`poll.c`及scheduler-facing符号，轻量runtime不再依赖标准库
  runtime patch间接带入该对象。`llgo.allocaCStrs`在coroutine body中改用managed
  storage，exported runtime hook同时进入LLVM optimizer与final-link retention。
  file/pipe/TCP/time同步风格acceptance、select合法并发输出、native defer/panic、
  multi-executor fleet/channel shutdown以及C2 errno IR均已在本地分层通过；完整
  `test/*`、Go 1.26 GOROOT、跨LLVM 19–22和各target CI仍是合并前验收，不得由这些
  focused结果外推为完整标准库兼容。
- host complete-operation网络族现以同一个8-word以内的typed recipe覆盖
  read/write/accept/connect/recvfrom/sendto/recvmsg/sendmsg；deadline metadata只选择
  Worker-only或Worker+Timer ParkSet，动态SetDeadline与Close均通过exact read/write lane
  取消并等待物理ack。Go 1.26同步`net` fixture已在Darwin、`wasm-unknown`和`wasip2`
  运行通过；同一fixture又让Go自带pure-Go resolver通过现有文件HostOp读取受控
  nsswitch/resolv/hosts并完成`LookupHost("localhost")`，并经受控UDP DNS server并发完成
  A/AAAA wire查询。WASM显式Node embedding的功能场景账本为114个operation、17次cancel、
  18个alarm和171个schedule action；追加80次listener顺序创建/关闭后最终为
  594/17/18/651，确定性越过64槽poll descriptor页。并发查询暴露的preemptible poll descriptor扫描重复
  `runtimeCtx`分配已改为target-lowered原子CAS保留并由源码gate固定。read/write control
  lane又加入单调epoch：deadline/Close先发布descriptor状态并推进epoch，park hook在HostOp
  Submit后重检snapshot，失配时向仍处于preparing/sealed的ParkSet发布sticky operation
  cancel，随后统一resolver完成exact物理Cancel、terminal ack与result discard。pull adapter
  用独立`SubmitCancelPending`状态保留两项义务并固定同generation的Submit先于Cancel；
  两个WASM target上的确定性stale-epoch E2E已覆盖该窗口。最终`runtime_pollClose`还把
  标准`FD` read/write ref所保证的post-resume/unbind lane-idle变成fail-stop gate，违约不能
  静默留下固定表槽或形成`runtimeCtx` ABA。
  新增RecvMsg/SendMsg只增加target-neutral opcode、36-byte POD result和stdlib薄wrapper，
  没有增加compiler effect、executor或source状态机分支。
- HostOp的compiler边界现已完全进入同一ProgramIR事实流：call-site freeze一次验证exact
  direct shape并保存`opcode/pointerMask/argumentCount/metadataWords`，physical operation
  recipe只引用该POD，codegen不再从raw SSA恢复opcode、pointer provenance或deadline metadata。
  HostOp与worker word-call共用一个Park CFG emitter，operation select/observe也经统一入口；
  architecture debt的direct plan authority从354降到352，raw intrinsic shape、Park emission和
  physical operation selector预算没有上升。使用本次重构后新构建的compiler，两个WASM目标均
  重新通过直接网络`16/2/15`、文件`5/10`、Sleep `4/1`及TCP/DNS
  `594/17/18/651`纵向账本。
- Phase 36之后的主要缺口仍是工程闭环而非新的coroutine可行性障碍：零/单source
  `ResumePacket`、Channel/select typed multi-source materialization、单Worker HostOp、Worker+Timer
  deadline和keyed/private-registry park的P-neutral composite cleanup已经完成；基于无指针需求claim、exact mailbox和Imported首次执行门的global injection/work sharing也已完成。native现在于首个managed resume前创建固定8-route物理topology，route/source/event identity保持到进程关闭；启动环境或online CPU只初始化逻辑execution quota，标准`runtime.GOMAXPROCS(n)`可在运行期查询、增大或缩小该quota，而不销毁P、迁移source或撤销已经开始的resume。single-executor target固定返回1。locked-M阻塞已接通同P replacement owner、嵌套handoff和route-local channel/timer/poll驱动。下一步是pthread standby缓存、批量/local-deque steal、完整affinity、shutdown/fatal阻塞矩阵，以及Linux自动cgroup/affinity默认值刷新；paged/dynamic channel、timer与
  worker capacity；完整defer/recover/Goexit和precise suspended-frame GC；外部DNS
  server/cgo resolver、Unix/raw socket与ancillary OOB、process/signal；logical stack/tooling；以及
  WASM/WASI/RTOS/baremetal各自内建
  event/host/HAL adapter。native退出实验模式仍必须满足35.1与35.2，尤其仓库
  `test/*`和GOROOT不得存在unexpected failure。
- compiler的所有现有initial、child-await、yield和legacy-park resume边已接入terminating dispatch gate。zero-ticket路径调用scalar `__llgo_coro_run_decision_take_zero_v1(g) uint32`，正常值进入唯一normal continuation，Abort/Shutdown在cleanup lowering完成前进入共享trap而不会误执行用户continuation；full ticket/lease ABI继续供bootstrap与未来park-site reconciliation使用。同一LLVM/target的gate开关对照证明scalar gate不会增加stackless coroutine frame，CoroSplit ramp/destroy也没有可达gate。
- 两字Operation identity已冻结为`source:8/route:9/local:15 + generation:32`，保持size 8、align 4。route按runtime instance单调分配且永不复用，关闭后保留永久tombstone；Manual/TaskControl ingress的producer lease覆盖`source.Post -> executor.Request`完整tail，strong join后才允许清除source/executor pointer；Timer V2 reserve、publish、Apply和result lease也验证exact route/local/generation。52-byte pointer-free `ResumePacket`已覆盖零/单Timer、Manual、Poll、Worker以及经typed plan物化的direct Channel/multi-case select，HostOp deadline与keyed/private-registry cleanup也复用同一POD plan。P-neutral global injection/work sharing已在1/4/8 route core生命周期以及1/8 P真实linked E2E验证。Command root用G尾部padding中的一字节`AnyOwner/CurrentOwner` mobility保持program-P边界；这不增加G大小、P指针或registry，也不等于完整thread/realm affinity。运行期逻辑execution quota和locked-M同P replacement handoff均已完成：active resume先变为私有rooted的`GForeignWaiting`，10,000槽BSS M目录以generation/owner-epoch baton把active route从parent scalar slot切到replacement，replacement复用原driver/source/common reducer并服务channel、timer和poll；原C返回后request/ring/join，重获permit并恢复exact resume。`GOMAXPROCS=1`下同route channel、timer、真实socket poll及nested M0→M1→M2 linked E2E均已通过。尚未完成的是pthread standby缓存、`SetMaxThreads`联动、批量/local-deque steal、完整affinity以及标准库/GOROOT的locked syscall/callback矩阵。
- 第一个标准库同步风格原型已以GOROOT source patch实现`time.Sleep`：普通`time.Sleep(d)`被Effect分析自动传播为`DirectCoro/AwaitStructured`，不修改public signature，不依赖libuv、BDWGC、pthread producer或用户goroutine。真实linked native+nogc E2E已编译production runtime island，实际等待30ms并恢复原frame；timer/wake路径由monotonic clock与pipe/poll/fcntl实现，符号审计确认不依赖libuv、BDWGC或pthread producer。另一focused production-overlay测试直接读取真实注入的`time.Sleep`源，不用测试effect seed，验证跨包同步caller染色、frame证书和CoroSplit，但不声称链接执行标准库`time.Sleep`。LLVM 19–22都跑该契约，Go 1.24跑真实linked E2E，Go 1.26也跑production overlay分析/codegen。
- Phase 22 仍是有界prototype，不是完整`time`runtime：第65个同时live timer会按fail-stop ABI终止，尚需dynamic/sharded table和heap；`Timer`/`Ticker`/`AfterFunc`仍使用legacy libuv路径；`f := time.Sleep`、interface/reflect和dynamic dispatch还没有end-to-end callable coroutine descriptor；`Sleep(0)`/负值在Sleep体内不注册timer，但value-insensitive caller仍会创建并await child frame，尚需conditional effect或call-site fast path才能避免可观测的多余handoff。完整`Do`标准库构建现在先被`sync.Pool` TLS destructor的捕获闭包挡住：exact同步C callback ABI没有closure context slot，不能直接放宽。后续需改成显式`owner/local` TLS state，并同时为`tls.Handle[T]`经`Pool.local`的unsafe transport建立字段级whole-program证书。WASM、WASI、RTOS和baremetal也尚未有对应timer source。
- wait/preempt core 要求目标提供可靠的 32-bit atomic load/store/CAS。WASM 可直接满足；带 A 扩展的 RISC-V 可满足；ESP32-C3 RV32IMC 当前会在链接时缺少 `__atomic_*_4`，直到平台用 IRQ critical section 提供单核适配。这里故意不使用非原子 fallback。
- `wasip1`、`wasip2` 和 `wasm-unknown` 明确选择 leaking/nogc frame backend，不依赖 libuv 或 BDWGC。`wasip2` 与 `wasm-unknown` 已通过真实 `llgo build -target=...`、wasm magic/symbol closure、无 `GC_*`/undefined 检查，并由 wasmtime 运行返回 0。当前 `wasip2` 产物是 Preview 2 目标的 core module，尚不是 WIT component。
- frame allocator 已有 conservative BDWGC、nogc/WASM malloc 和 tinygogc/baremetal 后端。跨 suspend 的 pointer 目前只在 conservative 或 non-collecting 配置下安全；精确 frame root map、write barrier、STW、weak timer/finalizer 与 cleanup 语义尚未实现，不能据此宣称完整 Go GC 兼容。
- deterministic scheduler core 已能管理多个 frame、ready queue、park/wake、稳定 wait registration/cancel、绑定 P 的 target-neutral executor driver、正常 main-return cancellation、terminal panic frame destruction和 idle/requested/stopping/disabled 状态。Native production runner使用固定8-route topology；route 1收养command M，其余route各由一个joinable pthread M拥有，所有M复用同一reducer和route-local source/reactor。target-neutral原子execution quota只包围真正的managed resume，缺少permit的route仍可服务timer/poll/channel/cancel/transfer；缩容不撤销in-flight resume，扩容通过sticky waiter与doorbell关闭lost-wake窗口。locked-M blocking call现会detach active resume并把同一P/driver/source执行域交给临时replacement M；replacement沿公共reducer继续处理同route事件，原M在C返回后strong join replacement、重获quota并精确restore。core race、真实1→4→1物理并发、单配额同/跨route补偿、channel/timer/socket poll和嵌套replacement E2E以及普通标准库`runtime.GOMAXPROCS`同步调用已经通过。尚未接入registration unregister枚举、dynamic/sharded timer heap、standby replacement cache、完整peer fatal teardown、完整panic/defer/recover/Goexit和全量标准库/GOROOT矩阵。
- native+nogc scheduler-island 已把真实 nested static `go` lowering、V2 entry/factory/control wrapper、production scheduler/spawn/shutdown/coroalloc 最终链接并执行。确定性 fixture 验证 `Before=1, After=0, Leaf=0`，最终符号审计同时要求 production `CommitSpawn`/`BeginCommandShutdown` 且禁止 legacy `Panic/Rethrow/TracePanic/printany`。该测试以四个 bounded init no-op 和 fail-stop nil-check/libc allocation stub 隔离完整标准库 runtime，因此证明的是可运行 scheduler 原型，不是完整 runtime 启动兼容。
- terminal panic 的独立 native+nogc scheduler-island 已真实编译并运行 `panic(&GlobalPayload)`。production internal runner返回精确`DrivePanic`状态，导出的void program-run ABI随后执行fatal abort；bootstrap、main、panicChild三个不同LLVM handle各destroy一次，两个祖先均不resume，task-local record在三层frame销毁后仍保持exact type/data word，且G为Dead/non-Reclaimable。最终二进制要求production `PreparePanic`/`PanicDestroyed`/`LoadPanicRecord`并禁止legacy panic/print链；测试report只观察internal drive-panic与record，不代替production printer/exit owner。
- 完整真实 `entry → allocator → v2 factory → runtime/package init → main → scheduler` linked smoke 仍受上述 runtime/Panic/foreign blockers 限制；scheduler-island、runtime adapter 和 freestanding wasm CLI fixture 各自证明的边界不能合并表述为完整 Go runtime 已经端到端运行。
- 当前 cache digest 只解决同一完整程序计划下的内部 package cache；未知未来 caller 可复用的预编译 archive/标准库仍需 producer summary、canonical boundary Dispatch 和 linker ABI 校验。
- hchan typed materialization的buffer/closed peer循环已拆成显式runtime子游标，每个runner reduction至多处理一个peer且不跨返回持锁；同一固定枚举typed plan已接入单Worker HostOp、Worker+Timer deadline和keyed park：runtime-private adapter/control/key registry先在旧owner退休，各物理source再由common core逐个确认、取结果和回收，packet物化后resume不回访旧P。plan把runtime step数量与source数量分开，worker scalar只写已有packet，不增加callback/interface或重复payload。keyed `Posting`退休也已改成exact generation终态协议：producer先发布private Delivered再Post Manual fact，取消先胜时producer跳过或由Manual source admission/quiescence吸收stale Post，不再busy loop或增加runner wait状态；真实`Mutex + Cond + WaitGroup`同步程序和WASM32布局已验证。固定2048槽key registry的线性FIFO选择仍是后续高争用性能项。后续cost工作继续覆盖park preparation/sort、普通try discard scan、同步close drain、单element copy上限，以及Phase 31 physical resume内部的frame/ancestry/link scan。再把idle prepare/wake、terminal/command close、shutdown、frame scan/Zero与source-specific隐藏工作纳入同一账本，并把Phase 31b POD扩展为WASM/JS、WASI、RTOS和baremetal的queued/blocked/deadline adapter，完成post-LLVM cost certificate。其后把当前64槽native timer升级为dynamic/sharded heap，补齐`Sleep(0)` fast path、Timer/Ticker/AfterFunc和dynamic callable descriptor，并实现有界blocking worker、registration unregister和异步syscall source。各target复用同一core并分别证明完整ingress join边界。多P开放前还必须为每一种composite park完成P-neutral packet和parkable capacity permit；未物化packet的G不可steal。随后补suspended-frame GC、完整defer/recover/Goexit、dynamic/closure/method `go`及平台tooling。所有阶段保持无栈、单primary、静态source catalog和未证明即fail closed，不引入其他语言的Task/Future对象层。
- 标准host-Go下`self` pointer导致的逃逸只是Phase 32 C0 test artifact，不是production allocation contract。C1需由compiler提供caller-owned pair storage，并用noescape/frame certificate证明它位于不移动coroutine frame且无heap allocation，同时单独证明`hchan` critical section NoSuspend/NoPanic。

### Phase 1：单 P deterministic scheduler

- Fake platform、虚拟时钟和 event token。
- G、frame chain、spawn、ordinary async call、completion/destroy。
- 稳定`ParkState/WaitSetRecord/OperationID`、logical terminal/detach/quiesce/recycle分层和防丢park/wake handshake。
- 静态`SourceSet`、A/ack/B idle transaction与全路径bounded `RunSlice` cursor。
- 最小resume reconciliation、versioned `CompletionRecord`和`Abort/Shutdown` cleanup skeleton；完整Go defer/recover在Phase 4扩展。
- Async bootstrap/init/main。
- 单 executor native 参考实现。
- 一份共享executor stack运行任意数量G，禁止每G pthread/ucontext/RTOS task fallback。

验收：无lost wake、无重复resume、`AwaitExternalFact`不产生无事件忙转、main返回语义正确、frame exactly-once destroy；10万普通parked G不增加M/机器栈数量。执行取消只有在result lease已Take/Discard且cleanup skeleton可达时才可标通过。

### Phase 2：抢占

- Loop/recursion/long-block poll。
- managed poll budget + request epoch，以及source/affected/candidate/G动作共用的executor reduction budget。
- Preempt disable。
- Post-optimization safepoint verifier。
- Infinite-loop fairness 测试。

验收：两个不含显式yield的无限计算G都持续前进，且可由测试控制器请求preempt/GCStop；持续source producer、inline-ready和child-await也不能绕过budget或饿死已claim epoch，本阶段不依赖尚未实现的timer。

### Phase 3：Go 阻塞原语

- Scheduler-aware sema。
- Mutex/RWMutex/WaitGroup/Cond/Once slow path。
- Channel，以及具备`ReadyThenTryCommit/Reservable/IrreversibleCompletion`、default不可提交证明和physical ack barrier的select。
- Sleep、公共 timer heap、AfterFunc。
- 单线程 netpoll。

验收：单executor下持锁者被抢占不会导致waiter阻塞executor；stale readiness、reservation rollback、all-fail+default和cancel-vs-commit通过；ticker在另一个G纯循环期间仍可唤醒。

### Phase 4：Panic/GC/调试

- 在Phase 1最小CompletionRecord/cleanup skeleton上补齐task-local panic/defer/recover/Goexit、nested control stack和可挂起defer。
- NativeEH/WasmEH/ExplicitStatus/EpisodeSJLJ PanicABI与语言fault显式check。
- Frame root allocator。
- STW handshake。
- Suspended frame GC 测试。
- Channel Timer/Ticker weak lease、finalizer、AddCleanup、weak/unique在GC-capable target的ordering。
- `runtime.newcoro/coroswitch`两个无栈G baton语义。
- Reflect Call/Method/MakeFunc typed descriptor/trampoline；AOT未知signature capability诊断。
- `testing/synctest` durable park与虚拟时钟。
- Logical traceback、Caller 基础支持。

验收：plain/coro跨层defer/panic/recover/Goexit及nil/bounds/divide在Native、WASM/WASI、Cortex-M/RISC-V所选PanicABI通过；forced GC能扫描suspended frame并回收不可达channel timer；`iter.Pull`、reflect和synctest核心用例通过。Nogc target对GC相关API给出准确capability而非silent stub。

### Phase 5：Native 多 P

- P-neutral `ResumePacket/ResultCell`物化和source-affine ready边界完成后，使用无指针demand + exact mailbox启用按需global injection/work sharing；该层已完成。Native固定创建8个有界route，C线程叶只传递`uint32` M slot，不传函数地址、G、P或coroutine handle；启动环境/CPU初始化逻辑execution quota，`runtime.GOMAXPROCS`在固定identity上动态调整managed resume并行上限。locked-M阻塞已用`ExecutionDomainHandoff + ExecutorResumeHandoff + active M slot`完成同P ready/source handoff；replacement复用公共reducer，支持嵌套并受同一execution quota约束。下一步增加pthread standby缓存、本地deque/批量steal和完整affinity，不改变transfer ownership协议。
- Worker pool及其他有限source使用generation capacity permit/backpressure，不按operation增生线程或对象。
- ForeignOp worker/locked-M clean-stack execution、P release/reacquire和ForeignReentry。
- `Syscall*`/`RawSyscall*`的single-call ForeignOp、PollWait wrapper event lowering、pointer provenance/pin和thread-affine thunk。
- LockOSThread。
- Central netpoller。
- BDWGC thread/TLS integration。
- runtime.Pinner、runtime/cgo.Handle、SetCgoTraceback、signal token delivery和plugin descriptor registration。

验收：Native多P work-stealing与blocking Syscall/RawSyscall/C并发时其他G持续前进，同时保留single-call EAGAIN/EINTR/short-result、thread scope和uintptr pin语义；cgo外部root/同G重入、nested permit、Pinner/Handle/traceback、LockOSThread和signal专项通过；目标范围`go test std`及GOROOT并发/runtime门槛通过。

### Phase 6：WASI 和 JS/WASM

- WASI `poll_oneoff`。
- JS `runSlice/notify` host protocol。
- HostOp/HostReentry、`syscall/js` FuncOf/Release/Value.Call和wasmimport/exportMode。
- setTimeout、Promise、显式sync/async/dual exports。
- Linear-memory frame-aware conservative/tiny GC；nogc作为明确degraded profile。
- WASI nonpoll blocking import的async/thread/diagnostic路径。
- Browser/Node 运行 CI。

验收：每次runSlice返回均无managed stack continuation；tight loop、timer、Promise、同步host reentry和callback Release/generation通过；无proof的sync MayPark ABI不被静默改写且在构建/park前诊断；WASI分别验证pollable I/O和一个blocking file import下的`foreignBlockCompensation`声明；GC-full profile通过suspended-frame forced GC。

### Phase 7：Baremetal 与 RTOS

- Cortex-M/RISC-V QEMU clock、compare IRQ、WFI。
- ISR token ring。
- tinygogc frame root。
- Static/slab allocator。
- Executor/foreign stack linker预算、全部静态capacity填满/OOM和高水位测量。
- RTOS one-shot timer、driver task adapter。
- 32位/无锁target atomic fallback、PanicABI显式fault check和RawCritical syscall/HAL verifier。

验收：Cortex-M与RISC-V baremetal QEMU以及至少一个FreeRTOS/Zephyr QEMU或硬件job通过timer、抢占、channel、forced GC和resource exhaustion；`maxG/frame/timer/wait/token/callback/foreign-depth`逐项填满时只产生声明的error/fatal且queue/root保持一致；机器栈数量不随G增长。

### Phase 8：优化与扩展

- Lock-free/local deque。
- Frame pooling和内存上限。
- Precise frame maps。
- WASM threads、MCU SMP。
- Profiling/race/debugger 深度集成。

验收：优化前后Plan/ABI/stack和抢占证明不变；race/pprof/trace/Caller显示logical G/frame；WASM threads/MCU SMP只有在并发GC与atomic litmus通过后才标Full；Native plugin/reflect任意signature达到声明的L5能力。

production已硬切换为mandatory LLVM stackless coroutine，不再提供`None`、pthread scheduler或profile切换。尚未完成的平台能力必须通过target capability诊断或明确降级，不能回退到另一套每G线程运行时。

## 26. 测试与 CI

### 26.1 编译器分类

- Pure direct sync chain 不生成 `$coro`。
- MaySuspend/NeedsPreempt chain 只生成 coroutine primary。
- Static sync boundary 只生成薄 adapter。
- Local singleton func value 保持 Direct。
- Phi(sync, async)、global/map/channel/any/reflect 变 Dispatch。
- Nested struct/array/slice/generic aggregate中的func叶子在addressable/archive/reflect/memmove边界递归canonicalize，封闭SSA aggregate才允许Direct。
- `go` 不 taint caller，`defer` 正确传播 effect。
- Mutual recursion 中一个 seed 使整个相关 SCC 正确提升。
- 无 seed 的有界递归应由 verifier 判定；无法证明有界则 NeedsPreempt。
- 泛型不同实例获得不同 plan。
- Linkname、method promotion、value/pointer receiver。
- Cross-package summary 和 cache digest。
- PanicABI/hidden outcome/layout hash不匹配在archive/link阶段失败。
- `Syscall*`/`RawSyscall*` direct、取地址、跨archive调用按metadata传播WaitForeign；只有声明PollWait/ExactAsync的wrapper传播WaitPlatform/WaitHost，RawCritical才保持已验证NoSuspend。
- Exported/archive function value保持canonical Dispatch，LTO优化不改变published ABI。
- 所有spawn使用LLVM-coro root trampoline；bounded sync target本身仍不复制coro body。
- `NoSuspend` transitive call graph不含park/coro intrinsic；所有MaySuspend/NeedsPreempt primary包含完整coro lifecycle。
- Plain call region具有MaxPlainDAGStack/MaxAtomicCost，最终artifact具有MaxEpisodeStack；未知或超目标预算时提升为coro或诊断。

### 26.2 LLVM

对以下 triple 做 pre/post CoroSplit verifier 和 codegen：

- `x86_64-unknown-linux`
- `aarch64-apple-darwin`
- `wasm32-unknown-unknown`
- `wasm32-unknown-wasip1`
- Cortex-M/Thumb
- `riscv32-unknown-elf`
- 后续 Xtensa、AVR compile-only

覆盖：

- Target pointer-width coro size。
- Over-aligned frame。
- 多个 suspend state。
- Go pointer 跨 suspend。
- Dynamic result layout。
- Panic/defer cleanup。
- 跨suspend pointer不能指向executor alloca；不生成跨suspend的stacksave/stackrestore、setjmp或stack-copy。
- Resume到再次suspend后native stack回到scheduler基线；resume深度不随历史yield次数增长。
- `MaxEpisodeStack`包含post-split root/ramp/resume/destroy MFI、plain DAG、ABI/IRQ reserve，并在超过target executor/foreign stack预算时link失败。

Pre-CoroSplit verifier按CoroPlan检查 `coro.id/begin/suspend/end`、park/await可达suspend和NoSuspend闭包。Post-CoroSplit时intrinsic可能已消除，因此改查每个root/async symbol的ramp/resume/destroy集合、FrameDescriptor、state-PC map、allocator hook和ABI version。Link verifier只检查版本、symbol/relocation和禁止的pthread-per-G、RTOS-task-per-G、driver→Go callback引用，不能声称从最终binary恢复SSA liveness。

### 26.3 Scheduler model

- Wake before Parking、during handoff、after Waiting。
- Duplicate wake/ready。
- G 不能同时在两个 queue。
- Frame 不能并发 resume。
- 每个suspension epoch的ResumePermit只能消费一次，stale/duplicate permit不能resume或destroy新epoch。
- Timer Stop/Reset/fire generation race。
- I/O cancel/completion race。
- Work stealing 和 pinned G。
- 未物化P-neutral ResumePacket的source-affine ready G不能进入global queue或被steal；物化后新P不访问旧route/source，packet只能消费一次。
- 小budget下持续source producer、affected batch、loser detach和inline-ready分别保留cursor并返回`more`；只有external ack缺失时返回`blocked`，连续空Poll不得因`AwaitExternalFact`忙转。
- Command main返回立即退出；Reactor/Embedded bootstrap返回host后仍可接受export。
- Main Goexit deadlock。
- Deterministic trace/replay。
- 10万/100万普通parked G的M/thread/RTOS-task数量保持 `maxExecutors + boundedWorkers`。
- 大量LockOSThread G按maxLockedM增长，并在maxThreads边界触发规定fatal，不伪装成普通无栈扩容。
- G未pin时跨M resume；suspend hook验证没有残留plain Go activation。
- 取消通过显式unwind运行defer，不直接destroy frame chain。
- ForeignOp cancel-vs-return、duplicate completion和never-return：record/root不提前释放，owner G不并发resume。
- G-target preempt请求在目标G跨P迁移后仍处理；Preempt/GCStop/Profile并发发布不覆盖未ack generation，非target G/P不能代ack。
- Root return/panic/Goexit先DestroyPending/unregister再Dead/terminal ack；blockOn与CallbackHandle不能提前释放record。
- Foreign/HostReentry参数/result只驻留ReentryRecord；每次resume前attach/STW/acquire-P，suspend后clear-currentG/release-P。
- 全部普通foreign permit被外层callback占有时，nested op只使用reserved quota或确定性resource-fatal，不在祖先permit上死锁。

### 26.4 抢占

- 两个纯无限循环。
- 无限递归。
- 长基本块。
- Poll 被 LLVM 优化后仍存在。
- `nopreempt` pending request 在临界区退出后立即生效。
- 纯循环期间 Sleep/ticker 能按允许误差触发。
- Blocking C 时其他 native G 继续运行。
- 变量长度memmove/hash/compiler-rt helper和高竞争LL/SC路径经切块/slow path后满足MaxAtomicCost。
- Strict/release各target artifact的 `unboundedRegions` 必须为0；故意引入未知asm loop应在link失败。
- CPU-time preempt bound与STW/OS/host wall-time分项指标分别校验。

### 26.5 同步原语

- Mutex owner 被抢占。
- Mutex starvation/handoff、RWMutex reader/writer fairness。
- WaitGroup Add/Wait/Go竞态和misuse panic；Cond ticket/wake ordering；Once panic语义。
- Pool per-P、victim cache和GC cleanup。
- Buffered/unbuffered channel。
- Close 与 send/recv race。
- Select 多 case 同时 ready。
- Select + timeout + cancel。
- `ReadyThenTryCommit`的stale/duplicate readiness generation、TryCommit失败后republish以及多个candidate依次失败。
- `Reservable` winner physical Commit、loser Rollback、ack前不promote，以及commit/rollback duplicate与旧generation拒绝。
- 所有候选TryCommit失败后才允许default；budget耗尽、缺少ready通知和pending reservation都不能伪造default证明。
- Abort/Shutdown-before-commit、late prompt cancellation-after-commit和ordinary cancel-vs-completion分别验证结果Take/Discard与不可撤销副作用。
- Timer Stop/Reset stale value、Ticker drop和AfterFunc Stop/reset race。
- Go1.23+ channel Timer/Ticker丢弃最后引用后forced GC会detach heap lease；GC-vs-fire/Reset/Stop generation race无UAF/stale callback，Sleep/AfterFunc仍被正确强保活。
- Native多P memory-model litmus/stress；Cortex-M/RISC-V 64位atomic对齐、关中断/锁fallback和atomic.Pointer barrier。
- 当前 Go GOROOT channel/select/sync/time 测试。

### 26.6 GC 与生命周期

- Plain→plain panic/Goexit运行各层defer后再跨coro completion；baremetal显式PanicABI专项。
- Park/select resume先按exact ticket reconciliation并Take/Discard result lease，再进入normal continuation或Abort/Shutdown cleanup；ticket mismatch与重复消费fail closed。
- Child先release发布`CompletionRecord{Return/Panic/Goexit/Abort/Shutdown}`再destroy；parent只在Return读取普通result，其他kind经可挂起cleanup传播。
- Direct deferred function先park再recover成功，间接helper recover失败，nested panic generation不混淆。
- 对象只被 suspended frame 引用，强制 GC 后仍存活。
- Frame completion/unlink 后对象可回收。
- Promise、result、closure、timer、waiter 中的 Go pointer。
- BDWGC、nogc、tinygogc 分别覆盖。
- Nogc repeated create/destroy allocation count 归零。
- C/token registry 不丢 root。
- Running plain activation只依赖当前M stack；同一G suspend后仅靠frame graph保活对象。
- 每G frame count/bytes limit、pool exhaustion和OOM/fatal路径。
- Tinygogc/static slab中live slot forced-GC保活，free/reused slot清零后不因stale pointer保活。
- runtime.Pinner跨ForeignOp保持地址，cgo.Handle Delete/stale generation，SetCgoTraceback RawCritical hook和callback registry Release并发。
- 无userdata裸callback在未确认external quiescence时只retire/tombstone不复用code address；pool耗尽明确报错，userdata/token路径才测试generation复用。

### 26.7 平台运行

- Linux amd64、macOS arm64：单 P 和多 P。
- Native syscall语义：nonblocking pipe立即EAGAIN、EINTR不自动重试、short result不补齐；动态SYS_GETTID/rt_sigprocmask在LockOSThread前后保持目标M；pointer→uintptr为对象唯一引用时，blocking ForeignOp期间forced GC仍保活/pin到ack。
- Node/Chrome wasm：tight-loop fairness、setTimeout、Promise、host re-entry。
- JS sync export直接Sleep/fetch、以及 `recv(ch)` 而producer随后Sleep/fetch，必须在构建/park前诊断而不是hang；NoSuspend/YieldOnly export仍可同步完成。
- JS Go→Value.Call→FuncOf callback使用同G HostReentry；外部首次callback使用newG；Release/迟到callback、嵌套reentry及callback内WaitHost诊断通过。
- Reactor/Embedded注册FuncOf或export后main park会`ReturnToHost`，未来callback可唤醒G；Release最后动态handle后按command/reactor quiescentPolicy继续，不误报/漏报deadlock。
- Reactor/Embedded bootstrap/main返回后host可再次调用export并创建G；显式immediate/graceful shutdown按manifest处理detached G。Command artifact仍验证main返回立即退出。
- Wasmtime/WAMR：WASI clock、Sleep、fd readiness。
- WASI blocking regular-file/path host import在async/thread capability下不阻塞其他G；无补偿时strict拒绝、degraded job准确报告不能保证并发。
- Cortex-M QEMU、RISC-V QEMU：SysTick、WFI、timer wake、tinygogc frame root。
- 至少一个FreeRTOS/Zephyr QEMU或硬件job为合入门槛；ESP32/FreeRTOS等额外硬件nightly。
- Native/WASM/WASI/Cortex-M/RISC-V的nil/bounds/divide panic均走PanicABI并可跨coro defer/recover，不依赖不可恢复host trap。
- AVR 初期 code size + compile-only。
- WASM检查managed产物不依赖Asyncify/stack-copy；每次runSlice返回时无G continuation留在host stack。
- Cortex-M/RISC-V检查main/IRQ stack high-water在高并发和深逻辑递归下保持预算内。
- RTOS/baremetal逐项耗尽G/frame/timer/wait/token/callback/foreign-depth容量，验证reserve-before-publish和确定性error/fatal后状态仍一致。

现有 wasm `continue-on-error` C hello 不能作为 coroutine 验收。新 job 稳定后取消 continue-on-error。

## 27. 可观测性与性能基线

Runtime 暴露 debug counters：

- G states 和 queue length。
- Spawn/resume/suspend/preempt/park/wake/destroy count。
- Duplicate/stale wake count。
- Max safepoint gap。
- Max nopreempt duration。
- Unbounded region count、max plain/target-machine atomic cost。
- Timer lateness。
- Poller sleep/wake。
- Work steal attempts/success。
- Frame bytes/current/peak。
- Frame bytes/G、peak frame depth、frame pool OOM。
- Executor stack high-water、max plain call chain/stack bytes。
- BlockOn depth 和 rejected JS sync wait。

关键 benchmark：

- Sync call 和 closure call：启用 scheduler 前后不应出现 coroutine开销。
- Async call/await。
- Spawn/complete。
- Preempt context switch。
- Channel ping-pong。
- Timer create/reset/fire。
- 1P/NP work stealing。
- Frame bytes 对比当前 pthread stack。
- Binary size，尤其 Cortex-M/RISC-V/AVR。

## 28. 正确性不变量

实现和 debug verifier 必须持续检查：

1. 一个 G 同时只由一个 M 运行。
2. 一个 coroutine handle 同时只被一次 resume。
3. Runnable G 在 ready queues 中最多一次。
4. Waiting G 必须有有效 wait owner/token。
5. Dead G 不在任何 queue/registry。
6. Active frame 必须从 G root graph 可达。
7. Parent/child frame 构成无环链。
8. Completion 在parent resume前release publish；root terminal ack只在frame destroy/unregister后发布。
9. Frame exactly-once destroy。
10. ISR/signal/poller/异步host notification只投递token，不执行resume、destroy、Go callback或GC allocation；显式同步C/JS ABI callback只能经attach + newG/Reentry协议进入。
11. Scheduler lock 内不 suspend。
12. `nopreempt` 区域无 unbounded path。
13. Dynamic call 的 ABI hash 与静态 signature 一致。
14. Command main正常返回不drain detached G；Reactor/Embedded只在显式shutdown按policy处理。
15. Scheduler重新取得控制时，该G没有残留managed native Go activation或指向其栈的continuation；预算内foreign/host boundary stack不属于G continuation。
16. `blockOn` 不在managed G/DirectPlain activation中执行。
17. 普通未pin G数量不改变M/RTOS task/executor stack数量；locked/foreign/worker增长不超过各自manifest预算。
18. Cancellation在destroy前完成defer/unwind。
19. Per-kind target request在ack前不被覆盖，G迁移不能代消耗其pending generation。
20. 所有有限capacity遵守reserve-before-publish，失败后queue/root/token状态不变。
21. 每个suspension epoch的ResumePermit只能消费一次；destroy使该frame全部旧permit失效。
22. Operation的logical terminal、waiter detach、backend quiescence和storage recycle是四个独立阶段，result lease未Take/Discard不得recycle。
23. `ReadyThenTryCommit`和`Reservable`只接受exact readiness/reservation generation；default只有在完整不可提交证明后发布，physical commit/rollback ack前不得promote。
24. `AwaitExternalFact`不能因同一operation设置`more`；budget耗尽和可续cursor只能返回`RetryBudget/more`，不能伪装成external blocked。
25. 持有source-affine result/cleanup lease的G不能steal；P-neutral ResumePacket release发布且旧lease结束后，新P才可acquire运行并且不得回访旧route。
26. Abort/Shutdown只在resume gate或safepoint claim并先完成本地result reconciliation；取消不能直接destroy仍有defer、payload或physical disposition的frame。
27. 一个route同一时刻只有active-M目录指定的M可驱动其P。replacement必须先claim同generation/owner-epoch baton再切active slot；归还必须在driver无issued action、source事务Idle、route mailbox稳定且物理wait已disarm的边界把active slot切回parent。
28. Detached active G由`ExecutorResumeHandoff`私有保根，不进入ready/wait队列。其间最后一个scheduler-visible子G完成只能提交普通domain destroy并保持driver Active；只有restore后的program owner可以执行command terminal close。

## 29. 风险与缓解

| 风险 | 等级 | 缓解 |
|---|---|---|
| IR外backend/helper循环漏掉抢占 | Critical | target-machine cost proof + unboundedRegions=0 + link failure |
| Wake/park handoff 丢唤醒或并发 resume | Critical | 稳定ParkState、exact ticket、sticky source fact与deterministic model test |
| G迁移/并发kind覆盖抢占或STW请求 | Critical | Per-kind request slot + target-owned seen/ack + migration model test |
| Frame 未进入 GC root graph | Critical | Runtime allocator + suspended-frame forced-GC tests |
| 继续使用 pthread cond 阻塞 executor | Critical | Coroutine mode 全量切换 sema/channel/poll |
| JS sync export的间接MayPark/WaitHost死锁 | Critical | MayPark effect + completion proof + Promise/JSPI诊断 |
| Panic/defer 仍绑定 TLS/native stack | High | G-local state + frame-by-frame completion unwind |
| Blocking C 占住全部 executor | High | caller stack-cut + 有界ForeignOp worker/指定M thunk + P补偿 |
| RawSyscall隐藏阻塞或把executor alloca交给worker | Critical | syscall intrinsic effect + stable argumentRecord/pin + RawCritical verifier |
| Nested Foreign/HostReentry耗尽permit后自死锁 | Critical | ancestor检测 + reserved quota/depth + pre-entry deterministic failure |
| Interface/reflect ABI 误配 | High | Descriptor ABI hash + plan verifier |
| Frame/result destroy 时序错误 | High | 外部 result slot + exactly-once state machine |
| MCU heap碎片和 code size | High | slab/limit + size CI |
| Logical stack/debug 不完整 | Medium | FrameDescriptor state map |
| 全程序 plan 破坏 cache 正确性 | High | CoroPlanDigest + ABI version |
| 隐式回退到per-G thread/task/stack | Critical | 唯一newG入口 + link verifier + high-concurrency stack-count CI |
| Plain call region耗尽嵌入式共享stack | High | MaxPlainDAGStack/MaxEpisodeStack + linker budget + high-water/OOM tests |

## 30. 明确拒绝的方案

### 30.1 所有函数无条件双版本

会扩大代码体积、闭包和 itab ABI，并把 function coloring 问题转成全局复制。新设计只生成一个 primary body。

### 30.2 R12/TLS/global mode 动态判断

它与多架构 ABI、G 跨 M 迁移、嵌套调度和 C 互操作冲突。调用模式由编译计划显式决定。

### 30.3 Signal/ISR 中直接 suspend/resume

LLVM coroutine 没有任意 PC frame capture 能力，且 runtime/GC/queue 操作不具备 async-signal safety。

### 30.4 Queue 中保存 raw handle

会丢失 goroutine identity、panic/defer、timer、pinning、GC root 和多 frame call chain。

### 30.5 Queue 空时直接 resume 等待对象

等待 timer/I/O/channel 的条件尚未满足，直接 resume 会破坏语义。

### 30.6 在 coroutine scheduler 中继续使用 pthread channel/sema

单 executor 会死锁，多 executor 会造成阻塞放大和线程数量失控。

### 30.7 全程序 Asyncify

LLVM coro 已负责 Go async frame；Asyncify 仅可作为特定 C/JS 边界的 opt-in，不应重复转换整个 Go 调用图。

### 30.8 把 libuv 固定为 scheduler core

Libuv 可成为 Native 平台 adapter，但 wasm、WASI、RTOS 和 baremetal 需要统一的更小平台接口。

## 31. 尚需在实现中验证的决策

以下不改变总体架构，但需通过原型确定细节：

- LLVM 19 各 target 上获取最终 frame alignment 的最佳方式：intrinsic 或 post-CoroSplit descriptor pass。
- Native 第一个 poller 使用 portable poll+wake pipe，还是直接按 OS 使用 epoll/kqueue。
- Async panic reference backend 采用 LLVM EH 还是显式 cleanup edge；baremetal 必须有不跨 suspend 保存 jump buffer 的实现。
- Interface method descriptor 是直接扩展 itab header，还是独立 versioned side table。
- Frame conservative arena 与未来 precise map 的切换 ABI。
- 默认 poll static cost、quantum 和 MCU frame pool size。

所有这些决策都必须保持以下不变：选择性单 primary、显式动态 descriptor、每G无机器栈、G-owned LLVM-coro frame chain、安全点抢占、scheduler-aware park/wake 和 GC-visible frame。

## 32. Go 语言特性兼容性审计

本节按 Go 1.26 语言与主要 runtime 语义逐项检查。判断分为：

- 无结构性障碍：现有 lowering 或 coroutine frame 可直接承载。
- 可行但需要专项实现：不改变 Go API，但需要 compiler/runtime 新机制。
- 平台能力限制：语言设计可兼容，具体 target 没有对应 OS/host 能力。
- 固有限制：在 LLVM stackless coro 和目标 host 约束下不能透明完成，必须 adapter、offload 或诊断。

### 32.1 基础类型、表达式和控制流

| 特性 | Coroutine 下的处理 | 障碍与方案 | 判断 |
|---|---|---|---|
| 常量、数值、字符串、复数、运算符 | 与现有 lowering 相同 | 无 scheduler 影响 | 无结构性障碍 |
| Struct、array、slice、map、pointer | 跨 suspend 的 live value spill 到 frame | Frame 必须 GC-visible；地址和 alignment 由 DataLayout 决定 | 无结构性障碍 |
| Named type、alias、embedding | 类型元数据不变 | Method descriptor 扩展需保持 receiver ABI | 无结构性障碍 |
| If、switch、type switch | 普通 CFG lowering | Type switch 仍使用 itab/type metadata | 无结构性障碍 |
| For、range integer、goto 构成的循环 | 每个 cyclic path 插入 suspendable preempt poll | Post-LLVM verifier 防止 poll 被优化掉 | 可行，抢占关键 |
| Range array/slice/string/map | 保持求值和迭代语义 | 大循环自动 poll；map 迭代状态 spill 到 frame | 无结构性障碍 |
| Label、goto、break、continue、fallthrough | Presplit CFG 中正常保留 | 不允许 goto 跨越 Go 本来就禁止的变量作用域；循环 verifier按 CFG工作 | 无结构性障碍 |
| Builtin new/make/append/copy/delete/clear | 沿用 runtime helper | Allocation slow path兼作 safepoint；大型 copy需审计最长不可抢占时间 | 无结构性障碍 |
| min/max/complex/real/imag/len/cap | 纯计算或现有 helper | 无 | 无结构性障碍 |
| go:embed、build constraint | 编译期行为 | 与 scheduler 无关 | 无结构性障碍 |

大型 `memmove`、hash、crypto 或压缩循环如果落在不可插桩汇编中，poll 前后无法保证很小延迟。可行顺序是：优先使用可插桩 LLVM IR；其次把大操作切块；再其次把已知长操作标成 foreign blocking/offload。不能把无限或用户可控超长汇编标成 `nopreempt` 后仍宣称有界抢占。

### 32.2 函数、方法、返回值和闭包

| 特性 | 方案 | 需保持的语义 | 判断 |
|---|---|---|---|
| 普通函数/方法调用 | CallPlan 选择 direct 或 child coro + transparent await | 源签名和返回错误风格不变 | 无结构性障碍 |
| 多返回值、named result | Result slot 具有完整 tuple layout；named result 位于 frame | Defer 修改 named result 后再 publish completion | 无结构性障碍 |
| Variadic | 调用前构造 slice，随后 direct/await | 参数求值顺序不变 | 无结构性障碍 |
| Method expression/value | 静态 method direct；method value捕获 receiver并按需 Dispatch | Receiver 在表达式求值时捕获，nil/value receiver panic 时机一致 | 可行，需要 descriptor |
| Closure | Captured env由 frame/GC root 可达；逃逸closure的env独立heap-lift | Capture by reference/value语义不变；不得在parent frame destroy后引用其storage | 无结构性障碍 |
| Function value | Direct 或 Dispatch 两字表示 | Nil function panic、比较仅与 nil、赋值/传递语义不变 | 可行，需要 value-flow canonicalization |
| Higher-order callback | 动态callsite可选择plain/coro entry | Caller自动coroutine化，不暴露await | 可行，是stdlib兼容关键 |
| Recursion | SCC fixed point；managed 无界递归插 poll并 coroutine 化 | Stack overflow 替换为 frame/资源上限检查 | 可行，需要资源策略 |

同一个 source function 不因多个调用者自动复制主体。MaySuspend/NeedsPreempt 函数以 coroutine 为 primary；静态同步边界用薄 adapter。只有动态开放调用确实需要不同 entry capability 时才创建 descriptor。

### 32.3 Interface、`any`、type assertion 与泛型

| 特性 | 方案 | 障碍 | 判断 |
|---|---|---|---|
| Empty interface / `any` | Box function value前 canonicalize 为 Dispatch；普通数据不变 | Runtime type metadata需认识 dynamic func rep | 可行 |
| Non-empty interface | Itab method slot保存 MethodInvoke descriptor | 当前单 code pointer ABI需 version bump | 可行，改动较大 |
| Interface invoke | Managed caller动态await coro entry；hard-sync consumer生成typed root adapter | Unknown method implementation保守 MaySuspend | 可行 |
| Type assertion/switch | 保留 descriptor和具体类型 | Assert 出 func/method value不能丢失 rep metadata | 可行 |
| Generics | 每个实例独立分析和 emission | Linkonce/COMDAT/cache digest必须一致 | 可行 |
| Constraint/interface method | 实例化后 direct/VTA；开放字典调用走 descriptor | 跨包摘要表达高阶 effect | 可行 |

以 `io.Reader` 为例：

- `bytes.Buffer.Read` 可以只有 plain sync implementation。
- `net.TCPConn.Read` 可以只有 coroutine implementation。
- `io.Reader.Read` 动态 callsite 通过 itab descriptor 选择。
- `io.Copy` 源码仍直接调用 `Read/Write`；若 receiver 开放，`io.Copy` 编译为一个 coroutine primary。

这种实现满足标准库接口风格，同时不要求每个 concrete method 双版本。

### 32.4 `go`、channel 和 select

#### `go f(args...)`

必须保持求值时机：

1. 在 caller G 中按源顺序求值 function value、receiver 和参数。
2. 把已求值结果复制到新 G 的 root frame/result-independent startup record。
3. 创建并 ready 新 G。
4. Caller 立即继续，不 await 新 G。

若 `f` 是 bounded sync function，新 G 使用通用 coroutine trampoline 调用它；`f` 本身不需要 coroutine clone。若 `f` 为 coroutine primary，root frame直接使用其 coro entry。Dynamic function value由 descriptor选择。

Nil function value的求值发生在 caller，但调用 panic属于新 G 开始执行目标时；测试需与 Go 保持一致。

#### Channel

- Buffered/unbuffered send/recv、close、nil channel、closed channel panic都由 scheduler-aware channel实现。
- Blocking send/recv lower 成当前 frame 的 park suspend。
- Nonblocking fast path不 suspend。
- Send value和channel operand按 Go 规定先求值并复制到GC-visible send slot，再开始 wait registration。
- Close唤醒receiver并返回元素零值/`ok=false`；被唤醒的sender在自己的G中panic。
- 空select和对nil channel的单独操作永久park，但仍能参与runtime deadlock detection。
- Range-over-channel等价重复 recv，阻塞时透明 park。

#### Select

- 进入 select 时，所有 channel operands以及 send RHS 按规范求值一次。
- Case permutation只影响选择，不重复表达式求值。
- 每个channel case使用`ReadyThenTryCommit`：ready notification只提名candidate，必须携带exact readiness generation并在channel同步域内原子`TryCommit`；状态已变化时消费该hint并继续本轮其他candidate或重新probe。
- Default存在时，只有本轮所有非nil case都完成不可提交probe、所有TryCommit均失败且没有待确认reservation/commit时才立即返回；budget耗尽或暂时没有notification不是default证明。
- Nil channel case永不 ready。
- 多case同时ready使用预生成伪随机rank，source扫描顺序不能决定winner。
- Wait registration采用logical ParkTicket、per-candidate readiness generation和loser detach barrier，确保只提交一个case；winner payload和所有physical ack完成前不恢复用户continuation。
- Closed receive、closed send panic、timer case和任务Abort/Shutdown都保留各自payload/control kind，不能压成一个ready boolean。

Phase 32 C0已经实现上述选择提交的无payload runtime内核：统一claim discovery、owner `TryCommit`、external forced publication、A/ack/B边界和strong-cancel result discard均进入同一个bounded resolver。它没有改变Go源码调用风格，也没有给每个G或每个函数增加Task/Future对象。当前fixed Channel source仅供协议与调度接线，真正的channel queue、typed value、close/panic和compiler case/result lowering属于C1；尤其claim-less单case对端提交必须先有hchan-local committing fence，不能把“物理effect后调用Post”当作实现。

Go memory model中的 channel send/recv、close happens-before由 value publish 的 release 和 waker/resume 的 acquire 建立。

判断：无结构性障碍，但 channel/select 是 runtime correctness 的 Critical 模块。

### 32.5 Defer、panic、recover 和 Goexit

| 特性 | 必须保持 | 方案 | 判断 |
|---|---|---|---|
| Defer 参数 | Defer statement执行时立即求值 | 值存入当前 coroutine frame/defer node | 可行 |
| LIFO defer | Return、panic、Goexit均逆序执行 | Frame cleanup state machine | 可行 |
| Deferred async call | Defer body可调用 Sleep/channel等 | 在同一 G创建 child frame并 await | 可行，需 async unwind |
| Panic | 沿逻辑 Go 调用栈传播 | G panic state + frame-by-frame completion | 可行，改动大 |
| Recover | 只在正确的direct deferred call上下文成功；defer先park再recover仍有效，helper中的recover必须失败 | 每次invocation的RecoverToken记录panic generation/owner且不向callee传播 | 可行，需严格测试 |
| Named result + defer | Defer可修改返回值 | Publish result在所有 defer完成后 | 可行 |
| Goexit | 执行 defer、不被 recover捕获 | 独立 CompletionKind | 可行 |
| Runtime fault panic | nil/bounds/divide等必须可被defer/recover捕获 | Compiler显式check并进入PanicABI；不能依赖不可恢复WASM trap/MCU HardFault | 可行，平台专项 |

真正困难的是 portable panic transport，而不是 Go 源码风格。任何 SJLJ/EH state都不能跨 suspend 保存；跨 frame传播通过 completion协议完成。

语言规定的nil dereference、bounds、divide-by-zero等panic优先在LLVM产生trap前由compiler显式检查，并走当前G的PanicABI cleanup；Native signal-to-panic只能作为已验证优化。真正的外部memory corruption、不可分类WASM trap或baremetal HardFault按target fault capability报告/fatal，不能冒充可recover的Go语言panic。

专项语义还必须覆盖：`panic(nil)`、defer中再次panic、recover后named result、Goexit执行defer时发生panic、该panic被recover后继续原Goexit，以及 `os.Exit` 不运行任何defer。

### 32.6 Range-over-function 与 iterator

Range-over-function 会把循环体 lower 为 yield callback，属于典型高阶动态调用。

- 当前 x/tools SSA 已把 range-func lower 为 synthetic yield closure 和 READY/BUSY/DONE/EXIT 状态；CoroPlan应在该lowering之后分析真实callback边。
- Iterator 和 yield callback都按 FuncRep 分析。
- Yield callback若可能 park，iterator caller必须是 coroutine。
- Iterator调用 `yield(v)` 时透明 await callback；`yield` 返回 false后不得再次调用。
- Break、return、panic需要通过 iterator lowering状态正确传播。
- Iterator defer与循环体 defer分别属于各自 frame。
- 同步 iterator + bounded callback仍可保持 plain direct。

没有根本障碍，但必须新增针对 iterator/yield 双向控制流的 coroutine tests，不能仅依赖普通 closure 测试。

Go 1.26的 `iter.Pull/Pull2` 还依赖 runtime `newcoro/coroswitch` 提供成对控制转移。这里不能把 producer 当作 consumer 同一 G 的普通 child frame：Goexit隔离、goroutine identity、race hooks和LockOSThread donation都要求独立逻辑 G。

首选实现是 “两个无栈 G + dedicated coro link + direct baton transfer”：

1. `newcoro` 创建独立 producer G及 LLVM-coro root，不为其创建pthread/RTOS task或native stack。
2. Producer/consumer各自保持 `activeFrame`，`coroswitch` 原子切换状态和 baton。
3. 对端可直接成为 next G，不经过普通ready queue和channel allocation，但所有切换仍返回scheduler trampoline，不能从一个resume episode嵌套resume对端。
4. Stop、panic、Goexit、race acquire/release和locked-M donation记录在专用link；producer的Goexit不能终止consumer。

普通无缓冲channel handoff可以作为最初的正确性实现，但不是最终语义/性能路径。不能继续链接到当前pthread/native-stack coroutine实现，也不能让 `coroswitch` 绕过G状态机直接resume裸handle。

### 32.7 Init 和程序生命周期

- Scheduler、GC root registry和platform clock必须在第一个package init前可用。
- Package dependency和 init order不变。
- Compiler生成 bootstrap G，逐个 direct call或 await init。
- Init中允许启动goroutine、channel wait、Sleep和panic；新G可在当前init park或被抢占时并发运行。
- Init deadlock由 scheduler检测。
- 所有 init结束后进入 `main.main`。
- Command main返回立即退出且不drain其他G；Reactor/Embedded bootstrap完成只ReturnToHost。

判断：可行；必须替换 #1532 的 post-main drain。

### 32.8 Reflection

Reflection 是完整兼容中改动最大的动态特性之一。

- `reflect.Value.Call/CallSlice/Method` 读取 function/method descriptor。
- Async caller调用 coro entry并透明 await。
- Hard-sync boundary创建BoundaryRecord/root G并typed blockOn。
- `reflect.MakeFunc` 生成带 FuncDispatch 的 trampoline，回调本体可 coroutine 化。
- Native 可继续把 libffi 用于纯 sync foreign ABI；Go dynamic call不能依赖 libffi frame跨 suspend。
- `reflect.Send/Recv/TrySend/TryRecv/Select` 复用scheduler-aware channel/select内核。
- WASM、Harvard 架构 MCU 和 W^X/无 executable heap 环境优先为程序中可达func signatures预生成trampoline，不要求 `ffi_closure_alloc`。
- `reflect.FuncOf` 在运行时构造、且最终跨到静态未知native calling convention的任意新签名，AOT目标若没有libffi closure/JIT或universal ABI，无法完全泛化；应限制为已注册signature、通过packed reflect.Call使用，或明确报capability错误。
- `reflect.Value.Pointer` 等暴露 code identity的 API需要定义 primary/adapter的稳定返回规则，通常返回 source-function canonical entry，而不是随机 wrapper。
- Method enumeration和 `Type.Method` metadata携带 MethodInvoke。
- Libffi只用于对应的 sync ABI；coroutine ABI由 LLGo typed trampoline处理。

`reflect.Call` 和链接时已知signature没有理论障碍；任意运行时 `FuncOf + MakeFunc` 在无universal trampoline的AOT平台是明确能力边界。在对应 capability 完成前不能宣称 reflect完整兼容。对可能 async 的未知 reflect call静默走 sync pointer是不允许的。

### 32.9 Unsafe、地址和 GC liveness

- 跨 suspend仍 live 的 local由 LLVM spill到稳定 heap frame，`&local` 在 frame生命周期内有效。
- `unsafe.Pointer` 保存在 scanned frame时保持对象可达。
- `uintptr` 按 Go 语义不是 GC root。保守扫描可能造成额外保活，但不能提前回收。
- `runtime.KeepAlive` 必须作为 compiler barrier延长到指定 safepoint/completion。
- `unsafe.Offsetof/Sizeof/Alignof` 仍由目标 DataLayout计算。
- `unsafe.Slice/String` 不改变 scheduler。
- 把 Go pointer传给同步 C call时frame不能在 C活动期间移动或 destroy。
- C保留 Go pointer必须继续遵守 cgo pointer rules；coroutine不能放宽规则。

判断：保守 GC模式下可行；未来 precise frame map需精确表达 state liveness。

### 32.10 Atomic、数据竞争和 Go memory model

- `sync/atomic` 和 internal atomic保持不可 suspend的短 intrinsic。
- LLVM ordering必须满足Go sequentially-consistent atomic语义；32位target的64位atomic对齐/锁实现、typed And/Or以及atomic pointer write barrier都需专项实现。
- Atomic操作本身不成为 coroutine effect seed。
- Channel、mutex、Once、WaitGroup、Cond、timer cancellation的同步边必须映射到正确 acquire/release。
- G 在 M 间迁移不能把 goroutine状态错误放在线程 TLS。
- Preemption不新增 happens-before；有数据竞争的程序仍保持未定义/竞态语义。
- Race detector需要对 park/wake、channel handoff、sema和G迁移加 hooks。

判断：语言内存模型可兼容；race instrumentation是后续工具链工作。

### 32.11 Cgo、C export 和 callback

| 场景 | 方案 | 固有限制 |
|---|---|---|
| Go -> 短 C call | 同一 M同步执行，前后 safepoint | C frame活动时不可 suspend |
| Go -> blocking C | Caller形成ForeignOp并stack-cut；有界worker或指定locked M从干净stack执行thunk并释放P | 单线程平台无法并行补偿 |
| C -> Go sync callback | 外部thread用newG root；Go→C→Go用原G ForeignReentry child | C返回前不恢复原foreign continuation |
| C -> Go async notification | C提交token，scheduler创建/ready G | C端不能持有无根frame pointer |
| C保存Go pointer | 继续执行cgo pointer checks/pinning | Coroutine不改变Go规则 |
| JS/WASM C callback等待host event | Promise/JSPI/Asyncify adapter | 纯同步返回在单线程host上不可实现 |

Native cgo整体可行但工作量大。Baremetal/wasm是否支持 C API取决于 linker、libc和host能力。

Go callback中的panic不能跨越C frame进行language unwind；boundary wrapper必须在该G内完成defer后按cgo/runtime策略报告或终止。C长期持有业务锁再同步等待可挂起Go callback仍可能形成应用层死锁，runtime只能检测re-entrancy，不能自动破坏C锁语义。

公开interop生命周期也必须落到同一root协议：`runtime.Pinner`把对象登记到GC pin registry并在所有相关ForeignOp ack前保持地址稳定；`runtime/cgo.Handle`只向C暴露整数handle，registry强保根到Delete且拒绝stale generation，不能泄露Go pointer。`runtime.SetCgoTraceback`注册的context/traceback/symbolizer可在signal或foreign stack上被调用，必须使用独立RawCritical ABI并验证NoSuspend/NoAlloc/NoCallback；其结果再与FrameDescriptor logical chain拼接，不能从这些C hook直接resume G。

### 32.12 Assembly、intrinsic 和 linkname

- 普通汇编函数是 opaque sync/foreign region，不能在内部 suspend。
- 已知短 intrinsic可标 `nosuspend/noblock`。
- 输入规模可导致长时间执行的汇编需切块、改 LLVM IR、offload或接受明确的 unbounded-preemption diagnostic。
- 汇编调用 Go callback时按 C callback边界处理。
- `go:linkname` 的 effect、entry capability和ABI hash必须进入 summary。
- `ABI0` wrapper、`//go:nosplit`、`//go:noescape`、`systemstack`、`mcall` 等runtime/compiler contract必须逐项映射：`nosplit` 只表示共享executor stack约束，不等于 `nosuspend`；`noescape` 不能覆盖跨suspend liveness；依赖g0/native-stack切换的入口必须重写为scheduler-stack intrinsic或明确不支持。
- Compiler/runtime magic函数由手写 effect table覆盖，并由测试防止漏项。

任意不返回的外部汇编循环无法在 LLVM coro约束下透明抢占，这是固有限制。

### 32.13 Plugin 与动态加载

Native plugin不是理论障碍，但要求：

- Plugin携带相同 scheduler/coro ABI version。
- 加载时注册 effect summary、type/method descriptor、frame descriptor和logical stack metadata。
- Open-world dynamic function默认 Dispatch。
- 正在运行的scheduler/GC可安全publish新root和metadata。

JS/WASM、WASI、RTOS、baremetal本身通常不支持 Go plugin。第一阶段可明确禁用；Native完整兼容阶段再实现，不能把未知 plugin函数当普通 sync code pointer。

### 32.14 Finalizer、Cleanup、weak pointer

- GC callback只把记录发布到scheduler队列，不直接运行用户代码。
- `SetFinalizer` 由专用低优先级 finalizer G按runtime要求串行执行。
- `AddCleanup` 的每个cleanup在独立G中运行，可受scheduler并发上限控制，但不能与finalizer错误合并成一个串行callback G。
- Callback function value使用 Dispatch，可调用普通同步风格API并park；所有这些G仍是LLVM无栈root。
- GC只在对象不可达且frame roots扫描完成后enqueue callback。当前BDWGC仅在显式GC路径drain队列的行为必须替换为scheduler wake。
- `Cleanup.Stop` 与enqueue/start做原子状态竞争，保证at-most-once；不能保留当前no-op。
- Finalizer resurrection、cleanup ordering、weak-to-strong转换、interior pointer identity和弱引用清除遵守现有 runtime contract。
- Runtime shutdown是否等待cleanup按Go语义处理，不因main返回而额外drain。

BDWGC理论上可实现完整队列；tinygogc需要新增mark/sweep联动；nogc因无法判断unreachable而本质不支持这些API。缺少能力的平台必须显式诊断/裁剪，而不是在driver线程执行callback或静默忽略。

### 32.15 Signal、fault 和 OS thread语义

- `os/signal.Notify` 的 native handler只写async-signal-safe token/pipe；scheduler安全上下文向channel发送。
- Fatal fault关联到 M.currentG和active FrameDescriptor。
- Signal handler不能分配、获取Go锁或resume coroutine。
- `LockOSThread` 通过 G/M pinning实现。
- Thread-local C库状态仅在locked M上可靠。
- `runtime.GOMAXPROCS` 调整允许同时进入managed resume的逻辑配额；native固定物理route不随调用销毁或重建，single-executor target返回/限制为1。

Native可行；JS/WASM、WASI、RTOS/baremetal仅实现其host存在的signal/interrupt语义。

`LockOSThread` 兼容性独立于“只有一个executor所以身份平凡相同”：Go还要求locked期间该OS thread不运行其他G。单executor target只能声明受限Degraded：bounded NoSuspend region可保持exclusivity，任何park/preempt先诊断；strict模式必须静态证明，不能列入无条件Full。

### 32.16 Stack inspection、debugger 和工具

- `runtime.Caller/Callers/Stack` 遍历logical frame chain。
- Panic stack、goroutine dump、pprof和trace使用FrameDescriptor state map。
- Running G先遍历当前resume episode完整的active native/shadow call chain，再接coroutine parent state；suspended parent完全由metadata恢复。不能只拼一个top PC，也不能把adapter/scheduler frame暴露成Go caller。
- Existing caller shadow state迁入per-G，不使用进程全局或M-local状态表示可迁移G。
- Delve/LLDB需要认识split resume函数和source FunctionID。
- Race、MSan、ASan等instrumentation必须覆盖frame allocator和resume边界。

这不是语言执行障碍，但在这些功能完成前不能称为完整 runtime 工具兼容。

### 32.17 语言特性总评

Go spec内没有必须暴露 `await` 才能实现的特性。最困难但可行的部分是：

1. Interface/reflect/higher-order callback的动态 ABI。
2. Panic/recover跨frame的精确语义。
3. Channel/select/同步原语的park/wake竞态。
4. Logical stack和runtime工具。
5. GC frame root和future precise map。

真正的固有限制集中在语言外部边界：

- 任意PC硬抢占。
- 活动C/assembly frame中间suspend。
- 单线程JS同步等待未来host event。
- 未插桩且不返回的外部代码。

这些限制都不要求改变普通Go标准库的源码调用风格；它们由边界adapter、worker/offload、平台capability或明确诊断处理。

## 33. Go 标准库同步调用风格兼容方案

### 33.1 编译策略

目标是用上游标准库源码直接构建，不维护 “async stdlib fork”：

1. 先构造完整SSA program和compiler/runtime effect table。
2. 分析标准库和应用调用图。
3. Pure/bounded函数生成plain primary。
4. MaySuspend或NeedsPreempt函数生成coroutine primary；dynamic-open本身只决定Dispatch表示，不把NoSuspend函数变成coroutine。
5. 包间调用通过effect summary选择direct或transparent await。
6. C/host export才生成sync adapter。
7. Full LTO可进一步devirtualize/prune descriptor，但正确性不依赖LTO。

预编译标准库archive必须包含effect summary。Exported Go函数不因为“exported”就自动生成完整sync clone；只有真实hard sync ABI需要adapter。

### 33.2 Runtime contract

Go 1.26标准库大量通过linkname依赖runtime。Coroutine mode必须提供同名、同语义契约，并允许compiler把可能park的调用识别为MaySuspend。

#### Scheduling

- Goroutine spawn、Gosched、Goexit。
- GOMAXPROCS、procPin/procUnpin。
- LockOSThread/UnlockOSThread。
- `runtime.newcoro/coroswitch`，供Go 1.26 `iter.Pull/Pull2` 使用。
- KeepAlive、finalizer/cleanup queue。

#### Sync

- `runtime_Semacquire*` / `runtime_Semrelease`。
- `runtime_notifyList*`。
- Spin policy、mutex/block profiling。
- Pool cleanup和per-P storage。

#### Time

- `time.Sleep`。
- `newTimer`、`stopTimer`、`resetTimer`。
- monotonic `runtimeNano` 和 wall clock。
- Timer/Ticker channel semantics、AfterFunc。

#### Poll

- `runtime_pollServerInit`。
- `runtime_pollOpen/Close/Reset/Wait/WaitCanceled`。
- `runtime_pollSetDeadline/Unblock`。

#### Testing 与工具

- `runtime.NumGoroutine`、allgs枚举和 `GOMAXPROCS` 读取真实G/P状态。
- `entersyscall/exitsyscall`、thread limit和 `SetMaxThreads` 使用真实M生命周期。
- `debug.SetMaxStack` 映射为每G logical frame bytes/depth上限并同时校验plain executor stack budget；达到限制仍按runtime fatal策略处理。
- `testing/synctest` 给G和timer标记bubble；只有bubble内所有G均durably blocked时才推进虚拟时间，普通短暂runnable/foreign block不能误判。
- Test、benchmark、fuzz、Cleanup callback统一走Dispatch；`FailNow/Goexit` 跨frame运行Cleanup。

这些入口的Go声明可以保持不变；CoroPlan把Wait/Sleep/Semacquire等识别为suspend intrinsic，并在caller中生成当前frame suspend，而不是把 `llvm.coro.suspend` 藏在普通runtime sync函数内。

Intrinsic也必须可取地址。Direct call可在caller中inline lowering；但 `f := time.Sleep`、method value、reflect、未知archive或dynamic callback需要真实callable entry。每个可address-taken/exported suspend intrinsic生成唯一typed coroutine shim，shim本身就是该声明的primary并进入descriptor/effect summary，不再生成sync主体。专项测试覆盖 `var f = time.Sleep; f(d)`、`reflect.ValueOf(time.Sleep).Call` 和 `go f(d)`。

### 33.3 标准库子系统矩阵

| 子系统/包 | 保持的同步用法 | Coroutine runtime实现 | 平台限制 |
|---|---|---|---|
| `runtime` | Go现有API | G/P/M、frame、GC、panic、stack、metrics | 全平台核心 |
| `sync/atomic`、internal atomic | 原子函数/类型 | LLVM/target atomic，不suspend | MCU需真实原子或关中断 |
| `sync`、`internal/sync` | Lock/Wait/Do/Get | Fast path原子，slow path park G；Pool按P | 全平台 |
| `time` | Sleep/Timer/Ticker/AfterFunc | 公共timer heap + platform alarm | 需monotonic clock |
| `context` | Done channel、WithTimeout、AfterFunc | 建立在channel/timer/G上 | 无额外障碍 |
| `io`、`bufio`、`bytes`、`strings` | Read/Write接口 | Pure实现plain；开放Reader/Writer动态dispatch | 无额外障碍 |
| `fmt`、`log` | Fprint/Sprintf/Stringer | Writer/Stringer/Error方法可async，caller透明提升 | Descriptor覆盖面较大 |
| `encoding/*`、`compress/*` | Marshal/Encode/Decode | 计算loop有preempt poll；Marshaler等callback动态 | Assembly热点需审计 |
| `sort`、`slices`、`maps`、`iter` | Comparator/yield普通func | Callback descriptor；range-func双向await | 无额外障碍 |
| `internal/poll` | FD.Read/Write/Wait | 注册fd/deadline后park G | 依赖平台event driver |
| `net` | Dial/Accept/Read/Write/Resolver | Netpoll + timer；blocking DNS走ForeignOp worker | Baremetal需网络HAL |
| `crypto/tls`、`net/http` | 普通Conn/Handler API | netpoll、timer、channel、dynamic Handler | 建立在net完整性上 |
| `os`、`io/fs` | File.Read/Write、Open等 | Readiness fd走poll；regular file走blocking worker | Host需filesystem |
| `os/exec` | Start/Wait/CommandContext | Process wait poll或blocking worker；signal/cancel token | JS/baremetal通常无process |
| `os/signal` | Notify channel | Native handler写token，G安全发送channel | 依赖host signal |
| `syscall`、`internal/syscall/*` | Syscall*/RawSyscall*原同步签名和single-call结果 | Compiler intrinsic按wrapper contract走event token或exact-once ForeignOp并传播effect；ThreadScoped绑定M，仅RawCritical受限直调 | 平台specific；uintptr provenance/pin需验证 |
| `syscall/js`、`go:wasmimport/export` | Value.Call/Invoke/New、FuncOf/Release、同步或Promise export契约 | HostOp stack-cut、HostReentry同G child、CallbackHandle registry、显式exportMode | JS/WASM；WaitHost需async/JSPI |
| `database/sql` | Query/Exec/Rows | 依赖sync/channel/timer和driver callbacks | Driver/cgo能力 |
| `reflect` | Value.Call/MakeFunc/Method | Func/Method descriptor + typed coro trampoline | 必须专项完成 |
| `plugin` | Open/Lookup后普通调用 | 动态注册summary/descriptor | 初期仅Native |
| `testing` | Run/Parallel/Deadline/Cleanup | G、timer、logical stack；parallel调度 | Fuzz/process平台specific |
| `testing/synctest`及内部测试时钟 | 同步测试代码 | Fake platform/虚拟clock和durable park | 需scheduler专门支持 |
| `runtime/debug` | Stack/GC/SetMaxThreads等 | Logical frames、STW、M限制 | 工具阶段 |
| `runtime/pprof`、`runtime/trace` | 原API | G事件和frame state采样 | 需metadata |
| Finalizer/Cleanup/weak/unique | 原callback API | 串行finalizer G + 独立cleanup G + GC root graph | GC能力specific；nogc不可用 |
| `crypto/rand` | Read | OS fd/host entropy，可能park/offload | 需entropy source |
| `runtime/cgo`、cgo DNS | 普通同步调用 | ForeignOp、callback attach/reentry、token | 单线程host限制 |
| `math/rand`等per-P优化 | 原API | 随机状态挂P/G，不依赖pthread identity | 无额外障碍 |

### 33.4 高阶标准库调用

完整兼容必须假设用户callback可能阻塞，即使通常不会：

- `fmt.Stringer.String`、`error.Error`。
- `json.Marshaler/Unmarshaler`、`encoding.TextMarshaler`。
- `sort.Slice` comparator。
- `slices.SortFunc`、`maps`、`iter.Seq`。
- `http.Handler.ServeHTTP`。
- `sync.Once.Do`、`Pool.New`。
- `time.AfterFunc`、`context.AfterFunc`。
- `filepath.WalkFunc` 等visitor。
- SQL driver interface。
- reflect.MakeFunc callback。

如果VTA/whole-program证明callback集合全是bounded sync，调用保持plain。否则caller拥有coroutine primary，并通过descriptor透明await。保守分析可能让较多标准库函数成为coroutine，但不会让它们产生两份完整函数体。

Runtime内部持有scheduler lock、GC lock或 `preemptDisable` 时不得调用开放用户callback。标准库自己的用户级Mutex可以跨await持有，竞争者会park；这与goroutine在持锁状态被Go scheduler切换的现有语义一致。

### 33.5 File、DNS、process等不可poll操作

不是所有同步OS API都能由readiness poller表示：

- Regular file I/O。
- Filesystem metadata和目录遍历。
- Blocking libc DNS。
- Process wait和部分ioctl。
- 随机数设备或平台库。

Native使用有界blocking worker pool：

1. Caller把参数和GC roots放入operation record。
2. Park当前G。
3. Worker执行sync OS/C API。
4. Completion通过token投递scheduler。
5. Scheduler ready G并返回原有同步结果。

`Syscall*`/`RawSyscall*`是这条路径的compiler intrinsic入口，而不是例外：其effect会自动传播到所有上层Go函数，使未经修改的标准库同步代码获得异步底层实现。公开primitive始终保留单次调用及EAGAIN/EINTR/short-result语义；readiness wait/retry只在有明确契约的`internal/poll`层发生。Safe blocking syscall走worker ForeignOp，thread-scoped或动态unknown绑定调用时M，process-control走专门协议。只有13.1定义的RawCritical上下文允许验证后的不可挂起直调。

对于必须保留thread-local状态的调用，Caller仍先stack-cut；operation绑定目标M，由该M回到干净scheduler stack后执行typed thunk并释放P，而不是把活跃Go continuation留在C frame之下或搬到普通worker。

当前实现已把`LockOSThread`选择的同M foreign边界接到完整同P补偿：
active resume先detach到M-local record，generation/owner-epoch baton预留scalar-slot
replacement；原M先释放managed permit，再创建并把route的active-M slot切到replacement，
后者沿公共reducer服务同一ready/source域。permit必须在线程创建前释放，因为quota
holder按route而非M计账，child抢跑会被判为同route double-acquire。原M执行C并保留
物理thread identity；实际`pthread_create`只由启动期clean factory执行，原M仅同步提交
scalar M slot，因此其已修改的可继承thread state不会污染replacement。返回后
request/ring、strong join replacement、重获permit，再精确restore。单配额同route channel、timer、
socket readiness和nested blocking linked E2E均已通过。当前仍是每次locked call
create/join一个replacement pthread的正确性实现；standby缓存、`SetMaxThreads`联动、
blocked parent上的shutdown/fatal和完整callback/entersyscall矩阵未完成，因此仍不能标成
完整Go `entersyscall/exitsyscall`实现。

Worker pool必须有backpressure、取消/generation、shutdown和最大线程数；不能退化为每次调用创建pthread。

Native `os/exec` 优先使用 `posix_spawn`。必须 `fork` 时由runtime fork lock与STW/scheduler协调，child在exec前只执行async-signal-safe操作，不触碰allocator、BDWGC或遗留在其他M上的锁。任意低级 `syscall.Fork` 不能获得超出多线程Go runtime本身能保证的安全性。

### 33.6 Source和ABI兼容

- Go source API和类型签名保持不变。
- 包内/包间Go调用ABI由CoroPlan和summary选择，属于LLGo内部ABI。
- C ABI、plugin ABI、reflect ABI通过versioned descriptor/adapter稳定。
- `go:linkname` 需要明确绑定plain、coro或intrinsic语义，不能只按字符串碰巧链接。
- 标准库archive与runtime必须使用相同 `__llgo_coro_abi_v1`。

如果第三方包使用不受支持的unsafe方式读取函数值/itab私有布局，本来就不属于Go稳定ABI；可提供迁移诊断，但不为此保留#1532的全局三字closure。

### 33.7 标准库验收层次

#### Tier 0：编译和符号

- 编译全部 `go list std` package。
- 验证effect summary、linkname和descriptor ABI。
- 检查Pure sync package没有无用coro body。

#### Tier 1：Runtime核心

- runtime、sync、time、context、channel/select、internal/poll。
- Forced-GC suspended frame。
- Infinite loop preemption。

#### Tier 2：Native标准库

- 仓库`test/*`（包括`test/std/*`）全部适用测试。
- `go test std`。
- Go GOROOT test driver并发、timer、net、reflect、panic、runtime tests。
- HTTP/TLS/database/sql/os/exec/signal集成。

#### Tier 3：工具语义

- Caller/Stack、pprof、trace、race、testing parallel/fuzz。
- Plugin/cgo advanced callbacks。

#### Tier 4：平台标准库

- JS/WASM、WASI按对应host package支持范围运行上游tests。
- RTOS/baremetal仅对target声明具备的time/sync/io/HAL package运行；xfail必须是平台能力说明，不能掩盖scheduler语义错误。

Native退出实验模式的最低要求是 Tier 2，而不是少量自定义coroutine demo。

## 34. 实现障碍、可行解法与能力分级

### 34.1 不会迫使Go源码异步化的问题

以下问题复杂，但都有保持同步调用风格的路径：

| 问题 | 可行路径 |
|---|---|
| Blocking stdlib call | Caller coroutine primary + transparent await |
| Interface实现有sync/async混合 | MethodInvoke descriptor |
| Function callback可能阻塞 | FuncDispatch descriptor |
| Sync包调用async包 | Effect summary + compile-time await |
| C要求sync返回 | 最外层typed blockOn或worker |
| Timer/channel/netpoll | Park G + event wake |
| Panic跨await | Completion + frame-by-frame unwind |
| GC扫描suspended local | Runtime frame allocator/root graph |
| Stack trace | FrameDescriptor logical chain |

### 34.2 固有限制和最可行方案

#### 任意PC抢占

不可行：LLVM stackless coro不能捕获任意native stack。

最可行方案：保证所有managed无限路径经过compiler safepoint，把可观测最大延迟纳入CI。这已能满足高并发Go程序不需要显式yield的核心需求。

#### 活动C/assembly frame中间挂起

不可行：LLVM frame不包含外部native stack。

最可行方案：Native先把caller continuation完全保存进LLVM frame并返回scheduler，再由有界worker或指定M的干净thunk释放P后调用C；单线程host使用async adapter；不可插桩无限外部代码给出unbounded诊断。只有经证明有界、nonblocking且无callback的外部调用才允许在当前episode内直接执行。

#### JS/WASM同步导出的park与host future

不可行组合：wasm不返回JS时，Promise/setTimeout callback无法执行；而channel/WaitGroup等MayPark还可能经另一个G间接依赖这些host event，仅检查当前direct call graph会漏判。

最可行方案：Sync export只放行NoSuspend/YieldOnly，或通过closed-world completion proof的本地structured task closure；只有显式Async/Dual export contract才生成Promise-returning wrapper/companion，既有Sync symbol不能被静默改签；否则启用声明的JSPI/Asyncify或保守拒绝，不在运行后静默死锁。

#### Open-world plugin/reflect

可行但不能仅静态分析。

最可行方案：Versioned descriptor、module registration、OpaqueSuspend/unknown ExecFlags和ABI hash。第一阶段对未实现路径明确诊断。

#### Baremetal完整OS标准库

不是coroutine问题，而是平台没有process/filesystem/socket/signal。

最可行方案：Target capability + HAL。对已声明支持的API保持同步Go风格；不存在的服务按标准build tag或明确unsupported返回。

### 34.3 实现/测试成熟度

下表只描述实现和测试的累进门槛，不代表平台能力。平台是否具有process、filesystem、GC、reflect closure等能力，必须使用20.1的正交四态capability逐项声明；不能因为达到L4就推导该平台存在L3中的每一种OS服务。

| 等级 | 定义 |
|---|---|
| L0 Codegen | LLVM coro可生成、链接、基础frame生命周期正确 |
| L1 Language Core | 函数、go、channel/select、defer/panic、抢占、GC通过 |
| L2 Stdlib Core | runtime/sync/time/context/io/internal-poll通过 |
| L3 OS Stdlib | net/os/exec/signal等target能力包通过 |
| L4 Tooling | reflect完整、Caller/Stack、pprof/trace/race/testing完整 |
| L5 Interop | cgo callback、plugin、host async boundary完整 |

预期目标：

- Native POSIX：最终达到L5。
- JS/WASM：无栈语言核心和host可用stdlib可达到L4；但只有frame-aware GC、logical tooling及其声明的GC相关API均通过时才可标L4，nogc profile必须降级标注。Interop按Promise/JSPI capability定义。
- WASI：无栈语言核心和host可用包可达到L4；同样要求GC/tooling门槛，process/signal及nonpoll blocking import按WASI capability。
- RTOS/baremetal：无栈语言核心达到L2；L3按HAL逐项声明；通常不承诺plugin。

### 34.4 对总体可行性的判断

以完整Go标准库同步调用风格为前提，方案仍然可行，但工作量主要从 “LLVM coro lowering” 转移到以下runtime/compiler工程：

1. 全程序Effect/Demand/FuncRep及跨包summary。
2. Dynamic function/interface/reflect ABI。
3. 有界safepoint抢占和post-LLVM verifier。
4. Scheduler-aware sema/channel/select/timer/netpoll。
5. G-owned frame chain、panic/defer、GC和logical stack。
6. Native blocking operation compensation和JS host re-entry。
7. Stackless verifier、executor stack-cost summary和受限frame allocator。

没有必要把所有函数双版本，也没有必要修改标准库public API。真正不可兼容的组合都位于外部平台边界，并可用adapter、offload、capability或明确诊断隔离。

## 35. 最终验收标准

升级按target独立进行，不要求Native等待尚未实现的MCU/WASM能力，也不允许某个平台借另一个平台的通过结果宣称Full。某target从实验模式升级必须满足35.1全部通用门槛，再满足自己的平台门槛；声明`garbageCollector/finalizer/reflectMakeFunc`等为Full时还必须满足对应条件门槛。

### 35.1 通用门槛

1. Pure sync函数不生成多余coroutine版本；MaySuspend/NeedsPreempt函数不复制完整sync body；只有开放动态边界出现descriptor dispatch。
2. Go标准库和用户源码保持同步public API，不出现LLGo专用Future/await分支；`Syscall*`/`RawSyscall*`等底层intrinsic的effect可自动传播到未经修改的上层代码。
3. Interface、func value、嵌套aggregate、高阶callback、generics和reflect按canonical FuncRep/ABI hash混合plain-only与coro-only实现，不靠运行时位模式猜测。
4. 每个G都没有pthread/ucontext/RTOS-task/复制栈或managed Asyncify stack；所有spawn root与可挂起调用由LLVM-coro frame承载。
5. Suspend返回scheduler后不保留该G的managed-Go native/host activation或指向它的continuation。唯一允许保留的是按permit、depth和stack bytes独立预算的foreign/host ABI boundary stack；它不拥有Go continuation。
6. `MaxEpisodeStack`、foreign boundary stack、frame pool及所有静态resource capacity通过link/runtime预算，普通G数量不增加M/task/机器栈数量。
7. 不含显式yield的循环、递归和长路径可被稳定抢占；Strict/release artifact的`unboundedRegions == 0`，CPU-time bound与GC/OS/host pause分项报告。
8. Per-kind/per-target request generation不会因G迁移或并发Preempt/GCStop/Profile而丢失/覆盖；只有scheduler完成handoff后ack。
9. Timer/channel/select/sema在单executor下不阻塞平台thread；wake/park模型和stress无lost wake、duplicate enqueue或并发resume。
10. OS/host I/O和其他外部等待先stack-cut并park或转交bounded worker/event source；至少一个对抗用例证明等待尚未完成时另一G仍前进。纯计算可在当前过渡阶段暂时占用一个P，但生产级Native必须通过M/P/G、P-neutral runnable/result、动态P/steal和blocking compensation把密集计算、I/O及不可避免的阻塞解耦。
11. Root/child frame都按publish→unlink→DestroyPending→destroy/unregister→terminal ack顺序exactly-once终结；cancel、foreign return和GC竞态无UAF。
12. Panic/defer/recover/Goexit、named result和语言级nil/bounds/divide panic跨plain/coro frame符合Go语义，不依赖不可恢复host trap。
13. Command main正常返回立即退出；Reactor/Embedded bootstrap完成和显式shutdown遵守host lifecycle；range-over-func、`iter.Pull`、init、finalizer/cleanup、signal和LockOSThread按target capability有专项测试。
14. GC Full target的suspended-frame root、timer weak lease、Pinner/Handle与forced-GC测试通过；nogc target完成后无frame/task泄漏并准确报告GC相关语义不可用/降级。
15. Logical panic stack、Caller和goroutine dump显示source frame chain，不暴露scheduler/adapter噪声。
16. Build cache、archive、plugin/module registration和linker校验Coro/Scheduler/PanicABI、recursive FuncRep layout与CoroPlanDigest。
17. Go memory model同步边和atomic实现通过该target的并发/对齐/barrier测试；不支持lock-free宽度时使用验证过的锁/关中断fallback。
18. 每个xfail只归因于明确host/HAL capability，不能掩盖transparent await、抢占、stack-cut、park/wake或ABI错误。最终`test/*`和Go 1.26 GOROOT基线无unexpected failure和stale XPASS。

### 35.2 Native POSIX 门槛

1. Blocking C、Syscall和RawSyscall执行时其他G继续前进；ForeignOp/ForeignReentry、reserved permit、LockOSThread和STW/cancel协议通过。
2. `runtime.Pinner`、`runtime/cgo.Handle`、SetCgoTraceback、signal和plugin/callback registry达到声明能力。
3. 单P/多P、work stealing、BDWGC或所选GC、race-sensitive atomic测试通过。
4. 通过仓库`test/*`、目标范围`go test std`和Go 1.26 GOROOT全部适用测试；不得用function/package白名单或扩大xfail掩盖通用缺口。Native退出实验模式至少达到33.7 Tier 2。

### 35.3 JS/WASM 与 WASI 门槛

1. JS/WASM每次`runSlice`返回时无managed continuation留在host stack；timer/Promise/HostOp/HostReentry和FuncOf/Release不busy-wait、无stale callback。
2. Sync export ABI不被静默改写；间接wait-for graph包含spawn/WaitHost，无completion proof的MayPark默认拒绝或使用已声明JSPI/Async contract。
3. WASI clock/fd poll运行测试通过；nonpoll blocking import只有在async/thread compensation下才可声明Full，否则strict拒绝且degraded报告准确。
4. 声明GC Full/L4的WASM/WASI profile必须通过linear-memory suspended-frame forced GC、timer回收及logical tooling；nogc profile不能借L4标签暗示finalizer/weak完整。

### 35.4 RTOS 与 baremetal 门槛

1. Cortex-M和RISC-V baremetal QEMU以及至少一个FreeRTOS/Zephyr QEMU或硬件job通过真实timer、preemption、channel、PanicABI和tinygogc/frame-root测试。
2. Executor/IRQ/foreign stack high-water满足linker manifest；10万或target上限普通parked G不增加scheduler task/机器栈。
3. `maxG/liveFrames/frameDepth/timers/waitNodes/hostOps/callbackSlots/foreignDepth/eventRing`逐项耗尽时产生声明的resource error/fatal，且queue/root/token仍一致。
4. 32位atomic、64位fallback、atomic.Pointer barrier、ISR临界区和RawCritical HAL/syscall verifier通过。

满足通用加对应平台门槛后，才能评估将coroutine scheduler设为该平台默认。Native pthread模式的移除是更晚、独立的兼容性决策。
