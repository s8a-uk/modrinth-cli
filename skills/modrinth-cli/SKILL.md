---
name: modrinth-cli
description: Query and publish to Modrinth (the Minecraft mod, modpack, resource pack and shader site) with the `mdr` command. Use it to search projects, inspect versions and the loaders and game versions they support, identify local jar files by hash, check them for updates, download version files with hash verification, and upload or edit versions and projects.
---

# mdr CLI

`mdr` wraps the Modrinth API v2. It never prompts when stdin is not a terminal and is safe to script.

## Discover before guessing

```
mdr schema                 # every command, one line each (JSON)
mdr schema version list    # args, flags, output fields, examples of one command
```

Do not guess command, flag or field names: an unknown one exits 2 with a `hint`. Flags are `--name` (or `-x`), `--name value` or `--name=value`; on/off flags take no value (`--full`, never `--full=true`).

## Output contract

- When stdout is not a terminal, output is **JSON** (one document). `-o ndjson` prints one item per line. Never parse `-o table`.
- stdout carries only the result. Notes (`note: ...`) and warnings (`warning: ...`) go to stderr.
- `get` returns an object, `get-many` and `list` an array, `search` `{"hits":[...],"offset","limit","total_hits"}`.
- Commands return a concise default field set; long fields (`body`, `changelog`, `gallery`) are left out. `--fields a,b.c` selects fields (those in `mdr schema <command>`, or any key the response has; an unknown name fails with `unknown_field` and suggestions), `--full` returns everything the API sent. Values are never truncated: a field is complete or absent.

```
mdr project get sodium --fields id,slug,title,downloads
mdr project get sodium --fields body
```

## Errors

On failure the exit code is non-zero and the **last line of stderr** is one JSON object:
`{"error":{"code":"not_found","message":"...","http_status":404,"retryable":false,"hint":"..."}}`.
Branch on `error.code` and follow `hint`. Retry only when `retryable` is true, after `retry_after_s` if present.

Exit codes: 0 ok, 1 internal, 2 usage or invalid input, 3 not found, 4 auth, 5 rejected by the API, 6 rate limited, 7 network, 8 server error, 9 hash mismatch, 130 interrupted.

## Common tasks

Search. Values of one repeated flag are ORed, different flags are ANDed; each `--category` and `--facet` is its own AND group:
```
mdr search --type mod --loader fabric --game-version 1.20.1 --category optimization --sort downloads --limit 5
mdr search shader --facet 'downloads>=100000' --fields slug,title,downloads
```
Hits use `project_id`; other commands take an id or a slug. Ids are stable, slugs can change: store ids.

Find the right version, then download it (the sha512 is verified; a file already there with the same content is skipped, different content fails with `file_exists` unless `--force`):
```
mdr version list sodium --loader fabric --game-version 1.20.1 --version-type release --limit 1
mdr version download OihdIimA --dir mods
```
`--dir` must exist. Pass the `id` from `version list`. Output is an array of `{"version_id","filename","path","size","sha512","status"}` with `status` `downloaded` or `exists`.

Identify local files and check them for updates (only hashes are sent, never file contents):
```
mdr file identify mods/
mdr file updates mods/ --loader fabric --game-version 1.20.1 --only-updates
```
`file updates` gives each file a `status`: `up_to_date`, `update_available`, `no_compatible_version` or `unknown`. It never changes files; pass `latest.version_id` to `version download`.

Fetch many items in one call instead of looping (the rate limit is 300 requests per minute):
```
mdr project get-many sodium lithium iris
```

Anything without a dedicated command (the raw API response is printed):
```
mdr api /project/sodium/dependencies
mdr api /search --query query=sodium --query limit=5
mdr api /project/sodium/version --fields version_number,date_published
```
Paths are relative to `/v2`; `--api-version v3` or `--api-version none` (the API root, e.g. `/updates/<id>/forge_updates.json`) select others.

## Auth

Reads work without a token. Writes and `user me` need a Modrinth personal access token (`mrp_...`), taken from the first that is set:

1. `MODRINTH_TOKEN`
2. `MODRINTH_TOKEN_FILE` (a file whose first line is the token)
3. the file saved by `mdr auth login --with-token < token.txt` (reads stdin)

With `--staging` the same order uses `MODRINTH_STAGING_TOKEN` and `MODRINTH_STAGING_TOKEN_FILE`; a production token is never sent to staging. Check with `mdr auth status`. Never print, echo or log a token, and never pass it as an argument.

## Writing safely

- Preview every write with `--dry-run` first. It checks the inputs, prints `{"dry_run":true,"request":{...},"auth":{"token":"set|missing","required_scope":...}}` (the token shown as `***`) and sends nothing that changes the server. It works without a token (`"token":"missing"`, no `Authorization`); the real run then exits 4 with `auth_required`.
- `version create` defaults the fields you leave out: `version_type` `release`, `name` = the version number, `featured` false, no dependencies, no changelog. Show these to the user in the dry-run and ask before sending.
- Destructive commands (`delete`) fail with `confirmation_required` unless `--yes` is given. Get the user's approval before passing it.
- Test uploads against the staging site with `--staging`.

```
mdr version create my-mod --file build/libs/my-mod-1.2.0.jar \
  --version-number 1.2.0 --loader fabric --game-version 1.20.1 \
  --changelog-file CHANGELOG.md --staging --dry-run
mdr version edit IIJJKKLL --set version_type=beta --dry-run
```

## Do not

- Do not parse table output or stderr notes.
- Do not write back a `body` or `changelog` you did not fetch with `--fields` or `--full`; the default output omits them.
- Do not loop single `get` calls over many ids; use `get-many`.
