package testutil

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// MockAPI is a fake Modrinth API (and CDN) server backed by httptest.Server.
type MockAPI struct {
	URL string // pass as MODRINTH_API_URL (Run does it)

	t        *testing.T
	srv      *httptest.Server
	mu       sync.Mutex
	stubs    []*Stub
	requests []Recorded
}

// Stub is one expected request and its canned response.
type Stub struct {
	m      *MockAPI
	method string
	path   string
	query  map[string]string
	status int
	header http.Header
	drop   []string // default headers to leave out
	body   []byte
	times  int // remaining uses; -1 = unlimited
}

// Recorded is a request the server received.
type Recorded struct {
	Method string
	Path   string
	Query  url.Values
	Header http.Header
	Body   []byte
	// ContentLength is the request's Content-Length (-1 = not sent).
	ContentLength int64
}

// live holds the URLs of running mock servers so golden files can replace them with <BASE>.
var live sync.Map

// NewMockAPI starts a server. It is closed at the end of the test, and any expectation
// that was not used is reported as an error.
func NewMockAPI(t *testing.T) *MockAPI {
	t.Helper()
	m := &MockAPI{t: t}
	m.srv = httptest.NewServer(http.HandlerFunc(m.serve))
	m.URL = m.srv.URL
	live.Store(m.URL, true)
	t.Cleanup(func() {
		m.srv.Close()
		live.Delete(m.URL)
		m.mu.Lock()
		defer m.mu.Unlock()
		for _, s := range m.stubs {
			if s.times > 0 {
				t.Errorf("mock API: expected request not received: %s %s", s.method, s.path)
			}
		}
	})
	return m
}

// On registers an expected request. Registering the same method and path again queues
// another response, used in registration order (e.g. 429 then 200). path includes the
// API version, e.g. "/v2/project/sodium".
func (m *MockAPI) On(method, path string) *Stub {
	s := &Stub{m: m, method: method, path: path, status: 200, header: http.Header{}, times: 1}
	m.mu.Lock()
	m.stubs = append(m.stubs, s)
	m.mu.Unlock()
	return s
}

// Query makes the stub match only requests whose decoded query value for key is want.
func (s *Stub) Query(key, want string) *Stub {
	if s.query == nil {
		s.query = map[string]string{}
	}
	s.query[key] = want
	return s
}

// Status sets the status code (default 200).
func (s *Stub) Status(code int) *Stub { s.status = code; return s }

// Header adds a response header.
func (s *Stub) Header(key, value string) *Stub { s.header.Add(key, value); return s }

// WithoutHeader leaves out a header the server sends by default (the X-Ratelimit-* trio).
func (s *Stub) WithoutHeader(key string) *Stub { s.drop = append(s.drop, key); return s }

// JSON sets a JSON body.
func (s *Stub) JSON(body string) *Stub { return s.Body("application/json", []byte(body)) }

// Fixture sets the body to testdata/responses/<name>.json, with {{BASE}} replaced by the server URL.
func (s *Stub) Fixture(name string) *Stub {
	s.m.t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "responses", name+".json"))
	if err != nil {
		s.m.t.Fatalf("mock API: fixture %s: %v", name, err)
	}
	return s.JSON(strings.ReplaceAll(string(b), "{{BASE}}", s.m.URL))
}

// Body sets a body with a content type.
func (s *Stub) Body(contentType string, b []byte) *Stub {
	s.header.Set("Content-Type", contentType)
	s.body = b
	return s
}

// Empty sets an empty body.
func (s *Stub) Empty() *Stub { s.body = nil; s.header.Del("Content-Type"); return s }

// Times sets how many requests the stub answers (default 1; -1 = any number, not required).
func (s *Stub) Times(n int) *Stub { s.times = n; return s }

// Requests returns every request received, in order.
func (m *MockAPI) Requests() []Recorded {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Recorded(nil), m.requests...)
}

// Last returns the last request received.
func (m *MockAPI) Last() Recorded {
	rs := m.Requests()
	if len(rs) == 0 {
		m.t.Fatal("mock API: no requests received")
	}
	return rs[len(rs)-1]
}

func (m *MockAPI) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	m.mu.Lock()
	m.requests = append(m.requests, Recorded{Method: r.Method, Path: r.URL.Path, Query: r.URL.Query(), Header: r.Header.Clone(), Body: body, ContentLength: r.ContentLength})
	var stub *Stub
	for _, s := range m.stubs {
		if s.times != 0 && s.matches(r) {
			stub = s
			if s.times > 0 {
				s.times--
			}
			break
		}
	}
	m.mu.Unlock()

	h := w.Header()
	h.Set("X-Ratelimit-Limit", "300")
	h.Set("X-Ratelimit-Remaining", "299")
	h.Set("X-Ratelimit-Reset", "60")
	if stub == nil {
		m.t.Errorf("unexpected request: %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		w.WriteHeader(599)
		return
	}
	for _, k := range stub.drop {
		h.Del(k)
	}
	for k, vs := range stub.header {
		h[k] = vs
	}
	w.WriteHeader(stub.status)
	_, _ = w.Write(stub.body)
}

func (s *Stub) matches(r *http.Request) bool {
	if r.Method != s.method || r.URL.Path != s.path {
		return false
	}
	q := r.URL.Query()
	for k, want := range s.query {
		if q.Get(k) != want {
			return false
		}
	}
	return true
}
