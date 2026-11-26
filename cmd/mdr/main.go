// Command mdr is a command-line client for the Modrinth API.
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/s8a-uk/modrinth-cli/internal/app"
	"github.com/s8a-uk/modrinth-cli/internal/cli"
)

func main() {
	code := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	os.Exit(code)
}

// appRun is replaced in tests.
var appRun = app.Run

func run(args []string, stdin, stdout, stderr *os.File) (code int) {
	out := bufio.NewWriter(stdout)
	defer out.Flush() // runs last, after the panic handler; write errors are ignored
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(stderr, "error: internal: panic: %v\n", r)
			code = 1 // Go's default exit code 2 for a panic would collide with usage errors.
		}
	}()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	io := cli.IO{
		Stdin: stdin, Stdout: out, Stderr: stderr,
		StdinIsTTY: isTTY(stdin), StdoutIsTTY: isTTY(stdout), StderrIsTTY: isTTY(stderr),
	}
	if io.StdoutIsTTY {
		io.StdoutWidth = termWidth(stdout.Fd())
	}
	return appRun(ctx, args, io, os.Getenv, app.Deps{})
}

func isTTY(f *os.File) bool {
	return isTerminal(f.Fd())
}
