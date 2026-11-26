package api

import (
	"net/http"
	"sort"
	"strings"

	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
)

// DryRunError carries the request that would have been sent. app.Run prints Plan and
// exits 0.
type DryRunError struct {
	Plan jsonx.Value
}

func (e *DryRunError) Error() string { return "dry run: request not sent" }

// NewDryRunError returns the dry run of a body-less request sent outside Client
// (a download). Authorization appears only if h has it.
func NewDryRunError(method, url string, h http.Header) *DryRunError {
	p, _ := plan(Request{Method: method}, url, h, nil, nil) // fails only for multipart
	return &DryRunError{Plan: p}
}

// Values of "token" in the auth member of a plan.
const (
	TokenSet      = "set"      // the token would be sent
	TokenMissing  = "missing"  // no token is configured
	TokenWithheld = "withheld" // a token is configured but not sent (custom API host, or a request that never sends one)
)

// authPlan is the auth member of the plan of r: {"token":...,"required_scope":...}
// for a request that needs a scope or may change data (Request.MayWrite); nil
// otherwise. required_scope is left out when the scope is unknown (api).
func (c *Client) authPlan(r Request, sentToken bool) *jsonx.Value {
	if r.Scope == "" && !r.MayWrite {
		return nil
	}
	token := TokenWithheld
	switch {
	case sentToken:
		token = TokenSet
	case c.opts.MissingToken != nil:
		token = TokenMissing
	}
	auth := jsonx.Obj(jsonx.Member{Key: "token", Value: jsonx.Str(token)})
	if r.Scope != "" {
		auth.Set("required_scope", jsonx.Str(r.Scope))
	}
	return &auth
}

// plan builds {"dry_run":true,"request":{method,url,headers[,json|body_bytes|multipart]}[,"auth":{...}]}.
// Headers are sorted by name; Authorization appears (as ***) only when the token
// would be sent.
func plan(r Request, url string, h http.Header, body []byte, auth *jsonx.Value) (jsonx.Value, error) {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	headers := make([]jsonx.Member, 0, len(keys))
	for _, k := range keys {
		v := strings.Join(h[k], ", ")
		if strings.EqualFold(k, "Authorization") {
			v = "***"
		}
		headers = append(headers, jsonx.Member{Key: k, Value: jsonx.Str(v)})
	}
	req := []jsonx.Member{
		{Key: "method", Value: jsonx.Str(r.Method)},
		{Key: "url", Value: jsonx.Str(url)},
		{Key: "headers", Value: jsonx.Obj(headers...)},
	}
	switch {
	case r.JSON != nil:
		// body is json.Marshal output, so it always parses.
		v, err := jsonx.Parse(body)
		if err == nil {
			req = append(req, jsonx.Member{Key: "json", Value: v})
		}
	case r.RawBody != nil:
		req = append(req, jsonx.Member{Key: "body_bytes", Value: jsonx.Int(int64(len(body)))})
	case r.Multipart != nil:
		parts, err := r.Multipart.planParts()
		if err != nil {
			return jsonx.Value{}, err
		}
		req = append(req, jsonx.Member{Key: "multipart", Value: parts})
	}
	out := jsonx.Obj(
		jsonx.Member{Key: "dry_run", Value: jsonx.BoolV(true)},
		jsonx.Member{Key: "request", Value: jsonx.Obj(req...)},
	)
	if auth != nil {
		out.Set("auth", *auth)
	}
	return out, nil
}
