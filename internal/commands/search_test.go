package commands_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
	"github.com/s8a-uk/modrinth-cli/internal/testutil"
)

// exampleURL is the request of the search example in the command spec.
const exampleURL = "https://api.modrinth.com/v2/search?facets=%5B%5B%22project_type%3Amod%22%5D%2C%5B%22categories%3Afabric%22%2C%22categories%3Aquilt%22%5D%2C%5B%22versions%3A1.20.1%22%5D%2C%5B%22categories%3Aoptimization%22%5D%5D&index=downloads&limit=2&query=sodium"

func TestSearchFacetFlagsDryRun(t *testing.T) {
	r := testutil.Run(t, nil, testutil.RunOpts{}, "search", "sodium", "--type", "mod", "--loader", "fabric", "--loader", "quilt",
		"--game-version", "1.20.1", "--category", "optimization", "--sort", "downloads", "--limit", "2", "--dry-run")
	if r.Code != 0 {
		t.Fatalf("code %d, stderr %q", r.Code, r.Stderr)
	}
	v, err := jsonx.Parse([]byte(r.Stdout))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := v.Lookup("request.url"); got.Str != exampleURL {
		t.Errorf("url\n got %s\nwant %s", got.Str, exampleURL)
	}
}

// hitsJSON returns a search page with n hits numbered from first.
func hitsJSON(first, n, offset, limit, total int) string {
	hits := make([]string, n)
	for i := range hits {
		hits[i] = fmt.Sprintf(`{"project_id":"id%05d","slug":"s%d","title":"T","downloads":%d}`, first+i, first+i, first+i)
	}
	return fmt.Sprintf(`{"hits":[%s],"offset":%d,"limit":%d,"total_hits":%d}`, strings.Join(hits, ","), offset, limit, total)
}

func TestSearchPaging(t *testing.T) {
	cases := []struct {
		name  string
		limit string
		pages func(m *testutil.MockAPI)
		reqs  int
		hits  int
	}{
		{"150 is 100 + 50", "150", func(m *testutil.MockAPI) {
			m.On("GET", "/v2/search").Query("limit", "100").Query("offset", "").JSON(hitsJSON(0, 100, 0, 100, 375))
			m.On("GET", "/v2/search").Query("limit", "50").Query("offset", "100").JSON(hitsJSON(100, 50, 100, 50, 375))
		}, 2, 150},
		{"stops when the hits run out", "150", func(m *testutil.MockAPI) {
			m.On("GET", "/v2/search").Query("limit", "100").Query("offset", "").JSON(hitsJSON(0, 100, 0, 100, 120))
			m.On("GET", "/v2/search").Query("limit", "50").Query("offset", "100").JSON(hitsJSON(100, 20, 100, 50, 120))
		}, 2, 120},
		{"one page when total is reached", "150", func(m *testutil.MockAPI) {
			m.On("GET", "/v2/search").Query("limit", "100").JSON(hitsJSON(0, 30, 0, 100, 30))
		}, 1, 30},
		{"up to 100 is one request", "100", func(m *testutil.MockAPI) {
			m.On("GET", "/v2/search").Query("limit", "100").JSON(hitsJSON(0, 100, 0, 100, 375))
		}, 1, 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := testutil.NewMockAPI(t)
			tc.pages(m)
			r := testutil.Run(t, m, testutil.RunOpts{}, "search", "--limit", tc.limit)
			if r.Code != 0 {
				t.Fatalf("code %d, stderr %q", r.Code, r.Stderr)
			}
			if n := len(m.Requests()); n != tc.reqs {
				t.Errorf("%d requests, want %d", n, tc.reqs)
			}
			v, err := jsonx.Parse([]byte(r.Stdout))
			if err != nil {
				t.Fatal(err)
			}
			hits, _ := v.Get("hits")
			limit, _ := v.Get("limit")
			if len(hits.Items) != tc.hits || limit.Num.String() != tc.limit {
				t.Errorf("%d hits, limit %s; want %d, %s", len(hits.Items), limit.Num, tc.hits, tc.limit)
			}
			for i, h := range hits.Items {
				if id, _ := h.Get("project_id"); id.Str != fmt.Sprintf("id%05d", i) {
					t.Fatalf("hit %d is %s (order or overlap is wrong)", i, id.Str)
				}
			}
		})
	}
}

func TestSearch(t *testing.T) {
	fixture := func(m *testutil.MockAPI) { m.On("GET", "/v2/search").Fixture("search_sodium") }
	cases := []struct {
		name   string
		args   []string
		env    map[string]string
		setup  func(m *testutil.MockAPI)
		query  map[string]string // expected query values of the one request
		tty    bool
		code   int
		stdout string // golden; "" = empty
		stderr string // golden; "" = not compared
	}{
		{name: "default fields", args: []string{"search", "sodium", "--limit", "3"}, setup: fixture,
			query: map[string]string{"query": "sodium", "limit": "3"}, stdout: "search/default.stdout"},
		{name: "table at the default width", args: []string{"search", "sodium", "--limit", "3"}, setup: fixture, tty: true,
			stdout: "search/table.stdout", stderr: "search/table.stderr"},
		{name: "table at 60 columns drops columns", args: []string{"search", "sodium", "--limit", "3"}, setup: fixture, tty: true,
			env: map[string]string{"COLUMNS": "60"}, stdout: "search/table_60.stdout", stderr: "search/table_60.stderr"},
		{name: "table without color", args: []string{"search", "sodium", "--limit", "3"}, setup: fixture, tty: true,
			env: map[string]string{"NO_COLOR": "1", "COLUMNS": "120"}, stdout: "search/table_nocolor.stdout"},
		{name: "table with fields", args: []string{"search", "sodium", "--limit", "3", "-o", "table", "--fields", "slug,downloads,license,categories"},
			setup: fixture, stdout: "search/table_fields.stdout"},
		{name: "ndjson", args: []string{"search", "sodium", "--limit", "3", "-o", "ndjson", "--fields", "slug"}, setup: fixture,
			stdout: "search/ndjson.stdout", stderr: "search/ndjson.stderr"},
		{name: "full keeps every hit field", args: []string{"search", "sodium", "--limit", "3", "--full"}, setup: fixture,
			stdout: "search/full.stdout"},
		{name: "facet expressions, = normalized", args: []string{"search", "--facet", "categories=fabric,categories:quilt", "--facet", "downloads>=1000", "--open-source", "--offset", "20"},
			setup: func(m *testutil.MockAPI) {
				m.On("GET", "/v2/search").JSON(`{"hits":[],"offset":20,"limit":10,"total_hits":0}`)
			},
			query:  map[string]string{"facets": `[["open_source:true"],["categories:fabric","categories:quilt"],["downloads>=1000"]]`, "offset": "20", "limit": "10", "query": ""},
			stdout: "search/empty_offset.stdout"},
		{name: "raw facets are re-encoded", args: []string{"search", "--facets", ` [ ["anything:goes" , "x:y"] ] `},
			setup: func(m *testutil.MockAPI) {
				m.On("GET", "/v2/search").JSON(`{"hits":[],"offset":0,"limit":10,"total_hits":0}`)
			},
			query: map[string]string{"facets": `[["anything:goes","x:y"]]`}, stdout: "search/empty.stdout"},
		{name: "no results on a terminal", args: []string{"search", "zzzz"}, tty: true,
			setup: func(m *testutil.MockAPI) {
				m.On("GET", "/v2/search").JSON(`{"hits":[],"offset":0,"limit":10,"total_hits":0}`)
			},
			stderr: "search/empty_tty.stderr"},
		{name: "facets with a facet flag", args: []string{"search", "--facets", `[["a:b"]]`, "--loader", "fabric"}, code: 2,
			stderr: "search/facets_conflict.stderr"},
		{name: "invalid facet", args: []string{"search", "--facet", "downloads>>5"}, code: 2},
		{name: "unknown facet key", args: []string{"search", "--facet", "foo:bar"}, code: 2},
		{name: "invalid facets json", args: []string{"search", "--facets", `{}`}, code: 2},
		{name: "invalid tag", args: []string{"search", "--loader", "fabric;rm"}, code: 2},
		{name: "invalid sort", args: []string{"search", "--sort", "rating"}, code: 2},
		{name: "limit over 1000", args: []string{"search", "--limit", "1001"}, code: 2},
		{name: "limit 0", args: []string{"search", "--limit", "0"}, code: 2},
		{name: "query too long", args: []string{"search", strings.Repeat("a", 257)}, code: 2},
		{name: "unknown field", args: []string{"search", "--fields", "follows,followers"}, code: 2,
			setup: func(m *testutil.MockAPI) {
				m.On("GET", "/v2/search").JSON(`{"hits":[{"project_id":"AANobbMI","follows":1}],"offset":0,"limit":10,"total_hits":1}`)
			}, stderr: "search/unknown_field.stderr"},
		{name: "bad request from the API", args: []string{"search", "--facets", `[["nope:x"]]`},
			setup: func(m *testutil.MockAPI) {
				m.On("GET", "/v2/search").Status(400).JSON(`{"error":"invalid_input","description":"Error while parsing facets"}`)
			}, code: 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := testutil.NewMockAPI(t)
			if tc.setup != nil {
				tc.setup(m)
			}
			r := testutil.Run(t, m, testutil.RunOpts{StdoutTTY: tc.tty, Env: tc.env}, tc.args...)
			if r.Code != tc.code {
				t.Fatalf("exit code = %d, want %d (stderr %q)", r.Code, tc.code, r.Stderr)
			}
			if tc.stdout == "" {
				if r.Stdout != "" {
					t.Errorf("stdout = %q, want empty", r.Stdout)
				}
			} else {
				testutil.Golden(t, tc.stdout, r.Stdout)
			}
			if tc.stderr != "" {
				testutil.Golden(t, tc.stderr, r.Stderr)
			}
			if tc.setup == nil && len(m.Requests()) != 0 {
				t.Errorf("requests were sent: %v", m.Requests())
			}
			for k, want := range tc.query {
				if got := m.Last().Query.Get(k); got != want {
					t.Errorf("query %s = %q, want %q", k, got, want)
				}
			}
			if tc.code != 0 && !tc.tty && !strings.HasPrefix(lastLine(r.Stderr), `{"error":{`) {
				t.Errorf("last stderr line is not an error object: %q", r.Stderr)
			}
		})
	}
}
