#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
export GOCACHE="${GOCACHE:-$PWD/.build/gocache}"
export GOMODCACHE="${GOMODCACHE:-$PWD/.build/gomodcache}"
if [[ -x .build/toolchains/go/bin/go ]]; then
  export PATH="$PWD/.build/toolchains/go/bin:$PATH"
fi
if [[ -x .build/toolchains/zig-linux-x86_64-0.13.0/zig && -z ${CC:-} ]]; then
  export CC="$PWD/.build/toolchains/zig-linux-x86_64-0.13.0/zig cc"
  export CGO_ENABLED="${CGO_ENABLED:-1}"
fi
exec go "$@"
