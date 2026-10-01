#!/bin/bash
# Build on the architecture being released. Never uses Rosetta or cross linking.
set -euo pipefail
cd "$(dirname "$0")/.."
ROOT="$PWD"
VERSION="${1:?usage: build-native.sh VERSION [arm64]}"
ARCH="${2:-arm64}"
test "$(uname -s)" = Darwin || { echo 'Native macOS is required' >&2; exit 1; }
case "$ARCH:$(uname -m)" in
  arm64:arm64) TARGET=aarch64-apple-darwin ;;
  *) echo "Wrong native runner: $ARCH / $(uname -m)" >&2; exit 1 ;;
esac
mkdir -p "$ROOT/.build" "$ROOT/dist"
export GOTOOLCHAIN=local
TOOL="$ROOT/.build/release-tool"
CGO_ENABLED=0 go build -trimpath -o "$TOOL" ./cmd/release-tool
"$TOOL" validate-version "$VERSION"
UPSTREAM="$ROOT/.build/upstream-$ARCH"
STAGE="$ROOT/.build/stage-$ARCH-$$"
META="$ROOT/.build/metadata-$ARCH.json"
read_pin() { "$TOOL" pin "$1"; }
COMMIT="$(read_pin commit)"
RUST="$(read_pin rust_toolchain)"
GO="$(read_pin go_toolchain)"
export MACOSX_DEPLOYMENT_TARGET="$(read_pin minimum_macos)"
export CARGO_NET_GIT_FETCH_WITH_CLI=true
export CGO_ENABLED=0 GOOS=darwin GOARCH="$ARCH" GOTOOLCHAIN=local
# Fail if a runner/image change silently supplies a different compiler.
test "$(rustc +"$RUST" --version | awk '{print $2}')" = "$RUST"
test "$(go env GOVERSION)" = "go$GO"
# Fail fast on real launchd lifecycle problems before the longer Rust build.
RUSTDESK_MACOS_INTEGRATION=1 go test ./internal/service -run 'Test(LaunchdIntegration|NativeLocaleFallback)$' -count=1 -v
if [ ! -d "$UPSTREAM/.git" ]; then
  git init "$UPSTREAM"
  git -C "$UPSTREAM" remote add origin "$(read_pin repository)"
  git -C "$UPSTREAM" fetch --depth=1 origin "$COMMIT"
  git -C "$UPSTREAM" checkout --detach FETCH_HEAD
fi
test "$(git -C "$UPSTREAM" rev-parse HEAD)" = "$COMMIT"
git -C "$UPSTREAM" submodule update --init --recursive
"$TOOL" check-upstream "$UPSTREAM"
# SQLx schema-copy setup (outside the strictly checked upstream checkout).
# Compile-time query macros may change SQLite headers/journals. Always give
# them a disposable copy of the committed schema, never upstream's tracked DB.
BUILD_SCHEMA="$ROOT/.build/build-schema-$ARCH.sqlite3"
rm -f "$BUILD_SCHEMA" "$BUILD_SCHEMA-journal" "$BUILD_SCHEMA-wal" "$BUILD_SCHEMA-shm"
git -C "$UPSTREAM" show "$COMMIT:db_v2.sqlite3" > "$BUILD_SCHEMA"
export DATABASE_URL="sqlite://$BUILD_SCHEMA"
# End SQLx schema-copy setup.
# Never set SODIUM_USE_PKG_CONFIG: libsodium-sys builds its bundled source.
unset SODIUM_USE_PKG_CONFIG SODIUM_LIB_DIR SODIUM_SHARED
(
  cd "$UPSTREAM"
  cargo +"$RUST" build --release --locked --target "$TARGET" --bin hbbs --bin hbbr
  cargo +"$RUST" metadata --locked --format-version 1 --filter-platform "$TARGET" > "$META"
)
test -z "$(git -C "$UPSTREAM" diff -- Cargo.lock)"
mkdir -p "$STAGE/bin" "$STAGE/share/rustdesk-server"
install -m 755 "$UPSTREAM/target/$TARGET/release/hbbs" "$STAGE/bin/hbbs"
install -m 755 "$UPSTREAM/target/$TARGET/release/hbbr" "$STAGE/bin/hbbr"
go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$STAGE/bin/rustdesk-server" ./cmd/rustdesk-server
scripts/check-native.sh "$STAGE" "$ARCH" "$MACOSX_DEPLOYMENT_TARGET"
"$TOOL" metadata "$UPSTREAM" "$META" "$STAGE" "$VERSION" "$ARCH"
"$TOOL" archive "$STAGE" "dist/rustdesk-server-macos-$VERSION-darwin-$ARCH.tar.gz"
cp "$STAGE/share/rustdesk-server/bom.cdx.json" "dist/rustdesk-server-macos-$VERSION-darwin-$ARCH.cdx.json"
cp "$STAGE/share/rustdesk-server/source.json" "dist/rustdesk-server-macos-$VERSION-darwin-$ARCH.source.json"
if [ "${PACKAGE_SOURCE:-0}" = 1 ]; then
  scripts/package-source.sh "$UPSTREAM" "$VERSION"
fi
