package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/s8a-uk/modrinth-cli/internal/api"
	"github.com/s8a-uk/modrinth-cli/internal/apperr"
	"github.com/s8a-uk/modrinth-cli/internal/cli"
	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
	"github.com/s8a-uk/modrinth-cli/internal/output"
	"github.com/s8a-uk/modrinth-cli/internal/validate"
)

// maxInputBytes is the largest JSON document read from --data, --input or
// --changelog-file.
const maxInputBytes = 16 << 20

// maxUploadFiles is the most files one version create / add-file takes.
const maxUploadFiles = 32

var (
	dependencyTypes = []string{"required", "optional", "incompatible", "embedded"}
	versionStatuses = []string{"listed", "archived", "draft", "unlisted"}
	reFieldKey      = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	reJSONNumber    = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)
)

// writeErrors are the errors every write command can return besides the common ones.
var writeErrors = []apperr.Code{apperr.AuthRequired, apperr.Unauthorized, apperr.Forbidden, apperr.NotFound, apperr.BadRequest}

// setFlagHelp is the help of --set.
const setFlagHelp = "set a field: key=value (repeatable); true/false/null, numbers and JSON " +
	"([...], {...}, \"...\") are sent as JSON, anything else as a string"

// editLong is the shared description of version edit / project edit.
const editLong = "Sends only the given fields (PATCH). Values of --set are read like api -f. The default " +
	"output of get does not include long text fields such as body or changelog; fetch them with " +
	"--fields body (or --fields changelog) before editing them. Prints an ack on success."

func dataFlag(help string) cli.Flag {
	return cli.Flag{Name: "data", Kind: cli.KindString, Value: "<file|->", Help: help}
}

func setFlag() cli.Flag {
	return cli.Flag{Name: "set", Kind: cli.KindStrings, Value: "<key=value>", Help: setFlagHelp, Validate: fieldPair}
}

// fieldPair accepts key=value with a simple key (no nesting).
func fieldPair(s string) error {
	k, _, ok := strings.Cut(s, "=")
	if !ok || !reFieldKey.MatchString(k) {
		return errors.New("use key=value with a key of letters, digits, _ or - (nest with key={...})")
	}
	return nil
}

// fieldValue reads the value of api -f / --set: true, false, null, numbers and
// strings that start with [, { or " and parse as JSON are JSON; the rest is a string.
func fieldValue(s string) jsonx.Value {
	switch s {
	case "true":
		return jsonx.BoolV(true)
	case "false":
		return jsonx.BoolV(false)
	case "null":
		return jsonx.Value{}
	}
	if reJSONNumber.MatchString(s) || strings.HasPrefix(s, "[") || strings.HasPrefix(s, "{") || strings.HasPrefix(s, `"`) {
		if v, err := jsonx.Parse([]byte(s)); err == nil {
			return v
		}
	}
	return jsonx.Str(s)
}

// fieldsObject builds the JSON object of key=value pairs (a repeated key: last wins).
func fieldsObject(pairs []string) jsonx.Value {
	obj := jsonx.Obj()
	for _, p := range pairs {
		k, v, _ := strings.Cut(p, "=")
		obj.Set(k, fieldValue(v))
	}
	return obj
}

// readInput reads a file, or stdin for "-", up to maxInputBytes.
func readInput(inv *cli.Invocation, flag, path string) ([]byte, error) {
	var r io.Reader
	if path == "-" {
		r = inv.IO.Stdin
	} else {
		f, err := os.Open(path)
		if err != nil {
			return nil, apperr.New(apperr.InvalidInput, "cannot read --%s %s: %s", flag, path, pathErrText(err)).WithCause(err)
		}
		defer f.Close()
		r = f
	}
	b, err := io.ReadAll(io.LimitReader(r, maxInputBytes+1))
	if err != nil {
		return nil, apperr.New(apperr.IOError, "cannot read --%s: %s", flag, err.Error()).WithCause(err)
	}
	if len(b) > maxInputBytes {
		return nil, apperr.New(apperr.InvalidInput, "--%s is larger than %d MiB", flag, maxInputBytes>>20)
	}
	return b, nil
}

// readDataObject reads --data: a JSON object.
func readDataObject(inv *cli.Invocation) (jsonx.Value, error) {
	b, err := readInput(inv, "data", inv.String("data"))
	if err != nil {
		return jsonx.Value{}, err
	}
	v, err := jsonx.Parse(b)
	if err != nil {
		return jsonx.Value{}, apperr.New(apperr.InvalidInput, "--data is not valid JSON: %s", err.Error()).WithCause(err)
	}
	if v.Kind != jsonx.Object {
		return jsonx.Value{}, apperr.New(apperr.InvalidInput, "--data must be a JSON object")
	}
	return v, nil
}

// editBody is the PATCH body from --set or --data (exactly one of them).
func editBody(inv *cli.Invocation) (jsonx.Value, error) {
	set, data := inv.Changed("set"), inv.Changed("data")
	switch {
	case set && data:
		return jsonx.Value{}, apperr.New(apperr.Usage, "--set and --data cannot be combined")
	case data:
		return readDataObject(inv)
	case set:
		return fieldsObject(inv.Strings("set")), nil
	}
	return jsonx.Value{}, apperr.New(apperr.Usage, "nothing to change").WithHint("pass --set key=value or --data <file|->")
}

// uploads checks the files to upload and returns them with their sizes.
func uploads(paths []string, exts []string) ([]api.Upload, error) {
	if len(paths) > maxUploadFiles {
		return nil, apperr.New(apperr.Usage, "too many files: %d (at most %d)", len(paths), maxUploadFiles)
	}
	out := make([]api.Upload, 0, len(paths))
	for _, p := range paths {
		if err := validate.UploadFile(p, exts); err != nil {
			return nil, err
		}
		st, err := os.Stat(p)
		if err != nil {
			return nil, apperr.New(apperr.IOError, "cannot read %s: %s", p, pathErrText(err)).WithCause(err)
		}
		out = append(out, api.Upload{Path: p, Size: st.Size()})
	}
	return out, nil
}

// uploadProgress is the progress line of an upload on a terminal, or nil.
func uploadProgress(inv *cli.Invocation) (func(done, total int64), func()) {
	if !inv.IO.StderrIsTTY || inv.Globals.Quiet {
		return nil, func() {}
	}
	w := inv.IO.Stderr
	return func(done, total int64) { fmt.Fprintf(w, "\ruploading  %s / %s", mib(done), mib(total)) },
		func() { fmt.Fprint(w, "\r\x1b[K") }
}

// ack is {"ok":true,"action":...,"resource":...,"id":...}.
func ack(action, resource, id string) *output.Result {
	return &output.Result{Shape: output.Ack, Value: jsonx.Obj(
		member("ok", jsonx.BoolV(true)),
		member("action", jsonx.Str(action)),
		member("resource", jsonx.Str(resource)),
		member("id", jsonx.Str(id)),
	)}
}

func versionIDArg() cli.Arg {
	return cli.Arg{Name: "id", Required: true, Help: "version id", Validate: validate.VersionID}
}

// ---- version create ----

// versionContentFlags are the flags that build data; --data excludes them.
var versionContentFlags = []string{"version-number", "name", "loader", "game-version", "version-type",
	"changelog", "changelog-file", "dependency", "featured", "status"}

func versionCreateCmd() *cli.Command {
	return &cli.Command{
		Name:    "create",
		Summary: "Upload a new version of a project",
		Long: "Uploads the files (--file, repeatable) and the version metadata as one multipart request. " +
			"A project slug is first resolved to its id (GET /project/{slug}/check, also with --dry-run); an " +
			"8-character id is used as is. The first --file is the primary file unless --primary names " +
			"another. --data passes the whole metadata object instead of the content flags; mdr still sets " +
			"project_id, file_parts and primary_file. Prints the created version.",
		Args: []cli.Arg{
			{Name: "project", Required: true, Help: "project id or slug", Validate: validate.ProjectRef},
		},
		Flags: []cli.Flag{
			{Name: "file", Kind: cli.KindStrings, Value: "<path>", Help: "file to upload: .jar, .zip, .mrpack or .litemod (repeatable, required)"},
			{Name: "version-number", Kind: cli.KindString, Value: "<number>", Help: "version number, e.g. 1.2.0 (required)", Validate: validate.NewVersionNumber},
			{Name: "name", Kind: cli.KindString, Value: "<name>", Help: "display name (default: the version number)"},
			{Name: "loader", Kind: cli.KindStrings, Value: "<loader>", Help: "loader, e.g. fabric (repeatable, required)", Validate: validate.Tag},
			{Name: "game-version", Kind: cli.KindStrings, Value: "<version>", Help: "game version, e.g. 1.20.1 (repeatable, required)", Validate: validate.Tag},
			{Name: "version-type", Kind: cli.KindEnum, Enum: versionTypes, Default: "release", Help: "release, beta or alpha"},
			{Name: "changelog", Kind: cli.KindString, Value: "<text>", Help: "changelog (Markdown)"},
			{Name: "changelog-file", Kind: cli.KindString, Value: "<path|->", Help: "read the changelog from a file or stdin"},
			{Name: "dependency", Kind: cli.KindStrings, Value: "<project>:<type>", Help: "dependency; type is required, optional, incompatible or embedded (repeatable)", Validate: dependencyFlag},
			{Name: "featured", Kind: cli.KindBool, Help: "feature the version"},
			{Name: "status", Kind: cli.KindString, Value: "<status>", Help: "listed, archived, draft or unlisted", Validate: oneOf(versionStatuses)},
			{Name: "primary", Kind: cli.KindString, Value: "<path>", Help: "the --file that is the primary file (default: the first)"},
			dataFlag("version metadata as a JSON object, from a file or stdin (instead of the content flags)"),
		},
		Endpoints: []cli.Endpoint{
			{Method: "POST", Path: "/version", Scope: api.ScopeVersionCreate},
			{Method: "GET", Path: "/project/{id|slug}/check"},
		},
		Output:   versionGetSpec(),
		Mutation: cli.Write,
		Examples: []string{
			"mdr version create my-mod --file build/libs/my-mod-1.2.0.jar --version-number 1.2.0 --loader fabric --game-version 1.20.1",
			"mdr version create AABBCCDD --file a.jar --file a-sources.jar --version-number 1.2.0 --loader fabric --game-version 1.20.1 --version-type beta --changelog-file CHANGELOG.md --dry-run",
		},
		Errors: writeErrors,
		Run:    runVersionCreate,
	}
}

// dependencyFlag accepts <project>:<type>.
func dependencyFlag(s string) error {
	i := strings.LastIndexByte(s, ':')
	if i < 0 {
		return errors.New("use <project>:<type>, e.g. fabric-api:required")
	}
	if err := validate.ProjectRef(s[:i]); err != nil {
		return errors.New("invalid project in " + s)
	}
	return oneOf(dependencyTypes)(s[i+1:])
}

func runVersionCreate(ctx context.Context, inv *cli.Invocation) (*output.Result, error) {
	paths := inv.Strings("file")
	if len(paths) == 0 {
		return nil, apperr.New(apperr.Usage, "--file is required").WithHint("pass the file to upload: --file build/libs/mod.jar")
	}
	files, err := uploads(paths, validate.VersionFileExts)
	if err != nil {
		return nil, err
	}
	primary := 0
	if p := inv.String("primary"); p != "" {
		primary = slices.Index(paths, p)
		if primary < 0 {
			return nil, apperr.New(apperr.Usage, "--primary %s is not one of the --file values", p)
		}
	}

	var data jsonx.Value
	if inv.Changed("data") {
		for _, f := range versionContentFlags {
			if inv.Changed(f) {
				return nil, apperr.New(apperr.Usage, "--data cannot be combined with --%s", f)
			}
		}
		if data, err = readDataObject(inv); err != nil {
			return nil, err
		}
	} else if data, err = versionData(ctx, inv); err != nil {
		return nil, err
	}

	projectID, err := inv.Client.ResolveProjectID(ctx, inv.Arg("project"))
	if err != nil {
		return nil, err
	}
	data.Set("project_id", jsonx.Str(projectID))
	data.Set("file_parts", jsonx.Strs(api.FilePartNames(len(files))))
	data.Set("primary_file", jsonx.Str(api.FilePartName(primary)))

	progress, done := uploadProgress(inv)
	v, err := inv.Client.CreateVersion(ctx, data, files, progress)
	done()
	if err != nil {
		return nil, err
	}
	return &output.Result{Shape: output.Object, Value: v}, nil
}

// versionData builds the data object from the content flags, in the key order of
// the API documentation. project_id, file_parts and primary_file are added later.
func versionData(ctx context.Context, inv *cli.Invocation) (jsonx.Value, error) {
	number := inv.String("version-number")
	switch {
	case number == "":
		return jsonx.Value{}, apperr.New(apperr.Usage, "--version-number is required")
	case len(inv.Strings("loader")) == 0:
		return jsonx.Value{}, apperr.New(apperr.Usage, "--loader is required").WithHint("e.g. --loader fabric (see: mdr tag list loader)")
	case len(inv.Strings("game-version")) == 0:
		return jsonx.Value{}, apperr.New(apperr.Usage, "--game-version is required").WithHint("e.g. --game-version 1.20.1")
	case inv.Changed("changelog") && inv.Changed("changelog-file"):
		return jsonx.Value{}, apperr.New(apperr.Usage, "--changelog and --changelog-file cannot be combined")
	}
	name := inv.String("name")
	if name == "" {
		name = number
	}
	changelog, hasChangelog := inv.String("changelog"), inv.Changed("changelog")
	if path := inv.String("changelog-file"); path != "" {
		b, err := readInput(inv, "changelog-file", path)
		if err != nil {
			return jsonx.Value{}, err
		}
		changelog, hasChangelog = string(b), true
	}
	deps := []jsonx.Value{}
	for _, d := range inv.Strings("dependency") {
		i := strings.LastIndexByte(d, ':')
		id, err := inv.Client.ResolveProjectID(ctx, d[:i])
		if err != nil {
			return jsonx.Value{}, err
		}
		deps = append(deps, jsonx.Obj(member("project_id", jsonx.Str(id)), member("dependency_type", jsonx.Str(d[i+1:]))))
	}

	data := jsonx.Obj(member("name", jsonx.Str(name)), member("version_number", jsonx.Str(number)))
	if hasChangelog {
		data.Set("changelog", jsonx.Str(changelog))
	}
	data.Set("dependencies", jsonx.Arr(deps...))
	data.Set("game_versions", jsonx.Strs(inv.Strings("game-version")))
	data.Set("version_type", jsonx.Str(inv.String("version-type")))
	data.Set("loaders", jsonx.Strs(inv.Strings("loader")))
	data.Set("featured", jsonx.BoolV(inv.Bool("featured")))
	if s := inv.String("status"); s != "" {
		data.Set("status", jsonx.Str(s))
	}
	return data, nil
}

// ---- version add-file / edit / delete ----

func versionAddFileCmd() *cli.Command {
	return &cli.Command{
		Name:    "add-file",
		Summary: "Upload more files to an existing version",
		Long:    "Uploads the files (--file, repeatable) to the version as one multipart request. Prints an ack on success.",
		Args:    []cli.Arg{versionIDArg()},
		Flags: []cli.Flag{
			{Name: "file", Kind: cli.KindStrings, Value: "<path>", Help: "file to upload: .jar, .zip, .mrpack or .litemod (repeatable, required)"},
		},
		Endpoints: []cli.Endpoint{{Method: "POST", Path: "/version/{id}/file", Scope: api.ScopeVersionWrite}},
		Output:    &output.Spec{Shape: output.Ack},
		Mutation:  cli.Write,
		Examples:  []string{"mdr version add-file IIJJKKLL --file build/libs/my-mod-1.2.0-sources.jar"},
		Errors:    writeErrors,
		Run: func(ctx context.Context, inv *cli.Invocation) (*output.Result, error) {
			paths := inv.Strings("file")
			if len(paths) == 0 {
				return nil, apperr.New(apperr.Usage, "--file is required")
			}
			files, err := uploads(paths, validate.VersionFileExts)
			if err != nil {
				return nil, err
			}
			id := inv.Arg("id")
			progress, done := uploadProgress(inv)
			err = inv.Client.AddVersionFiles(ctx, id, files, progress)
			done()
			if err != nil {
				return nil, err
			}
			return ack("add-file", "version", id), nil
		},
	}
}

func versionEditCmd() *cli.Command {
	return &cli.Command{
		Name:      "edit",
		Summary:   "Change fields of a version",
		Long:      editLong,
		Args:      []cli.Arg{versionIDArg()},
		Flags:     []cli.Flag{setFlag(), dataFlag("fields to change as a JSON object, from a file or stdin (instead of --set)")},
		Endpoints: []cli.Endpoint{{Method: "PATCH", Path: "/version/{id}", Scope: api.ScopeVersionWrite}},
		Output:    &output.Spec{Shape: output.Ack},
		Mutation:  cli.Write,
		Examples: []string{
			"mdr version edit IIJJKKLL --set featured=true --set version_type=beta",
			"mdr version edit IIJJKKLL --set 'game_versions=[\"1.20.1\",\"1.20.2\"]' --dry-run",
		},
		Errors: writeErrors,
		Run: func(ctx context.Context, inv *cli.Invocation) (*output.Result, error) {
			body, err := editBody(inv)
			if err != nil {
				return nil, err
			}
			id := inv.Arg("id")
			if err := inv.Client.EditVersion(ctx, id, body); err != nil {
				return nil, err
			}
			return ack("edit", "version", id), nil
		},
	}
}

func versionDeleteCmd() *cli.Command {
	return &cli.Command{
		Name:      "delete",
		Summary:   "Delete a version and its files",
		Long:      "Deletes the version. On a terminal it asks you to type the id; otherwise it needs --yes.",
		Args:      []cli.Arg{versionIDArg()},
		Endpoints: []cli.Endpoint{{Method: "DELETE", Path: "/version/{id}", Scope: api.ScopeVersionDelete}},
		Output:    &output.Spec{Shape: output.Ack},
		Mutation:  cli.Destructive,
		Examples:  []string{"mdr version delete IIJJKKLL --yes", "mdr version delete IIJJKKLL --dry-run"},
		Errors:    append(slices.Clone(writeErrors), apperr.ConfirmationRequired),
		Run: func(ctx context.Context, inv *cli.Invocation) (*output.Result, error) {
			id := inv.Arg("id")
			if err := inv.Client.DeleteVersion(ctx, id); err != nil {
				return nil, err
			}
			return ack("delete", "version", id), nil
		},
	}
}

// ---- project create / edit / delete ----

func projectCreateCmd() *cli.Command {
	return &cli.Command{
		Name:    "create",
		Summary: "Create a project from a JSON description",
		Long: "Creates a project from --data, a JSON object with the fields of the API (slug, title, " +
			"description, body, categories, client_side, server_side, license_id, project_type, ...; set " +
			"is_draft to true), and an optional --icon. Prints the created project.",
		Flags: []cli.Flag{
			dataFlag("project as a JSON object, from a file or stdin (required)"),
			{Name: "icon", Kind: cli.KindString, Value: "<path>", Help: "icon image: .png, .jpg, .jpeg, .bmp, .gif, .webp, .svg, .svgz or .rgb"},
		},
		Endpoints: []cli.Endpoint{{Method: "POST", Path: "/project", Scope: api.ScopeProjectCreate}},
		Output:    projectSpec(),
		Mutation:  cli.Write,
		Examples:  []string{"mdr project create --data project.json --icon icon.png --dry-run"},
		Errors:    writeErrors,
		Run: func(ctx context.Context, inv *cli.Invocation) (*output.Result, error) {
			if !inv.Changed("data") {
				return nil, apperr.New(apperr.Usage, "--data is required").WithHint("pass the project as JSON: --data project.json")
			}
			var icon *api.Upload
			if p := inv.String("icon"); p != "" {
				ups, err := uploads([]string{p}, validate.ImageExts)
				if err != nil {
					return nil, err
				}
				icon = &ups[0]
			}
			data, err := readDataObject(inv)
			if err != nil {
				return nil, err
			}
			progress, done := uploadProgress(inv)
			v, err := inv.Client.CreateProject(ctx, data, icon, progress)
			done()
			if err != nil {
				return nil, err
			}
			return &output.Result{Shape: output.Object, Value: v}, nil
		},
	}
}

func projectEditCmd() *cli.Command {
	return &cli.Command{
		Name:      "edit",
		Summary:   "Change fields of a project",
		Long:      editLong,
		Args:      []cli.Arg{projectRefArg()},
		Flags:     []cli.Flag{setFlag(), dataFlag("fields to change as a JSON object, from a file or stdin (instead of --set)")},
		Endpoints: []cli.Endpoint{{Method: "PATCH", Path: "/project/{id|slug}", Scope: api.ScopeProjectWrite}},
		Output:    &output.Spec{Shape: output.Ack},
		Mutation:  cli.Write,
		Examples: []string{
			"mdr project edit my-mod --set 'description=A better description'",
			"mdr project edit my-mod --data changes.json --dry-run",
		},
		Errors: writeErrors,
		Run: func(ctx context.Context, inv *cli.Invocation) (*output.Result, error) {
			body, err := editBody(inv)
			if err != nil {
				return nil, err
			}
			id := inv.Arg("id")
			if err := inv.Client.EditProject(ctx, id, body); err != nil {
				return nil, err
			}
			return ack("edit", "project", id), nil
		},
	}
}

func projectDeleteCmd() *cli.Command {
	return &cli.Command{
		Name:      "delete",
		Summary:   "Delete a project with all its versions",
		Long:      "Deletes the project. On a terminal it asks you to type the id or slug as given; otherwise it needs --yes.",
		Args:      []cli.Arg{projectRefArg()},
		Endpoints: []cli.Endpoint{{Method: "DELETE", Path: "/project/{id|slug}", Scope: api.ScopeProjectDelete}},
		Output:    &output.Spec{Shape: output.Ack},
		Mutation:  cli.Destructive,
		Examples:  []string{"mdr project delete my-test-mod --yes", "mdr project delete my-test-mod --dry-run"},
		Errors:    append(slices.Clone(writeErrors), apperr.ConfirmationRequired),
		Run: func(ctx context.Context, inv *cli.Invocation) (*output.Result, error) {
			id := inv.Arg("id")
			if err := inv.Client.DeleteProject(ctx, id); err != nil {
				return nil, err
			}
			return ack("delete", "project", id), nil
		},
	}
}
