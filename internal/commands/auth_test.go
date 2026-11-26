package commands_test

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
	"github.com/s8a-uk/modrinth-cli/internal/testutil"
)

// configDir returns a config directory (not created yet) and the env that selects it.
func configDir(t *testing.T) (string, map[string]string) {
	dir := filepath.Join(t.TempDir(), "cfg")
	return dir, map[string]string{"MODRINTH_CONFIG_DIR": dir}
}

func withEnv(base map[string]string, kv ...string) map[string]string {
	out := map[string]string{}
	for k, v := range base {
		out[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		out[kv[i]] = kv[i+1]
	}
	return out
}

func field(t *testing.T, out, path string) string {
	t.Helper()
	v, err := jsonx.Parse([]byte(out))
	if err != nil {
		t.Fatalf("not JSON: %q", out)
	}
	f, ok := v.Lookup(path)
	if !ok {
		return "<missing>"
	}
	switch f.Kind {
	case jsonx.String:
		return f.Str
	case jsonx.Null:
		return "null"
	}
	var b strings.Builder
	_ = jsonx.Encode(&b, f, "")
	return strings.TrimSpace(b.String())
}

// TestAuthLoginStatusLogout: login saves the token (0600 in a 0700 directory),
// status reports token_source file, logout removes it.
func TestAuthLoginStatusLogout(t *testing.T) {
	dir, env := configDir(t)
	m := testutil.NewMockAPI(t)
	m.On("GET", "/v2/user").Fixture("user_jellysquid3")
	r := testutil.Run(t, m, testutil.RunOpts{Env: env, Stdin: fakeToken + "\n"}, "auth", "login", "--with-token")
	cred := filepath.Join(dir, "credentials.json")
	if r.Code != 0 || field(t, r.Stdout, "username") != "jellysquid3" || field(t, r.Stdout, "credentials_file") != cred {
		t.Fatalf("login: code %d stdout %q stderr %q", r.Code, r.Stdout, r.Stderr)
	}
	if got := m.Last().Header.Get("Authorization"); got != fakeToken {
		t.Errorf("login sent Authorization %q", got)
	}
	if r.Stderr != "note: token saved in plain text to "+cred+"\n" {
		t.Errorf("login stderr %q", r.Stderr)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(cred)
		di, _ := os.Stat(dir)
		if fi.Mode().Perm() != 0o600 || di.Mode().Perm() != 0o700 {
			t.Errorf("modes: file %v dir %v", fi.Mode().Perm(), di.Mode().Perm())
		}
	}

	m.On("GET", "/v2/user").Fixture("user_jellysquid3")
	r = testutil.Run(t, m, testutil.RunOpts{Env: env}, "auth", "status")
	if r.Code != 0 || field(t, r.Stdout, "token_source") != "file" || field(t, r.Stdout, "token") != "mrp_…FAKE" ||
		field(t, r.Stdout, "authenticated") != "true" {
		t.Errorf("status: code %d stdout %q", r.Code, r.Stdout)
	}

	r = testutil.Run(t, m, testutil.RunOpts{Env: withEnv(env, "MODRINTH_TOKEN", "mrp_OTHEROTHEROTHEROTHER")}, "auth", "logout")
	want := `{"ok":true,"action":"logout","resource":"auth","removed":true}` + "\n"
	if r.Code != 0 || r.Stdout != want || r.Stderr != "note: MODRINTH_TOKEN is still set in the environment\n" {
		t.Errorf("logout: code %d stdout %q stderr %q", r.Code, r.Stdout, r.Stderr)
	}
	if _, err := os.Stat(cred); !os.IsNotExist(err) {
		t.Errorf("credentials.json still there: %v", err)
	}
	n := len(m.Requests())
	r = testutil.Run(t, m, testutil.RunOpts{Env: env}, "auth", "status")
	want = `{"authenticated":false,"token_source":null,"api_url":"` + m.URL + `/v2","staging":false}` + "\n"
	if r.Code != 0 || r.Stdout != want || len(m.Requests()) != n {
		t.Errorf("status without token: code %d stdout %q, %d requests", r.Code, r.Stdout, len(m.Requests())-n)
	}
}

func TestAuthLoginErrors(t *testing.T) {
	dir, env := configDir(t)
	cred := filepath.Join(dir, "credentials.json")
	cases := []struct {
		name  string
		stdin string
		args  []string
		setup func(m *testutil.MockAPI)
		code  int
		err   string
	}{
		{"no --with-token", fakeToken, nil, nil, 2, `"code":"usage","message":"--with-token is required"`},
		{"empty stdin", "", []string{"--with-token"}, nil, 2, `"message":"no token on stdin"`},
		{"bad token", "mrp_SECRET with spaces SECRET\n", []string{"--with-token"}, nil, 2, `"code":"invalid_input"`},
		{"too long", strings.Repeat("a", 5000), []string{"--with-token"}, nil, 2, `longer than 4096 bytes`},
		{"rejected", fakeToken, []string{"--with-token"},
			func(m *testutil.MockAPI) {
				m.On("GET", "/v2/user").Status(401).JSON(`{"error":"unauthorized","description":"Invalid token"}`)
			}, 4, `"code":"unauthorized"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := testutil.NewMockAPI(t)
			if tc.setup != nil {
				tc.setup(m)
			}
			r := testutil.Run(t, m, testutil.RunOpts{Env: env, Stdin: tc.stdin}, append([]string{"auth", "login"}, tc.args...)...)
			if r.Code != tc.code || !strings.Contains(r.Stderr, tc.err) || strings.Contains(r.Stderr, "SECRET") {
				t.Errorf("code %d stderr %q", r.Code, r.Stderr)
			}
			if tc.setup == nil && len(m.Requests()) != 0 {
				t.Errorf("requests sent: %d", len(m.Requests()))
			}
			if _, err := os.Stat(cred); !os.IsNotExist(err) {
				t.Errorf("credentials saved: %v", err)
			}
		})
	}

	// --dry-run prints the plan and saves nothing.
	m := testutil.NewMockAPI(t)
	r := testutil.Run(t, m, testutil.RunOpts{Env: env, Stdin: fakeToken}, "auth", "login", "--with-token", "--dry-run")
	if r.Code != 0 || !strings.Contains(r.Stdout, `"dry_run":true`) || !strings.Contains(r.Stdout, `"Authorization":"***"`) ||
		strings.Contains(r.Stdout, "FAKE") || len(m.Requests()) != 0 {
		t.Errorf("dry-run: code %d stdout %q", r.Code, r.Stdout)
	}
	if _, err := os.Stat(cred); !os.IsNotExist(err) {
		t.Errorf("dry-run saved credentials: %v", err)
	}

	// On a terminal, a prompt goes to stderr first. --staging saves staging_token.
	m.On("GET", "/v2/user").Fixture("user_jellysquid3")
	r = testutil.Run(t, m, testutil.RunOpts{Env: env, Stdin: fakeToken, StdinTTY: true}, "auth", "login", "--with-token", "--staging")
	if r.Code != 0 || !strings.HasPrefix(r.Stderr, "Paste your Modrinth PAT and press Enter (input is visible):\n") ||
		field(t, r.Stdout, "staging") != "true" {
		t.Errorf("staging login: code %d stdout %q stderr %q", r.Code, r.Stdout, r.Stderr)
	}
	data, _ := os.ReadFile(cred)
	if !strings.Contains(string(data), `"staging_token": "`+fakeToken+`"`) || strings.Contains(string(data), `"token"`) {
		t.Errorf("credentials.json: %s", data)
	}
}

func TestAuthStatusTokenSources(t *testing.T) {
	dir, env := configDir(t)
	tokFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokFile, []byte(fakeToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "credentials.json"), []byte(`{"token":"mrp_CREDCREDCREDCREDCREDCRED"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		env    map[string]string
		source string
		masked string
	}{
		{env, "file", "mrp_…CRED"},
		{withEnv(env, "MODRINTH_TOKEN_FILE", tokFile), "token_file", "mrp_…FAKE"},
		{withEnv(env, "MODRINTH_TOKEN_FILE", tokFile, "MODRINTH_TOKEN", "mrp_ENVENVENVENVENVENVENVX"), "env", "mrp_…ENVX"},
	}
	for _, tc := range cases {
		m := testutil.NewMockAPI(t)
		m.On("GET", "/v2/user").Fixture("user_jellysquid3")
		r := testutil.Run(t, m, testutil.RunOpts{Env: tc.env}, "auth", "status")
		if r.Code != 0 || field(t, r.Stdout, "token_source") != tc.source || field(t, r.Stdout, "token") != tc.masked {
			t.Errorf("%s: code %d stdout %q stderr %q", tc.source, r.Code, r.Stdout, r.Stderr)
		}
	}

	// A rejected token: unauthorized, exit 4.
	m := testutil.NewMockAPI(t)
	m.On("GET", "/v2/user").Status(401).JSON(`{"error":"unauthorized","description":"Invalid token"}`)
	r := testutil.Run(t, m, testutil.RunOpts{Env: env}, "auth", "status")
	if r.Code != 4 || !strings.Contains(r.Stderr, `"code":"unauthorized"`) {
		t.Errorf("rejected: code %d stderr %q", r.Code, r.Stderr)
	}
}

// TestUserMeRequiresToken: without a token, user me (and whoami) fail with
// auth_required before sending anything; --dry-run prints the plan instead.
func TestUserMeRequiresToken(t *testing.T) {
	for _, args := range [][]string{{"user", "me"}, {"whoami"}} {
		m := testutil.NewMockAPI(t)
		r := testutil.Run(t, m, testutil.RunOpts{}, args...)
		if r.Code != 4 || !strings.Contains(lastLine(r.Stderr), `{"error":{"code":"auth_required","message":"authentication required"`) ||
			!strings.Contains(r.Stderr, "required scope: USER_READ") || len(m.Requests()) != 0 {
			t.Errorf("%v: code %d stderr %q requests %d", args, r.Code, r.Stderr, len(m.Requests()))
		}
	}
	{
		m := testutil.NewMockAPI(t)
		r := testutil.Run(t, m, testutil.RunOpts{}, "whoami", "--dry-run")
		if r.Code != 0 || !strings.Contains(r.Stdout, `"auth":{"token":"missing","required_scope":"USER_READ"}`) || len(m.Requests()) != 0 {
			t.Errorf("whoami --dry-run: code %d stdout %q stderr %q", r.Code, r.Stdout, r.Stderr)
		}
	}
	m := testutil.NewMockAPI(t)
	m.On("GET", "/v2/user").Fixture("user_jellysquid3")
	r := testutil.Run(t, m, testutil.RunOpts{Env: map[string]string{"MODRINTH_TOKEN": fakeToken}}, "whoami", "--fields", "username")
	if r.Code != 0 || r.Stdout != `{"username":"jellysquid3"}`+"\n" {
		t.Errorf("whoami: code %d stdout %q stderr %q", r.Code, r.Stdout, r.Stderr)
	}
}

// TestConfigCommands: set/unset write config.json; get/list show flag > env > file >
// default with the source.
func TestConfigCommands(t *testing.T) {
	dir, env := configDir(t)
	run := func(env map[string]string, args ...string) testutil.RunResult {
		t.Helper()
		r := testutil.Run(t, nil, testutil.RunOpts{Env: env}, args...)
		if r.Code != 0 {
			t.Fatalf("%v: code %d stderr %q", args, r.Code, r.Stderr)
		}
		return r
	}
	run(env, "config", "set", "timeout", "45")
	run(env, "config", "set", "max_wait", "5")
	run(env, "config", "set", "output", "ndjson")
	run(env, "config", "set", "user_agent_contact", "file@example.com")
	data, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	want := "{\n  \"timeout\": 45,\n  \"max_wait\": 5,\n  \"output\": \"ndjson\",\n  \"user_agent_contact\": \"file@example.com\"\n}\n"
	if string(data) != want {
		t.Errorf("config.json:\n%s", data)
	}

	r := run(withEnv(env, "MODRINTH_MAX_WAIT", "7", "MODRINTH_TIMEOUT", "50"), "config", "list", "--timeout", "9", "-o", "json")
	want = `[{"key":"output","value":"json","source":"flag"},{"key":"staging","value":false,"source":"default"},` +
		`{"key":"user_agent_contact","value":"file@example.com","source":"file"},{"key":"timeout","value":9,"source":"flag"},` +
		`{"key":"max_wait","value":7,"source":"env"}]` + "\n"
	if r.Stdout != want {
		t.Errorf("list:\n got %s\nwant %s", r.Stdout, want)
	}
	// The file's output setting applies: ndjson.
	r = run(env, "config", "get", "timeout")
	if r.Stdout != `{"key":"timeout","value":45,"source":"file"}`+"\n" {
		t.Errorf("get: %q", r.Stdout)
	}
	r = run(env, "config", "list", "-o", "table")
	want = "KEY                 VALUE             SOURCE\noutput              table             flag\nstaging             no                default\n" +
		"user_agent_contact  file@example.com  file\ntimeout             45                file\nmax_wait            5                 file\n"
	if r.Stdout != want {
		t.Errorf("table:\n%s", r.Stdout)
	}
	r = run(withEnv(env, "MODRINTH_TIMEOUT", "50"), "config", "unset", "timeout")
	if r.Stderr != "note: the environment overrides timeout (see: mdr config get timeout)\n" {
		t.Errorf("unset note: %q", r.Stderr)
	}
	if r = run(env, "config", "get", "timeout", "-o", "json"); field(t, r.Stdout, "source") != "default" {
		t.Errorf("after unset: %q", r.Stdout)
	}

	for _, tc := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"config", "get", "nope"}, 2, `"code":"usage","message":"unknown config key \"nope\""`},
		{[]string{"config", "set", "timeout", "0"}, 2, `"code":"invalid_input"`},
		{[]string{"config", "set", "staging", "maybe"}, 2, `"code":"invalid_input"`},
		{[]string{"config", "set", "token", "x"}, 2, `mdr auth login`},
	} {
		r := testutil.Run(t, nil, testutil.RunOpts{Env: env}, tc.args...)
		if r.Code != tc.code || !strings.Contains(r.Stderr, tc.want) {
			t.Errorf("%v: code %d stderr %q", tc.args, r.Code, r.Stderr)
		}
	}

	// A broken config.json stops every command but schema.
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	r = testutil.Run(t, nil, testutil.RunOpts{Env: env}, "config", "list")
	if r.Code != 2 || !strings.Contains(r.Stderr, `config file is not valid JSON`) || !strings.Contains(r.Stderr, `"hint":"fix or delete the file"`) {
		t.Errorf("broken: code %d stderr %q", r.Code, r.Stderr)
	}
	if r = testutil.Run(t, nil, testutil.RunOpts{Env: env}, "schema"); r.Code != 0 {
		t.Errorf("schema with a broken config: %d", r.Code)
	}

	// config path follows the OS rules when MODRINTH_CONFIG_DIR is not set.
	if runtime.GOOS == "linux" {
		r = testutil.Run(t, nil, testutil.RunOpts{Env: map[string]string{"MODRINTH_CONFIG_DIR": "", "HOME": "/home/u"}}, "config", "path")
		if field(t, r.Stdout, "config_dir") != "/home/u/.config/modrinth-cli" {
			t.Errorf("path: %q", r.Stdout)
		}
		r = testutil.Run(t, nil, testutil.RunOpts{Env: map[string]string{"MODRINTH_CONFIG_DIR": ""}}, "config", "path")
		if r.Code != 1 || !strings.Contains(r.Stderr, `"code":"io_error"`) {
			t.Errorf("no dir: code %d stderr %q", r.Code, r.Stderr)
		}
	}
}

// TestUserAgentContact: the contact comes from the environment or config.json, the
// suffix from the environment.
func TestUserAgentContact(t *testing.T) {
	dir, env := configDir(t)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"user_agent_contact":"file@example.com"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		env  map[string]string
		want string
	}{
		{env, " (file@example.com; +https://github.com/s8a-uk/modrinth-cli)"},
		{withEnv(env, "MODRINTH_USER_AGENT_CONTACT", "env@example.com", "MODRINTH_USER_AGENT_SUFFIX", "my-tool/1.2"),
			" (env@example.com; +https://github.com/s8a-uk/modrinth-cli) via my-tool/1.2"},
	} {
		d := &recordDoer{}
		r := testutil.Run(t, nil, testutil.RunOpts{Doer: d, Env: tc.env}, "stats")
		if r.Code != 0 || len(d.reqs) != 1 || !strings.HasSuffix(d.reqs[0].Header.Get("User-Agent"), tc.want) {
			t.Errorf("code %d, UA %q, want suffix %q", r.Code, d.reqs[0].Header.Get("User-Agent"), tc.want)
		}
	}
}

// TestTokenNotSentToCustomHost: a host that is not the Modrinth API (nor loopback)
// gets no Authorization header, with a warning, unless explicitly allowed.
func TestTokenNotSentToCustomHost(t *testing.T) {
	env := map[string]string{"MODRINTH_API_URL": "https://example.test", "MODRINTH_TOKEN": canary}
	d := &recordDoer{}
	r := testutil.Run(t, nil, testutil.RunOpts{Doer: d, Env: env}, "stats")
	if r.Code != 0 || len(d.reqs) != 1 || d.reqs[0].URL.Host != "example.test" {
		t.Fatalf("code %d, %d requests, stderr %q", r.Code, len(d.reqs), r.Stderr)
	}
	if got := d.reqs[0].Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization sent to a custom host: %q", got)
	}
	if !strings.HasPrefix(r.Stderr, "warning: token not sent to custom host https://example.test") {
		t.Errorf("stderr %q", r.Stderr)
	}
	d = &recordDoer{}
	r = testutil.Run(t, nil, testutil.RunOpts{Doer: d, Env: withEnv(env, "MODRINTH_ALLOW_TOKEN_TO_CUSTOM_HOST", "1")}, "stats")
	if r.Code != 0 || d.reqs[0].Header.Get("Authorization") != canary || strings.Contains(r.Stderr, "warning") {
		t.Errorf("allowed: code %d stderr %q", r.Code, r.Stderr)
	}
}

// statusDoer answers every request with an empty response of one status.
type statusDoer int

func (s statusDoer) Do(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: int(s), Proto: "HTTP/1.1", Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
}

// TestNoTokenLeak runs every command with a canary token from each token source,
// at each verbosity, with --dry-run, against 401 and 500 responses and with an
// invalid --fields, in json and table output, and checks that the canary never
// appears on stdout or stderr.
func TestNoTokenLeak(t *testing.T) {
	const mark = "LEAKCANARY"
	sources := map[string]func(t *testing.T) map[string]string{
		"env": func(t *testing.T) map[string]string {
			_, env := configDir(t)
			return withEnv(env, "MODRINTH_TOKEN", canary, "MODRINTH_STAGING_TOKEN", canary)
		},
		"token_file": func(t *testing.T) map[string]string {
			_, env := configDir(t)
			f := filepath.Join(t.TempDir(), "tok")
			if err := os.WriteFile(f, []byte(canary+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return withEnv(env, "MODRINTH_TOKEN_FILE", f)
		},
		"file": func(t *testing.T) map[string]string {
			dir, env := configDir(t)
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "credentials.json"), []byte(`{"token":"`+canary+`"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			return env
		},
	}
	modes := []struct {
		name  string
		extra []string
		code  int // 0 = success against the mock; else the status the API answers with
	}{
		{name: "quiet", extra: []string{"-q"}},
		{name: "default"},
		{name: "-v", extra: []string{"-v"}},
		{name: "-vv", extra: []string{"-vv"}},
		{name: "dry-run", extra: []string{"--dry-run", "-vv"}, code: -1},
		{name: "401", extra: []string{"-vv", "--no-retry"}, code: 401},
		{name: "500", extra: []string{"-vv", "--no-retry"}, code: 500},
		{name: "bad fields", extra: []string{"--fields", "zz_nope"}, code: -1},
	}
	for _, path := range allLeafPaths() {
		run := defaultRuns[path]
		for srcName, mkEnv := range sources {
			for _, mode := range modes {
				for _, format := range []string{"json", "table"} {
					t.Run(path+"/"+srcName+"/"+mode.name+"/"+format, func(t *testing.T) {
						opts := run.opts(testutil.RunOpts{Env: mkEnv(t)})
						opts.Stdin = canary + "\n" // auth login reads the token from stdin
						if srcName != "env" {
							delete(opts.Env, "MODRINTH_TOKEN") // the default run's own token
						}
						var m *testutil.MockAPI
						switch {
						case mode.code == 0 && run.setup != nil:
							m = testutil.NewMockAPI(t)
							run.setup(m)
						case mode.code > 0:
							opts.Doer = statusDoer(mode.code)
							opts.Env["MODRINTH_API_URL"] = "http://127.0.0.1:1"
						default:
							opts.Doer = statusDoer(599) // never reached (dry-run, or no request)
							opts.Env["MODRINTH_API_URL"] = "http://127.0.0.1:1"
						}
						args := run.argv(t, mode.extra...)
						args = append(args, "-o", format)
						r := testutil.Run(t, m, opts, args...)
						if strings.Contains(r.Stdout+r.Stderr, mark) {
							t.Errorf("token leaked (exit %d):\nstdout %s\nstderr %s", r.Code, r.Stdout, r.Stderr)
						}
					})
				}
			}
		}
	}
}
