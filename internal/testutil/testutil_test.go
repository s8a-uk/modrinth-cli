package testutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplaceWord(t *testing.T) {
	cases := []struct{ in, want string }{
		{`"version":"dev"`, `"version":"<V>"`},
		{"mdr dev\n", "mdr <V>\n"},
		{"devel /dev/null dev", "devel /<V>/null <V>"},
		{"undev devs dev_x", "undev devs dev_x"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := replaceWord(tc.in, "dev", "<V>"); got != tc.want {
			t.Errorf("replaceWord(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSlashPaths(t *testing.T) {
	cases := []struct{ in, want string }{
		// JSON output: each separator is an escaped backslash.
		{`{"path":"<TMP>\\sub\\a-1.0+mc.jar","n":"\n"}`, `{"path":"<TMP>/sub/a-1.0+mc.jar","n":"\n"}`},
		{`"<TMP>\\mod.jar exists"`, `"<TMP>/mod.jar exists"`},
		// Table output: plain backslashes.
		{`a.jar  <TMP>\mods\a.jar`, `a.jar  <TMP>/mods/a.jar`},
		// A backslash not followed by a path element is kept.
		{`<TMP>\ <TMP>\\`, `<TMP>\ <TMP>\\`},
		{`C:\x <TMP>`, `C:\x <TMP>`},
	}
	for _, tc := range cases {
		if got := slashPaths(tc.in, "<TMP>"); got != tc.want {
			t.Errorf("slashPaths(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDiff(t *testing.T) {
	if d := Diff("a\nb\n", "a\nb\n"); d != "" {
		t.Errorf("equal inputs: %q", d)
	}
	d := Diff("1\n2\n3\n4\n5\n", "1\n2\n3\n4\nX\n")
	if !strings.Contains(d, "-    5   5") || !strings.Contains(d, "+    5   X") || strings.Contains(d, "   1   1") {
		t.Errorf("Diff output:\n%s", d)
	}
}

func TestGoldenCompare(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll(filepath.Join("testdata", "golden"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("testdata", "golden", "x.stdout"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	Golden(t, "x.stdout", "hello\n")
}
