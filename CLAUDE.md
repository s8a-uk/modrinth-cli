# modrinth-cli development guide

A CLI for the Modrinth API v2, written with the Go standard library only. The binary is `mdr`
(`cmd/mdr`); the module is `github.com/s8a-uk/modrinth-cli`. All repository content (code,
comments, `--help`, errors, docs, commit messages) is in English.

## Hard rules

- **Never run `go`, `gofmt` or `staticcheck` on the host.** Use `make` (or `docker compose exec -T dev <cmd>`); everything runs in the dev container.
- **No dependencies.** No `require` in `go.mod`, no `go.sum`, no `golang.org/x/...`. `make deps-check` (part of `make check`) enforces it. Ask before working around a missing stdlib feature.
- **Never print a token**: not in output, logs, errors, test data or commits. Tokens are `config.Token` (prints `***`); `Reveal()` is called only where the client sets `Authorization` and where `credentials.json` is written. `TestNoTokenLeak` runs every command with a canary token; keep it passing.
- **Do not break the output contract** (below) without saying so in the commit.
- Run `make check` before committing. Work on a topic branch, never commit to `main`.

## Commands

| Task | Command |
|---|---|
| Everything (gofmt check, vet, staticcheck, tests, deps-check) | `make check` |
| Tests, narrowed | `make test ARGS="./internal/api/ -run TestRetry"` |
| Regenerate goldens | `make test-update` (review the diff) |
| Run | `make run ARGS="project get sodium"` |
| Format | `make fmt` |
| Build `bin/mdr` / all targets into `dist/` | `make build` / `make dist` |
| Smoke-test the linux binary | `make smoke` |
| Anonymous read-only smoke against production (the weekly drift check) | `make drift-smoke` |
| Release archives and `SHA256SUMS` in `dist/release/` | `make dist package VERSION=vX.Y.Z` |
| Live tests / run against staging | `MODRINTH_LIVE_TEST=1 MODRINTH_LIVE_PROJECT=<id> make test-live`, `make run-staging ARGS="..."` |

`make test-live` and `make run-staging` use a **real staging token**: `compose.live.yaml` mounts the file at `MODRINTH_STAGING_TOKEN_PATH` (default `~/.config/modrinth-cli/staging-token`) read-only. Run them only when asked; never read or print that file.

## Layout

```
cmd/mdr/          main: signals, panic recovery, os.Exit, TTY detection
internal/
  app/            wiring: Run(args) -> exit code (settings, format, token checks, errors)
  commands/       every command (<noun>.go, <noun>_<verb>.go), fields.go, registry.go
  cli/            command table types, parsing, help and schema generation, Check
  api/            HTTP client, retries, rate limits, error mapping, endpoints, dry run
  output/         json / ndjson / table rendering, --fields projection, stderr notes
  config/         settings, config.json, credentials.json, token files, Token type
  download/       CDN downloads with hash verification
  hashfile/       sha1 / sha512 of files
  apperr/         Error type, error codes, exit codes
  jsonx/          order-preserving JSON tree
  validate/       input validation (all regexps live here)
  suggest/        "did you mean" suggestions
  buildinfo/      version
  testutil/       mock API, Run helper, golden comparison
  livetest/       live staging tests (build tag live)
spec/openapi.yaml copy of the Modrinth API spec, checked against the command table
```

Imports flow one way: `cmd -> app -> commands -> cli -> {api, output, config, download} -> {apperr, jsonx, validate, hashfile}`. Never import upward.

## Design rules

- **The command table is the single source of truth.** `cli.Command` drives parsing, `--help`, `mdr schema` and `mdr completion` (`cli/completion.go`: static bash/zsh/fish/PowerShell scripts with commands, flags, `Enum` values and `Arg.Values` of a first argument). Do not write help text or completion lists anywhere else.
- **Parsing is ours, not the `flag` package's** (`cli/parse.go`). Accepted: `--name` (names longer than two letters), `-s` and `-vv` (short and one- or two-letter names), `--name value` / `--name=value`, bool flags without a value (`--full=false` is a usage error), `--` to end flags. No bundling (`-qy`), no single-dash long names (`-output`). Errors are built directly as `*apperr.Error`; keep the boundary tables in `cli_test.go` in step.
- **`Run` returns a value.** It returns `*output.Result` and never writes to stdout; `output` applies `--fields` and the format.
- **`Spec.Fields` documents, it does not limit.** It feeds defaults, table columns, `schema` and suggestions. `--fields` syntax is checked before the request (for mutating commands the names too, so a typo never hides a write's result); other names are checked against the response (`output.ResolveFields`): present = selected with a note, absent = `unknown_field`. `Spec.AnyFields` (api) has no list at all. When the API adds a field worth documenting, add it to `fields.go`.
- **Inject the outside world.** No `os.Getenv`, `os.Stdout`, `time.Sleep` or `os.Exit` in commands; use `Invocation` (`Env`, `IO`, `Log`) and `api.Clock`.
- **Errors are `*apperr.Error`** with an existing `Code`. Adding a code changes the public interface.
- **Do not trust input.** Arguments, flags and file names or URLs from the API go through `validate`; path segments use `url.PathEscape`.
- Only GET/HEAD and requests marked idempotent are retried; 429 is retried for any method.
- `--dry-run` never needs a token. Without a token, a mutating command's client refuses every request with `auth_required` (`api.Options.RequireToken`), so `Run` checks its inputs first and nothing is sent; with `--dry-run` the plan shows `"auth":{"token":"missing","required_scope":...}`. Set `Request.Scope` on every request that needs a token.
- OS-specific code goes in `_unix.go` / `_windows.go` (or `_linux.go`, ...) files.

## Adding a command

1. Write a func returning `*cli.Command` plus its `Run` in `internal/commands/<noun>_<verb>.go` (or `<noun>.go`). Fill `Summary` (at most 60 chars), `Args` (with `Values` when a first argument has a fixed set, for completion), `Flags` with validators, `Endpoints` with scopes, `Output` (`output.Spec`: `Fields`, `DefaultFields`, `Table`; shared lists go in `fields.go`), `Mutation`, `Examples`, `Errors`.
2. Add it to its parent's `Sub` (top-level nouns are in `registry.go`). `TestRegistryValid` runs `cli.Check`.
3. Put public, token-free API responses in `internal/commands/testdata/responses/` (no personal data) and list them in `fixtureSpecs`.
4. Add the command's default run to `defaultRuns` (`leaves_test.go`), and tests for success, 404, `--fields` and table output in `<noun>_test.go` (`package commands_test`).
5. If it calls a new endpoint, remove that endpoint from `specAllowlist` (`crosscut_test.go`).
6. `make test-update` to create `testdata/golden/<path>/default.stdout` and refresh `schema/*` and `completion/*`; read the diff.
7. Update `skills/modrinth-cli/SKILL.md` and `README.md` if they mention it. `TestSkillExamplesParse` parses every `mdr` line in their code blocks.
8. `make check`.

## Output contract (public interface)

Scripts and agents depend on this; the `schema` and help goldens catch changes.

- stdout carries only data. Notes, warnings, progress and errors go to stderr.
- Non-TTY default is JSON. `get` prints an object, `get-many`/`list` an array, `search` `{hits, offset, limit, total_hits}`.
- With JSON output an error is the last stderr line: `{"error":{"code","message","http_status","retryable",...}}`.
- Exit codes: 0 ok, 1 internal, 2 usage/input, 3 not found, 4 auth, 5 rejected by API, 6 rate limited, 7 network, 8 server error, 9 hash mismatch, 130 interrupted.
- Removing a default field, or changing a key's meaning or an exit code, bumps `cli.SchemaVersion`.
- Field values are never truncated. Destructive commands never prompt off a TTY: they need `--yes`.

## Modrinth API notes

- `User-Agent` is always `s8a-uk/modrinth-cli/<version> (<contact>)`; users cannot replace it.
- `Authorization` is the bare token (no `Bearer`), sent only to the API host, never to the CDN.
- A 404 for a missing project has an empty body; never assume error bodies are JSON. 410 means the API version is gone; do not retry.
- Rate limit: 300 requests/minute (`X-Ratelimit-*` headers). Use bulk endpoints (`ids=`), not loops.
- Array query params (`ids`, `loaders`, `game_versions`, `facets`) are JSON strings.
- Test writes only against staging (`--staging`).

## CI and releases

`.github/workflows/ci.yml` runs on pushes to `main` and on pull requests: on Linux,
`make check` and `make dist smoke` in the dev container; on Windows and macOS (arm64),
`go vet` and `go test` with the Go version of the `Dockerfile` (no container), plus a
smoke run of the binary. Tests must pass on all three: build paths with `filepath`,
compare output paths through `testutil.Normalize` (it writes `/` on Windows), and skip
POSIX permission checks on Windows with `runtime.GOOS`. `.gitattributes` keeps LF line
endings, which the golden files and the hashed test files rely on.

`.github/workflows/release.yml` runs when a `v*` tag is pushed: it checks that the tag is
`vX.Y.Z` and on `main`, runs `make check`, `make dist package VERSION=<tag>`, attests the
build provenance of the archives (`actions/attest-build-provenance`; users check it with
`gh attestation verify <archive> --repo s8a-uk/modrinth-cli`) and creates the GitHub
release with the archives, `SHA256SUMS` and generated notes. Its job is the only one with
`contents: write` (plus `id-token: write` and `attestations: write` for the attestation).
Actions are pinned to full commit SHAs (version in a comment); update them deliberately.
Attestations need a public repository (or a plan that supports them for private ones).

`.github/workflows/drift.yml` runs weekly (and by hand): `scripts/drift-smoke.sh` makes
about ten anonymous GET requests to production with the built binary
(`make drift-smoke` runs it locally), and the upstream
`apps/docs/public/openapi.yaml` of `modrinth/code` (main) is diffed against
`spec/openapi.yaml` without its 3-line header. A failure means the API or its spec
changed: fix the commands or fixtures, or re-vendor the spec with a new `Source:` line.

`.github/dependabot.yml` opens weekly PRs for the pinned actions and the Go image in
`Dockerfile` (bump `go` in `go.mod` too if needed; see Go updates).

To release (only when asked):

1. Check the Go release notes for security fixes; if there are any, update Go first (below) and merge that.
2. Make sure `main` is green on CI, and `README.md` and `skills/modrinth-cli/SKILL.md` match the release.
3. Tag the CI-tested commit of `main` and push the tag: `git tag -a vX.Y.Z -m vX.Y.Z origin/main && git push origin vX.Y.Z`. A tag with a `-` (`v0.2.0-rc.1`) becomes a prerelease.
4. Check the release page: four archives and `SHA256SUMS`. Never move or reuse a pushed tag; fix forward with a new version.

## Go updates

The only dependency is Go itself. Bump `FROM golang:<version>` in `Dockerfile` (and `go` in `go.mod` if needed), then `make rebuild && make check`. The Windows and macOS CI jobs read the version from that `FROM` line. Before a release, check the Go release notes for security fixes and update first.
