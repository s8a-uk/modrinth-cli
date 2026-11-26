package api

import (
	"bytes"
	"encoding/json"

	"github.com/s8a-uk/modrinth-cli/internal/apperr"
	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
)

// VersionFile is a file of a version, as far as mdr reads it.
type VersionFile struct {
	Hashes struct {
		SHA1   string `json:"sha1"`
		SHA512 string `json:"sha512"`
	} `json:"hashes"`
	URL      string `json:"url"`
	Filename string `json:"filename"`
	Primary  bool   `json:"primary"`
	Size     int64  `json:"size"`
}

// Version is a version, as far as mdr reads it (unknown fields are ignored).
type Version struct {
	ID            string        `json:"id"`
	ProjectID     string        `json:"project_id"`
	Name          string        `json:"name"`
	VersionNumber string        `json:"version_number"`
	VersionType   string        `json:"version_type"`
	Loaders       []string      `json:"loaders"`
	GameVersions  []string      `json:"game_versions"`
	DatePublished string        `json:"date_published"`
	Files         []VersionFile `json:"files"`
}

// PrimaryFile returns the file marked primary, else the first; false if there are none.
func (v Version) PrimaryFile() (VersionFile, bool) {
	for _, f := range v.Files {
		if f.Primary {
			return f, true
		}
	}
	if len(v.Files) > 0 {
		return v.Files[0], true
	}
	return VersionFile{}, false
}

// DecodeVersion reads a version from its JSON value. Failure is unexpected_response.
func DecodeVersion(v jsonx.Value) (Version, error) {
	var out Version
	var buf bytes.Buffer
	if err := jsonx.Encode(&buf, v, ""); err != nil {
		return out, apperr.New(apperr.UnexpectedResponse, "cannot read the version: %s", err.Error()).WithCause(err)
	}
	if v.Kind != jsonx.Object || json.Unmarshal(buf.Bytes(), &out) != nil {
		return out, apperr.New(apperr.UnexpectedResponse, "the API did not return a version object")
	}
	return out, nil
}
