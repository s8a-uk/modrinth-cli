// Package download saves version files from the Modrinth CDN: host allowlist, no
// token, a temporary file in the target directory, size and hash verification, and
// an atomic rename into place.
package download

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/s8a-uk/modrinth-cli/internal/api"
	"github.com/s8a-uk/modrinth-cli/internal/apperr"
	"github.com/s8a-uk/modrinth-cli/internal/hashfile"
	"github.com/s8a-uk/modrinth-cli/internal/validate"
)

// Item is one file to download, as the API described it (not yet validated).
type Item struct {
	URL      string
	Filename string
	Size     int64  // 0 = unknown
	SHA512   string // at least one of the hashes is required
	SHA1     string
}

// Options configure Fetch.
type Options struct {
	Dir         string
	Force       bool          // replace an existing file with different content
	Staging     bool          // also allow staging-cdn.modrinth.com
	APIURL      string        // the API base URL; a loopback host (tests) is allowed for downloads too
	IdleTimeout time.Duration // fail when no data arrives for this long; 0 = never
	DryRun      bool          // stop before the request and return *api.DryRunError
	Doer        api.Doer      // nil = api.NewDownloadHTTPClient
	UserAgent   string
	Log         api.Logger                               // nil = silent
	Progress    func(filename string, done, total int64) // nil = no progress
}

// Outcome is the result of one Fetch.
type Outcome struct {
	Filename string
	Path     string
	Size     int64
	SHA512   string
	Status   string // StatusDownloaded or StatusExists
}

// Statuses of an Outcome.
const (
	StatusDownloaded = "downloaded"
	StatusExists     = "exists" // a file with the same content was already there
)

// Fetch saves it.Filename in opts.Dir. An existing file with the expected hash is
// left alone (StatusExists); one with other content is file_exists unless
// opts.Force. The data goes to a temporary file in the same directory, is checked
// against the size and hash, and is then renamed into place, so a failed or
// interrupted download leaves neither a partial file nor a temporary one.
func Fetch(ctx context.Context, it Item, opts Options) (Outcome, error) {
	if opts.Log == nil {
		opts.Log = nopLogger{}
	}
	if err := validate.DownloadFilename(it.Filename); err != nil {
		return Outcome{}, err
	}
	if err := validate.DownloadURL(it.URL, opts.Staging, loopbackOf(opts.APIURL)); err != nil {
		return Outcome{}, err
	}
	if err := validate.Dir(opts.Dir); err != nil {
		return Outcome{}, err
	}
	alg, want := "sha512", strings.ToLower(it.SHA512)
	if want == "" {
		alg, want = "sha1", strings.ToLower(it.SHA1)
	}
	if want == "" {
		return Outcome{}, apperr.New(apperr.UnexpectedResponse, "the API gave no hash for file '%s'", it.Filename)
	}
	dest := filepath.Join(opts.Dir, it.Filename)

	// An existing file: the same content is success, other content needs --force.
	if st, err := os.Lstat(dest); err == nil {
		switch {
		case st.IsDir():
			return Outcome{}, apperr.New(apperr.FileExists, "%s exists and is a directory", dest).
				WithHint("remove it or choose another --dir")
		case st.Mode().IsRegular():
			sums, err := hashfile.File(ctx, dest)
			if err != nil {
				return Outcome{}, readError(ctx, dest, err)
			}
			if pick(sums, alg) == want {
				return Outcome{Filename: it.Filename, Path: dest, Size: sums.Size, SHA512: sums.SHA512, Status: StatusExists}, nil
			}
		}
		if !opts.Force {
			return Outcome{}, apperr.New(apperr.FileExists, "%s exists with different content", dest).
				WithHint("use --force to overwrite")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Outcome{}, readError(ctx, dest, err)
	}

	header := http.Header{"User-Agent": {opts.UserAgent}} // never Authorization
	if opts.DryRun {
		return Outcome{}, api.NewDryRunError(http.MethodGet, it.URL, header)
	}
	return fetch(ctx, it, opts, dest, header, alg, want)
}

// fetch downloads into a temporary file and renames it to dest.
func fetch(ctx context.Context, it Item, opts Options, dest string, header http.Header, alg, want string) (out Outcome, err error) {
	tmp, err := os.CreateTemp(opts.Dir, "."+it.Filename+".*.part")
	if err != nil {
		return Outcome{}, ioError(err, "cannot create a temporary file in %s: %s", opts.Dir, errText(err))
	}
	done := false
	defer func() {
		if !done {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var stalled atomic.Bool
	var timer *time.Timer
	if opts.IdleTimeout > 0 {
		timer = time.AfterFunc(opts.IdleTimeout, func() { stalled.Store(true); cancel() })
		defer timer.Stop()
	}

	info := &apperr.RequestInfo{Method: http.MethodGet, URL: it.URL}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, it.URL, nil)
	if err != nil {
		return Outcome{}, apperr.New(apperr.Internal, "cannot build request: %s", err.Error()).WithCause(err)
	}
	req.Header = header
	doer := opts.Doer
	if doer == nil {
		doer = api.NewDownloadHTTPClient(func(u *url.URL) error {
			return validate.DownloadURL(u.String(), opts.Staging, loopbackOf(opts.APIURL))
		})
	}
	opts.Log.Request(http.MethodGet, it.URL)
	opts.Log.Headers(">", req.Header)
	start := time.Now()
	resp, err := doer.Do(req)
	if err != nil {
		return Outcome{}, transportError(ctx, err, info, opts.IdleTimeout, &stalled)
	}
	defer resp.Body.Close()
	opts.Log.Response(resp.StatusCode, time.Since(start), -1)
	opts.Log.Headers("<", resp.Header)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return Outcome{}, api.StatusError(resp, body, api.Request{
			Method:       http.MethodGet,
			Resource:     fmt.Sprintf("file '%s'", it.Filename),
			NotFoundHint: "the file may have been removed; check the version with: mdr version get <id>",
		}, info)
	}

	hw := hashfile.NewWriter()
	fw := &errWriter{w: tmp}
	var dst io.Writer = io.MultiWriter(fw, hw)
	if opts.Progress != nil {
		dst = &progressWriter{w: dst, name: it.Filename, total: it.Size, f: opts.Progress}
	}
	var src io.Reader = resp.Body
	if timer != nil {
		src = &idleReader{r: resp.Body, timer: timer, d: opts.IdleTimeout}
	}
	if _, err := io.Copy(dst, src); err != nil {
		if fw.err != nil {
			return Outcome{}, ioError(fw.err, "cannot write %s: %s", tmp.Name(), errText(fw.err))
		}
		return Outcome{}, transportError(ctx, err, info, opts.IdleTimeout, &stalled)
	}

	sums := hw.Sums()
	if it.Size > 0 && sums.Size != it.Size {
		return Outcome{}, apperr.New(apperr.HashMismatch, "size mismatch for %s: expected %d bytes, got %d", it.Filename, it.Size, sums.Size).
			WithHint("the download was incomplete or the file changed; retry")
	}
	if got := pick(sums, alg); got != want {
		return Outcome{}, apperr.New(apperr.HashMismatch, "%s mismatch for %s: expected %s..., got %s...", alg, it.Filename, want[:16], got[:16]).
			WithHint("the download was corrupted or the file changed; retry")
	}
	if err := tmp.Sync(); err != nil {
		return Outcome{}, ioError(err, "cannot write %s: %s", tmp.Name(), errText(err))
	}
	if err := tmp.Close(); err != nil { // closed before the rename: required on Windows
		return Outcome{}, ioError(err, "cannot write %s: %s", tmp.Name(), errText(err))
	}
	if err := setMode(tmp.Name()); err != nil {
		return Outcome{}, ioError(err, "cannot set the permissions of %s: %s", tmp.Name(), errText(err))
	}
	if err := renameReplace(tmp.Name(), dest); err != nil {
		e := ioError(err, "cannot move the download to %s: %s", dest, errText(err))
		if inUse(err) {
			e.Hint = "the destination file is in use; close the program using it and retry"
		}
		return Outcome{}, e
	}
	done = true
	return Outcome{Filename: it.Filename, Path: dest, Size: sums.Size, SHA512: sums.SHA512, Status: StatusDownloaded}, nil
}

// loopbackOf returns the host of apiURL if it is a loopback host (tests), else "".
func loopbackOf(apiURL string) string {
	if u, err := url.Parse(apiURL); err == nil && validate.IsLoopback(u.Hostname()) {
		return u.Hostname()
	}
	return ""
}

func pick(s hashfile.Sums, alg string) string {
	if alg == "sha1" {
		return s.SHA1
	}
	return s.SHA512
}

// transportError classifies a failed request or body read. A stall (no data for
// the idle timeout) is a timeout.
func transportError(ctx context.Context, err error, info *apperr.RequestInfo, idle time.Duration, stalled *atomic.Bool) *apperr.Error {
	var refused *apperr.Error
	if errors.As(err, &refused) && ctx.Err() == nil { // a redirect off the allowlist
		return refused
	}
	if stalled.Load() {
		e := apperr.New(apperr.Timeout, "download stalled: no data for %ds", int64(idle/time.Second)).
			WithHint("increase --timeout (currently %ds)", int64(idle/time.Second)).WithCause(err)
		e.Request = info
		return e
	}
	return api.TransportError(ctx, err, info, idle)
}

func readError(ctx context.Context, path string, err error) *apperr.Error {
	if ctx.Err() != nil {
		return apperr.New(apperr.Interrupted, "interrupted").WithCause(err)
	}
	return ioError(err, "cannot read %s: %s", path, errText(err))
}

func ioError(err error, format string, args ...any) *apperr.Error {
	return apperr.New(apperr.IOError, format, args...).WithCause(err)
}

// errText is the error without the operation and path that *os.PathError repeats.
func errText(err error) string {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	var le *os.LinkError
	if errors.As(err, &le) {
		return le.Err.Error()
	}
	return err.Error()
}

// errWriter remembers a write error, to tell it from a read error after io.Copy.
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) Write(p []byte) (int, error) {
	n, err := e.w.Write(p)
	if err != nil {
		e.err = err
	}
	return n, err
}

// idleReader pushes the idle deadline back whenever data arrives.
type idleReader struct {
	r     io.Reader
	timer *time.Timer
	d     time.Duration
}

func (r *idleReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 {
		r.timer.Reset(r.d)
	}
	return n, err
}

type progressWriter struct {
	w     io.Writer
	name  string
	done  int64
	total int64
	f     func(string, int64, int64)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.done += int64(n)
	p.f(p.name, p.done, p.total)
	return n, err
}

type nopLogger struct{}

func (nopLogger) Request(string, string)           {}
func (nopLogger) Response(int, time.Duration, int) {}
func (nopLogger) Headers(string, http.Header)      {}
func (nopLogger) Note(string, ...any)              {}
