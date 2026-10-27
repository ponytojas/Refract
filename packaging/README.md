# CLI packages

The package-manager installations are for `refract login` and other CLI commands.
They install the existing executable, not a running server. Do not add server
services, encryption keys, data directories or LaTeX dependencies to these packages.
Keep the portable archives and their enclosing `refract-<tag>-<os>-<arch>/` directory:
WinGet's nested executable paths depend on that layout.

## Release artifacts

The release workflow builds the six existing portable archives, including README
and LICENSE. `scripts/package-deb.sh` packages each Linux binary with `dpkg-deb`
(available on the Ubuntu runner). Packages contain only `/usr/bin/refract` and
documentation, have no maintainer scripts or required dependencies, and do not
touch credentials when installed, upgraded or removed. Prerelease tags use Debian's
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

The Debian payload test needs `dpkg-deb`; it runs in Linux CI and skips if the tool
is absent locally. `make test` also runs these tests. No tests open the OAuth
browser or read real credentials.
