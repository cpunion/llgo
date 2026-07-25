// Drive a freestanding LLGo host-pull module without WASI Preview 1 imports.
// Usage: node host_runner.mjs path/to/module.wasm

import { readFile } from "node:fs/promises";

const modulePath = process.argv[2];
if (!modulePath) {
  throw new Error("usage: node host_runner.mjs path/to/module.wasm");
}

const bytes = await readFile(modulePath);
const { instance } = await WebAssembly.instantiate(bytes, {});
const api = instance.exports;

for (const name of [
  "memory",
  "malloc",
  "free",
  "main",
  "__llgo_coro_host_next_action_v1",
  "__llgo_coro_host_profile_v1",
  "__llgo_coro_host_continue_slice_v1",
  "__llgo_coro_host_ack_cancel_v1",
]) {
  if (!(name in api)) {
    throw new Error(`missing export ${name}`);
  }
}

const actionPtr = api.malloc(32) >>> 0;
const resultPtr = api.malloc(32) >>> 0;
if (actionPtr === 0 || resultPtr === 0) {
  throw new Error("host ABI scratch allocation failed");
}

const readWords = (ptr) => {
  const view = new DataView(api.memory.buffer, ptr, 32);
  return Array.from({ length: 8 }, (_, index) =>
    view.getUint32(index * 4, true),
  );
};
const joinWords = (lo, hi) => (BigInt(hi) << 32n) | BigInt(lo);
const splitWord = (word) => [
  Number(word & 0xffff_ffffn),
  Number((word >> 32n) & 0xffff_ffffn),
];

const profile = api.__llgo_coro_host_profile_v1() >>> 0;
if ((profile & 0xff) === 0 || (profile & 0x300) !== 0x300) {
  throw new Error(`invalid host schedule/alarm profile 0x${profile.toString(16)}`);
}

let now = 0n;
let status = 2;
let actions = 0;
let alarms = 0;

try {
  const entryStatus = api.main(0, 0);
  if (entryStatus !== 0) {
    throw new Error(`main returned ${entryStatus}`);
  }

  while (status !== 1) {
    if (++actions > 4096) {
      throw new Error("host action loop did not complete");
    }
    const kind = api.__llgo_coro_host_next_action_v1(actionPtr) >>> 0;
    const action = readWords(actionPtr);
    if (kind !== action[0]) {
      throw new Error(`action kind mismatch: return=${kind}, record=${action[0]}`);
    }

    const [, slot, generation, epoch, deadlineLo, deadlineHi, reserved0, reserved1] =
      action;
    if (reserved0 !== 0 || reserved1 !== 0) {
      throw new Error(`nonzero action reserved words: ${reserved0}, ${reserved1}`);
    }

    if (kind === 3 || kind === 4) {
      if (!api.__llgo_coro_host_ack_cancel_v1(slot, generation, epoch, kind)) {
        throw new Error(`cancel acknowledgement failed for kind ${kind}`);
      }
      continue;
    }
    if (kind !== 1 && kind !== 2) {
      throw new Error(`no runnable host action while status=${status}`);
    }

    let cause = 1;
    if (kind === 2) {
      cause = 2;
      const deadline = joinWords(deadlineLo, deadlineHi);
      if (deadline < now) {
        throw new Error(`alarm deadline regressed: ${deadline} < ${now}`);
      }
      now = deadline;
      alarms++;
    }
    const [nowLo, nowHi] = splitWord(now);
    status =
      api.__llgo_coro_host_continue_slice_v1(
        slot,
        generation,
        epoch,
        cause,
        nowLo,
        nowHi,
        1024,
        resultPtr,
      ) >>> 0;
    const result = readWords(resultPtr);
    if (result[7] !== 0) {
      throw new Error(`nonzero run-result reserved word: ${result[7]}`);
    }
    if (status === 0 || status === 4 || status === 5 || status > 6) {
      throw new Error(`invalid terminal drive status ${status}: ${result.join(",")}`);
    }
  }

  if (alarms === 0 || now < 200_000_000n) {
    throw new Error(`time.Sleep completed without its 200ms alarm: alarms=${alarms}, now=${now}`);
  }
  console.log(
    JSON.stringify({
      status: "complete",
      profile: `0x${profile.toString(16)}`,
      actions,
      alarms,
      monotonicNanoseconds: now.toString(),
    }),
  );
} finally {
  api.free(resultPtr);
  api.free(actionPtr);
}
