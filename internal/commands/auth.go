package commands

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/s8a-uk/modrinth-cli/internal/api"
	"github.com/s8a-uk/modrinth-cli/internal/apperr"
	"github.com/s8a-uk/modrinth-cli/internal/cli"
	"github.com/s8a-uk/modrinth-cli/internal/config"
	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
	"github.com/s8a-uk/modrinth-cli/internal/output"
	"github.com/s8a-uk/modrinth-cli/internal/validate"
)

func authCmd() *cli.Command {
	return &cli.Command{
		Name:    "auth",
		Summary: "Manage the saved API token",
		Sub:     []*cli.Command{authLoginCmd(), authStatusCmd(), authLogoutCmd()},
	}
}

// maxTokenLine is the longest line auth login reads from stdin.
const maxTokenLine = 4096

func authLoginCmd() *cli.Command {
	return &cli.Command{
		Name:    "login",
		Summary: "Check a token from stdin and save it",
		Long: "Reads a personal access token from the first line of stdin, checks it with GET /user and saves it " +
			"in plain text to credentials.json in the config directory (0600). With --staging the staging token is saved. " +
			"The token is never taken as an argument. For agents and CI prefer MODRINTH_TOKEN or MODRINTH_TOKEN_FILE.",
		Flags: []cli.Flag{
			{Name: "with-token", Kind: cli.KindBool, Help: "read the token from stdin (required)"},
		},
		Endpoints: []cli.Endpoint{{Method: "GET", Path: "/user", Scope: api.ScopeUserRead}},
		Output:    &output.Spec{Shape: output.Ack},
		Examples:  []string{"mdr auth login --with-token < token.txt", "mdr auth login --with-token --staging < staging-token.txt"},
		Errors:    []apperr.Code{apperr.Usage, apperr.InvalidInput, apperr.Unauthorized, apperr.IOError},
		Run: func(ctx context.Context, inv *cli.Invocation) (*output.Result, error) {
			if !inv.Bool("with-token") {
				return nil, apperr.New(apperr.Usage, "--with-token is required").
					WithHint("pipe the token on stdin: mdr auth login --with-token < token.txt")
			}
			if err := config.RequireDir(inv.Paths); err != nil {
				return nil, err
			}
			if inv.IO.StdinIsTTY {
				fmt.Fprintln(inv.IO.Stderr, "Paste your Modrinth PAT and press Enter (input is visible):")
			}
			raw, err := readTokenLine(inv.IO.Stdin)
			if err != nil {
				return nil, err
			}
			if validate.Token(raw) != nil {
				return nil, apperr.New(apperr.InvalidInput, "invalid token on stdin: unexpected characters or length").
					WithHint("a token is 20-512 letters, digits or _-.; the value is not shown")
			}
			if !strings.HasPrefix(raw, "mrp_") {
				inv.Log.Warn("token does not look like a Modrinth PAT (mrp_...)")
			}
			tok := config.NewToken(raw)
			user, err := inv.NewClient(tok).GetMe(ctx)
			if err != nil {
				return nil, err
			}
			staging := inv.Settings.Staging
			if err := config.SaveToken(inv.Paths, staging, tok); err != nil {
				return nil, err
			}
			notes := []string{"token saved in plain text to " + inv.Paths.Credentials}
			if inv.Settings.TokenVar != "" {
				notes = append(notes, inv.Settings.TokenVar+" is set and takes precedence over the saved token")
			}
			return &output.Result{Shape: output.Ack, Notes: notes, Value: jsonx.Obj(
				jsonx.Member{Key: "ok", Value: jsonx.BoolV(true)},
				jsonx.Member{Key: "action", Value: jsonx.Str("login")},
				jsonx.Member{Key: "resource", Value: jsonx.Str("auth")},
				jsonx.Member{Key: "username", Value: stringOrNull(user, "username")},
				jsonx.Member{Key: "user_id", Value: stringOrNull(user, "id")},
				jsonx.Member{Key: "credentials_file", Value: jsonx.Str(inv.Paths.Credentials)},
				jsonx.Member{Key: "staging", Value: jsonx.BoolV(staging)},
			)}, nil
		},
	}
}

// readTokenLine reads the first line of r (at most maxTokenLine bytes), trimmed.
// Errors never contain what was read.
func readTokenLine(r io.Reader) (string, error) {
	br := bufio.NewReaderSize(io.LimitReader(r, maxTokenLine+1), maxTokenLine+1)
	line, err := br.ReadSlice('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		if errors.Is(err, bufio.ErrBufferFull) {
			return "", apperr.New(apperr.InvalidInput, "the token line on stdin is longer than %d bytes", maxTokenLine)
		}
		return "", apperr.New(apperr.IOError, "cannot read the token from stdin").WithCause(err)
	}
	if len(line) > maxTokenLine {
		return "", apperr.New(apperr.InvalidInput, "the token line on stdin is longer than %d bytes", maxTokenLine)
	}
	tok := strings.TrimSpace(string(line))
	if tok == "" {
		return "", apperr.New(apperr.InvalidInput, "no token on stdin").
			WithHint("pipe the token on stdin: mdr auth login --with-token < token.txt")
	}
	return tok, nil
}

func authStatusCmd() *cli.Command {
	return &cli.Command{
		Name:    "status",
		Summary: "Show whether a token is set and whose it is",
		Long: "Without a token, prints authenticated: false (exit 0) and sends nothing. With one, checks it with GET /user; " +
			"a rejected token fails with unauthorized (exit 4). token is masked (mrp_…wxyz). token_source is env " +
			"(MODRINTH_TOKEN / MODRINTH_STAGING_TOKEN), token_file (MODRINTH_TOKEN_FILE / MODRINTH_STAGING_TOKEN_FILE) " +
			"or file (credentials.json).",
		Endpoints: []cli.Endpoint{{Method: "GET", Path: "/user", Scope: api.ScopeUserRead}},
		Output:    &output.Spec{Shape: output.Raw},
		Examples:  []string{"mdr auth status", "mdr auth status --staging"},
		Errors:    []apperr.Code{apperr.Unauthorized},
		Run: func(ctx context.Context, inv *cli.Invocation) (*output.Result, error) {
			s := inv.Settings
			tail := []jsonx.Member{
				{Key: "api_url", Value: jsonx.Str(s.APIURL + "/v2")},
				{Key: "staging", Value: jsonx.BoolV(s.Staging)},
			}
			if s.Token.IsZero() {
				return &output.Result{Shape: output.Raw, Value: jsonx.Obj(append([]jsonx.Member{
					{Key: "authenticated", Value: jsonx.BoolV(false)},
					{Key: "token_source", Value: jsonx.Value{}},
				}, tail...)...)}, nil
			}
			user, err := inv.Client.GetMe(ctx)
			if err != nil {
				return nil, err
			}
			return &output.Result{Shape: output.Raw, Value: jsonx.Obj(append([]jsonx.Member{
				{Key: "authenticated", Value: jsonx.BoolV(true)},
				{Key: "username", Value: stringOrNull(user, "username")},
				{Key: "user_id", Value: stringOrNull(user, "id")},
				{Key: "token", Value: jsonx.Str(s.Token.Masked())},
				{Key: "token_source", Value: jsonx.Str(string(s.TokenSource))},
			}, tail...)...)}, nil
		},
	}
}

func authLogoutCmd() *cli.Command {
	return &cli.Command{
		Name:    "logout",
		Summary: "Remove the saved token",
		Long: "Removes the token (with --staging, the staging token) from credentials.json, and the file when nothing " +
			"is left in it. Token environment variables are not affected.",
		Output:   &output.Spec{Shape: output.Ack},
		Local:    true,
		Examples: []string{"mdr auth logout", "mdr auth logout --staging"},
		Errors:   []apperr.Code{apperr.IOError},
		Run: func(_ context.Context, inv *cli.Invocation) (*output.Result, error) {
			staging := inv.Settings.Staging
			removed, err := config.DeleteToken(inv.Paths, staging)
			if err != nil {
				return nil, err
			}
			var notes []string
			name, fileName := config.TokenVars(staging)
			for _, v := range []string{name, fileName} {
				if inv.Env(v) != "" {
					notes = append(notes, v+" is still set in the environment")
				}
			}
			return &output.Result{Shape: output.Ack, Notes: notes, Value: jsonx.Obj(
				jsonx.Member{Key: "ok", Value: jsonx.BoolV(true)},
				jsonx.Member{Key: "action", Value: jsonx.Str("logout")},
				jsonx.Member{Key: "resource", Value: jsonx.Str("auth")},
				jsonx.Member{Key: "removed", Value: jsonx.BoolV(removed)},
			)}, nil
		},
	}
}

// stringOrNull returns the string at key of obj, or null.
func stringOrNull(obj jsonx.Value, key string) jsonx.Value {
	if v, ok := obj.Get(key); ok && v.Kind == jsonx.String {
		return v
	}
	return jsonx.Value{}
}
