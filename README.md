<p align="center"><img src="assets/logo.svg" width="88" alt=""></p>

<h1 align="center">Refract</h1>

<p align="center">
A self-hosted LaTeX workspace with an AI assistant that proposes changes you review as diffs.<br>
Edit, compile and read the PDF side by side, cite real papers, and keep every version in git, on your own server.
</p>

<p align="center">
<a href="https://github.com/Use-Refract/Refract/releases/latest"><img src="https://img.shields.io/github/v/release/Use-Refract/Refract" alt="Latest release"></a>
<a href="https://github.com/Use-Refract/Refract/pkgs/container/refract"><img src="https://img.shields.io/badge/docker-ghcr.io%2Fuse-refract%2Frefract-2496ED?logo=docker&logoColor=white" alt="Docker image"></a>
<a href="https://github.com/Use-Refract/Refract/actions/workflows/ci.yml"><img src="https://github.com/Use-Refract/Refract/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
<a href="https://github.com/Use-Refract/Refract/actions/workflows/security.yml"><img src="https://github.com/Use-Refract/Refract/actions/workflows/security.yml/badge.svg" alt="Security"></a>
<a href="go.mod"><img src="https://img.shields.io/github/go-mod/go-version/Use-Refract/Refract" alt="Go version"></a>
<a href="LICENSE"><img src="https://img.shields.io/github/license/Use-Refract/Refract" alt="MIT License"></a>
</p>

<p align="center">
<a href="#quick-start">Quick start</a> ·
<a href="#features">Features</a> ·
<a href="#screenshots">Screenshots</a> ·
<a href="#deploy">Deploy</a> ·
<a href="#ai-models">AI models</a> ·
<a href="#configuration">Configuration</a> ·
<a href="#development">Development</a>
</p>

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/screenshots/hero-dark.webp">
  <img src="assets/screenshots/hero-light.webp" alt="Refract: the assistant floating over the editor with its reasoning and reply, the proposed change as a diff above it, and the compiled PDF beside them">
</picture>

## Why Refract

Refract started as a replacement for OpenAI Prism: it opens Prism's exports as they are and keeps what made it useful (an editor next to the PDF, an assistant that understands the whole project) while running on infrastructure you control.

- **Your files stay yours.** Projects are plain folders and git repositories on your server. Export any project as a .zip, or push it to GitHub or any git remote.
- **The assistant never edits behind your back.** It works on a copy of the project, can compile and fix its own errors, and hands you a diff. Nothing changes until you accept it, file by file if you like.
- **Citations come from real metadata.** The assistant searches OpenAlex and arXiv, and every bibliography entry is built from the publisher's or arXiv's record, never written by a model.
- **Citation checks** in the project menu find missing citation keys, duplicate keys or works, incomplete entries and unused bibliography entries in the selected main document, its `\input`/`\include` files and the bibliographies it loads with `\bibliography`/`\addbibresource` (paths resolve from the main file's folder). **Check DOIs online** explicitly contacts doi.org and distinguishes missing DOIs from unavailable metadata services. Incomplete entries with a DOI or arXiv identifier offer **Review source metadata**: the correction preserves the citation key and local fields absent from the source, and appears as an editor proposal to accept (Tab) or reject (Esc). No AI connection is needed. Alternate drafts, independent subdocuments and unloaded bibliographies are excluded. Checks do not expand custom macros or evaluate conditional LaTeX; `\nocite{*}` marks all entries as used, and `crossref`/`xdata` dependencies are counted. Files over 8 MB are reported as skipped.
- **Use the AI you already pay for.** Your ChatGPT plan, your own API keys (OpenAI, Anthropic, Gemini, OpenRouter and more), or local models through Ollama when self-hosted.

## Features

### Editor and PDF

- LaTeX editor (CodeMirror) with tabs, autosave and syntax highlighting.
- Completion as you type: common commands, environments (`\begin{…}` closes itself), `\ref` from the project's labels, `\cite` from its `.bib` files, and file paths in `\input`, `\include` and `\includegraphics`, relative to the main file. Each command shows its arguments and a short description, and keeps them in view while you fill them in; citations show the author, year and title of each work.
- A collapsible document outline under the file tree, following `\input` and `\include`: click a heading to jump to it, and the one containing the cursor is highlighted. Headings are searchable from the command palette too.
- **Sticky scroll** keeps the enclosing LaTeX sections and environments at the top of the editor as you scroll. It is on by default and can be turned off in **Settings → Editor**.
- Enable **Word count** in **Settings → Editor** (or <kbd>⌘⇧M</kbd>) to see the document total and each section's count in the outline, including its subsections and included files. Counts refresh on save; **Include titles** also applies here. Each included file is counted once; missing files, repeated includes and cycles mark the total as partial. Counts do not expand custom macros or evaluate TeX conditionals.
- Compilation with [Tectonic](https://tectonic-typesetting.github.io): no TeX distribution to install, packages are fetched and cached on first use. Errors and warnings are listed with file and line, each with **Fix with AI**.
- PDF preview with SyncTeX in both directions: <kbd>⌘J</kbd> shows the cursor's position in the PDF, <kbd>⌘</kbd>-click in the PDF jumps to the source.
- **Search PDF** in the preview toolbar highlights matches across every page. With the PDF focused, <kbd>⌘F</kbd> opens search; in the search input, <kbd>↓</kbd>/<kbd>↑</kbd> or <kbd>Enter</kbd>/<kbd>Shift+Enter</kbd> moves forward/backward and <kbd>Esc</kbd> closes it. Search ignores case, handles ligatures and line-wrap hyphenation, and refreshes after compilation while preserving the selected match number (clamped if fewer remain) and reading position. It searches the PDF's extracted text, so scanned images need OCR first. A hyphen at the end of a line is removed when joining words: PDF text cannot reliably distinguish a wrapping hyphen from a real one, so a split compound such as `state-of-the-art` may need to be searched without the hyphen at the line break.
- PDF contents, citations and cross-references are clickable and jump to their target within the document while keeping your zoom. Wrapped entries in contents and figure lists highlight together across their lines. Website and email links open separately. <kbd>Tab</kbd> and <kbd>Enter</kbd> also work on PDF links; <kbd>⌘</kbd>/<kbd>Ctrl</kbd>-click keeps the jump-to-source shortcut.
- In PDFs with linked LaTeX citations, a return arrow beside each cited bibliography entry takes you back to the exact citation and briefly highlights it. If a reference is cited more than once, choose an occurrence from the menu, including separate occurrences on the same page. These viewer controls work with existing PDFs, update after compilation and are not added to the downloaded PDF.
- A right-click menu in the PDF (go to source, zoom, download).
- Command palette (<kbd>⌘K</kbd>) and a shortcut overlay (hold <kbd>⌘</kbd> or press <kbd>?</kbd>).
- English and Spanish interface, light and dark themes. Installable as an app from the browser.

### Fonts

Named `fontspec` families, such as `\setmainfont{Open Sans}`, are prepared automatically when you compile. Refract first looks for `.ttf` and `.otf` files included in the project, then uses the TeX bundle for standard Latin Modern and TeX Gyre text families, and otherwise downloads static faces from Google Fonts. Regular, bold, italic and bold italic are selected when the family supplies them. `\IfFontExistsTF` recognizes resolved families, and explicit font file selections keep their original options.

Your `.tex` files are left unchanged. Generated assets live in the project's private `.refract/` directory; public Google Fonts downloads are cached under `REFRACT_DATA_DIR/font-cache` and reused across projects and restarts. Uploaded project fonts stay private to that project. Only the requested family name is sent to Google, never document text. Set `REFRACT_FONT_DOWNLOADS=0` to use project, TeX bundle and previously cached fonts without contacting Google Fonts. Tectonic's TeX bundle has its own first-use downloads as usual.

For a font that Google Fonts does not supply, include its licensed font files in the project. Literal family names in fontspec commands are detected automatically; names assembled through TeX macros and custom file/face options remain under the document's control. A genuinely unavailable font is reported during compilation instead of producing repeated import warnings. Fonts are never silently replaced with a different family.

### Assistant

- A chat per topic, docked beside the PDF or floating over the editor.
- The agent can list, read, search, edit and create files, compile the project, search papers and add citations. It works in a separate git worktree, so you can keep writing while it runs.
- Changes arrive as a review: a diff per file, accepted or rejected all at once or file by file. Accepting creates one commit; rejecting leaves no trace.
- Shows the model's reasoning while it thinks, when the model provides it, folded away once it answers.
- Each chat remembers the model and reasoning level it used.
- **Project instructions**: a `REFRACT.md` at the project root (project menu → **Project instructions** creates it) tells the assistant what to always respect in this project: journal style, spelling, notation, what not to touch. It is an ordinary file, versioned with the project and included in exports. The assistant reads its first 8 KB on every turn, and quick actions read its first 2 KB; text inside `<!-- -->` comments is ignored. Ask the assistant to remember something and it proposes an edit to the file, which you review like any other change. The file can't override the assistant's own rules, such as never inventing citations.

### Quick actions

Select text and a toolbar offers **Improve**, **Shorten**, **Fix grammar**, **Table**, **Explain**, or a free instruction (<kbd>⌘K</kbd> with a selection). The result appears inline as a word diff: <kbd>Tab</kbd> accepts, <kbd>Esc</kbd> rejects.

### References

- **Find papers for a passage:** the assistant turns the passage into searches, looks in [OpenAlex](https://openalex.org) (published work) and [arXiv](https://arxiv.org) (preprints), and suggests the papers that support it, with a reason for each.
- **Cite:** Refract adds the BibTeX entry to your `.bib` file from doi.org or arXiv metadata, and places `\cite{…}` in the text as a change you review.
- Paste a DOI, an arXiv id or a URL to cite one specific paper.

### Projects, history and sync

- Create a blank project, or import a Prism export (.zip) or a folder. Imports are checked: up to 200 MB and 5,000 files, nothing outside the project, no symlinks.
- **New → From a template…** starts a project from an article, thesis, Beamer presentation, letter or CV. The starters include styled typography, a restrained colour palette and editable metadata at the top of `main.tex`: an article with equations, a results table and references; a thesis with a cover, contents, research chapters and appendix; eight widescreen slides; a formal letter with a personal letterhead; and a one-page CV. Choose **English** or **Español** for a built-in starter; the picker defaults to the current Refract interface language and lets you switch before creating the project. Metadata and layout stay in `main.tex`, which uses `\input` to assemble separate files in `sections/` for English or `secciones/` for Spanish. Section filenames also follow the selected language: for example `sections/introduction.tex` and `sections/methods.tex`, or `secciones/introduccion.tex` and `secciones/metodos.tex`. Sample text, headings, automatic labels and dates use the selected language. Saved personal templates keep their original content and language. Sample text and clearly labelled illustrative data show how to use each layout. All five compile with Tectonic without external images or bibliography tools; standard TeX packages may download on first use. You can still choose **Blank project** in the same picker. Changes to built-in starters apply to newly created projects; existing projects and saved templates keep their source.
- **Save as template…** in a project's menu (or its row in the project list) keeps a private copy of its current source files and selected main file. Open editor changes are saved first. Your templates appear in the new-project picker, are visible only to you, and can be deleted there. Each project created from one has independent files and a fresh history; chats, remote connections and build output are not copied. Editing or deleting the original or template does not change projects already created from it. Deleted templates move to the trash and can be restored there; restored templates return to the template picker.
- Every project is a git repository. Compiling, accepting AI changes and restoring old versions all create commits. The history view shows each version's diff and restores a file or the whole project, as a new commit.
- Push and pull to any https git remote with a token, or connect a GitHub account and pick or create repositories from Refract. Optional auto-push after each commit. A saved token is only sent to the host and port it was saved for (changing the remote to another host asks for a new one), the GitHub account's token only to GitHub, and git doesn't follow any redirect while sending a token, not even within the same server: a renamed or moved repository needs its new URL. Git runs these commands without the server's `GIT_*` environment (except `GIT_EXEC_PATH`, `GIT_SSL_CAINFO` and `GIT_SSL_CAPATH`), and refuses a project whose own git configuration has `url`, `http`, `include` or `credential` settings. With `REFRACT_OUTBOUND=public`, remotes must be public addresses and redirects aren't followed: a renamed repository needs its new URL.
- Export any project as a .zip at any time.
- Deleting a project moves it to the trash. Restore it within 30 days, or delete it forever from there; after 30 days it is deleted for good.

### Accounts

- Several users, each with their own projects, chats, AI connections and GitHub account. Nothing is shared between users.
- Sign-in through Cloudflare Access, Refract's own sign-in page (passwords and, optionally, OpenID Connect such as Google, Microsoft Entra, Keycloak or Authentik), or no sign-in on a private network.
- With Refract's own sign-in and a mail server, people can create their own account and confirm their address, choose a new password by email, and delete their account with everything in it from **Settings → Account**. Self-hosted installs stay as they are until you turn it on (see [Let people sign up](#let-people-sign-up)).

## Screenshots

<table>
  <tr>
    <td width="50%"><img src="assets/screenshots/docked.webp" alt="The assistant docked as a side panel, next to a change under review and the PDF"></td>
    <td width="50%"><img src="assets/screenshots/quick-action.webp" alt="A quick action on a selected sentence, shown as an inline word diff"></td>
  </tr>
  <tr>
    <td><b>Docked assistant.</b> The chat as a side panel, with the change it proposes open for review.</td>
    <td><b>Quick actions.</b> Improve a selected sentence and accept the word diff with <kbd>Tab</kbd>.</td>
  </tr>
  <tr>
    <td><img src="assets/screenshots/models.webp" alt="The model picker with reasoning levels and models grouped by provider"></td>
    <td><img src="assets/screenshots/history.webp" alt="The project history with the diff of an accepted AI change"></td>
  </tr>
  <tr>
    <td><b>Model picker.</b> Reasoning levels for the current model, and every model grouped by provider.</td>
    <td><b>History.</b> Every version with its diff; restore a file or the whole project.</td>
  </tr>
  <tr>
    <td><img src="assets/screenshots/palette.webp" alt="The command palette in dark mode"></td>
    <td><img src="assets/screenshots/projects.webp" alt="The project list in dark mode"></td>
  </tr>
  <tr>
    <td><b>Command palette.</b> Every action and file, a keystroke away.</td>
    <td><b>Projects.</b> All your projects; import Prism exports or folders.</td>
  </tr>
  <tr>
    <td><img src="assets/screenshots/settings.webp" alt="Settings: ChatGPT account and API providers"></td>
    <td><img src="assets/screenshots/login.webp" alt="The sign-in page with a password form and a Continue with Google button"></td>
  </tr>
  <tr>
    <td><b>AI connections.</b> Your ChatGPT plan, API keys and local model servers.</td>
    <td><b>Sign-in.</b> Passwords, with an optional OpenID Connect provider.</td>
  </tr>
</table>

The paper in these screenshots is a made-up example, and the AI responses come from a scripted test model.

## Quick start

Try Refract on your own computer in a few minutes, either with the `refract` program or with Docker.

> **Using an AI coding agent?** Ask it to follow [AGENTS.md](AGENTS.md): a step-by-step guide for agents to get Refract running for you, whether you want to try it, work on the code or put it on a server.

### Run it on your computer

[Install the `refract` program](#install-the-cli), plus [Tectonic](https://tectonic-typesetting.github.io/book/latest/installation/) to compile and git for project history, then run:

```sh
refract local
```

It needs no configuration. It opens Refract in your browser and keeps running until you press <kbd>Ctrl</kbd>+<kbd>C</kbd>. By default the address is <http://127.0.0.1:8080>, or another free port if 8080 is taken; it prints the one it uses. `--port` chooses the port and fails if it is taken; `--no-browser` doesn't open the browser.

- **Your data** goes in your user folder: `~/Library/Application Support/Refract` on macOS, `~/.local/share/refract` on Linux (or `$XDG_DATA_HOME/refract`), `%LocalAppData%\Refract` on Windows. `REFRACT_DATA_DIR` chooses another folder.
- **The secret key** that protects stored passwords and API keys lives in your system's credential store when there is one: macOS Keychain, the Secret Service on Linux (GNOME Keyring, KWallet) or Windows Credential Manager. It is the item `Refract` with an account name derived from the data folder (`data-` and a hash of its path), so two data folders keep two keys. The first run creates it; Refract never prints or logs it. When no store can be reached (headless Linux, no D-Bus session, a locked store), Refract says so once in the log and keeps the key in an owner-only `secret.key` in the data folder instead; it refuses to start if other users can read that file. On Windows the file has the folder's permissions; `%LocalAppData%` is private to your account, so choose an equally private folder if you set `REFRACT_DATA_DIR`. If `REFRACT_SECRET_KEY` or `REFRACT_SECRET_KEY_FILE` is set, Refract uses the supplied key and stores nothing.
  - **A `secret.key` that already exists is kept**, even when a credential store works: nothing moves silently.
  - **Back it up with the data.** With a credential store, the data folder alone is not enough: restoring it on another computer, or after the item is deleted, starts with a new key and the stored ChatGPT sign-in, API keys and git tokens can't be read (Refract logs this when it happens; add them again in Settings). Every start logs where the key is and its `account` (`data-` and a hash of the folder's real path), also when the key is in `secret.key`. The account follows the folder's path: moving or renaming the data folder starts with a new key, like restoring it elsewhere, so move the key with it (below). To read the key in the same form as `secret.key`: on macOS `v=$(security find-generic-password -s Refract -a <account> -w); case $v in go-keyring-base64:*) printf %s "${v#go-keyring-base64:}" | base64 -D;; *) printf '%s\n' "$v";; esac` (an item Refract created carries that prefix; one you added by hand, as below, doesn't); on Linux `secret-tool lookup service Refract username <account>`. Windows Credential Manager lists the item as `Refract:<account>` but can't show its value: on Windows, if you want a key you can back up, create `secret.key` yourself before the first run (`openssl rand -base64 32`), since an existing file always wins.
  - **From keychain to file:** save that value as `secret.key` in the data folder (`chmod 600`) and Refract uses the file from then on. **From file to keychain:** with Refract stopped, store the file's contents as the `Refract` item for that account, then delete `secret.key`. On macOS run `security add-generic-password -s Refract -a <account> -w` and paste the contents when it asks (passing the key after `-w` would show it to other processes); on Linux `secret-tool store --label=Refract service Refract username <account> < secret.key`.
- **There is no sign-in**, so Refract only listens on this computer's loopback address: other computers can't reach it. `REFRACT_ADDR` may choose another address on `127.0.0.1` or `[::1]` instead of `--port` (not both); any other address is refused before anything listens. `REFRACT_AUTH` is ignored, and `REFRACT_MODE=saas` and `REFRACT_PROFILE=team` are refused: they are for the server, `refract` without `local`. Other `REFRACT_*` variables work as for the server.
- **One instance per data folder.** `refract local` holds an exclusive lock on `local.lock` in the data folder and records its `pid` and `version` there as soon as it takes the lock, and the address it serves (`url`) once it listens. The file is never opened through a symbolic link. A second `refract local` on the same folder prints `Refract is already running at <address>`, opens the browser there (unless `--no-browser`) and exits 0. While the first one is still starting (it may be waiting for the credential store or upgrading the database), the second says so and waits up to a minute. If the lock is held but nothing answers `/healthz` with a direct `200` (only loopback addresses are ever contacted, and redirects aren't followed), it explains which process to stop (the `pid` in `local.lock`) and exits 1; it never deletes anything. The operating system drops the lock when the process ends, even after `kill -9`, so a leftover `local.lock` never blocks the next start and is safe to leave (but don't delete it while Refract runs: a new file would let a second one start). Different data folders run side by side. Server-mode `refract` doesn't take the lock.
- **`refract local --status`** is for programs that wrap Refract, such as the desktop app. It starts nothing and prints one JSON line, for example `{"state":"running","url":"http://127.0.0.1:8080","pid":1234,"version":"v0.2.0","dataDir":"...","lockFile":"..."}`. `state` is `running` (exit code 0), `starting` (the lock is held and `pid` recorded, but it doesn't listen yet; exit code 2, so a wrapper can wait and ask again), `stopped` (nobody holds the lock) or `unresponsive` (the lock is held but the recorded address doesn't answer; `url` may be missing); the last two exit 1, and a failure to check prints `{"state":"error","error":"..."}` and exits 1. It waits a few seconds for an instance that is about to answer or stopping. It creates and changes nothing: a data folder with no `local.lock` (never started) reports `stopped`.
- **The first PDF works offline.** Installers ship a [TeX seed](#offline-first-pdf): `refract local` copies it into `tectonic-cache/` in the data folder at startup, so the starter templates compile with no network and no first-use download.


#### For packagers

`refract local --embedded` is for a program that runs Refract as a child process and shows it in a web view, such as the desktop app. It never opens a browser, listens on a free port of 127.0.0.1 (`--port` or `REFRACT_ADDR` still choose one), and keeps standard output for exactly one JSON line; logs go to standard error. Run it with the same data folder as `refract local` to share projects, and with standard input connected to a pipe you keep open.

- **Per-launch token.** Make a random token (at least 16 characters of base64url or hex; 32 random bytes is right) and give it to the child on an inherited file descriptor (`--token-fd N`, Unix only, 3 or more: write the token and close your end, since Refract reads until end of file; it waits at most 10 seconds for it, and stops waiting when standard input closes or on SIGTERM) or in `REFRACT_EMBED_TOKEN`. Prefer the descriptor: processes of the same user can read a process's starting environment (`ps eww`, `/proc/<pid>/environ`) for as long as it runs. Refract reads it once, removes the variable from its own environment (Tectonic and git never see it) and never logs or prints it. Without a token it refuses to start. It then answers every request except `GET /healthz` with 401 unless the request carries the cookie `refract_embed` with that value. The app sets the cookie itself in the web view's cookie store before the first load: name `refract_embed`, host-only (no `Domain`), `Path=/`, `HttpOnly`, `SameSite=Strict`, for the host and port in the `url` of the ready line (no `Secure`: the server speaks plain http on loopback). The token check sits inside the Host check, so a request with another `Host` is still refused.
- **Keep the cookie inside this launch.** Browsers send a host-only cookie to every port of that host, so a web view holding the token would also hand it to any other service on `127.0.0.1` it requests. Keep the cookie in a store only your app's web views use (WKWebView's default store is per app; a new `WKWebsiteDataStore.nonPersistent()` per launch also works but forgets what the web app keeps in the browser, such as its settings), set it as a session cookie (no `Expires` or `Max-Age`, so it isn't written to disk), and delete the previous launch's `refract_embed` cookie before setting the new one. Above all, let the web view load nothing outside the exact origin of the ready `url` (scheme, host and port): allow only that origin in the navigation delegate, open any other link in the system browser instead, and block every other request, subresources included (with WKWebView, a `WKContentRuleList` that blocks all URLs except that origin and `blob:`, `data:` and `about:`). Refract's own pages already restrict themselves to it (`Content-Security-Policy` with `'self'`), but project files it serves (an HTML or SVG file opened from the file tree) run under a `sandbox` policy that doesn't, so the web view's own limit is what keeps the token in.
- **The line on standard output** is one JSON object with a `state`:
  - `{"state":"ready","url":"http://127.0.0.1:51234","pid":1234,"version":"v0.3.0","dataDir":"...","lockFile":"..."}`: the server answers; exit code is 0 after a graceful stop.
  - `{"state":"running",...}` (same fields as `--status`): another `refract local` holds the data folder and answers at `url`; this process exits 0 and started nothing. Without `"embedded":true` it is a `refract local` started from a terminal, which needs no token, so the app can load its `url` as it is. With `"embedded":true` it is another app launch's instance, whose token this launch doesn't know: show an error (it answers 401). A terminal `refract local` that finds an embedded instance says it runs in the desktop app and doesn't open the browser.
  - `{"state":"unresponsive",...}`: the lock is held but nothing answers (still starting, or stuck). Exit code 1; wait and start again, or tell the person which `pid` to stop. Nothing is deleted.
  - `{"state":"error","error":"..."}`: starting failed (no or bad token, no data folder, unreadable key, `REFRACT_MODE=saas`, a bad flag, or a server that doesn't answer `/healthz` within 30 seconds, which then stops on its own...). The message says what to fix. Exit code 1. `--status` also prints this line for its failures, so a program always gets a line.

  After a `ready` line nothing more is printed on standard output. A crash that takes the process down without a line (exit code 2) counts as `error`: show the end of standard error.
- **Stopping.** First save what is still in the editor: the server can't see text typed in the last moment. Call `window.refractFlush()` in the web view and wait for its promise (with WKWebView, `callAsyncJavaScript("return await window.refractFlush?.() ?? true", …)`; `evaluateJavaScript` doesn't wait for promises). It resolves within about 5 seconds to `true` when everything is saved, or `false` when some text couldn't be (the project is locked or gone, or the save failed): then ask the person before quitting. Then close the child's standard input (or send SIGTERM). Refract stops accepting connections, lets requests in flight finish (up to 10 seconds, then closes what is still open, such as assistant streams, and gives their handlers 5 seconds to record the outcome), waits for running auto-pushes and trash cleanup, releases the lock and exits; the whole stop is bounded at about 25 seconds, so allow 30 before killing it. Saves and commits are written before the request that made them answers, so a request that completed is never lost.
- **Recovering.** If the child is killed (even with `kill -9`) project files and the database stay as they were, the lock is released by the operating system, and starting it again just works; an assistant turn that was running is marked as interrupted. A child that exits before the `ready` line leaves an `error`, `unresponsive` or `running` line; one that exits after it has no line, so show the last lines of its log and offer Restart.

To share Refract with other people, [deploy](#deploy) it with sign-in instead.

### Offline first PDF

Tectonic downloads the LaTeX files a document needs the first time, which on an empty cache takes minutes and fails without a network. The tools that ship with Refract (the folder described in [Where Refract finds its tools](#where-refract-finds-its-tools)) therefore include a **TeX seed** in `tex-seed/`: the 525 bundle files and index that these need, pinned to one Tectonic bundle (TeX Live 2022):

- every built-in template (article, thesis, presentation, letter, CV), in English and Spanish;
- a plain `article` at 10 pt and 12 pt;
- a common set of packages: `amsmath`, `amsthm`, `graphicx`, `booktabs`, `xcolor`, `tikz`, `siunitx`, `listings`, `natbib`, `hyperref`, `cleveref` and `lmodern` with T1 fonts.

It is 25 MB installed (7.7 MB compressed in the download). When `refract local` starts it copies the seed into the compile cache (`TECTONIC_CACHE_DIR`, or `tectonic-cache/` in the data folder), and compiles with the seed's bundle. The copy is verified with SHA-256, only adds or replaces the seed's own files (a package you downloaded stays, and so do the files of older bundles) and is repeated if the cache is emptied; on macOS the `tectonic-cache/` folder in the data folder is excluded from Time Machine (a `TECTONIC_CACHE_DIR` you set is never touched). The seed also stamps the bundle as just checked, so the first compile does not wait for the bundle server. Tectonic builds its 23 MB format file from it on the first compile, in about a second. Updating Refract refreshes only the files that changed; your projects, `refract.db` and `secret.key` are never touched, and the installation itself can be read-only. Upgrading from a version without the seed: `refract local` now uses the seed's bundle and its own `tectonic-cache/`, not Tectonic's default bundle and cache folder (`~/Library/Caches/Tectonic`, `~/.cache/Tectonic`, ...). The packages you downloaded before belong to that other bundle and can't be reused, so a document that needs one downloads it again; the old folder is left untouched (the log names it) and can be deleted to reclaim space. `REFRACT_TEX_SEED=none` keeps the old behaviour.

A document that needs another package downloads it as before when you are online, and keeps it in the cache for later offline compiles. Offline, Refract says that a download is needed and keeps your document: compile again once you are connected.

The seed is used only by `refract local`; the server (`refract` without `local`) keeps Tectonic's own cache. `REFRACT_TEX_SEED` names another seed folder or turns it off with `none`. It is skipped, with a line in the log, when `REFRACT_TECTONIC_BUNDLE` names another bundle or `REFRACT_SANDBOX=bwrap` is on. [Packaging](packaging/README.md#tex-seed) describes how it is built and tested.

### Run it with Docker

You need [Docker](https://docs.docker.com/get-docker/).

The image is `ghcr.io/use-refract/refract` from the release after v0.2.0 on. Versions up to v0.2.0 were published as `ghcr.io/ponytojas/refract`, where they stay.

```sh
openssl rand -base64 32 > refract.key && chmod 600 refract.key   # once: keep this file
docker run -d --name refract -p 127.0.0.1:8080:8080 -v refract-data:/data \
  -e REFRACT_SECRET_KEY="$(cat refract.key)" \
  -e REFRACT_AUTH=none \
  -e REFRACT_SECRET_KEY="$(openssl rand -base64 32)" \
  -e REFRACT_AUTH=none -e REFRACT_ALLOWED_HOSTS=localhost \
  ghcr.io/use-refract/refract
```

The key encrypts the credentials you'll save (AI keys, git tokens), so use the same `refract.key` whenever you start the container again: with another key they can't be read back.

Open <http://localhost:8080>, create a project or drop a Prism .zip on the page, and press <kbd>⌘↵</kbd> to compile. The first compile downloads the LaTeX packages it needs, so it takes longer than the next ones.

To use the assistant, connect an AI in **Settings → Assistant** (see [AI models](#ai-models)).

`REFRACT_AUTH=none` means no sign-in at all: keep it to your own machine (the `127.0.0.1:` above makes sure of that). Inside the container Refract can't see that limit, so it refuses to start without sign-in unless you name the hosts you open it at: that is what `REFRACT_ALLOWED_HOSTS=localhost` says. To put Refract on a server, read [Deploy](#deploy).

## Deploy

Refract is a single container: the Go server with the web app built in, Tectonic, SyncTeX and git. Inside the container it listens on port 8080 on every interface (`REFRACT_ADDR=:8080`, set by the image) and keeps everything under `/data`. `-p 127.0.0.1:8080:8080` maps the port on the Docker host to IPv4 loopback only, for a reverse proxy in front; other containers on a shared Docker network can still reach port 8080 directly.

```sh
docker run -d --name refract --restart unless-stopped --stop-timeout 30 \
  -p 127.0.0.1:8080:8080 -v refract-data:/data \
  -e REFRACT_SECRET_KEY="$(cat refract.key)" \
  -e REFRACT_AUTH=builtin \
  -e REFRACT_PUBLIC_URL=https://refract.example.com \
  ghcr.io/use-refract/refract:0.2
```

`refract.key` holds the secret key: create it once with `openssl rand -base64 32 > refract.key && chmod 600 refract.key`, and keep it with your backups.
Set `REFRACT_PUBLIC_URL` to the address people open Refract at: Refract then refuses requests for other host names (see [Security](#security)). Add LAN names or IP addresses with `REFRACT_ALLOWED_HOSTS`.

Each release publishes the image for amd64 and arm64 as `latest`, as `0.1` (the newest 0.1.x, which only gets fixes) and as the exact version, such as `0.1.0`. To build it yourself instead, run `docker build -t refract .` in this repository, or `make docker`, which also tests it for both architectures.

**Keep the encryption key safe and stable.** It encrypts the stored ChatGPT credentials, API keys and git tokens. With a different key, those can't be read back and users have to connect again. For Docker secrets or systemd credentials, set `REFRACT_SECRET_KEY_FILE` to the key file path inside the service. See [Read secrets from files](#read-secrets-from-files).

**Decide who you trust before you invite anyone.** By default (`REFRACT_PROFILE=personal`) Tectonic runs with the server's own permissions: a document can `\input` any file the server can read, including the database (accounts, sessions, encrypted credentials) and every other account's projects. That is fine for one person, or for people who would trust each other with all of that. If the people signing in don't, run the [team profile](#run-it-for-a-team): every compile is isolated, and Refract refuses to start if it can't be. Without it, Refract warns at startup when several accounts compile without isolation.

### Run it for a team

`REFRACT_PROFILE=team` is for people who must not read each other's files: a lab, a class, a department. It requires sign-in (`REFRACT_AUTH=builtin` or `cloudflare`), runs every compile in the [compile sandbox](#compile-sandbox), the editor's and the assistant's alike, and refuses to start, saying what is missing, without sign-in, with `REFRACT_SANDBOX=none`, or when the sandbox can't compile its self-test document. It runs on Linux hosts (amd64 and arm64) with Docker, or directly with the [systemd unit](packaging/server/refract.service). Verified end to end with Docker 29 on arm64 and, on every change to the sandbox and weekly, on GitHub's Ubuntu runners (amd64, `.github/workflows/team-sandbox.yml`).

The sandbox has no network, so Tectonic needs the whole TeX bundle on disk (about 3 GB, prepared once):

```sh
# 1. The offline TeX bundle, into its own volume (downloads 2.7 GB).
docker run --rm -v refract-tex:/opt/refract/tex --entrypoint refract-prepare-bundle \
  ghcr.io/use-refract/refract:0.2 /opt/refract/tex

# 2. Refract, with the bundle read-only and the options bubblewrap needs.
docker run -d --name refract --restart unless-stopped --stop-timeout 30 \
  -p 127.0.0.1:8080:8080 -v refract-data:/data -v refract-tex:/opt/refract/tex:ro \
  --security-opt seccomp=unconfined --security-opt apparmor=unconfined --security-opt systempaths=unconfined \
  -e REFRACT_SECRET_KEY="$(cat refract.key)" \
  -e REFRACT_PROFILE=team -e REFRACT_AUTH=builtin \
  -e REFRACT_TECTONIC_BUNDLE=/opt/refract/tex/bundle -e TECTONIC_CACHE_DIR=/opt/refract/tex/cache \
  ghcr.io/use-refract/refract:0.2
```

Then create the accounts (`docker exec refract refract users add <email>`), put it [behind a reverse proxy](#behind-a-reverse-proxy) with https, and [back it up](#data-backups-and-upgrades) with the key. When you upgrade to an image with another Tectonic or bundle, prepare a new volume.

What the three `--security-opt` do, and why: bubblewrap builds each compile's sandbox from Linux user namespaces, which Docker's default seccomp and AppArmor profiles block, and mounts its own `/proc`, which Docker's masked `/proc` paths (`systempaths`) prevent. They loosen the container's own confinement, not the sandbox's: each compile still sees only its project, the bundle and a read-only system, with no network. Refract itself still runs as an unprivileged user (uid 10001). If your host restricts user namespaces (Ubuntu 24.04 and later: `kernel.apparmor_restrict_unprivileged_userns=1`), the startup check fails with a `uid map` or `Operation not permitted` error: see [Compile sandbox](#compile-sandbox).

To run it for a team without Docker, see [Run it without Docker](#run-it-without-docker).

`scripts/smoke-team.sh <image>` checks a deployment like this one end to end: the refusals, a compile, and that one account's document can't see the database or another account's project (while the same probe sees both without the profile). Biber isn't available in the sandbox: bibliographies need BibTeX (`natbib`, or `biblatex` with `backend=bibtex`).

### Run it without Docker

The `refract` executable is the whole server: the release's `.deb` (Debian, Ubuntu) or `.tar.gz` (other Linux) has it, and it needs Tectonic, SyncTeX, git and ripgrep beside it. These steps set up a team server on Debian or Ubuntu with systemd, with the [team profile](#run-it-for-a-team)'s isolation. Run them as root, from a clone of this repository at the release's tag (for the scripts):

```sh
# 1. Refract, and the tools it runs.
apt install ./refract_<version>_<arch>.deb git ripgrep bubblewrap curl build-essential zlib1g-dev
scripts/install-tex-tools.sh /usr/local/bin          # Tectonic and SyncTeX, at the image's versions

# 2. Its user, the offline TeX bundle (2.7 GB download) and the secret key.
useradd --system --home-dir /var/lib/refract --shell /usr/sbin/nologin refract
packaging/server/prepare-bundle.sh /opt/refract/tex
mkdir -p /etc/refract && openssl rand -base64 32 > /etc/refract/refract.key && chmod 600 /etc/refract/refract.key

# 3. The service: set REFRACT_PUBLIC_URL in the unit first.
cp packaging/server/refract.service /etc/systemd/system/ && systemctl daemon-reload
systemctl enable --now refract && journalctl -u refract -n 20

# 4. Accounts, with the server's key and data folder (the key stays out of the command line).
sh -c 'REFRACT_SECRET_KEY="$(cat /etc/refract/refract.key)" REFRACT_DATA_DIR=/var/lib/refract exec runuser -u refract -- refract users add you@example.com'
```

Then put it [behind a reverse proxy](#behind-a-reverse-proxy) with https: the unit listens on `127.0.0.1:8080` only. The unit keeps the data in `/var/lib/refract` and gives the key to the service through systemd's credentials, so it is never in the unit or a process listing. If Refract refuses to start, the journal says why: [Compile sandbox](#compile-sandbox) lists the usual causes (user namespaces on Ubuntu 24.04 and later, the bundle's permissions). For a single person on their own server, the sandbox is optional: remove the `REFRACT_PROFILE`, `REFRACT_TECTONIC_BUNDLE` and `TECTONIC_CACHE_DIR` lines from the unit and skip the bundle (Tectonic then downloads packages as it needs them).

### Choose how people sign in

Refract refuses to start until `REFRACT_AUTH` says how people get in.

| `REFRACT_AUTH` | Who gets in | Use it when |
|---|---|---|
| `cloudflare` (default) | Whoever your [Cloudflare Access](https://developers.cloudflare.com/cloudflare-one/applications/) policy allows. Refract verifies Access's token itself, so reaching the server directly doesn't bypass it. | Refract is behind a Cloudflare Tunnel or proxy. |
| `builtin` | Accounts you create (or, if you turn it on, people sign up for), with a password and optionally an OpenID Connect provider. `password` still works as the old name. | Any other public deployment. Serve it over https. |
| `none` | Anyone who can reach the port, as a single local user. | Only on your own machine or a private network. |

**Cloudflare Access:** set `REFRACT_CF_TEAM_DOMAIN` (e.g. `yourteam.cloudflareaccess.com`) and `REFRACT_CF_AUD` (the application's AUD tag). Each email Access lets in gets its own account on first visit.

**Passwords:** create accounts on the server. The command prints a generated password, which the user can change in **Settings → Account**. Running it again for the same email resets the password and signs that user out everywhere.

```sh
docker exec refract refract users add ana@example.com
docker exec refract refract users list
```

Accounts you add this way count as confirmed: you vouch for the address. People can see and end their own sessions in Settings → Account. To end all of someone's sessions for them (a lost laptop, say), run `refract users signout <email>`. After too many failed sign-ins an account waits 15 minutes; `refract users unlock <email>` lets it in at once. To delete an account with its projects (those in the trash too), chats and connections, run `refract users remove <email>`: it lists what would go, and deletes only when run again with `--yes`. With Cloudflare Access, also remove the person from your Access policy, or they get a new, empty account on their next visit.

### Sign in with OpenID Connect

With `REFRACT_AUTH=builtin`, an OpenID Connect provider (Google, Microsoft Entra, Keycloak, Authentik, Authelia…) can add a **Continue with …** button next to the password form.

1. Register Refract with your provider as a web application. The redirect URI is `https://<your Refract address>/api/auth/oidc/callback`.
2. Set `REFRACT_PUBLIC_URL` (e.g. `https://refract.example.com`), `REFRACT_OIDC_ISSUER`, `REFRACT_OIDC_CLIENT_ID`, `REFRACT_OIDC_CLIENT_SECRET`, and optionally `REFRACT_OIDC_NAME` for the button label.
3. Decide who may sign in. By default, only emails that already have an account (from `refract users add`). To let people create their account by signing in, list their emails or domains in `REFRACT_OIDC_ALLOWED`, e.g. `@lab.example.org,guest@example.com`.

A provider that reports the email as not verified (`email_verified` false) is refused. A provider that leaves the claim out (some Microsoft Entra setups) can't sign in to an account that has a password, because without the claim Refract can't tell the owner from someone who registered the address; it can still sign in to accounts without a password, such as those made by signing in. Binding identities to the provider's issuer and subject, which would lift this, is planned. If the account came from a sign-up nobody confirmed, the provider must report the email as verified, and then its word wins: everything the sign-up attached is removed (password, sessions, AI keys and connections, usage, projects) and the address is confirmed. The sign-in uses the authorization code flow with PKCE, and the ID token is checked against the provider's keys.

### Let people sign up

By default nobody can create an account: you add them with `refract users add`. With `REFRACT_AUTH=builtin` and a mail server, Refract can also let people sign up, confirm their address and reset a forgotten password themselves.

```sh
-e REFRACT_PUBLIC_URL=https://refract.example.com \
-e REFRACT_SMTP_URL=smtp://user:password@smtp.example.com:587 \
-e REFRACT_MAIL_FROM='Refract <no-reply@refract.example.com>' \
-e REFRACT_SIGNUP=open
```

- **Mail:** set both `REFRACT_SMTP_URL` and `REFRACT_MAIL_FROM`, and `REFRACT_PUBLIC_URL` (the base of the links in the emails). The server must offer STARTTLS on port 587; Refract refuses to send mail or credentials in clear, except to `localhost`, `127.0.0.1` and `::1`. Without a usable `REFRACT_PUBLIC_URL`, sign-up and password reset stay off, with a warning in the log.
- **What is sent:** a confirmation link (valid 24 hours), a reset link (1 hour), and a note when an account is deleted. They are plain text, in English or Spanish: the person picks the language when signing up and can change it in **Settings → Account**. Each link works once, and the database only keeps a hash of it. The link carries its token in the part of the address after `#`, so it doesn't reach server logs, and mail scanners that merely open it can't use it up: the page asks for a button press.
- **Unconfirmed accounts** can sign in but can't create or import projects until the address is confirmed. Confirming asks for the account's password unless the browser is already signed in to it, so someone who signs up with another person's address can't get them to confirm it.
- **No guessing which addresses have accounts:** signing up or asking for a reset answers the same either way and sends its email after answering. Signing up with an address that already has an account changes nothing and sends that address a reset link instead. At most 3 emails per address and 20 per network address are sent every 15 minutes: past 3 for an address the answer stays the same but nothing is sent, and past 20 for a network address the request is refused. Someone who keeps asking for emails for an address can therefore keep its owner from getting a new link for a while (links sent earlier still work, and a new one doesn't cancel the 2 before it); `refract users unlock <email>` clears an address's count.
- **Deleting an account** (Settings → Account) asks for the password (or a sign-in with the provider in the last 10 minutes, for accounts without one) and deletes the projects, those in the trash too, with their history, chats and connections.
- **`REFRACT_SIGNUP`** is `open` or `closed` (the default). It only applies with `REFRACT_AUTH=builtin`, and only takes effect once mail works. Creating accounts through OpenID Connect is governed by `REFRACT_OIDC_ALLOWED` alone.
- **Hosted service:** with `REFRACT_MODE=saas`, `REFRACT_SIGNUP=open` is refused until the terms and privacy policy people accept when signing up exist in a future version. Keep it `closed` and add accounts with `refract users add`.
- **Trying it:** run [Mailpit](https://mailpit.axllent.org/) (`docker run -p 127.0.0.1:1025:1025 -p 127.0.0.1:8025:8025 axllent/mailpit`), set `REFRACT_SMTP_URL=smtp://127.0.0.1:1025`, and read the emails at <http://127.0.0.1:8025>.

### Behind a reverse proxy

Put Refract behind a proxy that terminates https. Two things matter:

- **Send `X-Forwarded-Proto` and `X-Forwarded-For`.** Refract marks its session cookie `Secure` when the request came over https, and limits sign-in attempts per client address. It reads that address from `X-Forwarded-For` only when the request comes from the same machine or a private network, where your proxy is; otherwise anyone could forge it.
- **Keep the `Host` header**, or set `REFRACT_PUBLIC_URL`. Refract checks the host name of every request (see [Security](#security)) and the `Origin` of every change. Caddy and the nginx example below pass the public name through; if your proxy rewrites `Host` to the upstream address (`127.0.0.1:8080`), set `REFRACT_PUBLIC_URL` to the public address, or changes are refused with `403`.
- **Don't buffer responses.** The assistant streams its answer (server-sent events); a buffering proxy holds it back until the end.

[Caddy](https://caddyserver.com) does all of this by default:

```caddy
refract.example.com {
	reverse_proxy 127.0.0.1:8080
}
```

With nginx:

```nginx
location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_buffering off;          # stream the assistant's answers
    proxy_read_timeout 15m;       # long assistant turns
    client_max_body_size 200m;    # project imports
}
```

### Compile sandbox

Tectonic runs as the same user as the server, so a document could `\input` any file that user can read: the database, other accounts' projects, the server's environment. With `REFRACT_SANDBOX=bwrap` (Linux, [bubblewrap](https://github.com/containers/bubblewrap)) every compile runs in a sandbox instead: no network, a read-only view of the system, the TeX bundle and the one project being built (only `.refract/build` is writable), nothing else of the host, and limits of CPU time (the compile timeout plus a margin), 1 GiB of memory, 64 processes and 200 MiB per file. **`REFRACT_PROFILE=team` and `REFRACT_MODE=saas` require it.** Whether or not it is on, compiles go through a queue: `REFRACT_COMPILE_WORKERS` at once, one per account (with `REFRACT_AUTH=none` everyone is the same account, so projects compile one after another), and a compile that waits longer than `REFRACT_COMPILE_MAX_WAIT` seconds is answered "the server is busy" (or, when only the account's own running compile is in the way, "another of your compiles is still running"). A newer compile replaces a waiting one of the same project; other projects of the account wait their turn. `REFRACT_COMPILE_MAX_WAIT=0` waits without limit, which `REFRACT_MODE=saas` refuses. The CPU limit is `REFRACT_COMPILE_TIMEOUT` plus 5 s, and a compile that hits it is reported as timed out. A project whose `.refract` or `.refract/build` is a symbolic link is not compiled, and the folder checked is the one mounted, even if the path changes during the compile (a `git pull`, for example).

Because the sandbox has no network, Tectonic can't download packages: it needs the whole TeX bundle as a local folder, with its format file built once. On the server:

```sh
sudo apt install bubblewrap                       # and util-linux's prlimit, already there on Ubuntu
packaging/server/prepare-bundle.sh /opt/refract/tex   # downloads 2.7 GB, needs 3 GB; tectonic on the PATH
```

then set `REFRACT_SANDBOX=bwrap`, `REFRACT_TECTONIC_BUNDLE=/opt/refract/tex/bundle` and `TECTONIC_CACHE_DIR=/opt/refract/tex/cache`. Run the script again when Tectonic or the bundle changes. [`packaging/server/refract.service`](packaging/server/refract.service) is a systemd unit for a team server (see [Run it without Docker](#run-it-without-docker)), with the secret key given through `LoadCredential=`; its comments list what to prepare. The unit leaves out `RestrictNamespaces=`, `PrivateUsers=`, `ProtectKernelTunables=`, `ProtectProc=` and `ProcSubset=` because bubblewrap needs user namespaces and its own `/proc`.

The sandbox needs bubblewrap 0.5 or later (for `--bind-fd`). The process limit needs Linux 5.14 or later (which counts processes per user namespace) and a `bwrap` that is not setuid; Refract refuses to start with a setuid one. Fontconfig's configuration (`/etc/fonts`) and the loader cache (`/etc/ld.so.cache`) are mounted read-only; nothing else of `/etc` is.

At startup Refract compiles a one-line document in the sandbox and refuses to start if that fails, printing Tectonic's last lines. If it fails:

- **`bwrap: setting up uid map: Permission denied`, or `Operation not permitted`.** The host blocks unprivileged user namespaces. Ubuntu 24.04 and later do so through AppArmor (`kernel.apparmor_restrict_unprivileged_userns=1`) for programs without a profile. Recent releases (tested on 26.04) install a `bwrap-userns-restrict` profile with the `bubblewrap` package, and then nothing is needed. If your `bwrap` has none (or lives elsewhere), give it one that allows `userns`, for example in `/etc/apparmor.d/bwrap-refract`, then `sudo apparmor_parser -r /etc/apparmor.d/bwrap-refract`:

  ```
  abi <abi/4.0>,
  include <tunables/global>
  profile bwrap-refract /usr/bin/bwrap flags=(unconfined) {
    userns,
  }
  ```

  Setting `kernel.apparmor_restrict_unprivileged_userns=0` also works, but lifts the restriction for every program.
- **`Permission denied` reading the bundle.** The bundle's files must be readable by the server's user: `prepare-bundle.sh` sets them to `0444`.
- **A format file error, or `Read-only file system` under the cache.** The cache has no format file for this Tectonic and bundle: run `prepare-bundle.sh` again with the Tectonic the server uses.

The Docker image includes bubblewrap and prlimit but keeps `REFRACT_SANDBOX=none` unless you choose the team profile: in a container the sandbox needs `--security-opt seccomp=unconfined --security-opt apparmor=unconfined --security-opt systempaths=unconfined` and the bundle on a volume, as [Run it for a team](#run-it-for-a-team) shows. Biber isn't available inside the sandbox.

### Data, backups and upgrades

Everything lives in the `/data` volume:

| Path | Contents |
|---|---|
| `refract.db` | Users, sessions, projects, chats and settings (SQLite). Credentials and keys in it are encrypted with `REFRACT_SECRET_KEY`. |
| `refract.db.pre-upgrade` | A copy of the database exactly as it was when the last upgrade started. Safe to delete once the new version works. |
| `projects/` | One git repository per project, including those in the trash. |
| `tectonic-cache/` | LaTeX packages Tectonic has downloaded; with `refract local` it also holds the copy of the [TeX seed](#offline-first-pdf) (`refract-seed.json` marks it). Safe to delete: the seed comes back at the next start or compile, other packages on the next download. |
| `tmp/` | Uploads in progress. |

To back up, stop the container, copy the volume, and keep `REFRACT_SECRET_KEY` with the copy.

To upgrade, pull the new image (`docker pull ghcr.io/use-refract/refract:0.2`, or a newer version's tag) and start a new container on the same volume. Database changes are applied automatically at startup.

To go back to an older version after an upgrade:

1. Stop everything that uses the database: the container and any `refract users …` command still running.
2. Move `refract.db` aside, together with `refract.db-wal` and `refract.db-shm` if they exist. After an abrupt stop these hold changes not yet written to `refract.db`, and left in place SQLite would replay them over the restored copy.
3. Copy `refract.db.pre-upgrade` to `refract.db` (changes made since the upgrade are lost) and start the older version.

An older Refract refuses to open a database a newer one has upgraded.

If an upgrade fails, the database is left as it was (the migrations are one transaction) and Refract refuses to start, saying so and naming `refract.db.pre-upgrade`. The macOS app updates itself the same way (the update replaces the app, never the data folder) and going back to an older app is only supported by this same restore. See [macos/README.md](macos/README.md#updates).

Stopping Refract lets requests in flight and auto-pushes finish, and running assistant turns record that they were stopped: up to about 20 seconds. Docker's default is to kill the container after 10, so start it with `--stop-timeout 30` as above.

### Moving from `REFRACT_AUTH=none` to a sign-in mode

Data created without sign-in belongs to an account called `local`. Give it your email before switching, so your projects and connections stay yours:

```sh
docker exec refract refract users rename local you@example.com
docker exec refract refract users add you@example.com   # prints your password
```

Then restart with the new `REFRACT_AUTH`. With Cloudflare Access, the rename alone is enough.

## AI models

Each user connects their own AI in **Settings → Assistant**. Every connected model appears in the model picker under the chat, grouped by provider, with a search field when the list is long. Models that accept a reasoning setting show the levels they support.

### ChatGPT plan

Refract can use a ChatGPT subscription through "Sign in with ChatGPT", so requests count against your plan instead of an API bill. The sign-in runs on your own computer, because ChatGPT only accepts it from a local address:

1. [Install the CLI](#install-the-cli) on your own computer, or run `make login` from a clone.
2. Run `refract login` and choose **Continue with ChatGPT** in the browser that opens.
3. Upload the credential file it writes in **Settings → Assistant**.

For a macOS binary downloaded manually with a browser, run `xattr -d com.apple.quarantine refract` once if macOS quarantines it because it isn't signed.

The sign-in checks what ChatGPT returns (tokens, account and callback), keeps the sign-in address free of tokens and prints it if the browser can't be opened.

To restrict a server to one ChatGPT account, set `REFRACT_ALLOWED_SUBJECT` to the subject `refract login` prints.

### Install the CLI

The CLI lets you run `refract login` on your computer and upload the resulting credential file to an existing Refract instance. Login does not need Docker, Go, Node.js, git or LaTeX tools. Installing a package never starts a service, creates a key or writes data; see the [packaging policy](packaging/README.md). The same executable also includes the server and its administration commands (and `refract local`, a local-only instance); a server needs the configuration described in [Deploy](#deploy).

**macOS (Apple Silicon or Intel):**

```sh
brew install use-refract/tap/refract
refract login
```

Update with `brew update` followed by `brew upgrade use-refract/tap/refract`.

**Debian / Ubuntu:** download the `.deb` for your architecture (`amd64` or `arm64`) from [Releases](https://github.com/Use-Refract/Refract/releases). Packages are available starting with v0.1.2. For example, from the download directory:

```sh
sudo apt install ./refract_0.1.2_amd64.deb
refract login
```

Replace the version and architecture with those of your download. Install a newer `.deb` the same way to update; there is no Refract APT repository. On Linux, login opens a browser with `xdg-open` if available. Use `refract login --no-browser` to open the printed URL yourself on the same computer.

**Windows and other Linux distributions:** download the `.zip` or `.tar.gz` for your system from [Releases](https://github.com/Use-Refract/Refract/releases), extract it and run `refract.exe login` (Windows) or `./refract login` (Linux) from the extracted directory. You can also place the executable in a directory on your `PATH`.

Each new release includes WinGet manifests for the Windows zip archives. These are prepared for submission to Microsoft's community repository; `winget install --id ponytojas.Refract --exact` becomes available after Microsoft accepts a submission. See [packaging maintenance](packaging/README.md) for validation and publication.

### API keys and local models

Add providers in **Settings → Assistant → API providers**. **Test** checks the key by listing the provider's models.

| Provider | Key | Reasoning levels |
|---|---|---|
| OpenAI | Required | o-series, GPT-5 and codex models: low, medium, high, with reasoning summaries |
| Anthropic | Required | Claude 3.7 and later: extended thinking off, low, medium or high |
| Google Gemini | Required | Gemini 2.5 (low, medium, high) and Gemini 3 (low, high), with thoughts |
| OpenRouter | Required | Models OpenRouter marks as supporting reasoning |
| Mistral, DeepSeek, xAI, Groq | Required | The model's default |
| Ollama | None | The model's default |
| OpenAI-compatible (LM Studio, vLLM, LiteLLM…) | Optional | The model's default |

- Keys are encrypted, belong to the user who added them, and are never sent back to the browser.
- For endpoints that can't list their models, type the model ids in the provider's settings.
- Base URLs must be reachable from the Refract server: from inside Docker, a local Ollama is usually `http://host.docker.internal:11434`. Private and loopback addresses are allowed; link-local addresses (cloud metadata services) are refused. With `REFRACT_OUTBOUND=public`, base URLs must be public `https://` addresses (a provider saved with `http://` earlier stops working until you edit it) and local models (Ollama) aren't offered.
- Refract sets no spending limits. Usage is between you and the provider.
- Refract records how many tokens each model call uses (per account and model, never the text). The account menu shows this month's total (hover it for each model), and **Settings → Assistant → Token usage** has the details. They are what each provider reported; your provider's bill is the reference.

`REFRACT_MODEL_AGENT` and `REFRACT_MODEL_QUICK` set default models for the assistant and for quick actions and citations, used until a user picks one.

## Configuration

All settings are environment variables.

| Variable | Purpose |
|---|---|
| `REFRACT_SECRET_KEY` | **Required for the server**, via this variable or `REFRACT_SECRET_KEY_FILE`. 32 bytes, base64 (`openssl rand -base64 32`). Encrypts stored credentials and keys. |
| `REFRACT_AUTH` | `cloudflare` (default), `builtin` (Refract's own sign-in; `password` still works) or `none`. See [Choose how people sign in](#choose-how-people-sign-in). |
| `REFRACT_DATA_DIR` | Where projects, the database and caches live. Default `/data`. |
| `REFRACT_ADDR` | Listen address. Default `127.0.0.1:8080`, this computer only; the container image sets `:8080` (every interface), where `-p` decides the mapping on the Docker host and containers on a shared Docker network can still connect. Use `:8080` or a LAN address to serve other computers directly. |
| `REFRACT_ALLOWED_HOSTS` | Extra host names or IP addresses people open Refract at, separated by commas, e.g. `refract.lan,192.168.1.20` (a port is ignored). `localhost`, `127.0.0.1`, `[::1]`, `*.localhost` and the host of `REFRACT_PUBLIC_URL` are always allowed; requests for other names get `421`. When Refract listens on an address other computers can reach (judged by the address it actually binds) and neither this nor `REFRACT_PUBLIC_URL` is set, any name is accepted, with a warning in the log, and `REFRACT_AUTH=none` refuses to start. An invalid entry, or an invalid `REFRACT_PUBLIC_URL`, stops startup. |
| `REFRACT_MODE` | `selfhost` (default) or `saas`, for the hosted service. With `saas`, the ChatGPT plan is off (OpenAI's plan usage covers self-hosted apps): people use the assistant with API providers only. Refract refuses to start with `REFRACT_AUTH=none` or without an https `REFRACT_PUBLIC_URL`. |
| `REFRACT_CF_TEAM_DOMAIN`, `REFRACT_CF_AUD` | Cloudflare Access team domain and application AUD tag. |
| `REFRACT_OUTBOUND` | `any` (default) or `public`: whether addresses people supply may reach loopback and private networks. **`any`**: AI providers may use `http://` or `https://` addresses on loopback and private networks (local models, Ollama); Git remotes (always `https://`) may be on private networks and aren't pinned. **`public`** (suits a team on a shared network): provider base URLs must be public `https://` addresses, and every provider request and redirect must be `https://`, also for providers saved earlier; Ollama isn't offered; loopback, private, shared (CGNAT) and reserved addresses are refused on every connection and proxy settings are ignored; Git remotes are resolved, checked and pinned to public addresses without redirects (needs git 2.37 or later; Refract won't start with an older one). **In both modes**, link-local addresses (169.254.0.0/16, fe80::/10, such as cloud metadata services) are refused: for providers on every connection, for Git remotes when the address in the URL, or the addresses its host name resolves to when a push or pull starts, is link-local (in `any` the remote isn't pinned, so this check can't stop a host name that changes its answer in between). Any other value stops startup. |
| `REFRACT_PUBLIC_URL` | The address users open Refract at, e.g. `https://refract.example.com`. Its host is allowed by the host check (see `REFRACT_ALLOWED_HOSTS`). Needed for OpenID Connect and for the links in emails, and required with `REFRACT_MODE=saas`. |
| `REFRACT_SIGNUP` | `open` or `closed` (default): whether people can create their own account. Only with `REFRACT_AUTH=builtin`; needs the three mail settings. See [Let people sign up](#let-people-sign-up). |
| `REFRACT_SMTP_URL` | The mail server, `smtp://user:pass@host:587` (URL-encode special characters). STARTTLS is required except to `localhost`, `127.0.0.1` and `::1`. |
| `REFRACT_MAIL_FROM` | The sender, e.g. `Refract <no-reply@refract.example.com>`. Set both mail variables or neither. |
| `REFRACT_OIDC_ISSUER`, `REFRACT_OIDC_CLIENT_ID`, `REFRACT_OIDC_CLIENT_SECRET` | Your OpenID Connect provider's issuer URL and Refract's client credentials there. |
| `REFRACT_OIDC_NAME` | Label of the sign-in button: *Continue with …*. Default `SSO`. |
| `REFRACT_OIDC_ALLOWED` | Comma-separated emails and `@domains` that may create an account by signing in with OpenID Connect. Empty: existing accounts only. |
| `REFRACT_ALLOWED_SUBJECT` | The only ChatGPT account accepted (printed by `refract login`). |
| `REFRACT_MODEL_AGENT` | Default model for the assistant. |
| `REFRACT_MODEL_QUICK` | Default model for quick actions and citations. |
| `REFRACT_COMPILE_TIMEOUT` | Seconds per compile. Default 90. |
| `REFRACT_PROFILE` | `personal` (default) or `team`: people who must not read each other's files. `team` requires sign-in and the [compile sandbox](#compile-sandbox) (it turns `REFRACT_SANDBOX=bwrap` on), and refuses to start without them. See [Run it for a team](#run-it-for-a-team). |
| `REFRACT_SANDBOX` | `none` (default; `bwrap` with `REFRACT_PROFILE=team`) or `bwrap`: run every compile in a [sandbox](#compile-sandbox). Required with `REFRACT_PROFILE=team` and `REFRACT_MODE=saas`. |
| `REFRACT_COMPILE_WORKERS` | Compiles running at once, across all accounts. Default: the number of CPUs minus one, at least 1. Each account runs one compile at a time. |
| `REFRACT_COMPILE_MAX_WAIT` | Seconds a compile waits for its turn before the server answers "busy, try again". Default 20. With `REFRACT_MODE=saas`, this plus `REFRACT_COMPILE_TIMEOUT` must not exceed 95, because a proxy cuts a request with no response at about 100 s. |
| `REFRACT_FONT_DOWNLOADS` | `0` disables Google Fonts requests; project, TeX bundle and cached fonts still work. Default: downloads enabled. |
| `REFRACT_GITHUB_CLIENT_ID` | Client ID of your GitHub OAuth App, to connect GitHub accounts (see below). |
| `REFRACT_OPENALEX_API_KEY` | A free [OpenAlex key](https://openalex.org/settings/api). Without one, paper searches share a small daily budget per IP address. |
| `REFRACT_TECTONIC_BUNDLE` | The Tectonic bundle: a URL (pinned in the image), or a local folder (required with the sandbox). With `refract local`, unset means the [TeX seed](#offline-first-pdf)'s bundle; naming another one turns the seed off. |
| `REFRACT_TEX_SEED` | `refract local` only: the folder of the [TeX seed](#offline-first-pdf) to copy into the compile cache, or `none`. Default: `tex-seed/` in the bundled tools folder, when it is there and trusted. |
| `REFRACT_TECTONIC_BIN`, `REFRACT_SYNCTEX_BIN`, `REFRACT_RG_BIN`, `REFRACT_GIT_BIN` | Use this program instead of the one [Refract finds](#where-refract-finds-its-tools). A path, or a name looked up on the `PATH`. If it isn't runnable, the tool counts as missing: Refract doesn't fall back to another. |

### Read secrets from files

Set the usual variable to the secret value, or use its `_FILE` counterpart to give Refract a file path. Refract trims whitespace around the file contents, including the trailing newline. Setting both variables to non-empty values or pointing to an unreadable file causes a startup error.

| Secret value | File path |
|---|---|
| `REFRACT_SECRET_KEY` | `REFRACT_SECRET_KEY_FILE` |
| `REFRACT_OIDC_CLIENT_SECRET` | `REFRACT_OIDC_CLIENT_SECRET_FILE` |
| `REFRACT_SMTP_URL` | `REFRACT_SMTP_URL_FILE` |
| `REFRACT_OPENALEX_API_KEY` | `REFRACT_OPENALEX_API_KEY_FILE` |

For example, if Docker Compose mounts a secret named `refract_key` for the Refract service, set `REFRACT_SECRET_KEY_FILE=/run/secrets/refract_key` in that service's environment. The included systemd unit uses `LoadCredential=` and points `REFRACT_SECRET_KEY_FILE` to systemd's private copy. With `refract local`, either key variable overrides the automatic key lookup.

### Where Refract finds its tools

Refract runs `tectonic` (compiling), `synctex` (source ↔ PDF jumps), `rg` (the assistant's search) and `git` (history). For each it looks, in this order, at:

1. its `REFRACT_<NAME>_BIN` variable (see [Configuration](#configuration));
2. the folder of bundled tools next to the program: `../libexec/refract/` relative to the `refract` executable on macOS and Linux, and the executable's own folder on Windows (symbolic links of the executable are resolved first);
3. the `PATH`.

It always runs the absolute path it found, and the startup log says what was found and where (`tool name=git path=… source=bundled|env|path`). The desktop packages ship private copies in the bundled folder:

```text
libexec/refract/
  tectonic  synctex  rg          (.exe on Windows)
  tex-seed/                      the TeX seed (see Offline first PDF)
  git/
    bin/git                      macOS and Linux
    libexec/git-core/            Git's helpers
    share/git-core/templates/    optional: Git's init templates
```

For a Git found in `git/bin/`, Refract sets `GIT_EXEC_PATH` to `git/libexec/git-core`, `GIT_TEMPLATE_DIR` to the templates folder when there is one, and `GIT_CONFIG_NOSYSTEM=1`, so it never uses helpers or configuration from the system. On Windows `git/` is an unmodified [MinGit](https://github.com/git-for-windows/git/releases) tree: Refract runs `git\cmd\git.exe`, which finds its own helpers under `git\ucrt64\`, and sets only `GIT_CONFIG_NOSYSTEM=1`. A Git from `REFRACT_GIT_BIN` or the `PATH` is run as it is.

**Trust.** Whoever can replace a bundled program runs code as the user running Refract. On macOS and Linux Refract therefore ignores the bundled folder, or any single bundled tool, when it or any folder up to `libexec/` is writable by group or others, or owned by neither the current user nor root, and logs why once (it then falls back to the `PATH`). Bundled Git also requires its helper directory and checks its supporting files and templates under `git/`. On Windows Refract trusts the executable's own folder as it is and can't check its permissions: install it where only administrators can write (for example under `Program Files`), because anyone who can write there can make Refract run their programs.

A missing tool doesn't stop Refract: it logs what won't work without it and the web app shows one notice. Without `git`, creating projects and using project history are unavailable (whoever installed Refract can repair the installation or install Git); without `tectonic`, compiling doesn't work; without `synctex`, jumping between source and PDF is unavailable; without `rg`, the assistant can't search the project. The compile sandbox resolves Tectonic the same way.

### Connect GitHub

Without this, any project can still push to and pull from an https git remote with a personal access token. To let users pick or create GitHub repositories from Refract:

1. Create a [GitHub OAuth App](https://github.com/settings/developers) and check **Enable Device Flow**. The callback URL is not used, and no client secret is needed.
2. Set `REFRACT_GITHUB_CLIENT_ID` to its client ID.
3. Each user connects their account in **Settings → Integrations**.

## Command line

```text
refract                         run the server (configured with REFRACT_* variables)
refract local                   run Refract on this computer with no configuration: data in your
                                user folder, no sign-in, loopback only (--port, --no-browser)
refract login                   sign in with ChatGPT on this computer and write a credential
                                file to upload in Settings → Assistant (refract login -h)
refract users add <email>       create a confirmed account or reset its password (REFRACT_AUTH=builtin)
refract users rename <a> <b>    change an account's email, keeping its data
refract users signout <email>   end all of an account's sessions
refract users unlock <email>    clear an account's failed sign-ins and email limit (--all: every account and address)
refract users remove <email>    delete an account and everything it owns, trash included (asks for --yes)
refract users list              list accounts
refract version                 print the version
```

`refract users` works on the server's database. Run it with the same `REFRACT_DATA_DIR` and encryption key as the server, using `REFRACT_SECRET_KEY` or `REFRACT_SECRET_KEY_FILE`. Inside the container, `docker exec refract refract users …` already has the container's environment and mounted files.

## Keyboard shortcuts

On Windows and Linux, read <kbd>Ctrl</kbd> for <kbd>⌘</kbd>. Hold <kbd>⌘</kbd> anywhere in Refract to see them all.

| Keys | Action |
|---|---|
| <kbd>⌘K</kbd> | Command palette; with text selected, quick actions |
| <kbd>⌘↵</kbd> | Compile |
| <kbd>⌘J</kbd> | Show the cursor's position in the PDF |
| <kbd>⌘</kbd>-click in the PDF | Jump to the source |
| <kbd>⌘S</kbd> | Save now (it also saves on its own) |
| <kbd>Ctrl</kbd> <kbd>Space</kbd> | Show completions (<kbd>Tab</kbd> or <kbd>↵</kbd> accepts one) |
| <kbd>Tab</kbd> / <kbd>Esc</kbd> | Accept / reject a quick action's suggestion |
| <kbd>↵</kbd> / <kbd>⇧↵</kbd> | Send / new line in the chat |

## Security

- **Sign-in:** Cloudflare Access tokens are verified on every request. Passwords are hashed with PBKDF2-SHA256 (600,000 iterations). Repeated failures for an account, or from one address, are refused for 15 minutes; the counts survive restarts. Sessions are random tokens stored only as hashes, in `HttpOnly`, `SameSite=Lax` cookies (named `__Host-refract_session` over https). A session ends 30 days after signing in, or after 7 days without use; Settings → Account lists where you're signed in and signs out one device or all the others. Changing the password signs out every other device.
- **Account links:** confirmation and reset links are random, expire, work once and are stored only as hashes. Setting a password by any means cancels reset links. Sign-up and reset answer the same for addresses with and without an account, email goes out after the answer, and emails are limited per address and per network address.
- **Requests:** every API call must carry a custom header that browsers can't add cross-site, which blocks cross-site request forgery. Refract answers only to the host names it expects (`localhost` and the loopback addresses, the host of `REFRACT_PUBLIC_URL` and `REFRACT_ALLOWED_HOSTS`) and refuses others with `421`, so a website can't use DNS rebinding to reach a Refract running on your computer. The exception is a server that listens on an address other computers can reach with neither setting: it accepts any name and warns in the log, and with `REFRACT_AUTH=none` it refuses to start. Changes (anything but `GET`, `HEAD` and `OPTIONS`) must carry no `Origin` or exactly one from an allowed host (in that exception, the host asked for); others get `403`. The host check doesn't limit who can connect: the listen address, a firewall and sign-in do that.
- **Browser:** a strict Content-Security-Policy lets the app run only its own scripts and connect only to its own server. Refract can't be embedded in other sites, and over https it sends `Strict-Transport-Security`.
- **Secrets at rest:** ChatGPT credentials, API keys and git tokens are encrypted with AES-GCM under `REFRACT_SECRET_KEY`, each bound to its owner.
- **Isolation between users:** projects, chats and connections are checked against their owner on every access.
- **Compilation:** Tectonic runs with shell escape disabled (`--untrusted`) and a time limit, and with `REFRACT_SANDBOX=bwrap` (required for the hosted service) inside a [sandbox](#compile-sandbox) with no network, no access to other projects or the database, and limits on memory, processes and CPU. Imports are size-limited and can't write outside the project.
- **Outbound requests:** user-supplied AI provider URLs and git remotes can't reach link-local addresses such as cloud metadata services, in any `REFRACT_OUTBOUND` mode. With `REFRACT_OUTBOUND=public`, AI providers and git remotes can't reach the server's own network either: loopback, private, shared (CGNAT) and reserved addresses are refused, checked on every connection (so DNS answers and redirects are covered), and git connects only to the addresses it checked. A host firewall that limits outbound traffic is still a good second layer. Compiling a document that names a font found neither in the project nor in the TeX bundle downloads it from Google Fonts, sending only the family name; `REFRACT_FONT_DOWNLOADS=0` turns that off.

Found a vulnerability? Please report it privately through the repository's **Security** tab (*Report a vulnerability*) rather than in a public issue.

## Development

You need Go, Node.js, git, ripgrep, Tectonic and `synctex` (and Google Chrome for the browser tests). The image bundles all of them. Packaging tests also use Python 3.11+; `dpkg-deb` is optional locally and required in Linux CI to verify Debian packages. [AGENTS.md](AGENTS.md) lists how to check and install the development tools, and the conventions to follow when changing the code.

```sh
make dev                                  # API on 127.0.0.1:8080 (REFRACT_ADDR overrides) and Vite on :5173; open http://localhost:5173
make dev AUTH=builtin                     # the same, with the sign-in page
make users ARGS="add you@example.com"     # accounts in the development data
make login                                # refract login, for ChatGPT
make test                                 # go vet, Go tests and web unit tests
make e2e                                  # build, then the Playwright browser tests in Chrome
make e2e BROWSER=webkit                   # the same in WebKit (once: cd web && npx playwright install webkit)
make e2e BROWSER=all                      # both, as CI does
make docker                               # images for amd64 and arm64, each smoke-tested
```

Set `REFRACT_E2E_PORT` and `REFRACT_E2E_MODEL_PORT` to override ports 18090 and 18091 when running two checkouts side by side.
After `make build`, run `npx playwright test` from `web/` to select Chrome, or set `REFRACT_E2E_BROWSER=webkit` to select WebKit.
The results go to `web/test-results/chrome` and `web/test-results/webkit`.

Development data lives in `./data`, with its own key in `data/dev.key`. To contribute, see [CONTRIBUTING.md](CONTRIBUTING.md).

### How it's built

- **Server:** Go's standard HTTP server, SQLite ([modernc.org/sqlite](https://modernc.org/sqlite), no cgo), git through the git command line, [go-oidc](https://github.com/coreos/go-oidc) for Cloudflare Access and OpenID Connect, and [GoAI](https://github.com/zendev-sh/goai) for API-key providers.
- **Web app:** React, TypeScript, Vite, Tailwind CSS, [Base UI](https://base-ui.com), CodeMirror 6 and PDF.js, embedded in the Go binary.
- **Compilation:** [Tectonic](https://tectonic-typesetting.github.io) with a pinned bundle, and SyncTeX for source ↔ PDF positions.

| Directory | What it holds |
|---|---|
| `cmd/refract` | The binary: HTTP API, wiring, and the `login` and `users` commands |
| `internal/projects` | Projects as git repositories: files, imports, history, worktrees, remotes |
| `internal/compile` | Tectonic runs, log parsing and SyncTeX |
| `internal/agent`, `internal/assistant` | The assistant's tool loop; chats, turns and reviews |
| `internal/ai`, `internal/siwc`, `internal/providers` | The AI interface, the ChatGPT plan, and API-key providers |
| `internal/quick`, `internal/cite`, `internal/openalex`, `internal/arxiv`, `internal/refs` | Quick actions and citations |
| `internal/auth`, `internal/users` | Sign-in modes, accounts and sessions |
| `internal/store` | SQLite, migrations and encrypted settings |
| `web/` | The web app |
| `macos/` | The macOS app: a Swift shell around the server (see below) |

### Building the macOS app

`Refract.app` is a Swift app (AppKit and a `WKWebView`, macOS 13 or later) that runs `refract local --embedded` as a child process with its own copies of Git, Tectonic, SyncTeX and ripgrep, and shows the web app. It uses the same data folder as `refract local`, so the app and the command line share projects. Logs are in `~/Library/Logs/Refract/`.

You need a Mac with Xcode (its command line tools are enough to build), Go and Node.js:

```sh
make mac           # build/mac/<arch>/Refract.app, signed ad hoc (arm64 on Apple silicon, amd64 on Intel)
open build/mac/arm64/Refract.app
make mac-test      # the lifecycle tests (swift test)
```

The first `make mac` compiles the bundled Git and SyncTeX with `scripts/build-tools.sh` (a few minutes, cached in `build/mac/cache`, which is ignored by git). Set `TOOLS_ARCHIVE=<refract-tools-...-darwin-<arch>.tar.gz>` to use an archive you already have. Each architecture gets its own app, because the tools are built on a Mac of that architecture. Ad-hoc signing is enough to run it on the Mac that built it; signing for distribution and notarization come later. To stop at the Swift part, `cd macos && swift build`. How the pieces fit, and why, is in [macos/README.md](macos/README.md).

### CI and releases

Every push to `main` and every pull request runs the Go tests (with the race detector), the web tests and the browser tests (`.github/workflows/ci.yml`). A pull request or push that changes the macOS app (`macos/`) also runs its Swift tests on a macOS runner (`make mac-test`, `.github/workflows/macos.yml`).

Pushing a tag such as `v0.1.0` builds `refract` for macOS, Linux and Windows (amd64 and arm64) and publishes the archives with checksums on GitHub Releases. It also builds, smoke-tests and scans the Docker image for amd64 and arm64 before publishing it to GitHub Container Registry (`.github/workflows/release.yml`): see [Release image scanning](#release-image-scanning).

#### Release image scanning

Each architecture's image is pushed by digest only, with no public tag, and then scanned exactly as pushed. The public tags (`latest`, `0.1`, the exact version) move to those digests only once both architectures pass. The GitHub release waits for the scan too. `security.yml` runs the same gate on every pull request, on `main` and weekly, against an image it builds and never pushes.

- **What is scanned.** [Syft](https://github.com/anchore/syft) lists everything in the image as an SBOM: Alpine's packages (git, ripgrep, bubblewrap, OpenSSL, zlib, busybox…) and the Go modules in `refract`. [Grype](https://github.com/anchore/grype) matches that list against vulnerability databases. Both are free, Apache-2.0 tools by Anchore. `scripts/install-scanners.sh` installs them at pinned versions and checks their checksums.
- **What it can't see.** Tectonic (a static Rust binary downloaded from its releases, checked against a pinned SHA-256) and SyncTeX (built from a pinned commit and statically linked with Alpine's zlib) carry no package metadata, so the scanner doesn't recognise them. The report lists them with their pins, the version the image reports and where to look for advisories. Check those by hand when either changes and before each release: [Tectonic's advisories](https://github.com/tectonic-typesetting/tectonic/security/advisories) and the Rust crates it uses ([RustSec](https://rustsec.org)), and zlib's advisories, since SyncTeX embeds the zlib version the SBOM shows.
- **What blocks.** A finding of Critical severity stops the release, unless `security/image-scan-exceptions.json` has a reviewed exception for it. Other findings are listed in the report and don't block.
- **Recording an exception.** Add an entry with the vulnerability's id (as Grype reports it), the package, the reason it can't be exploited in Refract (or why waiting for a fix is acceptable), your GitHub login and the dates of the review and of expiry (at most 90 days apart). Merge it through a pull request like any other change. An expired or incomplete entry blocks the gate until someone reviews it again.
- **The report.** It stays with the build: each architecture's `refract-image-<version>-<arch>-scan.tar.gz` has `report.md` (the decision, the findings and the native tools), both SBOMs, Grype's full results and `image.txt` with the digest it covers. It is attached to the GitHub release and kept for 90 days as a workflow artifact, also when the scan blocks. Run it locally with `scripts/scan-image.sh <image> <folder>`.

Releases also include Debian packages for amd64 and arm64, a Homebrew formula (`refract.rb`) and WinGet manifests (`refract-winget-manifests.tar.gz`). CLI smoke tests run on macOS, Linux and Windows before the release is published. Stable releases can update the Homebrew tap automatically once its repository token is configured. [Packaging maintenance](packaging/README.md) describes the setup and manual publication steps.

## License

Refract is released under the [MIT License](LICENSE).

## Acknowledgements

Refract stands on [Tectonic](https://tectonic-typesetting.github.io), [SyncTeX](https://github.com/jlaurens/synctex), [CodeMirror](https://codemirror.net), [PDF.js](https://mozilla.github.io/pdf.js/), [OpenAlex](https://openalex.org), [arXiv](https://arxiv.org) and [GoAI](https://github.com/zendev-sh/goai). Thank you to their authors and maintainers.


### Word counts and section budgets

Open **Settings → Editor** and toggle **Word count** to see the
number of readable words in your current source or PDF selection. The setting
is remembered in this browser. Press **⌘⇧M** (**Ctrl+Shift+M** on Windows/Linux) to toggle it. With no selection,
no count is shown. Turn it off to hide the count. Commands, comments, formulas,
bibliography and citation keys are excluded from source counts; headings,
footnotes, captions and table contents are included by default.
**Include titles** stays visible underneath it in Settings → Editor and is disabled while Word count is off. Turn it off to exclude
LaTeX titles and section headings from source counts, word limits and shortening checks.
The choice is remembered when the counter is turned off or the page is reloaded.

Right-click a section's heading or body and choose **Word limit…**. Enter the
maximum and save. The same dialog lets you change or remove an existing limit.
To limit arbitrary text, select it and choose **Word limit for selection…**.
The Word count toggle also enables limit controls, excess highlighting and the
shortening assistant. Turning it off keeps saved limits for next time. No running
section counts are displayed while writing. Only words beyond the maximum are highlighted red.
Within a nested limit, its own maximum controls highlighting, even when the parent
section exceeds its limit.

When a section exceeds its maximum, a floating **Suggest a shorter version**
button identifies the innermost limited section or fragment containing the cursor or selection, including
section headings. Outside a limited range, the button is hidden. It asks the connected AI to preserve the main ideas, facts,
citations and valid LaTeX while fitting the limit. Without a connected AI, it
opens Assistant settings. The suggestion must pass the same counter before it
appears as an inline diff. Review and accept or reject it before replacing any
text. Edits during a request invalidate its response.

Limits follow edits and are saved per project and file in this browser. They survive
file and folder renames, and undoing a heading deletion restores its limit. Manual
ranges reset if the file changes outside this editor. Automatic sections are
rediscovered when the file is opened again.

PDF selections count extracted visible text directly, including any selected
numbers and references. Line-wrap hyphenation and common ligatures are normalized.
Source counting is a local prose parser, not a TeX engine: custom macros,
conditional text, generated references and included files are not expanded.
Counts cover the selected passage of the current file or PDF.
