package commands

import (
	"context"

	"github.com/s8a-uk/modrinth-cli/internal/apperr"
	"github.com/s8a-uk/modrinth-cli/internal/buildinfo"
	"github.com/s8a-uk/modrinth-cli/internal/cli"
	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
	"github.com/s8a-uk/modrinth-cli/internal/output"
)

func schemaCmd() *cli.Command {
	return &cli.Command{
		Name:    "schema",
		Aliases: []string{"describe"},
		Summary: "Describe commands as JSON (for agents and scripts)",
		Long: "Without arguments, lists every command in one JSON object. With a command path, " +
			"describes that command: arguments, flags, output fields and examples. " +
			"Always prints JSON, whatever --output says. Does not use the network.",
		Args: []cli.Arg{
			{Name: "command", Variadic: true, Help: "command path, e.g. \"project get\""},
		},
		Flags: []cli.Flag{
			{Name: "all", Kind: cli.KindBool, Help: "describe every command, one JSON object per line"},
		},
		Output:   &output.Spec{Shape: output.Raw},
		Examples: []string{"mdr schema", "mdr schema schema", "mdr schema --all"},
		Errors:   []apperr.Code{apperr.Usage},
		Offline:  true,
		Run:      runSchema,
	}
}

func runSchema(_ context.Context, inv *cli.Invocation) (*output.Result, error) {
	path := inv.ArgList("command")
	if inv.Bool("all") {
		if len(path) > 0 {
			return nil, apperr.New(apperr.Usage, "--all cannot be combined with a command path").
				WithHint("run either \"mdr schema --all\" or \"mdr schema <command>\"")
		}
		var items []jsonx.Value
		for _, p := range cli.LeafPaths(inv.Root) {
			v, err := cli.SchemaCommand(inv.Root, p)
			if err != nil {
				return nil, err
			}
			items = append(items, v)
		}
		return &output.Result{Shape: output.Raw, Value: jsonx.Arr(items...), Format: output.NDJSON}, nil
	}
	if len(path) == 0 {
		return &output.Result{Shape: output.Raw, Value: cli.SchemaTree(inv.Root, buildinfo.Resolved())}, nil
	}
	v, err := cli.SchemaCommand(inv.Root, path)
	if err != nil {
		return nil, err
	}
	return &output.Result{Shape: output.Raw, Value: v}, nil
}
