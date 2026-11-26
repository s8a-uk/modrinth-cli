// Package cli holds the command table types and everything generated from the table:
// argument parsing, help text and the machine-readable schema.
package cli

import (
	"context"
	"io"
	"strings"

	"github.com/s8a-uk/modrinth-cli/internal/api"
	"github.com/s8a-uk/modrinth-cli/internal/apperr"
	"github.com/s8a-uk/modrinth-cli/internal/config"
	"github.com/s8a-uk/modrinth-cli/internal/output"
)

// Mutation marks commands that change server state: "" | "write" | "destructive".
type Mutation string

// Mutation levels.
const (
	ReadOnly    Mutation = ""
	Write       Mutation = "write"
	Destructive Mutation = "destructive"
)

// FlagKind is the value type of a flag.
type FlagKind uint8

// Flag kinds.
const (
	KindBool FlagKind = iota
	KindString
	KindInt
	KindStrings
	KindEnum
)

// Arg is a positional argument.
type Arg struct {
	Name     string // "id"
	Help     string
	Required bool
	Variadic bool     // only the last argument may be variadic
	Values   []string // values offered by shell completion (first argument only); not enforced
	Validate func(string) error
}

// Flag is a command-specific or global flag.
type Flag struct {
	Name     string // "game-version" (without dashes)
	Short    string // "o" (optional)
	Kind     FlagKind
	Enum     []string // KindEnum only
	Default  string   // shown in help and schema
	Value    string   // value placeholder for help, e.g. "<n>"
	Help     string
	Validate func(string) error // per value (each value for KindStrings)
	Hidden   bool
}

// Endpoint is an API endpoint a command calls.
type Endpoint struct {
	Method string // "GET"
	Path   string // "/project/{id|slug}"
	Scope  string // "" = no scope needed
}

// Command is a node of the command tree. A node with Run is a leaf.
type Command struct {
	Name      string
	Aliases   []string
	Summary   string // one line, English, at most 60 characters
	Long      string // optional description for help
	Args      []Arg
	Flags     []Flag
	Endpoints []Endpoint
	Output    *output.Spec
	Mutation  Mutation
	Examples  []string
	Errors    []apperr.Code // main errors the command can return (for schema)
	Hidden    bool
	Run       func(ctx context.Context, inv *Invocation) (*output.Result, error)
	Sub       []*Command

	// Offline marks commands that need neither the settings nor the API client
	// (schema). They still run when the settings cannot be loaded.
	Offline bool

	// Local marks commands that need the settings but not the API client (config,
	// auth logout). They get no Client, so they never send a request.
	Local bool

	// AliasOf makes this node an alias of the command at that space-separated path
	// from the root (e.g. "whoami" with AliasOf "user me"). An alias node has only
	// Name and AliasOf; it is resolved while parsing and never listed itself.
	AliasOf string
}

// IsLeaf reports whether c is a runnable command.
func (c *Command) IsLeaf() bool { return c.Run != nil }

// IO is the process's standard streams.
type IO struct {
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
	StdinIsTTY  bool
	StdoutIsTTY bool
	StderrIsTTY bool
	StdoutWidth int // terminal width of stdout in cells; 0 = unknown or not a terminal
}

// Globals holds the parsed global flags.
type Globals struct {
	Output  string
	Fields  []string
	Full    bool
	Staging bool
	DryRun  bool
	Yes     bool
	Timeout int
	MaxWait int
	NoRetry bool
	Verbose int // 0, 1, 2
	Quiet   bool
	NoColor bool
	Help    bool
	Version bool
	set     map[string]bool // flags given explicitly
}

// Changed reports whether the global flag was given explicitly.
func (g *Globals) Changed(name string) bool { return g.set[name] }

// Invocation is everything a command's Run receives.
type Invocation struct {
	Cmd      *Command
	Path     []string // ["project", "get"]
	Globals  *Globals
	IO       IO
	Log      *output.Stderr
	Root     *Command // for schema / completion
	Env      config.Env
	Settings *config.Settings
	Paths    config.Paths
	Client   *api.Client // nil for Offline and Local commands
	// NewClient returns a client like Client but sending tok instead of the
	// configured token (auth login). nil for Offline and Local commands.
	NewClient func(tok config.Token) *api.Client
	// Doer replaces the HTTP client of downloads in tests; nil = the real one.
	Doer api.Doer

	args  map[string][]string
	flags map[string]*value
}

// Arg returns the value of a positional argument, or "".
func (inv *Invocation) Arg(name string) string {
	if v := inv.args[name]; len(v) > 0 {
		return v[0]
	}
	return ""
}

// ArgList returns all values of a (variadic) positional argument.
func (inv *Invocation) ArgList(name string) []string { return inv.args[name] }

// NArgs returns the number of positional values.
func (inv *Invocation) NArgs() int {
	n := 0
	for _, v := range inv.args {
		n += len(v)
	}
	return n
}

// String returns a string or enum flag.
func (inv *Invocation) String(flag string) string {
	if v := inv.flags[flag]; v != nil {
		return v.s
	}
	return ""
}

// Strings returns a list flag.
func (inv *Invocation) Strings(flag string) []string {
	if v := inv.flags[flag]; v != nil {
		return v.list
	}
	return nil
}

// Int returns an int flag.
func (inv *Invocation) Int(flag string) int {
	if v := inv.flags[flag]; v != nil {
		return v.n
	}
	return 0
}

// Bool returns a bool flag.
func (inv *Invocation) Bool(flag string) bool {
	if v := inv.flags[flag]; v != nil {
		return v.b
	}
	return false
}

// Changed reports whether the flag was given explicitly.
func (inv *Invocation) Changed(flag string) bool {
	if v := inv.flags[flag]; v != nil {
		return v.set
	}
	return false
}

// Parsed is the result of Parse.
type Parsed struct {
	Cmd     *Command // the resolved node; may be a non-leaf (then help is shown)
	Path    []string
	Globals *Globals

	args  map[string][]string
	flags map[string]*value
}

// Invocation returns an Invocation with the parse results filled in. The caller
// sets IO, Log and Root.
func (p *Parsed) Invocation() *Invocation {
	return &Invocation{Cmd: p.Cmd, Path: p.Path, Globals: p.Globals, args: p.args, flags: p.flags}
}

// commandLine returns "mdr" or "mdr project get".
func commandLine(path []string) string {
	if len(path) == 0 {
		return "mdr"
	}
	return "mdr " + strings.Join(path, " ")
}

// dashName renders a flag name: one- and two-letter names ("v", "vv") take one dash.
func dashName(name string) string {
	if len(name) <= 2 {
		return "-" + name
	}
	return "--" + name
}
