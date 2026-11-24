# Changelog

Notable changes to Refract. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- Refract can read its encryption key, OpenID Connect client secret, SMTP URL and OpenAlex API key from files using the corresponding `REFRACT_*_FILE` variables. `refract users` and `refract local` also accept the encryption key from a file, and the systemd unit starts Refract directly with a systemd credential.
- `REFRACT_OUTBOUND` (`any` by default, or `public`) chooses whether AI provider base URLs and git remotes may reach loopback and private networks. `public` does what `REFRACT_MODE=saas` did for outbound requests, and checks at startup that git is 2.37 or later. With `public`, provider requests and redirects must be https, and link-local git remotes are refused in every mode. `REFRACT_MODE=saas` no longer changes it.
- Refract finds `tectonic`, `synctex`, `rg` and `git` in a bundled `libexec/refract` folder next to the executable (the executable's own folder on Windows) before the `PATH`, and runs them by absolute path; a bundled Git uses only its own helpers and templates. `REFRACT_TECTONIC_BIN`, `REFRACT_SYNCTEX_BIN`, `REFRACT_RG_BIN` and `REFRACT_GIT_BIN` override the lookup. The startup log shows where each was found, and the web app shows one notice when a tool is missing, saying what won't work, on whichever page opens first. On macOS and Linux a bundled tool in a folder that others can write, or that another user owns, is ignored. The same checks cover bundled Git's helpers and templates.
- `refract local` runs once per data folder: a second start opens the running one (or waits while it starts) instead of starting another, and `refract local --status` prints its state as one JSON line for programs that wrap Refract.
- `refract local --embedded` for programs that embed Refract (the desktop app): no browser, a free loopback port, one JSON line on standard output when ready (or when another instance runs, or when starting fails), a per-launch token (`--token-fd` or `REFRACT_EMBED_TOKEN`, never logged and removed from the environment) required as the `refract_embed` cookie on every request except `/healthz`, and a graceful stop when standard input closes. `local.lock`, `--status` and the `running` line say `"embedded":true` for such an instance, and a terminal `refract local` that finds one doesn't open the browser on it. See "For packagers" in the README.
- `refract local --status` prints a JSON `error` line when it fails, instead of nothing.
- `window.refractFlush()` in the web app saves pending edits and tells whether everything was saved, for the desktop app to call before it stops the server.
- The macOS app updates itself with Sparkle 2 (**Refract → Check for Updates…**, automatic checks when the person agrees), from a feed per architecture published with the release and signed with an EdDSA key. Before it relaunches, the server stops the way it does on quit. A failed or interrupted download, or an update that doesn't verify, leaves the running app as it is. `scripts/make-appcast.py` writes the feed from the release's CHANGELOG section.

### Changed

- Sign in with ChatGPT is stricter: the sign-in address no longer carries a token, token responses and refreshed ID tokens are verified (a rotation cut short by a restart or a key-server outage resumes without a new login), the local callback ignores stray or malformed requests, a failed code exchange reuses the issued registration, sign-out retries revocation once, and ChatGPT requests don't follow redirects or retry a usage limit.
- A failed database upgrade now says that nothing was changed and where the copy from before the upgrade is (`refract.db.pre-upgrade`).
- `refract local` keeps its secret key in the system credential store (macOS Keychain, Secret Service on Linux, Windows Credential Manager), one item per data folder, and falls back to an owner-only `secret.key` in the data folder when no store can be reached, saying so in the log. An existing `secret.key` is kept. If the key is gone from the store for a data folder that was used before, Refract logs that stored credentials can't be read instead of failing.

### Fixed

- A compile that produces the PDF despite TeX errors now saves a version in the history too. Documents imported from Prism often keep a recoverable error on every build, and until now their history never grew.

- `refract local` compiles its first PDF offline: the tools ship a TeX seed (the LaTeX files the built-in templates in English and Spanish, a plain article and a common set of packages need), which `refract local` verifies and copies into `tectonic-cache/` in the data folder at startup and again if the cache is emptied. Updates refresh only changed files and never touch projects, the database or the key; the installation can be read-only. `REFRACT_TEX_SEED` chooses another seed or `none`; the seed is skipped when `REFRACT_TECTONIC_BUNDLE` names another bundle or the compile sandbox is on. A document that needs a package that isn't cached says so when offline and keeps your text. Closes #92.
- Upgrading `refract local`: it now compiles with the seed's pinned bundle (TeX Live 2022) in `tectonic-cache/` in the data folder, no longer with Tectonic's default bundle in Tectonic's own cache folder. Packages downloaded before belong to another bundle, so they can't be reused: a document that needs one downloads it again (or says "Needs a download" offline). Tectonic's old cache is left untouched and the log names it; to keep using it, set `TECTONIC_CACHE_DIR` and `REFRACT_TECTONIC_BUNDLE` yourself, or `REFRACT_TEX_SEED=none`.

## [0.2.0] - 2026-11-20

### Added

- `refract local` runs Refract on your own computer with no configuration: data in the per-user data folder (`REFRACT_DATA_DIR` still overrides it), a secret key created on first run as an owner-only `secret.key` in that folder and reused after (`REFRACT_SECRET_KEY` takes precedence), no sign-in, listening on loopback only (127.0.0.1:8080 by default, or a free port when it is taken; `--port`, or `REFRACT_ADDR` on 127.0.0.1 or ::1, chooses another), and it opens the browser once the server answers (`--no-browser` to skip). Logs are readable lines on a terminal.
- Citation checks scoped to the selected main document, its included sources and loaded bibliographies, for missing keys, duplicate or incomplete bibliography entries, unused entries and optional online DOI verification; source metadata corrections from doi.org/arXiv are shown as editor proposals, with Spanish translations.
- A word count for the current source or PDF selection (toggle **Word count** in the account menu) and word limits for sections, so you can keep each one within its budget.
- `REFRACT_PROFILE=team` for people who must not read each other's files: it requires sign-in, runs every compile in the compile sandbox and refuses to start, saying what is missing, when it can't isolate them. The default, `REFRACT_PROFILE=personal`, warns at startup when several accounts compile without isolation.
- Document and hierarchical section word counts in the outline, following included files and the existing counter/title preferences.
- PDF search with highlighted matches, previous/next navigation, keyboard controls and parallel page indexing. Reading and zooming preserve your position, recompiling keeps the selected match number, and search buttons work with the keyboard.
- Return links beside cited PDF bibliography entries, with a choice of occurrences for references cited multiple times.
- Built-in templates offer English and Spanish, defaulting to the interface language, and generate separate section files assembled by `main.tex`, with folder and section filenames in the selected language.
- Project templates: styled article, thesis, widescreen Beamer presentation, letter and one-page CV starters with editable metadata, structured sample content and examples of tables and references; save private templates from your projects and reuse independent copies with their selected main file.
- LaTeX completion in the editor: common commands and environments (`\begin{…}` closes itself), `\ref` and friends from the project's labels, `\cite` from its `.bib` files, and file paths in `\input`, `\include` and `\includegraphics`. Commands and environments show their arguments and a short description, the arguments stay in view while filling them in, and citations show the author, year and title of each work.
- `REFRACT_MODE=selfhost|saas` says whether Refract runs as a self-hosted install (the default) or as the hosted service. The hosted service doesn't offer the ChatGPT plan: Settings → Assistant shows API providers only.
- A document outline under the file tree: the sections of the main file and the files it `\input`s or `\include`s, indented by level. Click a heading to jump to it; the one containing the cursor is highlighted, and it refreshes on save. Headings are also in the command palette (<kbd>⌘K</kbd>, **Go to section…**).
- Project instructions: a `REFRACT.md` at the project root that the assistant and quick actions always follow (style, spelling, notation, what not to touch). Open or create it from the project menu; ask the assistant to remember something and it proposes an edit to it.
- Before an upgrade changes the database, Refract keeps a copy of it as `refract.db.pre-upgrade`, and an older Refract refuses to open a database a newer version has upgraded.
- Refract records the tokens each model call uses, per account and model (never the text), and shows this month's total in the account menu (per model on hover) and in **Settings → Assistant → Token usage**.
- Deleting a project moves it to the trash for 30 days instead of removing it at once. Undo from the notification, or restore or delete forever from the new Trash page; projects older than 30 days in the trash are deleted for good.
- A project open in the workspace when it goes to the trash (from another tab or device) becomes read-only with a banner and a Restore button: unsaved edits are kept and saved on restore, and if the project was deleted for good meanwhile, you can copy the unsaved text.
- Accounts can be created and managed by their owners. With `REFRACT_AUTH=builtin`, a mail server (`REFRACT_SMTP_URL`, `REFRACT_MAIL_FROM`) and `REFRACT_PUBLIC_URL`, `REFRACT_SIGNUP=open` adds a sign-up page; there is a confirmation email for new addresses, a "Forgot your password?" reset by email, and **Settings → Account** to confirm the address, choose the language of the emails, change the password and delete the account with everything in it, projects in the trash included. Links in emails work once, expire after 24 hours (confirming) or 1 hour (resetting) and are plain text, in English or Spanish.
- `REFRACT_AUTH=builtin` is the new name of `REFRACT_AUTH=password`, which keeps working.
- `REFRACT_SANDBOX=bwrap` runs every compile in a [bubblewrap](https://github.com/containers/bubblewrap) sandbox: no network, a read-only view of the system and of the project being built, and limits on CPU, memory, processes and file size. It needs the TeX bundle as a local folder: `packaging/server/prepare-bundle.sh` prepares it, and `packaging/server/refract.service` is a systemd unit for the hosted service. Refract checks it at startup by compiling a small document.
- `REFRACT_COMPILE_WORKERS` and `REFRACT_COMPILE_MAX_WAIT` set how many compiles run at once and how long one waits for its turn.
- Sticky scroll in the editor: the enclosing LaTeX sections and environments stay at the top while you scroll, drawn like their own lines and pushed out smoothly as the next one arrives. On by default; turn it off in **Settings → Editor**.
- A **Download PDF** button in the preview's header.
- Settings → Account lists the browsers signed in to the account (device, last activity, address) and signs out one of them or all the others. Sessions now also end after 7 days without use, besides 30 days after signing in, and over https the cookie is named `__Host-refract_session`; the upgrade itself signs nobody out (sessions keep their original 30-day end).

### Changed

- Refract listens on `127.0.0.1:8080` by default, so a server started without `REFRACT_ADDR` is reachable from this computer only. Set `REFRACT_ADDR=:8080` to listen on every interface; the container image already does, so there `-p` decides the mapping on the Docker host, and containers on a shared Docker network can still connect.
- **Compile** and its main file menu moved from the editor's tab row to the preview's header, beside the compile status.
- Changing the password moved from Settings → General to the new Settings → Account.
- Until an address is confirmed, an account that signed up can sign in but can't create or import projects. Existing accounts, and those added with `refract users add`, count as confirmed.
- With `REFRACT_MODE=saas` and `REFRACT_AUTH=builtin`, `REFRACT_SIGNUP=open` is refused until the terms and privacy policy people accept at sign-up exist: keep the default (`closed`) and add accounts with `refract users add`.
- Each account compiles one project at a time, in every mode: with `REFRACT_AUTH=none` everyone is the same account, so compiles of different projects now run one after another. When the server is busy, a compile waits up to `REFRACT_COMPILE_MAX_WAIT` and then answers "the server is busy, try again in a moment" (the assistant is told to try again).

### Fixed

- PDF contents, citations and cross-references now navigate to their destination in the preview; website and email links are clickable too.
- Citations added from Refract go to the bibliography next to the main file when it is in a subfolder, so they reach the PDF (#13).
- When no file loads a bibliography yet, the setup hint after citing (in the editor and from the assistant) names the `.bib` relative to the main file's folder, as `\bibliography` resolves it.
- Citing ignores a commented-out `\bibliography` or `\addbibresource`, uses the first usable name of a list such as `\bibliography{a,b}`, and drops spaces from `\bibliography` names as LaTeX does (`{my refs}` is `myrefs.bib`).
- When no file loads a bibliography yet and the main file is in a subfolder, citations go to a `.bib` in that folder if there is one, before any other `.bib` in the project.
- Stopping the server waits for requests in flight (up to 10 s) and for auto-pushes in progress, and lets running assistant turns record that they were stopped, instead of closing the database under them. Give Docker time for it with `docker run --stop-timeout 30`.
- Starting Refract for the first time while a `refract users` command runs no longer fails with "database is locked".
- Compiling, accepting assistant changes, restoring history, pulling and auto-push no longer fail at random when they touch the same project at the same moment, and a turn can't be accepted and rejected at once.

### Security

- Downloading the PDF or the log no longer follows symbolic links: a project whose build folder, or an output in it, was a link (a pulled repository can contain one) could serve a file from outside the project. Downloads now open them like the compiler does: no part of `.refract/build` may be a link or junction, the output must be a regular file, and anything else answers "not built yet". On Unix each part is opened relative to the one before without following links. On Windows each part is opened relative to the handle of the one before and can't resolve outside it, so a part swapped for a link after its check still can't lead outside the project. CI runs these tests on Windows.
- Refract answers only to the host names it expects and refuses others with 421 Misdirected Request, so a website can't reach a local Refract through DNS rebinding. `localhost`, the loopback addresses and the host of `REFRACT_PUBLIC_URL` are always allowed; `REFRACT_ALLOWED_HOSTS` adds more. A server listening on an address other computers can reach with neither setting still accepts any name, with a warning in the log. Changes requested with another site's `Origin`, or with a malformed or repeated one, are refused with 403. `REFRACT_AUTH=none` on an address other computers can reach (judged by the address actually bound) now refuses to start unless allowed hosts are set (the Docker quick start adds `REFRACT_ALLOWED_HOSTS=localhost`); an invalid `REFRACT_PUBLIC_URL` now stops startup; `make dev` listens on `127.0.0.1`. Behind a proxy that rewrites `Host`, set `REFRACT_PUBLIC_URL`.
- OpenID Connect refuses an email the provider reports as not verified. For an account made by a sign-up nobody confirmed, the provider must also report it as verified; then it takes the account over, and everything the sign-up attached goes: password, sessions, links, AI provider keys, ChatGPT and GitHub connections, usage and projects, so someone who registered another person's address can't keep anything. An account that has a password can only be signed in to by a provider that reports `email_verified` as true, so providers that leave the claim out (some Microsoft Entra setups) can't sign in to it; binding identities to issuer and subject is planned. Resetting the password or confirming through a link on a never-confirmed account does the same clean-up.
- A person's pending password-reset and confirmation links are no longer cancelled when someone else asks for a new one (the newest 3 of each kind stay valid), and once an address has had its 3 emails in 15 minutes, further requests answer as usual but send nothing, so the answer says nothing about the address. Known limit: someone who keeps requesting emails for an address can still keep its owner from receiving a new link until the window passes (the owner's links already sent keep working); the 20 per network address limit and `refract users unlock <email>` are the tools against it.
- Sign-up, password reset and resending a confirmation do their account work after answering, so the request takes the same path for addresses with and without an account.
- With `REFRACT_MODE=saas`, Refract refuses to start without `REFRACT_SANDBOX=bwrap`, or if `REFRACT_COMPILE_MAX_WAIT` plus `REFRACT_COMPILE_TIMEOUT` is over 95 s.
- Every API route that touches a project, chat, turn or file goes through one authorization check, declared with the route; without a signed-in user nothing is reachable. Auto-push and assistant turns check again, as the user who started them, before they write. Projects left without an owner after the first account was created stay unreachable.
- A saved git token is only sent to the host and port it was saved for (changing the remote to another host asks for a new one), the GitHub account's token only to GitHub, and git follows no redirect while sending a token: a renamed or moved repository needs its new URL. Git network commands run without the server's `GIT_*` environment, and a project whose own git configuration (worktree and included files too) has `url`, `http`, `include` or `credential` settings is refused.
- Release images are pushed by digest, scanned for vulnerabilities (Syft and Grype) and only then tagged; a Critical finding without a reviewed exception stops the release. Each release carries the scan report and SBOMs.
- A failed request to OpenAlex no longer includes the server's OpenAlex key in the error shown to the user or sent to the assistant's model.
- Built with Go 1.27.4, which fixes a crash of the HTTP server in Go's `net/http` (GO-2026-6859).
- With `REFRACT_MODE=saas`, Refract refuses to start without sign-in (`REFRACT_AUTH=none`) or without an https `REFRACT_PUBLIC_URL`.
- With `REFRACT_MODE=saas`, AI providers and git remotes can only reach public addresses: loopback, private and metadata addresses are refused at every connection, Ollama isn't offered, base URLs must be https, and git connects only to the addresses it checked, without following redirects.

## [0.1.2] - 2026-10-27

### Added

- Install the CLI on macOS with `brew install ponytojas/tap/refract`.
- Debian/Ubuntu CLI packages for amd64 and arm64, with no server service or LaTeX dependencies.
- Homebrew formula and WinGet manifests generated from each release's archives and checksums.
- CLI smoke tests on macOS, Linux and Windows before publishing release assets, plus packaging tests in CI.
- Optional automatic Homebrew tap updates for stable releases.

## [0.1.1] - 2026-10-27

### Fixed

- The Docker image is published. The release check for 0.1.0 asked SyncTeX for a relative file name, failed, and stopped that version's image before it reached the registry. The application is the same as in 0.1.0.

## [0.1.0] - 2026-10-27

The first release: a self-hosted LaTeX workspace with an AI assistant.

### Editor and PDF

- LaTeX editor with tabs, autosave and syntax highlighting.
- Compilation with Tectonic, which fetches the packages a document needs; errors and warnings listed with file and line, each with **Fix with AI**.
- Named `fontspec` families (`\setmainfont{Open Sans}`) work without changing the document: Refract uses the project's own font files, the TeX bundle's Latin Modern and TeX Gyre, or downloads the family from Google Fonts (`REFRACT_FONT_DOWNLOADS=0` turns downloads off). By [Lorena Jimenez](https://github.com/Lorena-J) in [#1](https://github.com/ponytojas/Refract/pull/1).
- PDF preview with SyncTeX in both directions (<kbd>⌘J</kbd>, and <kbd>⌘</kbd>-click in the PDF), and a right-click menu in the PDF.
- Command palette (<kbd>⌘K</kbd>) and a shortcut overlay.
- English and Spanish interface, light and dark themes, installable as an app from the browser.
- Smooth transitions between pages, without reloads.

### Assistant

- An agent that reads, searches, edits and compiles the project in a separate git worktree, and hands back its changes as a review: accept or reject all at once or file by file.
- Shows the model's reasoning when the model provides it; each chat remembers its model and reasoning level.
- Quick actions on a selection (improve, shorten, fix grammar, table, explain, free instruction) shown as inline word diffs.

### References

- Finds published papers (OpenAlex) and arXiv preprints that support a passage, and places `\cite` as a change to review.
- Bibliography entries always come from the publisher's or arXiv's metadata.

### Projects, history and sync

- Import Prism exports (.zip) and folders; export any project as a .zip.
- Every project is a git repository: history with diffs, and restoring a file or the whole project.
- Push and pull to any https git remote, or to GitHub repositories picked or created from Refract; optional auto-push.

### AI models

- Your ChatGPT plan through "Sign in with ChatGPT" (`refract login`).
- API keys for OpenAI, Anthropic, Google Gemini, OpenRouter, Mistral, DeepSeek, xAI and Groq; local models through Ollama; any OpenAI-compatible endpoint.
- One model picker for all of them, grouped by provider, with search and the reasoning levels each model supports.

### Accounts and security

- Several users, each with their own projects, chats and connections.
- Sign-in through Cloudflare Access, Refract's own sign-in page (passwords, and optionally OpenID Connect), or no sign-in on a private network.
- `refract users` to add, rename, sign out, unlock and remove accounts.
- Sign-in attempts limited per account and per address; credentials and API keys encrypted at rest; a strict Content-Security-Policy.

### Distribution

- `refract` binaries for macOS, Linux and Windows (amd64 and arm64).
- A Docker image for amd64 and arm64 on GitHub Container Registry, with Tectonic and SyncTeX included.

[Unreleased]: https://github.com/ponytojas/Refract/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/ponytojas/Refract/compare/v0.1.2...v0.2.0
[0.1.2]: https://github.com/ponytojas/Refract/releases/tag/v0.1.2
[0.1.1]: https://github.com/ponytojas/Refract/releases/tag/v0.1.1
[0.1.0]: https://github.com/ponytojas/Refract/releases/tag/v0.1.0
