#!/usr/bin/env python3
"""Builds the Refract disk image: the app, a link to Applications and a designed window.

    python3 scripts/make-dmg.py <Refract.app> <output.dmg> [--volume-name Refract]

Uses dmgbuild (MIT; pinned with hashes in scripts/requirements-dmg.txt), which writes the
window's .DS_Store itself, so no Finder or logged-in session is needed. Layout: macos/dmg/settings.py,
background: macos/dmg/background*.png (scripts/make-dmg-background.py), icon: macos/Resources/AppIcon.icns.
The image is not signed here: macos/sign-dmg.sh signs it and macos/notarize-dmg.sh notarizes it.
macOS only (dmgbuild mounts the image with hdiutil).
"""
import argparse
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
SETTINGS = os.path.normpath(os.path.join(HERE, "..", "macos", "dmg", "settings.py"))


def build(app, output, volume_name="Refract"):
    try:
        import dmgbuild
    except ImportError:
        sys.exit(
            "error: dmgbuild is not installed. In a virtualenv:\n"
            "  python3 -m venv .venv && .venv/bin/pip install --require-hashes -r scripts/requirements-dmg.txt\n"
            "  .venv/bin/python scripts/make-dmg.py ..."
        )
    if not os.path.isdir(os.path.join(app, "Contents")):
        sys.exit("error: %s is not an app bundle" % app)
    if os.path.exists(output):
        os.remove(output)  # dmgbuild refuses to overwrite
    dmgbuild.build_dmg(
        output, volume_name, settings_file=SETTINGS,
        defines={"app": os.path.abspath(app), "settings_path": SETTINGS},
    )


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("app", help="the Refract.app to put in the image")
    ap.add_argument("output", help="the .dmg to write")
    ap.add_argument("--volume-name", default="Refract")
    args = ap.parse_args()
    if sys.platform != "darwin":
        sys.exit("error: needs macOS")
    build(args.app, args.output, args.volume_name)
    print(args.output)


if __name__ == "__main__":
    main()
