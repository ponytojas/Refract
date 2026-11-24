# Refract for macOS

A Swift app that runs `refract local --embedded` (the contract is in the top-level README,
"For packagers") and shows it in a `WKWebView`. The Go server stays the only backend.

```
macos/
  Package.swift            SwiftPM package: RefractCore (library), Refract (app), tests
  Sources/RefractCore/     the server's lifecycle, no UI: status line, supervisor, logs, bundle layout
  Sources/Refract/         AppKit: window, loading/error views, WKWebView, quit sequence, minimal menu
  Resources/               Info.plist, the English/Spanish strings and AppIcon.icns
  dmg/                     the disk image's window: settings.py, background.svg and its PNGs
  Tests/RefractCoreTests/  lifecycle tests against a fake server script
  build-app.sh             assembles Refract.app, then signs and verifies it (run by `make mac`)
  sign-app.sh              signs every Mach-O inside out, then the app (REFRACT_SIGN_IDENTITY)
  verify-app.sh            the check that fails on any unsigned or ad hoc Mach-O (--release)
  notarize.sh              notarizes, staples and assesses a signed app (`make mac-release`)
  build-dmg.sh             builds the disk image, and signs, notarizes and staples it with an identity
```

## Decisions

**SwiftPM and an assembly script, not an Xcode project.** A hand-written `.pbxproj` is
fragile and unreadable in review, `xcodebuild` adds nothing the app needs yet, and
SwiftPM builds and tests from the command line (`swift build`, `swift test`). The script
puts the bundle together. #69 (signing) and #70 (Sparkle) work from the same script. If a
feature ever needs an Xcode-only capability (asset catalogs for the icon, extensions), generate the
project from this package then.

**Minimum macOS 13 (Ventura).** The app needs `callAsyncJavaScript` (11), `WKDownload` (11.3) and
`WKHTTPCookieStore` (10.13), and the Go 1.27 binary is built for 13 (checked with `vtool`). 13 keeps the audience wide without
old-API workarounds, and it is the maintainer's to raise; the Swift app, the Go binary and, through
`MACOSX_DEPLOYMENT_TARGET` in `scripts/tools-common.sh`, Git and SyncTeX target it (`smoke-tools.sh`
checks the tools). Tectonic and ripgrep are upstream builds that target 11.

**One app per architecture.** The tools are built on a Mac of their own architecture (Git and
SyncTeX are compiled, not downloaded), so a universal app would need both archives on one machine
and `lipo` for every Mach-O, doubling the download (about 100 MB per architecture) for nothing the
person gains: an update channel (#70) and a Homebrew cask serve each architecture its own app.
`make mac MAC_ARCH=amd64` builds the Intel app on an Intel Mac, or with a `TOOLS_ARCHIVE` built
there; Go and Swift cross-compile.

**Layout: `Contents/Resources/Server/{bin/refract,libexec/refract/...}`, not `Contents/Helpers`.**
`internal/tools` (#53) finds the tools at `<exe dir>/../libexec/refract`, and this layout keeps that
rule unchanged. `Helpers/` was the candidate, and it fails: codesign's default rules treat every
file under `Helpers/` (like `MacOS/` and `Frameworks/`) as nested code, and Git's tree holds shell
scripts, templates and licences, so `codesign` refuses to seal the app (`code object is not signed
at all, in subcomponent: .../LICENSE-MIT`). Under `Resources/` the files are sealed as resources: the
build signs every Mach-O first (hardened runtime), then the app, and
`codesign --verify --deep --strict` passes. Changing one byte of a tool afterwards makes the
verification fail (checked). `--deep` does not look at signatures of code under `Resources/`, so
`verify-app.sh` also verifies each Mach-O, scanning the whole bundle the same way (see "Signing and
notarization" below).

**Same data folder as `refract local`.** The app sets no `REFRACT_DATA_DIR` (and drops the
variables `refract local` refuses), so the server uses `~/Library/Application Support/Refract`.

**A remembered port.** The web app keeps settings in the browser, per origin, and the origin includes
the port, so a different free port at every launch would forget them. The app asks for the port it
used last (`--port`) and, if it is taken, goes back to a free one.

## The server's life

- Start: token on a pipe (descriptor 3), standard input a pipe the app keeps, standard output read
  for one JSON line, standard error written straight to `~/Library/Logs/Refract/refract.log`
  (a file over 2 MB is rotated at the next start; two old files are kept). The child has its own
  process group and inherits nothing else from the app.
- `ready`: the token goes into the web view's cookie store (`refract_embed`, host only, HttpOnly,
  SameSite=Strict) and the page loads. `running` from a terminal: its URL is loaded as it is.
  Either way, before the first load the web view gets a `WKContentRuleList` (`originOnlyRules` in
  RefractCore) that blocks every request outside that exact origin (scheme, host and port),
  subresources included, and allows only `blob:`, `data:` and `about:` besides: a host-only cookie
  goes to every port of the host, so this is what keeps the token away from other local services
  (project files are served under a `sandbox` CSP, which doesn't restrict loads). If the rules
  don't compile, nothing loads and the error view shows.
  `running` with `embedded`, `unresponsive`, `error` or no line: the error view.
- Quit (also `osascript -e 'quit app "Refract"'`): `window.refractFlush()`; if it answers
  false the person is asked; then standard input is closed, a progress view shows after one second,
  and the server's process group is killed after 30 seconds (a compile in flight runs in a group
  of its own, which the server stops when it shuts down normally but a kill doesn't reach). If the app itself dies, the pipe closes and the
  server stops by itself.
- The server exits after it was ready: the error view with the end of the log, Restart (stops what is
  left, starts again with a new token) and Show Logs. Projects and the database are not touched.
- A missing or non-executable server or tool, or `missing_tools` in `/api/me`: an error view with a
  link to download the app again (the bundle is signed and read-only: repairing is replacing it).

## Signing and notarization

Gatekeeper accepts the downloaded app only if the app and every executable in it are signed with a
Developer ID Application certificate, use the hardened runtime, and the whole is notarized by Apple.

**What the scripts do.** `sign-app.sh` signs every Mach-O (the tools, Git's helpers and libraries,
the server, then the app's executable) with `--options runtime`, a stable identifier
(`<bundle id>.<file name>`; the linker's carries a hash that changes every build) and, for a real
identity, a secure timestamp, then the app that seals them. Symlinks (Git's `git-*` links) carry no signature and are
not signed. `verify-app.sh` runs `codesign --verify --deep --strict`, then checks every Mach-O:
valid signature, hardened runtime and no entitlements in every architecture slice, no symlink leaving the bundle, and with
`--release` also: not ad hoc, a Developer ID Application authority, a secure timestamp and the same
TeamIdentifier as the app. `build-app.sh` runs both (`--release` is left to `notarize.sh`, so `make mac` also works with an Apple Development or self-signed identity; only a
`Developer ID Application:` identity gets the secure timestamp), so a build with an unsigned binary fails. `notarize.sh` zips with
`ditto`, submits with `notarytool submit` (printing the submission id at once), waits with `notarytool wait`, prints `notarytool log` when Apple rejects it, staples,
runs `stapler validate` and `spctl --assess --type execute -vv`, and verifies again.

**No entitlements, no sandbox.** The hardened runtime only needs an entitlement for what it
forbids: JIT or unsigned executable memory, DYLD environment variables, loading libraries signed by
another team, debugging. The Go server, Tectonic (Rust), ripgrep, SyncTeX and Git are ahead-of-time
compiled and load only system libraries and their own files; the Swift app uses `WKWebView`, whose
web content runs in Apple's own processes. Checked: the ad hoc build with the hardened runtime and
no entitlements launches, runs `refract local` (tools found as `bundled`), and each tool runs
(`tectonic`, `git`, `rg`, `synctex`). The App Sandbox, and with it the network-client and
network-server entitlements, is not used: the app starts a server, runs Git and Tectonic on
the person's files in their own folders, and reads `~/Library/Application Support/Refract`, which a
sandbox would forbid or move. If a future tool needs an entitlement (a JIT, say), add a file here
with the one key and its reason, and have `sign-app.sh` pass it for that file only.

### Maintainer: one-time setup

You need the Apple Developer Program account; only the Account Holder can create the certificate.

1. **Developer ID Application certificate.** In Xcode: Settings → Accounts → your team → Manage
   Certificates → `+` → Developer ID Application. Or at developer.apple.com → Certificates,
   Identifiers & Profiles → Certificates → `+` → Developer ID Application (upload a CSR made with
   Keychain Access → Certificate Assistant → Request a Certificate From a Certificate Authority;
   download the `.cer` and open it). The identity name, `Developer ID Application: Your Name
   (TEAMID)`, is the value of `REFRACT_SIGN_IDENTITY`.
2. **Export it as a .p12** (for CI): Keychain Access → My Certificates → right-click the certificate
   (with its private key under it) → Export → `.p12`, with a strong password. Keep the file out of
   the repository.
3. **App Store Connect API key.** appstoreconnect.apple.com → Users and Access → Integrations →
   Team Keys → `+`, role Developer (enough for `notarytool`; the first time, the Account Holder has
   to request API access on that page). Download the `.p8` once (Apple will not show it again) and note
   the Key ID and the Issuer ID shown on that page.
4. **Local notarization profile**, stored in your keychain:

   ```sh
   xcrun notarytool store-credentials refract-notary --key /path/to/AuthKey_XXXX.p8 --key-id XXXX --issuer ISSUER-UUID
   ```

5. **Repository secrets** (names only here; the values stay on your computer):

   ```sh
   base64 < certificate.p12 | tr -d '\n' | gh secret set MACOS_CERT_P12_BASE64
   gh secret set MACOS_CERT_PASSWORD            # paste the .p12 password at the prompt
   gh secret set MACOS_SIGN_IDENTITY --body 'Developer ID Application: Your Name (TEAMID)'
   base64 < AuthKey_XXXX.p8 | tr -d '\n' | gh secret set NOTARY_KEY_P8_BASE64
   gh secret set NOTARY_KEY_ID --body XXXX
   gh secret set NOTARY_ISSUER_ID --body ISSUER-UUID
   ```

   Then delete the `.p12` and the `.p8` copies you don't need (keep them in a password manager).

### Producing a signed, notarized app

Locally, on a Mac of the architecture you want (the Git and SyncTeX tools are compiled there):

```sh
REFRACT_SIGN_IDENTITY="Developer ID Application: Your Name (TEAMID)" \
REFRACT_NOTARY_PROFILE=refract-notary make mac-release
```

It leaves `build/mac/<arch>/Refract.app` (stapled) and `Refract-<arch>.zip`. Check by hand with
`macos/verify-app.sh --release build/mac/<arch>/Refract.app`, `xcrun stapler validate`,
`spctl --assess --type execute -vv`, and, from a zip downloaded the way people will, on a Mac
without developer tools.

In CI, `.github/workflows/macos-app.yml` builds both architectures (macos-15 and macos-15-intel),
imports the certificate into a temporary keychain with a random password, signs, notarizes with the
API key, staples, verifies the unpacked zip and uploads it. The release workflow (#71) calls it with
`secrets: inherit`, `require-signed: true` (missing secrets fail the run instead of producing an
ad hoc build) and `tools-artifact: true` (it signs the `tools-darwin-<arch>` archive that
`tools.yml` built and smoke-tested earlier in the same run, instead of building another). When the
secrets are missing (a fork's pull request, or none set) and `require-signed` is off, and always on
pull requests, it builds ad hoc, says so in a notice and uploads `Refract-app-adhoc-<arch>`, which
Gatekeeper rejects. Pull requests never sign: the workflow skips the signing steps for them.

Secrets never appear in a log or a file in the repository: the scripts read them from the
environment and never print them, the p12 and the key are written to the runner's temp folder with
owner-only permissions and removed at the end, and the temporary keychain is deleted. The p12
password and the key and issuer ids are passed as arguments to `security` and `notarytool` on the
runner, which is used by this job only and discarded.

## Icon and disk image

**The icon** is generated, not drawn by hand: `python3 scripts/make-app-icon.py` renders
`assets/logo.svg` onto the macOS icon shape (a 1024 px canvas, an 824 px rounded square, a soft shadow,
a faint cyan/violet halo behind the logo and a cyan-to-coral beam leaving it to the right, like the
landing page) at 16 to 512 px, 1x and 2x, and writes
`macos/Resources/AppIcon.icns` with `iconutil`. `--variant paper` (the default, the one checked in: a near white squircle with a
cool gradient and a hairline) or `--variant night` (`#111c23`, same light) picks the colours; every
colour and measure is in `PARAMS` and `VARIANTS` at the top of that script. It renders with AppKit (`scripts/render-icon.swift`, run by `swift`): AppKit reads
SVG, is on every Mac with Xcode's tools, and composes the shadow and gradient, where `sips` and
`qlmanage` can't. Commit the regenerated `.icns`; `build-app.sh` copies it into the app and
`Info.plist` names it (`CFBundleIconFile`). After a logo change, regenerate and look at it in
the Dock and in a Finder list view.

**The disk image's window** is made by `scripts/make-dmg.py` with
[dmgbuild](https://github.com/dmgbuild/dmgbuild) (MIT), which writes the window's `.DS_Store`
without Finder or AppleScript, so it runs on a headless CI runner. The layout (660 x 420 of content, 128 pt
icons, the app at (180, 200), the Applications link at (480, 200), no toolbar, sidebar, path bar or
status bar, UDZO) is `macos/dmg/settings.py`. The background is `macos/dmg/background.svg`
rendered to `background.png` and `background@2x.png` by `python3 scripts/make-dmg-background.py`
(colours and the two lines of text are `PARAMS` there; English only). It is the landing page's
language on its paper colours: a pale red "−" band and a pale cyan "+" band for the instruction, and a
cyan, violet and coral beam with an arrowhead between the two icons. The text is Helvetica Neue,
the system font, not Switzer: the Switzer files on the landing page are woff2, which AppKit can't load,
and converting them would make a derivative of the font. To build one by hand:

```sh
python3 -m venv .venv && .venv/bin/pip install --require-hashes -r scripts/requirements-dmg.txt
make mac
REFRACT_PYTHON=.venv/bin/python macos/build-dmg.sh build/mac/arm64/Refract.app build/mac/arm64/Refract-arm64.dmg
```

With `REFRACT_SIGN_IDENTITY` set to a Developer ID Application identity and the notarization
credentials of `notarize.sh`, `build-dmg.sh` also signs the image, notarizes it, staples the ticket
and runs `spctl --assess --type open`. Finder draws a title bar on a disk image window that can't be
removed, and with a background picture Finder draws the icon names in black in light and dark mode
alike, so the background is light (the landing page's paper) and needs no chip behind each name. The window is 32 pt taller than the
660 x 420 content (the title bar), and the background 20 pt taller than the content.

## Testing without touching real state

Environment variables of the app (not passed to the server) redirect what it writes outside the
data folder: `REFRACT_APP_LOG_DIR=<dir>` for the log folder and `REFRACT_APP_DEFAULTS_SUITE=<name>`
for a separate UserDefaults suite (the remembered port). The data folder is the server's: the app
never sets `REFRACT_DATA_DIR`, so to test the server with scratch data run the bundled binary directly,
`REFRACT_DATA_DIR=<dir> Refract.app/Contents/Resources/Server/bin/refract local --no-browser`.
Launch the app with them via `open --env` or `env ... Refract.app/Contents/MacOS/Refract`.

## Left for later

Links, downloads and file pickers (#67, `TODO(#67)` in `WebViewController`), the real menu (#68, the
current one is the minimum for Quit, Close and editing), updates (#70).
