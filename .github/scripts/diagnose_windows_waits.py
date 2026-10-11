import concurrent.futures
import json
import os
from pathlib import Path
import subprocess
import sys
import threading
import time

root = Path(os.environ['WAIT_PROBE_DIR'])
exe = root / 'demos' / '0003' / 'demo.exe'
if '--demo' in sys.argv:
    environment_keys = ['PATH', 'PYTHONHOME', 'PYTHONPATH', 'LLGO_WINDOWS_ABI',
                        'LLGO_WINDOWS_PYTHON_HOME', 'LLGO_WINDOWS_TARGET_PYTHON_HOME',
                        'PKG_CONFIG_PATH', 'CC', 'CXX', 'GOARCH', 'GOOS']
    (root / 'target-environment.json').write_text(json.dumps(
        {key: os.environ.get(key) for key in environment_keys}, indent=2))
    p = Path('.github/workflows/test_demo.sh')
    source = p.read_text()
    source = source.replace('tmp_root="$(mktemp -d)"', 'tmp_root="$PWD/.artifacts/waits/demos"\nmkdir -p "$tmp_root"')
    source = "\n".join(": # Preserve diagnostic files" if line.startswith("trap ") else line for line in source.splitlines())
    p.write_text(source)
    with (root / 'demo-harness.log').open('w') as log:
        harness = subprocess.Popen([os.environ['DEMO_BASH'], str(p)], stdout=log, stderr=log)
        started = time.monotonic()
        while time.monotonic() - started < 240 and harness.poll() is None:
            time.sleep(10)
            pending = []
            for logfile in (root / 'demos').glob('*.log'):
                text = logfile.read_text(errors='replace')
                if not text.rstrip().endswith(('PASS','FAIL')):
                    pending.append({'file':str(logfile), 'tail':text[-400:]})
            print(json.dumps({'seconds':time.monotonic()-started,'pending':pending}),flush=True)
        result={'result':harness.poll(),'pid':harness.pid,'seconds':time.monotonic()-started}
        (root/'harness-result.json').write_text(json.dumps(result,indent=2))
        if harness.poll() is None:
            processes = subprocess.check_output(['powershell.exe','-NoProfile','-Command','Get-CimInstance Win32_Process | Select-Object ProcessId,ParentProcessId,Name,ExecutablePath,CommandLine | ConvertTo-Json -Compress'],text=True,encoding='utf-8',errors='replace')
            (root/'processes.json').write_text(processes)
            allps=json.loads(processes)
            pids=[item['ProcessId'] for item in allps if item['ExecutablePath'] and Path(item['ExecutablePath'])==exe]
            (root/'stalled-pids.json').write_text(json.dumps(pids))
            print('STALL PIDS',pids,flush=True)
    sys.exit(0)

import lldb
if (root / 'stalled-pids.json').exists():
    debugger = lldb.debugger
    debugger.SetAsync(False)
    for pid in json.loads((root / 'stalled-pids.json').read_text()):
        target = debugger.CreateTarget(str(exe))
        debugger.SetSelectedTarget(target)
        error = lldb.SBError()
        process = target.AttachToProcessWithID(debugger.GetListener(), pid, error)
        print('ATTACH',pid,str(error),flush=True)
        if error.Success():
            for thread in process:
                print(str(thread),flush=True)
                for frame in list(thread)[:40]:
                    print(str(frame),flush=True)
            result = lldb.SBCommandReturnObject()
            debugger.GetCommandInterpreter().HandleCommand('image list',result)
            print(result.GetOutput(),result.GetError(),flush=True)
            process.Detach()
    result=json.loads((root/'harness-result.json').read_text())
    subprocess.run(['taskkill','/PID',str(result['pid']),'/T','/F'])
else:
    print('No stalled process',flush=True)
    debugger = lldb.debugger
    debugger.SetAsync(False)
    target = debugger.CreateTarget(str(exe))
    debugger.SetSelectedTarget(target)
    entry = target.GetModuleAtIndex(0).GetObjectFileEntryPointAddress()
    breakpoint = target.BreakpointCreateBySBAddress(entry)
    print('ENTRY',str(entry),'LOCATIONS',breakpoint.GetNumLocations(),flush=True)
    launch = target.GetLaunchInfo()
    environment = json.loads((root / 'target-environment.json').read_text())
    launch.SetEnvironmentEntries(['PATH='+environment['PATH']],True)
    error = lldb.SBError()
    process = target.Launch(launch,error)
    print('LOAD WITH DEMO PATH',str(error),flush=True)
    if error.Success():
        result = lldb.SBCommandReturnObject()
        debugger.GetCommandInterpreter().HandleCommand('image list',result)
        print(result.GetOutput(),result.GetError(),flush=True)
        process.Kill()
