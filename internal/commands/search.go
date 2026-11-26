package commands

import (
	"context"
	"errors"
	"strconv"
	"unicode/utf8"

	"github.com/s8a-uk/modrinth-cli/internal/api"
	"github.com/s8a-uk/modrinth-cli/internal/apperr"
	"github.com/s8a-uk/modrinth-cli/internal/cli"
	"github.com/s8a-uk/modrinth-cli/internal/output"
	"github.com/s8a-uk/modrinth-cli/internal/validate"
)

// searchMaxLimit is the most hits one search command fetches (in pages of 100).
const searchMaxLimit = 1000

// facetFlagNames are the flags that build facets; --facets replaces them all.
var facetFlagNames = []string{"type", "loader", "game-version", "category", "environment", "license", "author", "open-source", "facet"}

func searchCmd() *cli.Command {
	return &cli.Command{
		Name:    "search",
		Summary: "Search projects",
		Long: "Searches projects. Values of one repeated flag are ORed, different flags are ANDed; " +
			"each --category and each --facet is its own AND group. --facets passes the raw facets JSON " +
			"instead (not combinable with the facet flags). More than 100 results are fetched in pages.",
		Args: []cli.Arg{
			{Name: "query", Help: "search text (at most 256 characters)", Validate: searchQuery},
		},
		Flags: []cli.Flag{
			{Name: "type", Kind: cli.KindStrings, Value: "<type>", Help: "project type, e.g. mod, modpack, resourcepack (repeatable, OR)", Validate: validate.Tag},
			{Name: "loader", Kind: cli.KindStrings, Value: "<loader>", Help: "loader, e.g. fabric (repeatable, OR)", Validate: validate.Tag},
			{Name: "game-version", Kind: cli.KindStrings, Value: "<version>", Help: "game version, e.g. 1.20.1 (repeatable, OR)", Validate: validate.Tag},
			{Name: "category", Kind: cli.KindStrings, Value: "<category>", Help: "category (repeatable, AND)", Validate: validate.Tag},
			{Name: "environment", Kind: cli.KindStrings, Value: "<env>", Help: "environment (repeatable, OR)", Validate: validate.Tag},
			{Name: "license", Kind: cli.KindStrings, Value: "<spdx>", Help: "license id (repeatable, OR)", Validate: validate.Tag},
			{Name: "author", Kind: cli.KindStrings, Value: "<user>", Help: "author username (repeatable, OR)", Validate: validate.Author},
			{Name: "open-source", Kind: cli.KindBool, Help: "only open-source projects"},
			{Name: "facet", Kind: cli.KindStrings, Value: "<expr,...>", Help: "facet group, e.g. downloads>=1000; commas OR (repeatable, AND)", Validate: validate.FacetGroup},
			{Name: "facets", Kind: cli.KindString, Value: "<json>", Help: "raw facets JSON, e.g. '[[\"categories:fabric\"]]'", Validate: validate.FacetsJSON},
			{Name: "sort", Kind: cli.KindEnum, Enum: validate.SortIndexes, Help: "sort order (API default: relevance)"},
			{Name: "limit", Kind: cli.KindInt, Default: "10", Value: "<n>", Help: "number of hits, 1-1000", Validate: intFlag(1, searchMaxLimit)},
			{Name: "offset", Kind: cli.KindInt, Default: "0", Value: "<n>", Help: "hits to skip, 0-100000", Validate: intFlag(0, 100000)},
		},
		Endpoints: []cli.Endpoint{{Method: "GET", Path: "/search"}},
		Output:    searchSpec(),
		Examples: []string{
			"mdr search sodium",
			"mdr search --type mod --loader fabric --game-version 1.20.1 --sort downloads --limit 5",
			"mdr search shader --facet 'downloads>=100000' --fields slug,title,downloads",
			`mdr search --facets '[["categories:fabric"],["versions:1.20.1"]]'`,
		},
		Errors: []apperr.Code{apperr.BadRequest},
		Run:    runSearch,
	}
}

func searchQuery(s string) error {
	if utf8.RuneCountInString(s) > 256 {
		return errors.New("at most 256 characters")
	}
	return nil
}

func intFlag(lo, hi int) func(string) error {
	return func(s string) error {
		n, err := strconv.Atoi(s)
		if err != nil {
			return errors.New("not an integer")
		}
		if n < lo || n > hi {
			return errors.New("must be between " + strconv.Itoa(lo) + " and " + strconv.Itoa(hi))
		}
		return nil
	}
}

func runSearch(ctx context.Context, inv *cli.Invocation) (*output.Result, error) {
	p := api.SearchParams{Query: inv.Arg("query"), Index: inv.String("sort"), Offset: inv.Int("offset")}
	if inv.Changed("facets") {
		for _, f := range facetFlagNames {
			if inv.Changed(f) {
				return nil, apperr.New(apperr.Usage, "--facets cannot be combined with --%s", f).
					WithHint("put every facet in --facets, or use only the facet flags")
			}
		}
		groups, err := validate.ParseFacetsJSON(inv.String("facets"))
		if err != nil {
			return nil, err
		}
		p.Facets = groups
	} else {
		groups, err := api.BuildFacets(api.FacetFlags{
			Types:        inv.Strings("type"),
			Loaders:      inv.Strings("loader"),
			GameVersions: inv.Strings("game-version"),
			Categories:   inv.Strings("category"),
			Environments: inv.Strings("environment"),
			Licenses:     inv.Strings("license"),
			Authors:      inv.Strings("author"),
			OpenSource:   inv.Bool("open-source"),
			Facets:       inv.Strings("facet"),
		})
		if err != nil {
			return nil, err
		}
		p.Facets = groups
	}
	page, err := inv.Client.SearchAll(ctx, p, inv.Int("limit"))
	if err != nil {
		return nil, err
	}
	return &output.Result{Shape: output.SearchPage, Value: page.Value()}, nil
}
