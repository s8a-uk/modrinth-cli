package commands_test

import (
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/s8a-uk/modrinth-cli/internal/testutil"
)

func TestAPI(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		env    map[string]string
		setup  func(m *testutil.MockAPI)
		code   int
		stdout string // exact; compared when non-empty or when the code is 0
		stderr string // substring
	}{
		{name: "json keeps key order", args: []string{"api", "/statistics"},
			setup: func(m *testutil.MockAPI) {
				m.On("GET", "/v2/statistics").JSON(`{"projects":3,"versions":2,"files":1,"authors":9}`)
			},
			stdout: `{"projects":3,"versions":2,"files":1,"authors":9}` + "\n"},
		{name: "query", args: []string{"api", "/search", "--query", "query=sodium", "--query", "limit=1"},
			setup: func(m *testutil.MockAPI) {
				m.On("GET", "/v2/search").Query("query", "sodium").Query("limit", "1").JSON(`{"hits":[],"offset":0,"limit":1,"total_hits":0}`)
			},
			stdout: `{"hits":[],"offset":0,"limit":1,"total_hits":0}` + "\n"},
		{name: "api version", args: []string{"api", "--api-version", "v3", "/project/sodium"},
			setup:  func(m *testutil.MockAPI) { m.On("GET", "/v3/project/sodium").JSON(`{"id":"AANobbMI"}`) },
			stdout: `{"id":"AANobbMI"}` + "\n"},
		{name: "api version none calls the root", args: []string{"api", "--api-version", "none", "/updates/sodium/forge_updates.json"},
			setup: func(m *testutil.MockAPI) {
				m.On("GET", "/updates/sodium/forge_updates.json").JSON(`{"homepage":"https://modrinth.com/mod/sodium","promos":{}}`)
			},
			stdout: `{"homepage":"https://modrinth.com/mod/sodium","promos":{}}` + "\n"},
		{name: "api version none not found names the root path", args: []string{"api", "--api-version", "none", "/nope"},
			setup: func(m *testutil.MockAPI) { m.On("GET", "/nope").Status(404).Empty() },
			code:  3, stderr: `"message":"/nope not found"`},
		{name: "non-json body is printed as is", args: []string{"api", "/text"},
			setup: func(m *testutil.MockAPI) {
				m.On("GET", "/v2/text").Body("text/plain", []byte("plain text"))
			},
			stdout: "plain text"},
		{name: "empty body prints nothing", args: []string{"api", "/empty"},
			setup: func(m *testutil.MockAPI) { m.On("GET", "/v2/empty").Status(204).Empty() }},
		{name: "include", args: []string{"api", "-i", "/statistics"},
			setup: func(m *testutil.MockAPI) {
				m.On("GET", "/v2/statistics").Header("X-Test", "yes").JSON(`{}`)
			},
			stdout: "{}\n", stderr: "X-Test: yes\n"},
		{name: "410", args: []string{"api", "/old"},
			setup: func(m *testutil.MockAPI) {
				m.On("GET", "/v2/old").Status(410).Body("text/plain", []byte("API v2 is gone"))
			},
			code: 5,
			stderr: `{"error":{"code":"api_deprecated","message":"this API version has been retired","http_status":410,"retryable":false,` +
				`"hint":"upgrade modrinth-cli: https://github.com/s8a-uk/modrinth-cli/releases","request":{"method":"GET","url":"<BASE>/v2/old"},` +
				`"api_error":null,"api_description":null,"body_excerpt":"API v2 is gone"}}`},
		{name: "html error body", args: []string{"api", "/broken"},
			setup: func(m *testutil.MockAPI) {
				m.On("GET", "/v2/broken").Status(502).Body("text/html", []byte("<html>Bad Gateway</html>")).Times(3) // GET: 2 retries
			},
			code: 8, stderr: `"body_excerpt":"<html>Bad Gateway</html>"`},
		{name: "api error body", args: []string{"api", "/search", "--query", "facets=x"},
			setup: func(m *testutil.MockAPI) {
				m.On("GET", "/v2/search").Status(400).JSON(`{"error":"invalid_input","description":"Error while parsing facets"}`)
			},
			code: 5, stderr: `"message":"Error while parsing facets","http_status":400,"retryable":false,"request":{"method":"GET","url":"<BASE>/v2/search?facets=x"},"api_error":"invalid_input","api_description":"Error while parsing facets"}}`},
		{name: "404", args: []string{"api", "/project/zz"},
			setup: func(m *testutil.MockAPI) { m.On("GET", "/v2/project/zz").Status(404).Empty() },
			code:  3, stderr: `"message":"/v2/project/zz not found"`},
		{name: "absolute url rejected", args: []string{"api", "https://example.com/x"}, code: 2, stderr: `"code":"invalid_input"`},
		{name: "dot-dot rejected", args: []string{"api", "/a/../b"}, code: 2, stderr: `"code":"invalid_input"`},
		{name: "version in path rejected", args: []string{"api", "/v2/project/x"}, code: 2, stderr: `"hint":"use --api-version"`},
		{name: "bad query rejected", args: []string{"api", "/search", "--query", "novalue"}, code: 2, stderr: `"code":"invalid_input"`},
		{name: "bad api version", args: []string{"api", "/x", "--api-version", "v9"}, code: 2},
		{name: "fields of a JSON response", args: []string{"api", "/x", "--fields", "id,a.b"},
			setup:  func(m *testutil.MockAPI) { m.On("GET", "/v2/x").JSON(`{"n":1,"a":[{"b":2,"c":3}],"id":"i"}`) },
			stdout: `{"id":"i","a":[{"b":2}]}` + "\n"},
		{name: "fields with full", args: []string{"api", "/x", "--fields", "id", "--full"}, code: 2, stderr: "cannot be used together"},
		{name: "full is a no-op", args: []string{"api", "/x", "--full"},
			setup:  func(m *testutil.MockAPI) { m.On("GET", "/v2/x").JSON(`{"n":1}`) },
			stdout: `{"n":1}` + "\n"},
		{name: "unknown field of a JSON response", args: []string{"api", "/x", "--fields", "nn"}, code: 2,
			setup:  func(m *testutil.MockAPI) { m.On("GET", "/v2/x").JSON(`{"n":1}`) },
			stderr: `"hint":"did you mean: n? see the whole response without --fields"`},
		{name: "unknown field after a write is a warning", args: []string{"api", "-X", "POST", "/x", "--fields", "nn"},
			setup:  func(m *testutil.MockAPI) { m.On("POST", "/v2/x").JSON(`{"n":1}`) },
			stdout: `{"nn":null}` + "\n", stderr: `warning: unknown field "nn" for "api"`},
		{name: "fields on a non-JSON response", args: []string{"api", "/x", "--fields", "id"},
			setup:  func(m *testutil.MockAPI) { m.On("GET", "/v2/x").Body("text/plain", []byte("hi")) },
			stdout: "hi", stderr: "warning: --fields ignored: the response is not JSON"},
		{name: "bad base url", args: []string{"api", "/x"}, env: map[string]string{"MODRINTH_API_URL": "http://example.com"}, code: 2,
			stderr: "MODRINTH_API_URL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := testutil.NewMockAPI(t)
			if tc.setup != nil {
				tc.setup(m)
			}
			r := testutil.Run(t, m, testutil.RunOpts{Env: tc.env}, tc.args...)
			if r.Code != tc.code {
				t.Errorf("exit code = %d, want %d (stderr %q)", r.Code, tc.code, r.Stderr)
			}
			if (tc.stdout != "" || tc.code == 0) && r.Stdout != tc.stdout {
				t.Errorf("stdout = %q, want %q", r.Stdout, tc.stdout)
			}
			if tc.code != 0 && r.Stdout != "" {
				t.Errorf("stdout on error = %q", r.Stdout)
			}
			if got := testutil.Normalize(r.Stderr); !strings.Contains(got, tc.stderr) {
				t.Errorf("stderr = %q, want it to contain %q", got, tc.stderr)
			}
			if tc.setup == nil && len(m.Requests()) != 0 {
				t.Errorf("requests were sent: %v", m.Requests())
			}
		})
	}
}

// recordDoer records requests and answers with a fixed response or error.
type recordDoer struct {
	mu   sync.Mutex
	reqs []*http.Request
	err  error
}

func (d *recordDoer) Do(req *http.Request) (*http.Response, error) {
	d.mu.Lock()
	d.reqs = append(d.reqs, req)
	d.mu.Unlock()
	if d.err != nil {
		return nil, d.err
	}
	return &http.Response{StatusCode: 200, Proto: "HTTP/2.0", Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{"projects":1}`))}, nil
}

func TestStagingURL(t *testing.T) {
	d := &recordDoer{}
	r := testutil.Run(t, nil, testutil.RunOpts{Doer: d}, "api", "/statistics", "--staging", "-v")
	if r.Code != 0 || len(d.reqs) != 1 {
		t.Fatalf("code %d, %d requests, stderr %q", r.Code, len(d.reqs), r.Stderr)
	}
	if got := d.reqs[0].URL.String(); got != "https://staging-api.modrinth.com/v2/statistics" {
		t.Errorf("URL = %q", got)
	}
	if !strings.HasPrefix(r.Stderr, "> GET https://staging-api.modrinth.com/v2/statistics\n< 200 (") {
		t.Errorf("-v output = %q", r.Stderr)
	}
	// MODRINTH_STAGING selects staging too, and the production token is not sent there.
	d = &recordDoer{}
	r = testutil.Run(t, nil, testutil.RunOpts{Doer: d, Env: map[string]string{"MODRINTH_STAGING": "1", "MODRINTH_TOKEN": "mrp_PRODUCTIONTOKENPRODUCTION"}},
		"api", "/statistics")
	if r.Code != 0 || d.reqs[0].URL.Host != "staging-api.modrinth.com" || d.reqs[0].Header.Get("Authorization") != "" {
		t.Errorf("MODRINTH_STAGING: code %d, host %q, auth %q", r.Code, d.reqs[0].URL.Host, d.reqs[0].Header.Get("Authorization"))
	}
}

func TestTLSError(t *testing.T) {
	d := &recordDoer{err: &url.Error{Op: "Get", URL: "https://api.modrinth.com/v2/statistics", Err: x509.UnknownAuthorityError{}}}
	r := testutil.Run(t, nil, testutil.RunOpts{Doer: d}, "api", "/statistics")
	want := `{"error":{"code":"tls","message":"x509: certificate signed by unknown authority","http_status":null,"retryable":false,` +
		`"hint":"install system CA certificates (e.g. the ca-certificates package) or set SSL_CERT_FILE",` +
		`"request":{"method":"GET","url":"https://api.modrinth.com/v2/statistics"}}}` + "\n"
	if r.Code != 7 || r.Stderr != want {
		t.Errorf("code %d, stderr\n got %q\nwant %q", r.Code, r.Stderr, want)
	}
	d = &recordDoer{err: errors.New("connection refused")}
	if r := testutil.Run(t, nil, testutil.RunOpts{Doer: d}, "project", "get", "sodium"); r.Code != 7 || !strings.Contains(r.Stderr, `"code":"network"`) {
		t.Errorf("network: code %d, stderr %q", r.Code, r.Stderr)
	}
}

const canary = "mrp_LEAKCANARYLEAKCANARYLEAKCANARY"

// TestUserAgentAndAuthHeader runs every network command against the mock API with a
// token and checks the User-Agent and Authorization of every recorded request.
func TestUserAgentAndAuthHeader(t *testing.T) {
	reUA := regexp.MustCompile(`^s8a-uk/modrinth-cli/\S+ \(.+\)$`)
	for _, path := range leafPathsUsingNetwork() {
		run, ok := defaultRuns[path]
		if !ok {
			t.Errorf("command %q has no entry in defaultRuns", path)
			continue
		}
		m := testutil.NewMockAPI(t)
		run.setup(m)
		opts := run.opts(testutil.RunOpts{Env: map[string]string{"MODRINTH_TOKEN": canary}})
		if run.stdin != "" {
			opts.Stdin = canary + "\n" // auth login sends the token it reads
		}
		r := testutil.Run(t, m, opts, run.argv(t, "-vv")...)
		if r.Code != 0 {
			t.Fatalf("%v: code %d, stderr %q", run.args, r.Code, r.Stderr)
		}
		for _, req := range m.Requests() {
			if ua := req.Header.Get("User-Agent"); !reUA.MatchString(ua) {
				t.Errorf("%v: User-Agent = %q", run.args, ua)
			}
			auth, hasAuth := req.Header.Get("Authorization"), len(req.Header.Values("Authorization")) > 0
			switch {
			case strings.HasPrefix(req.Path, "/data/"): // the CDN (version download) never gets the token
				if hasAuth {
					t.Errorf("%v: Authorization sent to the CDN: %q", run.args, auth)
				}
			case auth != canary || strings.HasPrefix(auth, "Bearer "):
				t.Errorf("%v: Authorization = %q, want the raw token", run.args, auth)
			}
		}
		if strings.Contains(r.Stdout+r.Stderr, "LEAKCANARY") {
			t.Errorf("%v: token leaked to the output", run.args)
		}
		if !strings.Contains(r.Stderr, "> Authorization: ***\n") || !strings.Contains(r.Stderr, "> User-Agent: s8a-uk/modrinth-cli/") {
			t.Errorf("%v: -vv headers = %q", run.args, r.Stderr)
		}
	}
}
