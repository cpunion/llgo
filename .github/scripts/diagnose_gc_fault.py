"""Private diagnostic harness: preserve every failure, never retry to pass."""
import json
import os
from pathlib import Path
import subprocess
import sys
import time

root = Path(os.environ['GC_PROBE_DIR'])
exe = root / 'gc.exe'
rounds = int(os.environ['GC_PROBE_ROUNDS'])

if '--direct' in sys.argv:
    records = []
    timeouts = 0
    for run in range(rounds):
        started = time.monotonic()
        with (root / f'direct-{run}.log').open('w') as log:
            p = subprocess.Popen([str(exe)], cwd=root, stdout=log, stderr=log)
            try:
                p.wait(timeout=90)
                result = p.returncode
            except subprocess.TimeoutExpired:
                p.kill()
                p.wait()
                result = 'timeout'
                timeouts += 1
        item = {'round': run, 'result': result, 'seconds': time.monotonic() - started}
        print(json.dumps(item), flush=True)
        records.append(item)
        if result not in (0, 'timeout') or timeouts == 3:
            break
    (root / 'direct-results.json').write_text(json.dumps(records, indent=2))
    sys.exit(int(any(r['result'] != 0 for r in records)))

import lldb

debugger = lldb.debugger
debugger.SetAsync(True)
target = debugger.CreateTarget(str(exe))
assert target.IsValid(), 'invalid target'
debugger.SetSelectedTarget(target)
bp = target.BreakpointCreateByRegex('panicWindowsException|onWindowsFault')
print('PANIC BREAKPOINT', bp.GetNumLocations(), flush=True)
interpreter = debugger.GetCommandInterpreter()

def command(text):
    result = lldb.SBCommandReturnObject()
    interpreter.HandleCommand(text, result)
    print(text, result.GetOutput(), result.GetError(), flush=True)

command('image list')
records = []
for run in range(2):
    print('ROUND', run, flush=True)
    listener = lldb.SBListener('gc-fault-probe')
    launch = target.GetLaunchInfo()
    launch.SetWorkingDirectory(str(root))
    launch.SetListener(listener)
    launch.AddOpenFileAction(1, str(root / f'debug-{run}.stdout'), False, True)
    launch.AddOpenFileAction(2, str(root / f'debug-{run}.stderr'), False, True)
    error = lldb.SBError()
    process = target.Launch(launch, error)
    print('LAUNCH', str(error), process.GetProcessID(), flush=True)
    assert error.Success() and process.IsValid(), str(error)
    started = time.monotonic()
    result = 'timeout'
    stop_count = 0
    while time.monotonic() - started < 90:
        event = lldb.SBEvent()
        if not listener.WaitForEvent(1, event):
            continue
        if not lldb.SBProcess.EventIsProcessEvent(event):
            continue
        state = lldb.SBProcess.GetStateFromEvent(event)
        if state == lldb.eStateExited:
            result = process.GetExitStatus()
            break
        if state != lldb.eStateStopped or lldb.SBProcess.GetRestartedFromEvent(event):
            continue
        stop_count += 1
        thread = process.GetSelectedThread()
        print('STOP', process.GetNumThreads(), thread.GetStopDescription(512), flush=True)

        for thread in process:
            print(str(thread), flush=True)
            for frame in list(thread)[:24]:
                print(str(frame), flush=True)
        command('register read')
        command('frame variable')
        command('disassemble --frame')
        command('memory read --format x --size 4 --count 32 $esp')
        if stop_count >= 12:
            result = 'too-many-exceptions'
            break
        print('CONTINUE', str(process.Continue()), flush=True)
    if process.GetState() != lldb.eStateExited:
        process.Stop()
        for _ in range(20):
            if process.GetState() == lldb.eStateStopped:
                break
            time.sleep(0.05)
        for thread in process:
            print(str(thread), flush=True)
            for frame in list(thread)[:24]:
                print(str(frame), flush=True)
        process.Kill()
    item = {'round': run, 'result': result, 'seconds': time.monotonic() - started, 'stops': stop_count}
    records.append(item)
    print(json.dumps(item), flush=True)
    if result not in (0, 'timeout'):
        break
(root / 'debug-results.json').write_text(json.dumps(records, indent=2))
print('GC_PROBE_DONE', flush=True)
