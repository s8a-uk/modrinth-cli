package commands_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/s8a-uk/modrinth-cli/internal/testutil"
)

func rateLimited(m *testutil.MockAPI, path string) *testutil.Stub {
	return m.On("GET", path).Status(429).Header("X-Ratelimit-Remaining", "0").Header("X-Ratelimit-Reset", "7")
}

// M3-1: a 429 is waited out (X-Ratelimit-Reset + 1s) and announced.
func TestRateLimitThenSuccess(t *testing.T) {
	m := testutil.NewMockAPI(t)
	rateLimited(m, "/v2/project/sodium")
	m.On("GET", "/v2/project/sodium").Fixture("project_sodium")
	clock := testutil.NewFakeClock()
	r := testutil.Run(t, m, testutil.RunOpts{Clock: clock}, "project", "get", "sodium")
	if r.Code != 0 || !slices.Equal(clock.Slept, []time.Duration{8 * time.Second}) {
		t.Fatalf("code %d, slept %v, stderr %q", r.Code, clock.Slept, r.Stderr)
	}
	if r.Stderr != "note: rate limited; waiting 8s (use --max-wait 0 to fail fast)\n" {
		t.Errorf("stderr = %q", r.Stderr)
	}
	if !strings.Contains(r.Stdout, `"id":"AANobbMI"`) {
		t.Errorf("stdout = %.60q", r.Stdout)
	}
}

// M3-2: --max-wait 0 fails at once with exit 6 and retry_after_s.
func TestMaxWaitZero(t *testing.T) {
	m := testutil.NewMockAPI(t)
	rateLimited(m, "/v2/project/sodium")
	clock := testutil.NewFakeClock()
	r := testutil.Run(t, m, testutil.RunOpts{Clock: clock}, "project", "get", "sodium", "--max-wait", "0")
	want := `{"error":{"code":"rate_limited","message":"rate limited","http_status":429,"retryable":true,"retry_after_s":8,` +
		`"hint":"retry after 8s","request":{"method":"GET","url":"<BASE>/v2/project/sodium"},"api_error":null,"api_description":null}}` + "\n"
	if r.Code != 6 || testutil.Normalize(r.Stderr) != want || len(clock.Slept) != 0 || len(m.Requests()) != 1 {
		t.Errorf("code %d, slept %v, %d requests, stderr\n got %q\nwant %q", r.Code, clock.Slept, len(m.Requests()), testutil.Normalize(r.Stderr), want)
	}
}

// M3-3: 5xx on GET is retried after 1s and 3s. (POST is covered in internal/api.)
func TestServerErrorRetriesGET(t *testing.T) {
	m := testutil.NewMockAPI(t)
	m.On("GET", "/v2/project/sodium").Status(502).Times(2)
	m.On("GET", "/v2/project/sodium").Fixture("project_sodium")
	clock := testutil.NewFakeClock()
	r := testutil.Run(t, m, testutil.RunOpts{Clock: clock}, "project", "get", "sodium")
	if r.Code != 0 || !slices.Equal(clock.Slept, []time.Duration{time.Second, 3 * time.Second}) {
		t.Errorf("code %d, slept %v, stderr %q", r.Code, clock.Slept, r.Stderr)
	}

	m = testutil.NewMockAPI(t)
	m.On("GET", "/v2/project/sodium").Status(500).Times(3)
	r = testutil.Run(t, m, testutil.RunOpts{}, "project", "get", "sodium")
	if r.Code != 8 || len(m.Requests()) != 3 || !strings.Contains(r.Stderr, `"code":"server_error"`) {
		t.Errorf("code %d, %d requests, stderr %q", r.Code, len(m.Requests()), r.Stderr)
	}
}

// M3-4: --no-retry sends exactly once for both 429 and 5xx.
func TestNoRetry(t *testing.T) {
	for _, status := range []int{429, 503} {
		m := testutil.NewMockAPI(t)
		m.On("GET", "/v2/project/sodium").Status(status).Header("X-Ratelimit-Reset", "7")
		clock := testutil.NewFakeClock()
		r := testutil.Run(t, m, testutil.RunOpts{Clock: clock}, "project", "get", "sodium", "--no-retry")
		want := map[int]int{429: 6, 503: 8}[status]
		if r.Code != want || len(m.Requests()) != 1 || len(clock.Slept) != 0 {
			t.Errorf("%d: code %d, %d requests, slept %v", status, r.Code, len(m.Requests()), clock.Slept)
		}
	}
}

// M3-5: --dry-run prints the plan (03 6.3) and sends nothing.
func TestDryRun(t *testing.T) {
	cases := []struct {
		name string
		args []string
		env  map[string]string
		tty  bool
		want string
	}{
		{name: "json", args: []string{"project", "get", "sodium", "--dry-run"},
			want: `{"dry_run":true,"request":{"method":"GET","url":"<BASE>/v2/project/sodium","headers":{"Accept":"application/json",` +
				`"User-Agent":"s8a-uk/modrinth-cli/<VERSION> (https://github.com/s8a-uk/modrinth-cli)"}}}` + "\n"},
		{name: "token is masked", args: []string{"project", "get", "sodium", "--dry-run"}, env: map[string]string{"MODRINTH_TOKEN": canary},
			want: `{"dry_run":true,"request":{"method":"GET","url":"<BASE>/v2/project/sodium","headers":{"Accept":"application/json",` +
				`"Authorization":"***","User-Agent":"s8a-uk/modrinth-cli/<VERSION> (https://github.com/s8a-uk/modrinth-cli)"}}}` + "\n"},
		{name: "table prints indented json", args: []string{"api", "/statistics", "--dry-run", "-o", "table"},
			want: "{\n  \"dry_run\": true,\n  \"request\": {\n    \"method\": \"GET\",\n    \"url\": \"<BASE>/v2/statistics\",\n" +
				"    \"headers\": {\n      \"Accept\": \"application/json\",\n" +
				"      \"User-Agent\": \"s8a-uk/modrinth-cli/<VERSION> (https://github.com/s8a-uk/modrinth-cli)\"\n    }\n  }\n}\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := testutil.NewMockAPI(t)
			r := testutil.Run(t, m, testutil.RunOpts{Env: tc.env, StdoutTTY: tc.tty}, tc.args...)
			if r.Code != 0 || r.Stderr != "" {
				t.Errorf("code %d, stderr %q", r.Code, r.Stderr)
			}
			if got := testutil.Normalize(r.Stdout); got != tc.want {
				t.Errorf("stdout\n got %q\nwant %q", got, tc.want)
			}
			if len(m.Requests()) != 0 {
				t.Errorf("requests were sent: %v", m.Requests())
			}
		})
	}
}

// M3-6: -v logs each attempt, -vv adds headers (token masked), -q drops notes.
func TestVerbosity(t *testing.T) {
	m := testutil.NewMockAPI(t)
	rateLimited(m, "/v2/project/sodium")
	m.On("GET", "/v2/project/sodium").Fixture("project_sodium")
	r := testutil.Run(t, m, testutil.RunOpts{}, "project", "get", "sodium", "-v")
	want := "> GET <BASE>/v2/project/sodium\n< 429 (0ms) ratelimit-remaining=0\n" +
		"note: rate limited; waiting 8s (use --max-wait 0 to fail fast)\n" +
		"> GET <BASE>/v2/project/sodium\n< 200 (0ms) ratelimit-remaining=299\n"
	if got := testutil.Normalize(r.Stderr); r.Code != 0 || got != want {
		t.Errorf("-v: code %d, stderr\n got %q\nwant %q", r.Code, got, want)
	}

	m = testutil.NewMockAPI(t)
	m.On("GET", "/v2/project/sodium").Fixture("project_sodium")
	r = testutil.Run(t, m, testutil.RunOpts{Env: map[string]string{"MODRINTH_TOKEN": canary}}, "project", "get", "sodium", "-vv")
	for _, s := range []string{"> GET <BASE>/v2/project/sodium\n", "> Authorization: ***\n", "> User-Agent: s8a-uk/modrinth-cli/",
		"< 200 (0ms) ratelimit-remaining=299\n", "< X-Ratelimit-Limit: 300\n"} {
		if !strings.Contains(testutil.Normalize(r.Stderr), s) {
			t.Errorf("-vv: stderr lacks %q:\n%s", s, r.Stderr)
		}
	}
	if strings.Contains(r.Stderr, "LEAKCANARY") {
		t.Error("-vv leaks the token")
	}

	m = testutil.NewMockAPI(t)
	rateLimited(m, "/v2/project/sodium")
	m.On("GET", "/v2/project/sodium").Fixture("project_sodium")
	r = testutil.Run(t, m, testutil.RunOpts{}, "project", "get", "sodium", "-q")
	if r.Code != 0 || r.Stderr != "" {
		t.Errorf("-q: code %d, stderr %q", r.Code, r.Stderr)
	}
}

// M3-7: cancelling the context (SIGINT) exits 130, during a request or a wait.
func TestInterrupt(t *testing.T) {
	// During a request: the transport blocks until the context is cancelled.
	ctx, cancel := context.WithCancel(context.Background())
	d := blockingDoer{started: make(chan struct{})}
	go func() { <-d.started; cancel() }()
	r := testutil.Run(t, nil, testutil.RunOpts{Doer: d, Context: ctx}, "project", "get", "sodium")
	if r.Code != 130 || !strings.Contains(r.Stderr, `"code":"interrupted"`) {
		t.Errorf("during request: code %d, stderr %q", r.Code, r.Stderr)
	}

	// During a rate-limit wait: cancelled while the 429 is being read.
	m := testutil.NewMockAPI(t)
	rateLimited(m, "/v2/project/sodium")
	ctx, cancel = context.WithCancel(context.Background())
	r = testutil.Run(t, m, testutil.RunOpts{Doer: cancelAfter{cancel: cancel}, Context: ctx}, "project", "get", "sodium")
	if r.Code != 130 || !strings.Contains(r.Stderr, `"code":"interrupted"`) || len(m.Requests()) != 1 {
		t.Errorf("during wait: code %d, %d requests, stderr %q", r.Code, len(m.Requests()), r.Stderr)
	}
}

type blockingDoer struct{ started chan struct{} }

func (d blockingDoer) Do(req *http.Request) (*http.Response, error) {
	close(d.started)
	<-req.Context().Done()
	return nil, req.Context().Err()
}

// cancelAfter sends the request for real, then cancels the context.
type cancelAfter struct{ cancel context.CancelFunc }

func (d cancelAfter) Do(req *http.Request) (*http.Response, error) {
	resp, err := http.DefaultClient.Do(req)
	d.cancel()
	return resp, err
}
