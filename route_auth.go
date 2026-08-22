package main

import (
	"github.com/gofiber/fiber/v2"
	fiberbasicauth "github.com/gofiber/fiber/v2/middleware/basicauth"
	"github.com/mritd/logger"

	"github.com/wallleap/timelynotify-server/internal/authfree"
)

// basicAuthEnabled reports whether Basic Auth was configured at startup. It
// gates features that should not be exposed to an unauthenticated network
// (e.g. the device count on GET /info).
var basicAuthEnabled bool

// ctxUsername is the Locals key the BasicAuth middleware records the
// authenticated username under, on success only. /info uses it to decide
// whether the request presented valid credentials.
const ctxUsername = "username"

// isAuthFreePath reports whether p is (or is under) a Basic-Auth whitelisted
// path. Logic lives in internal/authfree so it is unit-testable without the
// package-main deviceToken gate; this wrapper keeps the call site stable.
func isAuthFreePath(urlPrefix, p string) bool {
	return authfree.IsAuthFreePath(urlPrefix, p)
}

func routerAuth(user, passwd string, router fiber.Router, urlPrefix string) {
	basicAuthEnabled = user != "" && passwd != ""
	if user == "" && passwd == "" {
		logger.Warn("************************************************************")
		logger.Warn("TimelyNotify Server Has NO Basic Auth.")
		logger.Warn("PUBLIC deployments should set BARK_SERVER_BASIC_AUTH_USER/PASSWORD.")
		logger.Warn("/push, /register, /mcp* and /:device_key are OPEN to everyone.")
		logger.Warn("************************************************************")
		return
	}

	logger.Info("TimelyNotify Server Has Basic Auth Enabled.")
	basicAuth := fiberbasicauth.New(fiberbasicauth.Config{
		Users:           map[string]string{user: passwd},
		Realm:           "Coffee Time",
		ContextUsername: ctxUsername,
		Unauthorized: func(c *fiber.Ctx) error {
			if isAuthFreePath(urlPrefix, c.Path()) {
				return c.Next()
			}
			return c.Status(418).SendString("I'm a teapot")
		},
	})

	router.Use("/+", basicAuth)
}
