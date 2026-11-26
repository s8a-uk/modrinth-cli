package cli

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// MaxSummary is the maximum length of Command.Summary in characters.
const MaxSummary = 60

// Check validates the command tree and returns every problem found, joined.
func Check(root *Command) error {
	globalNames := map[string]bool{}
	for _, f := range GlobalFlags() {
		globalNames[f.Name] = true
		if f.Short != "" {
			globalNames[f.Short] = true
		}
	}
	var errs []error
	report := func(path []string, format string, args ...any) {
		errs = append(errs, fmt.Errorf("%q: %s", commandLine(path), fmt.Sprintf(format, args...)))
	}
	var walk func(c *Command, path []string)
	walk = func(c *Command, path []string) {
		seen := map[string]bool{}
		for _, s := range c.Sub {
			for _, n := range append([]string{s.Name}, s.Aliases...) {
				if n == "" {
					report(path, "empty command name or alias")
				} else if seen[n] {
					report(path, "duplicate command name or alias %q", n)
				}
				seen[n] = true
			}
		}
		for _, s := range c.Sub {
			p := append(slices.Clip(path), s.Name)
			if s.AliasOf != "" {
				checkAlias(root, s, p, report)
				continue
			}
			checkNode(s, p, globalNames, report)
			walk(s, p)
		}
	}
	if root.Summary == "" {
		report(nil, "empty summary")
	}
	if root.IsLeaf() || len(root.Args) > 0 || len(root.Flags) > 0 {
		report(nil, "the root must not have Run, Args or Flags")
	}
	walk(root, nil)
	return errors.Join(errs...)
}

type reportFunc func(path []string, format string, args ...any)

func checkAlias(root, c *Command, path []string, report reportFunc) {
	if c.Run != nil || c.Offline || c.Local || len(c.Sub) > 0 || len(c.Args) > 0 || len(c.Flags) > 0 || len(c.Aliases) > 0 {
		report(path, "an alias node must have only Name and AliasOf")
	}
	target := Find(root, strings.Fields(c.AliasOf))
	if target == nil || target.AliasOf != "" || target == root {
		report(path, "alias target %q does not exist", c.AliasOf)
	}
}

func checkNode(c *Command, path []string, globalNames map[string]bool, report reportFunc) {
	if c.Summary == "" {
		report(path, "empty summary")
	} else if n := utf8.RuneCountInString(c.Summary); n > MaxSummary {
		report(path, "summary is %d characters (max %d)", n, MaxSummary)
	}
	if !c.IsLeaf() {
		if len(c.Sub) == 0 {
			report(path, "a node without Run must have Sub")
		}
		if len(c.Args) > 0 || len(c.Flags) > 0 {
			report(path, "a non-leaf node must not have Args or Flags")
		}
		return
	}
	if len(c.Sub) > 0 {
		report(path, "a leaf must not have Sub")
	}
	if len(c.Examples) == 0 {
		report(path, "a leaf needs at least one example")
	}
	optional := false
	for i, a := range c.Args {
		if a.Name == "" {
			report(path, "argument %d has no name", i)
		}
		if a.Variadic && i != len(c.Args)-1 {
			report(path, "only the last argument may be variadic (%s)", a.Name)
		}
		if a.Required && optional {
			report(path, "required argument %s follows an optional one", a.Name)
		}
		if !a.Required {
			optional = true
		}
	}
	seen := map[string]bool{}
	for _, f := range c.Flags {
		if globalNames[f.Name] {
			report(path, "flag --%s collides with a global flag", f.Name)
		}
		if seen[f.Name] {
			report(path, "duplicate flag --%s", f.Name)
		}
		seen[f.Name] = true
		if f.Short != "" {
			if utf8.RuneCountInString(f.Short) != 1 {
				report(path, "short flag %q of --%s must be one character", f.Short, f.Name)
			}
			if globalNames[f.Short] || seen[f.Short] {
				report(path, "short flag -%s collides with another flag", f.Short)
			}
			seen[f.Short] = true
		}
		if f.Kind == KindEnum && len(f.Enum) == 0 {
			report(path, "enum flag --%s has no values", f.Name)
		}
	}
	if c.Output == nil {
		report(path, "a leaf needs Output")
	} else {
		for _, d := range c.Output.DefaultFields {
			// A default may be a field or the parent of fields ("dependencies").
			if !slices.ContainsFunc(c.Output.Fields, func(f string) bool { return f == d || strings.HasPrefix(f, d+".") }) {
				report(path, "default field %q is not in Fields", d)
			}
		}
	}
	if c.Mutation != ReadOnly && !slices.ContainsFunc(c.Endpoints, func(e Endpoint) bool { return e.Scope != "" }) {
		report(path, "a mutating command needs an endpoint with a Scope")
	}
}
