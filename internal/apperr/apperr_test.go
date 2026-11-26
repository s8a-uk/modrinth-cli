package apperr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"
)

func TestExitCodeAndRetryable(t *testing.T) {
	want := map[Code]struct {
		exit      int
		retryable bool
	}{
		Internal: {1, false}, UnexpectedResponse: {1, false}, IOError: {1, false},
		Usage: {2, false}, InvalidInput: {2, false}, ConfirmationRequired: {2, false},
		UnknownField: {2, false}, FileExists: {2, false},
		NotFound:     {3, false},
		AuthRequired: {4, false}, Unauthorized: {4, false}, Forbidden: {4, false},
		BadRequest: {5, false}, Conflict: {5, false}, APIDeprecated: {5, false},
		PayloadTooLarge: {5, false}, ClientError: {5, false},
		RateLimited: {6, true},
		Network:     {7, true}, DNS: {7, true}, TLS: {7, false}, Timeout: {7, true},
		ServerError:  {8, true},
		HashMismatch: {9, false},
		Interrupted:  {130, false},
	}
	if len(want) != len(AllCodes) {
		t.Fatalf("table has %d codes, AllCodes has %d", len(want), len(AllCodes))
	}
	for _, c := range AllCodes {
		w, ok := want[c]
		if !ok {
			t.Errorf("code %q missing from the test table", c)
			continue
		}
		if got := c.ExitCode(); got != w.exit {
			t.Errorf("%s.ExitCode() = %d, want %d", c, got, w.exit)
		}
		if got := c.Retryable(); got != w.retryable {
			t.Errorf("%s.Retryable() = %t, want %t", c, got, w.retryable)
		}
	}
	if got := Code("bogus").ExitCode(); got != 1 {
		t.Errorf("unknown code exit = %d, want 1", got)
	}
	if Code("bogus").Retryable() {
		t.Error("unknown code is retryable")
	}
}

func TestJSON(t *testing.T) {
	cases := []struct {
		name string
		err  *Error
		want string
	}{
		{
			name: "usage with hint",
			err:  New(Usage, "unknown flag %q for %q", "--loder", "mdr search").WithHint("did you mean --loader?"),
			want: `{"error":{"code":"usage","message":"unknown flag \"--loder\" for \"mdr search\"","http_status":null,"retryable":false,"hint":"did you mean --loader?"}}`,
		},
		{
			name: "not found with request",
			err: &Error{Code: NotFound, Message: "project 'sodiumm' not found", HTTPStatus: 404,
				Hint:    "check the id or slug; try: mdr search sodiumm",
				Request: &RequestInfo{Method: "GET", URL: "https://api.modrinth.com/v2/project/sodiumm"}},
			want: `{"error":{"code":"not_found","message":"project 'sodiumm' not found","http_status":404,"retryable":false,"hint":"check the id or slug; try: mdr search sodiumm","request":{"method":"GET","url":"https://api.modrinth.com/v2/project/sodiumm"},"api_error":null,"api_description":null}}`,
		},
		{
			name: "rate limited rounds retry_after up",
			err: &Error{Code: RateLimited, Message: "rate limited", HTTPStatus: 429, RetryAfter: 41*time.Second + time.Millisecond,
				Hint: "retry after 42s"},
			want: `{"error":{"code":"rate_limited","message":"rate limited","http_status":429,"retryable":true,"retry_after_s":42,"hint":"retry after 42s","api_error":null,"api_description":null}}`,
		},
		{
			name: "api fields, details and excerpt",
			err: &Error{Code: BadRequest, Message: "bad <input> & more", HTTPStatus: 400,
				APIError: "invalid_input", APIDesc: "Error while validating input",
				APIDetails: json.RawMessage(`{ "a" : [1, 2] }`), BodyExcerpt: "x"},
			want: `{"error":{"code":"bad_request","message":"bad <input> & more","http_status":400,"retryable":false,"api_error":"invalid_input","api_description":"Error while validating input","api_details":{"a":[1,2]},"body_excerpt":"x"}}`,
		},
		{
			name: "invalid details are dropped",
			err:  &Error{Code: Internal, Message: "m", APIDetails: json.RawMessage(`{`)},
			want: `{"error":{"code":"internal","message":"m","http_status":null,"retryable":false}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(tc.err.JSON())
			if got != tc.want {
				t.Errorf("JSON()\n got: %s\nwant: %s", got, tc.want)
			}
			if !json.Valid([]byte(got)) {
				t.Error("JSON() is not valid JSON")
			}
		})
	}
}

func TestHuman(t *testing.T) {
	e := &Error{Code: NotFound, Message: "project 'sodiumm' not found", HTTPStatus: 404, Hint: "check the id or slug"}
	want := "error: project 'sodiumm' not found (not_found, HTTP 404)\nhint: check the id or slug\n"
	if got := e.Human(); got != want {
		t.Errorf("Human() = %q, want %q", got, want)
	}
	e = New(Usage, "missing argument <id>")
	if got, want := e.Human(), "error: missing argument <id> (usage)\n"; got != want {
		t.Errorf("Human() = %q, want %q", got, want)
	}
}

func TestFrom(t *testing.T) {
	if From(nil) != nil {
		t.Error("From(nil) != nil")
	}
	orig := New(NotFound, "x")
	if got := From(fmt.Errorf("wrapped: %w", orig)); got != orig {
		t.Errorf("From(wrapped *Error) = %v, want the original", got)
	}
	if got := From(fmt.Errorf("op: %w", context.Canceled)); got.Code != Interrupted {
		t.Errorf("From(Canceled).Code = %s, want interrupted", got.Code)
	}
	got := From(io.ErrUnexpectedEOF)
	if got.Code != Internal || got.Message != io.ErrUnexpectedEOF.Error() || !errors.Is(got, io.ErrUnexpectedEOF) {
		t.Errorf("From(other) = %+v", got)
	}
	if s := orig.Error(); s != "not_found: x" {
		t.Errorf("Error() = %q", s)
	}
}
