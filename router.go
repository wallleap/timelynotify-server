package main

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	fiberlogger "github.com/gofiber/fiber/v2/middleware/logger"
	fiberrecover "github.com/gofiber/fiber/v2/middleware/recover"

	"github.com/gofiber/fiber/v2"

	"github.com/mritd/logger"
)

type CommonResp struct {
	Code      int         `json:"code"`
	Message   string      `json:"message"`
	Data      interface{} `json:"data,omitempty"`
	Timestamp int64       `json:"timestamp"`
}

type routerFunc struct {
	Name   string
	Weight int
	Func   func(router fiber.Router)
}

type routeSlice []routerFunc

func (r routeSlice) Len() int { return len(r) }

func (r routeSlice) Less(i, j int) bool { return r[i].Weight > r[j].Weight }

func (r routeSlice) Swap(i, j int) { r[i], r[j] = r[j], r[i] }

// commonOnce guards the request-scoped middleware registration so that
// routerSetupCommon applies it exactly once per process.
var commonOnce sync.Once

// routeOnce guards route registration.
var routeOnce sync.Once
var routes routeSlice

// register new route with key name
// key name is used to eliminate duplicate routes
// key name not case sensitive
func registerRoute(name string, f func(router fiber.Router)) {
	registerRouteWithWeight(name, 50, f)
}

// register new route with weight
func registerRouteWithWeight(name string, weight int, f func(router fiber.Router)) {
	if weight > 100 || weight < 0 {
		logger.Fatalf("route [%s] weight must be >= 0 and <=100", name)
	}

	for _, r := range routes {
		if strings.EqualFold(name, r.Name) {
			logger.Fatalf("route [%s] already registered", r.Name)
		}
	}

	routes = append(routes, routerFunc{
		Name:   name,
		Weight: weight,
		Func:   f,
	})
}

// tokenParamRe matches a token query parameter together with its leading
// '?' or '&', so the value can be masked in logs. token values are
// base64url, so [^&\s]* covers the whole value.
var tokenParamRe = regexp.MustCompile(`([?&]token=)[^&\s]*`)

// redactingWriter masks the value of the "token" query parameter in whatever
// it forwards, e.g. /stream?token=<clientToken> is logged as ?token=***.
// Other query params and the request body pass through untouched.
type redactingWriter struct{ w io.Writer }

func (r redactingWriter) Write(p []byte) (int, error) {
	redacted := tokenParamRe.ReplaceAll(p, []byte(`${1}***`))
	if _, err := r.w.Write(redacted); err != nil {
		return 0, err
	}
	return len(p), nil
}

// routerSetupCommon installs the request-scoped middleware (access log,
// recover, metrics). It must be registered before the Basic Auth middleware:
// a rejected request returns early from the auth gate without calling Next, so
// anything mounted after it (like the logger) would never see the request.
func routerSetupCommon(router fiber.Router) {
	commonOnce.Do(func() {
		router.Use(fiberlogger.New(fiberlogger.Config{
			// No ${body}: request payloads carry push content and credentials
			// (device_token / device_key) and must not land in logs. Audit of
			// what was pushed is served by the gotify compat history
			// (/message). The token query param is masked by redactingWriter.
			Format:     "${time}     INFO    ${ip} -> [${status}] ${method} ${latency} ${route} => ${url}\n",
			TimeFormat: "2006-01-02 15:04:05",
			Output:     redactingWriter{w: os.Stdout},
		}))
		router.Use(fiberrecover.New())
		// Instrument every request (guarded: tnMetrics is nil until
		// runServer initializes it).
		if tnMetrics != nil {
			router.Use(tnMetrics.Middleware())
		}
	})
}

// routerSetupRoutes registers the application routes. Split from the common
// middleware so Basic Auth can sit between logging and the endpoints.
func routerSetupRoutes(router fiber.Router) {
	routeOnce.Do(func() {
		sort.Sort(routes)
		for _, r := range routes {
			r.Func(router)
			logger.Infof("load route [%s] success...", r.Name)
		}
	})
}

// routerSetup registers the request-scoped middleware and then the routes.
// Kept for tests (NewServer) that build an app without Basic Auth.
func routerSetup(router fiber.Router) {
	routerSetupCommon(router)
	routerSetupRoutes(router)
}

// for the fast return success result
func success() CommonResp {
	return CommonResp{
		Code:      200,
		Message:   "success",
		Timestamp: time.Now().Unix(),
	}
}

// for the fast return failed result
func failed(code int, message string, args ...interface{}) CommonResp {
	return CommonResp{
		Code:      code,
		Message:   fmt.Sprintf(message, args...),
		Timestamp: time.Now().Unix(),
	}
}

// for the fast return result with custom data
func data(data interface{}) CommonResp {
	return CommonResp{
		Code:      200,
		Message:   "success",
		Timestamp: time.Now().Unix(),
		Data:      data,
	}
}
