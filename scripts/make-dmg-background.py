#!/usr/bin/env python3
"""Regenerates macos/dmg/background.png and background@2x.png from macos/dmg/background.svg.

    python3 scripts/make-dmg-background.py

The SVG is the layout; the colours and the two lines of text are PARAMS below.
The light "paper" background is on purpose: Finder draws the icon names in black. macOS only (AppKit,
through scripts/rasterize.swift, like the app icon: see scripts/make-app-icon.py). The window is
660 x 420 points of content (the image is 20 taller); the @2x file is 1320 x 880 and Finder picks it on Retina displays.
Commit the PNGs.
"""
import os
import re
import subprocess
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
DMG = os.path.join(HERE, "..", "macos", "dmg")
SVG = os.path.join(DMG, "background.svg")
RASTERIZE = os.path.join(HERE, "rasterize.swift")
SIZE = (660, 440)  # points: 20 taller than the 660 x 420 content area, in case the title bar is shorter

# The look. English only, on purpose: the DMG is one file for everyone.
PARAMS = {
    "paper_top": "#f6f8fa",     # the landing page's paper ...
    "paper_bottom": "#edf1f5",  # ... to its mist
    "ink": "#141d23",
    "muted": "#7b8893",         # the struck-out line, like "Less friction," on the landing page
    "cyan": "#25c9f1",          # the logo's light: cyan, violet, coral
    "violet": "#756efa",
    "coral": "#fa6474",
    "blue": "#326cf1",          # the plus
    "minus": "#e5566a",         # the minus
    "ring": "#aebccb",          # the faint orbit rings
    "line_minus": "Less friction,",
    "line_plus": "Drag Refract to Applications.",
}


def render(params=PARAMS):
    with open(SVG, encoding="utf-8") as f:
        svg = f.read()
    svg = re.sub(r"\{\{(\w+)\}\}", lambda m: params[m.group(1)], svg)
    with tempfile.TemporaryDirectory() as tmp:
        src = os.path.join(tmp, "background.svg")
        with open(src, "w", encoding="utf-8") as f:
            f.write(svg)
        for suffix, scale in (("", 1), ("@2x", 2)):
            out = os.path.join(DMG, "background%s.png" % suffix)
            subprocess.run(["swift", RASTERIZE, src, out, str(SIZE[0] * scale), str(SIZE[1] * scale)], check=True)
            print(os.path.normpath(out))


if __name__ == "__main__":
    if sys.platform != "darwin":
        sys.exit("error: needs macOS (AppKit)")
    render()
