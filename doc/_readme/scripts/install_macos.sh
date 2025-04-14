# shellcheck disable=all
brew update
brew install llvm@20 lld@20 bdw-gc openssl cjson libffi libuv pkg-config
brew install python@3.12 # optional
brew link --overwrite llvm@20 lld@20 libffi
# curl https://raw.githubusercontent.com/goplus/llgo/refs/heads/main/install.sh | bash
./install.sh