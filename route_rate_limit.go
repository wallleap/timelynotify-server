package main

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mritd/logger"

	"github.com/wallleap/timelynotify-server/internal/ratelimit"
)

// ipLimiter is the configured per-IP limiter; nil means rate limiting disabled.
var ipLimiter *ratelimit.Limiter

// rateLimitPush controls whether the per-IP limiter also applies to the push
// endpoints (/push and /:device_key). Push is unlimited by default: legitimate
// clients push frequently, while /register and /mcp* are the abuse-prone paths.
var rateLimitPush bool

// setupRateLimits configures the per-IP limiter from flags. ipRate is in
// requests/second. burst<=0 defaults to ipRate. push enables limiting on the
// push endpoints in addition to /register and /mcp*.
func setupRateLimits(ipRate, burst int, push bool) {
	rateLimitPush = push
	if ipRate <= 0 {
		return
	}
	if burst <= 0 {
		burst = ipRate
	}
	if burst < 1 {
		burst = 1
	}
	ipLimiter = ratelimit.New(float64(burst), float64(ipRate))
	logger.Infof("rate limit enabled: %d req/s per IP (burst %d), push endpoints included: %t", ipRate, burst, push)
}

// limitIP returns the shared 429 handling for a rejected request.
func limitIP(c *fiber.Ctx) error {
	return c.Status(429).JSON(failed(429, "rate limit exceeded, retry later"))
}

// rateLimitMiddleware rejects requests that exceed the configured per-IP limit.
// It applies to /register and /mcp* (abuse-prone, always limited when enabled).
// When no limiter is configured (rate limiting disabled), requests pass through.
func rateLimitMiddleware(c *fiber.Ctx) error {
	if ratelimit.ShouldLimit(ipLimiter, c.IP()) {
		return limitIP(c)
	}
	return c.Next()
}

// rateLimitPushMiddleware is rateLimitMiddleware but gated by the optional push
// opt-in, so push endpoints stay unlimited unless explicitly enabled.
func rateLimitPushMiddleware(c *fiber.Ctx) error {
	if !rateLimitPush {
		return c.Next()
	}
	if ratelimit.ShouldLimit(ipLimiter, c.IP()) {
		return limitIP(c)
	}
	return c.Next()
}
