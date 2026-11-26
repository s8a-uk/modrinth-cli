package output

import (
	"bytes"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/s8a-uk/modrinth-cli/internal/apperr"
	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
)

func TestResolveFormat(t *testing.T) {
	cases := []struct {
		setting string
		tty     bool
		want    Format
	}{
		{"auto", true, Table}, {"auto", false, JSON}, {"", true, Table}, {"", false, JSON},
		{"json", true, JSON}, {"ndjson", false, NDJSON}, {"table", false, Table},
	}
	for _, tc := range cases {
		got, err := ResolveFormat(tc.setting, tc.tty)
		if err != nil || got != tc.want {
			t.Errorf("ResolveFormat(%q, %t) = %q, %v; want %q", tc.setting, tc.tty, got, err, tc.want)
		}
	}
	_, err := ResolveFormat("xml", false)
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != apperr.InvalidInput {
		t.Errorf("ResolveFormat(xml) error = %v, want invalid_input", err)
	}
}

func mustParse(t *testing.T, s string) jsonx.Value {
	t.Helper()
	v, err := jsonx.Parse([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestRenderers(t *testing.T) {
	obj := `{"id":"a","n":1}`
	arr := `[{"id":"a"},{"id":"b"}]`
	page := `{"hits":[{"id":"a"},{"id":"b"}],"offset":0,"limit":10,"total_hits":2}`
	cases := []struct {
		name   string
		shape  Shape
		value  string
		format Format
		pretty bool
		want   string
	}{
		{"object json", Object, obj, JSON, false, obj + "\n"},
		{"object json pretty", Object, obj, JSON, true, "{\n  \"id\": \"a\",\n  \"n\": 1\n}\n"},
		{"object ndjson", Object, obj, NDJSON, false, obj + "\n"},
		{"array json", Array, arr, JSON, false, arr + "\n"},
		{"array ndjson", Array, arr, NDJSON, false, "{\"id\":\"a\"}\n{\"id\":\"b\"}\n"},
		{"search_page json", SearchPage, page, JSON, false, page + "\n"},
		{"search_page ndjson", SearchPage, page, NDJSON, false, "{\"id\":\"a\"}\n{\"id\":\"b\"}\n"},
		{"ack ndjson", Ack, `{"ok":true}`, NDJSON, false, "{\"ok\":true}\n"},
		{"raw array ndjson", Raw, `[1,2]`, NDJSON, false, "1\n2\n"},
		{"raw object ndjson", Raw, obj, NDJSON, false, obj + "\n"},
		{"raw table is indented json", Raw, `{"a":1}`, Table, false, "{\n  \"a\": 1\n}\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var b bytes.Buffer
			r := &Result{Shape: tc.shape, Value: mustParse(t, tc.value)}
			err := RendererFor(tc.format).Render(&b, r, &Spec{Shape: tc.shape}, Options{Format: tc.format, Pretty: tc.pretty})
			if err != nil {
				t.Fatal(err)
			}
			if b.String() != tc.want {
				t.Errorf("got %q, want %q", b.String(), tc.want)
			}
		})
	}
}

func TestStderr(t *testing.T) {
	var b bytes.Buffer
	s := NewStderr(&b, 0, false)
	s.Note("%d of %d", 1, 2)
	s.Warn("careful")
	if got, want := b.String(), "note: 1 of 2\nwarning: careful\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	b.Reset()
	q := NewStderr(&b, 0, true)
	q.Note("hidden")
	q.Warn("shown")
	if got, want := b.String(), "warning: shown\n"; got != want {
		t.Errorf("quiet: got %q, want %q", got, want)
	}
}

func TestStderrLogging(t *testing.T) {
	h := http.Header{"Authorization": {"mrp_secret"}, "User-Agent": {"ua"}}
	var b bytes.Buffer
	NewStderr(&b, 0, false).Request("GET", "u")
	if b.Len() != 0 {
		t.Errorf("verbosity 0 logged %q", b.String())
	}
	s := NewStderr(&b, 1, false)
	s.Request("GET", "https://x/v2/a")
	s.Response(200, 142*time.Millisecond, 287)
	s.Response(404, time.Millisecond, -1)
	s.Headers(">", h)
	if got, want := b.String(), "> GET https://x/v2/a\n< 200 (142ms) ratelimit-remaining=287\n< 404 (1ms)\n"; got != want {
		t.Errorf("-v: got %q, want %q", got, want)
	}
	b.Reset()
	NewStderr(&b, 2, false).Headers(">", h)
	if got, want := b.String(), "> Authorization: ***\n> User-Agent: ua\n"; got != want {
		t.Errorf("-vv: got %q, want %q", got, want)
	}
	b.Reset()
	NewStderr(&b, 0, true).Include("HTTP/2.0", 200, http.Header{"B": {"2"}, "A": {"1"}})
	if got, want := b.String(), "HTTP/2.0 200 OK\nA: 1\nB: 2\n"; got != want {
		t.Errorf("Include: got %q, want %q", got, want)
	}
}

func TestRawBytes(t *testing.T) {
	for _, f := range []Format{JSON, NDJSON, Table} {
		var b bytes.Buffer
		if err := RendererFor(f).Render(&b, &Result{Shape: Raw, Bytes: []byte("<html>")}, &Spec{}, Options{}); err != nil || b.String() != "<html>" {
			t.Errorf("%s: %q %v", f, b.String(), err)
		}
	}
}
