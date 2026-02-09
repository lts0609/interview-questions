# 一文详解Golang GMP机制

## 0 前言

本文默认已经对GMP调度模型有了最基本的认识,内容中先根据**底层实现**、**核心函数**和**场景分析**三个部分进行说明。

源码版本采用1.25.5。

## 1.1 资源类型的底层实现

在Go的源码中,核心资源的实现基本都在`runtime`包下,g/m/p三种资源也不例外,结构定义都位于`runtime/runtime2.go`文件中。

### 1.1.1 G的实现

`g`是Golang当中最核心的数据结构之一,是GMP模型中G的实现,由于涉及底层其中的内容非常多,所以只关注最核心的几个。

```go
type g struct {
	// Stack parameters.
	// stack describes the actual stack memory: [stack.lo, stack.hi).
	// stackguard0 is the stack pointer compared in the Go stack growth prologue.
	// It is stack.lo+StackGuard normally, but can be StackPreempt to trigger a preemption.
	// stackguard1 is the stack pointer compared in the //go:systemstack stack growth prologue.
	// It is stack.lo+StackGuard on g0 and gsignal stacks.
	// It is ~0 on other goroutine stacks, to trigger a call to morestackc (and crash).
	stack       stack   // offset known to runtime/cgo
	stackguard0 uintptr // offset known to liblink
	stackguard1 uintptr // offset known to liblink

	_panic    *_panic // innermost panic - offset known to liblink
	_defer    *_defer // innermost defer
	m         *m      // current m; offset known to arm liblink
	sched     gobuf
	syscallsp uintptr // if status==Gsyscall, syscallsp = sched.sp to use during gc
	syscallpc uintptr // if status==Gsyscall, syscallpc = sched.pc to use during gc
	syscallbp uintptr // if status==Gsyscall, syscallbp = sched.bp to use in fpTraceback
	stktopsp  uintptr // expected sp at top of stack, to check in traceback
	// param is a generic pointer parameter field used to pass
	// values in particular contexts where other storage for the
	// parameter would be difficult to find. It is currently used
	// in four ways:
	// 1. When a channel operation wakes up a blocked goroutine, it sets param to
	//    point to the sudog of the completed blocking operation.
	// 2. By gcAssistAlloc1 to signal back to its caller that the goroutine completed
	//    the GC cycle. It is unsafe to do so in any other way, because the goroutine's
	//    stack may have moved in the meantime.
	// 3. By debugCallWrap to pass parameters to a new goroutine because allocating a
	//    closure in the runtime is forbidden.
	// 4. When a panic is recovered and control returns to the respective frame,
	//    param may point to a savedOpenDeferState.
	param        unsafe.Pointer
	atomicstatus atomic.Uint32
	stackLock    uint32 // sigprof/scang lock; TODO: fold in to atomicstatus
	goid         uint64
	schedlink    guintptr
	waitsince    int64      // approx time when the g become blocked
	waitreason   waitReason // if status==Gwaiting

	preempt       bool // preemption signal, duplicates stackguard0 = stackpreempt
	preemptStop   bool // transition to _Gpreempted on preemption; otherwise, just deschedule
	preemptShrink bool // shrink stack at synchronous safe point

	// asyncSafePoint is set if g is stopped at an asynchronous
	// safe point. This means there are frames on the stack
	// without precise pointer information.
	asyncSafePoint bool

	paniconfault bool // panic (instead of crash) on unexpected fault address
	gcscandone   bool // g has scanned stack; protected by _Gscan bit in status
	throwsplit   bool // must not split stack
	// activeStackChans indicates that there are unlocked channels
	// pointing into this goroutine's stack. If true, stack
	// copying needs to acquire channel locks to protect these
	// areas of the stack.
	activeStackChans bool
	// parkingOnChan indicates that the goroutine is about to
	// park on a chansend or chanrecv. Used to signal an unsafe point
	// for stack shrinking.
	parkingOnChan atomic.Bool
	// inMarkAssist indicates whether the goroutine is in mark assist.
	// Used by the execution tracer.
	inMarkAssist bool
	coroexit     bool // argument to coroswitch_m

	raceignore      int8  // ignore race detection events
	nocgocallback   bool  // whether disable callback from C
	tracking        bool  // whether we're tracking this G for sched latency statistics
	trackingSeq     uint8 // used to decide whether to track this G
	trackingStamp   int64 // timestamp of when the G last started being tracked
	runnableTime    int64 // the amount of time spent runnable, cleared when running, only used when tracking
	lockedm         muintptr
	fipsIndicator   uint8
	syncSafePoint   bool // set if g is stopped at a synchronous safe point.
	runningCleanups atomic.Bool
	sig             uint32
	writebuf        []byte
	sigcode0        uintptr
	sigcode1        uintptr
	sigpc           uintptr
	parentGoid      uint64          // goid of goroutine that created this goroutine
	gopc            uintptr         // pc of go statement that created this goroutine
	ancestors       *[]ancestorInfo // ancestor information goroutine(s) that created this goroutine (only used if debug.tracebackancestors)
	startpc         uintptr         // pc of goroutine function
	racectx         uintptr
	waiting         *sudog         // sudog structures this g is waiting on (that have a valid elem ptr); in lock order
	cgoCtxt         []uintptr      // cgo traceback context
	labels          unsafe.Pointer // profiler labels
	timer           *timer         // cached timer for time.Sleep
	sleepWhen       int64          // when to sleep until
	selectDone      atomic.Uint32  // are we participating in a select and did someone win the race?

	// goroutineProfiled indicates the status of this goroutine's stack for the
	// current in-progress goroutine profile
	goroutineProfiled goroutineProfileStateHolder

	coroarg *coro // argument during coroutine transfers
	bubble  *synctestBubble

	// Per-G tracer state.
	trace gTraceState

	// Per-G GC state

	// gcAssistBytes is this G's GC assist credit in terms of
	// bytes allocated. If this is positive, then the G has credit
	// to allocate gcAssistBytes bytes without assisting. If this
	// is negative, then the G must correct this by performing
	// scan work. We track this in bytes to make it fast to update
	// and check for debt in the malloc hot path. The assist ratio
	// determines how this corresponds to scan work debt.
	gcAssistBytes int64

	// valgrindStackID is used to track what memory is used for stacks when a program is
	// built with the "valgrind" build tag, otherwise it is unused.
	valgrindStackID uintptr
}
```

#### 1.1.1.1 栈相关字段

栈是Goroutine执行代码的内存载体,这部分字段控制栈增长、抢占触发、系统栈/用户栈区分。

|    字段     |  类型   |                     核心含义                     |                             作用                             |
| :---------: | :-----: | :----------------------------------------------: | :----------------------------------------------------------: |
|    stack    |  stack  |                   栈的内存区间                   | 每个G有独立的stack,存储函数调用栈、局部变量、返回地址等,是执行代码的内存基础 |
| stackguard0 | uintptr | 用户栈的栈保护阈值（默认 `stack.lo+stackGuard`） | 栈增长：G执行函数调用时,汇编层检查栈指针（SP）是否低于该值,低于则触发栈扩容； 被动抢占：sysmon检测到G运行超10ms,将该值设为 `StackPreempt`（抢占阈值）,G执行到安全点（函数调用/栈扩容）时触发抢占 |
| stackguard1 | uintptr | 系统栈的栈保护阈值（默认 `stack.lo+stackGuard`） | 溢出保护：仅g0、gsignal有效,用该字段检查栈增长,触发栈增长时直接崩溃 |

##### stack

标识栈的地址范围,即[stack.lo, stack.hi),Go的栈是**从高地址向低地址**增长的：

- 初始时,栈指针（SP）指向 `stack.hi-8`（64 位系统）,即栈顶下方第一个可用地址；
- 函数调用时,SP向 `stack.lo` 方向移动（栈空间占用增加）；
- `[lo, hi)` 刚好覆盖了从栈底到栈顶下方所有可用地址,符合栈的实际使用逻辑。

```Go
// Stack describes a Go execution stack.
// The bounds of the stack are exactly [lo, hi),
// with no implicit data structures on either side.
type stack struct {
	lo uintptr // 栈底
	hi uintptr // 栈顶
}
```

##### stackguard0/stackguard1

这两个字段标识用户/系统栈的阈值，一般都是`stack.lo+stackGuard`,根据`runtime/stack.go`中定义,`stackGuard`大小为928。

```Go
stackGuard = stackNosplit + stackSystem + abi.StackSmall
```

当汇编层汇编层检查栈指针SP指向小于阈值时，用户G和系统G（g0/gsignal）都会触发对应的逻辑

- 用户G执行函数调用时，汇编层检查栈指针SP：若`SP < stackguard0`，说明栈空间不足，触发栈动态扩容;
- g0/gsignal执行runtime逻辑（调度、GC、信号处理）时，汇编层检查SP：若`SP < stackguard0` ，说明系统栈溢出，直接崩溃；

其中`stackguard0`还与被动抢占的触发相关：

sysmon检测到用户G运行超 10ms 时，将`stackguard0`设置为特殊抢占值`StackPreempt`；当G 到安全点时，检查到该值触发抢占。这段调用逻辑可以根据调用流程`sysmon() -> retake() -> preemptone()`了解。



抢占和栈扩容仅对用户G有效，g0/gsignal不使用`stackguard0`做抢占 / 扩容;

同样，`stackguard1`相关的溢出保护也仅在g0/gsignal有效，用户G的 `stackguard1` 被设为全1（无效值），从不使用。

#### 1.1.1.2 状态与调度核心字段

这部分字段控制 G 的状态流转、上下文保存、与 M/P 的绑定关系,是调度器操作 G 的核心入口。

|     字段     |     类型      |                核心含义                |                             作用                             |
| :----------: | :-----------: | :------------------------------------: | :----------------------------------------------------------: |
| atomicstatus | atomic.Uint32 |              G的原子状态               |                     原子操作保证并发安全                     |
|      m       |      *m       |              当前绑定的 M              |        G在M上执行时,`m`指向该M对象,M的 `curg`指向该G         |
|    sched     |     gobuf     | 保存G的执行上下文（SP/PC/BP 寄存器值） | M切换执行不同G时,先将当前G的上下文保存到`sched`,再从目标G的`sched`加载上下文 |
|  schedlink   |   guintptr    |           指向队列中下一个G            | `_Grunnable`状态的G连接成链表,是P本地队列、全局队列的核心节点 |
|   lockedm    |   muintptr    |              G绑定的特定M              | 调度器会优先让该M绑定P执行该G，抢占时先解除 `lockedm`,避免M无法复用 |

##### atmoicstatus

该字段表示当前g的状态，每次变更都是原子操作，枚举值定义如下:

```Go
const (
	// G status
	//
	// _Gidle means this goroutine was just allocated and has not
	// yet been initialized.
	_Gidle = iota // 0

	// _Grunnable means this goroutine is on a run queue. It is
	// not currently executing user code. The stack is not owned.
	_Grunnable // 1

	// _Grunning means this goroutine may execute user code. The
	// stack is owned by this goroutine. It is not on a run queue.
	// It is assigned an M and a P (g.m and g.m.p are valid).
	_Grunning // 2

	// _Gsyscall means this goroutine is executing a system call.
	// It is not executing user code. The stack is owned by this
	// goroutine. It is not on a run queue. It is assigned an M.
	_Gsyscall // 3

	// _Gwaiting means this goroutine is blocked in the runtime.
	// It is not executing user code. It is not on a run queue,
	// but should be recorded somewhere (e.g., a channel wait
	// queue) so it can be ready()d when necessary. The stack is
	// not owned *except* that a channel operation may read or
	// write parts of the stack under the appropriate channel
	// lock. Otherwise, it is not safe to access the stack after a
	// goroutine enters _Gwaiting (e.g., it may get moved).
	_Gwaiting // 4

	// _Gmoribund_unused is currently unused, but hardcoded in gdb
	// scripts.
	_Gmoribund_unused // 5

	// _Gdead means this goroutine is currently unused. It may be
	// just exited, on a free list, or just being initialized. It
	// is not executing user code. It may or may not have a stack
	// allocated. The G and its stack (if any) are owned by the M
	// that is exiting the G or that obtained the G from the free
	// list.
	_Gdead // 6

	// _Genqueue_unused is currently unused.
	_Genqueue_unused // 7

	// _Gcopystack means this goroutine's stack is being moved. It
	// is not executing user code and is not on a run queue. The
	// stack is owned by the goroutine that put it in _Gcopystack.
	_Gcopystack // 8

	// _Gpreempted means this goroutine stopped itself for a
	// suspendG preemption. It is like _Gwaiting, but nothing is
	// yet responsible for ready()ing it. Some suspendG must CAS
	// the status to _Gwaiting to take responsibility for
	// ready()ing this G.
	_Gpreempted // 9

	// _Gscan combined with one of the above states other than
	// _Grunning indicates that GC is scanning the stack. The
	// goroutine is not executing user code and the stack is owned
	// by the goroutine that set the _Gscan bit.
	//
	// _Gscanrunning is different: it is used to briefly block
	// state transitions while GC signals the G to scan its own
	// stack. This is otherwise like _Grunning.
	//
	// atomicstatus&~Gscan gives the state the goroutine will
	// return to when the scan completes.
	_Gscan          = 0x1000
	_Gscanrunnable  = _Gscan + _Grunnable  // 0x1001
	_Gscanrunning   = _Gscan + _Grunning   // 0x1002
	_Gscansyscall   = _Gscan + _Gsyscall   // 0x1003
	_Gscanwaiting   = _Gscan + _Gwaiting   // 0x1004
	_Gscanpreempted = _Gscan + _Gpreempted // 0x1009
)
```

对于上面的状态，可以分为两类：**基础状态**和**GC扫描叠加态**，基础状态包括初始/可运行/运行中/抢占等，扫描叠加态不是独立状态，而是叠加在基础状态上的标记，标识GC正在扫描该G的栈。

下面对于0-9的基础状态做出说明：

|     状态常量      | 数值 |                       核心含义                        |                           关键特征                           |
| :---------------: | :--: | :---------------------------------------------------: | :----------------------------------------------------------: |
|      _Gidle       |  0   |                       刚分配的G                       |        无栈、不在任何队列、无M/P绑定、仅内存分配完成         |
|    _Grunnable     |  1   | 可运行状态，在调度队列（P 本地 / 全局队列）中等待执行 |          有栈、在调度队列、无M/P绑定、仅等待被调度           |
|     _Grunning     |  2   |                   正在执行用户代码                    |      有栈（独占）、不在调度队列、绑定M+P、执行用户逻辑       |
|     _Gsyscall     |  3   |                   正在执行系统调用                    |   有栈（独占）、不在调度队列、仅绑定M（无P）、执行内核逻辑   |
|     _Gwaiting     |  4   |        阻塞态（IO/channel/mutex/runtime阻塞）         | 无栈所有权（可被GC移动）、不在调度队列、无M/P 绑定、需被主动唤醒 |
| _Gmoribund_unused |  5   |                       废弃状态                        |                              无                              |
|      _Gdead       |  6   |    退出/闲置态（执行完毕/在G自由链表中/ 待初始化）    |          栈可能释放/保留、不在调度队列、归属于M管理          |
| _Genqueue_unused  |  7   |                       废弃状态                        |                              无                              |
|    _Gcopystack    |  8   |            栈正在被拷贝（扩容/收缩/移动）             |  无执行、不在调度队列、栈被 runtime 独占、完成后恢复原状态   |
|    _Gpreempted    |  9   |                      被动抢占态                       |         不在调度队列、经CAS转为`_Gwaiting`后等待唤醒         |

`_Gscan` 是位掩码（0x1000），不是独立状态，而是叠加在基础状态上的标记，标识 GC 正在扫描该 G 的栈，下面对于叠加状态状态做出说明：

|    叠加态常量     |  数值  |  对应基础态   |                  核心含义                  |            关键特征             |
| :---------------: | :----: | :-----------: | :----------------------------------------: | :-----------------------------: |
| `_Gscanrunnable`  | 0x1001 | `_Grunnable`  |          GC 扫描 “可运行 G” 的栈           | 扫描时 G 仍在调度队列，暂停出队 |
|  `_Gscanrunning`  | 0x1002 |  `_Grunning`  | GC 通知运行中的 G 自行扫描栈（特殊叠加态） | G 仍在执行，仅短暂阻塞状态变更  |
|  `_Gscansyscall`  | 0x1003 |  `_Gsyscall`  |        GC 扫描 “系统调用中 G” 的栈         |  系统调用暂停，扫描完成后恢复   |
|  `_Gscanwaiting`  | 0x1004 |  `_Gwaiting`  |          GC 扫描 “阻塞态 G” 的栈           |    阻塞状态不变，扫描后恢复     |
| `_Gscanpreempted` | 0x1009 | `_Gpreempted` |          GC 扫描 “被抢占 G” 的栈           |    抢占状态不变，扫描后恢复     |

###### 状态转换过程

**场景1**：`_Gidle` -> `_Gdead` -> `_Grunnable`

这部分完全在 `newproc1()`函数的逻辑中实现，该函数是在执行`go func`启动一个新的协程时的实际操作，简化代码如下：

```Go
func newproc1(fn *funcval, callergp *g, callerpc uintptr, parked bool, waitreason waitReason) *g {
	mp := acquirem() // disable preemption because we hold M and P in local vars.
	pp := mp.p.ptr()
	newg := gfget(pp)
  // 没有从gFree中获取到G就新创建一个
	if newg == nil {
		newg = malg(stackMin)
    // 状态从_Gidle切换到_Gdead
		casgstatus(newg, _Gidle, _Gdead)
		allgadd(newg) // publishes with a g->status of Gdead so GC scanner doesn't look at uninitialized stack.
	}
	// 新G一定要是_Gdead状态
	if readgstatus(newg) != _Gdead {
		throw("newproc1: new g is not Gdead")
	}
	......
  // 初始化G的栈和上下文
  ......
	var status uint32 = _Grunnable
	......
  // 正常情况下状态切换到_Grunnable
	casgstatus(newg, _Gdead, status)

	return newg
}
```

**场景2**：`_Grunnable` -> `_Grunning`

该状态转换由`execute()`函数触发，向上层寻找`execute()`的触发场景，包括常规调度、系统调用返回、阻塞唤醒、被动抢占恢复等，但最终确认都是入队后经过`schedule()`函数而来，然后G与M绑定后切换到用户栈执行逻辑。



**场景3**：`_Grunning` -> `_Gsyscall` -> `_Grunnable` -> `_Grunning`

这条状态转换链路场景为发生系统调用，M与P解绑，然后系统调用结束后G被唤醒重新执行。

函数`entersyscall()`中把`_Grunning`状态转换为`_Gsyscall`，`exitsyscall()`函数会把G的状态从`_Gsyscall`转换为 `_Grunnable` ，然后走正常调度流程，最终到执行状态。

特殊情况：M和P绑定异常，此时`exitsyscall()`内部会调用`exitsyscall0()`兜底流程，把`_Gsyscall`直接转换为 `_Grunning` 状态，此时不走调度流程。



**场景4**：`_Grunning` -> `_Gwaiting` -> `_Grunnable` -> `_Grunning`

G主动阻塞或休眠，先调用`gopark()`进入`_Gwaiting`状态，满足条件后被`goready()`函数唤醒并进行调度。



**场景5**：`_Grunning` -> `_Gpreempted` -> `_Gwaiting` -> `_Grunnable` -> `_Grunning`

G的被动抢占场景，`sysmon`检测G运行超时后设置`stackguard0`为`StackPreempt`，到安全点时执行`preemptPark()`把G切换到`_Gpreempted`状态，在GC的STW阶段扫描G时`suspendG()`函数会把`_Gpreempted`置为`_Gwaiting`，然后调度流程中`findrunnable()`会把`_Gpreempted` 转换为`_Grunnable`，然后继续走调度流程。



**场景6**：`_Grunning` -> `_Gcopystack` -> `_Grunning`

发生在栈的扩容期间，`newstack()`中进行状态转换。栈缩容发生在GC阶段，不会经过`_Gcopystack`状态。



**场景7**：`_Grunning` -> `_Gdead` -> `_Grunnable` -> `_Grunning`

当一个用户G运行结束后，会调用`goexit0()`将`_Grunning`转换到`_Gdead`，内部还调用了`gfput()`函数把这个G放入gFree列表。在后续申请新的G时，就会和`newproc()`创建流程一样，但是可以从gFree中直接获取到一个`_Gdead`状态的空闲G，然后转换为`_Grunnable`并调度。

```Go
// goexit continuation on g0.
func goexit0(gp *g) {
	gdestroy(gp)
	schedule()
}
```



**场景8**：`基础状态` -> `叠加态` -> `基础状态`

这部分逻辑在`runtime/gcmark.go`中，当进入GC阶段后会执行`markroot()`函数，其中暂停G的逻辑`suspendG()`会把基础状态转变成叠加态。完整的代码如下：

```Go
func suspendG(gp *g) suspendGState {
  ......
	for i := 0; ; i++ {
		switch s := readgstatus(gp); s {
		default:
			if s&_Gscan != 0 {
				// Someone else is suspending it. Wait
				// for them to finish.
				//
				// TODO: It would be nicer if we could
				// coalesce suspends.
				break
			}

			dumpgstatus(gp)
			throw("invalid g status")

		case _Gdead:
			// Nothing to suspend.
			//
			// preemptStop may need to be cleared, but
			// doing that here could race with goroutine
			// reuse. Instead, goexit0 clears it.
			return suspendGState{dead: true}

		case _Gcopystack:
			// The stack is being copied. We need to wait
			// until this is done.

		case _Gpreempted:
			// We (or someone else) suspended the G. Claim
			// ownership of it by transitioning it to
			// _Gwaiting.
			if !casGFromPreempted(gp, _Gpreempted, _Gwaiting) {
				break
			}

			// We stopped the G, so we have to ready it later.
			stopped = true

			s = _Gwaiting
			fallthrough

		case _Grunnable, _Gsyscall, _Gwaiting:
			// Claim goroutine by setting scan bit.
			// This may race with execution or readying of gp.
			// The scan bit keeps it from transition state.
			if !castogscanstatus(gp, s, s|_Gscan) {
				break
			}

			// Clear the preemption request. It's safe to
			// reset the stack guard because we hold the
			// _Gscan bit and thus own the stack.
			gp.preemptStop = false
			gp.preempt = false
			gp.stackguard0 = gp.stack.lo + stackGuard

			// The goroutine was already at a safe-point
			// and we've now locked that in.
			//
			// TODO: It would be much better if we didn't
			// leave it in _Gscan, but instead gently
			// prevented its scheduling until resumption.
			// Maybe we only use this to bump a suspended
			// count and the scheduler skips suspended
			// goroutines? That wouldn't be enough for
			// {_Gsyscall,_Gwaiting} -> _Grunning. Maybe
			// for all those transitions we need to check
			// suspended and deschedule?
			return suspendGState{g: gp, stopped: stopped}

		case _Grunning:
			// Optimization: if there is already a pending preemption request
			// (from the previous loop iteration), don't bother with the atomics.
			if gp.preemptStop && gp.preempt && gp.stackguard0 == stackPreempt && asyncM == gp.m && asyncM.preemptGen.Load() == asyncGen {
				break
			}

			// Temporarily block state transitions.
			if !castogscanstatus(gp, _Grunning, _Gscanrunning) {
				break
			}

			// Request synchronous preemption.
			gp.preemptStop = true
			gp.preempt = true
			gp.stackguard0 = stackPreempt

			// Prepare for asynchronous preemption.
			asyncM2 := gp.m
			asyncGen2 := asyncM2.preemptGen.Load()
			needAsync := asyncM != asyncM2 || asyncGen != asyncGen2
			asyncM = asyncM2
			asyncGen = asyncGen2

			casfrom_Gscanstatus(gp, _Gscanrunning, _Grunning)

			// Send asynchronous preemption. We do this
			// after CASing the G back to _Grunning
			// because preemptM may be synchronous and we
			// don't want to catch the G just spinning on
			// its status.
			if preemptMSupported && debug.asyncpreemptoff == 0 && needAsync {
				// Rate limit preemptM calls. This is
				// particularly important on Windows
				// where preemptM is actually
				// synchronous and the spin loop here
				// can lead to live-lock.
				now := nanotime()
				if now >= nextPreemptM {
					nextPreemptM = now + yieldDelay/2
					preemptM(asyncM)
				}
			}
		}

		if i == 0 {
			nextYield = nanotime() + yieldDelay
		}
		if nanotime() < nextYield {
			procyield(10)
		} else {
			osyield()
			nextYield = nanotime() + yieldDelay/2
		}
	}
}
```

由基础状态到叠加态的转变可以分为几个类型：

1. 直接叠加`_Gscan`的状态：_Grunnable, _Gsyscall, _Gwaiting
2. 先转换为中间态再叠加：_Gpreempted -> _Gwaiting
3. 不进行叠加态转换：_Gdead, _Gcopystack
4. `_Grunning`分支：_Grunning -> _Gscanrunning -> _Gpreempted -> _Gwaiting

这里面有一个比较特殊的分支即`_Grunning`，从上面的源码中可以看到，这个case中会先把`_Grunning`转换成`_Gscanrunning`临时锁定，然后对`g.stackguard0`设置抢占标记，再恢复到`_Grunning`状态。后续就和`sysmon`触发的被动抢占一样，G运行到安全点后栈检查时设置为`_Gpreempted`。

这部分属于逻辑的复用但是目标不同，被动抢占是为了公平调度，而GC时触发的抢占是为了能够进行栈扫描。

##### m

在G和M是在runtime中是双向关联的的，M结构体中存了G的指针，G的结构体也存了对应M的指针。

```Go
func execute(gp *g, inheritTime bool) {
	mp := getg().m

	// 绑定M和G
	mp.curg = gp
  // 绑定G和M
	gp.m = mp
	gp.syncSafePoint = false // Clear the flag, which may have been set by morestack.
	casgstatus(gp, _Grunnable, _Grunning)
	gp.waitsince = 0
	gp.preempt = false
	gp.stackguard0 = gp.stack.lo + stackGuard
	......
}
```

```Go
func dropg() {
	gp := getg()
	// 没有写屏障地解除G对M的绑定
	setMNoWB(&gp.m.curg.m, nil)
  // 没有写屏障地解除M对G的绑定
	setGNoWB(&gp.m.curg, nil)
}
```

##### sched

`sched`字段保存了G的寄存器状态。

```Go
type gobuf struct {
  sp   uintptr        // 栈指针（Stack Pointer）
  pc   uintptr        // 程序计数器（Program Counter）
  g    guintptr       // 指向当前所属的G结构体
  ctxt unsafe.Pointer // 通用上下文指针
  lr   uintptr        // 链接寄存器（Link Register）
  bp   uintptr        // 基指针（Base Pointer），仅用于启用帧指针的架构
}
```

在进行G的切换时会进行状态保存，也就是把程序上下文存入`sched`字段，然后挂起这个G，等到它被重新调度时，执行`gogo()`从`sched`中恢复状态。

```Go
func save(pc, sp, bp uintptr) {
	gp := getg()

	if gp == gp.m.g0 || gp == gp.m.gsignal {
		throw("save on system g not allowed")
	}

	gp.sched.pc = pc
	gp.sched.sp = sp
	gp.sched.lr = 0
	gp.sched.bp = bp

	if gp.sched.ctxt != nil {
		badctxt()
	}
}
```

##### schedlink

在GMP模型中，存在如P的本地队列、全局队列等结构，`schedlink`就是G能被串联成链表的基础，作为 G 的成员字段无需为链表节点单独分配内存。关联结构包括`gQueue`和`gList`等，同一时刻一个G只能存在于`gQueue`或者`gList`之一中，其中`gQueue`仅用于P本地队列的实现，而`gList`是队列的通用基础结构。

```Go
// A G can only be on one gQueue or gList at a time.
type gQueue struct {
	head guintptr
	tail guintptr
	size int32
}

type gList struct {
	head guintptr
	size int32
}
```

##### lockedm

某些情况下G和M存在强制绑定关系，这个关联也是双向的，和G中有M，M中有G的实现相同，M中也存在`lockedg`字段标识M锁定的G。

#### 1.1.1.3 抢占相关标识字段

辅助控制抢占流程,区分抢占类型、触发栈优化,是 1.14+ 被动抢占的关键辅助字段。

|      字段      | 类型 |             核心含义              |                             作用                             |
| :------------: | :--: | :-------------------------------: | :----------------------------------------------------------: |
|    preempt     | bool |             抢占信号              |      抢占标记：与`stackguard0`字段同步设置，标记G待抢占      |
|  preemptStop   | bool | 抢占时是否转为 `_Gpreempted` 状态 | 抢占行为控制：true为被动抢占，后续转为 `_Gpreempted`，false 为主动让出，后续转为 `_Grunnable` |
| preemptShrink  | bool |      是否在同步安全点收缩栈       |         内存优化：若该值为true，抢占时调度器会收缩栈         |
| asyncSafePoint | bool |        G是否停在异步安全点        |           抢占安全：`preemptPark()` 中会校验该字段           |

其中的`preempt`和`stackguard0`设置可在抢占的核心函数`preemptone()`中看到：

```Go
func preemptone(pp *p) bool {
	mp := pp.m.ptr()
	if mp == nil || mp == getg().m {
		return false
	}
	gp := mp.curg
	if gp == nil || gp == mp.g0 {
		return false
	}
	// 设置抢占相关标识
	gp.preempt = true
	gp.stackguard0 = stackPreempt

	// Request an async preemption of this P.
	if preemptMSupported && debug.asyncpreemptoff == 0 {
		pp.preempt = true
		preemptM(mp)
	}

	return true
}
```

`preemptStop`字段仅会在`suspendG()`中的`_Grunning`分支才会被设置为true，表示强制**被动抢占**，即GC时为了STW强制停止正在运行的G。`asyncPreempt2()`中也是根据该字段选择对应的抢占逻辑，值为true的**被动抢占**后面状态会被修改为`_Gpreempted`，值为false对应的**主动让出**状态会被修改为`_Grunnable`。这部分逻辑名为抢占，但实际上描述的是G与M的解绑，然后资源的重新调度。

```Go
func asyncPreempt2() {
	gp := getg()
	gp.asyncSafePoint = true
	if gp.preemptStop {
    // 被动
		mcall(preemptPark)
	} else {
    // 主动
		mcall(gopreempt_m)
	}
	gp.asyncSafePoint = false
}
```

`asyncSafePoint`字段用于标识G是否处在异步抢占安全点，Go 的安全点分两类：

1. 主动安全点：G主动进入（如函数调用边界、循环回边、channel 操作）；
2. 异步安全点：G被信号（SIGURG）打断后被动进入（即`asyncPreempt2()`触发的场景）；

#### 1.1.1.4 阻塞/等待相关字段

|      字段       |     类型      |           核心含义           |                     GMP 视角 & 关联流程                      |
| :-------------: | :-----------: | :--------------------------: | :----------------------------------------------------------: |
|   `waitsince`   |    `int64`    |    G进入阻塞状态的时间戳     |        监控分析：调度器统计阻塞时长,检测长时间阻塞的G        |
|  `waitreason`   | `waitReason`  |         G阻塞的原因          | 语义区分：调度器/监控工具（pprof/trace）区分阻塞类型（IO/Mutex/Channel）,优化调度策略 |
|    `waiting`    |   `*sudog`    |    G等待的 `sudog` 结构体    | 同步原语绑定：G等待Channel/Mutex时,`sudog` 挂到该字段,转为 `_Gwaiting` |
| `parkingOnChan` | `atomic.Bool` | G是否即将在channel操作中挂起 | 栈安全：标记通道阻塞前置状态,避免栈收缩时修改通道相关栈数据（不安全），值为 `true` 时延迟栈收缩直到G解除阻塞 |

#### 1.1.1.5 GC / 安全点相关字段（GC 协作）

配合 GC 执行,保证 GC 扫描的准确性、安全性,是 Go 垃圾回收的核心辅助字段。

|      字段       |   类型   |                核心含义                |                     GMP 视角 & 关联流程                      |
| :-------------: | :------: | :------------------------------------: | :----------------------------------------------------------: |
|   `stackLock`   | `uint32` |     栈修改锁（保护栈不被并发修改）     | - **GC / 采样安全**：GC 扫描栈、sigprof 采样时加锁,避免栈扩容 / 收缩导致数据错乱；扫描完成后解锁。 |
|  `gcscandone`   |  `bool`  |       G 的栈是否已被 GC 扫描完成       | - **GC 标记**：GC 扫描完栈后置 `true`；G 被抢占时,需等待扫描完成再调度。 |
| `gcAssistBytes` | `int64`  |      G 的 GC 辅助信用值（字节数）      | - **内存分配控制**：`>0` 可直接分配内存,`<0` 需先执行 GC 扫描工作偿还 “债务”；- **调度优先级**：G 被调度时,先检查该值,有债务则先执行 GC 扫描,再执行业务逻辑。 |
| `syncSafePoint` |  `bool`  | G 是否停在同步安全点（有精确指针信息） | - **GC 扫描**：同步安全点的 G 可精确扫描栈指针,异步抢占的 G 需等待到同步安全点再扫描。 |

#### 1.1.1.6 调试 / 追踪 / 性能分析字段

非核心但辅助排查问题、分析性能,是调试 Goroutine 问题的关键依据。

|                          字段                           |             类型              |           核心含义           |                           关联场景                           |
| :-----------------------------------------------------: | :---------------------------: | :--------------------------: | :----------------------------------------------------------: |
|                         `goid`                          |           `uint64`            | G 的唯一标识（Goroutine ID） |   调试（`runtime.Stack()`）、日志、trace 工具区分不同 G。    |
| `tracking`/`trackingSeq`/`trackingStamp`/`runnableTime` |          布尔 / 整型          |       调度延迟统计相关       | 调度器统计 G 从 `_Grunnable` 到 `_Grunning` 的时长,优化调度策略。 |
|                         `trace`                         |         `gTraceState`         |        Per-G 追踪状态        | `runtime/trace` 工具记录 G 的调度、抢占、阻塞事件,生成可视化轨迹。 |
|                   `goroutineProfiled`                   | `goroutineProfileStateHolder` |          栈分析状态          |    `pprof` 采集 Goroutine 栈信息时标记状态,避免重复采集。    |

#### 1.1.1.7 其他辅助字段

支撑 cgo、定时器、协程创建等边缘场景,非核心但保证 Goroutine 功能完整性。

|              字段               |                   核心含义                   |                           关联场景                           |
| :-----------------------------: | :------------------------------------------: | :----------------------------------------------------------: |
| `syscallsp/syscallpc/syscallbp` |         G 执行系统调用时的 SP/PC/BP          |     GC 扫描系统调用的栈上下文（G 处于 `_Gsyscall` 时）。     |
|             `param`             |                 通用参数指针                 | 通道唤醒时传递 `sudog`、debugCall 传递参数、GC 辅助传递状态。 |
|       `timer`/`sleepWhen`       |     `time.Sleep` 的缓存定时器 / 唤醒时间     | G 执行 `time.Sleep` 时,定时器触发后将 G 转为 `_Grunnable`。  |
|  `parentGoid`/`gopc`/`startpc`  | 父 G ID / 创建 G 的 go 语句 PC/G 入口函数 PC |         调试时追溯 Goroutine 的创建来源、执行入口。          |
|            `cgoCtxt`            |                cgo 回溯上下文                |             cgo 调用的栈回溯,排查 cgo 相关问题。             |

------

## 

### 1.1.2 M的实现

```Go
type m struct {
	g0      *g     // goroutine with scheduling stack
	morebuf gobuf  // gobuf arg to morestack
	divmod  uint32 // div/mod denominator for arm - known to liblink (cmd/internal/obj/arm/obj5.go)

	// Fields not known to debuggers.
	procid          uint64            // for debuggers, but offset not hard-coded
	gsignal         *g                // signal-handling g
	goSigStack      gsignalStack      // Go-allocated signal handling stack
	sigmask         sigset            // storage for saved signal mask
	tls             [tlsSlots]uintptr // thread-local storage (for x86 extern register)
	mstartfn        func()
	curg            *g       // current running goroutine
	caughtsig       guintptr // goroutine running during fatal signal
	p               puintptr // attached p for executing go code (nil if not executing go code)
	nextp           puintptr
	oldp            puintptr // the p that was attached before executing a syscall
	id              int64
	mallocing       int32
	throwing        throwType
	preemptoff      string // if != "", keep curg running on this m
	locks           int32
	dying           int32
	profilehz       int32
	spinning        bool // m is out of work and is actively looking for work
	blocked         bool // m is blocked on a note
	newSigstack     bool // minit on C thread called sigaltstack
	printlock       int8
	incgo           bool          // m is executing a cgo call
	isextra         bool          // m is an extra m
	isExtraInC      bool          // m is an extra m that does not have any Go frames
	isExtraInSig    bool          // m is an extra m in a signal handler
	freeWait        atomic.Uint32 // Whether it is safe to free g0 and delete m (one of freeMRef, freeMStack, freeMWait)
	needextram      bool
	g0StackAccurate bool // whether the g0 stack has accurate bounds
	traceback       uint8
	allpSnapshot    []*p          // Snapshot of allp for use after dropping P in findRunnable, nil otherwise.
	ncgocall        uint64        // number of cgo calls in total
	ncgo            int32         // number of cgo calls currently in progress
	cgoCallersUse   atomic.Uint32 // if non-zero, cgoCallers in use temporarily
	cgoCallers      *cgoCallers   // cgo traceback if crashing in cgo call
	park            note
	alllink         *m // on allm
	schedlink       muintptr
	lockedg         guintptr
	createstack     [32]uintptr // stack that created this thread, it's used for StackRecord.Stack0, so it must align with it.
	lockedExt       uint32      // tracking for external LockOSThread
	lockedInt       uint32      // tracking for internal lockOSThread
	mWaitList       mWaitList   // list of runtime lock waiters

	mLockProfile mLockProfile // fields relating to runtime.lock contention
	profStack    []uintptr    // used for memory/block/mutex stack traces

	// wait* are used to carry arguments from gopark into park_m, because
	// there's no stack to put them on. That is their sole purpose.
	waitunlockf          func(*g, unsafe.Pointer) bool
	waitlock             unsafe.Pointer
	waitTraceSkip        int
	waitTraceBlockReason traceBlockReason

	syscalltick uint32
	freelink    *m // on sched.freem
	trace       mTraceState

	// these are here because they are too large to be on the stack
	// of low-level NOSPLIT functions.
	libcall    libcall
	libcallpc  uintptr // for cpu profiler
	libcallsp  uintptr
	libcallg   guintptr
	winsyscall winlibcall // stores syscall parameters on windows

	vdsoSP uintptr // SP for traceback while in VDSO call (0 if not in call)
	vdsoPC uintptr // PC for traceback while in VDSO call

	// preemptGen counts the number of completed preemption
	// signals. This is used to detect when a preemption is
	// requested, but fails.
	preemptGen atomic.Uint32

	// Whether this is a pending preemption signal on this M.
	signalPending atomic.Uint32

	// pcvalue lookup cache
	pcvalueCache pcvalueCache

	dlogPerM

	mOS

	chacha8   chacha8rand.State
	cheaprand uint64

	// Up to 10 locks held by this m, maintained by the lock ranking code.
	locksHeldLen int
	locksHeld    [10]heldLockInfo
}
```



### 1.1.3 P的实现

```Go
type p struct {
	id          int32
	status      uint32 // one of pidle/prunning/...
	link        puintptr
	schedtick   uint32     // incremented on every scheduler call
	syscalltick uint32     // incremented on every system call
	sysmontick  sysmontick // last tick observed by sysmon
	m           muintptr   // back-link to associated m (nil if idle)
	mcache      *mcache
	pcache      pageCache
	raceprocctx uintptr

	deferpool    []*_defer // pool of available defer structs (see panic.go)
	deferpoolbuf [32]*_defer

	// Cache of goroutine ids, amortizes accesses to runtime·sched.goidgen.
	goidcache    uint64
	goidcacheend uint64

	// Queue of runnable goroutines. Accessed without lock.
	runqhead uint32
	runqtail uint32
	runq     [256]guintptr
	// runnext, if non-nil, is a runnable G that was ready'd by
	// the current G and should be run next instead of what's in
	// runq if there's time remaining in the running G's time
	// slice. It will inherit the time left in the current time
	// slice. If a set of goroutines is locked in a
	// communicate-and-wait pattern, this schedules that set as a
	// unit and eliminates the (potentially large) scheduling
	// latency that otherwise arises from adding the ready'd
	// goroutines to the end of the run queue.
	//
	// Note that while other P's may atomically CAS this to zero,
	// only the owner P can CAS it to a valid G.
	runnext guintptr

	// Available G's (status == Gdead)
	gFree gList

	sudogcache []*sudog
	sudogbuf   [128]*sudog

	// Cache of mspan objects from the heap.
	mspancache struct {
		// We need an explicit length here because this field is used
		// in allocation codepaths where write barriers are not allowed,
		// and eliminating the write barrier/keeping it eliminated from
		// slice updates is tricky, more so than just managing the length
		// ourselves.
		len int
		buf [128]*mspan
	}

	// Cache of a single pinner object to reduce allocations from repeated
	// pinner creation.
	pinnerCache *pinner

	trace pTraceState

	palloc persistentAlloc // per-P to avoid mutex

	// Per-P GC state
	gcAssistTime         int64 // Nanoseconds in assistAlloc
	gcFractionalMarkTime int64 // Nanoseconds in fractional mark worker (atomic)

	// limiterEvent tracks events for the GC CPU limiter.
	limiterEvent limiterEvent

	// gcMarkWorkerMode is the mode for the next mark worker to run in.
	// That is, this is used to communicate with the worker goroutine
	// selected for immediate execution by
	// gcController.findRunnableGCWorker. When scheduling other goroutines,
	// this field must be set to gcMarkWorkerNotWorker.
	gcMarkWorkerMode gcMarkWorkerMode
	// gcMarkWorkerStartTime is the nanotime() at which the most recent
	// mark worker started.
	gcMarkWorkerStartTime int64

	// gcw is this P's GC work buffer cache. The work buffer is
	// filled by write barriers, drained by mutator assists, and
	// disposed on certain GC state transitions.
	gcw gcWork

	// wbBuf is this P's GC write barrier buffer.
	//
	// TODO: Consider caching this in the running G.
	wbBuf wbBuf

	runSafePointFn uint32 // if 1, run sched.safePointFn at next safe point

	// statsSeq is a counter indicating whether this P is currently
	// writing any stats. Its value is even when not, odd when it is.
	statsSeq atomic.Uint32

	// Timer heap.
	timers timers

	// Cleanups.
	cleanups       *cleanupBlock
	cleanupsQueued uint64 // monotonic count of cleanups queued by this P

	// maxStackScanDelta accumulates the amount of stack space held by
	// live goroutines (i.e. those eligible for stack scanning).
	// Flushed to gcController.maxStackScan once maxStackScanSlack
	// or -maxStackScanSlack is reached.
	maxStackScanDelta int64

	// gc-time statistics about current goroutines
	// Note that this differs from maxStackScan in that this
	// accumulates the actual stack observed to be used at GC time (hi - sp),
	// not an instantaneous measure of the total stack size that might need
	// to be scanned (hi - lo).
	scannedStackSize uint64 // stack size of goroutines scanned by this P
	scannedStacks    uint64 // number of goroutines scanned by this P

	// preempt is set to indicate that this P should be enter the
	// scheduler ASAP (regardless of what G is running on it).
	preempt bool

	// gcStopTime is the nanotime timestamp that this P last entered _Pgcstop.
	gcStopTime int64

	// Padding is no longer needed. False sharing is now not a worry because p is large enough
	// that its size class is an integer multiple of the cache line size (for any of our architectures).
}
```

### 1.1.4 调度器的实现

```Go
type schedt struct {
	goidgen    atomic.Uint64
	lastpoll   atomic.Int64 // time of last network poll, 0 if currently polling
	pollUntil  atomic.Int64 // time to which current poll is sleeping
	pollingNet atomic.Int32 // 1 if some P doing non-blocking network poll

	lock mutex

	// When increasing nmidle, nmidlelocked, nmsys, or nmfreed, be
	// sure to call checkdead().

	midle        muintptr // idle m's waiting for work
	nmidle       int32    // number of idle m's waiting for work
	nmidlelocked int32    // number of locked m's waiting for work
	mnext        int64    // number of m's that have been created and next M ID
	maxmcount    int32    // maximum number of m's allowed (or die)
	nmsys        int32    // number of system m's not counted for deadlock
	nmfreed      int64    // cumulative number of freed m's

	ngsys atomic.Int32 // number of system goroutines

	pidle        puintptr // idle p's
	npidle       atomic.Int32
	nmspinning   atomic.Int32  // See "Worker thread parking/unparking" comment in proc.go.
	needspinning atomic.Uint32 // See "Delicate dance" comment in proc.go. Boolean. Must hold sched.lock to set to 1.

	// Global runnable queue.
	runq gQueue

	// disable controls selective disabling of the scheduler.
	//
	// Use schedEnableUser to control this.
	//
	// disable is protected by sched.lock.
	disable struct {
		// user disables scheduling of user goroutines.
		user     bool
		runnable gQueue // pending runnable Gs
	}

	// Global cache of dead G's.
	gFree struct {
		lock    mutex
		stack   gList // Gs with stacks
		noStack gList // Gs without stacks
	}

	// Central cache of sudog structs.
	sudoglock  mutex
	sudogcache *sudog

	// Central pool of available defer structs.
	deferlock mutex
	deferpool *_defer

	// freem is the list of m's waiting to be freed when their
	// m.exited is set. Linked through m.freelink.
	freem *m

	gcwaiting  atomic.Bool // gc is waiting to run
	stopwait   int32
	stopnote   note
	sysmonwait atomic.Bool
	sysmonnote note

	// safePointFn should be called on each P at the next GC
	// safepoint if p.runSafePointFn is set.
	safePointFn   func(*p)
	safePointWait int32
	safePointNote note

	profilehz int32 // cpu profiling rate

	procresizetime int64 // nanotime() of last change to gomaxprocs
	totaltime      int64 // ∫gomaxprocs dt up to procresizetime

	customGOMAXPROCS bool // GOMAXPROCS was manually set from the environment or runtime.GOMAXPROCS

	// sysmonlock protects sysmon's actions on the runtime.
	//
	// Acquire and hold this mutex to block sysmon from interacting
	// with the rest of the runtime.
	sysmonlock mutex

	// timeToRun is a distribution of scheduling latencies, defined
	// as the sum of time a G spends in the _Grunnable state before
	// it transitions to _Grunning.
	timeToRun timeHistogram

	// idleTime is the total CPU time Ps have "spent" idle.
	//
	// Reset on each GC cycle.
	idleTime atomic.Int64

	// totalMutexWaitTime is the sum of time goroutines have spent in _Gwaiting
	// with a waitreason of the form waitReasonSync{RW,}Mutex{R,}Lock.
	totalMutexWaitTime atomic.Int64

	// stwStoppingTimeGC/Other are distributions of stop-the-world stopping
	// latencies, defined as the time taken by stopTheWorldWithSema to get
	// all Ps to stop. stwStoppingTimeGC covers all GC-related STWs,
	// stwStoppingTimeOther covers the others.
	stwStoppingTimeGC    timeHistogram
	stwStoppingTimeOther timeHistogram

	// stwTotalTimeGC/Other are distributions of stop-the-world total
	// latencies, defined as the total time from stopTheWorldWithSema to
	// startTheWorldWithSema. This is a superset of
	// stwStoppingTimeGC/Other. stwTotalTimeGC covers all GC-related STWs,
	// stwTotalTimeOther covers the others.
	stwTotalTimeGC    timeHistogram
	stwTotalTimeOther timeHistogram

	// totalRuntimeLockWaitTime (plus the value of lockWaitTime on each M in
	// allm) is the sum of time goroutines have spent in _Grunnable and with an
	// M, but waiting for locks within the runtime. This field stores the value
	// for Ms that have exited.
	totalRuntimeLockWaitTime atomic.Int64
}

```

## 1.2 核心函数

### 1.2.1 getg()



### 1.2.2 newproc()





1.2.3 acquireg()



1.2.4 dropg()

### 1.2.3 goready()



### 1.2.4 gopark()



### 1.2.5 drop()





### 



