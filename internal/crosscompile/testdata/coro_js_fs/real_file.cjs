// Exercise the real Node fs implementation and the production LLGo runner.
// Track physical descriptors before process exit can hide an ordinary-path leak.
"use strict";
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");

const active = new Set();
let opened = 0;
let closed = 0;
global.fs = Object.create(fs);
global.fs.open = (...args) => {
    const callback = args.pop();
    fs.open(...args, (error, fd) => {
        if (!error) {
            assert.equal(active.has(fd), false, "open reused a still-owned descriptor");
            active.add(fd);
            opened++;
        }
        callback(error, fd);
    });
};
global.fs.close = (fd, callback) => {
    fs.close(fd, (error) => {
        if (!error) {
            assert.equal(active.delete(fd), true, "close had no tracked open");
            closed++;
        }
        callback(error);
    });
};
require(path.resolve(process.argv[2]));

(async () => {
    const go = new Go();
    const { instance } = await WebAssembly.instantiate(fs.readFileSync(process.argv[3]), go.importObject);
    assert.equal(await go.run(instance), 0);
    assert.ok(opened >= 2, "fixture must exercise both ordinary and directory opens");
    assert.equal(active.size, 0, "Go completed with physically open descriptors");
    assert.equal(closed, opened);
    console.log(`Node physical filesystem cleanup ok (${opened} opens/${closed} closes)`);
})().catch((error) => {
    console.error(error);
    for (const fd of active) {
        try { fs.closeSync(fd); } catch (_) { /* Preserve the primary failure. */ }
    }
    process.exitCode = 1;
});
