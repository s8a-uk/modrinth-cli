package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildFacets(t *testing.T) {
	cases := []struct {
		name string
		in   FacetFlags
		want string
	}{
		{"none", FacetFlags{}, "null"},
		{"group order follows the flag table", FacetFlags{
			Facets:       []string{"downloads>=10", "categories=a,versions:b"},
			OpenSource:   true,
			Authors:      []string{"me"},
			Licenses:     []string{"MIT", "Apache-2.0"},
			Environments: []string{"client"},
			Categories:   []string{"optimization", "utility"},
			GameVersions: []string{"1.20.1"},
			Loaders:      []string{"fabric", "quilt"},
			Types:        []string{"mod"},
		}, `[["project_type:mod"],["categories:fabric","categories:quilt"],["versions:1.20.1"],` +
			`["categories:optimization"],["categories:utility"],["environment:client"],["license:MIT","license:Apache-2.0"],` +
			`["author:me"],["open_source:true"],["downloads>=10"],["categories:a","versions:b"]]`},
	}
	for _, tc := range cases {
		got, err := BuildFacets(tc.in)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(got)
		if s := strings.ReplaceAll(string(b), `\u003e`, ">"); s != tc.want {
			t.Errorf("%s:\n got %s\nwant %s", tc.name, s, tc.want)
		}
	}
	if _, err := BuildFacets(FacetFlags{Facets: []string{"nope:x"}}); err == nil {
		t.Error("unknown key accepted")
	}
}
