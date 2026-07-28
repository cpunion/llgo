// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// This file has been modified for use by the TinyGo compiler.

(() => {
	// Map multiple JavaScript environments to a single common API,
	// preferring web standards over Node.js API.
	//
	// Environments considered:
	// - Browsers
	// - Node.js
	// - Electron
	// - Parcel

	if (typeof global !== "undefined") {
		// global already exists
	} else if (typeof window !== "undefined") {
		window.global = window;
	} else if (typeof self !== "undefined") {
		self.global = self;
	} else {
		throw new Error("cannot export Go (neither global, window nor self is defined)");
	}

	if (!global.require && typeof require !== "undefined") {
		global.require = require;
	}

	if (!global.fs && global.require) {
		global.fs = require("node:fs");
	}

	const enosys = () => {
		const err = new Error("not implemented");
		err.code = "ENOSYS";
		return err;
	};

	if (!global.fs) {
		let outputBuf = "";
		global.fs = {
			constants: { O_WRONLY: -1, O_RDWR: -1, O_CREAT: -1, O_TRUNC: -1, O_APPEND: -1, O_EXCL: -1 }, // unused
			writeSync(fd, buf) {
				outputBuf += decoder.decode(buf);
				const nl = outputBuf.lastIndexOf("\n");
				if (nl != -1) {
					console.log(outputBuf.substr(0, nl));
					outputBuf = outputBuf.substr(nl + 1);
				}
				return buf.length;
			},
			write(fd, buf, offset, length, position, callback) {
				if (offset !== 0 || length !== buf.length || position !== null) {
					callback(enosys());
					return;
				}
				const n = this.writeSync(fd, buf);
				callback(null, n);
			},
			chmod(path, mode, callback) { callback(enosys()); },
			chown(path, uid, gid, callback) { callback(enosys()); },
			close(fd, callback) { callback(enosys()); },
			fchmod(fd, mode, callback) { callback(enosys()); },
			fchown(fd, uid, gid, callback) { callback(enosys()); },
			fstat(fd, callback) { callback(enosys()); },
			fsync(fd, callback) { callback(null); },
			ftruncate(fd, length, callback) { callback(enosys()); },
			lchown(path, uid, gid, callback) { callback(enosys()); },
			link(path, link, callback) { callback(enosys()); },
			lstat(path, callback) { callback(enosys()); },
			mkdir(path, perm, callback) { callback(enosys()); },
			open(path, flags, mode, callback) { callback(enosys()); },
			read(fd, buffer, offset, length, position, callback) { callback(enosys()); },
			readdir(path, callback) { callback(enosys()); },
			readlink(path, callback) { callback(enosys()); },
			rename(from, to, callback) { callback(enosys()); },
			rmdir(path, callback) { callback(enosys()); },
			stat(path, callback) { callback(enosys()); },
			symlink(path, link, callback) { callback(enosys()); },
			truncate(path, length, callback) { callback(enosys()); },
			unlink(path, callback) { callback(enosys()); },
			utimes(path, atime, mtime, callback) { callback(enosys()); },
		};
	}

	if (!global.process) {
		global.process = {
			getuid() { return -1; },
			getgid() { return -1; },
			geteuid() { return -1; },
			getegid() { return -1; },
			getgroups() { throw enosys(); },
			pid: -1,
			ppid: -1,
			umask() { throw enosys(); },
			cwd() { throw enosys(); },
			chdir() { throw enosys(); },
		}
	}

	if (!global.crypto) {
		const nodeCrypto = require("node:crypto");
		global.crypto = {
			getRandomValues(b) {
				nodeCrypto.randomFillSync(b);
			},
		};
	}

	if (!global.performance) {
		global.performance = {
			now() {
				const [sec, nsec] = process.hrtime();
				return sec * 1000 + nsec / 1000000;
			},
		};
	}

	if (!global.TextEncoder) {
		global.TextEncoder = require("node:util").TextEncoder;
	}

	if (!global.TextDecoder) {
		global.TextDecoder = require("node:util").TextDecoder;
	}

	// End of polyfills for common API.

	const encoder = new TextEncoder("utf-8");
	const decoder = new TextDecoder("utf-8");
	let reinterpretBuf = new DataView(new ArrayBuffer(8));
	var logLine = [];
	const wasmExit = {}; // thrown to exit via proc_exit (not an error)

	global.Go = class {
		constructor() {
			this._callbackTimeouts = new Map();
			this._nextCallbackTimeoutID = 1;

			const mem = () => {
				// The buffer may change when requesting more memory.
				return new DataView(this._inst.exports.memory.buffer);
			}

			const unboxValue = (v_ref) => {
				reinterpretBuf.setBigInt64(0, v_ref, true);
				const f = reinterpretBuf.getFloat64(0, true);
				if (f === 0) {
					return undefined;
				}
				if (!isNaN(f)) {
					return f;
				}

				const id = v_ref & 0xffffffffn;
				return this._values[id];
			}


			const loadValue = (addr) => {
				let v_ref = mem().getBigUint64(addr, true);
				return unboxValue(v_ref);
			}

			const boxValue = (v) => {
				const nanHead = 0x7FF80000n;

				if (typeof v === "number") {
					if (isNaN(v)) {
						return nanHead << 32n;
					}
					if (v === 0) {
						return (nanHead << 32n) | 1n;
					}
					reinterpretBuf.setFloat64(0, v, true);
					return reinterpretBuf.getBigInt64(0, true);
				}

				switch (v) {
					case undefined:
						return 0n;
					case null:
						return (nanHead << 32n) | 2n;
					case true:
						return (nanHead << 32n) | 3n;
					case false:
						return (nanHead << 32n) | 4n;
				}

				let id = this._ids.get(v);
				if (id === undefined) {
					id = this._idPool.pop();
					if (id === undefined) {
						id = BigInt(this._values.length);
					}
					this._values[id] = v;
					this._goRefCounts[id] = 0;
					this._ids.set(v, id);
				}
				this._goRefCounts[id]++;
				let typeFlag = 1n;
				switch (typeof v) {
					case "string":
						typeFlag = 2n;
						break;
					case "symbol":
						typeFlag = 3n;
						break;
					case "function":
						typeFlag = 4n;
						break;
				}
				return id | ((nanHead | typeFlag) << 32n);
			}

			const storeValue = (addr, v) => {
				let v_ref = boxValue(v);
				mem().setBigUint64(addr, v_ref, true);
			}

			const loadSlice = (array, len, cap) => {
				return new Uint8Array(this._inst.exports.memory.buffer, array, len);
			}

			const loadSliceOfValues = (array, len, cap) => {
				const a = new Array(len);
				for (let i = 0; i < len; i++) {
					a[i] = loadValue(array + i * 8);
				}
				return a;
			}

			const loadString = (ptr, len) => {
				return decoder.decode(new DataView(this._inst.exports.memory.buffer, ptr, len));
			}

			const loadCString = (ptr) => {
				ptr >>>= 0;
				if (ptr === 0) {
					return null;
				}
				const bytes = new Uint8Array(this._inst.exports.memory.buffer);
				let end = ptr;
				while (end < bytes.length && bytes[end] !== 0) {
					end++;
				}
				if (end === bytes.length) {
					throw new Error("unterminated LLGo JavaScript host string");
				}
				return decoder.decode(bytes.subarray(ptr, end));
			}

			const llgoJSValue = (handle) => {
				handle >>>= 0;
				switch (handle) {
					case 2: return undefined;
					case 4: return null;
					case 6: return true;
					case 8: return false;
				}
				if (!this._llgoJSValues || !this._llgoJSValues.has(handle)) {
					throw new Error(`unknown LLGo JavaScript value handle ${handle}`);
				}
				return this._llgoJSValues.get(handle);
			}

			const llgoJSHandle = (value) => {
				switch (value) {
					case undefined: return 2;
					case null: return 4;
					case true: return 6;
					case false: return 8;
				}
				if (!this._llgoJSValues) {
					throw new Error("LLGo JavaScript value table is not initialized");
				}
				const handle = this._llgoJSNextHandle;
				this._llgoJSNextHandle += 2;
				if (handle === 0 || this._llgoJSNextHandle > 0xffff_fffe) {
					throw new Error("LLGo JavaScript value handle space exhausted");
				}
				this._llgoJSValues.set(handle, value);
				return handle;
			}

			const loadLLGoJSArgs = (ptr, count) => {
				ptr >>>= 0;
				count |= 0;
				if (count < 0 || (count !== 0 && ptr === 0) ||
					ptr + count * 4 > this._inst.exports.memory.buffer.byteLength) {
					throw new Error("invalid LLGo JavaScript argument range");
				}
				const view = mem();
				const args = new Array(count);
				for (let index = 0; index < count; index++) {
					args[index] = llgoJSValue(view.getUint32(ptr + index * 4, true));
				}
				return args;
			}

			const timeOrigin = Date.now() - performance.now();
			this.importObject = {
				wasi_snapshot_preview1: {
					// https://github.com/WebAssembly/WASI/blob/main/phases/snapshot/docs.md#fd_write
					fd_write: function(fd, iovs_ptr, iovs_len, nwritten_ptr) {
						let nwritten = 0;
						if (fd == 1) {
							for (let iovs_i=0; iovs_i<iovs_len;iovs_i++) {
								let iov_ptr = iovs_ptr+iovs_i*8; // assuming wasm32
								let ptr = mem().getUint32(iov_ptr + 0, true);
								let len = mem().getUint32(iov_ptr + 4, true);
								nwritten += len;
								for (let i=0; i<len; i++) {
									let c = mem().getUint8(ptr+i);
									if (c == 13) { // CR
										// ignore
									} else if (c == 10) { // LF
										// write line
										let line = decoder.decode(new Uint8Array(logLine));
										logLine = [];
										console.log(line);
									} else {
										logLine.push(c);
									}
								}
							}
						} else {
							console.error('invalid file descriptor:', fd);
						}
						mem().setUint32(nwritten_ptr, nwritten, true);
						return 0;
					},
					fd_close: () => 0,      // dummy
					fd_fdstat_get: () => 0, // dummy
					fd_seek: () => 0,       // dummy
					proc_exit: (code) => {
						this._wasmExit(code);
					},
					random_get: (bufPtr, bufLen) => {
						crypto.getRandomValues(loadSlice(bufPtr, bufLen));
						return 0;
					},
				},
				gojs: {
					// func ticks() int64
					"runtime.ticks": () => {
						return BigInt((timeOrigin + performance.now()) * 1e6);
					},

					// func finalizeRef(v ref)
					"syscall/js.finalizeRef": (v_ref) => {
						// Note: TinyGo does not support finalizers so this is only called
						// for one specific case, by js.go:jsString. and can/might leak memory.
						const id = v_ref & 0xffffffffn;
						if (this._goRefCounts?.[id] !== undefined) {
							this._goRefCounts[id]--;
							if (this._goRefCounts[id] === 0) {
								const v = this._values[id];
								this._values[id] = null;
								this._ids.delete(v);
								this._idPool.push(id);
							}
						} else {
							console.error("syscall/js.finalizeRef: unknown id", id);
						}
					},

					// func stringVal(value string) ref
					"syscall/js.stringVal": (value_ptr, value_len) => {
						value_ptr >>>= 0;
						const s = loadString(value_ptr, value_len);
						return boxValue(s);
					},

					// func valueGet(v ref, p string) ref
					"syscall/js.valueGet": (v_ref, p_ptr, p_len) => {
						let prop = loadString(p_ptr, p_len);
						let v = unboxValue(v_ref);
						let result = Reflect.get(v, prop);
						return boxValue(result);
					},

					// func valueSet(v ref, p string, x ref)
					"syscall/js.valueSet": (v_ref, p_ptr, p_len, x_ref) => {
						const v = unboxValue(v_ref);
						const p = loadString(p_ptr, p_len);
						const x = unboxValue(x_ref);
						Reflect.set(v, p, x);
					},

					// func valueDelete(v ref, p string)
					"syscall/js.valueDelete": (v_ref, p_ptr, p_len) => {
						const v = unboxValue(v_ref);
						const p = loadString(p_ptr, p_len);
						Reflect.deleteProperty(v, p);
					},

					// func valueIndex(v ref, i int) ref
					"syscall/js.valueIndex": (v_ref, i) => {
						return boxValue(Reflect.get(unboxValue(v_ref), i));
					},

					// valueSetIndex(v ref, i int, x ref)
					"syscall/js.valueSetIndex": (v_ref, i, x_ref) => {
						Reflect.set(unboxValue(v_ref), i, unboxValue(x_ref));
					},

					// func valueCall(v ref, m string, args []ref) (ref, bool)
					"syscall/js.valueCall": (ret_addr, v_ref, m_ptr, m_len, args_ptr, args_len, args_cap) => {
						const v = unboxValue(v_ref);
						const name = loadString(m_ptr, m_len);
						const args = loadSliceOfValues(args_ptr, args_len, args_cap);
						try {
							const m = Reflect.get(v, name);
							storeValue(ret_addr, Reflect.apply(m, v, args));
							mem().setUint8(ret_addr + 8, 1);
						} catch (err) {
							storeValue(ret_addr, err);
							mem().setUint8(ret_addr + 8, 0);
						}
					},

					// func valueInvoke(v ref, args []ref) (ref, bool)
					"syscall/js.valueInvoke": (ret_addr, v_ref, args_ptr, args_len, args_cap) => {
						try {
							const v = unboxValue(v_ref);
							const args = loadSliceOfValues(args_ptr, args_len, args_cap);
							storeValue(ret_addr, Reflect.apply(v, undefined, args));
							mem().setUint8(ret_addr + 8, 1);
						} catch (err) {
							storeValue(ret_addr, err);
							mem().setUint8(ret_addr + 8, 0);
						}
					},

					// func valueNew(v ref, args []ref) (ref, bool)
					"syscall/js.valueNew": (ret_addr, v_ref, args_ptr, args_len, args_cap) => {
						const v = unboxValue(v_ref);
						const args = loadSliceOfValues(args_ptr, args_len, args_cap);
						try {
							storeValue(ret_addr, Reflect.construct(v, args));
							mem().setUint8(ret_addr + 8, 1);
						} catch (err) {
							storeValue(ret_addr, err);
							mem().setUint8(ret_addr+ 8, 0);
						}
					},

					// func valueLength(v ref) int
					"syscall/js.valueLength": (v_ref) => {
						return unboxValue(v_ref).length;
					},

					// valuePrepareString(v ref) (ref, int)
					"syscall/js.valuePrepareString": (ret_addr, v_ref) => {
						const s = String(unboxValue(v_ref));
						const str = encoder.encode(s);
						storeValue(ret_addr, str);
						mem().setInt32(ret_addr + 8, str.length, true);
					},

					// valueLoadString(v ref, b []byte)
					"syscall/js.valueLoadString": (v_ref, slice_ptr, slice_len, slice_cap) => {
						const str = unboxValue(v_ref);
						loadSlice(slice_ptr, slice_len, slice_cap).set(str);
					},

					// func valueInstanceOf(v ref, t ref) bool
					"syscall/js.valueInstanceOf": (v_ref, t_ref) => {
 						return unboxValue(v_ref) instanceof unboxValue(t_ref);
					},

					// func copyBytesToGo(dst []byte, src ref) (int, bool)
					"syscall/js.copyBytesToGo": (ret_addr, dest_addr, dest_len, dest_cap, src_ref) => {
						let num_bytes_copied_addr = ret_addr;
						let returned_status_addr = ret_addr + 4; // Address of returned boolean status variable

						const dst = loadSlice(dest_addr, dest_len);
						const src = unboxValue(src_ref);
						if (!(src instanceof Uint8Array || src instanceof Uint8ClampedArray)) {
							mem().setUint8(returned_status_addr, 0); // Return "not ok" status
							return;
						}
						const toCopy = src.subarray(0, dst.length);
						dst.set(toCopy);
						mem().setUint32(num_bytes_copied_addr, toCopy.length, true);
						mem().setUint8(returned_status_addr, 1); // Return "ok" status
					},

					// copyBytesToJS(dst ref, src []byte) (int, bool)
					// Originally copied from upstream Go project, then modified:
					//   https://github.com/golang/go/blob/3f995c3f3b43033013013e6c7ccc93a9b1411ca9/misc/wasm/wasm_exec.js#L404-L416
					"syscall/js.copyBytesToJS": (ret_addr, dst_ref, src_addr, src_len, src_cap) => {
						let num_bytes_copied_addr = ret_addr;
						let returned_status_addr = ret_addr + 4; // Address of returned boolean status variable

						const dst = unboxValue(dst_ref);
						const src = loadSlice(src_addr, src_len);
						if (!(dst instanceof Uint8Array || dst instanceof Uint8ClampedArray)) {
							mem().setUint8(returned_status_addr, 0); // Return "not ok" status
							return;
						}
						const toCopy = src.subarray(0, dst.length);
						dst.set(toCopy);
						mem().setUint32(num_bytes_copied_addr, toCopy.length, true);
						mem().setUint8(returned_status_addr, 1); // Return "ok" status
					},
				}
			};

			// LLGo uses a direct scalar/pointer WebAssembly ABI for syscall/js.
			// It is intentionally separate from both Emscripten embind internals
			// and the Go toolchain's private stack-pointer "gojs" ABI above.
			this.importObject.llgo_js = {
				timezone_offset: () => new Date().getTimezoneOffset(),
				invoke_v1: (opcode, recordPtr) => {
					opcode >>>= 0;
					recordPtr >>>= 0;
					if (recordPtr === 0 ||
						recordPtr + 64 > this._inst.exports.memory.buffer.byteLength) {
						throw new Error("invalid LLGo JavaScript host record");
					}
					const record = new DataView(
						this._inst.exports.memory.buffer,
						recordPtr,
						64,
					);
					const word = (index) => record.getBigUint64(index * 8, true);
					const scalar = (index) => Number(word(index) & 0xffff_ffffn);
					const setWord = (index, value) => {
						record.setBigUint64(index * 8, BigInt(value), true);
					};
					const setHandle = (value) => setWord(0, llgoJSHandle(value));

					switch (opcode) {
						case 2: {
							const name = loadCString(scalar(0));
							setHandle(name === null ? global : global[name]);
							break;
						}
						case 3:
							setHandle(record.getFloat64(0, true));
							break;
						case 4:
							setHandle(loadCString(scalar(0)));
							break;
						case 5:
							setHandle({});
							break;
						case 6:
							setHandle([]);
							break;
						case 7:
							Reflect.set(
								llgoJSValue(scalar(0)),
								llgoJSValue(scalar(1)),
								llgoJSValue(scalar(2)),
							);
							break;
						case 8:
							setHandle(Reflect.get(
								llgoJSValue(scalar(0)),
								llgoJSValue(scalar(1)),
							));
							break;
						case 9:
							setWord(0, Reflect.deleteProperty(
								llgoJSValue(scalar(0)),
								llgoJSValue(scalar(1)),
							) ? 1 : 0);
							break;
						case 10:
							setWord(0, typeof llgoJSValue(scalar(0)) === "number" ? 1 : 0);
							break;
						case 11:
							setWord(0, typeof llgoJSValue(scalar(0)) === "string" ? 1 : 0);
							break;
						case 12:
							setWord(0, llgoJSValue(scalar(0)) in llgoJSValue(scalar(1)) ? 1 : 0);
							break;
						case 13:
							setHandle(typeof llgoJSValue(scalar(0)));
							break;
						case 14:
							setWord(0,
								llgoJSValue(scalar(0)) instanceof llgoJSValue(scalar(1)) ? 1 : 0,
							);
							break;
						case 15:
							record.setFloat64(0, Number(llgoJSValue(scalar(0))), true);
							break;
						case 16:
							setWord(0, encoder.encode(String(llgoJSValue(scalar(0)))).length);
							break;
						case 17: {
							const bytes = encoder.encode(String(llgoJSValue(scalar(0))));
							const dataPtr = scalar(1);
							const size = scalar(2);
							if (bytes.length !== size ||
								dataPtr + size > this._inst.exports.memory.buffer.byteLength) {
								setWord(0, 0);
								break;
							}
							new Uint8Array(
								this._inst.exports.memory.buffer,
								dataPtr,
								size,
							).set(bytes);
							setWord(0, size);
							break;
						}
						case 18:
							setWord(0,
								llgoJSValue(scalar(0)) === llgoJSValue(scalar(1)) ? 1 : 0,
							);
							break;
						case 19: {
							const receiver = llgoJSValue(scalar(0));
							try {
								const method = Reflect.get(receiver, loadCString(scalar(1)));
								setHandle(Reflect.apply(
									method,
									receiver,
									loadLLGoJSArgs(scalar(2), scalar(3)),
								));
								setWord(1, 0);
							} catch (error) {
								setHandle(error);
								setWord(1, 1);
							}
							break;
						}
						case 20: {
							const callable = llgoJSValue(scalar(0));
							const args = loadLLGoJSArgs(scalar(1), scalar(2));
							try {
								setHandle(scalar(3) === 1
									? Reflect.construct(callable, args)
									: Reflect.apply(callable, undefined, args));
								setWord(1, 0);
							} catch (error) {
								setHandle(error);
								setWord(1, 1);
							}
							break;
						}
						case 21: {
							const length = scalar(0);
							const dataPtr = scalar(1);
							if (dataPtr + length > this._inst.exports.memory.buffer.byteLength) {
								throw new Error("invalid LLGo JavaScript memory view");
							}
							setHandle(new Uint8Array(
								this._inst.exports.memory.buffer,
								dataPtr,
								length,
							));
							break;
						}
						case 22:
							console.log(llgoJSValue(scalar(0)));
							break;
						default:
							throw new Error(`unsupported LLGo JavaScript host opcode ${opcode}`);
					}
					return 1;
				},
			};

			// Go 1.20 uses 'env'. Go 1.21 uses 'gojs'.
			// For compatibility, we use both as long as Go 1.20 is supported.
			this.importObject.env = this.importObject.gojs;
		}

		_wasmExit(code) {
			this.exited = true;
			this.exitCode = Number(code);
			this._cancelCoroHostActions();
			if (this._resolveExitPromise) {
				this._resolveExitPromise();
			}
			throw wasmExit;
		}

		_coroNow() {
			return BigInt(Math.floor(performance.now() * 1e6));
		}

		_coroSplitWord(word) {
			const unsigned = BigInt.asUintN(64, word);
			return [
				Number(unsigned & 0xffff_ffffn),
				Number((unsigned >> 32n) & 0xffff_ffffn),
			];
		}

		_coroReadWords(ptr, count) {
			const state = this._coroHost;
			if (!state || ptr < 0 || count < 0 || ptr + count * 4 > state.api.memory.buffer.byteLength) {
				throw new Error("invalid LLGo coroutine host scratch range");
			}
			const view = new DataView(state.api.memory.buffer, ptr, count * 4);
			return Array.from({ length: count }, (_, index) =>
				view.getUint32(index * 4, true),
			);
		}

		_coroAllZero(words) {
			return words.every((word) => word === 0);
		}

		_coroPublishClocks() {
			const state = this._coroHost;
			const now = this._coroNow();
			const [nowLo, nowHi] = this._coroSplitWord(now);
			if (!state.api.__llgo_coro_host_publish_time_v1(nowLo, nowHi)) {
				throw new Error("LLGo coroutine host rejected monotonic time");
			}

			const milliseconds = BigInt(Date.now());
			let seconds = milliseconds / 1000n;
			let remainder = milliseconds % 1000n;
			if (remainder < 0) {
				seconds--;
				remainder += 1000n;
			}
			const [secLo, secHi] = this._coroSplitWord(seconds);
			if (!state.api.__llgo_coro_host_publish_wall_time_v1(
				secLo,
				secHi,
				Number(remainder * 1_000_000n),
			)) {
				throw new Error("LLGo coroutine host rejected wall time");
			}
			return [now, nowLo, nowHi];
		}

		_prepareCoroHost() {
			const api = this._inst.exports;
			if (typeof api.__llgo_coro_host_profile_v1 !== "function") {
				return false;
			}
			for (const name of [
				"memory",
				"malloc",
				"free",
				"__llgo_coro_host_next_action_v1",
				"__llgo_coro_host_publish_time_v1",
				"__llgo_coro_host_publish_wall_time_v1",
				"__llgo_coro_host_ack_cancel_v1",
				"__llgo_coro_host_continue_slice_v1",
				"__llgo_coro_host_next_operation_v1",
				"__llgo_coro_host_complete_operation_v1",
			]) {
				if (!(name in api)) {
					throw new Error(`missing LLGo coroutine host export ${name}`);
				}
			}
			if (!api.memory || !api.memory.buffer ||
				typeof api.malloc !== "function" || typeof api.free !== "function") {
				throw new Error("invalid LLGo coroutine host memory ABI");
			}

			const profile = api.__llgo_coro_host_profile_v1() >>> 0;
			const profileKindJS = 1;
			const capabilitySchedule = 1 << 8;
			const capabilityAlarm = 1 << 9;
			const externalReactor = 0x80000000;
			if ((profile & 0xff) !== profileKindJS ||
				(profile & (capabilitySchedule | capabilityAlarm)) !==
					(capabilitySchedule | capabilityAlarm) ||
				(profile & externalReactor) === 0) {
				throw new Error(`unsupported LLGo coroutine JS host profile 0x${profile.toString(16)}`);
			}

			const scratch = api.malloc(160) >>> 0;
			if (scratch === 0 || scratch + 160 > api.memory.buffer.byteLength) {
				throw new Error("LLGo coroutine host scratch allocation failed");
			}
			this._coroHost = {
				api,
				profile,
				scratch,
				actionPtr: scratch,
				resultPtr: scratch + 32,
				operationPtr: scratch + 64,
				schedule: null,
				alarm: null,
				disposed: false,
			};
			try {
				this._coroPublishClocks();
			} catch (error) {
				this._disposeCoroHost();
				throw error;
			}
			return true;
		}

		_cancelCoroHostActions() {
			const state = this._coroHost;
			if (!state) {
				return;
			}
			if (state.schedule) {
				state.schedule.active = false;
				state.schedule = null;
			}
			if (state.alarm) {
				state.alarm.active = false;
				if (state.alarm.timeout !== undefined) {
					clearTimeout(state.alarm.timeout);
				}
				state.alarm = null;
			}
		}

		_disposeCoroHost() {
			const state = this._coroHost;
			if (!state || state.disposed) {
				return;
			}
			this._cancelCoroHostActions();
			state.disposed = true;
			state.api.free(state.scratch);
		}

		_failCoroHost(error) {
			if (this.exited) {
				return;
			}
			this.exited = true;
			this.exitCode = 1;
			this._disposeCoroHost();
			this._rejectExitPromise(error);
		}

		_finishCoroHost() {
			if (this.exited) {
				return;
			}
			this.exited = true;
			this.exitCode = 0;
			this._disposeCoroHost();
			this._resolveExitPromise();
		}

		_scheduleCoroTurn(record) {
			const enqueue = typeof queueMicrotask === "function"
				? queueMicrotask
				: (callback) => Promise.resolve().then(callback);
			enqueue(() => {
				const state = this._coroHost;
				if (!state || this.exited || !record.active || state.schedule !== record) {
					return;
				}
				record.active = false;
				state.schedule = null;
				this._continueCoroHost(record, 1);
			});
		}

		_armCoroAlarm(record) {
			const tick = () => {
				const state = this._coroHost;
				if (!state || this.exited || !record.active || state.alarm !== record) {
					return;
				}
				const remaining = record.deadline - this._coroNow();
				if (remaining > 0) {
					const maximumDelay = 2_147_483_647n * 1_000_000n;
					const bounded = remaining > maximumDelay ? maximumDelay : remaining;
					const delay = Number((bounded + 999_999n) / 1_000_000n);
					record.timeout = setTimeout(tick, delay);
					return;
				}
				record.active = false;
				state.alarm = null;
				this._continueCoroHost(record, 2);
			};
			record.timeout = setTimeout(tick, 0);
		}

		_takeCoroAction(kind, words) {
			const state = this._coroHost;
			const [, slot, generation, epoch, deadlineLo, deadlineHi, reserved0, reserved1] = words;
			if (reserved0 !== 0 || reserved1 !== 0 || slot === 0 || generation === 0 || epoch === 0) {
				throw new Error(`malformed LLGo coroutine host action ${words.join(",")}`);
			}

			if (kind === 3 || kind === 4) {
				if (deadlineLo !== 0 || deadlineHi !== 0) {
					throw new Error(`LLGo coroutine cancel carries a deadline: ${words.join(",")}`);
				}
				const field = kind === 3 ? "schedule" : "alarm";
				const record = state[field];
				if (!record || record.slot !== slot || record.generation !== generation || record.epoch !== epoch) {
					throw new Error(`LLGo coroutine cancel has no exact ${field} obligation`);
				}
				record.active = false;
				if (field === "alarm" && record.timeout !== undefined) {
					clearTimeout(record.timeout);
				}
				state[field] = null;
				if (!state.api.__llgo_coro_host_ack_cancel_v1(slot, generation, epoch, kind)) {
					throw new Error(`LLGo coroutine ${field} cancellation was not acknowledged`);
				}
				return;
			}

			if (kind !== 1 && kind !== 2) {
				throw new Error(`unknown LLGo coroutine host action kind ${kind}`);
			}
			const field = kind === 1 ? "schedule" : "alarm";
			if (state[field]) {
				throw new Error(`duplicate LLGo coroutine ${field} obligation`);
			}
			if (kind === 1 && (deadlineLo !== 0 || deadlineHi !== 0)) {
				throw new Error(`LLGo coroutine schedule carries a deadline: ${words.join(",")}`);
			}
			const record = {
				slot,
				generation,
				epoch,
				deadline: (BigInt(deadlineHi) << 32n) | BigInt(deadlineLo),
				active: true,
			};
			state[field] = record;
			if (kind === 1) {
				this._scheduleCoroTurn(record);
			} else {
				this._armCoroAlarm(record);
			}
		}

		_drainCoroHost() {
			const state = this._coroHost;
			for (;;) {
				const kind = state.api.__llgo_coro_host_next_operation_v1(state.operationPtr) >>> 0;
				const words = this._coroReadWords(state.operationPtr, 24);
				if (kind !== words[0]) {
					throw new Error(`LLGo coroutine operation kind mismatch: ${kind}/${words[0]}`);
				}
				if (kind === 0) {
					if (!this._coroAllZero(words)) {
						throw new Error(`nonzero empty LLGo coroutine operation: ${words.join(",")}`);
					}
					break;
				}
				throw new Error(`unsupported LLGo JavaScript host operation opcode ${words[3]} (kind ${kind})`);
			}

			for (;;) {
				const kind = state.api.__llgo_coro_host_next_action_v1(state.actionPtr) >>> 0;
				const words = this._coroReadWords(state.actionPtr, 8);
				if (kind !== words[0]) {
					throw new Error(`LLGo coroutine action kind mismatch: ${kind}/${words[0]}`);
				}
				if (kind === 0) {
					if (!this._coroAllZero(words)) {
						throw new Error(`nonzero empty LLGo coroutine action: ${words.join(",")}`);
					}
					break;
				}
				this._takeCoroAction(kind, words);
			}
			return state.schedule !== null || state.alarm !== null;
		}

		_validateCoroRunResult(status, result) {
			const [flags, used, slot, generation, epoch, deadlineLo, deadlineHi, reserved] = result;
			if (reserved !== 0 || used > 1024) {
				throw new Error(`malformed LLGo coroutine run result ${status}: ${result.join(",")}`);
			}
			if (status === 1) {
				if (flags !== 0 || slot !== 0 || generation !== 0 || epoch !== 0 ||
					deadlineLo !== 0 || deadlineHi !== 0) {
					throw new Error(`malformed LLGo coroutine completion: ${result.join(",")}`);
				}
				return;
			}
			if (status === 3 || status === 6) {
				if (flags !== 17 || slot === 0 || generation === 0 || epoch === 0 ||
					deadlineLo !== 0 || deadlineHi !== 0) {
					throw new Error(`malformed LLGo coroutine queued result ${status}: ${result.join(",")}`);
				}
				return;
			}
			if (status === 2) {
				const hasDeadline = (flags & 4) !== 0;
				if ((flags & ~6) !== 0 || (flags & 2) === 0 ||
					slot === 0 || generation === 0 || epoch === 0 ||
					(!hasDeadline && (deadlineLo !== 0 || deadlineHi !== 0))) {
					throw new Error(`malformed LLGo coroutine suspended result: ${result.join(",")}`);
				}
				return;
			}
			throw new Error(`invalid LLGo coroutine drive status ${status}: ${result.join(",")}`);
		}

		_continueCoroHost(record, cause) {
			if (this.exited) {
				return;
			}
			try {
				const state = this._coroHost;
				const [, nowLo, nowHi] = this._coroPublishClocks();
				let status;
				try {
					status = state.api.__llgo_coro_host_continue_slice_v1(
						record.slot,
						record.generation,
						record.epoch,
						cause,
						nowLo,
						nowHi,
						1024,
						state.resultPtr,
					) >>> 0;
				} catch (error) {
					if (error === wasmExit) {
						this._disposeCoroHost();
						return;
					}
					throw error;
				}
				const result = this._coroReadWords(state.resultPtr, 8);
				this._validateCoroRunResult(status, result);
				if (status === 1) {
					this._finishCoroHost();
					return;
				}
				if (!this._drainCoroHost()) {
					throw new Error(`LLGo coroutine drive status ${status} returned without a host obligation`);
				}
			} catch (error) {
				this._failCoroHost(error);
			}
		}

		async run(instance) {
			this._inst = instance;
			this._values = [ // JS values that Go currently has references to, indexed by reference id
				NaN,
				0,
				null,
				true,
				false,
				global,
				this,
			];
			this._goRefCounts = []; // number of references that Go has to a JS value, indexed by reference id
			this._ids = new Map();  // mapping from JS values to reference ids
			this._idPool = [];      // unused ids that have been garbage collected
			this._llgoJSValues = new Map();
			this._llgoJSNextHandle = 10;
			this.exited = false;    // whether the Go program has exited
			this.exitCode = 0;

			if (this._inst.exports._start) {
				let exitPromise = new Promise((resolve, reject) => {
					this._resolveExitPromise = resolve;
					this._rejectExitPromise = reject;
				});
				let coroHost = false;

				// Run program, but catch the wasmExit exception that's thrown
				// to return back here.
				try {
					coroHost = this._prepareCoroHost();
					this._inst.exports._start();
				} catch (e) {
					if (e !== wasmExit) {
						this._disposeCoroHost();
						throw e;
					}
				}
				if (this.exited) {
					this._disposeCoroHost();
				} else if (coroHost) {
					try {
						if (!this._drainCoroHost()) {
							this._finishCoroHost();
						}
					} catch (error) {
						this._failCoroHost(error);
					}
				}

				await exitPromise;
				return this.exitCode;
			} else {
				this._inst.exports._initialize();
			}
		}

		_resume() {
			if (this.exited) {
				throw new Error("Go program has already exited");
			}
			try {
				this._inst.exports.resume();
			} catch (e) {
				if (e !== wasmExit) throw e;
			}
			if (this.exited) {
				this._resolveExitPromise();
			}
		}

		_makeFuncWrapper(id) {
			const go = this;
			return function () {
				const event = { id: id, this: this, args: arguments };
				go._pendingEvent = event;
				go._resume();
				return event.result;
			};
		}
	}

	if (
		global.require &&
		global.require.main === module &&
		global.process &&
		global.process.versions &&
		!global.process.versions.electron
	) {
		if (process.argv.length != 3) {
			console.error("usage: go_js_wasm_exec [wasm binary] [arguments]");
			process.exit(1);
		}

		const go = new Go();
		WebAssembly.instantiate(fs.readFileSync(process.argv[2]), go.importObject).then(async (result) => {
			let exitCode = await go.run(result.instance);
			process.exit(exitCode);
		}).catch((err) => {
			console.error(err);
			process.exit(1);
		});
	}
})();
