// Package apperr defines the structured error type shared by every layer of mdr,
// the stable error codes, their exit codes, and the JSON / human renderings.
package apperr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// Code is a stable, machine-readable error identifier (lower snake case).
type Code string

// Error codes. Agents branch on these; never rename one.
const (
	Internal             Code = "internal"
	UnexpectedResponse   Code = "unexpected_response"
	IOError              Code = "io_error"
	Usage                Code = "usage"
	InvalidInput         Code = "invalid_input"
	ConfirmationRequired Code = "confirmation_required"
	UnknownField         Code = "unknown_field"
	FileExists           Code = "file_exists"
	NotFound             Code = "not_found"
	AuthRequired         Code = "auth_required"
	Unauthorized         Code = "unauthorized"
	Forbidden            Code = "forbidden"
	BadRequest           Code = "bad_request"
	Conflict             Code = "conflict"
	APIDeprecated        Code = "api_deprecated"
	PayloadTooLarge      Code = "payload_too_large"
	ClientError          Code = "client_error"
	RateLimited          Code = "rate_limited"
	Network              Code = "network"
	DNS                  Code = "dns"
	TLS                  Code = "tls"
	Timeout              Code = "timeout"
	ServerError          Code = "server_error"
	HashMismatch         Code = "hash_mismatch"
	Interrupted          Code = "interrupted"
)

// AllCodes lists every known code in declaration order.
var AllCodes = []Code{
	Internal, UnexpectedResponse, IOError,
	Usage, InvalidInput, ConfirmationRequired, UnknownField, FileExists,
	NotFound,
	AuthRequired, Unauthorized, Forbidden,
	BadRequest, Conflict, APIDeprecated, PayloadTooLarge, ClientError,
	RateLimited,
	Network, DNS, TLS, Timeout,
	ServerError,
	HashMismatch,
	Interrupted,
}

// ExitCode returns the process exit code for c. Unknown codes map to 1.
func (c Code) ExitCode() int {
	switch c {
	case Internal, UnexpectedResponse, IOError:
		return 1
	case Usage, InvalidInput, ConfirmationRequired, UnknownField, FileExists:
		return 2
	case NotFound:
		return 3
	case AuthRequired, Unauthorized, Forbidden:
		return 4
	case BadRequest, Conflict, APIDeprecated, PayloadTooLarge, ClientError:
		return 5
	case RateLimited:
		return 6
	case Network, DNS, TLS, Timeout:
		return 7
	case ServerError:
		return 8
	case HashMismatch:
		return 9
	case Interrupted:
		return 130
	}
	return 1
}

// Retryable reports whether running the same command later may succeed.
func (c Code) Retryable() bool {
	switch c {
	case RateLimited, Network, DNS, Timeout, ServerError:
		return true
	}
	return false
}

// RequestInfo identifies the request an error belongs to. The URL never contains a token.
type RequestInfo struct {
	Method string `json:"method"`
	URL    string `json:"url"`
}

// Error is the single error type rendered to users and agents.
type Error struct {
	Code        Code
	Message     string        // one English sentence, no trailing period
	HTTPStatus  int           // 0 = not an HTTP error
	Hint        string        // what to do next; omitted when empty
	RetryAfter  time.Duration // RateLimited only
	Request     *RequestInfo
	APIError    string          // the API's "error"
	APIDesc     string          // the API's "description"
	APIDetails  json.RawMessage // the API's "details", if any
	BodyExcerpt string          // first bytes of a non-JSON error body
	Cause       error           // for errors.Unwrap; never rendered
}

// JSONKeys returns the keys an error object may contain, in output order.
func JSONKeys() []string {
	return []string{
		"code", "message", "http_status", "retryable", "retry_after_s", "hint",
		"request", "api_error", "api_description", "api_details", "body_excerpt",
	}
}

// Error returns "<code>: <message>".
func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }

// Unwrap returns the cause.
func (e *Error) Unwrap() error { return e.Cause }

// New creates an error with a formatted message.
func New(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// WithHint sets the hint and returns e.
func (e *Error) WithHint(format string, args ...any) *Error {
	e.Hint = fmt.Sprintf(format, args...)
	return e
}

// WithCause sets the cause and returns e.
func (e *Error) WithCause(err error) *Error {
	e.Cause = err
	return e
}

// From converts any error to *Error: an *Error (anywhere in the chain) is returned as is,
// context.Canceled becomes Interrupted, and everything else becomes Internal.
func From(err error) *Error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	if errors.Is(err, context.Canceled) {
		return New(Interrupted, "interrupted").WithCause(err)
	}
	return New(Internal, "%s", err.Error()).WithCause(err)
}

// JSON returns the error as a single-line JSON object {"error":{...}} without a trailing newline.
func (e *Error) JSON() []byte {
	var b bytes.Buffer
	b.WriteString(`{"error":{"code":`)
	writeString(&b, string(e.Code))
	b.WriteString(`,"message":`)
	writeString(&b, e.Message)
	b.WriteString(`,"http_status":`)
	if e.HTTPStatus != 0 {
		fmt.Fprintf(&b, "%d", e.HTTPStatus)
	} else {
		b.WriteString("null")
	}
	fmt.Fprintf(&b, `,"retryable":%t`, e.Code.Retryable())
	if e.Code == RateLimited {
		fmt.Fprintf(&b, `,"retry_after_s":%d`, int64(math.Ceil(e.RetryAfter.Seconds())))
	}
	if e.Hint != "" {
		b.WriteString(`,"hint":`)
		writeString(&b, e.Hint)
	}
	if e.Request != nil {
		b.WriteString(`,"request":{"method":`)
		writeString(&b, e.Request.Method)
		b.WriteString(`,"url":`)
		writeString(&b, e.Request.URL)
		b.WriteString("}")
	}
	if e.HTTPStatus != 0 {
		b.WriteString(`,"api_error":`)
		writeStringOrNull(&b, e.APIError)
		b.WriteString(`,"api_description":`)
		writeStringOrNull(&b, e.APIDesc)
	}
	if len(e.APIDetails) > 0 && json.Valid(e.APIDetails) {
		var c bytes.Buffer
		if json.Compact(&c, e.APIDetails) == nil {
			b.WriteString(`,"api_details":`)
			b.Write(c.Bytes())
		}
	}
	if e.BodyExcerpt != "" {
		b.WriteString(`,"body_excerpt":`)
		writeString(&b, e.BodyExcerpt)
	}
	b.WriteString("}}")
	return b.Bytes()
}

// Human returns "error: <message> (<code>[, HTTP <status>])\n" followed by "hint: <hint>\n" if any.
func (e *Error) Human() string {
	var b strings.Builder
	b.WriteString("error: ")
	b.WriteString(e.Message)
	b.WriteString(" (")
	b.WriteString(string(e.Code))
	if e.HTTPStatus != 0 {
		fmt.Fprintf(&b, ", HTTP %d", e.HTTPStatus)
	}
	b.WriteString(")\n")
	if e.Hint != "" {
		b.WriteString("hint: ")
		b.WriteString(e.Hint)
		b.WriteString("\n")
	}
	return b.String()
}

func writeString(b *bytes.Buffer, s string) {
	enc := json.NewEncoder(b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s) // encoding a string cannot fail
	b.Truncate(b.Len() - 1)
}

func writeStringOrNull(b *bytes.Buffer, s string) {
	if s == "" {
		b.WriteString("null")
		return
	}
	writeString(b, s)
}
