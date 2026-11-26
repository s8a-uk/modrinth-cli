package commands_test

import (
	"strings"
	"testing"

	"github.com/s8a-uk/modrinth-cli/internal/cli"
	"github.com/s8a-uk/modrinth-cli/internal/commands"
	"github.com/s8a-uk/modrinth-cli/internal/testutil"
)

func TestRegistryValid(t *testing.T) {
	if err := cli.Check(commands.Root()); err != nil {
		t.Fatal(err)
	}
}

// TestSchemaGolden pins the machine-readable contract and the help text. A change
// to default fields or exit codes fails here on purpose.
func TestSchemaGolden(t *testing.T) {
	cases := []struct {
		golden string
		args   []string
	}{
		{"schema/tree.stdout", []string{"schema"}},
		{"schema/all.stdout", []string{"schema", "--all"}},
		{"schema/schema.stdout", []string{"schema", "schema"}},
		{"help/root.stdout", []string{"--help"}},
		{"help/schema.stdout", []string{"schema", "--help"}},
		{"help/project.stdout", []string{"project"}},
		{"help/project_get.stdout", []string{"project", "get", "--help"}},
		{"help/api.stdout", []string{"help", "api"}},
		{"schema/project_get.stdout", []string{"schema", "project", "get"}},
		{"help/search.stdout", []string{"search", "--help"}},
		{"schema/search.stdout", []string{"schema", "search"}},
	}
	for _, tc := range cases {
		t.Run(tc.golden, func(t *testing.T) {
			r := testutil.Run(t, nil, testutil.RunOpts{}, tc.args...)
			if r.Code != 0 || r.Stderr != "" {
				t.Fatalf("exit %d, stderr %q", r.Code, r.Stderr)
			}
			testutil.Golden(t, tc.golden, r.Stdout)
		})
	}
}

// leafPathsUsingNetwork lists every visible leaf that is neither Offline nor Local.
func leafPathsUsingNetwork() []string {
	root := commands.Root()
	var out []string
	for _, p := range cli.LeafPaths(root) {
		if c := cli.Find(root, p); !c.Offline && !c.Local {
			out = append(out, strings.Join(p, " "))
		}
	}
	return out
}
