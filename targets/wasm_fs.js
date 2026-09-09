// wasm_fs.js: browser host shim for Go syscall/fs_js.go on LLGo/Emscripten.
// Provides globalThis.fs, process, and path. Load before the generated main.js.
(function (global) {
  if (global.TextDecoder && !global.TextDecoder.prototype.__llgoResizableSafe) {
    const NativeTextDecoder = global.TextDecoder;
    class LLGoTextDecoder extends NativeTextDecoder {
      decode(input, options) {
        if (ArrayBuffer.isView(input)) input = Uint8Array.from(input);
        else if (input instanceof ArrayBuffer) input = new Uint8Array(input.slice(0));
        return super.decode(input, options);
      }
    }
    LLGoTextDecoder.prototype.__llgoResizableSafe = true;
    global.TextDecoder = LLGoTextDecoder;
  }

  if (global.fs) return;

  const decoder = new TextDecoder("utf-8");
  let outputBuf = "";
  let umaskValue = 0o022;

  // Emscripten WASI errno numbers from struct_info_generated.json.
  const errnoToCode = {
    2: "EACCES", 6: "EAGAIN", 8: "EBADF", 10: "EBUSY", 13: "ECONNABORTED",
    16: "EDEADLK", 20: "EEXIST", 27: "EINTR", 28: "EINVAL", 29: "EIO",
    31: "EISDIR", 32: "ELOOP", 33: "EMFILE", 34: "EMLINK", 37: "ENAMETOOLONG",
    43: "ENODEV", 44: "ENOENT", 48: "ENOMEM", 51: "ENOSPC", 52: "ENOSYS",
    54: "ENOTDIR", 55: "ENOTEMPTY", 59: "ENOTTY", 63: "EPERM", 64: "EPIPE",
    69: "EROFS", 70: "ESPIPE",
  };

  function enosys() {
    const err = new Error("not implemented");
    err.code = "ENOSYS";
    return err;
  }

  function nodeError(code, message) {
    const err = new Error(message || code);
    err.code = code;
    return err;
  }

  function toNodeError(e) {
    if (!e) return nodeError("EIO");
    if (typeof e.code === "string" && e.code !== "ErrnoError") return e;
    if (typeof e.errno === "number") {
      const code = errnoToCode[e.errno] || "EIO";
      const err = nodeError(code, e.message || code);
      err.errno = e.errno;
      return err;
    }
    return nodeError("EIO", e.message || String(e));
  }

  function emFS() {
    const fs = global.Module && global.Module.FS;
    if (!fs) throw nodeError("ENOSYS", "Emscripten Module.FS is not initialized");
    return fs;
  }

  function timeMs(t) {
    if (t == null) return Date.now();
    if (t instanceof Date) return t.getTime();
    if (typeof t === "number") return t < 1e12 ? t * 1000 : t;
    return Date.now();
  }

  function statObject(st) {
    const atimeMs = timeMs(st.atime);
    const mtimeMs = timeMs(st.mtime);
    const ctimeMs = timeMs(st.ctime);
    const size = st.size || 0;
    return {
      dev: st.dev || 0,
      ino: st.ino || 0,
      mode: st.mode || 0,
      nlink: st.nlink || 1,
      uid: st.uid || 0,
      gid: st.gid || 0,
      rdev: st.rdev || 0,
      size,
      blksize: st.blksize || 4096,
      blocks: st.blocks != null ? st.blocks : Math.ceil(size / 512),
      atimeMs,
      mtimeMs,
      ctimeMs,
      isDirectory: () => (st.mode & 16384) !== 0,
    };
  }

  function call(callback, fn) {
    try {
      callback(null, fn());
    } catch (e) {
      callback(toNodeError(e));
    }
  }

  function bytesToString(buf) {
    return decoder.decode(new Uint8Array(buf));
  }

  function sliceBuf(buf, offset, length) {
    if (offset === 0 && length === buf.length) return buf;
    return buf.subarray(offset, offset + length);
  }

  function writeSync(fd, buf) {
    const text = bytesToString(buf);
    outputBuf += text;
    const nl = outputBuf.lastIndexOf("\n");
    if (nl !== -1) {
      const lines = outputBuf.substring(0, nl);
      outputBuf = outputBuf.substring(nl + 1);
      if (fd === 2) console.error(lines);
      else console.log(lines);
    }
    return buf.length;
  }

  function streamOf(fd) {
    return emFS().getStreamChecked(fd);
  }

  global.fs = {
    constants: {
      O_RDONLY: 0,
      O_WRONLY: 1,
      O_RDWR: 2,
      O_CREAT: 64,
      O_EXCL: 128,
      O_TRUNC: 512,
      O_APPEND: 1024,
      O_DIRECTORY: 65536,
    },
    writeSync,
    write(fd, buf, offset, length, position, callback) {
      if (fd === 1 || fd === 2) {
        if (position != null) {
          callback(enosys());
          return;
        }
        callback(null, writeSync(fd, sliceBuf(buf, offset, length)));
        return;
      }
      call(callback, () => emFS().write(streamOf(fd), buf, offset, length, position == null ? undefined : position));
    },
    read(fd, buffer, offset, length, position, callback) {
      call(callback, () => emFS().read(streamOf(fd), buffer, offset, length, position == null ? undefined : position));
    },
    open(path, flags, mode, callback) {
      call(callback, () => emFS().open(path, flags, mode).fd);
    },
    close(fd, callback) {
      call(callback, () => {
        emFS().close(streamOf(fd));
      });
    },
    fsync(fd, callback) {
      if (fd === 1 || fd === 2) {
        callback(null);
        return;
      }
      call(callback, () => {
        const fs = emFS();
        if (fs.fsync) fs.fsync(streamOf(fd));
      });
    },
    stat(path, callback) { call(callback, () => statObject(emFS().stat(path))); },
    lstat(path, callback) { call(callback, () => statObject(emFS().lstat(path))); },
    fstat(fd, callback) { call(callback, () => statObject(emFS().fstat(fd))); },
    mkdir(path, perm, callback) { call(callback, () => { emFS().mkdir(path, perm); }); },
    unlink(path, callback) { call(callback, () => { emFS().unlink(path); }); },
    rmdir(path, callback) { call(callback, () => { emFS().rmdir(path); }); },
    rename(from, to, callback) { call(callback, () => { emFS().rename(from, to); }); },
    truncate(path, length, callback) { call(callback, () => { emFS().truncate(path, length); }); },
    ftruncate(fd, length, callback) { call(callback, () => { emFS().ftruncate(fd, length); }); },
    chmod(path, mode, callback) { call(callback, () => { emFS().chmod(path, mode); }); },
    fchmod(fd, mode, callback) { call(callback, () => { emFS().fchmod(fd, mode); }); },
    chown(path, uid, gid, callback) { call(callback, () => { emFS().chown(path, uid, gid); }); },
    fchown(fd, uid, gid, callback) { call(callback, () => { emFS().fchown(fd, uid, gid); }); },
    lchown(path, uid, gid, callback) { call(callback, () => { emFS().lchown(path, uid, gid); }); },
    readdir(path, callback) {
      call(callback, () => emFS().readdir(path).filter((x) => x !== "." && x !== ".."));
    },
    readlink(path, callback) { call(callback, () => emFS().readlink(path)); },
    link(from, to, callback) { call(callback, () => { emFS().link(from, to); }); },
    symlink(from, to, callback) { call(callback, () => { emFS().symlink(from, to); }); },
    utimes(path, atime, mtime, callback) {
      call(callback, () => {
        emFS().utime(path, timeMs(atime), timeMs(mtime));
      });
    },
  };

  if (!global.process) {
    global.process = {
      getuid() { return 0; },
      getgid() { return 0; },
      geteuid() { return 0; },
      getegid() { return 0; },
      getgroups() { return []; },
      pid: 1,
      ppid: 0,
      platform: "browser",
      umask(mask) {
        const old = umaskValue;
        if (mask != null) umaskValue = mask;
        return old;
      },
      cwd() {
        const fs = global.Module && global.Module.FS;
        return fs && fs.cwd ? fs.cwd() : "/";
      },
      chdir(path) {
        const fs = global.Module && global.Module.FS;
        if (fs && fs.chdir) fs.chdir(path);
      },
    };
  }

  if (!global.path) {
    global.path = {
      resolve(...pathSegments) {
        let out = "";
        for (const seg of pathSegments) {
          if (!seg) continue;
          if (seg.startsWith("/")) out = seg;
          else out = out ? out.replace(/\/+$/, "") + "/" + seg.replace(/^\/+/, "") : seg;
        }
        return out || ".";
      },
    };
  }
})(globalThis);
