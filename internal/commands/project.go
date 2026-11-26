package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/s8a-uk/modrinth-cli/internal/api"
	"github.com/s8a-uk/modrinth-cli/internal/apperr"
	"github.com/s8a-uk/modrinth-cli/internal/cli"
	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
	"github.com/s8a-uk/modrinth-cli/internal/output"
	"github.com/s8a-uk/modrinth-cli/internal/validate"
)

// maxBulkArgs is the most ids one get-many command takes.
const maxBulkArgs = 1000

// maxMissingShown is the number of missing ids listed in the note.
const maxMissingShown = 10

func projectCmd() *cli.Command {
	return &cli.Command{
		Name:    "project",
		Summary: "Read, create and edit projects (mods, modpacks, ...)",
		Sub: []*cli.Command{
			projectGetCmd(),
			projectGetManyCmd(),
			projectRandomCmd(),
			projectCheckCmd(),
			projectDependenciesCmd(),
			projectMembersCmd(),
			projectCreateCmd(),
			projectEditCmd(),
			projectDeleteCmd(),
			{Name: "search", AliasOf: "search"},
		},
	}
}

func projectRefArg() cli.Arg {
	return cli.Arg{Name: "id", Required: true, Help: "project id (8-char base62) or slug", Validate: validate.ProjectRef}
}

func projectGetManyCmd() *cli.Command {
	return &cli.Command{
		Name:    "get-many",
		Summary: "Get several projects by id or slug",
		Long: "Fetches up to 1000 projects, 100 per request. The output keeps the order of the arguments; " +
			"ids that match nothing are left out and listed in a note on stderr (exit 0).",
		Args: []cli.Arg{
			{Name: "id", Required: true, Variadic: true, Help: "project ids or slugs (1-1000)", Validate: validate.ProjectRef},
		},
		Endpoints: []cli.Endpoint{{Method: "GET", Path: "/projects"}},
		Output:    projectListSpec(),
		Examples:  []string{"mdr project get-many sodium lithium", "mdr project get-many AANobbMI gvQqBUqZ --fields slug,downloads"},
		Run: func(ctx context.Context, inv *cli.Invocation) (*output.Result, error) {
			ids := inv.ArgList("id")
			if err := checkBulkCount(ids); err != nil {
				return nil, err
			}
			res, err := inv.Client.GetProjects(ctx, ids)
			if err != nil {
				return nil, err
			}
			return bulkResult(res), nil
		},
	}
}

// checkBulkCount rejects more than maxBulkArgs ids.
func checkBulkCount(ids []string) error {
	if len(ids) > maxBulkArgs {
		return apperr.New(apperr.Usage, "too many ids: %d (at most %d)", len(ids), maxBulkArgs).
			WithHint("split the ids over several calls")
	}
	return nil
}

// bulkResult is the array of found elements, with a note listing the missing ids.
func bulkResult(res *api.BulkResult) *output.Result {
	out := &output.Result{Shape: output.Array, Value: jsonx.Arr(res.Found...)}
	if n := len(res.Missing); n > 0 {
		shown := res.Missing[:min(n, maxMissingShown)]
		note := fmt.Sprintf("%d of %d ids not found: %s", n, res.Total, strings.Join(shown, ", "))
		if n > maxMissingShown {
			note += fmt.Sprintf(", ... (+%d more)", n-maxMissingShown)
		}
		out.Notes = append(out.Notes, note)
	}
	return out
}

func projectRandomCmd() *cli.Command {
	return &cli.Command{
		Name:    "random",
		Summary: "Get random projects",
		Flags: []cli.Flag{
			{Name: "count", Kind: cli.KindInt, Default: "10", Value: "<n>", Help: "number of projects, 1-100", Validate: intFlag(1, 100)},
		},
		Endpoints: []cli.Endpoint{{Method: "GET", Path: "/projects_random"}},
		Output:    projectListSpec(),
		Examples:  []string{"mdr project random --count 3"},
		Run: func(ctx context.Context, inv *cli.Invocation) (*output.Result, error) {
			v, err := inv.Client.RandomProjects(ctx, inv.Int("count"))
			if err != nil {
				return nil, err
			}
			return &output.Result{Shape: output.Array, Value: v}, nil
		},
	}
}

func projectCheckCmd() *cli.Command {
	return &cli.Command{
		Name:      "check",
		Summary:   "Check that a project exists and get its id",
		Long:      "Resolves a slug (or id) to the project id. Exits 3 (not_found) when there is no such project.",
		Args:      []cli.Arg{projectRefArg()},
		Endpoints: []cli.Endpoint{{Method: "GET", Path: "/project/{id|slug}/check"}},
		Output:    &output.Spec{Shape: output.Object, Fields: []string{"id"}},
		Examples:  []string{"mdr project check sodium"},
		Errors:    []apperr.Code{apperr.NotFound},
		Run: func(ctx context.Context, inv *cli.Invocation) (*output.Result, error) {
			v, err := inv.Client.CheckProject(ctx, inv.Arg("id"))
			if err != nil {
				return nil, err
			}
			return &output.Result{Shape: output.Object, Value: v}, nil
		},
	}
}

func projectDependenciesCmd() *cli.Command {
	return &cli.Command{
		Name:    "dependencies",
		Summary: "List the projects and versions a project depends on",
		Long: "Returns {\"projects\": [...], \"versions\": [...]}: the projects the project's versions depend on, " +
			"and the specific versions they pin. Fields are projects.<project field> and versions.<version field>.",
		Args:      []cli.Arg{projectRefArg()},
		Endpoints: []cli.Endpoint{{Method: "GET", Path: "/project/{id|slug}/dependencies"}},
		Output:    dependenciesSpec(),
		Examples:  []string{"mdr project dependencies fabulously-optimized", "mdr project dependencies sodium --fields projects.slug"},
		Errors:    []apperr.Code{apperr.NotFound},
		Run: func(ctx context.Context, inv *cli.Invocation) (*output.Result, error) {
			v, err := inv.Client.ProjectDependencies(ctx, inv.Arg("id"))
			if err != nil {
				return nil, err
			}
			return &output.Result{Shape: output.Object, Value: v}, nil
		},
	}
}

func projectMembersCmd() *cli.Command {
	return &cli.Command{
		Name:      "members",
		Summary:   "List the team members of a project",
		Args:      []cli.Arg{projectRefArg()},
		Endpoints: []cli.Endpoint{{Method: "GET", Path: "/project/{id|slug}/members"}},
		Output:    memberSpec(),
		Examples:  []string{"mdr project members sodium"},
		Errors:    []apperr.Code{apperr.NotFound},
		Run: func(ctx context.Context, inv *cli.Invocation) (*output.Result, error) {
			v, err := inv.Client.ProjectMembers(ctx, inv.Arg("id"))
			if err != nil {
				return nil, err
			}
			return &output.Result{Shape: output.Array, Value: v}, nil
		},
	}
}
