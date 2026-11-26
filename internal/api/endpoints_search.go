package api

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/s8a-uk/modrinth-cli/internal/apperr"
	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
	"github.com/s8a-uk/modrinth-cli/internal/validate"
)

// SearchPageMax is the largest page /search returns.
const SearchPageMax = 100

// SearchParams are the query of one /search request.
type SearchParams struct {
	Query  string
	Facets [][]string // outer AND, inner OR; nil = not sent
	Index  string     // "" = not sent (the API default is relevance)
	Offset int
	Limit  int
}

// SearchPage is a page of search hits.
type SearchPage struct {
	Hits      []jsonx.Value
	Offset    int
	Limit     int
	TotalHits int
}

// Value returns the page as {"hits":[...],"offset":N,"limit":N,"total_hits":N}.
func (p *SearchPage) Value() jsonx.Value {
	return jsonx.Obj(
		jsonx.Member{Key: "hits", Value: jsonx.Arr(p.Hits...)},
		jsonx.Member{Key: "offset", Value: jsonx.Int(int64(p.Offset))},
		jsonx.Member{Key: "limit", Value: jsonx.Int(int64(p.Limit))},
		jsonx.Member{Key: "total_hits", Value: jsonx.Int(int64(p.TotalHits))},
	)
}

// FacetFlags are the facet flags of search, already validated.
type FacetFlags struct {
	Types        []string // --type: one OR group of project_type
	Loaders      []string // --loader: one OR group of categories
	GameVersions []string // --game-version: one OR group of versions
	Categories   []string // --category: one group per value (AND)
	Environments []string // --environment
	Licenses     []string // --license
	Authors      []string // --author
	OpenSource   bool     // --open-source
	Facets       []string // --facet: one group each, expressions joined by commas
}

// BuildFacets turns the facet flags into the facets query value, in a fixed group
// order. It returns nil when there is no group (facets is then not sent).
func BuildFacets(f FacetFlags) ([][]string, error) {
	var groups [][]string
	orGroup := func(key string, vals []string) {
		if len(vals) == 0 {
			return
		}
		g := make([]string, len(vals))
		for i, v := range vals {
			g[i] = key + ":" + v
		}
		groups = append(groups, g)
	}
	orGroup("project_type", f.Types)
	orGroup("categories", f.Loaders)
	orGroup("versions", f.GameVersions)
	for _, c := range f.Categories {
		groups = append(groups, []string{"categories:" + c})
	}
	orGroup("environment", f.Environments)
	orGroup("license", f.Licenses)
	orGroup("author", f.Authors)
	if f.OpenSource {
		groups = append(groups, []string{"open_source:true"})
	}
	for _, raw := range f.Facets {
		var g []string
		for e := range strings.SplitSeq(raw, ",") {
			n, err := validate.ParseFacet(strings.TrimSpace(e))
			if err != nil {
				return nil, err
			}
			g = append(g, n)
		}
		groups = append(groups, g)
	}
	return groups, nil
}

// Search fetches one page of search results (Limit at most SearchPageMax).
func (c *Client) Search(ctx context.Context, p SearchParams) (*SearchPage, error) {
	q := url.Values{"limit": {strconv.Itoa(min(p.Limit, SearchPageMax))}}
	if p.Query != "" {
		q["query"] = []string{p.Query}
	}
	if len(p.Facets) > 0 {
		// Encode without HTML escaping so "downloads>=10" stays readable in -v and --dry-run.
		var b strings.Builder
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(p.Facets); err != nil {
			return nil, apperr.New(apperr.Internal, "cannot encode facets: %s", err.Error())
		}
		q["facets"] = []string{strings.TrimSuffix(b.String(), "\n")}
	}
	if p.Index != "" {
		q["index"] = []string{p.Index}
	}
	if p.Offset > 0 {
		q["offset"] = []string{strconv.Itoa(p.Offset)}
	}
	resp, err := c.Do(ctx, Request{Path: "/search", Query: q})
	if err != nil {
		return nil, err
	}
	v, err := requireJSON(resp)
	if err != nil {
		return nil, err
	}
	hits, ok := v.Get("hits")
	if !ok || hits.Kind != jsonx.Array {
		e := apperr.New(apperr.UnexpectedResponse, "search response has no hits array")
		e.HTTPStatus, e.BodyExcerpt = resp.Status, excerpt(resp.Body)
		return nil, e
	}
	return &SearchPage{
		Hits:      hits.Items,
		Offset:    intMember(v, "offset", p.Offset),
		Limit:     intMember(v, "limit", p.Limit),
		TotalHits: intMember(v, "total_hits", len(hits.Items)),
	}, nil
}

// SearchAll fetches up to limit hits starting at p.Offset, a page of at most
// SearchPageMax at a time. The returned Limit is the requested limit; fewer hits
// than that means there are no more.
func (c *Client) SearchAll(ctx context.Context, p SearchParams, limit int) (*SearchPage, error) {
	out := &SearchPage{Hits: []jsonx.Value{}, Offset: p.Offset, Limit: limit}
	offset := p.Offset
	for len(out.Hits) < limit {
		q := p
		q.Offset, q.Limit = offset, min(SearchPageMax, limit-len(out.Hits))
		page, err := c.Search(ctx, q)
		if err != nil {
			return nil, err
		}
		out.Hits = append(out.Hits, page.Hits...)
		out.TotalHits = page.TotalHits
		if len(page.Hits) == 0 || offset+len(page.Hits) >= page.TotalHits {
			break
		}
		offset += len(page.Hits)
	}
	if len(out.Hits) > limit { // a server that ignores limit
		out.Hits = out.Hits[:limit]
	}
	return out, nil
}

func intMember(v jsonx.Value, key string, def int) int {
	m, ok := v.Get(key)
	if !ok || m.Kind != jsonx.Number {
		return def
	}
	n, err := strconv.Atoi(m.Num.String())
	if err != nil {
		return def
	}
	return n
}
