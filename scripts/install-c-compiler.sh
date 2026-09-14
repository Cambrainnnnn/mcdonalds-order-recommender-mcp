#!/usr/bin/env bash
# Optional project-local C compiler for Go's race detector on Linux x86_64.
set -euo pipefail
cd "$(dirname "$0")/.."
if [[ $(uname -s) != Linux || $(uname -m) != x86_64 ]]; then
  echo 'Use a system C compiler on this platform.' >&2
  exit 1
fi
version=0.13.0
checksum=d45312e61ebcc48032b77bc4cf7fd6915c11fa16e4aad116b66c9468211230ea
mkdir -p .build/toolchains
archive="$PWD/.build/toolchains/zig.tar.xz"
curl -fsSL "https://ziglang.org/download/$version/zig-linux-x86_64-$version.tar.xz" -o "$archive"
printf '%s  %s\n' "$checksum" "$archive" | sha256sum -c -
tar -xJf "$archive" -C .build/toolchains
.build/toolchains/zig-linux-x86_64-$version/zig version
