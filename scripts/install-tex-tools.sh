#!/bin/sh
# Installs the Tectonic and synctex that the Docker image ships, into <dir>,
# on Linux (amd64 or arm64). CI uses it so tests compile real documents with
# the same versions as production. Keep the pins in sync with the Dockerfile.
# Usage: scripts/install-tex-tools.sh <dir>   (needs curl, git, a C compiler and zlib headers)
set -eu
DIR=$1
TECTONIC_VERSION=0.17.0
SYNCTEX_COMMIT=c3641da45b41cd61f9e625fe722d0a8d54cedaa9
mkdir -p "$DIR"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

case "$(uname -m)" in
  x86_64) triple=x86_64-unknown-linux-musl  sha=8533d07f9ccbd7a65824b9e0459041bca34af1eb33daba48f59215593753a3b7 ;;
  aarch64 | arm64) triple=aarch64-unknown-linux-musl sha=b10954a95404f3ab2328d2fa59a5ebab8e657f893fab096f98be8db7c0c979b8 ;;
  *) echo "unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac
curl -fsSL -o "$WORK/t.tgz" "https://github.com/tectonic-typesetting/tectonic/releases/download/tectonic%40${TECTONIC_VERSION}/tectonic-${TECTONIC_VERSION}-${triple}.tar.gz"
echo "$sha  $WORK/t.tgz" | sha256sum -c -
tar -xzf "$WORK/t.tgz" -C "$DIR" tectonic

# The synctex CLI from the official parser sources, as in the Dockerfile.
git clone -q https://github.com/jlaurens/synctex.git "$WORK/synctex"
git -C "$WORK/synctex" checkout -q --detach "$SYNCTEX_COMMIT"
cd "$WORK/synctex"
mkdir -p w2c
echo '#include "synctex_parser_c-auto.h"' > w2c/c-auto.h
cc -O2 -I. synctex_main.c synctex_parser.c synctex_parser_utils.c -lz -o "$DIR/synctex"

"$DIR/tectonic" --version
"$DIR/synctex" help 2>&1 | head -1
