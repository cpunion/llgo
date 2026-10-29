"""Create WinGet-compatible ZIPs and compare them with an extracted release."""
import argparse
import hashlib
import json
from pathlib import Path, PurePosixPath
import shutil
import stat
import tempfile
import zipfile


def entries(root):
    root = Path(root).resolve()
    result = {}
    for path in sorted(root.rglob("*")):
        if path.is_symlink():
            raise ValueError(f"Release contains a symbolic link: {path}")
        name = path.relative_to(root).as_posix() + ("/" if path.is_dir() else "")
        result[name] = path
    return result


def create(source, destination):
    source, destination = Path(source).resolve(), Path(destination).resolve()
    if destination.is_relative_to(source):
        raise ValueError("The ZIP must be outside the release directory")
    with zipfile.ZipFile(destination, "w", compression=zipfile.ZIP_DEFLATED) as archive:
        for name, path in entries(source).items():
            if path.is_dir():
                # DEFLATE produces nonzero compressed data even for empty input.
                # WinGet's pure ZIP checker rejects such directory records.
                info = zipfile.ZipInfo(name)
                info.external_attr = (stat.S_IFDIR | 0o755) << 16 | 0x10
                archive.writestr(info, b"", compress_type=zipfile.ZIP_STORED)
            else:
                archive.write(path, name)
    verify(destination, source)


def digest(stream):
    return hashlib.file_digest(stream, "sha256").hexdigest()


def create_native(source, destination):
    """Keep the native compiler, DLLs and resources; leave ESP in the full ZIP."""
    source, destination = Path(source).resolve(), Path(destination).resolve()
    if destination.is_relative_to(source):
        raise ValueError("The ZIP must be outside the release directory")
    entries(source)  # Reject links before copying the release tree.
    metadata = json.loads((source / 'release.json').read_text(encoding='utf-8-sig'))
    metadata['distribution'] = 'native'
    for key in ('esp_clang_version', 'esp_clang_host', 'esp_clang_sha256'):
        metadata.pop(key, None)
    with tempfile.TemporaryDirectory(prefix='llgo-native-') as temporary:
        stage = Path(temporary)
        for path in source.iterdir():
            if path.name == 'crosscompile':
                continue
            if path.is_dir():
                shutil.copytree(path, stage / path.name)
            else:
                shutil.copy2(path, stage / path.name)
        (stage / 'release.json').write_text(json.dumps(metadata, indent=2) + '\n', encoding='utf-8')
        create(stage, destination)


def verify(archive_path, directory):
    expected = entries(directory)
    with zipfile.ZipFile(archive_path) as archive:
        names = set()
        for info in archive.infolist():
            name = info.filename
            if (name in names or "\\" in name or name.startswith("/") or
                    ".." in PurePosixPath(name).parts or name not in expected):
                raise ValueError(f"Unexpected ZIP entry: {name}")
            names.add(name)
            path = expected[name]
            if info.flag_bits & 1 or info.is_dir() != path.is_dir():
                raise ValueError(f"Invalid ZIP entry: {name}")
            if info.is_dir():
                if info.file_size or info.compress_size or info.compress_type != zipfile.ZIP_STORED:
                    raise ValueError(f"ZIP directory must be empty and stored: {name}")
            else:
                with archive.open(info) as packed, path.open("rb") as original:
                    if info.file_size != path.stat().st_size or digest(packed) != digest(original):
                        raise ValueError(f"ZIP file differs from release: {name}")
        if names != set(expected):
            raise ValueError(f"ZIP is missing release entries: {sorted(set(expected) - names)}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("operation", choices=("create", "create-native", "verify"))
    parser.add_argument("archive", type=Path)
    parser.add_argument("directory", type=Path)
    args = parser.parse_args()
    if args.operation == "create":
        create(args.directory, args.archive)
    elif args.operation == "create-native":
        create_native(args.directory, args.archive)
    else:
        verify(args.archive, args.directory)
    print(f"Verified ZIP contents and directory records: {args.archive.name}")


if __name__ == "__main__":
    main()
