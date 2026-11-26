// Package api is the Modrinth HTTP client: request building, User-Agent, auth,
// rate limits and retries, response reading and error classification, dry runs,
// plus thin endpoint functions.
package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/s8a-uk/modrinth-cli/internal/apperr"
	"github.com/s8a-uk/modrinth-cli/internal/buildinfo"
	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
	"github.com/s8a-uk/modrinth-cli/internal/validate"
)

// Doer sends HTTP requests. *http.Client satisfies it.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Logger receives -v / -vv output. output.Stderr satisfies it.
type Logger interface {
	Request(method, url string)
	Response(status int, elapsed time.Duration, remaining int) // remaining < 0 = unknown
	Headers(dir string, h http.Header)                         // dir is ">" or "<"; implementations mask Authorization
	Note(format string, args ...any)
}

// Options configure a Client.
type Options struct {
	BaseURL   string // e.g. "https://api.modrinth.com" (no trailing / or /v2)
	Token     string // "" = never send
	UserAgent string
	Timeout   time.Duration // per request; 0 = none
	MaxWait   time.Duration // total time Do may wait on rate limits; 0 = never wait
	NoRetry   bool          // no retries at all (rate limits and transient failures)
	DryRun    bool          // build requests but do not send them (Do returns *DryRunError)
	// MissingToken is set when no token is configured. It returns the auth_required
	// error for a request that needs one (scope: its required scope, or "").
	MissingToken func(scope string) error
	// RequireToken makes Do refuse every request with MissingToken (a write command
	// run without a token and without --dry-run: nothing is sent).
	RequireToken bool
	Doer         Doer   // nil = NewHTTPClient()
	Clock        Clock  // nil = the real clock
	Log          Logger // nil = silent
}

// Client calls the Modrinth API. It is not safe for concurrent use.
type Client struct {
	opts Options
	rate rateState
}

// New returns a Client.
func New(opts Options) *Client {
	if opts.Doer == nil {
		opts.Doer = NewHTTPClient()
	}
	if opts.Clock == nil {
		opts.Clock = realClock{}
	}
	if opts.Log == nil {
		opts.Log = nopLogger{}
	}
	return &Client{opts: opts}
}

// Request describes one API call.
type Request struct {
	Method      string
	APIVersion  string   // "" = "v2"; "none" = no version prefix
	Path        string   // "/project/{id}"; each {...} is replaced by the next PathArgs value
	PathArgs    []string // escaped with url.PathEscape
	Query       url.Values
	JSON        any         // non-nil: sent as a JSON body
	RawBody     []byte      // non-nil: sent as is (exclusive with JSON and Multipart)
	ContentType string      // for RawBody; "" = application/json
	Multipart   *Multipart  // non-nil: sent as multipart/form-data (exclusive with JSON and RawBody)
	Idempotent  bool        // retry transient failures even though the method is not GET/HEAD
	Header      http.Header // extra headers
	Scope       string      // required token scope (for 401 hints and dry runs)
	// MayWrite marks a request without a known Scope that may change data (api with
	// a method other than GET and HEAD); its dry run shows whether a token is set.
	MayWrite     bool
	Resource     string // noun phrase for 404 messages, e.g. "project 'sodium'"
	NotFoundHint string // hint for 404; "" = "check the id or slug"
	NoAuth       bool   // never send the token
	LongRunning  bool   // no per-request timeout
	// SendInDryRun sends the request even with Options.DryRun: a read that a
	// command needs before the request the dry run stops at.
	SendInDryRun bool
	// NotFoundMessage replaces the 404 message "<Resource> not found".
	NotFoundMessage string
}

// Response is a successful (2xx) response.
type Response struct {
	Status int
	Proto  string
	Header http.Header
	Body   []byte
	Value  jsonx.Value // parsed body when IsJSON; null for an empty body
	IsJSON bool
}

// MaxBody is the largest response body read.
const MaxBody = 64 << 20

// excerptBytes is the length of BodyExcerpt.
const excerptBytes = 200

// NewHTTPClient returns the HTTP client for the API. It never follows redirects.
func NewHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			DialContext:         (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout: 10 * time.Second,
			ForceAttemptHTTP2:   true,
			MaxIdleConnsPerHost: 2,
			IdleConnTimeout:     30 * time.Second,
			TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// Info is what other HTTP users (downloads) need to know about a Client. It never
// holds the token.
type Info struct {
	BaseURL   string
	UserAgent string
	Timeout   time.Duration
	DryRun    bool
	Log       Logger
}

// Info returns the settings of c that downloads share.
func (c *Client) Info() Info {
	return Info{BaseURL: c.opts.BaseURL, UserAgent: c.opts.UserAgent, Timeout: c.opts.Timeout, DryRun: c.opts.DryRun, Log: c.opts.Log}
}

// maxDownloadRedirects is the number of redirects a download follows.
const maxDownloadRedirects = 3

// NewDownloadHTTPClient returns the HTTP client for file downloads. It follows at
// most 3 redirects, each only if check accepts the new URL (the CDN host allowlist).
// Callers never set Authorization on download requests.
func NewDownloadHTTPClient(check func(u *url.URL) error) *http.Client {
	c := NewHTTPClient()
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > maxDownloadRedirects {
			return fmt.Errorf("stopped after %d redirects", maxDownloadRedirects)
		}
		if err := check(req.URL); err != nil {
			return err
		}
		req.Header.Del("Authorization")
		return nil
	}
	return c
}

// UserAgent builds "<product>/<version> (<contact>)[ via <suffix>]".
func UserAgent(version, contact, suffix string) string {
	c := buildinfo.RepoURL
	if contact != "" {
		c = contact + "; +" + buildinfo.RepoURL
	}
	ua := fmt.Sprintf("%s/%s (%s)", buildinfo.UAProduct, version, c)
	if suffix != "" {
		ua += " via " + suffix
	}
	return ua
}

// TokenAllowed reports whether a token may be sent to baseURL: the Modrinth API
// hosts over https, loopback hosts (tests), or any host when allowCustom is set.
func TokenAllowed(baseURL string, allowCustom bool) bool {
	u, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	switch {
	case u.Scheme == "https" && (host == "api.modrinth.com" || host == "staging-api.modrinth.com"):
		return true
	case validate.IsLoopback(host):
		return true
	}
	return allowCustom
}

// BuildURL returns the full URL of r.
func (c *Client) BuildURL(r Request) (string, error) {
	ver := r.APIVersion
	if ver == "" {
		ver = "v2"
	}
	prefix := "/" + ver
	if ver == "none" {
		prefix = "" // routes outside the versioned API, e.g. /updates/{id}/forge_updates.json
	}
	var path strings.Builder
	rest, args := r.Path, r.PathArgs
	for {
		i := strings.IndexByte(rest, '{')
		if i < 0 {
			path.WriteString(rest)
			break
		}
		j := strings.IndexByte(rest[i:], '}')
		if j < 0 || len(args) == 0 {
			return "", apperr.New(apperr.Internal, "path %q does not match its %d arguments", r.Path, len(r.PathArgs))
		}
		path.WriteString(rest[:i])
		path.WriteString(url.PathEscape(args[0]))
		rest, args = rest[i+j+1:], args[1:]
	}
	if len(args) > 0 {
		return "", apperr.New(apperr.Internal, "path %q does not match its %d arguments", r.Path, len(r.PathArgs))
	}
	u := c.opts.BaseURL + prefix + path.String()
	if len(r.Query) > 0 {
		u += "?" + r.Query.Encode()
	}
	return u, nil
}

// Do sends r, retrying rate limits and transient failures (see retry.go). Any non-2xx
// status or transport failure is returned as *apperr.Error. With Options.DryRun it
// sends nothing and returns *DryRunError.
func (c *Client) Do(ctx context.Context, r Request) (*Response, error) {
	if r.Method == "" {
		r.Method = http.MethodGet
	}
	u, err := c.BuildURL(r)
	if err != nil {
		return nil, err
	}
	info := &apperr.RequestInfo{Method: r.Method, URL: u}
	body, err := encodeBody(r)
	if err != nil {
		return nil, err
	}
	sentToken := c.opts.Token != "" && !r.NoAuth
	header := c.header(r, body != nil, sentToken)
	if c.opts.DryRun && !r.SendInDryRun {
		if r.Multipart != nil {
			// The boundary is random and says nothing; the parts are listed instead.
			header.Set("Content-Type", "multipart/form-data")
		}
		p, err := plan(r, u, header, body, c.authPlan(r, sentToken))
		if err != nil {
			return nil, err
		}
		return nil, &DryRunError{Plan: p}
	}
	if c.opts.MissingToken != nil && (c.opts.RequireToken || (r.Scope != "" && !r.NoAuth)) {
		return nil, c.opts.MissingToken(r.Scope)
	}
	if r.Multipart != nil {
		header.Set("Content-Type", r.Multipart.contentType())
	}
	return c.retry(ctx, r, info, func() (*Response, *apperr.Error) {
		return c.attempt(ctx, r, info, header, body, sentToken)
	})
}

// encodeBody returns the request body: JSON marshalled once (and reused on retries),
// RawBody, or nil for none.
func encodeBody(r Request) ([]byte, error) {
	n := 0
	for _, set := range []bool{r.JSON != nil, r.RawBody != nil, r.Multipart != nil} {
		if set {
			n++
		}
	}
	switch {
	case n > 1:
		return nil, apperr.New(apperr.Internal, "request has more than one kind of body")
	case r.JSON != nil:
		b, err := json.Marshal(r.JSON)
		if err != nil {
			return nil, apperr.New(apperr.Internal, "cannot encode the request body: %s", err.Error()).WithCause(err)
		}
		return b, nil
	}
	return r.RawBody, nil
}

// header builds the request headers.
func (c *Client) header(r Request, hasBody, sentToken bool) http.Header {
	h := http.Header{}
	for k, vs := range r.Header {
		for _, v := range vs {
			h.Add(k, v)
		}
	}
	h.Set("User-Agent", c.opts.UserAgent)
	h.Set("Accept", "application/json")
	if hasBody {
		ct := r.ContentType
		if ct == "" {
			ct = "application/json"
		}
		h.Set("Content-Type", ct)
	}
	if sentToken {
		h.Set("Authorization", c.opts.Token) // no "Bearer" prefix
	}
	return h
}

// attempt sends the request once and reads the response.
func (c *Client) attempt(ctx context.Context, r Request, info *apperr.RequestInfo, header http.Header, body []byte, sentToken bool) (*Response, *apperr.Error) {
	attemptCtx := ctx
	if c.opts.Timeout > 0 && !r.LongRunning {
		var cancel context.CancelFunc
		attemptCtx, cancel = context.WithTimeout(ctx, c.opts.Timeout)
		defer cancel()
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(attemptCtx, r.Method, info.URL, rd)
	if err != nil {
		return nil, apperr.New(apperr.Internal, "cannot build request: %s", err.Error()).WithCause(err)
	}
	req.Header = header.Clone()
	if m := r.Multipart; m != nil {
		// Rebuilt (files reopened) on every attempt; GetBody lets net/http resend it too.
		_, mb, n, err := m.build()
		if err != nil {
			return nil, apperr.From(err)
		}
		defer mb.Close()
		req.Body, req.ContentLength = mb, n
		req.GetBody = func() (io.ReadCloser, error) {
			_, b, _, err := m.build()
			return b, err
		}
	}

	c.opts.Log.Request(r.Method, info.URL)
	c.opts.Log.Headers(">", req.Header)
	start := c.opts.Clock.Now()
	resp, err := c.opts.Doer.Do(req)
	if err != nil {
		return nil, TransportError(ctx, err, info, c.opts.Timeout)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody+1))
	if err != nil {
		return nil, TransportError(ctx, err, info, c.opts.Timeout)
	}
	c.rate.update(resp.Header, c.opts.Clock.Now())
	c.opts.Log.Response(resp.StatusCode, c.opts.Clock.Now().Sub(start), headerInt(resp.Header, "X-Ratelimit-Remaining"))
	c.opts.Log.Headers("<", resp.Header)
	if len(data) > MaxBody {
		e := apperr.New(apperr.UnexpectedResponse, "response body is larger than %d MiB", MaxBody>>20)
		e.HTTPStatus, e.Request = resp.StatusCode, info
		return nil, e
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, classifyStatus(resp, data, r, info, sentToken)
	}
	out := &Response{Status: resp.StatusCode, Proto: resp.Proto, Header: resp.Header, Body: data}
	if len(bytes.TrimSpace(data)) == 0 {
		return out, nil
	}
	v, perr := jsonx.Parse(data)
	if perr != nil {
		if strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "application/json") {
			e := apperr.New(apperr.UnexpectedResponse, "the API returned invalid JSON: %s", perr.Error()).WithCause(perr)
			e.HTTPStatus, e.Request, e.BodyExcerpt = resp.StatusCode, info, excerpt(data)
			return nil, e
		}
		return out, nil
	}
	out.Value, out.IsJSON = v, true
	return out, nil
}

// classifyStatus maps a non-2xx response to an error.
func classifyStatus(resp *http.Response, body []byte, r Request, info *apperr.RequestInfo, sentToken bool) *apperr.Error {
	status := resp.StatusCode
	var apiBody struct {
		Error       *string         `json:"error"`
		Description *string         `json:"description"`
		Details     json.RawMessage `json:"details"`
	}
	e := &apperr.Error{HTTPStatus: status, Request: info}
	if trimmed := bytes.TrimSpace(body); len(trimmed) > 0 {
		if trimmed[0] == '{' && json.Unmarshal(trimmed, &apiBody) == nil && apiBody.Error != nil {
			e.APIError = *apiBody.Error
			if apiBody.Description != nil {
				e.APIDesc = *apiBody.Description
			}
			if len(apiBody.Details) > 0 && string(apiBody.Details) != "null" {
				e.APIDetails = apiBody.Details
			}
		} else {
			e.BodyExcerpt = excerpt(body)
		}
	}
	descOr := func(def string) string {
		if e.APIDesc != "" {
			return e.APIDesc
		}
		return def
	}
	switch {
	case status == 400:
		e.Code, e.Message = apperr.BadRequest, descOr("bad request")
	case status == 401 && !sentToken:
		e.Code, e.Message = apperr.AuthRequired, "authentication required"
		e.Hint = "set MODRINTH_TOKEN or run: mdr auth login --with-token"
		if r.Scope != "" {
			e.Hint += "; required scope: " + r.Scope
		}
	case status == 401:
		e.Code, e.Message = apperr.Unauthorized, descOr("token was rejected")
		if r.Scope != "" {
			e.Hint = "check that the token is valid, not expired and has the " + r.Scope + " scope"
		} else {
			e.Hint = "check that the token is valid and not expired"
		}
	case status == 403:
		e.Code, e.Message = apperr.Forbidden, descOr("forbidden")
	case status == 404:
		// Modrinth answers unknown projects with an empty 404, so the message is always ours.
		res := r.Resource
		if res == "" {
			res = "resource"
		}
		e.Code, e.Message = apperr.NotFound, res+" not found"
		if r.NotFoundMessage != "" {
			e.Message = r.NotFoundMessage
		}
		e.Hint = r.NotFoundHint
		if e.Hint == "" {
			e.Hint = "check the id or slug"
		}
	case status == 409:
		e.Code, e.Message = apperr.Conflict, descOr("conflict")
	case status == 410:
		e.Code, e.Message = apperr.APIDeprecated, "this API version has been retired"
		e.Hint = "upgrade modrinth-cli: " + buildinfo.RepoURL + "/releases"
	case status == 413:
		e.Code, e.Message = apperr.PayloadTooLarge, "payload too large"
	case status == 429:
		e.Code, e.Message = apperr.RateLimited, "rate limited"
		e.RetryAfter = retryAfter(resp.Header)
		e.Hint = fmt.Sprintf("retry after %ds", seconds(e.RetryAfter))
	case status >= 500:
		e.Code, e.Message = apperr.ServerError, fmt.Sprintf("server error (%d)", status)
		e.Hint = "this is usually temporary; retry later"
	default:
		e.Code, e.Message = apperr.ClientError, descOr(fmt.Sprintf("unexpected status %d", status))
	}
	return e
}

// retryAfter is X-Ratelimit-Reset + resetPadding, else Retry-After (integer seconds),
// else defaultRetryAfter.
func retryAfter(h http.Header) time.Duration {
	if n := headerInt(h, "X-Ratelimit-Reset"); n >= 0 {
		return time.Duration(n)*time.Second + resetPadding
	}
	if n := headerInt(h, "Retry-After"); n >= 0 {
		return time.Duration(n) * time.Second
	}
	return defaultRetryAfter
}

// StatusError classifies a non-2xx response to a request sent without a token
// (downloads from the CDN). body is the start of the response body.
func StatusError(resp *http.Response, body []byte, r Request, info *apperr.RequestInfo) *apperr.Error {
	return classifyStatus(resp, body, r, info, false)
}

// TransportError classifies a failure to send or receive. timeout is the value
// named in the hint of a timeout.
func TransportError(ctx context.Context, err error, info *apperr.RequestInfo, timeout time.Duration) *apperr.Error {
	msg := err.Error()
	var ue *url.Error
	if errors.As(err, &ue) {
		msg = ue.Err.Error()
	}
	e := &apperr.Error{Message: msg, Request: info, Cause: err}
	var netErr net.Error
	var certErr *tls.CertificateVerificationError
	var unknownAuth x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	var invalidCert x509.CertificateInvalidError
	var dnsErr *net.DNSError
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		e.Code, e.Message = apperr.Interrupted, "interrupted"
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()):
		e.Code = apperr.Timeout
		e.Hint = fmt.Sprintf("increase --timeout (currently %ds)", int64(timeout/time.Second))
	case errors.As(err, &certErr) || errors.As(err, &unknownAuth) || errors.As(err, &hostErr) || errors.As(err, &invalidCert):
		e.Code = apperr.TLS
		e.Hint = "install system CA certificates (e.g. the ca-certificates package) or set SSL_CERT_FILE"
	case errors.As(err, &dnsErr):
		e.Code = apperr.DNS
		e.Hint = "check network connectivity and DNS"
	default:
		e.Code = apperr.Network
	}
	return e
}

func headerInt(h http.Header, key string) int {
	v := h.Get(key)
	if v == "" {
		return -1
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 {
		return -1
	}
	return n
}

// excerpt returns the first bytes of body as valid UTF-8.
func excerpt(body []byte) string {
	if len(body) > excerptBytes {
		body = body[:excerptBytes]
	}
	return strings.ToValidUTF8(string(body), "\uFFFD")
}

type nopLogger struct{}

func (nopLogger) Request(string, string)           {}
func (nopLogger) Response(int, time.Duration, int) {}
func (nopLogger) Headers(string, http.Header)      {}
func (nopLogger) Note(string, ...any)              {}
