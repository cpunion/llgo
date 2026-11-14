"""Attribute the exact adverse binaries with software CPU-clock samples."""
import collections
import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess

ROOT = Path('lto-clock-results').resolve()
INPUT = Path('lto-clock-input').resolve()
SAMPLES = 1500


def capture(command, **kwargs):
    return subprocess.check_output(command, text=True, timeout=180, **kwargs).strip()


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def save(report):
    (ROOT/'results.json').write_text(json.dumps(report, indent=2)+'\n')


def main():
    ROOT.mkdir()
    frozen = json.loads((INPUT/'results.json').read_text())
    report = dict(status='preflight', frozenRun=43599604900,
                  compilerBase=frozen['compilerBase'], compilerFix=frozen['compilerFix'],
                  classification='Software CPU-clock attribution; not PMU counters or uninstrumented throughput',
                  host=dict(platform=platform.platform(), cpu=capture(['lscpu'])), profiles=[])
    save(report)
    try:
        tools = sorted(Path('/usr/lib/linux-tools').rglob('perf'))
        perf = next((p for p in tools if p.is_file() and os.access(p, os.X_OK)), None)
        if perf is None:
            raise RuntimeError('No real perf executable from linux-tools-generic')
        report['perf'] = capture([str(perf), '--version'])
        preflight = subprocess.run(['sudo', str(perf), 'stat', '-e', 'cpu-clock:u', '--', 'true'],
                                   capture_output=True, text=True, timeout=30)
        (ROOT/'preflight.log').write_text(preflight.stdout+preflight.stderr)
        if preflight.returncode:
            report['status'] = 'software-event-unavailable'
            raise RuntimeError('Software CPU-clock event unavailable; see preflight.log')
        affinity = min(os.sched_getaffinity(0))
        report['affinity'] = affinity
        cases = [c for c in frozen['cases'] if c['fixture']['id'] in
                 {'core/integer-loop', 'memory/dependent-address'}]
        assert len(cases) == 2
        for case in cases:
            fixture = case['fixture']
            path = Path('fixture-source')/fixture['path'].split('/source-head/', 1)[1]
            assert sha(path) == fixture['wasmSha256']
            for role in ('main', 'fix', 'nocold', 'main-aa'):
                name = 'llgo-'+role.removesuffix('-aa')+'-base'
                row = frozen['builds'][name]
                binary = INPUT/'bin'/name
                assert sha(binary) == row['sha256']
                assert binary.read_bytes()[:4] == b'\x7fELF'
                assert int.from_bytes(binary.read_bytes()[16:18], 'little') == 2, 'Require ET_EXEC for raw IP attribution'
                binary.chmod(0o755)
                assert 'not found' not in capture(['ldd', str(binary)])
                symbol = row['dispatcherSymbol'][0].split()
                begin, size = int(symbol[0],16), int(symbol[1],16)
                label = fixture['id'].replace('/','-')+'-'+role
                output = ROOT/(label+'.perf')
                args = [str(binary), '--mode', 'api', '--backend', 'interpreter',
                        '--wasm', str(path.resolve()), '--iterations', str(fixture['iterations']),
                        '--seed', str(fixture['seed']), '--expected', str(fixture['expectedChecksum']),
                        '--samples', str(SAMPLES), '--warmups', '0']
                command = ['sudo', 'env', 'GOMAXPROCS=1', str(perf), 'record', '--no-buildid',
                           '-e', 'cpu-clock:u', '-F', '1999', '-o', str(output), '--',
                           'taskset', '-c', str(affinity), *args]
                result = subprocess.run(command, input='run\n'*SAMPLES, text=True,
                                        capture_output=True, timeout=180)
                (ROOT/(label+'.stdout')).write_text(result.stdout)
                (ROOT/(label+'.stderr')).write_text(result.stderr)
                result.check_returncode()
                records = [json.loads(line) for line in result.stdout.splitlines() if line.startswith('{')]
                record = records[-1]
                assert record['compiler'] == 'llgo' and record['sourceRevision'] == 'development'
                assert record['result'] == fixture['expectedChecksum']
                assert len(record['warmResults']) == SAMPLES
                assert set(record['warmResults']) == {fixture['expectedChecksum']}
                subprocess.run(['sudo','chmod','a+r',str(output)],check=True)
                addresses = capture([str(perf),'script','-i',str(output),'-F','ip'])
                (ROOT/(label+'.ips')).write_text(addresses+'\n')
                all_ips = [int(line.strip(),16) for line in addresses.splitlines() if line.strip()]
                hits = collections.Counter(ip for ip in all_ips if begin <= ip < begin+size)
                assert sum(hits.values()) > 1000, 'Insufficient dispatcher samples'
                disassembly = capture(['objdump','-d','-M','intel','--start-address='+str(begin),
                                       '--stop-address='+str(begin+size),str(binary)])
                (ROOT/(name+'.asm')).write_text(disassembly+'\n')
                profile = dict(case=fixture['id'], role=role, binarySha256=row['sha256'],
                               fixtureSha256=fixture['wasmSha256'], iterations=fixture['iterations'],
                               samples=SAMPLES, command=command, dispatcherBegin=begin, dispatcherSize=size,
                               totalSamples=len(all_ips), dispatcherSamples=sum(hits.values()),
                               ipCounts={hex(k):v for k,v in sorted(hits.items())},
                               top=[dict(ip=hex(ip), offset=hex(ip-begin), samples=count)
                                    for ip,count in hits.most_common(25)])
                report['profiles'].append(profile)
                save(report)
                print(label,'dispatcher samples',sum(hits.values()),flush=True)
        report['status'] = 'complete'
    except BaseException as error:
        report['error'] = repr(error)
        if report['status'] != 'software-event-unavailable':
            report['status'] = 'failed'
        raise
    finally:
        save(report)


if __name__ == '__main__':
    main()
