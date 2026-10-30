# Unix release payloads

The macOS and Linux tar.gz archives include the LLVM shared libraries needed by
LLGo, native and ESP compiler/linker tools, Clang resource headers, target
runtimes and licenses. They omit LLVM/Clang SDK headers, top-level static
development libraries and the llvm-exegesis benchmark tool. On macOS, identical
libLLVM dylibs share one payload through a relative symlink.

GoReleaser's before hook runs `stage-toolchains.sh` to copy the runtime payload
from `.sysroot` into `.release-toolchains`. The build still reads the complete
SDK from `.sysroot`; pruning never modifies a cached SDK. Run the same hook when
preparing archives manually. The four native archive tests verify the installed
payload, run the Go/C/C++ smoke program with the packaged LLVM selected and the
build cache disabled, and build ESP32 firmware.

The same four jobs also select a runtime root without bundled tools and force a
fresh, checksum-pinned ESP download. `test-esp-download.py` builds ESP32 and
ESP32-C3 firmware with default and Full LTO, checks the ELF architecture and
downloaded runtime manifest, and retains logs as CI artifacts. This covers the
standalone ESP release independently of the SDK used to build LLGo. Windows
archive tests run the same four firmware cases on each host architecture/ABI.

DEB/RPM contain only LLGo's compiler, runtime, targets and license documents.
They depend on system LLVM/Clang/LLD 22 and native libraries. Separate package
compilers omit Xtensa from their LLVM initializer lists because Fedora's LLVM
does not provide that experimental backend. This generated header overlay is
used only while building the package compilers; the SDK and ESP-enabled tarball
compilers are unchanged. Their binaries use the system `libLLVM.so.22.1`. On
Debian/Ubuntu, the configured fallback is `llvm-config-22`; Fedora supplies
unversioned LLVM commands. Ubuntu 26.04 and Fedora 44 supply the required LLVM
22 packages. Earlier Debian/Ubuntu releases need the matching apt.llvm.org
repository enabled first. Use the tar.gz distribution for ESP development.

CI installs each DEB/RPM with its declared dependencies in a fresh distribution
container, checks that no toolchain is bundled and that LLVM resolves from the
system, then compiles and runs a mixed Go/C/C++ program. The release container
installs the same external LLVM 22 dependencies on its Debian base.

Local packaging regression tests:

```sh
python3 -m unittest discover -s .github/unix -p 'test_*.py' -v
```
