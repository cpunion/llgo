// Drives the host-pull scheduler plus a target-neutral virtual TCP transport.
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
const operationKey = (slot, generation) => `${slot}:${generation}`;

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
const fail = (operation, errno) =>
  complete(operation.slot, operation.generation, 0xffff_ffffn, 0n, BigInt(errno));

const readAddress = (pointer, size) => {
  if (size !== 32) {
    throw new Error(`invalid host sockaddr size ${size}`);
  }
  const view = new DataView(api.memory.buffer, pointer, size);
  const version = view.getUint32(0, true);
  const family = view.getUint32(4, true);
  const port = view.getUint32(8, true);
  const zone = view.getUint32(12, true);
  const length = family === 1 ? 4 : family === 2 ? 16 : 0;
  if (version !== 1 || length === 0 || port > 65535) {
    throw new Error(`invalid host sockaddr version/family/port ${version}/${family}/${port}`);
  }
  const address = Array.from(new Uint8Array(api.memory.buffer, pointer + 16, length));
  return { version, family, port, zone, address };
};

const writeAddress = (pointer, size, address) => {
  if (size !== 32 || !address || (address.family !== 1 && address.family !== 2)) {
    throw new Error("invalid output host sockaddr");
  }
  const view = new DataView(api.memory.buffer, pointer, size);
  view.setUint32(0, 1, true);
  view.setUint32(4, address.family, true);
  view.setUint32(8, address.port, true);
  view.setUint32(12, address.zone, true);
  const bytes = new Uint8Array(api.memory.buffer, pointer + 16, 16);
  bytes.fill(0);
  bytes.set(address.address);
};

const addressKey = (address) =>
  `${address.family}/${address.address.join(".")}/${address.port}/${address.zone}`;
const loopbackPeer = {
  version: 1,
  family: 1,
  port: 49000,
  zone: 0,
  address: [127, 0, 0, 1],
};

let nextFD = 64;
let operations = 0;
let cancellations = 0;
let scheduleActions = 0;
const opcodes = new Set();
const logicalOperations = new Set();
const descriptors = new Map();
const listeners = new Map();
const pending = new Map();

const newSocket = (family, kind, protocol) => {
  const fd = nextFD++;
  descriptors.set(fd, {
    fd,
    family,
    kind,
    protocol,
    state: "created",
    local: null,
    peerAddress: null,
    peer: null,
    queued: [],
    reads: [],
    accepts: [],
    connections: [],
  });
  return fd;
};

const removePending = (operation) => {
  pending.delete(operationKey(operation.slot, operation.generation));
};

const finishRead = (operation, socket, bytes) => {
  const count = Math.min(operation.length, bytes.length);
  new Uint8Array(api.memory.buffer, operation.pointer, count).set(bytes.subarray(0, count));
  if (count < bytes.length) {
    socket.queued.unshift(bytes.subarray(count));
  }
  removePending(operation);
  complete(operation.slot, operation.generation, BigInt(count));
};

const finishAccept = (operation, listener, connectionFD) => {
  const connection = descriptors.get(connectionFD);
  if (!connection) {
    throw new Error("accepted connection disappeared");
  }
  writeAddress(operation.pointer, operation.length, connection.peerAddress);
  removePending(operation);
  complete(operation.slot, operation.generation, BigInt(connectionFD));
};

const serviceOperation = (record) => {
  const [kind, slot, generation, opcode, argc, reserved, ...words] = record;
  if (reserved !== 0 || kind !== 1 || argc > 9) {
    throw new Error(`invalid host operation record: ${record.join(",")}`);
  }
  const args = Array.from({ length: argc }, (_, index) =>
    join(words[index * 2], words[index * 2 + 1]),
  );
  const arg = (index) => Number(args[index]);
  const operation = { slot, generation, opcode };
  operations++;
  opcodes.add(opcode);
  logicalOperations.add(operationKey(slot, generation));

  switch (opcode) {
    case 0x00020001: { // socket(family, kind, protocol)
      if (arg(0) !== 1 || arg(1) !== 1 || (arg(2) !== 0 && arg(2) !== 1)) {
        fail(operation, 97);
        return;
      }
      complete(slot, generation, BigInt(newSocket(arg(0), arg(1), arg(2))));
      return;
    }
    case 0x00020002: { // bind(fd, sockaddr*, size)
      const socket = descriptors.get(arg(0));
      if (!socket || socket.state !== "created") {
        fail(operation, 9);
        return;
      }
      socket.local = readAddress(arg(1), arg(2));
      socket.state = "bound";
      complete(slot, generation, 0n);
      return;
    }
    case 0x00020003: { // listen(fd, backlog)
      const socket = descriptors.get(arg(0));
      if (!socket || socket.state !== "bound" || arg(1) <= 0) {
        fail(operation, 22);
        return;
      }
      const key = addressKey(socket.local);
      if (listeners.has(key)) {
        fail(operation, 98);
        return;
      }
      socket.state = "listening";
      socket.backlog = arg(1);
      listeners.set(key, socket);
      complete(slot, generation, 0n);
      return;
    }
    case 0x00020004: { // accept(fd, sockaddr*, size)
      const listener = descriptors.get(arg(0));
      if (!listener || listener.state !== "listening") {
        fail(operation, 22);
        return;
      }
      operation.pointer = arg(1);
      operation.length = arg(2);
      if (listener.connections.length !== 0) {
        finishAccept(operation, listener, listener.connections.shift());
        return;
      }
      listener.accepts.push(operation);
      pending.set(operationKey(slot, generation), {
        operation,
        cancel: () => {
          const index = listener.accepts.indexOf(operation);
          if (index >= 0) {
            listener.accepts.splice(index, 1);
          }
        },
      });
      return;
    }
    case 0x00020005: { // connect(fd, sockaddr*, size)
      const client = descriptors.get(arg(0));
      const remote = readAddress(arg(1), arg(2));
      const listener = listeners.get(addressKey(remote));
      if (!client || client.state !== "created" || !listener) {
        fail(operation, 111);
        return;
      }
      const serverFD = newSocket(client.family, client.kind, client.protocol);
      const server = descriptors.get(serverFD);
      client.state = "connected";
      client.local = loopbackPeer;
      client.peerAddress = remote;
      client.peer = serverFD;
      server.state = "connected";
      server.local = remote;
      server.peerAddress = loopbackPeer;
      server.peer = client.fd;
      if (listener.accepts.length !== 0) {
        finishAccept(listener.accepts.shift(), listener, serverFD);
      } else {
        listener.connections.push(serverFD);
      }
      complete(slot, generation, 0n);
      return;
    }
    case 0x00010003: { // write(fd, pointer, len)
      const socket = descriptors.get(arg(0));
      const peer = socket && descriptors.get(socket.peer);
      if (!socket || socket.state !== "connected" || !peer) {
        fail(operation, 107);
        return;
      }
      const bytes = new Uint8Array(arg(2));
      bytes.set(new Uint8Array(api.memory.buffer, arg(1), arg(2)));
      if (peer.reads.length !== 0) {
        finishRead(peer.reads.shift(), peer, bytes);
      } else {
        peer.queued.push(bytes);
      }
      complete(slot, generation, BigInt(bytes.length));
      return;
    }
    case 0x00010002: { // read(fd, pointer, len)
      const socket = descriptors.get(arg(0));
      if (!socket || socket.state !== "connected") {
        fail(operation, 107);
        return;
      }
      operation.pointer = arg(1);
      operation.length = arg(2);
      if (socket.queued.length !== 0) {
        finishRead(operation, socket, socket.queued.shift());
        return;
      }
      socket.reads.push(operation);
      pending.set(operationKey(slot, generation), {
        operation,
        cancel: () => {
          const index = socket.reads.indexOf(operation);
          if (index >= 0) {
            socket.reads.splice(index, 1);
          }
        },
      });
      return;
    }
    case 0x00010004: { // close(fd)
      const socket = descriptors.get(arg(0));
      if (!socket) {
        fail(operation, 9);
        return;
      }
      if (socket.state === "listening" && socket.local) {
        listeners.delete(addressKey(socket.local));
      }
      if (socket.peer !== null) {
        const peer = descriptors.get(socket.peer);
        if (peer) {
          peer.peer = null;
          for (const read of peer.reads.splice(0)) {
            removePending(read);
            complete(read.slot, read.generation, 0n);
          }
        }
      }
      descriptors.delete(socket.fd);
      complete(slot, generation, 0n);
      return;
    }
    case 0x7fff0001: { // deliberately pending cancellation probe
      if (argc !== 1 || args[0] !== 0x4c4c474fn) {
        fail(operation, 22);
        return;
      }
      pending.set(operationKey(slot, generation), {
        operation,
        cancel: () => {},
      });
      return;
    }
    case 0x7fff0003: { // stale control-epoch reconciliation probe
      if (argc !== 1 || args[0] !== 0x4c4c474fn) {
        fail(operation, 22);
        return;
      }
      pending.set(operationKey(slot, generation), {
        operation,
        cancel: () => {},
      });
      return;
    }
    case 0x7fff0002: { // release main after the pending probe is observable
      if (argc !== 1 || args[0] !== 0x4c4c474fn) {
        fail(operation, 22);
        return;
      }
      complete(slot, generation, 0x4c4c474fn);
      return;
    }
    default:
      throw new Error(`unsupported host operation opcode 0x${opcode.toString(16)}`);
  }
};

const serviceCancellation = (record) => {
  const [kind, slot, generation] = record;
  if (kind !== 2) {
    throw new Error(`invalid cancellation action ${record.join(",")}`);
  }
  const key = operationKey(slot, generation);
  const retained = pending.get(key);
  if (!retained) {
    throw new Error(`cancellation has no pending operation ${key}`);
  }
  logicalOperations.add(key);
  opcodes.add(retained.operation.opcode);
  retained.cancel();
  pending.delete(key);
  cancellations++;
  fail(retained.operation, 125);
};

let status = 2;
try {
  if (api.main(0, 0) !== 0) {
    throw new Error("module entry failed");
  }
  while (status !== 1) {
    if (operations + cancellations + scheduleActions > 8192) {
      throw new Error("host network action loop did not complete");
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
      if (kind === 1) {
        serviceOperation(record);
      } else if (kind === 2) {
        serviceCancellation(record);
      } else {
        throw new Error(`unknown host operation action ${kind}`);
      }
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
      throw new Error(`no scheduler action while status=${status}, pending=${pending.size}`);
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
      throw new Error(`invalid scheduler result ${status}: ${result.join(",")}`);
    }
  }
  if (
    operations !== 16 ||
    cancellations !== 2 ||
    logicalOperations.size !== 16 ||
    opcodes.size !== 11 ||
    pending.size !== 0 ||
    listeners.size !== 0 ||
    descriptors.size !== 0
  ) {
    throw new Error(
      `incomplete network lifecycle: operations=${operations}, cancellations=${cancellations}, logical=${logicalOperations.size}, opcodes=${opcodes.size}, pending=${pending.size}, listeners=${listeners.size}, fds=${descriptors.size}`,
    );
  }
  console.log(JSON.stringify({
    status: "complete",
    operations,
    cancellations,
    scheduleActions,
  }));
} finally {
  api.free(operationPtr);
  api.free(resultPtr);
  api.free(schedulePtr);
}
