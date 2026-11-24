#!/bin/sh
# Checks a signed Refract.app: the seal is valid, and every Mach-O inside it (codesign --deep
# doesn't look under Resources) is validly signed with the hardened runtime and no entitlements.
#
# Usage: macos/verify-app.sh [--release] <Refract.app>
#   --release  also fail on anything ad hoc, unsigned, without a secure timestamp, or not signed
#              by the same Developer ID Application team as the app itself
set -eu
RELEASE=0
if [ "${1:-}" = "--release" ]; then RELEASE=1; shift; fi
[ $# -eq 1 ] && [ -d "$1/Contents" ] || { echo "usage: verify-app.sh [--release] <Refract.app>" >&2; exit 2; }
APP=$1
fail() { echo "error: $*" >&2; exit 1; }

echo "==> verifying $APP"
codesign --verify --deep --strict --verbose=2 "$APP" || fail "the app's signature is not valid"

# A link out of the bundle, or to nothing, is rejected by notarization.
ROOT=$(cd "$APP" && pwd -P)
find "$APP" -type l -print | while IFS= read -r l; do
  t=$(realpath "$l" 2>/dev/null) || fail "dangling symlink: $l"
  case "$t" in "$ROOT"/*) ;; *) fail "symlink leaves the bundle: $l -> $t" ;; esac
done

# Updates: Sparkle is embedded whole (its helpers signed, no unused XPC services) and the app knows
# whom to trust. A release without the key, or with a feed that is not https, can't update safely.
FW="$APP/Contents/Frameworks/Sparkle.framework"
PLIST="$APP/Contents/Info.plist"
KEY=$(/usr/libexec/PlistBuddy -c 'Print :SUPublicEDKey' "$PLIST" 2>/dev/null || true)
FEED=$(/usr/libexec/PlistBuddy -c 'Print :SUFeedURL' "$PLIST" 2>/dev/null || true)
[ -d "$FW/Versions/B/Updater.app" ] && [ -f "$FW/Versions/B/Autoupdate" ] || fail "Sparkle.framework is missing or incomplete"
[ ! -e "$FW/Versions/B/XPCServices" ] || fail "Sparkle's XPC services are shipped but unused (the app is not sandboxed)"
codesign --verify --strict "$FW" || fail "Sparkle.framework is not validly signed"
codesign --verify --strict "$FW/Versions/B/Updater.app" || fail "Sparkle's Updater.app is not validly signed"
if [ "$RELEASE" = 1 ] && /usr/libexec/PlistBuddy -c 'Print :RefractAllowsUpdateFeedOverride' "$PLIST" >/dev/null 2>&1; then
  fail "RefractAllowsUpdateFeedOverride is set: a release must not let the environment move its update feed"
fi
if [ -n "$KEY" ]; then
  [ "$(printf '%s' "$KEY" | base64 -D 2>/dev/null | wc -c | tr -d ' ')" = 32 ] || fail "SUPublicEDKey is not a 32-byte Ed25519 public key"
  case "$FEED" in
    https://*) ;;
    # A test build (REFRACT_UPDATE_FEED_BASE, macos/README.md) may serve its feed from this computer.
    http://127.0.0.1[:/]*|http://localhost[:/]*|"http://[::1]"[:/]*)
      [ "$RELEASE" = 1 ] && fail "SUFeedURL must be an https URL in a release, got: $FEED" ;;
    *) fail "SUFeedURL must be an https URL, got: ${FEED:-nothing}" ;;
  esac
  echo "ok: updates on, feed $FEED"
elif [ "$RELEASE" = 1 ]; then
  fail "no SUPublicEDKey: a release can't verify updates (set REFRACT_SPARKLE_PUBLIC_KEY)"
else
  echo "note: no SUPublicEDKey, updates are off in this build"
fi

TEAM=$(codesign -dvv "$APP" 2>&1 | sed -n 's/^TeamIdentifier=//p')
COUNT=0
LIST=$(mktemp)
trap 'rm -f "$LIST"' EXIT
find "$APP" -type f -print > "$LIST"
while IFS= read -r f; do
  case "$(file -b "$f")" in *Mach-O*) ;; *) continue ;; esac
  COUNT=$((COUNT + 1))
  codesign --verify --strict "$f" || fail "$f is not validly signed"
  # A universal binary has one signature per slice: the policy holds for each of them.
  archs=$(lipo -archs "$f" 2>/dev/null) || fail "cannot read the architectures of $f"
  [ -n "$archs" ] || fail "no architecture found in $f"
  for arch in $archs; do
    info=$(codesign -dvv --arch "$arch" "$f" 2>&1) || fail "cannot read the $arch signature of $f"
    printf '%s\n' "$info" | grep -q 'flags=.*runtime' || fail "$f ($arch) lacks the hardened runtime"
    ent=$(codesign -d --entitlements - --xml --arch "$arch" "$f" 2>/dev/null | tr -d '[:space:]')
    if [ -n "$ent" ]; then
      # The one exception: an ad hoc development build, whose app executable switches library validation
      # off so that it can load the embedded Sparkle.framework (see sign-app.sh). Never in a release.
      if [ "$RELEASE" = 0 ] && [ "$f" = "$APP/Contents/MacOS/Refract" ] && \
         [ "$(printf '%s' "$ent" | grep -o '<key>[^<]*</key>' | tr -d '\n')" = "<key>com.apple.security.cs.disable-library-validation</key>" ] && \
         printf '%s\n' "$info" | grep -q '^Signature=adhoc'; then :
      else
        fail "$f ($arch) has entitlements (none are expected)"
      fi
    fi
    if [ "$RELEASE" = 1 ]; then
      printf '%s\n' "$info" | grep -q '^Signature=adhoc' && fail "$f ($arch) is signed ad hoc"
      printf '%s\n' "$info" | grep -q '^Authority=Developer ID Application:' || fail "$f ($arch) is not signed with a Developer ID Application certificate"
      printf '%s\n' "$info" | grep -q '^Timestamp=' || fail "$f ($arch) has no secure timestamp"
      [ -n "$TEAM" ] && [ "$TEAM" != "not set" ] || fail "the app has no team identifier"
      [ "$(printf '%s\n' "$info" | sed -n 's/^TeamIdentifier=//p')" = "$TEAM" ] || fail "$f ($arch) is signed by another team than the app"
    fi
  done
done < "$LIST"
[ "$COUNT" -gt 0 ] || fail "no Mach-O found in $APP"
echo "ok: $COUNT Mach-O files, all validly signed with the hardened runtime$([ "$RELEASE" = 1 ] && echo " by team $TEAM")"
