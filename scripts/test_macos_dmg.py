"""The app icon, the DMG background and the DMG settings: the committed files have the sizes and
contents the scripts promise (the scripts that make them need macOS and Xcode, and run by hand)."""
import os
import plistlib
import re
import struct
import unittest

ROOT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..")
RES = os.path.join(ROOT, "macos", "Resources")
DMG = os.path.join(ROOT, "macos", "dmg")
PNG_SIGNATURE = b"\x89PNG\r\n\x1a\n"


def png_size(data):
    assert data[:8] == PNG_SIGNATURE, "not a PNG"
    return struct.unpack(">II", data[16:24])


def icns_chunks(path):
    with open(path, "rb") as f:
        data = f.read()
    magic, total = struct.unpack(">4sI", data[:8])
    assert magic == b"icns" and total == len(data), "not a well formed .icns"
    chunks, pos = {}, 8
    while pos < len(data):
        kind, length = struct.unpack(">4sI", data[pos:pos + 8])
        chunks[kind.decode()] = data[pos + 8:pos + length]
        pos += length
    return chunks


class AppIcon(unittest.TestCase):
    def test_sizes(self):
        chunks = icns_chunks(os.path.join(RES, "AppIcon.icns"))
        # iconutil's types: 16 and 32 px as ARGB data, the rest as PNG, 2x ones included.
        want = {"ic11": 32, "ic12": 64, "ic07": 128, "ic13": 256, "ic08": 256, "ic14": 512, "ic09": 512, "ic10": 1024}
        self.assertEqual(set(chunks) - {"info"}, set(want) | {"ic04", "ic05"})
        for kind in ("ic04", "ic05"):
            self.assertEqual(chunks[kind][:4], b"ARGB", kind)
        for kind, px in want.items():
            self.assertEqual(png_size(chunks[kind]), (px, px), kind)

    def test_bundle_uses_it(self):
        with open(os.path.join(RES, "Info.plist"), "rb") as f:
            info = plistlib.load(f)
        self.assertEqual(info["CFBundleIconFile"], "AppIcon")
        with open(os.path.join(ROOT, "macos", "build-app.sh"), encoding="utf-8") as f:
            self.assertIn("AppIcon.icns", f.read())


class IconVariants(unittest.TestCase):
    def test_every_variant_sets_what_the_renderer_reads(self):
        import importlib.util
        spec = importlib.util.spec_from_file_location("icon", os.path.join(ROOT, "scripts", "make-app-icon.py"))
        icon = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(icon)
        with open(os.path.join(ROOT, "scripts", "render-icon.swift"), encoding="utf-8") as f:
            used = set(re.findall(r'(?:num|color)\("(\w+)"\)', f.read()))
        self.assertEqual(set(icon.VARIANTS), {"paper", "night"})
        for name, overrides in icon.VARIANTS.items():
            self.assertLessEqual(used, set(dict(icon.PARAMS, **overrides)), name)
        self.assertEqual(icon.PARAMS["variant"], "paper")


class DmgBackground(unittest.TestCase):
    def test_sizes(self):
        for suffix, scale in (("", 1), ("@2x", 2)):
            with open(os.path.join(DMG, "background%s.png" % suffix), "rb") as f:
                self.assertEqual(png_size(f.read()), (660 * scale, 440 * scale), suffix)

    def test_svg_has_a_value_for_every_placeholder(self):
        import importlib.util
        spec = importlib.util.spec_from_file_location("bg", os.path.join(ROOT, "scripts", "make-dmg-background.py"))
        bg = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(bg)
        with open(bg.SVG, encoding="utf-8") as f:
            names = set(re.findall(r"\{\{(\w+)\}\}", f.read()))
        self.assertEqual(names, set(bg.PARAMS))


class DmgSettings(unittest.TestCase):
    def settings(self):
        path = os.path.join(DMG, "settings.py")
        env = {"defines": {"app": "/somewhere/Refract.app", "settings_path": path}}
        with open(path, encoding="utf-8") as f:
            exec(compile(f.read(), path, "exec"), env)
        return env

    def test_layout(self):
        s = self.settings()
        self.assertEqual(s["window_rect"][1], (660, 452))
        self.assertEqual(s["icon_size"], 128)
        self.assertEqual(s["icon_locations"], {"Refract.app": (180, 200), "Applications": (480, 200)})
        self.assertEqual(s["symlinks"], {"Applications": "/Applications"})
        self.assertEqual(s["format"], "UDZO")
        for name in ("toolbar", "sidebar", "pathbar", "status_bar"):
            self.assertIs(s["show_" + name], False, name)
        self.assertTrue(os.path.isfile(s["background"]))
        self.assertTrue(os.path.isfile(s["icon"]))

    def test_requirements_are_pinned_with_hashes(self):
        with open(os.path.join(ROOT, "scripts", "requirements-dmg.txt"), encoding="utf-8") as f:
            text = f.read()
        pins = re.findall(r"^([\w.-]+)==[\d.]+ \\\n\s+--hash=sha256:[0-9a-f]{64}$", text, re.M)
        self.assertEqual(sorted(pins), ["dmgbuild", "ds-store", "mac-alias"])


if __name__ == "__main__":
    unittest.main()
