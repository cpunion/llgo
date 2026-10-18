# Execute by gdb's Python interpreter. Never call into the inferior.
import gdb, os, json, struct, pathlib, time, hashlib
out = pathlib.Path(os.environ['DIAG_OUT'])
out.mkdir(exist_ok=True, parents=True)
image = os.environ['DIAG_IMAGE']
assert hashlib.sha256(pathlib.Path(image).read_bytes()).hexdigest() == 'e6f276e8309521c8b5b56c94979bc39702e4f74bf9ebac10fdd6189a52561e6e'
for cmd in ['set pagination off', 'set confirm off', 'set startup-with-shell off', 'set disable-randomization off', 'set print thread-events off', 'handle SIGPWR nostop noprint pass', 'handle SIGXCPU nostop noprint pass', 'set environment GOMAXPROCS 2', 'set environment GOMEMLIMIT 2GiB']:
    gdb.execute(cmd)
gdb.execute('file ' + image)
bp = gdb.Breakpoint('*0x39154f', internal=True)
physical_panic_bp = gdb.Breakpoint('*0x3aa6f4', internal=True)
last_event = None
def stopped(event):
    global last_event
    last_event = event
def exited(event):
    global last_event
    last_event = event
gdb.events.stop.connect(stopped)
gdb.events.exited.connect(exited)
rows = []
for iteration in range(1, 501):
    last_event = None
    started = time.monotonic()
    transcript = gdb.execute('run', to_string=True)
    row = {'iteration': iteration, 'seconds': time.monotonic() - started, 'event': type(last_event).__name__}
    if isinstance(last_event, gdb.ExitedEvent):
        row['exit'] = last_event.exit_code
        rows.append(row)
        if last_event.exit_code == 0:
            if iteration % 25 == 0: print('DIAG PASS', iteration, flush=True)
            continue
    failure = out / ('gdb-stop-%04d' % iteration)
    failure.mkdir()
    (failure / 'transcript.txt').write_text(transcript)
    inf = gdb.selected_inferior()
    maps = pathlib.Path('/proc/%d/maps' % inf.pid).read_text()
    (failure / 'maps.txt').write_text(maps)
    (failure / 'threads.txt').write_text(gdb.execute('thread apply all bt 40', to_string=True))
    (failure / 'registers.txt').write_text(gdb.execute('thread apply all info registers', to_string=True))
    handles = []
    visited = set()
    for bucket in range(64):
        h, = struct.unpack('<Q', inf.read_memory(0x4b1450 + 8 * bucket, 8))
        while h:
            if h in visited: break
            visited.add(h)
            key, live, next_h = struct.unpack('<QQQ', inf.read_memory(h, 24))
            handles.append({'bucket': bucket, 'handle': h, 'key': key, 'live': live})
            h = next_h
    (failure / 'weak-handles.json').write_text(json.dumps(handles, indent=2))
    targets = {((~h['key']) & ((1 << 64) - 1)): h for h in handles if h['live']}
    hits = []
    scanned = 0
    for line in maps.splitlines():
        parts = line.split()
        if not parts[1].startswith('rw'): continue
        lo, hi = [int(v, 16) for v in parts[0].split('-')]
        size = hi - lo
        if size > 64 * 1024**2 or scanned + size > 256 * 1024**2: continue
        try: raw = bytes(inf.read_memory(lo, size))
        except gdb.MemoryError: continue
        scanned += size
        for target, h in targets.items():
            needle = struct.pack('<Q', target)
            pos = raw.find(needle)
            while pos != -1:
                if pos % 8 == 0:
                    context = raw[max(0, pos - 64):pos + 72]
                    hits.append({'target': target, 'weak_handle': h['handle'], 'root_address': lo + pos, 'mapping': line, 'context_offset': max(0, pos - 64), 'context_hex': context.hex()})
                pos = raw.find(needle, pos + 1)
    (failure / 'strong-pointer-hits.json').write_text(json.dumps({'targets': list(targets), 'scanned_bytes': scanned, 'hits': hits}, indent=2))
    try: gdb.execute('generate-core-file ' + str(failure / 'timer.core'), to_string=True)
    except gdb.error as e: (failure / 'core-error.txt').write_text(str(e))
    row.update({'captured': str(failure), 'live_weak_handles': len(targets), 'pointer_hits': len(hits)})
    rows.append(row)
    print('DIAG STOP', json.dumps(row), flush=True)
    gdb.execute('kill', to_string=True)
    break
(out / 'gdb-result.json').write_text(json.dumps({'rows': rows, 'policy': 'Unmodified CI ELF, failure-only breakpoint, no calls into inferior; debugger success is not a fix'}, indent=2))
