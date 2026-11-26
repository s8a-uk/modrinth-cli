package api

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/s8a-uk/modrinth-cli/internal/apperr"
	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
)

// Bulk ("ids=[...]") request limits. Measured on 2026-11-26 (production, anonymous):
// /projects answered 300 ids in a 5.1 KB query without complaint, so these are
// conservative limits of our own rather than the API's.
const (
	maxIDsPerRequest = 100
	maxQueryBytes    = 4096 // length of the encoded "ids=[...]"
)

// BulkResult is the outcome of a bulk lookup.
type BulkResult struct {
	Found   []jsonx.Value // in the order of the input; each element at most once
	Missing []string      // inputs nothing matched, in input order
	Total   int           // distinct inputs
}

// matchKey is a response field an input value may refer to.
type matchKey struct {
	field      string
	ignoreCase bool
}

var (
	projectKeys = []matchKey{{"id", false}, {"slug", true}}
	versionKeys = []matchKey{{"id", false}}
	userKeys    = []matchKey{{"id", false}, {"username", true}}
)

// idsQuery encodes ids as the query "ids=[...]".
func idsQuery(ids []string) url.Values {
	b, _ := json.Marshal(ids) // []string never fails
	return url.Values{"ids": {string(b)}}
}

// chunkIDs splits ids so that each chunk has at most maxIDsPerRequest values and
// its encoded "ids=[...]" query is at most maxQueryBytes long. Order is kept.
func chunkIDs(ids []string) [][]string {
	const base = len("ids=%5B%5D")
	comma := len(url.QueryEscape(","))
	var chunks [][]string
	var cur []string
	size := base
	for _, id := range ids {
		b, _ := json.Marshal(id)
		n := len(url.QueryEscape(string(b)))
		if len(cur) > 0 {
			n += comma
		}
		if len(cur) > 0 && (len(cur) == maxIDsPerRequest || size+n > maxQueryBytes) {
			chunks = append(chunks, cur)
			cur, size = nil, base
			n -= comma
		}
		cur = append(cur, id)
		size += n
	}
	if len(cur) > 0 {
		chunks = append(chunks, cur)
	}
	return chunks
}

// dedupe drops repeated values, keeping the first (case-sensitive).
func dedupe(ids []string) []string {
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// getBulk fetches path?ids=[...] in chunks, one after another (so the rate-limit
// preflight applies between them), and orders the elements by the input.
func (c *Client) getBulk(ctx context.Context, path string, ids []string, keys []matchKey) (*BulkResult, error) {
	ids = dedupe(ids)
	var items []jsonx.Value
	for _, chunk := range chunkIDs(ids) {
		resp, err := c.Do(ctx, Request{Path: path, Query: idsQuery(chunk)})
		if err != nil {
			return nil, err
		}
		v, err := requireJSON(resp)
		if err != nil {
			return nil, err
		}
		if v.Kind != jsonx.Array {
			e := apperr.New(apperr.UnexpectedResponse, "the API did not return an array")
			e.HTTPStatus, e.BodyExcerpt = resp.Status, excerpt(resp.Body)
			return nil, e
		}
		items = append(items, v.Items...)
	}
	return reorder(ids, items, keys), nil
}

// reorder matches items to the inputs and returns them in input order.
func reorder(ids []string, items []jsonx.Value, keys []matchKey) *BulkResult {
	exact := map[string]int{}
	folded := map[string]int{}
	for i, it := range items {
		for _, k := range keys {
			v, ok := it.Get(k.field)
			if !ok || v.Kind != jsonx.String {
				continue
			}
			if k.ignoreCase {
				if _, dup := folded[strings.ToLower(v.Str)]; !dup {
					folded[strings.ToLower(v.Str)] = i
				}
			} else if _, dup := exact[v.Str]; !dup {
				exact[v.Str] = i
			}
		}
	}
	res := &BulkResult{Found: []jsonx.Value{}, Total: len(ids)}
	used := map[int]bool{}
	for _, id := range ids {
		i, ok := exact[id]
		if !ok {
			i, ok = folded[strings.ToLower(id)]
		}
		if !ok {
			res.Missing = append(res.Missing, id)
			continue
		}
		if !used[i] {
			used[i] = true
			res.Found = append(res.Found, items[i])
		}
	}
	return res
}
