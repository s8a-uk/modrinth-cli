package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/s8a-uk/modrinth-cli/internal/apperr"
	"github.com/s8a-uk/modrinth-cli/internal/jsonx"
)

// testDir returns a config directory that does not exist yet (inside a temp dir).
func testDir(t *testing.T) (string, Paths) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "modrinth-cli")
	p, err := ResolvePaths(envOf(map[string]string{"MODRINTH_CONFIG_DIR": dir}))
	if err != nil {
		t.Fatal(err)
	}
	return dir, p
}

func writeFile(t *testing.T, path, data string, perm os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), perm); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, perm); err != nil { // umask
		t.Fatal(err)
	}
}

func codeOf(err error) apperr.Code {
	var ae *apperr.Error
	if errors.As(err, &ae) {
		return ae.Code
	}
	return ""
}

func TestResolvePaths(t *testing.T) {
	cases := []struct {
		goos string
		env  map[string]string
		want string // "" = error
	}{
		{"linux", map[string]string{"XDG_CONFIG_HOME": "/x/cfg", "HOME": "/home/u"}, "/x/cfg"},
		{"linux", map[string]string{"XDG_CONFIG_HOME": "relative", "HOME": "/home/u"}, "/home/u/.config"},
		{"linux", map[string]string{"HOME": "/home/u"}, "/home/u/.config"},
		{"linux", nil, ""},
		{"darwin", map[string]string{"HOME": "/Users/u", "XDG_CONFIG_HOME": "/x"}, "/Users/u/Library/Application Support"},
		{"windows", map[string]string{"APPDATA": `C:\Users\u\AppData\Roaming`, "HOME": "/home/u"}, `C:\Users\u\AppData\Roaming`},
		{"windows", map[string]string{"HOME": "/home/u"}, ""},
	}
	for _, tc := range cases {
		if runtime.GOOS == "windows" && tc.goos == "linux" {
			// The linux rules use path/filepath of the host, and "/x/cfg" is not
			// absolute on Windows; they never run there.
			continue
		}
		got, err := userConfigDir(envOf(tc.env), tc.goos)
		if (err != nil) != (tc.want == "") || got != tc.want {
			t.Errorf("%s %v: got %q, %v; want %q", tc.goos, tc.env, got, err, tc.want)
		}
	}
	p, err := ResolvePaths(envOf(map[string]string{"MODRINTH_CONFIG_DIR": "/c/d", "XDG_CONFIG_HOME": "/x"}))
	cd, _ := filepath.Abs("/c/d") // on Windows the current drive is added: C:\c\d
	if err != nil || p.Dir != cd || p.ConfigFile != filepath.Join(cd, "config.json") ||
		p.Credentials != filepath.Join(cd, "credentials.json") {
		t.Errorf("MODRINTH_CONFIG_DIR: %+v %v", p, err)
	}
	if runtime.GOOS == "linux" {
		p, _ = ResolvePaths(envOf(map[string]string{"XDG_CONFIG_HOME": "/x"}))
		if p.Dir != "/x/modrinth-cli" {
			t.Errorf("XDG_CONFIG_HOME: %+v", p)
		}
	}
}

// TestPrecedence checks flag > env > file > default for every key, and the source.
func TestPrecedence(t *testing.T) {
	dir, p := testDir(t)
	writeFile(t, p.ConfigFile, `{"output":"ndjson","staging":true,"user_agent_contact":"file@example.com","timeout":11,"max_wait":12}`, 0o644)
	base := map[string]string{"MODRINTH_CONFIG_DIR": dir}
	envAll := map[string]string{"MODRINTH_CONFIG_DIR": dir, "MODRINTH_OUTPUT": "table", "MODRINTH_STAGING": "0",
		"MODRINTH_USER_AGENT_CONTACT": "env@example.com", "MODRINTH_TIMEOUT": "21", "MODRINTH_MAX_WAIT": "22"}
	out, yes, f31, f32 := "json", true, 31, 32
	flags := Flags{Output: &out, Staging: &yes, Timeout: &f31, MaxWait: &f32}

	cases := []struct {
		name  string
		env   map[string]string
		flags Flags
		want  map[string]string // key -> "value source"
	}{
		{"default", map[string]string{"MODRINTH_CONFIG_DIR": filepath.Join(dir, "none")}, Flags{}, map[string]string{
			"output": `"auto" default`, "staging": "false default", "user_agent_contact": `"" default`,
			"timeout": "30 default", "max_wait": "60 default"}},
		{"file", base, Flags{}, map[string]string{
			"output": `"ndjson" file`, "staging": "true file", "user_agent_contact": `"file@example.com" file`,
			"timeout": "11 file", "max_wait": "12 file"}},
		{"env", envAll, Flags{}, map[string]string{
			"output": `"table" env`, "staging": "false env", "user_agent_contact": `"env@example.com" env`,
			"timeout": "21 env", "max_wait": "22 env"}},
		{"flag", envAll, flags, map[string]string{
			"output": `"json" flag`, "staging": "true flag", "user_agent_contact": `"env@example.com" env`,
			"timeout": "31 flag", "max_wait": "32 flag"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _, warnings, err := Load(envOf(tc.env), tc.flags)
			if err != nil || len(warnings) > 0 {
				t.Fatalf("err %v, warnings %v", err, warnings)
			}
			for _, k := range Keys {
				v, _ := s.Value(k)
				var b strings.Builder
				_ = jsonx.Encode(&b, v, "")
				if got := strings.TrimSpace(b.String()) + " " + string(s.Sources[k]); got != tc.want[k] {
					t.Errorf("%s = %s, want %s", k, got, tc.want[k])
				}
			}
		})
	}
	s, _, _, _ := Load(envOf(base), Flags{})
	if s.APIURL != StagingURL || s.Timeout.Seconds() != 11 {
		t.Errorf("file staging/timeout not applied: %s %v", s.APIURL, s.Timeout)
	}
}

func TestConfigFileProblems(t *testing.T) {
	dir, p := testDir(t)
	env := envOf(map[string]string{"MODRINTH_CONFIG_DIR": dir})

	writeFile(t, p.ConfigFile, `{"output": "json",`, 0o644)
	_, _, _, err := Load(env, Flags{})
	if codeOf(err) != apperr.InvalidInput || !strings.Contains(err.Error(), "config file is not valid JSON: "+p.ConfigFile) {
		t.Errorf("broken JSON: %v", err)
	}

	writeFile(t, p.ConfigFile, `{"foo":1,"timeout":"30","max_wait":99999,"output":"xml","staging":false}`, 0o644)
	s, _, warnings, err := Load(env, Flags{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		`ignoring unknown key "foo" in ` + p.ConfigFile,
		`ignoring key "timeout": expected an integer between 1 and 3600 in ` + p.ConfigFile,
		`ignoring key "max_wait": expected an integer between 0 and 3600 in ` + p.ConfigFile,
		`ignoring key "output": invalid value "xml" in ` + p.ConfigFile,
	}
	if strings.Join(warnings, "\n") != strings.Join(want, "\n") {
		t.Errorf("warnings:\n%s\nwant:\n%s", strings.Join(warnings, "\n"), strings.Join(want, "\n"))
	}
	if s.Sources["staging"] != SourceFile || s.Sources["timeout"] != SourceDefault {
		t.Errorf("sources: %v", s.Sources)
	}

	for _, env := range []map[string]string{{"MODRINTH_TIMEOUT": "abc"}, {"MODRINTH_MAX_WAIT": "-1"}, {"MODRINTH_OUTPUT": "xml"},
		{"MODRINTH_USER_AGENT_CONTACT": "a b"}, {"MODRINTH_USER_AGENT_SUFFIX": "x y"}} {
		if _, _, _, err := Load(envOf(env), Flags{}); codeOf(err) != apperr.InvalidInput {
			t.Errorf("%v: %v", env, err)
		}
	}
	_, _, _, err = Load(envOf(map[string]string{"MODRINTH_TIMEOUT": "abc"}), Flags{})
	if e := apperr.From(err); e.Message != `invalid MODRINTH_TIMEOUT: "abc"` || e.Hint == "" {
		t.Errorf("message %q hint %q", e.Message, e.Hint)
	}
}

func TestSetKeyKeepsUnknownKeysAndOrder(t *testing.T) {
	dir, p := testDir(t)
	if _, err := SetKey(p, "timeout", "60"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o700) {
		t.Errorf("dir mode: %v %v", info.Mode(), err)
	}
	writeFile(t, p.ConfigFile, `{"zeta":{"a":[1,2]},"timeout":5,"output":"json"}`, 0o644)
	v, err := SetKey(p, "timeout", "60")
	if err != nil || v.Num != "60" {
		t.Fatalf("SetKey: %v %v", v, err)
	}
	if _, err := SetKey(p, "staging", "true"); err != nil {
		t.Fatal(err)
	}
	if err := UnsetKey(p, "output"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p.ConfigFile)
	want := "{\n  \"zeta\": {\n    \"a\": [\n      1,\n      2\n    ]\n  },\n  \"timeout\": 60,\n  \"staging\": true\n}\n"
	if string(got) != want {
		t.Errorf("file:\n%s\nwant:\n%s", got, want)
	}
	if info, _ := os.Stat(p.ConfigFile); runtime.GOOS != "windows" && info.Mode().Perm() != 0o644 {
		t.Errorf("config.json mode %v", info.Mode())
	}
	if _, err := SetKey(p, "nope", "1"); codeOf(err) != apperr.Usage {
		t.Errorf("unknown key: %v", err)
	}
	if _, err := SetKey(p, "token", "x"); !strings.Contains(apperr.From(err).Hint, "auth login") {
		t.Errorf("token key: %v", err)
	}
	if _, err := SetKey(p, "timeout", "0"); codeOf(err) != apperr.InvalidInput {
		t.Errorf("timeout 0: %v", err)
	}
	if _, err := SetKey(Paths{}, "timeout", "5"); codeOf(err) != apperr.IOError {
		t.Errorf("no dir: %v", err)
	}
	if err := UnsetKey(p, "nope"); codeOf(err) != apperr.Usage {
		t.Errorf("unset unknown: %v", err)
	}
}

func TestSaveTokenPermissions(t *testing.T) {
	dir, p := testDir(t)
	if err := SaveToken(p, false, NewToken(prodTok)); err != nil {
		t.Fatal(err)
	}
	if err := SaveToken(p, true, NewToken(stagTok)); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(p.Credentials); info.Mode().Perm() != 0o600 {
			t.Errorf("credentials.json mode %v, want 0600", info.Mode().Perm())
		}
		if info, _ := os.Stat(dir); info.Mode().Perm() != 0o700 {
			t.Errorf("dir mode %v, want 0700", info.Mode().Perm())
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("leftover files: %v", entries)
	}

	// Production and staging stay apart.
	env := map[string]string{"MODRINTH_CONFIG_DIR": dir}
	s, _, warnings, err := Load(envOf(env), Flags{})
	if err != nil || s.Token.Reveal() != prodTok || s.TokenSource != SourceFile || len(warnings) != 0 {
		t.Errorf("production: %v %q %v", err, s.TokenSource, warnings)
	}
	yes := true
	s, _, _, _ = Load(envOf(env), Flags{Staging: &yes})
	if s.Token.Reveal() != stagTok || s.TokenSource != SourceFile {
		t.Errorf("staging: %q", s.TokenSource)
	}

	// Loose permissions: a warning, and the token is still used.
	if runtime.GOOS != "windows" {
		if err := os.Chmod(p.Credentials, 0o644); err != nil {
			t.Fatal(err)
		}
		s, _, warnings, _ = Load(envOf(env), Flags{})
		want := p.Credentials + " is readable by other users; run: chmod 600 " + p.Credentials
		if s.Token.Reveal() != prodTok || len(warnings) != 1 || warnings[0] != want {
			t.Errorf("loose permissions: %v", warnings)
		}
	}

	existed, err := DeleteToken(p, false)
	if !existed || err != nil {
		t.Fatalf("DeleteToken: %v %v", existed, err)
	}
	if existed, _ := DeleteToken(p, false); existed {
		t.Error("deleted twice")
	}
	data, _ := os.ReadFile(p.Credentials)
	if strings.Contains(string(data), "PROD") || !strings.Contains(string(data), stagTok) {
		t.Errorf("after deleting the production token: %s", data)
	}
	if existed, _ := DeleteToken(p, true); !existed {
		t.Error("staging token not deleted")
	}
	if _, err := os.Stat(p.Credentials); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("empty credentials.json not removed: %v", err)
	}
}

func TestTokenPrecedence(t *testing.T) {
	dir, p := testDir(t)
	if err := SaveToken(p, false, NewToken("mrp_CREDCREDCREDCREDCREDCRED")); err != nil {
		t.Fatal(err)
	}
	tokFile := filepath.Join(dir, "token.txt")
	writeFile(t, tokFile, "  mrp_FILEFILEFILEFILEFILEFILE  \nsecond line ignored\n", 0o600)
	stagFile := filepath.Join(dir, "staging-token.txt")
	writeFile(t, stagFile, stagTok+"\n", 0o600)

	cases := []struct {
		name    string
		env     map[string]string
		staging bool
		token   string
		source  Source
		envVar  string
	}{
		{"credentials", nil, false, "mrp_CREDCREDCREDCREDCREDCRED", SourceFile, ""},
		{"token file wins over credentials", map[string]string{"MODRINTH_TOKEN_FILE": tokFile}, false, "mrp_FILEFILEFILEFILEFILEFILE", SourceTokenFile, "MODRINTH_TOKEN_FILE"},
		{"env wins over token file", map[string]string{"MODRINTH_TOKEN_FILE": tokFile, "MODRINTH_TOKEN": prodTok}, false, prodTok, SourceEnv, "MODRINTH_TOKEN"},
		{"staging file", map[string]string{"MODRINTH_STAGING_TOKEN_FILE": stagFile, "MODRINTH_TOKEN": prodTok}, true, stagTok, SourceTokenFile, "MODRINTH_STAGING_TOKEN_FILE"},
		{"staging never uses production", map[string]string{"MODRINTH_TOKEN_FILE": tokFile, "MODRINTH_TOKEN": prodTok}, true, "", "", ""},
		{"production never uses staging", map[string]string{"MODRINTH_STAGING_TOKEN_FILE": stagFile, "MODRINTH_STAGING_TOKEN": stagTok, "MODRINTH_CONFIG_DIR": filepath.Join(dir, "none")}, false, "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{"MODRINTH_CONFIG_DIR": dir}
			for k, v := range tc.env {
				env[k] = v
			}
			var flags Flags
			if tc.staging {
				flags.Staging = &tc.staging
			}
			s, _, warnings, err := Load(envOf(env), flags)
			if err != nil || len(warnings) != 0 {
				t.Fatalf("err %v warnings %v", err, warnings)
			}
			if s.Token.Reveal() != tc.token || s.TokenSource != tc.source || s.TokenVar != tc.envVar {
				t.Errorf("got %q from %q (%q)", s.Token.Masked(), s.TokenSource, s.TokenVar)
			}
		})
	}
}

func TestTokenFileProblems(t *testing.T) {
	dir := t.TempDir()
	const secret = "mrp_bad SECRETSECRET token"
	bad := filepath.Join(dir, "bad.txt")
	writeFile(t, bad, secret+"\n", 0o600)
	empty := filepath.Join(dir, "empty.txt")
	writeFile(t, empty, "\n"+prodTok+"\n", 0o600)
	cases := []struct {
		path string
		code apperr.Code
	}{
		{filepath.Join(dir, "missing.txt"), apperr.IOError},
		{bad, apperr.InvalidInput},
		{empty, apperr.InvalidInput},
		{dir, apperr.IOError},
	}
	for _, tc := range cases {
		_, _, _, err := Load(envOf(map[string]string{"MODRINTH_TOKEN_FILE": tc.path}), Flags{})
		e := apperr.From(err)
		if e.Code != tc.code {
			t.Errorf("%s: %v, want %s", tc.path, err, tc.code)
		}
		if !strings.Contains(e.Message, tc.path) || !strings.Contains(e.Message, "MODRINTH_TOKEN_FILE") {
			t.Errorf("%s: message %q should name the variable and the path", tc.path, e.Message)
		}
		if strings.Contains(string(e.JSON())+e.Human(), "SECRET") {
			t.Errorf("%s: error shows the file contents: %s", tc.path, e.JSON())
		}
	}

	// A broken credentials.json never shows its contents either.
	_, p := testDir(t)
	writeFile(t, p.Credentials, `{"token":"mrp_SECRETSECRETSECRETSECRET"`, 0o600)
	_, _, _, err := Load(envOf(map[string]string{"MODRINTH_CONFIG_DIR": p.Dir}), Flags{})
	if e := apperr.From(err); e.Code != apperr.InvalidInput || strings.Contains(string(e.JSON())+e.Human(), "SECRET") {
		t.Errorf("broken credentials: %s", e.JSON())
	}

	// Loose permissions and a non-PAT token: warnings only.
	if runtime.GOOS != "windows" {
		loose := filepath.Join(dir, "loose.txt")
		writeFile(t, loose, "gho_OAUTHOAUTHOAUTHOAUTHOAUTH\n", 0o644)
		s, _, warnings, err := Load(envOf(map[string]string{"MODRINTH_TOKEN_FILE": loose}), Flags{})
		want := []string{loose + " is readable by other users; run: chmod 600 " + loose,
			"token does not look like a Modrinth PAT (mrp_...)"}
		if err != nil || s.Token.IsZero() || strings.Join(warnings, "|") != strings.Join(want, "|") {
			t.Errorf("loose: %v %v", err, warnings)
		}
	}
}
