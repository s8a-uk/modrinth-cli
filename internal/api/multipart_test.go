package api

import (
	"bytes"
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/s8a-uk/modrinth-cli/internal/apperr"
	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
)

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func readAll(t *testing.T, m *Multipart) (string, []byte, int64) {
	t.Helper()
	ct, body, n, err := m.build()
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	b, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	return ct, b, n
}

func TestMultipartBuild(t *testing.T) {
	jar := writeTemp(t, "my mod.jar", "JARDATA")
	m := &Multipart{Parts: []Part{
		DataPart(jsonx.Obj(jsonx.Member{Key: "file_parts", Value: jsonx.Strs([]string{"file0"})})),
		FilePart("file0", jar, 7),
	}}
	m.SetBoundary("TESTBOUNDARY")
	ct, got, n := readAll(t, m)
	want := "--TESTBOUNDARY\r\n" +
		"Content-Disposition: form-data; name=\"data\"\r\n" +
		"Content-Type: application/json\r\n\r\n" +
		`{"file_parts":["file0"]}` +
		"\r\n--TESTBOUNDARY\r\n" +
		"Content-Disposition: form-data; name=\"file0\"; filename=\"my mod.jar\"\r\n" +
		"Content-Type: application/java-archive\r\n\r\n" +
		"JARDATA" +
		"\r\n--TESTBOUNDARY--\r\n"
	if ct != "multipart/form-data; boundary=TESTBOUNDARY" {
		t.Errorf("content type = %q", ct)
	}
	if string(got) != want {
		t.Errorf("body =\n%q\nwant\n%q", got, want)
	}
	if n != int64(len(got)) {
		t.Errorf("length = %d, body has %d bytes", n, len(got))
	}
	_, again, _ := readAll(t, m)
	if !bytes.Equal(again, got) {
		t.Errorf("a second build differs:\n%q", again)
	}
}

func TestMultipartRandomBoundaryIsStable(t *testing.T) {
	m := &Multipart{Parts: []Part{{Name: "a", Data: []byte("x")}}}
	ct1, b1, _ := readAll(t, m)
	ct2, b2, _ := readAll(t, m)
	if ct1 != ct2 || !bytes.Equal(b1, b2) {
		t.Errorf("boundary changed between builds: %q %q", ct1, ct2)
	}
	_, params, err := mime.ParseMediaType(ct1)
	if err != nil || params["boundary"] == "" {
		t.Fatalf("content type %q: %v", ct1, err)
	}
	r := multipart.NewReader(bytes.NewReader(b1), params["boundary"])
	p, err := r.NextPart()
	if err != nil || p.FormName() != "a" {
		t.Fatalf("part = %v, %v", p, err)
	}
}

func TestMultipartProgressAndMissingFile(t *testing.T) {
	jar := writeTemp(t, "a.jar", strings.Repeat("x", 1000))
	var last, total int64
	m := &Multipart{Parts: []Part{FilePart("file0", jar, 1000)}, Progress: func(d, n int64) { last, total = d, n }}
	_, b, n := readAll(t, m)
	if last != int64(len(b)) || total != n {
		t.Errorf("progress = %d/%d, body %d/%d", last, total, len(b), n)
	}

	gone := &Multipart{Parts: []Part{FilePart("file0", filepath.Join(t.TempDir(), "gone.jar"), 3)}}
	if _, _, _, err := gone.build(); apperr.From(err).Code != apperr.IOError {
		t.Errorf("missing file: %v", err)
	}
}

// recordBodies records each request's body and Content-Length.
type recordBodies struct {
	statuses []int
	bodies   [][]byte
	lengths  []int64
	types    []string
}

func (d *recordBodies) Do(req *http.Request) (*http.Response, error) {
	b, _ := io.ReadAll(req.Body)
	d.bodies = append(d.bodies, b)
	d.lengths = append(d.lengths, req.ContentLength)
	d.types = append(d.types, req.Header.Get("Content-Type"))
	st := d.statuses[len(d.bodies)-1]
	h := http.Header{"Content-Type": {"application/json"}}
	if st == 429 {
		h.Set("Retry-After", "1")
	}
	return &http.Response{StatusCode: st, Proto: "HTTP/1.1", Header: h, Body: io.NopCloser(strings.NewReader(`{"id":"IIJJKKLL"}`))}, nil
}

// TestMultipartResentAfter429 checks that a 429 resends the same body (files are
// reopened), and that a 500 on a POST is not retried.
func TestMultipartResentAfter429(t *testing.T) {
	jar := writeTemp(t, "a.jar", "JAR")
	newReq := func() Request {
		return Request{Method: "POST", Path: "/version", Multipart: &Multipart{Parts: []Part{
			DataPart(jsonx.Obj()), FilePart("file0", jar, 3)}}}
	}
	d := &recordBodies{statuses: []int{429, 200}}
	c := New(Options{BaseURL: "https://api.modrinth.com", UserAgent: "ua", MaxWait: time.Minute, Doer: d, Clock: &stepClock{}})
	if _, err := c.Do(context.Background(), newReq()); err != nil {
		t.Fatal(err)
	}
	if len(d.bodies) != 2 || !bytes.Equal(d.bodies[0], d.bodies[1]) || d.types[0] != d.types[1] {
		t.Fatalf("bodies = %q, types %q", d.bodies, d.types)
	}
	if d.lengths[0] != int64(len(d.bodies[0])) {
		t.Errorf("Content-Length = %d, body %d", d.lengths[0], len(d.bodies[0]))
	}

	d = &recordBodies{statuses: []int{500, 200}}
	c = New(Options{BaseURL: "https://api.modrinth.com", UserAgent: "ua", MaxWait: time.Minute, Doer: d, Clock: &stepClock{}})
	_, err := c.Do(context.Background(), newReq())
	if apperr.From(err).Code != apperr.ServerError || len(d.bodies) != 1 {
		t.Errorf("500: err %v after %d requests, want server_error after 1", err, len(d.bodies))
	}
}

// stepClock sleeps instantly.
type stepClock struct{ now time.Time }

func (c *stepClock) Now() time.Time { return c.now }
func (c *stepClock) Sleep(_ context.Context, d time.Duration) error {
	c.now = c.now.Add(d)
	return nil
}

func TestMultipartDryRunPlan(t *testing.T) {
	jar := writeTemp(t, "a.jar", "abc")
	c := New(Options{BaseURL: "https://api.modrinth.com", Token: "tok", UserAgent: "ua", DryRun: true})
	_, err := c.Do(context.Background(), Request{Method: "POST", Path: "/version", Multipart: &Multipart{Parts: []Part{
		DataPart(jsonx.Obj(jsonx.Member{Key: "a", Value: jsonx.Int(1)})),
		{Name: "note", Data: []byte("hi")},
		FilePart("file0", jar, 3),
	}}})
	dry, ok := err.(*DryRunError)
	if !ok {
		t.Fatalf("err = %v", err)
	}
	var b bytes.Buffer
	_ = jsonx.Encode(&b, dry.Plan, "")
	want := `{"dry_run":true,"request":{"method":"POST","url":"https://api.modrinth.com/v2/version","headers":{"Accept":"application/json",` +
		`"Authorization":"***","Content-Type":"multipart/form-data","User-Agent":"ua"},"multipart":[{"name":"data","json":{"a":1}},` +
		`{"name":"note","value":"hi"},{"name":"file0","filename":"a.jar","content_type":"application/java-archive","size":3,` +
		`"sha512":"ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f"}]}}` + "\n"
	if b.String() != want {
		t.Errorf("plan =\n%s\nwant\n%s", b.String(), want)
	}
}
