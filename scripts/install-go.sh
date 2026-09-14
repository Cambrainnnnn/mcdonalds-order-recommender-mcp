#!/usr/bin/env bash
# Install a checksum-verified, project-local compiler; never changes system Go.
set -euo pipefail
cd "$(dirname "$0")/.."
if [[ $(uname -s) != Linux || $(uname -m) != x86_64 ]]; then
  echo 'This installer supports Linux x86_64; install Go >= 1.25 manually on other platforms.' >&2
  exit 1
fi
version=1.26.8
checksum=d0f743b33e8d8945e6b1f432edd15785c70507121d6e2a723b21285eddf8b57b
mkdir -p .build/toolchains
archive="$PWD/.build/toolchains/go-$version.tar.gz"
curl -fsSL "https://go.dev/dl/go$version.linux-amd64.tar.gz" -o "$archive"
printf '%s  %s\n' "$checksum" "$archive" | sha256sum -c -
staging=$(mktemp -d "$PWD/.build/toolchains/install.XXXXXX")
trap 'rm -rf "$staging"' EXIT
tar -xzf "$archive" -C "$staging"
if [[ -e .build/toolchains/go ]]; then
  mv .build/toolchains/go "$staging/previous"
fi
mv "$staging/go" .build/toolchains/go
.build/toolchains/go/bin/go version
