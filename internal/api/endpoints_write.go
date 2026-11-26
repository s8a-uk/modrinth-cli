package api

import (
	"bytes"
	"context"
	"fmt"
	"net/http"

	"github.com/s8a-uk/modrinth-cli/internal/apperr"
	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
	"github.com/s8a-uk/modrinth-cli/internal/validate"
)

// Token scopes of the write endpoints (named in 401 hints).
const (
	ScopeVersionCreate = "VERSION_CREATE"
	ScopeVersionWrite  = "VERSION_WRITE"
	ScopeVersionDelete = "VERSION_DELETE"
	ScopeProjectCreate = "PROJECT_CREATE"
	ScopeProjectWrite  = "PROJECT_WRITE"
	ScopeProjectDelete = "PROJECT_DELETE"
)

// Upload is a local file to send in a multipart request.
type Upload struct {
	Path string
	Size int64
}

// FileParts returns the parts of files, named FilePartName(0), FilePartName(1), ...
func FileParts(files []Upload) []Part {
	parts := make([]Part, len(files))
	for i, f := range files {
		parts[i] = FilePart(FilePartName(i), f.Path, f.Size)
	}
	return parts
}

// FilePartNames returns the names FileParts gives n files (data.file_parts).
func FilePartNames(n int) []string {
	names := make([]string, n)
	for i := range names {
		names[i] = FilePartName(i)
	}
	return names
}

// DataPart is the "data" part: the JSON metadata of a create request.
func DataPart(data jsonx.Value) Part {
	var b bytes.Buffer
	_ = jsonx.Encode(&b, data, "")
	return Part{Name: "data", ContentType: "application/json", Data: bytes.TrimSuffix(b.Bytes(), []byte("\n"))}
}

// CreateVersion uploads a new version: POST /version with the data part followed
// by the files. data must already list the file part names in file_parts.
func (c *Client) CreateVersion(ctx context.Context, data jsonx.Value, files []Upload, progress func(done, total int64)) (jsonx.Value, error) {
	return c.getJSON(ctx, Request{
		Method:      http.MethodPost,
		Path:        "/version",
		Multipart:   &Multipart{Parts: append([]Part{DataPart(data)}, FileParts(files)...), Progress: progress},
		Scope:       ScopeVersionCreate,
		Resource:    "project",
		LongRunning: true,
	})
}

// AddVersionFiles uploads more files to a version: POST /version/{id}/file.
func (c *Client) AddVersionFiles(ctx context.Context, id string, files []Upload, progress func(done, total int64)) error {
	data := jsonx.Obj(jsonx.Member{Key: "file_parts", Value: jsonx.Strs(FilePartNames(len(files)))})
	_, err := c.Do(ctx, Request{
		Method:      http.MethodPost,
		Path:        "/version/{id}/file",
		PathArgs:    []string{id},
		Multipart:   &Multipart{Parts: append([]Part{DataPart(data)}, FileParts(files)...), Progress: progress},
		Scope:       ScopeVersionWrite,
		Resource:    fmt.Sprintf("version '%s'", id),
		LongRunning: true,
	})
	return err
}

// EditVersion changes the fields in body: PATCH /version/{id}.
func (c *Client) EditVersion(ctx context.Context, id string, body jsonx.Value) error {
	_, err := c.Do(ctx, Request{
		Method:   http.MethodPatch,
		Path:     "/version/{id}",
		PathArgs: []string{id},
		JSON:     body,
		Scope:    ScopeVersionWrite,
		Resource: fmt.Sprintf("version '%s'", id),
	})
	return err
}

// DeleteVersion deletes a version: DELETE /version/{id}.
func (c *Client) DeleteVersion(ctx context.Context, id string) error {
	_, err := c.Do(ctx, Request{
		Method:   http.MethodDelete,
		Path:     "/version/{id}",
		PathArgs: []string{id},
		Scope:    ScopeVersionDelete,
		Resource: fmt.Sprintf("version '%s'", id),
	})
	return err
}

// CreateProject creates a project: POST /project with the data part and an
// optional icon part.
func (c *Client) CreateProject(ctx context.Context, data jsonx.Value, icon *Upload, progress func(done, total int64)) (jsonx.Value, error) {
	parts := []Part{DataPart(data)}
	if icon != nil {
		parts = append(parts, FilePart("icon", icon.Path, icon.Size))
	}
	return c.getJSON(ctx, Request{
		Method:      http.MethodPost,
		Path:        "/project",
		Multipart:   &Multipart{Parts: parts, Progress: progress},
		Scope:       ScopeProjectCreate,
		LongRunning: true,
	})
}

// EditProject changes the fields in body: PATCH /project/{id}.
func (c *Client) EditProject(ctx context.Context, idOrSlug string, body jsonx.Value) error {
	r := projectRequest("/project/{id}", idOrSlug)
	r.Method, r.JSON, r.Scope = http.MethodPatch, body, ScopeProjectWrite
	_, err := c.Do(ctx, r)
	return err
}

// DeleteProject deletes a project: DELETE /project/{id}.
func (c *Client) DeleteProject(ctx context.Context, idOrSlug string) error {
	r := projectRequest("/project/{id}", idOrSlug)
	r.Method, r.Scope = http.MethodDelete, ScopeProjectDelete
	_, err := c.Do(ctx, r)
	return err
}

// ResolveProjectID returns the id of a project given by id or slug. An 8-character
// base62 value is taken to be an id and returned as is; anything else is looked up
// with GET /project/{slug}/check, which is sent even in a dry run.
func (c *Client) ResolveProjectID(ctx context.Context, idOrSlug string) (string, error) {
	if validate.Base62ID(idOrSlug) == nil {
		return idOrSlug, nil
	}
	r := projectRequest("/project/{id}/check", idOrSlug)
	r.SendInDryRun = true
	v, err := c.getJSON(ctx, r)
	if err != nil {
		return "", err
	}
	id, _ := v.Get("id")
	if id.Kind != jsonx.String || id.Str == "" {
		return "", apperr.New(apperr.UnexpectedResponse, "the project check response has no id")
	}
	return id.Str, nil
}
