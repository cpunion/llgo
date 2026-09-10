import assert from "node:assert/strict";

const output = [];
const errors = [];
const hostConsole = globalThis.console;
globalThis.console = {
  ...hostConsole,
  log(value) { output.push(String(value)); },
  error(value) { errors.push(String(value)); },
};
globalThis.Module = { FS: {} };

await import("../targets/wasm_fs.js");

const encoder = new TextEncoder();
assert.equal(globalThis.fs.writeSync(1, encoder.encode("stdout")), 6);
assert.deepEqual(output, []);
globalThis.fs.writeSync(2, encoder.encode("stderr\n"));
assert.deepEqual(errors, ["stderr"]);
assert.deepEqual(output, []);
globalThis.fs.writeSync(1, encoder.encode("\n"));
assert.deepEqual(output, ["stdout"]);

const longLine = "x".repeat(64 << 10);
globalThis.fs.writeSync(1, encoder.encode(longLine));
assert.equal(output.length, 2);
assert.equal(output[1], longLine);

hostConsole.log("wasm_fs host shim ok");
