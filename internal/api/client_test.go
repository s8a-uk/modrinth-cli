package api

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/s8a-uk/modrinth-cli/internal/apperr"
)

// fakeDoer returns a canned response or error and records the request.
type fakeDoer struct {
	status int
	header http.Header
	body   string
	err    error
	got    *http.Request
}

func (f *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	f.got = req
	if f.err != nil {
		return nil, f.err
	}
	h := f.header
	if h == nil {
		h = http.Header{}
	}
	return &http.Response{StatusCode: f.status, Proto: "HTTP/2.0", Header: h, Body: io.NopCloser(strings.NewReader(f.body))}, nil
}

func client(d Doer, token string) *Client {
	// NoRetry: these tests are about a single attempt; retry_test.go covers retries.
	return New(Options{BaseURL: "https://api.modrinth.com", Token: token, UserAgent: "ua", Timeout: 30 * time.Second, NoRetry: true, Doer: d})
}

func TestBuildURL(t *testing.T) {
	c := client(nil, "")
	cases := []struct {
		r    Request
		want string
	}{
		{Request{Path: "/project/{id}", PathArgs: []string{"sodium"}}, "https://api.modrinth.com/v2/project/sodium"},
		{Request{Path: "/project/{id}", PathArgs: []string{"a/b c?"}}, "https://api.modrinth.com/v2/project/a%2Fb%20c%3F"},
		{Request{Path: "/project/{id}/version/{n}", PathArgs: []string{"x", "1.0+mc"}}, "https://api.modrinth.com/v2/project/x/version/1.0+mc"},
		{Request{APIVersion: "v3", Path: "/statistics"}, "https://api.modrinth.com/v3/statistics"},
		{Request{Path: "/search", Query: url.Values{"query": {"sodium"}, "limit": {"1"}, "facets": {`[["a:b"]]`}}},
			"https://api.modrinth.com/v2/search?facets=%5B%5B%22a%3Ab%22%5D%5D&limit=1&query=sodium"},
	}
	for _, tc := range cases {
		got, err := c.BuildURL(tc.r)
		if err != nil || got != tc.want {
			t.Errorf("BuildURL(%+v) = %q, %v; want %q", tc.r, got, err, tc.want)
		}
	}
	for _, r := range []Request{{Path: "/p/{id}"}, {Path: "/p", PathArgs: []string{"x"}}, {Path: "/p/{id", PathArgs: []string{"x"}}} {
		var ae *apperr.Error
		if _, err := c.BuildURL(r); !errors.As(err, &ae) || ae.Code != apperr.Internal {
			t.Errorf("BuildURL(%+v) error = %v, want internal", r, err)
		}
	}
}

func TestUserAgent(t *testing.T) {
	cases := []struct{ contact, suffix, want string }{
		{"", "", "s8a-uk/modrinth-cli/0.1.0 (https://github.com/s8a-uk/modrinth-cli)"},
		{"you@example.com", "", "s8a-uk/modrinth-cli/0.1.0 (you@example.com; +https://github.com/s8a-uk/modrinth-cli)"},
		{"", "my-tool/1.2", "s8a-uk/modrinth-cli/0.1.0 (https://github.com/s8a-uk/modrinth-cli) via my-tool/1.2"},
	}
	for _, tc := range cases {
		if got := UserAgent("0.1.0", tc.contact, tc.suffix); got != tc.want {
			t.Errorf("UserAgent(%q, %q) = %q, want %q", tc.contact, tc.suffix, got, tc.want)
		}
	}
}

func TestTokenAllowed(t *testing.T) {
	cases := []struct {
		url    string
		custom bool
		want   bool
	}{
		{"https://api.modrinth.com", false, true},
		{"https://staging-api.modrinth.com", false, true},
		{"http://api.modrinth.com", false, false},
		{"http://127.0.0.1:1234", false, true},
		{"http://localhost:1", false, true},
		{"http://[::1]:1", false, true},
		{"https://api.modrinth.com.evil.example", false, false},
		{"https://mirror.example", false, false},
		{"https://mirror.example", true, true},
	}
	for _, tc := range cases {
		if got := TokenAllowed(tc.url, tc.custom); got != tc.want {
			t.Errorf("TokenAllowed(%q, %t) = %t, want %t", tc.url, tc.custom, got, tc.want)
		}
	}
}

func TestHeaders(t *testing.T) {
	d := &fakeDoer{status: 200, body: `{}`}
	if _, err := client(d, "mrp_tok").Do(context.Background(), Request{Path: "/x"}); err != nil {
		t.Fatal(err)
	}
	if got := d.got.Header.Get("Authorization"); got != "mrp_tok" {
		t.Errorf("Authorization = %q, want the raw token without Bearer", got)
	}
	if d.got.Header.Get("User-Agent") != "ua" || d.got.Header.Get("Accept") != "application/json" {
		t.Errorf("headers = %v", d.got.Header)
	}
	if d.got.Header.Get("Accept-Encoding") != "" {
		t.Error("Accept-Encoding must be left to net/http")
	}
	d = &fakeDoer{status: 200}
	_, _ = client(d, "mrp_tok").Do(context.Background(), Request{Path: "/x", NoAuth: true})
	if _, ok := d.got.Header["Authorization"]; ok {
		t.Error("NoAuth sent Authorization")
	}
	_, _ = client(d, "").Do(context.Background(), Request{Path: "/x"})
	if _, ok := d.got.Header["Authorization"]; ok {
		t.Error("empty token sent Authorization")
	}
}

func TestSuccessBodies(t *testing.T) {
	cases := []struct {
		name   string
		ctype  string
		body   string
		isJSON bool
		errOut apperr.Code
	}{
		{"json", "application/json", `{"b":1,"a":2}`, true, ""},
		{"empty", "", "", false, ""},
		{"text", "text/plain", "hello", false, ""},
		{"broken json", "application/json; charset=utf-8", `{"a":`, false, apperr.UnexpectedResponse},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &fakeDoer{status: 200, header: http.Header{"Content-Type": {tc.ctype}}, body: tc.body}
			resp, err := client(d, "").Do(context.Background(), Request{Path: "/x"})
			if tc.errOut != "" {
				var ae *apperr.Error
				if !errors.As(err, &ae) || ae.Code != tc.errOut {
					t.Fatalf("err = %v, want %s", err, tc.errOut)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if resp.IsJSON != tc.isJSON || string(resp.Body) != tc.body {
				t.Errorf("IsJSON=%t body=%q", resp.IsJSON, resp.Body)
			}
			if !tc.isJSON && !resp.Value.IsNull() {
				t.Error("Value is not null")
			}
		})
	}
}

func TestStatusClassification(t *testing.T) {
	html := "<html>" + strings.Repeat("x", 300) + "</html>"
	cases := []struct {
		name     string
		status   int
		header   http.Header
		body     string
		token    string
		req      Request
		code     apperr.Code
		message  string
		hint     string
		apiError string
		excerpt  bool
	}{
		{name: "404 empty", status: 404, req: Request{Resource: "project 'zz'", NotFoundHint: "try search"},
			code: apperr.NotFound, message: "project 'zz' not found", hint: "try search"},
		{name: "404 json keeps our message", status: 404, body: `{"error":"not_found","description":"The requested item(s) were not found or no authorization to access the requested item(s)"}`,
			code: apperr.NotFound, message: "resource not found", hint: "check the id or slug", apiError: "not_found"},
		{name: "404 html", status: 404, body: html, code: apperr.NotFound, message: "resource not found", excerpt: true},
		{name: "400 description", status: 400, body: `{"error":"invalid_input","description":"bad facet"}`,
			code: apperr.BadRequest, message: "bad facet", apiError: "invalid_input"},
		{name: "400 bare", status: 400, code: apperr.BadRequest, message: "bad request"},
		{name: "401 no token", status: 401, req: Request{Scope: "USER_READ"}, code: apperr.AuthRequired,
			message: "authentication required", hint: "set MODRINTH_TOKEN or run: mdr auth login --with-token; required scope: USER_READ"},
		{name: "401 token", status: 401, token: "mrp_x", body: `{"error":"unauthorized","description":"Authentication Error: Invalid Authentication Credentials"}`,
			req: Request{Scope: "VERSION_CREATE"}, code: apperr.Unauthorized,
			message: "Authentication Error: Invalid Authentication Credentials", hint: "check that the token is valid, not expired and has the VERSION_CREATE scope", apiError: "unauthorized"},
		{name: "401 token no scope", status: 401, token: "mrp_x", code: apperr.Unauthorized, message: "token was rejected",
			hint: "check that the token is valid and not expired"},
		{name: "403", status: 403, code: apperr.Forbidden, message: "forbidden"},
		{name: "409", status: 409, code: apperr.Conflict, message: "conflict"},
		{name: "410", status: 410, body: "Gone", code: apperr.APIDeprecated, message: "this API version has been retired",
			hint: "upgrade modrinth-cli: https://github.com/s8a-uk/modrinth-cli/releases", excerpt: true},
		{name: "413", status: 413, code: apperr.PayloadTooLarge, message: "payload too large"},
		{name: "418", status: 418, code: apperr.ClientError, message: "unexpected status 418"},
		{name: "302", status: 302, code: apperr.ClientError, message: "unexpected status 302"},
		{name: "429", status: 429, header: http.Header{"X-Ratelimit-Reset": {"41"}}, code: apperr.RateLimited, message: "rate limited", hint: "retry after 42s"},
		{name: "500", status: 500, body: "oops", code: apperr.ServerError, message: "server error (500)",
			hint: "this is usually temporary; retry later", excerpt: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &fakeDoer{status: tc.status, header: tc.header, body: tc.body}
			tc.req.Path = "/x"
			_, err := client(d, tc.token).Do(context.Background(), tc.req)
			var ae *apperr.Error
			if !errors.As(err, &ae) {
				t.Fatalf("err = %v", err)
			}
			if ae.Code != tc.code || ae.Message != tc.message || (tc.hint != "" && ae.Hint != tc.hint) {
				t.Errorf("got %s %q hint %q; want %s %q hint %q", ae.Code, ae.Message, ae.Hint, tc.code, tc.message, tc.hint)
			}
			if ae.HTTPStatus != tc.status || ae.Request == nil || ae.Request.URL != "https://api.modrinth.com/v2/x" || ae.Request.Method != "GET" {
				t.Errorf("status/request = %d %+v", ae.HTTPStatus, ae.Request)
			}
			if ae.APIError != tc.apiError {
				t.Errorf("APIError = %q, want %q", ae.APIError, tc.apiError)
			}
			if tc.excerpt != (ae.BodyExcerpt != "") || len(ae.BodyExcerpt) > excerptBytes {
				t.Errorf("BodyExcerpt = %q", ae.BodyExcerpt)
			}
		})
	}
}

func TestExcerptIsValidUTF8(t *testing.T) {
	body := strings.Repeat("a", 199) + "é" // the 2-byte rune straddles the 200-byte cut
	if got := excerpt([]byte(body)); !strings.HasSuffix(got, "�") || strings.Contains(got, "é") {
		t.Errorf("excerpt = %q", got[190:])
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestTransportErrors(t *testing.T) {
	wrap := func(err error) error { return &url.Error{Op: "Get", URL: "https://api.modrinth.com/v2/x", Err: err} }
	cases := []struct {
		name string
		err  error
		code apperr.Code
		hint string
	}{
		{"tls unknown authority", wrap(x509.UnknownAuthorityError{}), apperr.TLS,
			"install system CA certificates (e.g. the ca-certificates package) or set SSL_CERT_FILE"},
		{"tls hostname", wrap(x509.HostnameError{Certificate: &x509.Certificate{}, Host: "x"}), apperr.TLS, ""},
		{"dns", wrap(&net.DNSError{Err: "no such host", Name: "api.modrinth.com", IsNotFound: true}), apperr.DNS, "check network connectivity and DNS"},
		{"timeout", wrap(timeoutErr{}), apperr.Timeout, "increase --timeout (currently 30s)"},
		{"deadline", wrap(context.DeadlineExceeded), apperr.Timeout, ""},
		{"other", wrap(errors.New("connection refused")), apperr.Network, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client(&fakeDoer{err: tc.err}, "").Do(context.Background(), Request{Path: "/x"})
			var ae *apperr.Error
			if !errors.As(err, &ae) || ae.Code != tc.code || (tc.hint != "" && ae.Hint != tc.hint) {
				t.Fatalf("err = %v (hint %q), want %s", err, ae.Hint, tc.code)
			}
			if strings.Contains(ae.Message, "https://") {
				t.Errorf("message contains the URL: %q", ae.Message)
			}
			if ae.Request == nil || ae.HTTPStatus != 0 {
				t.Errorf("request = %+v status = %d", ae.Request, ae.HTTPStatus)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client(&fakeDoer{err: wrap(context.Canceled)}, "").Do(ctx, Request{Path: "/x"})
	if ae := apperr.From(err); ae.Code != apperr.Interrupted {
		t.Errorf("canceled: %v", err)
	}
}

func TestBodyLimit(t *testing.T) {
	d := &bigDoer{}
	_, err := client(d, "").Do(context.Background(), Request{Path: "/x"})
	if ae := apperr.From(err); ae.Code != apperr.UnexpectedResponse {
		t.Errorf("err = %v, want unexpected_response", err)
	}
}

type bigDoer struct{}

func (bigDoer) Do(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(io.LimitReader(zeros{}, MaxBody+10))}, nil
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func TestGetProjectRequiresJSON(t *testing.T) {
	d := &fakeDoer{status: 200, header: http.Header{"Content-Type": {"text/html"}}, body: "<html>"}
	_, err := client(d, "").GetProject(context.Background(), "sodium")
	if ae := apperr.From(err); ae.Code != apperr.UnexpectedResponse {
		t.Errorf("err = %v", err)
	}
	d = &fakeDoer{status: 200, body: `{"id":"AANobbMI"}`}
	v, err := client(d, "").GetProject(context.Background(), "sodium")
	if err != nil || v.Members[0].Value.Str != "AANobbMI" || d.got.URL.String() != "https://api.modrinth.com/v2/project/sodium" {
		t.Errorf("GetProject = %v, %v (%s)", v, err, d.got.URL)
	}
}
