// Package commands defines every mdr command and the command tree returned by Root.
package commands

import "github.com/s8a-uk/modrinth-cli/internal/cli"

// Root returns the command tree. Each call builds a new tree so tests stay independent.
func Root() *cli.Command {
	return &cli.Command{
		Name:    "mdr",
		Summary: "mdr: command-line client for the Modrinth API",
		Sub: []*cli.Command{
			searchCmd(),
			projectCmd(),
			versionCmd(),
			fileCmd(),
			userCmd(),
			{Name: "whoami", AliasOf: "user me"},
			teamCmd(),
			tagCmd(),
			statsCmd(),
			authCmd(),
			configCmd(),
			apiCmd(),
			completionCmd(),
			schemaCmd(),
		},
	}
}
