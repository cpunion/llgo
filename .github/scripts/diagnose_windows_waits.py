import os
from pathlib import Path
import subprocess
import sys
import time

root = Path(os.environ['WAIT_PROBE_DIR'])
exe = root / 'commandrun.exe'
if '--direct' in sys.argv:
    for i in range(10):
        with (root / f'commandrun-direct-{i}.log').open('w') as log:
            process = subprocess.Popen([str(exe)], stdout=log, stderr=log)
            try:
                process.wait(timeout=15)
                print(i, process.returncode, flush=True)
                if process.returncode:
                    break
            except subprocess.TimeoutExpired:
                subprocess.run(['taskkill', '/PID', str(process.pid), '/T', '/F'])
                print(i, 'timeout', flush=True)
                break
    sys.exit(0)

import lldb
debugger = lldb.debugger
debugger.SetAsync(True)
target = debugger.CreateTarget(str(exe))
debugger.SetSelectedTarget(target)
listener = lldb.SBListener('wait-probe')
launch = target.GetLaunchInfo()
launch.SetListener(listener)
launch.AddOpenFileAction(1, str(root / 'commandrun-debug.stdout'), False, True)
launch.AddOpenFileAction(2, str(root / 'commandrun-debug.stderr'), False, True)
error = lldb.SBError()
process = target.Launch(launch, error)
print('LAUNCH', str(error), flush=True)
assert error.Success(), str(error)
started = time.monotonic()
while time.monotonic() - started < 15:
    event = lldb.SBEvent()
    if not listener.WaitForEvent(1, event):
        continue
    if not lldb.SBProcess.EventIsProcessEvent(event):
        continue
    state = lldb.SBProcess.GetStateFromEvent(event)
    if state == lldb.eStateExited:
        print('EXIT', process.GetExitStatus(), flush=True)
        break
    if state == lldb.eStateStopped and not lldb.SBProcess.GetRestartedFromEvent(event):
        print('STOP', str(process.GetSelectedThread()), flush=True)
        process.Continue()
if process.GetState() != lldb.eStateExited:
    process.Stop()
    for _ in range(20):
        if process.GetState() == lldb.eStateStopped:
            break
        time.sleep(.05)
    for thread in process:
        print(str(thread), flush=True)
        for frame in list(thread)[:32]:
            print(str(frame), flush=True)
    process.Kill()
