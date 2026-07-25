// Drives the standard-library TCP/deadline fixture on the target-neutral
// host-pull ABI. Usage: node host_runner.mjs path/to/module.wasm

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

const readWords = (pointer, count) => {
  const view = new DataView(api.memory.buffer, pointer, count * 4);
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
const textDecoder = new TextDecoder();
const textEncoder = new TextEncoder();
const traceHostOperations = process.env.LLGO_CORO_HOST_TRACE === "1";

const readGuestString = (pointer, length) =>
  textDecoder.decode(new Uint8Array(api.memory.buffer, pointer, length));

const complete = (operation, r1, r2 = 0n, errno = 0n) => {
  const [r1Lo, r1Hi] = split(r1);
  const [r2Lo, r2Hi] = split(r2);
  const [errnoLo, errnoHi] = split(errno);
  const posted = api.__llgo_coro_host_complete_operation_v1(
    operation.slot,
    operation.generation,
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
  complete(operation, 0xffff_ffffn, 0n, BigInt(errno));

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
    throw new Error(`invalid sockaddr ${version}/${family}/${port}`);
  }
  return {
    version,
    family,
    port,
    zone,
    address: Array.from(
      new Uint8Array(api.memory.buffer, pointer + 16, length),
    ),
  };
};

const writeAddress = (pointer, size, address) => {
  if (size !== 32 || !address) {
    throw new Error("invalid output sockaddr");
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

const writeRecvMsgResult = (pointer, size, address, sysflags) => {
  if (size !== 36) {
    throw new Error(`invalid recvmsg result size ${size}`);
  }
  writeAddress(pointer, 32, address);
  new DataView(api.memory.buffer, pointer + 32, 4).setUint32(
    0,
    sysflags,
    true,
  );
};

const addressKey = (address) =>
  `${address.family}/${address.address.join(".")}/${address.port}/${address.zone}`;
const loopback = (port) => ({
  version: 1,
  family: 1,
  port,
  zone: 0,
  address: [127, 0, 0, 1],
});

const hostFileContents = new Map([
  ["/etc/hosts", "127.0.0.1 localhost\n::1 localhost\n"],
  ["/etc/nsswitch.conf", "hosts: files dns\n"],
  [
    "/etc/resolv.conf",
    "nameserver 127.0.0.1\noptions timeout:1 attempts:1\n",
  ],
]);

let nextFD = 64;
let nextPort = 43000;
let now = 0n;
let operationCount = 0;
let cancellationCount = 0;
let scheduleCount = 0;
let alarmCount = 0;
const dnsQueryTypes = new Set();
const descriptors = new Map();
const listeners = new Map();
const datagrams = new Map();
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
    writes: [],
    connects: [],
    packets: [],
    recvfroms: [],
    recvmsgs: [],
    accepts: [],
    connections: [],
  });
  return fd;
};

const newHostFile = (path, contents) => {
  const fd = nextFD++;
  descriptors.set(fd, {
    fd,
    state: "file",
    path,
    contents: textEncoder.encode(contents),
    offset: 0,
  });
  return fd;
};

const dnsResponse = (query) => {
  if (query.length < 17) {
    throw new Error("short DNS query");
  }
  let cursor = 12;
  for (;;) {
    if (cursor >= query.length) {
      throw new Error("unterminated DNS question");
    }
    const length = query[cursor++];
    if ((length & 0xc0) !== 0 || cursor + length > query.length) {
      throw new Error("invalid DNS question name");
    }
    if (length === 0) {
      break;
    }
    cursor += length;
  }
  if (cursor + 4 > query.length) {
    throw new Error("short DNS question footer");
  }
  const queryView = new DataView(
    query.buffer,
    query.byteOffset,
    query.byteLength,
  );
  const queryType = queryView.getUint16(cursor, false);
  dnsQueryTypes.add(queryType);
  const questionEnd = cursor + 4;
  let rdata;
  if (queryType === 1) {
    rdata = Uint8Array.of(192, 0, 2, 42);
  } else if (queryType === 28) {
    rdata = Uint8Array.of(
      0x20,
      0x01,
      0x0d,
      0xb8,
      0,
      0,
      0,
      0,
      0,
      0,
      0,
      0,
      0,
      0,
      0,
      0x42,
    );
  }
  const answerLength = rdata ? 12 + rdata.length : 0;
  const response = new Uint8Array(questionEnd + answerLength);
  response.set(query.subarray(0, questionEnd));
  const view = new DataView(response.buffer);
  view.setUint16(2, 0x8180, false);
  view.setUint16(4, 1, false);
  view.setUint16(6, rdata ? 1 : 0, false);
  view.setUint16(8, 0, false);
  view.setUint16(10, 0, false);
  if (rdata) {
    let answer = questionEnd;
    view.setUint16(answer, 0xc00c, false);
    answer += 2;
    view.setUint16(answer, queryType, false);
    answer += 2;
    view.setUint16(answer, 1, false);
    answer += 2;
    view.setUint32(answer, 60, false);
    answer += 4;
    view.setUint16(answer, rdata.length, false);
    answer += 2;
    response.set(rdata, answer);
  }
  return response;
};

const forgetPending = (operation) => {
  pending.delete(operationKey(operation.slot, operation.generation));
};

const finishRead = (operation, socket, bytes) => {
  const count = Math.min(operation.length, bytes.length);
  new Uint8Array(api.memory.buffer, operation.pointer, count).set(
    bytes.subarray(0, count),
  );
  if (count < bytes.length) {
    socket.queued.unshift(bytes.subarray(count));
  }
  forgetPending(operation);
  complete(operation, BigInt(count));
};

const finishAccept = (operation, connectionFD) => {
  const connection = descriptors.get(connectionFD);
  if (!connection) {
    throw new Error("accepted connection disappeared");
  }
  writeAddress(operation.pointer, operation.length, connection.peerAddress);
  forgetPending(operation);
  complete(operation, BigInt(connectionFD));
};

const finishRecvFrom = (operation, packet) => {
  const count = Math.min(operation.length, packet.bytes.length);
  new Uint8Array(api.memory.buffer, operation.pointer, count).set(
    packet.bytes.subarray(0, count),
  );
  writeAddress(operation.addressPointer, operation.addressLength, packet.from);
  forgetPending(operation);
  complete(operation, BigInt(count));
};

const finishRecvMsg = (operation, packet) => {
  const count = Math.min(operation.length, packet.bytes.length);
  const oob = packet.oob ?? new Uint8Array(0);
  const oobCount = Math.min(operation.oobLength, oob.length);
  new Uint8Array(api.memory.buffer, operation.pointer, count).set(
    packet.bytes.subarray(0, count),
  );
  new Uint8Array(api.memory.buffer, operation.oobPointer, oobCount).set(
    oob.subarray(0, oobCount),
  );
  writeRecvMsgResult(
    operation.resultPointer,
    operation.resultLength,
    packet.from,
    packet.sysflags ?? 0,
  );
  forgetPending(operation);
  complete(operation, BigInt(count), BigInt(oobCount));
};

const retain = (operation, list) => {
  list.push(operation);
  pending.set(operationKey(operation.slot, operation.generation), {
    operation,
    cancel: () => {
      const index = list.indexOf(operation);
      if (index >= 0) {
        list.splice(index, 1);
      }
    },
  });
};

const serviceOperation = (record) => {
  const [kind, slot, generation, opcode, argc, reserved, ...words] = record;
  if (kind !== 1 || reserved !== 0 || argc > 9) {
    throw new Error(`invalid host operation record: ${record.join(",")}`);
  }
  const args = Array.from({ length: argc }, (_, index) =>
    join(words[index * 2], words[index * 2 + 1]),
  );
  const arg = (index) => Number(args[index]);
  const operation = { slot, generation, opcode };
  operationCount++;
  if (traceHostOperations) {
    console.error(
      `host-op #${operationCount} id=${slot}:${generation} opcode=0x${opcode.toString(16)} args=${args.join(",")}`,
    );
  }

  switch (opcode) {
    case 0x00010001: // open
      {
        const path = readGuestString(arg(0), arg(1));
        const contents = hostFileContents.get(path);
        if (contents === undefined) {
          fail(operation, 2);
          return;
        }
        complete(operation, BigInt(newHostFile(path, contents)));
      }
      return;
    case 0x00010005: // seek
    case 0x00010006: // unlink
      fail(operation, 2);
      return;
    case 0x00020001: { // socket
      if (arg(0) !== 1 || (arg(1) !== 1 && arg(1) !== 2)) {
        fail(operation, 97);
        return;
      }
      complete(operation, BigInt(newSocket(arg(0), arg(1), arg(2))));
      return;
    }
    case 0x00020002: { // bind
      const socket = descriptors.get(arg(0));
      if (!socket || socket.state !== "created") {
        fail(operation, 9);
        return;
      }
      socket.local = readAddress(arg(1), arg(2));
      if (socket.local.port === 0) {
        socket.local.port = nextPort++;
      }
      if (socket.kind === 2) {
        const key = addressKey(socket.local);
        if (datagrams.has(key)) {
          fail(operation, 98);
          return;
        }
        socket.state = "datagram";
        datagrams.set(key, socket);
        complete(operation, 0n);
        return;
      }
      socket.state = "bound";
      complete(operation, 0n);
      return;
    }
    case 0x00020003: { // listen
      const socket = descriptors.get(arg(0));
      if (!socket || socket.state !== "bound") {
        fail(operation, 22);
        return;
      }
      const key = addressKey(socket.local);
      if (listeners.has(key)) {
        fail(operation, 98);
        return;
      }
      socket.state = "listening";
      listeners.set(key, socket);
      complete(operation, 0n);
      return;
    }
    case 0x00020004: { // accept
      const listener = descriptors.get(arg(0));
      if (!listener || listener.state !== "listening") {
        fail(operation, 22);
        return;
      }
      operation.pointer = arg(1);
      operation.length = arg(2);
      if (listener.connections.length !== 0) {
        finishAccept(operation, listener.connections.shift());
      } else {
        retain(operation, listener.accepts);
      }
      return;
    }
    case 0x00020005: { // connect
      const client = descriptors.get(arg(0));
      const remote = readAddress(arg(1), arg(2));
      const listener = listeners.get(addressKey(remote));
      if (!client || client.state !== "created") {
        fail(operation, 111);
        return;
      }
      if (client.kind === 1 && remote.family === 1 && remote.port === 49999) {
        retain(operation, client.connects);
        return;
      }
      if (client.kind === 2) {
        client.state = "datagram";
        client.local = loopback(nextPort++);
        client.peerAddress = remote;
        datagrams.set(addressKey(client.local), client);
        complete(operation, 0n);
        return;
      }
      if (!listener) {
        fail(operation, 111);
        return;
      }
      const serverFD = newSocket(client.family, client.kind, client.protocol);
      const server = descriptors.get(serverFD);
      client.state = "connected";
      client.local = loopback(nextPort++);
      client.peerAddress = remote;
      client.peer = serverFD;
      server.state = "connected";
      server.local = remote;
      server.peerAddress = client.local;
      server.peer = client.fd;
      if (listener.accepts.length !== 0) {
        finishAccept(listener.accepts.shift(), serverFD);
      } else {
        listener.connections.push(serverFD);
      }
      complete(operation, 0n);
      return;
    }
    case 0x00020006: { // getsockname
      const socket = descriptors.get(arg(0));
      if (!socket || !socket.local) {
        fail(operation, 9);
        return;
      }
      writeAddress(arg(1), arg(2), socket.local);
      complete(operation, 0n);
      return;
    }
    case 0x00020007: { // getpeername
      const socket = descriptors.get(arg(0));
      if (!socket || !socket.peerAddress) {
        fail(operation, 107);
        return;
      }
      writeAddress(arg(1), arg(2), socket.peerAddress);
      complete(operation, 0n);
      return;
    }
    case 0x00020008: // setsockopt
      complete(operation, 0n);
      return;
    case 0x00020009: // getsockopt
      complete(operation, 0n);
      return;
    case 0x0002000a: // shutdown
      complete(operation, 0n);
      return;
    case 0x0002000b: { // recvfrom
      const socket = descriptors.get(arg(0));
      if (!socket || socket.state !== "datagram" || arg(3) !== 0) {
        fail(operation, 22);
        return;
      }
      operation.pointer = arg(1);
      operation.length = arg(2);
      operation.addressPointer = arg(4);
      operation.addressLength = arg(5);
      if (socket.packets.length !== 0) {
        finishRecvFrom(operation, socket.packets.shift());
      } else {
        retain(operation, socket.recvfroms);
      }
      return;
    }
    case 0x0002000c: { // sendto
      const socket = descriptors.get(arg(0));
      const remote = readAddress(arg(4), arg(5));
      const peer = datagrams.get(addressKey(remote));
      if (!socket || socket.state !== "datagram" || arg(3) !== 0 || !peer) {
        fail(operation, 22);
        return;
      }
      const bytes = new Uint8Array(arg(2));
      bytes.set(new Uint8Array(api.memory.buffer, arg(1), arg(2)));
      const packet = {
        bytes,
        oob: new Uint8Array(0),
        from: socket.local,
        sysflags: 0,
      };
      if (peer.recvmsgs.length !== 0) {
        finishRecvMsg(peer.recvmsgs.shift(), packet);
      } else if (peer.recvfroms.length !== 0) {
        finishRecvFrom(peer.recvfroms.shift(), packet);
      } else {
        peer.packets.push(packet);
      }
      complete(operation, BigInt(bytes.length));
      return;
    }
    case 0x0002000d: { // recvmsg
      const socket = descriptors.get(arg(0));
      if (!socket || socket.state !== "datagram") {
        fail(operation, 22);
        return;
      }
      operation.pointer = arg(1);
      operation.length = arg(2);
      operation.oobPointer = arg(3);
      operation.oobLength = arg(4);
      operation.flags = arg(5);
      operation.resultPointer = arg(6);
      operation.resultLength = arg(7);
      if (socket.packets.length !== 0) {
        finishRecvMsg(operation, socket.packets.shift());
      } else {
        retain(operation, socket.recvmsgs);
      }
      return;
    }
    case 0x0002000e: { // sendmsg
      const socket = descriptors.get(arg(0));
      const remote =
        arg(6) === 0 && arg(7) === 0
          ? socket?.peerAddress
          : readAddress(arg(6), arg(7));
      const peer = remote && datagrams.get(addressKey(remote));
      if (!socket || socket.state !== "datagram" || !peer) {
        fail(operation, 22);
        return;
      }
      const bytes = new Uint8Array(arg(2));
      bytes.set(new Uint8Array(api.memory.buffer, arg(1), arg(2)));
      const oob = new Uint8Array(arg(4));
      oob.set(new Uint8Array(api.memory.buffer, arg(3), arg(4)));
      const packet = { bytes, oob, from: socket.local, sysflags: 0 };
      if (peer.recvmsgs.length !== 0) {
        finishRecvMsg(peer.recvmsgs.shift(), packet);
      } else if (peer.recvfroms.length !== 0) {
        finishRecvFrom(peer.recvfroms.shift(), packet);
      } else {
        peer.packets.push(packet);
      }
      complete(operation, BigInt(bytes.length), BigInt(oob.length));
      return;
    }
    case 0x00010003: { // write
      const socket = descriptors.get(arg(0));
      if (socket?.state === "datagram" && socket.peerAddress) {
        const bytes = new Uint8Array(arg(2));
        bytes.set(new Uint8Array(api.memory.buffer, arg(1), arg(2)));
        if (
          socket.peerAddress.family === 1 &&
          socket.peerAddress.port === 53 &&
          socket.peerAddress.address.join(".") === "127.0.0.1"
        ) {
          const response = dnsResponse(bytes);
          if (socket.reads.length !== 0) {
            finishRead(socket.reads.shift(), socket, response);
          } else {
            socket.queued.push(response);
          }
          complete(operation, BigInt(bytes.length));
          return;
        }
        fail(operation, 111);
        return;
      }
      const peer = socket && descriptors.get(socket.peer);
      if (!socket || socket.state !== "connected" || !peer) {
        fail(operation, 107);
        return;
      }
      const bytes = new Uint8Array(arg(2));
      bytes.set(new Uint8Array(api.memory.buffer, arg(1), arg(2)));
      // The standard-library fixture uses one distinguished payload to model
      // transport backpressure. It remains physically pending until a
      // dynamically installed write deadline requests exact cancellation.
      if (bytes.length === 1 && (bytes[0] === 0x77 || bytes[0] === 0x7a)) {
        retain(operation, socket.writes);
        return;
      }
      if (peer.reads.length !== 0) {
        finishRead(peer.reads.shift(), peer, bytes);
      } else {
        peer.queued.push(bytes);
      }
      complete(operation, BigInt(bytes.length));
      return;
    }
    case 0x00010002: { // read
      const socket = descriptors.get(arg(0));
      if (socket?.state === "file") {
        const remaining = socket.contents.subarray(socket.offset);
        const count = Math.min(arg(2), remaining.length);
        new Uint8Array(api.memory.buffer, arg(1), count).set(
          remaining.subarray(0, count),
        );
        socket.offset += count;
        complete(operation, BigInt(count));
        return;
      }
      if (
        !socket ||
        (socket.state !== "connected" && socket.state !== "datagram")
      ) {
        fail(operation, 107);
        return;
      }
      operation.pointer = arg(1);
      operation.length = arg(2);
      if (socket.queued.length !== 0) {
        finishRead(operation, socket, socket.queued.shift());
      } else {
        retain(operation, socket.reads);
      }
      return;
    }
    case 0x00010004: { // close
      const socket = descriptors.get(arg(0));
      if (!socket) {
        fail(operation, 9);
        return;
      }
      if (socket.state === "file") {
        descriptors.delete(socket.fd);
        complete(operation, 0n);
        return;
      }
      if (socket.state === "listening") {
        listeners.delete(addressKey(socket.local));
      }
      if (socket.state === "datagram") {
        datagrams.delete(addressKey(socket.local));
      }
      if (socket.peer !== null) {
        const peer = descriptors.get(socket.peer);
        if (peer) {
          peer.peer = null;
          for (const read of peer.reads.splice(0)) {
            forgetPending(read);
            complete(read, 0n);
          }
        }
      }
      descriptors.delete(socket.fd);
      complete(operation, 0n);
      return;
    }
    default:
      throw new Error(`unsupported host opcode 0x${opcode.toString(16)}`);
  }
};

const serviceCancellation = (record) => {
  const [kind, slot, generation] = record;
  const key = operationKey(slot, generation);
  const retained = pending.get(key);
  if (kind !== 2 || !retained) {
    throw new Error(`invalid host cancellation ${record.join(",")}`);
  }
  retained.cancel();
  pending.delete(key);
  cancellationCount++;
  // Completion is the physical cancellation acknowledgement. The worker
  // result is discarded because the timer has already won the ParkSet.
  fail(retained.operation, 125);
};

let status = 2;
try {
  if (api.main(0, 0) !== 0) {
    throw new Error("module entry failed");
  }
  while (status !== 1) {
    if (operationCount + cancellationCount + scheduleCount > 16384) {
      throw new Error("host action loop did not complete");
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
        throw new Error(`unknown operation action ${kind}`);
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
    if (kind === 2) {
      const deadline = join(action[4], action[5]);
      if (deadline < now) {
        throw new Error(`alarm deadline regressed: ${deadline} < ${now}`);
      }
      now = deadline;
      alarmCount++;
    }
    const [nowLo, nowHi] = split(now);
    scheduleCount++;
    try {
      status = api.__llgo_coro_host_continue_slice_v1(
        action[1],
        action[2],
        action[3],
        kind === 1 ? 1 : 2,
        nowLo,
        nowHi,
        1024,
        resultPtr,
      ) >>> 0;
    } catch (error) {
      throw new Error(
        `scheduler trap after operations=${operationCount}, cancellations=${cancellationCount}, alarms=${alarmCount}, pending=${pending.size}, fds=${descriptors.size}`,
        { cause: error },
      );
    }
    const result = readWords(resultPtr, 8);
    if (status === 0 || status === 4 || status === 5 || status > 6 || result[7] !== 0) {
      throw new Error(`invalid scheduler result ${status}: ${result.join(",")}`);
    }
  }

  if (
    operationCount !== 594 ||
    cancellationCount !== 17 ||
    alarmCount < 18 ||
    dnsQueryTypes.size !== 2 ||
    !dnsQueryTypes.has(1) ||
    !dnsQueryTypes.has(28) ||
    pending.size !== 0 ||
    listeners.size !== 0 ||
    datagrams.size !== 0 ||
    descriptors.size !== 0
  ) {
    throw new Error(
      `incomplete stdlib network lifecycle: operations=${operationCount}, cancellations=${cancellationCount}, alarms=${alarmCount}, dnsQueries=${[...dnsQueryTypes].sort((a, b) => a - b)}, pending=${pending.size}, listeners=${listeners.size}, datagrams=${datagrams.size}, fds=${descriptors.size}`,
    );
  }
  console.log(JSON.stringify({
    status: "complete",
    operations: operationCount,
    cancellations: cancellationCount,
    alarms: alarmCount,
    dnsQueries: [...dnsQueryTypes].sort((a, b) => a - b),
    scheduleActions: scheduleCount,
    monotonicNanoseconds: now.toString(),
  }));
} finally {
  api.free(operationPtr);
  api.free(resultPtr);
  api.free(schedulePtr);
}
