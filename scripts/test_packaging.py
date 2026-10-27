"""Packaging contracts: checksums, nested executables, Debian payload and metadata."""

import hashlib
import io
from pathlib import Path
import runpy
import shutil
import subprocess
import tarfile
import tempfile
import unittest
import zipfile


ROOT = Path(__file__).resolve().parents[1]
generate = runpy.run_path(str(ROOT / "scripts/package-metadata.py"))["generate"]


class PackagingTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
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
        self.assertNotIn("service do", formula)
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
                self.assertEqual(archive.getmember("./usr/bin/refract").mode, 0o755)
                self.assertEqual(archive.extractfile("./usr/bin/refract").read(), binary.read_bytes())
            scripts = subprocess.check_output(["dpkg-deb", "--ctrl-tarfile", str(package)])
            with tarfile.open(fileobj=io.BytesIO(scripts)) as archive:
                self.assertEqual([m.name for m in archive.getmembers() if m.isfile()], ["./control"])


if __name__ == "__main__":
    unittest.main()
