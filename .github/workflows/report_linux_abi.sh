#!/usr/bin/env bash
# Fork-only release experiment: inspect requirements, never version definitions.
set -euo pipefail
export LC_ALL=C

bundle=${1:?release bundle directory required}
report=${2:?report directory required}
test -x "$bundle/bin/llgo"
mkdir -p "$report"
printf 'file\trequired_version\n' > "$report/requirements.tsv"

while IFS= read -r -d '' binary; do
    # Skip headers and static archives; inspect every ELF in the shipped bundle,
    # including Clang/LLVM tools and shared libraries, not just bin/llgo.
    header=$(readelf -h "$binary" 2>/dev/null) || continue
    [[ "$header" == 'ELF Header:'* ]] || continue
    readelf --version-info --wide "$binary" | awk -v file="${binary#"$bundle/"}" '
        /Version needs section/ { needs = 1; next }
        /^Version .* section/ { needs = 0 }
        needs {
            for (i = 1; i < NF; i++) {
                if ($i == "Name:" && $(i+1) ~ /^(GLIBC_|GLIBCXX_|CXXABI_)/)
                    print file "\t" $(i+1)
            }
        }
    ' >> "$report/requirements.tsv"
done < <(find "$bundle" -type f -print0)

{
    echo '### Linux release bundle ABI requirements'
    echo
    echo 'Highest numeric version required by any shipped ELF (not versions provided by libraries):'
    echo
    for family in GLIBC GLIBCXX CXXABI; do
        highest=$(awk -F '\t' -v prefix="${family}_" '
            index($2, prefix) == 1 && substr($2, length(prefix) + 1) ~ /^[0-9]+(\.[0-9]+)*$/ { print $2 }
        ' "$report/requirements.tsv" | sort -Vu | tail -n 1)
        printf -- '- %s: %s\n' "$family" "${highest:-none}"
    done
    echo
    echo 'See requirements.tsv for every file/version, including nonnumeric ABI names.'
    echo 'These are symbol-version requirements, not a complete minimum-OS guarantee.'
} > "$report/summary.md"
cat "$report/summary.md"
