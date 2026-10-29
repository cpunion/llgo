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

DEB/RPM contain only LLGo's compiler, runtime, targets and license documents.
They depend on system LLVM/Clang/LLD 22 and native libraries. Their binaries use
the system `libLLVM.so.22.1`; the tar.gz binary's relative runtime search path
also permits this system fallback when the bundled toolchain is absent. On
Debian/Ubuntu, the configured fallback is `llvm-config-22`; Fedora supplies
unversioned LLVM commands. Ubuntu 26.04 and Fedora 44 supply the required LLVM
22 packages. Earlier Debian/Ubuntu releases need the matching apt.llvm.org
repository enabled first. ESP tools remain available in the tar.gz distribution
or through LLGo's target toolchain download support.

CI installs each DEB/RPM with its declared dependencies in a fresh distribution
container, checks that no toolchain is bundled and that LLVM resolves from the
system, then compiles and runs a mixed Go/C/C++ program. The release container
installs the same external LLVM 22 dependencies on its Debian base.

Local packaging regression tests:

```sh
python3 -m unittest discover -s .github/unix -p 'test_*.py' -v
```
