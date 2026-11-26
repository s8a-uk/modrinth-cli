package output

import (
	"slices"
	"strings"

	"github.com/s8a-uk/modrinth-cli/internal/apperr"
	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
	"github.com/s8a-uk/modrinth-cli/internal/suggest"
)

// maxSuggestions is the number of field names offered for an unknown field.
const maxSuggestions = 3

// CheckFields validates --fields and --full before any request is sent: the flags
// go together, the command supports --fields, and every name is a well-formed
// path. command is the command path ("project get") used in messages.
//
// Names that are not in spec.Fields are checked against the response later
// (ResolveFields), so a field the API added since this version still works. With
// strict (commands that change data, whose result must not be lost to a typo
// after the request) every name must be in spec.Fields now.
func CheckFields(spec *Spec, fields []string, full bool, command string, strict bool) error {
	if len(fields) == 0 {
		return nil
	}
	if full {
		return apperr.New(apperr.Usage, "--fields and --full cannot be used together")
	}
	if spec == nil || spec.Shape == Ack || (spec.Shape == Raw && !spec.AnyFields) {
		return apperr.New(apperr.Usage, "--fields is not supported by this command").
			WithHint("see: mdr schema %s", command)
	}
	if isAll(fields) {
		return nil
	}
	for _, f := range fields {
		if f == "all" {
			return apperr.New(apperr.Usage, "--fields all cannot be combined with other fields")
		}
		if slices.Contains(strings.Split(f, "."), "") {
			return apperr.New(apperr.Usage, "invalid field %q: empty path segment", f).
				WithHint("use dot-separated keys, e.g. license.id")
		}
		if strict && !validField(spec.Fields, f) {
			return unknownField(f, command, spec.Fields, spec.Fields, spec.AnyFields)
		}
	}
	return nil
}

// ResolveFields checks the --fields names that are not in spec.Fields against the
// result v: a name that exists in it (in any element of an array) is kept and
// returned in undocumented, a name that exists in neither is unknown_field with
// suggestions from both lists. An empty array has nothing to check against, so
// every name passes.
func ResolveFields(v jsonx.Value, shape Shape, spec *Spec, fields []string, command string) (undocumented []string, err error) {
	if len(fields) == 0 || isAll(fields) || spec == nil || spec.Shape == Ack || (spec.Shape == Raw && !spec.AnyFields) {
		return nil, nil
	}
	data := v
	if shape == SearchPage && v.Kind == jsonx.Object {
		data, _ = v.Get("hits")
	}
	if data.Kind == jsonx.Array && len(data.Items) == 0 {
		return nil, nil
	}
	for _, f := range fields {
		if validField(spec.Fields, f) {
			continue
		}
		if !hasPath(data, strings.Split(f, ".")) {
			return nil, unknownField(f, command, spec.Fields, slices.Concat(spec.Fields, observedPaths(data, "", 0, nil)), spec.AnyFields)
		}
		undocumented = append(undocumented, f)
	}
	return undocumented, nil
}

// maxListedFields is the most documented fields an unknown_field hint lists when
// nothing is close.
const maxListedFields = 10

// unknownField is the unknown_field error for f: the closest candidates (the
// documented fields and those seen in the response), else the documented fields
// when there are few.
func unknownField(f, command string, documented, candidates []string, anyFields bool) error {
	e := apperr.New(apperr.UnknownField, "unknown field %q for %q", f, command)
	see := "see: mdr schema " + command
	if anyFields {
		see = "see the whole response without --fields"
	}
	if s := suggest.Closest(f, dedupe(candidates), maxSuggestions); len(s) > 0 {
		return e.WithHint("did you mean: %s? %s", strings.Join(s, ", "), see)
	}
	if n := len(documented); n > 0 && n <= maxListedFields {
		return e.WithHint("fields: %s; %s", strings.Join(documented, ", "), see)
	}
	return e.WithHint("%s", see)
}

// hasPath reports whether the dotted path exists in v; through an array it must
// exist in at least one element.
func hasPath(v jsonx.Value, parts []string) bool {
	if len(parts) == 0 {
		return true
	}
	switch v.Kind {
	case jsonx.Object:
		m, ok := v.Get(parts[0])
		return ok && hasPath(m, parts[1:])
	case jsonx.Array:
		return slices.ContainsFunc(v.Items, func(it jsonx.Value) bool { return hasPath(it, parts) })
	}
	return false
}

// Limits of observedPaths, which only feeds suggestions.
const (
	maxObservedDepth = 3
	maxObservedItems = 20
)

// observedPaths lists the dotted key paths in v (through arrays), for suggestions.
func observedPaths(v jsonx.Value, prefix string, depth int, out []string) []string {
	if depth >= maxObservedDepth {
		return out
	}
	switch v.Kind {
	case jsonx.Object:
		for _, m := range v.Members {
			p := prefix + m.Key
			out = append(out, p)
			out = observedPaths(m.Value, p+".", depth+1, out)
		}
	case jsonx.Array:
		for _, it := range v.Items[:min(len(v.Items), maxObservedItems)] {
			out = observedPaths(it, prefix, depth, out)
		}
	}
	return out
}

func dedupe(s []string) []string {
	seen := map[string]bool{}
	out := s[:0:0]
	for _, x := range s {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// validField reports whether p is a field, or the parent of fields (e.g. "license").
func validField(fields []string, p string) bool {
	return slices.ContainsFunc(fields, func(f string) bool { return f == p || strings.HasPrefix(f, p+".") })
}

func isAll(fields []string) bool { return len(fields) == 1 && fields[0] == "all" }

// selection returns the paths to keep, or nil to keep everything.
func selection(spec *Spec, opts Options) []string {
	switch {
	case spec == nil || spec.Shape == Ack || opts.Full || isAll(opts.Fields):
		return nil
	case spec.Shape == Raw:
		if spec.AnyFields && len(opts.Fields) > 0 {
			return opts.Fields
		}
		return nil
	case len(opts.Fields) > 0:
		return opts.Fields
	}
	if opts.Format != Table || spec.DefaultFields == nil {
		return spec.DefaultFields
	}
	// A table also keeps the paths of its columns that are not defaults (file
	// identify shows the file name, while its JSON has only the path).
	paths := spec.DefaultFields
	for _, c := range spec.Table {
		if c.Path != "" && !slices.Contains(paths, c.Path) {
			paths = append(slices.Clip(paths), c.Path)
		}
	}
	return paths
}

// Project returns v reduced to the selected fields (see selection). For a
// search_page only the hits are projected; offset, limit and total_hits stay.
func Project(v jsonx.Value, shape Shape, spec *Spec, opts Options) jsonx.Value {
	paths := selection(spec, opts)
	if paths == nil {
		return v
	}
	if shape == SearchPage && v.Kind == jsonx.Object {
		out := jsonx.Obj()
		for _, m := range v.Members {
			if m.Key == "hits" {
				m.Value = project(m.Value, paths)
			}
			out.Members = append(out.Members, m)
		}
		return out
	}
	return project(v, paths)
}

// project keeps paths in v. Object keys come out in the order their first path
// appears; a missing key becomes null so every element has the same shape.
func project(v jsonx.Value, paths []string) jsonx.Value {
	switch v.Kind {
	case jsonx.Array:
		out := make([]jsonx.Value, len(v.Items))
		for i, it := range v.Items {
			out[i] = project(it, paths)
		}
		return jsonx.Arr(out...)
	case jsonx.Object:
		var keys []string
		whole := map[string]bool{}
		subs := map[string][]string{}
		for _, p := range paths {
			key, rest, nested := strings.Cut(p, ".")
			if !slices.Contains(keys, key) {
				keys = append(keys, key)
			}
			if nested {
				subs[key] = append(subs[key], rest)
			} else {
				whole[key] = true
			}
		}
		out := jsonx.Obj()
		for _, k := range keys {
			val, _ := v.Get(k) // missing = null
			if !whole[k] {
				val = project(val, subs[k])
			}
			out.Members = append(out.Members, jsonx.Member{Key: k, Value: val})
		}
		return out
	}
	return v
}

// lookup follows a dotted path, mapping over arrays on the way (gallery.url on a
// project gives the url of every image). It reports false if nothing was found.
func lookup(v jsonx.Value, path string) (jsonx.Value, bool) {
	return lookupParts(v, strings.Split(path, "."))
}

func lookupParts(v jsonx.Value, parts []string) (jsonx.Value, bool) {
	if len(parts) == 0 {
		return v, true
	}
	switch v.Kind {
	case jsonx.Object:
		m, ok := v.Get(parts[0])
		if !ok {
			return jsonx.Value{}, false
		}
		return lookupParts(m, parts[1:])
	case jsonx.Array:
		var items []jsonx.Value
		for _, it := range v.Items {
			if x, ok := lookupParts(it, parts); ok {
				items = append(items, x)
			}
		}
		return jsonx.Arr(items...), true
	}
	return jsonx.Value{}, false
}

// headerFor derives a column header from a path: "license.id" -> "LICENSE ID".
func headerFor(path string) string {
	return strings.ToUpper(strings.NewReplacer(".", " ", "_", " ").Replace(path))
}
