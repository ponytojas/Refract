#!/bin/sh
# Smoke test of the TeX seed in an extracted tools archive: with the network blocked,
# a read-only runtime and an empty home, the seed alone must compile every built-in
# template and every document in packaging/tex-seed/documents, without one download,
# and SyncTeX must map source and PDF positions both ways.
# Usage: scripts/smoke-tex-seed.sh <extracted-dir> <os>   (os: darwin|linux|windows)
# Network blocked by: macOS sandbox-exec; Linux `sudo unshare -n` (then setpriv back to
# this user); both are proven with a probe. Windows has no per-process sandbox: it adds a
# firewall rule on tectonic.exe when it can (an administrator shell; the rule is not
# probed) and always passes --only-cached, so a file missing from the seed fails the
# compile instead of being downloaded. The log says which of these held.
# Run the checks of the seed build itself with build-tex-seed.sh.
set -eu
DIR=$(cd "$1" && pwd)
OS=$2
ROOT=$(cd "$(dirname "$0")/.." && pwd)
fail() { echo "smoke-tex-seed: $*" >&2; exit 1; }
EXE=
[ "$OS" = windows ] && EXE=.exe
[ -d "$DIR/tex-seed/cache" ] || fail "no tex-seed/cache in $DIR"

URL=$(python3 -I "$ROOT/scripts/tex-seed.py" verify "$DIR/tex-seed") || exit 1

WORK=$(mktemp -d)
FWRULE=
cleanup() {
  chmod -R u+w "$DIR" 2>/dev/null || true
  [ -z "$FWRULE" ] || netsh advfirewall firewall delete rule name="$FWRULE" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT
HOMEDIR=$WORK/home
CACHE=$WORK/cache
mkdir -p "$HOMEDIR" "$WORK/projects"
if [ "$OS" = windows ]; then HOMEDIR=$(cygpath -w "$HOMEDIR"); fi
# What the installer does: the seed becomes the cache the first compile starts from.
cp -R "$DIR/tex-seed/cache" "$CACHE"
chmod -R u+w "$CACHE"
if [ "$OS" = windows ]; then CACHE=$(cygpath -w "$CACHE"); fi

# The runtime is read-only while the tests run.
case "$OS" in
  windows) attrib +R "$(cygpath -w "$DIR/tectonic.exe")" >/dev/null 2>&1 || true ;;
  *) chmod -R a-w "$DIR" ;;
esac

ONLY=
NET=
case "$OS" in
  darwin)
    SB='(version 1)(allow default)(deny network*)'
    offline() { sandbox-exec -p "$SB" "$@"; }
    ;;
  linux)
    offline() { sudo unshare -n setpriv --reuid="$(id -u)" --regid="$(id -g)" --clear-groups "$@"; }
    ;;
  windows)
    ONLY=--only-cached
    FWRULE=refract-smoke-tex-seed-$$
    TECT_WIN=$(cygpath -w "$DIR/tectonic.exe")
    if netsh advfirewall firewall add rule name="$FWRULE" dir=out action=block program="$TECT_WIN" >/dev/null 2>&1; then
      NET="firewall rule on tectonic.exe (not probed) plus --only-cached"
    else
      FWRULE=
      NET="NOT blocked (no firewall rule: not an administrator); --only-cached forbids downloads"
    fi
    offline() { "$@"; }
    ;;
esac
# Prove the block works: the same sandbox must not reach the network.
case "$OS" in
  darwin|linux)
    if offline curl -fsS --max-time 5 -o /dev/null "$URL" 2>/dev/null; then fail "the network is not blocked"; fi
    NET="blocked and probed"
    ;;
esac
echo "smoke-tex-seed: network: $NET"

n=0
compile() { # label dir
  label=$1 dir=$2
  proj=$WORK/projects/$(printf %s "$label" | tr -c 'A-Za-z0-9\n' -)
  cp -R "$dir" "$proj"
  main=main.tex
  if [ ! -f "$proj/main.tex" ]; then
    for f in "$proj"/*.tex; do main=${f##*/}; break; done
  fi
  mkdir "$proj/.out"
  log=$proj/.out/tectonic.log
  # The runtime finds nothing it should not: a fresh home, the seed as the only cache. Windows
  # keeps its environment (Tectonic needs the user's known folders there).
  case "$OS" in
    windows) TENV="env" ;;
    *) TENV="env -i PATH=/nonexistent" ;;
  esac
  # shellcheck disable=SC2086
  (cd "$proj" && offline $TENV "HOME=$HOMEDIR" "TECTONIC_CACHE_DIR=$CACHE" \
    "$DIR/tectonic$EXE" "$main" --outdir .out --synctex --keep-logs --untrusted -Z continue-on-errors $ONLY --bundle "$URL") >"$log" 2>&1 \
    || { cat "$log" >&2; fail "$label did not compile offline"; }
  if grep -qi 'downloading' "$log"; then grep -i downloading "$log" >&2; fail "$label downloaded files"; fi
  # continue-on-errors exits 0 with a broken PDF when a file is missing: any error fails.
  if grep -Eq '^error:|failed to download|not available in the cache' "$log"; then cat "$log" >&2; fail "$label reported errors"; fi
  stem=${main%.tex}
  [ "$(head -c 4 "$proj/.out/$stem.pdf")" = "%PDF" ] || fail "$label produced no PDF"
  [ -s "$proj/.out/$stem.synctex.gz" ] || fail "$label produced no synctex data"
  n=$((n + 1))
  echo "smoke-tex-seed: $label ok"
}

for t in "$ROOT"/internal/projects/templates/*/*/; do
  compile "$(basename "$(dirname "$t")")/$(basename "$t")" "$t"
done
for d in "$ROOT"/packaging/tex-seed/documents/*.tex; do
  doc=$WORK/doc-$(basename "$d" .tex)
  mkdir "$doc"
  cp "$d" "$doc/main.tex"
  compile "document $(basename "$d" .tex)" "$doc"
done

# SyncTeX round trip on the blank-10pt document.
sync=$WORK/projects/$(printf %s "document blank-10pt" | tr -c 'A-Za-z0-9\n' -)
case "$OS" in
  windows) SYNC=$DIR/synctex.exe ;;
  *) SYNC=$DIR/synctex ;;
esac
# synctex matches the absolute, symlink-free path Tectonic recorded (Refract passes one too).
sync=$(cd "$sync" && pwd -P)
case "$OS" in
  windows) sync=$(cygpath -m "$sync") ;;
esac
pdf=$sync/.out/main.pdf
# (view prints x and y, the point edit wants back.) The document is "Hello, world." on line 3: view maps line -> page, edit maps page -> line.
view=$(cd "$sync" && "$SYNC" view -i "3:1:$sync/main.tex" -o "$pdf" 2>&1) || true
printf '%s\n' "$view" | grep -q '^Page:1$' || { printf '%s\n' "$view" >&2; fail "synctex view found no page"; }
x=$(printf '%s\n' "$view" | sed -n 's/^x:\([0-9.]*\).*/\1/p' | head -1)
y=$(printf '%s\n' "$view" | sed -n 's/^y:\([0-9.]*\).*/\1/p' | head -1)
[ -n "$x" ] && [ -n "$y" ] || fail "synctex view gave no position"
edit=$(cd "$sync" && "$SYNC" edit -o "1:$x:$y:$pdf" 2>&1) || true
printf '%s\n' "$edit" | grep -q '^Input:.*main\.tex' || { printf '%s\n' "$edit" >&2; fail "synctex edit found no source"; }
printf '%s\n' "$edit" | grep -q '^Line:3$' || { printf '%s\n' "$edit" >&2; fail "synctex edit found the wrong line"; }
echo "smoke-tex-seed: synctex round trip ok"
echo "smoke-tex-seed: ok ($OS, $n documents, network: $NET)"
