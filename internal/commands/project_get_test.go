package commands_test

import (
	"strings"
	"testing"

	"github.com/s8a-uk/modrinth-cli/internal/testutil"
)

func TestProjectGet(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		setup  func(m *testutil.MockAPI)
		tty    bool
		code   int
		stdout string // golden name; "" = stdout must be empty
		stderr string // golden name; "" = not compared
	}{
		{name: "full", args: []string{"project", "get", "sodium", "--full"},
			setup:  func(m *testutil.MockAPI) { m.On("GET", "/v2/project/sodium").Fixture("project_sodium") },
			stdout: "project_get/full.stdout"},
		{name: "full on a terminal", args: []string{"project", "get", "sodium", "--full"}, tty: true,
			setup:  func(m *testutil.MockAPI) { m.On("GET", "/v2/project/sodium").Fixture("project_sodium") },
			stdout: "project_get/full_tty.stdout"},
		{name: "not found (empty 404 body)", args: []string{"project", "get", "this-slug-does-not-exist-zz"},
			setup: func(m *testutil.MockAPI) {
				m.On("GET", "/v2/project/this-slug-does-not-exist-zz").Status(404).Empty()
			},
			code: 3, stderr: "project_get/not_found.stderr"},
		{name: "not found on a terminal", args: []string{"project", "get", "nope"}, tty: true,
			setup: func(m *testutil.MockAPI) { m.On("GET", "/v2/project/nope").Status(404).Empty() },
			code:  3, stderr: "project_get/not_found_tty.stderr"},
		{name: "invalid ref is rejected before sending", args: []string{"project", "get", "a?b"},
			code: 2, stderr: "project_get/invalid_ref.stderr"},
		{name: "control character is rejected", args: []string{"project", "get", "a\nb"}, code: 2},
		{name: "default fields", args: []string{"project", "get", "sodium"},
			setup:  func(m *testutil.MockAPI) { m.On("GET", "/v2/project/sodium").Fixture("project_sodium") },
			stdout: "project_get/default.stdout"},
		{name: "default fields on a terminal (table)", args: []string{"project", "get", "sodium"}, tty: true,
			setup:  func(m *testutil.MockAPI) { m.On("GET", "/v2/project/sodium").Fixture("project_sodium") },
			stdout: "project_get/default_tty.stdout"},
		{name: "selected fields, nested path", args: []string{"project", "get", "sodium", "--fields", "id,license.id"},
			setup:  func(m *testutil.MockAPI) { m.On("GET", "/v2/project/sodium").Fixture("project_sodium") },
			stdout: "project_get/fields_id_license.stdout"},
		{name: "a field outside the defaults", args: []string{"project", "get", "sodium", "--fields", "body"},
			setup:  func(m *testutil.MockAPI) { m.On("GET", "/v2/project/sodium").Fixture("project_sodium") },
			stdout: "project_get/fields_body.stdout"},
		{name: "a parent of fields, and arrays of objects", args: []string{"project", "get", "sodium", "--fields", "license,gallery.title"},
			setup:  func(m *testutil.MockAPI) { m.On("GET", "/v2/project/sodium").Fixture("project_sodium") },
			stdout: "project_get/fields_parent_array.stdout"},
		{name: "fields all is the full object", args: []string{"project", "get", "sodium", "--fields", "all"},
			setup:  func(m *testutil.MockAPI) { m.On("GET", "/v2/project/sodium").Fixture("project_sodium") },
			stdout: "project_get/full.stdout"},
		{name: "unknown field is rejected after the response", args: []string{"project", "get", "sodium", "--fields", "titel"},
			setup: func(m *testutil.MockAPI) { m.On("GET", "/v2/project/sodium").Fixture("project_sodium") },
			code:  2, stderr: "project_get/unknown_field.stderr"},
		{name: "unknown field on a terminal", args: []string{"project", "get", "sodium", "--fields", "titel"}, tty: true,
			setup: func(m *testutil.MockAPI) { m.On("GET", "/v2/project/sodium").Fixture("project_sodium") },
			code:  2, stderr: "project_get/unknown_field_tty.stderr"},
		{name: "far-away field gets no suggestion", args: []string{"project", "get", "sodium", "--fields", "zzzzzzzz"},
			setup: func(m *testutil.MockAPI) { m.On("GET", "/v2/project/sodium").Fixture("project_sodium") },
			code:  2, stderr: "project_get/unknown_field_far.stderr"},
		{name: "a field the API added is selected, with a note", args: []string{"project", "get", "sodium", "--fields", "id,new_key.n"},
			setup: func(m *testutil.MockAPI) {
				m.On("GET", "/v2/project/sodium").JSON(`{"id":"AANobbMI","title":"Sodium","new_key":{"n":1,"m":2}}`)
			},
			stdout: "project_get/fields_undocumented.stdout", stderr: "project_get/fields_undocumented.stderr"},
		{name: "a misspelled new field gets a suggestion from the response", args: []string{"project", "get", "sodium", "--fields", "new_kye"},
			setup: func(m *testutil.MockAPI) {
				m.On("GET", "/v2/project/sodium").JSON(`{"id":"AANobbMI","title":"Sodium","new_key":1}`)
			},
			code: 2, stderr: "project_get/unknown_field_observed.stderr"},
		{name: "an empty path segment is rejected before sending", args: []string{"project", "get", "sodium", "--fields", "license..id"},
			code: 2},
		{name: "fields with full", args: []string{"project", "get", "sodium", "--fields", "id", "--full"}, code: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := testutil.NewMockAPI(t)
			if tc.setup != nil {
				tc.setup(m)
			}
			r := testutil.Run(t, m, testutil.RunOpts{StdoutTTY: tc.tty}, tc.args...)
			if r.Code != tc.code {
				t.Errorf("exit code = %d, want %d (stderr %q)", r.Code, tc.code, r.Stderr)
			}
			if tc.stdout == "" {
				if r.Stdout != "" {
					t.Errorf("stdout = %q, want empty", r.Stdout)
				}
			} else {
				testutil.Golden(t, tc.stdout, r.Stdout)
			}
			if tc.stderr != "" {
				testutil.Golden(t, tc.stderr, r.Stderr)
			}
			if tc.setup == nil && len(m.Requests()) != 0 {
				t.Errorf("requests were sent: %v", m.Requests())
			}
			if tc.code != 0 && !tc.tty && !strings.HasPrefix(lastLine(r.Stderr), `{"error":{`) {
				t.Errorf("last stderr line is not an error object: %q", r.Stderr)
			}
		})
	}
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return lines[len(lines)-1]
}
