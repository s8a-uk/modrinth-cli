package suggest

import (
	"slices"
	"testing"
)

func TestDistance(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"titel", "title", 1}, // transposition
		{"ab", "ba", 1},
		{"", "", 0},
		{"", "abc", 3},
		{"abc", "", 3},
		{"loder", "loader", 1},
		{"kitten", "sitting", 3},
		{"ca", "abc", 3}, // OSA, not unrestricted Damerau (which gives 2)
		{"日本語", "日語本", 1},
		{"same", "same", 0},
	}
	for _, tc := range cases {
		if got := Distance(tc.a, tc.b); got != tc.want {
			t.Errorf("Distance(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestMaxDistance(t *testing.T) {
	for s, want := range map[string]int{"": 1, "id": 1, "slug": 1, "titel": 2, "日本語本": 1, "日本語本語": 2} {
		if got := MaxDistance(s); got != want {
			t.Errorf("MaxDistance(%q) = %d, want %d", s, got, want)
		}
	}
}

func TestClosest(t *testing.T) {
	fields := []string{"id", "slug", "title", "team", "status", "updated", "license.id"}
	cases := []struct {
		in   string
		n    int
		want []string
	}{
		{"titel", 3, []string{"title"}},
		{"tilte", 3, []string{"title"}},
		{"completely_unrelated", 3, nil}, // far away: nothing
		{"sulg", 3, []string{"slug"}},    // short input, transposition (1)
		{"slg", 3, []string{"slug"}},     // short input, distance 1
		{"sx", 3, nil},                   // short input: "id" is 2 away, over the cap of 1
		{"tem", 3, []string{"team"}},     // distance 1 only
		{"statu", 3, []string{"status"}},
		{"ix", 3, []string{"id"}},
		{"tea", 3, []string{"team"}},
	}
	for _, tc := range cases {
		got := Closest(tc.in, fields, tc.n)
		if !slices.Equal(got, tc.want) && !(len(got) == 0 && len(tc.want) == 0) {
			t.Errorf("Closest(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// Sorted by distance, then name; at most n.
	got := Closest("abcde", []string{"abcdx", "abcxx", "abcdf", "abcde", "abcdy"}, 3)
	if want := []string{"abcdf", "abcdx", "abcdy"}; !slices.Equal(got, want) {
		t.Errorf("Closest order = %q, want %q", got, want)
	}
}
