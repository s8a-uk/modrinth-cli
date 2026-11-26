package cli

import (
	"strings"

	"github.com/s8a-uk/modrinth-cli/internal/apperr"
	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
)

// SchemaVersion is bumped only when an existing key changes meaning or type, a field
// is removed from a default set, or an exit code changes meaning.
const SchemaVersion = 1

// API base URLs shown in the schema.
const (
	BaseURL    = "https://api.modrinth.com/v2"
	StagingURL = "https://staging-api.modrinth.com/v2"
)

// exitCodes is the exit code table shown in the schema, in order.
var exitCodes = []struct{ code, meaning string }{
	{"0", "success"}, {"1", "internal"}, {"2", "usage"}, {"3", "not_found"}, {"4", "auth"},
	{"5", "api_rejected"}, {"6", "rate_limited"}, {"7", "network"}, {"8", "server_error"},
	{"9", "hash_mismatch"}, {"130", "interrupted"},
}

func m(key string, v jsonx.Value) jsonx.Member { return jsonx.Member{Key: key, Value: v} }

// SchemaTree returns the compact description of the whole CLI (mdr schema).
func SchemaTree(root *Command, version string) jsonx.Value {
	var flags []jsonx.Value
	for _, f := range GlobalFlags() {
		if !f.Hidden {
			flags = append(flags, flagSchema(f))
		}
	}
	var codes []jsonx.Member
	for _, e := range exitCodes {
		codes = append(codes, m(e.code, jsonx.Str(e.meaning)))
	}
	var cmds []jsonx.Value
	for _, p := range LeafPaths(root) {
		cmds = append(cmds, commandSummary(Find(root, p), p))
	}
	return jsonx.Obj(
		m("schemaVersion", jsonx.Int(SchemaVersion)),
		m("name", jsonx.Str("mdr")),
		m("version", jsonx.Str(version)),
		m("api", jsonx.Obj(m("base_url", jsonx.Str(BaseURL)), m("staging_url", jsonx.Str(StagingURL)))),
		m("global_flags", jsonx.Arr(flags...)),
		m("exit_codes", jsonx.Obj(codes...)),
		m("error_shape", jsonx.Obj(m("stream", jsonx.Str("stderr")), m("keys", jsonx.Strs(apperr.JSONKeys())))),
		m("commands", jsonx.Arr(cmds...)),
	)
}

// SchemaCommand returns the detailed description of the command at path
// (mdr schema <path>). For a non-leaf path it lists the leaves below it.
func SchemaCommand(root *Command, path []string) (jsonx.Value, error) {
	cmd, canon := resolve(root, path)
	if cmd == nil || cmd == root {
		return jsonx.Value{}, apperr.New(apperr.Usage, "unknown command %q", strings.Join(path, " ")).
			WithHint("see: mdr schema")
	}
	if !cmd.IsLeaf() {
		var cmds []jsonx.Value
		for _, p := range LeafPaths(cmd) {
			full := append(append([]string{}, canon...), p...)
			cmds = append(cmds, commandSummary(Find(cmd, p), full))
		}
		return jsonx.Obj(
			m("schemaVersion", jsonx.Int(SchemaVersion)),
			m("path", jsonx.Str(strings.Join(canon, " "))),
			m("commands", jsonx.Arr(cmds...)),
		), nil
	}

	var endpoints []jsonx.Value
	for _, e := range cmd.Endpoints {
		scope := jsonx.Value{}
		if e.Scope != "" {
			scope = jsonx.Str(e.Scope)
		}
		endpoints = append(endpoints, jsonx.Obj(m("method", jsonx.Str(e.Method)), m("path", jsonx.Str(e.Path)), m("scope", scope)))
	}
	var args []jsonx.Value
	for _, a := range cmd.Args {
		args = append(args, jsonx.Obj(
			m("name", jsonx.Str(a.Name)),
			m("required", jsonx.BoolV(a.Required)),
			m("variadic", jsonx.BoolV(a.Variadic)),
			m("help", jsonx.Str(a.Help)),
		))
	}
	var flags []jsonx.Value
	for _, f := range cmd.Flags {
		if !f.Hidden {
			flags = append(flags, flagSchema(f))
		}
	}
	out := jsonx.Value{}
	if o := cmd.Output; o != nil {
		def := jsonx.Value{}
		if o.DefaultFields != nil {
			def = jsonx.Strs(o.DefaultFields)
		}
		out = jsonx.Obj(m("shape", jsonx.Str(string(o.Shape))), m("default_fields", def), m("fields", jsonx.Strs(o.Fields)))
	}
	var errs []string
	for _, c := range cmd.Errors {
		errs = append(errs, string(c))
	}
	return jsonx.Obj(
		m("schemaVersion", jsonx.Int(SchemaVersion)),
		m("path", jsonx.Str(strings.Join(canon, " "))),
		m("aliases", jsonx.Strs(aliasesOf(root, cmd, canon))),
		m("summary", jsonx.Str(cmd.Summary)),
		m("description", jsonx.Str(cmd.Long)),
		m("endpoints", jsonx.Arr(endpoints...)),
		m("args", jsonx.Arr(args...)),
		m("flags", jsonx.Arr(flags...)),
		m("output", out),
		m("mutating", mutatingValue(cmd.Mutation)),
		m("requires_yes", jsonx.BoolV(cmd.Mutation == Destructive)),
		m("supports_dry_run", jsonx.BoolV(len(cmd.Endpoints) > 0)),
		m("examples", jsonx.Strs(cmd.Examples)),
		m("errors", jsonx.Strs(errs)),
	), nil
}

// LeafPaths returns the paths (relative to root) of every visible leaf in
// definition order, depth first. Hidden nodes and alias nodes are skipped.
func LeafPaths(root *Command) [][]string {
	var out [][]string
	var walk func(c *Command, path []string)
	walk = func(c *Command, path []string) {
		for _, s := range c.Sub {
			if s.Hidden || s.AliasOf != "" {
				continue
			}
			p := append(append([]string{}, path...), s.Name)
			if s.IsLeaf() {
				out = append(out, p)
			} else {
				walk(s, p)
			}
		}
	}
	walk(root, nil)
	return out
}

func commandSummary(c *Command, path []string) jsonx.Value {
	ms := []jsonx.Member{
		m("path", jsonx.Str(strings.Join(path, " "))),
		m("summary", jsonx.Str(c.Summary)),
		m("mutating", mutatingValue(c.Mutation)),
	}
	if s := firstScope(c); s != "" {
		ms = append(ms, m("scope", jsonx.Str(s)))
	}
	return jsonx.Obj(ms...)
}

func mutatingValue(mu Mutation) jsonx.Value {
	if mu == ReadOnly {
		return jsonx.BoolV(false)
	}
	return jsonx.Str(string(mu))
}

func flagSchema(f Flag) jsonx.Value {
	ms := []jsonx.Member{m("name", jsonx.Str(dashName(f.Name)))}
	if f.Short != "" {
		ms = append(ms, m("short", jsonx.Str("-"+f.Short)))
	}
	ms = append(ms, m("type", jsonx.Str(kindName(f.Kind))))
	if f.Kind == KindEnum {
		ms = append(ms, m("values", jsonx.Strs(f.Enum)))
	}
	if f.Default != "" {
		ms = append(ms, m("default", jsonx.Str(f.Default)))
	}
	ms = append(ms, m("help", jsonx.Str(f.Help)))
	return jsonx.Obj(ms...)
}

func kindName(k FlagKind) string {
	switch k {
	case KindBool:
		return "bool"
	case KindInt:
		return "int"
	case KindStrings:
		return "list"
	case KindEnum:
		return "enum"
	}
	return "string"
}

// resolve finds the node at path following names and aliases, and returns its canonical path.
func resolve(root *Command, path []string) (*Command, []string) {
	cur := root
	var canon []string
	for _, name := range path {
		child := findChild(cur, name)
		if child == nil {
			return nil, nil
		}
		if child.AliasOf != "" {
			target := strings.Fields(child.AliasOf)
			child, _ = resolve(root, target)
			if child == nil {
				return nil, nil
			}
			cur, canon = child, target
			continue
		}
		cur, canon = child, append(canon, child.Name)
	}
	return cur, canon
}

// aliasesOf lists the alternative full paths of cmd: same-level aliases and alias nodes.
func aliasesOf(root, cmd *Command, canon []string) []string {
	parent := canon[:len(canon)-1]
	var out []string
	for _, a := range cmd.Aliases {
		out = append(out, strings.Join(append(append([]string{}, parent...), a), " "))
	}
	target := strings.Join(canon, " ")
	var walk func(c *Command, path []string)
	walk = func(c *Command, path []string) {
		for _, s := range c.Sub {
			p := append(append([]string{}, path...), s.Name)
			if s.AliasOf == target {
				out = append(out, strings.Join(p, " "))
			}
			walk(s, p)
		}
	}
	walk(root, nil)
	return out
}
