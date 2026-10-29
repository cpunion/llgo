import tempfile
import json
from pathlib import Path
import unittest
from unittest import mock
import zipfile

import release_zip


class ReleaseZIPTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.stage = self.root / 'release 空格'
        (self.stage / 'empty').mkdir(parents=True)
        (self.stage / 'bin').mkdir()
        (self.stage / 'bin/llgo.exe').write_bytes(bytes(range(256)))
        (self.stage / '.hidden').write_text('hidden release file')
        (self.stage / '说明.txt').write_text('UTF-8 content', encoding='utf-8')
        self.archive = self.root / 'release.zip'

    def test_roundtrip_and_stored_empty_directories(self):
        release_zip.create(self.stage, self.archive)
        with zipfile.ZipFile(self.archive) as archive:
            directory = archive.getinfo('empty/')
            self.assertEqual((directory.file_size, directory.compress_size, directory.compress_type), (0, 0, 0))
            archive.extractall(self.root / 'extracted')
        release_zip.verify(self.archive, self.root / 'extracted')

    def test_deflated_directory_regression(self):
        release_zip.create(self.stage, self.archive)
        broken = self.root / 'broken.zip'
        with zipfile.ZipFile(self.archive) as source, zipfile.ZipFile(broken, 'w') as target:
            for entry in source.infolist():
                target.writestr(entry.filename, source.read(entry),
                                compress_type=zipfile.ZIP_DEFLATED)
        with self.assertRaisesRegex(ValueError, 'directory must be empty and stored'):
            release_zip.verify(broken, self.stage)

    def test_changed_file(self):
        release_zip.create(self.stage, self.archive)
        (self.stage / 'bin/llgo.exe').write_bytes(b'other compiler')
        with self.assertRaisesRegex(ValueError, 'differs from release'):
            release_zip.verify(self.archive, self.stage)

    def test_missing_file(self):
        release_zip.create(self.stage, self.archive)
        (self.stage / 'new-file').write_text('must also be packaged')
        with self.assertRaisesRegex(ValueError, 'missing release entries'):
            release_zip.verify(self.archive, self.stage)

    def test_extra_path_rejected(self):
        release_zip.create(self.stage, self.archive)
        with zipfile.ZipFile(self.archive, 'a') as archive:
            archive.writestr('../outside', 'not a release entry')
        with self.assertRaisesRegex(ValueError, 'Unexpected ZIP entry'):
            release_zip.verify(self.archive, self.stage)

    def test_no_output_inside_source(self):
        with self.assertRaisesRegex(ValueError, 'outside the release directory'):
            release_zip.create(self.stage, self.stage / 'recursive.zip')

    def test_native_release_preserves_compiler_dlls_resources_and_full_tree(self):
        (self.stage / 'crosscompile/clang/bin').mkdir(parents=True)
        (self.stage / 'crosscompile/clang/bin/clang.exe').write_bytes(b'ESP toolchain')
        (self.stage / 'runtime').mkdir()
        (self.stage / 'runtime/go.mod').write_text('module runtime')
        (self.stage / 'bin/compiler-runtime.dll').write_bytes(b'required DLL')
        for arch in ('386', 'amd64', 'arm64'):
            for abi in ('msvc', 'mingw'):
                with self.subTest(arch=arch, abi=abi):
                    metadata = dict(goarch=arch, abi=abi, compiler_sha256='verified-compiler',
                                    esp_clang_version='22', esp_clang_host=f'windows/{arch}',
                                    esp_clang_sha256='verified-ESP')
                    original = json.dumps(metadata)
                    (self.stage / 'release.json').write_text(original)
                    release_zip.create_native(self.stage, self.archive)
                    with zipfile.ZipFile(self.archive) as archive:
                        self.assertFalse(any(n.startswith('crosscompile') for n in archive.namelist()))
                        self.assertEqual(archive.read('bin/llgo.exe'), bytes(range(256)))
                        self.assertEqual(archive.read('bin/compiler-runtime.dll'), b'required DLL')
                        self.assertEqual(archive.read('runtime/go.mod'), b'module runtime')
                        self.assertEqual(archive.read('.hidden'), b'hidden release file')
                        self.assertEqual(archive.getinfo('empty/').compress_size, 0)
                        self.assertEqual(json.loads(archive.read('release.json')), dict(
                            goarch=arch, abi=abi, compiler_sha256='verified-compiler', distribution='native'))
                    self.assertEqual((self.stage / 'release.json').read_text(), original)
                    self.assertEqual((self.stage / 'crosscompile/clang/bin/clang.exe').read_bytes(), b'ESP toolchain')

    def test_native_output_cannot_modify_source_tree(self):
        with self.assertRaisesRegex(ValueError, 'outside the release directory'):
            release_zip.create_native(self.stage, self.stage / 'native.zip')

    def test_native_rejects_links_introduced_after_source_scan(self):
        for name in ('bin/link', 'linked-directory', 'release.json'):
            with self.subTest(name=name):
                metadata = self.stage / 'release.json'
                metadata.write_text('{}')
                link = self.stage / name
                outside = self.root / 'outside.json'
                outside.write_text('{}')
                original_entries = release_zip.entries

                def scan_then_link(root):
                    result = original_entries(root)
                    if Path(root).resolve() == self.stage.resolve():
                        if link.exists():
                            link.unlink()
                        target = self.stage / 'bin' if name == 'linked-directory' else outside
                        link.symlink_to(target, target_is_directory=name == 'linked-directory')
                    return result

                with mock.patch.object(release_zip, 'entries', side_effect=scan_then_link):
                    with self.assertRaisesRegex(ValueError, 'symbolic link'):
                        release_zip.create_native(self.stage, self.archive)
                self.assertEqual((self.stage / '.hidden').read_text(), 'hidden release file')
                self.assertEqual(outside.read_text(), '{}')
                link.unlink()


if __name__ == '__main__':
    unittest.main()
