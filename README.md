# modrinth-cli (`mdr`)

`mdr` is a command-line client for the [Modrinth](https://modrinth.com) API v2: search
projects, read projects, versions, users and tags, identify local mod files by hash and
check them for updates, download version files with hash verification, and upload or edit
versions and projects.

It is built for scripts and AI coding agents as much as for people: output is JSON when
stdout is not a terminal, errors are structured, exit codes are documented, and
`mdr schema` describes every command as JSON. It is a single static binary written with
the Go standard library only.

`mdr` is not a mod manager (no profiles, dependency installs or launcher integration) and is
**not affiliated with or endorsed by Modrinth**.

## Install

**GitHub Releases.** Each [release](https://github.com/s8a-uk/modrinth-cli/releases) has
an archive per platform and a `SHA256SUMS` file. `<version>` is the tag without the
leading `v` (tag `v0.1.0`, archive `mdr_0.1.0_linux_amd64.tar.gz`):

| Platform | Archive |
|---|---|
| Linux x86-64 | `mdr_<version>_linux_amd64.tar.gz` |
| Linux arm64 | `mdr_<version>_linux_arm64.tar.gz` |
| macOS Apple Silicon | `mdr_<version>_darwin_arm64.tar.gz` |
| Windows x86-64 | `mdr_<version>_windows_amd64.zip` |

Each archive holds the binary (`mdr`, or `mdr.exe` on Windows), `LICENSE`, `README.md`
and `skills/modrinth-cli/SKILL.md`. Download your archive and `SHA256SUMS` into one
directory, check the archive, extract it and put the binary on your `PATH`.

Linux:

```sh
sha256sum --ignore-missing -c SHA256SUMS
tar -xzf mdr_0.1.0_linux_amd64.tar.gz
mkdir -p ~/.local/bin && install -m 0755 mdr ~/.local/bin/
```

macOS:

```sh
shasum -a 256 --ignore-missing -c SHA256SUMS
tar -xzf mdr_0.1.0_darwin_arm64.tar.gz
xattr -d com.apple.quarantine mdr   # only if the browser added it; the binary is not notarized
```

Windows (PowerShell): compare the hash with the line for the archive in `SHA256SUMS`,
then extract it:

```powershell
(Get-FileHash mdr_0.1.0_windows_amd64.zip -Algorithm SHA256).Hash.ToLower()
Select-String windows_amd64.zip SHA256SUMS
Expand-Archive mdr_0.1.0_windows_amd64.zip -DestinationPath "$env:LOCALAPPDATA\Programs\mdr"
```

and add that directory to your `PATH`.

**Build provenance.** `SHA256SUMS` lives in the same release as the archives, so it
only guards against broken downloads. To check that an archive was built from this
repository by its release workflow, verify its
[artifact attestation](https://docs.github.com/actions/security-for-github-actions/using-artifact-attestations)
with the [GitHub CLI](https://cli.github.com/):

```sh
gh attestation verify mdr_0.1.0_linux_amd64.tar.gz --repo s8a-uk/modrinth-cli
```

**From source** (Go 1.27 or later):

```sh
go install github.com/s8a-uk/modrinth-cli/cmd/mdr@latest
```

**Name clash.** If another program called `mdr` is already on your `PATH` (for example a
small Markdown viewer of the same name), rename this binary (say, to `modrinth-cli`) or put
its directory first on `PATH`.

## Quick start

```sh
mdr search --type mod --loader fabric --game-version 1.20.1 --sort downloads --limit 5
mdr project get sodium
mdr project get sodium --fields id,slug,title,downloads
mdr version list sodium --loader fabric --game-version 1.20.1 --version-type release --limit 1
mdr version download OihdIimA --dir mods
mdr file updates mods/ --loader fabric --game-version 1.20.1 --only-updates
mdr api /project/sodium/dependencies
mdr --help
mdr version create --help
```

On a terminal the output is a table; piped or redirected it is JSON. Use `-o json`,
`-o ndjson` or `-o table` to choose.

## Coverage of the API

`mdr` does not wrap every Modrinth endpoint with its own command. Like `gh` and its
`gh api`, it gives dedicated commands to the common tasks (searching, inspecting
projects and versions, identifying and downloading files, publishing versions) and
leaves the rest to `mdr api`, which calls any path of the API directly:

```sh
mdr api /user/jellysquid3/projects                    # GET, like a dedicated read command
mdr api -X POST /project/AANobbMI/follow --dry-run    # a write without a dedicated command
mdr api /project/sodium/version --fields version_number,date_published
```

`mdr api` goes through the same client as every other command: User-Agent, token
handling, rate-limit waits, `--staging`, `--dry-run`, JSON errors and exit codes all
apply. What it does not add is what dedicated commands provide: short arguments,
default field sets and tables, input checks and scope hints. The path is relative to
`/v2` by default; `--api-version v3` uses v3, and `--api-version none` calls the path
from the API root for routes outside the versioned API:

```sh
mdr api --api-version none /updates/sodium/forge_updates.json
```

Dedicated commands are added when a task is used often enough to be worth them; run
`mdr schema` for the current list.

## Output and errors

- stdout carries only the result. Notes, warnings, progress and errors go to stderr.
- `get` prints an object, `get-many` and `list` an array, `search`
  `{"hits":[...],"offset","limit","total_hits"}`. `-o ndjson` prints one item per line.
- Each command prints a concise default set of fields. `--fields a,b.c` selects fields and
  `--full` prints everything the API returned. Field values are never truncated.
  `mdr schema <command>` lists the documented fields; a name that is not documented
  but present in the response (a field the API added later) is selected too, with a
  note. A name in neither fails with `unknown_field` after the response, with
  suggestions. `mdr api` takes `--fields` for JSON responses.
- With JSON output, the last line of stderr on failure is a JSON object such as
  `{"error":{"code":"not_found","message":"...","http_status":404,"retryable":false,"hint":"..."}}`.
  `error.code` is stable; scripts should branch on it.

| Exit code | Meaning |
|---|---|
| 0 | success |
| 1 | internal error |
| 2 | usage error or invalid input |
| 3 | not found |
| 4 | authentication or permission |
| 5 | rejected by the API |
| 6 | rate limited |
| 7 | network error (DNS, TLS, timeout) |
| 8 | server error |
| 9 | hash mismatch |
| 130 | interrupted |

`mdr` waits on the API rate limit (`--max-wait`), retries reads on transient failures
(`--no-retry` to disable) and never retries writes, except after a 429.

## Authentication

Reading needs no token. Writing (and `mdr user me`) needs a Modrinth
[personal access token](https://modrinth.com/settings/pats) with the scopes the command
lists in its `--help`. The token is taken from the first of:

1. the `MODRINTH_TOKEN` environment variable
2. `MODRINTH_TOKEN_FILE`, a file whose first line is the token
3. `credentials.json` in the config directory, written by `mdr auth login`

```sh
mdr auth login --with-token < token.txt
mdr auth status
mdr auth logout
```

`--staging` sends requests to `staging-api.modrinth.com` and uses `MODRINTH_STAGING_TOKEN`,
`MODRINTH_STAGING_TOKEN_FILE` or the staging entry of `credentials.json` instead. A
production token is never sent to staging. Try writes there first, and preview any write
with `--dry-run`, which checks the inputs and prints the request instead of sending it.
It works without a token: the plan then has no `Authorization` header,
`"auth":{"token":"missing","required_scope":"VERSION_CREATE"}`, and a warning on stderr.
Without `--dry-run` and without a token, a write fails with `auth_required` (exit 4)
before anything is sent:

```sh
mdr version create my-mod --file build/libs/my-mod-1.2.0.jar --version-number 1.2.0 \
  --loader fabric --game-version 1.20.1 --changelog-file CHANGELOG.md --staging --dry-run
```

Destructive commands (`project delete`, `version delete`) ask for confirmation on a
terminal and otherwise require `--yes`. The token is never printed; in `--dry-run` and
`-vv` output it appears as `***`.

## Configuration

`mdr config list` shows every setting with its value and source; `mdr config path` shows
where the files are.

| OS | Config directory |
|---|---|
| Linux | `$XDG_CONFIG_HOME/modrinth-cli`, else `~/.config/modrinth-cli` |
| Windows | `%AppData%\modrinth-cli` |
| macOS | `~/Library/Application Support/modrinth-cli` |

`MODRINTH_CONFIG_DIR` overrides the directory. It holds `config.json` (settings) and
`credentials.json` (tokens, mode 0600 on Linux and macOS). Settings, in order of precedence
flag > environment > `config.json` > default:

| Setting | Environment | Default |
|---|---|---|
| `output` | `MODRINTH_OUTPUT` | `auto` |
| `staging` | `MODRINTH_STAGING` | `false` |
| `timeout` | `MODRINTH_TIMEOUT` | `30` (seconds) |
| `max_wait` | `MODRINTH_MAX_WAIT` | `60` (seconds) |
| `user_agent_contact` | `MODRINTH_USER_AGENT_CONTACT` | empty |

```sh
mdr config set output json
mdr config get timeout
```

`user_agent_contact` adds your contact (an email or URL) to the User-Agent that Modrinth
asks API clients to send. `NO_COLOR` disables colors.

If TLS fails with code `tls` in a minimal container, install the system CA certificates
(for example the `ca-certificates` package) or set `SSL_CERT_FILE`.

## Shell completion

`mdr completion <bash|zsh|fish|powershell>` prints a completion script generated from
the command table: commands, flags, the values of enum flags and the tag kinds. It does
not call the API.

```sh
source <(mdr completion bash)                                   # bash, e.g. in ~/.bashrc
mdr completion zsh > "${fpath[1]}/_mdr"                         # zsh
mdr completion fish > ~/.config/fish/completions/mdr.fish       # fish
mdr completion powershell | Out-String | Invoke-Expression      # PowerShell, e.g. in $PROFILE
```

## Use with AI agents

[`skills/modrinth-cli/SKILL.md`](skills/modrinth-cli/SKILL.md) is a short skill file for
agents such as Claude Code and Codex: the output and error contract, auth, safe writes and
typical tasks. Copy it into your agent's skills directory (for Claude Code,
`~/.claude/skills/modrinth-cli/SKILL.md` or `.claude/skills/modrinth-cli/SKILL.md` in a
project). For everything else, agents can ask the CLI itself:

```sh
mdr schema
mdr schema file updates
```

## Development

All builds and tests run in a Docker container; nothing is installed on the host. With
Docker and Docker Compose installed:

```sh
make check   # gofmt, go vet, staticcheck, tests, standard-library-only check
make test ARGS="./internal/api/ -run TestRetry"
make build   # bin/mdr
make dist    # cross-compiled binaries in dist/
make dist package VERSION=v0.1.0   # release archives and SHA256SUMS in dist/release/
```

## License

[MIT](LICENSE)
