#!/bin/bash
set -euo pipefail
STAGE="${1:?usage: check-native.sh STAGE arm64 [MINIMUM_MACOS]}"
case "${2:?architecture required}" in arm64) ARCH=arm64 ;; *) exit 2 ;; esac
MINIMUM_MACOS="${3:-15.0}"
test "$(uname -s)" = Darwin
for name in rustdesk-server hbbs hbbr; do
  binary="$STAGE/bin/$name"
  test -x "$binary"
  test "$(lipo -archs "$binary")" = "$ARCH"
  file "$binary"
  otool -L "$binary"
  # Distribution must be self-contained apart from Apple's system libraries.
  # Reject Homebrew paths, developer paths, @rpath and relative dylib names.
  while IFS= read -r dependency; do
    case "$dependency" in /usr/lib/*|/System/Library/*) ;;
      *) echo "$name has a non-system runtime dependency: $dependency" >&2; exit 1 ;;
    esac
  done < <(otool -L "$binary" | tail -n +2 | sed -E 's/^[[:space:]]*//; s/ \(.*$//')
  # Record the actual deployment target, and reject binaries newer than our
  # declared minimum. This does not claim testing on an older operating system.
  otool -l "$binary" | awk '/LC_BUILD_VERSION/{f=1;next} f && /minos/{print $2;f=0} /LC_VERSION_MIN_MACOSX/{g=1;next} g && /version/{print $2;g=0}' |
    awk -v maximum="$MINIMUM_MACOS" '
      function supported(version, parts, limit, n, m, i) {
        if (version !~ /^[0-9]+([.][0-9]+)*$/ || maximum !~ /^[0-9]+([.][0-9]+)*$/) return 0
        n=split(version, parts, "[.]"); m=split(maximum, limit, "[.]")
        for (i=1; i<=n || i<=m; i++) {
          if ((parts[i]+0) < (limit[i]+0)) return 1
          if ((parts[i]+0) > (limit[i]+0)) return 0
        }
        return 1
      }
      { count++; if (!supported($0)) { print "Unsupported Mach-O minimum: " $0 > "/dev/stderr"; failed=1 } }
      END { if (!count) print "Mach-O deployment target missing" > "/dev/stderr"; if (!count || failed) exit 1 }
    '
  # Ad-hoc signing satisfies native arm64 executable integrity requirements;
  # it is deliberately not Apple Developer ID signing or notarization.
  codesign --force --sign - "$binary"
  codesign --verify --strict "$binary"
  "$binary" --help > /dev/null
done
"$STAGE/bin/rustdesk-server" --version

# Localized help is read-only and must work in the actual native executable.
helper="$STAGE/bin/rustdesk-server"
en_help="$("$helper" --lang en --help)"
ja_help="$("$helper" --lang ja --help)"
test "$en_help" != "$ja_help"
test "$(LC_ALL=ja_JP.UTF-8 "$helper" --help)" = "$ja_help"
test "$(LC_ALL=fr_FR.UTF-8 "$helper" --help)" = "$en_help"
test "$(LC_ALL=ja_JP.UTF-8 "$helper" --lang en --help)" = "$en_help"
"$helper" setup --lang ja --help >/dev/null
"$helper" setup --lang en --help >/dev/null
# Exercise the native OS preference path without modifying the preference.
os_help="$(env -u LC_ALL -u LC_MESSAGES -u LANG "$helper" --help)"
test "$os_help" = "$en_help" || test "$os_help" = "$ja_help"
