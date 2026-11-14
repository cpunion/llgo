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
probe.ROOT = ARTIFACT/'pr3107-native'
probe.ROOT.mkdir(exist_ok=False)
probe.MEASUREMENT_SEED = 2026111506
source_base = Path('/private/tmp/wasmvm-3098-base-20261114')
sources = dict(base=source_base, head=Path('/private/tmp/wasmvm-3098-compact-20261114'),
               control=Path('/private/tmp/wasmvm-llgo-branch-boundaries-20261114'))
assert probe.capture(['git','-C',str(source_base),'rev-parse','HEAD']) == probe.BASE
assert probe.capture(['git','-C',str(sources['head']),'rev-parse','HEAD']) == probe.HEAD
assert probe.capture(['git','-C',str(sources['control']),'rev-parse','HEAD']) == probe.CONTROL
report = dict(status='running', compilerBase='6a482f7f6ad94e9f2d0a0fea97dd4cabd46b1130',
              compilerFix='a582e400267716ae3b49ef97e63ecda503ad16d9',
              commonRuntime='6a482f7f6ad94e9f2d0a0fea97dd4cabd46b1130',
              go=probe.capture(['go','version']), llvm=probe.capture(['clang','--version']),
              host=dict(platform=platform.platform(), cpu=probe.capture(['sysctl','-n','machdep.cpu.brand_string']),loadBefore=os.getloadavg()),
              source=probe.BASE, controlRevision=probe.CONTROL, builds={}, cases=[], checks=[])
probe.save(report)
try:
    env = dict(os.environ, LLGO_ROOT='/private/tmp/llgo-main-6a-native-control-20261115',
               LLGO_BUILD_CACHE='off', GOTOOLCHAIN='local', GOWORK='off', CGO_ENABLED='1',
               GOFLAGS='', GOEXPERIMENT='', CCFLAGS='', CFLAGS='', LDFLAGS='')
    for name in ['CGO_CPPFLAGS','CGO_CXXFLAGS','CGO_LDFLAGS','CGO_CFLAGS']:
        env.pop(name,None)
    compilers = [('go-base',shutil.which('go')),('llgo-main-base',str(ARTIFACT/'llgo-darwin-entry-base')),
                 ('llgo-entry-base',str(ARTIFACT/'llgo-darwin-entry-close-qualified')),
                 ('llgo-fix-base',str(ARTIFACT/'llgo-pr3107-a582e4002'))]
    bins = probe.ROOT/'bin'
    bins.mkdir()
    for name, compiler in compilers:
        binary = probe.ROOT/'linked-runner'
        command = [compiler,'build']
        if name.startswith('llgo-'):
            command += ['-O3','-lto=full']
        command += ['-o',str(binary),'./benchmarks/embedded/wasmvm']
        started = time.monotonic()
        output = bins/name
        frozen_name = 'llgo-fix-base' if name == 'llgo-entry-base' else name
        if name != 'llgo-fix-base':
            frozen = json.loads((ARTIFACT/'darwin-entry-native/results.json').read_text())
            frozen_row = frozen['builds'][frozen_name]
            frozen_binary = ARTIFACT/'darwin-entry-native/bin'/frozen_name
            assert probe.digest(frozen_binary) == frozen_row['sha256']
            shutil.copy2(frozen_binary,output)
        else:
            with (probe.ROOT/(name+'-build.log')).open('w') as log:
                subprocess.run(command,cwd=source_base,env=env,stdout=log,stderr=subprocess.STDOUT,timeout=300,check=True)
            shutil.copy2(binary,output)
        row = dict(command=command,source=probe.BASE,compiler=compiler,compilerSha256=probe.digest(Path(compiler)),
                   optimization='O3 Full LTO' if name.startswith('llgo-') else 'Go default',
                   sha256=probe.digest(output),size=output.stat().st_size,binary=str(output),compileSeconds=time.monotonic()-started)
        if name.startswith('llgo-'):
            nm=probe.capture(['llvm-nm','-n',str(output)])
            row['dispatcherSymbol']=[line for line in nm.splitlines() if line.endswith('_'+probe.SYMBOL)]
            asm=probe.capture(['llvm-objdump','-d','--no-show-raw-insn','--disassemble-symbols=_'+probe.SYMBOL,str(output)])
            (probe.ROOT/(name+'.asm')).write_text(asm+'\n')
        if name != 'llgo-fix-base':
            row.update(reusedFrom=str(frozen_binary), buildCommandExecuted=False, compileSeconds=None)
        else:
            row['buildCommandExecuted']=True
        report['builds'][name]=row
        probe.save(report)
        print('built',name,row['size'],flush=True)
    probe.measure(sources,report)
    report['status']='complete'
except BaseException as error:
    report.update(status='failed',error=repr(error))
    raise
finally:
    report['host']['loadAfter']=os.getloadavg()
    probe.save(report)
