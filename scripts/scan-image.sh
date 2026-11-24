#!/bin/sh
# Scans a Refract image and decides whether it may be published: the SBOM
# (Syft), the vulnerabilities in it (Grype), the native tools the scanner may
# not identify, and the gate (scripts/scan_gate.py: Critical findings block
# unless security/image-scan-exceptions.json has a reviewed exception).
#
# Usage: scripts/scan-image.sh <image> <out folder>
#   <image> is what `docker run` takes, ideally by digest
#   (ghcr.io/use-refract/refract@sha256:…), or a local tag in CI.
# Needs docker, python3, and syft and grype on the PATH (scripts/install-scanners.sh).
# Writes report.md, sbom.spdx.json, sbom.syft.json, grype.json and grype.txt.
# Exit status: 0 publishable, 1 blocked, 2 the scan itself failed.
set -eu
IMAGE=$1
OUT=$2
ROOT=$(cd "$(dirname "$0")/.." && pwd)
mkdir -p "$OUT"

echo "== SBOM of $IMAGE"
syft scan "docker:$IMAGE" -q -o "syft-json=$OUT/sbom.syft.json" -o "spdx-json=$OUT/sbom.spdx.json" || exit 2

echo "== vulnerabilities"
grype db update -q || exit 2
grype "sbom:$OUT/sbom.syft.json" -q -o "json=$OUT/grype.json" -o "table=$OUT/grype.txt" || exit 2

echo "== versions the native tools report"
tool() { docker run --rm --entrypoint "$1" "$IMAGE" "$2" 2>&1 | head -1; }

set +e
python3 "$ROOT/scripts/scan_gate.py" \
	--grype "$OUT/grype.json" --syft "$OUT/sbom.syft.json" \
	--dockerfile "$ROOT/Dockerfile" --exceptions "$ROOT/security/image-scan-exceptions.json" \
	--image "$IMAGE" --report "$OUT/report.md" \
	--tool "tectonic=$(tool tectonic --version)" \
	--tool "synctex=$(tool synctex help | sed 's/.*utility, //')"
status=$?
set -e
[ -n "${GITHUB_STEP_SUMMARY:-}" ] && cat "$OUT/report.md" >> "$GITHUB_STEP_SUMMARY"
exit $status
