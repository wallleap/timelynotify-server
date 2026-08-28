package main

import (
	"encoding/base64"
	"io"
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v2"
	fiberbasicauth "github.com/gofiber/fiber/v2/middleware/basicauth"
)

// newAuthProbeApp builds a minimal fiber app with the same Basic Auth gate
// wiring as routerAuth plus a handler that echoes the gotifyToken() result,
// so the interaction between the gate and the in-API token sources can be
// asserted directly.
func newAuthProbeApp() *fiber.App {
	app := fiber.New()
	app.Use(fiberbasicauth.New(fiberbasicauth.Config{
		Users: map[string]string{"admin": "secret"},
		Unauthorized: func(c *fiber.Ctx) error {
			return c.Status(418).SendString("teapot")
		},
	}))
	app.Get("/probe", func(c *fiber.Ctx) error {
		return c.SendString(gotifyToken(c))
	})
	return app
}

// doAuthProbe issues a GET /probe with the given Authorization header values
// (multiple values = multiple header lines) and optional raw query.
func doAuthProbe(t *testing.T, authValues []string, query string) (int, string) {
	t.Helper()
	url := "/probe" + query
	req, _ := http.NewRequest("GET", url, nil)
	req.Host = "localhost"
	if len(authValues) > 0 {
		req.Header["Authorization"] = authValues
	}
	res, err := newAuthProbeApp().Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(body)
}

func basicHeader() string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte("admin:secret"))
}

// TestBasicAuthWithQueryToken verifies the working combination: Basic in the
// Authorization header passes the gate, the client token travels via the
// ?token= query which does not touch the Authorization header.
func TestBasicAuthWithQueryToken(t *testing.T) {
	code, token := doAuthProbe(t, []string{basicHeader()}, "?token=client-tok")
	if code != 200 {
		t.Fatalf("Basic + query token should pass, got %d", code)
	}
	if token != "client-tok" {
		t.Fatalf("query token should reach the handler, got %q", token)
	}
}

// TestBasicAuthBearerConflict verifies the doc-claimed-but-broken
// combination: Basic and Bearer share the single Authorization header, so
// they cannot coexist. With Basic first, the gate passes but the Bearer
// value is invisible to gotifyToken (fasthttp returns the first header
// line), leaving the handler without a token.
func TestBasicAuthBearerConflict(t *testing.T) {
	code, token := doAuthProbe(t, []string{basicHeader(), "Bearer client-tok"}, "")
	if code != 200 {
		t.Fatalf("Basic-first gate should pass, got %d", code)
	}
	if token != "" {
		t.Fatalf("Bearer must not be reachable behind Basic in the same header, got %q", token)
	}
}

// TestBearerOnlyBlockedByGate verifies the reverse: an Authorization header
// carrying only Bearer fails the Basic Auth gate (418) before any in-API
// token check happens.
func TestBearerOnlyBlockedByGate(t *testing.T) {
	code, _ := doAuthProbe(t, []string{"Bearer client-tok"}, "")
	if code != 418 {
		t.Fatalf("Bearer-only should be rejected by the Basic gate, got %d", code)
	}
}
