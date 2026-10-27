# AGENTS.md

Instructions for AI coding agents (Claude Code, Codex, Cursor, Copilot and others) helping someone get Refract running, or start working on its code. The person may be new to the project, to Go, or to LaTeX: explain what you do in plain words and check each step before moving on.

Refract is a self-hosted LaTeX workspace with an AI assistant: a Go server with an embedded React app, Tectonic to compile, SyncTeX for source ↔ PDF positions, git for history, SQLite for data. The [README](README.md) describes features, deployment and configuration in full; this file is the step-by-step path.

## 1. Ask what they want

Ask before doing anything, because the three paths need different tools:

1. **Try it** on their computer → [Path A](#path-a-try-it-with-docker): only Docker needed.
2. **Work on the code** → [Path B](#path-b-develop-locally): Go, Node.js and the LaTeX tools.
3. **Run it on a server** for themselves or a team → [Path C](#path-c-run-it-on-a-server).

Also ask which AI they will use with the assistant: a ChatGPT subscription, an API key (OpenAI, Anthropic, Google Gemini, OpenRouter, Mistral, DeepSeek, xAI, Groq), or a local model (Ollama, LM Studio). Refract works without one, but the assistant, quick actions and citations need it.

## Rules while you help

- **Secrets stay out of commands, files and chat.** Never print, log, commit or paste `REFRACT_SECRET_KEY`, API keys, git tokens or passwords. Generate the secret key into a file the person keeps (`openssl rand -base64 32 > refract.key`, `chmod 600 refract.key`), and have them type API keys into Refract's Settings themselves.
- **`REFRACT_AUTH=none` only on `127.0.0.1`.** It means no sign-in at all. Never bind it to a public interface.
- **Don't delete data.** `REFRACT_DATA_DIR` (`./data` in development, the `refract-data` volume in Docker) holds the person's projects. Ask before removing or resetting anything in it.
- **Some steps are theirs.** Signing in to ChatGPT (`refract login` / `make login`) opens a browser on their computer, and creating OAuth apps happens on provider websites. Explain the step and wait for them.

## Path A: try it with Docker

Needs [Docker](https://docs.docker.com/get-docker/). Check with `docker version`.

```sh
git clone https://github.com/ponytojas/Refract.git
cd Refract
docker build -t refract .          # takes a few minutes the first time
openssl rand -base64 32 > refract.key && chmod 600 refract.key
docker run -d --name refract -p 127.0.0.1:8080:8080 -v refract-data:/data \
  -e REFRACT_SECRET_KEY="$(cat refract.key)" -e REFRACT_AUTH=none refract
```

Verify: `curl -s http://127.0.0.1:8080/healthz` prints `ok`. Then tell them to open <http://localhost:8080>, create a project and press ⌘↵ (Ctrl+Enter) to compile. The first compile downloads LaTeX packages and takes longer.

If something fails, `docker logs refract` shows why. Keep `refract.key`: without it, stored credentials can't be read back.

## Path B: develop locally

### Prerequisites

Check each tool and install what's missing. Versions are those the project builds with; newer usually works.

| Tool | Check | Notes |
|---|---|---|
| Go 1.27 | `go version` | <https://go.dev/dl/> |
| Node.js 24 | `node --version` | <https://nodejs.org> |
| git | `git --version` | Projects are git repositories. |
| ripgrep | `rg --version` | The assistant's search tool. macOS: `brew install ripgrep`; Debian/Ubuntu: `apt install ripgrep`. |
| Tectonic | `tectonic --version` | Compiles LaTeX. macOS: `brew install tectonic`; elsewhere see <https://tectonic-typesetting.github.io/book/latest/installation/>. |
| synctex | `synctex help` | Optional: without it, only jumping between source and PDF fails. It comes with TeX Live and MacTeX; otherwise build it as the `synctex` stage of the [Dockerfile](Dockerfile) does (drop `-static` on macOS). |
| Google Chrome | | Only for `make e2e`. |

### Run it

```sh
git clone https://github.com/ponytojas/Refract.git
cd Refract
make dev
```

`make dev` installs the web dependencies on first run, creates a development key in `data/dev.key`, and starts the API on `:8080` and Vite on `:5173` with `REFRACT_AUTH=none` and data in `./data`. Leave it running in its own terminal.

Verify: `curl -s http://127.0.0.1:8080/healthz` prints `ok`, and <http://localhost:5173> shows the project list. Open that address (5173: Vite serves the app and forwards `/api` to 8080).

Variants:

- `make dev AUTH=password` shows the sign-in page. Create an account with `make users ARGS="add you@example.com"` (it prints the password).
- `make login` signs in to ChatGPT on this computer; the person then uploads the file it writes in **Settings → Assistant**.

### Before you call a change done

```sh
make test                     # go vet, Go tests, web unit tests
cd web && npx tsc --noEmit    # type-check the web app
```

`make e2e` builds the binary and runs a Playwright smoke test (needs Tectonic and Chrome).

Use `npx tsc --noEmit`, not `npx tsc -b`: the latter writes `.js` files next to the sources.

### Conventions

- **Read the surrounding code first** and follow its patterns. The server uses the Go standard library where it can; add a dependency only when it clearly earns its place.
- **Every user-facing string goes through `t()`** (`web/src/lib/i18n.ts`) and needs a Spanish translation in `web/src/locales/es/`. A test fails when one is missing.
- **Moving between pages goes through `web/src/lib/router.tsx`** (`navigate`, `<Link>`), never `window.location`: it waits for pending saves and animates the change. A page that holds unsaved work registers it with `useBeforeLeave`.
- **Database changes are new migrations** appended to the list in `internal/store/store.go`. Never edit an existing one: installations have already applied it.
- **New behaviour gets tests**; changed configuration or commands get a README update.
- **Commit messages** describe the change in one line, in English.
- Never commit `data/`, `fixtures/`, `.env` files or keys (they are in `.gitignore`).

The README's [How it's built](README.md#how-its-built) section maps the directories.

## Path C: run it on a server

Follow the README's [Deploy](README.md#deploy) section. Decide these with the person first:

1. **How people sign in** (`REFRACT_AUTH`): `cloudflare` if the server is behind Cloudflare Access, otherwise `password`, optionally with an OpenID Connect provider (Google, Microsoft Entra, Keycloak…).
2. **The address and https**: a domain pointing at the server and a reverse proxy with TLS (the README has Caddy and nginx examples; the proxy must send `X-Forwarded-Proto` and must not buffer responses).
3. **Where the secret key lives**: somewhere it survives reinstalls and is backed up with the data volume.

Then build the image, start the container, create the first account (`docker exec refract refract users add <email>`), and verify `https://<domain>/healthz` prints `ok` and the sign-in page loads.

## When something goes wrong

| Symptom | Cause and fix |
|---|---|
| `mkdir /data: read-only file system` (or permission denied) | `REFRACT_DATA_DIR` isn't set, so the default `/data` is used outside Docker. Use `make dev`, or set `REFRACT_DATA_DIR=./data`. |
| `REFRACT_SECRET_KEY must be 32 bytes, base64-encoded` | Generate one with `openssl rand -base64 32`. Commands like `refract users` need the same key as the server. |
| `REFRACT_AUTH=cloudflare needs REFRACT_CF_TEAM_DOMAIN…` | Refract won't start without a sign-in mode. Set `REFRACT_AUTH` (see the README). |
| Compiling fails with `executable file not found` for `tectonic` | Tectonic isn't installed, or isn't on the server's `PATH`. |
| Jumping between source and PDF does nothing | `synctex` is missing (see Prerequisites). |
| The first compile is slow or fails offline | Tectonic downloads packages on first use; it needs internet once per package. |
| The assistant answers "No AI connected" | Connect ChatGPT or add an API provider in **Settings → Assistant**. |
| The assistant's answer arrives all at once behind a proxy | The proxy buffers responses: turn buffering off (README, *Behind a reverse proxy*). |
| macOS refuses to open a downloaded `refract` | It isn't signed: `xattr -d com.apple.quarantine refract`. |
