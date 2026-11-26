package commands_test

import (
	"bufio"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/s8a-uk/modrinth-cli/internal/cli"
	"github.com/s8a-uk/modrinth-cli/internal/commands"
	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
	"github.com/s8a-uk/modrinth-cli/internal/output"
	"github.com/s8a-uk/modrinth-cli/internal/testutil"
)

// noDefaultGolden lists leaves without golden/<path>/default.stdout, with the reason.
var noDefaultGolden = map[string]string{
	"schema": "its default output is pinned by golden/schema/tree.stdout in TestSchemaGolden",
}

// allLeafPaths lists every visible leaf, as "project get".
func allLeafPaths() []string {
	var out []string
	for _, p := range cli.LeafPaths(commands.Root()) {
		out = append(out, strings.Join(p, " "))
	}
	return out
}

// TestLeafDefaults pins the default (non-TTY) output of every leaf.
func TestLeafDefaults(t *testing.T) {
	for _, path := range allLeafPaths() {
		if _, skip := noDefaultGolden[path]; skip {
			continue
		}
		t.Run(path, func(t *testing.T) {
			run, ok := defaultRuns[path]
			if !ok {
				t.Fatalf("no entry in defaultRuns")
			}
			r := runLeaf(t, run, testutil.RunOpts{})
			if r.Code != 0 {
				t.Fatalf("exit %d, stderr %q", r.Code, r.Stderr)
			}
			testutil.Golden(t, goldenDir(path)+"/default.stdout", r.Stdout)
		})
	}
}

func runLeaf(t *testing.T, run leafRun, opts testutil.RunOpts, extra ...string) testutil.RunResult {
	t.Helper()
	var m *testutil.MockAPI
	if run.setup != nil {
		m = testutil.NewMockAPI(t)
		run.setup(m)
	}
	return testutil.Run(t, m, run.opts(opts), run.argv(t, extra...)...)
}

func TestEveryLeafHasGolden(t *testing.T) {
	for _, path := range allLeafPaths() {
		if _, skip := noDefaultGolden[path]; skip {
			continue
		}
		name := filepath.Join("testdata", "golden", goldenDir(path), "default.stdout")
		if _, err := os.Stat(name); err != nil {
			t.Errorf("%s: no %s (add it to defaultRuns and run make test-update, or to noDefaultGolden with a reason)", path, name)
		}
	}
}

// TestJSONOutputIsValid checks that the non-TTY output of every leaf parses and has
// the shape its command table entry declares.
func TestJSONOutputIsValid(t *testing.T) {
	root := commands.Root()
	for _, path := range allLeafPaths() {
		t.Run(path, func(t *testing.T) {
			run, ok := defaultRuns[path]
			if !ok {
				t.Fatalf("no entry in defaultRuns")
			}
			if path == "completion" {
				t.Skip("prints a shell script, not JSON")
			}
			for _, extra := range [][]string{nil, {"--full"}} {
				if path == "schema" && extra != nil {
					continue // schema has no fields; --full is meaningless there
				}
				r := runLeaf(t, run, testutil.RunOpts{}, extra...)
				if r.Code != 0 {
					t.Fatalf("%v: exit %d, stderr %q", extra, r.Code, r.Stderr)
				}
				v, err := jsonx.Parse([]byte(r.Stdout))
				if err != nil {
					t.Fatalf("%v: stdout is not JSON: %v", extra, err)
				}
				shape := cli.Find(root, strings.Fields(path)).Output.Shape
				var ok bool
				switch shape {
				case output.Object:
					ok = v.Kind == jsonx.Object
				case output.Array:
					ok = v.Kind == jsonx.Array
				case output.SearchPage:
					hits, has := v.Get("hits")
					ok = v.Kind == jsonx.Object && has && hits.Kind == jsonx.Array
				default:
					ok = true
				}
				if !ok {
					t.Errorf("%v: output does not have shape %s: %.80s", extra, shape, r.Stdout)
				}
			}
		})
	}
}

// failingDoer answers every request with an empty 500.
type failingDoer struct{}

func (failingDoer) Do(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 500, Proto: "HTTP/1.1", Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
}

// TestErrorsAreLastStderrLine checks that a failing run ends stderr with the JSON
// error object: for a server error, and for an invalid --fields value.
func TestErrorsAreLastStderrLine(t *testing.T) {
	for _, path := range allLeafPaths() {
		run := defaultRuns[path]
		t.Run(path, func(t *testing.T) {
			var cases [][]string
			cases = append(cases, []string{"--fields", "zz_no_such_field"})
			if run.setup != nil {
				cases = append(cases, []string{"--no-retry", "-v"})
			}
			for _, extra := range cases {
				r := testutil.Run(t, nil, run.opts(testutil.RunOpts{Doer: failingDoer{}, Env: map[string]string{"MODRINTH_API_URL": "http://127.0.0.1:1"}}),
					run.argv(t, extra...)...)
				if r.Code == 0 {
					t.Fatalf("%v: exit 0", extra)
				}
				last := lastLine(r.Stderr)
				if !strings.HasPrefix(last, `{"error":{`) {
					t.Errorf("%v: last stderr line = %q", extra, last)
				} else if _, err := jsonx.Parse([]byte(last)); err != nil {
					t.Errorf("%v: last stderr line is not JSON: %v", extra, err)
				}
			}
		})
	}
}

// fixtureSpecs maps each fixture to the fields that must cover it: the command
// whose output it is and, for search pages, the key the items sit under.
var fixtureSpecs = map[string]struct {
	command string
	under   string // "hits": keys are checked below this key only
	kind    string // tag list kind
	byHash  bool   // an object keyed by file hash: keys are checked in its values
}{
	"project_sodium":        {command: "project get"},
	"projects_2":            {command: "project get-many"},
	"projects_random":       {command: "project random"},
	"project_check":         {command: "project check"},
	"project_dependencies":  {command: "project dependencies"},
	"project_members":       {command: "project members"},
	"search_sodium":         {command: "search", under: "hits"},
	"versions_sodium":       {command: "version list"},
	"version_ygf8cVZg":      {command: "version get"},
	"user_jellysquid3":      {command: "user get"},
	"users_2":               {command: "user get-many"},
	"user_projects":         {command: "user projects"},
	"team_members":          {command: "team members"},
	"tag_category":          {kind: "category"},
	"tag_loader":            {kind: "loader"},
	"tag_game_version":      {kind: "game-version"},
	"tag_license":           {kind: "license"},
	"tag_donation_platform": {kind: "donation-platform"},
	"tag_report_type":       {kind: "report-type"},
	"tag_project_type":      {kind: "project-type"},
	"tag_side_type":         {kind: "side-type"},
	"tag_license_mit":       {command: "tag license"},
	"statistics":            {command: "stats"},
	"version_files":         {command: "version get", byHash: true},
	"version_files_update":  {command: "version get", byHash: true},
	"version_download":      {command: "version get"},
}

// covered reports whether key is a field, the parent of fields, or inside a field
// that is selected whole (e.g. user.campaigns.x under user.campaigns).
func covered(fields []string, key string) bool {
	for _, f := range fields {
		if f == key || strings.HasPrefix(f, key+".") || strings.HasPrefix(key, f+".") {
			return true
		}
	}
	return false
}

// TestFieldsCoverFixtures catches keys the API added: every key of every fixture
// must be selectable with --fields of the command it belongs to.
func TestFieldsCoverFixtures(t *testing.T) {
	root := commands.Root()
	files, err := filepath.Glob(filepath.Join("testdata", "responses", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".json")
		t.Run(name, func(t *testing.T) {
			fs, ok := fixtureSpecs[name]
			if !ok {
				t.Fatalf("fixture %s is not in fixtureSpecs", name)
			}
			var fields []string
			if fs.kind != "" {
				fields = commands.TagKindFields(fs.kind)
			} else {
				cmd := cli.Find(root, strings.Fields(fs.command))
				if cmd == nil || cmd.Output == nil {
					t.Fatalf("no command %q", fs.command)
				}
				fields = cmd.Output.Fields
			}
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			v, err := jsonx.Parse(data)
			if err != nil {
				t.Fatal(err)
			}
			if fs.under != "" {
				v, _ = v.Get(fs.under)
			}
			if fs.byHash {
				var items []jsonx.Value
				for _, m := range v.Members {
					items = append(items, m.Value)
				}
				v = jsonx.Arr(items...)
			}
			keys := jsonx.Keys(v)
			for _, k := range keys {
				if !covered(fields, k) {
					t.Errorf("key %q is not in the fields of %s", k, name)
				}
			}
			for _, f := range fields {
				if !slices.ContainsFunc(keys, func(k string) bool { return k == f || strings.HasPrefix(k, f+".") }) {
					t.Logf("field %q does not appear in %s", f, name)
				}
			}
		})
	}
}

// specAllowlist lists the spec's endpoints that no command calls, each with the reason.
var specAllowlist = map[string]string{
	"PATCH /projects":                           "bulk edit: use mdr api",
	"PATCH /project/{id|slug}/icon":             "P4: project icon set",
	"DELETE /project/{id|slug}/icon":            "P4: project icon delete",
	"POST /project/{id|slug}/gallery":           "P4: project gallery add",
	"PATCH /project/{id|slug}/gallery":          "P4: project gallery edit",
	"DELETE /project/{id|slug}/gallery":         "P4: project gallery delete",
	"POST /project/{id|slug}/follow":            "P4: project follow",
	"DELETE /project/{id|slug}/follow":          "P4: project unfollow",
	"POST /project/{id|slug}/schedule":          "P4: project schedule",
	"POST /version/{id}/schedule":               "P4: version schedule",
	"DELETE /version_file/{hash}":               "P4: file delete",
	"POST /version_file/{hash}/update":          "P2: file updates uses the bulk /version_files/update; single hash: use mdr api",
	"PATCH /user/{id|username}":                 "P4: user edit",
	"PATCH /user/{id|username}/icon":            "user icon change: use mdr api",
	"DELETE /user/{id|username}/icon":           "user icon removal: use mdr api",
	"GET /user/{id|username}/follows":           "P4: user follows",
	"GET /user/{id|username}/payouts":           "P4: user payouts",
	"POST /user/{id|username}/payouts":          "payout withdrawal: use mdr api",
	"GET /user/{id|username}/notifications":     "P4: notification list",
	"GET /notification/{id}":                    "P4: notification get",
	"PATCH /notification/{id}":                  "P4: notification read",
	"DELETE /notification/{id}":                 "P4: notification delete",
	"GET /notifications":                        "P4: notification get (bulk)",
	"PATCH /notifications":                      "P4: notification read (bulk)",
	"DELETE /notifications":                     "P4: notification delete (bulk)",
	"POST /report":                              "P4: report create",
	"GET /report":                               "P4: report list",
	"GET /report/{id}":                          "P4: report get",
	"PATCH /report/{id}":                        "P4: report edit",
	"GET /reports":                              "P4: report get (bulk)",
	"GET /thread/{id}":                          "P4: thread get",
	"POST /thread/{id}":                         "P4: thread send",
	"GET /threads":                              "P4: thread get-many",
	"DELETE /message/{id}":                      "P4: thread delete-message",
	"POST /team/{id}/members":                   "P4: team invite",
	"GET /teams":                                "P4: team get-many",
	"POST /team/{id}/join":                      "P4: team join",
	"PATCH /team/{id}/members/{id|username}":    "P4: team edit-member",
	"DELETE /team/{id}/members/{id|username}":   "P4: team remove-member",
	"PATCH /team/{id}/owner":                    "P4: team transfer",
	"GET /updates/{id|slug}/forge_updates.json": "Forge update checker format, served from the API root: use mdr api --api-version none",
}

var (
	reSpecPath   = regexp.MustCompile(`^  (/\S+):$`)
	reSpecMethod = regexp.MustCompile(`^    (get|post|patch|put|delete):$`)
)

// TestSpecCoverage matches the endpoints of spec/openapi.yaml with the command
// table: every spec endpoint is called by a command or allowlisted with a reason,
// and every command endpoint exists in the spec.
func TestSpecCoverage(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "..", "spec", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	spec := map[string]bool{}
	path := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if m := reSpecPath.FindStringSubmatch(line); m != nil {
			path = m[1]
		} else if m := reSpecMethod.FindStringSubmatch(line); m != nil && path != "" {
			spec[strings.ToUpper(m[1])+" "+path] = true
		} else if line != "" && (!strings.HasPrefix(line, " ") || !strings.HasPrefix(line, "   ")) {
			path = "" // another key at the level of the paths, or a new top-level section
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(spec) < 50 {
		t.Fatalf("read only %d endpoints from the spec; did its layout change?", len(spec))
	}

	root := commands.Root()
	table := map[string]string{}
	for _, p := range cli.LeafPaths(root) {
		for _, e := range cli.Find(root, p).Endpoints {
			table[e.Method+" "+e.Path] = strings.Join(p, " ")
		}
	}
	var missing []string
	for ep := range spec {
		if _, ok := table[ep]; ok {
			if _, listed := specAllowlist[ep]; listed {
				t.Errorf("%s is called by %q but still in specAllowlist", ep, table[ep])
			}
			continue
		}
		if reason := specAllowlist[ep]; reason == "" {
			missing = append(missing, ep)
		}
	}
	sort.Strings(missing)
	for _, ep := range missing {
		t.Errorf("spec endpoint %s is called by no command and not in specAllowlist", ep)
	}
	for ep, cmd := range table {
		if !spec[ep] {
			t.Errorf("command %q calls %s, which is not in the spec", cmd, ep)
		}
	}
	for ep := range specAllowlist {
		if !spec[ep] {
			t.Errorf("specAllowlist has %s, which is not in the spec", ep)
		}
	}
}
