// Drives the host-pull scheduler plus the generic file-operation ABI.
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
  "__llgo_coro_host_next_operation_v1",
  "__llgo_coro_host_complete_operation_v1",
  "__llgo_coro_host_continue_slice_v1",
  "__llgo_coro_host_ack_cancel_v1",
]) {
  if (!(name in api)) {
    throw new Error(`missing export ${name}`);
  }
}

const schedulePtr = api.malloc(32) >>> 0;
const resultPtr = api.malloc(32) >>> 0;
const operationPtr = api.malloc(96) >>> 0;
if (schedulePtr === 0 || resultPtr === 0 || operationPtr === 0) {
  throw new Error("host ABI scratch allocation failed");
}

const readWords = (ptr, count) => {
  const view = new DataView(api.memory.buffer, ptr, count * 4);
  return Array.from({ length: count }, (_, index) =>
    view.getUint32(index * 4, true),
  );
};
const join = (lo, hi) => (BigInt(hi) << 32n) | BigInt(lo);
const split = (value) => [
  Number(value & 0xffff_ffffn),
  Number((value >> 32n) & 0xffff_ffffn),
];
const decoder = new TextDecoder();
const files = new Map();
const descriptors = new Map();
let nextFD = 3;
let operations = 0;
const opcodes = new Set();

const complete = (slot, generation, r1, r2 = 0n, errno = 0n) => {
  const [r1Lo, r1Hi] = split(r1);
  const [r2Lo, r2Hi] = split(r2);
  const [errnoLo, errnoHi] = split(errno);
  const posted = api.__llgo_coro_host_complete_operation_v1(
    slot,
    generation,
    0,
    3,
    r1Lo,
    r1Hi,
    r2Lo,
    r2Hi,
    errnoLo,
    errnoHi,
  ) >>> 0;
  if (posted !== 1) {
    throw new Error(`host completion was not posted: ${posted}`);
  }
};

const readGuestString = (pointer, length) =>
  decoder.decode(new Uint8Array(api.memory.buffer, pointer, length));

const serviceOperation = (record) => {
  const [kind, slot, generation, opcode, argc, reserved, ...words] = record;
  if (reserved !== 0 || kind !== 1 || argc > 9) {
    throw new Error(`invalid host operation record: ${record.join(",")}`);
  }
  const args = Array.from({ length: argc }, (_, index) =>
    join(words[index * 2], words[index * 2 + 1]),
  );
  const arg = (index) => Number(args[index]);
  operations++;
  opcodes.add(opcode);

  switch (opcode) {
    case 0x00010001: { // open(path, len, mode, perm)
      const path = readGuestString(arg(0), arg(1));
      files.set(path, new Uint8Array());
      const fd = nextFD++;
      descriptors.set(fd, { path, offset: 0 });
      complete(slot, generation, BigInt(fd));
      return;
    }
    case 0x00010002: { // read(fd, pointer, len)
      const descriptor = descriptors.get(arg(0));
      if (!descriptor) {
        complete(slot, generation, 0xffff_ffffn, 0n, 9n);
        return;
      }
      const file = files.get(descriptor.path) ?? new Uint8Array();
      const count = Math.min(arg(2), Math.max(0, file.length - descriptor.offset));
      new Uint8Array(api.memory.buffer, arg(1), count).set(
        file.subarray(descriptor.offset, descriptor.offset + count),
      );
      descriptor.offset += count;
      complete(slot, generation, BigInt(count));
      return;
    }
    case 0x00010003: { // write(fd, pointer, len)
      const descriptor = descriptors.get(arg(0));
      if (!descriptor) {
        complete(slot, generation, 0xffff_ffffn, 0n, 9n);
        return;
      }
      const input = new Uint8Array(api.memory.buffer, arg(1), arg(2));
      const previous = files.get(descriptor.path) ?? new Uint8Array();
      const required = descriptor.offset + input.length;
      const output = new Uint8Array(Math.max(previous.length, required));
      output.set(previous);
      output.set(input, descriptor.offset);
      files.set(descriptor.path, output);
      descriptor.offset += input.length;
      complete(slot, generation, BigInt(input.length));
      return;
    }
    case 0x00010004: { // close(fd)
      if (!descriptors.delete(arg(0))) {
        complete(slot, generation, 0xffff_ffffn, 0n, 9n);
        return;
      }
      complete(slot, generation, 0n);
      return;
    }
    case 0x00010005: { // seek(fd, offset-lo, offset-hi, whence)
      const descriptor = descriptors.get(arg(0));
      if (!descriptor) {
        complete(slot, generation, 0xffff_ffffn, 0n, 9n);
        return;
      }
      let offset = Number(join(arg(1), arg(2)));
      if (arg(3) === 1) {
        offset += descriptor.offset;
      } else if (arg(3) === 2) {
        offset += (files.get(descriptor.path) ?? new Uint8Array()).length;
      }
      if (offset < 0) {
        complete(slot, generation, 0xffff_ffffn, 0n, 22n);
        return;
      }
      descriptor.offset = offset;
      const [lo, hi] = split(BigInt(offset));
      complete(slot, generation, BigInt(lo), BigInt(hi));
      return;
    }
    case 0x00010006: { // unlink(path, len)
      const path = readGuestString(arg(0), arg(1));
      if (!files.delete(path)) {
        complete(slot, generation, 0xffff_ffffn, 0n, 2n);
        return;
      }
      complete(slot, generation, 0n);
      return;
    }
    default:
      throw new Error(`unsupported host operation opcode 0x${opcode.toString(16)}`);
  }
};

let status = 2;
let scheduleActions = 0;
const stdlibFileStage = () =>
  "__llgo_coro_stdlib_file_stage_v1" in api
    ? api.__llgo_coro_stdlib_file_stage_v1() >>> 0
    : 0;
try {
  if (api.main(0, 0) !== 0) {
    throw new Error("module entry failed");
  }
  while (status !== 1) {
    if (operations + scheduleActions > 4096) {
      throw new Error("host file action loop did not complete");
    }
    for (;;) {
      const kind = api.__llgo_coro_host_next_operation_v1(operationPtr) >>> 0;
      const record = readWords(operationPtr, 24);
      if (kind !== record[0]) {
        throw new Error(`operation kind mismatch: ${kind} != ${record[0]}`);
      }
      if (kind === 0) {
        break;
      }
      if (kind === 2) {
        throw new Error("unexpected cancellation in host file probe");
      }
      serviceOperation(record);
    }

    const kind = api.__llgo_coro_host_next_action_v1(schedulePtr) >>> 0;
    const action = readWords(schedulePtr, 8);
    if (kind !== action[0]) {
      throw new Error(`schedule kind mismatch: ${kind} != ${action[0]}`);
    }
    if (kind === 3 || kind === 4) {
      if (!api.__llgo_coro_host_ack_cancel_v1(action[1], action[2], action[3], kind)) {
        throw new Error("schedule cancellation acknowledgement failed");
      }
      continue;
    }
    if (kind !== 1 && kind !== 2) {
      throw new Error(`no scheduler action while status=${status}`);
    }
    scheduleActions++;
    status = api.__llgo_coro_host_continue_slice_v1(
      action[1],
      action[2],
      action[3],
      kind === 1 ? 1 : 2,
      0,
      0,
      1024,
      resultPtr,
    ) >>> 0;
    const result = readWords(resultPtr, 8);
    if (status === 0 || status === 4 || status === 5 || status > 6 || result[7] !== 0) {
      throw new Error(
        `invalid scheduler result ${status}: ${result.join(",")}; ` +
          `operations=${operations}, opcodes=${[...opcodes].join(",")}, ` +
          `files=${files.size}, fds=${descriptors.size}, scheduleActions=${scheduleActions}, ` +
          `stdlibFileStage=${stdlibFileStage()}`,
      );
    }
  }
  const completeFileLifecycle =
    (operations === 6 && opcodes.size === 6 && files.size === 0) ||
    (operations === 5 && opcodes.size === 5 && files.size === 1);
  if (!completeFileLifecycle || descriptors.size !== 0) {
    throw new Error(
      `incomplete file lifecycle: operations=${operations}, opcodes=${opcodes.size}, files=${files.size}, fds=${descriptors.size}`,
    );
  }
  console.log(JSON.stringify({ status: "complete", operations, scheduleActions }));
} finally {
  api.free(operationPtr);
  api.free(resultPtr);
  api.free(schedulePtr);
}
