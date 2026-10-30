import os
from pathlib import Path
import subprocess
import tempfile
import unittest


SCRIPT = Path(__file__).with_name('stage-toolchains.sh')
PLATFORMS = ('darwin/amd64', 'darwin/arm64', 'linux/amd64', 'linux/arm64')


class RuntimeToolchainTests(unittest.TestCase):
    def test_published_runtime_and_archives_survive_without_changing_sdk(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source, runtime, output = root / 'SDK with spaces', root / 'published runtime', root / 'release output'
            development = ('include/llvm/IR.h', 'include/llvm-c/Core.h', 'include/clang/AST.h',
                           'include/clang-c/Index.h', 'lib/libLLVMCore.a', 'lib/libclangAST.a', 'bin/llvm-exegesis')
            retained = ('lib/clang/22/include/stddef.h', 'lib/clang/22/lib/linux/libclang_rt.builtins.a',
                        'lib/libLLVM.so.22.1', 'LICENSE-LLVM.txt', 'bin/llvm-config', 'bin/ld.lld')
            for platform in PLATFORMS:
                sdk = source / platform / 'crosscompile/clang'
                for name in (*development, *retained, 'bin/clang'):
                    path = sdk / name
                    path.parent.mkdir(parents=True, exist_ok=True)
                    path.write_bytes(b'SDK:' + name.encode())
                payload = runtime / platform
                for name in (*retained, 'bin/clang'):
                    path = payload / name
                    path.parent.mkdir(parents=True, exist_ok=True)
                    path.write_bytes(b'RUNTIME:' + name.encode())
                (payload / 'bin/clang').chmod(0o755)
                (payload / 'bin/clang++').symlink_to('clang')
                (payload / 'lib/libLLVM-22.so').symlink_to('libLLVM.so.22.1')
                archive = runtime / 'payloads' / platform / 'clang-esp-fixture.tar.xz'
                archive.parent.mkdir(parents=True)
                archive.write_bytes(b'original compressed payload:' + platform.encode())
                if platform.startswith('linux/'):
                    for name, macro in [('Targets', 'LLVM_TARGET'), ('AsmParsers', 'LLVM_ASM_PARSER'),
                                        ('AsmPrinters', 'LLVM_ASM_PRINTER')]:
                        path = sdk / f'include/llvm/Config/{name}.def'
                        path.parent.mkdir(parents=True, exist_ok=True)
                        path.write_text(f'{macro}(X86)\n{macro}(AArch64)\n{macro}(Xtensa)\n')
            for _ in range(2):
                subprocess.run(['bash', str(SCRIPT), str(source), str(output), str(runtime)], check=True, capture_output=True)
                for platform in PLATFORMS:
                    sdk, payload = source / platform / 'crosscompile/clang', runtime / platform
                    staged = output / platform
                    self.assertFalse((staged / 'stale').exists())
                    for name in development:
                        self.assertFalse((staged / name).exists(), name)
                        self.assertTrue((sdk / name).is_file(), name)
                    for name in retained:
                        self.assertEqual((staged / name).read_bytes(), (payload / name).read_bytes())
                        self.assertNotEqual((staged / name).read_bytes(), (sdk / name).read_bytes())
                    self.assertEqual(os.readlink(staged / 'bin/clang++'), 'clang')
                    self.assertTrue(os.access(staged / 'bin/clang', os.X_OK))
                    self.assertEqual(os.readlink(staged / 'lib/libLLVM-22.so'), 'libLLVM.so.22.1')
                    name = Path('payloads') / platform / 'clang-esp-fixture.tar.xz'
                    self.assertEqual((output / name).read_bytes(), (runtime / name).read_bytes())
                for arch in ('amd64', 'arm64'):
                    for name in ('Targets', 'AsmParsers', 'AsmPrinters'):
                        definition = output / f'system-linux/{arch}/include/llvm/Config/{name}.def'
                        self.assertNotIn('(Xtensa)', definition.read_text())
                        self.assertIn('(AArch64)', definition.read_text())
                        original = source / f'linux/{arch}/crosscompile/clang/include/llvm/Config/{name}.def'
                        self.assertIn('(Xtensa)', original.read_text())
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
