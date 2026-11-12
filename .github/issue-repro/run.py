import json
import os
from pathlib import Path
import subprocess
import time

root = Path('_issue-reports')
results = []
for name, repetitions, limit in [('issue25897a', 100, 90), ('issue75764', 100, 30)]:
    if name not in os.environ.get('ISSUE_CASES', 'issue25897a issue75764').split():
        continue
    for compiler in ('go', 'main', 'fixed'):
        failures = 0
        for iteration in range((1 if name == 'issue25897a' else 3) if compiler == 'main' else 3 if compiler == 'go' else repetitions):
            started = time.monotonic()
            try:
                process = subprocess.run([str((root / f'{compiler}-{name}.exe').resolve())],
                                         capture_output=True, timeout=limit)
                code = process.returncode
                output = (process.stdout + process.stderr).decode(errors='replace')
            except subprocess.TimeoutExpired as error:
                code = 'timeout'
                output = ((error.stdout or b'') + (error.stderr or b'')).decode(errors='replace')
            row = dict(case=name, compiler=compiler, iteration=iteration + 1,
                       seconds=time.monotonic() - started, code=code, output=output)
            results.append(row)
            print(json.dumps(row), flush=True)
            (root / 'results.json').write_text(json.dumps(results, indent=2))
            if code != 0 or output:
                failures += 1
                if failures >= 2:
                    break
if any((row['code'] != 0 or row['output']) and row['compiler'] != 'main' for row in results):
    raise SystemExit('Candidate or stock-Go cases have recorded failures; see results.json')
