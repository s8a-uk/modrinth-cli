package cli

import (
	"errors"
	"slices"
	"strings"

	"github.com/s8a-uk/modrinth-cli/internal/apperr"
	textsuggest "github.com/s8a-uk/modrinth-cli/internal/suggest"
	"github.com/s8a-uk/modrinth-cli/internal/validate"
)

// Parse resolves the command, then parses and validates its flags and positional
// arguments. Global flags may appear anywhere. A non-leaf result (including no
// arguments at all) or Globals.Help means "show help"; Globals.Version means
// "print the version".
//
// The grammar is parsed from the command table directly (no flag package):
//
//   - a flag whose name has more than two characters is --name; one- and
//     two-character names and short forms take one dash (-o, -vv). Any other
//     spelling (-output, --o) is an unknown flag, with the right one as the hint.
//   - a flag with a value takes it as --name value or --name=value; the next
//     argument is the value even if it starts with a dash.
//   - a bool flag takes no value: --full=false is a usage error.
//   - short flags are not bundled (-qy is unknown); "--" ends the flags; a lone
//     "-" is a positional argument.
//   - before the leaf command only global flags are accepted.
func Parse(root *Command, args []string) (*Parsed, error) {
	globalDefs := GlobalFlags()
	globals := make([]*value, len(globalDefs))
	for i := range globalDefs {
		globals[i] = newValue(&globalDefs[i])
	}

	// A. Resolve the command. Only global flags are allowed before the leaf.
	cur := root
	var path []string
	rest := args
	noFlags := false // after "--"
	for len(rest) > 0 && !cur.IsLeaf() {
		tok := rest[0]
		if !noFlags && tok == "--" {
			noFlags, rest = true, rest[1:]
			continue
		}
		if !noFlags && isFlag(tok) {
			n, err := parseFlag(rest, newFlagIndex(globals, nil), path)
			if err != nil {
				return nil, err
			}
			rest = rest[n:]
			continue
		}
		child := findChild(cur, tok)
		if child == nil && cur == root && tok == "help" {
			// "help --json" is schema; "help <path>" is "<path> --help".
			rest = rest[1:]
			if len(rest) > 0 && rest[0] == "--json" {
				rest = append([]string{"schema"}, rest[1:]...)
			} else {
				rest = append(slices.Clip(rest), "--help")
			}
			continue
		}
		if child == nil {
			return nil, unknownCommand(cur, path, tok)
		}
		rest = rest[1:]
		if child.AliasOf != "" {
			target := Find(root, strings.Fields(child.AliasOf))
			if target == nil {
				return nil, apperr.New(apperr.Internal, "alias %q points to unknown command %q", tok, child.AliasOf)
			}
			cur, path = target, strings.Fields(child.AliasOf)
		} else {
			cur, path = child, append(path, child.Name)
		}
	}

	flags := map[string]*value{}
	for _, v := range globals {
		flags[v.def.Name] = v
	}
	p := &Parsed{Cmd: cur, Path: path, args: map[string][]string{}, flags: flags}

	var local []*value
	if cur.IsLeaf() {
		// B. Leaf flags and positional arguments, interleaved.
		local = make([]*value, len(cur.Flags))
		for i := range cur.Flags {
			local[i] = newValue(&cur.Flags[i])
			flags[cur.Flags[i].Name] = local[i]
		}
		idx := newFlagIndex(globals, local)
		var positionals []string
		for len(rest) > 0 {
			tok := rest[0]
			switch {
			case noFlags:
				positionals, rest = append(positionals, rest...), nil
			case tok == "--":
				noFlags, rest = true, rest[1:]
			case isFlag(tok):
				n, err := parseFlag(rest, idx, path)
				if err != nil {
					return nil, err
				}
				rest = rest[n:]
			default:
				positionals, rest = append(positionals, tok), rest[1:]
			}
		}
		p.Globals = buildGlobals(flags)
		// C. Assign positional arguments (skipped when only help or the version is wanted).
		if !p.Globals.Help && !p.Globals.Version {
			if err := assignArgs(cur, path, positionals, p.args); err != nil {
				return nil, err
			}
		}
	} else {
		p.Globals = buildGlobals(flags)
	}

	// D. Validate flag values.
	for _, v := range slices.Concat(globals, local) {
		if err := validateFlag(v); err != nil {
			return nil, err
		}
	}
	if p.Globals.Quiet && p.Globals.Verbose > 0 {
		return nil, apperr.New(apperr.Usage, "--quiet cannot be combined with -v or -vv")
	}
	return p, nil
}

// isFlag reports whether an argument is a flag: it starts with a dash and is not
// a lone "-" (stdin).
func isFlag(s string) bool { return len(s) > 1 && s[0] == '-' }

// flagIndex maps every accepted spelling ("--output", "-o", "-vv") to its value,
// and keeps the definitions in order (local first) for suggestions.
type flagIndex struct {
	byName map[string]*value
	defs   []*Flag
}

func newFlagIndex(globals, local []*value) flagIndex {
	idx := flagIndex{byName: map[string]*value{}}
	for _, vs := range [][]*value{local, globals} {
		for _, v := range vs {
			idx.byName[dashName(v.def.Name)] = v
			if v.def.Short != "" {
				idx.byName["-"+v.def.Short] = v
			}
			idx.defs = append(idx.defs, v.def)
		}
	}
	return idx
}

// parseFlag parses the flag at args[0] (and its value) and returns how many
// arguments it used.
func parseFlag(args []string, idx flagIndex, path []string) (int, error) {
	name, val, hasVal := strings.Cut(args[0], "=")
	v := idx.byName[name]
	if v == nil {
		return 0, unknownFlag(name, idx, path)
	}
	f := v.def
	if f.Kind == KindBool {
		if hasVal {
			return 0, apperr.New(apperr.Usage, "flag %s takes no value (got %q)", name, val).
				WithHint("pass %s to turn it on, or leave it out", name)
		}
		v.b, v.set = true, true
		return 1, nil
	}
	n := 1
	if !hasVal {
		if len(args) < 2 {
			return 0, apperr.New(apperr.Usage, "flag %s needs a value", name).
				WithHint("see: %s --help", commandLine(path))
		}
		val, n = args[1], 2
	}
	if err := v.set1(val); err != nil {
		return 0, apperr.New(apperr.InvalidInput, "invalid value %q for %s: %s", val, name, err.Error())
	}
	return n, nil
}

// unknownFlag is the error for a flag spelling that is not accepted. A known name
// with the wrong number of dashes (-output, --o) gets the right spelling as the
// hint; otherwise the closest name, if any.
func unknownFlag(name string, idx flagIndex, path []string) error {
	e := apperr.New(apperr.Usage, "unknown flag %q for %q", name, commandLine(path))
	bare := strings.TrimLeft(name, "-")
	var names []string
	for _, f := range idx.defs {
		if f.Hidden {
			continue
		}
		if f.Name == bare || f.Short == bare {
			right := dashName(f.Name)
			if f.Short == bare {
				right = "-" + f.Short
			}
			return e.WithHint("did you mean %s? (flags longer than two letters take two dashes)", right)
		}
		names = append(names, f.Name)
	}
	if s := suggest(bare, names); s != "" {
		return e.WithHint("did you mean %s?", dashName(s))
	}
	return e.WithHint("see: %s --help", commandLine(path))
}

// Find returns the node at path, following names and aliases, or nil.
func Find(root *Command, path []string) *Command {
	c, _ := resolve(root, path)
	return c
}

func findChild(cur *Command, name string) *Command {
	for _, c := range cur.Sub {
		if c.Name == name || slices.Contains(c.Aliases, name) {
			return c
		}
	}
	return nil
}

func buildGlobals(flags map[string]*value) *Globals {
	g := &Globals{
		Output:  flags["output"].s,
		Fields:  flags["fields"].list,
		Full:    flags["full"].b,
		Staging: flags["staging"].b,
		DryRun:  flags["dry-run"].b,
		Yes:     flags["yes"].b,
		Timeout: flags["timeout"].n,
		MaxWait: flags["max-wait"].n,
		NoRetry: flags["no-retry"].b,
		Quiet:   flags["quiet"].b,
		NoColor: flags["no-color"].b,
		Help:    flags["help"].b,
		Version: flags["version"].b,
		set:     map[string]bool{},
	}
	switch {
	case flags["vv"].b:
		g.Verbose = 2
	case flags["verbose"].b:
		g.Verbose = 1
	}
	for _, f := range GlobalFlags() {
		if flags[f.Name].set {
			g.set[f.Name] = true
		}
	}
	return g
}

func assignArgs(cmd *Command, path []string, positionals []string, out map[string][]string) error {
	i := 0
	for _, a := range cmd.Args {
		if a.Variadic {
			if i >= len(positionals) && a.Required {
				return missingArg(path, a)
			}
			out[a.Name] = positionals[min(i, len(positionals)):]
			i = len(positionals)
			break
		}
		if i >= len(positionals) {
			if a.Required {
				return missingArg(path, a)
			}
			continue
		}
		out[a.Name] = []string{positionals[i]}
		i++
	}
	if i < len(positionals) {
		return apperr.New(apperr.Usage, "unexpected argument %q for %q", positionals[i], commandLine(path)).
			WithHint("see: %s --help", commandLine(path))
	}
	for _, a := range cmd.Args {
		for _, s := range out[a.Name] {
			if err := validate.NoControl(s); err != nil {
				return err
			}
			if a.Validate == nil {
				continue
			}
			if err := a.Validate(s); err != nil {
				var ae *apperr.Error
				if errors.As(err, &ae) {
					return ae
				}
				return apperr.New(apperr.InvalidInput, "invalid %s %q: %s", a.Name, s, err.Error()).WithCause(err)
			}
		}
	}
	return nil
}

func missingArg(path []string, a Arg) error {
	return apperr.New(apperr.Usage, "missing argument <%s> for %q", a.Name, commandLine(path)).
		WithHint("see: %s --help", commandLine(path))
}

func validateFlag(v *value) error {
	if !v.set {
		return nil
	}
	f := v.def
	for _, s := range v.raw() {
		if err := validate.NoControl(s); err != nil {
			return err
		}
		if f.Kind == KindEnum && !slices.Contains(f.Enum, s) {
			return apperr.New(apperr.InvalidInput, "invalid value %q for %s: must be one of %s",
				s, dashName(f.Name), strings.Join(f.Enum, ", "))
		}
		if f.Validate != nil {
			if err := f.Validate(s); err != nil {
				var ae *apperr.Error
				if errors.As(err, &ae) {
					return ae
				}
				return apperr.New(apperr.InvalidInput, "invalid value %q for %s: %s", s, dashName(f.Name), err.Error()).WithCause(err)
			}
		}
	}
	return nil
}

func unknownCommand(cur *Command, path []string, name string) error {
	var names []string
	for _, c := range cur.Sub {
		if !c.Hidden {
			names = append(names, c.Name)
		}
	}
	e := apperr.New(apperr.Usage, "unknown command %q for %q", name, commandLine(path))
	if s := suggest(name, names); s != "" {
		return e.WithHint("did you mean %q? see: %s --help", s, commandLine(path))
	}
	return e.WithHint("see: %s --help", commandLine(path))
}

// suggest returns the closest candidate within suggest.MaxDistance, or "".
func suggest(s string, candidates []string) string {
	if c := textsuggest.Closest(s, candidates, 1); len(c) > 0 {
		return c[0]
	}
	return ""
}
