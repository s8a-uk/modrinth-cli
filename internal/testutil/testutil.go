// Package testutil holds test helpers: running the CLI in-process and golden files.
// Import it only from tests (external test packages under internal/).
package testutil

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/s8a-uk/modrinth-cli/internal/api"
	"github.com/s8a-uk/modrinth-cli/internal/app"
	"github.com/s8a-uk/modrinth-cli/internal/buildinfo"
	"github.com/s8a-uk/modrinth-cli/internal/cli"
)

var update = flag.Bool("update", false, "rewrite golden files")

// RunResult is the outcome of one CLI run.
type RunResult struct {
	Code   int
	Stdout string
	Stderr string
}

// RunOpts configures Run.
type RunOpts struct {
	Env       map[string]string // MODRINTH_API_URL and MODRINTH_CONFIG_DIR are set automatically
	Stdin     string
	StdoutTTY bool
	StdinTTY  bool
	Doer      api.Doer        // replaces the HTTP client (e.g. to inject transport errors)
	Clock     api.Clock       // nil = a new FakeClock, so retries never really sleep
	Context   context.Context // nil = context.Background()
}

// Run calls app.Run in-process. Environment variables not in opts.Env are empty
// (except MODRINTH_CONFIG_DIR, MODRINTH_API_URL and TERM), so the host environment
// never leaks into tests.
func Run(t *testing.T, m *MockAPI, opts RunOpts, args ...string) RunResult {
	t.Helper()
	dir := t.TempDir()
	tempDirs.Store(dir, true)
	t.Cleanup(func() { tempDirs.Delete(dir) })
	// TERM makes tables on a TTY colored on Windows too (as on Unix), so the
	// table golden files fit every OS. opts.Env can override it.
	env := map[string]string{"MODRINTH_CONFIG_DIR": dir, "TERM": "xterm"}
	if m != nil {
		env["MODRINTH_API_URL"] = m.URL
	}
	for k, v := range opts.Env {
		env[k] = v
	}
	var stdout, stderr bytes.Buffer
	io := cli.IO{
		Stdin:       strings.NewReader(opts.Stdin),
		Stdout:      &stdout,
		Stderr:      &stderr,
		StdinIsTTY:  opts.StdinTTY,
		StdoutIsTTY: opts.StdoutTTY,
	}
	clock, ctx := opts.Clock, opts.Context
	if clock == nil {
		clock = NewFakeClock()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	code := app.Run(ctx, args, io, func(k string) string { return env[k] }, app.Deps{Doer: opts.Doer, Clock: clock})
	return RunResult{Code: code, Stdout: stdout.String(), Stderr: stderr.String()}
}

// Golden compares got with testdata/golden/<name>, or rewrites the file with -update.
// Before comparing, Normalize replaces volatile strings.
func Golden(t *testing.T, name string, got string) {
	t.Helper()
	got = Normalize(got)
	path := filepath.Join("testdata", "golden", filepath.FromSlash(name))
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden %s: %v (run make test-update to create it)", name, err)
	}
	if d := Diff(string(want), got); d != "" {
		t.Errorf("golden %s differs (run make test-update to accept):\n%s", name, d)
	}
}

// TempDir returns a new temporary directory that golden files show as <TMP>.
func TempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	tempDirs.Store(dir, true)
	t.Cleanup(func() { tempDirs.Delete(dir) })
	return dir
}

// tempDirs holds the config directories of running Run calls (replaced by <TMP>).
var tempDirs sync.Map

// Normalize replaces the URLs of live mock servers with <BASE>, Run's temporary
// directories with <TMP>, and the version string (as a whole word) with <VERSION>.
// On Windows it also writes the separators of paths under <TMP> and testdata as
// "/", so one set of golden files fits every OS.
func Normalize(s string) string {
	live.Range(func(k, _ any) bool {
		s = strings.ReplaceAll(s, k.(string), "<BASE>")
		return true
	})
	tempDirs.Range(func(k, _ any) bool {
		dir := k.(string)
		if filepath.Separator == '\\' {
			// In JSON output each backslash of the directory is escaped.
			s = strings.ReplaceAll(s, strings.ReplaceAll(dir, `\`, `\\`), "<TMP>")
		}
		s = strings.ReplaceAll(s, dir, "<TMP>")
		return true
	})
	if filepath.Separator == '\\' {
		s = slashPaths(s, "<TMP>")
		s = slashPaths(s, "testdata")
	}
	return replaceWord(s, buildinfo.Resolved(), "<VERSION>")
}

// slashPaths rewrites the separators of every path that starts with prefix as "/".
// A separator is a backslash, or an escaped one (two backslashes) in JSON output,
// followed by a path element; the path ends at the first other character.
func slashPaths(s, prefix string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, prefix)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i+len(prefix)])
		s = s[i+len(prefix):]
		for {
			n := 0
			switch {
			case strings.HasPrefix(s, `\\`):
				n = 2
			case strings.HasPrefix(s, `\`):
				n = 1
			}
			if n == 0 || len(s) == n || !isPathByte(s[n]) {
				break
			}
			b.WriteByte('/')
			s = s[n:]
			j := 0
			for j < len(s) && isPathByte(s[j]) {
				j++
			}
			b.WriteString(s[:j])
			s = s[j:]
		}
	}
}

// isPathByte reports whether c can be part of a path element in test output.
func isPathByte(c byte) bool {
	return isWordByte(c) || strings.IndexByte("-.+~", c) >= 0
}

func replaceWord(s, word, repl string) string {
	if word == "" {
		return s
	}
	var b strings.Builder
	for {
		i := strings.Index(s, word)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		end := i + len(word)
		if (i > 0 && isWordByte(s[i-1])) || (end < len(s) && isWordByte(s[end])) {
			b.WriteString(s[:end])
		} else {
			b.WriteString(s[:i] + repl)
		}
		s = s[end:]
	}
}

func isWordByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// Diff returns "" if want == got, otherwise the first differing line with 3 lines of context.
func Diff(want, got string) string {
	if want == got {
		return ""
	}
	wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
	i := 0
	for i < len(wl) && i < len(gl) && wl[i] == gl[i] {
		i++
	}
	var b strings.Builder
	from := max(0, i-3)
	for j := from; j < i; j++ {
		fmt.Fprintf(&b, "  %4d   %s\n", j+1, wl[j])
	}
	for j := i; j < min(i+4, len(wl)); j++ {
		fmt.Fprintf(&b, "- %4d   %s\n", j+1, wl[j])
	}
	for j := i; j < min(i+4, len(gl)); j++ {
		fmt.Fprintf(&b, "+ %4d   %s\n", j+1, gl[j])
	}
	return b.String()
}
