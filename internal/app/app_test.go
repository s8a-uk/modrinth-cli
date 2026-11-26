package app_test

import (
	"strings"
	"testing"

	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
	"github.com/s8a-uk/modrinth-cli/internal/testutil"
)

func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return lines[len(lines)-1]
}

func TestUsageErrors(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		tty    bool
		code   int
		stderr string // exact
	}{
		{name: "unknown command, json", args: []string{"nope"}, code: 2,
			stderr: `{"error":{"code":"usage","message":"unknown command \"nope\" for \"mdr\"","http_status":null,"retryable":false,"hint":"see: mdr --help"}}` + "\n"},
		{name: "unknown command, tty", args: []string{"nope"}, tty: true, code: 2,
			stderr: "error: unknown command \"nope\" for \"mdr\" (usage)\nhint: see: mdr --help\n"},
		{name: "near miss suggests", args: []string{"shema"}, tty: true, code: 2,
			stderr: "error: unknown command \"shema\" for \"mdr\" (usage)\nhint: did you mean \"schema\"? see: mdr --help\n"},
		{name: "tty but -o json gives json", args: []string{"nope", "-o", "json"}, tty: true, code: 2,
			stderr: `{"error":{"code":"usage","message":"unknown command \"nope\" for \"mdr\"","http_status":null,"retryable":false,"hint":"see: mdr --help"}}` + "\n"},
		{name: "non-tty but --output=table gives human", args: []string{"--output=table", "nope"}, code: 2,
			stderr: "error: unknown command \"nope\" for \"mdr\" (usage)\nhint: see: mdr --help\n"},
		{name: "unknown flag", args: []string{"schema", "--nope"}, code: 2,
			stderr: `{"error":{"code":"usage","message":"unknown flag \"--nope\" for \"mdr schema\"","http_status":null,"retryable":false,"hint":"see: mdr schema --help"}}` + "\n"},
		{name: "invalid output", args: []string{"schema", "-o", "xml"}, code: 2,
			stderr: `{"error":{"code":"invalid_input","message":"invalid value \"xml\" for --output: must be one of auto, json, ndjson, table","http_status":null,"retryable":false}}` + "\n"},
		{name: "fields with full", args: []string{"schema", "--fields", "a", "--full"}, code: 2,
			stderr: `{"error":{"code":"usage","message":"--fields and --full cannot be used together","http_status":null,"retryable":false}}` + "\n"},
		{name: "fields on raw", args: []string{"schema", "--fields", "a"}, tty: true, code: 2,
			stderr: "error: --fields is not supported by this command (usage)\nhint: see: mdr schema schema\n"},
		{name: "quiet and verbose", args: []string{"-q", "-v", "schema"}, code: 2,
			stderr: `{"error":{"code":"usage","message":"--quiet cannot be combined with -v or -vv","http_status":null,"retryable":false}}` + "\n"},
		{name: "unknown schema path", args: []string{"schema", "nope"}, code: 2,
			stderr: `{"error":{"code":"usage","message":"unknown command \"nope\"","http_status":null,"retryable":false,"hint":"see: mdr schema"}}` + "\n"},
		{name: "schema --all with path", args: []string{"schema", "--all", "schema"}, code: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := testutil.Run(t, nil, testutil.RunOpts{StdoutTTY: tc.tty}, tc.args...)
			if r.Code != tc.code {
				t.Errorf("exit code = %d, want %d", r.Code, tc.code)
			}
			if r.Stdout != "" {
				t.Errorf("stdout = %q, want empty", r.Stdout)
			}
			if tc.stderr != "" && r.Stderr != tc.stderr {
				t.Errorf("stderr\n got: %q\nwant: %q", r.Stderr, tc.stderr)
			}
			if !strings.HasPrefix(r.Stderr, "error: ") && !strings.HasPrefix(lastLine(r.Stderr), `{"error":{`) {
				t.Errorf("last stderr line is not an error object: %q", r.Stderr)
			}
		})
	}
}

func TestHelpAndVersionExitZero(t *testing.T) {
	for _, args := range [][]string{nil, {"--help"}, {"-h"}, {"help"}, {"schema", "--help"}, {"help", "schema"}, {"--version"}} {
		r := testutil.Run(t, nil, testutil.RunOpts{}, args...)
		if r.Code != 0 || r.Stdout == "" || r.Stderr != "" {
			t.Errorf("%v: code %d, stdout %d bytes, stderr %q", args, r.Code, len(r.Stdout), r.Stderr)
		}
	}
	r := testutil.Run(t, nil, testutil.RunOpts{}, "--version")
	if !strings.HasPrefix(r.Stdout, "mdr ") || strings.Count(r.Stdout, "\n") != 1 {
		t.Errorf("--version stdout = %q", r.Stdout)
	}
}

func TestSchemaFormats(t *testing.T) {
	// Non-TTY: one line of JSON.
	r := testutil.Run(t, nil, testutil.RunOpts{}, "schema")
	if r.Code != 0 || strings.Count(r.Stdout, "\n") != 1 {
		t.Fatalf("schema: code %d, stdout %q", r.Code, r.Stdout)
	}
	if _, err := jsonx.Parse([]byte(r.Stdout)); err != nil {
		t.Errorf("schema output is not JSON: %v", err)
	}
	// TTY (auto = table) and -o json on a TTY: indented JSON.
	for _, args := range [][]string{{"schema"}, {"schema", "-o", "json"}, {"schema", "-o", "table"}} {
		r := testutil.Run(t, nil, testutil.RunOpts{StdoutTTY: true}, args...)
		if r.Code != 0 || !strings.HasPrefix(r.Stdout, "{\n  \"schemaVersion\": 1,") {
			t.Errorf("%v on a TTY: code %d, stdout starts %q", args, r.Code, r.Stdout[:min(len(r.Stdout), 40)])
		}
	}
	// --all streams NDJSON whatever the format.
	for _, tty := range []bool{false, true} {
		r := testutil.Run(t, nil, testutil.RunOpts{StdoutTTY: tty}, "schema", "--all", "-o", "json")
		lines := strings.Split(strings.TrimRight(r.Stdout, "\n"), "\n")
		if r.Code != 0 || len(lines) < 4 || !strings.HasPrefix(lines[0], `{"schemaVersion":1,"path":"search",`) || !strings.HasPrefix(lines[len(lines)-1], `{"schemaVersion":1,"path":"schema",`) {
			t.Errorf("schema --all (tty=%t): code %d, stdout %q", tty, r.Code, r.Stdout)
		}
	}
	// Aliases.
	want := testutil.Run(t, nil, testutil.RunOpts{}, "schema").Stdout
	for _, args := range [][]string{{"describe"}, {"help", "--json"}} {
		if got := testutil.Run(t, nil, testutil.RunOpts{}, args...).Stdout; got != want {
			t.Errorf("%v differs from schema", args)
		}
	}
}
