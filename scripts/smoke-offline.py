"""Drives a running `refract local` (REFRACT_AUTH none, loopback) for scripts/smoke-offline.sh.

Usage: smoke-offline.py <base-url> <phase>
  compile   every template in en and es: compile, check the PDF and a SyncTeX round trip,
            nothing downloaded; then a document needing a package the seed lacks must
            fail fast with a download problem and keep its source
  ready     only wait for /healthz (exit 1 when it never answers)
"""
import json
import sys
import time
import urllib.error
import urllib.request

BASE = sys.argv[1]
PHASE = sys.argv[2]
FAILS = []


def call(method, path, body=None, raw=False, head=None):
    data = None
    headers = {"X-Refract": "1"}
    if body is not None:
        data = body if isinstance(body, bytes) else json.dumps(body).encode()
        headers["Content-Type"] = "application/json" if not isinstance(body, bytes) else "text/plain"
    req = urllib.request.Request(BASE + path, data=data, method=method, headers=headers)
    with urllib.request.urlopen(req, timeout=300) as resp:
        out = resp.read(head) if head else resp.read()
    return out if raw else (json.loads(out) if out else None)


def fail(msg):
    print("smoke-offline: FAIL " + msg, file=sys.stderr)
    FAILS.append(msg)


def sync_round_trip(pid, main):
    files = [f["path"] for f in call("GET", f"/api/projects/{pid}/files") if not f["dir"] and f["path"].endswith(".tex")]
    files.sort(key=lambda f: f != main)
    for f in files:
        text = call("GET", "/api/projects/%s/file?path=%s" % (pid, urllib.request.quote(f)), raw=True).decode()
        for i, line in enumerate(text.split("\n"), 1):
            s = line.strip()
            if len(s) < 30 or s.startswith(("\\", "%")):
                continue
            try:
                boxes = call("GET", "/api/projects/%s/synctex/forward?file=%s&line=%d&col=0" % (pid, urllib.request.quote(f), i))["boxes"]
            except urllib.error.HTTPError:
                continue
            if not boxes or boxes[0]["w"] <= 0:
                continue
            b = boxes[0]
            back = call("GET", "/api/projects/%s/synctex/inverse?page=%d&x=%f&y=%f" % (pid, b["page"], b["x"] + b["w"] / 2, b["y"] + b["h"] / 2))
            return back["file"] == f and back["line"] >= 1, f"{f}:{i} -> page {b['page']} -> {back}"
    return False, "no source line maps to the PDF"


def main():
    if PHASE == "ready":
        for _ in range(300):
            try:
                call("GET", "/healthz", raw=True)
                return 0
            except Exception:
                time.sleep(0.1)
        return 1
    templates = call("GET", "/api/templates")
    builtin = [t["id"] for t in templates if t.get("builtin")]
    if len(builtin) < 5:
        fail(f"only {len(builtin)} built-in templates: {builtin}")
    n = 0
    for lang in ("en", "es"):
        for tid in builtin:
            label = f"{tid}/{lang}"
            p = call("POST", "/api/projects", {"name": f"smoke {label}", "template": tid, "language": lang})
            t0 = time.time()
            res = call("POST", f"/api/projects/{p['id']}/compile")
            took = time.time() - t0
            dl = res["downloads"]
            if not res["ok"] or not res.get("pdf") or dl["fetched"] or dl["offline"] or dl["missing"] or res.get("timed_out"):
                fail(f"{label}: compile ok={res['ok']} pdf={res.get('pdf')} downloads={dl} problems={res['problems']}")
                continue
            pdf = call("GET", f"/api/projects/{p['id']}/pdf", raw=True, head=4)
            if pdf[:4] != b"%PDF":
                fail(f"{label}: the PDF endpoint did not return a PDF")
                continue
            ok, why = sync_round_trip(p["id"], p["main_file"])
            if not ok:
                fail(f"{label}: SyncTeX {why}")
                continue
            n += 1
            print(f"smoke-offline: {label} ok ({took:.1f}s, synctex {why})")

    # A package the seed does not hold: offline, that is a quick "needs a download", not a hang.
    p = call("POST", "/api/projects", {"name": "smoke needs forest"})
    main_file = p.get("main_file") or "main.tex"
    src = b"\\documentclass{article}\n\\usepackage{forest}\n\\begin{document}\nHello offline, this sentence is kept.\n\\end{document}\n"
    call("PUT", "/api/projects/%s/file?path=%s" % (p["id"], main_file), src)
    call("PATCH", f"/api/projects/{p['id']}", {"main_file": main_file})
    t0 = time.time()
    res = call("POST", f"/api/projects/{p['id']}/compile")
    took = time.time() - t0
    kinds = [x["kind"] for x in res["problems"]]
    if not res["downloads"]["offline"] or "download" not in kinds or res["ok"]:
        fail(f"missing package: want an offline download problem, got ok={res['ok']} downloads={res['downloads']} kinds={kinds}")
    elif took > 60:
        fail(f"missing package: took {took:.0f}s to say a download is needed")
    elif call("GET", "/api/projects/%s/file?path=%s" % (p["id"], main_file), raw=True) != src:
        fail("missing package: the document changed")
    else:
        print(f"smoke-offline: missing package reported in {took:.1f}s: {res['downloads']['missing']}")
    print(f"smoke-offline: {n} templates compiled offline")
    return 1 if FAILS else 0


sys.exit(main())
