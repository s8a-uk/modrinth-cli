package validate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/s8a-uk/modrinth-cli/internal/apperr"
)

func hashAuto(s string) error { _, err := Hash(s, ""); return err }

func downloadURL(s string) error { return DownloadURL(s, false, "") }

// TestRejectionTable is the rejection table of the validation design, row by row.
func TestRejectionTable(t *testing.T) {
	cases := []struct {
		fn     string
		f      func(string) error
		vals   []string
		reason string
	}{
		{"ProjectRef", ProjectRef, []string{"sodium?x=1"}, "?"},
		{"ProjectRef", ProjectRef, []string{"sodium#frag"}, "#"},
		{"ProjectRef", ProjectRef, []string{"a%2Fb"}, "% (double encoding)"},
		{"ProjectRef", ProjectRef, []string{"../user"}, "/"},
		{"ProjectRef", ProjectRef, []string{".."}, "parent directory"},
		{"ProjectRef", ProjectRef, []string{"sod ium"}, "space"},
		{"NoControl", NoControl, []string{"sodium\n"}, "control character"},
		{"ProjectRef", ProjectRef, []string{strings.Repeat("a", 65)}, "length"},
		{"VersionID", VersionID, []string{"AANobbM", "AANobbMI!"}, "shape"},
		{"Hash", hashAuto, []string{"xyz", strings.Repeat("a", 39)}, "digit count"},
		{"Tag", Tag, []string{"1.20.1;rm -rf"}, "character set"},
		{"FacetExpr", FacetExpr, []string{"downloads>>5", "foo:bar", `title:a"b`}, "operator / unknown key / quote"},
		{"FacetsJSON", FacetsJSON, []string{`[["a"],"b"]`, `{}`, `[]`}, "shape"},
		{"APIPath", APIPath, []string{"https://evil.example/v2/x", "project/x", "/v2/project/x", "/a/../b", "//host/x"},
			"absolute URL / no leading / / version / .. / //"},
		{"HeaderLine", HeaderLine, []string{"Authorization: x", "X: a\r\nY: b"}, "forbidden header / newline"},
		{"DownloadFilename", DownloadFilename, []string{"../../etc/passwd", "a/b.jar", `a\b.jar`, ".bashrc", "CON.jar", "nul", "mod.jar.", "a:b.jar"},
			"filename rules"},
		{"DownloadURL", downloadURL, []string{"http://cdn.modrinth.com/x.jar", "https://cdn.modrinth.com.evil.example/x.jar", "https://user@cdn.modrinth.com/x.jar"},
			"scheme / host / user info"},
	}
	for _, tc := range cases {
		for _, v := range tc.vals {
			t.Run(tc.fn+"/"+v, func(t *testing.T) {
				if err := tc.f(v); !isInvalid(err) {
					t.Errorf("%s(%q) = %v, want invalid_input (%s)", tc.fn, v, err, tc.reason)
				}
			})
		}
	}
}

// TestAcceptExamples are the values the validation design says must pass.
func TestAcceptExamples(t *testing.T) {
	cases := []struct {
		fn   string
		f    func(string) error
		vals []string
	}{
		{"ProjectRef", ProjectRef, []string{"sodium", "AANobbMI", "fabric-api", "Create_Fabric"}},
		{"VersionID", VersionID, []string{"AANobbMI"}},
		{"Tag", Tag, []string{"1.20.1", "24w14a", "1.21-pre1", "fabric"}},
		{"FacetExpr", FacetExpr, []string{"downloads>=10000", "versions:1.20.1", "project_type!=mod", "categories=fabric", "downloads<5"}},
		{"FacetGroup", FacetGroup, []string{"categories:fabric,categories:quilt", "versions:1.20.1"}},
		{"FacetsJSON", FacetsJSON, []string{`[["categories:fabric","categories:quilt"],["versions:1.20.1"]]`, `[["anything:goes"]]`}},
		{"DownloadFilename", DownloadFilename, []string{"sodium-fabric-0.5.3+mc1.20.1.jar", "Fabulously.Optimized-5.4.1.mrpack", "CONSOLE.jar"}},
		{"DownloadURL", downloadURL, []string{"https://cdn.modrinth.com/data/AANobbMI/versions/x/sodium.jar"}},
		{"HeaderLine", HeaderLine, []string{"X-Test: 1", "Accept-Language:en"}},
		{"UserRef", UserRef, []string{"jellysquid3", "a_b-c"}},
		{"TeamID", TeamID, []string{"4reLOAKe"}},
		{"Base62ID", Base62ID, []string{"AbCd1234"}},
		{"SortIndex", SortIndex, SortIndexes},
		{"UAContact", UAContact, []string{"me@example.com", "https://example.com/me"}},
		{"UASuffix", UASuffix, []string{"my-tool/1.0"}},
		{"Hash", hashAuto, []string{strings.Repeat("A", 40), strings.Repeat("f", 128)}},
	}
	for _, tc := range cases {
		for _, v := range tc.vals {
			if err := tc.f(v); err != nil {
				t.Errorf("%s(%q) = %v, want nil", tc.fn, v, err)
			}
		}
	}
}

func TestMoreRejects(t *testing.T) {
	cases := []struct {
		fn   string
		f    func(string) error
		vals []string
	}{
		{"VersionNumber", VersionNumber, []string{".", "..", "1.0/2", ""}},
		{"UserRef", UserRef, []string{"", "a b", strings.Repeat("a", 40)}},
		{"Author", Author, []string{"a/b"}},
		{"TeamID", TeamID, []string{"short"}},
		{"Base62ID", Base62ID, []string{"12345678x"}},
		{"Tag", Tag, []string{"", "-fabric", strings.Repeat("a", 65)}},
		{"FacetExpr", FacetExpr, []string{"", "versions:", "Versions:1", "versions:a,b", "versions:[x]"}},
		{"FacetGroup", FacetGroup, []string{"versions:1.20.1,", "versions:1,nope:x"}},
		{"FacetsJSON", FacetsJSON, []string{`[[]]`, `[[""]]`, `[["a\n"]]`, `[[1]]`, "[" + strings.Repeat(`["a"],`, 32) + `["a"]]`}},
		{"SortIndex", SortIndex, []string{"", "Downloads", "rating"}},
		{"HeaderLine", HeaderLine, []string{"user-agent: x", "Host: a", "Content-Length: 1", "cookie: a", "NoColon", "X: " + strings.Repeat("a", 1025)}},
		{"UAContact", UAContact, []string{"ab", "a b@c"}},
		{"UASuffix", UASuffix, []string{"", "a b"}},
		{"DownloadFilename", DownloadFilename, []string{"", ".", "..", "mod.jar ", "LPT1.txt", "com9", "a|b", "a*b", "a?b", "a<b", "a\tb", strings.Repeat("a", 256)}},
		{"DownloadURL", downloadURL, []string{"", "/x.jar", "https://staging-cdn.modrinth.com/x.jar", "ftp://cdn.modrinth.com/x"}},
	}
	for _, tc := range cases {
		for _, v := range tc.vals {
			if err := tc.f(v); !isInvalid(err) {
				t.Errorf("%s(%q) = %v, want invalid_input", tc.fn, v, err)
			}
		}
	}
}

func TestHash(t *testing.T) {
	up := strings.Repeat("AB", 20)
	if got, err := Hash(up, "sha1"); err != nil || got != strings.ToLower(up) {
		t.Errorf("Hash(sha1) = %q, %v", got, err)
	}
	if _, err := Hash(strings.Repeat("a", 40), "sha512"); !isInvalid(err) {
		t.Errorf("40 digits as sha512: %v", err)
	}
	if _, err := Hash(strings.Repeat("g", 40), ""); !isInvalid(err) {
		t.Errorf("non-hex: %v", err)
	}
	if _, err := Hash(strings.Repeat("a", 40), "md5"); !isInvalid(err) {
		t.Errorf("md5: %v", err)
	}
}

func TestParseFacet(t *testing.T) {
	for in, want := range map[string]string{
		"categories=fabric": "categories:fabric",
		"downloads>=10":     "downloads>=10",
		"versions:1.20.1":   "versions:1.20.1",
		"project_type!=mod": "project_type!=mod",
	} {
		if got, err := ParseFacet(in); err != nil || got != want {
			t.Errorf("ParseFacet(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestDownloadURLHosts(t *testing.T) {
	if err := DownloadURL("https://staging-cdn.modrinth.com/x.jar", true, ""); err != nil {
		t.Errorf("staging cdn: %v", err)
	}
	if err := DownloadURL("https://cdn.modrinth.com/x.jar", true, ""); err != nil {
		t.Errorf("cdn on staging: %v", err)
	}
	if err := DownloadURL("http://127.0.0.1:8080/x.jar", false, "127.0.0.1"); err != nil {
		t.Errorf("loopback: %v", err)
	}
	if err := DownloadURL("http://127.0.0.1:8080/x.jar", false, ""); !isInvalid(err) {
		t.Errorf("loopback without a loopback API: %v", err)
	}
	if err := DownloadURL("http://evil.example/x.jar", false, "evil.example"); !isInvalid(err) {
		t.Errorf("non-loopback host passed as loopbackHost: %v", err)
	}
}

func TestLimit(t *testing.T) {
	for _, n := range []int{0, 5, 100} {
		if err := Limit(n, 100); err != nil {
			t.Errorf("Limit(%d, 100) = %v", n, err)
		}
	}
	for _, n := range []int{-1, 101} {
		if err := Limit(n, 100); !isInvalid(err) {
			t.Errorf("Limit(%d, 100) = %v", n, err)
		}
	}
}

func TestFileRules(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	jar, upper, empty, txt := write("a.jar", "x"), write("b.MRPACK", "x"), write("c.jar", ""), write("d.txt", "x")
	png := write("e.png", "x")
	for _, p := range []string{jar, upper} {
		if err := UploadFile(p, VersionFileExts); err != nil {
			t.Errorf("UploadFile(%s) = %v", p, err)
		}
	}
	if err := UploadFile(png, ImageExts); err != nil {
		t.Errorf("UploadFile(png) = %v", err)
	}
	for _, p := range []string{empty, txt, png, dir, filepath.Join(dir, "missing.jar")} {
		if err := UploadFile(p, VersionFileExts); !isInvalid(err) {
			t.Errorf("UploadFile(%s) = %v, want invalid_input", p, err)
		}
	}
	if err := Dir(dir); err != nil {
		t.Errorf("Dir = %v", err)
	}
	for _, p := range []string{jar, filepath.Join(dir, "nope")} {
		if err := Dir(p); !isInvalid(err) {
			t.Errorf("Dir(%s) = %v", p, err)
		}
	}
	// A missing directory gets a hint to create it.
	nope := filepath.Join(dir, "nope")
	var ae *apperr.Error
	if !errors.As(Dir(nope), &ae) || ae.Hint != "create it first, e.g. mkdir -p "+nope {
		t.Errorf("Dir(missing) hint = %v", ae)
	}
}
