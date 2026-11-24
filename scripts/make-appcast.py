#!/usr/bin/env python3
"""Writes the Sparkle appcast of one macOS app archive: the feed a Refract.app of that
architecture reads to learn that a newer version exists (macos/README.md, "Updates").

    SPARKLE_ED_PRIVATE_KEY=... scripts/make-appcast.py --version v0.3.0 --arch arm64 \
        --archive build/mac/arm64/Refract-arm64.zip --output build/mac/arm64/appcast-arm64.xml

The archive is signed with Sparkle's `sign_update`, with the EdDSA private key from the
environment variable SPARKLE_ED_PRIVATE_KEY (CI secret; the content of the file that
`generate_keys -x` writes). The key goes to the tool's standard input: never into a file, an
argument or the output. Sparkle's tools are downloaded once from the pinned release and
checked against its SHA-256 on every run (or pass --sign-update with a binary of yours).

Before writing anything it checks, so a bad release fails here and not on people's Macs:
  - the app inside the archive has the version of the tag, an update key (SUPublicEDKey) and
    the feed URL this appcast will be served at (appcast-<arch>.xml);
  - the signature it just made verifies against that SUPublicEDKey, which catches a CI secret
    that is not the private half of the baked-in public key.

The release notes are the version's section of CHANGELOG.md (or --notes-file).
A prerelease tag (v0.3.0-rc.1) writes no appcast and exits 0: such a release is not offered.
"""
import argparse
import base64
import datetime
import email.utils
import hashlib
import html
import io
import os
import plistlib
import re
import subprocess
import sys
import tarfile
import tempfile
import urllib.request
import zipfile

SPARKLE_VERSION = "2.10.1"
SPARKLE_URL = f"https://github.com/sparkle-project/Sparkle/releases/download/{SPARKLE_VERSION}/Sparkle-{SPARKLE_VERSION}.tar.xz"
SPARKLE_SHA256 = "1a856d2a9edb8e628de88121d7be04cf0e2167f8641650b78fca00b14244cf27"
NAMESPACE = "http://www.andymatuschke.org/xmlns/sparkle"
REPO = "Use-Refract/Refract"
VERSION_RE = re.compile(r"^v?(\d+(?:\.\d+){0,2})(.*)$")


class Fail(Exception):
    pass


# --- Ed25519 verification (RFC 8032), to check the key pair before publishing ---------------
_P = 2**255 - 19
_L = 2**252 + 27742317777372353535851937790883648493
_D = -121665 * pow(121666, _P - 2, _P) % _P
_I = pow(2, (_P - 1) // 4, _P)


def _inv(x):
    return pow(x, _P - 2, _P)


def _recover_x(y, sign):
    x2 = (y * y - 1) * _inv(_D * y * y + 1) % _P
    x = pow(x2, (_P + 3) // 8, _P)
    if (x * x - x2) % _P:
        x = x * _I % _P
    if (x * x - x2) % _P:
        return None
    if x & 1 != sign:
        x = _P - x
    return x


_BY = 4 * _inv(5) % _P
_B = (_recover_x(_BY, 0), _BY, 1, _recover_x(_BY, 0) * _BY % _P)


def _add(p, q):
    a = (p[1] - p[0]) * (q[1] - q[0]) % _P
    b = (p[1] + p[0]) * (q[1] + q[0]) % _P
    c = 2 * p[3] * q[3] * _D % _P
    d = 2 * p[2] * q[2] % _P
    e, f, g, h = b - a, d - c, d + c, b + a
    return (e * f % _P, g * h % _P, f * g % _P, e * h % _P)


def _mul(s, p):
    r = (0, 1, 1, 0)
    while s:
        if s & 1:
            r = _add(r, p)
        p = _add(p, p)
        s >>= 1
    return r


def _decode(b):
    n = int.from_bytes(b, "little")
    y, sign = n & ((1 << 255) - 1), n >> 255
    x = _recover_x(y, sign)
    if x is None or y >= _P:
        return None
    return (x, y, 1, x * y % _P)


def _encode(p):
    zi = _inv(p[2])
    x, y = p[0] * zi % _P, p[1] * zi % _P
    return (y | ((x & 1) << 255)).to_bytes(32, "little")


def ed25519_verify(public_key, signature, path):
    """True if `signature` (64 bytes) is a valid Ed25519 signature of the file's bytes."""
    if len(public_key) != 32 or len(signature) != 64:
        return False
    a, r = _decode(public_key), _decode(signature[:32])
    s = int.from_bytes(signature[32:], "little")
    if a is None or r is None or s >= _L:
        return False
    h = hashlib.sha512(signature[:32] + public_key)
    with open(path, "rb") as f:
        while chunk := f.read(1 << 20):
            h.update(chunk)
    k = int.from_bytes(h.digest(), "little") % _L
    lhs, rhs = _mul(s, _B), _add(r, _mul(k, a))
    return _encode(lhs) == _encode(rhs)


# --- the app inside the archive ---------------------------------------------------------------
def read_app_info(archive):
    with zipfile.ZipFile(archive) as z:
        names = [n for n in z.namelist() if re.fullmatch(r"[^/]+\.app/Contents/Info\.plist", n)]
        if len(names) != 1:
            raise Fail(f"{archive} must hold exactly one app with an Info.plist, found {names}")
        return plistlib.loads(z.read(names[0]))


# --- release notes ----------------------------------------------------------------------------
def changelog_section(path, version):
    """The lines of '## [version]' in the changelog, up to the next '## [' (links excluded)."""
    head = f"## [{version}]"
    out, on = [], False
    with open(path, encoding="utf-8") as f:
        for line in f.read().splitlines():
            if line.startswith(head):
                on = True
                continue
            if on and line.startswith("## ["):
                break
            if on and not re.match(r"^\[[^\]]*\]: ", line):
                out.append(line)
    text = "\n".join(out).strip()
    if not text:
        raise Fail(f"CHANGELOG has no '## [{version}]' section with content (write the notes before tagging)")
    return text


def _inline(text):
    text = html.escape(text, quote=False)
    text = re.sub(r"`([^`]+)`", r"<code>\1</code>", text)
    text = re.sub(r"\*\*([^*]+)\*\*", r"<strong>\1</strong>", text)

    def link(m):
        url = m.group(2).replace('"', "&quot;").replace("'", "&#x27;")
        return '<a href="%s">%s</a>' % (url, m.group(1))

    return re.sub(r"\[([^\]]+)\]\((https?://[^)\s\"]+)\)", link, text)


def markdown_to_html(md):
    """The small part of Markdown the changelog uses: ### headings, '- ' lists, paragraphs, code,
    bold and links. Everything else is escaped text."""
    out, items = [], None

    def close():
        nonlocal items
        if items is not None:
            out.append("<ul>" + "".join(f"<li>{i}</li>" for i in items) + "</ul>")
            items = None

    for line in md.splitlines():
        if m := re.match(r"^#{2,6}\s+(.*)$", line):
            close()
            out.append(f"<h3>{_inline(m.group(1))}</h3>")
        elif m := re.match(r"^[-*]\s+(.*)$", line):
            if items is None:
                items = []
            items.append(_inline(m.group(1)))
        elif items is not None and re.match(r"^\s+\S", line):
            items[-1] += " " + _inline(line.strip())
        elif not line.strip():
            close()
        else:
            close()
            out.append(f"<p>{_inline(line.strip())}</p>")
    close()
    return "\n".join(out)


# --- Sparkle's sign_update --------------------------------------------------------------------
def fetch_sign_update(cache, workdir):
    """Sparkle's sign_update from the pinned release, extracted into `workdir` (a fresh
    directory). The archive is cached in `cache`, but its SHA-256 is checked on every run:
    the tool receives the private key, so nothing unverified is ever executed."""
    archive = os.path.join(cache, f"Sparkle-{SPARKLE_VERSION}.tar.xz")
    data = None
    if os.path.isfile(archive):
        with open(archive, "rb") as f:
            data = f.read()
        if hashlib.sha256(data).hexdigest() != SPARKLE_SHA256:
            data = None
    if data is None:
        print(f"downloading Sparkle {SPARKLE_VERSION} tools", file=sys.stderr)
        with urllib.request.urlopen(SPARKLE_URL, timeout=120) as r:
            data = r.read()
        if hashlib.sha256(data).hexdigest() != SPARKLE_SHA256:
            raise Fail("the Sparkle download does not match its pinned SHA-256: not using it")
        os.makedirs(cache, exist_ok=True)
        with tempfile.NamedTemporaryFile(dir=cache, delete=False) as f:
            f.write(data)
        os.replace(f.name, archive)
    path = os.path.join(workdir, "sign_update")
    with tarfile.open(fileobj=io.BytesIO(data), mode="r:xz") as t:
        member = t.getmember("./bin/sign_update")
        with open(path, "xb") as f:
            f.write(t.extractfile(member).read())
    os.chmod(path, 0o700)
    return path


def sign(sign_update, archive, private_key):
    """(signature base64, length) of the archive. The key goes on standard input only."""
    env = {k: v for k, v in os.environ.items() if k != "SPARKLE_ED_PRIVATE_KEY"}
    r = subprocess.run([sign_update, "--ed-key-file", "-", archive], input=private_key + "\n",
                       capture_output=True, text=True, env=env)
    if r.returncode != 0:
        # The tool's message, never the key (it is not in anything the tool prints).
        raise Fail("sign_update failed: " + (r.stderr.strip() or r.stdout.strip() or f"status {r.returncode}"))
    m = re.search(r'sparkle:edSignature="([^"]+)"\s+length="(\d+)"', r.stdout)
    if not m:
        raise Fail("sign_update printed no signature")
    return m.group(1), int(m.group(2))


# --- the appcast ------------------------------------------------------------------------------
def build_appcast(*, title, tag, short_version, build_version, minimum_system, notes_html, url,
                  signature, length, pub_date):
    def esc(s):
        return html.escape(s, quote=True)

    return f"""<?xml version="1.0" encoding="utf-8"?>
<rss version="2.0" xmlns:sparkle="{NAMESPACE}">
  <channel>
    <title>{esc(title)}</title>
    <link>https://github.com/{REPO}</link>
    <description>Updates for {esc(title)}</description>
    <language>en</language>
    <item>
      <title>Version {esc(short_version)}</title>
      <pubDate>{email.utils.format_datetime(pub_date)}</pubDate>
      <sparkle:version>{esc(build_version)}</sparkle:version>
      <sparkle:shortVersionString>{esc(short_version)}</sparkle:shortVersionString>
      <sparkle:minimumSystemVersion>{esc(minimum_system)}</sparkle:minimumSystemVersion>
      <sparkle:releaseNotesLink>https://github.com/{REPO}/releases/tag/{esc(tag)}</sparkle:releaseNotesLink>
      <description><![CDATA[{notes_html}]]></description>
      <enclosure url="{esc(url)}" length="{length}" type="application/octet-stream" sparkle:edSignature="{esc(signature)}"/>
    </item>
  </channel>
</rss>
"""


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--version", required=True, help="the tag, e.g. v0.3.0")
    ap.add_argument("--arch", required=True, choices=["arm64", "amd64"])
    ap.add_argument("--archive", required=True, help="the zip of Refract.app (what will be attached to the release)")
    ap.add_argument("--output", required=True, help="where to write the appcast (appcast-<arch>.xml)")
    ap.add_argument("--changelog", default="CHANGELOG.md")
    ap.add_argument("--notes-file", help="Markdown release notes, instead of the CHANGELOG section")
    ap.add_argument("--download-prefix", help=f"URL the archive will be served from (default: the tag's release on github.com/{REPO})")
    ap.add_argument("--sign-update", default=os.environ.get("SPARKLE_SIGN_UPDATE"), help="Sparkle's sign_update (default: downloaded, pinned)")
    ap.add_argument("--tools-dir", default=os.path.join(tempfile.gettempdir(), "refract-sparkle-tools"),
                    help="where the Sparkle download is cached (checked against its SHA-256 on every run)")
    ap.add_argument("--date", help="publication date, ISO 8601 (default: now)")
    args = ap.parse_args(argv)

    m = VERSION_RE.match(args.version)
    if not m:
        raise Fail(f"version must look like v0.3.0, got {args.version!r}")
    numeric, suffix = m.groups()
    if suffix and not suffix.startswith("-"):
        raise Fail(f"version must look like v0.3.0 or v0.3.0-rc.1, got {args.version!r}")
    if suffix:
        print(f"{args.version} is a prerelease: not offered through the update feed, no appcast written")
        return 0
    private_key = os.environ.get("SPARKLE_ED_PRIVATE_KEY", "").strip()
    if not private_key:
        raise Fail("SPARKLE_ED_PRIVATE_KEY is not set (the CI secret: macos/README.md, \"Updates\")")

    info = read_app_info(args.archive)
    build_version = str(info.get("CFBundleVersion", ""))
    if build_version != numeric:
        raise Fail(f"the app in the archive is version {build_version}, the tag is {args.version}")
    public = str(info.get("SUPublicEDKey", "")).strip()
    try:
        public_bytes = base64.b64decode(public, validate=True)
    except ValueError:
        public_bytes = b""
    if len(public_bytes) != 32:
        raise Fail("the app has no valid SUPublicEDKey: it was built without REFRACT_SPARKLE_PUBLIC_KEY and could never verify this update")
    feed = str(info.get("SUFeedURL", ""))
    expected = f"appcast-{args.arch}.xml"
    if not feed.startswith("https://") or feed.rsplit("/", 1)[-1] != expected:
        raise Fail(f"the app's SUFeedURL is {feed!r}: this appcast is {expected}, served over https")
    minimum = str(info.get("LSMinimumSystemVersion", "13.0"))

    if args.notes_file:
        with open(args.notes_file, encoding="utf-8") as f:
            notes_md = f.read()
    else:
        notes_md = changelog_section(args.changelog, numeric)
    with tempfile.TemporaryDirectory() as work:
        sign_update = args.sign_update or fetch_sign_update(args.tools_dir, work)
        signature, length = sign(sign_update, args.archive, private_key)
    if length != os.path.getsize(args.archive):
        raise Fail("sign_update measured another length than the file's")
    if not ed25519_verify(public_bytes, base64.b64decode(signature), args.archive):
        raise Fail("the signature does not verify with the app's SUPublicEDKey: SPARKLE_ED_PRIVATE_KEY is not the private half of REFRACT_SPARKLE_PUBLIC_KEY")

    prefix = (args.download_prefix or f"https://github.com/{REPO}/releases/download/{args.version}").rstrip("/")
    when = datetime.datetime.fromisoformat(args.date) if args.date else datetime.datetime.now(datetime.timezone.utc)
    if when.tzinfo is None:
        when = when.replace(tzinfo=datetime.timezone.utc)
    xml = build_appcast(
        title=str(info.get("CFBundleName", "Refract")), tag=args.version, short_version=str(info.get("CFBundleShortVersionString", numeric)),
        build_version=build_version, minimum_system=minimum, notes_html=markdown_to_html(notes_md),
        url=f"{prefix}/{os.path.basename(args.archive)}", signature=signature, length=length, pub_date=when)
    with open(args.output, "w", encoding="utf-8") as f:
        f.write(xml)
    print(f"wrote {args.output}: {args.version} for {args.arch}, {length} bytes, signature verified against the app's key")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Fail as e:
        print(f"error: {e}", file=sys.stderr)
        sys.exit(1)
