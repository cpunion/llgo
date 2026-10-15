"use strict";

const assert = require("node:assert/strict");
require(process.argv[2]);

const opcode = (4 << 16) | 1;
const rawOpenOpcode = (4 << 16) | 2;
const tick = () => new Promise((resolve) => queueMicrotask(resolve));
const canceledPayload = (operation) => operation === rawOpenOpcode ? [0, 0, 4, 0, 1, 0] : [2, 0, 4, 0, 0, 0];

function fixture(name, implementation, extra = {}, operation = opcode) {
    const go = new global.Go();
    const memory = new WebAssembly.Memory({ initial: 2, maximum: 8 });
    const completed = [];
    const operations = [];
    const actions = [];
    let freed = 0;
    const originalArgument = { path: "owned argument" };
    const words = Array(24).fill(0);
    words[0] = 1;
    words[1] = 7;
    words[2] = 11;
    words[3] = operation;
    words[4] = operation === rawOpenOpcode ? 5 : 4;
    words[6] = 512;
    words[8] = name.length;
    words[10] = 768;
    words[12] = 1;
    if (operation === rawOpenOpcode) words[14] = 1024;
    const write = (ptr, values) => {
        const view = new DataView(memory.buffer);
        values.forEach((value, index) => view.setUint32(ptr + 4 * index, value, true));
    };
    global.fs = { [name]: implementation, ...extra };
    const api = {
        memory,
        malloc: () => 128,
        free: (pointer) => { assert.equal(pointer, 128); freed++; },
        __llgo_coro_host_profile_v1: () => (0x80000000 | 1 | (1 << 8) | (1 << 9) | (1 << 11)) >>> 0,
        __llgo_coro_host_publish_time_v1: () => 1,
        __llgo_coro_host_publish_wall_time_v1: () => 1,
        __llgo_coro_host_next_operation_v1: (pointer) => {
            const next = operations.shift() || Array(24).fill(0);
            write(pointer, next);
            return next[0];
        },
        __llgo_coro_host_next_action_v1: (pointer) => {
            const next = actions.shift() || Array(8).fill(0);
            write(pointer, next);
            return next[0];
        },
        __llgo_coro_host_ack_cancel_v1: () => { throw new Error("operation cancellation must not use action acknowledgement"); },
        __llgo_coro_host_complete_operation_v1: (...result) => {
            assert.deepEqual(result.slice(0, 4), [7, 11, 0, 3]);
            completed.push(result);
            actions.push([1, 1, 1, 1, 0, 0, 0, 0]);
            return 1;
        },
        __llgo_coro_host_continue_slice_v1: (...args) => {
            assert.equal(completed.length, 1);
            write(args[7], Array(8).fill(0));
            return 1;
        },
        _start: () => {
            new Uint8Array(memory.buffer, 512, name.length).set(new TextEncoder().encode(name));
            // Existing private value table, not an added WASM or public API.
            go._llgoJSValues.set(10, originalArgument);
            go._llgoJSNextHandle = 12;
            write(768, [10]);
            write(1024, [0xffffffff]);
            operations.push(words.slice());
        },
    };
    const done = go.run({ exports: api });
    return {
        go, api, memory, words, originalArgument, completed, done,
        output: () => new DataView(memory.buffer).getInt32(1024, true),
        handles: () => go._llgoJSValues.size,
        freed: () => freed,
        cancel: () => {
            const cancel = words.slice();
            cancel[0] = 2;
            operations.push(cancel);
            return go._drainCoroHost();
        },
    };
}

async function delayedAndMemoryGrowth() {
    let callback;
    let received;
    const f = fixture("stat", (arg, cb) => { received = arg; callback = cb; });
    assert.equal(received, f.originalArgument);
    await tick();
    assert.equal(f.go.exited, false, "pending fs call is a live host obligation");
    assert.equal(f.completed.length, 0);
    new Uint8Array(f.memory.buffer, 512, 4).fill(0xff);
    new DataView(f.memory.buffer).setUint32(768, 0xffffffff, true);
    f.memory.grow(1);
    const result = { size: 42 };
    callback(null, result);
    assert.equal(f.completed.length, 0, "physical callback must not reenter WASM");
    assert.equal(await f.done, 0);
    assert.equal(f.completed.length, 1);
    const words = f.completed[0];
    assert.deepEqual([words[5], words[6], words[7], words[8], words[9]], [0, 4, 0, 0, 0]);
    assert.equal(f.go._llgoJSValues.get(words[4]), result);
    assert.equal(f.freed(), 1);
    const size = f.handles();
    callback(new Error("late duplicate"), {});
    await tick();
    assert.equal(f.completed.length, 1);
    assert.equal(f.handles(), size, "duplicate callback must not allocate handles");
}

async function synchronousAndThrow() {
    let returned = false;
    const result = { size: 9 };
    const f = fixture("stat", (_arg, callback) => {
        callback(null, result);
        callback(new Error("duplicate"));
        returned = true;
        throw new Error("throw after callback must not replace its first result");
    });
    assert.equal(returned, true);
    assert.equal(f.completed.length, 0);
    await f.done;
    assert.equal(f.completed.length, 1);
    assert.equal(f.go._llgoJSValues.get(f.completed[0][4]), result);

    const error = new Error("synchronous fs failure");
    const thrown = fixture("stat", () => { throw error; });
    assert.equal(thrown.completed.length, 0);
    await thrown.done;
    assert.equal(thrown.completed[0][4], 2);
    assert.equal(thrown.go._llgoJSValues.get(thrown.completed[0][6]), error);
}

async function canceledPhysicalJoin() {
    let callback;
    const f = fixture("stat", (_arg, cb) => { callback = cb; });
    const handles = f.handles();
    assert.equal(f.cancel(), true);
    await tick();
    assert.equal(f.go.exited, false);
    assert.equal(f.completed.length, 0, "cancel is not permission to release a physically pending operation");
    callback(null, { ignored: true });
    await f.done;
    assert.deepEqual(f.completed[0].slice(4), [2, 0, 4, 0, 0, 0]);
    assert.equal(f.handles(), handles, "canceled completion must not allocate unused handles");
    callback(null, {});
    await tick();
    assert.equal(f.completed.length, 1);
}

async function canceledOpenClosesBeforeCompletion(operation = opcode) {
    let opened;
    let closed;
    let closeCalls = 0;
    const f = fixture("open", (_arg, callback) => { opened = callback; }, {
        close(fd, callback) { assert.equal(fd, 37); closeCalls++; closed = callback; },
    }, operation);
    const handles = f.handles();
    f.cancel();
    opened(null, 37);
    await tick();
    assert.equal(closeCalls, 1, "unclaimed successful open must release its descriptor");
    assert.equal(f.completed.length, 0, "physical close is part of cancellation ownership");
    closed(null);
    await f.done;
    assert.deepEqual(f.completed[0].slice(4), canceledPayload(operation));
    assert.equal(f.handles(), handles);
    opened(null, 37);
    closed(null);
    await tick();
    assert.equal(closeCalls, 1);
    assert.equal(f.completed.length, 1);
    if (operation === rawOpenOpcode) assert.equal(f.output(), -1, "host-owned cancellation must not transfer the FD");
}

async function cancellationBeforeQueuedPublication(operation = opcode) {
    let opened;
    let closed = 0;
    const f = fixture("open", (_arg, callback) => { opened = callback; }, {
        close(fd, callback) {
            assert.equal(fd, 39);
            closed++;
            callback(null);
        },
    }, operation);
    const handles = f.handles();
    opened(null, 39);
    // Physical completion has occurred, but its queued publication has not.
    // Cancellation must still own and dispose of the unclaimed descriptor.
    f.cancel();
    assert.equal(f.completed.length, 0);
    await f.done;
    assert.equal(closed, 1);
    assert.deepEqual(f.completed[0].slice(4), canceledPayload(operation));
    assert.equal(f.handles(), handles);
    if (operation === rawOpenOpcode) assert.equal(f.output(), -1, "cancel before publication must keep host cleanup ownership");
}

async function cleanupFailureDoesNotAcknowledge(operation = opcode) {
    let opened;
    const failure = new Error("physical close failed");
    const f = fixture("open", (_arg, callback) => { opened = callback; }, {
        close() { throw failure; },
    }, operation);
    const rejected = assert.rejects(f.done, (error) => error === failure);
    const handles = f.handles();
    f.cancel();
    opened(null, 40);
    await rejected;
    assert.equal(f.go.exitCode, 1);
    assert.equal(f.completed.length, 0, "failed physical cleanup must not acknowledge cancellation");
    assert.equal(f.handles(), handles);
    assert.equal(f.freed(), 1);
}

async function exitBeforeCallback(operation = opcode) {
    for (const fatal of [false, true]) {
        let opened;
        let closed;
        const f = fixture("open", (_arg, callback) => { opened = callback; }, {
            close(fd, callback) { assert.equal(fd, 38); closed = callback; },
        }, operation);
        const failure = new Error("fatal guest failure");
        const rejected = assert.rejects(f.done, fatal
            ? (error) => error === failure : /before filesystem operations physically joined/);
        if (fatal) f.go._failCoroHost(failure);
        else f.go._finishCoroHost();
        await rejected;
        assert.equal(f.go.exitCode, 1, "normal completion must reject outstanding physical operations");
        assert.equal(f.freed(), 1);
        opened(null, 38);
        await tick();
        assert.equal(typeof closed, "function", "late open after exit must not leak a descriptor");
        closed(null);
        await tick();
        assert.equal(f.completed.length, 0, "late callback must not call disposed WASM exports");
        assert.equal(f.freed(), 1);
    }
}

async function exactIdentity() {
    let callback;
    const f = fixture("stat", (_arg, cb) => { callback = cb; });
    assert.throws(() => f.go._takeCoroOperation(1, f.words.slice()), /duplicate|pending|exact/i);
    const stale = f.words.slice();
    stale[0] = 2;
    stale[2]++;
    assert.throws(() => f.go._takeCoroOperation(2, stale), /cancel|exact|obligation/i);
    const changed = f.words.slice();
    changed[0] = 2;
    changed[6]++;
    assert.throws(() => f.go._takeCoroOperation(2, changed), /cancel|exact|obligation/i);
    callback(null, 0);
    await f.done;
    assert.equal(f.completed.length, 1);
}

async function allowlistAndReservedWords() {
    let invoked = false;
    const f = fixture("notAnFSCall", () => { invoked = true; });
    await f.done;
    assert.equal(invoked, false, "host properties outside the FS method allowlist must not be invoked");
    assert.equal(f.completed[0][4], 2);
    assert.equal(f.go._llgoJSValues.get(f.completed[0][6]).code, "ENOSYS");

    const browser = fixture("stat", undefined);
    await browser.done;
    assert.equal(browser.completed[0][4], 2);
    assert.equal(browser.go._llgoJSValues.get(browser.completed[0][6]).code, "ENOSYS",
        "an allowed method missing on this host must produce the ordinary JS error handle");

    let callback;
    const pending = fixture("stat", (_arg, cb) => { callback = cb; });
    for (const index of [5, 7, 13, 14, 23]) {
        const malformed = pending.words.slice();
        malformed[index] = 1;
        assert.throws(() => pending.go._takeCoroOperation(1, malformed), /unsupported|malformed/i);
    }
    callback(null, 1);
    await pending.done;
}

async function rawOpenResults() {
    for (const fd of [0, 53, 0x7fffffff]) {
        let callback;
        const f = fixture("open", (_arg, cb) => { callback = cb; }, {}, rawOpenOpcode);
        const handles = f.handles();
        const changed = f.words.slice();
        changed[0] = 2;
        changed[3] = opcode;
        assert.throws(() => f.go._takeCoroOperation(2, changed), /exact obligation|unsupported/);
        changed[3] = rawOpenOpcode;
        changed[14] += 4;
        assert.throws(() => f.go._takeCoroOperation(2, changed), /exact obligation/);
        f.memory.grow(1);
        callback(null, fd);
        assert.equal(f.completed.length, 0, "raw descriptor publication must not reenter WASM");
        await f.done;
        assert.deepEqual(f.completed[0].slice(4), [0, 0, 4, 0, 0, 0]);
        assert.equal(f.output(), fd, "resource must survive scalar payload consumption and memory growth");
        assert.equal(f.handles(), handles, "successful raw open must not allocate a descriptor handle");
        callback(null, fd);
        await tick();
        assert.equal(f.completed.length, 1);
        assert.equal(f.handles(), handles);
    }

    const error = Object.assign(new Error("open failed"), { code: "ENOENT" });
    for (const throws of [false, true]) {
        const f = fixture("open", (_arg, callback) => {
            if (throws) throw error;
            callback(error);
        }, {}, rawOpenOpcode);
        const handles = f.handles();
        await f.done;
        const payload = f.completed[0].slice(4);
        assert.deepEqual([payload[0], payload[1], payload[3], payload[4], payload[5]], [0, 0, 0, 1, 0]);
        assert.equal(f.go._llgoJSValues.get(payload[2]), error, "Go, not the core, interprets the original JS error");
        assert.equal(f.handles(), handles + 1);
        assert.equal(f.output(), -1, "failed acquisition must leave the borrowed guard empty");
    }
}

async function borrowedOpenPublicationOwnership() {
    let callback;
    let closes = 0;
    const f = fixture("open", (_arg, cb) => { callback = cb; }, {
        close(fd, cb) { assert.equal(fd, 43); closes++; cb(null); },
    }, rawOpenOpcode);
    const post = f.api.__llgo_coro_host_complete_operation_v1;
    f.api.__llgo_coro_host_complete_operation_v1 = (...args) => {
        assert.equal(f.output(), 43, "the guard must own the FD before physical Post");
        return post(...args);
    };
    callback(null, 43);
    await f.done;
    assert.equal(closes, 0, "the host must not close an output already transferred to Go");
    // Model the pre-registered ordinary Go guard on a later canceled resume:
    // scalar payload suppression cannot erase the borrowed output's resource.
    const owned = f.output();
    new DataView(f.memory.buffer).setInt32(1024, -1, true);
    global.fs.close(owned, (error) => assert.equal(error, null));
    callback(null, 43);
    await tick();
    assert.equal(closes, 1, "duplicate callback must not close the Go-owned FD again");
    assert.equal(f.output(), -1, "the producer must not touch guest output after Post");
}

async function borrowedOpenInlineCompletion() {
    const f = fixture("open", (_arg, callback) => {
        callback(null, 0);
        callback(new Error("duplicate inline callback"));
    }, {}, rawOpenOpcode);
    assert.equal(f.output(), -1, "inline callback must not touch Go storage on the submitting stack");
    assert.equal(f.completed.length, 0);
    f.memory.grow(1);
    await f.done;
    assert.equal(f.output(), 0, "FD zero must transfer after queued publication");
    assert.equal(f.completed.length, 1);
}

async function borrowedOpenRejectsInvalidOutput() {
    let callback;
    let calls = 0;
    const f = fixture("open", (_arg, cb) => { calls++; callback = cb; }, {}, rawOpenOpcode);
    const obsolete = f.words.slice();
    obsolete[4] = 4;
    obsolete[14] = 0;
    assert.throws(() => f.go._takeCoroOperation(1, obsolete), /unsupported/,
        "the unconsumed scalar-only raw-open protocol must not survive alongside borrowed output");
    for (const output of [0, 1025, f.memory.buffer.byteLength, 0xfffffffc]) {
        const words = f.words.slice();
        words[1]++;
        words[14] = output;
        assert.throws(() => f.go._takeCoroOperation(1, words), /empty aligned output/);
    }
    new DataView(f.memory.buffer).setInt32(1024, 0, true);
    const words = f.words.slice();
    words[1]++;
    assert.throws(() => f.go._takeCoroOperation(1, words), /empty aligned output/);
    new DataView(f.memory.buffer).setInt32(1024, -1, true);
    assert.equal(calls, 1, "invalid output must be rejected before invoking the host");
    callback(null, 44);
    await f.done;
}

async function borrowedOpenPreStoreFailureReleasesHostFD() {
    for (const lostObligation of [false, true]) {
        let opened;
        let closed;
        let closes = 0;
        const f = fixture("open", (_arg, callback) => { opened = callback; }, {
            close(fd, callback) { assert.equal(fd, 0); closes++; closed = callback; },
        }, rawOpenOpcode);
        const handles = f.handles();
        const rejected = assert.rejects(f.done, lostObligation
            ? /lost its exact obligation/ : /lost its empty output slot/);
        f.memory.grow(1);
        if (lostObligation) f.go._coroHost.operations.delete("7:11");
        else new DataView(f.memory.buffer).setInt32(1024, 91, true);
        opened(null, 0);
        await tick();
        assert.equal(closes, 1, "pre-store protocol failure must close the still host-owned FD, including zero");
        assert.equal(f.completed.length, 0, "invalid delivery must not post a completion");
        assert.equal(f.go.exited, false, "fatal rejection must wait for the host-owned FD's physical close");
        assert.equal(f.freed(), 0);
        assert.equal(f.output(), lostObligation ? -1 : 91, "failed pre-store validation must not overwrite guest storage");
        closed(null);
        await rejected;
        assert.equal(f.go.exitCode, 1);
        assert.equal(f.freed(), 1);
        assert.equal(f.handles(), handles);
        opened(null, 0);
        closed(null);
        await tick();
        assert.equal(closes, 1, "duplicate callbacks must not retry resource release");
        assert.equal(f.completed.length, 0);
    }
}

async function borrowedOpenPreStoreCloseFailureIsFatal() {
    for (const throws of [false, true]) {
        let opened;
        let closed;
        let closes = 0;
        const failure = new Error("pre-store physical close failed");
        const f = fixture("open", (_arg, callback) => { opened = callback; }, {
            close(fd, callback) {
                assert.equal(fd, 47);
                closes++;
                if (throws) throw failure;
                closed = callback;
            },
        }, rawOpenOpcode);
        const rejected = assert.rejects(f.done, (error) => error === failure);
        new DataView(f.memory.buffer).setInt32(1024, 92, true);
        opened(null, 47);
        if (!throws) {
            await tick();
            assert.equal(typeof closed, "function");
            closed(failure);
            closed(null);
        }
        await rejected;
        assert.equal(closes, 1, "a failed close must not be retried against a possibly reused FD");
        assert.equal(f.completed.length, 0, "failed cleanup cannot acknowledge physical completion");
        assert.equal(f.output(), 92);
        assert.equal(f.go.exitCode, 1);
    }
}

async function borrowedOpenPostStoreRejectionKeepsGoOwnership() {
    for (const status of [0, 2, 3, 4, "throw"]) {
        let opened;
        let closes = 0;
        let posts = 0;
        let resumes = 0;
        const f = fixture("open", (_arg, callback) => { opened = callback; }, {
            close() { closes++; },
        }, rawOpenOpcode);
        const handles = f.handles();
        f.api.__llgo_coro_host_continue_slice_v1 = () => { resumes++; return 1; };
        f.api.__llgo_coro_host_complete_operation_v1 = () => {
            posts++;
            assert.equal(f.output(), 0, "ownership transfers before Post, including a rejected Post");
            if (status === "throw") throw new Error("physical Post trapped");
            return status;
        };
        const rejected = assert.rejects(f.done, status === "throw" ? /Post trapped/ : /not acknowledged/);
        opened(null, 0);
        await rejected;
        assert.equal(f.go.exitCode, 1);
        assert.equal(f.freed(), 1);
        assert.equal(f.output(), 0, "fatal instance disposal must not fabricate a guest cleanup handoff");
        assert.equal(closes, 0, "host must not double-close a descriptor already owned by the Go output");
        assert.equal(posts, 1);
        assert.equal(resumes, 0, "rejected physical Post cannot resume Go");
        assert.equal(f.handles(), handles);
        opened(null, 0);
        await tick();
        assert.equal(closes, 0);
        assert.equal(posts, 1);
    }
}

async function rawOpenPhysicalLifecycle() {
    await canceledOpenClosesBeforeCompletion(rawOpenOpcode);
    await cancellationBeforeQueuedPublication(rawOpenOpcode);
    await cleanupFailureDoesNotAcknowledge(rawOpenOpcode);
    await exitBeforeCallback(rawOpenOpcode);

    let callback;
    const failed = fixture("open", (_arg, cb) => { callback = cb; }, {}, rawOpenOpcode);
    const handles = failed.handles();
    failed.cancel();
    callback(new Error("canceled open failed physically"));
    await failed.done;
    assert.deepEqual(failed.completed[0].slice(4), canceledPayload(rawOpenOpcode));
    assert.equal(failed.handles(), handles);
}

async function rawOpenRejectsMalformedResults() {
    let invoked = false;
    const wrong = fixture("stat", () => { invoked = true; }, {}, rawOpenOpcode);
    await assert.rejects(wrong.done, /must name open/);
    assert.equal(invoked, false);
    for (const fd of [-1, 0x80000000, 0.5, NaN, undefined, {}]) {
        const f = fixture("open", (_arg, callback) => callback(null, fd), {}, rawOpenOpcode);
        await assert.rejects(f.done, /invalid descriptor/);
        assert.equal(f.completed.length, 0, "invalid raw values must never become owned core descriptors");
    }
}

async function rejectedPublicationIsNotAcknowledgement() {
    for (const status of [0, 2, 3, 4]) {
        let callback;
        const f = fixture("stat", (_arg, cb) => { callback = cb; });
        f.api.__llgo_coro_host_complete_operation_v1 = () => status;
        callback(null, { size: 42 });
        await assert.rejects(f.done, /not acknowledged/);
        assert.equal(f.completed.length, 0);
        assert.equal(f.go.exitCode, 1);
    }
}

async function boundedGoTurnsYieldToHostTasks() {
    const immediate = global.setImmediate;
    for (const fallback of [false, true]) {
        if (fallback) global.setImmediate = undefined;
        try {
            const go = new global.Go();
            const state = { schedule: null };
            go._coroHost = state;
            let turns = 0;
            let complete;
            const done = new Promise((resolve) => { complete = resolve; });
            const host = new Promise((resolve) => immediate(() => resolve(turns)));
            const schedule = () => {
                const record = { active: true };
                state.schedule = record;
                go._scheduleCoroTurn(record);
            };
            go._continueCoroHost = () => {
                if (++turns === 50) complete();
                else schedule();
            };
            schedule();
            const [turnsBeforeHost] = await Promise.all([host, done]);
            assert.ok(turnsBeforeHost < 50, "runnable Go slices must not starve the host I/O/timer task phase");
        } finally {
            global.setImmediate = immediate;
        }
    }
}

(async () => {
    for (const test of [delayedAndMemoryGrowth, synchronousAndThrow, canceledPhysicalJoin,
        canceledOpenClosesBeforeCompletion, cancellationBeforeQueuedPublication,
        cleanupFailureDoesNotAcknowledge, exitBeforeCallback, exactIdentity,
        allowlistAndReservedWords, rawOpenResults, borrowedOpenPublicationOwnership, borrowedOpenInlineCompletion,
        borrowedOpenRejectsInvalidOutput, borrowedOpenPreStoreFailureReleasesHostFD,
        borrowedOpenPreStoreCloseFailureIsFatal, borrowedOpenPostStoreRejectionKeepsGoOwnership,
        rawOpenPhysicalLifecycle,
        rawOpenRejectsMalformedResults, rejectedPublicationIsNotAcknowledgement,
        boundedGoTurnsYieldToHostTasks]) {
        await test();
        console.log(`PASS ${test.name}`);
    }
    console.log("coro JS fs operation protocol ok");
})().catch((error) => { console.error(error); process.exitCode = 1; });
