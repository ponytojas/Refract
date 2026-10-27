# Changelog

Notable changes to Refract. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow [Semantic Versioning](https://semver.org/).

## [Unreleased]

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
