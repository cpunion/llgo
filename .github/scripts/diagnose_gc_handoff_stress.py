"""Run the published handoff probe repeatedly without retries or test edits."""
import json
from pathlib import Path
import statistics
import subprocess
import time

root = Path('probes').resolve()
exe = root / 'gc-handoff' / 'gc.exe'
results = []
for index in range(100):
    started = time.monotonic()
    with (root / f'handoff-stress-{index}.log').open('w') as output:
        try:
            result = subprocess.run([str(exe)], stdout=output, stderr=output, timeout=90).returncode
        except subprocess.TimeoutExpired:
            result = 'timeout'
    item = {'index': index, 'result': result, 'seconds': time.monotonic() - started}
    results.append(item)
    (root / 'stress-results.json').write_text(json.dumps(results, indent=2))
    print(json.dumps(item), flush=True)
    if result != 0:
        raise SystemExit(1)
print('SUMMARY', json.dumps({'runs': len(results), 'median': statistics.median(r['seconds'] for r in results),
                            'max': max(r['seconds'] for r in results)}), flush=True)
