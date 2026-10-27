#!/bin/sh
# M0 acceptance: tools present, hello world compiles with the cache on /data
# and SyncTeX works on Tectonic's output, server answers.
# Usage: scripts/smoke-docker.sh <image> [platform]
set -eu
IMAGE=$1
PLATFORM=${2:+--platform $2}
VOL=refract-smoke-$$
PORT=18089
cleanup() {
	docker rm -f "$VOL" >/dev/null 2>&1 || true
	docker volume rm "$VOL" >/dev/null 2>&1 || true
}
trap cleanup EXIT
run() { docker run --rm $PLATFORM -v "$VOL:/data" --entrypoint sh "$IMAGE" -c "$1"; }

echo "== versions"
run 'tectonic --version && synctex help 2>&1 | head -1 && git --version && rg --version | head -1 && whoami'

echo "== compile hello world"
run '
set -e
mkdir -p /data/smoke/out && cd /data/smoke
printf "%s\n" "\\documentclass{article}" "\\begin{document}" "Hello, Refract." "\\end{document}" > hello.tex
tectonic hello.tex --outdir out --synctex --keep-logs --untrusted --bundle "$REFRACT_TECTONIC_BUNDLE"
test -s out/hello.pdf && test -s out/hello.synctex.gz && test -s out/hello.log
test -d "$TECTONIC_CACHE_DIR/bundles"
echo "pdf, synctex.gz, log and cache on /data: ok"
'

echo "== synctex forward and inverse"
run '
set -e
cd /data/smoke
# Round trip: line 3 -> PDF position -> back to hello.tex line 3. SyncTeX
# records absolute paths, so it is asked with one, as the server does.
fwd=$(synctex view -i "3:1:$PWD/hello.tex" -o out/hello.pdf)
echo "$fwd" | grep -q "^Page:1"
x=$(echo "$fwd" | sed -n "s/^x://p" | head -1)
y=$(echo "$fwd" | sed -n "s/^y://p" | head -1)
inv=$(synctex edit -o "1:$x:$y:out/hello.pdf")
echo "$inv" | grep -q "^Input:.*hello.tex"
echo "$inv" | grep -q "^Line:3$"
echo "synctex view/edit round trip on Tectonic output: ok (page 1, $x,$y -> line 3)"
'

echo "== server"
docker run -d --name "$VOL" $PLATFORM -p "127.0.0.1:$PORT:8080" -v "$VOL:/data" \
	-e REFRACT_AUTH=none -e REFRACT_SECRET_KEY="$(openssl rand -base64 32)" "$IMAGE" >/dev/null
curl -fsS --retry 15 --retry-connrefused --retry-delay 1 "http://127.0.0.1:$PORT/healthz" && echo
curl -fsS "http://127.0.0.1:$PORT/" | grep -q '<div id="root">' && echo "UI served: ok"
echo "== all ok"
