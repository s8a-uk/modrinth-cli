// Package config resolves settings (flags > environment > config.json > defaults,
// per key) and the API token (environment > token file > credentials.json).
package config

import (
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/s8a-uk/modrinth-cli/internal/apperr"
	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
	"github.com/s8a-uk/modrinth-cli/internal/validate"
)

// Default API base URLs (without /v2).
const (
	ProductionURL = "https://api.modrinth.com"
	StagingURL    = "https://staging-api.modrinth.com"
	StagingHost   = "staging-api.modrinth.com"
)

// Defaults.
const (
	DefaultOutput  = "auto"
	DefaultTimeout = 30 * time.Second
	DefaultMaxWait = 60 * time.Second
)

// Token holds a secret. It never prints itself: String, GoString and MarshalJSON all
// give "***". Only Reveal returns the raw value.
type Token struct{ v string }

// NewToken wraps s.
func NewToken(s string) Token { return Token{v: s} }

// IsZero reports whether there is no token.
func (t Token) IsZero() bool { return t.v == "" }

// Reveal returns the raw value. Only the API client should need it.
func (t Token) Reveal() string { return t.v }

// Masked returns "mrp_…wxyz" (or "***" for values shorter than 8 characters).
func (t Token) Masked() string {
	if len(t.v) < 8 {
		return "***"
	}
	return t.v[:4] + "…" + t.v[len(t.v)-4:]
}

// String returns "***".
func (t Token) String() string { return "***" }

// GoString returns "***".
func (t Token) GoString() string { return "***" }

// MarshalJSON returns "***" as a JSON string.
func (t Token) MarshalJSON() ([]byte, error) { return []byte(`"***"`), nil }

// Source tells where a setting or the token came from.
type Source string

// Setting sources. SourceTokenFile is used for the token only (MODRINTH_TOKEN_FILE
// or MODRINTH_STAGING_TOKEN_FILE); SourceFile means config.json for settings and
// credentials.json for the token.
const (
	SourceFlag      Source = "flag"
	SourceEnv       Source = "env"
	SourceTokenFile Source = "token_file"
	SourceFile      Source = "file"
	SourceDefault   Source = "default"
)

// Keys are the config.json keys, in display order.
var Keys = []string{"output", "staging", "user_agent_contact", "timeout", "max_wait"}

// keyEnv maps each key to its environment variable.
var keyEnv = map[string]string{
	"output":             "MODRINTH_OUTPUT",
	"staging":            "MODRINTH_STAGING",
	"user_agent_contact": "MODRINTH_USER_AGENT_CONTACT",
	"timeout":            "MODRINTH_TIMEOUT",
	"max_wait":           "MODRINTH_MAX_WAIT",
}

// Settings are the resolved settings of one run.
type Settings struct {
	Output                 string // auto|json|ndjson|table
	Staging                bool   // the staging API is in use (flag/env/file, or the base URL host)
	APIURL                 string // resolved base URL, scheme://host[:port] without /v2
	UserAgentContact       string
	UserAgentSuffix        string
	Timeout                time.Duration
	MaxWait                time.Duration
	NoRetry                bool
	Token                  Token
	TokenSource            Source // "env" | "token_file" | "file" | "" (none)
	TokenVar               string // the variable the token came from (MODRINTH_TOKEN, MODRINTH_STAGING_TOKEN_FILE, ...); "" for file or none
	AllowTokenToCustomHost bool
	Sources                map[string]Source // per key of Keys
}

// Value returns the resolved value of a config key as JSON (number of seconds for
// durations). ok is false for unknown keys.
func (s *Settings) Value(key string) (v jsonx.Value, ok bool) {
	switch key {
	case "output":
		return jsonx.Str(s.Output), true
	case "staging":
		return jsonx.BoolV(s.Staging), true
	case "user_agent_contact":
		return jsonx.Str(s.UserAgentContact), true
	case "timeout":
		return jsonx.Int(int64(s.Timeout / time.Second)), true
	case "max_wait":
		return jsonx.Int(int64(s.MaxWait / time.Second)), true
	}
	return jsonx.Value{}, false
}

// set stores a checked value (from ParseValue or the file) for key.
func (s *Settings) set(key string, v jsonx.Value, src Source) {
	switch key {
	case "output":
		s.Output = v.Str
	case "staging":
		s.Staging = v.Bool
	case "user_agent_contact":
		s.UserAgentContact = v.Str
	case "timeout":
		n, _ := v.Num.Int64()
		s.Timeout = time.Duration(n) * time.Second
	case "max_wait":
		n, _ := v.Num.Int64()
		s.MaxWait = time.Duration(n) * time.Second
	}
	s.Sources[key] = src
}

// Flags are the command-line values; nil means not given.
type Flags struct {
	Output  *string
	Staging *bool
	Timeout *int
	MaxWait *int
	NoRetry *bool
}

// Env looks up an environment variable ("" if unset).
type Env func(key string) string

// Load resolves the settings: defaults, then config.json, then environment
// variables, then flags, per key; then the base URL and the token. The returned
// warnings are printed by the caller as "warning: ...". When the config directory
// cannot be determined, Paths is empty and no file is read.
func Load(env Env, flags Flags) (*Settings, Paths, []string, error) {
	s := &Settings{
		Output:  DefaultOutput,
		Timeout: DefaultTimeout,
		MaxWait: DefaultMaxWait,
		Sources: map[string]Source{},
	}
	for _, k := range Keys {
		s.Sources[k] = SourceDefault
	}
	var warnings []string
	paths, _ := ResolvePaths(env)

	// config.json.
	if paths.ConfigFile != "" {
		obj, _, err := readObject(paths.ConfigFile, "config")
		if err != nil {
			return nil, paths, nil, err
		}
		for _, m := range obj.Members {
			v, err := fileValue(m.Key, m.Value)
			if err != nil {
				warnings = append(warnings, err.Error()+" in "+paths.ConfigFile)
				continue
			}
			s.set(m.Key, v, SourceFile)
		}
	}

	// Environment variables.
	for _, k := range Keys {
		raw := env(keyEnv[k])
		if raw == "" {
			continue
		}
		v, err := ParseValue(k, raw)
		if err != nil {
			e := apperr.From(err)
			return nil, paths, nil, apperr.New(apperr.InvalidInput, "invalid %s: %q", keyEnv[k], raw).WithHint("%s", e.Hint)
		}
		s.set(k, v, SourceEnv)
	}
	if raw := env("MODRINTH_USER_AGENT_SUFFIX"); raw != "" {
		if err := validate.UASuffix(raw); err != nil {
			return nil, paths, nil, err
		}
		s.UserAgentSuffix = raw
	}
	s.AllowTokenToCustomHost = env("MODRINTH_ALLOW_TOKEN_TO_CUSTOM_HOST") == "1"

	// Flags (already validated by the parser).
	if flags.Output != nil {
		s.set("output", jsonx.Str(*flags.Output), SourceFlag)
	}
	if flags.Staging != nil {
		s.set("staging", jsonx.BoolV(*flags.Staging), SourceFlag)
	}
	if flags.Timeout != nil {
		s.set("timeout", jsonx.Int(int64(*flags.Timeout)), SourceFlag)
	}
	if flags.MaxWait != nil {
		s.set("max_wait", jsonx.Int(int64(*flags.MaxWait)), SourceFlag)
	}
	if flags.NoRetry != nil {
		s.NoRetry = *flags.NoRetry
	}

	// Base URL.
	switch raw := env("MODRINTH_API_URL"); {
	case raw != "":
		u, err := validate.BaseURL(raw)
		if err != nil {
			return nil, paths, nil, err
		}
		s.APIURL = u
		if pu, _ := url.Parse(u); pu != nil && strings.EqualFold(pu.Hostname(), StagingHost) && !s.Staging {
			s.Staging, s.Sources["staging"] = true, SourceEnv
		}
	case s.Staging:
		s.APIURL = StagingURL
	default:
		s.APIURL = ProductionURL
	}

	tok, err := resolveToken(env, paths, s.Staging)
	if err != nil {
		return nil, paths, nil, err
	}
	s.Token, s.TokenSource, s.TokenVar = tok.token, tok.source, tok.envVar
	warnings = append(warnings, tok.warnings...)
	return s, paths, warnings, nil
}

// ParseValue checks a value given as text (environment variable or config set) for
// key and returns it typed. Unknown keys are a usage error.
func ParseValue(key, raw string) (jsonx.Value, error) {
	switch key {
	case "output":
		if !slices.Contains(outputValues, raw) {
			return jsonx.Value{}, apperr.New(apperr.InvalidInput, "invalid output: %q", raw).
				WithHint("use one of: %s", strings.Join(outputValues, ", "))
		}
		return jsonx.Str(raw), nil
	case "staging":
		b, err := parseBool(raw)
		if err != nil {
			return jsonx.Value{}, apperr.New(apperr.InvalidInput, "invalid staging: %q", raw).WithHint("use true or false (or 1 or 0)")
		}
		return jsonx.BoolV(b), nil
	case "user_agent_contact":
		if validate.UAContact(raw) != nil {
			return jsonx.Value{}, apperr.New(apperr.InvalidInput, "invalid user_agent_contact: %q", raw).
				WithHint("use an email address or URL: 3-100 letters, digits or @._+:/-")
		}
		return jsonx.Str(raw), nil
	case "timeout", "max_wait":
		lo, hi := keyRange(key)
		n, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || n < lo || n > hi {
			return jsonx.Value{}, apperr.New(apperr.InvalidInput, "invalid %s: %q", key, raw).
				WithHint("use an integer between %d and %d (seconds)", lo, hi)
		}
		return jsonx.Int(int64(n)), nil
	}
	return jsonx.Value{}, UnknownKey(key)
}

// UnknownKey is the usage error for a key that is not in Keys.
func UnknownKey(key string) error {
	e := apperr.New(apperr.Usage, "unknown config key %q", key)
	if strings.Contains(key, "token") {
		return e.WithHint("tokens are not config keys; use: mdr auth login --with-token")
	}
	return e.WithHint("keys: %s", strings.Join(Keys, ", "))
}

// outputValues are the accepted values of output (output.Settings, without the import).
var outputValues = []string{"auto", "json", "ndjson", "table"}

func keyRange(key string) (int, int) {
	if key == "timeout" {
		return 1, 3600
	}
	return 0, 3600
}

// fileValue checks a config.json value. The error text is a warning without the path.
func fileValue(key string, v jsonx.Value) (jsonx.Value, error) {
	bad := func(want string) error {
		return fmt.Errorf("ignoring key %q: expected %s", key, want)
	}
	switch key {
	case "output", "user_agent_contact":
		if v.Kind != jsonx.String {
			return jsonx.Value{}, bad("a string")
		}
		if key == "user_agent_contact" && v.Str == "" {
			return v, nil // "" = the default contact
		}
		if _, err := ParseValue(key, v.Str); err != nil {
			return jsonx.Value{}, fmt.Errorf("ignoring key %q: invalid value %q", key, v.Str)
		}
		return v, nil
	case "staging":
		if v.Kind != jsonx.Bool {
			return jsonx.Value{}, bad("true or false")
		}
		return v, nil
	case "timeout", "max_wait":
		lo, hi := keyRange(key)
		n, err := v.Num.Int64()
		if v.Kind != jsonx.Number || err != nil || n < int64(lo) || n > int64(hi) {
			return jsonx.Value{}, bad(fmt.Sprintf("an integer between %d and %d", lo, hi))
		}
		return jsonx.Int(n), nil
	}
	return jsonx.Value{}, fmt.Errorf("ignoring unknown key %q", key)
}

func parseBool(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true":
		return true, nil
	case "0", "false", "":
		return false, nil
	}
	return false, apperr.New(apperr.InvalidInput, "not a boolean")
}
