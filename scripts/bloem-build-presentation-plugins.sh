#!/bin/sh
# Build the reviewed local presentation workers; they are not SDK catalog plugins.
set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
output_dir=${1:-"$repository_root/build/bloem-presentation-plugins"}
mkdir -p "$output_dir"
output_dir=$(CDPATH= cd -- "$output_dir" && pwd)
cd "$repository_root"

go build -o "$output_dir/promotions" ./cmd/bloem-presentation-promotions
go build -o "$output_dir/ambience" ./cmd/bloem-presentation-ambience
