#!/usr/bin/env python3
"""Exercise the installed compiler's real, checksum-pinned ESP download path."""
import argparse
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import tempfile
import uuid


def run(command, env, cwd, log):
    result = subprocess.run([str(arg) for arg in command], env=env, cwd=cwd,
                            stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                            text=True, timeout=900)
    log.write_text(result.stdout)
    if result.returncode:
        raise RuntimeError(f"{command} exited {result.returncode}:\n{result.stdout[-24000:]}")
    return result.stdout


def validate(root, output):
    pins = json.loads((Path(__file__).resolve().parents[2] /
                       'internal/crosscompile/espressif-windows.json').read_text())
    goos = {'Darwin': 'darwin', 'Linux': 'linux'}[platform.system()]
    arch = {'arm64': 'aarch64', 'aarch64': 'aarch64',
            'x86_64': 'x86_64', 'AMD64': 'x86_64'}[platform.machine()]
    target = arch + {'darwin': '-apple-darwin', 'linux': '-linux-gnu'}[goos]
    version = pins['release']
    binary = root / 'bin/llgo'
    output.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='esp-download-', dir=output) as temporary:
        work = Path(temporary)
        # Keep the installed compiler (and its host LLVM library) intact, but
        # select a runtime root with no bundled ESP tools. This must download.
        runtime_root = work / 'runtime root'
        runtime_root.mkdir()
        for name in ('runtime', 'targets', 'LICENSES'):
            shutil.copytree(root / name, runtime_root / name, symlinks=True)
        source = work / 'firmware source'
        source.mkdir()
        (source / 'go.mod').write_text('module esp-download-smoke\n\ngo 1.27.0\n')
        (source / 'main.go').write_text('package main\nfunc main() {}\n')
        env = os.environ.copy()
        for name in ('LLVM_CONFIG', 'CC', 'CXX', 'CGO_CPPFLAGS', 'CGO_CFLAGS',
                     'CGO_CXXFLAGS', 'CGO_LDFLAGS', 'LD_LIBRARY_PATH',
                     'DYLD_LIBRARY_PATH', 'DYLD_FALLBACK_LIBRARY_PATH', 'GOOS', 'GOARCH'):
            env.pop(name, None)
        env.update(LLGO_ROOT=str(runtime_root), LLGO_BUILD_CACHE='0', GOTOOLCHAIN='local', CGO_ENABLED='1',
                   XDG_CACHE_HOME=str(work / 'empty cache'))
        # Go uses ~/Library/Caches on macOS, independently of XDG_CACHE_HOME.
        # Preserve a preexisting copy while forcing this version to download.
        cache = (Path.home() / 'Library/Caches' if goos == 'darwin'
                 else Path(env['XDG_CACHE_HOME']))
        cached = cache / 'llgo/crosscompile' / f'esp-clang-{version}-{target}'
        saved = cached.with_name(cached.name + '.saved-' + uuid.uuid4().hex)
        if cached.exists():
            cached.rename(saved)
        try:
            results = []
            print(f"Testing download from https://github.com/{pins['repository']}/releases/tag/{version}",
                  flush=True)
            for board, machine in (('esp32-coreboard-v2', 94), ('esp32c3', 243)):
                for mode in ('default', 'full'):
                    name = f'{board}-{mode}'
                    command = [binary, 'build', '-x']
                    if mode == 'full':
                        command.append('-lto=full')
                    command += ['-target', board, '-o', source / (name + '.out'), '.']
                    trace = run(command, env, source, output / (name + '.log'))
                    normalized = trace.replace('\\', '/')
                    if str(cached / 'bin').replace('\\', '/') not in normalized:
                        raise ValueError('Firmware did not use the pinned downloaded toolchain')
                    downloaded = 'ESP Clang not found in LLGO_ROOT or cache, will download.' in trace
                    if downloaded != (len(results) == 0):
                        raise ValueError('Expected one fresh download followed by reuse of that payload')
                    header = (source / (name + '.out.elf')).read_bytes()[:20]
                    if header[:6] != b'\x7fELF\x01\x01' or int.from_bytes(header[18:20], 'little') != machine:
                        raise ValueError('Firmware has the wrong Xtensa/RISC-V ELF machine')
                    results.append(name)
                    print('PASS ' + name, flush=True)
            manifest = (cached / 'LLGO-LLVM-MANIFEST.txt').read_text().splitlines()
            for line in (f'payload_version={version}', f'host_target={target}',
                         'llvm_expected_version=22.1.4', 'payload_profile=runtime'):
                if line not in manifest:
                    raise ValueError('Downloaded payload is missing ' + line)
            for name in ('llvm', 'llvm-c', 'clang', 'clang-c'):
                if (cached / 'include' / name).exists():
                    raise ValueError('Downloaded payload still contains development headers')
            if list((cached / 'lib').glob('libLLVM*.a')) or list((cached / 'lib').glob('libclang*.a')):
                raise ValueError('Downloaded payload still contains development libraries')
            if not (cached / 'LICENSE-LLVM.txt').is_file():
                raise ValueError('Downloaded payload is missing its LLVM license')
            (output / 'results.json').write_text(json.dumps({
                'repository': pins['repository'], 'release': version, 'target': target,
                'firmware': results, 'status': 'passed',
            }, indent=2) + '\n')
        finally:
            if cached.exists():
                shutil.rmtree(cached)
            if saved.exists():
                saved.rename(cached)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('root', type=Path)
    parser.add_argument('--output', required=True, type=Path)
    args = parser.parse_args()
    validate(args.root.resolve(), args.output.resolve())
