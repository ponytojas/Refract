# Refract for macOS

A Swift app that runs `refract local --embedded` (the contract is in the top-level README,
"For packagers") and shows it in a `WKWebView`. The Go server stays the only backend.

```
macos/
  Package.swift            SwiftPM package: RefractCore (library), Refract (app), tests
  Sources/RefractCore/     the server's lifecycle, no UI: status line, supervisor, logs, bundle layout, update settings
  Sources/Refract/         AppKit: window, loading/error views, WKWebView, quit sequence, minimal menu
  Resources/               Info.plist, the English/Spanish strings and AppIcon.icns
  dmg/                     the disk image's window: settings.py, background.svg and its PNGs
  Tests/RefractCoreTests/  lifecycle tests against a fake server script
  build-app.sh             assembles Refract.app, then signs and verifies it (run by `make mac`)
  sign-app.sh              signs every Mach-O inside out, then the app (REFRACT_SIGN_IDENTITY)
  verify-app.sh            the check that fails on any unsigned or ad hoc Mach-O (--release)
  notarize.sh              notarizes, staples and assesses a signed app (`make mac-release`)
  build-dmg.sh             builds the disk image, and signs, notarizes and staples it with an identity
../scripts/make-appcast.py  the update feed of one architecture, signed (see "Updates")
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
three chevrons that strengthen towards Applications between the two icons, with faint orbit rings and soft corner glows behind them (nothing between the icons starts at the icon, so nothing has to line up with it). The text is Helvetica Neue,
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

## Updates

The app updates itself with [Sparkle](https://sparkle-project.org) 2. An update is the whole
signed app (the Swift app, the server and the bundled Git, Tectonic, SyncTeX and ripgrep), replaced
as one: never a piece of it. Projects, the database and the secret key live in
`~/Library/Application Support/Refract`, outside the bundle, and an update doesn't touch that folder.

**What the person sees.** `Refract → Check for Updates…`. Sparkle asks once, on the second launch,
whether to check automatically (`SUEnableAutomaticChecks` is deliberately not set in Info.plist, so
that the answer is the person's); the choice stays in its own dialog. A development build (no key,
see below) opens an explanation instead.

**What it does.** `Sources/Refract/Updater.swift`: `SPUStandardUpdaterController` with this class as
`SPUUpdaterDelegate`. Sparkle checks the feed, downloads the archive, verifies its EdDSA signature
against `SUPublicEDKey` **before extracting anything**, then replaces the app. Nothing is replaced if
any step fails, and the running app stays usable; the next check retries (an interrupted download
starts again, Sparkle keeps nothing half-installed). Before Sparkle relaunches the app,
`updater(_:shouldPostponeRelaunchForUpdate:untilInvokingBlock:)` runs the quit sequence's server part,
the same code as ⌘Q (`AppDelegate.stopServer`): `window.refractFlush()` (and the "couldn't save"
question if it answers false), then standard input closes and the app waits for the server, which
finishes its requests and closes the database. Only then is the installer released. "Install on
quit" and ⌘Q during an update go through `applicationShouldTerminate`, the same path. If the person
answers "Not Now" (the wording for an update; ⌘Q says "Don't Quit"), the update waits and the app goes
on, and Sparkle's relaunch is kept: Check for Updates… asks again and finishes it. If the update fails
after the server was stopped, the server is started again. A second ⌘Q while a quit is pending waits
for the same answer instead of asking twice.

**Decisions.**

- **Sparkle 2.10.1, exact** (`macos/Package.swift`, `Package.resolved`): the latest stable release that
  was more than two weeks old. SwiftPM compares the framework's zip with the checksum in Sparkle's own
  `Package.swift` at that tag (`4c2e48a3…022a`, which also matches the digest GitHub shows for the
  release asset), and `Package.resolved` pins the commit of the tag. Upgrading is a deliberate change of
  the version in `Package.swift` and in `scripts/make-appcast.py` (which downloads `sign_update` from the
  same release, checking its SHA-256).
- **No XPC services.** Sparkle's `Downloader.xpc` and `Installer.xpc` exist for sandboxed apps (a sandbox
  can't download or run the installer itself). Refract isn't sandboxed (see "No entitlements, no
  sandbox"), Info.plist doesn't set `SUEnableDownloaderService` or `SUEnableInstallerLauncherService`, so
  they would never run: `build-app.sh` removes them, which leaves less to sign and less to attack.
  What stays is `Autoupdate` and `Updater.app`, the helpers that replace the app after it quits.
- **Signing.** `sign-app.sh` follows Sparkle's instructions for a Developer ID: inside out and one by one,
  never `--deep`: `Autoupdate`, `Updater.app`, then `Sparkle.framework`, then (with the rest) the app, all
  with the same identity, the hardened runtime and a secure timestamp. `verify-app.sh` (and `--release`)
  checks the framework like every other Mach-O and also that it is there whole, that the XPC services
  are not, that `SUPublicEDKey` is a 32-byte key and `SUFeedURL` an https URL, and with `--release` that a
  key is present at all and that the feed can't be overridden (see "Testing updates without the real key").
- **Ad hoc builds get one entitlement.** The hardened runtime's library validation only loads libraries
  from the same team, and an ad hoc app has none, so an embedded `Sparkle.framework` can't load (checked:
  dyld says "different Team IDs"). `sign-app.sh` gives the app executable of an *ad hoc* build
  `com.apple.security.cs.disable-library-validation`; a real identity gets none, and `verify-app.sh
  --release` fails on any entitlement. A Developer ID build is checked by `--release` for the same team on
  every Mach-O instead.
- **One feed per architecture, on GitHub Releases.** Sparkle doesn't choose by architecture and the apps
  are per architecture (see "One app per architecture"), so each build is baked with its own feed,
  `https://github.com/Use-Refract/Refract/releases/latest/download/appcast-<arch>.xml` with `<arch>` as
  everywhere else in this repository (`arm64`, `amd64`). Releases rather than Pages: the feed is published
  by the same job that publishes the archive it points to (nothing to keep in step, no second deployment
  or token), `releases/latest/download/` is a stable URL, and the only cost is that a release flagged
  *pre-release* is not "latest", so desktop releases are published as full releases. A feed holds only the
  newest version, which is all a Sparkle client needs (it jumps straight to it). A prerelease tag
  (`v0.3.0-rc.1`) gets no appcast, and a build's version number is `0.3.0`, so it could not be told from
  the final one by Sparkle anyway.
- **Build variable.** `REFRACT_SPARKLE_PUBLIC_KEY` (the base64 public key) becomes `SUPublicEDKey`, with
  `SUFeedURL` and `SUVerifyUpdateBeforeExtraction`. `build-app.sh` refuses to build a release (`make mac-release`,
  which sets `REFRACT_RELEASE=1`, or any `Developer ID…` identity) when it is missing, and any build when it
  is not 32 bytes. A local build without it (`make mac`: ad hoc, Apple Development or self-signed) has updates **off**: no
  Sparkle updater starts, and the menu item says so instead of failing at the first check
  (`UpdateSettings`, tested in `Tests/RefractCoreTests/UpdateSettingsTests.swift`).
  `REFRACT_UPDATE_FEED_BASE` changes the directory of the feed (tests).

### Data safety on an update

The server, not the app, protects the database. When a newer server finds an older database, it
applies the migrations in one transaction and, before that, writes a consistent copy of the database
(`VACUUM INTO`, taken while holding the write lock) next to it as `refract.db.pre-upgrade`, replacing
the one from the previous upgrade; the copy is in the data folder, outside the app bundle, so the update
doesn't replace it. If a migration fails, the transaction is rolled back (the database is exactly as it
was) and the server refuses to start with a message that names the copy
(`migration 14: …. Your data was not changed: the database is still at schema version 13. A copy from
before the upgrade is at …/refract.db.pre-upgrade …`); the app shows that message in its error view with
the log, and nothing is deleted. A server that finds a database *newer* than it knows (the app was
downgraded) refuses to open it: "a newer Refract wrote it. Run that version or a later one, or restore a
backup from before the upgrade".

**Rollback.** Going back to an older app is unsupported, and Sparkle never does it. If the database
schema didn't change between the two versions, the older app opens the data as it is. If it did, the
older server refuses (above), and the supported way back is to restore the copy: quit the app, move
`refract.db`, `refract.db-wal` and `refract.db-shm` away, copy `refract.db.pre-upgrade` to `refract.db`,
install the older version. That loses what was done since the upgrade, so it is a manual step that says
so (README, "Data, backups and upgrades"), never automatic.

### Maintainer: create the update key

Done once, before the first release that can be updated. The key pair is Ed25519, made with Sparkle's
own tool; whoever holds the private half can publish updates every installed app trusts, and if it is
lost no installed app can be updated any more (Sparkle can't rotate it without the old one), so keep a
copy in a password manager.

1. Download Sparkle's tools: `Sparkle-2.10.1.tar.xz` from
   <https://github.com/sparkle-project/Sparkle/releases/tag/2.10.1> (SHA-256
   `1a856d2a9edb8e628de88121d7be04cf0e2167f8641650b78fca00b14244cf27`), and unpack it.
2. `./bin/generate_keys` creates the key in your login keychain (it asks for permission) and prints
   the **public** key: a 44-character base64 string. `./bin/generate_keys -p` prints it again.
3. Export the private key to a file only you can read, give it to CI, and delete the file:

   ```sh
   ./bin/generate_keys -x sparkle_private_key
   gh secret set SPARKLE_ED_PRIVATE_KEY < sparkle_private_key
   gh variable set REFRACT_SPARKLE_PUBLIC_KEY --body 'THE-PUBLIC-KEY-FROM-STEP-2'
   rm sparkle_private_key
   ```

   The secret is read only by the "Appcast" step of `macos-app.yml`, which hands it to `sign_update` on
   its standard input (`scripts/make-appcast.py`); it is never in an argument, a file or a log. The
   public key is not secret (it ships in every app), so it is a repository variable.
4. A local release needs the public key too:
   `REFRACT_SPARKLE_PUBLIC_KEY=... REFRACT_SIGN_IDENTITY=... REFRACT_NOTARY_PROFILE=... make mac-release`.

The release workflow calls `macos-app.yml` (#71), which for each architecture builds, signs, notarizes,
staples and zips the app, then runs `scripts/make-appcast.py` on that zip and uploads
`appcast-<arch>` next to the zip. `make-appcast.py` checks, before writing anything, that the app in
the zip has the tag's version, a valid `SUPublicEDKey` and the feed URL of that architecture, and that
the signature it just made verifies against the app's public key (a secret that isn't the private half
of the variable fails the release instead of every installed app); the notes are the version's
`CHANGELOG.md` section, which must exist. `release.yml` stops a release whose appcasts point anywhere
other than at the archives it attaches. Attach `appcast-arm64.xml`, `appcast-amd64.xml` and the zips
to the **same** full release.

### Testing updates without the real key

Make a throwaway key (never in a keychain or the repository), build two versions with its public key
and an ad hoc signature, give the old one its own bundle id, serve an appcast from loopback and point the
app at it:

```sh
openssl genpkey -algorithm ed25519 -outform DER -out k.der
tail -c 32 k.der | base64 | tr -d '\n' > seed.txt                                    # the private key file
openssl pkey -inform DER -in k.der -pubout -outform DER | tail -c 32 | base64 > pub.txt   # SUPublicEDKey
REFRACT_SPARKLE_PUBLIC_KEY=$(cat pub.txt) macos/build-app.sh arm64 v0.0.1 out1   # and v0.0.2 into out2
ditto -c -k --norsrc --noextattr --noqtn --keepParent out2/Refract.app Refract-arm64.zip
SPARKLE_ED_PRIVATE_KEY=$(cat seed.txt) scripts/make-appcast.py --version v0.0.2 --arch arm64 \
  --archive Refract-arm64.zip --output appcast-arm64.xml --notes-file notes.md \
  --download-prefix http://127.0.0.1:8099 --sign-update /path/to/Sparkle/bin/sign_update
python3 -m http.server 8099 --bind 127.0.0.1 &
REFRACT_APP_UPDATE_FEED=http://127.0.0.1:8099/appcast-arm64.xml REFRACT_APP_LOG_DIR=... out1/Refract.app/Contents/MacOS/Refract
```

`REFRACT_APP_UPDATE_FEED` (not passed to the server) replaces the feed for the run; only https and
loopback http are accepted, and it can't weaken the check, since the archive still has to be signed by
the key baked into the app. Only ad hoc builds honour it: `build-app.sh` adds
`RefractAllowsUpdateFeedOverride` to their Info.plist, a release never has it (`verify-app.sh --release`
fails on it), so a release always reads its own https feed. Run the app with a scratch `HOME` (the data folder is under it), a
`secret.key` made beforehand in `$HOME/Library/Application Support/Refract` (otherwise the key goes into
the real login keychain), a bundle id of its own (`PlistBuddy` the copy, then `sign-app.sh`) so its
preferences and caches are separate, `REFRACT_APP_LOG_DIR` and `REFRACT_APP_DEFAULTS_SUITE`. Setting
`SUEnableAutomaticChecks` and `SUAutomaticallyUpdate` in that bundle id's defaults makes Sparkle check
and install on quit with no window to click, so the run can be scripted with `osascript -e 'tell
application id "..." to quit'`.

**Install and Relaunch loses that isolation.** Sparkle relaunches the new version through Launch
Services, which starts it without the test environment: the relaunched app has the real `HOME`, so
its server opens the real data folder and it logs to the real log folder. Install on quit doesn't
relaunch, so it is safe to script; test the relaunch path in a separate macOS user account.

## Testing without touching real state

Environment variables of the app (not passed to the server) redirect what it writes outside the
data folder: `REFRACT_APP_LOG_DIR=<dir>` for the log folder and `REFRACT_APP_DEFAULTS_SUITE=<name>`
for a separate UserDefaults suite (the remembered port). The data folder is the server's: the app
never sets `REFRACT_DATA_DIR`, so to test the server with scratch data run the bundled binary directly,
`REFRACT_DATA_DIR=<dir> Refract.app/Contents/Resources/Server/bin/refract local --no-browser`.
Launch the app with them via `open --env` or `env ... Refract.app/Contents/MacOS/Refract`.

## The integrated window (#127)

The window has no title bar of its own (`.fullSizeContentView`, transparent title bar, hidden
title; the title stays for Mission Control, the Window menu and VoiceOver): the web app reaches the
top edge. **The app moves the traffic lights onto the bars' line, and the bars share one height.**
Every bar is 44 px tall and starts 8 px below the top edge (the page's padding), so its content row
is centred 30.5 pt down; `TrafficLights` (`RefractCore/WindowChrome.swift`) puts the buttons there,
18 pt from the left, which is inside the card on the Projects page and in the sidebar's grey on the
Workspace, so the lights never straddle an edge and header content and lights read as one row.
The alternative (a grey strip above each card) would add a dead band of 30 px to every screen.

- **Placing.** AppKit puts the buttons in a 28 pt title bar container; the app grows that container
  to 61 pt and moves the three buttons (`RootViewController.placeTrafficLights`). AppKit resets
  both on resize, full screen, appearance and screen changes, so the app places them again on those
  notifications and when the container's frame changes (the frame is written only when it differs).
- **Inset.** `WindowInset` measures where the zoom button ends. A `WKUserScript` at document start,
  and `evaluateJavaScript` when the window enters or leaves full screen, set `html[data-desktop="mac"]`
  and `--window-inset-left` / `--window-inset-top` (both 0 in full screen, where the buttons hide).
  Nothing sets them in a browser, so `index.css` rules under `[data-desktop]` never apply there. The
  first element of each `[data-window-bar]` clears the inset (buttons end + 12 pt). In the app the
  Workspace sidebar starts at 296 px (not 248) so the project name still fits next to the lights.
  The loading and error views sit in the safe area.
- **Dragging.** WKWebView has no `-webkit-app-region`. `web/src/lib/windowDrag.ts` posts
  `{type: "drag"}` to the `refractWindow` message handler on a mousedown on `[data-window-drag]`
  (not on buttons, links, inputs, tabs, `[data-no-drag]`); the app calls `performDrag(with:)`, and a
  double click follows `AppleActionOnDoubleClick`. Messages count only from the main frame of the
  app's origin (`WindowMessage.parse`). The handler exists only in the app.
- **Drag strip.** Screens without a top bar (the sign-in pages) and the overlay behind a dialog get
  a `.window-strip` element at the top (shown by one rule in `index.css`; delete the rule to go
  without it).

## Links, downloads, file pickers (#67)

What the web view does with things the page asks for (`WebViewController`; the pure decisions are in
`RefractCore/LinkPolicy.swift`, tested):

- **Links.** The server's own origin (same scheme, host and port, plus `about:blank`/`about:srcdoc`, `data:` and that origin's `blob:` URLs) stays in the
  app. `http`, `https` and `mailto` elsewhere open in the default browser through `NSWorkspace`, with the bare
  URL: the token cookie never leaves the web view. Any other scheme (`file:`, `javascript:`, custom schemes)
  is refused and logged, and so is another origin's `blob:`. Only the main frame opens the browser; a frame inside the page can't. `target="_blank"` and `window.open` never create a window: same-origin pages load in
  the main view (a `blob:`/`data:` one is ignored, it would replace the app), the rest follows the same rule. Sign-in pages (provider keys, GitHub's device flow, OIDC)
  therefore open in the browser.
- **Downloads** (Export .zip, Download PDF, a file's download). `WKDownload` for `shouldPerformDownload`
  (the web app's `a[download]` with a `blob:` URL works as it is, no web hook needed), for
  `Content-Disposition: attachment` and for responses the view can't show. A save panel opens as a sheet in
  Downloads with the suggested name (stripped of path parts, control and bidi characters and leading dots, cut to 255 bytes keeping the extension); a failure shows an alert; cancelling is silent.
- **File inputs.** `runOpenPanelWith`: multiple selection as asked; a directory input (`webkitdirectory`)
  gets a folder-only panel, the others a file-only one.
- **Drag and drop** onto the project list. WKWebView hands drops to the page. The project list now reads
  dropped folders through `webkitGetAsEntry` (`web/src/lib/dropfiles.ts`) and uploads them like the Folder
  picker; zips work as before.
- **`window.confirm`** (and `alert`/`prompt`, unused today) has a native sheet (without a delegate WebKit answers false, which would break the
  "leave this page" prompt).
- **Clipboard.** `navigator.clipboard.writeText` works from the loopback origin (a secure context). Reading
  (`readText`, the editor's Paste menu entry) shows WebKit's own "Paste" prompt.

Signing in to ChatGPT inside the app is not done here: today Settings → Assistant asks the person to run
`refract login` and upload the credentials file (the file picker works). The one-click flow from Settings
needs the server side of #61; its sign-in page then opens in the browser by the rule above.

## Left for later

The real menu (#68, the current one is the minimum for Quit, Close and editing; it has Check for Updates…)
and the release job (#71).
