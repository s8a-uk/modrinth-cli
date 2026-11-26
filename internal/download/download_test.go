package download_test

import (
	"context"
	"crypto/sha1"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/s8a-uk/modrinth-cli/internal/api"
	"github.com/s8a-uk/modrinth-cli/internal/apperr"
	"github.com/s8a-uk/modrinth-cli/internal/download"
	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
)

const content = "fake jar content\n"

func sums(s string) (string, string) {
	a, b := sha1.Sum([]byte(s)), sha512.Sum512([]byte(s))
	return hex.EncodeToString(a[:]), hex.EncodeToString(b[:])
}

// cdn is a fake CDN that records requests.
type cdn struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []*http.Request
}

func newCDN(t *testing.T, h http.HandlerFunc) *cdn {
	t.Helper()
	c := &cdn{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		c.reqs = append(c.reqs, r.Clone(context.Background()))
		c.mu.Unlock()
		h(w, r)
	}))
	t.Cleanup(c.Close)
	return c
}

func (c *cdn) requests() []*http.Request {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*http.Request(nil), c.reqs...)
}

func serve(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }
}

func item(c *cdn, name string) download.Item {
	_, h512 := sums(content)
	return download.Item{URL: c.URL + "/data/AANobbMI/versions/ygf8cVZg/" + name, Filename: name, Size: int64(len(content)), SHA512: h512}
}

func opts(c *cdn, dir string) download.Options {
	return download.Options{Dir: dir, APIURL: c.URL, UserAgent: "test-agent/1"}
}

// entries lists the names in dir.
func entries(t *testing.T, dir string) []string {
	t.Helper()
	des, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, d := range des {
		out = append(out, d.Name())
	}
	return out
}

func wantCode(t *testing.T, err error, code apperr.Code) *apperr.Error {
	t.Helper()
	if err == nil {
		t.Fatalf("no error, want %s", code)
	}
	e := apperr.From(err)
	if e.Code != code {
		t.Fatalf("code = %s (%s), want %s", e.Code, e.Message, code)
	}
	return e
}

func TestFetchDownloads(t *testing.T) {
	c := newCDN(t, serve(content))
	dir := t.TempDir()
	out, err := download.Fetch(context.Background(), item(c, "mod.jar"), opts(c, dir))
	if err != nil {
		t.Fatal(err)
	}
	_, h512 := sums(content)
	want := download.Outcome{Filename: "mod.jar", Path: filepath.Join(dir, "mod.jar"), Size: int64(len(content)), SHA512: h512, Status: "downloaded"}
	if out != want {
		t.Errorf("outcome = %+v, want %+v", out, want)
	}
	if b, _ := os.ReadFile(out.Path); string(b) != content {
		t.Errorf("content = %q", b)
	}
	if got := entries(t, dir); len(got) != 1 {
		t.Errorf("dir has %v, want only mod.jar", got)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(out.Path); st.Mode().Perm() != 0o644 {
			t.Errorf("mode = %v, want 0644", st.Mode().Perm())
		}
	}
	// Criterion: no token goes to the CDN, but the User-Agent does.
	for _, r := range c.requests() {
		if _, ok := r.Header["Authorization"]; ok {
			t.Errorf("Authorization sent to the CDN: %q", r.Header.Get("Authorization"))
		}
		if ua := r.Header.Get("User-Agent"); ua != "test-agent/1" {
			t.Errorf("User-Agent = %q", ua)
		}
	}
}

func TestFetchExistsIsIdempotent(t *testing.T) {
	c := newCDN(t, serve(content))
	dir := t.TempDir()
	for i, want := range []string{"downloaded", "exists", "exists"} {
		out, err := download.Fetch(context.Background(), item(c, "mod.jar"), opts(c, dir))
		if err != nil {
			t.Fatal(err)
		}
		if out.Status != want {
			t.Errorf("run %d: status = %s, want %s", i, out.Status, want)
		}
	}
	if n := len(c.requests()); n != 1 {
		t.Errorf("%d requests, want 1", n)
	}
}

func TestFetchSHA1Only(t *testing.T) {
	c := newCDN(t, serve(content))
	it := item(c, "mod.jar")
	it.SHA512, it.SHA1 = "", func() string { h, _ := sums(content); return strings.ToUpper(h) }()
	if _, err := download.Fetch(context.Background(), it, opts(c, t.TempDir())); err != nil {
		t.Fatal(err)
	}
	it.SHA1 = ""
	wantCode(t, func() error { _, err := download.Fetch(context.Background(), it, opts(c, t.TempDir())); return err }(), apperr.UnexpectedResponse)
}

func TestFetchOverwriteRules(t *testing.T) {
	c := newCDN(t, serve(content))
	dir := t.TempDir()
	dest := filepath.Join(dir, "mod.jar")
	if err := os.WriteFile(dest, []byte("other content"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := download.Fetch(context.Background(), item(c, "mod.jar"), opts(c, dir))
	e := wantCode(t, err, apperr.FileExists)
	if e.Hint != "use --force to overwrite" || e.Code.ExitCode() != 2 {
		t.Errorf("hint %q, exit %d", e.Hint, e.Code.ExitCode())
	}
	if b, _ := os.ReadFile(dest); string(b) != "other content" {
		t.Errorf("the existing file changed: %q", b)
	}
	if len(c.requests()) != 0 {
		t.Errorf("a request was sent")
	}
	o := opts(c, dir)
	o.Force = true
	out, err := download.Fetch(context.Background(), item(c, "mod.jar"), o)
	if err != nil || out.Status != "downloaded" {
		t.Fatalf("--force: %+v, %v", out, err)
	}
	if b, _ := os.ReadFile(dest); string(b) != content {
		t.Errorf("--force did not replace the file: %q", b)
	}
	if got := entries(t, dir); len(got) != 1 {
		t.Errorf("dir has %v", got)
	}

	// A directory in the way is never replaced.
	if err := os.Mkdir(filepath.Join(dir, "sub.jar"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err = download.Fetch(context.Background(), item(c, "sub.jar"), o)
	wantCode(t, err, apperr.FileExists)
}

func TestFetchMismatchLeavesNothing(t *testing.T) {
	cases := []struct {
		name, body string
		msg        string
	}{
		{"hash", strings.ToUpper(content), "sha512 mismatch for mod.jar: expected "},
		{"size", content + "extra", "size mismatch for mod.jar: expected 17 bytes, got 22"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newCDN(t, serve(tc.body))
			dir := t.TempDir()
			_, err := download.Fetch(context.Background(), item(c, "mod.jar"), opts(c, dir))
			e := wantCode(t, err, apperr.HashMismatch)
			if e.Code.ExitCode() != 9 || !strings.HasPrefix(e.Message, tc.msg) {
				t.Errorf("exit %d, message %q", e.Code.ExitCode(), e.Message)
			}
			if got := entries(t, dir); len(got) != 0 {
				t.Errorf("files left behind: %v", got)
			}
		})
	}
}

func TestFetchRefusesBadNamesAndHosts(t *testing.T) {
	c := newCDN(t, serve(content))
	_, h512 := sums(content)
	cases := []struct{ name, filename, url string }{
		{"parent dir", "../x.jar", ""},
		{"path", "a/b.jar", ""},
		{"backslash", `a\b.jar`, ""},
		{"reserved", "CON.jar", ""},
		{"dot file", ".bashrc", ""},
		{"other host", "x.jar", "https://evil.example/x.jar"},
		{"look-alike host", "x.jar", "https://cdn.modrinth.com.evil.example/x.jar"},
		{"http cdn", "x.jar", "http://cdn.modrinth.com/x.jar"},
		{"user info", "x.jar", "https://user@cdn.modrinth.com/x.jar"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parent := t.TempDir()
			dir := filepath.Join(parent, "d")
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			u := tc.url
			if u == "" {
				u = c.URL + "/data/x.jar"
			}
			_, err := download.Fetch(context.Background(), download.Item{URL: u, Filename: tc.filename, SHA512: h512}, opts(c, dir))
			e := wantCode(t, err, apperr.InvalidInput)
			if e.Code.ExitCode() != 2 {
				t.Errorf("exit %d", e.Code.ExitCode())
			}
			if got := entries(t, dir); len(got) != 0 {
				t.Errorf("written: %v", got)
			}
			if got := entries(t, parent); len(got) != 1 {
				t.Errorf("written next to the dir: %v", got)
			}
		})
	}
	if n := len(c.requests()); n != 0 {
		t.Errorf("%d requests sent", n)
	}
	// The staging CDN is allowed only for staging.
	o := opts(c, t.TempDir())
	o.APIURL = "https://api.modrinth.com"
	_, err := download.Fetch(context.Background(), download.Item{URL: "https://staging-cdn.modrinth.com/x.jar", Filename: "x.jar", SHA512: h512}, o)
	wantCode(t, err, apperr.InvalidInput)
}

func TestFetchMissingDir(t *testing.T) {
	c := newCDN(t, serve(content))
	_, err := download.Fetch(context.Background(), item(c, "mod.jar"), opts(c, filepath.Join(t.TempDir(), "nope")))
	wantCode(t, err, apperr.InvalidInput)
}

func TestFetchRedirects(t *testing.T) {
	var c *cdn
	c = newCDN(t, func(w http.ResponseWriter, r *http.Request) {
		switch path.Base(r.URL.Path) {
		case "moved.jar":
			http.Redirect(w, r, c.URL+"/data/real.jar", http.StatusFound)
		case "away.jar":
			http.Redirect(w, r, "https://evil.example/x.jar", http.StatusFound)
		case "plain.jar":
			http.Redirect(w, r, "http://cdn.modrinth.com/x.jar", http.StatusFound)
		default:
			_, _ = w.Write([]byte(content))
		}
	})
	dir := t.TempDir()
	if _, err := download.Fetch(context.Background(), item(c, "moved.jar"), opts(c, dir)); err != nil {
		t.Fatalf("redirect on the allowed host: %v", err)
	}
	for _, name := range []string{"away.jar", "plain.jar"} {
		_, err := download.Fetch(context.Background(), item(c, name), opts(c, dir))
		wantCode(t, err, apperr.InvalidInput)
	}
	if got := entries(t, dir); len(got) != 1 || got[0] != "moved.jar" {
		t.Errorf("dir has %v", got)
	}
}

func TestFetchHTTPErrors(t *testing.T) {
	c := newCDN(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "gone.jar") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusBadGateway)
	})
	dir := t.TempDir()
	_, err := download.Fetch(context.Background(), item(c, "gone.jar"), opts(c, dir))
	if e := wantCode(t, err, apperr.NotFound); e.Message != "file 'gone.jar' not found" {
		t.Errorf("message %q", e.Message)
	}
	_, err = download.Fetch(context.Background(), item(c, "mod.jar"), opts(c, dir))
	if e := wantCode(t, err, apperr.ServerError); !e.Code.Retryable() {
		t.Errorf("not retryable")
	}
	if got := entries(t, dir); len(got) != 0 {
		t.Errorf("files left behind: %v", got)
	}
}

// stalling serves half the content, then waits until the client goes away.
func stalling(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Length", "17")
	_, _ = w.Write([]byte(content[:8]))
	w.(http.Flusher).Flush()
	<-r.Context().Done()
}

func TestFetchInterruptedLeavesNothing(t *testing.T) {
	c := newCDN(t, stalling)
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	o := opts(c, dir)
	o.Progress = func(_ string, done, _ int64) {
		if done > 0 {
			cancel() // SIGINT in the middle of the body
		}
	}
	_, err := download.Fetch(ctx, item(c, "mod.jar"), o)
	if e := wantCode(t, err, apperr.Interrupted); e.Code.ExitCode() != 130 {
		t.Errorf("exit %d", e.Code.ExitCode())
	}
	if got := entries(t, dir); len(got) != 0 {
		t.Errorf("files left behind: %v", got)
	}
}

func TestFetchIdleTimeout(t *testing.T) {
	c := newCDN(t, stalling)
	dir := t.TempDir()
	o := opts(c, dir)
	o.IdleTimeout = 50 * time.Millisecond
	_, err := download.Fetch(context.Background(), item(c, "mod.jar"), o)
	if e := wantCode(t, err, apperr.Timeout); !e.Code.Retryable() || !strings.HasPrefix(e.Message, "download stalled") {
		t.Errorf("%+v", e)
	}
	if got := entries(t, dir); len(got) != 0 {
		t.Errorf("files left behind: %v", got)
	}
}

func TestFetchDryRun(t *testing.T) {
	c := newCDN(t, serve(content))
	dir := t.TempDir()
	o := opts(c, dir)
	o.DryRun = true
	_, err := download.Fetch(context.Background(), item(c, "mod.jar"), o)
	var dry *api.DryRunError
	if !errors.As(err, &dry) {
		t.Fatalf("err = %v, want a dry run", err)
	}
	if _, has := dry.Plan.Lookup("request.headers.Authorization"); has {
		t.Errorf("plan has Authorization")
	}
	if u, _ := dry.Plan.Lookup("request.url"); u.Str != item(c, "mod.jar").URL {
		t.Errorf("plan url = %v", jsonx.Keys(dry.Plan))
	}
	if len(c.requests()) != 0 || len(entries(t, dir)) != 0 {
		t.Errorf("the dry run sent a request or wrote a file")
	}
}
