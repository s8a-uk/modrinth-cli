package commands

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/s8a-uk/modrinth-cli/internal/api"
	"github.com/s8a-uk/modrinth-cli/internal/apperr"
	"github.com/s8a-uk/modrinth-cli/internal/cli"
	"github.com/s8a-uk/modrinth-cli/internal/download"
	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
	"github.com/s8a-uk/modrinth-cli/internal/output"
	"github.com/s8a-uk/modrinth-cli/internal/validate"
)

// versionTypes are the values of version list --version-type.
var versionTypes = []string{"release", "beta", "alpha"}

func versionCmd() *cli.Command {
	return &cli.Command{
		Name:    "version",
		Summary: "Read, download, upload and edit versions",
		Sub: []*cli.Command{
			versionListCmd(),
			versionGetCmd(),
			versionGetManyCmd(),
			versionDownloadCmd(),
			versionCreateCmd(),
			versionAddFileCmd(),
			versionEditCmd(),
			versionDeleteCmd(),
		},
	}
}

func versionListCmd() *cli.Command {
	return &cli.Command{
		Name:    "list",
		Aliases: []string{"ls"},
		Summary: "List the versions of a project, newest first",
		Long: "Lists versions newest first. --loader and --game-version are sent to the API; --version-type " +
			"and --limit are applied by mdr afterwards. Changelogs are not requested unless --fields includes " +
			"changelog or --full is given.",
		Args: []cli.Arg{
			{Name: "project", Required: true, Help: "project id or slug", Validate: validate.ProjectRef},
		},
		Flags: []cli.Flag{
			{Name: "loader", Kind: cli.KindStrings, Value: "<loader>", Help: "loader, e.g. fabric (repeatable, OR)", Validate: validate.Tag},
			{Name: "game-version", Kind: cli.KindStrings, Value: "<version>", Help: "game version, e.g. 1.20.1 (repeatable, OR)", Validate: validate.Tag},
			{Name: "featured", Kind: cli.KindBool, Help: "only featured versions"},
			{Name: "version-type", Kind: cli.KindStrings, Value: "<type>", Help: "release, beta or alpha (repeatable, OR)", Validate: oneOf(versionTypes)},
			{Name: "limit", Kind: cli.KindInt, Default: "20", Value: "<n>", Help: "at most this many versions, 0-10000; 0 = all", Validate: intFlag(0, 10000)},
		},
		Endpoints: []cli.Endpoint{{Method: "GET", Path: "/project/{id|slug}/version"}},
		Output:    versionListSpec(),
		Examples: []string{
			"mdr version list sodium --loader fabric --game-version 1.20.1 --version-type release --limit 1",
			"mdr version ls sodium --limit 0 --fields id,version_number",
		},
		Errors: []apperr.Code{apperr.NotFound},
		Run:    runVersionList,
	}
}

func runVersionList(ctx context.Context, inv *cli.Invocation) (*output.Result, error) {
	g := inv.Globals
	wantChangelog := g.Full || slices.ContainsFunc(g.Fields, func(f string) bool { return f == "all" || f == "changelog" })
	v, err := inv.Client.ListVersions(ctx, inv.Arg("project"), api.VersionFilter{
		Loaders:          inv.Strings("loader"),
		GameVersions:     inv.Strings("game-version"),
		Featured:         inv.Bool("featured"),
		ExcludeChangelog: !wantChangelog,
	})
	if err != nil {
		return nil, err
	}
	if v.Kind != jsonx.Array {
		return nil, apperr.New(apperr.UnexpectedResponse, "the API did not return an array of versions")
	}
	items := v.Items
	if types := inv.Strings("version-type"); len(types) > 0 {
		items = slices.DeleteFunc(slices.Clone(items), func(it jsonx.Value) bool {
			t, _ := it.Get("version_type")
			return !slices.Contains(types, t.Str)
		})
	}
	res := &output.Result{Shape: output.Array}
	if limit := inv.Int("limit"); limit > 0 && len(items) > limit {
		res.Notes = append(res.Notes, fmt.Sprintf("showing %d of %d versions (newest first); use --limit 0 for all", limit, len(items)))
		items = items[:limit]
	}
	res.Value = jsonx.Arr(items...)
	return res, nil
}

func versionGetCmd() *cli.Command {
	return &cli.Command{
		Name:    "get",
		Summary: "Get one version by id, or by project and version number",
		Long: "With one argument, looks the version up by id. With two, looks up the version of the project " +
			"with that version number.",
		Args: []cli.Arg{
			{Name: "id", Required: true, Help: "version id, or the project id or slug when <number> is given"},
			{Name: "number", Help: "version number, e.g. mc1.20.1-0.5.11", Validate: validate.VersionNumber},
		},
		Endpoints: []cli.Endpoint{
			{Method: "GET", Path: "/version/{id}"},
			{Method: "GET", Path: "/project/{id|slug}/version/{id|number}"},
		},
		Output:   versionGetSpec(),
		Examples: []string{"mdr version get ygf8cVZg", "mdr version get sodium mc1.20.1-0.5.11"},
		Errors:   []apperr.Code{apperr.NotFound},
		Run:      runVersionGet,
	}
}

func runVersionGet(ctx context.Context, inv *cli.Invocation) (*output.Result, error) {
	id, number := inv.Arg("id"), inv.Arg("number")
	var v jsonx.Value
	var err error
	if number == "" {
		if err := validate.VersionID(id); err != nil {
			return nil, apperr.From(err).
				WithHint("to look up by version number, pass the project too: mdr version get <project> <number>")
		}
		v, err = inv.Client.GetVersion(ctx, id)
	} else {
		if err := validate.ProjectRef(id); err != nil {
			return nil, err
		}
		v, err = inv.Client.GetVersionByNumber(ctx, id, number)
	}
	if err != nil {
		return nil, err
	}
	return &output.Result{Shape: output.Object, Value: v}, nil
}

func versionGetManyCmd() *cli.Command {
	return &cli.Command{
		Name:    "get-many",
		Summary: "Get several versions by id",
		Long: "Fetches up to 1000 versions, 100 per request. The output keeps the order of the arguments; " +
			"ids that match nothing are left out and listed in a note on stderr (exit 0).",
		Args: []cli.Arg{
			{Name: "id", Required: true, Variadic: true, Help: "version ids (1-1000)", Validate: validate.VersionID},
		},
		Endpoints: []cli.Endpoint{{Method: "GET", Path: "/versions"}},
		Output:    versionListSpec(),
		Examples:  []string{"mdr version get-many ygf8cVZg OihdIimA"},
		Run: func(ctx context.Context, inv *cli.Invocation) (*output.Result, error) {
			ids := inv.ArgList("id")
			if err := checkBulkCount(ids); err != nil {
				return nil, err
			}
			res, err := inv.Client.GetVersions(ctx, ids)
			if err != nil {
				return nil, err
			}
			return bulkResult(res), nil
		},
	}
}

var downloadFields = []string{"version_id", "filename", "path", "size", "sha512", "status"}

func versionDownloadCmd() *cli.Command {
	return &cli.Command{
		Name:    "download",
		Summary: "Download the files of a version and verify their hashes",
		Long: "Downloads the primary file of the version (or --file / --all) from the Modrinth CDN into --dir, " +
			"which must exist. Each file is written to a temporary file in --dir, checked against the size and " +
			"hash the API gives, and renamed into place; nothing is left behind on failure. A file that is " +
			"already there with the same content is not downloaded again (status exists); one with different " +
			"content is an error (file_exists) unless --force. No token is sent to the CDN.",
		Args: []cli.Arg{{Name: "id", Required: true, Help: "version id", Validate: validate.VersionID}},
		Flags: []cli.Flag{
			{Name: "dir", Kind: cli.KindString, Default: ".", Value: "<dir>", Help: "existing directory to save into", Validate: validate.Dir},
			{Name: "file", Kind: cli.KindString, Value: "<name>", Help: "only the file with this exact file name"},
			{Name: "all", Kind: cli.KindBool, Help: "all files of the version, not only the primary one"},
			{Name: "force", Kind: cli.KindBool, Help: "overwrite existing files with different content"},
		},
		Endpoints: []cli.Endpoint{{Method: "GET", Path: "/version/{id}"}},
		Output: &output.Spec{
			Shape:  output.Array,
			Fields: downloadFields,
			Table: []output.Column{
				{Header: "FILE", Path: "filename", Max: 44},
				{Header: "SIZE", Path: "size", Kind: output.KindNum},
				{Header: "STATUS", Path: "status"},
				{Header: "PATH", Path: "path"},
			},
		},
		Examples: []string{
			"mdr version download ygf8cVZg --dir mods",
			"mdr version download ygf8cVZg --all --dir mods --force",
		},
		Errors: []apperr.Code{apperr.NotFound, apperr.FileExists, apperr.HashMismatch, apperr.IOError, apperr.Interrupted},
		Run:    runVersionDownload,
	}
}

func runVersionDownload(ctx context.Context, inv *cli.Invocation) (*output.Result, error) {
	id, name := inv.Arg("id"), inv.String("file")
	if name != "" && inv.Bool("all") {
		return nil, apperr.New(apperr.Usage, "--file and --all cannot be combined")
	}
	raw, err := inv.Client.GetVersionForDownload(ctx, id)
	if err != nil {
		return nil, err
	}
	v, err := api.DecodeVersion(raw)
	if err != nil {
		return nil, err
	}
	files, err := pickFiles(v, id, name, inv.Bool("all"))
	if err != nil {
		return nil, err
	}

	info := inv.Client.Info()
	opts := download.Options{
		Dir:         inv.String("dir"),
		Force:       inv.Bool("force"),
		Staging:     inv.Settings.Staging,
		APIURL:      info.BaseURL,
		IdleTimeout: info.Timeout,
		DryRun:      info.DryRun,
		Doer:        inv.Doer,
		UserAgent:   info.UserAgent,
		Log:         info.Log,
	}
	if inv.IO.StderrIsTTY && !inv.Globals.Quiet {
		opts.Progress = progressLine(inv.IO.Stderr)
	}
	items := make([]jsonx.Value, 0, len(files))
	saved := 0
	for _, f := range files {
		out, err := download.Fetch(ctx, download.Item{
			URL: f.URL, Filename: f.Filename, Size: f.Size, SHA512: f.Hashes.SHA512, SHA1: f.Hashes.SHA1,
		}, opts)
		if opts.Progress != nil {
			fmt.Fprint(inv.IO.Stderr, "\r\x1b[K")
		}
		if err != nil {
			if saved > 0 {
				inv.Log.Note("%d of %d files were saved before the error", saved, len(files))
			}
			return nil, err
		}
		if out.Status == download.StatusDownloaded {
			saved++
		}
		items = append(items, jsonx.Obj(
			member("version_id", jsonx.Str(v.ID)),
			member("filename", jsonx.Str(out.Filename)),
			member("path", jsonx.Str(out.Path)),
			member("size", jsonx.Int(out.Size)),
			member("sha512", jsonx.Str(out.SHA512)),
			member("status", jsonx.Str(out.Status)),
		))
	}
	return &output.Result{Shape: output.Array, Value: jsonx.Arr(items...)}, nil
}

// pickFiles selects the files to download: the primary one, the one named name, or all.
func pickFiles(v api.Version, id, name string, all bool) ([]api.VersionFile, error) {
	if len(v.Files) == 0 {
		return nil, apperr.New(apperr.NotFound, "version '%s' has no files", id)
	}
	switch {
	case all:
		return v.Files, nil
	case name != "":
		var names []string
		for _, f := range v.Files {
			if f.Filename == name {
				return []api.VersionFile{f}, nil
			}
			names = append(names, f.Filename)
		}
		return nil, apperr.New(apperr.NotFound, "version '%s' has no file named '%s'", id, name).
			WithHint("its files: %s", strings.Join(names, ", "))
	}
	f, _ := v.PrimaryFile()
	return []api.VersionFile{f}, nil
}

// progressLine rewrites one stderr line: "sodium.jar  1.2MiB / 3.4MiB".
func progressLine(w io.Writer) func(string, int64, int64) {
	return func(name string, done, total int64) {
		if total > 0 {
			fmt.Fprintf(w, "\r%s  %s / %s", name, mib(done), mib(total))
		} else {
			fmt.Fprintf(w, "\r%s  %s", name, mib(done))
		}
	}
}

func mib(n int64) string { return fmt.Sprintf("%.1fMiB", float64(n)/(1<<20)) }
