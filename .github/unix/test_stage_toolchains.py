import os
from pathlib import Path
import subprocess
import tempfile
import unittest


SCRIPT = Path(__file__).with_name('stage-toolchains.sh')
PLATFORMS = ('darwin/amd64', 'darwin/arm64', 'linux/amd64', 'linux/arm64')


class RuntimeToolchainTests(unittest.TestCase):
    def test_runtime_resources_links_and_licenses_survive_without_changing_sdk(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source, output = root / 'SDK with spaces', root / 'runtime with spaces'
            removed = ('include/llvm/IR.h', 'include/llvm-c/Core.h', 'include/clang/AST.h',
                       'include/clang-c/Index.h', 'lib/libLLVMCore.a', 'lib/libclangAST.a',
                       'bin/llvm-exegesis')
            retained = ('lib/clang/22/include/stddef.h', 'lib/clang/22/lib/linux/libclang_rt.builtins.a',
                        'lib/libLLVM.so.22.1', 'LICENSE-LLVM.txt', 'bin/llvm-config', 'bin/ld.lld')
            for platform in PLATFORMS:
                sdk = source / platform / 'crosscompile/clang'
                for name in (*removed, *retained, 'bin/clang', 'lib/libLLVM.dylib', 'lib/libLLVM-22.dylib'):
                    path = sdk / name
                    path.parent.mkdir(parents=True, exist_ok=True)
                    path.write_bytes(b'library' if name.endswith('.dylib') else name.encode())
                (sdk / 'bin/clang').chmod(0o755)
                (sdk / 'bin/clang++').symlink_to('clang')
                (sdk / 'lib/libLLVM-22.so').symlink_to('libLLVM.so.22.1')
            # Different libraries must not be replaced by links.
            (source / 'darwin/amd64/crosscompile/clang/lib/libLLVM-22.dylib').write_bytes(b'different')
            for _ in range(2):  # Staging is repeatable; stale files cannot reappear.
                subprocess.run(['bash', str(SCRIPT), str(source), str(output)], check=True, capture_output=True)
                for platform in PLATFORMS:
                    sdk = source / platform / 'crosscompile/clang'
                    staged = output / platform
                    self.assertFalse((staged / 'stale').exists())
                    for name in removed:
                        self.assertFalse((staged / name).exists(), name)
                        self.assertTrue((sdk / name).is_file(), name)
                    for name in retained:
                        self.assertEqual((staged / name).read_bytes(), (sdk / name).read_bytes())
                    self.assertEqual(os.readlink(staged / 'bin/clang++'), 'clang')
                    self.assertTrue(os.access(staged / 'bin/clang', os.X_OK))
                    self.assertEqual(os.readlink(staged / 'lib/libLLVM-22.so'), 'libLLVM.so.22.1')
                self.assertEqual(os.readlink(output / 'darwin/arm64/lib/libLLVM-22.dylib'), 'libLLVM.dylib')
                self.assertFalse((output / 'darwin/amd64/lib/libLLVM-22.dylib').is_symlink())
                self.assertFalse((source / 'darwin/arm64/crosscompile/clang/lib/libLLVM-22.dylib').is_symlink())
                (output / 'linux/arm64/stale').touch()

    def test_source_and_staging_must_not_overlap(self):
        with tempfile.TemporaryDirectory() as temporary:
            source = Path(temporary) / 'SDK'
            source.mkdir()
            for output in (source, source / 'runtime', source.parent):
                result = subprocess.run(['bash', str(SCRIPT), str(source), str(output)], capture_output=True, text=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn('build sysroot', result.stderr)
                self.assertTrue(source.is_dir())


if __name__ == '__main__':
    unittest.main()
