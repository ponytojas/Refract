#!/bin/sh
# Builds the tools archive of one target: tectonic, synctex, rg and, for macOS and
# Windows, a private Git runtime, with licences and a manifest. Everything
# downloaded is checked against packaging/tools.lock.
# Usage: scripts/build-tools.sh <os> <arch> <tag> <output-dir>
#   os: darwin|linux|windows   arch: amd64|arm64   tag: the Refract release tag
# Writes <output-dir>/refract-tools-<tag>-<os>-<arch>.tar.gz (.zip on Windows).
#
# Host: darwin targets build on macOS of the same architecture (Git and synctex are
# compiled); linux targets build on Linux of the same architecture (static synctex);
# the windows target is cross-built on Linux amd64 with llvm-mingw (needs unzip, zip).
# Needs curl, git, tar, python3.
# TEX_SEED_DIR: the output of scripts/build-tex-seed.sh (tex-seed/ and licenses/tex/);
# every archive carries it. The workflow builds it once, on Linux, and passes it in. When
# it is unset and the target's Tectonic runs on this host (a local build), the seed is
# built here with that Tectonic (needs network); a cross build without it fails.
#
# The archive is extracted into <exe dir>/../libexec/refract/ (macOS, Linux) or
# next to the executable (Windows); see packaging/README.md for the layout.
# shellcheck disable=SC2154
set -eu
. "$(dirname "$0")/tools-common.sh"
[ $# -eq 4 ] || die "usage: build-tools.sh <os> <arch> <tag> <output-dir>"
OS=$1 ARCH=$2 TAG=$3 DEST=$4
EXE=
[ "$OS" = windows ] && EXE=.exe

if ! asset=$(tectonic_asset "$OS" "$ARCH"); then
  die "no $OS-$ARCH tools archive: upstream Tectonic publishes no build for it (documented gap in packaging/README.md)"
fi
tectonic_triple=${asset%|*} tectonic_ext=${asset#*|}
rg_asset=$(ripgrep_asset "$OS" "$ARCH") || die "no ripgrep build for $OS-$ARCH"
rg_triple=${rg_asset%|*} rg_ext=${rg_asset#*|}

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
STAGE="$WORK/stage"
mkdir -p "$STAGE/licenses/tectonic" "$STAGE/licenses/ripgrep" "$STAGE/licenses/synctex" "$STAGE/licenses/zlib" "$DEST"
DEST=$(cd "$DEST" && pwd)

unpack() { # archive dir
  mkdir -p "$2"
  case "$1" in
    *.zip) unzip -q "$1" -d "$2" ;;
    *) tar -xzf "$1" -C "$2" ;;
  esac
}

# Tectonic.
fetch "https://github.com/tectonic-typesetting/tectonic/releases/download/tectonic%40${TECTONIC_VERSION}/tectonic-${TECTONIC_VERSION}-${tectonic_triple}.${tectonic_ext}" \
  "$(lock_get TECTONIC_SHA256 "$OS" "$ARCH")" "$WORK/tectonic.$tectonic_ext"
unpack "$WORK/tectonic.$tectonic_ext" "$WORK/tectonic"
cp "$WORK/tectonic/tectonic$EXE" "$STAGE/tectonic$EXE"
fetch "https://raw.githubusercontent.com/tectonic-typesetting/tectonic/tectonic%40${TECTONIC_VERSION}/LICENSE" \
  "$TECTONIC_LICENSE_SHA256" "$STAGE/licenses/tectonic/LICENSE"

# ripgrep.
fetch "https://github.com/BurntSushi/ripgrep/releases/download/${RIPGREP_VERSION}/ripgrep-${RIPGREP_VERSION}-${rg_triple}.${rg_ext}" \
  "$(lock_get RIPGREP_SHA256 "$OS" "$ARCH")" "$WORK/rg.$rg_ext"
unpack "$WORK/rg.$rg_ext" "$WORK/rg"
RGDIR="$WORK/rg/ripgrep-${RIPGREP_VERSION}-${rg_triple}"
cp "$RGDIR/rg$EXE" "$STAGE/rg$EXE"
cp "$RGDIR/COPYING" "$RGDIR/LICENSE-MIT" "$RGDIR/UNLICENSE" "$STAGE/licenses/ripgrep/"

# synctex (and the zlib compiled into it).
if [ "$OS" = windows ]; then
  case "$(uname -s)-$(uname -m)" in
    Linux-x86_64) ;;
    *) die "the Windows tools are cross-built on Linux amd64" ;;
  esac
  case "$ARCH" in amd64) mingw=x86_64-w64-mingw32 ;; *) die "unsupported arch $ARCH" ;; esac
  fetch "https://github.com/mstorsjo/llvm-mingw/releases/download/${LLVM_MINGW_TAG}/llvm-mingw-${LLVM_MINGW_TAG}-ucrt-ubuntu-22.04-x86_64.tar.xz" \
    "$LLVM_MINGW_SHA256_linux_amd64" "$WORK/llvm-mingw.txz"
  mkdir "$WORK/llvm-mingw"
  tar -xJf "$WORK/llvm-mingw.txz" -C "$WORK/llvm-mingw" --strip-components=1
  CC="$WORK/llvm-mingw/bin/${mingw}-clang" AR="$WORK/llvm-mingw/bin/llvm-ar" \
    "$TOOLS_ROOT/scripts/build-synctex.sh" "$OS" "$ARCH" "$STAGE/synctex$EXE"
else
  "$TOOLS_ROOT/scripts/build-synctex.sh" "$OS" "$ARCH" "$STAGE/synctex$EXE"
fi
mv "$STAGE/synctex$EXE.LICENSE.synctex" "$STAGE/licenses/synctex/LICENSE"
mv "$STAGE/synctex$EXE.LICENSE.zlib" "$STAGE/licenses/zlib/LICENSE"

# Git: private runtime for macOS and Windows; Linux packages depend on the system Git.
case "$OS" in
  darwin)
    "$TOOLS_ROOT/scripts/build-git-macos.sh" "$STAGE/git"
    mkdir -p "$STAGE/licenses/git/source"
    mv "$STAGE/git/COPYING" "$STAGE/git/LGPL-2.1" "$STAGE/licenses/git/"
    mv "$STAGE/git/source/git-${GIT_VERSION}.tar.xz" "$STAGE/licenses/git/source/"
    rmdir "$STAGE/git/source"
    # The scripts that control the build are part of the corresponding source (GPLv2 §3).
    cp "$TOOLS_ROOT/scripts/build-git-macos.sh" "$TOOLS_ROOT/scripts/tools-common.sh" "$STAGE/licenses/git/source/"
    cat > "$STAGE/licenses/git/SOURCE-OFFER.txt" <<OFFER
Git ${GIT_VERSION} is free software under the GNU General Public License version 2
(COPYING) and, for some files, the LGPL 2.1 (LGPL-2.1). The complete corresponding
source of the bundled git executable is in this folder: source/git-${GIT_VERSION}.tar.xz
(the unmodified official tarball, SHA-256 ${GIT_SOURCE_SHA256}).
It was built by source/build-git-macos.sh (with source/tools-common.sh), the same
scripts as in the Refract repository (https://github.com/Use-Refract/Refract) at
release ${TAG}, against libraries that are part of macOS.
OFFER
    ;;
  windows)
    hash=$(lock_get MINGIT_SHA256 "$OS" "$ARCH")
    fetch "https://github.com/git-for-windows/git/releases/download/${MINGIT_TAG}/MinGit-${MINGIT_VERSION}-64-bit.zip" "$hash" "$WORK/mingit.zip"
    mkdir -p "$STAGE/git" "$STAGE/licenses/git"
    unzip -q "$WORK/mingit.zip" -d "$STAGE/git"
    cp "$STAGE/git/LICENSE.txt" "$STAGE/licenses/git/LICENSE.txt"
    cp "$STAGE/git/etc/package-versions.txt" "$STAGE/licenses/git/package-versions.txt"
    cat > "$STAGE/licenses/git/SOURCE-OFFER.txt" <<OFFER
git/ is MinGit ${MINGIT_VERSION} (Git for Windows ${MINGIT_TAG}, unmodified), which bundles Git
(GPLv2) and MSYS2/MinGW components under their own licences (LICENSE.txt in this
folder; the exact component versions are in package-versions.txt).
Source code: the Git for Windows source for this release is at
https://github.com/git-for-windows/git/tree/${MINGIT_TAG} and the sources of the
MSYS2 packages listed in package-versions.txt are at
https://github.com/git-for-windows/MSYS2-packages and
https://github.com/git-for-windows/MINGW-packages. For three years from the
release ${TAG} of Refract, the Refract maintainers will also send a copy of any of
these sources on request (https://github.com/Use-Refract/Refract/issues).
OFFER
    ;;
esac

# The TeX seed (see packaging/README.md, "TeX seed").
if [ -z "${TEX_SEED_DIR:-}" ]; then
  "$STAGE/tectonic$EXE" --version >/dev/null 2>&1 ||
    die "set TEX_SEED_DIR to the output of scripts/build-tex-seed.sh (the $OS-$ARCH Tectonic does not run here to build it)"
  TEX_SEED_DIR=$WORK/tex-seed
  "$TOOLS_ROOT/scripts/build-tex-seed.sh" "$STAGE/tectonic$EXE" "$TEX_SEED_DIR"
fi
[ -f "$TEX_SEED_DIR/tex-seed/seed.json" ] && [ -f "$TEX_SEED_DIR/licenses/tex/NOTICE.md" ] || die "$TEX_SEED_DIR is not a TeX seed build (scripts/build-tex-seed.sh)"
python3 -I "$TOOLS_ROOT/scripts/tex-seed.py" verify "$TEX_SEED_DIR/tex-seed" >/dev/null
cp -R "$TEX_SEED_DIR/tex-seed" "$STAGE/tex-seed"
mkdir -p "$STAGE/licenses/tex"
cp -R "$TEX_SEED_DIR/licenses/tex/." "$STAGE/licenses/tex/"

python3 -I "$TOOLS_ROOT/scripts/tools-archive.py" manifest "$STAGE" "$OS" "$ARCH" "$TAG"

name="refract-tools-${TAG}-${OS}-${ARCH}"
if [ "$OS" = windows ]; then
  (cd "$STAGE" && zip -qr -X "$DEST/$name.zip" .)
  out="$DEST/$name.zip"
else
  tar -czf "$DEST/$name.tar.gz" -C "$STAGE" .
  out="$DEST/$name.tar.gz"
fi
python3 -I "$TOOLS_ROOT/scripts/tools-archive.py" check "$out" "$OS" "$ARCH"
echo "$out"
