//go:build live

// Package livetest runs mdr against the Modrinth staging API. It is built only with
// -tags live and does nothing unless MODRINTH_LIVE_TEST=1 (make test-live). Every
// run passes --staging; production is never contacted.
//
// Environment:
//
//	MODRINTH_LIVE_TEST=1           required, or every test is skipped by TestMain
//	MODRINTH_STAGING_TOKEN_FILE    the staging token (compose.live.yaml mounts it),
//	                               or MODRINTH_STAGING_TOKEN
//	MODRINTH_LIVE_PROJECT          id of a staging project the token may upload to
//	                               (TestLiveVersionLifecycle)
package livetest

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/s8a-uk/modrinth-cli/internal/app"
	"github.com/s8a-uk/modrinth-cli/internal/cli"
	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
)

func TestMain(m *testing.M) {
	if os.Getenv("MODRINTH_LIVE_TEST") != "1" {
		fmt.Println("livetest: MODRINTH_LIVE_TEST is not 1; skipping")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// passEnv are the only host variables a live run sees. MODRINTH_API_URL and
// MODRINTH_TOKEN are deliberately absent: the target is always --staging.
var passEnv = []string{"MODRINTH_STAGING_TOKEN", "MODRINTH_STAGING_TOKEN_FILE"}

type result struct {
	code           int
	stdout, stderr string
}

// mdr runs the CLI in-process against staging, with real HTTP and a real clock.
func mdr(t *testing.T, args ...string) result {
	t.Helper()
	env := map[string]string{"MODRINTH_CONFIG_DIR": t.TempDir()}
	for _, k := range passEnv {
		if v := os.Getenv(k); v != "" {
			env[k] = v
		}
	}
	args = append([]string{"--staging", "-o", "json"}, args...)
	var stdout, stderr bytes.Buffer
	io := cli.IO{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	code := app.Run(ctx, args, io, func(k string) string { return env[k] }, app.Deps{})
	r := result{code, stdout.String(), stderr.String()}
	t.Logf("mdr %s -> exit %d", strings.Join(args, " "), r.code)
	if r.stderr != "" {
		t.Logf("stderr: %s", strings.TrimSpace(r.stderr))
	}
	return r
}

// mustJSON runs mdr, requires exit 0 and returns stdout parsed.
func mustJSON(t *testing.T, args ...string) jsonx.Value {
	t.Helper()
	r := mdr(t, args...)
	if r.code != 0 {
		t.Fatalf("exit %d", r.code)
	}
	v, err := jsonx.Parse([]byte(r.stdout))
	if err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, r.stdout)
	}
	return v
}

func str(v jsonx.Value, path string) string {
	x, _ := v.Lookup(path)
	return x.Str
}

func requireToken(t *testing.T) {
	t.Helper()
	if os.Getenv("MODRINTH_STAGING_TOKEN") == "" && os.Getenv("MODRINTH_STAGING_TOKEN_FILE") == "" {
		t.Skip("no MODRINTH_STAGING_TOKEN or MODRINTH_STAGING_TOKEN_FILE")
	}
}

func TestLiveAuthStatus(t *testing.T) {
	requireToken(t)
	v := mustJSON(t, "auth", "status")
	if a, _ := v.Get("authenticated"); !a.Bool {
		t.Fatalf("not authenticated")
	}
	if st, _ := v.Get("staging"); !st.Bool {
		t.Errorf("staging = false")
	}
	t.Logf("authenticated on staging as %s", str(v, "username"))
}

// dummyJar writes a minimal Fabric mod (a zip with fabric.mod.json). nonce makes
// its hash unique, since Modrinth refuses files it already has.
func dummyJar(t *testing.T, dir, name, nonce string) (path, sha string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("fabric.mod.json")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(w, `{"schemaVersion":1,"id":"mdr_live_test","version":"0.0.0+%s","name":"mdr live test","environment":"*"}`, nonce)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(dir, name)
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha512.Sum512(buf.Bytes())
	return path, hex.EncodeToString(sum[:])
}

// TestLiveVersionLifecycle uploads a version with two dummy jars (the second one
// marked primary) to MODRINTH_LIVE_PROJECT, reads it back by id and by hash,
// downloads it, and deletes it. The version is deleted in a cleanup even when a
// step fails.
//
// It also checks the multipart assumptions: parts named file0, file1, ... listed in
// data.file_parts, and data.primary_file naming the part of the primary file.
func TestLiveVersionLifecycle(t *testing.T) {
	requireToken(t)
	project := os.Getenv("MODRINTH_LIVE_PROJECT")
	if project == "" {
		t.Skip("no MODRINTH_LIVE_PROJECT")
	}
	nonce := strconv.FormatInt(time.Now().UnixNano(), 36)
	dir := t.TempDir()
	jarA, _ := dummyJar(t, dir, "mdr-live-"+nonce+"-a.jar", nonce+"a")
	jarB, shaB := dummyJar(t, dir, "mdr-live-"+nonce+"-b.jar", nonce+"b")
	number := "0.0.0-live-" + nonce // the API allows at most 32 characters

	// 1. Create (file1 = jarB is the primary file).
	created := mustJSON(t, "version", "create", project, "--file", jarA, "--file", jarB, "--primary", jarB,
		"--version-number", number, "--loader", "fabric", "--game-version", "1.20.1",
		"--version-type", "alpha", "--changelog", "Created by the mdr live test; deleted right after.", "--full")
	id := str(created, "id")
	if id == "" {
		t.Fatalf("no id in the created version")
	}
	deleted := false
	t.Cleanup(func() {
		if !deleted {
			if r := mdr(t, "version", "delete", id, "--yes"); r.code != 0 {
				t.Errorf("CLEANUP FAILED: delete version %s on staging by hand", id)
			}
		}
	})
	t.Logf("created version %s (%s)", id, number)

	// Multipart assumptions (milestone criterion 8): both parts arrived, and the part
	// named by primary_file (file1) is the primary file.
	files, _ := created.Get("files")
	if len(files.Items) != 2 {
		t.Errorf("the version has %d files, want 2 (were file0 and file1 both accepted?)", len(files.Items))
	}
	for _, f := range files.Items {
		p, _ := f.Get("primary")
		t.Logf("file %s primary=%v", str(f, "filename"), p.Bool)
		wantPrimary := str(f, "filename") == filepath.Base(jarB)
		if p.Bool != wantPrimary {
			t.Errorf("file %s: primary = %v, want %v (primary_file not honored?)", str(f, "filename"), p.Bool, wantPrimary)
		}
	}

	// 2. Get by id.
	got := mustJSON(t, "version", "get", id)
	if str(got, "version_number") != number || str(got, "project_id") == "" {
		t.Errorf("version get: version_number %q, project_id %q", str(got, "version_number"), str(got, "project_id"))
	}

	// 3. Find it by the hash of its primary file.
	byHash := mustJSON(t, "file", "get", shaB)
	if str(byHash, "id") != id {
		t.Errorf("file get: id %q, want %q", str(byHash, "id"), id)
	}

	// 4. Download the primary file and verify it.
	out := t.TempDir()
	dl := mustJSON(t, "version", "download", id, "--dir", out)
	if len(dl.Items) != 1 || str(dl.Items[0], "status") != "downloaded" || str(dl.Items[0], "sha512") != shaB {
		t.Errorf("download = %s", strings.TrimSpace(mdrText(dl)))
	}

	// 5. Delete.
	ack := mustJSON(t, "version", "delete", id, "--yes")
	if str(ack, "action") != "delete" || str(ack, "id") != id {
		t.Errorf("delete ack = %s", mdrText(ack))
	}
	deleted = true
	// Logged only: the API may serve a cached copy for a moment.
	if r := mdr(t, "version", "get", id); r.code != 3 {
		t.Logf("version get right after delete: exit %d (expected 3 once caches expire)", r.code)
	}
}

func mdrText(v jsonx.Value) string {
	var b bytes.Buffer
	_ = jsonx.Encode(&b, v, "")
	return b.String()
}
