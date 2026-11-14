"""Replay frozen native binaries with the existing loop-scoped Callgrind protocol."""
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import sys
import time

INPUT = Path(os.environ.get('FROZEN_INPUT', 'nil-callgrind-input')).resolve()
SOURCE = Path(os.environ.get('FIXTURE_SOURCE', 'fixture-source')).resolve()
OUT = Path(os.environ.get('PROFILE_OUTPUT', 'nil-callgrind-results')).resolve()
SYMBOL = 'github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop'
SMOKE = '--smoke' in sys.argv


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def capture(command):
    return subprocess.check_output(command, text=True, timeout=30).strip()


def save(report):
    temporary = OUT / 'results.next.json'
    temporary.write_text(json.dumps(report, indent=2) + '\n')
    temporary.replace(OUT / 'results.json')


def main():
    OUT.mkdir(exist_ok=False)
    original = json.loads((INPUT / 'results.json').read_text())
    assert original['status'] == 'complete'
    assert original['compilerBase'] == '44756a542fbb6173826c8fcb1d12d3ef5e4d777a'
    assert original['compilerFix'] == 'e57dd9ec011fa8a039c14ea6406de1d7208bcc15'
    os.environ['GOMAXPROCS'] = '1'
    os.sched_setaffinity(0, {min(os.sched_getaffinity(0))})
    report = dict(status='running', classification='Functional smoke' if SMOKE else
        'Instruction/cache-model diagnostic; not native throughput or PMU events',
        frozenRun=43590417831, compilerBase=original['compilerBase'], compilerFix=original['compilerFix'],
        referenceHarness='cpunion/wasmvm@0ddf028fa79f2b254818fff2632b714d231ee6cc/benchmarks/llgo_regression_profile.py',
        scope='Collection toggles only in runSlotsLoop, covering first and one measured call; normal GC/checks/polling retained.',
        caveat='Dr/Dw are VEX instrumentation events, including temporary stack accesses for register bit tests. Attribute real spills only with disassembly.',
        host=dict(platform=platform.platform(), cpu=capture(['lscpu']), affinity=sorted(os.sched_getaffinity(0))),
        valgrind=None if SMOKE else capture(['valgrind', '--version']), builds={}, profiles=[])
    try:
        for name in ['llgo-main-base', 'llgo-fix-base']:
            old = original['builds'][name]
            binary = INPUT / 'bin' / name
            assert old['source'] == '5e5e6a44b2062d61bfa569f384232483763d3380'
            assert sha(binary) == old['sha256']
            binary.chmod(0o755)
            libraries = capture(['ldd', str(binary)])
            assert 'not found' not in libraries
            library_paths = set(re.findall(r'/[^\s()]+', libraries))
            report['builds'][name] = dict(old, binary=str(binary), libraries=libraries,
                librarySha256={p: sha(Path(p)) for p in sorted(library_paths) if Path(p).is_file()})
            tool = shutil.which('llvm-objdump') or shutil.which('llvm-objdump-18') or shutil.which('objdump')
            assert tool
            args = [tool, '-d', '--no-show-raw-insn']
            args += (['--x86-asm-syntax=intel', '--disassemble-symbols=' + SYMBOL]
                     if 'llvm-' in Path(tool).name else ['-M', 'intel', '--disassemble=' + SYMBOL])
            (OUT / (name + '.asm')).write_text(capture(args + [str(binary)]) + '\n')
        save(report)
        wanted = ['core/integer-loop', 'memory/dependent-address']
        for case_id in wanted:
            fixture = next(c['fixture'] for c in original['cases'] if c['fixture']['id'] == case_id)
            relative = fixture['path'].split('/source-head/', 1)[1]
            assert not Path(relative).is_absolute() and '..' not in Path(relative).parts
            wasm = SOURCE / relative
            assert sha(wasm) == fixture['wasmSha256']
            observed = []
            for label, name in [('main', 'llgo-main-base'), ('fix', 'llgo-fix-base'), ('main-aa', 'llgo-main-base')]:
                stem = case_id.replace('/', '-') + '-' + label
                profile = OUT / (stem + '.callgrind')
                command = [] if SMOKE else ['valgrind', '--tool=callgrind', '--collect-atstart=no',
                    '--toggle-collect=' + SYMBOL, '--dump-instr=yes', '--cache-sim=yes', '--branch-sim=yes',
                    '--callgrind-out-file=' + str(profile)]
                command += [report['builds'][name]['binary'], '--mode', 'api', '--backend', 'interpreter',
                    '--wasm', str(wasm), '--iterations', str(fixture['iterations']), '--seed', str(fixture['seed']),
                    '--expected', str(fixture['expectedChecksum']), '--samples', '1', '--warmups', '0']
                started = time.monotonic()
                result = subprocess.run(command, input='run\n', capture_output=True, text=True, timeout=180)
                (OUT / (stem + '.stdout')).write_text(result.stdout)
                (OUT / (stem + '.stderr')).write_text(result.stderr)
                assert result.returncode == 0, result.stderr[-2000:]
                record = json.loads(result.stdout.splitlines()[-1])
                assert record['compiler'] == 'llgo' and record['sourceRevision'] == 'development'
                assert record['result'] == fixture['expectedChecksum']
                assert record['warmResults'] == [fixture['expectedChecksum']] and record['warmupResults'] == []
                row = dict(case=case_id, role=label, binarySha256=report['builds'][name]['sha256'],
                    fixture=fixture, command=command, record=record, diagnosticWallSeconds=time.monotonic()-started)
                if not SMOKE:
                    lines = profile.read_text().splitlines()
                    events = next(s for s in lines if s.startswith('events:')).split()[1:]
                    totals = next(s for s in lines if s.startswith('summary:')).split()[1:]
                    row['events'] = dict(zip(events, map(int, totals), strict=True))
                    assert row['events']['Ir'] > 1000000, 'loop collection did not start'
                report['profiles'].append(row)
                observed.append(row)
                save(report)
                print(case_id, label, json.dumps(row.get('events', {'functional': 'passed'})), flush=True)
            if not SMOKE:
                assert observed[0]['events']['Ir'] == observed[-1]['events']['Ir'], 'identical-binary instruction counts differ'
        report['status'] = 'complete'
    except BaseException as error:
        report.update(status='failed', error=repr(error))
        raise
    finally:
        save(report)


if __name__ == '__main__':
    main()
