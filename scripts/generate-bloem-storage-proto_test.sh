#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT

cat >"$scratch/protoc" <<'SH'
#!/usr/bin/env bash
if [[ ${1:-} == --version ]]; then
 echo 'libprotoc 99.0'
 exit
fi
exit 97
SH
chmod +x "$scratch/protoc"

if PROTOC="$scratch/protoc" bash "$repo_root/scripts/generate-bloem-storage-proto.sh" --check >"$scratch/output" 2>&1; then
 echo 'FAIL: unsupported protoc accepted' >&2
 exit 1
fi
if ! rg -q 'requires protoc 3.21.12' "$scratch/output"; then
 cat "$scratch/output" >&2
 echo 'FAIL: missing version rejection' >&2
 exit 1
fi
echo 'PASS: unsupported generator rejected before generation'
