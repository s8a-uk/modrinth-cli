package validate

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/s8a-uk/modrinth-cli/internal/apperr"
)

// FacetKeys are the facet keys --facet accepts. --facets (raw JSON) is not checked
// against them, so keys the API adds later stay usable.
var FacetKeys = []string{
	"project_type", "all_project_types", "categories", "versions", "open_source", "environment", "disclosure_types",
	"client_side", "server_side", "title", "author", "follows", "project_id", "license", "downloads",
	"created_timestamp", "modified_timestamp", "color",
}

// SortIndexes are the values of search --sort.
var SortIndexes = []string{"relevance", "downloads", "follows", "newest", "updated"}

// VersionFileExts are the extensions accepted for version files.
var VersionFileExts = []string{".jar", ".zip", ".mrpack", ".litemod"}

// ImageExts are the extensions accepted for icons and gallery images.
var ImageExts = []string{".png", ".jpg", ".jpeg", ".bmp", ".gif", ".webp", ".svg", ".svgz", ".rgb"}

// forbiddenHeaders may not be set with api -H.
var forbiddenHeaders = []string{"authorization", "user-agent", "host", "content-length", "cookie"}

// VersionID accepts a version id (8-character base62).
func VersionID(s string) error {
	if !reBase62ID.MatchString(s) {
		return invalid("version id", s, "expected 8 letters or digits")
	}
	return nil
}

// VersionNumber accepts the version number of "version get <project> <number>".
func VersionNumber(s string) error {
	if s == "." || s == ".." || !reSlug.MatchString(s) {
		return invalid("version number", s, "use 1-64 letters, digits or !@$()`.+,\"-'_")
	}
	return nil
}

// NewVersionNumber accepts a version number for creating or editing a version.
// The API limits new version numbers to 32 characters; lookups allow up to 64
// because older versions may have longer numbers.
func NewVersionNumber(s string) error {
	if err := VersionNumber(s); err != nil {
		return err
	}
	if len(s) > 32 {
		return invalid("version number", s, "at most 32 characters")
	}
	return nil
}

// UserRef accepts a user id or username.
func UserRef(s string) error {
	if !reUser.MatchString(s) {
		return invalid("user id or username", s, "use 1-39 letters, digits, _ or -")
	}
	return nil
}

// Author accepts a --author value (same rule as UserRef).
func Author(s string) error {
	if !reUser.MatchString(s) {
		return invalid("author", s, "use 1-39 letters, digits, _ or -")
	}
	return nil
}

// TeamID accepts a team id (8-character base62).
func TeamID(s string) error {
	if !reBase62ID.MatchString(s) {
		return invalid("team id", s, "expected 8 letters or digits")
	}
	return nil
}

// Base62ID accepts other ids (notifications, reports, threads).
func Base62ID(s string) error {
	if !reBase62ID.MatchString(s) {
		return invalid("id", s, "expected 8 letters or digits")
	}
	return nil
}

// Hash checks a file hash and returns it in lower case. algorithm is "sha1",
// "sha512", or "" to infer it from the length (40 or 128 hex digits).
func Hash(s, algorithm string) (string, error) {
	if algorithm == "" {
		switch len(s) {
		case 40:
			algorithm = "sha1"
		case 128:
			algorithm = "sha512"
		default:
			return "", invalid("hash", s, "expected 40 (sha1) or 128 (sha512) hex digits")
		}
	}
	switch algorithm {
	case "sha1":
		if !reSHA1.MatchString(s) {
			return "", invalid("sha1 hash", s, "expected 40 hex digits")
		}
	case "sha512":
		if !reSHA512.MatchString(s) {
			return "", invalid("sha512 hash", s, "expected 128 hex digits")
		}
	default:
		return "", invalid("hash algorithm", algorithm, "use sha1 or sha512")
	}
	return strings.ToLower(s), nil
}

// Tag accepts a tag value: --loader, --game-version, --category, --type, --license,
// --environment and --version-type.
func Tag(s string) error {
	if !reTag.MatchString(s) {
		return invalid("tag", s, "use 1-64 letters, digits or ._+-, starting with a letter or digit")
	}
	return nil
}

// ParseFacet checks one facet expression such as "versions:1.20.1" or
// "downloads>=1000" and returns it with "=" normalized to ":".
func ParseFacet(s string) (string, error) {
	m := reFacet.FindStringSubmatch(s)
	if m == nil {
		return "", invalid("facet", s, "use key:value or key OP value with OP one of != >= <= > <").
			WithHint("e.g. --facet versions:1.20.1 or --facet downloads>=1000")
	}
	key, op, val := m[1], m[2], m[3]
	if !slices.Contains(FacetKeys, key) {
		return "", invalid("facet key", key, "unknown key").
			WithHint("known keys: %s; use --facets for other keys", strings.Join(FacetKeys, ", "))
	}
	if op == "=" {
		op = ":"
	}
	return key + op + val, nil
}

// FacetExpr accepts one facet expression (see ParseFacet).
func FacetExpr(s string) error {
	_, err := ParseFacet(s)
	return err
}

// FacetGroup accepts a --facet value: facet expressions joined by commas (OR).
func FacetGroup(s string) error {
	for e := range strings.SplitSeq(s, ",") {
		if err := FacetExpr(strings.TrimSpace(e)); err != nil {
			return err
		}
	}
	return nil
}

// ParseFacetsJSON reads --facets: a JSON array of 1-32 arrays of 1-64 strings, each
// 1-256 characters without control characters.
func ParseFacetsJSON(s string) ([][]string, error) {
	var groups [][]string
	if err := json.Unmarshal([]byte(s), &groups); err != nil {
		return nil, invalid("facets", s, "expected a JSON array of arrays of strings").
			WithHint(`e.g. --facets '[["categories:fabric"],["versions:1.20.1"]]'`)
	}
	if len(groups) < 1 || len(groups) > 32 {
		return nil, invalid("facets", s, "expected 1-32 groups")
	}
	for _, g := range groups {
		if len(g) < 1 || len(g) > 64 {
			return nil, invalid("facets", s, "expected 1-64 expressions per group")
		}
		for _, e := range g {
			if n := utf8.RuneCountInString(e); n < 1 || n > 256 {
				return nil, invalid("facets", s, "each expression must be 1-256 characters")
			}
			if err := NoControl(e); err != nil {
				return nil, err
			}
		}
	}
	return groups, nil
}

// FacetsJSON accepts a --facets value (see ParseFacetsJSON).
func FacetsJSON(s string) error {
	_, err := ParseFacetsJSON(s)
	return err
}

// SortIndex accepts a search --sort value.
func SortIndex(s string) error {
	if !slices.Contains(SortIndexes, s) {
		return invalid("sort", s, "use one of "+strings.Join(SortIndexes, ", "))
	}
	return nil
}

// Limit accepts 0 <= n <= maxN (what 0 means depends on the command).
func Limit(n, maxN int) error {
	if n < 0 || n > maxN {
		return invalid("limit", strconv.Itoa(n), "must be between 0 and "+strconv.Itoa(maxN))
	}
	return nil
}

// HeaderLine accepts an api -H "Name: value" line. Headers mdr sets itself (and
// Cookie) are refused.
func HeaderLine(s string) error {
	m := reHeader.FindStringSubmatch(s)
	if m == nil || len(m[2]) > 1024 {
		return invalid("header", s, "use \"Name: value\" on one line (value at most 1024 bytes)")
	}
	if slices.Contains(forbiddenHeaders, strings.ToLower(m[1])) {
		return invalid("header", s, "mdr sets "+m[1]+" itself")
	}
	return nil
}

// UAContact accepts user_agent_contact (an email address or URL).
func UAContact(s string) error {
	if !reUAContact.MatchString(s) {
		return invalid("user agent contact", s, "use 3-100 letters, digits or @._+:/-")
	}
	return nil
}

// UASuffix accepts MODRINTH_USER_AGENT_SUFFIX.
func UASuffix(s string) error {
	if !reUASuffix.MatchString(s) {
		return invalid("MODRINTH_USER_AGENT_SUFFIX", s, "use 1-64 letters, digits or ._-/")
	}
	return nil
}

// UploadFile accepts a regular, non-empty file whose extension (case-insensitive) is
// in exts (VersionFileExts or ImageExts).
func UploadFile(path string, exts []string) error {
	st, err := os.Stat(path)
	switch {
	case err != nil:
		return invalid("file", path, "cannot be read").WithCause(err)
	case !st.Mode().IsRegular():
		return invalid("file", path, "not a regular file")
	case st.Size() == 0:
		return invalid("file", path, "empty file")
	case !slices.Contains(exts, strings.ToLower(filepath.Ext(path))):
		return invalid("file", path, "extension must be one of "+strings.Join(exts, " "))
	}
	return nil
}

// DownloadURL accepts a download URL returned by the API: https on the Modrinth CDN
// (plus staging-cdn.modrinth.com for staging), no user information. When the API
// itself is on a loopback host (tests), that host is allowed over http as well.
func DownloadURL(s string, staging bool, loopbackHost string) error {
	const what = "download url"
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return invalid(what, s, "not an absolute URL")
	}
	if u.User != nil {
		return invalid(what, s, "must not contain user information")
	}
	host := strings.ToLower(u.Hostname())
	if loopbackHost != "" && IsLoopback(loopbackHost) && host == strings.ToLower(loopbackHost) &&
		(u.Scheme == "http" || u.Scheme == "https") {
		return nil
	}
	if u.Scheme != "https" {
		return invalid(what, s, "scheme must be https")
	}
	allowed := []string{"cdn.modrinth.com"}
	if staging {
		allowed = append(allowed, "staging-cdn.modrinth.com")
	}
	if !slices.Contains(allowed, host) {
		return apperr.New(apperr.InvalidInput, "download url host not allowed: %s", show(host))
	}
	return nil
}

// DownloadFilename accepts a file name returned by the API only if it is safe to
// use as a single path element on every platform. It is never rewritten.
func DownloadFilename(s string) error {
	bad := func(reason string) error {
		return apperr.New(apperr.InvalidInput, "invalid download filename from API: %s (%s)", show(s), reason)
	}
	if err := NoControl(s); err != nil {
		return bad("control character or invalid UTF-8")
	}
	stem, _, _ := strings.Cut(s, ".")
	switch {
	case len(s) < 1 || len(s) > 255 || !reFilename.MatchString(s):
		return bad(`must be 1-255 bytes without / \ < > : " | ? *`)
	case strings.HasPrefix(s, "."):
		return bad("must not start with .")
	case strings.HasSuffix(s, " ") || strings.HasSuffix(s, "."):
		return bad("must not end with a space or .")
	case reReserved.MatchString(stem):
		return bad("reserved name on Windows")
	case filepath.Base(s) != s:
		return bad("not a single path element")
	}
	return nil
}

// Dir accepts an existing directory.
func Dir(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		e := invalid("directory", path, "does not exist").WithCause(err)
		if os.IsNotExist(err) {
			e = e.WithHint("create it first, e.g. mkdir -p %s", path)
		}
		return e
	}
	if !st.IsDir() {
		return invalid("directory", path, "not a directory")
	}
	return nil
}
