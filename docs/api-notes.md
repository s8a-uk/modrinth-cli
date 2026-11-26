# Modrinth API: observed behavior

Facts about the Modrinth API v2 that `mdr` relies on and that the official documentation
or the OpenAPI spec (`spec/openapi.yaml`) does not state, or states differently. Each entry
says when and how it was observed. Re-check an entry before changing code that depends on it.

All observations were made against production (`api.modrinth.com`) without a token unless
noted. "staging" is `staging-api.modrinth.com`.

## Errors

| Fact | Observed | Where it matters |
|---|---|---|
| An unknown project (`GET /project/{id}`) returns **404 with an empty body**, not the JSON error object. Errors cannot be assumed to be JSON. | 2026-11-25 | `internal/api` error classification; not-found messages are built from context |
| Other errors use `{"error":"<code>","description":"..."}`, often with an extra `details` array that the spec does not list. | 2026-11-25 | `api_error`, `api_description`, `api_details` in mdr's error object |
| An unknown SPDX id in `GET /tag/license/{id}` returns **400** (`details: ["Invalid SPDX identifier specified"]`), not 404. | 2026-11-26 | `tag license` exits 5 (`bad_request`) |
| `GET /updates/{id}/forge_updates.json` for an unknown project returns **400** "The specified project does not exist!". | 2026-11-26 | `mdr api --api-version none` |

## Routes

| Fact | Observed | Where it matters |
|---|---|---|
| `forge_updates.json` is served only from the **API root**: `/updates/{id}/forge_updates.json` returns 200, `/v2/updates/...` returns 404. The spec lists it under the `/v2` server. | 2026-11-26 | `mdr api --api-version none` |
| `GET /` on the API host redirects (301) to an HTML page. | 2026-11-26 | Not used |

## Rate limits

| Fact | Observed | Where it matters |
|---|---|---|
| Every response carries `X-Ratelimit-Limit: 300`, `X-Ratelimit-Remaining` and `X-Ratelimit-Reset`. `Reset` is **seconds until reset** (an integer, `0` when not limited), not an epoch time. | 2026-11-26 | `internal/api` retry state machine |
| `Remaining` returned to 300 between a few sequential requests, so the budget appears to refill continuously. | 2026-11-26 | Preflight waiting |
| The body and headers of an actual **429** have not been observed (never triggered on purpose). The client falls back from `X-Ratelimit-Reset` to `Retry-After` to 5 seconds. | open | `internal/api` retry state machine |

## Search and lists

| Fact | Observed | Where it matters |
|---|---|---|
| `/search` clamps `limit` above 100 to 100 **silently** (no error). | 2026-11-25 | `search` pages at 100 hits per request |
| Search hits contain fields the spec does not list: `all_project_types`, `author_id`, `organization`, `organization_id`, `environment` (an array), `disclosure_types`. `gallery` is an array of URL strings. | 2026-11-26 | Field sets in `internal/commands/fields.go` |
| With `include_changelog=false`, version objects have **no `changelog` key** at all. Versions also carry `environment` (a string) and `files[].id`. | 2026-11-26 | `version list` default request |
| Projects carry `environment` and `raw_icon_url`. | 2026-11-26 | Project field set |
| Team member objects embed a `user` with more keys than `GET /user/{id}` returns (`raw_avatar_url`, `badges`, `campaigns` keyed by campaign name, `auth_providers`, ...). | 2026-11-26 | Team member field set |

## Bulk endpoints

| Fact | Observed | Where it matters |
|---|---|---|
| `GET /projects?ids=[...]` accepted 100, 101, 250 and 300 ids (query strings up to 5.1 KB); no limit was hit. `GET /versions?ids=[...]` accepted 101. | 2026-11-26 | mdr still chunks at 100 ids / 4 KiB as its own conservative limit |
| `/projects` returns results in **neither input nor reverse order**; `/versions` returned input order. | 2026-11-26 | Bulk results are reordered to the input order client-side |
| `/projects?ids=` also accepts **slugs**, and `/users?ids=` also accepts **usernames**. | 2026-11-26 | `project get-many`, `user get-many` |
| `POST /version_files` accepted 100, 1000 and 5000 hashes (5000 in about 1 s); `POST /version_files/update` accepted 1000. Only known hashes appear in the response. | 2026-11-26 | mdr still sends 100 hashes per request |

## Writes

| Fact | Observed | Where it matters |
|---|---|---|
| `version_number` of a new version is limited to **32 characters** (400 `invalid_input`, "Field version_number failed validation with error: length"). The limit is in the server code, not the spec. | 2026-11-26, staging | `validate.NewVersionNumber` |
| Multipart `POST /version`: file parts named `file0`, `file1`, ... listed in `file_parts` are accepted, and `primary_file` naming one of them makes it the primary file. | 2026-11-26, staging | `api.FilePartName` |
| A project with no versions reports `project_type: "project"` (it is typed once versions exist). | 2026-11-26, staging | Display only |
