package output

import (
	"bytes"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
)

// Column kinds.
const (
	KindText = "text"
	KindNum  = "num"
	KindDate = "date"
	KindList = "list"
	KindBool = "bool"
)

const (
	gap       = 2  // spaces between columns
	minFlex   = 10 // narrowest the last column is squeezed to before columns are dropped
	autoMax   = 40 // Max of generated columns other than the last
	ansiBold  = "\x1b[1m"
	ansiDim   = "\x1b[2m"
	ansiReset = "\x1b[0m"
	nullText  = "-"
	ellipsis  = "…"
	ellipsisW = 1
)

// tableRenderer renders arrays and search pages as aligned columns, objects as
// KEY VALUE rows, and raw results as indented JSON (they are JSON by contract).
type tableRenderer struct{}

func (tableRenderer) Render(w io.Writer, r *Result, spec *Spec, opts Options) error {
	if r.Bytes != nil || r.Shape == Raw || spec == nil {
		opts.Pretty = true
		return jsonRenderer{}.Render(w, r, spec, opts)
	}
	v := r.Value
	switch r.Shape {
	case Array:
		if v.Kind == jsonx.Array {
			return renderRows(w, v.Items, spec, opts)
		}
	case SearchPage:
		hits, _ := v.Get("hits")
		err := renderRows(w, hits.Items, spec, opts)
		offset, _ := v.Get("offset")
		total, _ := v.Get("total_hits")
		opts.note("%d of %s hits (offset %s)", len(hits.Items), numText(total), numText(offset))
		return err
	case Ack:
		parts := []string{"ok:"}
		for _, k := range []string{"action", "resource", "id"} {
			if m, ok := v.Get(k); ok && m.Kind == jsonx.String {
				parts = append(parts, sanitize(m.Str))
			}
		}
		_, err := io.WriteString(w, strings.Join(parts, " ")+"\n")
		return err
	case Object:
		if v.Kind == jsonx.Object && len(spec.Sections) > 0 {
			return renderSections(w, v, spec, opts)
		}
		if v.Kind == jsonx.Object {
			return renderObject(w, v, spec, opts)
		}
	}
	opts.Pretty = true
	return jsonRenderer{}.Render(w, r, spec, opts)
}

// columns picks the table columns: the --fields paths, else spec.Table, else the
// default fields, else the keys of the first item.
func columns(spec *Spec, opts Options, first jsonx.Value) []Column {
	if len(opts.Fields) > 0 && !isAll(opts.Fields) {
		return pathColumns(opts.Fields, spec)
	}
	if spec.Table != nil {
		return spec.Table
	}
	if spec.DefaultFields != nil {
		return pathColumns(spec.DefaultFields, spec)
	}
	var keys []string
	for _, m := range first.Members {
		keys = append(keys, m.Key)
	}
	return pathColumns(keys, spec)
}

// pathColumns generates columns for paths, borrowing Kind and Max from spec.Table.
func pathColumns(paths []string, spec *Spec) []Column {
	cols := make([]Column, len(paths))
	for i, p := range paths {
		c := Column{Header: headerFor(p), Path: p, Max: autoMax}
		for _, t := range spec.Table {
			if t.Path == p {
				c.Kind, c.Max = t.Kind, t.Max
			}
		}
		if i == len(paths)-1 {
			c.Max = 0
		}
		cols[i] = c
	}
	return cols
}

type cell struct {
	text string
	null bool
}

func renderRows(w io.Writer, items []jsonx.Value, spec *Spec, opts Options) error {
	if len(items) == 0 {
		opts.note("no results")
		return nil
	}
	return writeRows(w, items, columns(spec, opts, items[0]), opts)
}

// renderSections prints each section's array as its own table, separated by blank
// lines. With --fields, a section shows the selected paths under it (all of its
// columns when the section itself is selected) and is left out when none is.
func renderSections(w io.Writer, v jsonx.Value, spec *Spec, opts Options) error {
	first := true
	for _, sec := range spec.Sections {
		cols := sectionColumns(sec, spec, opts)
		if cols == nil {
			continue
		}
		arr, _ := v.Get(sec.Path)
		if len(arr.Items) == 0 {
			opts.note("no %s", strings.ReplaceAll(sec.Path, "_", " "))
			continue
		}
		if !first {
			if _, err := io.WriteString(w, "\n"); err != nil {
				return err
			}
		}
		first = false
		if err := writeRows(w, arr.Items, cols, opts); err != nil {
			return err
		}
	}
	return nil
}

func sectionColumns(sec Section, spec *Spec, opts Options) []Column {
	if len(opts.Fields) == 0 || isAll(opts.Fields) {
		return sec.Columns
	}
	var paths []string
	for _, f := range opts.Fields {
		if f == sec.Path {
			return sec.Columns
		}
		if rest, ok := strings.CutPrefix(f, sec.Path+"."); ok {
			paths = append(paths, rest)
		}
	}
	if len(paths) == 0 {
		return nil
	}
	return pathColumns(paths, &Spec{Table: sec.Columns})
}

// writeRows prints items as a table with the given columns.
func writeRows(w io.Writer, items []jsonx.Value, cols []Column, opts Options) error {
	rows := make([][]cell, len(items))
	for i, it := range items {
		rows[i] = make([]cell, len(cols))
		for j, c := range cols {
			rows[i][j] = cellFor(it, c)
		}
	}
	widths := make([]int, len(cols))
	for j, c := range cols {
		widths[j] = textWidth(c.Header)
		for _, row := range rows {
			widths[j] = max(widths[j], textWidth(row[j].text))
		}
		if c.Max > 0 {
			widths[j] = min(widths[j], c.Max)
		}
	}
	n := fit(widths, opts.Width)
	if hidden := len(cols) - n; hidden > 0 {
		opts.note("%d of %d columns hidden to fit the width of %d; use --fields or -o json", hidden, len(cols), opts.Width)
	}
	cols, widths = cols[:n], widths[:n]

	var b bytes.Buffer
	header := make([]cell, n)
	for j, c := range cols {
		header[j] = cell{text: c.Header}
	}
	writeLine(&b, header, cols, widths, opts.Color, true)
	for _, row := range rows {
		writeLine(&b, row[:n], cols, widths, opts.Color, false)
	}
	_, err := w.Write(b.Bytes())
	return err
}

// fit shrinks the last column to the width, and drops columns from the right while
// even a squeezed last column does not fit. It returns the number of columns kept.
func fit(widths []int, width int) int {
	n := len(widths)
	if width <= 0 {
		return n
	}
	for n > 0 {
		total := gap * (n - 1)
		for _, wd := range widths[:n] {
			total += wd
		}
		if total <= width {
			return n
		}
		last := widths[n-1]
		remaining := width - (total - last)
		if remaining >= min(last, minFlex) || n == 1 {
			widths[n-1] = max(1, remaining)
			return n
		}
		n--
	}
	return n
}

func writeLine(b *bytes.Buffer, cells []cell, cols []Column, widths []int, color, header bool) {
	var line strings.Builder
	for j, c := range cells {
		text := truncate(c.text, widths[j])
		pad := strings.Repeat(" ", widths[j]-textWidth(text))
		if color && c.null {
			text = ansiDim + text + ansiReset
		}
		last := j == len(cells)-1
		switch {
		case cols[j].Kind == KindNum:
			line.WriteString(pad + text)
		case last:
			line.WriteString(text)
		default:
			line.WriteString(text + pad)
		}
		if !last {
			line.WriteString(strings.Repeat(" ", gap))
		}
	}
	s := strings.TrimRight(line.String(), " ")
	if color && header {
		s = ansiBold + s + ansiReset
	}
	b.WriteString(s + "\n")
}

// renderObject prints one KEY VALUE row per selected field.
func renderObject(w io.Writer, v jsonx.Value, spec *Spec, opts Options) error {
	paths := selection(spec, opts)
	if paths == nil {
		for _, m := range v.Members {
			paths = append(paths, m.Key)
		}
	}
	cols := pathColumns(paths, spec)
	keyW := 0
	for _, c := range cols {
		keyW = max(keyW, textWidth(c.Header))
	}
	valW := 0
	if opts.Width > 0 {
		valW = max(minFlex, opts.Width-keyW-gap)
	}
	var b bytes.Buffer
	for _, c := range cols {
		ce := cellFor(v, c)
		key := c.Header + strings.Repeat(" ", keyW-textWidth(c.Header))
		if opts.Color {
			key = ansiBold + key + ansiReset
		}
		text := ce.text
		if valW > 0 {
			text = truncate(text, valW)
		}
		if opts.Color && ce.null {
			text = ansiDim + text + ansiReset
		}
		b.WriteString(strings.TrimRight(key+strings.Repeat(" ", gap)+text, " ") + "\n")
	}
	_, err := w.Write(b.Bytes())
	return err
}

func cellFor(item jsonx.Value, c Column) cell {
	v, ok := item, true
	if c.Path != "" {
		v, ok = lookup(item, c.Path)
	}
	if !ok || v.Kind == jsonx.Null {
		return cell{text: nullText, null: true}
	}
	return cell{text: formatValue(v, c.Kind)}
}

// formatValue formats a cell. Kinds only change numbers (num) and dates (date);
// every kind shows booleans as yes/no, scalar arrays joined by commas and other
// arrays and objects as one-line JSON.
func formatValue(v jsonx.Value, kind string) string {
	switch v.Kind {
	case jsonx.Null:
		return nullText
	case jsonx.Bool:
		if v.Bool {
			return "yes"
		}
		return "no"
	case jsonx.Number:
		if kind == KindNum {
			return abbreviate(v.Num.String())
		}
		return v.Num.String()
	case jsonx.String:
		if kind == KindDate {
			if t, err := time.Parse(time.RFC3339Nano, v.Str); err == nil {
				return t.Format("2006-01-02")
			}
		}
		return sanitize(v.Str)
	case jsonx.Array:
		parts := make([]string, 0, len(v.Items))
		for _, it := range v.Items {
			if it.Kind == jsonx.Array || it.Kind == jsonx.Object {
				return oneLineJSON(v)
			}
			parts = append(parts, formatValue(it, kind))
		}
		return strings.Join(parts, ",")
	}
	return oneLineJSON(v)
}

func oneLineJSON(v jsonx.Value) string {
	var b bytes.Buffer
	_ = jsonx.Encode(&b, v, "")
	return sanitize(strings.TrimSuffix(b.String(), "\n"))
}

// abbreviate shows numbers of 1,000 and more as 1.2K / 3.4M / 5.6B (one decimal,
// truncated).
func abbreviate(num string) string {
	f, err := strconv.ParseFloat(num, 64)
	if err != nil || math.Abs(f) < 1000 {
		return num
	}
	unit, suffix := 1e3, "K"
	switch {
	case math.Abs(f) >= 1e9:
		unit, suffix = 1e9, "B"
	case math.Abs(f) >= 1e6:
		unit, suffix = 1e6, "M"
	}
	return strconv.FormatFloat(math.Trunc(f/unit*10)/10, 'f', 1, 64) + suffix
}

// numText prints a JSON number as is ("?" if it is not one).
func numText(v jsonx.Value) string {
	if v.Kind == jsonx.Number {
		return v.Num.String()
	}
	return "?"
}

// sanitize makes API text safe for one table cell: line breaks and tabs become
// spaces, other control characters (including C1 and so terminal escapes) are removed.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return ' '
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
			return -1
		}
		return r
	}, s)
}

// runeWidth is 2 for East Asian wide characters, 1 otherwise.
func runeWidth(r rune) int {
	switch {
	case r >= 0x1100 && r <= 0x115F, r >= 0x2E80 && r <= 0xA4CF, r >= 0xAC00 && r <= 0xD7A3,
		r >= 0xF900 && r <= 0xFAFF, r >= 0xFE30 && r <= 0xFE4F, r >= 0xFF00 && r <= 0xFF60,
		r >= 0xFFE0 && r <= 0xFFE6:
		return 2
	}
	return 1
}

func textWidth(s string) int {
	n := 0
	for _, r := range s {
		n += runeWidth(r)
	}
	return n
}

// truncate cuts s to at most width cells, ending in "…" when cut.
func truncate(s string, width int) string {
	if textWidth(s) <= width {
		return s
	}
	if width < ellipsisW {
		return ""
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		rw := runeWidth(r)
		if used+rw > width-ellipsisW {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	return b.String() + ellipsis
}
