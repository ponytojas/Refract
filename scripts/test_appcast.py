"""Tests of scripts/make-appcast.py: the notes, the feed, and the checks that stop a bad release."""
import base64
import contextlib
import hashlib
import importlib.util
import io
import os
import pathlib
import plistlib
import stat
import sys
import tarfile
import tempfile
import unittest
import xml.etree.ElementTree as ET
import zipfile

HERE = os.path.dirname(os.path.abspath(__file__))
spec = importlib.util.spec_from_file_location("make_appcast", os.path.join(HERE, "make-appcast.py"))
mk = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mk)


def ed25519_sign(seed, message):
    """RFC 8032 signing, for the fake sign_update (the real one is Sparkle's)."""
    h = hashlib.sha512(seed).digest()
    a = (int.from_bytes(h[:32], "little") & ((1 << 254) - 8)) | (1 << 254)
    pub = mk._encode(mk._mul(a, mk._B))
    r = int.from_bytes(hashlib.sha512(h[32:] + message).digest(), "little") % mk._L
    rs = mk._encode(mk._mul(r, mk._B))
    k = int.from_bytes(hashlib.sha512(rs + pub + message).digest(), "little") % mk._L
    return pub, rs + ((r + k * a) % mk._L).to_bytes(32, "little")


FAKE_SIGN_UPDATE = """#!/usr/bin/env python3
import base64, importlib.util, os, sys
sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location("t", {test!r}); t = importlib.util.module_from_spec(spec); spec.loader.exec_module(t)
assert sys.argv[1:3] == ["--ed-key-file", "-"], sys.argv
seed = base64.b64decode(sys.stdin.read().strip())
data = open(sys.argv[3], "rb").read()
_, sig = t.ed25519_sign(seed, data)
print('sparkle:edSignature="%s" length="%d"' % (base64.b64encode(sig).decode(), len(data)))
"""


class Ed25519(unittest.TestCase):
    def test_rfc8032_vectors(self):
        # RFC 8032, section 7.1, test 1 (empty message) and test 2 (one byte, 0x72).
        cases = [
            ("9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60", b"",
             "d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a",
             "e5564300c360ac729086e2cc806e828a84877f1eb8e5d974d873e065224901555fb8821590a33bacc61e39701cf9b46bd25bf5f0595bbe24655141438e7a100b"),
            ("4ccd089b28ff96da9db6c346ec114e0f5b8a319f35aba624da8cf6ed4fb8a6fb", b"\x72",
             "3d4017c3e843895a92b70aa74d1b7ebc9c982ccf2ec4968cc0cd55f12af4660c",
             "92a009a9f0d4cab8720e820b5f642540a2b27b5416503f8fb3762223ebdb69da085ac1e43e15996e458f3613d0f11d8c387b2eaeb4302aeeb00d291612bb0c00"),
        ]
        with tempfile.TemporaryDirectory() as d:
            for seed, msg, pub, sig in cases:
                f = os.path.join(d, "m")
                pathlib.Path(f).write_bytes(msg)
                self.assertTrue(mk.ed25519_verify(bytes.fromhex(pub), bytes.fromhex(sig), f))
                self.assertEqual(ed25519_sign(bytes.fromhex(seed), msg), (bytes.fromhex(pub), bytes.fromhex(sig)))
                pathlib.Path(f).write_bytes(msg + b"x")
                self.assertFalse(mk.ed25519_verify(bytes.fromhex(pub), bytes.fromhex(sig), f))


class Notes(unittest.TestCase):
    CHANGELOG = """# Changelog

## [Unreleased]

- Not yet.

## [0.3.0] - 2026-12-01

### Added

- The app updates itself, see `Check for Updates…` and [the guide](https://example.com/x).
  It waits for **saves**.
- <script>alert(1)</script>

### Fixed

- A thing.

## [0.2.0] - 2026-11-20

- Old.

[Unreleased]: https://example.com/u
"""

    def section(self, version):
        with tempfile.NamedTemporaryFile("w", suffix=".md", delete=False, encoding="utf-8") as f:
            f.write(self.CHANGELOG)
        try:
            return mk.changelog_section(f.name, version)
        finally:
            os.unlink(f.name)

    def test_section_is_the_versions_own(self):
        s = self.section("0.3.0")
        self.assertIn("The app updates itself", s)
        self.assertNotIn("Old.", s)
        self.assertNotIn("Not yet", s)

    def test_missing_section_fails(self):
        with self.assertRaises(mk.Fail):
            self.section("0.9.0")

    def test_link_url_cannot_inject_attributes(self):
        out = mk.markdown_to_html('[x](https://e.com/a"onmouseover="alert(1)) [y](https://e.com/a&b\'c)')
        self.assertNotIn('<a href="https://e.com/a"', out)
        self.assertEqual(out.count("<a "), 1)
        self.assertIn('href="https://e.com/a&amp;b&#x27;c"', out)

    def test_html_is_escaped_and_structured(self):
        out = mk.markdown_to_html(self.section("0.3.0"))
        self.assertIn("<h3>Added</h3>", out)
        self.assertIn("<code>Check for Updates…</code>", out)
        self.assertIn('<a href="https://example.com/x">the guide</a>', out)
        self.assertIn("<strong>saves</strong>", out)
        self.assertNotIn("<script>", out)
        self.assertIn("&lt;script&gt;", out)
        self.assertEqual(out.count("<ul>"), 2)


class Appcast(unittest.TestCase):
    SEED = bytes(range(32))

    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.dir.cleanup)
        d = self.dir.name
        self.pub = ed25519_sign(self.SEED, b"")[0]
        self.fake = os.path.join(d, "sign_update")
        with open(self.fake, "w") as f:
            f.write(FAKE_SIGN_UPDATE.format(test=os.path.abspath(__file__)))
        os.chmod(self.fake, os.stat(self.fake).st_mode | stat.S_IXUSR)
        self.notes = os.path.join(d, "notes.md")
        pathlib.Path(self.notes).write_text("- Something new.\n")
        self.env = dict(os.environ)
        os.environ["SPARKLE_ED_PRIVATE_KEY"] = base64.b64encode(self.SEED).decode()
        self.addCleanup(lambda: os.environ.pop("SPARKLE_ED_PRIVATE_KEY", None))

    def archive(self, version="0.3.0", key=None, feed="https://example.com/appcast-arm64.xml", name="Refract-arm64.zip"):
        info = {"CFBundleName": "Refract", "CFBundleVersion": version, "CFBundleShortVersionString": version,
                "LSMinimumSystemVersion": "13.0", "SUFeedURL": feed,
                "SUPublicEDKey": key if key is not None else base64.b64encode(self.pub).decode()}
        path = os.path.join(self.dir.name, name)
        with zipfile.ZipFile(path, "w") as z:
            z.writestr("Refract.app/Contents/Info.plist", plistlib.dumps(info))
            z.writestr("Refract.app/Contents/MacOS/Refract", b"binary")
        return path

    def run_main(self, archive, version="v0.3.0", arch="arm64", extra=()):
        out = os.path.join(self.dir.name, "appcast.xml")
        err = io.StringIO()
        with contextlib.redirect_stdout(io.StringIO()) as so, contextlib.redirect_stderr(err):
            try:
                rc = mk.main(["--version", version, "--arch", arch, "--archive", archive, "--output", out,
                              "--notes-file", self.notes, "--sign-update", self.fake, *extra])
            except mk.Fail as e:
                rc = str(e)
        return rc, out, so.getvalue()

    def test_writes_a_verifiable_feed(self):
        rc, out, _ = self.run_main(self.archive())
        self.assertEqual(rc, 0)
        item = ET.parse(out).getroot().find("channel/item")
        ns = {"s": mk.NAMESPACE}
        self.assertEqual(item.find("s:version", ns).text, "0.3.0")
        self.assertEqual(item.find("s:minimumSystemVersion", ns).text, "13.0")
        enc = item.find("enclosure")
        self.assertEqual(enc.get("url"), "https://github.com/ponytojas/Refract/releases/download/v0.3.0/Refract-arm64.zip")
        self.assertEqual(int(enc.get("length")), os.path.getsize(os.path.join(self.dir.name, "Refract-arm64.zip")))
        self.assertTrue(mk.ed25519_verify(self.pub, base64.b64decode(enc.get("{%s}edSignature" % mk.NAMESPACE)),
                                          os.path.join(self.dir.name, "Refract-arm64.zip")))
        self.assertIn("Something new", item.find("description").text)

    def test_download_prefix(self):
        rc, out, _ = self.run_main(self.archive(), extra=["--download-prefix", "http://127.0.0.1:8099/"])
        self.assertEqual(rc, 0)
        self.assertEqual(ET.parse(out).getroot().find("channel/item/enclosure").get("url"), "http://127.0.0.1:8099/Refract-arm64.zip")

    def test_secret_that_is_not_the_apps_key_fails(self):
        other = ed25519_sign(bytes(32), b"")[0]
        rc, out, _ = self.run_main(self.archive(key=base64.b64encode(other).decode()))
        self.assertIn("not the private half", rc)
        self.assertFalse(os.path.exists(out))

    def test_app_without_a_key_fails(self):
        rc, _, _ = self.run_main(self.archive(key=""))
        self.assertIn("SUPublicEDKey", rc)

    def test_version_and_feed_must_match_the_tag_and_arch(self):
        self.assertIn("is version 0.3.0", self.run_main(self.archive(), version="v0.4.0")[0])
        self.assertIn("appcast-amd64.xml", self.run_main(self.archive(), arch="amd64")[0])
        self.assertIn("https", self.run_main(self.archive(feed="http://example.com/appcast-arm64.xml"))[0])

    def test_prerelease_is_not_offered(self):
        rc, out, so = self.run_main(self.archive(), version="v0.3.0-rc.1")
        self.assertEqual(rc, 0)
        self.assertFalse(os.path.exists(out))
        self.assertIn("prerelease", so)

    def test_build_metadata_is_not_a_prerelease(self):
        rc, _, _ = self.run_main(self.archive(), version="v0.3.0+1")
        self.assertIn("version must look like", rc)

    def test_missing_secret_fails_without_naming_a_value(self):
        del os.environ["SPARKLE_ED_PRIVATE_KEY"]
        rc, _, _ = self.run_main(self.archive())
        self.assertIn("SPARKLE_ED_PRIVATE_KEY is not set", rc)


class FetchSignUpdate(unittest.TestCase):
    """The downloaded tool gets the private key: it runs only from an archive whose SHA-256
    matches the pin, on every run, including from the cache."""

    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.dir.cleanup)
        buf = io.BytesIO()
        with tarfile.open(fileobj=buf, mode="w:xz") as t:
            body = b"#!/bin/sh\necho genuine\n"
            info = tarfile.TarInfo("./bin/sign_update")
            info.size = len(body)
            t.addfile(info, io.BytesIO(body))
        self.good = buf.getvalue()
        self.downloads = []
        self.served = self.good
        test = self

        class Response(io.BytesIO):
            def __enter__(self):
                return self

            def __exit__(self, *a):
                return False

        def urlopen(url, timeout=None):
            test.downloads.append(url)
            return Response(test.served)

        for name, value in [("SPARKLE_SHA256", hashlib.sha256(self.good).hexdigest())]:
            old = getattr(mk, name)
            setattr(mk, name, value)
            self.addCleanup(setattr, mk, name, old)
        old_urlopen = mk.urllib.request.urlopen
        mk.urllib.request.urlopen = urlopen
        self.addCleanup(setattr, mk.urllib.request, "urlopen", old_urlopen)
        self.cache = os.path.join(self.dir.name, "cache")

    def fetch(self):
        work = tempfile.mkdtemp(dir=self.dir.name)
        with contextlib.redirect_stderr(io.StringIO()):
            path = mk.fetch_sign_update(self.cache, work)
        self.assertTrue(path.startswith(work))
        return pathlib.Path(path).read_bytes()

    def test_downloads_once_and_rechecks_the_cache(self):
        self.assertIn(b"genuine", self.fetch())
        self.assertIn(b"genuine", self.fetch())
        self.assertEqual(len(self.downloads), 1)

    def test_tampered_cache_is_downloaded_again(self):
        self.fetch()
        cached = os.path.join(self.cache, f"Sparkle-{mk.SPARKLE_VERSION}.tar.xz")
        pathlib.Path(cached).write_bytes(b"planted")
        self.assertIn(b"genuine", self.fetch())
        self.assertEqual(len(self.downloads), 2)

    def test_download_that_does_not_match_is_refused(self):
        self.served = self.good + b"x"
        with self.assertRaises(mk.Fail):
            self.fetch()


if __name__ == "__main__":
    unittest.main()
