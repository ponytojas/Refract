# Changelog

Notable changes to Refract. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- Document and hierarchical section word counts in the outline, following included files and the existing counter/title preferences.
- PDF search with highlighted matches, previous/next navigation, keyboard controls and parallel page indexing. Reading and zooming preserve your position, recompiling keeps the selected match number, and search buttons work with the keyboard.
- LaTeX completion in the editor: common commands and environments (`\begin{…}` closes itself), `\ref` and friends from the project's labels, `\cite` from its `.bib` files, and file paths in `\input`, `\include` and `\includegraphics`. Commands and environments show their arguments and a short description, the arguments stay in view while filling them in, and citations show the author, year and title of each work.
- `REFRACT_MODE=selfhost|saas` says whether Refract runs as a self-hosted install (the default) or as the hosted service. The hosted service doesn't offer the ChatGPT plan: Settings → Assistant shows API providers only.
- A document outline under the file tree: the sections of the main file and the files it `\input`s or `\include`s, indented by level. Click a heading to jump to it; the one containing the cursor is highlighted, and it refreshes on save. Headings are also in the command palette (<kbd>⌘K</kbd>, **Go to section…**).
- Project instructions: a `REFRACT.md` at the project root that the assistant and quick actions always follow (style, spelling, notation, what not to touch). Open or create it from the project menu; ask the assistant to remember something and it proposes an edit to it.
- Before an upgrade changes the database, Refract keeps a copy of it as `refract.db.pre-upgrade`, and an older Refract refuses to open a database a newer version has upgraded.
- Refract records the tokens each model call uses, per account and model (never the text), and shows this month's total in the account menu (per model on hover) and in **Settings → Assistant → Token usage**.

- `REFRACT_SANDBOX=bwrap` runs every compile in a [bubblewrap](https://github.com/containers/bubblewrap) sandbox: no network, a read-only view of the system and of the project being built, and limits on CPU, memory, processes and file size. It needs the TeX bundle as a local folder: `packaging/server/prepare-bundle.sh` prepares it, and `packaging/server/refract.service` is a systemd unit for the hosted service. Refract checks it at startup by compiling a small document.
- `REFRACT_COMPILE_WORKERS` and `REFRACT_COMPILE_MAX_WAIT` set how many compiles run at once and how long one waits for its turn.

### Changed

- Each account compiles one project at a time, in every mode: with `REFRACT_AUTH=none` everyone is the same account, so compiles of different projects now run one after another. When the server is busy, a compile waits up to `REFRACT_COMPILE_MAX_WAIT` and then answers "the server is busy, try again in a moment" (the assistant is told to try again).

### Fixed

- Citations added from Refract go to the bibliography next to the main file when it is in a subfolder, so they reach the PDF (#13).
- When no file loads a bibliography yet, the setup hint after citing (in the editor and from the assistant) names the `.bib` relative to the main file's folder, as `\bibliography` resolves it.
- Citing ignores a commented-out `\bibliography` or `\addbibresource`, uses the first usable name of a list such as `\bibliography{a,b}`, and drops spaces from `\bibliography` names as LaTeX does (`{my refs}` is `myrefs.bib`).
- When no file loads a bibliography yet and the main file is in a subfolder, citations go to a `.bib` in that folder if there is one, before any other `.bib` in the project.
- Stopping the server waits for requests in flight (up to 10 s) and for auto-pushes in progress, and lets running assistant turns record that they were stopped, instead of closing the database under them. Give Docker time for it with `docker run --stop-timeout 30`.
- Starting Refract for the first time while a `refract users` command runs no longer fails with "database is locked".
- Compiling, accepting assistant changes, restoring history, pulling and auto-push no longer fail at random when they touch the same project at the same moment, and a turn can't be accepted and rejected at once.

### Security

- With `REFRACT_MODE=saas`, Refract refuses to start without `REFRACT_SANDBOX=bwrap`, or if `REFRACT_COMPILE_MAX_WAIT` plus `REFRACT_COMPILE_TIMEOUT` is over 95 s.
- Every API route that touches a project, chat, turn or file goes through one authorization check, declared with the route; without a signed-in user nothing is reachable. Auto-push and assistant turns check again, as the user who started them, before they write. Projects left without an owner after the first account was created stay unreachable.
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

[Unreleased]: https://github.com/ponytojas/Refract/compare/v0.1.2...HEAD
[0.1.2]: https://github.com/ponytojas/Refract/releases/tag/v0.1.2
[0.1.1]: https://github.com/ponytojas/Refract/releases/tag/v0.1.1
[0.1.0]: https://github.com/ponytojas/Refract/releases/tag/v0.1.0
