package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/s8a-uk/modrinth-cli/internal/api"
	"github.com/s8a-uk/modrinth-cli/internal/apperr"
	"github.com/s8a-uk/modrinth-cli/internal/cli"
	"github.com/s8a-uk/modrinth-cli/internal/hashfile"
	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
	"github.com/s8a-uk/modrinth-cli/internal/output"
	"github.com/s8a-uk/modrinth-cli/internal/validate"
)

// maxLocalFiles is the most files file identify / file updates check in one run.
const maxLocalFiles = 5000

// hashAlgorithms are the values of --algorithm.
var hashAlgorithms = []string{"sha1", "sha512"}

// modFileExts are the extensions picked from a directory (case-insensitive).
var modFileExts = []string{".jar", ".zip", ".mrpack", ".litemod"}

const localFilesLong = " Arguments are files or directories; a directory contributes the files directly in it " +
	"(not in subdirectories) ending in .jar, .zip, .mrpack or .litemod, also with .disabled appended. " +
	"Only the hashes of the files are sent, never their content."

func fileCmd() *cli.Command {
	return &cli.Command{
		Name:    "file",
		Summary: "Look up versions by file hash (local mod files)",
		Sub:     []*cli.Command{fileGetCmd(), fileIdentifyCmd(), fileUpdatesCmd()},
	}
}

func algorithmFlag(def string) cli.Flag {
	help := "hash algorithm sent to the API"
	if def == "" {
		help = "hash algorithm; default: from the length (40 = sha1, 128 = sha512)"
	}
	return cli.Flag{Name: "algorithm", Kind: cli.KindEnum, Enum: hashAlgorithms, Default: def, Value: "<alg>", Help: help}
}

func noProjectFlag() cli.Flag {
	return cli.Flag{Name: "no-project", Kind: cli.KindBool, Help: "do not look up the project slug and title (one request fewer)"}
}

func pathsArg() cli.Arg {
	return cli.Arg{Name: "path", Required: true, Variadic: true, Help: "files or directories"}
}

func fileGetCmd() *cli.Command {
	return &cli.Command{
		Name:    "get",
		Summary: "Get the version that has a file with this hash",
		Args: []cli.Arg{{Name: "hash", Required: true, Help: "sha1 (40 hex digits) or sha512 (128 hex digits)",
			Validate: func(s string) error { _, err := validate.Hash(s, ""); return err }}},
		Flags:     []cli.Flag{algorithmFlag("")},
		Endpoints: []cli.Endpoint{{Method: "GET", Path: "/version_file/{hash}"}},
		Output:    &output.Spec{Shape: output.Object, Fields: versionFields, DefaultFields: versionListDefaultFields, Table: versionTable},
		Examples:  []string{"mdr file get c4293c1483f3b39c1c6c4c69143e8bb2c6e53daf"},
		Errors:    []apperr.Code{apperr.NotFound},
		Run: func(ctx context.Context, inv *cli.Invocation) (*output.Result, error) {
			alg := inv.String("algorithm")
			hash, err := validate.Hash(inv.Arg("hash"), alg)
			if err != nil {
				return nil, err
			}
			if alg == "" {
				alg = map[int]string{40: "sha1", 128: "sha512"}[len(hash)]
			}
			v, err := inv.Client.VersionFromHash(ctx, hash, alg)
			if err != nil {
				return nil, err
			}
			return &output.Result{Shape: output.Object, Value: v}, nil
		},
	}
}

var identifyFields = []string{
	"path", "filename", "size", "sha1", "sha512", "matched",
	"project_id", "project_slug", "project_title",
	"version_id", "version_number", "version_name", "version_type", "loaders", "game_versions", "date_published",
}

func fileIdentifyCmd() *cli.Command {
	return &cli.Command{
		Name:    "identify",
		Summary: "Find the project and version of local files",
		Long: "Hashes each file and asks Modrinth which version it belongs to, 100 hashes per request." + localFilesLong +
			" Files Modrinth does not know get matched: false; the exit code is still 0.",
		Args:  []cli.Arg{pathsArg()},
		Flags: []cli.Flag{algorithmFlag("sha512"), noProjectFlag()},
		Endpoints: []cli.Endpoint{
			{Method: "POST", Path: "/version_files"},
			{Method: "GET", Path: "/projects"},
		},
		Output: &output.Spec{
			Shape:  output.Array,
			Fields: identifyFields,
			DefaultFields: []string{"path", "matched", "project_id", "project_slug", "version_id", "version_number",
				"loaders", "game_versions"},
			Table: []output.Column{
				{Header: "FILE", Path: "filename", Max: 44},
				{Header: "MATCH", Path: "matched", Kind: output.KindBool},
				{Header: "PROJECT", Path: "project_slug", Max: 28},
				{Header: "VERSION", Path: "version_number", Max: 24},
				{Header: "LOADERS", Path: "loaders", Kind: output.KindList, Max: 20},
				{Header: "GAME", Path: "game_versions", Kind: output.KindList, Max: 20},
			},
		},
		Examples: []string{"mdr file identify mods/", "mdr file identify mods/sodium.jar --fields path,project_slug,version_number"},
		Errors:   []apperr.Code{apperr.IOError},
		Run:      runFileIdentify,
	}
}

// localFile is a file given to file identify / updates, with its hashes.
type localFile struct {
	path string
	sums hashfile.Sums
}

func (f localFile) hash(alg string) string {
	if alg == "sha1" {
		return f.sums.SHA1
	}
	return f.sums.SHA512
}

// loadFiles expands the arguments and hashes every file. Nothing is sent if any
// file cannot be read.
func loadFiles(ctx context.Context, args []string) ([]localFile, error) {
	paths, err := expandPaths(args)
	if err != nil {
		return nil, err
	}
	files := make([]localFile, len(paths))
	for i, p := range paths {
		s, err := hashfile.File(ctx, p)
		if err != nil {
			if ctx.Err() != nil {
				return nil, apperr.New(apperr.Interrupted, "interrupted").WithCause(err)
			}
			return nil, apperr.New(apperr.IOError, "cannot read %s: %s", p, pathErrText(err)).WithCause(err)
		}
		files[i] = localFile{path: p, sums: s}
	}
	return files, nil
}

// expandPaths turns the arguments into a sorted list of files without repeats. A
// directory contributes the regular files directly in it with a mod file
// extension (also when followed by .disabled); a file is taken whatever its name.
func expandPaths(args []string) ([]string, error) {
	var out []string
	for _, a := range args {
		st, err := os.Stat(a)
		if err != nil {
			return nil, apperr.New(apperr.IOError, "cannot read %s: %s", a, pathErrText(err)).WithCause(err)
		}
		if !st.IsDir() {
			out = append(out, filepath.Clean(a))
			continue
		}
		entries, err := os.ReadDir(a)
		if err != nil {
			return nil, apperr.New(apperr.IOError, "cannot read directory %s: %s", a, pathErrText(err)).WithCause(err)
		}
		for _, e := range entries {
			if !isModFile(e.Name()) {
				continue
			}
			p := filepath.Join(a, e.Name())
			if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
				out = append(out, p)
			}
		}
	}
	slices.Sort(out)
	out = slices.Compact(out)
	switch {
	case len(out) == 0:
		return nil, apperr.New(apperr.InvalidInput, "no files to check").
			WithHint("pass mod files, or directories containing .jar, .zip, .mrpack or .litemod files")
	case len(out) > maxLocalFiles:
		return nil, apperr.New(apperr.InvalidInput, "too many files: %d (at most %d)", len(out), maxLocalFiles).
			WithHint("split the files over several calls")
	}
	return out, nil
}

func isModFile(name string) bool {
	name = strings.ToLower(name)
	name = strings.TrimSuffix(name, ".disabled")
	return slices.Contains(modFileExts, filepath.Ext(name))
}

func pathErrText(err error) string {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}

func hashesOf(files []localFile, alg string) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.hash(alg)
	}
	return out
}

// projectsOf looks up the projects of the versions (unless --no-project), keyed by id.
func projectsOf(ctx context.Context, inv *cli.Invocation, versions ...map[string]jsonx.Value) (map[string]jsonx.Value, error) {
	out := map[string]jsonx.Value{}
	if inv.Bool("no-project") {
		return out, nil
	}
	var ids []string
	for _, m := range versions {
		for _, v := range m {
			if id, ok := v.Get("project_id"); ok && id.Kind == jsonx.String && !slices.Contains(ids, id.Str) {
				ids = append(ids, id.Str)
			}
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	slices.Sort(ids) // deterministic requests
	res, err := inv.Client.GetProjects(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, p := range res.Found {
		if id, ok := p.Get("id"); ok {
			out[id.Str] = p
		}
	}
	return out, nil
}

// get returns v[key], or null.
func get(v jsonx.Value, key string) jsonx.Value {
	x, _ := v.Get(key)
	return x
}

func member(key string, v jsonx.Value) jsonx.Member { return jsonx.Member{Key: key, Value: v} }

// fileMembers are the members describing the local file itself.
func fileMembers(f localFile, withSHA1 bool) []jsonx.Member {
	ms := []jsonx.Member{
		member("path", jsonx.Str(f.path)),
		member("filename", jsonx.Str(filepath.Base(f.path))),
		member("size", jsonx.Int(f.sums.Size)),
	}
	if withSHA1 {
		ms = append(ms, member("sha1", jsonx.Str(f.sums.SHA1)))
	}
	return append(ms, member("sha512", jsonx.Str(f.sums.SHA512)))
}

// projectMembers are project_id, project_slug and project_title of a version
// (null when there is no version; slug and title null without the project).
func projectMembers(v jsonx.Value, projects map[string]jsonx.Value) []jsonx.Member {
	pid := get(v, "project_id")
	p := projects[pid.Str]
	return []jsonx.Member{member("project_id", pid), member("project_slug", get(p, "slug")), member("project_title", get(p, "title"))}
}

func runFileIdentify(ctx context.Context, inv *cli.Invocation) (*output.Result, error) {
	alg := inv.String("algorithm")
	files, err := loadFiles(ctx, inv.ArgList("path"))
	if err != nil {
		return nil, err
	}
	found, err := inv.Client.VersionsFromHashes(ctx, hashesOf(files, alg), alg)
	if err != nil {
		return nil, err
	}
	projects, err := projectsOf(ctx, inv, found)
	if err != nil {
		return nil, err
	}
	items := make([]jsonx.Value, len(files))
	missing := 0
	for i, f := range files {
		v, ok := found[f.hash(alg)]
		if !ok {
			missing++
		}
		ms := append(fileMembers(f, true), member("matched", jsonx.BoolV(ok)))
		ms = append(ms, projectMembers(v, projects)...)
		ms = append(ms,
			member("version_id", get(v, "id")),
			member("version_number", get(v, "version_number")),
			member("version_name", get(v, "name")),
			member("version_type", get(v, "version_type")),
			member("loaders", get(v, "loaders")),
			member("game_versions", get(v, "game_versions")),
			member("date_published", get(v, "date_published")),
		)
		items[i] = jsonx.Obj(ms...)
	}
	res := &output.Result{Shape: output.Array, Value: jsonx.Arr(items...)}
	if missing > 0 {
		res.Notes = append(res.Notes, fmt.Sprintf("%d of %d files not found on Modrinth", missing, len(files)))
	}
	return res, nil
}

// Statuses of file updates.
const (
	statusUpToDate        = "up_to_date"
	statusUpdateAvailable = "update_available"
	statusNoCompatible    = "no_compatible_version"
	statusUnknown         = "unknown"
)

var updatesFields = []string{
	"path", "filename", "size", "sha512", "status",
	"project_id", "project_slug", "project_title",
	"current.version_id", "current.version_number", "current.date_published",
	"latest.version_id", "latest.version_number", "latest.version_type", "latest.date_published",
	"latest.filename", "latest.url", "latest.size", "latest.sha512",
}

func fileUpdatesCmd() *cli.Command {
	return &cli.Command{
		Name:    "updates",
		Summary: "Check local files for newer versions",
		Long: "Looks up the version of each file, then the newest version of its project for the given loaders " +
			"and game versions (both required by the API)." + localFilesLong + " status is up_to_date, " +
			"update_available, no_compatible_version (nothing for these loaders and game versions) or unknown " +
			"(not on Modrinth); the exit code is 0 either way. Nothing is downloaded: pass latest.version_id " +
			"to mdr version download.",
		Args: []cli.Arg{pathsArg()},
		Flags: []cli.Flag{
			{Name: "loader", Kind: cli.KindStrings, Value: "<loader>", Help: "loader, e.g. fabric (repeatable, required)", Validate: validate.Tag},
			{Name: "game-version", Kind: cli.KindStrings, Value: "<version>", Help: "game version, e.g. 1.20.1 (repeatable, required)", Validate: validate.Tag},
			{Name: "version-type", Kind: cli.KindStrings, Value: "<type>", Help: "release, beta or alpha (repeatable); default: all", Validate: oneOf(versionTypes)},
			algorithmFlag("sha512"),
			{Name: "only-updates", Kind: cli.KindBool, Help: "only output files with status update_available"},
			noProjectFlag(),
		},
		Endpoints: []cli.Endpoint{
			{Method: "POST", Path: "/version_files"},
			{Method: "POST", Path: "/version_files/update"},
			{Method: "GET", Path: "/projects"},
		},
		Output: &output.Spec{
			Shape:  output.Array,
			Fields: updatesFields,
			DefaultFields: []string{"path", "status", "project_id", "project_slug", "current.version_id",
				"current.version_number", "latest.version_id", "latest.version_number", "latest.filename"},
			Table: []output.Column{
				{Header: "FILE", Path: "filename", Max: 40},
				{Header: "STATUS", Path: "status"},
				{Header: "PROJECT", Path: "project_slug", Max: 28},
				{Header: "CURRENT", Path: "current.version_number", Max: 22},
				{Header: "LATEST", Path: "latest.version_number", Max: 22},
			},
		},
		Examples: []string{
			"mdr file updates mods/ --loader fabric --game-version 1.20.1",
			"mdr file updates mods/ --loader fabric --game-version 1.20.1 --version-type release --only-updates",
		},
		Errors: []apperr.Code{apperr.IOError},
		Run:    runFileUpdates,
	}
}

func runFileUpdates(ctx context.Context, inv *cli.Invocation) (*output.Result, error) {
	loaders, games := inv.Strings("loader"), inv.Strings("game-version")
	if len(loaders) == 0 || len(games) == 0 {
		return nil, apperr.New(apperr.Usage, "--loader and --game-version are required").
			WithHint("both are required by the API, e.g. --loader fabric --game-version 1.20.1")
	}
	alg := inv.String("algorithm")
	files, err := loadFiles(ctx, inv.ArgList("path"))
	if err != nil {
		return nil, err
	}
	hashes := hashesOf(files, alg)
	current, err := inv.Client.VersionsFromHashes(ctx, hashes, alg)
	if err != nil {
		return nil, err
	}
	var known []string
	for _, h := range hashes {
		if _, ok := current[h]; ok {
			known = append(known, h)
		}
	}
	latest := map[string]jsonx.Value{}
	if len(known) > 0 {
		latest, err = inv.Client.LatestFromHashes(ctx, known, alg, api.UpdateFilter{
			Loaders: loaders, GameVersions: games, VersionTypes: inv.Strings("version-type"),
		})
		if err != nil {
			return nil, err
		}
	}
	projects, err := projectsOf(ctx, inv, current)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	var items []jsonx.Value
	for _, f := range files {
		h := f.hash(alg)
		cur, isKnown := current[h]
		lat, hasLatest := latest[h]
		status := statusUnknown
		switch {
		case !isKnown:
		case !hasLatest:
			status = statusNoCompatible
		case get(lat, "id").Str == get(cur, "id").Str:
			status = statusUpToDate
		default:
			status = statusUpdateAvailable
		}
		counts[status]++
		if inv.Bool("only-updates") && status != statusUpdateAvailable {
			continue
		}
		ms := append(fileMembers(f, false), member("status", jsonx.Str(status)))
		ms = append(ms, projectMembers(cur, projects)...)
		ms = append(ms, member("current", currentValue(cur, isKnown)), member("latest", latestValue(lat, hasLatest)))
		items = append(items, jsonx.Obj(ms...))
	}
	note := fmt.Sprintf("%s available, %d up to date, %d unknown",
		plural(counts[statusUpdateAvailable], "update", "updates"), counts[statusUpToDate], counts[statusUnknown])
	if n := counts[statusNoCompatible]; n > 0 {
		note += fmt.Sprintf(", %d without a compatible version", n)
	}
	return &output.Result{Shape: output.Array, Value: jsonx.Arr(items...), Notes: []string{note}}, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func currentValue(v jsonx.Value, ok bool) jsonx.Value {
	if !ok {
		return jsonx.Value{}
	}
	return jsonx.Obj(
		member("version_id", get(v, "id")),
		member("version_number", get(v, "version_number")),
		member("date_published", get(v, "date_published")),
	)
}

// latestValue describes the newest version and its primary file (else the first).
func latestValue(v jsonx.Value, ok bool) jsonx.Value {
	if !ok {
		return jsonx.Value{}
	}
	var file jsonx.Value
	if files := get(v, "files"); files.Kind == jsonx.Array && len(files.Items) > 0 {
		file = files.Items[0]
		for _, f := range files.Items {
			if p := get(f, "primary"); p.Kind == jsonx.Bool && p.Bool {
				file = f
				break
			}
		}
	}
	sha512, _ := file.Lookup("hashes.sha512")
	return jsonx.Obj(
		member("version_id", get(v, "id")),
		member("version_number", get(v, "version_number")),
		member("version_type", get(v, "version_type")),
		member("date_published", get(v, "date_published")),
		member("filename", get(file, "filename")),
		member("url", get(file, "url")),
		member("size", get(file, "size")),
		member("sha512", sha512),
	)
}
