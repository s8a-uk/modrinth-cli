package api

import (
	"context"
	"fmt"

	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
)

// Tags fetches a tag list: name is the path under /tag, e.g. "game_version".
func (c *Client) Tags(ctx context.Context, name string) (jsonx.Value, error) {
	return c.getJSON(ctx, Request{Path: "/tag/" + name})
}

// License fetches the text of a license by SPDX id.
func (c *Client) License(ctx context.Context, id string) (jsonx.Value, error) {
	return c.getJSON(ctx, Request{
		Path:         "/tag/license/{id}",
		PathArgs:     []string{id},
		Resource:     fmt.Sprintf("license '%s'", id),
		NotFoundHint: "list license ids: mdr tag list license",
	})
}

// Statistics fetches the site-wide counts.
func (c *Client) Statistics(ctx context.Context) (jsonx.Value, error) {
	return c.getJSON(ctx, Request{Path: "/statistics"})
}
