#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${repo_root}/dev/go_toolchain.sh"
test_dir="$(mktemp -d)"
trap 'rm -rf "$test_dir"' EXIT
export FAKE_TOOLCHAIN_ROOT="$test_dir/toolchain"
export FAKE_TOOLCHAIN_ATTEMPTS="$test_dir/attempts"
export FAKE_TOOLCHAIN_MODE=success
export FAKE_TOOLCHAIN_VERSION=go1.21.13
mkdir -p "$test_dir/bin" "$FAKE_TOOLCHAIN_ROOT/bin"
cat >"$test_dir/bin/go" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$0" == "$FAKE_TOOLCHAIN_ROOT"/* ]]; then
  echo "$FAKE_TOOLCHAIN_VERSION"
elif [[ "$*" == 'env GOVERSION' ]]; then
  echo go1.27.0
else
  count=0
  if [[ -f "$FAKE_TOOLCHAIN_ATTEMPTS" ]]; then
    count=$(cat "$FAKE_TOOLCHAIN_ATTEMPTS")
  fi
  count=$((count + 1))
  echo "$count" >"$FAKE_TOOLCHAIN_ATTEMPTS"
  case "$FAKE_TOOLCHAIN_MODE" in
    transient)
      if [[ "$count" == 1 ]]; then
        echo 'verifying module: stream error: stream ID 3; INTERNAL_ERROR; received from peer' >&2
        exit 1
      fi ;;
    exhausted) echo 'connection reset by peer' >&2; exit 1 ;;
    integrity) echo 'SECURITY ERROR: checksum mismatch' >&2; exit 1 ;;
    missing) echo 'toolchain not available' >&2; exit 1 ;;
  esac
  echo "$FAKE_TOOLCHAIN_ROOT"
fi
EOF
cp "$test_dir/bin/go" "$FAKE_TOOLCHAIN_ROOT/bin/go"
cp "$test_dir/bin/go" "$FAKE_TOOLCHAIN_ROOT/bin/go.exe"
chmod +x "$test_dir/bin/go" "$FAKE_TOOLCHAIN_ROOT/bin/go" "$FAKE_TOOLCHAIN_ROOT/bin/go.exe"
export PATH="$test_dir/bin:$PATH"

for mode in success transient exhausted integrity missing mismatch; do
  export FAKE_TOOLCHAIN_MODE="$mode"
  export FAKE_TOOLCHAIN_VERSION=go1.21.13
  if [[ "$mode" == mismatch ]]; then export FAKE_TOOLCHAIN_VERSION=go1.21.12; fi
  rm -f "$FAKE_TOOLCHAIN_ATTEMPTS"
  status=0
  llgo_go_root 1.21.13 >"$test_dir/output" 2>"$test_dir/error" || status=$?
  attempts=$(cat "$FAKE_TOOLCHAIN_ATTEMPTS")
  expected_attempts=1
  case "$mode" in
    success|transient)
      [[ "$status" == 0 ]]
      [[ "$(cat "$test_dir/output")" == "$FAKE_TOOLCHAIN_ROOT" ]]
      if [[ "$mode" == transient ]]; then expected_attempts=2; fi ;;
    exhausted) [[ "$status" != 0 ]]; [[ ! -s "$test_dir/output" ]]; expected_attempts=3 ;;
    *) [[ "$status" != 0 ]]; [[ ! -s "$test_dir/output" ]] ;;
  esac
  [[ "$attempts" == "$expected_attempts" ]]
done
echo 'Go toolchain transport and exact-version checks passed'
