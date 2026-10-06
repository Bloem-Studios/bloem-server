#!/usr/bin/env bash
# Generate only the private Bloem storage bindings used by the host.
set -euo pipefail

case "${1:-}" in
 "") check=0 ;;
 --check) check=1 ;;
 *) echo 'usage: generate-bloem-storage-proto.sh [--check]' >&2; exit 2 ;;
esac
[[ $# -le 1 ]] || { echo 'too many arguments' >&2; exit 2; }

repo_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
protoc_bin=${PROTOC:-protoc}
go_bin=${PROTOC_GEN_GO:-protoc-gen-go}
grpc_bin=${PROTOC_GEN_GO_GRPC:-protoc-gen-go-grpc}

if [[ $("$protoc_bin" --version) != 'libprotoc 3.21.12' ]]; then
 echo 'private storage generation requires protoc 3.21.12' >&2
 exit 1
fi
if [[ $("$go_bin" --version) != 'protoc-gen-go v1.36.11' ]]; then
 echo 'private storage generation requires protoc-gen-go 1.36.11' >&2
 exit 1
fi
if [[ $("$grpc_bin" --version) != 'protoc-gen-go-grpc 1.6.1' ]]; then
 echo 'private storage generation requires protoc-gen-go-grpc 1.6.1' >&2
 exit 1
fi
go_bin=$(command -v "$go_bin")
grpc_bin=$(command -v "$grpc_bin")

scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
module=github.com/Silo-Server/silo-server
schema=bloem/plugin/v1/storage_provider.proto
package_path=internal/storageproto/bloem/plugin/v1
"$protoc_bin" -I "$repo_root/proto" \
 --plugin="protoc-gen-go=$go_bin" --plugin="protoc-gen-go-grpc=$grpc_bin" \
 --go_out="$scratch" --go_opt="module=$module" \
 --go_opt="M$schema=$module/$package_path" \
 --go-grpc_out="$scratch" --go-grpc_opt="module=$module" \
 --go-grpc_opt="M$schema=$module/$package_path" \
 "$schema"

for file in storage_provider.pb.go storage_provider_grpc.pb.go; do
 if [[ $check == 1 ]]; then
  cmp "$scratch/$package_path/$file" "$repo_root/$package_path/$file"
 else
  mkdir -p "$repo_root/$package_path"
  cp "$scratch/$package_path/$file" "$repo_root/$package_path/$file"
 fi
done
echo 'private storage bindings match the pinned schema/generators'
