package main

import (
	"strconv"
	"strings"
	"time"

	"github.com/wallleap/timelynotify-server/internal/gotifycompat"
	"github.com/gofiber/fiber/v2"
	fiberws "github.com/gofiber/websocket/v2"
	"github.com/mritd/logger"
)

// gotifyService is the lazily-initialized gotify-compatible monitoring service.
var gotifyService *gotifycompat.Service

func init() {
	registerRoute("gotify", func(router fiber.Router) {
		// --- Device-scoped gotify-compatible monitoring endpoints -----
		// Each supports per-device history & live stream. Authentication still
		// uses the global client token (device isolation is not a credential).
		// Static path segments take priority over the legacy push_compat
		// `/:device_key/:body` routes, so these never clash.
		router.Get("/:device_key/version", routeGotifyDeviceVersion)
		router.Get("/:device_key/message", routeGotifyDeviceMessage)
		router.Delete("/:device_key/message", routeGotifyDeviceMessageDeleteAll)
		router.Delete("/:device_key/message/:id", routeGotifyDeviceMessageDeleteOne)
		router.Get("/:device_key/stream", routeGotifyDeviceStreamUpgrade, fiberws.New(routeGotifyStream))
	})
}

// gotifyToken extracts a client token following gotify's precedence:
// query → X-Gotify-Key header → Authorization: Bearer.
func gotifyToken(c *fiber.Ctx) string {
	if t := c.Query("token"); t != "" {
		return t
	}
	if t := c.Get("X-Gotify-Key"); t != "" {
		return t
	}
	if auth := c.Get(fiber.HeaderAuthorization); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

// routeGotifyDeviceVersion serves the version for a device-scoped probe.
func routeGotifyDeviceVersion(c *fiber.Ctx) error {
	if gotifyService == nil {
		logger.Warnf("[Gotify] service not initialized for version check")
		return c.Status(503).JSON(failed(503, "gotify compat not initialized"))
	}
	logger.Infof("[Gotify] version probe: device_key=%s", c.Params("device_key"))
	return c.JSON(map[string]string{"version": gotifyService.Version()})
}

// messageQuery holds the shared limit/since pagination parsed from a request.
type messageQuery struct {
	limit int
	since uint64
}

// parseMessageQuery reads limit (default 100, max 200, min 1) and since
// (ID < since) from the request, following the global /message semantics.
func parseMessageQuery(c *fiber.Ctx) messageQuery {
	limit := c.QueryInt("limit", 100)
	if limit < 1 {
		limit = 1
	}
	if limit > 200 {
		limit = 200
	}
	var since uint64
	if s := c.Query("since"); s != "" {
		since, _ = strconv.ParseUint(s, 10, 64)
	}
	return messageQuery{limit: limit, since: since}
}

// messageResponse wraps a message list in the gotify paging envelope.
func messageResponse(messages []gotifycompat.Message, q messageQuery) map[string]interface{} {
	return map[string]interface{}{
		"paging": map[string]interface{}{
			"size":  len(messages),
			"limit": q.limit,
			"since": q.since,
		},
		"messages": messages,
	}
}

// routeGotifyDeviceMessage is the device-scoped GET /message: only messages of
// the device_key in the URL path are returned.
func routeGotifyDeviceMessage(c *fiber.Ctx) error {
	if gotifyService == nil {
		logger.Warnf("[Gotify] service not initialized for message query")
		return c.Status(503).JSON(failed(503, "gotify compat not initialized"))
	}
	if !gotifyService.ValidateToken(gotifyToken(c)) {
		logger.Warnf("[Gotify] unauthorized message query: device_key=%s", c.Params("device_key"))
		return c.Status(401).JSON(failed(401, "unauthorized"))
	}

	q := parseMessageQuery(c)
	device := c.Params("device_key")
	logger.Infof("[Gotify] message query: device_key=%s limit=%d since=%d", device, q.limit, q.since)

	messages, err := gotifyService.MessagesByDevice(device, q.limit, q.since)
	if err != nil {
		logger.Errorf("[Gotify] message query failed: device_key=%s err=%v", device, err)
		return c.Status(500).JSON(failed(500, "get messages failed: %v", err))
	}
	logger.Infof("[Gotify] message query success: device_key=%s count=%d", device, len(messages))
	return c.JSON(messageResponse(messages, q))
}

// routeGotifyDeviceMessageDeleteAll wipes only the given device's message
// history; other devices' messages are untouched.
func routeGotifyDeviceMessageDeleteAll(c *fiber.Ctx) error {
	if gotifyService == nil {
		logger.Warnf("[Gotify] service not initialized for delete all")
		return c.Status(503).JSON(failed(503, "gotify compat not initialized"))
	}
	if !gotifyService.ValidateToken(gotifyToken(c)) {
		logger.Warnf("[Gotify] unauthorized delete all: device_key=%s", c.Params("device_key"))
		return c.Status(401).JSON(failed(401, "unauthorized"))
	}
	device := c.Params("device_key")
	logger.Infof("[Gotify] delete all messages: device_key=%s", device)
	if err := gotifyService.DeleteAllMessagesByDevice(device); err != nil {
		logger.Errorf("[Gotify] delete all failed: device_key=%s err=%v", device, err)
		return c.Status(500).JSON(failed(500, "delete messages failed: %v", err))
	}
	logger.Infof("[Gotify] delete all success: device_key=%s", device)
	return c.JSON(success())
}

// routeGotifyDeviceMessageDeleteOne removes a single message that belongs to
// the device in the URL path; 404 when it does not exist or belongs to another
// device.
func routeGotifyDeviceMessageDeleteOne(c *fiber.Ctx) error {
	if gotifyService == nil {
		logger.Warnf("[Gotify] service not initialized for delete one")
		return c.Status(503).JSON(failed(503, "gotify compat not initialized"))
	}
	if !gotifyService.ValidateToken(gotifyToken(c)) {
		logger.Warnf("[Gotify] unauthorized delete one: device_key=%s", c.Params("device_key"))
		return c.Status(401).JSON(failed(401, "unauthorized"))
	}
	id, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil {
		logger.Warnf("[Gotify] invalid message id: %s", c.Params("id"))
		return c.Status(400).JSON(failed(400, "invalid message id: %v", err))
	}
	device := c.Params("device_key")
	logger.Infof("[Gotify] delete message: device_key=%s id=%d", device, id)
	existed, err := gotifyService.DeleteMessageByDevice(device, id)
	if err != nil {
		logger.Errorf("[Gotify] delete message failed: device_key=%s id=%d err=%v", device, id, err)
		return c.Status(500).JSON(failed(500, "delete message failed: %v", err))
	}
	if !existed {
		logger.Infof("[Gotify] message not found: device_key=%s id=%d", device, id)
		return c.Status(404).JSON(failed(404, "message not found"))
	}
	logger.Infof("[Gotify] delete message success: device_key=%s id=%d", device, id)
	return c.JSON(success())
}

// routeGotifyDeviceStreamUpgrade validates the token before the WebSocket
// upgrade so that unauthorized clients get a 401 handshake response.
// The device_key is stashed for the stream handler.
func routeGotifyDeviceStreamUpgrade(c *fiber.Ctx) error {
	if gotifyService == nil {
		logger.Warnf("[Gotify] service not initialized for stream")
		return c.Status(503).JSON(failed(503, "gotify compat not initialized"))
	}
	if !fiberws.IsWebSocketUpgrade(c) {
		logger.Warnf("[Gotify] websocket upgrade expected but got: %s", c.Path())
		return c.Status(400).JSON(failed(400, "websocket upgrade expected"))
	}
	token := gotifyToken(c)
	if !gotifyService.ValidateToken(token) {
		logger.Warnf("[Gotify] unauthorized stream: device_key=%s", c.Params("device_key"))
		return c.Status(401).JSON(failed(401, "unauthorized"))
	}
	deviceKey := c.Params("device_key")
	logger.Infof("[Gotify] stream connected: device_key=%s", deviceKey)
	c.Locals("device_key", deviceKey)
	return c.Next()
}

// routeGotifyStream serves the live WebSocket stream of bare gotify message
// JSON frames (no event/socketConnected envelope), matching what the
// hotify-bridge subscriber expects. Only messages for the registered device
// are streamed. It replies to client pings and reaches out with server pings
// so idle NAT'd connections survive. All writes happen in this goroutine
// (no concurrent writes on the connection).
func routeGotifyStream(conn *fiberws.Conn) {
	device := ""
	if v := conn.Locals("device_key"); v != nil {
		device, _ = v.(string)
	}
	ch, unsubscribe := gotifyService.SubscribeByDevice(device)
	defer unsubscribe()
	defer logger.Infof("[Gotify] stream disconnected: device_key=%s subscribers=%d",
		device, gotifyService.SubscriberCount()-1)
	if tnMetrics != nil {
		tnMetrics.SetActiveStreams(float64(gotifyService.SubscriberCount()))
		defer tnMetrics.SetActiveStreams(float64(gotifyService.SubscriberCount()))
	}

	conn.SetReadLimit(512)
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPingHandler(func(appData string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return conn.WriteControl(fiberws.PongMessage, []byte(appData), time.Now().Add(5*time.Second))
	})
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				logger.Infof("[Gotify] stream read error: device_key=%s err=%v", device, err)
				return
			}
		}
	}()

	ping := time.NewTicker(45 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-done:
			return
		case <-ping.C:
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if err := conn.WriteControl(fiberws.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
				logger.Infof("[Gotify] stream ping error: device_key=%s err=%v", device, err)
				return
			}
		case m, ok := <-ch:
			if !ok {
				return
			}
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteJSON(m); err != nil {
				logger.Infof("[Gotify] stream write error: device_key=%s err=%v", device, err)
				return
			}
		}
	}
}
