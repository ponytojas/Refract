"""Manifest and packaging check of a tools archive (see packaging/README.md).

  tools-archive.py manifest <stage-dir> <os> <arch> <tag>   writes <stage>/tools-manifest.json
  tools-archive.py check <archive> <os> <arch>              fails if a tool, licence or the manifest is missing
  tools-archive.py release <dist-dir> <tag>                 checks every target's archive of a release
  tools-archive.py gaps <tag>                               lists the targets that have no archive

The archive is extracted into <exe dir>/../libexec/refract/ (macOS, Linux) or next to
the executable (Windows). Versions and sources come from packaging/tools.lock only.
"""

import hashlib
import json
import sys
import tarfile
import zipfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
LOCK = ROOT / "packaging" / "tools.lock"
# The licence files the TeX seed needs (scripts/tex-seed.py writes and checks it).
SEED_LICENSES = ROOT / "packaging" / "tex-seed" / "licenses.txt"
MANIFEST = "tools-manifest.json"
TARGETS = [
    ("darwin", "arm64"),
    ("darwin", "amd64"),
    ("linux", "arm64"),
    ("linux", "amd64"),
    ("windows", "amd64"),
]
# Targets with no archive, and why. Reported by the release; never advertised.
GAPS = {
    ("windows", "arm64"): "upstream Tectonic publishes no Windows arm64 build",
}


def load_lock(path=LOCK):
    values = {}
    for line in Path(path).read_text().splitlines():
        line = line.strip()
        if line and not line.startswith("#"):
            key, _, value = line.partition("=")
            values[key] = value
    return values


def hash_file_name(url):
    """Tectonic's name for a bundle URL in bundles/hashes/ (same as scripts/tex-seed.py)."""
    return url.replace(":", ",58,").replace("/", ",47,")


def required(os_name, arch):
    """Relative paths that must exist in the archive of a target."""
    lock = load_lock()
    exe = ".exe" if os_name == "windows" else ""
    files = [
        f"tectonic{exe}",
        f"synctex{exe}",
        f"rg{exe}",
        MANIFEST,
        "licenses/tectonic/LICENSE",
        "licenses/ripgrep/COPYING",
        "licenses/ripgrep/LICENSE-MIT",
        "licenses/ripgrep/UNLICENSE",
        "licenses/synctex/LICENSE",
        "licenses/zlib/LICENSE",
        # The TeX seed (scripts/tex-seed.py): the cache fragment and its licences.
        "tex-seed/seed.json",
        f"tex-seed/cache/bundles/hashes/{hash_file_name(lock['TECTONIC_BUNDLE_URL'])}",
        f"tex-seed/cache/bundles/data/{lock['TECTONIC_BUNDLE_DIGEST']}.index",
        "licenses/tex/NOTICE.md",
    ]
    seed_prefixes = [f"tex-seed/cache/bundles/data/{lock['TECTONIC_BUNDLE_DIGEST']}/", "licenses/tex/"]
    if os_name == "darwin":
        files += [
            "git/bin/git",
            "git/libexec/git-core/git",
            # HTTPS remotes (push, pull), built against macOS's libcurl; git-remote-https links to it.
            "git/libexec/git-core/git-remote-http",
            "licenses/git/COPYING",
            "licenses/git/LGPL-2.1",
            "licenses/git/SOURCE-OFFER.txt",
            f"licenses/git/source/git-{lock['GIT_VERSION']}.tar.xz",
            "licenses/git/source/build-git-macos.sh",
        ]
        prefixes = ["git/share/git-core/templates/"]
    elif os_name == "windows":
        # MinGit keeps its own tree: git.exe finds sh.exe and the helpers relative to it.
        files += [
            "git/cmd/git.exe",
            "git/ucrt64/bin/git.exe",
            "git/usr/bin/sh.exe",
            # HTTPS remotes: MinGit's Schannel libcurl, found through cmd/git.exe's PATH.
            "git/ucrt64/bin/git-remote-https.exe",
            "licenses/git/LICENSE.txt",
            "licenses/git/package-versions.txt",
            "licenses/git/SOURCE-OFFER.txt",
        ]
        prefixes = ["git/ucrt64/libexec/git-core/", "git/ucrt64/share/git-core/templates/"]
    else:
        prefixes = []
    return files, prefixes + seed_prefixes


def components(os_name, arch):
    lock = load_lock()
    names = {
        "tectonic": {
            "version": lock["TECTONIC_VERSION"],
            "source": "https://github.com/tectonic-typesetting/tectonic/releases/tag/tectonic%40" + lock["TECTONIC_VERSION"],
            "sha256": lock.get(f"TECTONIC_SHA256_{os_name}_{arch}", ""),
            "license": "MIT",
        },
        "ripgrep": {
            "version": lock["RIPGREP_VERSION"],
            "source": "https://github.com/BurntSushi/ripgrep/releases/tag/" + lock["RIPGREP_VERSION"],
            "sha256": lock.get(f"RIPGREP_SHA256_{os_name}_{arch}", ""),
            "license": "MIT OR Unlicense",
        },
        "synctex": {
            "version": lock["SYNCTEX_COMMIT"],
            "source": lock["SYNCTEX_REPO"] + "#" + lock["SYNCTEX_COMMIT"],
            "sha256": "",
            "license": "MIT",
        },
        "zlib": {
            "version": lock["ZLIB_VERSION"],
            "source": "https://github.com/madler/zlib/releases/tag/v" + lock["ZLIB_VERSION"],
            "sha256": lock["ZLIB_SHA256"],
            "license": "Zlib",
        },
        "tex-seed": {
            "version": "TeX Live 2022 (" + lock["TECTONIC_BUNDLE_URL"].rsplit("/", 1)[-1] + ")",
            "source": lock["TECTONIC_BUNDLE_URL"],
            "sha256": lock["TECTONIC_BUNDLE_DIGEST"],
            "license": "various free licences, per file (licenses/tex/NOTICE.md)",
        },
    }
    if os_name == "darwin":
        names["git"] = {
            "version": lock["GIT_VERSION"],
            "source": f"https://mirrors.edge.kernel.org/pub/software/scm/git/git-{lock['GIT_VERSION']}.tar.xz",
            "sha256": lock["GIT_SOURCE_SHA256"],
            "license": "GPL-2.0-only (and LGPL-2.1)",
        }
    elif os_name == "windows":
        names["git"] = {
            "version": lock["MINGIT_VERSION"],
            "source": "https://github.com/git-for-windows/git/releases/tag/" + lock["MINGIT_TAG"],
            "sha256": lock.get(f"MINGIT_SHA256_{os_name}_{arch}", ""),
            "license": "GPL-2.0-only and the licences of the bundled MSYS2 components (licenses/git)",
        }
    return names


def sha256_file(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def manifest(stage, os_name, arch, tag):
    stage = Path(stage)
    files = {}
    for path in sorted(stage.rglob("*")):
        if path.is_file() and not path.is_symlink() and path.name != MANIFEST:
            files[path.relative_to(stage).as_posix()] = sha256_file(path)
    data = {
        "schema": 1,
        "release": tag,
        "os": os_name,
        "arch": arch,
        "components": components(os_name, arch),
        "files": files,
    }
    (stage / MANIFEST).write_text(json.dumps(data, indent=2, sort_keys=True) + "\n")


def read_archive(path):
    """Returns {relative path: (sha256, executable)}, skipping directories and symlinks."""
    path = Path(path)
    found = {}
    if path.suffix == ".zip":
        with zipfile.ZipFile(path) as archive:
            for info in archive.infolist():
                if not info.is_dir():
                    found[info.filename] = (hashlib.sha256(archive.read(info)).hexdigest(), True)
    else:
        with tarfile.open(path) as archive:
            for member in archive:
                if member.isfile():
                    name = member.name[2:] if member.name.startswith("./") else member.name
                    digest = hashlib.sha256(archive.extractfile(member).read()).hexdigest()
                    found[name] = (digest, bool(member.mode & 0o111))
    return found


def check(archive, os_name, arch):
    """Returns the list of problems; empty when the archive is complete."""
    if (os_name, arch) in GAPS:
        return [f"{os_name}-{arch} has no tools archive: {GAPS[(os_name, arch)]}"]
    problems = []
    found = read_archive(archive)
    files, prefixes = required(os_name, arch)
    for rel in files:
        if rel not in found:
            problems.append(f"missing {rel}")
    for prefix in prefixes:
        if not any(name.startswith(prefix) for name in found):
            problems.append(f"missing {prefix}")
    for rel in files[:3]:
        if rel in found and not found[rel][1]:
            problems.append(f"{rel} is not executable")
    if os_name == "darwin":
        for rel in ("git/bin/git", "git/libexec/git-core/git-remote-http"):
            if rel in found and not found[rel][1]:
                problems.append(f"{rel} is not executable")
    if MANIFEST in found:
        with tarfile_or_zip_member(archive, MANIFEST) as handle:
            data = json.load(handle)
        expected = components(os_name, arch)
        if (data.get("os"), data.get("arch")) != (os_name, arch):
            problems.append("manifest is for another target")
        for name, component in expected.items():
            got = data.get("components", {}).get(name)
            if not got:
                problems.append(f"manifest lacks {name}")
            elif got != component:
                problems.append(f"manifest {name} does not match packaging/tools.lock")
        # Every file, not only the required ones: nothing changed, added or dropped after the build.
        listed = data.get("files", {})
        for rel, (digest, _) in sorted(found.items()):
            if rel == MANIFEST:
                continue
            if rel not in listed:
                problems.append(f"{rel} is not in the manifest")
            elif listed[rel] != digest:
                problems.append(f"manifest checksum of {rel} does not match")
        for rel in sorted(set(listed) - set(found)):
            problems.append(f"manifest lists {rel}, which is not in the archive")
    problems += check_seed_licenses(archive, found)
    return problems


def check_seed_licenses(archive, found):
    """The licence files the seed needs (seed.json "licenses", which must equal the committed
    packaging/tex-seed/licenses.txt) are in the archive with those checksums."""
    if "tex-seed/seed.json" not in found:
        return []  # reported as missing above
    with tarfile_or_zip_member(archive, "tex-seed/seed.json") as handle:
        listed = json.load(handle).get("licenses")
    if not isinstance(listed, dict) or not listed:
        return ["tex-seed/seed.json lists no licence files"]
    committed = {}
    for line in SEED_LICENSES.read_text().splitlines():
        if line.strip():
            digest, _, rel = line.partition("  ")
            committed[rel] = digest
    problems = []
    if listed != committed:
        problems.append("tex-seed/seed.json licence files differ from packaging/tex-seed/licenses.txt")
    for rel, digest in sorted(listed.items()):
        if rel not in found:
            problems.append(f"missing {rel} (a licence of the TeX seed)")
        elif found[rel][0] != digest:
            problems.append(f"{rel} does not match its checksum in tex-seed/seed.json")
    return problems


def tarfile_or_zip_member(archive, name):
    import io

    archive = Path(archive)
    if archive.suffix == ".zip":
        with zipfile.ZipFile(archive) as zf:
            return io.BytesIO(zf.read(name))
    with tarfile.open(archive) as tf:
        for member in tf:
            if member.isfile() and (member.name[2:] if member.name.startswith("./") else member.name) == name:
                return io.BytesIO(tf.extractfile(member).read())
    raise KeyError(name)


def check_release(dist, tag):
    """Problems of a release's tool archives: each target has a valid archive or a documented gap."""
    problems = []
    for os_name, arch in TARGETS:
        ext = "zip" if os_name == "windows" else "tar.gz"
        archive = Path(dist) / f"refract-tools-{tag}-{os_name}-{arch}.{ext}"
        if not archive.is_file():
            problems.append(f"missing {archive.name}")
            continue
        problems += [f"{archive.name}: {p}" for p in check(archive, os_name, arch)]
    return problems


def gaps_text(tag):
    lines = [f"Tools archives of {tag}: targets without one (no desktop installer may claim them):"]
    lines += [f"- {os_name}-{arch}: {why}" for (os_name, arch), why in sorted(GAPS.items())]
    return "\n".join(lines) + "\n"


def main(argv):
    if len(argv) == 4 and argv[1] == "release":
        problems = check_release(argv[2], argv[3])
        for problem in problems:
            print(f"error: {problem}", file=sys.stderr)
        return 1 if problems else 0
    if len(argv) == 3 and argv[1] == "gaps":
        print(gaps_text(argv[2]), end="")
        return 0
    if len(argv) == 6 and argv[1] == "manifest":
        manifest(argv[2], argv[3], argv[4], argv[5])
        return 0
    if len(argv) == 5 and argv[1] == "check":
        problems = check(argv[2], argv[3], argv[4])
        for problem in problems:
            print(f"error: {problem}", file=sys.stderr)
        return 1 if problems else 0
    print(__doc__, file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv))
