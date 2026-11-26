package output

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/s8a-uk/modrinth-cli/internal/apperr"
	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
)

var testSpec = &Spec{
	Shape:         Object,
	Fields:        []string{"id", "title", "license.id", "license.name", "gallery.url", "gallery.title", "n"},
	DefaultFields: []string{"id", "license.id"},
}

func TestCheckFields(t *testing.T) {
	ok := [][]string{nil, {"id"}, {"license"}, {"license.id", "title"}, {"gallery"}, {"all"}}
	for _, f := range ok {
		if err := CheckFields(testSpec, f, false, "x get", true); err != nil {
			t.Errorf("CheckFields(%q) = %v", f, err)
		}
	}
	cases := []struct {
		fields []string
		full   bool
		spec   *Spec
		code   apperr.Code
		hint   string
	}{
		{[]string{"titel"}, false, testSpec, apperr.UnknownField, "did you mean: title? see: mdr schema x get"},
		{[]string{"zzzzzzzz"}, false, testSpec, apperr.UnknownField, "fields: id, title, license.id, license.name, gallery.url, gallery.title, n; see: mdr schema x get"},
		{[]string{"lic"}, false, testSpec, apperr.UnknownField, "fields: id, title, license.id, license.name, gallery.url, gallery.title, n; see: mdr schema x get"}, // a prefix is not a parent
		{[]string{"license.idd"}, false, testSpec, apperr.UnknownField, "did you mean: license.id? see: mdr schema x get"},
		{[]string{"id"}, true, testSpec, apperr.Usage, ""},
		{[]string{"all", "id"}, false, testSpec, apperr.Usage, ""},
		{[]string{"id"}, false, &Spec{Shape: Raw}, apperr.Usage, "see: mdr schema x get"},
		{[]string{"a..b"}, false, &Spec{Shape: Raw, AnyFields: true}, apperr.Usage, "use dot-separated keys, e.g. license.id"},
		{[]string{"id"}, false, &Spec{Shape: Ack}, apperr.Usage, "see: mdr schema x get"},
	}
	for _, tc := range cases {
		err := CheckFields(tc.spec, tc.fields, tc.full, "x get", true)
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != tc.code || (tc.hint != "" && ae.Hint != tc.hint) {
			t.Errorf("CheckFields(%q, full=%t) = %v (hint %q); want %s with hint %q", tc.fields, tc.full, err, hintOf(ae), tc.code, tc.hint)
		}
	}
	// Up to three suggestions, closest first.
	spec := &Spec{Shape: Object, Fields: []string{"abcde", "abcdx", "abcdy", "abcdz", "zzzzz"}}
	var ae *apperr.Error
	errors.As(CheckFields(spec, []string{"abcdq"}, false, "c", true), &ae)
	if want := "did you mean: abcde, abcdx, abcdy? see: mdr schema c"; ae == nil || ae.Hint != want {
		t.Errorf("hint = %q, want %q", hintOf(ae), want)
	}
}

// TestCheckFieldsNotStrict: without strict, names outside Fields wait for the response.
func TestCheckFieldsNotStrict(t *testing.T) {
	if err := CheckFields(testSpec, []string{"new_key", "x.y"}, false, "x get", false); err != nil {
		t.Errorf("CheckFields = %v", err)
	}
	if err := CheckFields(&Spec{Shape: Raw, AnyFields: true}, []string{"id"}, false, "api", false); err != nil {
		t.Errorf("CheckFields(AnyFields) = %v", err)
	}
}

func TestResolveFields(t *testing.T) {
	v, _ := jsonx.Parse([]byte(`[{"id":"a","new":{"k":1}},{"id":"b","other":[{"z":1}]}]`))
	spec := &Spec{Shape: Array, Fields: []string{"id", "title"}}
	undoc, err := ResolveFields(v, Array, spec, []string{"id", "new.k", "other.z"}, "x list")
	if err != nil || strings.Join(undoc, ",") != "new.k,other.z" {
		t.Errorf("undocumented = %q, err %v", undoc, err)
	}
	_, err = ResolveFields(v, Array, spec, []string{"othr"}, "x list")
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != apperr.UnknownField || ae.Hint != "did you mean: other? see: mdr schema x list" {
		t.Errorf("err = %v (hint %q)", err, hintOf(ae))
	}
	// An empty array cannot be checked; documented names never need the response.
	if _, err := ResolveFields(jsonx.Arr(), Array, spec, []string{"zz"}, "x list"); err != nil {
		t.Errorf("empty array: %v", err)
	}
	// A raw result without AnyFields (a dry-run plan) is not checked.
	if _, err := ResolveFields(v, Raw, &Spec{Shape: Raw}, []string{"zz"}, "x"); err != nil {
		t.Errorf("raw: %v", err)
	}
}

func hintOf(e *apperr.Error) string {
	if e == nil {
		return ""
	}
	return e.Hint
}

func encode(v jsonx.Value) string {
	var b bytes.Buffer
	_ = jsonx.Encode(&b, v, "")
	return strings.TrimSuffix(b.String(), "\n")
}

func TestProject(t *testing.T) {
	obj := `{"title":"T","id":"a","license":{"id":"MIT","name":"M","url":"u"},"gallery":[{"url":"u1","title":"g1","x":1},{"url":"u2"}],"n":null}`
	cases := []struct {
		name  string
		shape Shape
		in    string
		opts  Options
		want  string
	}{
		{"defaults, in selection order", Object, obj, Options{}, `{"id":"a","license":{"id":"MIT"}}`},
		{"fields order", Object, obj, Options{Fields: []string{"license.name", "id", "license.id"}}, `{"license":{"name":"M","id":"MIT"},"id":"a"}`},
		{"parent keeps the object", Object, obj, Options{Fields: []string{"license"}}, `{"license":{"id":"MIT","name":"M","url":"u"}}`},
		{"array of objects", Object, obj, Options{Fields: []string{"gallery.title"}}, `{"gallery":[{"title":"g1"},{"title":null}]}`},
		{"missing keys are null", Object, `{"id":"a"}`, Options{Fields: []string{"title", "license.id"}}, `{"title":null,"license":null}`},
		{"full", Object, obj, Options{Full: true}, obj},
		{"all", Object, obj, Options{Fields: []string{"all"}}, obj},
		{"array shape", Array, `[{"id":"a","title":"x"},{"title":"y"}]`, Options{Fields: []string{"id"}}, `[{"id":"a"},{"id":null}]`},
		{"search page keeps the counts", SearchPage, `{"hits":[{"id":"a","title":"x"}],"offset":0,"limit":1,"total_hits":9}`,
			Options{Fields: []string{"title"}}, `{"hits":[{"title":"x"}],"offset":0,"limit":1,"total_hits":9}`},
	}
	for _, tc := range cases {
		spec := *testSpec
		spec.Shape = tc.shape
		got := encode(Project(mustParse(t, tc.in), tc.shape, &spec, tc.opts))
		if got != tc.want {
			t.Errorf("%s:\n got %s\nwant %s", tc.name, got, tc.want)
		}
	}
	raw := mustParse(t, obj)
	if got := encode(Project(raw, Raw, &Spec{Shape: Raw}, Options{})); got != obj {
		t.Errorf("raw is projected: %s", got)
	}
}

func TestFormatValue(t *testing.T) {
	cases := []struct {
		in, kind, want string
	}{
		{`999`, KindNum, "999"},
		{`1000`, KindNum, "1.0K"},
		{`1299`, KindNum, "1.2K"},
		{`98765432`, KindNum, "98.7M"},
		{`5699999999`, KindNum, "5.6B"},
		{`-1500`, KindNum, "-1.5K"},
		{`12.5`, KindNum, "12.5"},
		{`98765432`, KindText, "98765432"},
		{`"2026-10-30T18:11:02.123456Z"`, KindDate, "2026-10-30"},
		{`"2026-11-25T02:40:48.239935+00:00"`, KindDate, "2026-11-25"},
		{`"yesterday"`, KindDate, "yesterday"},
		{`["a","b",1]`, KindList, "a,b,1"},
		{`["a","b"]`, "", "a,b"},
		{`[{"a":1}]`, KindList, `[{"a":1}]`},
		{`{"a":[1,2]}`, "", `{"a":[1,2]}`},
		{`true`, KindBool, "yes"},
		{`false`, "", "no"},
		{`null`, KindText, "-"},
		{`"a\nb\tc\u001b[31mred\u0085"`, KindText, "a b c[31mred"},
	}
	for _, tc := range cases {
		if got := formatValue(mustParse(t, tc.in), tc.kind); got != tc.want {
			t.Errorf("formatValue(%s, %q) = %q, want %q", tc.in, tc.kind, got, tc.want)
		}
	}
}

func TestWidthAndTruncate(t *testing.T) {
	if w := textWidth("日本語abc"); w != 9 {
		t.Errorf("textWidth = %d, want 9", w)
	}
	if w := textWidth("한글ｱＡ"); w != 7 { // Hangul and fullwidth A are wide, halfwidth katakana is not
		t.Errorf("textWidth = %d, want 7", w)
	}
	cases := []struct {
		in   string
		w    int
		want string
	}{
		{"abcdef", 6, "abcdef"},
		{"abcdef", 5, "abcd…"},
		{"abcdef", 1, "…"},
		{"abcdef", 0, ""},
		{"日本語です", 6, "日本…"},
		{"日本語です", 5, "日本…"},
	}
	for _, tc := range cases {
		if got := truncate(tc.in, tc.w); got != tc.want || textWidth(got) > tc.w {
			t.Errorf("truncate(%q, %d) = %q, want %q", tc.in, tc.w, got, tc.want)
		}
	}
}

func renderTable(t *testing.T, shape Shape, value string, spec *Spec, opts Options) (string, []string) {
	t.Helper()
	var notes []string
	opts.Format = Table
	opts.Notef = func(f string, a ...any) { notes = append(notes, fmt.Sprintf(f, a...)) }
	var b bytes.Buffer
	if err := RendererFor(Table).Render(&b, &Result{Shape: shape, Value: mustParse(t, value)}, spec, opts); err != nil {
		t.Fatal(err)
	}
	return b.String(), notes
}

var rowsSpec = &Spec{
	Shape:  Array,
	Fields: []string{"id", "n", "ok", "when", "tags", "desc"},
	Table: []Column{
		{Header: "ID", Path: "id"},
		{Header: "N", Path: "n", Kind: KindNum},
		{Header: "OK", Path: "ok", Kind: KindBool},
		{Header: "WHEN", Path: "when", Kind: KindDate},
		{Header: "TAGS", Path: "tags", Kind: KindList, Max: 8},
		{Header: "DESC", Path: "desc"},
	},
}

const rows = `[{"id":"a","n":1234,"ok":true,"when":"2026-01-02T03:04:05Z","tags":["x","y"],"desc":"short"},` +
	`{"id":"日本","n":5,"ok":false,"when":null,"tags":["longtag","more"],"desc":"a much longer description here"}]`

func TestTableRows(t *testing.T) {
	got, notes := renderTable(t, Array, rows, rowsSpec, Options{Width: 100})
	want := "" +
		"ID       N  OK   WHEN        TAGS      DESC\n" +
		"a     1.2K  yes  2026-01-02  x,y       short\n" +
		"日本     5  no   -           longtag…  a much longer description here\n"
	if got != want || len(notes) != 0 {
		t.Errorf("got\n%s\nwant\n%s\nnotes %q", got, want, notes)
	}

	// The last column shrinks to the width.
	got, notes = renderTable(t, Array, rows, rowsSpec, Options{Width: 50})
	want = "" +
		"ID       N  OK   WHEN        TAGS      DESC\n" +
		"a     1.2K  yes  2026-01-02  x,y       short\n" +
		"日本     5  no   -           longtag…  a much lon…\n"
	if got != want || len(notes) != 0 {
		t.Errorf("width 50: got\n%s\nwant\n%s\nnotes %q", got, want, notes)
	}
	for _, line := range strings.Split(strings.TrimSuffix(got, "\n"), "\n") {
		if w := textWidth(line); w > 50 {
			t.Errorf("line is %d wide: %q", w, line)
		}
	}

	// Too narrow for the last column: columns are dropped from the right.
	got, notes = renderTable(t, Array, rows, rowsSpec, Options{Width: 30})
	want = "" +
		"ID       N  OK   WHEN\n" +
		"a     1.2K  yes  2026-01-02\n" +
		"日本     5  no   -\n"
	if got != want || len(notes) != 1 || !strings.HasPrefix(notes[0], "2 of 6 columns hidden") {
		t.Errorf("width 30: got\n%s\nwant\n%s\nnotes %q", got, want, notes)
	}

	// Color: bold header, dim null.
	got, _ = renderTable(t, Array, rows, rowsSpec, Options{Width: 100, Color: true})
	if !strings.HasPrefix(got, "\x1b[1mID ") || !strings.Contains(got, "\x1b[2m-\x1b[0m") {
		t.Errorf("color: %q", got)
	}
}

func TestTableFieldsAndEmpty(t *testing.T) {
	got, _ := renderTable(t, Array, rows, rowsSpec, Options{Width: 100, Fields: []string{"desc", "n"}})
	want := "" +
		"DESC                               N\n" +
		"short                           1.2K\n" +
		"a much longer description here     5\n"
	if got != want {
		t.Errorf("fields: got\n%s\nwant\n%s", got, want)
	}
	got, notes := renderTable(t, Array, `[]`, rowsSpec, Options{Width: 100})
	if got != "" || len(notes) != 1 || notes[0] != "no results" {
		t.Errorf("empty: %q, notes %q", got, notes)
	}
	page := `{"hits":[{"id":"a"}],"offset":5,"limit":1,"total_hits":42}`
	got, notes = renderTable(t, SearchPage, page, &Spec{Shape: SearchPage, Table: []Column{{Header: "ID", Path: "id"}}}, Options{Width: 100})
	if got != "ID\na\n" || len(notes) != 1 || notes[0] != "1 of 42 hits (offset 5)" {
		t.Errorf("page: %q, notes %q", got, notes)
	}
}

func TestTableObject(t *testing.T) {
	spec := &Spec{Shape: Object, Fields: testSpec.Fields, DefaultFields: []string{"id", "license.id", "gallery.url", "n", "title"},
		Table: []Column{{Header: "N", Path: "n", Kind: KindNum}}}
	obj := `{"id":"a","license":{"id":"MIT"},"gallery":[{"url":"u1"},{"url":"u2"}],"n":12345,"title":"a long title that is cut"}`
	got, _ := renderTable(t, Object, obj, spec, Options{Width: 24})
	want := "" +
		"ID           a\n" +
		"LICENSE ID   MIT\n" +
		"GALLERY URL  u1,u2\n" +
		"N            12.3K\n" +
		"TITLE        a long tit…\n"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	// --full: every top-level key.
	got, _ = renderTable(t, Object, `{"b":1,"a":{"x":true}}`, spec, Options{Width: 80, Full: true})
	if want := "B  1\nA  {\"x\":true}\n"; got != want {
		t.Errorf("full: got %q, want %q", got, want)
	}
	// Raw falls back to indented JSON.
	got, _ = renderTable(t, Raw, `{"a":1}`, &Spec{Shape: Raw}, Options{})
	if got != "{\n  \"a\": 1\n}\n" {
		t.Errorf("raw: %q", got)
	}
}
