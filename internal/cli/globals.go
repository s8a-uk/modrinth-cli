package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/s8a-uk/modrinth-cli/internal/output"
)

// GlobalFlags returns the global flag definitions shared by parsing, help and schema.
func GlobalFlags() []Flag {
	return []Flag{
		{Name: "output", Short: "o", Kind: KindEnum, Enum: output.Settings, Default: "auto",
			Help: "output format; auto: table on a terminal, json otherwise; do not parse table"},
		{Name: "fields", Kind: KindStrings, Value: "<a,b.c>", Help: "select output fields (comma-separated paths, or all)"},
		{Name: "full", Kind: KindBool, Help: "output all fields (same as --fields all)"},
		{Name: "staging", Kind: KindBool, Help: "use the staging API (staging-api.modrinth.com)"},
		{Name: "dry-run", Kind: KindBool, Help: "print the request that would be sent instead of sending it"},
		{Name: "yes", Short: "y", Kind: KindBool, Help: "skip the confirmation of destructive commands"},
		{Name: "timeout", Kind: KindInt, Default: "30", Value: "<seconds>", Help: "per-request timeout in seconds, 1-3600",
			Validate: intRange(1, 3600)},
		{Name: "max-wait", Kind: KindInt, Default: "60", Value: "<seconds>", Help: "max seconds to wait on rate limits in total, 0-3600",
			Validate: intRange(0, 3600)},
		{Name: "no-retry", Kind: KindBool, Help: "disable automatic retries"},
		{Name: "verbose", Short: "v", Kind: KindBool, Help: "log requests to stderr"},
		{Name: "vv", Kind: KindBool, Help: "log requests and headers to stderr"},
		{Name: "quiet", Short: "q", Kind: KindBool, Help: "suppress notes on stderr (errors are still printed)"},
		{Name: "no-color", Kind: KindBool, Help: "disable colors (also NO_COLOR)"},
		{Name: "help", Short: "h", Kind: KindBool, Help: "show help"},
		{Name: "version", Kind: KindBool, Help: "print the mdr version"},
	}
}

func intRange(lo, hi int) func(string) error {
	return func(s string) error {
		n, err := strconv.Atoi(s)
		if err != nil {
			return errors.New("not an integer")
		}
		if n < lo || n > hi {
			return fmt.Errorf("must be between %d and %d", lo, hi)
		}
		return nil
	}
}

// value holds a parsed flag. It records whether the flag was given.
type value struct {
	def   *Flag
	s     string
	n     int
	b     bool
	list  []string
	set   bool
	split bool // split on commas (--fields)
}

func newValue(f *Flag) *value {
	v := &value{def: f, split: f.Name == "fields"}
	switch f.Kind {
	case KindString, KindEnum:
		v.s = f.Default
	case KindInt:
		v.n, _ = strconv.Atoi(f.Default)
	case KindBool:
		v.b, _ = strconv.ParseBool(f.Default)
	}
	return v
}

// set1 stores one given value of a flag that takes a value (bool flags never do).
func (v *value) set1(s string) error {
	switch v.def.Kind {
	case KindInt:
		n, err := strconv.Atoi(s)
		if err != nil {
			return errors.New("not an integer")
		}
		v.n = n
	case KindStrings:
		if v.split {
			for p := range strings.SplitSeq(s, ",") {
				if p = strings.TrimSpace(p); p != "" {
					v.list = append(v.list, p)
				}
			}
		} else {
			v.list = append(v.list, s)
		}
	default:
		v.s = s
	}
	v.set = true
	return nil
}

// raw returns the given string values for validation.
func (v *value) raw() []string {
	switch v.def.Kind {
	case KindStrings:
		return v.list
	case KindInt:
		return []string{strconv.Itoa(v.n)}
	case KindBool:
		return nil
	}
	return []string{v.s}
}
