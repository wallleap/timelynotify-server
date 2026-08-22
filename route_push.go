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
	contentType := utils.ToLower(utils.UnsafeString(c.Request().Header.ContentType()))
	contentType = utils.ParseVendorSpecificContentType(contentType)

	logger.Infof("[Push] received: method=%s path=%s content-type=%s",
		c.Method(), c.Path(), contentType)

	if strings.HasPrefix(contentType, "application/json") {
		return routeDoPushV2(c)
	}
	return routeDoPushV1(c)
}

func routeDoPushV1(c *fiber.Ctx) error {
	logger.Infof("[Push] V1 request: path=%s", c.Path())

	params := make(map[string]interface{})
	visitor := func(key, value []byte) {
		params[strings.ToLower(string(key))] = string(value)
	}
	c.Request().URI().QueryArgs().VisitAll(visitor)
	c.Request().PostArgs().VisitAll(visitor)
	form, err := c.Request().MultipartForm()
	if err == nil {
		for key, val := range form.Value {
			if len(val) > 0 {
				params[key] = val[0]
			}
		}
	}
	pathParams, err := extractUrlPathParams(c)
	if err != nil {
		logger.Warnf("[Push] V1 path parse failed: %v", err)
		return c.Status(400).JSON(failed(400, "url path parse failed: %v", err))
	}
	for key, val := range pathParams {
		params[key] = val
	}

	deviceKey := ""
	if dk, ok := params["device_key"]; ok {
		deviceKey = fmt.Sprint(dk)
	}
	logger.Infof("[Push] V1 params parsed: device_key=%s", deviceKey)

	code, err := push(params)
	if err != nil {
		logger.Errorf("[Push] V1 failed: device_key=%s code=%d err=%v", deviceKey, code, err)
		return c.Status(code).JSON(failed(code, "%s", err.Error()))
	}
	logger.Infof("[Push] V1 success: device_key=%s code=%d", deviceKey, code)
	return c.JSON(success())
}
func routeDoPushV2(c *fiber.Ctx) error {
	logger.Infof("[Push] V2 request: path=%s", c.Path())

	params := make(map[string]interface{})
	if err := c.BodyParser(&params); err != nil && err != fiber.ErrUnprocessableEntity {
		logger.Warnf("[Push] V2 body parse failed: %v", err)
		return c.Status(400).JSON(failed(400, "request bind failed: %v", err))
	}
	c.Request().URI().QueryArgs().VisitAll(func(key, value []byte) {
		params[strings.ToLower(string(key))] = string(value)
	})
	pathParams, err := extractUrlPathParams(c)
	if err != nil {
		logger.Warnf("[Push] V2 path parse failed: %v", err)
		return c.Status(400).JSON(failed(400, "url path parse failed: %v", err))
	}
	for key, val := range pathParams {
		params[key] = val
	}

	var deviceKeys []string
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
		deviceKey := ""
		if dk, ok := params["device_key"]; ok {
			deviceKey = fmt.Sprint(dk)
		}
		logger.Infof("[Push] V2 single push: device_key=%s", deviceKey)
		code, err := push(params)
		if err != nil {
			logger.Errorf("[Push] V2 single failed: device_key=%s code=%d err=%v", deviceKey, code, err)
			return c.Status(code).JSON(failed(code, "%s", err.Error()))
		}
		logger.Infof("[Push] V2 single success: device_key=%s code=%d", deviceKey, code)
		return c.JSON(success())
	}

	logger.Infof("[Push] V2 batch push: count=%d", count)
	if count > maxBatchPushCount && maxBatchPushCount != -1 {
		logger.Warnf("[Push] V2 batch exceeds limit: count=%d limit=%d", count, maxBatchPushCount)
		return c.Status(400).JSON(failed(400, "batch push count exceeds the maximum limit: %d", maxBatchPushCount))
	}

	var wg sync.WaitGroup
	result := make([]map[string]interface{}, count)
	var mu sync.Mutex

	for i := 0; i < count; i++ {
		newParams := make(map[string]interface{})
		for k, v := range params {
			newParams[k] = v
		}
		newParams["device_key"] = deviceKeys[i]

		wg.Add(1)
		go func(i int, newParams map[string]interface{}) {
			defer wg.Done()

			code, err := push(newParams)

			mu.Lock()
			result[i] = make(map[string]interface{})
			if err != nil {
				result[i]["message"] = err.Error()
				logger.Errorf("[Push] V2 batch item failed: device_key=%s code=%d err=%v",
					deviceKeys[i], code, err)
			} else {
				logger.Infof("[Push] V2 batch item success: device_key=%s code=%d",
					deviceKeys[i], code)
			}
			result[i]["code"] = code
			result[i]["device_key"] = deviceKeys[i]
			mu.Unlock()
		}(i, newParams)
	}
	wg.Wait()
	logger.Infof("[Push] V2 batch completed: total=%d", count)
	return c.JSON(data(result))
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
				if strings.HasSuffix(val, ".caf") {
					msg.Sound = val
				} else {
					msg.Sound = val + ".caf"
				}
			case "platform":
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
		logger.Errorf("[Push] device key is empty")
		return 400, fmt.Errorf("device key is empty")
	}

	if msg.IsEmptyAlert() {
		msg.Body = "Empty Message"
	}

	logger.Infof("[Push] device lookup: device_key=%s", msg.DeviceKey)
	deviceInfo, err := db.DeviceInfoByKey(msg.DeviceKey)
	if err != nil {
		logger.Errorf("[Push] device not found: device_key=%s err=%v", msg.DeviceKey, err)
		return 400, fmt.Errorf("failed to get device info: %v", err)
	}

	platform := deviceInfo.Platform
	if explicitPlatform, ok := msg.ExtParams["_platform"].(string); ok && explicitPlatform != "" {
		platform = explicitPlatform
		logger.Infof("[Push] platform override: device_key=%s explicit=%s stored=%s",
			msg.DeviceKey, explicitPlatform, deviceInfo.Platform)
	} else {
		logger.Infof("[Push] platform resolved: device_key=%s platform=%s", msg.DeviceKey, platform)
	}

	if platform == "harmony" {
		initHarmony()
	}

	msg.DeviceToken = deviceInfo.Token

	gotifyPublish(&msg)

	if platform == "harmony" {
		logger.Infof("[Push] routing to HarmonyOS: device_key=%s", msg.DeviceKey)
		return pushToHarmony(deviceInfo, &msg)
	}

	logger.Infof("[Push] routing to APNs: device_key=%s", msg.DeviceKey)
	return pushToAPNs(deviceInfo, &msg)
}

// pushToAPNs sends notification via APNs
func pushToAPNs(deviceInfo *database.DeviceInfo, msg *apns.PushMessage) (int, error) {
	code, err := pushAPNs(msg)

	if code == 410 || (code == 400 && strings.Contains(err.Error(), "BadDeviceToken")) {
		logger.Warnf("[Push] APNs invalid token, clearing: device_key=%s code=%d", msg.DeviceKey, code)
		_, _ = db.SaveDeviceTokenByKey(msg.DeviceKey, "")
	}
	if err != nil {
		logger.Errorf("[Push] APNs failed: device_key=%s code=%d err=%v", msg.DeviceKey, code, err)
		return 500, fmt.Errorf("push failed: %v", err)
	}
	logger.Infof("[Push] APNs success: device_key=%s code=%d", msg.DeviceKey, code)
	return 200, nil
}

// pushToHarmony sends notification via Huawei Push Kit
func pushToHarmony(deviceInfo *database.DeviceInfo, msg *apns.PushMessage) (int, error) {
	if harmonyClient == nil {
		logger.Errorf("[Push] HarmonyOS client not initialized: device_key=%s", msg.DeviceKey)
		return 500, fmt.Errorf("harmony push client is not initialized")
	}

	clickAction := "launch"
	if level, ok := msg.ExtParams["level"].(string); ok {
		switch strings.ToLower(level) {
		case "critical", "timeSensitive":
			clickAction = "launch"
		case "active":
			clickAction = "banner"
		default:
			clickAction = "page"
		}
	}

	logger.Infof("[Push] HarmonyOS push: device_key=%s click_action=%s", msg.DeviceKey, clickAction)

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
		if hmsCode == 80200001 {
			logger.Warnf("[Push] HarmonyOS invalid token, clearing: device_key=%s hmsCode=%d",
				msg.DeviceKey, hmsCode)
			_, _ = db.SaveDeviceTokenByKey(msg.DeviceKey, "")
		}
		logger.Errorf("[Push] HarmonyOS failed: device_key=%s hmsCode=%d err=%v",
			msg.DeviceKey, hmsCode, err)
		return 500, fmt.Errorf("harmony push failed (code %d): %v", hmsCode, err)
	}

	logger.Infof("[Push] HarmonyOS success: device_key=%s hmsCode=%d", msg.DeviceKey, hmsCode)
	return 200, nil
}
