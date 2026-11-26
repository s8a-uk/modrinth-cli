package commands_test

import (
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/s8a-uk/modrinth-cli/internal/testutil"
)

const (
	sodiumSHA1 = "c4293c1483f3b39c1c6c4c69143e8bb2c6e53daf"
	// sha512 of testdata/files/a.jar (the key of the version_files fixtures)
	aSHA512 = "b0919519f2ce602e41268027bd20607d7533e06299d9d3730612649b0be61ebc58f4180fa1297cbddf0b6e231e26b6049d652aa97e238d3f1e20653e6c54a7aa"
)

func sha512Hex(s string) string {
	h := sha512.Sum512([]byte(s))
	return hex.EncodeToString(h[:])
}

// writeFiles creates name -> content files in a new <TMP> directory.
func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := testutil.TempDir(t)
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// hashBody is the body of POST /version_files(/update).
type hashBody struct {
	Hashes       []string `json:"hashes"`
	Algorithm    string   `json:"algorithm"`
	Loaders      []string `json:"loaders"`
	GameVersions []string `json:"game_versions"`
	VersionTypes []string `json:"version_types"`
}

func bodyOf(t *testing.T, m *testutil.MockAPI, method, path string) []hashBody {
	t.Helper()
	var out []hashBody
	for _, r := range m.Requests() {
		if r.Method == method && r.Path == path {
			var b hashBody
			if err := json.Unmarshal(r.Body, &b); err != nil {
				t.Fatalf("%s %s body %q: %v", method, path, r.Body, err)
			}
			if ct := r.Header.Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q", ct)
			}
			out = append(out, b)
		}
	}
	return out
}

func TestFileGet(t *testing.T) {
	sha512 := strings.Repeat("Ab", 64)
	runReadCases(t, []readCase{
		{name: "sha1 from the length", args: []string{"file", "get", sodiumSHA1, "-o", "table"},
			setup: fixture("/v2/version_file/"+sodiumSHA1, "version_ygf8cVZg"),
			query: map[string]string{"algorithm": "sha1"}, stdout: "file_get/table.stdout"},
		{name: "sha512 from the length, sent in lower case", args: []string{"file", "get", sha512, "--fields", "id"},
			setup: fixture("/v2/version_file/"+strings.ToLower(sha512), "version_ygf8cVZg"),
			query: map[string]string{"algorithm": "sha512"}},
		{name: "--algorithm that does not fit the length", args: []string{"file", "get", sodiumSHA1, "--algorithm", "sha512"},
			code: 2, errIn: `"code":"invalid_input"`},
		{name: "not a hash", args: []string{"file", "get", "xyz"}, code: 2, errIn: "expected 40 (sha1) or 128 (sha512) hex digits"},
		{name: "39 hex digits", args: []string{"file", "get", sodiumSHA1[:39]}, code: 2},
		{name: "unknown algorithm", args: []string{"file", "get", sodiumSHA1, "--algorithm", "md5"}, code: 2},
		{name: "not found", args: []string{"file", "get", sodiumSHA1}, code: 3,
			setup:  func(m *testutil.MockAPI) { m.On("GET", "/v2/version_file/"+sodiumSHA1).Status(404).Empty() },
			stderr: "file_get/not_found.stderr"},
		{name: "dry run", args: []string{"file", "get", sodiumSHA1, "--dry-run"}, stdout: "file_get/dry_run.stdout"},
	})
}

func identifyMock(m *testutil.MockAPI) {
	m.On("POST", "/v2/version_files").Fixture("version_files")
	m.On("GET", "/v2/projects").Fixture("projects_2")
}

func TestFileIdentify(t *testing.T) {
	files := filepath.Join("testdata", "files")
	runReadCases(t, []readCase{
		{name: "table", args: []string{"file", "identify", files}, setup: identifyMock, tty: true,
			stdout: "file_identify/table.stdout", stderr: "file_identify/table.stderr"},
		{name: "all fields", args: []string{"file", "identify", files, "--full"}, setup: identifyMock,
			stdout: "file_identify/full.stdout"},
		{name: "no project lookup", args: []string{"file", "identify", files, "--no-project", "--fields", "path,project_id,project_slug"},
			setup:  func(m *testutil.MockAPI) { m.On("POST", "/v2/version_files").Fixture("version_files") },
			stdout: "file_identify/no_project.stdout"},
		{name: "dry run shows the hashes", args: []string{"file", "identify", files, "--dry-run"},
			stdout: "file_identify/dry_run.stdout"},
		{name: "missing path", args: []string{"file", "identify", filepath.Join(files, "nope.jar")}, code: 1,
			errIn: `"code":"io_error"`},
		{name: "directory without mod files", args: []string{"file", "identify", filepath.Join("testdata", "golden", "help")}, code: 2,
			errIn: "no files to check"},
	})
}

func TestFileIdentifyRequest(t *testing.T) {
	m := testutil.NewMockAPI(t)
	identifyMock(m)
	r := testutil.Run(t, m, testutil.RunOpts{}, "file", "identify", filepath.Join("testdata", "files", "b.jar"), filepath.Join("testdata", "files"))
	if r.Code != 0 {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
	b := bodyOf(t, m, "POST", "/v2/version_files")
	want := []string{aSHA512, sha512Hex("fake mod b\n")}
	if len(b) != 1 || !slices.Equal(b[0].Hashes, want) || b[0].Algorithm != "sha512" {
		t.Errorf("body = %+v, want hashes %v (b.jar once)", b, want)
	}
	if q := m.Last().Query.Get("ids"); q != `["AANobbMI"]` {
		t.Errorf("projects ids = %s", q)
	}
	if !strings.Contains(r.Stderr, "note: 1 of 2 files not found on Modrinth") {
		t.Errorf("stderr %q", r.Stderr)
	}
}

func TestFileIdentifySHA1(t *testing.T) {
	m := testutil.NewMockAPI(t)
	m.On("POST", "/v2/version_files").JSON(`{}`)
	r := testutil.Run(t, m, testutil.RunOpts{}, "file", "identify", filepath.Join("testdata", "files", "a.jar"), "--algorithm", "sha1")
	if r.Code != 0 {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
	if b := bodyOf(t, m, "POST", "/v2/version_files"); len(b) != 1 || b[0].Algorithm != "sha1" ||
		!slices.Equal(b[0].Hashes, []string{"e85f0313003eedae80ed8334c11549e1db03ae08"}) {
		t.Errorf("body = %+v", b)
	}
}

func TestExpandPaths(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"b.jar": "b", "A.JAR": "a", "c.zip": "c", "d.mrpack": "d", "e.litemod": "e", "f.jar.disabled": "f",
		"notes.txt": "n", "g.disabled": "g", "sub/h.jar": "h",
	})
	extra := filepath.Join(dir, "notes.txt")
	m := testutil.NewMockAPI(t)
	m.On("POST", "/v2/version_files").JSON(`{}`)
	r := testutil.Run(t, m, testutil.RunOpts{}, "file", "identify", dir, extra, dir, "--fields", "path", "-o", "ndjson")
	if r.Code != 0 {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
	got := strings.Split(strings.TrimSpace(testutil.Normalize(r.Stdout)), "\n")
	var want []string
	for _, n := range []string{"A.JAR", "b.jar", "c.zip", "d.mrpack", "e.litemod", "f.jar.disabled", "notes.txt"} {
		want = append(want, fmt.Sprintf(`{"path":"<TMP>/%s"}`, n)) // Normalize writes "/" on every OS
	}
	if !slices.Equal(got, want) {
		t.Errorf("paths\n got %v\nwant %v", got, want)
	}
	if !strings.Contains(r.Stderr, "note: 7 of 7 files not found on Modrinth") {
		t.Errorf("stderr %q", r.Stderr)
	}
}

func TestFileHashChunks(t *testing.T) {
	files := map[string]string{}
	for i := range 150 {
		files[fmt.Sprintf("m%03d.jar", i)] = fmt.Sprintf("content %d", i)
	}
	files["dup.jar"] = "content 0" // same hash as m000.jar: sent once
	dir := writeFiles(t, files)
	m := testutil.NewMockAPI(t)
	m.On("POST", "/v2/version_files").JSON(`{}`).Times(2)
	r := testutil.Run(t, m, testutil.RunOpts{}, "file", "identify", dir, "--no-project")
	if r.Code != 0 {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
	b := bodyOf(t, m, "POST", "/v2/version_files")
	if len(b) != 2 || len(b[0].Hashes) != 100 || len(b[1].Hashes) != 50 {
		t.Errorf("%d requests: %v", len(b), b)
	}
}

// updatesMock answers for four files: a.jar has a newer version, c.jar is current,
// d.jar has nothing for the loader, and b.jar is unknown.
func updatesMock(a, c, d string) func(m *testutil.MockAPI) {
	ver := func(id, number string, files string) string {
		return fmt.Sprintf(`{"id":%q,"project_id":"P%s","version_number":%q,"version_type":"release","date_published":"2024-01-01T00:00:00Z","files":[%s]}`,
			id, id[:1], number, files)
	}
	return func(m *testutil.MockAPI) {
		m.On("POST", "/v2/version_files").JSON(fmt.Sprintf(`{%q:%s,%q:%s,%q:%s}`,
			a, ver("AAAAAAA1", "1.0", ""), c, ver("CCCCCCC1", "3.0", ""), d, ver("DDDDDDD1", "4.0", "")))
		m.On("POST", "/v2/version_files/update").JSON(fmt.Sprintf(`{%q:%s,%q:%s}`,
			a, ver("AAAAAAA2", "1.1", `{"filename":"extra.txt","primary":false,"url":"u0","size":1,"hashes":{"sha512":"x"}},`+
				`{"filename":"a-1.1.jar","primary":true,"url":"https://cdn.modrinth.com/data/PA/versions/AAAAAAA2/a-1.1.jar","size":42,"hashes":{"sha512":"abc"}}`),
			c, ver("CCCCCCC1", "3.0", "")))
		m.On("GET", "/v2/projects").JSON(`[{"id":"PA","slug":"alpha","title":"Alpha"},{"id":"PC","slug":"gamma","title":"Gamma"}]`)
	}
}

func updatesDir(t *testing.T) (dir string, setup func(m *testutil.MockAPI)) {
	dir = writeFiles(t, map[string]string{"a.jar": "a", "b.jar": "b", "c.jar": "c", "d.jar": "d"})
	return dir, updatesMock(sha512Hex("a"), sha512Hex("c"), sha512Hex("d"))
}

func TestFileUpdates(t *testing.T) {
	dir, setup := updatesDir(t)
	runReadCases(t, []readCase{
		{name: "statuses", args: []string{"file", "updates", dir, "--loader", "fabric", "--game-version", "1.20.1"}, setup: setup,
			stdout: "file_updates/statuses.stdout", stderr: "file_updates/statuses.stderr"},
		{name: "table", args: []string{"file", "updates", dir, "--loader", "fabric", "--game-version", "1.20.1"}, setup: setup, tty: true,
			stdout: "file_updates/table.stdout"},
		{name: "full", args: []string{"file", "updates", dir, "--loader", "fabric", "--game-version", "1.20.1", "--full"}, setup: setup,
			stdout: "file_updates/full.stdout"},
		{name: "only updates", args: []string{"file", "updates", dir, "--loader", "fabric", "--game-version", "1.20.1", "--only-updates",
			"--fields", "filename,status,latest.version_id"}, setup: setup, stdout: "file_updates/only_updates.stdout"},
		{name: "--loader is required", args: []string{"file", "updates", dir, "--game-version", "1.20.1"}, code: 2,
			stderr: "file_updates/no_loader.stderr"},
		{name: "--game-version is required", args: []string{"file", "updates", dir, "--loader", "fabric"}, code: 2,
			errIn: `"code":"usage"`},
		{name: "bad version type", args: []string{"file", "updates", dir, "--loader", "fabric", "--game-version", "1.20.1", "--version-type", "stable"},
			code: 2},
	})
}

func TestFileUpdatesRequests(t *testing.T) {
	dir, setup := updatesDir(t)
	m := testutil.NewMockAPI(t)
	setup(m)
	r := testutil.Run(t, m, testutil.RunOpts{}, "file", "updates", dir, "--loader", "fabric", "--loader", "quilt",
		"--game-version", "1.20.1", "--version-type", "release")
	if r.Code != 0 {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
	b := bodyOf(t, m, "POST", "/v2/version_files/update")
	known := []string{sha512Hex("a"), sha512Hex("c"), sha512Hex("d")} // b.jar is unknown: not asked
	if len(b) != 1 || !slices.Equal(b[0].Hashes, known) || b[0].Algorithm != "sha512" ||
		!slices.Equal(b[0].Loaders, []string{"fabric", "quilt"}) || !slices.Equal(b[0].GameVersions, []string{"1.20.1"}) ||
		!slices.Equal(b[0].VersionTypes, []string{"release"}) {
		t.Errorf("update body = %+v", b)
	}
	raw := ""
	for _, req := range m.Requests() {
		if req.Path == "/v2/version_files/update" {
			raw = string(req.Body)
		}
	}
	m2 := testutil.NewMockAPI(t)
	setup(m2)
	if r := testutil.Run(t, m2, testutil.RunOpts{}, "file", "updates", dir, "--loader", "fabric", "--game-version", "1.20.1"); r.Code != 0 {
		t.Fatalf("exit %d", r.Code)
	}
	for _, req := range m2.Requests() {
		if req.Path == "/v2/version_files/update" && strings.Contains(string(req.Body), "version_types") {
			t.Errorf("version_types sent without --version-type: %s (with it: %s)", req.Body, raw)
		}
	}
}

func TestFileUpdatesAllUnknown(t *testing.T) {
	m := testutil.NewMockAPI(t)
	m.On("POST", "/v2/version_files").JSON(`{}`)
	r := testutil.Run(t, m, testutil.RunOpts{}, "file", "updates", filepath.Join("testdata", "files"), "--loader", "fabric", "--game-version", "1.20.1")
	if r.Code != 0 || len(m.Requests()) != 1 {
		t.Fatalf("exit %d, %d requests: %s", r.Code, len(m.Requests()), r.Stderr)
	}
	if !strings.Contains(r.Stderr, "note: 0 updates available, 0 up to date, 2 unknown") {
		t.Errorf("stderr %q", r.Stderr)
	}
}
