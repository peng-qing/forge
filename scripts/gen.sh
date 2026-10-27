#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
project_root="$(cd -- "${script_dir}/.." && pwd)"

cd "${project_root}"

"${script_dir}/proto.sh"

go run ./cmd/gencmd \
  -pb-dir api/pb \
  -out-dir command \
  -handler-import-pattern 'forge/services/{service}/handler' \
  "$@"
