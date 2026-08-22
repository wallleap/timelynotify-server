package main

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/gofiber/fiber/v2/utils"
	"github.com/mritd/logger"

	"github.com/wallleap/hotify-bark-server/apns"
	"github.com/wallleap/hotify-bark-server/database"
	"github.com/wallleap/hotify-bark-server/harmony"

	"github.com/gofiber/fiber/v2"
)

// Maximum number of batch pushes allowed, -1 means no limit
var maxBatchPushCount = -1

// pushAPNs is the seam used by push() to deliver to APNs. Tests override it to
// run the push pipeline offline; production keeps the real APNs client.
var pushAPNs = func(msg *apns.PushMessage) (int, error) { return apns.Push(msg) }

// harmonyClient is initialized at startup if HarmonyOS credentials are valid.
var harmonyClient *harmony.Client
var harmonyInitOnce sync.Once

func initHarmony() {
	harmonyInitOnce.Do(func() {
		ts, err := harmony.NewTokenSource()
		if err != nil {
			logger.Warnf("HarmonyOS push client not initialized: %v", err)
			return
		}

		// Check for mock URL in environment
		mockURL := os.Getenv("BARK_SERVER_HARMONY_MOCK_URL")
		if mockURL != "" {
			logger.Infof("HarmonyOS push client using mock URL: %s", mockURL)
			harmonyClient = harmony.NewClientWithURL(ts, mockURL)
		} else {
			harmonyClient = harmony.NewClient(ts)
		}

		logger.Info("HarmonyOS push client initialized")
	})
}

var pushHarmony = func(targetTokens []string, title, body, data, clickAction string) (int, int, error) {
	if harmonyClient == nil {
		return 0, 0, fmt.Errorf("harmony client not initialized")
	}
	return harmonyClient.Send(targetTokens, title, body, data, clickAction)
}

func init() {
	// V2 API
	registerRouteWithWeight("push", 50, func(router fiber.Router) {
		router.Post("/push", rateLimitPushMiddleware, func(c *fiber.Ctx) error { return routeDoPush(c) })
	})

	// compatible with old requests
	registerRouteWithWeight("push_compat", 1, func(router fiber.Router) {
		router.Get("/:device_key", rateLimitPushMiddleware, func(c *fiber.Ctx) error { return routeDoPush(c) })
		router.Post("/:device_key", rateLimitPushMiddleware, func(c *fiber.Ctx) error { return routeDoPush(c) })

		router.Get("/:device_key/:body", rateLimitPushMiddleware, func(c *fiber.Ctx) error { return routeDoPush(c) })
		router.Post("/:device_key/:body", rateLimitPushMiddleware, func(c *fiber.Ctx) error { return routeDoPush(c) })

		router.Get("/:device_key/:title/:body", rateLimitPushMiddleware, func(c *fiber.Ctx) error { return routeDoPush(c) })
		router.Post("/:device_key/:title/:body", rateLimitPushMiddleware, func(c *fiber.Ctx) error { return routeDoPush(c) })

		router.Get("/:device_key/:title/:subtitle/:body", rateLimitPushMiddleware, func(c *fiber.Ctx) error { return routeDoPush(c) })
		router.Post("/:device_key/:title/:subtitle/:body", rateLimitPushMiddleware, func(c *fiber.Ctx) error { return routeDoPush(c) })
	})
}

// Set the maximum number of batch pushes allowed
func SetMaxBatchPushCount(count int) {
	maxBatchPushCount = count
}
func routeDoPush(c *fiber.Ctx) error {
	// Get content-type
	contentType := utils.ToLower(utils.UnsafeString(c.Request().Header.ContentType()))
	contentType = utils.ParseVendorSpecificContentType(contentType)
	// Json request uses the API V2
	if strings.HasPrefix(contentType, "application/json") {
		return routeDoPushV2(c)
	} else {
		return routeDoPushV1(c)
	}
}

func routeDoPushV1(c *fiber.Ctx) error {

	params := make(map[string]interface{})
	visitor := func(key, value []byte) {
		params[strings.ToLower(string(key))] = string(value)
	}
	// parse query args (medium priority)
	c.Request().URI().QueryArgs().VisitAll(visitor)
	// parse post args
	c.Request().PostArgs().VisitAll(visitor)
	// parse multipartForm values
	form, err := c.Request().MultipartForm()
	if err == nil {
		for key, val := range form.Value {
			if len(val) > 0 {
				params[key] = val[0]
			}
		}
	}
	// parse url path (highest priority)
	pathParams, err := extractUrlPathParams(c)
	if err != nil {
		return c.Status(400).JSON(failed(400, "url path parse failed: %v", err))
	}
	for key, val := range pathParams {
		params[key] = val
	}

	code, err := push(params)
	if err != nil {
		return c.Status(code).JSON(failed(code, "%s", err.Error()))
	} else {
		return c.JSON(success())
	}
}
func routeDoPushV2(c *fiber.Ctx) error {
	params := make(map[string]interface{})
	// parse body
	if err := c.BodyParser(&params); err != nil && err != fiber.ErrUnprocessableEntity {
		return c.Status(400).JSON(failed(400, "request bind failed: %v", err))
	}
	// parse query args (medium priority)
	c.Request().URI().QueryArgs().VisitAll(func(key, value []byte) {
		params[strings.ToLower(string(key))] = string(value)
	})
	// parse url path (highest priority)
	pathParams, err := extractUrlPathParams(c)
	if err != nil {
		return c.Status(400).JSON(failed(400, "url path parse failed: %v", err))
	}
	for key, val := range pathParams {
		params[key] = val
	}

	var deviceKeys []string
	// Get the device_keys array from params
	if keys, ok := params["device_keys"]; ok {
		switch keys := keys.(type) {
		case string:
			deviceKeys = strings.Split(keys, ",")
		case []interface{}:
			for _, key := range keys {
				deviceKeys = append(deviceKeys, fmt.Sprint(key))
			}
		default:
			return c.Status(400).JSON(failed(400, "invalid type for device_keys"))
		}
		delete(params, "device_keys")
	}

	count := len(deviceKeys)

	if count == 0 {
		// Single push
		code, err := push(params)
		if err != nil {
			return c.Status(code).JSON(failed(code, "%s", err.Error()))
		} else {
			return c.JSON(success())
		}
	} else {
		// Batch push
		if count > maxBatchPushCount && maxBatchPushCount != -1 {
			return c.Status(400).JSON(failed(400, "batch push count exceeds the maximum limit: %d", maxBatchPushCount))
		}

		var wg sync.WaitGroup
		result := make([]map[string]interface{}, count)
		var mu sync.Mutex

		for i := 0; i < count; i++ {
			// Copy params
			newParams := make(map[string]interface{})
			for k, v := range params {
				newParams[k] = v
			}
			newParams["device_key"] = deviceKeys[i]

			wg.Add(1)
			go func(i int, newParams map[string]interface{}) {
				defer wg.Done()

				// Push
				code, err := push(newParams)

				// Save result
				mu.Lock()
				result[i] = make(map[string]interface{})
				if err != nil {
					result[i]["message"] = err.Error()
				}
				result[i]["code"] = code
				result[i]["device_key"] = deviceKeys[i]
				mu.Unlock()
			}(i, newParams)
		}
		wg.Wait()
		return c.JSON(data(result))
	}
}

func extractUrlPathParams(c *fiber.Ctx) (map[string]interface{}, error) {
	// parse url path (highest priority)
	params := make(map[string]interface{})
	if pathDeviceKey := c.Params("device_key"); pathDeviceKey != "" {
		params["device_key"] = pathDeviceKey
	}
	if subtitle := c.Params("subtitle"); subtitle != "" {
		str, err := url.QueryUnescape(subtitle)
		if err != nil {
			return nil, err
		}
		params["subtitle"] = str
	}
	if title := c.Params("title"); title != "" {
		str, err := url.QueryUnescape(title)
		if err != nil {
			return nil, err
		}
		params["title"] = str
	}
	if body := c.Params("body"); body != "" {
		str, err := url.QueryUnescape(body)
		if err != nil {
			return nil, err
		}
		params["body"] = str
	}
	return params, nil
}

func push(params map[string]interface{}) (int, error) {
	// default value
	msg := apns.PushMessage{
		Body:      "",
		Sound:     "1107",
		ExtParams: make(map[string]interface{}),
	}

	for key, val := range params {
		switch val := val.(type) {
		case string:
			switch strings.ToLower(string(key)) {
			case "id":
				msg.Id = val
				msg.ExtParams["id"] = val
			case "device_key":
				msg.DeviceKey = val
			case "subtitle":
				msg.Subtitle = val
			case "title":
				msg.Title = val
			case "body":
				msg.Body = val
			case "sound":
				// Compatible with old parameters
				if strings.HasSuffix(val, ".caf") {
					msg.Sound = val
				} else {
					msg.Sound = val + ".caf"
				}
			case "platform":
				// Allow specifying platform in request (e.g. "harmony" or "ios")
				msg.ExtParams["_platform"] = val
			default:
				msg.ExtParams[strings.ToLower(string(key))] = val
			}
		case map[string]interface{}:
			for k, v := range val {
				msg.ExtParams[k] = v
			}
		default:
			msg.ExtParams[key] = val
		}
	}

	if msg.DeviceKey == "" {
		return 400, fmt.Errorf("device key is empty")
	}

	if msg.IsEmptyAlert() {
		// For encrypted push notifications, a Body is required; otherwise, APNs will discard the notification
		msg.Body = "Empty Message"
	}

	// Get device info (includes platform)
	deviceInfo, err := db.DeviceInfoByKey(msg.DeviceKey)
	if err != nil {
		return 400, fmt.Errorf("failed to get device info: %v", err)
	}

	// Determine platform: use explicitly provided one, or fall back to stored one
	platform := deviceInfo.Platform
	if explicitPlatform, ok := msg.ExtParams["_platform"].(string); ok && explicitPlatform != "" {
		platform = explicitPlatform
	}

	// Ensure harmony client is initialized if needed
	if platform == "harmony" {
		initHarmony()
	}

	msg.DeviceToken = deviceInfo.Token

	// Mirror the push into the gotify-compatible monitoring stream (hotify-bridge).
	// Published once the device token is resolved, independent of iOS delivery.
	gotifyPublish(&msg)

	// Route to the appropriate push channel based on platform
	if platform == "harmony" {
		return pushToHarmony(deviceInfo, &msg)
	}
	
	// Default: iOS (APNs)
	return pushToAPNs(deviceInfo, &msg)
}

// pushToAPNs sends notification via APNs
func pushToAPNs(deviceInfo *database.DeviceInfo, msg *apns.PushMessage) (int, error) {
	code, err := pushAPNs(msg)

	// Invalid token, delete it from database.
	if code == 410 || (code == 400 && strings.Contains(err.Error(), "BadDeviceToken")) {
		_, _ = db.SaveDeviceTokenByKey(msg.DeviceKey, "")
	}
	if err != nil {
		return 500, fmt.Errorf("push failed: %v", err)
	}
	return 200, nil
}

// pushToHarmony sends notification via Huawei Push Kit
func pushToHarmony(deviceInfo *database.DeviceInfo, msg *apns.PushMessage) (int, error) {
	if harmonyClient == nil {
		return 500, fmt.Errorf("harmony push client is not initialized")
	}

	// Extract click_action level if present
	clickAction := "launch"
	if level, ok := msg.ExtParams["level"].(string); ok {
		// Map bark levels to Huawei click actions
		switch strings.ToLower(level) {
		case "critical", "timeSensitive":
			clickAction = "launch" // Full-screen notification
		case "active":
			clickAction = "banner" // Banner notification
		default:
			clickAction = "page" // Normal notification
		}
	}

	// Extract data payload
	var dataStr string
	if customData, ok := msg.ExtParams["data"].(string); ok {
		dataStr = customData
	}

	_, hmsCode, err := pushHarmony(
		[]string{deviceInfo.Token},
		msg.Title,
		msg.Body,
		dataStr,
		clickAction,
	)

	if err != nil {
		// Handle token expiry - clear the token if it's invalid
		if hmsCode == 80200001 { // invalid token
			_, _ = db.SaveDeviceTokenByKey(msg.DeviceKey, "")
		}
		return 500, fmt.Errorf("harmony push failed (code %d): %v", hmsCode, err)
	}

	return 200, nil
}
