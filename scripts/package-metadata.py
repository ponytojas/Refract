#!/usr/bin/env python3
"""Generate Homebrew and WinGet metadata from the exact release archives.

Usage: python3 scripts/package-metadata.py v0.1.2 dist packaging-output
Only the Python standard library is required. Run before publishing the release.
"""

import argparse
import hashlib
from pathlib import Path
import re
import tarfile
import zipfile


REPO = "https://github.com/ponytojas/Refract"
IDENTIFIER = "ponytojas.Refract"
SCHEMA = "1.12.0"


def generate(tag: str, archives: Path, output: Path) -> None:
    if not re.fullmatch(r"v\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?", tag):
        raise ValueError("Expected a version tag such as v0.1.2 or v0.1.2-rc1")
    version = tag[1:]
    hashes = {}
    # Check the nested binary paths too: Homebrew strips the enclosing directory,
    # whereas WinGet needs the full path within the zip.
    for platform in ("darwin", "windows"):
        for arch in ("amd64", "arm64"):
            name = f"refract-{tag}-{platform}-{arch}"
            extension = ".zip" if platform == "windows" else ".tar.gz"
            path = archives / (name + extension)
            if platform == "windows":
                with zipfile.ZipFile(path) as archive:
                    archive.getinfo(f"{name}/refract.exe")
            else:
                with tarfile.open(path) as archive:
                    member = archive.getmember(f"{name}/refract")
                    if not member.isfile() or not member.mode & 0o111:
                        raise ValueError(f"Not an executable: {path}")
            with path.open("rb") as source:
                hashes[platform, arch] = hashlib.file_digest(source, "sha256").hexdigest()

    formula = output / "homebrew" / "refract.rb"
    formula.parent.mkdir(parents=True, exist_ok=True)
    base = f"{REPO}/releases/download/{tag}"
    formula.write_text(f'''class Refract < Formula
  desc "CLI for the Refract LaTeX workspace"
  homepage "{REPO}"
  license "MIT"

  depends_on :macos

  on_macos do
    on_arm do
      url "{base}/refract-{tag}-darwin-arm64.tar.gz"
      sha256 "{hashes['darwin', 'arm64']}"
    end
    on_intel do
      url "{base}/refract-{tag}-darwin-amd64.tar.gz"
      sha256 "{hashes['darwin', 'amd64']}"
    end
  end

  def install
    bin.install "refract"
  end

  def caveats
    <<~EOS
      Run `refract login` to sign in with ChatGPT, then upload the credential
      file in your Refract instance under Settings > Assistant.
    EOS
  end

  test do
    assert_match "refract {tag}", shell_output("#{{bin}}/refract version")
    assert_match "refract login", shell_output("#{{bin}}/refract --help")
    assert_match "-no-browser", shell_output("#{{bin}}/refract login --help 2>&1")
  end
end
''', encoding="utf-8")

    winget = output / "winget" / "manifests" / "p" / "ponytojas" / "Refract" / version
    winget.mkdir(parents=True, exist_ok=True)

    def manifest(kind: str, body: str, suffix: str = "") -> None:
        header = f"# yaml-language-server: $schema=https://aka.ms/winget-manifest.{kind}.{SCHEMA}.schema.json\n"
        (winget / f"{IDENTIFIER}{suffix}.yaml").write_text(
            header + f"PackageIdentifier: {IDENTIFIER}\nPackageVersion: {version}\n" + body
            + f"ManifestType: {kind}\nManifestVersion: {SCHEMA}\n", encoding="utf-8"
        )

    manifest("version", "DefaultLocale: en-US\n")
    manifest("defaultLocale", f'''PackageLocale: en-US
Publisher: ponytojas
PublisherUrl: https://github.com/ponytojas
PackageName: Refract
PackageUrl: {REPO}
License: MIT
LicenseUrl: {REPO}/blob/{tag}/LICENSE
ShortDescription: CLI for the Refract LaTeX workspace
Description: Sign in with ChatGPT using refract login and upload the credential file to your Refract instance. Login requires no local server or LaTeX installation.
Moniker: refract
Tags:
  - cli
  - latex
  - chatgpt
''', ".locale.en-US")
    installers = "InstallerType: zip\nNestedInstallerType: portable\nCommands:\n  - refract\nInstallers:\n"
    for arch, winget_arch in (("amd64", "x64"), ("arm64", "arm64")):
        name = f"refract-{tag}-windows-{arch}"
        installers += f'''  - Architecture: {winget_arch}
    InstallerUrl: {base}/{name}.zip
    InstallerSha256: {hashes['windows', arch].upper()}
    NestedInstallerFiles:
      - RelativeFilePath: {name}/refract.exe
        PortableCommandAlias: refract
'''
    manifest("installer", installers, ".installer")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("tag")
    parser.add_argument("archives", type=Path)
    parser.add_argument("output", type=Path)
    args = parser.parse_args()
    generate(args.tag, args.archives, args.output)
