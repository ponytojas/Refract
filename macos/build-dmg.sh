#!/bin/sh
# Builds the Refract disk image from a built Refract.app (scripts/make-dmg.py) and, with a
# Developer ID identity, signs it, notarizes it, staples the ticket and checks that Gatekeeper
# accepts it. Without an identity the image is left unsigned (a development build).
#
# Usage: macos/build-dmg.sh <Refract.app> <output.dmg>
#   REFRACT_SIGN_IDENTITY  "Developer ID Application: Name (TEAMID)": sign and notarize the image
#                          (default "-" or empty: do neither). Same variable as sign-app.sh.
#   REFRACT_KEYCHAIN       optional keychain to look the identity up in (CI uses a temporary one)
#   REFRACT_NOTARY_PROFILE, or REFRACT_NOTARY_KEY_PATH with REFRACT_NOTARY_KEY_ID and
#   REFRACT_NOTARY_ISSUER, and REFRACT_NOTARY_TIMEOUT: as in notarize.sh (values are never printed)
#   REFRACT_PYTHON         python with dmgbuild installed (scripts/requirements-dmg.txt); default python3
# The app inside should already be notarized and stapled (notarize.sh): the image carries its own ticket.
set -eu
[ $# -eq 2 ] && [ -d "$1/Contents" ] || { echo "usage: build-dmg.sh <Refract.app> <output.dmg>" >&2; exit 2; }
APP=$1 DMG=$2
ROOT=$(cd "$(dirname "$0")/.." && pwd)
{ set +x; } 2>/dev/null   # never trace the credentials below, even under sh -x
IDENTITY=${REFRACT_SIGN_IDENTITY:--}

echo "==> disk image"
"${REFRACT_PYTHON:-python3}" "$ROOT/scripts/make-dmg.py" "$APP" "$DMG"

if [ "$IDENTITY" = "-" ]; then
  echo "==> not signed (no Developer ID identity): Gatekeeper will not accept it"
  exit 0
fi
case "$IDENTITY" in
  "Developer ID Application:"*) ;;
  *) echo "error: REFRACT_SIGN_IDENTITY must be a Developer ID Application identity to sign the image" >&2; exit 2 ;;
esac

if [ -n "${REFRACT_NOTARY_PROFILE:-}" ]; then
  set -- --keychain-profile "$REFRACT_NOTARY_PROFILE"
elif [ -n "${REFRACT_NOTARY_KEY_PATH:-}" ] && [ -n "${REFRACT_NOTARY_KEY_ID:-}" ] && [ -n "${REFRACT_NOTARY_ISSUER:-}" ]; then
  [ -f "$REFRACT_NOTARY_KEY_PATH" ] || { echo "error: REFRACT_NOTARY_KEY_PATH is not a file" >&2; exit 2; }
  set -- --key "$REFRACT_NOTARY_KEY_PATH" --key-id "$REFRACT_NOTARY_KEY_ID" --issuer "$REFRACT_NOTARY_ISSUER"
else
  echo "error: set REFRACT_NOTARY_PROFILE, or REFRACT_NOTARY_KEY_PATH with REFRACT_NOTARY_KEY_ID and REFRACT_NOTARY_ISSUER" >&2
  exit 2
fi

echo "==> signing the image"
if [ -n "${REFRACT_KEYCHAIN:-}" ]; then
  codesign --force --timestamp --keychain "$REFRACT_KEYCHAIN" --sign "$IDENTITY" "$DMG"
else
  codesign --force --timestamp --sign "$IDENTITY" "$DMG"
fi
codesign --verify --verbose=2 "$DMG"

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
echo "==> submitting to Apple"
xcrun notarytool submit "$DMG" "$@" --output-format json > "$WORK/submit.json" || true
ID=$(plutil -extract id raw -o - "$WORK/submit.json" 2>/dev/null || echo "")
if [ -z "$ID" ]; then
  cat "$WORK/submit.json"
  echo "error: Apple did not accept the upload" >&2
  exit 1
fi
echo "==> submitted: $ID"
xcrun notarytool wait "$ID" "$@" --timeout "${REFRACT_NOTARY_TIMEOUT:-2h}" --output-format json > "$WORK/result.json" || true
cat "$WORK/result.json"
STATUS=$(plutil -extract status raw -o - "$WORK/result.json" 2>/dev/null || echo unknown)
if [ "$STATUS" != "Accepted" ]; then
  echo "error: notarization status: $STATUS" >&2
  xcrun notarytool log "$ID" "$@" || true
  exit 1
fi

echo "==> stapling"
n=0
until xcrun stapler staple "$DMG"; do
  n=$((n + 1))
  [ "$n" -lt 5 ] || { echo "error: stapling failed (submission $ID)" >&2; exit 1; }
  sleep 30
done
xcrun stapler validate "$DMG"
echo "==> Gatekeeper"
spctl --assess --type open --context context:primary-signature -vv "$DMG"
echo "ok: $DMG is signed, notarized and stapled"
