package services

import (
	"context"

	"golang.org/x/time/rate"
)

// Simple package-level rate limiter for SmartSender requests. Configurable via variables.
var (
	// Conservative defaults tuned for SmartSender quota: 180 requests per 60 seconds => 3 req/sec
	// Use lower defaults to avoid hitting the quota: 2 req/sec
	defaultRate  = rate.Limit(2) // requests per second
	defaultBurst = 1
	limiter      = rate.NewLimiter(defaultRate, defaultBurst)
	// You can replace limiter with a different one in tests if necessary.
)

// Acquire blocks until a token is available or context is done.
func Acquire(ctx context.Context) error {
	return limiter.Wait(ctx)
}

// SetLimiter allows tests to replace the limiter.
func SetLimiter(l *rate.Limiter) {
	if l != nil {
		limiter = l
	}
}

// Configure allows runtime configuration of rate and burst. It replaces the limiter.
func Configure(rateLimit float64, burst int) {
	limiter = rate.NewLimiter(rate.Limit(rateLimit), burst)
}
