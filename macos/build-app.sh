#!/bin/sh
# Assembles Refract.app for one architecture: the Swift app, the refract server built
# with the web app inside, and the bundled tools, signed ad hoc, or with the identity in
# REFRACT_SIGN_IDENTITY (macos/sign-app.sh, then macos/verify-app.sh). Run through `make mac`;
# needs Xcode (swift, codesign), Go, and the web app built (`make web`).
#
# Usage: macos/build-app.sh <arm64|amd64> <version> <output-dir>
#   REFRACT_SIGN_IDENTITY  "Developer ID Application: Name (TEAMID)"; default "-" (ad hoc)
#   TOOLS_ARCHIVE=path  use this refract-tools-*-darwin-<arch>.tar.gz instead of building one
#                       (the release workflow passes the archive it already built)
#
# Layout (the server finds its tools at <exe dir>/../libexec/refract, internal/tools):
#   Refract.app/Contents/MacOS/Refract
#   Refract.app/Contents/Resources/Server/bin/refract
#   Refract.app/Contents/Resources/Server/libexec/refract/{tectonic,synctex,rg,git/,licenses/,...}
# Not Contents/Helpers: codesign treats every file under Helpers/ (and MacOS/, Frameworks/)
# as nested code, and the tools' tree also holds Git's scripts, templates and licences.
# See macos/README.md.
#   Refract.app/Contents/Resources/{en,es}.lproj/Localizable.strings
#   Refract.app/Contents/Resources/AppIcon.icns
#   Refract.app/Contents/Info.plist
set -eu
[ -z "${CODESIGN_IDENTITY:-}" ] || { echo "error: CODESIGN_IDENTITY is no longer used: set REFRACT_SIGN_IDENTITY instead" >&2; exit 2; }
[ $# -eq 3 ] || { echo "usage: build-app.sh <arm64|amd64> <version> <output-dir>" >&2; exit 2; }
ARCH=$1 VERSION=$2 OUT=$3
ROOT=$(cd "$(dirname "$0")/.." && pwd)
case "$ARCH" in
  arm64) SWIFT_ARCH=arm64 ;;
  amd64) SWIFT_ARCH=x86_64 ;;
  *) echo "error: arch must be arm64 or amd64" >&2; exit 2 ;;
esac
BUILD="$ROOT/build/mac"
mkdir -p "$BUILD" "$OUT"
OUT=$(cd "$OUT" && pwd)
APP="$OUT/Refract.app"

# CFBundleVersion must be dotted numbers: take them from the tag, 0.0.0 for a development build.
NUM=0.0.0
if printf '%s' "$VERSION" | grep -Eq '^v?[0-9]+(\.[0-9]+){0,2}($|[-+])'; then
  NUM=${VERSION#v}
  NUM=${NUM%%[-+]*}
fi

echo "==> tools ($ARCH)"
if [ -n "${TOOLS_ARCHIVE:-}" ]; then
  TOOLS=$TOOLS_ARCHIVE
else
  # Keyed by what the archive is built from, so it is rebuilt when a pin or a build script changes.
  KEY=$(cat "$ROOT/packaging/tools.lock" "$ROOT"/scripts/build-tools.sh "$ROOT"/scripts/build-git-macos.sh \
    "$ROOT"/scripts/build-synctex.sh "$ROOT"/scripts/tools-common.sh | shasum -a 256 | cut -c1-12)
  CACHE="$BUILD/cache/$KEY-$ARCH"
  TOOLS="$CACHE/refract-tools-v0.0.0-dev-darwin-$ARCH.tar.gz"
  if [ ! -f "$TOOLS" ]; then
    [ "$ARCH" = "$(uname -m | sed 's/x86_64/amd64/')" ] ||
      { echo "error: the $ARCH tools are built on a $ARCH Mac (Git and synctex are compiled); build them there, or set TOOLS_ARCHIVE" >&2; exit 1; }
    "$ROOT/scripts/build-tools.sh" darwin "$ARCH" v0.0.0-dev "$CACHE"
  fi
fi
[ -f "$TOOLS" ] || { echo "error: no tools archive at $TOOLS" >&2; exit 1; }

echo "==> server ($ARCH)"
mkdir -p "$BUILD/go-$ARCH"
(cd "$ROOT" && CGO_ENABLED=0 GOOS=darwin GOARCH="$ARCH" \
  go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$BUILD/go-$ARCH/refract" ./cmd/refract)

echo "==> app ($SWIFT_ARCH)"
swift build -c release --arch "$SWIFT_ARCH" --package-path "$ROOT/macos" --scratch-path "$BUILD/swift"
BIN=$(swift build -c release --arch "$SWIFT_ARCH" --package-path "$ROOT/macos" --scratch-path "$BUILD/swift" --show-bin-path)

echo "==> assembling $APP"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources/Server/bin" "$APP/Contents/Resources/Server/libexec/refract" "$APP/Contents/Resources"
cp "$BIN/Refract" "$APP/Contents/MacOS/Refract"
cp "$BUILD/go-$ARCH/refract" "$APP/Contents/Resources/Server/bin/refract"
tar -xzf "$TOOLS" -o -C "$APP/Contents/Resources/Server/libexec/refract"
sed "s/@VERSION@/$NUM/g" "$ROOT/macos/Resources/Info.plist" > "$APP/Contents/Info.plist"
cp "$ROOT/macos/Resources/AppIcon.icns" "$APP/Contents/Resources/AppIcon.icns"
cp -R "$ROOT/macos/Resources/en.lproj" "$ROOT/macos/Resources/es.lproj" "$APP/Contents/Resources/"
# The server refuses bundled tools that group or others could replace (internal/tools).
chmod -R go-w "$APP"
chmod 755 "$APP/Contents/MacOS/Refract" "$APP/Contents/Resources/Server/bin/refract"

"$ROOT/macos/sign-app.sh" "$APP"
# The release checks (Developer ID, timestamp) run in notarize.sh, from `make mac-release`.
"$ROOT/macos/verify-app.sh" "$APP"
echo "$APP"
