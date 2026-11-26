package commands

import (
	"context"
	"strings"

	"github.com/s8a-uk/modrinth-cli/internal/apperr"
	"github.com/s8a-uk/modrinth-cli/internal/cli"
	"github.com/s8a-uk/modrinth-cli/internal/config"
	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
	"github.com/s8a-uk/modrinth-cli/internal/output"
)

func configCmd() *cli.Command {
	return &cli.Command{
		Name:    "config",
		Summary: "Read and change settings in config.json",
		Sub:     []*cli.Command{configGetCmd(), configSetCmd(), configUnsetCmd(), configListCmd(), configPathCmd()},
	}
}

var configKeyHelp = "config key: " + strings.Join(config.Keys, ", ")

var configEntryFields = []string{"key", "value", "source"}

var configEntryTable = []output.Column{
	{Header: "KEY", Path: "key"},
	{Header: "VALUE", Path: "value"},
	{Header: "SOURCE", Path: "source"},
}

// configEntry is {"key","value","source"} with the resolved value of key.
func configEntry(inv *cli.Invocation, key string) (jsonx.Value, error) {
	v, ok := inv.Settings.Value(key)
	if !ok {
		return jsonx.Value{}, config.UnknownKey(key)
	}
	return jsonx.Obj(
		jsonx.Member{Key: "key", Value: jsonx.Str(key)},
		jsonx.Member{Key: "value", Value: v},
		jsonx.Member{Key: "source", Value: jsonx.Str(string(inv.Settings.Sources[key]))},
	), nil
}

func configGetCmd() *cli.Command {
	return &cli.Command{
		Name:    "get",
		Summary: "Show the resolved value of a setting and its source",
		Long: "Precedence per key: flag > environment variable > config.json > default. " +
			"source is flag, env, file or default.",
		Args:     []cli.Arg{{Name: "key", Required: true, Help: configKeyHelp}},
		Output:   &output.Spec{Shape: output.Object, Fields: configEntryFields, Table: configEntryTable},
		Local:    true,
		Examples: []string{"mdr config get timeout"},
		Errors:   []apperr.Code{apperr.Usage},
		Run: func(_ context.Context, inv *cli.Invocation) (*output.Result, error) {
			v, err := configEntry(inv, inv.Arg("key"))
			if err != nil {
				return nil, err
			}
			return &output.Result{Shape: output.Object, Value: v}, nil
		},
	}
}

func configListCmd() *cli.Command {
	return &cli.Command{
		Name:     "list",
		Summary:  "Show every setting with its value and source",
		Long:     "Tokens are not listed; see mdr auth status.",
		Output:   &output.Spec{Shape: output.Array, Fields: configEntryFields, Table: configEntryTable},
		Local:    true,
		Examples: []string{"mdr config list", "mdr config list -o table"},
		Run: func(_ context.Context, inv *cli.Invocation) (*output.Result, error) {
			var items []jsonx.Value
			for _, k := range config.Keys {
				v, err := configEntry(inv, k)
				if err != nil {
					return nil, err
				}
				items = append(items, v)
			}
			return &output.Result{Shape: output.Array, Value: jsonx.Arr(items...)}, nil
		},
	}
}

func configSetCmd() *cli.Command {
	return &cli.Command{
		Name:    "set",
		Summary: "Save a setting to config.json",
		Long: "Values: output auto|json|ndjson|table; staging true|false; user_agent_contact an email address or URL; " +
			"timeout 1-3600 and max_wait 0-3600 (integer seconds). Other keys in the file are kept.",
		Args: []cli.Arg{
			{Name: "key", Required: true, Help: configKeyHelp},
			{Name: "value", Required: true, Help: "new value"},
		},
		Output:   &output.Spec{Shape: output.Ack},
		Local:    true,
		Examples: []string{"mdr config set timeout 60", "mdr config set user_agent_contact you@example.com"},
		Errors:   []apperr.Code{apperr.Usage, apperr.InvalidInput, apperr.IOError},
		Run: func(_ context.Context, inv *cli.Invocation) (*output.Result, error) {
			key := inv.Arg("key")
			v, err := config.SetKey(inv.Paths, key, inv.Arg("value"))
			if err != nil {
				return nil, err
			}
			return &output.Result{Shape: output.Ack, Notes: overriddenNote(inv, key), Value: jsonx.Obj(
				jsonx.Member{Key: "ok", Value: jsonx.BoolV(true)},
				jsonx.Member{Key: "action", Value: jsonx.Str("set")},
				jsonx.Member{Key: "resource", Value: jsonx.Str("config")},
				jsonx.Member{Key: "key", Value: jsonx.Str(key)},
				jsonx.Member{Key: "value", Value: v},
			)}, nil
		},
	}
}

func configUnsetCmd() *cli.Command {
	return &cli.Command{
		Name:     "unset",
		Summary:  "Remove a setting from config.json",
		Args:     []cli.Arg{{Name: "key", Required: true, Help: configKeyHelp}},
		Output:   &output.Spec{Shape: output.Ack},
		Local:    true,
		Examples: []string{"mdr config unset timeout"},
		Errors:   []apperr.Code{apperr.Usage, apperr.IOError},
		Run: func(_ context.Context, inv *cli.Invocation) (*output.Result, error) {
			key := inv.Arg("key")
			if err := config.UnsetKey(inv.Paths, key); err != nil {
				return nil, err
			}
			return &output.Result{Shape: output.Ack, Notes: overriddenNote(inv, key), Value: jsonx.Obj(
				jsonx.Member{Key: "ok", Value: jsonx.BoolV(true)},
				jsonx.Member{Key: "action", Value: jsonx.Str("unset")},
				jsonx.Member{Key: "resource", Value: jsonx.Str("config")},
				jsonx.Member{Key: "key", Value: jsonx.Str(key)},
			)}, nil
		},
	}
}

// overriddenNote tells that the file value of key has no effect in this environment.
func overriddenNote(inv *cli.Invocation, key string) []string {
	if inv.Settings.Sources[key] == config.SourceEnv {
		return []string{"the environment overrides " + key + " (see: mdr config get " + key + ")"}
	}
	return nil
}

func configPathCmd() *cli.Command {
	return &cli.Command{
		Name:    "path",
		Summary: "Show where the config files are",
		Long: "The directory is MODRINTH_CONFIG_DIR, else <user config dir>/modrinth-cli: $XDG_CONFIG_HOME or " +
			"~/.config on Linux, %AppData% on Windows, ~/Library/Application Support on macOS. The files need not exist.",
		Output:   &output.Spec{Shape: output.Raw},
		Local:    true,
		Examples: []string{"mdr config path"},
		Errors:   []apperr.Code{apperr.IOError},
		Run: func(_ context.Context, inv *cli.Invocation) (*output.Result, error) {
			p := inv.Paths
			if err := config.RequireDir(p); err != nil {
				return nil, err
			}
			return &output.Result{Shape: output.Raw, Value: jsonx.Obj(
				jsonx.Member{Key: "config_dir", Value: jsonx.Str(p.Dir)},
				jsonx.Member{Key: "config_file", Value: jsonx.Str(p.ConfigFile)},
				jsonx.Member{Key: "credentials_file", Value: jsonx.Str(p.Credentials)},
			)}, nil
		},
	}
}
