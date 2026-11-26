// Package suggest finds "did you mean" candidates for mistyped command, flag and
// field names.
package suggest

import "sort"

// Distance is the optimal string alignment distance between a and b, counted in
// runes: insertions, deletions, substitutions and transpositions of two adjacent
// runes cost 1 each ("titel" -> "title" is 1).
func Distance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	// d[i][j] is the distance between ra[:i] and rb[:j].
	d := make([][]int, len(ra)+1)
	for i := range d {
		d[i] = make([]int, len(rb)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for i := 1; i <= len(ra); i++ {
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[len(ra)][len(rb)]
}

// MaxDistance is the largest distance at which a candidate is suggested for s:
// 1 for inputs of at most 4 runes, 2 otherwise.
func MaxDistance(s string) int {
	if len([]rune(s)) <= 4 {
		return 1
	}
	return 2
}

// Closest returns up to n candidates within MaxDistance(s), sorted by distance and
// then by name. Duplicates and s itself are not returned.
func Closest(s string, candidates []string, n int) []string {
	type cand struct {
		name string
		d    int
	}
	limit := MaxDistance(s)
	seen := map[string]bool{}
	var cs []cand
	for _, c := range candidates {
		if seen[c] || c == s {
			continue
		}
		seen[c] = true
		if d := Distance(s, c); d <= limit {
			cs = append(cs, cand{c, d})
		}
	}
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].d != cs[j].d {
			return cs[i].d < cs[j].d
		}
		return cs[i].name < cs[j].name
	})
	out := make([]string, 0, min(n, len(cs)))
	for _, c := range cs[:min(n, len(cs))] {
		out = append(out, c.name)
	}
	return out
}
