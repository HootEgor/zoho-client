package services

import (
	"context"
	_ "time"

	"golang.org/x/time/rate"
)

// Simple package-level rate limiter for SmartSender requests. Configurable via variables.
var (
	// Default values; you can expose setters or read from config if needed.
	defaultRate  = rate.Limit(5) // requests per second
	defaultBurst = 10
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
