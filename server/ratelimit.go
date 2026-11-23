package main

import "time"

// limiter is a token bucket: it allows bursts up to its capacity and, over
// time, `rate` events per second. It is not safe for concurrent use; each
// connection's read loop has its own.
type limiter struct {
	tokens float64
	burst  float64
	rate   float64 // tokens added per second
	last   time.Time
}

// newLimiter returns a full bucket holding `burst` tokens.
func newLimiter(rate float64, burst int, now time.Time) *limiter {
	return &limiter{tokens: float64(burst), burst: float64(burst), rate: rate, last: now}
}

// allow takes one token and reports whether there was one.
func (l *limiter) allow(now time.Time) bool {
	l.tokens = min(l.burst, l.tokens+now.Sub(l.last).Seconds()*l.rate)
	l.last = now
	if l.tokens < 1 {
		return false
	}
	l.tokens--
	return true
}
