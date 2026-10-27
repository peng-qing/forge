#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
project_root="$(cd -- "${script_dir}/.." && pwd)"

cd "${project_root}"

mapfile -d '' proto_files < <(
  find api/proto -type f -name '*.proto' -print0 | sort -z
)

if ((${#proto_files[@]} == 0)); then
  echo "proto: no .proto files found under api/proto" >&2
  exit 1
fi

protoc \
  -I api/proto \
  --go_out=. \
  --go_opt=module=forge \
  "${proto_files[@]}"
