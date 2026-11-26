package commands

import (
	"slices"

	"github.com/s8a-uk/modrinth-cli/internal/output"
)

// projectFields are the selectable paths of a project.
var projectFields = []string{
	"id", "slug", "project_type", "team", "organization", "title", "description", "body", "body_url",
	"published", "updated", "approved", "queued", "status", "requested_status",
	"moderator_message.message", "moderator_message.body",
	"license.id", "license.name", "license.url",
	"client_side", "server_side", "environment", "downloads", "followers",
	"categories", "additional_categories", "game_versions", "loaders", "versions",
	"icon_url", "raw_icon_url", "color", "thread_id", "monetization_status",
	"issues_url", "source_url", "wiki_url", "discord_url",
	"donation_urls.id", "donation_urls.platform", "donation_urls.url",
	"gallery.url", "gallery.raw_url", "gallery.featured", "gallery.title", "gallery.description",
	"gallery.created", "gallery.ordering",
}

// projectDefaultFields leave out the heavy fields (body, gallery, versions, game_versions, URLs).
var projectDefaultFields = []string{
	"id", "slug", "title", "description", "project_type", "downloads", "followers", "categories", "loaders",
	"license.id", "client_side", "server_side", "status", "updated", "team", "source_url",
}

// projectTable are the columns of projects in a table.
var projectTable = []output.Column{
	{Header: "ID", Path: "id"},
	{Header: "SLUG", Path: "slug", Max: 28},
	{Header: "TYPE", Path: "project_type"},
	{Header: "DOWNLOADS", Path: "downloads", Kind: output.KindNum},
	{Header: "FOLLOWERS", Path: "followers", Kind: output.KindNum},
	{Header: "UPDATED", Path: "updated", Kind: output.KindDate},
	{Header: "TITLE", Path: "title"},
}

// projectSpec is the output of commands returning one project.
func projectSpec() *output.Spec {
	return &output.Spec{Shape: output.Object, Fields: projectFields, DefaultFields: projectDefaultFields, Table: projectTable}
}

// searchHitFields are the selectable paths of a search hit. Hits use project_id and
// follows where projects use id and followers; the API names are kept.
var searchHitFields = []string{
	"project_id", "slug", "title", "description", "project_type", "all_project_types", "author", "author_id",
	"organization", "organization_id", "categories", "display_categories",
	"versions", "downloads", "follows", "icon_url", "date_created", "date_modified", "latest_version", "license",
	"client_side", "server_side", "environment", "disclosure_types", "gallery", "featured_gallery", "color",
}

var searchHitDefaultFields = []string{
	"project_id", "slug", "title", "description", "project_type", "author", "downloads", "follows",
	"latest_version", "date_modified",
}

var searchHitTable = []output.Column{
	{Header: "ID", Path: "project_id"},
	{Header: "SLUG", Path: "slug", Max: 28},
	{Header: "TYPE", Path: "project_type"},
	{Header: "DOWNLOADS", Path: "downloads", Kind: output.KindNum},
	{Header: "FOLLOWS", Path: "follows", Kind: output.KindNum},
	{Header: "UPDATED", Path: "date_modified", Kind: output.KindDate},
	{Header: "TITLE", Path: "title", Max: 40},
	{Header: "DESCRIPTION", Path: "description"},
}

// searchSpec is the output of search.
func searchSpec() *output.Spec {
	return &output.Spec{Shape: output.SearchPage, Fields: searchHitFields, DefaultFields: searchHitDefaultFields, Table: searchHitTable}
}

// projectListSpec is the output of commands returning an array of projects.
func projectListSpec() *output.Spec {
	return &output.Spec{Shape: output.Array, Fields: projectFields, DefaultFields: projectDefaultFields, Table: projectTable}
}

// versionFields are the selectable paths of a version.
var versionFields = []string{
	"id", "project_id", "author_id", "name", "version_number", "changelog", "changelog_url", "date_published",
	"downloads", "version_type", "status", "requested_status", "featured", "game_versions", "loaders", "environment",
	"dependencies.version_id", "dependencies.project_id", "dependencies.file_name", "dependencies.dependency_type",
	"files.id", "files.hashes.sha1", "files.hashes.sha512", "files.url", "files.filename", "files.primary",
	"files.size", "files.file_type",
}

// versionListDefaultFields are the defaults of version lists (no files, no changelog).
var versionListDefaultFields = []string{
	"id", "project_id", "version_number", "name", "version_type", "loaders", "game_versions", "date_published", "downloads",
}

// versionGetDefaultFields add what is needed to download one version.
var versionGetDefaultFields = []string{
	"id", "project_id", "version_number", "name", "version_type", "status", "featured", "loaders", "game_versions",
	"date_published", "downloads", "dependencies",
	"files.filename", "files.url", "files.size", "files.primary", "files.hashes.sha512",
}

var versionTable = []output.Column{
	{Header: "ID", Path: "id"},
	{Header: "NUMBER", Path: "version_number", Max: 24},
	{Header: "TYPE", Path: "version_type"},
	{Header: "LOADERS", Path: "loaders", Kind: output.KindList, Max: 20},
	{Header: "GAME", Path: "game_versions", Kind: output.KindList, Max: 24},
	{Header: "PUBLISHED", Path: "date_published", Kind: output.KindDate},
	{Header: "DOWNLOADS", Path: "downloads", Kind: output.KindNum},
	{Header: "NAME", Path: "name"},
}

func versionListSpec() *output.Spec {
	return &output.Spec{Shape: output.Array, Fields: versionFields, DefaultFields: versionListDefaultFields, Table: versionTable}
}

func versionGetSpec() *output.Spec {
	return &output.Spec{Shape: output.Object, Fields: versionFields, DefaultFields: versionGetDefaultFields, Table: versionTable}
}

// dependenciesSpec is the output of project dependencies: projects and versions,
// each with the paths of its resource under it, printed as two tables.
func dependenciesSpec() *output.Spec {
	fields := make([]string, 0, len(projectFields)+len(versionFields))
	for _, f := range projectFields {
		fields = append(fields, "projects."+f)
	}
	for _, f := range versionFields {
		fields = append(fields, "versions."+f)
	}
	return &output.Spec{
		Shape:  output.Object,
		Fields: fields,
		DefaultFields: []string{
			"projects.id", "projects.slug", "projects.title", "projects.project_type",
			"versions.id", "versions.project_id", "versions.version_number",
		},
		Sections: []output.Section{
			{Path: "projects", Columns: []output.Column{
				{Header: "ID", Path: "id"},
				{Header: "SLUG", Path: "slug", Max: 28},
				{Header: "TYPE", Path: "project_type"},
				{Header: "TITLE", Path: "title"},
			}},
			{Path: "versions", Columns: []output.Column{
				{Header: "ID", Path: "id"},
				{Header: "PROJECT", Path: "project_id"},
				{Header: "NUMBER", Path: "version_number"},
			}},
		},
	}
}

// userFields are the selectable paths of a user. The private ones (email, payout
// data, ...) are null unless the token belongs to that user.
var userFields = []string{
	"id", "username", "name", "email", "bio", "avatar_url", "created", "role", "badges", "auth_providers",
	"email_verified", "has_password", "has_totp", "github_id",
	"payout_data.balance", "payout_data.payout_wallet", "payout_data.payout_wallet_type", "payout_data.payout_address",
}

var userDefaultFields = []string{"id", "username", "name", "bio", "role", "created"}

var userTable = []output.Column{
	{Header: "ID", Path: "id"},
	{Header: "USERNAME", Path: "username", Max: 28},
	{Header: "ROLE", Path: "role"},
	{Header: "CREATED", Path: "created", Kind: output.KindDate},
	{Header: "BIO", Path: "bio"},
}

func userSpec() *output.Spec {
	return &output.Spec{Shape: output.Object, Fields: userFields, DefaultFields: userDefaultFields, Table: userTable}
}

// userMeSpec adds email to the defaults: it is only filled for the token's own user.
func userMeSpec() *output.Spec {
	return &output.Spec{Shape: output.Object, Fields: userFields,
		DefaultFields: []string{"id", "username", "name", "email", "role", "created"}, Table: userTable}
}

func userListSpec() *output.Spec {
	return &output.Spec{Shape: output.Array, Fields: userFields, DefaultFields: userDefaultFields, Table: userTable}
}

// memberFields are the selectable paths of a team member. The user object of a
// member carries more keys than GET /user (campaigns is an object keyed by campaign).
var memberFields = []string{
	"team_id",
	"user.id", "user.username", "user.name", "user.avatar_url", "user.raw_avatar_url", "user.bio", "user.created",
	"user.role", "user.badges", "user.campaigns", "user.auth_providers", "user.email", "user.email_verified",
	"user.has_password", "user.has_totp", "user.payout_data", "user.stripe_customer_id",
	"user.allow_friend_requests", "user.eligibility_verified_at", "user.github_id",
	"role", "permissions", "accepted", "payouts_split", "ordering",
}

func memberSpec() *output.Spec {
	return &output.Spec{
		Shape:         output.Array,
		Fields:        memberFields,
		DefaultFields: []string{"user.id", "user.username", "role", "accepted", "ordering"},
		Table: []output.Column{
			{Header: "USER ID", Path: "user.id"},
			{Header: "USERNAME", Path: "user.username", Max: 28},
			{Header: "ROLE", Path: "role", Max: 28},
			{Header: "ACCEPTED", Path: "accepted", Kind: output.KindBool},
		},
	}
}

// tagKind is one kind of tag list <kind>.
type tagKind struct {
	name     string // CLI name
	path     string // under /tag
	fields   []string
	defaults []string        // nil = all fields
	table    []output.Column // nil = generated from the defaults
}

// tagKinds are the kinds of tag list, in help order. Kinds without fields are
// arrays of strings.
var tagKinds = []tagKind{
	{name: "category", path: "category", fields: []string{"icon", "name", "project_type", "header"},
		defaults: []string{"name", "project_type", "header"}},
	{name: "loader", path: "loader", fields: []string{"icon", "name", "supported_project_types"},
		defaults: []string{"name", "supported_project_types"}},
	{name: "game-version", path: "game_version", fields: []string{"version", "version_type", "date", "major"},
		table: []output.Column{
			{Header: "VERSION", Path: "version", Max: 28},
			{Header: "VERSION TYPE", Path: "version_type"},
			{Header: "DATE", Path: "date", Kind: output.KindDate},
			{Header: "MAJOR", Path: "major", Kind: output.KindBool},
		}},
	{name: "license", path: "license", fields: []string{"short", "name"},
		table: []output.Column{{Header: "SHORT", Path: "short", Max: 40}, {Header: "NAME", Path: "name"}}},
	{name: "donation-platform", path: "donation_platform", fields: []string{"short", "name"}},
	{name: "report-type", path: "report_type"},
	{name: "project-type", path: "project_type"},
	{name: "side-type", path: "side_type"},
}

// spec returns the output of the kind.
func (k tagKind) spec() *output.Spec {
	if k.fields == nil {
		return &output.Spec{Shape: output.Array, Table: []output.Column{{Header: "NAME", Path: ""}}}
	}
	defaults := k.defaults
	if defaults == nil {
		defaults = k.fields
	}
	return &output.Spec{Shape: output.Array, Fields: k.fields, DefaultFields: defaults, Table: k.table}
}

// tagListSpec is the static output of tag list: every kind's fields, so --fields is
// checked against the union before the command checks it against the kind.
func tagListSpec() *output.Spec {
	var fields []string
	for _, k := range tagKinds {
		for _, f := range k.fields {
			if !slices.Contains(fields, f) {
				fields = append(fields, f)
			}
		}
	}
	return &output.Spec{Shape: output.Array, Fields: fields}
}
