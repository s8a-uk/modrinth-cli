package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestResolved(t *testing.T) {
	origVersion, origRead := Version, readBuildInfo
	t.Cleanup(func() { Version, readBuildInfo = origVersion, origRead })

	tests := []struct {
		name    string
		version string
		module  string
		ok      bool
		want    string
	}{
		{"injected wins", "v1.2.3", "v0.0.1", true, "v1.2.3"},
		{"go install version", "", "v0.1.0", true, "v0.1.0"},
		{"devel falls back", "", "(devel)", true, "dev"},
		{"empty module version", "", "", true, "dev"},
		{"no build info", "", "", false, "dev"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			Version = tt.version
			readBuildInfo = func() (*debug.BuildInfo, bool) {
				if !tt.ok {
					return nil, false
				}
				return &debug.BuildInfo{Main: debug.Module{Version: tt.module}}, true
			}
			if got := Resolved(); got != tt.want {
				t.Errorf("Resolved() = %q, want %q", got, tt.want)
			}
		})
	}
}
