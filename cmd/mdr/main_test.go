package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/s8a-uk/modrinth-cli/internal/app"
	"github.com/s8a-uk/modrinth-cli/internal/cli"
	"github.com/s8a-uk/modrinth-cli/internal/config"
)

// runFiles calls run with temporary files as the standard streams.
func runFiles(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	dir := t.TempDir()
	open := func(name string) *os.File {
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { f.Close() })
		return f
	}
	in, out, errOut := open("stdin"), open("stdout"), open("stderr")
	code = run(args, in, out, errOut)
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	return code, read("stdout"), read("stderr")
}

func TestRunVersion(t *testing.T) {
	code, out, errOut := runFiles(t, "--version")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, errOut)
	}
	if !strings.HasPrefix(out, "mdr ") || !strings.HasSuffix(out, "\n") {
		t.Errorf("stdout = %q, want \"mdr <version>\\n\"", out)
	}
	if errOut != "" {
		t.Errorf("stderr = %q, want empty", errOut)
	}
}

func TestRunFlushesStdout(t *testing.T) {
	code, out, _ := runFiles(t, "schema")
	if code != 0 || !strings.HasPrefix(out, `{"schemaVersion":1,`) {
		t.Errorf("schema: code %d, stdout %q", code, out)
	}
}

func TestRunPanicExitsOne(t *testing.T) {
	orig := appRun
	t.Cleanup(func() { appRun = orig })
	appRun = func(context.Context, []string, cli.IO, config.Env, app.Deps) int { panic("boom") }

	code, out, errOut := runFiles(t, "schema")
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if out != "" {
		t.Errorf("stdout = %q, want empty", out)
	}
	if want := "error: internal: panic: boom\n"; errOut != want {
		t.Errorf("stderr = %q, want %q", errOut, want)
	}
}
