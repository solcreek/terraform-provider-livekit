// SPDX-License-Identifier: MPL-2.0

package livekit

import (
	"context"
	"sync"
	"time"
)

// The API allows a session about 60 requests in any 60 seconds and answers
// the rest with 429, without a Retry-After or rate limit headers; refused
// requests seem to count against the window too (observed 2026-11-23). The
// client therefore paces itself below that: a burst of DefaultBurst, then
// DefaultRequestsPerMinute.
const (
	DefaultRequestsPerMinute = 55
	DefaultBurst             = 50
)

// limiter is a token bucket.
type limiter struct {
	mu     sync.Mutex
	tokens float64
	burst  float64
	perSec float64
	last   time.Time
}

func newLimiter(perMinute, burst int) *limiter {
	if perMinute <= 0 {
		return nil
	}
	if burst < 1 {
		burst = 1
	}
	return &limiter{tokens: float64(burst), burst: float64(burst), perSec: float64(perMinute) / 60, last: time.Now()}
}

// wait blocks until a request may be sent. A nil limiter never waits.
func (l *limiter) wait(ctx context.Context) error {
	if l == nil {
		return nil
	}
	for {
		l.mu.Lock()
		now := time.Now()
		l.tokens = min(l.burst, l.tokens+now.Sub(l.last).Seconds()*l.perSec)
		l.last = now
		if l.tokens >= 1 {
			l.tokens--
			l.mu.Unlock()
			return nil
		}
		need := time.Duration((1 - l.tokens) / l.perSec * float64(time.Second))
		l.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(need):
		}
	}
}
