# Packaging policy

This folder covers the installable packages: the CLI packages that exist today
(Homebrew formula `refract`, WinGet manifests, `.deb`, portable archives) and the
desktop installers being added (macOS and Windows apps, Debian/Ubuntu package, Homebrew
cask `refract-app`). `server/` is a separate path for server deployments.

Keep the portable archives and their enclosing `refract-<tag>-<os>-<arch>/` directory:
WinGet's nested executable paths depend on that layout.

## What a package may contain

- The `refract` executable, which includes the server and its administration commands.
- Bundled tools, under a fixed layout: a `libexec/refract/` folder next to the binary's
  `bin/` folder (`../libexec/refract/` from the executable), and the same folder as the
  executable on Windows. #53 makes `refract` resolve its tools there. Desktop apps
  (macOS, Windows) ship private copies of Git with the helper executables and libraries
  it needs, Tectonic, SyncTeX and ripgrep (see "Bundled tools"). Debian/Ubuntu packages do not bundle Git:
  they declare it as a package dependency (`Depends: git`) and use the system copy.
- Licences and notices, and the documentation.
- A desktop entry and icons for graphical installs.
- Versions, checksums and redistribution terms (licences) of every bundled tool must be
  recorded with the release. #55 implements that record.
- A per-user service definition that is not enabled: a Homebrew `service` block for
  `brew services`, or a systemd user unit in `/usr/lib/systemd/user/`. It runs
  `refract local --no-browser` as the person, and only once they start it.

Today's packages (Homebrew formula, WinGet, `.deb`, portable archives) contain only the
binary and documentation; #58–#60 add the bundled tools to them.

## What a package must not do

- Enable or start a service, or install a system-wide one. A per-user service runs only
  when the person starts it (`refract service install` (#57), `brew services start` or
  `systemctl --user enable --now refract.service`), never from an installer,
  maintainer script, Homebrew `post_install` or cask `postflight`.
- Create keys, databases, projects or any data folder at install time. `refract local`
  creates them on first run, in per-user locations.
- Store data, the key or caches inside the application directory: they live in
  per-user locations outside it, so replacing the app's executables leaves them intact.
- Run as root after installation, or ship setuid or world-writable files.
- Touch an existing data folder or the stored credentials on install, upgrade or
  removal. Uninstalling the app keeps the person's data.
- Add server configuration (secrets, listening addresses or sign-in settings).

The packaging tests (`scripts/test_packaging.py`) enforce the rules that can be
checked: no Debian maintainer scripts, no Homebrew `post_install` or cask `postflight`,
no system-wide service or data paths, no setuid or world-writable files, and a payload
that contains only the allowed paths.

## Bundled tools

`.github/workflows/tools.yml` builds a tools archive per target, `refract-tools-<tag>-<os>-<arch>`
(`.tar.gz`, `.zip` on Windows). The release attaches them and fails when an archive lacks a
required tool or licence (`scripts/tools-archive.py`, also run by the tests). The installers
(#58, #59, #60, #66) unpack one into the tools folder described above:

```
tectonic  synctex  rg            (.exe on Windows)
git/bin/git                      macOS only
git/libexec/git-core/            macOS only: the Git helpers (GIT_EXEC_PATH)
git/share/git-core/templates/    macOS only (GIT_TEMPLATE_DIR)
git/cmd/git.exe                  Windows: MinGit keeps its own tree (see below)
tex-seed/                        the TeX seed: a Tectonic cache fragment (see "TeX seed")
licenses/<component>/...         licences, notices, and for Git the source or its offer
licenses/tex/                    licences of the TeX seed and NOTICE.md (file -> package -> licence)
tools-manifest.json              versions, sources and SHA-256 of the inputs and of every file
```

Windows deviates from the macOS paths on purpose: `git.exe` of Git for Windows finds `sh.exe`
and its helpers relative to its own `ucrt64/` and `usr/` folders, so MinGit stays unmodified
under `git/` (`git/cmd/git.exe`, `git/ucrt64/libexec/git-core/`, `git/usr/bin/`). Linux archives
have no Git: the Debian/Ubuntu package depends on the system one.

What Refract runs (`internal/tools`, #53) and with which environment:

| | Git it runs | Environment |
|---|---|---|
| macOS | `git/bin/git` | `GIT_EXEC_PATH=git/libexec/git-core`, `GIT_TEMPLATE_DIR=git/share/git-core/templates`, `GIT_CONFIG_NOSYSTEM=1` |
| Windows | `git\cmd\git.exe` (MinGit's launcher: it puts `ucrt64\bin` and `usr\bin` first on `PATH`) | `GIT_CONFIG_NOSYSTEM=1` only. No `GIT_EXEC_PATH` or `GIT_TEMPLATE_DIR`: Git finds `ucrt64\libexec\git-core` and its templates relative to itself |

HTTPS remotes (push and pull) work with only these: on macOS Git uses the system libcurl, which
checks servers against `/etc/ssl/cert.pem` (part of macOS; certificates added only to the
Keychain are not used); on Windows MinGit's default libcurl uses Schannel and the Windows
certificate store, so ignoring MinGit's `etc/gitconfig` (which sets only `core.autocrlf`,
`core.symlinks`, `credential.helper=manager` and similar) does not affect them. The smoke test
reads an HTTPS remote on both.

**Pins.** `packaging/tools.lock` is the only place with versions, sources and checksums. The
Dockerfile, `scripts/install-tex-tools.sh` (CI), `scripts/build-tools.sh` and the release all
read it; a test fails if any of them hardcodes a pin. Every download is verified against the
lock before use (`scripts/tools-common.sh`).

| Component | Source | Notes |
|---|---|---|
| Tectonic (MIT) | official release assets | musl static on Linux, MSVC static CRT on Windows |
| ripgrep (MIT or Unlicense) | official release assets | musl static on Linux |
| SyncTeX (MIT) | built from the pinned commit of jlaurens/synctex | zlib linked statically; Windows cross-built with llvm-mingw |
| zlib (Zlib) | pinned release tarball, compiled into SyncTeX | |
| Git, macOS (GPLv2, parts LGPL 2.1) | built from the official source tarball, `RUNTIME_PREFIX`, HTTPS through macOS's libcurl, Apple Common Crypto; no OpenSSL, expat, Perl, Python, Tcl or gettext | links only libSystem, libz, libiconv, libcurl and the CoreFoundation and CoreServices frameworks, which macOS provides; the smoke test fails if anything else is linked |
| Git, Windows (GPLv2 and the MSYS2/MinGW component licences) | MinGit from Git for Windows, unmodified | |

**Licence obligations.** Each archive carries the licence texts under `licenses/`. Git is GPLv2:
the macOS archive includes the exact source tarball (`licenses/git/source/`) and a source
offer; the Windows archive includes MinGit's `LICENSE.txt`, its component list and a written
offer pointing at the Git for Windows and MSYS2 sources. Tectonic's binary statically includes
many Rust crates and TeX components; its `LICENSE` points to their licences, and the crate list
is in the Tectonic source tag recorded in the manifest. Installers that ship the archive must
keep `licenses/` and the manifest and show them (for example in an About or Licences screen).
Review the Windows and Tectonic notices again when bumping a pin.

**Security updates are manual.** The libraries compiled into the tools (zlib in SyncTeX; the
Rust crates in Tectonic and ripgrep; Git and MinGit's components) are invisible to image and
package scanners, and nothing upgrades them: the release ships exactly what `tools.lock` pins.
Watch the security advisories of zlib, Git and Git for Windows, Tectonic and ripgrep, and bump
the pin (version and checksums in `tools.lock`) when one affects a bundled tool; each archive's
`tools-manifest.json` records which versions a release shipped.

**Gaps.** `windows-arm64` has no tools archive: upstream Tectonic publishes no Windows arm64
build. The release attaches `refract-tools-<tag>-gaps.txt` listing it, and no Windows arm64
desktop installer may be advertised as complete until Tectonic runs there. The macOS amd64
tools are built and smoke-tested on an Intel runner; the Windows archive is cross-built on
Linux and only smoke-tested on a Windows amd64 runner. Those runners have a Git installed:
the smoke test strips `PATH` so the bundled runtime is what runs, which is close to but not
identical to a clean machine.

To build one locally (macOS arm64 example): `scripts/build-tools.sh darwin arm64 v0.0.0-dev dist`,
then extract it and run `scripts/smoke-tools.sh <folder> darwin` and `scripts/smoke-tex-seed.sh <folder> darwin`.
`build-tools.sh` takes the seed from `TEX_SEED_DIR` (the output of `scripts/build-tex-seed.sh`); when it
is unset and the target's Tectonic runs on the host, it builds the seed itself (needs network).

## TeX seed

Tectonic downloads the TeX files a document needs on first use, so a fresh install cannot compile
offline. Every tools archive therefore carries the **TeX seed**: the bundle files that the built-in
templates and the documents in `packaging/tex-seed/documents/` need, laid out as a Tectonic cache
fragment, so that the installer or `refract local` can put it into the person's cache
(`TECTONIC_CACHE_DIR`, or Tectonic's default) and compile with no network at all.

```
tex-seed/seed.json                         schema, id, Tectonic version, bundle URL and digest,
                                           covered templates and documents, SHA-256 of every file
tex-seed/cache/bundles/hashes/<url,>       the bundle URL -> digest file (':' and '/' written ,58, and ,47,)
tex-seed/cache/bundles/data/<digest>.index the bundle index
tex-seed/cache/bundles/data/<digest>/*     the bundle files that were needed
licenses/tex/                              licence texts and NOTICE.md
```

No `formats/` (Tectonic builds the format file offline from the seed on first compile), no `*.lock`,
no `*.prefetch`. The bundle is the one `packaging/tools.lock` pins (`TECTONIC_BUNDLE_URL`, TeX Live
2022, `tlextras-2022.0r0`, the same the Docker image and `prepare-bundle.sh` use; a test checks they
agree); its digest (`TECTONIC_BUNDLE_DIGEST`) is checked against what the cache records.

**Build.** `scripts/build-tex-seed.sh <tectonic> <out-dir>` (needs network and the Tectonic pinned in
the lock) starts from an empty cache, compiles every `internal/projects/templates/<id>/<lang>` and every
document with Refract's flags (`--synctex --keep-logs --untrusted -Z continue-on-errors --bundle <URL>`),
and assembles `tex-seed/` and `licenses/tex/`. `scripts/tex-seed.py` does the work. The list of files
and their checksums is committed as `packaging/tex-seed/files.txt`: the build fails with a diff when it
changes (a new template, another package used, a new bundle or Tectonic), and
`scripts/build-tex-seed.sh <tectonic> <out-dir> --update` rewrites it. That makes the build
reproducible and reviewable. The licence files (`licenses/tex/`, with `NOTICE.md`) are listed the same
way in `seed.json` (`licenses`) and committed as `packaging/tex-seed/licenses.txt`, which `--update` rewrites
too. To cover another template, add it under `internal/projects/templates/`
(or a document under `packaging/tex-seed/documents/`), run with `--update`, and commit `files.txt` and `licenses.txt`.
In CI the `seed` job of `tools.yml` builds it once with the pinned Linux Tectonic and hands it to every
`build` job (`TEX_SEED_DIR`; the Windows cross-build cannot build it itself). `build-tools.sh` runs
`tex-seed.py verify` on a seed it is given: besides being self-consistent, its files must equal
`files.txt` and its licence files (next to it, in `licenses/tex/`) must equal `licenses.txt`. The archive
check (`tools-archive.py check`) requires every licence file `seed.json` lists, with its checksum.

**Coverage.** The five built-in templates (article, beamer, cv, letter, thesis) in English and Spanish
(10 projects) and three documents: `blank-10pt`, `blank-12pt` (a bare `article` at those sizes) and
`common` (`amsmath`, `amsthm`, `graphicx`, `booktabs`, `xcolor`, `tikz`, `siunitx`, `listings`,
`natbib`, `hyperref`, `cleveref`, `lmodern`, T1). A document that needs another package downloads it
as before when there is a network; the seed does not make that fail.

**Size.** Measured with Tectonic 0.17.0: 525 bundle files plus the 4.9 MB index, 25 MB on disk
(527 files), 7.7 MB compressed (`tar.gz` of `tex-seed/` and `licenses/tex/`), the same in every
archive.

**Use.** `refract local` (internal/compile/seed.go, cmd/refract/texseed.go) finds the seed with
`tools.Resource("tex-seed")` (the tools folder, subject to the same trust check as the tools; `REFRACT_TEX_SEED`
overrides it, `none` turns it off), copies it into `TECTONIC_CACHE_DIR` or `<data dir>/tectonic-cache` through
`os.OpenRoot` (no symlink is followed), verifying each file's SHA-256 and writing `refract-seed.json` last,
and passes `--bundle <seed bundle URL>`. It never deletes: a changed seed adds or replaces only its own
files. A cache without the marker is seeded again before the next compile. It is off for the plain server,
when `REFRACT_TECTONIC_BUNDLE` names another bundle and when the bwrap sandbox is on. Installers need
nothing else: ship `tex-seed/` next to the tools, read-only. `scripts/smoke-offline.sh` tests it end to end.

**Smoke test.** `scripts/smoke-tex-seed.sh <extracted-dir> <os>` (run by the `smoke` job after the
tools smoke test) verifies `seed.json` against the files, then, with a read-only runtime, an empty home
and a copy of the seed as the only cache, compiles every covered project and document, asserting no
`downloading` or error line, a `%PDF` output and SyncTeX data, and a `synctex view` / `synctex edit` round trip.
The network is blocked and a probe proves it, and then the documents compile with exactly Refract's flags:
macOS `sandbox-exec`, Linux `sudo unshare -n` (then `setpriv` back to the user), Windows an outbound
firewall rule on `tectonic.exe` (an administrator shell, as on CI), proven by Tectonic itself failing to
compile with an empty cache. Only outside CI, on Windows without administrator rights, the test falls back
to `--only-cached` (a file missing from the seed fails instead of downloading), which proves the coverage
but not the offline behaviour; the log states which blocking held.

**Offline smoke test.** `scripts/smoke-offline.sh <refract> <extracted-dir> <os>` (the `smoke` job builds the binary
and runs it on macOS and Linux) runs the whole of `refract local` with the network blocked, from a read-only
install with a fresh home and data folder: every template in en and es compiles with no download and a SyncTeX
round trip, a package outside the seed is reported as a needed download within a second, and after an update with
another seed id the projects, `refract.db` and `secret.key` are byte-identical while downloaded files stay and the
marker changes. Windows isn't covered (blocking its network needs an administrator).

**Licences.** The files are TeX Live packages under many free licences (mostly LPPL; also GPL, MIT,
OFL, the GUST Font License, Knuth's licence, Apache-2.0 for the glyph lists, and the licence stated in
each hyphenation file). `licenses/tex/NOTICE.md` maps every file to its TeX Live package and licence,
and `licenses/tex/` has the texts (committed in `packaging/tex-seed/licenses/`). The mapping comes from
the TeX Live 2022 database pinned in `packaging/tools.lock` (`TEXLIVE_TLPDB_URL`, `_SHA256`), plus
`packaging/tex-seed/license-overrides.txt` for files and packages it does not describe (each entry
says where its licence comes from). A seed file without a licence fails the build. The notice also says
where the sources are (the TeX Live 2022 archives and Subversion tag) and offers them on request.
Installers that ship an archive must keep `licenses/tex/` and show it with the other licences. Review
the overrides again when a template or the bundle changes.

## Server deployments

Server deployments are a distinct path: `server/` (a systemd unit and
`prepare-bundle.sh` for the compile sandbox) and the Docker image, configured with
`REFRACT_*` variables as described in "Deploy" in the top-level README. Desktop and CLI
packages never include `server/` files or the server's system-wide unit.

## Release artifacts

The release workflow builds the six existing portable archives, including README
and LICENSE. `scripts/package-deb.sh` packages each Linux binary with `dpkg-deb`
(available on the Ubuntu runner). Today's CLI packages contain only `/usr/bin/refract` and
documentation, have no maintainer scripts or required dependencies, and do not
touch credentials when installed, upgraded or removed. The desktop Debian/Ubuntu
package adds the Git dependency and desktop entry under the policy above. Prerelease tags use Debian's
`~` ordering (`v0.1.2-rc1` becomes `0.1.2~rc1`).

`scripts/package-metadata.py` uses Python 3.11+ and its standard library to verify
the expected nested executable paths and calculate SHA-256 hashes from the actual
archives. It generates a macOS Homebrew formula and three WinGet manifests (version,
English locale and the x64/arm64 zip installers). They are release assets too:

```sh
python3 scripts/package-metadata.py v0.1.2 dist /tmp/refract-packages
```

`SHA256SUMS` covers all published files, including the packages and metadata.
The release waits for CLI smoke tests on macOS ARM64, Linux AMD64 and Windows AMD64;
the Linux test installs the actual `.deb`. The other architectures are cross-built.
OAuth sign-in itself remains an interactive user step and is not exercised by CI.

## Homebrew

The tap is [ponytojas/homebrew-tap](https://github.com/ponytojas/homebrew-tap).
Copy the generated `homebrew/refract.rb` to `Formula/refract.rb` in that repository.
It uses the existing macOS binaries; neither Go nor Node is needed to install.
Validate on macOS before pushing the tap update:

```sh
ruby -c Formula/refract.rb
brew install ponytojas/tap/refract
brew test ponytojas/tap/refract
```

For automatic updates after stable releases, configure these in the **Refract**
repository's GitHub Actions settings:

- Secret `HOMEBREW_TAP_TOKEN`: a fine-grained token restricted to
  `ponytojas/homebrew-tap`, with **Contents: read and write**. Enter it directly
  in GitHub's settings, not in a shell command or a committed file.
- Variable `HOMEBREW_TAP_ENABLED`: `true`, after adding the secret.

The default `GITHUB_TOKEN` cannot write to the separate tap. Without this setup,
releases still publish packages and metadata, and the tap can be updated manually.
Prereleases never automatically update the stable formula.

## WinGet

The release asset `refract-winget-manifests.tar.gz` contains the directory structure
expected by [microsoft/winget-pkgs](https://github.com/microsoft/winget-pkgs).
Extract it and, on Windows with WinGet installed, validate the version directory:

```powershell
winget validate --manifest manifests/p/ponytojas/Refract/0.1.2
```

Test the local manifest in a disposable Windows environment (enabling local
manifests may require an administrator terminal):

```powershell
winget settings --enable LocalManifestFiles
winget install --manifest manifests/p/ponytojas/Refract/0.1.2
refract version
refract login --help
winget uninstall --id ponytojas.Refract --exact
```

After validation, submit the generated version directory in a pull request to
`microsoft/winget-pkgs`. Microsoft's acceptance is required before users can run
`winget install --id ponytojas.Refract --exact` from the community source. Repeat
for each version; no automatic submission or approval is assumed. Use the stable
release manifests for the community package.

## Tests

```sh
python3 -m unittest discover -s scripts -p 'test_*.py' -v
```

The Debian payload test uses `dpkg-deb`. The Homebrew formula test uses Ruby to
run the generated install method against temporary paths. Both tests run in Linux
CI and skip locally if their tool is missing. `make test` also runs these tests.
No tests open the OAuth browser or read real credentials.
