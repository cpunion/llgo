"""Native Darwin qualification using the frozen, checked interleaved protocol."""
import hashlib
import importlib.util
import inspect
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import time

ARTIFACT = Path('/private/tmp/llgo-issue-audit-results-20261113/new-performance-followups')
PROTOCOL = Path('/private/tmp/llgo-lto-inline-performance-20261114/.github/scripts/verify_lto_inline.py')
spec = importlib.util.spec_from_file_location('native_protocol', PROTOCOL)
probe = importlib.util.module_from_spec(spec)
spec.loader.exec_module(probe)
# Keep checksums, ordering, observations and A/A calibration identical. Darwin
# offers no sched_setaffinity; record that limit and validate actual host fields.
source = inspect.getsource(probe.measure)
old = '''    affinity = min(os.sched_getaffinity(0))
    os.sched_setaffinity(0, {affinity})
'''
assert old in source
source = source.replace(old, '')
source = source.replace("    report['affinity'] = sorted(os.sched_getaffinity(0))", "    report['affinity'] = 'Darwin has no sched_setaffinity; same-binary controls retained'")
source = source.replace("goos='linux', goarch='amd64'", "goos='darwin', goarch='arm64'")
exec(compile(source, str(__file__), 'exec'), probe.__dict__)
probe.ROOT = ARTIFACT/'darwin-symbol-fixed-repeat'
probe.ROOT.mkdir(exist_ok=False)
probe.MEASUREMENT_SEED = 2026111508
source_base = Path('/private/tmp/wasmvm-3098-base-20261114')
sources = dict(base=source_base, head=Path('/private/tmp/wasmvm-3098-compact-20261114'),
               control=Path('/private/tmp/wasmvm-llgo-branch-boundaries-20261114'))
assert probe.capture(['git','-C',str(source_base),'rev-parse','HEAD']) == probe.BASE
assert probe.capture(['git','-C',str(sources['head']),'rev-parse','HEAD']) == probe.HEAD
assert probe.capture(['git','-C',str(sources['control']),'rev-parse','HEAD']) == probe.CONTROL
report = dict(status='running', compilerBase='6a482f7f6ad94e9f2d0a0fea97dd4cabd46b1130',
              compilerFix='d53e71fe26659c27635ee676c4d08c938afe4e70',
              commonRuntime='6a482f7f6ad94e9f2d0a0fea97dd4cabd46b1130',
              go=probe.capture(['go','version']), llvm=probe.capture(['clang','--version']),
              host=dict(platform=platform.platform(), cpu=probe.capture(['sysctl','-n','machdep.cpu.brand_string']),loadBefore=os.getloadavg()),
              source=probe.BASE, controlRevision=probe.CONTROL, builds={}, cases=[], checks=[])
probe.save(report)
try:
    frozen_root = ARTIFACT/'darwin-symbol-final-native'
    frozen = json.loads((frozen_root/'results.json').read_text())
    assert frozen['status'] == 'complete'
    bins = probe.ROOT/'bin'
    bins.mkdir()
    for name, original in frozen['builds'].items():
        source_binary = Path(original['binary'])
        assert probe.digest(source_binary) == original['sha256']
        output = bins/name
        shutil.copy2(source_binary,output)
        row = dict(original, binary=str(output), buildCommandExecuted=False,
                   compileSeconds=None, reusedFrom=str(source_binary))
        report['builds'][name]=row
        if name.startswith('llgo-'):
            shutil.copy2(frozen_root/(name+'.asm'),probe.ROOT/(name+'.asm'))
        probe.save(report)
        print('reused',name,row['sha256'],flush=True)
    probe.measure(sources,report)
    report['status']='complete'
except BaseException as error:
    report.update(status='failed',error=repr(error))
    raise
finally:
    report['host']['loadAfter']=os.getloadavg()
    probe.save(report)
