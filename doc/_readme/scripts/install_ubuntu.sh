# shellcheck disable=all
echo "deb http://apt.llvm.org/$(lsb_release -cs)/ llvm-toolchain-$(lsb_release -cs)-20 main" | sudo tee /etc/apt/sources.list.d/llvm.list
wget -O - https://apt.llvm.org/llvm-snapshot.gpg.key | sudo apt-key add -
sudo apt-get update
sudo apt-get install -y llvm-20-dev clang-20 libclang-20-dev lld-20 pkg-config libgc-dev libssl-dev zlib1g-dev libcjson-dev libsqlite3-dev libunwind-dev libuv1-dev
sudo apt-get install -y python3.12-dev # optional
#curl https://raw.githubusercontent.com/goplus/llgo/refs/heads/main/install.sh | bash
./install.sh