# Contributing to Refract

Thanks for helping. Bug reports, fixes and features are all welcome.

## Before you start

- **Bugs:** open an issue with what you did, what you expected and what happened. For compile problems, attach the smallest `.tex` that shows it, if you can.
- **Features:** open an issue first for anything larger than a small fix, so we can agree on the approach before you spend time on it.
- **Security issues:** don't open a public issue. Report them privately from the repository's **Security** tab (*Report a vulnerability*).

## Set up and run it

[AGENTS.md](AGENTS.md) has the full path, written so that you or your AI coding agent can follow it: the tools to install, `make dev`, and how to check it works. In short:

```sh
make dev     # API on :8080, the app on http://localhost:5173
make test    # go vet, Go tests, web unit tests
make e2e     # browser tests against the real binary (needs Tectonic and Chrome)
```

## Making a change

Follow the conventions in [AGENTS.md](AGENTS.md#conventions). The ones that trip people up most:

- Every user-facing string goes through `t()` and needs a Spanish translation; a test fails otherwise.
- Database changes are new migrations; never edit an existing one.
- New behaviour comes with tests, and changed configuration or commands with a README update.
- Run `gofmt` on Go code and `npx tsc --noEmit` in `web/` (not `tsc -b`, which writes `.js` files next to the sources).

## Pull requests

- Keep each PR to one change, with a description of what it does and why, and how you tested it.
- CI runs the Go tests (with the race detector), the web tests and the browser tests on every PR. It has to pass before merging. On a first contribution, a maintainer approves the run.
- Commit messages are one line in English describing the change.

By contributing, you agree that your contribution is licensed under the [MIT License](LICENSE).
