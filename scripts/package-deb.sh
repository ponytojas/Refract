#!/bin/sh
# Package an already-built Linux CLI. No services, server config or TeX dependencies.
# Usage: sh scripts/package-deb.sh v0.1.2 amd64 dist/refract-v0.1.2-linux-amd64/refract dist
set -eu
tag=$1
arch=$2
binary=$3
out=$4
case "$tag" in v[0-9]*) ;; *) echo "Expected a version tag starting with v" >&2; exit 1 ;; esac
case "$arch" in amd64|arm64) ;; *) echo "Unsupported architecture: $arch" >&2; exit 1 ;; esac
# Debian sorts prereleases before the final version.
version=$(printf '%s' "${tag#v}" | sed 's/-/~/')
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/usr/bin" "$work/usr/share/doc/refract" "$work/DEBIAN" "$out"
install -m 755 "$binary" "$work/usr/bin/refract"
install -m 644 LICENSE "$work/usr/share/doc/refract/copyright"
install -m 644 README.md "$work/usr/share/doc/refract/README.md"
cat > "$work/DEBIAN/control" <<EOF
Package: refract
Version: $version
Architecture: $arch
Maintainer: Daniel Villalobos <ponytojas@users.noreply.github.com>
Section: utils
Priority: optional
Homepage: https://github.com/ponytojas/Refract
Description: CLI for the Refract LaTeX workspace
 Sign in to ChatGPT on this computer with refract login, then upload
 the credential file to your Refract instance. No local server or
 LaTeX installation is required for login.
EOF
dpkg-deb --root-owner-group --build "$work" "$out/refract_${version}_${arch}.deb"
