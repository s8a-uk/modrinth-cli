package api

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
)

func seqIDs(n, width int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%0*d", width, i)
	}
	return out
}

func TestChunkIDs(t *testing.T) {
	cases := []struct {
		name  string
		ids   []string
		sizes []int
	}{
		{"none", nil, nil},
		{"one", []string{"AANobbMI"}, []int{1}},
		{"exactly 100", seqIDs(100, 8), []int{100}},
		{"101", seqIDs(101, 8), []int{100, 1}},
		{"250", seqIDs(250, 8), []int{100, 100, 50}},
		// "ids=%5B%5D" is 10 bytes and a 64-character slug 70 (%22 twice) plus 3 (%2C), so 4096 bytes hold 56.
		{"long slugs split by bytes", seqIDs(120, 64), []int{56, 56, 8}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			chunks := chunkIDs(tc.ids)
			var sizes []int
			var joined []string
			for _, c := range chunks {
				sizes = append(sizes, len(c))
				joined = append(joined, c...)
				if n := len(idsQuery(c).Encode()); n > maxQueryBytes {
					t.Errorf("chunk of %d encodes to %d bytes", len(c), n)
				}
			}
			if !slices.Equal(sizes, tc.sizes) {
				t.Errorf("sizes = %v, want %v", sizes, tc.sizes)
			}
			if !slices.Equal(joined, tc.ids) {
				t.Errorf("order or content changed")
			}
		})
	}
	// The byte boundary is tight: one more id would not have fit.
	c := chunkIDs(seqIDs(120, 64))[0]
	if n := len(idsQuery(append(c, strings.Repeat("x", 64))).Encode()); n <= maxQueryBytes {
		t.Errorf("a chunk of %d long slugs plus one is %d bytes; the chunk could have been larger", len(c), n)
	}
}

func TestIDsQueryEncoding(t *testing.T) {
	got := idsQuery([]string{"sodium", "AANobbMI"}).Encode()
	want := "ids=" + url.QueryEscape(`["sodium","AANobbMI"]`)
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestReorder(t *testing.T) {
	items := []jsonx.Value{
		mustParse(t, `{"id":"gvQqBUqZ","slug":"lithium"}`),
		mustParse(t, `{"id":"AANobbMI","slug":"sodium"}`),
	}
	res := reorder([]string{"Sodium", "nope", "gvQqBUqZ", "AANobbMI", "aanobbmi"}, items, projectKeys)
	var got []string
	for _, it := range res.Found {
		s, _ := it.Get("slug")
		got = append(got, s.Str)
	}
	if !slices.Equal(got, []string{"sodium", "lithium"}) {
		t.Errorf("found = %v", got)
	}
	// Ids are case-sensitive: "aanobbmi" is not AANobbMI (nor a slug).
	if !slices.Equal(res.Missing, []string{"nope", "aanobbmi"}) || res.Total != 5 {
		t.Errorf("missing = %v, total %d", res.Missing, res.Total)
	}
	if empty := reorder([]string{"a"}, nil, versionKeys); len(empty.Found) != 0 || empty.Found == nil {
		t.Errorf("found must be an empty, non-nil slice: %#v", empty.Found)
	}
}

func TestDedupe(t *testing.T) {
	if got := dedupe([]string{"a", "b", "a", "A"}); !slices.Equal(got, []string{"a", "b", "A"}) {
		t.Errorf("got %v", got)
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
