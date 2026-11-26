package commands

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/s8a-uk/modrinth-cli/internal/apperr"
	"github.com/s8a-uk/modrinth-cli/internal/cli"
	"github.com/s8a-uk/modrinth-cli/internal/output"
)

func completionCmd() *cli.Command {
	return &cli.Command{
		Name:    "completion",
		Summary: "Print a shell completion script",
		Long: "Prints a static completion script for bash, zsh, fish or PowerShell, generated from the " +
			"command table: commands, flags, enum values and tag kinds. It calls no API. " +
			"bash: source <(mdr completion bash). zsh: mdr completion zsh > \"${fpath[1]}/_mdr\". " +
			"fish: mdr completion fish > ~/.config/fish/completions/mdr.fish. " +
			"PowerShell: mdr completion powershell | Out-String | Invoke-Expression (add it to $PROFILE).",
		Args: []cli.Arg{
			{Name: "shell", Required: true, Help: "bash, zsh, fish or powershell", Validate: shellArg, Values: cli.Shells},
		},
		Output:   &output.Spec{Shape: output.Raw},
		Examples: []string{"mdr completion bash", "mdr completion zsh"},
		Errors:   []apperr.Code{apperr.Usage},
		Offline:  true,
		Run: func(_ context.Context, inv *cli.Invocation) (*output.Result, error) {
			script, err := cli.Completion(inv.Root, inv.Arg("shell"))
			if err != nil {
				return nil, err
			}
			return &output.Result{Shape: output.Raw, Bytes: []byte(script)}, nil
		},
	}
}

func shellArg(s string) error {
	if !slices.Contains(cli.Shells, s) {
		return errors.New("use one of: " + strings.Join(cli.Shells, ", "))
	}
	return nil
}
