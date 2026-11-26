package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
)

// VersionFilter is the query of GET /project/{id}/version.
type VersionFilter struct {
	Loaders          []string // nil = not sent
	GameVersions     []string // nil = not sent
	Featured         bool     // true = featured=true; false = not sent
	ExcludeChangelog bool     // true = include_changelog=false
}

// ListVersions fetches the versions of a project, newest first.
func (c *Client) ListVersions(ctx context.Context, project string, f VersionFilter) (jsonx.Value, error) {
	q := url.Values{}
	if len(f.Loaders) > 0 {
		b, _ := json.Marshal(f.Loaders)
		q.Set("loaders", string(b))
	}
	if len(f.GameVersions) > 0 {
		b, _ := json.Marshal(f.GameVersions)
		q.Set("game_versions", string(b))
	}
	if f.Featured {
		q.Set("featured", "true")
	}
	if f.ExcludeChangelog {
		q.Set("include_changelog", "false")
	}
	r := projectRequest("/project/{id}/version", project)
	r.Query = q
	return c.getJSON(ctx, r)
}

// GetVersion fetches one version by id.
func (c *Client) GetVersion(ctx context.Context, id string) (jsonx.Value, error) {
	return c.getJSON(ctx, Request{
		Path:         "/version/{id}",
		PathArgs:     []string{id},
		Resource:     fmt.Sprintf("version '%s'", id),
		NotFoundHint: "check the version id; to look up by version number: mdr version get <project> <number>",
	})
}

// GetVersionByNumber fetches the version of a project with that version number (or id).
func (c *Client) GetVersionByNumber(ctx context.Context, project, number string) (jsonx.Value, error) {
	return c.getJSON(ctx, Request{
		Path:         "/project/{id}/version/{number}",
		PathArgs:     []string{project, number},
		Resource:     fmt.Sprintf("version '%s' of project '%s'", number, project),
		NotFoundHint: "list versions: mdr version list " + project,
	})
}

// GetVersions fetches versions by id (GET /versions?ids=[...]), in input order.
func (c *Client) GetVersions(ctx context.Context, ids []string) (*BulkResult, error) {
	return c.getBulk(ctx, "/versions", ids, versionKeys)
}
