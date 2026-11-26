package app

import (
	"runtime"
	"testing"

	"github.com/s8a-uk/modrinth-cli/internal/output"
)

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestTermWidth(t *testing.T) {
	cases := []struct {
		columns  string
		measured int
		want     int
	}{
		{"60", 200, 60}, {" 80 ", 0, 80}, {"", 132, 132}, {"0", 132, 132}, {"-5", 0, 100}, {"wide", 0, 100}, {"", 0, 100},
	}
	for _, tc := range cases {
		if got := termWidth(envOf(map[string]string{"COLUMNS": tc.columns}), tc.measured); got != tc.want {
			t.Errorf("termWidth(COLUMNS=%q, %d) = %d, want %d", tc.columns, tc.measured, got, tc.want)
		}
	}
}

func TestUseColor(t *testing.T) {
	term := map[string]string{"TERM": "xterm"}
	if !useColor(output.Table, true, false, envOf(term)) {
		t.Error("table on a terminal: no color")
	}
	for name, ok := range map[string]bool{
		"json":     useColor(output.JSON, true, false, envOf(term)),
		"not tty":  useColor(output.Table, false, false, envOf(term)),
		"no-color": useColor(output.Table, true, true, envOf(term)),
		"NO_COLOR": useColor(output.Table, true, false, envOf(map[string]string{"TERM": "xterm", "NO_COLOR": "1"})),
	} {
		if ok {
			t.Errorf("%s: color", name)
		}
	}
	if runtime.GOOS == "windows" && useColor(output.Table, true, false, envOf(nil)) {
		t.Error("windows console without WT_SESSION or TERM: color")
	}
}
