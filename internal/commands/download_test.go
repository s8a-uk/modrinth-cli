package commands_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/s8a-uk/modrinth-cli/internal/testutil"
)

const aJar = "fake mod a\n" // testdata/files/a.jar

// dlFile is one file of a version served by the mock API and CDN.
type dlFile struct {
	name    string // filename in the API response
	content string // what the CDN serves
	hash    string // sha512 in the API response; "" = of content
	url     string // "" = the mock CDN
	primary bool
}

// versionWith answers GET /v2/version/AAAAAAAA with files, and serves each file
// from the mock CDN (unless serve is false or the URL points elsewhere).
func versionWith(serve bool, files ...dlFile) func(m *testutil.MockAPI) {
	return func(m *testutil.MockAPI) {
		var parts []string
		for i, f := range files {
			path := fmt.Sprintf("/data/PPPPPPPP/versions/AAAAAAAA/f%d.jar", i)
			u, h := f.url, f.hash
			if u == "" {
				u = m.URL + path
				if serve {
					m.On("GET", path).Body("application/java-archive", []byte(f.content)).Times(-1) // not every file is fetched
				}
			}
			if h == "" {
				h = sha512Hex(f.content)
			}
			parts = append(parts, fmt.Sprintf(`{"hashes":{"sha512":%q,"sha1":"x"},"url":%q,"filename":%q,"primary":%t,"size":%d,"file_type":null}`,
				h, u, f.name, f.primary, len(f.content)))
		}
		m.On("GET", "/v2/version/AAAAAAAA").JSON(fmt.Sprintf(`{"id":"AAAAAAAA","project_id":"PPPPPPPP","files":[%s]}`, strings.Join(parts, ",")))
	}
}

func dirEntries(t *testing.T, dir string) []string {
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

func download(t *testing.T, setup func(m *testutil.MockAPI), env map[string]string, args ...string) (testutil.RunResult, *testutil.MockAPI) {
	t.Helper()
	m := testutil.NewMockAPI(t)
	if setup != nil {
		setup(m)
	}
	return testutil.Run(t, m, testutil.RunOpts{Env: env}, append([]string{"version", "download", "AAAAAAAA"}, args...)...), m
}

func TestVersionDownloadThenExists(t *testing.T) {
	dir := testutil.TempDir(t)
	for i, want := range []string{"downloaded", "exists"} {
		m := testutil.NewMockAPI(t)
		if i == 0 {
			downloadSetup(m)
		} else {
			m.On("GET", "/v2/version/ygf8cVZg").Fixture("version_download") // no CDN request
		}
		r := testutil.Run(t, m, testutil.RunOpts{}, "version", "download", "ygf8cVZg", "--dir", dir, "--fields", "status,path")
		if r.Code != 0 {
			t.Fatalf("run %d: exit %d: %s", i, r.Code, r.Stderr)
		}
		got := testutil.Normalize(r.Stdout)
		if w := fmt.Sprintf(`[{"status":%q,"path":"<TMP>/sodium-fabric-0.5.11+mc1.20.1.jar"}]`+"\n", want); got != w {
			t.Errorf("run %d: stdout %s, want %s", i, got, w)
		}
		if n := len(m.Requests()); i == 1 && n != 1 {
			t.Errorf("run %d: %d requests, want only the version", i, n)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "sodium-fabric-0.5.11+mc1.20.1.jar")); string(b) != aJar {
		t.Errorf("content %q", b)
	}
	if got := dirEntries(t, dir); len(got) != 1 {
		t.Errorf("dir has %v", got)
	}
}

// TestVersionDownloadNoTokenToCDN: the API gets the token, the CDN never does.
func TestVersionDownloadNoTokenToCDN(t *testing.T) {
	dir := testutil.TempDir(t)
	m := testutil.NewMockAPI(t)
	downloadSetup(m)
	r := testutil.Run(t, m, testutil.RunOpts{Env: map[string]string{"MODRINTH_TOKEN": fakeToken}}, "version", "download", "ygf8cVZg", "--dir", dir, "-vv")
	if r.Code != 0 {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
	reqs := m.Requests()
	if len(reqs) != 2 || reqs[1].Path != cdnPath {
		t.Fatalf("requests %v", reqs)
	}
	if got := reqs[0].Header.Get("Authorization"); got != fakeToken {
		t.Errorf("API Authorization = %q", got)
	}
	if _, has := reqs[1].Header["Authorization"]; has {
		t.Errorf("CDN request has Authorization: %v", reqs[1].Header)
	}
	if !strings.HasPrefix(reqs[1].Header.Get("User-Agent"), "s8a-uk/modrinth-cli/") {
		t.Errorf("CDN User-Agent = %q", reqs[1].Header.Get("User-Agent"))
	}
	if !strings.Contains(r.Stderr, "> GET "+m.URL+"/data/") || strings.Count(r.Stderr, "> Authorization: ***") != 1 {
		t.Errorf("-vv log:\n%s", r.Stderr)
	}
}

func TestVersionDownloadHashMismatch(t *testing.T) {
	cases := map[string]dlFile{
		"hash": {name: "mod.jar", content: "served bytes", hash: sha512Hex("expected bytes"), primary: true},
		"size": {name: "mod.jar", content: "served bytes", hash: sha512Hex("served bytes"), primary: true},
	}
	for name, f := range cases {
		t.Run(name, func(t *testing.T) {
			dir := testutil.TempDir(t)
			setup := versionWith(true, f)
			if name == "size" {
				setup = func(m *testutil.MockAPI) {
					m.On("GET", "/v2/version/AAAAAAAA").JSON(fmt.Sprintf(`{"id":"AAAAAAAA","files":[{"hashes":{"sha512":%q},"url":%q,"filename":"mod.jar","primary":true,"size":5}]}`,
						sha512Hex("served bytes"), m.URL+"/data/x/mod.jar"))
					m.On("GET", "/data/x/mod.jar").Body("application/java-archive", []byte("served bytes"))
				}
			}
			r, _ := download(t, setup, nil, "--dir", dir)
			if r.Code != 9 || !strings.Contains(lastLine(r.Stderr), `"code":"hash_mismatch"`) {
				t.Fatalf("exit %d: %s", r.Code, r.Stderr)
			}
			if got := dirEntries(t, dir); len(got) != 0 {
				t.Errorf("left behind: %v", got)
			}
		})
	}
}

func TestVersionDownloadRefusesDangerousFiles(t *testing.T) {
	cases := []dlFile{
		{name: "../x.jar", content: aJar},
		{name: "..", content: aJar},
		{name: "CON.jar", content: aJar},
		{name: "nul", content: aJar},
		{name: `a\b.jar`, content: aJar},
		{name: "mod.jar.", content: aJar},
		{name: "x.jar", content: aJar, url: "https://evil.example/x.jar"},
		{name: "x.jar", content: aJar, url: "http://cdn.modrinth.com/x.jar"},
	}
	for _, f := range cases {
		t.Run(f.name+" "+f.url, func(t *testing.T) {
			parent := testutil.TempDir(t)
			dir := filepath.Join(parent, "mods")
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			f.primary = true
			r, m := download(t, versionWith(false, f), nil, "--dir", dir)
			if r.Code != 2 || !strings.Contains(lastLine(r.Stderr), `"code":"invalid_input"`) {
				t.Fatalf("exit %d: %s", r.Code, r.Stderr)
			}
			if got := dirEntries(t, dir); len(got) != 0 {
				t.Errorf("written: %v", got)
			}
			if got := dirEntries(t, parent); !slices.Equal(got, []string{"mods"}) {
				t.Errorf("written next to --dir: %v", got)
			}
			if n := len(m.Requests()); n != 1 {
				t.Errorf("%d requests: the CDN was contacted", n)
			}
		})
	}
}

func TestVersionDownloadOverwrite(t *testing.T) {
	dir := writeFiles(t, map[string]string{"mod.jar": "old content"})
	f := dlFile{name: "mod.jar", content: aJar, primary: true}
	r, _ := download(t, versionWith(false, f), nil, "--dir", dir)
	if r.Code != 2 {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
	testutil.Golden(t, "version_download/file_exists.stderr", r.Stderr)
	if b, _ := os.ReadFile(filepath.Join(dir, "mod.jar")); string(b) != "old content" {
		t.Errorf("replaced without --force: %q", b)
	}
	r, _ = download(t, versionWith(true, f), nil, "--dir", dir, "--force", "--fields", "status")
	if r.Code != 0 || r.Stdout != `[{"status":"downloaded"}]`+"\n" {
		t.Fatalf("--force: exit %d, %s %s", r.Code, r.Stdout, r.Stderr)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "mod.jar")); string(b) != aJar {
		t.Errorf("not replaced with --force: %q", b)
	}
	if got := dirEntries(t, dir); len(got) != 1 {
		t.Errorf("dir has %v", got)
	}
}

func TestVersionDownloadSelection(t *testing.T) {
	two := []dlFile{{name: "extra.jar", content: "extra"}, {name: "main.jar", content: "main", primary: true}}
	cases := []struct {
		name  string
		args  []string
		serve bool
		files []dlFile
		code  int
		want  []string // files in --dir afterwards
		errIn string
	}{
		{name: "primary by default", serve: true, files: two, want: []string{"main.jar"}},
		{name: "first without a primary", serve: true, files: []dlFile{{name: "a.jar", content: "a"}, {name: "b.jar", content: "b"}},
			want: []string{"a.jar"}},
		{name: "--file", args: []string{"--file", "extra.jar"}, serve: true, files: two, want: []string{"extra.jar"}},
		{name: "--all", args: []string{"--all"}, serve: true, files: two, want: []string{"extra.jar", "main.jar"}},
		{name: "--file not in the version", args: []string{"--file", "nope.jar"}, files: two, code: 3,
			errIn: `"message":"version 'AAAAAAAA' has no file named 'nope.jar'","http_status":null,"retryable":false,"hint":"its files: extra.jar, main.jar"`},
		{name: "no files", files: nil, code: 3, errIn: "version 'AAAAAAAA' has no files"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := testutil.TempDir(t)
			setup := versionWith(tc.serve, tc.files...)
			r, _ := download(t, setup, nil, append([]string{"--dir", dir}, tc.args...)...)
			if r.Code != tc.code {
				t.Fatalf("exit %d: %s", r.Code, r.Stderr)
			}
			if tc.errIn != "" && !strings.Contains(r.Stderr, tc.errIn) {
				t.Errorf("stderr %q", r.Stderr)
			}
			if got := dirEntries(t, dir); !slices.Equal(got, tc.want) {
				t.Errorf("dir has %v, want %v", got, tc.want)
			}
		})
	}
}

func TestVersionDownloadPartial(t *testing.T) {
	dir := testutil.TempDir(t)
	files := []dlFile{{name: "a.jar", content: "a", primary: true}, {name: "b.jar", content: "b", hash: sha512Hex("other")}}
	r, _ := download(t, versionWith(true, files...), nil, "--dir", dir, "--all")
	if r.Code != 9 || !strings.Contains(r.Stderr, "note: 1 of 2 files were saved before the error\n") {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
	if got := dirEntries(t, dir); !slices.Equal(got, []string{"a.jar"}) {
		t.Errorf("dir has %v", got)
	}
}

func TestVersionDownloadUsage(t *testing.T) {
	dir := testutil.TempDir(t)
	runReadCases(t, []readCase{
		{name: "--file with --all", args: []string{"version", "download", "AAAAAAAA", "--file", "a.jar", "--all"}, code: 2, errIn: `"code":"usage"`},
		{name: "missing --dir", args: []string{"version", "download", "AAAAAAAA", "--dir", filepath.Join(dir, "nope")}, code: 2,
			errIn: `"code":"invalid_input"`},
		{name: "--dir is a file", args: []string{"version", "download", "AAAAAAAA", "--dir", filepath.Join("testdata", "files", "a.jar")}, code: 2,
			errIn: "not a directory"},
		{name: "invalid id", args: []string{"version", "download", "sodium"}, code: 2},
		{name: "version not found", args: []string{"version", "download", "AAAAAAAA", "--dir", dir}, code: 3,
			setup: func(m *testutil.MockAPI) { m.On("GET", "/v2/version/AAAAAAAA").Status(404).Empty() }, errIn: "version 'AAAAAAAA' not found"},
		// COLUMNS: the temporary directory's length varies, so the PATH column must not be cut.
		{name: "table", args: []string{"version", "download", "ygf8cVZg", "--dir", dir}, setup: downloadSetup, tty: true,
			env: map[string]string{"COLUMNS": "300"}, stdout: "version_download/table.stdout"},
	})
	if got := dirEntries(t, dir); len(got) != 1 {
		t.Errorf("dir has %v", got)
	}
}

// TestVersionDownloadDryRun: the version is fetched, the CDN request is shown
// (without Authorization, even with a token) and not sent.
func TestVersionDownloadDryRun(t *testing.T) {
	dir := testutil.TempDir(t)
	m := testutil.NewMockAPI(t)
	m.On("GET", "/v2/version/ygf8cVZg").Fixture("version_download")
	r := testutil.Run(t, m, testutil.RunOpts{Env: map[string]string{"MODRINTH_TOKEN": fakeToken}}, "version", "download", "ygf8cVZg", "--dir", dir, "--dry-run")
	if r.Code != 0 {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
	testutil.Golden(t, "version_download/dry_run.stdout", r.Stdout)
	if len(m.Requests()) != 1 || len(dirEntries(t, dir)) != 0 {
		t.Errorf("requests %v, dir %v", m.Requests(), dirEntries(t, dir))
	}
}
