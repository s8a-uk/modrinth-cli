// Package jsonx is an order-preserving JSON tree: parsing, encoding without HTML
// escaping, and path lookup. Numbers keep their original spelling.
package jsonx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Kind is the type of a JSON value.
type Kind uint8

// JSON value kinds. The zero Kind is Null.
const (
	Null Kind = iota
	Bool
	Number
	String
	Array
	Object
)

// Member is one key/value pair of an object.
type Member struct {
	Key   string
	Value Value
}

// Value is an order-preserving JSON value. The zero value is null.
type Value struct {
	Kind    Kind
	Bool    bool
	Num     json.Number // original spelling
	Str     string
	Items   []Value  // Array
	Members []Member // Object, in order of appearance
}

// MaxDepth is the maximum nesting depth Parse accepts.
const MaxDepth = 512

// Parse reads exactly one JSON value. Anything but whitespace after it is an error.
// Duplicate keys: the last value wins, at the position of the first occurrence.
func Parse(data []byte) (Value, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := parseValue(dec, 0)
	if err != nil {
		return Value{}, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return Value{}, errors.New("jsonx: unexpected data after the top-level value")
	}
	return v, nil
}

func parseValue(dec *json.Decoder, depth int) (Value, error) {
	tok, err := dec.Token()
	if err != nil {
		if err == io.EOF {
			return Value{}, io.ErrUnexpectedEOF
		}
		return Value{}, err
	}
	switch t := tok.(type) {
	case nil:
		return Value{}, nil
	case bool:
		return BoolV(t), nil
	case json.Number:
		return Value{Kind: Number, Num: t}, nil
	case string:
		return Str(t), nil
	case json.Delim:
		if depth >= MaxDepth {
			return Value{}, fmt.Errorf("jsonx: nesting deeper than %d", MaxDepth)
		}
		switch t {
		case '[':
			v := Value{Kind: Array, Items: []Value{}}
			for dec.More() {
				item, err := parseValue(dec, depth+1)
				if err != nil {
					return Value{}, err
				}
				v.Items = append(v.Items, item)
			}
			if _, err := dec.Token(); err != nil { // ']'
				return Value{}, err
			}
			return v, nil
		case '{':
			v := Value{Kind: Object, Members: []Member{}}
			index := map[string]int{}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return Value{}, err
				}
				key, ok := kt.(string)
				if !ok {
					return Value{}, errors.New("jsonx: object key is not a string")
				}
				val, err := parseValue(dec, depth+1)
				if err != nil {
					return Value{}, err
				}
				if i, dup := index[key]; dup {
					v.Members[i].Value = val
					continue
				}
				index[key] = len(v.Members)
				v.Members = append(v.Members, Member{Key: key, Value: val})
			}
			if _, err := dec.Token(); err != nil { // '}'
				return Value{}, err
			}
			return v, nil
		}
	}
	return Value{}, fmt.Errorf("jsonx: unexpected token %v", tok)
}

// Encode writes v followed by one newline. An empty indent writes a single line;
// otherwise each level is indented by indent. <, > and & are not escaped.
func Encode(w io.Writer, v Value, indent string) error {
	var b bytes.Buffer
	encodeValue(&b, v, indent, 0)
	b.WriteByte('\n')
	_, err := w.Write(b.Bytes())
	return err
}

func encodeValue(b *bytes.Buffer, v Value, indent string, depth int) {
	switch v.Kind {
	case Null:
		b.WriteString("null")
	case Bool:
		b.WriteString(strconv.FormatBool(v.Bool))
	case Number:
		if v.Num == "" {
			b.WriteString("0")
		} else {
			b.WriteString(string(v.Num))
		}
	case String:
		encodeString(b, v.Str)
	case Array:
		if len(v.Items) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteByte('[')
		for i, it := range v.Items {
			if i > 0 {
				b.WriteByte(',')
			}
			newline(b, indent, depth+1)
			encodeValue(b, it, indent, depth+1)
		}
		newline(b, indent, depth)
		b.WriteByte(']')
	case Object:
		if len(v.Members) == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteByte('{')
		for i, m := range v.Members {
			if i > 0 {
				b.WriteByte(',')
			}
			newline(b, indent, depth+1)
			encodeString(b, m.Key)
			b.WriteByte(':')
			if indent != "" {
				b.WriteByte(' ')
			}
			encodeValue(b, m.Value, indent, depth+1)
		}
		newline(b, indent, depth)
		b.WriteByte('}')
	}
}

func newline(b *bytes.Buffer, indent string, depth int) {
	if indent == "" {
		return
	}
	b.WriteByte('\n')
	for range depth {
		b.WriteString(indent)
	}
}

func encodeString(b *bytes.Buffer, s string) {
	enc := json.NewEncoder(b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s) // encoding a string cannot fail
	b.Truncate(b.Len() - 1)
}

// Get returns the member named key. It reports false for non-objects and missing keys.
func (v Value) Get(key string) (Value, bool) {
	if v.Kind != Object {
		return Value{}, false
	}
	for _, m := range v.Members {
		if m.Key == key {
			return m.Value, true
		}
	}
	return Value{}, false
}

// Lookup follows a dotted path such as "a.b.c". It reports false if any step is
// missing or is not an object (arrays are not traversed).
func (v Value) Lookup(path string) (Value, bool) {
	cur := v
	for _, k := range strings.Split(path, ".") {
		next, ok := cur.Get(k)
		if !ok {
			return Value{}, false
		}
		cur = next
	}
	return cur, true
}

// StringOr returns the string if v is a string, otherwise def.
func (v Value) StringOr(def string) string {
	if v.Kind == String {
		return v.Str
	}
	return def
}

// IsNull reports whether v is null.
func (v Value) IsNull() bool { return v.Kind == Null }

// Obj builds an object.
func Obj(members ...Member) Value {
	if members == nil {
		members = []Member{}
	}
	return Value{Kind: Object, Members: members}
}

// Arr builds an array.
func Arr(items ...Value) Value {
	if items == nil {
		items = []Value{}
	}
	return Value{Kind: Array, Items: items}
}

// Str builds a string.
func Str(s string) Value { return Value{Kind: String, Str: s} }

// Int builds a number.
func Int(n int64) Value { return Value{Kind: Number, Num: json.Number(strconv.FormatInt(n, 10))} }

// BoolV builds a boolean.
func BoolV(b bool) Value { return Value{Kind: Bool, Bool: b} }

// Strs builds an array of strings.
func Strs(ss []string) Value {
	items := make([]Value, len(ss))
	for i, s := range ss {
		items[i] = Str(s)
	}
	return Arr(items...)
}

// FromAny converts x to a Value by marshaling it with encoding/json and parsing the result.
func FromAny(x any) (Value, error) {
	data, err := json.Marshal(x)
	if err != nil {
		return Value{}, err
	}
	return Parse(data)
}

// Keys walks objects (and arrays of objects) and returns every path that exists,
// such as "a" and "a.b", in order of first appearance.
func Keys(v Value) []string {
	var out []string
	seen := map[string]bool{}
	collectKeys(v, "", &out, seen)
	return out
}

func collectKeys(v Value, prefix string, out *[]string, seen map[string]bool) {
	switch v.Kind {
	case Array:
		for _, it := range v.Items {
			collectKeys(it, prefix, out, seen)
		}
	case Object:
		for _, m := range v.Members {
			p := m.Key
			if prefix != "" {
				p = prefix + "." + m.Key
			}
			if !seen[p] {
				seen[p] = true
				*out = append(*out, p)
			}
			collectKeys(m.Value, p, out, seen)
		}
	}
}

// MarshalJSON lets a Value be sent with encoding/json (e.g. as a request body),
// keeping its key order and number spelling.
func (v Value) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	encodeValue(&b, v, "", 0)
	return b.Bytes(), nil
}

// Set replaces the value of key in an object, or appends the member if the key is
// not there. v must be an object.
func (v *Value) Set(key string, val Value) {
	for i := range v.Members {
		if v.Members[i].Key == key {
			v.Members[i].Value = val
			return
		}
	}
	v.Members = append(v.Members, Member{Key: key, Value: val})
}
