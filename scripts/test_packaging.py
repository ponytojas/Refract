"""Packaging contracts: checksums, nested executables, Debian payload and metadata."""

import contextlib
import hashlib
import importlib.util
import io
import json
import os
import re
from pathlib import Path
import runpy
import shutil
import subprocess
import tarfile
import tempfile
import unittest
import zipfile


ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("tools_archive", ROOT / "scripts/tools-archive.py")
tools_archive = importlib.util.module_from_spec(spec)
spec.loader.exec_module(tools_archive)
spec = importlib.util.spec_from_file_location("tex_seed", ROOT / "scripts/tex-seed.py")
tex_seed = importlib.util.module_from_spec(spec)
spec.loader.exec_module(tex_seed)
generate = runpy.run_path(str(ROOT / "scripts/package-metadata.py"))["generate"]


class PackagingTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.addCleanup(subprocess.call, ["chmod", "-R", "u+w", self.temp.name])
        self.archives = self.root / "dist"
        self.archives.mkdir()
        self.output = self.root / "metadata"

    def archives_for(self, tag):
        for platform in ("darwin", "windows"):
            for arch in ("amd64", "arm64"):
                name = f"refract-{tag}-{platform}-{arch}"
                if platform == "windows":
                    with zipfile.ZipFile(self.archives / f"{name}.zip", "w") as archive:
                        archive.writestr(f"{name}/refract.exe", f"{platform}-{arch}")
                else:
                    with tarfile.open(self.archives / f"{name}.tar.gz", "w:gz") as archive:
                        member = tarfile.TarInfo(f"{name}/refract")
                        member.mode = 0o755
                        member.size = len(arch)
                        archive.addfile(member, io.BytesIO(arch.encode()))

    def test_metadata_matches_release_archives(self):
        self.archives_for("v1.2.3")
        generate("v1.2.3", self.archives, self.output)
        formula = (self.output / "homebrew/refract.rb").read_text()
        self.assertIn("/releases/download/v1.2.3/", formula)
        self.assertNotIn('depends_on "', formula)
        winget = self.output / "winget/manifests/p/ponytojas/Refract/1.2.3"
        self.assertEqual(len(list(winget.glob("*.yaml"))), 3)
        installer = (winget / "ponytojas.Refract.installer.yaml").read_text()
        self.assertIn("NestedInstallerType: portable", installer)
        for platform in ("darwin", "windows"):
            for arch in ("amd64", "arm64"):
                ext = "zip" if platform == "windows" else "tar.gz"
                filename = f"refract-v1.2.3-{platform}-{arch}.{ext}"
                checksum = hashlib.sha256((self.archives / filename).read_bytes()).hexdigest()
                document = installer if platform == "windows" else formula
                self.assertIn(filename, document)
                self.assertIn(checksum.upper() if platform == "windows" else checksum, document)
                if platform == "windows":
                    self.assertIn(f"RelativeFilePath: refract-v1.2.3-windows-{arch}/refract.exe", installer)

    def test_rejects_missing_nested_executable_before_writing_metadata(self):
        self.archives_for("v1.2.3")
        with zipfile.ZipFile(self.archives / "refract-v1.2.3-windows-arm64.zip", "w") as archive:
            archive.writestr("refract.exe", "wrong path")
        with self.assertRaises(KeyError):
            generate("v1.2.3", self.archives, self.output)
        self.assertFalse(self.output.exists())

    def test_version_validation_and_prerelease(self):
        for tag in ("main", "v1.2.3/escape", 'v1.2.3"', "v1.2.3\n"):
            with self.assertRaises(ValueError):
                generate(tag, self.archives, self.output)
        self.archives_for("v1.2.3-rc1")
        generate("v1.2.3-rc1", self.archives, self.output)
        self.assertIn("/releases/download/v1.2.3-rc1/", (self.output / "homebrew/refract.rb").read_text())

    @unittest.skipUnless(shutil.which("dpkg-deb"), "Debian package verification needs dpkg-deb")
    def test_deb_installs_only_cli_and_docs_as_root(self):
        binary = self.root / "refract"
        binary.write_bytes(b"test executable\n")
        for arch in ("amd64", "arm64"):
            subprocess.run(["sh", str(ROOT / "scripts/package-deb.sh"), "v1.2.3-rc1", arch,
                            str(binary), str(self.archives)], cwd=ROOT, check=True, capture_output=True)
            package = self.archives / f"refract_1.2.3~rc1_{arch}.deb"
            control = subprocess.check_output(["dpkg-deb", "--field", str(package)], text=True)
            self.assertIn(f"Architecture: {arch}\n", control)
            self.assertIn("Version: 1.2.3~rc1\n", control)
            self.assertNotIn("Depends:", control)
            contents = subprocess.check_output(["dpkg-deb", "--fsys-tarfile", str(package)])
            with tarfile.open(fileobj=io.BytesIO(contents)) as archive:
                files = [m for m in archive.getmembers() if m.isfile()]
                self.assertEqual({m.name for m in files}, {"./usr/bin/refract",
                                 "./usr/share/doc/refract/copyright", "./usr/share/doc/refract/README.md"})
                self.assertTrue(all(m.uid == 0 and m.gid == 0 for m in files))
                # No setuid, setgid or world-writable entries.
                self.assertFalse([m.name for m in archive.getmembers() if m.mode & 0o6002 and not m.issym()])
                self.assertEqual(archive.getmember("./usr/bin/refract").mode, 0o755)
                self.assertEqual(archive.extractfile("./usr/bin/refract").read(), binary.read_bytes())
            scripts = subprocess.check_output(["dpkg-deb", "--ctrl-tarfile", str(package)])
            with tarfile.open(fileobj=io.BytesIO(scripts)) as archive:
                self.assertEqual([m.name for m in archive.getmembers() if m.isfile()], ["./control"])

    def test_policy_documented_and_no_install_time_side_effects(self):
        policy = (ROOT / "packaging/README.md").read_text()
        for heading in ("## What a package may contain", "## What a package must not do",
                        "## Server deployments"):
            self.assertIn(heading, policy)
        self.assertIn("../libexec/refract/", policy)
        readme = (ROOT / "README.md").read_text()
        for stale in ("not a running server", "Do not add server", "does not start a server"):
            self.assertNotIn(stale, policy + readme)
        deb = (ROOT / "scripts/package-deb.sh").read_text()
        # The only control file the script may write is DEBIAN/control: no maintainer scripts.
        self.assertEqual(set(re.findall(r"DEBIAN/([\w.-]+)", deb)), {"control"})
        # Nothing enabled or installed system-wide, and no data or configuration paths.
        for marker in ("systemctl", "lib/systemd/system", "/etc/", "/var/"):
            self.assertNotIn(marker, deb)
        # Homebrew install-time hooks: a formula may declare a service, never run code at install.
        self.archives_for("v1.2.3")
        generate("v1.2.3", self.archives, self.output)
        for formula in (self.output / "homebrew").rglob("*.rb"):
            for marker in ("post_install", "postflight", "systemctl", "launchctl"):
                self.assertNotIn(marker, formula.read_text())

    def prepare_bundle(self, dest):
        """Runs prepare-bundle.sh with a fake curl (serves a tiny bundle) and tectonic."""
        fake = self.root / "fakebin"
        if not fake.exists():
            fake.mkdir()
            digest = "6ffe055852f8faf66c0acbe1a7fb27f87b869a90bad1204f3bf4d9683f597c7c"
            tar = self.root / "bundle.tar"
            with tarfile.open(tar, "w") as archive:
                data = digest.encode()
                member = tarfile.TarInfo("SHA256SUM")
                member.size = len(data)
                archive.addfile(member, io.BytesIO(data))
            (fake / "curl").write_text(
                '#!/bin/sh\nwhile [ $# -gt 0 ]; do [ "$1" = -o ] && out=$2; shift; done\ncp "%s" "$out"\n' % tar
            )
            (fake / "tectonic").write_text(
                '#!/bin/sh\nmkdir -p "$TECTONIC_CACHE_DIR/formats" && echo fmt > "$TECTONIC_CACHE_DIR/formats/new.fmt"\n'
            )
            for name in ("curl", "tectonic"):
                (fake / name).chmod(0o755)
        env = {**os.environ, "PATH": f"{fake}:{os.environ['PATH']}"}
        return subprocess.run(
            ["sh", str(ROOT / "packaging/server/prepare-bundle.sh"), str(dest)],
            env=env, capture_output=True, text=True,
        )

    def test_prepare_bundle_replaces_both_or_refuses(self):
        dest = self.root / "server"
        first = self.prepare_bundle(dest)
        self.assertEqual(first.returncode, 0, first.stderr)
        self.assertTrue((dest / "cache/formats/new.fmt").exists())
        # Rerunning with either folder left over refuses and changes nothing.
        for keep in ("bundle", "cache"):
            subprocess.check_call(["chmod", "-R", "u+w", str(dest)])
            shutil.rmtree(dest)
            (dest / keep).mkdir(parents=True)
            again = self.prepare_bundle(dest)
            self.assertNotEqual(again.returncode, 0)
            self.assertIn("remove both", again.stderr)
            self.assertEqual(sorted(p.name for p in dest.iterdir()), [keep])
            self.assertEqual(list((dest / keep).iterdir()), [])
        # Following the printed instructions works, and nothing nests.
        subprocess.check_call(["chmod", "-R", "u+w", str(dest)])
        shutil.rmtree(dest)
        self.assertEqual(self.prepare_bundle(dest).returncode, 0)
        self.assertTrue((dest / "cache/formats/new.fmt").exists())
        self.assertFalse((dest / "cache/cache").exists())


class ToolsArchiveTests(unittest.TestCase):
    """Layout of the tool archives (#55) and the single source of the pins."""

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)

    def stage(self, os_name, arch, skip=()):
        """A fake extracted archive with every required path, plus its manifest."""
        stage = self.root / f"stage-{os_name}-{arch}"
        shutil.rmtree(stage, ignore_errors=True)
        files, prefixes = tools_archive.required(os_name, arch)
        for rel in files + [prefix + "x" for prefix in prefixes]:
            if rel == tools_archive.MANIFEST or rel in skip:
                continue
            path = stage / rel
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(rel)
            path.chmod(0o755)
        tools_archive.manifest(stage, os_name, arch, "v1.2.3")
        return stage

    def pack(self, stage, os_name, arch):
        if os_name == "windows":
            out = self.root / f"{stage.name}.zip"
            with zipfile.ZipFile(out, "w") as archive:
                for path in sorted(stage.rglob("*")):
                    if path.is_file():
                        archive.write(path, path.relative_to(stage).as_posix())
        else:
            out = self.root / f"{stage.name}.tar.gz"
            with tarfile.open(out, "w:gz") as archive:
                archive.add(stage, arcname=".")
        return out

    def test_complete_archives_pass_for_every_target(self):
        for os_name, arch in tools_archive.TARGETS:
            archive = self.pack(self.stage(os_name, arch), os_name, arch)
            self.assertEqual(tools_archive.check(archive, os_name, arch), [], (os_name, arch))

    def test_layout_matches_what_refract_resolves(self):
        mac, _ = tools_archive.required("darwin", "arm64")
        for rel in ("tectonic", "synctex", "rg", "git/bin/git", "git/libexec/git-core/git", "git/libexec/git-core/git-remote-http"):
            self.assertIn(rel, mac)
        # Windows: MinGit unmodified; Refract runs git/cmd/git.exe, which finds its own
        # helpers (no GIT_EXEC_PATH), and there is no git/bin/git.exe.
        win, win_prefixes = tools_archive.required("windows", "amd64")
        for rel in ("tectonic.exe", "synctex.exe", "rg.exe", "git/cmd/git.exe", "git/ucrt64/bin/git-remote-https.exe"):
            self.assertIn(rel, win)
        self.assertNotIn("git/bin/git.exe", win)
        self.assertIn("git/ucrt64/libexec/git-core/", win_prefixes)
        linux, _ = tools_archive.required("linux", "amd64")
        self.assertFalse([rel for rel in linux if rel.startswith("git/")], "Linux packages depend on the system Git")

    def test_missing_tool_licence_or_manifest_fails(self):
        for os_name, arch, skip in (
            ("darwin", "arm64", "synctex"),
            ("darwin", "arm64", "git/libexec/git-core/git"),
            ("darwin", "arm64", "git/libexec/git-core/git-remote-http"),
            ("windows", "amd64", "git/ucrt64/bin/git-remote-https.exe"),
            ("darwin", "amd64", "licenses/git/SOURCE-OFFER.txt"),
            ("linux", "amd64", "rg"),
            ("linux", "arm64", "licenses/ripgrep/COPYING"),
            ("windows", "amd64", "git/usr/bin/sh.exe"),
            ("windows", "amd64", "tectonic.exe"),
            ("linux", "amd64", "tex-seed/seed.json"),
            ("darwin", "arm64", "licenses/tex/NOTICE.md"),
        ):
            stage = self.stage(os_name, arch, skip=(skip,))
            problems = tools_archive.check(self.pack(stage, os_name, arch), os_name, arch)
            self.assertIn(f"missing {skip}", problems, (os_name, arch, skip))
        stage = self.stage("linux", "amd64")
        (stage / "tools-manifest.json").unlink()
        self.assertIn("missing tools-manifest.json", tools_archive.check(self.pack(stage, "linux", "amd64"), "linux", "amd64"))

    def test_tampered_file_or_stale_pin_fails(self):
        stage = self.stage("linux", "amd64")
        (stage / "rg").write_text("replaced")
        problems = tools_archive.check(self.pack(stage, "linux", "amd64"), "linux", "amd64")
        self.assertIn("manifest checksum of rg does not match", problems)
        stage = self.stage("linux", "arm64")
        manifest = json.loads((stage / "tools-manifest.json").read_text())
        manifest["components"]["tectonic"]["version"] = "0.0.1"
        (stage / "tools-manifest.json").write_text(json.dumps(manifest))
        problems = tools_archive.check(self.pack(stage, "linux", "arm64"), "linux", "arm64")
        self.assertIn("manifest tectonic does not match packaging/tools.lock", problems)

    def test_non_executable_tool_fails(self):
        stage = self.stage("linux", "amd64")
        (stage / "synctex").chmod(0o644)
        # The manifest is regenerated by stage(); only the mode differs.
        problems = tools_archive.check(self.pack(stage, "linux", "amd64"), "linux", "amd64")
        self.assertIn("synctex is not executable", problems)

    def test_release_needs_every_archive_and_reports_the_gap(self):
        dist = self.root / "dist"
        dist.mkdir()
        self.assertTrue(tools_archive.check_release(dist, "v1.2.3"))
        for os_name, arch in tools_archive.TARGETS:
            archive = self.pack(self.stage(os_name, arch), os_name, arch)
            ext = "zip" if os_name == "windows" else "tar.gz"
            shutil.copy(archive, dist / f"refract-tools-v1.2.3-{os_name}-{arch}.{ext}")
        self.assertEqual(tools_archive.check_release(dist, "v1.2.3"), [])
        (dist / "refract-tools-v1.2.3-linux-amd64.tar.gz").unlink()
        self.assertEqual(tools_archive.check_release(dist, "v1.2.3"), ["missing refract-tools-v1.2.3-linux-amd64.tar.gz"])
        self.assertIn("windows-arm64", tools_archive.gaps_text("v1.2.3"))
        self.assertTrue(tools_archive.check(self.root / "none", "windows", "arm64"))

    def test_pins_live_only_in_tools_lock(self):
        lock = tools_archive.load_lock()
        hashes = {value for value in lock.values() if re.fullmatch(r"[0-9a-f]{64}", value)}
        self.assertGreaterEqual(len(hashes), 10)
        for value in lock.values():
            self.assertNotRegex(value, r"\s")
        consumers = [
            "Dockerfile",
            "scripts/install-tex-tools.sh",
            "scripts/build-tools.sh",
            "scripts/build-synctex.sh",
            "scripts/build-git-macos.sh",
            "scripts/smoke-tools.sh",
            ".github/workflows/release.yml",
            ".github/workflows/tools.yml",
            ".github/workflows/ci.yml",
            ".github/workflows/team-sandbox.yml",
        ]
        literals = [lock[k] for k in ("TECTONIC_VERSION", "RIPGREP_VERSION", "SYNCTEX_COMMIT", "ZLIB_VERSION", "GIT_VERSION", "MINGIT_TAG")]
        for name in consumers:
            text = (ROOT / name).read_text()
            self.assertNotRegex(text, r"[0-9a-f]{64}", f"{name} hardcodes a checksum")
            self.assertNotRegex(text, r"[0-9a-f]{40}", f"{name} hardcodes a commit")
            for literal in literals:
                self.assertNotIn(literal, text, f"{name} hardcodes {literal}")
        self.assertIn("packaging/tools.lock", (ROOT / "Dockerfile").read_text())
        for name in ("install-tex-tools.sh", "build-tools.sh", "build-synctex.sh", "build-git-macos.sh"):
            self.assertIn("tools-common.sh", (ROOT / "scripts" / name).read_text())
        self.assertIn("packaging/tools.lock", (ROOT / "scripts/tools-common.sh").read_text())

    def test_bundle_pin_is_the_lock_everywhere(self):
        lock = tools_archive.load_lock()
        url, digest = lock["TECTONIC_BUNDLE_URL"], lock["TECTONIC_BUNDLE_DIGEST"]
        for name in ("Dockerfile", ".github/workflows/ci.yml", "packaging/server/prepare-bundle.sh"):
            text = (ROOT / name).read_text()
            self.assertIn(url, text, name)
            for other in re.findall(r"https://\S*tlextras-\S*\.tar", text):
                self.assertEqual(other, url, name)
        self.assertIn(digest, (ROOT / "packaging/server/prepare-bundle.sh").read_text())
        for name in ("Dockerfile", ".github/workflows/ci.yml"):
            self.assertNotIn(digest, (ROOT / name).read_text(), name)
        self.assertEqual(tex_seed.hash_file_name(url), tools_archive.hash_file_name(url))
        self.assertEqual(tex_seed.hash_file_name("https://a/b:c"), "https,58,,47,,47,a,47,b,58,c")

    def test_tex_seed_needs_a_licence_for_every_file(self):
        packages = {
            "known": {"lic": "lppl1.3c", "run": ["tex/latex/known/known.sty"]},
            "nolic": {"lic": None, "run": ["tex/generic/nolic/nolic.tex"]},
            "mapped": {"lic": None, "run": ["tex/generic/mapped/hyph-xx.tex"]},
        }
        overrides = {("glob", "hyph-*.tex"): ["in-file"]}
        found = tex_seed.attribute(["known.sty", "hyph-xx.tex"], packages, overrides)
        self.assertEqual(found["known.sty"], [("known", ["lppl1.3c"])])
        self.assertEqual(found["hyph-xx.tex"][0][1], ["in-file"])
        with self.assertRaises(SystemExit):
            tex_seed.attribute(["nolic.tex"], packages, overrides)
        with self.assertRaises(SystemExit):
            tex_seed.attribute(["unknown.tex"], packages, overrides)
        # The most specific glob wins, whichever line comes first.
        overrides = {("glob", "hyph-*.tex"): ["in-file"], ("glob", "hyph-quote-*.tex"): ["derived"]}
        self.assertEqual(tex_seed.attribute(["hyph-quote-xx.tex"], packages, overrides)["hyph-quote-xx.tex"][0][1], ["derived"])

    def test_tex_seed_licence_texts_exist_and_covers_match_the_repository(self):
        for name, (filename, _) in tex_seed.TEXTS.items():
            if filename:
                self.assertTrue((ROOT / "packaging/tex-seed/licenses" / filename).is_file(), name)
        overrides = tex_seed.load_overrides()
        self.assertTrue(overrides)
        documents = {p.stem for p in (ROOT / "packaging/tex-seed/documents").glob("*.tex")}
        self.assertEqual(documents, {"blank-10pt", "blank-12pt", "common"})
        labels = [label for label, _, _ in tex_seed.covered_projects()]
        self.assertIn("article/en", labels)
        self.assertEqual(len(labels), len(set(labels)))

    def test_tex_seed_verify_checks_files_and_refuses_unsafe_paths(self):
        lock = tools_archive.load_lock()

        def seed_with(files, extra=None):
            seed = Path(tempfile.mkdtemp())
            self.addCleanup(shutil.rmtree, seed, True)
            hashes = {}
            for rel, body in files.items():
                (seed / rel).parent.mkdir(parents=True, exist_ok=True)
                (seed / rel).write_bytes(body)
                hashes[rel] = hashlib.sha256(body).hexdigest()
            hashes.update(extra or {})
            listing = "".join(f"{v}  {r}\n" for r, v in sorted(hashes.items()))
            covers = {"templates": [label for label, _, _ in tex_seed.covered_projects()],
                      "documents": [d.stem for d in tex_seed.covered_documents()]}
            (seed / "seed.json").write_text(json.dumps({
                "schema": 1, "id": hashlib.sha256(listing.encode()).hexdigest(), "tectonic": lock["TECTONIC_VERSION"],
                "bundle": {"url": lock["TECTONIC_BUNDLE_URL"], "digest": lock["TECTONIC_BUNDLE_DIGEST"]},
                "covers": covers, "files": hashes}))
            return seed

        with contextlib.redirect_stdout(io.StringIO()):
            tex_seed.verify(seed_with({"cache/bundles/a.tex": b"a"}))
        for extra in ({"../escape": "0" * 64}, {"/abs": "0" * 64}, {"cache/../x": "0" * 64}, {"other/x": "0" * 64}):
            with self.subTest(extra=extra), contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
                tex_seed.verify(seed_with({"cache/bundles/a.tex": b"a"}, extra))
        seed = seed_with({"cache/bundles/a.tex": b"a"})
        (seed / "cache/bundles/a.tex").write_bytes(b"b")
        with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
            tex_seed.verify(seed)

    def test_tools_workflow_builds_and_smokes_the_seed(self):
        tools = (ROOT / ".github/workflows/tools.yml").read_text()
        for phrase in ("seed:", "needs: seed", "TEX_SEED_DIR", "smoke-tex-seed.sh", "name: tex-seed",
                       "internal/projects/templates/**", "packaging/tex-seed/**", "scripts/*tex-seed*",
                       "smoke-offline.sh"):
            self.assertIn(phrase, tools)
        self.assertIn("TEX_SEED_DIR", (ROOT / "scripts/build-tools.sh").read_text())

    def test_release_builds_and_gates_the_tool_archives(self):
        release = (ROOT / ".github/workflows/release.yml").read_text()
        self.assertIn("uses: ./.github/workflows/tools.yml", release)
        self.assertIn("tools-archive.py release", release)
        tools = (ROOT / ".github/workflows/tools.yml").read_text()
        for system in ("macos-15", "macos-15-intel", "ubuntu-24.04-arm", "windows-latest"):
            self.assertIn(system, tools)
        self.assertIn("smoke-tools.sh", tools)

    def test_readme_records_versions_and_git_obligations(self):
        readme = (ROOT / "packaging/README.md").read_text()
        for phrase in ("packaging/tools.lock", "GPLv2", "source", "windows-arm64", "tools-manifest.json", "git/libexec/git-core", "TeX seed", "files.txt"):
            self.assertIn(phrase, readme)


if __name__ == "__main__":
    unittest.main()
