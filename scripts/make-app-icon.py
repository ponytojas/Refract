#!/usr/bin/env python3
"""Regenerates macos/Resources/AppIcon.icns from assets/logo.svg.

    python3 scripts/make-app-icon.py            # rewrite the .icns
    python3 scripts/make-app-icon.py --png out/ # also keep the 1024 px preview there

macOS only: it renders with AppKit (scripts/render-icon.swift, run by `swift`) and packs with
`iconutil`. AppKit because it is already on every Mac with Xcode's tools and reads SVG itself,
so nothing is installed and no rasteriser's version changes the output; sips and qlmanage can't
compose a shadow and a gradient behind the logo. Commit the result.

Everything that sets the look (colours, sizes, shadow) is in PARAMS below.
"""
import argparse
import json
import os
import shutil
import subprocess
import sys
import tempfile

ROOT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..")
LOGO = os.path.join(ROOT, "assets", "logo.svg")
ICNS = os.path.join(ROOT, "macos", "Resources", "AppIcon.icns")
RENDERER = os.path.join(os.path.dirname(os.path.abspath(__file__)), "render-icon.swift")

# The look. Lengths are in a 1024 px canvas (Apple's template: an 824 px shape, the rest is shadow room).
# Two variants, picked with --variant: "paper" (the default, the landing page's light surface) and
# "night" (its dark one). A variant only overrides the colours; the geometry is shared.
PARAMS = {
    "canvas": 1024,
    "shape": 824,
    "squircle_exponent": 5,        # 2 is a circle, large is a square; about 5 matches the system shape
    "rim_width": 2,                # a hairline border
    "shadow_dy": 12,               # soft shadow under the shape
    "shadow_blur": 28,
    "shadow_alpha": 0.32,
    "logo_box": [259, 104, 721, 1080],  # the visible part of logo.svg: x, y, width, height
    "logo_height": 0.66,           # the logo's height as a share of the shape
    "logo_dx": -34,                # nudge right (positive) or left: leaves room for the beam
    "logo_dy": 0,                  # nudge up (positive) or down, in canvas px
    "glow_cyan": "#25c9f1",        # the glow behind the logo and the beam shooting out to the right
    "glow_violet": "#756efa",
    "glow_coral": "#fa6474",
    "glow_alpha": 0.30,            # strength of the glow behind the logo
    "beam_alpha": 0.55,            # strength of the beam
    "beam_dy": -30,                # the beam leaves the logo this far up (positive) or down
    "ring_alpha": 0.10,            # the faint orbit lines
    "variant": "paper",
}
VARIANTS = {
    "paper": {
        "bg_top": "#ffffff", "bg_bottom": "#e9eff5",   # near white with a very soft cool gradient
        "rim": "#9fb0c0", "rim_alpha": 0.45,
        "ring": "#7f93a8", "shadow_alpha": 0.28,
    },
    "night": {
        "bg_top": "#16252e", "bg_bottom": "#0d161c",   # the landing's "night", #111c23, in the middle
        "rim": "#ffffff", "rim_alpha": 0.12,
        "ring": "#ffffff", "shadow_alpha": 0.5,
        "glow_alpha": 0.38, "beam_alpha": 0.7,
    },
}

# iconutil's names: the pixel size each file needs.
ICONSET = {
    "icon_16x16.png": 16, "icon_16x16@2x.png": 32,
    "icon_32x32.png": 32, "icon_32x32@2x.png": 64,
    "icon_128x128.png": 128, "icon_128x128@2x.png": 256,
    "icon_256x256.png": 256, "icon_256x256@2x.png": 512,
    "icon_512x512.png": 512, "icon_512x512@2x.png": 1024,
}


def build(out_icns, png_dir=None, variant="paper"):
    params_ = dict(PARAMS, **VARIANTS[variant], variant=variant)
    with tempfile.TemporaryDirectory() as tmp:
        params = os.path.join(tmp, "params.json")
        with open(params, "w") as f:
            json.dump(params_, f)
        sizes = sorted(set(ICONSET.values()))
        subprocess.run(["swift", RENDERER, LOGO, params, tmp] + [str(s) for s in sizes], check=True)
        iconset = os.path.join(tmp, "AppIcon.iconset")
        os.mkdir(iconset)
        for name, px in ICONSET.items():
            shutil.copy(os.path.join(tmp, "icon_%d.png" % px), os.path.join(iconset, name))
        subprocess.run(["iconutil", "-c", "icns", iconset, "-o", out_icns], check=True)
        if png_dir:
            os.makedirs(png_dir, exist_ok=True)
            for px in sizes:
                shutil.copy(os.path.join(tmp, "icon_%d.png" % px), png_dir)


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("-o", "--output", default=ICNS, help="the .icns to write (default: macos/Resources/AppIcon.icns)")
    ap.add_argument("--png", metavar="DIR", help="also keep the rendered PNGs (icon_<px>.png) in DIR")
    ap.add_argument("--variant", choices=sorted(VARIANTS), default="paper", help="paper (default, light) or night (dark)")
    args = ap.parse_args()
    if sys.platform != "darwin":
        sys.exit("error: needs macOS (AppKit and iconutil)")
    build(args.output, args.png, args.variant)
    print(args.output)


if __name__ == "__main__":
    main()
