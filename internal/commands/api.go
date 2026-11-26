package commands

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/s8a-uk/modrinth-cli/internal/api"
	"github.com/s8a-uk/modrinth-cli/internal/apperr"
	"github.com/s8a-uk/modrinth-cli/internal/cli"
	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
	"github.com/s8a-uk/modrinth-cli/internal/output"
	"github.com/s8a-uk/modrinth-cli/internal/validate"
)

// apiMethods are the methods api -X accepts (in any case).
var apiMethods = []string{"GET", "POST", "PATCH", "PUT", "DELETE", "HEAD"}

// maxPaginateItems is the most hits api --paginate collects.
const maxPaginateItems = 1000

func apiCmd() *cli.Command {
	return &cli.Command{
		Name:    "api",
		Summary: "Call any API endpoint and print the response",
		Long: "Sends a request to the given path with the same User-Agent, authentication, " +
			"error format and exit codes as the other commands. JSON responses keep the API's key order; " +
			"other bodies are printed as is. Methods other than GET and HEAD may change data on Modrinth: " +
			"mdr does not ask for confirmation, so preview them with --dry-run. -f builds a JSON body, " +
			"-F a multipart body (k=@path for a file), --input sends a file or stdin as is; they cannot be combined. " +
			"--fields selects keys of a JSON response (dotted paths, through arrays), checked against the response itself.",
		Args: []cli.Arg{
			{Name: "path", Required: true, Help: "endpoint path without the version, e.g. /project/sodium", Validate: validate.APIPath},
		},
		Flags: []cli.Flag{
			{Name: "method", Short: "X", Kind: cli.KindString, Value: "<method>",
				Help: "GET, POST, PATCH, PUT, DELETE or HEAD (default GET, or POST with -f, -F or --input)", Validate: apiMethod},
			{Name: "query", Kind: cli.KindStrings, Value: "<key=value>", Help: "add a query parameter (repeatable)", Validate: validate.QueryPair},
			{Name: "field", Short: "f", Kind: cli.KindStrings, Value: "<key=value>", Help: "add a JSON body field; values are read like --set (repeatable)", Validate: fieldPair},
			{Name: "form", Short: "F", Kind: cli.KindStrings, Value: "<key=value|key=@path>", Help: "add a multipart part; @path uploads a file (repeatable)", Validate: formPair},
			{Name: "input", Kind: cli.KindString, Value: "<file|->", Help: "send a file (or stdin) as the body; Content-Type application/json unless -H sets it"},
			{Name: "header", Short: "H", Kind: cli.KindStrings, Value: "<Name: value>", Help: "add a request header (repeatable)", Validate: validate.HeaderLine},
			{Name: "api-version", Kind: cli.KindEnum, Enum: []string{"v2", "v3", "none"}, Default: "v2", Help: "API version prefix of the path; none calls the path from the API root, e.g. /updates/{id}/forge_updates.json"},
			{Name: "include", Short: "i", Kind: cli.KindBool, Help: "print the status line and response headers to stderr"},
			{Name: "paginate", Kind: cli.KindBool, Help: "GET only: fetch every page of a hits/offset/limit/total_hits response (up to 1000 hits) as NDJSON"},
		},
		Output: &output.Spec{Shape: output.Raw, AnyFields: true},
		Examples: []string{
			"mdr api /project/sodium",
			"mdr api /search --query query=sodium --query limit=5",
			"mdr api /search --query query=sodium --paginate",
			"mdr api --api-version v3 /project/sodium",
			"mdr api --api-version none /updates/sodium/forge_updates.json",
			"mdr api /project/sodium/version --fields version_number,date_published",
			"mdr api -X PATCH /project/AANobbMI -f title=\"New title\" --dry-run",
			"mdr api -X POST /version -F data=@version.json -F file0=@build/mod.jar --dry-run",
		},
		Errors: []apperr.Code{apperr.NotFound, apperr.AuthRequired, apperr.BadRequest},
		Run:    runAPI,
	}
}

func apiMethod(s string) error {
	if !slices.Contains(apiMethods, strings.ToUpper(s)) {
		return errors.New("use one of: " + strings.Join(apiMethods, ", "))
	}
	return nil
}

// formPair accepts key=value and key=@path.
func formPair(s string) error {
	k, _, ok := strings.Cut(s, "=")
	if !ok || !reFieldKey.MatchString(k) {
		return errors.New("use key=value or key=@path with a key of letters, digits, _ or -")
	}
	return nil
}

func runAPI(ctx context.Context, inv *cli.Invocation) (*output.Result, error) {
	path := inv.Arg("path")
	query := url.Values{}
	for _, kv := range inv.Strings("query") {
		k, v, _ := strings.Cut(kv, "=")
		query.Add(k, v)
	}
	ver := inv.String("api-version")
	r := api.Request{
		APIVersion:   ver,
		Path:         path,
		Query:        query,
		Resource:     resourcePath(ver, path),
		NotFoundHint: "check the path and the ids in it",
	}
	if err := apiBody(inv, &r); err != nil {
		return nil, err
	}
	if inv.Bool("paginate") {
		if r.Method != http.MethodGet {
			return nil, apperr.New(apperr.Usage, "--paginate works with GET only")
		}
		return paginate(ctx, inv, r)
	}
	resp, err := inv.Client.Do(ctx, r)
	if err != nil {
		return nil, err
	}
	if inv.Bool("include") {
		inv.Log.Include(resp.Proto, resp.Status, resp.Header)
	}
	if resp.IsJSON {
		return &output.Result{Shape: output.Raw, Value: resp.Value, MayHaveWritten: r.MayWrite}, nil
	}
	// A non-nil (possibly empty) Bytes prints the body as is; an empty body prints nothing.
	return &output.Result{Shape: output.Raw, Bytes: append([]byte{}, resp.Body...), MayHaveWritten: r.MayWrite}, nil
}

// apiBody sets the method, headers and body of r from -X, -H, -f, -F and --input.
func apiBody(inv *cli.Invocation, r *api.Request) error {
	fields, forms, input := inv.Strings("field"), inv.Strings("form"), inv.String("input")
	n := 0
	for _, given := range []bool{len(fields) > 0, len(forms) > 0, inv.Changed("input")} {
		if given {
			n++
		}
	}
	if n > 1 {
		return apperr.New(apperr.Usage, "-f, -F and --input cannot be combined")
	}
	method := strings.ToUpper(inv.String("method"))
	if method == "" {
		method = http.MethodGet
		if n > 0 {
			method = http.MethodPost
		}
	}
	if n > 0 && (method == http.MethodGet || method == http.MethodHead) {
		return apperr.New(apperr.Usage, "%s requests take no body (-f, -F, --input)", method)
	}
	r.Method = method
	r.MayWrite = method != http.MethodGet && method != http.MethodHead

	h := http.Header{}
	for _, line := range inv.Strings("header") {
		k, v, _ := strings.Cut(line, ":")
		h.Add(strings.TrimSpace(k), strings.TrimSpace(v))
	}
	contentType := h.Get("Content-Type")
	if contentType != "" && n > 0 && !inv.Changed("input") {
		return apperr.New(apperr.Usage, "-H Content-Type works only with --input (-f sends JSON, -F multipart)")
	}
	if len(h) > 0 {
		r.Header = h
	}

	switch {
	case len(fields) > 0:
		r.JSON = fieldsObject(fields)
	case len(forms) > 0:
		m := &api.Multipart{}
		for _, kv := range forms {
			k, v, _ := strings.Cut(kv, "=")
			p, err := formPart(k, v)
			if err != nil {
				return err
			}
			m.Parts = append(m.Parts, p)
		}
		r.Multipart, r.LongRunning = m, true
	case inv.Changed("input"):
		b, err := readInput(inv, "input", input)
		if err != nil {
			return err
		}
		r.RawBody = b
		if contentType != "" {
			r.ContentType = contentType
			r.Header.Del("Content-Type")
		}
	}
	return nil
}

// formPart is one -F part: a file for key=@path, otherwise a text value (JSON when
// it starts with { or [).
func formPart(key, val string) (api.Part, error) {
	if path, ok := strings.CutPrefix(val, "@"); ok {
		st, err := os.Stat(path)
		switch {
		case err != nil:
			return api.Part{}, apperr.New(apperr.InvalidInput, "cannot read -F %s=@%s: %s", key, path, pathErrText(err)).WithCause(err)
		case !st.Mode().IsRegular():
			return api.Part{}, apperr.New(apperr.InvalidInput, "-F %s=@%s is not a regular file", key, path)
		}
		return api.FilePart(key, path, st.Size()), nil
	}
	p := api.Part{Name: key, Data: []byte(val)}
	if strings.HasPrefix(val, "{") || strings.HasPrefix(val, "[") {
		p.ContentType = "application/json"
	}
	return p, nil
}

// paginate follows offset over the pages of a search-like response and prints the
// hits as NDJSON. Any other response is printed as is, with a note.
func paginate(ctx context.Context, inv *cli.Invocation, r api.Request) (*output.Result, error) {
	var hits []jsonx.Value
	for {
		resp, err := inv.Client.Do(ctx, r)
		if err != nil {
			return nil, err
		}
		if inv.Bool("include") {
			inv.Log.Include(resp.Proto, resp.Status, resp.Header)
		}
		page, offset, total, ok := pageOf(resp)
		if !ok {
			if hits != nil {
				return nil, apperr.New(apperr.UnexpectedResponse, "a later page is not paginated like the first")
			}
			res := &output.Result{Shape: output.Raw, Notes: []string{"response is not paginated"}}
			if resp.IsJSON {
				res.Value = resp.Value
			} else {
				res.Bytes = append([]byte{}, resp.Body...)
			}
			return res, nil
		}
		hits = append(hits, page...)
		next := offset + len(page)
		if len(page) == 0 || next >= total || len(hits) >= maxPaginateItems {
			break
		}
		q := url.Values{}
		for k, v := range r.Query {
			q[k] = v
		}
		q.Set("offset", strconv.Itoa(next))
		r.Query = q
	}
	if len(hits) > maxPaginateItems {
		hits = hits[:maxPaginateItems]
	}
	return &output.Result{Shape: output.Raw, Format: output.NDJSON, Value: jsonx.Arr(hits...)}, nil
}

// pageOf reads {hits, offset, limit, total_hits}.
func pageOf(resp *api.Response) (hits []jsonx.Value, offset, total int, ok bool) {
	if !resp.IsJSON || resp.Value.Kind != jsonx.Object {
		return nil, 0, 0, false
	}
	h, okH := resp.Value.Get("hits")
	o, okO := resp.Value.Get("offset")
	_, okL := resp.Value.Get("limit")
	t, okT := resp.Value.Get("total_hits")
	if !okH || !okO || !okL || !okT || h.Kind != jsonx.Array || o.Kind != jsonx.Number || t.Kind != jsonx.Number {
		return nil, 0, 0, false
	}
	off, err1 := strconv.Atoi(string(o.Num))
	tot, err2 := strconv.Atoi(string(t.Num))
	if err1 != nil || err2 != nil {
		return nil, 0, 0, false
	}
	return h.Items, off, tot, true
}

// resourcePath is the path as the API sees it, used in not-found messages.
func resourcePath(ver, path string) string {
	if ver == "none" {
		return path
	}
	return "/" + ver + path
}
