#!/usr/bin/env python3
"""Run one complete compiler/build package with exact inventory and owned cleanup."""

import argparse
import collections
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import signal
import struct
import subprocess
import tempfile
import time
import traceback

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--package', choices=['./cl', './internal/build'], required=True)
parser.add_argument('--reports', type=Path, required=True)
args = parser.parse_args()
SOURCE = Path.cwd()
OUT = args.reports
OUT.mkdir(parents=True, exist_ok=False)
HERE = OUT
results = []
all_seen = {}
env = dict(os.environ)
source = {}
tools = {}
source_status = None
owned_tmp = None


def sha(path):
    with Path(path).open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def sources():
    names = subprocess.check_output(
        ['git', 'ls-files', '-c', '-o', '--exclude-standard', '-z']).split(b'\0')
    return {
        os.fsdecode(name): sha(SOURCE / os.fsdecode(name))
        for name in names
        if name and (SOURCE / os.fsdecode(name)).is_file()
    }


def final_source_evidence():
    errors = {}

    def observe(name, action):
        try:
            value = action()
            if value is None:
                errors[name] = 'returned None'
            return value
        except BaseException as error:
            errors[name] = repr(error)
            return None

    after_source = observe('source', sources)
    after_status = observe('status', lambda: subprocess.check_output(
        ['git', 'status', '--porcelain', '--untracked-files=all'], text=True))
    after_head = observe('head', lambda: subprocess.check_output(
        ['git', 'rev-parse', 'HEAD'], text=True).strip())
    after_tools = {path: observe('tool:' + str(path), lambda: sha(path))
                   for path in tools}
    tool_missing = [path for path in tools
                    if observe('tool_exists:' + str(path), lambda: Path(path).is_file()) is False]
    checks = {
        'source_captured': bool(source),
        'source_unchanged': after_source is not None and after_source == source,
        'status_unchanged': after_status is not None and after_status == source_status,
        'head_unchanged': after_head is not None and after_head == env.get('SOURCE_REF'),
        'tools_unchanged': all(after_tools[path] is not None and after_tools[path] == digest
                               for path, digest in tools.items()),
    }
    return {
        'after_source': after_source,
        'source_added': sorted(after_source.keys() - source.keys()) if after_source is not None else None,
        'source_deleted': sorted(source.keys() - after_source.keys()) if after_source is not None else None,
        'source_changed': sorted(path for path in source.keys() & after_source.keys()
                                 if source[path] != after_source[path]) if after_source is not None else None,
        'expected_head': env.get('SOURCE_REF'), 'after_head': after_head,
        'before_status': source_status, 'after_status': after_status,
        'after_tools': after_tools, 'tool_missing': tool_missing,
        'tool_hash_changed': [path for path, digest in tools.items()
                              if after_tools[path] is not None and after_tools[path] != digest],
        'errors': errors, 'checks': checks, 'source_frozen': all(checks.values()),
    }


def cancelled(signum, frame):
    raise KeyboardInterrupt('signal ' + str(signum))


signal.signal(signal.SIGTERM, cancelled)
signal.signal(signal.SIGINT, cancelled)


# Reuse the qualified process-group and PID/lstart ownership implementation.
def process_table():
    text = subprocess.check_output(
        ['/bin/ps', '-axo', 'pid=,ppid=,pgid=,lstart='], text=True, timeout=10)
    table = {}
    for line in text.splitlines():
        fields = line.split(None, 3)
        if len(fields) == 4:
            pid, ppid, pgid = map(int, fields[:3])
            table[pid] = (ppid, pgid, fields[3])
    return table


def observe(group, seen):
    table = process_table()
    owned = {pid for pid, row in table.items()
             if row[1] == group or seen.get(pid) == row[2]}
    while True:
        children = {pid for pid, row in table.items() if row[0] in owned}
        if children <= owned:
            break
        owned |= children
    for pid in owned:
        seen[pid] = table[pid][2]
        all_seen[pid] = table[pid][2]
    return {pid: table[pid][2] for pid in owned}


def remove_owned(identities):
    # Recheck PID identity before each signal; never kill unrelated processes.
    for sig in [signal.SIGTERM, signal.SIGKILL]:
        table = process_table()
        for pid, identity in identities.items():
            if pid in table and table[pid][2] == identity:
                try:
                    os.kill(pid, sig)
                except ProcessLookupError:
                    pass
        time.sleep(0.2)
    table = process_table()
    return {pid: identity for pid, identity in identities.items()
            if pid in table and table[pid][2] == identity}


def run(name, command, cwd, deadline):
    print('PHASE', name, 'start', flush=True)
    start = time.monotonic()
    error, child, seen, interrupted_owned = None, None, {}, {}
    cancellation = None
    disk_before = shutil.disk_usage(HERE)
    minimum_free = disk_before.free
    required_free = int(env.get('LLGO_DIAGNOSTIC_MIN_FREE_BYTES', '0'))
    assert required_free >= 0, f"explicit disk reserve must be nonnegative, got {required_free}"
    with (OUT / (name + '.stdout')).open('wb') as stdout, (OUT / (name + '.stderr')).open('wb') as stderr:
        try:
            assert disk_before.free >= required_free, 'explicit diagnostic disk reserve reached before spawn'
            child = subprocess.Popen(command, cwd=cwd, env=env, stdout=stdout,
                                     stderr=stderr, start_new_session=True)
            while child.poll() is None:
                observe(child.pid, seen)
                minimum_free = min(minimum_free, shutil.disk_usage(HERE).free)
                assert minimum_free >= required_free, 'explicit diagnostic disk reserve reached'
                assert time.monotonic() - start < deadline, 'bounded stage deadline reached'
                time.sleep(0.5)
        except BaseException as failure:
            error = repr(failure)
            if isinstance(failure, KeyboardInterrupt):
                cancellation = failure
            if child is not None:
                try:
                    interrupted_owned = observe(child.pid, seen)
                except Exception as tracking_error:
                    error += '; process tracking: '+repr(tracking_error)
                try:
                    os.killpg(child.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                child.wait()
    remaining = {}
    if child is not None:
        child.wait()
        for attempt in range(5):
            remaining = observe(child.pid, seen)
            if not remaining:
                break
            time.sleep(0.2)
    before_cleanup = interrupted_owned | remaining
    after_cleanup = remove_owned(before_cleanup) if before_cleanup else {}
    report = {
        'stage': name, 'command': list(map(str, command)),
        'exit': child.returncode if child is not None else None,
        'seconds': time.monotonic() - start, 'interruption': error,
        'observed_owned_pids': sorted(seen),
        'owned_residuals_before_cleanup': before_cleanup,
        'owned_residuals_after_cleanup': after_cleanup,
        'owned_children_drained': not before_cleanup and not after_cleanup,
        'disk_total_bytes': disk_before.total,
        'disk_before_free_bytes': disk_before.free,
        'disk_minimum_observed_free_bytes': minimum_free,
        'disk_after_free_bytes': shutil.disk_usage(HERE).free,
        'optional_explicit_disk_reserve_bytes': required_free,
        'ownership_scope': 'Fresh stage process group plus observed descendant PIDs, identity-bound by lstart; no command-path heuristics',
    }
    report['passed'] = report['exit'] == 0 and error is None and report['owned_children_drained']
    results.append(report)
    (OUT / 'stages.json').write_text(json.dumps(results, indent=2) + '\n')
    print('PHASE', name, 'end', json.dumps(report), flush=True)
    if cancellation is not None:
        raise cancellation
    return report




def metadata_command(command):
    try:
        result = subprocess.run(command, capture_output=True, text=True, timeout=30)
        return {"command": command, "exit": result.returncode,
                "stdout": result.stdout, "stderr": result.stderr}
    except (OSError, subprocess.TimeoutExpired) as error:
        return {"command": command, "exit": None, "error": repr(error)}


def write_preflight():
    snapshot = {
        "expected_source_ref": env.get("SOURCE_REF"),
        "expected_translator_pin": env.get("TRANSLATOR_VERSION"),
        "working_directory": str(SOURCE),
        "driver_sha256": sha(__file__),
        "git_head": metadata_command(["git", "rev-parse", "HEAD"]),
        "git_status": metadata_command([
            "git", "status", "--porcelain", "--untracked-files=all"]),
        "tracked_diff": metadata_command(["git", "diff", "--stat", "HEAD"]),
        "configured_llvm": env.get("LLVM_CONFIG"),
        "tool_paths": {
            name: shutil.which(name, path=env.get("PATH"))
            for name in ["git", "go", "clang", "llvm-config-22",
                         "qemu-system-riscv32", "qemu-system-xtensa"]
        },
    }
    (OUT / "preflight.json").write_text(json.dumps(snapshot, indent=2) + "\n")
    return snapshot


def audit(inventory, actual):
    roots = {
        line for line in (OUT / 'inventory.stdout').read_text().splitlines()
        if re.fullmatch(r'(Test|Example|Fuzz)\w*', line)
    }
    path = OUT / 'actual.jsonl'
    events = []
    if path.exists():
        events = [json.loads(line) for line in path.read_text().splitlines()
                  if line.startswith('{')]
    runs = collections.Counter(event['Test'] for event in events
                               if event.get('Test') and event.get('Action') == 'run')
    terminals = collections.Counter(event['Test'] for event in events
                                    if event.get('Test') and event.get('Action') in {'pass', 'fail', 'skip'})
    counts = collections.Counter(event['Action'] for event in events
                                 if event.get('Test') and event.get('Action') in {'pass', 'fail', 'skip'})
    actual_roots = {name for name in runs if '/' not in name}
    package_passes = [event for event in events
                      if not event.get('Test') and event.get('Action') == 'pass']
    package_failures = [event for event in events
                        if not event.get('Test') and event.get('Action') == 'fail']
    expected_package = 'github.com/xgo-dev/llgo/' + args.package.removeprefix('./')
    skips = []
    for event in events:
        if event.get('Test') and event.get('Action') == 'skip':
            output = ''.join(row.get('Output', '') for row in events
                             if row.get('Test') == event['Test'])
            skips.append({'test': event['Test'], 'output': output,
                          'counterpart': 'Requires explicit target/tool review; never counted as runtime PASS'})
    complete = (bool(roots) and roots == actual_roots and runs == terminals
                and all(count == 1 for count in runs.values())
                and len(package_passes) == 1
                and package_passes[0].get('Package') == expected_package
                and not package_failures and not counts.get('fail')
                and inventory['passed'] and actual['passed'])
    return {'execution_complete': complete, 'roots': sorted(roots),
            'counts': dict(counts), 'missing': sorted(roots - actual_roots),
            'unexpected_roots': sorted(actual_roots - roots), 'exact_terminals': runs == terminals,
            'package_passes': package_passes, 'package_failures': package_failures,
            'skips': skips,
            'all_owners_runtime_pass': complete and not skips,
            'skip_boundary': 'No skip is classified as host-inapplicable by this runner. Missing-tool/configuration skips and cross-platform counterparts require explicit review.'}


failure = None
qualification = None
try:
    preflight = write_preflight()
    assert preflight['git_head']['exit'] == 0, f"cannot read product Git HEAD: {preflight['git_head']}"
    head = preflight['git_head']['stdout'].strip()
    assert head == env.get('SOURCE_REF'), f"product HEAD mismatch: expected {env.get('SOURCE_REF')!r}, actual {head!r}"
    assert preflight['git_status']['exit'] == 0, f"cannot read product source status: {preflight['git_status']}"
    source_status = preflight['git_status']['stdout']
    assert not source_status, f"product source must be clean before compilation; actual status:\n{source_status}"
    source = sources()
    llvm_config = Path(env['LLVM_CONFIG']).resolve()
    llvm_bindir = Path(subprocess.check_output(
        [str(llvm_config), '--bindir'], text=True).strip())
    required_paths = [llvm_config]
    for name in ['clang', 'clang++', 'llc', 'FileCheck']:
        path = llvm_bindir / name
        assert path.is_file() and os.access(path, os.X_OK), 'selected LLVM tool missing: ' + str(path)
        required_paths.append(path.resolve())
    env['PATH'] = str(llvm_bindir) + os.pathsep + env['PATH']
    env['CC'] = str(llvm_bindir / 'clang')
    env['CXX'] = str(llvm_bindir / 'clang++')
    for name in ['go', 'qemu-system-riscv32', 'qemu-system-xtensa']:
        path = shutil.which(name, path=env['PATH'])
        assert path is not None, 'required diagnostic tool missing: ' + name
        required_paths.append(Path(path).resolve())
    for path in required_paths:
        tools[str(path)] = sha(path)
    owned_tmp = Path(tempfile.mkdtemp(prefix='funcval-package-', dir=env['RUNNER_TEMP']))
    go_tmp = owned_tmp / 'go-build'
    go_tmp.mkdir()
    env['TMPDIR'] = str(owned_tmp)
    env['GOTMPDIR'] = str(go_tmp)
    env['LLGO_ROOT'] = str(SOURCE)
    compiler = OUT / 'llgo-diagnostic'
    env['LLGO_TEST_LLGO'] = str(compiler)
    env['CGO_CPPFLAGS'] = subprocess.check_output([env['LLVM_CONFIG'], '--cppflags'], text=True).strip()
    env['CGO_CXXFLAGS'] = '-std=c++17'
    env['CGO_LDFLAGS'] = subprocess.check_output([env['LLVM_CONFIG'], '--ldflags', '--libs', 'all'], text=True).strip().replace('\n', ' ')
    llvm_version = subprocess.check_output([env['LLVM_CONFIG'], '--version'], text=True).strip()
    assert llvm_version.split('.')[0] == '22', f"expected LLVM22, actual {llvm_version!r} via {env['LLVM_CONFIG']}"
    go_version = subprocess.check_output(['go', 'env', 'GOVERSION'], env=env, text=True).strip()
    expected_go = 'go' + (SOURCE / '.go-version').read_text().strip()
    assert go_version == expected_go, f"expected Go {expected_go!r}, actual {go_version!r}"
    selected = json.loads(subprocess.check_output(['go', 'list', '-m', '-json', 'github.com/xgo-dev/plan9asm'], env=env, text=True))
    assert selected.get('Replace', {}).get('Path') == 'github.com/cpunion/plan9asm', f"expected public fork module replacement, actual {selected!r}"
    assert selected.get('Replace', {}).get('Version') == env.get('TRANSLATOR_VERSION'), f"translator pin mismatch: expected {env.get('TRANSLATOR_VERSION')!r}, actual {selected.get('Replace')!r}"
    (OUT / 'source-evidence.json').write_text(json.dumps({
        'head': head, 'source': source, 'control_workflow': env['GITHUB_WORKFLOW_REF'],
        'control_sha': env['GITHUB_SHA'], 'driver_sha256': sha(__file__),
        'go_version': go_version, 'llvm_version': llvm_version,
        'translator': selected, 'tools': tools,
        'ESP_pins': json.loads((SOURCE / 'internal/crosscompile/espressif.json').read_text()),
    }, indent=2) + '\n')
    (OUT / 'go-env.json').write_bytes(subprocess.check_output(['go', 'env', '-json'], env=env))
    build = run('compiler-build', ['go', 'build', '-p=1', '-buildvcs=true', '-o', str(compiler), './cmd/llgo'], SOURCE, 900)
    assert build['passed'], 'actual compiler build failed'
    stat = compiler.stat()
    identity = {'sha256': sha(compiler), 'bytes': stat.st_size, 'mtime_ns': stat.st_mtime_ns,
                'metadata_hash': hashlib.sha256(struct.pack('<QQ', stat.st_mtime_ns, stat.st_size)).hexdigest()}
    version = run('compiler-version', [str(compiler), 'version'], SOURCE, 60)
    assert version['passed'], 'actual compiler version failed'
    identity['actual_version_output'] = (OUT / 'compiler-version.stdout').read_text().strip()
    identity['cache_binding'] = 'Clean Git uses LLGO_VERSION; metadata_hash is separately computed diagnostic data'
    (OUT / 'compiler-identity.json').write_text(json.dumps(identity, indent=2) + '\n')
    (OUT / 'compiler-build-info.txt').write_bytes(subprocess.check_output(['go', 'version', '-m', str(compiler)], env=env))
    inventory = run('inventory', ['go', 'test', '-p=1', '-count=1', '-list=.', args.package], SOURCE, 900)
    assert inventory['passed'], 'actual package inventory failed'
    actual = run('actual', ['go', 'test', '-p=1', '-parallel=2', '-count=1', '-timeout=45m', '-json', args.package], SOURCE, 2940)
    shutil.copyfile(OUT / 'actual.stdout', OUT / 'actual.jsonl')
    qualification = audit(inventory, actual)
except BaseException as error:
    failure = repr(error)
    (OUT / 'failure-traceback.txt').write_text(traceback.format_exc())
finally:
    table = process_table()
    residual = {pid: identity for pid, identity in all_seen.items()
                if pid in table and table[pid][2] == identity}
    remaining = remove_owned(residual) if residual else {}
    drained = not residual and not remaining and all(row['owned_children_drained'] for row in results)
    final_source = final_source_evidence()
    frozen = final_source['source_frozen']
    final_evidence = {'path': 'final-source-evidence.json',
                      'checks': final_source['checks'], 'errors': final_source['errors']}
    try:
        encoded = json.dumps(final_source, indent=2) + '\n'
        (OUT / final_evidence['path']).write_text(encoded)
        final_evidence['sha256'] = hashlib.sha256(encoded.encode()).hexdigest()
    except BaseException as error:
        final_evidence['write_error'] = repr(error)
    removed = False
    if owned_tmp is not None and drained:
        shutil.rmtree(owned_tmp)
        removed = not owned_tmp.exists()
    passed = failure is None and frozen and drained and bool(qualification and qualification['execution_complete'])
    report = {'execution_complete': passed, 'qualification': qualification, 'failure': failure,
              'source_frozen': frozen, 'source_freeze_evidence': final_evidence,
              'owned_children_drained': drained,
              'owned_residuals_before_cleanup': residual, 'owned_residuals_after_cleanup': remaining,
              'owned_TMP_removed': removed, 'stages': results,
              'boundary': 'Complete package execution and runtime coverage are distinct. Platform/tool skips retain explicit reasons and require counterparts; no skip is promoted to PASS.'}
    (OUT / 'receipt.json').write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps(report, indent=2), flush=True)
    if not passed:
        raise SystemExit(1)
