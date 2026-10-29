"""Repackage the original 1.0.9 compilers without changing released assets."""
import hashlib
import importlib.util
import json
import os
from datetime import datetime, timezone
from pathlib import Path
import shutil
import subprocess
import tempfile
import time
import zipfile

HERE = Path(__file__).resolve().parent
ROOT = HERE.parent.parent
VERSION = '1.0.9'
SOURCE_COMMIT = 'b2e1d4e84fcccdc893b77a652c2f5c9b2fc65836'
ORIGINALS = {
    ('386', 'msvc'): '358a6b9bec7cf8658c2c189dc3523f63c0a81de8faf849a543250f9b9b7e7c90',
    ('amd64', 'msvc'): '1ec2f580b8dd3062f9c3158b1243b5a65f8e24119b89858330b3f870b9b38b6c',
    ('arm64', 'msvc'): '9f07e1d223fcbe0c205409014a6f0e95c65832220d7451bd58a039a710a9a955',
    ('386', 'mingw'): '45f0eb449256505dae1840c54656b1ab862ea9eee234ef953d972707c30845a2',
    ('amd64', 'mingw'): 'e8f19968fc901398f319d7d96734da4ab691dbf33eff32c6974d4a5bedff7310',
    ('arm64', 'mingw'): '57bfceadc6765a2c51f2e18b77d4b9edca10b9d5e598ec38e20d56256cd4bae2',
}


def load(name):
    spec = importlib.util.spec_from_file_location(name, HERE / f'{name}.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def checksum(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def main():
    os.environ['TZ'] = 'UTC'
    time.tzset()
    output = ROOT / 'qualification'
    native = output / 'native'
    native.mkdir(parents=True, exist_ok=True)
    zipper = load('release_zip')
    winget = load('prepare-winget')
    records = []
    for (arch, profile), expected in ORIGINALS.items():
        full_name = f'llgo{VERSION}.windows-{arch}-{profile}.zip'
        with tempfile.TemporaryDirectory(prefix='llgo-109-') as directory:
            temporary = Path(directory)
            original = temporary / full_name
            subprocess.run(['curl', '--fail', '--location', '--retry', '5', '--retry-all-errors',
                            '--output', str(original),
                            f'https://github.com/xgo-dev/llgo/releases/download/v{VERSION}/{full_name}'], check=True)
            if checksum(original) != expected:
                raise ValueError(f'Original release hash changed: {full_name}')
            source = temporary / 'source'
            with zipfile.ZipFile(original) as archive:
                archive.extractall(source)
                for info in archive.infolist():
                    if not info.is_dir():
                        timestamp = datetime(*info.date_time, tzinfo=timezone.utc).timestamp()
                        os.utime(source / info.filename, (timestamp, timestamp))
            zipper.verify(original, source)
            metadata = json.loads((source / 'release.json').read_text(encoding='utf-8-sig'))
            if metadata['commit'] != SOURCE_COMMIT or metadata['version'] != VERSION:
                raise ValueError(f'Original release identity changed: {full_name}')
            compiler = checksum(source / 'bin/llgo.exe')
            if metadata['compiler_sha256'] != compiler:
                raise ValueError(f'Compiler hash mismatch: {full_name}')
            if profile == 'msvc':
                inspection = output / 'inspection' / arch
                inspection.mkdir(parents=True, exist_ok=True)
                for path in (source / 'crosscompile/clang/bin').iterdir():
                    if path.name == 'llvm-readobj.exe' or path.suffix.lower() == '.dll':
                        shutil.copy2(path, inspection / path.name)
            # The compiler/resources remain byte-identical. Refresh only the
            # packaging guide, retaining its timestamp for reproducible ZIPs.
            guide = source / 'WINDOWS.md'
            times = guide.stat()
            guide.write_bytes((ROOT / 'WINDOWS.md').read_bytes())
            os.utime(guide, ns=(times.st_atime_ns, times.st_mtime_ns))
            destination = native / winget.archive_name(VERSION, arch, profile)
            zipper.create_native(source, destination)
            digest = checksum(destination)
            destination.with_suffix('.zip.sha256').write_text(f'{digest}  {destination.name}\n')
            with zipfile.ZipFile(destination) as archive:
                if hashlib.sha256(archive.read('bin/llgo.exe')).hexdigest() != compiler:
                    raise ValueError('Repackaging changed the compiler')
                for info in archive.infolist():
                    if info.is_dir() or info.filename == 'release.json':
                        continue
                    if archive.read(info) != (source / info.filename).read_bytes():
                        raise ValueError(f'Repackaging changed {info.filename}')
            records.append({'name': destination.name, 'sha256': digest, 'bytes': destination.stat().st_size,
                            'original': full_name, 'original_sha256': expected,
                            'compiler_sha256': compiler})
            print(f'QUALIFIED_CONTENTS {destination.name} {digest}', flush=True)
    winget.prepare(native, output / 'candidates', VERSION)
    (native / f'llgo{VERSION}.windows-native.checksums.txt').write_text(''.join(
        f'{record["sha256"]}  {record["name"]}\n' for record in sorted(records, key=lambda item: item['name'])))
    provenance = {'version': VERSION, 'original_commit': SOURCE_COMMIT,
                  'packaging_commit': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip(),
                  'changes': ['Omit crosscompile/ ESP toolchain', 'Set distribution=native and remove ESP metadata',
                              'Refresh WINDOWS.md to describe both package variants'],
                  'artifacts': records}
    (native / f'llgo{VERSION}.windows-native.provenance.json').write_text(json.dumps(provenance, indent=2) + '\n')


if __name__ == '__main__':
    main()
