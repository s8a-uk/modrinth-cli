// Package buildinfo holds the version string and project identity.
package buildinfo

import "runtime/debug"

// Version is injected at build time with
// -ldflags "-X github.com/s8a-uk/modrinth-cli/internal/buildinfo.Version=v0.1.0".
var Version = ""

// RepoURL is the project homepage. It is also the contact in the User-Agent.
const RepoURL = "https://github.com/s8a-uk/modrinth-cli"

// UAProduct is the product part of the User-Agent required by the Modrinth API.
const UAProduct = "s8a-uk/modrinth-cli"

// readBuildInfo is replaced in tests.
var readBuildInfo = debug.ReadBuildInfo

// Resolved returns Version if set, otherwise the module version recorded by
// `go install` (e.g. v0.1.0), otherwise "dev".
func Resolved() string {
	if Version != "" {
		return Version
	}
	if bi, ok := readBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}
