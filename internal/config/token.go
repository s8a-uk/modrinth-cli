package config

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/s8a-uk/modrinth-cli/internal/apperr"
	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
	"github.com/s8a-uk/modrinth-cli/internal/validate"
)

// tokenVars are the variables of one target API, in order of precedence.
type tokenVars struct {
	env     string // the token itself
	fileEnv string // the path of a file holding the token
}

var (
	productionVars = tokenVars{env: "MODRINTH_TOKEN", fileEnv: "MODRINTH_TOKEN_FILE"}
	stagingVars    = tokenVars{env: "MODRINTH_STAGING_TOKEN", fileEnv: "MODRINTH_STAGING_TOKEN_FILE"}
)

// TokenVars returns the names of the token variable and the token file variable
// for production or staging.
func TokenVars(staging bool) (env, fileEnv string) {
	v := productionVars
	if staging {
		v = stagingVars
	}
	return v.env, v.fileEnv
}

type resolvedToken struct {
	token    Token
	source   Source
	envVar   string
	warnings []string
}

// maxTokenFile is how much of a token file is read; only the first line is used.
const maxTokenFile = 64 << 10

// resolveToken picks the token for the target API: the variable, then the token
// file variable, then credentials.json. A production token is never used for
// staging and vice versa. Errors and warnings never contain the token or the
// contents of a file.
func resolveToken(env Env, paths Paths, staging bool) (resolvedToken, error) {
	name, fileName := TokenVars(staging)
	var r resolvedToken
	var raw, what string
	switch {
	case strings.TrimSpace(env(name)) != "":
		raw, what = env(name), name
		r.source, r.envVar = SourceEnv, name
	case env(fileName) != "":
		path := env(fileName)
		v, warn, err := readTokenFile(path, fileName)
		if err != nil {
			return r, err
		}
		r.warnings = append(r.warnings, warn...)
		raw, what = v, fileName+" ("+path+")"
		r.source, r.envVar = SourceTokenFile, fileName
	case paths.Credentials != "":
		obj, exists, err := readObject(paths.Credentials, "credentials")
		if err != nil || !exists {
			return r, err
		}
		if w := permWarning(paths.Credentials); w != "" {
			r.warnings = append(r.warnings, w)
		}
		key := credKey(staging)
		v, ok := obj.Get(key)
		switch {
		case !ok || v.IsNull():
			return r, nil
		case v.Kind != jsonx.String:
			r.warnings = append(r.warnings, "ignoring key \""+key+"\" in "+paths.Credentials+": expected a string")
			return r, nil
		}
		raw, what = v.Str, "token in "+paths.Credentials
		r.source = SourceFile
	default:
		return r, nil
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return resolvedToken{}, nil
	}
	if validate.Token(raw) != nil {
		return resolvedToken{}, apperr.New(apperr.InvalidInput, "invalid %s: unexpected characters or length", what).
			WithHint("a token is 20-512 letters, digits or _-.; the value is not shown")
	}
	if !strings.HasPrefix(raw, "mrp_") {
		r.warnings = append(r.warnings, "token does not look like a Modrinth PAT (mrp_...)")
	}
	r.token = NewToken(raw)
	return r, nil
}

// readTokenFile returns the first line of the file at path, trimmed. An empty
// first line is an error (a set variable that yields no token is a mistake).
func readTokenFile(path, varName string) (string, []string, error) {
	f, err := os.Open(path)
	if err != nil {
		e := apperr.New(apperr.IOError, "cannot read %s: %s", varName, path).WithCause(err)
		if errors.Is(err, os.ErrNotExist) {
			e.Message = "file in " + varName + " does not exist: " + path
		}
		return "", nil, e.WithHint("point %s at a file that holds the token on its first line", varName)
	}
	defer f.Close()
	var warnings []string
	if w := permWarning(path); w != "" {
		warnings = append(warnings, w)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxTokenFile))
	if err != nil {
		return "", nil, apperr.New(apperr.IOError, "cannot read %s: %s", varName, path).WithCause(err)
	}
	line, _, _ := bytes.Cut(data, []byte("\n"))
	tok := strings.TrimSpace(string(line))
	if tok == "" {
		return "", nil, apperr.New(apperr.InvalidInput, "the first line of %s is empty: %s", varName, path).
			WithHint("put the token on the first line of the file")
	}
	return tok, warnings, nil
}
