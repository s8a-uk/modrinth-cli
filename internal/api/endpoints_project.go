package api

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	"github.com/s8a-uk/modrinth-cli/internal/apperr"
	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
)

// GetProject fetches one project by id or slug.
func (c *Client) GetProject(ctx context.Context, idOrSlug string) (jsonx.Value, error) {
	return c.getJSON(ctx, projectRequest("/project/{id}", idOrSlug))
}

// requireJSON returns the parsed body, or unexpected_response if it is not JSON.
func requireJSON(resp *Response) (jsonx.Value, error) {
	if !resp.IsJSON {
		e := apperr.New(apperr.UnexpectedResponse, "the API did not return JSON")
		e.HTTPStatus, e.BodyExcerpt = resp.Status, excerpt(resp.Body)
		return jsonx.Value{}, e
	}
	return resp.Value, nil
}

// GetProjects fetches projects by id or slug (GET /projects?ids=[...]), in input order.
func (c *Client) GetProjects(ctx context.Context, refs []string) (*BulkResult, error) {
	return c.getBulk(ctx, "/projects", refs, projectKeys)
}

// RandomProjects fetches count random projects.
func (c *Client) RandomProjects(ctx context.Context, count int) (jsonx.Value, error) {
	return c.getJSON(ctx, Request{Path: "/projects_random", Query: url.Values{"count": {strconv.Itoa(count)}}})
}

// CheckProject resolves an id or slug to {"id": ...}.
func (c *Client) CheckProject(ctx context.Context, idOrSlug string) (jsonx.Value, error) {
	return c.getJSON(ctx, projectRequest("/project/{id}/check", idOrSlug))
}

// ProjectDependencies fetches {"projects":[...],"versions":[...]} of a project.
func (c *Client) ProjectDependencies(ctx context.Context, idOrSlug string) (jsonx.Value, error) {
	return c.getJSON(ctx, projectRequest("/project/{id}/dependencies", idOrSlug))
}

// ProjectMembers fetches the team members of a project.
func (c *Client) ProjectMembers(ctx context.Context, idOrSlug string) (jsonx.Value, error) {
	return c.getJSON(ctx, projectRequest("/project/{id}/members", idOrSlug))
}

// projectRequest is a GET of a path under /project/{id} with the project 404 message.
func projectRequest(path, idOrSlug string) Request {
	return Request{
		Path:         path,
		PathArgs:     []string{idOrSlug},
		Resource:     fmt.Sprintf("project '%s'", idOrSlug),
		NotFoundHint: "check the id or slug; try: mdr search " + idOrSlug,
	}
}

// getJSON sends r and returns the JSON body.
func (c *Client) getJSON(ctx context.Context, r Request) (jsonx.Value, error) {
	resp, err := c.Do(ctx, r)
	if err != nil {
		return jsonx.Value{}, err
	}
	return requireJSON(resp)
}
