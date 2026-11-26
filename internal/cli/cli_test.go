package cli_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/s8a-uk/modrinth-cli/internal/apperr"
	"github.com/s8a-uk/modrinth-cli/internal/cli"
	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
	"github.com/s8a-uk/modrinth-cli/internal/output"
	"github.com/s8a-uk/modrinth-cli/internal/testutil"
)

func noop(context.Context, *cli.Invocation) (*output.Result, error) { return nil, nil }

// fakeRoot is a small command tree shaped like the real one.
func fakeRoot() *cli.Command {
	projectSpec := &output.Spec{Shape: output.Object, Fields: []string{"id", "slug", "title", "license.id"},
		DefaultFields: []string{"id", "slug"}}
	return &cli.Command{
		Name:    "mdr",
		Summary: "mdr: test tree",
		Sub: []*cli.Command{
			{Name: "search", Summary: "Search projects",
				Args:      []cli.Arg{{Name: "query", Help: "search text"}},
				Flags:     []cli.Flag{{Name: "loader", Kind: cli.KindStrings, Help: "filter by loader (repeatable)"}},
				Endpoints: []cli.Endpoint{{Method: "GET", Path: "/search"}},
				Output:    &output.Spec{Shape: output.SearchPage, Fields: []string{"slug"}},
				Examples:  []string{"mdr search sodium"}, Run: noop},
			{Name: "project", Summary: "Projects", Sub: []*cli.Command{
				{Name: "get", Summary: "Get one project by id or slug",
					Args:      []cli.Arg{{Name: "id", Required: true, Help: "project id or slug"}},
					Endpoints: []cli.Endpoint{{Method: "GET", Path: "/project/{id|slug}"}},
					Output:    projectSpec, Examples: []string{"mdr project get sodium"}, Run: noop},
				{Name: "get-many", Summary: "Get several projects",
					Args:   []cli.Arg{{Name: "ids", Required: true, Variadic: true, Help: "project ids or slugs"}},
					Output: &output.Spec{Shape: output.Array}, Examples: []string{"mdr project get-many a b"}, Run: noop},
				{Name: "delete", Summary: "Delete a project", Aliases: []string{"rm"},
					Args:      []cli.Arg{{Name: "id", Required: true, Help: "project id or slug"}},
					Flags:     []cli.Flag{{Name: "reason", Short: "r", Kind: cli.KindString, Help: "why"}},
					Endpoints: []cli.Endpoint{{Method: "DELETE", Path: "/project/{id|slug}", Scope: "PROJECT_DELETE"}},
					Mutation:  cli.Destructive, Output: &output.Spec{Shape: output.Ack},
					Examples: []string{"mdr project delete my-mod --yes"}, Errors: []apperr.Code{apperr.NotFound}, Run: noop},
				{Name: "search", AliasOf: "search"},
			}},
			{Name: "version", Summary: "Versions", Sub: []*cli.Command{
				{Name: "list", Aliases: []string{"ls"}, Summary: "List versions of a project",
					Args:     []cli.Arg{{Name: "project", Required: true}},
					Flags:    []cli.Flag{{Name: "limit", Kind: cli.KindInt, Default: "20", Help: "max items"}},
					Output:   &output.Spec{Shape: output.Array},
					Examples: []string{"mdr version list sodium"}, Run: noop},
			}},
			{Name: "user", Summary: "Users", Sub: []*cli.Command{
				{Name: "me", Summary: "Show the authenticated user", Output: &output.Spec{Shape: output.Object},
					Examples: []string{"mdr user me"}, Run: noop},
			}},
			{Name: "whoami", AliasOf: "user me"},
			{Name: "secret", Summary: "Hidden command", Hidden: true, Output: &output.Spec{Shape: output.Raw},
				Examples: []string{"mdr secret"}, Run: noop},
			{Name: "schema", Aliases: []string{"describe"}, Summary: "Describe commands as JSON",
				Args:   []cli.Arg{{Name: "command", Variadic: true}},
				Output: &output.Spec{Shape: output.Raw}, Examples: []string{"mdr schema"}, Run: noop},
		},
	}
}

func TestFakeTreeIsValid(t *testing.T) {
	if err := cli.Check(fakeRoot()); err != nil {
		t.Fatal(err)
	}
}

func split(s string) []string { return strings.Fields(s) }

// TestParseBoundaryCases is the boundary-case table of the CLI framework design.
func TestParseBoundaryCases(t *testing.T) {
	type want struct {
		path    string
		args    map[string][]string
		fields  []string
		output  string
		full    bool
		loaders []string
		verbose int
		help    bool
	}
	cases := []struct {
		in      string
		want    want
		errCode apperr.Code
		errMsg  string // substring of the message
	}{
		{in: "project get sodium --fields id",
			want: want{path: "project get", args: map[string][]string{"id": {"sodium"}}, fields: []string{"id"}}},
		{in: "--fields id project get sodium",
			want: want{path: "project get", args: map[string][]string{"id": {"sodium"}}, fields: []string{"id"}}},
		{in: "project --fields id get sodium",
			want: want{path: "project get", args: map[string][]string{"id": {"sodium"}}, fields: []string{"id"}}},
		{in: "project get --fields id,slug sodium -o json",
			want: want{path: "project get", args: map[string][]string{"id": {"sodium"}}, fields: []string{"id", "slug"}, output: "json"}},
		{in: "project get-many a b c --full",
			want: want{path: "project get-many", args: map[string][]string{"ids": {"a", "b", "c"}}, full: true}},
		{in: "search",
			want: want{path: "search", args: map[string][]string{}}},
		{in: "search --loader fabric --loader quilt sodium",
			want: want{path: "search", args: map[string][]string{"query": {"sodium"}}, loaders: []string{"fabric", "quilt"}}},
		{in: "project get -- --weird",
			want: want{path: "project get", args: map[string][]string{"id": {"--weird"}}}},
		{in: "project get", errCode: apperr.Usage, errMsg: "missing argument <id>"},
		{in: "project get a b", errCode: apperr.Usage, errMsg: `unexpected argument "b"`},
		{in: "project frobnicate", errCode: apperr.Usage, errMsg: `unknown command "frobnicate" for "mdr project"`},
		{in: "project get sodium --nope", errCode: apperr.Usage, errMsg: `unknown flag "--nope" for "mdr project get"`},
		{in: "project get sodium --fields", errCode: apperr.Usage, errMsg: "flag --fields needs a value"},
		{in: "-vv project get sodium",
			want: want{path: "project get", args: map[string][]string{"id": {"sodium"}}, verbose: 2}},
		{in: "-q -v project get sodium", errCode: apperr.Usage, errMsg: "--quiet"},
		{in: "project", want: want{path: "project", help: false}},
		{in: "version ls sodium",
			want: want{path: "version list", args: map[string][]string{"project": {"sodium"}}}},
		{in: "whoami", want: want{path: "user me", args: map[string][]string{}}},
		{in: "help project get", want: want{path: "project get", help: true}},
		{in: "help --json", want: want{path: "schema", args: map[string][]string{}}},
		{in: "describe", want: want{path: "schema", args: map[string][]string{}}},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			p, err := cli.Parse(fakeRoot(), split(tc.in))
			if tc.errCode != "" {
				var ae *apperr.Error
				if !errors.As(err, &ae) {
					t.Fatalf("err = %v, want %s", err, tc.errCode)
				}
				if ae.Code != tc.errCode || !strings.Contains(ae.Message, tc.errMsg) {
					t.Fatalf("err = %s %q, want %s containing %q", ae.Code, ae.Message, tc.errCode, tc.errMsg)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			w := tc.want
			if got := strings.Join(p.Path, " "); got != w.path {
				t.Errorf("path = %q, want %q", got, w.path)
			}
			inv := p.Invocation()
			if w.args != nil {
				got := map[string][]string{}
				for _, a := range p.Cmd.Args {
					if v := inv.ArgList(a.Name); len(v) > 0 {
						got[a.Name] = v
					}
				}
				if !reflect.DeepEqual(got, w.args) {
					t.Errorf("args = %v, want %v", got, w.args)
				}
			}
			g := p.Globals
			if !reflect.DeepEqual(g.Fields, w.fields) {
				t.Errorf("fields = %#v, want %#v", g.Fields, w.fields)
			}
			wantOutput := w.output
			if wantOutput == "" {
				wantOutput = "auto"
			}
			if g.Output != wantOutput {
				t.Errorf("output = %q, want %q", g.Output, wantOutput)
			}
			if g.Full != w.full {
				t.Errorf("full = %t, want %t", g.Full, w.full)
			}
			if got := inv.Strings("loader"); !reflect.DeepEqual(got, w.loaders) {
				t.Errorf("loader = %v, want %v", got, w.loaders)
			}
			if g.Verbose != w.verbose {
				t.Errorf("verbose = %d, want %d", g.Verbose, w.verbose)
			}
			if g.Help != w.help {
				t.Errorf("help = %t, want %t", g.Help, w.help)
			}
		})
	}
}

func TestParseMore(t *testing.T) {
	cases := []struct {
		in      string
		errCode apperr.Code
		errMsg  string
		hint    string
	}{
		{in: "search --loder x", errCode: apperr.Usage, errMsg: `unknown flag "--loder"`, hint: "did you mean --loader?"},
		{in: "serch", errCode: apperr.Usage, errMsg: `unknown command "serch" for "mdr"`, hint: `did you mean "search"?`},
		{in: "zzzzzz", errCode: apperr.Usage, hint: "see: mdr --help"},
		{in: "search --laoder x", errCode: apperr.Usage, hint: "did you mean --loader?"}, // transposition costs 1
		{in: "sarech", errCode: apperr.Usage, hint: `did you mean "search"?`},
		{in: "search --lod x", errCode: apperr.Usage, hint: "see: mdr search --help"}, // short input: at most 1 edit
		{in: "search --ful", errCode: apperr.Usage, hint: "did you mean --full?"},
		{in: "--timeout 0 search", errCode: apperr.InvalidInput, errMsg: `invalid value "0" for --timeout: must be between 1 and 3600`},
		{in: "search --timeout abc", errCode: apperr.InvalidInput, errMsg: `invalid value "abc" for --timeout: not an integer`},
		{in: "search -o xml", errCode: apperr.InvalidInput, errMsg: `invalid value "xml" for --output: must be one of auto, json, ndjson, table`},
		{in: "search --full=maybe", errCode: apperr.Usage, errMsg: `flag --full takes no value (got "maybe")`},
		{in: "search --full=false", errCode: apperr.Usage, errMsg: `flag --full takes no value (got "false")`},
		{in: "search --full=true", errCode: apperr.Usage, errMsg: `flag --full takes no value`},
		// Only the documented spellings: --long, -s, and one dash for one- and two-letter names.
		{in: "-output json search", errCode: apperr.Usage, errMsg: `unknown flag "-output" for "mdr"`, hint: "did you mean --output?"},
		{in: "search -loader x", errCode: apperr.Usage, errMsg: `unknown flag "-loader" for "mdr search"`, hint: "did you mean --loader?"},
		{in: "search --o json", errCode: apperr.Usage, errMsg: `unknown flag "--o"`, hint: "did you mean -o?"},
		{in: "search --vv", errCode: apperr.Usage, errMsg: `unknown flag "--vv"`, hint: "did you mean -vv?"},
		{in: "search -qy", errCode: apperr.Usage, errMsg: `unknown flag "-qy"`},
		{in: "search --timeout", errCode: apperr.Usage, errMsg: "flag --timeout needs a value"},
		{in: "search -o=xml", errCode: apperr.InvalidInput, errMsg: `invalid value "xml" for --output`},
		{in: "project delete a -r", errCode: apperr.Usage, errMsg: "flag -r needs a value"},
		{in: "project get-many", errCode: apperr.Usage, errMsg: "missing argument <ids>"},
		{in: "nope project", errCode: apperr.Usage, errMsg: `unknown command "nope"`},
		{in: "--loader x search", errCode: apperr.Usage, errMsg: `unknown flag "--loader" for "mdr"`},
		{in: "search a\x01b", errCode: apperr.InvalidInput, errMsg: `invalid input: "a\x01b" (contains a control character)`},
		{in: "search --loader fab\x7fric", errCode: apperr.InvalidInput, errMsg: "control character"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			_, err := cli.Parse(fakeRoot(), split(tc.in))
			var ae *apperr.Error
			if !errors.As(err, &ae) {
				t.Fatalf("err = %v, want %s", err, tc.errCode)
			}
			if ae.Code != tc.errCode || !strings.Contains(ae.Message, tc.errMsg) || !strings.Contains(ae.Hint, tc.hint) {
				t.Errorf("err = %s %q (hint %q), want %s %q (hint %q)", ae.Code, ae.Message, ae.Hint, tc.errCode, tc.errMsg, tc.hint)
			}
		})
	}

	p, err := cli.Parse(fakeRoot(), split("project delete x --reason gone --full --timeout=5 -y"))
	if err != nil {
		t.Fatal(err)
	}
	inv := p.Invocation()
	if inv.String("reason") != "gone" || !inv.Changed("reason") || !inv.Changed("full") || !p.Globals.Full {
		t.Errorf("reason=%q changed=%t full=%t", inv.String("reason"), inv.Changed("reason"), p.Globals.Full)
	}
	if p.Globals.Timeout != 5 || !p.Globals.Changed("timeout") || p.Globals.MaxWait != 60 || p.Globals.Changed("max-wait") || !p.Globals.Yes {
		t.Errorf("globals = %+v", p.Globals)
	}
	if inv.Arg("id") != "x" || inv.NArgs() != 1 {
		t.Errorf("id = %q, nargs = %d", inv.Arg("id"), inv.NArgs())
	}

	p, err = cli.Parse(fakeRoot(), split("version list sodium"))
	if err != nil {
		t.Fatal(err)
	}
	if n := p.Invocation().Int("limit"); n != 20 {
		t.Errorf("limit default = %d, want 20", n)
	}

	// No arguments resolves to the root (help); --version is reported anywhere.
	p, err = cli.Parse(fakeRoot(), nil)
	if err != nil || p.Cmd.IsLeaf() || len(p.Path) != 0 {
		t.Errorf("no args: %v %v", p, err)
	}
	p, err = cli.Parse(fakeRoot(), split("--version"))
	if err != nil || !p.Globals.Version {
		t.Errorf("--version: %v %v", p, err)
	}
	// A value may start with a dash; "-" is a positional argument; "--" ends the flags.
	p, err = cli.Parse(fakeRoot(), split("project delete -r -x - "))
	if err != nil || p.Invocation().String("reason") != "-x" || p.Invocation().Arg("id") != "-" {
		t.Errorf("dash values: %v %v", p, err)
	}
	p, err = cli.Parse(fakeRoot(), split("search -- --full"))
	if err != nil || p.Globals.Full || p.Invocation().Arg("query") != "--full" {
		t.Errorf("after --: %v %v", p, err)
	}
	// Help skips argument checks.
	if _, err := cli.Parse(fakeRoot(), split("project get --help")); err != nil {
		t.Errorf("project get --help: %v", err)
	}
}

func TestCheckRejectsBadTables(t *testing.T) {
	leaf := func(mod func(c *cli.Command)) *cli.Command {
		c := &cli.Command{Name: "x", Summary: "X", Output: &output.Spec{Shape: output.Object},
			Examples: []string{"mdr x"}, Run: noop}
		mod(c)
		return &cli.Command{Name: "mdr", Summary: "root", Sub: []*cli.Command{c}}
	}
	cases := []struct {
		name string
		root *cli.Command
		want string
	}{
		{"duplicate names", &cli.Command{Name: "mdr", Summary: "r", Sub: []*cli.Command{
			{Name: "a", Summary: "A", Output: &output.Spec{}, Examples: []string{"e"}, Run: noop},
			{Name: "b", Aliases: []string{"a"}, Summary: "B", Output: &output.Spec{}, Examples: []string{"e"}, Run: noop},
		}}, `duplicate command name or alias "a"`},
		{"leaf with sub", leaf(func(c *cli.Command) { c.Sub = []*cli.Command{{Name: "y"}} }), "must not have Sub"},
		{"non-leaf with flags", leaf(func(c *cli.Command) {
			c.Run = nil
			c.Flags = []cli.Flag{{Name: "f"}}
			c.Sub = []*cli.Command{{Name: "y", Summary: "Y", Output: &output.Spec{}, Examples: []string{"e"}, Run: noop}}
		}), "must not have Args or Flags"},
		{"variadic not last", leaf(func(c *cli.Command) {
			c.Args = []cli.Arg{{Name: "a", Variadic: true}, {Name: "b"}}
		}), "only the last argument may be variadic"},
		{"required after optional", leaf(func(c *cli.Command) {
			c.Args = []cli.Arg{{Name: "a"}, {Name: "b", Required: true}}
		}), "follows an optional one"},
		{"global flag collision", leaf(func(c *cli.Command) { c.Flags = []cli.Flag{{Name: "output"}} }), "collides with a global flag"},
		{"global short collision", leaf(func(c *cli.Command) { c.Flags = []cli.Flag{{Name: "other", Short: "o"}} }), "collides with another flag"},
		{"long short", leaf(func(c *cli.Command) { c.Flags = []cli.Flag{{Name: "other", Short: "ab"}} }), "must be one character"},
		{"empty enum", leaf(func(c *cli.Command) { c.Flags = []cli.Flag{{Name: "e", Kind: cli.KindEnum}} }), "has no values"},
		{"empty summary", leaf(func(c *cli.Command) { c.Summary = "" }), "empty summary"},
		{"long summary", leaf(func(c *cli.Command) { c.Summary = strings.Repeat("x", 61) }), "summary is 61 characters"},
		{"no examples", leaf(func(c *cli.Command) { c.Examples = nil }), "at least one example"},
		{"no output", leaf(func(c *cli.Command) { c.Output = nil }), "needs Output"},
		{"default not in fields", leaf(func(c *cli.Command) {
			c.Output = &output.Spec{Shape: output.Object, Fields: []string{"id"}, DefaultFields: []string{"slug"}}
		}), `default field "slug" is not in Fields`},
		{"mutation without scope", leaf(func(c *cli.Command) { c.Mutation = cli.Write }), "needs an endpoint with a Scope"},
		{"bad alias", &cli.Command{Name: "mdr", Summary: "r", Sub: []*cli.Command{{Name: "w", AliasOf: "user me"}}},
			`alias target "user me" does not exist`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := cli.Check(tc.root)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Check() = %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

func encode(t *testing.T, v jsonx.Value) string {
	t.Helper()
	var b bytes.Buffer
	if err := jsonx.Encode(&b, v, "  "); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestHelpText(t *testing.T) {
	root := fakeRoot()
	for _, path := range []string{"", "project", "project get", "project delete", "search"} {
		p := split(path)
		cmd := cli.Find(root, p)
		name := strings.ReplaceAll(path, " ", "_")
		if name == "" {
			name = "root"
		}
		testutil.Golden(t, "help/"+name+".stdout", cli.HelpText(root, cmd, p))
	}
}

func TestSchema(t *testing.T) {
	root := fakeRoot()
	testutil.Golden(t, "schema/tree.stdout", encode(t, cli.SchemaTree(root, "1.2.3")))
	for _, path := range []string{"project get", "project delete", "version ls", "user me", "project"} {
		v, err := cli.SchemaCommand(root, split(path))
		if err != nil {
			t.Fatalf("SchemaCommand(%q): %v", path, err)
		}
		testutil.Golden(t, "schema/"+strings.ReplaceAll(path, " ", "_")+".stdout", encode(t, v))
	}
	for _, path := range []string{"nope", "project nope", ""} {
		_, err := cli.SchemaCommand(root, split(path))
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.Usage {
			t.Errorf("SchemaCommand(%q) error = %v, want usage", path, err)
		}
	}
	var got []string
	for _, p := range cli.LeafPaths(root) {
		got = append(got, strings.Join(p, " "))
	}
	want := []string{"search", "project get", "project get-many", "project delete", "version list", "user me", "schema"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("LeafPaths = %v, want %v", got, want)
	}
}

func TestCompletion(t *testing.T) {
	root := fakeRoot()
	for _, sh := range cli.Shells {
		s, err := cli.Completion(root, sh)
		if err != nil {
			t.Fatalf("%s: %v", sh, err)
		}
		testutil.Golden(t, "completion/"+sh, s)
	}
	if _, err := cli.Completion(root, "tcsh"); err == nil {
		t.Error("tcsh: no error")
	}
}

// TestCompletionBash runs the bash script against command lines.
func TestCompletionBash(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil || runtime.GOOS == "windows" {
		t.Skip("needs bash (not run on Windows)")
	}
	root := fakeRoot()
	root.Sub[1].Sub[0].Args[0].Values = []string{"sodium", "lithium"} // project get <id>
	s, err := cli.Completion(root, "bash")
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "mdr.bash")
	if err := os.WriteFile(script, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ line, want string }{
		{"mdr pro", "project"},
		{"mdr project g", "get get-many"},
		{"mdr project ''", "get get-many delete search"},
		{"mdr -o json project get ''", "sodium lithium"},
		{"mdr --output ''", "auto json ndjson table"},
		{"mdr version ls sodium --li", "--limit"},
		{"mdr project rm x -", "--reason -r --output -o --fields --full --staging --dry-run --yes -y --timeout --max-wait --no-retry --verbose -v -vv --quiet -q --no-color --help -h --version"},
		{"mdr whoami --st", "--staging"},
		{"mdr search --loader ''", ""}, // a free value: file names (none in the empty directory)
	}
	for _, tc := range cases {
		cmd := exec.Command(bash, "--norc", "-c", `source "$1"; eval "COMP_WORDS=($2)"; COMP_CWORD=$((${#COMP_WORDS[@]}-1)); _mdr; echo "${COMPREPLY[*]}"`, "_", script, tc.line)
		cmd.Dir = t.TempDir()
		out, err := cmd.CombinedOutput()
		if got := strings.TrimSpace(string(out)); err != nil || got != tc.want {
			t.Errorf("%s: got %q (%v), want %q", tc.line, got, err, tc.want)
		}
	}
}
