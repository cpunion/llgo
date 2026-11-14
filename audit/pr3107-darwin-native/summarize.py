"""Keep paired intervals and each affected same-binary control together."""
import json
import math
from pathlib import Path
import random
import statistics
import sys

PAIRS = {
    'scalar-fix/main': ('llgo-fix-head', 'llgo-main-head'),
    'original-fix/main': ('llgo-fix-base', 'llgo-main-base'),
    'scalar-alignment/main': ('llgo-align-head', 'llgo-main-head'),
    'original-alignment/main': ('llgo-align-base', 'llgo-main-base'),
    'scalar-fix/alignment': ('llgo-fix-head', 'llgo-align-head'),
    'original-fix/alignment': ('llgo-fix-base', 'llgo-align-base'),
    'O2-fix/main': ('llgo-fix-O2', 'llgo-main-O2'),
    'Go-scalar/original': ('go-head', 'go-base'),
}

def summarize(folder):
    report = json.loads((folder/'results.json').read_text())
    assert report['status'] == 'complete'
    cpu = next((l.split(':',1)[1].strip() for l in report['host']['cpu'].splitlines() if 'Model name:' in l), report['host']['cpu'].strip())
    result = dict(cpu=cpu, compilerBase=report['compilerBase'], compilerFix=report['compilerFix'], cases=[])
    for case in report['cases']:
        controls = {r['runtime'].removeprefix('aa-'): r for r in case['calibration']}
        values = {(row['runtime'], row['block']): statistics.median(row['record']['warmCallNs'])
                  for row in case['observations']}
        blocks = report['protocol']['blocks']
        row = dict(id=case['fixture']['id'], comparisons={}, nsPerIteration={
            name: value/case['fixture']['iterations'] for name,value in case['warmMedianNs'].items()})
        for label, (numerator, denominator) in PAIRS.items():
            if numerator not in controls or denominator not in controls:
                continue
            logs = [math.log(values[numerator,k]/values[denominator,k]) for k in range(blocks)]
            rng = random.Random(20261114)
            draws = sorted(math.exp(statistics.mean(rng.choices(logs,k=blocks))) for _ in range(8000))
            row['comparisons'][label] = dict(ratio=math.exp(statistics.mean(logs)), lower=draws[200],
                upper=draws[-201], controlsResolved=all(controls[n]['resolved'] for n in [numerator,denominator]),
                controls={name: controls[name] for name in [numerator,denominator]})
        result['cases'].append(row)
    (folder/'paired-summary.json').write_text(json.dumps(result,indent=2)+'\n')
    print(cpu)
    for row in result['cases']:
        print(row['id'])
        for name, r in row['comparisons'].items():
            print(' ',name, f"{r['ratio']:.5f} [{r['lower']:.5f}, {r['upper']:.5f}]", 'AA-PASS' if r['controlsResolved'] else 'AA-UNRESOLVED')

if __name__=='__main__':
    for folder in sys.argv[1:]:summarize(Path(folder))
