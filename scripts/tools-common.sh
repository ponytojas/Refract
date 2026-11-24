# Shared helpers for the tool scripts. Source it; it reads packaging/tools.lock,
# the single source of truth for versions, sources and checksums.
# shellcheck shell=sh disable=SC1090,SC1091,SC2034

TOOLS_ROOT=${TOOLS_ROOT:-$(cd "$(dirname "$0")/.." && pwd)}
. "$TOOLS_ROOT/packaging/tools.lock"

# The macOS tools run on the oldest macOS the desktop app supports (13, see #66,
# LSMinimumSystemVersion), not only on the Mac that builds them: without this, clang
# targets the build machine's own version. Windows and Linux builds ignore it.
export MACOSX_DEPLOYMENT_TARGET=${MACOSX_DEPLOYMENT_TARGET:-13.0}

die() { echo "error: $*" >&2; exit 1; }

# lock_get NAME os arch: the value of NAME_<os>_<arch> from the lock.
lock_get() {
  eval "printf '%s' \"\${${1}_${2}_${3}:-}\""
}

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1
  else shasum -a 256 "$1" | cut -d' ' -f1; fi
}

# fetch URL SHA256 FILE: download and refuse anything that does not match the pin. Retries any
# failure (a reset connection isn't retried by --retry alone; the checksum still guards the result).
fetch() {
  [ -n "$2" ] || die "no checksum pinned for $1"
  curl -fsSL --retry 5 --retry-all-errors --retry-delay 5 -o "$3" "$1" || die "download failed: $1"
  got=$(sha256_of "$3")
  [ "$got" = "$2" ] || die "checksum mismatch for $1 (expected $2, got $got)"
}

# Release asset names: Tectonic and ripgrep both name their builds by Rust target triple.
rust_asset() { # os arch -> triple|ext
  case "$1-$2" in
    darwin-arm64) echo "aarch64-apple-darwin|tar.gz" ;;
    darwin-amd64) echo "x86_64-apple-darwin|tar.gz" ;;
    linux-arm64) echo "aarch64-unknown-linux-musl|tar.gz" ;;
    linux-amd64) echo "x86_64-unknown-linux-musl|tar.gz" ;;
    windows-amd64) echo "x86_64-pc-windows-msvc|zip" ;;
    *) return 1 ;;
  esac
}
tectonic_asset() { rust_asset "$@"; }
ripgrep_asset() { rust_asset "$@"; }
