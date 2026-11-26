// Package validate checks untrusted input (arguments, flag values, environment
// variables) before any request is sent. Every regular expression lives here.
// Failures are *apperr.Error with code invalid_input.
package validate

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/s8a-uk/modrinth-cli/internal/apperr"
)

var (
	reControl = regexp.MustCompile(`[\x00-\x1f\x7f]`)
	// Modrinth slugs: letters, digits and ! @ $ ( ) ` . + , " - ' _
	reSlug     = regexp.MustCompile("^[A-Za-z0-9!@$()`.+,\"\\-'_]{1,64}$")
	reAPIPath  = regexp.MustCompile(`^/[A-Za-z0-9._~!$&'()*+,;=:@/-]*$`) // length checked separately (RE2 caps repeats at 1000)
	reQueryKey = regexp.MustCompile(`^[A-Za-z0-9_.\-\[\]]{1,64}$`)
	reToken    = regexp.MustCompile(`^[A-Za-z0-9_\-.]{20,512}$`)
	reBase62ID = regexp.MustCompile(`^[A-Za-z0-9]{8}$`)
	reUser     = regexp.MustCompile(`^[A-Za-z0-9_-]{1,39}$`)
	reSHA1     = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
	reSHA512   = regexp.MustCompile(`^[0-9a-fA-F]{128}$`)
	reTag      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)
	// key, operator (longest first), value. The value excludes the operator characters
	// < > = so that "downloads>>5" is rejected instead of read as downloads > ">5".
	reFacet     = regexp.MustCompile(`^([a-z_]{1,32})(:|!=|>=|<=|>|<|=)([^\x00-\x1f\x7f"\\\[\],<>=]{1,128})$`)
	reHeader    = regexp.MustCompile(`^([A-Za-z0-9-]{1,64}):[ \t]*([^\r\n]*)$`) // value length checked separately (RE2 caps repeats at 1000)
	reUAContact = regexp.MustCompile(`^[A-Za-z0-9@._+:/\-]{3,100}$`)
	reUASuffix  = regexp.MustCompile(`^[A-Za-z0-9._\-/]{1,64}$`)
	// DownloadFilename: forbidden characters and length, and Windows reserved names.
	reFilename = regexp.MustCompile(`^[^/\\<>:"|?*\x00-\x1f\x7f]{1,255}$`)
	reReserved = regexp.MustCompile(`^(?i)(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])$`)
)

// maxShown is the number of characters of an invalid value shown in messages.
const maxShown = 80

// invalid builds the standard error: invalid <what>: "<value>" (<reason>).
func invalid(what, value, reason string) *apperr.Error {
	return apperr.New(apperr.InvalidInput, "invalid %s: %s (%s)", what, show(value), reason)
}

// show quotes v for a message: at most maxShown characters, control characters as \xNN.
func show(v string) string {
	if utf8.RuneCountInString(v) > maxShown {
		v = string([]rune(v)[:maxShown]) + "..."
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range v {
		switch {
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// NoControl rejects control characters and invalid UTF-8. It applies to every
// positional argument and flag value.
func NoControl(s string) error {
	if !utf8.ValidString(s) {
		return invalid("input", strings.ToValidUTF8(s, "\uFFFD"), "not valid UTF-8")
	}
	if reControl.MatchString(s) {
		return invalid("input", s, "contains a control character")
	}
	return nil
}

// ProjectRef accepts a project id or slug.
func ProjectRef(s string) error {
	if s == "." || s == ".." || !reSlug.MatchString(s) {
		return invalid("project id or slug", s, "use 1-64 letters, digits or !@$()`.+,\"-'_").
			WithHint("check the id or slug; try: mdr search <name>")
	}
	return nil
}

// APIPath accepts the <path> of the api command, such as /project/sodium.
func APIPath(s string) error {
	const what = "api path"
	switch {
	case strings.Contains(s, "://"):
		return invalid(what, s, "absolute URLs are not allowed").WithHint("pass only the path, e.g. /project/sodium")
	case !strings.HasPrefix(s, "/"):
		return invalid(what, s, "must start with /").WithHint("pass the path, e.g. /project/sodium")
	case strings.HasPrefix(s, "/v2/") || strings.HasPrefix(s, "/v3/") || s == "/v2" || s == "/v3":
		return invalid(what, s, "must not include the API version").WithHint("use --api-version")
	case strings.Contains(s, "//"):
		return invalid(what, s, "must not contain //")
	case len(s) > 1024 || !reAPIPath.MatchString(s):
		return invalid(what, s, "use 1-1024 URL path characters; put queries in --query")
	}
	for seg := range strings.SplitSeq(s, "/") {
		if seg == "." || seg == ".." {
			return invalid(what, s, "must not contain . or .. segments")
		}
	}
	return nil
}

// QueryPair accepts key=value for --query. The value may be empty.
func QueryPair(s string) error {
	k, v, ok := strings.Cut(s, "=")
	if !ok {
		return invalid("query", s, "use key=value")
	}
	if !reQueryKey.MatchString(k) {
		return invalid("query key", k, "use 1-64 letters, digits or _.-[]")
	}
	return NoControl(v)
}

// Token checks the shape of an API token. The value is never shown.
func Token(s string) error {
	if !reToken.MatchString(s) {
		return apperr.New(apperr.InvalidInput, "invalid token (expected 20-512 letters, digits or _-.)").
			WithHint("check MODRINTH_TOKEN")
	}
	return nil
}

// BaseURL validates MODRINTH_API_URL and returns it normalized to scheme://host[:port]
// (a trailing "/" or "/v2" is removed). Plain http is allowed only for loopback hosts.
func BaseURL(s string) (string, error) {
	const what = "MODRINTH_API_URL"
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return "", invalid(what, s, "not an absolute URL")
	}
	switch {
	case u.User != nil:
		return "", invalid(what, s, "must not contain user information")
	case u.RawQuery != "" || u.ForceQuery || u.Fragment != "":
		return "", invalid(what, s, "must not contain a query or fragment")
	}
	switch strings.TrimSuffix(u.Path, "/") {
	case "", "/v2":
	default:
		return "", invalid(what, s, "must not contain a path other than /v2")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !IsLoopback(u.Hostname()) {
			return "", invalid(what, s, "http is allowed only for 127.0.0.1, ::1 and localhost").WithHint("use https")
		}
	default:
		return "", invalid(what, s, "scheme must be https")
	}
	return u.Scheme + "://" + u.Host, nil
}

// IsLoopback reports whether host is 127.0.0.1, ::1 or localhost.
func IsLoopback(host string) bool {
	return host == "127.0.0.1" || host == "::1" || strings.EqualFold(host, "localhost")
}
