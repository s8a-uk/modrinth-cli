package api

import (
	"context"
	"fmt"

	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
)

// GetUser fetches one user by id or username.
func (c *Client) GetUser(ctx context.Context, user string) (jsonx.Value, error) {
	return c.getJSON(ctx, userRequest("/user/{id}", user))
}

// ScopeUserRead is the token scope GET /user needs.
const ScopeUserRead = "USER_READ"

// GetMe fetches the user the token belongs to (GET /user).
func (c *Client) GetMe(ctx context.Context) (jsonx.Value, error) {
	return c.getJSON(ctx, Request{Path: "/user", Scope: ScopeUserRead, Resource: "user"})
}

// GetUsers fetches users by id or username (GET /users?ids=[...]), in input order.
func (c *Client) GetUsers(ctx context.Context, users []string) (*BulkResult, error) {
	return c.getBulk(ctx, "/users", users, userKeys)
}

// UserProjects fetches the projects of a user.
func (c *Client) UserProjects(ctx context.Context, user string) (jsonx.Value, error) {
	return c.getJSON(ctx, userRequest("/user/{id}/projects", user))
}

// TeamMembers fetches the members of a team.
func (c *Client) TeamMembers(ctx context.Context, team string) (jsonx.Value, error) {
	return c.getJSON(ctx, Request{
		Path:         "/team/{id}/members",
		PathArgs:     []string{team},
		Resource:     fmt.Sprintf("team '%s'", team),
		NotFoundHint: "for a project's members use: mdr project members <project>",
	})
}

func userRequest(path, user string) Request {
	return Request{
		Path:         path,
		PathArgs:     []string{user},
		Resource:     fmt.Sprintf("user '%s'", user),
		NotFoundHint: "check the user id or username",
	}
}
