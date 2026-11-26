package validate

import (
	"errors"
	"strings"
	"testing"

	"github.com/s8a-uk/modrinth-cli/internal/apperr"
)

func isInvalid(err error) bool {
	var ae *apperr.Error
	return errors.As(err, &ae) && ae.Code == apperr.InvalidInput
}

func TestRejects(t *testing.T) {
	cases := []struct {
		fn   string
		f    func(string) error
		vals []string
	}{
		{"NoControl", NoControl, []string{"sodium\n", "a\x00b", "\x7f", "bad\xffutf8"}},
		{"ProjectRef", ProjectRef, []string{"sodium?x=1", "sodium#frag", "a%2Fb", "../user", "..", ".", "sod ium", "", strings.Repeat("a", 65), "a?b"}},
		{"APIPath", APIPath, []string{"https://evil.example/v2/x", "project/x", "/v2/project/x", "/v3", "/a/../b", "/a/./b", "//host/x", "/a?b=1", "/a#x", "/a b", "/" + strings.Repeat("a", 1024)}},
		{"QueryPair", QueryPair, []string{"noequals", "=v", "a b=1", "k=v\n", strings.Repeat("k", 65) + "=v"}},
		{"Token", Token, []string{"short", "mrp_" + strings.Repeat("x", 20) + " ", "mrp_" + strings.Repeat("x", 20) + "\n"}},
	}
	for _, tc := range cases {
		for _, v := range tc.vals {
			if err := tc.f(v); !isInvalid(err) {
				t.Errorf("%s(%q) = %v, want invalid_input", tc.fn, v, err)
			}
		}
	}
}

func TestAccepts(t *testing.T) {
	cases := []struct {
		fn   string
		f    func(string) error
		vals []string
	}{
		{"NoControl", NoControl, []string{"", "sodium", "日本語", "tab-free text"}},
		{"ProjectRef", ProjectRef, []string{"sodium", "AANobbMI", "fabric-api", "Create_Fabric", "a.b", strings.Repeat("a", 64)}},
		{"APIPath", APIPath, []string{"/", "/statistics", "/project/sodium", "/project/AANobbMI/version", "/tag/game_version", "/user/a@b"}},
		{"QueryPair", QueryPair, []string{"query=sodium", "limit=1", "facets=[[\"categories:fabric\"]]", "empty=", "a=b=c"}},
		{"Token", Token, []string{"mrp_" + strings.Repeat("A1_-.", 10)}},
	}
	for _, tc := range cases {
		for _, v := range tc.vals {
			if err := tc.f(v); err != nil {
				t.Errorf("%s(%q) = %v, want nil", tc.fn, v, err)
			}
		}
	}
}

func TestBaseURL(t *testing.T) {
	ok := map[string]string{
		"https://api.modrinth.com":         "https://api.modrinth.com",
		"https://api.modrinth.com/":        "https://api.modrinth.com",
		"https://api.modrinth.com/v2":      "https://api.modrinth.com",
		"https://api.modrinth.com/v2/":     "https://api.modrinth.com",
		"http://127.0.0.1:8080":            "http://127.0.0.1:8080",
		"http://localhost:1/v2":            "http://localhost:1",
		"http://[::1]:9000":                "http://[::1]:9000",
		"https://staging-api.modrinth.com": "https://staging-api.modrinth.com",
	}
	for in, want := range ok {
		got, err := BaseURL(in)
		if err != nil || got != want {
			t.Errorf("BaseURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "api.modrinth.com", "http://api.modrinth.com", "ftp://x", "https://u:p@api.modrinth.com",
		"https://api.modrinth.com/v3", "https://api.modrinth.com/x", "https://api.modrinth.com?x=1", "https://api.modrinth.com#f"} {
		if _, err := BaseURL(in); !isInvalid(err) {
			t.Errorf("BaseURL(%q) = %v, want invalid_input", in, err)
		}
	}
}

func TestMessageShape(t *testing.T) {
	err := NoControl("a\nb")
	if want := `invalid input: "a\x0ab" (contains a control character)`; err.(*apperr.Error).Message != want {
		t.Errorf("message = %q, want %q", err.(*apperr.Error).Message, want)
	}
	long := ProjectRef(strings.Repeat("é", 100)).(*apperr.Error).Message
	if !strings.Contains(long, strings.Repeat("é", 80)+`..."`) || strings.Contains(long, strings.Repeat("é", 81)) {
		t.Errorf("long value not truncated to 80 characters: %q", long)
	}
	if msg := Token("secret value!!").(*apperr.Error).Message; strings.Contains(msg, "secret") {
		t.Errorf("Token leaks the value: %q", msg)
	}
}
