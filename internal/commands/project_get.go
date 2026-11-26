package commands

import (
	"context"

	"github.com/s8a-uk/modrinth-cli/internal/apperr"
	"github.com/s8a-uk/modrinth-cli/internal/cli"
	"github.com/s8a-uk/modrinth-cli/internal/output"
	"github.com/s8a-uk/modrinth-cli/internal/validate"
)

func projectGetCmd() *cli.Command {
	return &cli.Command{
		Name:    "get",
		Summary: "Get one project by id or slug",
		Args: []cli.Arg{
			{Name: "id", Required: true, Help: "project id (8-char base62) or slug", Validate: validate.ProjectRef},
		},
		Endpoints: []cli.Endpoint{{Method: "GET", Path: "/project/{id|slug}"}},
		Output:    projectSpec(),
		Examples:  []string{"mdr project get sodium", "mdr project get sodium --full"},
		Errors:    []apperr.Code{apperr.NotFound},
		Run:       runProjectGet,
	}
}

func runProjectGet(ctx context.Context, inv *cli.Invocation) (*output.Result, error) {
	v, err := inv.Client.GetProject(ctx, inv.Arg("id"))
	if err != nil {
		return nil, err
	}
	return &output.Result{Shape: output.Object, Value: v}, nil
}
