package api

import (
	"bytes"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/textproto"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/s8a-uk/modrinth-cli/internal/apperr"
	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
)

// FilePartName is the multipart field name of the i-th uploaded file of a version
// ("file0", "file1", ...). Modrinth requires the names in data.file_parts and
// data.primary_file to match the part names; this is the one place that picks them.
func FilePartName(i int) string { return "file" + strconv.Itoa(i) }

// Multipart is a multipart/form-data body. The file parts are streamed from disk
// on every attempt (never held in memory), and the length is known in advance so
// the request carries a Content-Length.
type Multipart struct {
	Parts []Part
	// Progress, if set, receives the bytes sent so far and the total while uploading.
	Progress func(done, total int64)

	boundary string // fixed in tests; random otherwise (chosen on first use)
}

// Part is one form field: a small in-memory value (Data) or a file (Open + Size).
type Part struct {
	Name        string // form field name
	Filename    string // file parts only
	ContentType string
	Data        []byte                        // in-memory value (JSON, text)
	Open        func() (io.ReadCloser, error) // file part; reopened on every attempt
	Size        int64                         // required with Open
}

// FilePart returns a part that uploads the file at path under name.
func FilePart(name, path string, size int64) Part {
	return Part{
		Name:        name,
		Filename:    filepath.Base(path),
		ContentType: ContentTypeOf(path),
		Open:        func() (io.ReadCloser, error) { return os.Open(path) },
		Size:        size,
	}
}

// ContentTypeOf is the Content-Type of an uploaded file, from its extension.
func ContentTypeOf(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jar", ".litemod":
		return "application/java-archive"
	case ".zip":
		return "application/zip"
	case ".mrpack":
		return "application/x-modrinth-modpack+zip"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".bmp":
		return "image/bmp"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg", ".svgz":
		return "image/svg+xml"
	case ".json":
		return "application/json"
	case ".txt", ".md":
		return "text/plain; charset=utf-8"
	}
	return "application/octet-stream"
}

// SetBoundary fixes the boundary (tests).
func (m *Multipart) SetBoundary(b string) { m.boundary = b }

// contentType is the Content-Type header value. It fixes the boundary on first use,
// so every attempt of a request sends the same one.
func (m *Multipart) contentType() string {
	if m.boundary == "" {
		m.boundary = multipart.NewWriter(io.Discard).Boundary()
	}
	return "multipart/form-data; boundary=" + m.boundary
}

var quoteEscaper = strings.NewReplacer("\\", "\\\\", `"`, "\\\"")

func (p Part) header() textproto.MIMEHeader {
	h := textproto.MIMEHeader{}
	cd := fmt.Sprintf(`form-data; name="%s"`, quoteEscaper.Replace(p.Name))
	if p.Filename != "" {
		cd += fmt.Sprintf(`; filename="%s"`, quoteEscaper.Replace(p.Filename))
	}
	h.Set("Content-Disposition", cd)
	if p.ContentType != "" {
		h.Set("Content-Type", p.ContentType)
	}
	return h
}

// build returns the Content-Type header value, the body and its length. Only the
// part headers and in-memory values are buffered; files are opened and chained with
// io.MultiReader. Closing the body closes the files.
func (m *Multipart) build() (contentType string, body io.ReadCloser, length int64, err error) {
	contentType = m.contentType()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.SetBoundary(m.boundary); err != nil {
		return "", nil, 0, apperr.New(apperr.Internal, "invalid multipart boundary: %s", err.Error())
	}
	mb := &multiBody{}
	var segments []io.Reader
	flush := func() {
		seg := bytes.Clone(buf.Bytes())
		buf.Reset()
		segments = append(segments, bytes.NewReader(seg))
		length += int64(len(seg))
	}
	for _, p := range m.Parts {
		if _, err := w.CreatePart(p.header()); err != nil {
			mb.Close()
			return "", nil, 0, apperr.New(apperr.Internal, "cannot build multipart body: %s", err.Error())
		}
		flush()
		if p.Open == nil {
			segments = append(segments, bytes.NewReader(p.Data))
			length += int64(len(p.Data))
			continue
		}
		f, err := p.Open()
		if err != nil {
			mb.Close()
			return "", nil, 0, apperr.New(apperr.IOError, "cannot open %s: %s", p.Filename, pathErr(err)).WithCause(err)
		}
		mb.closers = append(mb.closers, f)
		segments = append(segments, io.LimitReader(f, p.Size))
		length += p.Size
	}
	if err := w.Close(); err != nil {
		mb.Close()
		return "", nil, 0, apperr.New(apperr.Internal, "cannot build multipart body: %s", err.Error())
	}
	flush()
	mb.r = io.MultiReader(segments...)
	if m.Progress != nil {
		mb.r = &progressReader{r: mb.r, total: length, fn: m.Progress}
	}
	return contentType, mb, length, nil
}

// pathErr is the reason of a file error without the path (the caller names the file).
func pathErr(err error) string {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}

// multiBody is the request body of a multipart upload.
type multiBody struct {
	r       io.Reader
	closers []io.Closer
}

func (b *multiBody) Read(p []byte) (int, error) { return b.r.Read(p) }

func (b *multiBody) Close() error {
	for _, c := range b.closers {
		_ = c.Close()
	}
	b.closers = nil
	return nil
}

type progressReader struct {
	r     io.Reader
	done  int64
	total int64
	fn    func(done, total int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.done += int64(n)
		p.fn(p.done, p.total)
	}
	return n, err
}

// planParts describes the parts for a dry run: in-memory JSON values as "json",
// other values as "value", files with their name, type, size and SHA-512.
func (m *Multipart) planParts() (jsonx.Value, error) {
	items := make([]jsonx.Value, 0, len(m.Parts))
	for _, p := range m.Parts {
		mem := []jsonx.Member{{Key: "name", Value: jsonx.Str(p.Name)}}
		if p.Open == nil {
			if v, err := jsonx.Parse(p.Data); err == nil && strings.HasPrefix(p.ContentType, "application/json") {
				mem = append(mem, jsonx.Member{Key: "json", Value: v})
			} else {
				mem = append(mem, jsonx.Member{Key: "value", Value: jsonx.Str(strings.ToValidUTF8(string(p.Data), "�"))})
			}
			items = append(items, jsonx.Obj(mem...))
			continue
		}
		sum, err := sha512Of(p)
		if err != nil {
			return jsonx.Value{}, err
		}
		mem = append(mem,
			jsonx.Member{Key: "filename", Value: jsonx.Str(p.Filename)},
			jsonx.Member{Key: "content_type", Value: jsonx.Str(p.ContentType)},
			jsonx.Member{Key: "size", Value: jsonx.Int(p.Size)},
			jsonx.Member{Key: "sha512", Value: jsonx.Str(sum)},
		)
		items = append(items, jsonx.Obj(mem...))
	}
	return jsonx.Arr(items...), nil
}

func sha512Of(p Part) (string, error) {
	f, err := p.Open()
	if err != nil {
		return "", apperr.New(apperr.IOError, "cannot open %s: %s", p.Filename, pathErr(err)).WithCause(err)
	}
	defer f.Close()
	h := sha512.New()
	if _, err := io.Copy(h, io.LimitReader(f, p.Size)); err != nil {
		return "", apperr.New(apperr.IOError, "cannot read %s: %s", p.Filename, err.Error()).WithCause(err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
