package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/s8a-uk/modrinth-cli/internal/apperr"
)

func envOf(m map[string]string) Env { return func(k string) string { return m[k] } }

const prodTok = "mrp_PRODPRODPRODPRODPRODPROD"
const stagTok = "mrp_STAGSTAGSTAGSTAGSTAGSTAG"

func TestLoad(t *testing.T) {
	yes, ten, out := true, 10, "json"
	cases := []struct {
		name    string
		env     map[string]string
		flags   Flags
		url     string
		staging bool
		token   string
	}{
		{name: "defaults", url: ProductionURL},
		{name: "production token", env: map[string]string{"MODRINTH_TOKEN": " " + prodTok + "\n", "MODRINTH_STAGING_TOKEN": stagTok},
			url: ProductionURL, token: prodTok},
		{name: "staging env", env: map[string]string{"MODRINTH_STAGING": "1", "MODRINTH_TOKEN": prodTok},
			url: StagingURL, staging: true},
		{name: "staging flag uses staging token", env: map[string]string{"MODRINTH_TOKEN": prodTok, "MODRINTH_STAGING_TOKEN": stagTok},
			flags: Flags{Staging: &yes}, url: StagingURL, staging: true, token: stagTok},
		{name: "api url wins", env: map[string]string{"MODRINTH_API_URL": "http://127.0.0.1:9/v2/", "MODRINTH_STAGING": "true"},
			url: "http://127.0.0.1:9", staging: true},
		{name: "staging host is staging", env: map[string]string{"MODRINTH_API_URL": "https://staging-api.modrinth.com", "MODRINTH_TOKEN": prodTok},
			url: StagingURL, staging: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _, err := Load(envOf(tc.env), tc.flags)
			if err != nil {
				t.Fatal(err)
			}
			if s.APIURL != tc.url || s.Staging != tc.staging || s.Token.Reveal() != tc.token {
				t.Errorf("got url=%q staging=%t token=%q", s.APIURL, s.Staging, s.Token.Reveal())
			}
			if (tc.token != "") != (s.TokenSource == SourceEnv) {
				t.Errorf("token source = %q", s.TokenSource)
			}
		})
	}

	s, _, _, err := Load(envOf(nil), Flags{Timeout: &ten, MaxWait: &ten, Output: &out})
	if err != nil || s.Timeout != 10*time.Second || s.MaxWait != 10*time.Second || s.Output != "json" || s.Sources["timeout"] != SourceFlag {
		t.Errorf("flags: %+v %v", s, err)
	}
	s, _, _, _ = Load(envOf(nil), Flags{})
	if s.Timeout != 30*time.Second || s.MaxWait != 60*time.Second || s.Output != "auto" {
		t.Errorf("defaults: %+v", s)
	}
}

func TestLoadErrors(t *testing.T) {
	for _, env := range []map[string]string{
		{"MODRINTH_STAGING": "yes"},
		{"MODRINTH_API_URL": "http://example.com"},
		{"MODRINTH_TOKEN": "mrp_bad token with spaces inside"},
	} {
		_, _, _, err := Load(envOf(env), Flags{})
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.InvalidInput {
			t.Errorf("%v: err = %v, want invalid_input", env, err)
		}
		if ae != nil && strings.Contains(ae.Message+ae.Hint, "spaces") {
			t.Errorf("error leaks the token: %q", ae.Message)
		}
	}
}

func TestTokenNeverPrints(t *testing.T) {
	tok := NewToken("mrp_SECRETSECRETSECRETwxyz")
	type wrapper struct{ T Token }
	for _, s := range []string{
		fmt.Sprintf("%v", tok), fmt.Sprintf("%+v", tok), fmt.Sprintf("%#v", tok), fmt.Sprintf("[%s]", tok),
		fmt.Sprintf("%v", wrapper{tok}), fmt.Sprintf("%+v", wrapper{tok}), fmt.Sprintf("%#v", wrapper{tok}),
	} {
		if strings.Contains(s, "SECRET") {
			t.Errorf("token printed: %q", s)
		}
	}
	b, _ := json.Marshal(wrapper{tok})
	if strings.Contains(string(b), "SECRET") {
		t.Errorf("json: %s", b)
	}
	if got := tok.Masked(); got != "mrp_…wxyz" {
		t.Errorf("Masked = %q", got)
	}
	if NewToken("short").Masked() != "***" || !NewToken("").IsZero() {
		t.Error("Masked/IsZero")
	}
}
