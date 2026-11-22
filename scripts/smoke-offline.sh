#!/bin/sh
# Smoke test of `refract local` on a computer with no network and an empty profile: the first
# PDF must compile from the TeX seed that ships next to the tools.
# Usage: scripts/smoke-offline.sh <refract-binary> <extracted-tools-dir> <os>   (os: darwin|linux)
#
# Lays out an install (bin/refract, libexec/refract/ = the tools archive's contents), read-only,
# with a fresh HOME and REFRACT_DATA_DIR, and runs the whole test with the network blocked
# (macOS: sandbox-exec allowing only loopback; Linux: `sudo unshare -n` with lo up, then
# setpriv back to this user). Then, through the HTTP API: every built-in template in en and
# es compiles with no download and a SyncTeX round trip; a document needing a package the
# seed lacks reports "needs a download" quickly and keeps its text; and after an update (a new
# install with a changed seed id) the projects, refract.db and secret.key are byte-identical,
# a file the person had downloaded is still in the cache, and the seed files are refreshed.
# Windows is not covered (blocking its network needs an administrator).
set -eu
OS=${3:-}
ROOT=$(cd "$(dirname "$0")/.." && pwd)
case "${1:-}" in /*) BIN=$1 ;; *) BIN=$PWD/${1:-} ;; esac
TOOLS=$(cd "${2:-.}" && pwd)

if [ -z "${SMOKE_OFFLINE_INNER:-}" ]; then
  [ $# -eq 3 ] || { echo "usage: smoke-offline.sh <refract-binary> <tools-dir> <darwin|linux>" >&2; exit 2; }
  [ -x "$BIN" ] || { echo "smoke-offline: $BIN is not executable" >&2; exit 2; }
  [ -d "$TOOLS/tex-seed/cache" ] || { echo "smoke-offline: no tex-seed in $TOOLS" >&2; exit 2; }
  export SMOKE_OFFLINE_INNER=1
  case "$OS" in
    darwin)
      exec sandbox-exec -p '(version 1)(allow default)(deny network*)
(allow network-bind (local ip "localhost:*"))(allow network-inbound (local ip "localhost:*"))
(allow network-outbound (remote ip "localhost:*"))' "$0" "$BIN" "$TOOLS" "$OS"
      ;;
    linux)
      exec sudo -E unshare -n sh -c 'ip link set lo up && exec setpriv --reuid="$1" --regid="$2" --clear-groups "$3" "$4" "$5" "$6"' \
        sh "$(id -u)" "$(id -g)" "$0" "$BIN" "$TOOLS" "$OS"
      ;;
    *) echo "smoke-offline: unsupported os $OS" >&2; exit 2 ;;
  esac
fi

fail() { echo "smoke-offline: $*" >&2; exit 1; }
WORK=$(mktemp -d)
SERVER_PID=
cleanup() {
  [ -z "$SERVER_PID" ] || kill "$SERVER_PID" 2>/dev/null || true
  chmod -R u+w "$WORK" 2>/dev/null || true
  rm -rf "$WORK"
}
trap cleanup EXIT

# The network really is blocked.
if curl -fsS --max-time 5 -o /dev/null https://data1b.fullyjustified.net/ 2>/dev/null; then fail "the network is not blocked"; fi

install_to() { # dir: bin/refract + libexec/refract/<tools>, read-only
  mkdir -p "$1/bin" "$1/libexec"
  cp "$BIN" "$1/bin/refract"
  cp -R "$TOOLS" "$1/libexec/refract"
  chmod 0755 "$1/bin/refract"
}
seal() { chmod -R a-w "$1"; }
unseal() { chmod -R u+w "$1"; }

PORT=$(python3 -I -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])')
HOMEDIR=$WORK/home
DATA=$WORK/data
mkdir -p "$HOMEDIR"
chmod 700 "$WORK"

start() { # install-dir log
  # Nothing of the developer's environment: no REFRACT_*, no TECTONIC_CACHE_DIR, a fresh HOME.
  env -i PATH=/usr/bin:/bin "HOME=$HOMEDIR" "REFRACT_DATA_DIR=$DATA" \
    "$1/bin/refract" local --no-browser --port "$PORT" >"$2" 2>&1 &
  SERVER_PID=$!
  python3 -I "$ROOT/scripts/smoke-offline.py" "http://127.0.0.1:$PORT" ready \
    || { cat "$2" >&2; fail "refract local did not start"; }
}
stop() {
  kill "$SERVER_PID" 2>/dev/null || true
  wait "$SERVER_PID" 2>/dev/null || true
  SERVER_PID=
}
digest() { # the person's data: projects, database and key (path, mtime and SHA-256 of every file)
  python3 -I - "$DATA" <<'PY'
import hashlib, os, sys
root = sys.argv[1]
paths = []
for top in ("projects", "refract.db", "secret.key"):
    full = os.path.join(root, top)
    if os.path.isdir(full):
        for d, _, names in os.walk(full):
            paths += [os.path.join(d, n) for n in names]
    elif os.path.exists(full):
        paths.append(full)
for p in sorted(paths):
    with open(p, "rb") as f:
        print(os.path.relpath(p, root), os.stat(p).st_mtime_ns, hashlib.sha256(f.read()).hexdigest())
PY
}

# 1. First run on an empty profile, from a read-only install.
install_to "$WORK/install1"
seal "$WORK/install1"
start "$WORK/install1" "$WORK/server1.log"
grep -q 'TeX seed ready' "$WORK/server1.log" || { cat "$WORK/server1.log" >&2; fail "the TeX seed was not applied at startup"; }
SEED_ID=$(python3 -I -c 'import json,sys; print(json.load(open(sys.argv[1]))["id"])' "$TOOLS/tex-seed/seed.json")
python3 -I "$ROOT/scripts/smoke-offline.py" "http://127.0.0.1:$PORT" compile || { cat "$WORK/server1.log" >&2; fail "compile checks failed"; }
grep -q "$SEED_ID" "$DATA/tectonic-cache/refract-seed.json" || fail "the cache marker does not name the seed"
# Tectonic used the cache under the data folder, not one in the profile.
if [ -e "$HOMEDIR/Library/Caches/Tectonic" ] || [ -e "$HOMEDIR/.cache/Tectonic" ]; then fail "Tectonic made a cache in the home folder"; fi
stop

# 2. An update: the install is replaced by one with another seed (a changed id and one more
# file); the person's data and the packages they downloaded must survive.
DIGEST=$(python3 -I -c 'import json,sys; print(json.load(open(sys.argv[1]))["bundle"]["digest"])' "$TOOLS/tex-seed/seed.json")
mkdir -p "$DATA/tectonic-cache/bundles/data/$DIGEST"
printf 'downloaded by the person\n' >"$DATA/tectonic-cache/bundles/data/$DIGEST/smoke-user-download.sty"
BEFORE=$(digest)
[ -n "$BEFORE" ] || fail "no data to compare"
install_to "$WORK/install2"
python3 -I - "$WORK/install2/libexec/refract/tex-seed" "$DIGEST" <<'PY'
import hashlib, json, os, sys
seed, digest = sys.argv[1:3]
data = b"% added by the update\n"
rel = f"cache/bundles/data/{digest}/smoke-new-in-update.sty"
os.chmod(seed, 0o755)
with open(os.path.join(seed, rel), "wb") as f:
    f.write(data)
m = json.load(open(os.path.join(seed, "seed.json")))
m["files"][rel] = hashlib.sha256(data).hexdigest()
listing = "".join(f"{v}  {k}\n" for k, v in sorted(m["files"].items()))
m["id"] = hashlib.sha256((listing + "update").encode()).hexdigest()
json.dump(m, open(os.path.join(seed, "seed.json"), "w"), indent=2, sort_keys=True)
PY
NEW_ID=$(python3 -I -c 'import json,sys; print(json.load(open(sys.argv[1]))["id"])' "$WORK/install2/libexec/refract/tex-seed/seed.json")
[ "$NEW_ID" != "$SEED_ID" ] || fail "the update seed has the same id"
seal "$WORK/install2"
start "$WORK/install2" "$WORK/server2.log"
stop
[ "$(digest)" = "$BEFORE" ] || { echo "$BEFORE" >&2; digest >&2; fail "the update changed the projects, the database or the key"; }
grep -q "$NEW_ID" "$DATA/tectonic-cache/refract-seed.json" || fail "the marker still names the old seed"
[ -f "$DATA/tectonic-cache/bundles/data/$DIGEST/smoke-new-in-update.sty" ] || fail "the update's new file was not copied"
grep -q 'downloaded by the person' "$DATA/tectonic-cache/bundles/data/$DIGEST/smoke-user-download.sty" || fail "a downloaded package was lost"
grep -Eq '"copied":1,|copied=1 ' "$WORK/server2.log" || { cat "$WORK/server2.log" >&2; fail "the update should copy exactly the new file"; }

# 3. The same projects still compile after the update.
start "$WORK/install2" "$WORK/server3.log"
python3 -I "$ROOT/scripts/smoke-offline.py" "http://127.0.0.1:$PORT" compile || { cat "$WORK/server3.log" >&2; fail "compile checks failed after the update"; }
stop
echo "smoke-offline: ok ($OS)"
