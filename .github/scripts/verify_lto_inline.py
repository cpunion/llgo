"""Fork-only diagnostic; no production compiler or workload changes."""
import hashlib
import json
import os
from pathlib import Path
import platform
import random
import re
import shutil
import subprocess
import sys
import time

BASE = '5e5e6a44b2062d61bfa569f384232483763d3380'
HEAD = '26b788258ac6c280639001ecda0af28159cf8068'
CONTROL = '9fbd8ff1f46e099e58e7d7240c0cd6b95a57ba52'
COMPILER_BASE = 'ac1292206d672c2aeb546780a1d585ad98edbc3d'
BLOCKS = 20
SYMBOL = 'github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop'
ROOT = Path('lto-inline-results').resolve()


def capture(args, **kwargs):
    return subprocess.check_output(args, text=True, timeout=180, **kwargs).strip()


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def save(value):
    temporary = ROOT / 'results.next.json'
    temporary.write_text(json.dumps(value, indent=2) + '\n')
    temporary.replace(ROOT / 'results.json')


def prepare_sources():
    repository = ROOT / 'source.git'
    subprocess.run(['git', 'init', '--bare', str(repository)], check=True)
    subprocess.run(['git', '--git-dir', str(repository), 'fetch', '--no-tags', '--depth=1',
                    'https://github.com/cpunion/wasmvm.git', BASE, HEAD, CONTROL], check=True)
    result = {}
    for role, commit in [('base', BASE), ('head', HEAD), ('control', CONTROL)]:
        folder = ROOT / ('source-' + role)
        subprocess.run(['git', '--git-dir', str(repository), 'worktree', 'add', '--detach',
                        str(folder), commit], check=True)
        assert capture(['git', '-C', str(folder), 'rev-parse', 'HEAD']) == commit
        result[role] = folder
    return result


def build(sources, report):
    env = dict(os.environ, GOTOOLCHAIN='local', GOWORK='off', CGO_ENABLED='1',
               GOFLAGS='', GOEXPERIMENT='', CCFLAGS='', CFLAGS='', LDFLAGS='')
    for name in ['CGO_CPPFLAGS', 'CGO_CXXFLAGS', 'CGO_LDFLAGS', 'CGO_CFLAGS']:
        env.pop(name, None)
    variants = [('go-base', 'base', 'go', ''), ('go-head', 'head', 'go', ''),
                ('llgo-base', 'base', 'llgo', ''), ('llgo-head', 'head', 'llgo', ''),
                ('llgo-nocold', 'head', 'llgo', '-Wl,-mllvm,-cold-callsite-rel-freq=0')]
    binaries = ROOT / 'bin'
    binaries.mkdir()
    for name, role, compiler, flags in variants:
        source = sources[role]
        # The same role always links to the same path. Keep frozen guest
        # provenance labels unchanged; real identities are recorded beside it.
        output = ROOT / ('link-' + role)
        command = [compiler, 'build']
        if compiler == 'llgo':
            command += ['-O3', '-lto=full']
        command += ['-o', str(output), './benchmarks/embedded/wasmvm']
        started = time.monotonic()
        with (ROOT / (name + '-build.log')).open('w') as log:
            subprocess.run(command, cwd=source, env=dict(env, LDFLAGS=flags),
                           stdout=log, stderr=subprocess.STDOUT, timeout=240, check=True)
        binary = binaries / name
        shutil.copy2(output, binary)
        assert not capture(['git', '-C', str(source), 'status', '--porcelain', '--untracked-files=no'])
        row = dict(command=command, linkerDiagnostic=flags, source=BASE if role == 'base' else HEAD,
                   sha256=digest(binary), size=binary.stat().st_size,
                   binary=str(binary), compileSeconds=time.monotonic()-started)
        if compiler == 'llgo':
            nm = capture(['llvm-nm', '-S', str(binary)])
            row['dispatcherSymbol'] = [line for line in nm.splitlines() if line.endswith(SYMBOL)]
            assembly = capture(['llvm-objdump', '-d', '--no-show-raw-insn', '--x86-asm-syntax=intel',
                                '--disassemble-symbols=' + SYMBOL, str(binary)])
            (ROOT / (name + '.asm')).write_text(assembly + '\n')
            row['stackReservation'] = re.findall(r'sub\s+rsp,.*', assembly)[:1]
            row['outlinedHotCalls'] = [line.strip() for line in assembly.splitlines()
                if '\tcall' in line and any(helper in line for helper in
                    ['.(*Execution).poll', '.(*Memory).Load', '.(*Memory).Store', 'runtime.NewSlice2'])]
        report['builds'][name] = row
        save(report)
        print('built', name, row['size'], flush=True)


def measure(sources, report, blocks=BLOCKS):
    # Reuse the existing, hash-pinned ready/sample protocol and paired control
    # implementation instead of introducing a second timing framework.
    sys.path.insert(0, str(sources['control'] / 'benchmarks'))
    import interleaved
    import routing_compare
    import phase_ci
    affinity = min(os.sched_getaffinity(0))
    os.sched_setaffinity(0, {affinity})
    os.environ['GOMAXPROCS'] = '1'
    report['affinity'] = sorted(os.sched_getaffinity(0))
    names = list(report['builds'])
    runtime_names = names + ['aa-' + name for name in names]
    report['protocol'] = dict(blocks=blocks, warmups=2, samples=7, seed=2026111409,
                              controls=['aa-' + name for name in names])
    wanted = {'core/integer-loop', 'core/function-calls', 'core/memory-load-store',
              'memory/dependent-address', 'memory/float64-unaligned'}
    fixtures = routing_compare.fixtures(sources['head'])
    fixtures = [fixture for fixture in fixtures if fixture['id'] in wanted]
    assert {fixture['id'] for fixture in fixtures} == wanted
    rng = random.Random(report['protocol']['seed'])
    for fixture in fixtures:
        fixture = dict(fixture, iterations=100000)
        fixture['expectedChecksum'] = routing_compare.oracle(sources['head'], fixture,
            fixture['seed'], fixture['iterations'])
        path = Path(fixture['path'])
        assert digest(path) == fixture['wasmSha256']
        commands, runtimes = {}, {}
        for name in runtime_names:
            original = name.removeprefix('aa-')
            row = report['builds'][original]
            assert digest(Path(row['binary'])) == row['sha256']
            commands[name] = [row['binary'], '--mode', 'api', '--backend', 'interpreter',
                '--wasm', str(path), '--iterations', str(fixture['iterations']),
                '--seed', str(fixture['seed']), '--expected', str(fixture['expectedChecksum']),
                '--samples', '7', '--warmups', '2']
            runtimes[name] = dict(engine='wasmvm', sourceRevision='development',
                compilerRevision='development', compiler='gc' if original.startswith('go-') else 'llgo',
                backend='interpreter', mode='api', goos='linux', goarch='amd64',
                policy='unmetered; bounded cancellation; checked Core 2.0')
        case = dict(fixture=fixture, observations=[], sampleOrders=[])
        report['cases'].append(case)
        # Independently verify every executable with the alternate seed/count.
        for name, command in commands.items():
            alternate = list(command)
            expected = routing_compare.oracle(sources['head'], fixture, 0xffffffff, 17)
            for flag, value in [('--iterations', 17), ('--seed', 0xffffffff), ('--expected', expected)]:
                alternate[alternate.index(flag)+1] = str(value)
            observation = dict(phase_ci.invoke(alternate, 60), wasmSha256=fixture['wasmSha256'])
            phase_ci.validate_observation(observation, runtimes[name],
                dict(fixture, seed=0xffffffff, iterations=17, expectedChecksum=expected))
            report['checks'].append(dict(runtime=name, fixture=fixture['id'], observation=observation))
        for block in range(blocks):
            rows, orders = interleaved.invoke(commands, 90, rng)
            for name, row in rows.items():
                row = dict(row, wasmSha256=fixture['wasmSha256'])
                phase_ci.validate_observation(row, runtimes[name], fixture)
                case['observations'].append(dict(row, runtime=name, block=block))
            case['sampleOrders'].append(orders)
            save(report)
        case['calibration'] = routing_compare.calibration(case, runtimes, blocks=blocks)
        case['warmMedianNs'] = routing_compare.warm_values(case)
        save(report)
        print('measured', fixture['id'], json.dumps(case['warmMedianNs']), flush=True)


def main():
    ROOT.mkdir(exist_ok=False)
    assert capture(['go', 'version']).startswith('go version go1.27.3 ')
    assert '22.1.8' in capture(['clang', '--version'])
    subprocess.run(['git', 'diff', '--exit-code', COMPILER_BASE, 'HEAD', '--',
                    'cl', 'ssa', 'internal', 'cmd', 'runtime'], check=True)
    report = dict(status='running', classification='Hosted native diagnostic; not full-portfolio acceptance',
                  compilerBase=COMPILER_BASE, diagnosticCommit=capture(['git', 'rev-parse', 'HEAD']),
                  compilerSha256=digest(Path(shutil.which('llgo'))), go=capture(['go', 'version']),
                  llvm=capture(['clang', '--version']), host=dict(platform=platform.platform(),
                      cpu=capture(['lscpu']), loadBefore=os.getloadavg()),
                  controlRevision=CONTROL, builds={}, cases=[], checks=[])
    save(report)
    try:
        sources = prepare_sources()
        build(sources, report)
        measure(sources, report)
        report['status'] = 'complete'
    except BaseException as error:
        report.update(status='failed', error=repr(error))
        raise
    finally:
        report['host']['loadAfter'] = os.getloadavg()
        save(report)


if __name__ == '__main__':
    main()
