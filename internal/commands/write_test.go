package commands_test

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/s8a-uk/modrinth-cli/internal/cli"
	"github.com/s8a-uk/modrinth-cli/internal/commands"
	"github.com/s8a-uk/modrinth-cli/internal/testutil"
)

var (
	jarA = filepath.Join("testdata", "files", "a.jar")
	jarB = filepath.Join("testdata", "files", "b.jar")
)

// tokenEnv is the environment of a run with a token.
func tokenEnv() map[string]string { return map[string]string{"MODRINTH_TOKEN": fakeToken} }

type formPart struct {
	name, filename, contentType, data string
}

// readMultipart reads a recorded multipart request back with mime/multipart and
// checks that its Content-Length is the length of the body.
func readMultipart(t *testing.T, rec testutil.Recorded) []formPart {
	t.Helper()
	mt, params, err := mime.ParseMediaType(rec.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/form-data" || params["boundary"] == "" {
		t.Fatalf("Content-Type = %q (%v)", rec.Header.Get("Content-Type"), err)
	}
	if rec.ContentLength != int64(len(rec.Body)) || rec.ContentLength <= 0 {
		t.Errorf("Content-Length = %d, body has %d bytes", rec.ContentLength, len(rec.Body))
	}
	r := multipart.NewReader(bytes.NewReader(rec.Body), params["boundary"])
	var parts []formPart
	for {
		p, err := r.NextPart()
		if err == io.EOF {
			return parts
		}
		if err != nil {
			t.Fatalf("reading part %d: %v", len(parts), err)
		}
		b, err := io.ReadAll(p)
		if err != nil {
			t.Fatal(err)
		}
		parts = append(parts, formPart{p.FormName(), p.FileName(), p.Header.Get("Content-Type"), string(b)})
	}
}

func fileText(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// requestTo returns the recorded requests with this method.
func requestsWith(m *testutil.MockAPI, method string) []testutil.Recorded {
	var out []testutil.Recorded
	for _, r := range m.Requests() {
		if r.Method == method {
			out = append(out, r)
		}
	}
	return out
}

func TestVersionCreateMultipart(t *testing.T) {
	m := testutil.NewMockAPI(t)
	m.On("GET", "/v2/project/sodium/check").Fixture("project_check")
	m.On("GET", "/v2/project/lithium/check").JSON(`{"id":"gvQqBUqZ"}`)
	m.On("POST", "/v2/version").Fixture("version_ygf8cVZg")
	r := testutil.Run(t, m, testutil.RunOpts{Env: tokenEnv()},
		"version", "create", "sodium", "--file", jarA, "--file", jarB, "--primary", jarB,
		"--version-number", "1.2.0", "--name", "Sodium 1.2", "--loader", "fabric", "--loader", "quilt",
		"--game-version", "1.20.1", "--version-type", "beta", "--changelog", "Fixed <things> & stuff",
		"--dependency", "lithium:optional", "--featured", "--status", "unlisted")
	if r.Code != 0 {
		t.Fatalf("exit %d, stderr %s", r.Code, r.Stderr)
	}
	posts := requestsWith(m, "POST")
	if len(posts) != 1 {
		t.Fatalf("%d POST requests", len(posts))
	}
	parts := readMultipart(t, posts[0])
	if len(parts) != 3 {
		t.Fatalf("parts = %+v", parts)
	}
	wantData := `{"name":"Sodium 1.2","version_number":"1.2.0","changelog":"Fixed <things> & stuff",` +
		`"dependencies":[{"project_id":"gvQqBUqZ","dependency_type":"optional"}],"game_versions":["1.20.1"],` +
		`"version_type":"beta","loaders":["fabric","quilt"],"featured":true,"status":"unlisted",` +
		`"project_id":"AANobbMI","file_parts":["file0","file1"],"primary_file":"file1"}`
	if p := parts[0]; p.name != "data" || p.filename != "" || p.contentType != "application/json" || p.data != wantData {
		t.Errorf("data part = %+v\nwant data %s", p, wantData)
	}
	for i, want := range []formPart{
		{"file0", "a.jar", "application/java-archive", fileText(t, jarA)},
		{"file1", "b.jar", "application/java-archive", fileText(t, jarB)},
	} {
		if parts[i+1] != want {
			t.Errorf("part %d = %+v, want %+v", i+1, parts[i+1], want)
		}
	}
	if auth := posts[0].Header.Get("Authorization"); auth != fakeToken {
		t.Errorf("Authorization = %q", auth)
	}
}

// TestVersionCreateDryRun (milestone criterion 1): nothing is sent, and the plan
// shows the data part and file0 with its size and sha512.
func TestVersionCreateDryRun(t *testing.T) {
	d := &countDoer{}
	r := testutil.Run(t, nil, testutil.RunOpts{Doer: d, Env: map[string]string{"MODRINTH_STAGING_TOKEN": fakeToken}},
		"version", "create", "AANobbMI", "--file", jarA, "--version-number", "0.0.1", "--loader", "fabric",
		"--game-version", "1.20.1", "--staging", "--dry-run")
	if r.Code != 0 || d.n != 0 {
		t.Fatalf("exit %d after %d requests, stderr %s", r.Code, d.n, r.Stderr)
	}
	testutil.Golden(t, "version_create/dry_run.stdout", r.Stdout)
}

// A slug is resolved even in a dry run (a read before the write); the write is not sent.
func TestVersionCreateDryRunResolvesSlug(t *testing.T) {
	m := testutil.NewMockAPI(t)
	m.On("GET", "/v2/project/sodium/check").Fixture("project_check")
	r := testutil.Run(t, m, testutil.RunOpts{Env: tokenEnv()},
		"version", "create", "sodium", "--file", jarA, "--version-number", "0.0.1", "--loader", "fabric",
		"--game-version", "1.20.1", "--dry-run")
	if r.Code != 0 || len(m.Requests()) != 1 {
		t.Fatalf("exit %d, %d requests, stderr %s", r.Code, len(m.Requests()), r.Stderr)
	}
	if !strings.Contains(r.Stdout, `"project_id":"AANobbMI"`) || !strings.Contains(r.Stdout, `"method":"POST"`) {
		t.Errorf("plan = %s", r.Stdout)
	}
}

// countDoer counts requests and answers 599 (it should never be reached).
type countDoer struct{ n int }

func (d *countDoer) Do(*http.Request) (*http.Response, error) {
	d.n++
	return statusDoer(599).Do(nil)
}

func TestVersionCreateData(t *testing.T) {
	data := filepath.Join(t.TempDir(), "v.json")
	if err := os.WriteFile(data, []byte(`{"name":"N","version_number":"1","file_parts":["x"],"loaders":["fabric"],"project_id":"zz"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	m := testutil.NewMockAPI(t)
	m.On("POST", "/v2/version").Fixture("version_ygf8cVZg")
	r := testutil.Run(t, m, testutil.RunOpts{Env: tokenEnv()}, "version", "create", "AANobbMI", "--file", jarA, "--data", data)
	if r.Code != 0 {
		t.Fatalf("exit %d, stderr %s", r.Code, r.Stderr)
	}
	parts := readMultipart(t, m.Last())
	want := `{"name":"N","version_number":"1","file_parts":["file0"],"loaders":["fabric"],"project_id":"AANobbMI","primary_file":"file0"}`
	if parts[0].data != want {
		t.Errorf("data = %s\nwant   %s", parts[0].data, want)
	}
}

func TestVersionAddFile(t *testing.T) {
	m := testutil.NewMockAPI(t)
	m.On("POST", "/v2/version/IIJJKKLL/file").Status(204).Empty()
	r := testutil.Run(t, m, testutil.RunOpts{Env: tokenEnv()}, "version", "add-file", "IIJJKKLL", "--file", jarA, "--file", jarB)
	if r.Code != 0 || r.Stdout != `{"ok":true,"action":"add-file","resource":"version","id":"IIJJKKLL"}`+"\n" {
		t.Fatalf("exit %d, stdout %q, stderr %s", r.Code, r.Stdout, r.Stderr)
	}
	parts := readMultipart(t, m.Last())
	if len(parts) != 3 || parts[0].data != `{"file_parts":["file0","file1"]}` || parts[1].name != "file0" || parts[2].filename != "b.jar" {
		t.Errorf("parts = %+v", parts)
	}
}

func TestEditBodies(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		stdin string
		path  string
		want  string
	}{
		{name: "set values", path: "/v2/version/IIJJKKLL",
			args: []string{"version", "edit", "IIJJKKLL", "--set", "featured=true", "--set", `game_versions=["1.20.1"]`,
				"--set", "name=New name", "--set", "downloads=5", "--set", "changelog=null", "--set", "x=[not json", "--set", "name=Last"},
			want: `{"featured":true,"game_versions":["1.20.1"],"name":"Last","downloads":5,"changelog":null,"x":"[not json"}`},
		{name: "data from stdin", path: "/v2/project/my-mod", stdin: `{"title":"T","license":{"id":"MIT"}}`,
			args: []string{"project", "edit", "my-mod", "--data", "-"},
			want: `{"title":"T","license":{"id":"MIT"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := testutil.NewMockAPI(t)
			m.On("PATCH", tc.path).Status(204).Empty()
			r := testutil.Run(t, m, testutil.RunOpts{Env: tokenEnv(), Stdin: tc.stdin}, tc.args...)
			if r.Code != 0 {
				t.Fatalf("exit %d, stderr %s", r.Code, r.Stderr)
			}
			last := m.Last()
			if string(last.Body) != tc.want || last.Header.Get("Content-Type") != "application/json" {
				t.Errorf("body %s (%s), want %s", last.Body, last.Header.Get("Content-Type"), tc.want)
			}
		})
	}
}

func TestProjectCreate(t *testing.T) {
	icon := filepath.Join(t.TempDir(), "icon.png")
	if err := os.WriteFile(icon, []byte("PNGDATA"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := testutil.NewMockAPI(t)
	m.On("POST", "/v2/project").Fixture("project_sodium")
	data := filepath.Join("testdata", "requests", "project_create.json")
	r := testutil.Run(t, m, testutil.RunOpts{Env: tokenEnv()}, "project", "create", "--data", data, "--icon", icon, "--fields", "id")
	if r.Code != 0 || r.Stdout != `{"id":"AANobbMI"}`+"\n" {
		t.Fatalf("exit %d, stdout %q, stderr %s", r.Code, r.Stdout, r.Stderr)
	}
	parts := readMultipart(t, m.Last())
	if len(parts) != 2 || parts[0].name != "data" || !strings.HasPrefix(parts[0].data, `{"slug":"mdr-test-mod",`) ||
		parts[1] != (formPart{"icon", "icon.png", "image/png", "PNGDATA"}) {
		t.Errorf("parts = %+v", parts)
	}
}

// TestWriteInputErrors: bad input fails before any request.
func TestWriteInputErrors(t *testing.T) {
	txt := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(txt, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	create := []string{"version", "create", "AANobbMI", "--version-number", "1", "--loader", "fabric", "--game-version", "1.20.1"}
	with := func(base []string, extra ...string) []string { return append(append([]string{}, base...), extra...) }
	cases := []struct {
		name  string
		args  []string
		errIn string
	}{
		{"no file", create, "--file is required"},
		{"wrong extension", with(create, "--file", txt), "extension must be one of"},
		{"missing file", with(create, "--file", "nope.jar"), "cannot be read"},
		{"no version number", []string{"version", "create", "AANobbMI", "--file", jarA, "--loader", "fabric", "--game-version", "1.20.1"}, "--version-number is required"},
		{"no loader", []string{"version", "create", "AANobbMI", "--file", jarA, "--version-number", "1", "--game-version", "1.20.1"}, "--loader is required"},
		{"no game version", []string{"version", "create", "AANobbMI", "--file", jarA, "--version-number", "1", "--loader", "fabric"}, "--game-version is required"},
		{"primary not a file", with(create, "--file", jarA, "--primary", jarB), "--primary"},
		{"bad dependency", with(create, "--file", jarA, "--dependency", "lithium:sometimes"), "use one of"},
		{"data with content flags", with(create, "--file", jarA, "--data", "-"), "--data cannot be combined with --version-number"},
		{"bad version type", with(create, "--file", jarA, "--version-type", "nightly"), "version-type"},
		{"edit without changes", []string{"version", "edit", "IIJJKKLL"}, "nothing to change"},
		{"edit set and data", []string{"version", "edit", "IIJJKKLL", "--set", "a=1", "--data", "-"}, "cannot be combined"},
		{"edit bad key", []string{"project", "edit", "x", "--set", "a.b=1"}, "key=value"},
		{"edit data not an object", []string{"project", "edit", "x", "--data", jarA}, "not valid JSON"},
		{"create project without data", []string{"project", "create"}, "--data is required"},
		{"create project bad icon", []string{"project", "create", "--data", "-", "--icon", jarA}, "extension must be one of"},
		{"add-file without file", []string{"version", "add-file", "IIJJKKLL"}, "--file is required"},
		{"delete invalid id", []string{"version", "delete", "nope", "--yes"}, "invalid version id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := testutil.NewMockAPI(t)
			r := testutil.Run(t, m, testutil.RunOpts{Env: tokenEnv(), Stdin: "{}"}, tc.args...)
			if r.Code != 2 || !strings.Contains(r.Stderr, tc.errIn) {
				t.Errorf("exit %d, stderr %s; want 2 and %q", r.Code, r.Stderr, tc.errIn)
			}
			if n := len(m.Requests()); n != 0 {
				t.Errorf("%d requests sent", n)
			}
		})
	}
}

// TestDestructiveConfirmation (milestone criterion 4).
func TestDestructiveConfirmation(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		tty      bool
		stdin    string
		code     int
		sent     bool
		stderrIn string
	}{
		{name: "no terminal, no --yes", args: []string{"version", "delete", "IIJJKKLL"}, code: 2,
			stderrIn: `{"error":{"code":"confirmation_required","message":"mdr version delete needs confirmation","http_status":null,"retryable":false,"hint":"re-run with --yes to confirm, or with --dry-run to preview"`},
		{name: "--yes", args: []string{"version", "delete", "IIJJKKLL", "--yes"}, sent: true},
		{name: "-y", args: []string{"version", "delete", "-y", "IIJJKKLL"}, sent: true},
		{name: "terminal, id typed", args: []string{"version", "delete", "IIJJKKLL"}, tty: true, stdin: "  IIJJKKLL \n", sent: true,
			stderrIn: "Delete version 'IIJJKKLL'? Type the id to confirm: "},
		{name: "terminal, wrong id", args: []string{"version", "delete", "IIJJKKLL"}, tty: true, stdin: "y\n", code: 2,
			stderrIn: `"code":"usage","message":"confirmation did not match; nothing was changed"`},
		{name: "terminal, end of input", args: []string{"version", "delete", "IIJJKKLL"}, tty: true, code: 2,
			stderrIn: "confirmation did not match"},
		{name: "dry run needs no confirmation", args: []string{"version", "delete", "IIJJKKLL", "--dry-run"}, stderrIn: ""},
		{name: "project, terminal, slug typed", args: []string{"project", "delete", "my-mod"}, tty: true, stdin: "my-mod\n", sent: true,
			stderrIn: "Delete project 'my-mod'? Type the id to confirm: "},
		{name: "project, no terminal", args: []string{"project", "delete", "my-mod"}, code: 2, stderrIn: "confirmation_required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := testutil.NewMockAPI(t)
			path := "/v2/version/IIJJKKLL"
			if tc.args[0] == "project" {
				path = "/v2/project/my-mod"
			}
			if tc.sent {
				m.On("DELETE", path).Status(204).Empty()
			}
			r := testutil.Run(t, m, testutil.RunOpts{Env: tokenEnv(), StdinTTY: tc.tty, Stdin: tc.stdin}, tc.args...)
			if r.Code != tc.code {
				t.Errorf("exit %d, want %d (stderr %s)", r.Code, tc.code, r.Stderr)
			}
			if !strings.Contains(r.Stderr, tc.stderrIn) {
				t.Errorf("stderr %q does not contain %q", r.Stderr, tc.stderrIn)
			}
			if n := len(m.Requests()); (n == 1) != tc.sent || n > 1 {
				t.Errorf("%d requests sent", n)
			}
			if tc.sent && !strings.Contains(r.Stdout, `"action":"delete"`) {
				t.Errorf("stdout = %q", r.Stdout)
			}
		})
	}
}

// mutatingLeaves lists the leaves that change server state.
func mutatingLeaves(t *testing.T) []string {
	t.Helper()
	root := commands.Root()
	var out []string
	for _, p := range allLeafPaths() {
		if cli.Find(root, strings.Fields(p)).Mutation != cli.ReadOnly {
			out = append(out, p)
		}
	}
	if len(out) < 7 {
		t.Fatalf("only %d mutating leaves: %v", len(out), out)
	}
	return out
}

// firstScope is the scope of a command's write endpoint.
func firstScope(t *testing.T, path string) string {
	t.Helper()
	for _, e := range cli.Find(commands.Root(), strings.Fields(path)).Endpoints {
		if e.Scope != "" {
			return e.Scope
		}
	}
	t.Fatalf("%s has no scope", path)
	return ""
}

// TestWriteCommandsNeedToken (milestone criterion 5): without a token every write
// command fails with auth_required (exit 4) before sending anything, and the hint
// names the scope. With --dry-run it prints the plan instead (exit 0): no
// Authorization header, "auth":{"token":"missing","required_scope":...} and a
// warning on stderr; still nothing is sent.
func TestWriteCommandsNeedToken(t *testing.T) {
	for _, path := range mutatingLeaves(t) {
		run := defaultRuns[path]
		for _, extra := range [][]string{nil, {"--staging"}} {
			t.Run(path+"/"+strings.Join(extra, ""), func(t *testing.T) {
				m := testutil.NewMockAPI(t)
				d := &countDoer{}
				r := testutil.Run(t, m, testutil.RunOpts{Doer: d}, run.argv(t, extra...)...)
				if r.Code != 4 || !strings.Contains(r.Stderr, `"code":"auth_required"`) {
					t.Errorf("exit %d, stderr %s", r.Code, r.Stderr)
				}
				if !strings.Contains(r.Stderr, "required scope: "+firstScope(t, path)) {
					t.Errorf("hint does not name the scope: %s", r.Stderr)
				}
				if len(m.Requests())+d.n != 0 {
					t.Errorf("requests were sent")
				}
			})
		}
		t.Run(path+"/--dry-run", func(t *testing.T) {
			d := &countDoer{}
			r := testutil.Run(t, nil, testutil.RunOpts{Doer: d}, run.argv(t, "--dry-run")...)
			scope := firstScope(t, path)
			if r.Code != 0 || d.n != 0 {
				t.Fatalf("exit %d after %d requests, stderr %s", r.Code, d.n, r.Stderr)
			}
			if want := `"auth":{"token":"missing","required_scope":"` + scope + `"}`; !strings.Contains(r.Stdout, want) ||
				strings.Contains(r.Stdout, "Authorization") {
				t.Errorf("plan %s; want %s and no Authorization header", r.Stdout, want)
			}
			if !strings.Contains(r.Stderr, "warning: no token is configured") || !strings.Contains(r.Stderr, scope) {
				t.Errorf("stderr %q; want a warning naming %s", r.Stderr, scope)
			}
		})
	}
}

// TestDryRunChecksInputsFirst: without a token, an input error comes before
// auth_required, with and without --dry-run.
func TestDryRunChecksInputsFirst(t *testing.T) {
	for _, extra := range [][]string{nil, {"--dry-run"}} {
		d := &countDoer{}
		r := testutil.Run(t, nil, testutil.RunOpts{Doer: d}, append([]string{"version", "create", "AANobbMI",
			"--file", filepath.Join(t.TempDir(), "missing.jar"), "--version-number", "1.0.0", "--loader", "fabric",
			"--game-version", "1.20.1"}, extra...)...)
		if r.Code != 2 || !strings.Contains(r.Stderr, `"code":"invalid_input"`) || d.n != 0 {
			t.Errorf("%v: exit %d after %d requests, stderr %s", extra, r.Code, d.n, r.Stderr)
		}
	}
}

// TestWriteUnauthorizedNamesScope (milestone criterion 6): a 401 with a token is
// unauthorized (exit 4) and the hint names the scope.
func TestWriteUnauthorizedNamesScope(t *testing.T) {
	for _, path := range mutatingLeaves(t) {
		run := defaultRuns[path]
		t.Run(path, func(t *testing.T) {
			r := testutil.Run(t, nil, testutil.RunOpts{Doer: statusDoer(401), Env: map[string]string{
				"MODRINTH_TOKEN": fakeToken, "MODRINTH_API_URL": "http://127.0.0.1:1"}}, run.argv(t)...)
			scope := firstScope(t, path)
			if r.Code != 4 || !strings.Contains(r.Stderr, `"code":"unauthorized"`) || !strings.Contains(r.Stderr, "the "+scope+" scope") {
				t.Errorf("exit %d, stderr %s; want unauthorized naming %s", r.Code, r.Stderr, scope)
			}
		})
	}
}

// TestWritesAreNotRetried (milestone criterion 7): a 500 on a write ends after one
// request; a 429 is retried with the same body.
func TestWritesAreNotRetried(t *testing.T) {
	for _, tc := range []struct{ method, path, leaf string }{
		{"POST", "/v2/version", "version create"},
		{"POST", "/v2/version/ygf8cVZg/file", "version add-file"},
		{"PATCH", "/v2/version/ygf8cVZg", "version edit"},
		{"DELETE", "/v2/version/ygf8cVZg", "version delete"},
		{"POST", "/v2/project", "project create"},
	} {
		t.Run(tc.leaf+"/500", func(t *testing.T) {
			m := testutil.NewMockAPI(t)
			m.On(tc.method, tc.path).Status(500).Empty()
			r := testutil.Run(t, m, testutil.RunOpts{Env: tokenEnv()}, defaultRuns[tc.leaf].argv(t)...)
			if r.Code != 8 || len(m.Requests()) != 1 {
				t.Errorf("exit %d after %d requests; want 8 after 1", r.Code, len(m.Requests()))
			}
		})
	}
	t.Run("version create/429", func(t *testing.T) {
		m := testutil.NewMockAPI(t)
		m.On("POST", "/v2/version").Status(429).Header("X-Ratelimit-Reset", "2").JSON(`{"error":"ratelimit_error"}`)
		m.On("POST", "/v2/version").Fixture("version_ygf8cVZg")
		clock := testutil.NewFakeClock()
		r := testutil.Run(t, m, testutil.RunOpts{Env: tokenEnv(), Clock: clock}, defaultRuns["version create"].argv(t)...)
		reqs := m.Requests()
		if r.Code != 0 || len(reqs) != 2 {
			t.Fatalf("exit %d after %d requests, stderr %s", r.Code, len(reqs), r.Stderr)
		}
		if !bytes.Equal(reqs[0].Body, reqs[1].Body) || reqs[0].Header.Get("Content-Type") != reqs[1].Header.Get("Content-Type") {
			t.Errorf("the retry sent a different body")
		}
		readMultipart(t, reqs[1])
	})
}

func TestAPIWrite(t *testing.T) {
	input := filepath.Join(t.TempDir(), "body.json")
	if err := os.WriteFile(input, []byte(`{"raw":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	type want struct {
		method, contentType, body, header string
	}
	cases := []struct {
		name  string
		args  []string
		stdin string
		path  string
		want  want
	}{
		{name: "-X PATCH with -f", path: "/v2/project/AANobbMI",
			args: []string{"api", "-X", "patch", "/project/AANobbMI", "-f", "title=New title", "-f", "n=3", "-f", `license={"id":"MIT"}`},
			want: want{"PATCH", "application/json", `{"title":"New title","n":3,"license":{"id":"MIT"}}`, ""}},
		{name: "-f defaults to POST", path: "/v2/thing", args: []string{"api", "/thing", "--field", "a=b"},
			want: want{"POST", "application/json", `{"a":"b"}`, ""}},
		{name: "--input file", path: "/v2/thing", args: []string{"api", "-X", "PUT", "/thing", "--input", input},
			want: want{"PUT", "application/json", `{"raw":true}`, ""}},
		{name: "--input stdin with Content-Type", path: "/v2/thing", stdin: "plain body",
			args: []string{"api", "/thing", "--input", "-", "-H", "Content-Type: text/plain", "-H", "X-Extra: yes"},
			want: want{"POST", "text/plain", "plain body", "yes"}},
		{name: "DELETE without body", path: "/v2/thing", args: []string{"api", "-X", "DELETE", "/thing"},
			want: want{"DELETE", "", "", ""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := testutil.NewMockAPI(t)
			m.On(tc.want.method, tc.path).Status(204).Empty()
			r := testutil.Run(t, m, testutil.RunOpts{Stdin: tc.stdin}, tc.args...)
			if r.Code != 0 || r.Stdout != "" {
				t.Fatalf("exit %d, stdout %q, stderr %s", r.Code, r.Stdout, r.Stderr)
			}
			last := m.Last()
			got := want{last.Method, last.Header.Get("Content-Type"), string(last.Body), last.Header.Get("X-Extra")}
			if got != tc.want {
				t.Errorf("got %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestAPIForm(t *testing.T) {
	m := testutil.NewMockAPI(t)
	m.On("POST", "/v2/version").JSON(`{"id":"IIJJKKLL"}`)
	r := testutil.Run(t, m, testutil.RunOpts{Env: tokenEnv()},
		"api", "/version", "-F", `data={"file_parts":["file0"]}`, "-F", "file0=@"+jarA, "-F", "note=hi")
	if r.Code != 0 || r.Stdout != `{"id":"IIJJKKLL"}`+"\n" {
		t.Fatalf("exit %d, stdout %q, stderr %s", r.Code, r.Stdout, r.Stderr)
	}
	parts := readMultipart(t, m.Last())
	want := []formPart{
		{"data", "", "application/json", `{"file_parts":["file0"]}`},
		{"file0", "a.jar", "application/java-archive", fileText(t, jarA)},
		{"note", "", "", "hi"},
	}
	if len(parts) != len(want) {
		t.Fatalf("parts = %+v", parts)
	}
	for i := range want {
		if parts[i] != want[i] {
			t.Errorf("part %d = %+v, want %+v", i, parts[i], want[i])
		}
	}
}

func TestAPIWriteUsage(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		errIn string
	}{
		{"GET with a body", []string{"api", "-X", "GET", "/x", "-f", "a=1"}, "GET requests take no body"},
		{"HEAD with a body", []string{"api", "-X", "head", "/x", "--input", "-"}, "HEAD requests take no body"},
		{"-f and -F", []string{"api", "/x", "-f", "a=1", "-F", "b=2"}, "cannot be combined"},
		{"-f and --input", []string{"api", "/x", "-f", "a=1", "--input", "-"}, "cannot be combined"},
		{"unknown method", []string{"api", "-X", "FETCH", "/x"}, "use one of"},
		{"bad field", []string{"api", "/x", "-f", "novalue"}, "key=value"},
		{"Content-Type with -f", []string{"api", "/x", "-f", "a=1", "-H", "Content-Type: text/plain"}, "-H Content-Type works only with --input"},
		{"forbidden header", []string{"api", "/x", "-H", "Authorization: x"}, "mdr sets Authorization itself"},
		{"paginate with POST", []string{"api", "-X", "POST", "/x", "--paginate"}, "--paginate works with GET only"},
		{"missing form file", []string{"api", "/x", "-F", "f=@nope.jar"}, "cannot read -F f=@nope.jar"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := testutil.NewMockAPI(t)
			r := testutil.Run(t, m, testutil.RunOpts{}, tc.args...)
			if r.Code != 2 || !strings.Contains(r.Stderr, tc.errIn) {
				t.Errorf("exit %d, stderr %s; want 2 and %q", r.Code, r.Stderr, tc.errIn)
			}
			if len(m.Requests()) != 0 {
				t.Errorf("requests were sent")
			}
		})
	}
}

func TestAPIDryRunWrite(t *testing.T) {
	r := testutil.Run(t, nil, testutil.RunOpts{Doer: &countDoer{}, Env: tokenEnv()},
		"api", "-X", "PATCH", "/project/AANobbMI", "-f", "title=New title", "--dry-run")
	want := `{"dry_run":true,"request":{"method":"PATCH","url":"https://api.modrinth.com/v2/project/AANobbMI","headers":{"Accept":"application/json",` +
		`"Authorization":"***","Content-Type":"application/json","User-Agent":"s8a-uk/modrinth-cli/<VERSION> (https://github.com/s8a-uk/modrinth-cli)"},` +
		`"json":{"title":"New title"}},"auth":{"token":"set"}}` + "\n"
	if r.Code != 0 || testutil.Normalize(r.Stdout) != want {
		t.Errorf("exit %d, stdout\n%s\nwant\n%s", r.Code, testutil.Normalize(r.Stdout), want)
	}

	// Without a token: the same plan without Authorization, and a warning.
	r = testutil.Run(t, nil, testutil.RunOpts{Doer: &countDoer{}},
		"api", "-X", "PATCH", "/project/AANobbMI", "-f", "title=New title", "--dry-run")
	if r.Code != 0 || !strings.Contains(r.Stdout, `"auth":{"token":"missing"}`) || strings.Contains(r.Stdout, "Authorization") ||
		!strings.Contains(r.Stderr, "warning: no token is configured") {
		t.Errorf("no token: exit %d, stdout %s, stderr %s", r.Code, r.Stdout, r.Stderr)
	}
	// A GET plan has no auth member.
	r = testutil.Run(t, nil, testutil.RunOpts{Doer: &countDoer{}}, "api", "/project/AANobbMI", "--dry-run")
	if r.Code != 0 || strings.Contains(r.Stdout, `"auth"`) || r.Stderr != "" {
		t.Errorf("GET: exit %d, stdout %s, stderr %s", r.Code, r.Stdout, r.Stderr)
	}
}

func TestAPIPaginate(t *testing.T) {
	m := testutil.NewMockAPI(t)
	m.On("GET", "/v2/search").Query("limit", "2").JSON(`{"hits":[{"n":1},{"n":2}],"offset":0,"limit":2,"total_hits":3}`)
	m.On("GET", "/v2/search").Query("offset", "2").Query("limit", "2").JSON(`{"hits":[{"n":3}],"offset":2,"limit":2,"total_hits":3}`)
	r := testutil.Run(t, m, testutil.RunOpts{}, "api", "/search", "--query", "limit=2", "--paginate")
	if r.Code != 0 || r.Stdout != "{\"n\":1}\n{\"n\":2}\n{\"n\":3}\n" || len(m.Requests()) != 2 {
		t.Errorf("exit %d after %d requests, stdout %q, stderr %s", r.Code, len(m.Requests()), r.Stdout, r.Stderr)
	}

	m = testutil.NewMockAPI(t)
	m.On("GET", "/v2/statistics").JSON(`{"projects":1}`)
	r = testutil.Run(t, m, testutil.RunOpts{}, "api", "/statistics", "--paginate")
	if r.Code != 0 || r.Stdout != `{"projects":1}`+"\n" || !strings.Contains(r.Stderr, "note: response is not paginated") {
		t.Errorf("not paginated: exit %d, stdout %q, stderr %q", r.Code, r.Stdout, r.Stderr)
	}
}
