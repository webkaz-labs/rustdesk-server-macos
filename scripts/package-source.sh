#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/.."
ROOT="$PWD"
UPSTREAM="${1:?usage: package-source.sh UPSTREAM VERSION}"
VERSION="${2:?version required}"
RUST="$(python3 -c 'import json; print(json.load(open("upstream.json"))["rust_toolchain"])')"
python3 scripts/release.py validate-version "$VERSION"
python3 scripts/release.py check-upstream "$UPSTREAM"
# cargo vendor includes both registry and locked Git dependencies, for every
# platform, including licenses and embedded native dependency source archives.
# The source bundle can rebuild without a crates.io/GitHub source connection.
VENDOR="$ROOT/.build/vendor"
mkdir -p "$ROOT/.build"
(
  cd "$UPSTREAM"
  cargo +"$RUST" vendor --locked --versioned-dirs "$VENDOR" > "$ROOT/.build/vendor-config.toml"
)
python3 scripts/release.py source "$UPSTREAM" "$VENDOR" "$ROOT/.build/vendor-config.toml" "$VERSION"
