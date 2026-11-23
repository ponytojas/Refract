#!/bin/sh
# Smoke test of an extracted tools archive, with a stripped environment: no PATH
# entries for the tools and none that could supply a system Git.
# Usage: scripts/smoke-tools.sh <extracted-dir> <os>   (os: darwin|linux|windows)
# Runs each tool's --version (synctex: help), checks the executables bring their
# libraries (macOS: only OS libraries; Linux: static), and for Git creates a
# project repository and a checkpoint, and reads an HTTPS remote (as Refract's
# push and pull do), using only the bundled runtime and the environment Refract
# gives it (internal/tools GitEnv: GIT_CONFIG_NOSYSTEM=1, and on macOS
# GIT_EXEC_PATH and GIT_TEMPLATE_DIR). Needs network access for the HTTPS check.
set -eu
DIR=$(cd "$1" && pwd)
OS=$2
fail() { echo "smoke: $*" >&2; exit 1; }

EXE=
case "$OS" in
  windows)
    EXE=.exe
    # A POSIX path, so Git Bash converts PATH correctly for the Windows programs.
    WINDIR_PATH=$(cygpath -u "${SYSTEMROOT:-C:\\Windows}")/System32
    run() { env -i "SYSTEMROOT=${SYSTEMROOT:-C:\\Windows}" "PATH=$WINDIR_PATH" "$@"; }
    ;;
  *) run() { env -i PATH=/nonexistent "$@"; } ;;
esac

run "$DIR/tectonic$EXE" --version | head -1
run "$DIR/rg$EXE" --version | head -1
# synctex has no --version: `help` prints its banner (and exits non-zero on some builds).
run "$DIR/synctex$EXE" help 2>&1 | head -1 | grep -q SyncTeX || fail "synctex help"

case "$OS" in
  darwin)
    want=${MACOSX_DEPLOYMENT_TARGET:-13.0}
    for f in tectonic synctex rg git/bin/git git/libexec/git-core/git git/libexec/git-core/git-remote-http; do
      bad=$(otool -L "$DIR/$f" | tail -n +2 | awk '{print $1}' | grep -v -e '^/usr/lib/' -e '^/System/Library/' || true)
      [ -z "$bad" ] || fail "$f links libraries that are not part of macOS: $bad"
      # LC_BUILD_VERSION says `minos X`; older binaries (Tectonic's x86_64 build) have
      # LC_VERSION_MIN_MACOSX, which says `version X`. Either comes before any tool version.
      minos=$(vtool -show-build "$DIR/$f" 2>/dev/null | awk '$1 == "minos" || $1 == "version" {print $2; exit}')
      [ -n "$minos" ] || fail "$f declares no minimum macOS version"
      newer=$(awk -v a="$minos" -v b="$want" 'BEGIN { split(a, x, "."); split(b, y, "."); print (x[1] > y[1] || (x[1] == y[1] && x[2] + 0 > y[2] + 0)) }')
      [ "$newer" = 0 ] || fail "$f needs macOS $minos, newer than the supported $want"
    done
    ;;
  linux)
    for f in tectonic synctex rg; do
      ldd "$DIR/$f" 2>&1 | grep -q -e 'not a dynamic' -e 'statically linked' || fail "$f is not static"
    done
    ;;
esac

[ "$OS" = linux ] && { echo "smoke: ok ($OS)"; exit 0; }

# The entry points and environment internal/tools uses for a bundled Git.
case "$OS" in
  windows)
    # MinGit's launcher puts its own ucrt64/bin and usr/bin first on PATH; Git then
    # finds its helpers relative to itself, so GIT_EXEC_PATH must not be set.
    GIT="$DIR/git/cmd/git.exe"
    GITENV="GIT_CONFIG_NOSYSTEM=1"
    WANT_EXEC="$(cygpath -m "$DIR")/git/ucrt64/libexec/git-core"
    ;;
  *)
    GIT="$DIR/git/bin/git"
    GITENV="GIT_CONFIG_NOSYSTEM=1 GIT_EXEC_PATH=$DIR/git/libexec/git-core GIT_TEMPLATE_DIR=$DIR/git/share/git-core/templates"
    WANT_EXEC="$DIR/git/libexec/git-core"
    ;;
esac
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
# shellcheck disable=SC2086 # GITENV is a list of NAME=value words without spaces.
g() { run "HOME=$WORK" "USERPROFILE=$WORK" $GITENV "$GIT" -C "$WORK/project" -c user.name=Refract -c user.email=refract@example.invalid "$@"; }
mkdir "$WORK/project"
g --version
g init -q
echo hello > "$WORK/project/main.tex"
g add main.tex
g commit -q -m "first checkpoint"
echo more >> "$WORK/project/main.tex"
g commit -q -a -m "second checkpoint"
test "$(g rev-list --count HEAD)" = 2 || fail "git history"
g status --porcelain | grep -q . && fail "git status is not clean"
g tag checkpoint-1
g gc -q
# The helpers come from the bundled folder, not from a system path.
[ "$(g --exec-path)" = "$WANT_EXEC" ] || fail "git exec path is $(g --exec-path), not $WANT_EXEC"
# HTTPS remotes (Refract's push and pull) work with the bundled runtime alone:
# macOS's libcurl and certificates, MinGit's Schannel libcurl and the Windows store.
# Retried, so a passing network hiccup isn't mistaken for a broken runtime.
remote_ok=
for attempt in 1 2 3; do
  if g ls-remote https://github.com/git/git HEAD | grep -q 'HEAD$'; then remote_ok=1; break; fi
  [ "$attempt" = 3 ] || sleep $((attempt * 5))
done
[ -n "$remote_ok" ] || fail "git cannot read an HTTPS remote (3 attempts)"
echo "smoke: ok ($OS)"
