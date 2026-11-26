package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/s8a-uk/modrinth-cli/internal/apperr"
)

// Retry and rate-limit tuning.
const (
	max429Retries       = 3               // retries after a 429
	maxTransientRetries = 2               // retries after a 5xx or transport error (idempotent requests only)
	defaultRetryAfter   = 5 * time.Second // 429 wait when the response does not say
	resetPadding        = time.Second     // added to X-Ratelimit-Reset
	noteThreshold       = 3 * time.Second // waits at least this long are announced on stderr
)

// transientBackoff is the wait before each transient retry.
var transientBackoff = [maxTransientRetries]time.Duration{1 * time.Second, 3 * time.Second}

// Clock is the source of time and sleeping, replaced in tests.
type Clock interface {
	Now() time.Time
	Sleep(ctx context.Context, d time.Duration) error // returns ctx.Err() when ctx is done first
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// rateState is the last rate-limit state the API reported. It lives only in memory.
type rateState struct {
	known     bool
	remaining int
	resetAt   time.Time // time the response arrived + X-Ratelimit-Reset seconds
}

// update records X-Ratelimit-Remaining and X-Ratelimit-Reset when both are integers.
func (s *rateState) update(h http.Header, now time.Time) {
	rem, reset := headerInt(h, "X-Ratelimit-Remaining"), headerInt(h, "X-Ratelimit-Reset")
	if rem < 0 || reset < 0 {
		return
	}
	s.known, s.remaining, s.resetAt = true, rem, now.Add(time.Duration(reset)*time.Second)
}

func idempotent(r Request) bool {
	return r.Method == http.MethodGet || r.Method == http.MethodHead || r.Idempotent
}

// retry runs send until it succeeds or a failure is final:
//
//   - before each attempt, if the last response said no requests remain until a reset
//     time in the future, wait for it (counted against MaxWait);
//   - 429 is retried up to max429Retries times for any method, after
//     X-Ratelimit-Reset + 1s (else Retry-After, else 5s), as long as the total wait
//     stays within MaxWait;
//   - 5xx and transport errors (except TLS) are retried maxTransientRetries times
//     after transientBackoff, for idempotent requests only (not counted against MaxWait);
//   - NoRetry disables all of it.
func (c *Client) retry(ctx context.Context, r Request, info *apperr.RequestInfo, send func() (*Response, *apperr.Error)) (*Response, error) {
	var waited time.Duration
	n429, nTransient := 0, 0
	for {
		// PREFLIGHT
		if now := c.opts.Clock.Now(); !c.opts.NoRetry && c.rate.known && c.rate.remaining == 0 && now.Before(c.rate.resetAt) {
			d := c.rate.resetAt.Sub(now) + resetPadding
			if waited+d > c.opts.MaxWait {
				return nil, rateLimited(d, info)
			}
			if err := c.wait(ctx, d, info, "rate limited; waiting %ds (use --max-wait 0 to fail fast)", seconds(d)); err != nil {
				return nil, err
			}
			waited += d
		}

		// SEND
		resp, e := send()
		if e == nil {
			return resp, nil
		}

		// CLASSIFY
		switch {
		case e.Code == apperr.RateLimited:
			d := e.RetryAfter
			if c.opts.NoRetry || n429 == max429Retries || waited+d > c.opts.MaxWait {
				return nil, e
			}
			n429++
			if err := c.wait(ctx, d, info, "rate limited; waiting %ds (use --max-wait 0 to fail fast)", seconds(d)); err != nil {
				return nil, err
			}
			waited += d
		case transient(e):
			if c.opts.NoRetry || !idempotent(r) || nTransient == maxTransientRetries {
				return nil, e
			}
			d := transientBackoff[nTransient]
			nTransient++
			reason := e.Message
			if e.HTTPStatus == 0 {
				reason = string(e.Code)
			}
			if err := c.wait(ctx, d, info, "request failed (%s); retrying in %ds", reason, seconds(d)); err != nil {
				return nil, err
			}
		default:
			return nil, e
		}
	}
}

// transient reports whether e may succeed on an immediate retry: a 5xx, or a transport
// failure other than TLS and interruption.
func transient(e *apperr.Error) bool {
	switch e.Code {
	case apperr.ServerError, apperr.Network, apperr.DNS, apperr.Timeout:
		return true
	}
	return false
}

// wait sleeps for d, announcing it first when it is at least noteThreshold.
func (c *Client) wait(ctx context.Context, d time.Duration, info *apperr.RequestInfo, format string, args ...any) error {
	if d >= noteThreshold {
		c.opts.Log.Note(format, args...)
	}
	if err := c.opts.Clock.Sleep(ctx, d); err != nil {
		e := apperr.New(apperr.Interrupted, "interrupted").WithCause(err)
		e.Request = info
		return e
	}
	return nil
}

// rateLimited is the error for a wait that would exceed MaxWait before sending.
func rateLimited(d time.Duration, info *apperr.RequestInfo) *apperr.Error {
	e := apperr.New(apperr.RateLimited, "rate limited")
	e.RetryAfter, e.Request = d, info
	e.Hint = fmt.Sprintf("retry after %ds", seconds(d))
	return e
}

// seconds rounds d up to whole seconds.
func seconds(d time.Duration) int64 {
	return int64((d + time.Second - 1) / time.Second)
}
