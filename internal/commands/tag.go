package commands

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/s8a-uk/modrinth-cli/internal/apperr"
	"github.com/s8a-uk/modrinth-cli/internal/cli"
	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
	"github.com/s8a-uk/modrinth-cli/internal/output"
	"github.com/s8a-uk/modrinth-cli/internal/suggest"
	"github.com/s8a-uk/modrinth-cli/internal/validate"
)

// gameVersionTypes are the values of tag list game-version --version-type.
var gameVersionTypes = []string{"release", "snapshot", "alpha", "beta"}

// defaultGameVersionLimit is the default --limit of tag list game-version (there are
// close to a thousand game versions).
const defaultGameVersionLimit = 30

func tagCmd() *cli.Command {
	return &cli.Command{
		Name:    "tag",
		Summary: "Read tags (categories, loaders, game versions, licenses)",
		Sub:     []*cli.Command{tagListCmd(), tagLicenseCmd()},
	}
}

func tagKindNames() []string {
	names := make([]string, len(tagKinds))
	for i, k := range tagKinds {
		names[i] = k.name
	}
	return names
}

func findTagKind(name string) (tagKind, bool) {
	i := slices.IndexFunc(tagKinds, func(k tagKind) bool { return k.name == name })
	if i < 0 {
		return tagKind{}, false
	}
	return tagKinds[i], true
}

// tagKindArg rejects unknown kinds as a usage error listing the kinds.
func tagKindArg(s string) error {
	if _, ok := findTagKind(s); ok {
		return nil
	}
	e := apperr.New(apperr.Usage, "unknown tag kind %q", s)
	if c := suggest.Closest(s, tagKindNames(), 1); len(c) > 0 {
		return e.WithHint("did you mean %s? kinds: %s", c[0], strings.Join(tagKindNames(), ", "))
	}
	return e.WithHint("kinds: %s", strings.Join(tagKindNames(), ", "))
}

func tagListCmd() *cli.Command {
	return &cli.Command{
		Name:    "list",
		Summary: "List the tags of one kind",
		Long: "Kinds: " + strings.Join(tagKindNames(), ", ") + ". report-type, project-type and side-type " +
			"are arrays of strings (no --fields). Filters are applied by mdr.",
		Args: []cli.Arg{
			{Name: "kind", Required: true, Help: "tag kind: " + strings.Join(tagKindNames(), ", "), Validate: tagKindArg, Values: tagKindNames()},
		},
		Flags: []cli.Flag{
			{Name: "project-type", Kind: cli.KindString, Value: "<type>", Help: "category and loader only: tags for this project type", Validate: validate.Tag},
			{Name: "version-type", Kind: cli.KindEnum, Enum: gameVersionTypes, Help: "game-version only: versions of this type"},
			{Name: "limit", Kind: cli.KindInt, Value: "<n>", Help: "at most this many tags, 0-10000; 0 = all (default 30 for game-version, else 0)", Validate: intFlag(0, 10000)},
		},
		Endpoints: []cli.Endpoint{
			{Method: "GET", Path: "/tag/category"},
			{Method: "GET", Path: "/tag/loader"},
			{Method: "GET", Path: "/tag/game_version"},
			{Method: "GET", Path: "/tag/license"},
			{Method: "GET", Path: "/tag/donation_platform"},
			{Method: "GET", Path: "/tag/report_type"},
			{Method: "GET", Path: "/tag/project_type"},
			{Method: "GET", Path: "/tag/side_type"},
		},
		Output: tagListSpec(),
		Examples: []string{
			"mdr tag list game-version --version-type release --limit 5",
			"mdr tag list category --project-type mod --fields name,header",
			"mdr tag list loader",
		},
		Run: runTagList,
	}
}

func runTagList(ctx context.Context, inv *cli.Invocation) (*output.Result, error) {
	kind, _ := findTagKind(inv.Arg("kind"))
	if inv.Changed("project-type") && kind.name != "category" && kind.name != "loader" {
		return nil, apperr.New(apperr.Usage, "--project-type works only with the category and loader kinds")
	}
	if inv.Changed("version-type") && kind.name != "game-version" {
		return nil, apperr.New(apperr.Usage, "--version-type works only with the game-version kind")
	}
	if err := checkKindFields(kind, inv.Globals.Fields); err != nil {
		return nil, err
	}
	v, err := inv.Client.Tags(ctx, kind.path)
	if err != nil {
		return nil, err
	}
	if v.Kind != jsonx.Array {
		return nil, apperr.New(apperr.UnexpectedResponse, "the API did not return an array of tags")
	}
	items := v.Items
	keep := func(f func(jsonx.Value) bool) {
		items = slices.DeleteFunc(slices.Clone(items), func(it jsonx.Value) bool { return !f(it) })
	}
	if t := inv.String("project-type"); t != "" {
		keep(func(it jsonx.Value) bool {
			if kind.name == "category" {
				pt, _ := it.Get("project_type")
				return pt.Str == t
			}
			types, _ := it.Get("supported_project_types")
			return slices.ContainsFunc(types.Items, func(x jsonx.Value) bool { return x.Str == t })
		})
	}
	if t := inv.String("version-type"); t != "" {
		keep(func(it jsonx.Value) bool {
			vt, _ := it.Get("version_type")
			return vt.Str == t
		})
	}
	limit := inv.Int("limit")
	if !inv.Changed("limit") && kind.name == "game-version" {
		limit = defaultGameVersionLimit
	}
	res := limitArray(jsonx.Arr(items...), limit, strings.ReplaceAll(kind.name, "-", " ")+" tags")
	res.Spec = kind.spec()
	return res, nil
}

// checkKindFields rejects --fields for a kind whose tags are plain strings. Field
// names are checked against the kind's spec (res.Spec) and the response later.
func checkKindFields(kind tagKind, fields []string) error {
	if len(fields) > 0 && kind.fields == nil {
		return apperr.New(apperr.Usage, "--fields is not supported by tag kind %s (an array of strings)", kind.name)
	}
	return nil
}

func tagLicenseCmd() *cli.Command {
	return &cli.Command{
		Name:    "license",
		Summary: "Get the title and text of a license",
		Long:    "The text (body) is long, so the default output has only the title; use --full or --fields body.",
		Args: []cli.Arg{
			{Name: "spdx-id", Required: true, Help: "SPDX license id, e.g. MIT or LGPL-3.0-or-later", Validate: validate.Tag},
		},
		Endpoints: []cli.Endpoint{{Method: "GET", Path: "/tag/license/{id}"}},
		Output:    &output.Spec{Shape: output.Object, Fields: []string{"title", "body"}, DefaultFields: []string{"title"}},
		Examples:  []string{"mdr tag license MIT", "mdr tag license LGPL-3.0-or-later --full"},
		Errors:    []apperr.Code{apperr.NotFound, apperr.BadRequest},
		Run: func(ctx context.Context, inv *cli.Invocation) (*output.Result, error) {
			v, err := inv.Client.License(ctx, inv.Arg("spdx-id"))
			if err != nil {
				return nil, err
			}
			return &output.Result{Shape: output.Object, Value: v}, nil
		},
	}
}

func statsCmd() *cli.Command {
	fields := []string{"projects", "versions", "files", "authors"}
	return &cli.Command{
		Name:      "stats",
		Summary:   "Show site-wide counts of projects, versions, files, authors",
		Endpoints: []cli.Endpoint{{Method: "GET", Path: "/statistics"}},
		Output:    &output.Spec{Shape: output.Object, Fields: fields, DefaultFields: fields},
		Examples:  []string{"mdr stats"},
		Run: func(ctx context.Context, inv *cli.Invocation) (*output.Result, error) {
			v, err := inv.Client.Statistics(ctx)
			if err != nil {
				return nil, err
			}
			return &output.Result{Shape: output.Object, Value: v}, nil
		},
	}
}

// oneOf accepts only the given values.
func oneOf(values []string) func(string) error {
	return func(s string) error {
		if !slices.Contains(values, s) {
			return errors.New("use one of: " + strings.Join(values, ", "))
		}
		return nil
	}
}
