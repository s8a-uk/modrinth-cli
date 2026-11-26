package commands_test

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
	"github.com/s8a-uk/modrinth-cli/internal/testutil"
)

// readCase is one run of a read command.
type readCase struct {
	name   string
	args   []string
	setup  func(m *testutil.MockAPI) // nil = no request may be sent
	env    map[string]string
	tty    bool
	code   int
	query  map[string]string // expected query of the last request ("" = absent)
	stdout string            // golden; "" = not compared
	stderr string            // golden; "" = not compared
	errIn  string            // substring of stderr
}

func runReadCases(t *testing.T, cases []readCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := testutil.NewMockAPI(t)
			if tc.setup != nil {
				tc.setup(m)
			}
			r := testutil.Run(t, m, testutil.RunOpts{StdoutTTY: tc.tty, Env: tc.env}, tc.args...)
			if r.Code != tc.code {
				t.Fatalf("exit code = %d, want %d (stderr %q)", r.Code, tc.code, r.Stderr)
			}
			if tc.stdout != "" {
				testutil.Golden(t, tc.stdout, r.Stdout)
			}
			if tc.stderr != "" {
				testutil.Golden(t, tc.stderr, r.Stderr)
			}
			if tc.errIn != "" && !strings.Contains(r.Stderr, tc.errIn) {
				t.Errorf("stderr %q does not contain %q", r.Stderr, tc.errIn)
			}
			if tc.setup == nil && len(m.Requests()) != 0 {
				t.Errorf("requests were sent: %v", m.Requests())
			}
			for k, want := range tc.query {
				q := m.Last().Query
				if want == "" {
					if q.Has(k) {
						t.Errorf("query %s = %q, want it absent", k, q.Get(k))
					}
				} else if got := q.Get(k); got != want {
					t.Errorf("query %s = %q, want %q", k, got, want)
				}
			}
			if tc.code != 0 && !tc.tty && !strings.HasPrefix(lastLine(r.Stderr), `{"error":{`) {
				t.Errorf("last stderr line is not an error object: %q", r.Stderr)
			}
		})
	}
}

func TestProjectReadCommands(t *testing.T) {
	getMany := fixture("/v2/projects", "projects_2")
	runReadCases(t, []readCase{
		{name: "get-many keeps the input order and notes missing ids",
			args: []string{"project", "get-many", "lithium", "sodium", "no-such-zz"}, setup: getMany,
			query: map[string]string{"ids": `["lithium","sodium","no-such-zz"]`}, stderr: "project_get-many/missing.stderr"},
		{name: "get-many matches slugs without case and drops repeats",
			args: []string{"project", "get-many", "SODIUM", "AANobbMI", "SODIUM", "gvQqBUqZ", "--fields", "slug"}, setup: getMany,
			query: map[string]string{"ids": `["SODIUM","AANobbMI","gvQqBUqZ"]`}, stdout: "project_get-many/case_dedupe.stdout"},
		{name: "get-many table", args: []string{"project", "get-many", "sodium", "lithium"}, setup: getMany, tty: true,
			stdout: "project_get-many/table.stdout"},
		{name: "get-many with nothing found is an empty array",
			args: []string{"project", "get-many", "zz-a", "zz-b"}, setup: func(m *testutil.MockAPI) { m.On("GET", "/v2/projects").JSON(`[]`) },
			stdout: "project_get-many/none.stdout", stderr: "project_get-many/none.stderr"},
		{name: "get-many dry run", args: []string{"project", "get-many", "sodium", "lithium", "--dry-run"},
			stdout: "project_get-many/dry_run.stdout"},
		{name: "get-many needs an id", args: []string{"project", "get-many"}, code: 2},
		{name: "get-many rejects an invalid ref", args: []string{"project", "get-many", "sodium", "a?b"}, code: 2},
		{name: "get-many takes at most 1000 ids", args: append([]string{"project", "get-many"}, ids("p", 1001)...), code: 2,
			errIn: "too many ids: 1001"},
		{name: "random", args: []string{"project", "random", "--count", "2", "--fields", "slug"}, setup: fixture("/v2/projects_random", "projects_random"),
			query: map[string]string{"count": "2"}},
		{name: "random count over 100", args: []string{"project", "random", "--count", "101"}, code: 2},
		{name: "check on a terminal", args: []string{"project", "check", "sodium"}, tty: true,
			setup: fixture("/v2/project/sodium/check", "project_check"), stdout: "project_check/table.stdout"},
		{name: "check not found", args: []string{"project", "check", "nope-zz"}, code: 3,
			setup:  func(m *testutil.MockAPI) { m.On("GET", "/v2/project/nope-zz/check").Status(404).Empty() },
			stderr: "project_check/not_found.stderr"},
		{name: "dependencies as two tables", args: []string{"project", "dependencies", "fabulously-optimized"}, tty: true,
			setup: fixture("/v2/project/fabulously-optimized/dependencies", "project_dependencies"), env: map[string]string{"COLUMNS": "100"},
			stdout: "project_dependencies/table.stdout"},
		{name: "dependencies table with fields of one section", args: []string{"project", "dependencies", "fabulously-optimized", "-o", "table", "--fields", "projects.slug,projects.downloads"},
			setup:  fixture("/v2/project/fabulously-optimized/dependencies", "project_dependencies"),
			stdout: "project_dependencies/table_fields.stdout"},
		{name: "dependencies without versions", args: []string{"project", "dependencies", "sodium"}, tty: true,
			setup: func(m *testutil.MockAPI) {
				m.On("GET", "/v2/project/sodium/dependencies").JSON(`{"projects":[{"id":"P7dR8mSH","slug":"fabric-api","title":"Fabric API","project_type":"mod"}],"versions":[]}`)
			},
			stdout: "project_dependencies/no_versions.stdout", stderr: "project_dependencies/no_versions.stderr"},
		{name: "dependencies unknown field", args: []string{"project", "dependencies", "sodium", "--fields", "projects.titel"}, code: 2,
			setup: func(m *testutil.MockAPI) {
				m.On("GET", "/v2/project/sodium/dependencies").JSON(`{"projects":[{"id":"P7dR8mSH","slug":"fabric-api","title":"Fabric API","project_type":"mod"}],"versions":[]}`)
			},
			errIn: "projects.title"},
		{name: "members table", args: []string{"project", "members", "sodium"}, tty: true,
			setup: fixture("/v2/project/sodium/members", "project_members"), stdout: "project_members/table.stdout"},
		{name: "members not found", args: []string{"project", "members", "nope-zz"}, code: 3,
			setup: func(m *testutil.MockAPI) { m.On("GET", "/v2/project/nope-zz/members").Status(404).Empty() },
			errIn: `"message":"project 'nope-zz' not found"`},
		{name: "project search is search", args: []string{"project", "search", "sodium", "--limit", "3"},
			setup: fixture("/v2/search", "search_sodium"), stdout: "search/default.stdout"},
	})
}

// ids returns n distinct 8-character ids starting with prefix.
func ids(prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%s%07d", prefix, i)
	}
	return out
}

// M5-3: 250 ids are sent as 100 + 100 + 50 and come back in input order even when
// each response is in another order.
func TestBulkChunking(t *testing.T) {
	all := ids("p", 250)
	m := testutil.NewMockAPI(t)
	for _, chunk := range [][]string{all[:100], all[100:200], all[200:]} {
		q, _ := json.Marshal(chunk)
		items := make([]string, len(chunk))
		for i, id := range chunk {
			items[len(chunk)-1-i] = fmt.Sprintf(`{"id":%q,"slug":"s-%s"}`, id, id)
		}
		m.On("GET", "/v2/projects").Query("ids", string(q)).JSON("[" + strings.Join(items, ",") + "]")
	}
	r := testutil.Run(t, m, testutil.RunOpts{}, append([]string{"project", "get-many", "--fields", "id"}, all...)...)
	if r.Code != 0 {
		t.Fatalf("code %d, stderr %q", r.Code, r.Stderr)
	}
	if n := len(m.Requests()); n != 3 {
		t.Errorf("%d requests, want 3", n)
	}
	v, err := jsonx.Parse([]byte(r.Stdout))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range v.Items {
		id, _ := it.Get("id")
		got = append(got, id.Str)
	}
	if !slices.Equal(got, all) {
		t.Errorf("order not kept: got %d ids, first %v", len(got), got[:min(3, len(got))])
	}
	if r.Stderr != "" {
		t.Errorf("stderr = %q", r.Stderr)
	}
}

func TestMissingNoteIsCapped(t *testing.T) {
	m := testutil.NewMockAPI(t)
	m.On("GET", "/v2/versions").JSON(`[]`)
	r := testutil.Run(t, m, testutil.RunOpts{}, append([]string{"version", "get-many"}, ids("v", 12)...)...)
	want := "note: 12 of 12 ids not found: v0000000, v0000001, v0000002, v0000003, v0000004, v0000005, v0000006, v0000007, v0000008, v0000009, ... (+2 more)\n"
	if r.Code != 0 || r.Stdout != "[]\n" || r.Stderr != want {
		t.Errorf("code %d, stdout %q, stderr\n got %q\nwant %q", r.Code, r.Stdout, r.Stderr, want)
	}
}

func TestVersionCommands(t *testing.T) {
	list := fixture("/v2/project/sodium/version", "versions_sodium")
	runReadCases(t, []readCase{
		{name: "list leaves out changelogs by default", args: []string{"version", "list", "sodium"}, setup: list,
			query: map[string]string{"include_changelog": "false", "loaders": "", "game_versions": "", "featured": ""}},
		{name: "list filters", args: []string{"version", "list", "sodium", "--loader", "fabric", "--loader", "quilt",
			"--game-version", "1.20.1", "--featured", "--version-type", "release", "--limit", "1"}, setup: list,
			query:  map[string]string{"loaders": `["fabric","quilt"]`, "game_versions": `["1.20.1"]`, "featured": "true", "include_changelog": "false"},
			stdout: "version_list/filtered.stdout", stderr: "version_list/filtered.stderr"},
		{name: "list version type beta", args: []string{"version", "list", "sodium", "--version-type", "beta", "--fields", "id,version_type"}, setup: list,
			stdout: "version_list/beta.stdout"},
		{name: "list asks for changelogs when selected", args: []string{"version", "list", "sodium", "--fields", "id,changelog", "--limit", "2"}, setup: list,
			query: map[string]string{"include_changelog": ""}, stdout: "version_list/changelog.stdout"},
		{name: "list asks for changelogs with full", args: []string{"version", "list", "sodium", "--full"}, setup: list,
			query: map[string]string{"include_changelog": ""}},
		{name: "ls is list, as a table", args: []string{"version", "ls", "sodium"}, setup: list, tty: true,
			stdout: "version_list/table.stdout"},
		{name: "list rejects an unknown version type", args: []string{"version", "list", "sodium", "--version-type", "snapshot"}, code: 2},
		{name: "list not found", args: []string{"version", "list", "nope-zz"}, code: 3,
			setup: func(m *testutil.MockAPI) { m.On("GET", "/v2/project/nope-zz/version").Status(404).Empty() },
			errIn: `"message":"project 'nope-zz' not found"`},
		{name: "get by project and number", args: []string{"version", "get", "sodium", "mc1.20.1-0.5.11"},
			setup:  fixture("/v2/project/sodium/version/mc1.20.1-0.5.11", "version_ygf8cVZg"),
			stdout: "version_get/default.stdout"},
		{name: "get on a terminal", args: []string{"version", "get", "ygf8cVZg"}, tty: true,
			setup: fixture("/v2/version/ygf8cVZg", "version_ygf8cVZg"), env: map[string]string{"COLUMNS": "100"},
			stdout: "version_get/table.stdout"},
		{name: "get with a version number alone", args: []string{"version", "get", "mc1.20.1-0.5.11"}, code: 2,
			stderr: "version_get/number_alone.stderr"},
		{name: "get not found by id", args: []string{"version", "get", "zzzzzzzz"}, code: 3,
			setup: func(m *testutil.MockAPI) { m.On("GET", "/v2/version/zzzzzzzz").Status(404).Empty() },
			errIn: `"message":"version 'zzzzzzzz' not found"`},
		{name: "get not found by number", args: []string{"version", "get", "sodium", "9.9.9"}, code: 3,
			setup:  func(m *testutil.MockAPI) { m.On("GET", "/v2/project/sodium/version/9.9.9").Status(404).Empty() },
			stderr: "version_get/number_not_found.stderr"},
		{name: "get-many keeps the order", args: []string{"version", "get-many", "dEpHs0Hg", "OihdIimA", "zzzzzzzz", "--fields", "id"},
			setup: fixture("/v2/versions", "versions_sodium"), stdout: "version_get-many/order.stdout", stderr: "version_get-many/order.stderr"},
		{name: "get-many takes version ids only", args: []string{"version", "get-many", "sodium"}, code: 2},
	})
}

func TestUserTeamCommands(t *testing.T) {
	runReadCases(t, []readCase{
		{name: "user get table", args: []string{"user", "get", "jellysquid3"}, tty: true,
			setup: fixture("/v2/user/jellysquid3", "user_jellysquid3"), stdout: "user_get/table.stdout"},
		{name: "user get not found", args: []string{"user", "get", "nobody-zz"}, code: 3,
			setup: func(m *testutil.MockAPI) { m.On("GET", "/v2/user/nobody-zz").Status(404).Empty() },
			errIn: `"message":"user 'nobody-zz' not found"`},
		{name: "user get rejects an invalid name", args: []string{"user", "get", "a.b"}, code: 2},
		{name: "user get-many matches usernames without case", args: []string{"user", "get-many", "ims", "TEZXhE2U", "ghost", "--fields", "username"},
			setup: fixture("/v2/users", "users_2"), stdout: "user_get-many/order.stdout", stderr: "user_get-many/order.stderr"},
		{name: "user projects limit", args: []string{"user", "projects", "jellysquid3", "--limit", "1", "--fields", "slug"},
			setup: fixture("/v2/user/jellysquid3/projects", "user_projects"), stdout: "user_projects/limit.stdout", stderr: "user_projects/limit.stderr"},
		{name: "team members not found", args: []string{"team", "members", "zzzzzzzz"}, code: 3,
			setup:  func(m *testutil.MockAPI) { m.On("GET", "/v2/team/zzzzzzzz/members").Status(404).Empty() },
			stderr: "team_members/not_found.stderr"},
		{name: "team members takes a team id", args: []string{"team", "members", "sodium"}, code: 2},
	})
}

func TestTagAndStats(t *testing.T) {
	runReadCases(t, []readCase{
		{name: "category for a project type", args: []string{"tag", "list", "category", "--project-type", "resourcepack"},
			setup: fixture("/v2/tag/category", "tag_category"), stdout: "tag_list/category_resourcepack.stdout"},
		{name: "loader for a project type", args: []string{"tag", "list", "loader", "--project-type", "plugin"},
			setup: fixture("/v2/tag/loader", "tag_loader"), stdout: "tag_list/loader_plugin.stdout"},
		{name: "game versions of a type, limited", args: []string{"tag", "list", "game-version", "--version-type", "release", "--limit", "2"},
			setup: fixture("/v2/tag/game_version", "tag_game_version"), stdout: "tag_list/game_version_release.stdout", stderr: "tag_list/game_version_release.stderr"},
		{name: "game versions table", args: []string{"tag", "list", "game-version"}, tty: true,
			setup: fixture("/v2/tag/game_version", "tag_game_version"), stdout: "tag_list/game_version_table.stdout"},
		{name: "license list table", args: []string{"tag", "list", "license"}, tty: true,
			setup: fixture("/v2/tag/license", "tag_license"), stdout: "tag_list/license_table.stdout"},
		{name: "donation platforms", args: []string{"tag", "list", "donation-platform"},
			setup: fixture("/v2/tag/donation_platform", "tag_donation_platform"), stdout: "tag_list/donation_platform.stdout"},
		{name: "array of strings", args: []string{"tag", "list", "report-type"},
			setup: fixture("/v2/tag/report_type", "tag_report_type"), stdout: "tag_list/report_type.stdout"},
		{name: "array of strings as a table", args: []string{"tag", "list", "side-type"}, tty: true,
			setup: fixture("/v2/tag/side_type", "tag_side_type"), stdout: "tag_list/side_type_table.stdout"},
		{name: "project types", args: []string{"tag", "list", "project-type", "--full"},
			setup: fixture("/v2/tag/project_type", "tag_project_type"), stdout: "tag_list/project_type.stdout"},
		{name: "fields of the kind", args: []string{"tag", "list", "category", "--fields", "name"},
			setup: fixture("/v2/tag/category", "tag_category"), stdout: "tag_list/category_name.stdout"},
		{name: "field of another kind", args: []string{"tag", "list", "game-version", "--fields", "name"}, code: 2,
			setup:  fixture("/v2/tag/game_version", "tag_game_version"),
			stderr: "tag_list/wrong_kind_field.stderr"},
		{name: "fields on an array of strings", args: []string{"tag", "list", "report-type", "--fields", "name"}, code: 2,
			errIn: `"code":"usage"`},
		{name: "unknown kind", args: []string{"tag", "list", "loaders"}, code: 2, stderr: "tag_list/unknown_kind.stderr"},
		{name: "project type with another kind", args: []string{"tag", "list", "license", "--project-type", "mod"}, code: 2,
			errIn: "--project-type works only"},
		{name: "version type with another kind", args: []string{"tag", "list", "loader", "--version-type", "release"}, code: 2,
			errIn: "--version-type works only"},
		{name: "license text with full", args: []string{"tag", "license", "MIT", "--full"},
			setup: fixture("/v2/tag/license/MIT", "tag_license_mit"), stdout: "tag_license/full.stdout"},
		{name: "license not found", args: []string{"tag", "license", "No-Such-License"}, code: 3,
			setup: func(m *testutil.MockAPI) { m.On("GET", "/v2/tag/license/No-Such-License").Status(404).Empty() },
			errIn: `"message":"license 'No-Such-License' not found"`},
		{name: "stats on a terminal", args: []string{"stats"}, tty: true,
			setup: fixture("/v2/statistics", "statistics"), stdout: "stats/table.stdout"},
	})
}
