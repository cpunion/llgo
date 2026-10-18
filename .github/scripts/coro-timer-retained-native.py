import pathlib, hashlib, subprocess, os, time, json
out = pathlib.Path(os.environ['DIAG_OUT']); out.mkdir(exist_ok=True, parents=True)
image = pathlib.Path(os.environ['DIAG_IMAGE'])
expected = 'e6f276e8309521c8b5b56c94979bc39702e4f74bf9ebac10fdd6189a52561e6e'
assert hashlib.sha256(image.read_bytes()).hexdigest() == expected
rows = []
for i in range(1, 1001):
    log = out / ('unmodified-%04d.log' % i)
    start = time.monotonic()
    with log.open('xb') as f:
        try:
            p = subprocess.run([str(image)], env=os.environ | {'GOMAXPROCS': '2', 'GOMEMLIMIT': '2GiB'}, stdout=f, stderr=subprocess.STDOUT, timeout=30)
            code = p.returncode
        except subprocess.TimeoutExpired: code = 'timeout30s'
    row = {'run': i, 'exit': code, 'seconds': time.monotonic()-start, 'log': str(log), 'sha256': hashlib.sha256(log.read_bytes()).hexdigest()}
    rows.append(row)
    if code != 0:
        print('UNMODIFIED FAIL', json.dumps(row), log.read_text(), flush=True)
        break
    if i % 50 == 0: print('UNMODIFIED PASS', i, flush=True)
assert hashlib.sha256(image.read_bytes()).hexdigest() == expected
(out / 'unmodified-result.json').write_text(json.dumps({'image_sha256': expected, 'rows': rows, 'policy': 'No tracing or source patch. A passing streak does not resolve the original failure.'}, indent=2))
