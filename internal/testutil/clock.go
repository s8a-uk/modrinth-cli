package testutil

import (
	"context"
	"sync"
	"time"
)

// FakeClock is an api.Clock whose Sleep returns at once (or with ctx.Err() when ctx
// is already done), records the duration and advances Now by it.
type FakeClock struct {
	mu    sync.Mutex
	now   time.Time
	Slept []time.Duration
}

// NewFakeClock returns a FakeClock starting at a fixed time.
func NewFakeClock() *FakeClock {
	return &FakeClock{now: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)}
}

// Now returns the fake time.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Sleep records d and advances the clock without blocking.
func (c *FakeClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Slept = append(c.Slept, d)
	c.now = c.now.Add(d)
	return nil
}

// Advance moves the clock forward without recording a sleep.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}
