"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const directory = fs.mkdtempSync(path.join(os.tmpdir(), "llgo-js-borrowed-"));
for (const name of ["normal", "early", "late"]) fs.writeFileSync(path.join(directory, name), name);

const active = new Map();
const pendingCloses = [];
let opened = 0, closed = 0, progress = 0, bits = 0;
let earlyCompletion;
global.llgoBorrowedPath = directory;
global.llgoBorrowedState = () => bits;
global.llgoBorrowedMark = (event) => {
    if (event === 1) {
        assert.equal(bits & 16, 16, "main must pass the suspended GC point before cancellation cleanup");
        assert.equal(typeof earlyCompletion, "function");
        const callback = earlyCompletion;
        earlyCompletion = null;
        callback();
    } else if (event === 2) {
        bits |= 2;
    } else if (event === 3) {
        assert.equal(bits & 12, 12);
        assert.equal(pendingCloses.length, 2);
        progress++;
    } else if (event === 4) {
        assert.equal(progress, 8);
        for (const complete of pendingCloses.splice(0)) complete();
    } else if (event === 5) {
        bits |= 16;
    } else {
        throw new Error(`unexpected borrowed-resource marker ${event}`);
    }
};

global.fs = Object.create(fs);
global.fs.open = (filename, flags, perm, callback) => {
    fs.open(filename, flags, perm, (error, fd) => {
        if (error) return callback(error);
        assert.equal(active.has(fd), false);
        const name = path.basename(filename);
        active.set(fd, name);
        opened++;
        if (name === "early") {
            assert.equal(earlyCompletion, undefined);
            earlyCompletion = () => callback(null, fd);
            bits |= 1;
        } else {
            callback(null, fd);
        }
    });
};
global.fs.close = (fd, callback) => {
    const name = active.get(fd);
    assert.ok(name, "close must have one tracked resource owner");
    const complete = () => fs.close(fd, (error) => {
        if (!error) {
            assert.equal(active.delete(fd), true, "descriptor was closed twice");
            closed++;
        }
        callback(error);
    });
    if (name === "early" || name === "late") {
        const bit = name === "early" ? 4 : 8;
        assert.equal(bits & bit, 0, "cancellation submitted close more than once");
        bits |= bit;
        pendingCloses.push(complete);
    } else {
        complete();
    }
};
require(path.resolve(process.argv[2]));

(async () => {
    const go = new Go();
    const { instance } = await WebAssembly.instantiate(fs.readFileSync(process.argv[3]), go.importObject);
    assert.equal(await go.run(instance), 0);
    assert.equal(bits, 31);
    assert.equal(progress, 8);
    assert.equal(earlyCompletion, null);
    assert.equal(pendingCloses.length, 0);
    assert.equal(opened, 6);
    assert.equal(closed, opened);
    assert.equal(active.size, 0, "process exit must not hide a leaked descriptor");
    console.log("Node borrowed fd cancellation and asynchronous cleanup ok");
})().catch((error) => {
    console.error(error);
    process.exitCode = 1;
}).finally(() => {
    // Test failure cleanup is not acceptance evidence; assertions run first.
    for (const fd of active.keys()) {
        try { fs.closeSync(fd); } catch (_) { /* Keep the original failure. */ }
    }
    fs.rmSync(directory, { recursive: true, force: true });
});
