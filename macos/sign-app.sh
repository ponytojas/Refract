#!/bin/sh
# Signs Refract.app: every Mach-O inside it first, then the app that seals them.
#
# Usage: macos/sign-app.sh <Refract.app>
#   REFRACT_SIGN_IDENTITY  the signing identity, e.g. "Developer ID Application: Name (TEAMID)".
#                          Default "-": ad hoc, for development builds (not accepted by Gatekeeper).
#   REFRACT_KEYCHAIN       optional keychain to look the identity up in (CI uses a temporary one)
#
# Hardened runtime everywhere. No entitlements, on purpose: see macos/README.md, "Signing".
# A Developer ID Application identity also gets a secure timestamp, which notarization requires.
# Sparkle.framework is a bundle and is signed as one, see below.
# Signatures live in the files, so symlinks (Git's hundreds of git-* links) are not signed.
# Each nested Mach-O gets a stable identifier, <bundle id>.<file name>: without one, codesign
# keeps the linker's, which carries a hash that changes with every build.
set -eu
[ -z "${CODESIGN_IDENTITY:-}" ] || { echo "error: CODESIGN_IDENTITY is no longer used: set REFRACT_SIGN_IDENTITY instead" >&2; exit 2; }
[ $# -eq 1 ] && [ -d "$1/Contents" ] || { echo "usage: sign-app.sh <Refract.app>" >&2; exit 2; }
APP=$1
IDENTITY=${REFRACT_SIGN_IDENTITY:--}
# Only Developer ID certificates get Apple's timestamp service; others refuse it.
case "$IDENTITY" in
  "Developer ID Application:"*) TIMESTAMP=--timestamp ;;
  *) TIMESTAMP=--timestamp=none ;;
esac
# Library validation (part of the hardened runtime) loads only libraries signed by the same team.
# An ad hoc build has no team, so the embedded Sparkle.framework could never load: ad hoc builds
# (development only, never accepted by Gatekeeper) switch that one check off for the app's own
# executable, which is the only place a framework is loaded. A real identity gets no entitlement.
ENTITLEMENTS=
if [ "$IDENTITY" = "-" ]; then
  ENTITLEMENTS=$(mktemp)
  trap 'rm -f "$ENTITLEMENTS"' EXIT
  cat > "$ENTITLEMENTS" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>com.apple.security.cs.disable-library-validation</key><true/></dict></plist>
PLIST
fi
BUNDLE_ID=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "$APP/Contents/Info.plist")

# sign <path> [codesign options...]
sign() {
  f=$1; shift
  if [ -n "${REFRACT_KEYCHAIN:-}" ]; then
    codesign --force --options runtime "$TIMESTAMP" --keychain "$REFRACT_KEYCHAIN" --sign "$IDENTITY" "$@" "$f"
  else
    codesign --force --options runtime "$TIMESTAMP" --sign "$IDENTITY" "$@" "$f"
  fi
}

if [ "$IDENTITY" = "-" ]; then
  echo "==> signing ad hoc (development build: Gatekeeper will not accept it)"
else
  echo "==> signing with: $IDENTITY"
fi
# Inside out. codesign --deep doesn't sign what is under Resources, so each Mach-O is
# signed here (the tools, then the server, then the app executable) and the app seals them.
FW="$APP/Contents/Frameworks/Sparkle.framework"
(cd "$APP/Contents" && find . -type f ! -path './MacOS/*' ! -path './Frameworks/Sparkle.framework/*' -print) | while IFS= read -r rel; do
  f="$APP/Contents/${rel#./}"
  case "$(file -b "$f")" in
    *Mach-O*) sign "$f" --identifier "$BUNDLE_ID.$(basename "$f")" ;;
  esac
done
# Sparkle, signed as its documentation asks for a Developer ID: inside out, one at a time, with the
# hardened runtime and the same identity, never --deep. Its XPC services (for sandboxed apps) are
# not shipped (build-app.sh), so what is left is the updater helper Autoupdate, Updater.app and
# the framework that seals them.
if [ -d "$FW" ]; then
  V="$FW/Versions/B"
  [ -d "$V/Updater.app" ] && [ -f "$V/Autoupdate" ] || { echo "error: $FW is not the Sparkle 2 layout" >&2; exit 1; }
  [ ! -e "$V/XPCServices" ] || { echo "error: $FW still has XPC services (build-app.sh removes them: the app isn't sandboxed)" >&2; exit 1; }
  sign "$V/Autoupdate" --identifier org.sparkle-project.Sparkle.Autoupdate
  sign "$V/Updater.app"
  sign "$FW"
fi
# The app's executable and the app take the bundle id from Info.plist.
if [ -n "$ENTITLEMENTS" ]; then
  find "$APP/Contents/MacOS" -type f -print | while IFS= read -r f; do sign "$f" --entitlements "$ENTITLEMENTS"; done
  sign "$APP" --entitlements "$ENTITLEMENTS"
else
  find "$APP/Contents/MacOS" -type f -print | while IFS= read -r f; do sign "$f"; done
  sign "$APP"
fi
