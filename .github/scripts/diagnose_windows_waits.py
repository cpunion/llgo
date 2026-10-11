import concurrent.futures
import json
import os
from pathlib import Path
import subprocess
import sys
import threading
import time

root = Path(os.environ['WAIT_PROBE_DIR'])
exe = root / 'commandrun.exe'
if '--direct' in sys.argv:
    failed = threading.Event()
    records = []
    def check(i):
        if failed.is_set():
            return
        start = time.monotonic()
        with (root / f'commandrun-direct-{i}.log').open('w') as log:
            process = subprocess.Popen([str(exe)], cwd='_demo/go/commandrun', stdout=log, stderr=log)
            try:
                process.wait(timeout=15)
                result = process.returncode
            except subprocess.TimeoutExpired:
                result = 'timeout'
                if not failed.is_set():
                    (root / 'stalled-pid').write_text(str(process.pid))
                failed.set()
            item = {'round': i, 'pid': process.pid, 'result': result, 'seconds': time.monotonic()-start}
            print(json.dumps(item), flush=True)
            records.append(item)
            if result:
                failed.set()
    with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
        list(pool.map(check, range(100)))
    (root / 'results.json').write_text(json.dumps(records, indent=2))
    sys.exit(0)

import lldb
if (root / 'stalled-pid').exists():
    pid = int((root / 'stalled-pid').read_text())
    debugger = lldb.debugger
    debugger.SetAsync(False)
    target = debugger.CreateTarget(str(exe))
    debugger.SetSelectedTarget(target)
    error = lldb.SBError()
    process = target.AttachToProcessWithID(debugger.GetListener(), pid, error)
    print('ATTACH', str(error), flush=True)
    if error.Success():
        for thread in process:
            print(str(thread), flush=True)
            for frame in list(thread)[:40]:
                print(str(frame), flush=True)
        result = lldb.SBCommandReturnObject()
        debugger.GetCommandInterpreter().HandleCommand('image list', result)
        print(result.GetOutput(), result.GetError(), flush=True)
        process.Kill()
    for item in json.loads((root / 'results.json').read_text()):
        if item['result'] == 'timeout':
            subprocess.run(['taskkill', '/PID', str(item['pid']), '/T', '/F'])
else:
    print('No stalled process', flush=True)
