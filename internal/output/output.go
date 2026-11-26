// Package output decides the output format and renders command results to stdout,
// and writes supplementary messages (notes, warnings) to stderr.
package output

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/s8a-uk/modrinth-cli/internal/apperr"
	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
)

// Format is a resolved output format.
type Format string

// Output formats.
const (
	JSON   Format = "json"
	NDJSON Format = "ndjson"
	Table  Format = "table"
)

// Settings lists the accepted values of --output, in display order.
var Settings = []string{"auto", "json", "ndjson", "table"}

// Shape is the structure of a command's result.
type Shape string

// Result shapes.
const (
	Object     Shape = "object"
	Array      Shape = "array"
	SearchPage Shape = "search_page"
	Ack        Shape = "ack"
	Raw        Shape = "raw"
)

// Column describes one table column.
type Column struct {
	Header string // "DOWNLOADS"
	Path   string // "downloads"; "" = the item itself (arrays of strings)
	Kind   string // "text" | "num" | "date" | "list" | "bool"
	Max    int    // maximum width; 0 = unlimited (last column only)
}

// Spec is the static description of a command's output, kept in the command table.
type Spec struct {
	Shape         Shape
	Fields        []string  // selectable field paths (validates --fields)
	DefaultFields []string  // default selection; nil = all fields
	Table         []Column  // nil = generated from DefaultFields
	Sections      []Section // split the table into several (rare)
	// AnyFields lets --fields select from a raw JSON result, which has no Fields
	// (api): every name is checked against the response itself.
	AnyFields bool
}

// Section renders the array at Path of an object result as its own table. The
// sections are printed one after another, separated by a blank line.
type Section struct {
	Path    string
	Columns []Column // paths relative to Path
}

// Result is what a command returns.
type Result struct {
	Shape Shape
	Value jsonx.Value
	Notes []string // printed to stderr as "note: ..."
	// Format, if set, overrides the resolved output format (schema --all always streams NDJSON).
	Format Format
	// Bytes, if non-nil, is written to stdout as is instead of Value (a non-JSON
	// raw response; empty for a body-less response).
	Bytes []byte
	// Spec, if set, replaces the command's Spec for projection and rendering (a
	// command whose output depends on an argument, such as tag list <kind>).
	Spec *Spec
	// MayHaveWritten marks a result of a request that may have changed data (api
	// with a method other than GET and HEAD): an unknown --fields name is then a
	// warning instead of an error, so the result is not lost.
	MayHaveWritten bool
}

// Options controls rendering.
type Options struct {
	Format Format
	Fields []string // --fields; nil = default; ["all"] = everything
	Full   bool
	Pretty bool // indent json (stdout is a terminal)
	Color  bool // bold headers and dim nulls in tables
	Width  int  // terminal width for tables; 0 = unlimited
	// Notef, if set, receives notes about the rendering (hit counts, hidden columns),
	// which the caller prints to stderr as "note: ...".
	Notef func(format string, args ...any)
}

func (o Options) note(format string, args ...any) {
	if o.Notef != nil {
		o.Notef(format, args...)
	}
}

// Renderer writes a result to w.
type Renderer interface {
	Render(w io.Writer, r *Result, spec *Spec, opts Options) error
}

// RendererFor returns the renderer for f.
func RendererFor(f Format) Renderer {
	switch f {
	case NDJSON:
		return ndjsonRenderer{}
	case Table:
		return tableRenderer{}
	}
	return jsonRenderer{}
}

// ResolveFormat turns an --output setting into a format. "auto" (or empty) is table
// when stdout is a terminal and json otherwise.
func ResolveFormat(setting string, stdoutIsTTY bool) (Format, error) {
	switch setting {
	case "", "auto":
		if stdoutIsTTY {
			return Table, nil
		}
		return JSON, nil
	case "json", "ndjson", "table":
		return Format(setting), nil
	}
	return "", apperr.New(apperr.InvalidInput, "invalid output format %q", setting).
		WithHint("use one of: auto, json, ndjson, table")
}

type jsonRenderer struct{}

func (jsonRenderer) Render(w io.Writer, r *Result, _ *Spec, opts Options) error {
	if r.Bytes != nil {
		_, err := w.Write(r.Bytes)
		return err
	}
	indent := ""
	if opts.Pretty {
		indent = "  "
	}
	return jsonx.Encode(w, r.Value, indent)
}

type ndjsonRenderer struct{}

func (ndjsonRenderer) Render(w io.Writer, r *Result, _ *Spec, opts Options) error {
	if r.Bytes != nil {
		_, err := w.Write(r.Bytes)
		return err
	}
	v := r.Value
	switch r.Shape {
	case Array, Raw:
		if v.Kind == jsonx.Array {
			return encodeLines(w, v.Items)
		}
	case SearchPage:
		hits, _ := v.Get("hits")
		total, _ := v.Get("total_hits")
		opts.note("%d of %s hits", len(hits.Items), numText(total))
		return encodeLines(w, hits.Items)
	}
	return jsonx.Encode(w, v, "")
}

func encodeLines(w io.Writer, items []jsonx.Value) error {
	for _, it := range items {
		if err := jsonx.Encode(w, it, ""); err != nil {
			return err
		}
	}
	return nil
}

// Stderr is the channel for supplementary output on stderr. Write errors are ignored.
type Stderr struct {
	w         io.Writer
	verbosity int
	quiet     bool
}

// NewStderr returns a Stderr writing to w.
func NewStderr(w io.Writer, verbosity int, quiet bool) *Stderr {
	return &Stderr{w: w, verbosity: verbosity, quiet: quiet}
}

// Note prints "note: ..." unless quiet.
func (s *Stderr) Note(format string, args ...any) {
	if s.quiet {
		return
	}
	fmt.Fprintf(s.w, "note: "+format+"\n", args...)
}

// Request logs "> METHOD URL" at -v.
func (s *Stderr) Request(method, url string) {
	if s.verbosity >= 1 {
		fmt.Fprintf(s.w, "> %s %s\n", method, url)
	}
}

// Response logs "< STATUS (ELAPSED) ratelimit-remaining=N" at -v.
func (s *Stderr) Response(status int, elapsed time.Duration, remaining int) {
	if s.verbosity < 1 {
		return
	}
	line := fmt.Sprintf("< %d (%dms)", status, elapsed.Milliseconds())
	if remaining >= 0 {
		line += fmt.Sprintf(" ratelimit-remaining=%d", remaining)
	}
	fmt.Fprintln(s.w, line)
}

// Headers logs headers as "> Name: value" (dir ">" or "<") at -vv. Authorization is
// always shown as ***.
func (s *Stderr) Headers(dir string, h http.Header) {
	if s.verbosity >= 2 {
		writeHeaders(s.w, dir+" ", h)
	}
}

// Include prints the status line and response headers (api --include), regardless of
// verbosity and --quiet.
func (s *Stderr) Include(proto string, status int, h http.Header) {
	fmt.Fprintf(s.w, "%s %d %s\n", proto, status, http.StatusText(status))
	writeHeaders(s.w, "", h)
}

func writeHeaders(w io.Writer, prefix string, h http.Header) {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range h[k] {
			if strings.EqualFold(k, "Authorization") {
				v = "***"
			}
			fmt.Fprintf(w, "%s%s: %s\n", prefix, k, v)
		}
	}
}

// Warn prints "warning: ..." always.
func (s *Stderr) Warn(format string, args ...any) {
	fmt.Fprintf(s.w, "warning: "+format+"\n", args...)
}
