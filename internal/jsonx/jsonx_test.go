package jsonx

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func roundTrip(t *testing.T, in string) string {
	t.Helper()
	v, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse(%q): %v", in, err)
	}
	var b bytes.Buffer
	if err := Encode(&b, v, ""); err != nil {
		t.Fatal(err)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func TestRoundTrip(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"b":1,"a":2,"c":{"z":null,"y":[true,false]}}`, `{"b":1,"a":2,"c":{"z":null,"y":[true,false]}}`},
		{` { "x" : [ 1 , 2 ] } `, `{"x":[1,2]}`},
		{`12345678901234567890`, `12345678901234567890`},
		{`1e400`, `1e400`},
		{`0.1`, `0.1`},
		{`-0.0`, `-0.0`},
		{`"<a href=\"x\">&</a>"`, `"<a href=\"x\">&</a>"`},
		{`"😀"`, `"😀"`},
		{`"a\u0000b"`, `"a\u0000b"`},
		{`"tab\tnl\n"`, `"tab\tnl\n"`},
		{`[]`, `[]`},
		{`{}`, `{}`},
		{`{"a":1,"b":2,"a":3}`, `{"a":3,"b":2}`}, // last wins, first position
	}
	for _, tc := range cases {
		if got := roundTrip(t, tc.in); got != tc.want {
			t.Errorf("round trip %s = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for _, in := range []string{``, `{`, `[1,]`, `{"a"}`, `1 2`, `{} x`, `nul`, `"abc`, `{1:2}`} {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("Parse(%q) succeeded, want error", in)
		}
	}
}

func TestDepthLimit(t *testing.T) {
	ok := strings.Repeat("[", MaxDepth) + strings.Repeat("]", MaxDepth)
	if _, err := Parse([]byte(ok)); err != nil {
		t.Errorf("depth %d: %v", MaxDepth, err)
	}
	deep := strings.Repeat("[", MaxDepth+1) + strings.Repeat("]", MaxDepth+1)
	if _, err := Parse([]byte(deep)); err == nil {
		t.Errorf("depth %d accepted, want error", MaxDepth+1)
	}
}

func TestEncodeIndent(t *testing.T) {
	v, err := Parse([]byte(`{"a":1,"b":[1,{"c":"x"}],"d":{},"e":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := Encode(&b, v, "  "); err != nil {
		t.Fatal(err)
	}
	want := `{
  "a": 1,
  "b": [
    1,
    {
      "c": "x"
    }
  ],
  "d": {},
  "e": []
}
`
	if b.String() != want {
		t.Errorf("Encode indent:\n%s\nwant:\n%s", b.String(), want)
	}
}

func TestLookupAndGet(t *testing.T) {
	v, _ := Parse([]byte(`{"a":{"b":{"c":"deep"}},"arr":[{"x":1}],"s":"str"}`))
	if got, ok := v.Lookup("a.b.c"); !ok || got.StringOr("") != "deep" {
		t.Errorf("Lookup(a.b.c) = %v, %t", got, ok)
	}
	if _, ok := v.Lookup("arr.x"); ok {
		t.Error("Lookup through an array succeeded")
	}
	if _, ok := v.Lookup("a.zz"); ok {
		t.Error("Lookup of a missing key succeeded")
	}
	if _, ok := Str("x").Get("a"); ok {
		t.Error("Get on a string succeeded")
	}
	if got := v.Members[0].Value.StringOr("def"); got != "def" {
		t.Errorf("StringOr on object = %q", got)
	}
	if !(Value{}).IsNull() || Str("").IsNull() {
		t.Error("IsNull is wrong")
	}
}

func TestKeys(t *testing.T) {
	v, _ := Parse([]byte(`[{"id":1,"license":{"id":"x"}},{"id":2,"extra":true,"license":{"name":"n"}}]`))
	want := []string{"id", "license", "license.id", "extra", "license.name"}
	if got := Keys(v); !reflect.DeepEqual(got, want) {
		t.Errorf("Keys = %v, want %v", got, want)
	}
}

func TestBuildersAndFromAny(t *testing.T) {
	v := Obj(Member{"n", Int(-3)}, Member{"b", BoolV(true)}, Member{"l", Strs([]string{"a"})}, Member{"e", Arr()}, Member{"o", Obj()})
	var b bytes.Buffer
	_ = Encode(&b, v, "")
	if got, want := b.String(), `{"n":-3,"b":true,"l":["a"],"e":[],"o":{}}`+"\n"; got != want {
		t.Errorf("builders = %s, want %s", got, want)
	}
	type s struct {
		B string `json:"b"`
		A int    `json:"a"`
	}
	fv, err := FromAny(s{B: "<x>", A: 1})
	if err != nil {
		t.Fatal(err)
	}
	b.Reset()
	_ = Encode(&b, fv, "")
	if got, want := b.String(), `{"b":"<x>","a":1}`+"\n"; got != want {
		t.Errorf("FromAny = %s, want %s", got, want)
	}
}

func TestMarshalJSONAndSet(t *testing.T) {
	v, err := Parse([]byte(`{"b":1,"a":1e400,"s":"<x>"}`))
	if err != nil {
		t.Fatal(err)
	}
	v.Set("a", Str("z"))
	v.Set("c", BoolV(true))
	b, err := v.MarshalJSON()
	if err != nil || string(b) != `{"b":1,"a":"z","s":"<x>","c":true}` {
		t.Errorf("MarshalJSON = %s, %v", b, err)
	}
}
