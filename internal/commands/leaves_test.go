package commands_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/s8a-uk/modrinth-cli/internal/testutil"
)

// leafRun is one successful run of a leaf command against the mock API.
type leafRun struct {
	args  []string
	setup func(m *testutil.MockAPI) // nil for commands that send no request
	env   map[string]string         // extra environment (a test's own Env wins)
	stdin string
}

// fakeToken is the token of the default runs that need one.
const fakeToken = "mrp_FAKEFAKEFAKEFAKEFAKEFAKE"

func fixture(path, name string) func(m *testutil.MockAPI) {
	return func(m *testutil.MockAPI) { m.On("GET", path).Fixture(name) }
}

// defaultRuns has the default run of every leaf, keyed by the command path. Its
// stdout is the golden <path with _>/default.stdout, and the cross-cutting tests
// (JSON validity, error format, headers) run every entry.
var defaultRuns = map[string]leafRun{
	"search":      {args: []string{"search", "sodium", "--limit", "3"}, setup: fixture("/v2/search", "search_sodium")},
	"project get": {args: []string{"project", "get", "sodium"}, setup: fixture("/v2/project/sodium", "project_sodium")},
	"project get-many": {args: []string{"project", "get-many", "lithium", "sodium", "no-such-zz"},
		setup: fixture("/v2/projects", "projects_2")},
	"project random":       {args: []string{"project", "random", "--count", "2"}, setup: fixture("/v2/projects_random", "projects_random")},
	"project check":        {args: []string{"project", "check", "sodium"}, setup: fixture("/v2/project/sodium/check", "project_check")},
	"project dependencies": {args: []string{"project", "dependencies", "fabulously-optimized"}, setup: fixture("/v2/project/fabulously-optimized/dependencies", "project_dependencies")},
	"project members":      {args: []string{"project", "members", "sodium"}, setup: fixture("/v2/project/sodium/members", "project_members")},
	"version list":         {args: []string{"version", "list", "sodium"}, setup: fixture("/v2/project/sodium/version", "versions_sodium")},
	"version get":          {args: []string{"version", "get", "ygf8cVZg"}, setup: fixture("/v2/version/ygf8cVZg", "version_ygf8cVZg")},
	"version get-many":     {args: []string{"version", "get-many", "ygf8cVZg", "OihdIimA"}, setup: fixture("/v2/versions", "versions_sodium")},
	"user get":             {args: []string{"user", "get", "jellysquid3"}, setup: fixture("/v2/user/jellysquid3", "user_jellysquid3")},
	"user get-many":        {args: []string{"user", "get-many", "jellysquid3", "IMS"}, setup: fixture("/v2/users", "users_2")},
	"user projects":        {args: []string{"user", "projects", "jellysquid3"}, setup: fixture("/v2/user/jellysquid3/projects", "user_projects")},
	"team members":         {args: []string{"team", "members", "4reLOAKe"}, setup: fixture("/v2/team/4reLOAKe/members", "team_members")},
	"tag list":             {args: []string{"tag", "list", "game-version"}, setup: fixture("/v2/tag/game_version", "tag_game_version")},
	"tag license":          {args: []string{"tag", "license", "MIT"}, setup: fixture("/v2/tag/license/MIT", "tag_license_mit")},
	"stats":                {args: []string{"stats"}, setup: fixture("/v2/statistics", "statistics")},
	"api":                  {args: []string{"api", "/statistics"}, setup: fixture("/v2/statistics", "statistics")},
	"schema":               {args: []string{"schema"}},
	"completion":           {args: []string{"completion", "bash"}},
	"user me": {args: []string{"user", "me"}, setup: fixture("/v2/user", "user_jellysquid3"),
		env: map[string]string{"MODRINTH_TOKEN": fakeToken}},
	"auth login": {args: []string{"auth", "login", "--with-token"}, setup: fixture("/v2/user", "user_jellysquid3"),
		stdin: fakeToken + "\n"},
	"auth status": {args: []string{"auth", "status"}, setup: fixture("/v2/user", "user_jellysquid3"),
		env: map[string]string{"MODRINTH_TOKEN": fakeToken}},
	"auth logout":  {args: []string{"auth", "logout"}},
	"config get":   {args: []string{"config", "get", "timeout"}},
	"config set":   {args: []string{"config", "set", "timeout", "60"}},
	"config unset": {args: []string{"config", "unset", "timeout"}},
	"config list":  {args: []string{"config", "list"}},
	"config path":  {args: []string{"config", "path"}},
	"file get": {args: []string{"file", "get", "c4293c1483f3b39c1c6c4c69143e8bb2c6e53daf"},
		setup: fixture("/v2/version_file/c4293c1483f3b39c1c6c4c69143e8bb2c6e53daf", "version_ygf8cVZg")},
	"file identify": {args: []string{"file", "identify", filepath.Join("testdata", "files")},
		setup: func(m *testutil.MockAPI) {
			m.On("POST", "/v2/version_files").Fixture("version_files")
			m.On("GET", "/v2/projects").Fixture("projects_2")
		}},
	"file updates": {args: []string{"file", "updates", filepath.Join("testdata", "files"), "--loader", "fabric", "--game-version", "1.20.1"},
		setup: func(m *testutil.MockAPI) {
			m.On("POST", "/v2/version_files").Fixture("version_files")
			m.On("POST", "/v2/version_files/update").Fixture("version_files_update")
			m.On("GET", "/v2/projects").Fixture("projects_2")
		}},
	"version download": {args: []string{"version", "download", "ygf8cVZg", "--dir", tmpDirArg}, setup: downloadSetup},
	"version create": {args: []string{"version", "create", "AANobbMI", "--file", filepath.Join("testdata", "files", "a.jar"),
		"--version-number", "0.0.1", "--loader", "fabric", "--game-version", "1.20.1"},
		setup: func(m *testutil.MockAPI) { m.On("POST", "/v2/version").Fixture("version_ygf8cVZg") }, env: withToken},
	"version add-file": {args: []string{"version", "add-file", "ygf8cVZg", "--file", filepath.Join("testdata", "files", "b.jar")},
		setup: noContent("POST", "/v2/version/ygf8cVZg/file"), env: withToken},
	"version edit": {args: []string{"version", "edit", "ygf8cVZg", "--set", "featured=true"},
		setup: noContent("PATCH", "/v2/version/ygf8cVZg"), env: withToken},
	"version delete": {args: []string{"version", "delete", "ygf8cVZg", "--yes"},
		setup: noContent("DELETE", "/v2/version/ygf8cVZg"), env: withToken},
	"project create": {args: []string{"project", "create", "--data", filepath.Join("testdata", "requests", "project_create.json")},
		setup: func(m *testutil.MockAPI) { m.On("POST", "/v2/project").Fixture("project_sodium") }, env: withToken},
	"project edit": {args: []string{"project", "edit", "sodium", "--set", "title=Sodium 2"},
		setup: noContent("PATCH", "/v2/project/sodium"), env: withToken},
	"project delete": {args: []string{"project", "delete", "sodium", "--yes"},
		setup: noContent("DELETE", "/v2/project/sodium"), env: withToken},
}

// withToken is the environment of the default runs of the write commands.
var withToken = map[string]string{"MODRINTH_TOKEN": fakeToken}

// noContent answers method path with an empty 204.
func noContent(method, path string) func(m *testutil.MockAPI) {
	return func(m *testutil.MockAPI) { m.On(method, path).Status(204).Empty() }
}

// cdnPath is the path of the file in the version_download fixture.
const cdnPath = "/data/AANobbMI/versions/ygf8cVZg/sodium-fabric-0.5.11+mc1.20.1.jar"

// downloadSetup serves the version_download fixture and its file (testdata/files/a.jar).
func downloadSetup(m *testutil.MockAPI) {
	m.On("GET", "/v2/version/ygf8cVZg").Fixture("version_download")
	b, _ := os.ReadFile(filepath.Join("testdata", "files", "a.jar"))
	m.On("GET", cdnPath).Body("application/java-archive", b)
}

// tmpDirArg in leafRun.args is replaced by a new temporary directory on every run
// (shown as <TMP> in golden files).
const tmpDirArg = "{{TMPDIR}}"

// argv returns the run's arguments followed by extra, with tmpDirArg replaced.
func (run leafRun) argv(t *testing.T, extra ...string) []string {
	t.Helper()
	args := slices.Clone(run.args)
	for i, a := range args {
		if a == tmpDirArg {
			args[i] = testutil.TempDir(t)
		}
	}
	return append(args, extra...)
}

// opts merges the run's env and stdin into o (o's own Env wins).
func (run leafRun) opts(o testutil.RunOpts) testutil.RunOpts {
	env := map[string]string{}
	for k, v := range run.env {
		env[k] = v
	}
	for k, v := range o.Env {
		env[k] = v
	}
	o.Env = env
	if o.Stdin == "" {
		o.Stdin = run.stdin
	}
	return o
}

// goldenDir is the golden directory of a command path: "project get" -> "project_get".
func goldenDir(path string) string { return strings.ReplaceAll(path, " ", "_") }
