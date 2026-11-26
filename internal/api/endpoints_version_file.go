package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/s8a-uk/modrinth-cli/internal/apperr"
	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
)

// maxHashesPerRequest is the number of hashes sent in one POST /version_files or
// /version_files/update. Measured on 2026-11-26 (production, anonymous): 5000
// hashes in one /version_files request and 1000 in one /version_files/update were
// answered without complaint, so this is a conservative limit of our own.
const maxHashesPerRequest = 100

// UpdateFilter is the body of /version_files/update besides the hashes.
type UpdateFilter struct {
	Loaders      []string `json:"loaders"`
	GameVersions []string `json:"game_versions"`
	VersionTypes []string `json:"version_types,omitempty"`
}

// GetVersionForDownload fetches one version by id, also in a dry run (version
// download stops before the first file request instead).
func (c *Client) GetVersionForDownload(ctx context.Context, id string) (jsonx.Value, error) {
	return c.getJSON(ctx, Request{
		Path:         "/version/{id}",
		PathArgs:     []string{id},
		Resource:     fmt.Sprintf("version '%s'", id),
		NotFoundHint: "check the version id; to look up by version number: mdr version get <project> <number>",
		SendInDryRun: true,
	})
}

// VersionFromHash fetches the version that has a file with this hash.
func (c *Client) VersionFromHash(ctx context.Context, hash, algorithm string) (jsonx.Value, error) {
	return c.getJSON(ctx, Request{
		Path:            "/version_file/{hash}",
		PathArgs:        []string{hash},
		Query:           url.Values{"algorithm": {algorithm}},
		NotFoundMessage: "no version has a file with this " + algorithm + " hash",
		NotFoundHint:    "the file may not be on Modrinth; check the hash and --algorithm",
	})
}

// VersionsFromHashes looks up the versions of files by hash (POST /version_files),
// 100 hashes per request. The map is keyed by the lower-case hash; hashes nothing
// matched are absent.
func (c *Client) VersionsFromHashes(ctx context.Context, hashes []string, algorithm string) (map[string]jsonx.Value, error) {
	return c.postHashes(ctx, "/version_files", hashes, func(chunk []string) any {
		return struct {
			Hashes    []string `json:"hashes"`
			Algorithm string   `json:"algorithm"`
		}{chunk, algorithm}
	})
}

// LatestFromHashes looks up the newest version matching f of the project of each
// file (POST /version_files/update), 100 hashes per request. Keyed like
// VersionsFromHashes.
func (c *Client) LatestFromHashes(ctx context.Context, hashes []string, algorithm string, f UpdateFilter) (map[string]jsonx.Value, error) {
	return c.postHashes(ctx, "/version_files/update", hashes, func(chunk []string) any {
		return struct {
			Hashes    []string `json:"hashes"`
			Algorithm string   `json:"algorithm"`
			UpdateFilter
		}{chunk, algorithm, f}
	})
}

// postHashes sends the deduplicated hashes in chunks, one request after another,
// and merges the answers (objects keyed by hash).
func (c *Client) postHashes(ctx context.Context, path string, hashes []string, body func([]string) any) (map[string]jsonx.Value, error) {
	hashes = dedupe(hashes)
	out := map[string]jsonx.Value{}
	for start := 0; start < len(hashes); start += maxHashesPerRequest {
		chunk := hashes[start:min(start+maxHashesPerRequest, len(hashes))]
		resp, err := c.Do(ctx, Request{Method: http.MethodPost, Path: path, JSON: body(chunk), Idempotent: true})
		if err != nil {
			return nil, err
		}
		v, err := requireJSON(resp)
		if err != nil {
			return nil, err
		}
		if v.Kind != jsonx.Object {
			e := apperr.New(apperr.UnexpectedResponse, "the API did not return an object keyed by hash")
			e.HTTPStatus, e.BodyExcerpt = resp.Status, excerpt(resp.Body)
			return nil, e
		}
		for _, m := range v.Members {
			out[strings.ToLower(m.Key)] = m.Value
		}
	}
	return out, nil
}
