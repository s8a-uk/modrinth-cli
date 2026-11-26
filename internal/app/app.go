// Package app wires the command table, parsing, rendering and error reporting into
// a single run of the CLI.
package app

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/s8a-uk/modrinth-cli/internal/api"
	"github.com/s8a-uk/modrinth-cli/internal/apperr"
	"github.com/s8a-uk/modrinth-cli/internal/buildinfo"
	"github.com/s8a-uk/modrinth-cli/internal/cli"
	"github.com/s8a-uk/modrinth-cli/internal/commands"
	"github.com/s8a-uk/modrinth-cli/internal/config"
	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
	"github.com/s8a-uk/modrinth-cli/internal/output"
)

// Deps are the parts tests replace. The zero value means the real ones.
type Deps struct {
	Doer  api.Doer
	Clock api.Clock
}

// Run executes the CLI once and returns the exit code. It never calls os.Exit.
func Run(ctx context.Context, args []string, io cli.IO, env config.Env, deps Deps) int {
	// 1. Parse.
	root := commands.Root()
	p, err := cli.Parse(root, args)
	if err != nil {
		return writeError(io, errorFormat(args, io.StdoutIsTTY), err)
	}
	g := p.Globals

	// 2. Version and help.
	if g.Version {
		fmt.Fprintf(io.Stdout, "mdr %s\n", buildinfo.Resolved())
		return 0
	}
	if g.Help || !p.Cmd.IsLeaf() {
		fmt.Fprint(io.Stdout, cli.HelpText(root, p.Cmd, p.Path))
		return 0
	}

	// 3. Settings. Offline commands (schema) run even when they cannot be loaded.
	log := output.NewStderr(io.Stderr, g.Verbose, g.Quiet)
	settings, paths, warnings, err := config.Load(env, configFlags(g))
	if err != nil {
		if !p.Cmd.Offline {
			return writeError(io, errorFormat(args, io.StdoutIsTTY), err)
		}
		settings = &config.Settings{Output: g.Output}
	}
	for _, w := range warnings {
		log.Warn("%s", w)
	}

	// 4. Output format.
	format, err := output.ResolveFormat(settings.Output, io.StdoutIsTTY)
	if err != nil {
		return writeError(io, errorFormat(args, io.StdoutIsTTY), err)
	}

	// 5. Fields: the syntax before any request (and the names too for commands
	// that change data); other names are checked against the response.
	spec := p.Cmd.Output
	cmdPath := strings.Join(p.Path, " ")
	if err := output.CheckFields(spec, g.Fields, g.Full, cmdPath, p.Cmd.Mutation != cli.ReadOnly); err != nil {
		return writeError(io, format, err)
	}

	// 7. Invocation.
	inv := p.Invocation()
	inv.IO = io
	inv.Log = log
	inv.Root = root
	inv.Env = env
	inv.Settings = settings
	inv.Paths = paths
	// A command that changes the server and has no token: the client refuses every
	// request with auth_required (unless --dry-run, which prints the plan instead), so
	// the inputs are still checked first and nothing is sent.
	cmdScope := firstScope(p.Cmd)
	var missing func(scope string) error
	if settings.Token.IsZero() {
		missing = func(scope string) error {
			if scope == "" {
				scope = cmdScope
			}
			return commands.MissingToken(settings.Staging, scope)
		}
	}
	if !p.Cmd.Offline && !p.Cmd.Local {
		cc := clientConfig{settings: settings, dryRun: g.DryRun, deps: deps, log: log,
			missing: missing, requireToken: p.Cmd.Mutation != cli.ReadOnly && !g.DryRun}
		inv.Client = cc.client(settings.Token)
		inv.NewClient = func(tok config.Token) *api.Client { return cc.client(tok) }
		inv.Doer = deps.Doer
	}

	// 8. Destructive commands: a token, then the confirmation, both before any
	// request. Neither applies to --dry-run.
	if p.Cmd.Mutation == cli.Destructive && !g.DryRun {
		if missing != nil {
			return writeError(io, format, missing(""))
		}
		if !g.Yes {
			if err := confirm(inv); err != nil {
				return writeError(io, format, err)
			}
		}
	}

	// 9. Run.
	res, err := p.Cmd.Run(ctx, inv)
	var dry *api.DryRunError
	switch {
	case errors.As(err, &dry):
		// The plan is printed as a raw result: --fields does not apply to raw, and
		// table shows indented JSON.
		warnMissingToken(log, dry.Plan)
		res, spec = &output.Result{Shape: output.Raw, Value: dry.Plan}, &output.Spec{Shape: output.Raw}
	case err != nil && errors.Is(ctx.Err(), context.Canceled) && apperr.From(err).Code != apperr.Interrupted:
		// Interrupted (SIGINT), whatever the command made of the cancellation.
		return writeError(io, format, apperr.New(apperr.Interrupted, "interrupted").WithCause(err))
	case err != nil:
		return writeError(io, format, err)
	}

	// 10. Notes, then the result, then the notes about the result.
	for _, n := range res.Notes {
		inv.Log.Note("%s", n)
	}
	if res.Format != "" {
		format = res.Format
	}
	if res.Spec != nil {
		spec = res.Spec
	}
	var notes []string
	opts := output.Options{
		Format: format,
		Fields: g.Fields,
		Full:   g.Full,
		Pretty: format == output.JSON && io.StdoutIsTTY,
		Color:  useColor(format, io.StdoutIsTTY, g.NoColor, env),
		Width:  termWidth(env, io.StdoutWidth),
		Notef:  func(f string, args ...any) { notes = append(notes, fmt.Sprintf(f, args...)) },
	}
	if res.Bytes == nil {
		undocumented, err := output.ResolveFields(res.Value, res.Shape, spec, g.Fields, cmdPath)
		if err != nil {
			if !res.MayHaveWritten {
				return writeError(io, format, err)
			}
			// The request may have changed data: print the result anyway.
			log.Warn("%s", apperr.From(err).Message)
		}
		if !spec.AnyFields {
			for _, f := range undocumented {
				log.Note("field %q is not in the documented fields of %q (see: mdr schema %s)", f, cmdPath, cmdPath)
			}
		}
		res.Value = output.Project(res.Value, res.Shape, spec, opts)
	} else if len(g.Fields) > 0 && spec.AnyFields {
		log.Warn("--fields ignored: the response is not JSON")
	}
	var buf bytes.Buffer
	if err := output.RendererFor(format).Render(&buf, res, spec, opts); err != nil {
		return writeError(io, format, err)
	}
	// A failed write (e.g. EPIPE after "| head") is not an error.
	_, _ = io.Stdout.Write(buf.Bytes())
	if len(notes) > 0 {
		// Show the notes after the result on a shared terminal (stdout is buffered).
		if f, ok := io.Stdout.(interface{ Flush() error }); ok {
			_ = f.Flush()
		}
	}
	for _, n := range notes {
		inv.Log.Note("%s", n)
	}
	return 0
}

// firstScope is the first token scope among the command's endpoints.
func firstScope(c *cli.Command) string {
	for _, e := range c.Endpoints {
		if e.Scope != "" {
			return e.Scope
		}
	}
	return ""
}

// warnMissingToken warns when a dry run shows a request that needs a token but
// none is configured.
func warnMissingToken(log *output.Stderr, plan jsonx.Value) {
	auth, _ := plan.Get("auth")
	if tok, _ := auth.Get("token"); tok.Str != api.TokenMissing {
		return
	}
	if scope, ok := auth.Get("required_scope"); ok {
		log.Warn("no token is configured: without --dry-run this fails with auth_required (required scope: %s)", scope.Str)
		return
	}
	log.Warn("no token is configured: the API rejects most requests that change data without one")
}

// confirm asks for the id of the target of a destructive command on a terminal,
// and refuses to go on without --yes otherwise.
func confirm(inv *cli.Invocation) error {
	if !inv.IO.StdinIsTTY {
		return apperr.New(apperr.ConfirmationRequired, "%s needs confirmation", "mdr "+strings.Join(inv.Path, " ")).
			WithHint("re-run with --yes to confirm, or with --dry-run to preview")
	}
	id := ""
	if len(inv.Cmd.Args) > 0 {
		id = inv.Arg(inv.Cmd.Args[0].Name)
	}
	resource := inv.Path[0]
	fmt.Fprintf(inv.IO.Stderr, "Delete %s '%s'? Type the id to confirm: ", resource, id)
	line, err := bufio.NewReader(inv.IO.Stdin).ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(inv.IO.Stderr)
	}
	if strings.TrimSpace(line) != id {
		return apperr.New(apperr.Usage, "confirmation did not match; nothing was changed")
	}
	return nil
}

// useColor: tables on a terminal, unless --no-color or NO_COLOR, and on Windows only
// in terminals known to understand ANSI escapes (Windows Terminal, or TERM set).
func useColor(format output.Format, stdoutIsTTY, noColor bool, env config.Env) bool {
	if format != output.Table || !stdoutIsTTY || noColor || env("NO_COLOR") != "" {
		return false
	}
	return runtime.GOOS != "windows" || env("WT_SESSION") != "" || env("TERM") != ""
}

// termWidth is COLUMNS if it is a positive integer, else the measured width of
// stdout, else 100.
func termWidth(env config.Env, measured int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(env("COLUMNS"))); err == nil && n > 0 {
		return n
	}
	if measured > 0 {
		return measured
	}
	return 100
}

func configFlags(g *cli.Globals) config.Flags {
	var f config.Flags
	if g.Changed("output") {
		f.Output = &g.Output
	}
	if g.Changed("staging") {
		f.Staging = &g.Staging
	}
	if g.Changed("timeout") {
		f.Timeout = &g.Timeout
	}
	if g.Changed("max-wait") {
		f.MaxWait = &g.MaxWait
	}
	if g.Changed("no-retry") {
		f.NoRetry = &g.NoRetry
	}
	return f
}

// clientConfig is what every API client of a run shares.
type clientConfig struct {
	settings     *config.Settings
	dryRun       bool
	deps         Deps
	log          *output.Stderr
	missing      func(scope string) error // nil when a token is configured
	requireToken bool                     // refuse every request when missing is set
}

// client builds the API client sending tok. The token is revealed here and only
// here, and only for hosts it may be sent to.
func (cc clientConfig) client(tok config.Token) *api.Client {
	s := cc.settings
	token := ""
	missing := cc.missing
	if !tok.IsZero() {
		missing = nil // auth login checks a token that is not configured yet
		if api.TokenAllowed(s.APIURL, s.AllowTokenToCustomHost) {
			token = tok.Reveal()
		} else {
			cc.log.Warn("token not sent to custom host %s (set MODRINTH_ALLOW_TOKEN_TO_CUSTOM_HOST=1 to send it)", s.APIURL)
		}
	}
	return api.New(api.Options{
		BaseURL:      s.APIURL,
		Token:        token,
		UserAgent:    api.UserAgent(buildinfo.Resolved(), s.UserAgentContact, s.UserAgentSuffix),
		Timeout:      s.Timeout,
		MaxWait:      s.MaxWait,
		NoRetry:      s.NoRetry,
		DryRun:       cc.dryRun,
		MissingToken: missing,
		RequireToken: cc.requireToken && missing != nil,
		Doer:         cc.deps.Doer,
		Clock:        cc.deps.Clock,
		Log:          cc.log,
	})
}

// writeError renders err on stderr in the given format and returns its exit code.
func writeError(io cli.IO, format output.Format, err error) int {
	e := apperr.From(err)
	if format == output.Table {
		fmt.Fprint(io.Stderr, e.Human())
	} else {
		fmt.Fprintf(io.Stderr, "%s\n", e.JSON())
	}
	return e.Code.ExitCode()
}

// errorFormat picks the error format before the output format is resolved: the last
// valid -o/--output value in the raw arguments, otherwise by whether stdout is a terminal.
func errorFormat(args []string, stdoutIsTTY bool) output.Format {
	setting := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		name, val, hasVal := strings.Cut(a, "=")
		if name != "-o" && name != "--output" {
			continue
		}
		if !hasVal {
			if i+1 >= len(args) {
				break
			}
			i++
			val = args[i]
		}
		if slices.Contains(output.Settings, val) {
			setting = val
		}
	}
	f, err := output.ResolveFormat(setting, stdoutIsTTY)
	if err != nil {
		return output.JSON
	}
	return f
}
