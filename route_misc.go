package main

import (
	"runtime"
	"time"

	"github.com/gofiber/fiber/v2"
)

func init() {
	registerRoute("misc", func(router fiber.Router) {
		// Return an OK to indicate the server is running normally
		router.Get("/", func(c *fiber.Ctx) error {
			return c.SendString("ok")
		})

		// ping func only returns a "pong" string, usually used to test server response
		router.Get("/ping", func(c *fiber.Ctx) error {
			return c.JSON(CommonResp{
				Code:      200,
				Message:   "pong",
				Timestamp: time.Now().Unix(),
			})
		})

		// healthz func only returns an "ok" string, similar to ping func,
		// healthz func is usually used for health check
		router.Get("/healthz", func(c *fiber.Ctx) error {
			return c.SendString("ok")
		})

		// metrics exposes Prometheus metrics (HTTP counters + Go/process).
		router.Get("/metrics", func(c *fiber.Ctx) error {
			if tnMetrics == nil {
				return c.Status(503).JSON(failed(503, "metrics not initialized"))
			}
			return tnMetrics.Handler()(c)
		})

		// version func returns the server version in CommonResp format,
		// used by Bark/Hotify clients to verify server identity
		router.Get("/version", func(c *fiber.Ctx) error {
			return c.JSON(CommonResp{
				Code:      200,
				Message:   "success",
				Timestamp: time.Now().Unix(),
				Data:      map[string]string{"version": version},
			})
		})

		// info func returns information about the server version
		router.Get("/info", func(c *fiber.Ctx) error {
			resp := map[string]interface{}{
				"version": version,
				"build":   buildDate,
				"arch":    runtime.GOOS + "/" + runtime.GOARCH,
				"commit":  commitID,
			}
			// Device count is sensitive (leaks deployment scale). /info is
			// Basic-Auth-free so probes work, but the device count is only
			// added when this exact request presented valid Basic Auth
			// credentials (basicauth records the username on success).
			if basicAuthEnabled && c.Locals(ctxUsername) != nil {
				devices, _ := db.CountAll()
				resp["devices"] = devices
			}
			return c.JSON(resp)
		})
	})
}
