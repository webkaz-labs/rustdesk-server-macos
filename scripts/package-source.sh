#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/.."
ROOT="$PWD"
UPSTREAM="${1:?usage: package-source.sh UPSTREAM VERSION}"
VERSION="${2:?version required}"
mkdir -p "$ROOT/.build"
TOOL="$ROOT/.build/release-tool"
if [ ! -x "$TOOL" ]; then
  CGO_ENABLED=0 GOTOOLCHAIN=local go build -trimpath -o "$TOOL" ./cmd/release-tool
fi
RUST="$("$TOOL" pin rust_toolchain)"
"$TOOL" validate-version "$VERSION"
"$TOOL" check-upstream "$UPSTREAM"
# cargo vendor includes both registry and locked Git dependencies, for every
# platform, including licenses and embedded native dependency source archives.
# The source bundle can rebuild without a crates.io/GitHub source connection.
VENDOR="$ROOT/.build/vendor"
mkdir -p "$ROOT/.build"
(
  cd "$UPSTREAM"
  cargo +"$RUST" vendor --locked --versioned-dirs "$VENDOR" > "$ROOT/.build/vendor-config.toml"
)
"$TOOL" source "$UPSTREAM" "$VENDOR" "$ROOT/.build/vendor-config.toml" "$VERSION"
