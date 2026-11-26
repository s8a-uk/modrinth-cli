package config

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/s8a-uk/modrinth-cli/internal/apperr"
	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
)

// maxFile is the largest config or credentials file read.
const maxFile = 1 << 20

// readObject reads a JSON object file. A missing file is an empty object
// (exists = false). Neither the contents nor the parser's message (which can quote
// the contents) are put in errors: credentials.json holds tokens.
func readObject(path, what string) (obj jsonx.Value, exists bool, err error) {
	empty := jsonx.Obj()
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return empty, false, nil
	case err != nil:
		return empty, false, apperr.New(apperr.IOError, "cannot read the %s file: %s", what, path).WithCause(err).
			WithHint("check the permissions of the file")
	case len(data) > maxFile:
		return empty, true, apperr.New(apperr.InvalidInput, "%s file is too large: %s", what, path).WithHint("fix or delete the file")
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return empty, true, nil
	}
	v, perr := jsonx.Parse(data)
	if perr != nil || v.Kind != jsonx.Object {
		return empty, true, apperr.New(apperr.InvalidInput, "%s file is not valid JSON: %s", what, path).WithHint("fix or delete the file")
	}
	return v, true, nil
}

// ReadFile returns the members of config.json (empty if there is none).
func ReadFile(p Paths) (map[string]jsonx.Value, error) {
	out := map[string]jsonx.Value{}
	if p.ConfigFile == "" {
		return out, nil
	}
	obj, _, err := readObject(p.ConfigFile, "config")
	for _, m := range obj.Members {
		out[m.Key] = m.Value
	}
	return out, err
}

// SetKey checks value for key, stores it in config.json and returns the typed
// value. Unknown keys and the order of the file are kept.
func SetKey(p Paths, key, value string) (jsonx.Value, error) {
	v, err := ParseValue(key, value)
	if err != nil {
		return jsonx.Value{}, err
	}
	if err := RequireDir(p); err != nil {
		return jsonx.Value{}, err
	}
	obj, _, err := readObject(p.ConfigFile, "config")
	if err != nil {
		return jsonx.Value{}, err
	}
	return v, writeObject(p.Dir, p.ConfigFile, setMember(obj, key, v), 0o644)
}

// UnsetKey removes key from config.json. A missing file or key is not an error.
func UnsetKey(p Paths, key string) error {
	if _, ok := (&Settings{}).Value(key); !ok {
		return UnknownKey(key)
	}
	if p.ConfigFile == "" {
		return nil
	}
	obj, exists, err := readObject(p.ConfigFile, "config")
	if err != nil || !exists {
		return err
	}
	obj, removed := deleteMember(obj, key)
	if !removed {
		return nil
	}
	return writeObject(p.Dir, p.ConfigFile, obj, 0o644)
}

// credKey is the credentials.json key of the production or staging token.
func credKey(staging bool) string {
	if staging {
		return "staging_token"
	}
	return "token"
}

// SaveToken stores t in credentials.json (0600; the directory is created 0700).
func SaveToken(p Paths, staging bool, t Token) error {
	if err := RequireDir(p); err != nil {
		return err
	}
	obj, _, err := readObject(p.Credentials, "credentials")
	if err != nil {
		return err
	}
	return writeObject(p.Dir, p.Credentials, setMember(obj, credKey(staging), jsonx.Str(t.Reveal())), 0o600)
}

// DeleteToken removes the token from credentials.json, and the file when no key is
// left. existed reports whether there was a token to remove.
func DeleteToken(p Paths, staging bool) (existed bool, err error) {
	if p.Credentials == "" {
		return false, nil
	}
	obj, exists, err := readObject(p.Credentials, "credentials")
	if err != nil || !exists {
		return false, err
	}
	obj, existed = deleteMember(obj, credKey(staging))
	if !existed {
		return false, nil
	}
	if len(obj.Members) == 0 {
		if err := os.Remove(p.Credentials); err != nil {
			return true, apperr.New(apperr.IOError, "cannot remove %s", p.Credentials).WithCause(err)
		}
		return true, nil
	}
	return true, writeObject(p.Dir, p.Credentials, obj, 0o600)
}

func setMember(obj jsonx.Value, key string, v jsonx.Value) jsonx.Value {
	ms := append([]jsonx.Member(nil), obj.Members...)
	for i := range ms {
		if ms[i].Key == key {
			ms[i].Value = v
			return jsonx.Obj(ms...)
		}
	}
	return jsonx.Obj(append(ms, jsonx.Member{Key: key, Value: v})...)
}

func deleteMember(obj jsonx.Value, key string) (jsonx.Value, bool) {
	var ms []jsonx.Member
	found := false
	for _, m := range obj.Members {
		if m.Key == key {
			found = true
			continue
		}
		ms = append(ms, m)
	}
	return jsonx.Obj(ms...), found
}

// writeObject writes obj with a 2-space indent through a temporary file in the same
// directory and a rename, so readers never see a partial file. The temporary file
// is created 0600 by os.CreateTemp, so a token is never readable by others, even
// briefly. On Windows the mode only maps to the read-only attribute; the ACL of
// %AppData% protects the files.
func writeObject(dir, path string, obj jsonx.Value, perm os.FileMode) error {
	fail := func(err error) error {
		return apperr.New(apperr.IOError, "cannot write %s", path).WithCause(err).
			WithHint("check the permissions of %s", dir)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fail(err)
	}
	var buf bytes.Buffer
	if err := jsonx.Encode(&buf, obj, "  "); err != nil {
		return fail(err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fail(err)
	}
	name := tmp.Name()
	defer os.Remove(name) // no-op after a successful rename
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		return fail(err)
	}
	if err := os.Chmod(name, perm); err != nil {
		return fail(err)
	}
	// os.Rename replaces an existing file on Windows too (MoveFileEx with
	// MOVEFILE_REPLACE_EXISTING).
	if err := os.Rename(name, path); err != nil {
		return fail(err)
	}
	return nil
}
