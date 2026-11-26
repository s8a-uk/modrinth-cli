package api_test

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/s8a-uk/modrinth-cli/internal/api"
	"github.com/s8a-uk/modrinth-cli/internal/apperr"
	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
	"github.com/s8a-uk/modrinth-cli/internal/testutil"
)

// noteLog records notes and ignores the rest.
type noteLog struct{ notes []string }

func (*noteLog) Request(string, string)           {}
func (*noteLog) Response(int, time.Duration, int) {}
func (*noteLog) Headers(string, http.Header)      {}
func (l *noteLog) Note(format string, args ...any) {
	l.notes = append(l.notes, fmt.Sprintf(format, args...))
}

type setup struct {
	maxWait time.Duration
	noRetry bool
	dryRun  bool
	token   string
	doer    api.Doer
}

func newClient(t *testing.T, m *testutil.MockAPI, s setup) (*api.Client, *testutil.FakeClock, *noteLog) {
	t.Helper()
	clock, log := testutil.NewFakeClock(), &noteLog{}
	base := "http://127.0.0.1:1"
	if m != nil {
		base = m.URL
	}
	c := api.New(api.Options{BaseURL: base, Token: s.token, UserAgent: "ua", Timeout: 30 * time.Second,
		MaxWait: s.maxWait, NoRetry: s.noRetry, DryRun: s.dryRun, Doer: s.doer, Clock: clock, Log: log})
	return c, clock, log
}

func code(err error) apperr.Code {
	if err == nil {
		return ""
	}
	return apperr.From(err).Code
}

func sec(ns ...int) []time.Duration {
	out := []time.Duration{}
	for _, n := range ns {
		out = append(out, time.Duration(n)*time.Second)
	}
	return out
}

func rateLimit(m *testutil.MockAPI, method, reset string) *testutil.Stub {
	return m.On(method, "/v2/x").Status(429).Header("X-Ratelimit-Remaining", "0").Header("X-Ratelimit-Reset", reset)
}

func TestRetryStateMachine(t *testing.T) {
	cases := []struct {
		name     string
		s        setup
		method   string
		req      api.Request
		stubs    func(m *testutil.MockAPI)
		code     apperr.Code // "" = success
		slept    []time.Duration
		requests int
		retryS   int64 // retry_after on rate_limited
		notes    []string
	}{
		{name: "429 then success", s: setup{maxWait: time.Minute},
			stubs: func(m *testutil.MockAPI) { rateLimit(m, "GET", "7"); m.On("GET", "/v2/x").JSON(`{}`) },
			slept: sec(8), requests: 2, notes: []string{"rate limited; waiting 8s (use --max-wait 0 to fail fast)"}},
		{name: "429 gives up after 3 retries", s: setup{maxWait: time.Minute},
			stubs: func(m *testutil.MockAPI) { rateLimit(m, "GET", "1").Times(4) },
			code:  apperr.RateLimited, slept: sec(2, 2, 2), requests: 4, retryS: 2},
		{name: "429 over max-wait", s: setup{maxWait: time.Minute},
			stubs: func(m *testutil.MockAPI) { rateLimit(m, "GET", "30").Times(2) },
			code:  apperr.RateLimited, slept: sec(31), requests: 2, retryS: 31},
		{name: "429 max-wait 0 fails fast", s: setup{maxWait: 0},
			stubs: func(m *testutil.MockAPI) { rateLimit(m, "GET", "7") },
			code:  apperr.RateLimited, slept: sec(), requests: 1, retryS: 8},
		{name: "429 Retry-After without reset", s: setup{maxWait: time.Minute},
			stubs: func(m *testutil.MockAPI) {
				m.On("GET", "/v2/x").Status(429).WithoutHeader("X-Ratelimit-Reset").Header("Retry-After", "12")
				m.On("GET", "/v2/x").JSON(`{}`)
			},
			slept: sec(12), requests: 2},
		{name: "429 without any hint waits 5s", s: setup{maxWait: time.Minute},
			stubs: func(m *testutil.MockAPI) {
				m.On("GET", "/v2/x").Status(429).WithoutHeader("X-Ratelimit-Reset")
				m.On("GET", "/v2/x").JSON(`{}`)
			},
			slept: sec(5), requests: 2},
		{name: "429 is retried for POST", s: setup{maxWait: time.Minute}, method: "POST",
			req: api.Request{JSON: map[string]any{"a": 1}},
			stubs: func(m *testutil.MockAPI) {
				rateLimit(m, "POST", "0")
				m.On("POST", "/v2/x").JSON(`{}`)
			},
			slept: sec(1), requests: 2},
		{name: "5xx GET retried twice then success", s: setup{maxWait: time.Minute},
			stubs: func(m *testutil.MockAPI) {
				m.On("GET", "/v2/x").Status(500).Times(2)
				m.On("GET", "/v2/x").JSON(`{}`)
			},
			slept: sec(1, 3), requests: 3, notes: []string{"request failed (server error (500)); retrying in 3s"}},
		{name: "5xx GET gives up", s: setup{maxWait: time.Minute},
			stubs: func(m *testutil.MockAPI) { m.On("GET", "/v2/x").Status(503).Times(3) },
			code:  apperr.ServerError, slept: sec(1, 3), requests: 3},
		{name: "5xx POST not retried", s: setup{maxWait: time.Minute}, method: "POST",
			stubs: func(m *testutil.MockAPI) { m.On("POST", "/v2/x").Status(500) },
			code:  apperr.ServerError, slept: sec(), requests: 1},
		{name: "5xx idempotent POST retried", s: setup{maxWait: time.Minute}, method: "POST", req: api.Request{Idempotent: true},
			stubs: func(m *testutil.MockAPI) {
				m.On("POST", "/v2/x").Status(502)
				m.On("POST", "/v2/x").JSON(`{}`)
			},
			slept: sec(1), requests: 2},
		{name: "no-retry 429", s: setup{maxWait: time.Minute, noRetry: true},
			stubs: func(m *testutil.MockAPI) { rateLimit(m, "GET", "7") },
			code:  apperr.RateLimited, slept: sec(), requests: 1, retryS: 8},
		{name: "no-retry 5xx", s: setup{maxWait: time.Minute, noRetry: true},
			stubs: func(m *testutil.MockAPI) { m.On("GET", "/v2/x").Status(500) },
			code:  apperr.ServerError, slept: sec(), requests: 1},
		{name: "4xx not retried", s: setup{maxWait: time.Minute},
			stubs: func(m *testutil.MockAPI) { m.On("GET", "/v2/x").Status(404).Empty() },
			code:  apperr.NotFound, slept: sec(), requests: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := testutil.NewMockAPI(t)
			tc.stubs(m)
			c, clock, log := newClient(t, m, tc.s)
			tc.req.Method, tc.req.Path = tc.method, "/x"
			_, err := c.Do(context.Background(), tc.req)
			if code(err) != tc.code {
				t.Fatalf("err = %v, want code %q", err, tc.code)
			}
			if !slices.Equal(clock.Slept, tc.slept) {
				t.Errorf("slept %v, want %v", clock.Slept, tc.slept)
			}
			if n := len(m.Requests()); n != tc.requests {
				t.Errorf("%d requests, want %d", n, tc.requests)
			}
			if tc.code == apperr.RateLimited {
				e := apperr.From(err)
				if got := int64(e.RetryAfter / time.Second); got != tc.retryS || !strings.Contains(string(e.JSON()), fmt.Sprintf(`"retry_after_s":%d`, tc.retryS)) {
					t.Errorf("retry after %ds (%s), want %d", got, e.JSON(), tc.retryS)
				}
			}
			if tc.notes != nil && !slices.Equal(log.notes, tc.notes) {
				t.Errorf("notes %q, want %q", log.notes, tc.notes)
			}
			if tc.method == "POST" && tc.req.JSON != nil {
				for _, r := range m.Requests() {
					if string(r.Body) != `{"a":1}` {
						t.Errorf("body %q on a retry", r.Body)
					}
				}
			}
		})
	}
}

func TestPreflight(t *testing.T) {
	// The first response says nothing remains for 10s: the next request waits 11s first.
	m := testutil.NewMockAPI(t)
	m.On("GET", "/v2/x").Header("X-Ratelimit-Remaining", "0").Header("X-Ratelimit-Reset", "10").JSON(`{}`)
	m.On("GET", "/v2/x").JSON(`{}`)
	c, clock, _ := newClient(t, m, setup{maxWait: time.Minute})
	for i := range 2 {
		if _, err := c.Do(context.Background(), api.Request{Path: "/x"}); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}
	if !slices.Equal(clock.Slept, sec(11)) {
		t.Errorf("slept %v, want [11s]", clock.Slept)
	}

	// Over max-wait: fail without sending.
	m = testutil.NewMockAPI(t)
	m.On("GET", "/v2/x").Header("X-Ratelimit-Remaining", "0").Header("X-Ratelimit-Reset", "10").JSON(`{}`)
	c, clock, _ = newClient(t, m, setup{maxWait: 5 * time.Second})
	_, _ = c.Do(context.Background(), api.Request{Path: "/x"})
	_, err := c.Do(context.Background(), api.Request{Path: "/x"})
	if e := apperr.From(err); e.Code != apperr.RateLimited || e.RetryAfter != 11*time.Second || e.Hint != "retry after 11s" {
		t.Errorf("err = %v (%s)", err, e.JSON())
	}
	if len(m.Requests()) != 1 || len(clock.Slept) != 0 {
		t.Errorf("requests %d, slept %v", len(m.Requests()), clock.Slept)
	}

	// Once the reset time has passed there is no wait.
	m = testutil.NewMockAPI(t)
	m.On("GET", "/v2/x").Header("X-Ratelimit-Remaining", "0").Header("X-Ratelimit-Reset", "10").JSON(`{}`)
	m.On("GET", "/v2/x").JSON(`{}`)
	c, clock, _ = newClient(t, m, setup{maxWait: time.Minute})
	_, _ = c.Do(context.Background(), api.Request{Path: "/x"})
	clock.Advance(10 * time.Second)
	if _, err := c.Do(context.Background(), api.Request{Path: "/x"}); err != nil || len(clock.Slept) != 0 {
		t.Errorf("err %v, slept %v", err, clock.Slept)
	}
}

// errDoer fails every request with err and counts them.
type errDoer struct {
	mu  sync.Mutex
	n   int
	err error
}

func (d *errDoer) Do(*http.Request) (*http.Response, error) {
	d.mu.Lock()
	d.n++
	d.mu.Unlock()
	return nil, d.err
}

func TestTransportRetries(t *testing.T) {
	wrap := func(err error) error { return &url.Error{Op: "Get", URL: "http://127.0.0.1:1/v2/x", Err: err} }
	cases := []struct {
		name   string
		err    error
		method string
		code   apperr.Code
		tries  int
	}{
		{"network GET retried", wrap(errors.New("connection reset")), "GET", apperr.Network, 3},
		{"dns GET retried", wrap(&net.DNSError{Err: "no such host", Name: "api.modrinth.com", IsNotFound: true}), "GET", apperr.DNS, 3},
		{"network POST not retried", wrap(errors.New("connection reset")), "POST", apperr.Network, 1},
		{"tls not retried", wrap(x509.UnknownAuthorityError{}), "GET", apperr.TLS, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &errDoer{err: tc.err}
			c, clock, _ := newClient(t, nil, setup{maxWait: time.Minute, doer: d})
			_, err := c.Do(context.Background(), api.Request{Method: tc.method, Path: "/x"})
			if code(err) != tc.code || d.n != tc.tries {
				t.Errorf("code %q after %d tries, want %q after %d", code(err), d.n, tc.code, tc.tries)
			}
			if tc.tries == 3 && !slices.Equal(clock.Slept, sec(1, 3)) {
				t.Errorf("slept %v", clock.Slept)
			}
		})
	}
}

func TestCancelDuringWait(t *testing.T) {
	m := testutil.NewMockAPI(t)
	rateLimit(m, "GET", "7")
	clock := testutil.NewFakeClock()
	ctx, cancel := context.WithCancel(context.Background())
	c := api.New(api.Options{BaseURL: m.URL, UserAgent: "ua", MaxWait: time.Minute, Clock: clock,
		Doer: cancelDoer{cancel: cancel, next: http.DefaultClient}})
	_, err := c.Do(ctx, api.Request{Path: "/x"})
	if e := apperr.From(err); e.Code != apperr.Interrupted || e.Code.ExitCode() != 130 {
		t.Errorf("err = %v", err)
	}
	if len(clock.Slept) != 0 || len(m.Requests()) != 1 {
		t.Errorf("slept %v, %d requests", clock.Slept, len(m.Requests()))
	}
}

// cancelDoer sends the request, then cancels the context (as SIGINT would).
type cancelDoer struct {
	cancel context.CancelFunc
	next   api.Doer
}

func (d cancelDoer) Do(req *http.Request) (*http.Response, error) {
	resp, err := d.next.Do(req)
	d.cancel()
	return resp, err
}

func TestDryRunPlan(t *testing.T) {
	m := testutil.NewMockAPI(t)
	c, _, _ := newClient(t, m, setup{dryRun: true, token: "mrp_secret"})
	_, err := c.Do(context.Background(), api.Request{Method: "PATCH", Path: "/version/{id}", PathArgs: []string{"IIJJKKLL"},
		JSON: map[string]any{"featured": true}})
	var dry *api.DryRunError
	if !errors.As(err, &dry) {
		t.Fatalf("err = %v, want *DryRunError", err)
	}
	var b strings.Builder
	_ = jsonx.Encode(&b, dry.Plan, "")
	want := `{"dry_run":true,"request":{"method":"PATCH","url":"` + m.URL + `/v2/version/IIJJKKLL","headers":{"Accept":"application/json",` +
		`"Authorization":"***","Content-Type":"application/json","User-Agent":"ua"},"json":{"featured":true}}}` + "\n"
	if b.String() != want {
		t.Errorf("plan\n got %s\nwant %s", b.String(), want)
	}
	if len(m.Requests()) != 0 {
		t.Errorf("requests were sent: %v", m.Requests())
	}

	// No token: no Authorization key. A raw body shows its size only.
	c, _, _ = newClient(t, m, setup{dryRun: true})
	_, err = c.Do(context.Background(), api.Request{Method: "POST", Path: "/x", RawBody: []byte("12345"), ContentType: "text/plain"})
	b.Reset()
	_ = errors.As(err, &dry)
	_ = jsonx.Encode(&b, dry.Plan, "")
	if s := b.String(); strings.Contains(s, "Authorization") || !strings.Contains(s, `"Content-Type":"text/plain"`) || !strings.Contains(s, `"body_bytes":5}`) {
		t.Errorf("plan %s", s)
	}
}

// TestMissingToken: without a token, a request with a scope fails with the
// MissingToken error and is not sent; RequireToken refuses every request; a dry run
// shows the auth member instead.
func TestMissingToken(t *testing.T) {
	missing := func(scope string) error { return apperr.New(apperr.AuthRequired, "no token (%s)", scope) }
	m := testutil.NewMockAPI(t)
	m.On("GET", "/v2/statistics").JSON(`{}`)
	for _, tc := range []struct {
		require bool
		r       api.Request
		want    apperr.Code
	}{
		{false, api.Request{Path: "/statistics"}, ""},
		{false, api.Request{Path: "/user", Scope: "USER_READ"}, apperr.AuthRequired},
		{true, api.Request{Path: "/statistics"}, apperr.AuthRequired},
	} {
		c := api.New(api.Options{BaseURL: m.URL, UserAgent: "ua", MissingToken: missing, RequireToken: tc.require})
		if _, err := c.Do(context.Background(), tc.r); code(err) != tc.want {
			t.Errorf("require=%t %s: %v, want %q", tc.require, tc.r.Path, err, tc.want)
		}
	}
	if n := len(m.Requests()); n != 1 {
		t.Errorf("%d requests, want 1", n)
	}

	c := api.New(api.Options{BaseURL: m.URL, UserAgent: "ua", MissingToken: missing, DryRun: true})
	_, err := c.Do(context.Background(), api.Request{Method: "DELETE", Path: "/version/x", Scope: "VERSION_DELETE"})
	var dry *api.DryRunError
	if !errors.As(err, &dry) {
		t.Fatalf("err = %v, want *DryRunError", err)
	}
	auth, _ := dry.Plan.Get("auth")
	var b strings.Builder
	_ = jsonx.Encode(&b, auth, "")
	if want := `{"token":"missing","required_scope":"VERSION_DELETE"}` + "\n"; b.String() != want {
		t.Errorf("auth = %s, want %s", b.String(), want)
	}
}
