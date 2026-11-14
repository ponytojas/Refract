#!/bin/sh
# Prepares the TeX bundle and the format file for the compile sandbox
# (REFRACT_SANDBOX=bwrap): the sandbox has no network, so the whole bundle
# must be on disk, and the cache must hold the format file before the first
# compile. Run it once per server, and again when Tectonic or the bundle changes.
#
# Usage: prepare-bundle.sh <folder>     (needs curl, tar and tectonic on the PATH)
#   <folder>/bundle   REFRACT_TECTONIC_BUNDLE=<folder>/bundle  (about 2.9 GB)
#   <folder>/cache    TECTONIC_CACHE_DIR=<folder>/cache        (the format file, 24 MB)
#
# The bundle and the digest are the ones the Dockerfile pins and the sandbox
# was tested with: keep them in sync with it. The format file's name holds the
# bundle's digest and Tectonic's format version, so use the same tectonic the
# server runs.
set -eu
BUNDLE_URL=https://data1b.fullyjustified.net/tlextras-2022.0r0.tar
BUNDLE_DIGEST=6ffe055852f8faf66c0acbe1a7fb27f87b869a90bad1204f3bf4d9683f597c7c

[ $# -eq 1 ] || { echo "usage: $0 <folder>" >&2; exit 1; }
DEST=$(mkdir -p "$1" && cd "$1" && pwd)
[ ! -e "$DEST/bundle" ] || { echo "$DEST/bundle exists: remove it first to prepare again" >&2; exit 1; }
command -v tectonic >/dev/null || { echo "tectonic is not on the PATH" >&2; exit 1; }
WORK=$(mktemp -d "$DEST/.work.XXXXXX")
trap 'chmod -R u+w "$WORK" 2>/dev/null; rm -rf "$WORK"' EXIT

# 1. Fetch and unpack the bundle. The tar is flat, and the tar stores every file with mode 000.
curl -fsSL -o "$WORK/bundle.tar" "$BUNDLE_URL"
mkdir "$WORK/bundle"
tar -xf "$WORK/bundle.tar" -C "$WORK/bundle"
rm "$WORK/bundle.tar"

# 2. Readable by everyone, writable by nobody (the folder's mode is set at the end: moving it needs write access).
find "$WORK/bundle" -type f -exec chmod 0444 {} +

# 3. Check it is the bundle that was tested.
got=$(tr -d ' \n' < "$WORK/bundle/SHA256SUM")
[ "$got" = "$BUNDLE_DIGEST" ] || { echo "unexpected bundle digest $got (want $BUNDLE_DIGEST)" >&2; exit 1; }

# 4. Build the format file with a trusted one-line document and a writable cache.
mkdir "$WORK/cache" "$WORK/warm"
printf '\\documentclass{article}\\begin{document}x\\end{document}\n' > "$WORK/warm/warm.tex"
if ! (cd "$WORK/warm" && TECTONIC_CACHE_DIR="$WORK/cache" tectonic warm.tex --untrusted --bundle "$WORK/bundle" >"$WORK/warm.log" 2>&1); then
  cat "$WORK/warm.log" >&2
  exit 1
fi
chmod -R a+rX "$WORK/cache"

mv "$WORK/bundle" "$DEST/bundle"
mv "$WORK/cache" "$DEST/cache"
chmod 0555 "$DEST/bundle" "$DEST/cache" "$DEST/cache/formats"
echo "ready: REFRACT_TECTONIC_BUNDLE=$DEST/bundle TECTONIC_CACHE_DIR=$DEST/cache"
