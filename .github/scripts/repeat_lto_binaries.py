"""One independent replication using immutable binaries from a completed run."""
import copy
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess

import verify_lto_inline as probe

ROOT = Path('lto-repeat-results').resolve()
INPUT = Path('lto-repeat-input').resolve()


def main():
    ROOT.mkdir()
    frozen = json.loads((INPUT/'results.json').read_text())
    assert frozen['status'] == 'complete'
    builds = copy.deepcopy(frozen['builds'])
    for name, row in builds.items():
        binary = INPUT/'bin'/name
        assert probe.digest(binary) == row['sha256']
        binary.chmod(0o755)
        row['binary'] = str(binary)
        row['runtimeLibraries'] = probe.capture(['ldd', str(binary)])
        assert 'not found' not in row['runtimeLibraries']
    for path in INPUT.glob('*.asm'):
        shutil.copy2(path, ROOT/path.name)
    probe.ROOT = ROOT
    probe.MEASUREMENT_SEED = 2026111410
    report = dict(status='running', classification='Preplanned independent fixed-binary replication; prior unresolved controls retained',
                  frozenRun=43605504511, compilerBase=frozen['compilerBase'], compilerFix=frozen['compilerFix'],
                  commonRuntime=frozen['commonRuntime'], go=probe.capture(['go','version']), llvm=frozen['llvm'],
                  host=dict(platform=platform.platform(), cpu=probe.capture(['lscpu']), loadBefore=os.getloadavg()),
                  controlRevision=frozen['controlRevision'], builds=builds, cases=[], checks=[])
    probe.save(report)
    try:
        sources = dict(head=Path('fixture-source').resolve(), control=Path('control-source').resolve())
        probe.measure(sources, report)
        report['status'] = 'complete'
    except BaseException as error:
        report.update(status='failed', error=repr(error))
        raise
    finally:
        report['host']['loadAfter'] = os.getloadavg()
        probe.save(report)


if __name__ == '__main__':
    main()
