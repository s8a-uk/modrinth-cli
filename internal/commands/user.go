package commands

import (
	"context"
	"fmt"

	"github.com/s8a-uk/modrinth-cli/internal/api"
	"github.com/s8a-uk/modrinth-cli/internal/apperr"
	"github.com/s8a-uk/modrinth-cli/internal/cli"
	"github.com/s8a-uk/modrinth-cli/internal/config"
	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
	"github.com/s8a-uk/modrinth-cli/internal/output"
	"github.com/s8a-uk/modrinth-cli/internal/validate"
)

func userCmd() *cli.Command {
	return &cli.Command{
		Name:    "user",
		Summary: "Read users",
		Sub: []*cli.Command{
			userMeCmd(),
			userGetCmd(),
			userGetManyCmd(),
			userProjectsCmd(),
		},
	}
}

func userRefArg() cli.Arg {
	return cli.Arg{Name: "user", Required: true, Help: "user id or username", Validate: validate.UserRef}
}

func userMeCmd() *cli.Command {
	return &cli.Command{
		Name:      "me",
		Summary:   "Show the user the token belongs to",
		Long:      "Needs a token (MODRINTH_TOKEN or mdr auth login); without one it fails with auth_required before sending anything.",
		Endpoints: []cli.Endpoint{{Method: "GET", Path: "/user", Scope: api.ScopeUserRead}},
		Output:    userMeSpec(),
		Examples:  []string{"mdr user me", "mdr whoami --fields username"},
		Errors:    []apperr.Code{apperr.AuthRequired, apperr.Unauthorized},
		Run: func(ctx context.Context, inv *cli.Invocation) (*output.Result, error) {
			// Without a token the client fails with auth_required before sending
			// (GET /user has a scope); with --dry-run it prints the plan.
			v, err := inv.Client.GetMe(ctx)
			if err != nil {
				return nil, err
			}
			return &output.Result{Shape: output.Object, Value: v}, nil
		},
	}
}

// MissingToken is the auth_required error for a request that needs a token when
// none is configured; scope, if known, is named in the hint.
func MissingToken(staging bool, scope string) *apperr.Error {
	name, fileName := config.TokenVars(staging)
	e := apperr.New(apperr.AuthRequired, "authentication required").
		WithHint("set %s (or %s), or run: mdr auth login --with-token", name, fileName)
	if scope != "" {
		e.Hint += "; required scope: " + scope
	}
	return e
}

func userGetCmd() *cli.Command {
	return &cli.Command{
		Name:      "get",
		Summary:   "Get one user by id or username",
		Args:      []cli.Arg{userRefArg()},
		Endpoints: []cli.Endpoint{{Method: "GET", Path: "/user/{id|username}"}},
		Output:    userSpec(),
		Examples:  []string{"mdr user get jellysquid3"},
		Errors:    []apperr.Code{apperr.NotFound},
		Run: func(ctx context.Context, inv *cli.Invocation) (*output.Result, error) {
			v, err := inv.Client.GetUser(ctx, inv.Arg("user"))
			if err != nil {
				return nil, err
			}
			return &output.Result{Shape: output.Object, Value: v}, nil
		},
	}
}

func userGetManyCmd() *cli.Command {
	return &cli.Command{
		Name:    "get-many",
		Summary: "Get several users by id or username",
		Long: "Fetches up to 1000 users, 100 per request. The output keeps the order of the arguments; " +
			"users that match nothing are left out and listed in a note on stderr (exit 0).",
		Args: []cli.Arg{
			{Name: "user", Required: true, Variadic: true, Help: "user ids or usernames (1-1000)", Validate: validate.UserRef},
		},
		Endpoints: []cli.Endpoint{{Method: "GET", Path: "/users"}},
		Output:    userListSpec(),
		Examples:  []string{"mdr user get-many jellysquid3 TEZXhE2U"},
		Run: func(ctx context.Context, inv *cli.Invocation) (*output.Result, error) {
			ids := inv.ArgList("user")
			if err := checkBulkCount(ids); err != nil {
				return nil, err
			}
			res, err := inv.Client.GetUsers(ctx, ids)
			if err != nil {
				return nil, err
			}
			return bulkResult(res), nil
		},
	}
}

func userProjectsCmd() *cli.Command {
	return &cli.Command{
		Name:    "projects",
		Summary: "List the projects of a user",
		Args:    []cli.Arg{userRefArg()},
		Flags: []cli.Flag{
			{Name: "limit", Kind: cli.KindInt, Default: "50", Value: "<n>", Help: "at most this many projects, 0-10000; 0 = all", Validate: intFlag(0, 10000)},
		},
		Endpoints: []cli.Endpoint{{Method: "GET", Path: "/user/{id|username}/projects"}},
		Output:    projectListSpec(),
		Examples:  []string{"mdr user projects jellysquid3"},
		Errors:    []apperr.Code{apperr.NotFound},
		Run: func(ctx context.Context, inv *cli.Invocation) (*output.Result, error) {
			v, err := inv.Client.UserProjects(ctx, inv.Arg("user"))
			if err != nil {
				return nil, err
			}
			return limitArray(v, inv.Int("limit"), "projects"), nil
		},
	}
}

// limitArray keeps the first limit items of an array result (0 = all) and notes the cut.
func limitArray(v jsonx.Value, limit int, noun string) *output.Result {
	res := &output.Result{Shape: output.Array, Value: v}
	if v.Kind == jsonx.Array && limit > 0 && len(v.Items) > limit {
		res.Notes = []string{fmt.Sprintf("showing %d of %d %s; use --limit 0 for all", limit, len(v.Items), noun)}
		res.Value = jsonx.Arr(v.Items[:limit]...)
	}
	return res
}

func teamCmd() *cli.Command {
	return &cli.Command{
		Name:    "team",
		Summary: "Read teams",
		Sub:     []*cli.Command{teamMembersCmd()},
	}
}

func teamMembersCmd() *cli.Command {
	return &cli.Command{
		Name:    "members",
		Summary: "List the members of a team",
		Long:    "Takes a team id (the team field of a project). For a project's members use: mdr project members <project>.",
		Args: []cli.Arg{
			{Name: "team-id", Required: true, Help: "team id (8-char base62)", Validate: validate.TeamID},
		},
		Endpoints: []cli.Endpoint{{Method: "GET", Path: "/team/{id}/members"}},
		Output:    memberSpec(),
		Examples:  []string{"mdr team members 4reLOAKe"},
		Errors:    []apperr.Code{apperr.NotFound},
		Run: func(ctx context.Context, inv *cli.Invocation) (*output.Result, error) {
			v, err := inv.Client.TeamMembers(ctx, inv.Arg("team-id"))
			if err != nil {
				return nil, err
			}
			return &output.Result{Shape: output.Array, Value: v}, nil
		},
	}
}
