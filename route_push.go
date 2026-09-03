package main

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/gofiber/fiber/v2/utils"
	jsoniter "github.com/json-iterator/go"
	"github.com/mritd/logger"

	"github.com/wallleap/timelynotify-server/apns"
	"github.com/wallleap/timelynotify-server/database"
	"github.com/wallleap/timelynotify-server/harmony"
	"github.com/wallleap/timelynotify-server/internal/logging"

	"github.com/gofiber/fiber/v2"
)

const DEFAULT_TITLE = "订阅通知"

// Maximum number of batch pushes allowed, -1 means no limit
var maxBatchPushCount = -1

// wellKnownProbeKeys are path segments commonly probed by internet scanners
// (GET /favicon.ico, /robots.txt, /sse, /api/*, ...). They fall through to
// the /:device_key push catch-all and would otherwise log a noisy ERROR per
// probe. An unregistered probe key gets a quiet 404 instead; a key that IS
// registered (custom keys are honored at registration) still pushes normally.
var wellKnownProbeKeys = map[string]struct{}{
	".env":        {},
	".git":        {},
	"api":         {},
	"events":      {},
	"favicon.ico": {},
	"robots.txt":  {},
	"sitemap.xml": {},
	"sse":         {},
	"stream":      {},
}

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

var pushHarmony = func(targetTokens []string, title, body, data, icon string, actionType int, setNum *int, sound string, soundDuration int) (int, int, error) {
	if harmonyClient == nil {
		return 0, 0, fmt.Errorf("harmony client not initialized")
	}
	return harmonyClient.Send(targetTokens, title, body, data, icon, actionType, setNum, sound, soundDuration)
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

// logPushFailure logs a push failure. 404 (device not found, e.g. scanner
// probes) is a client-side condition logged at INFO; everything else is a
// server-side fault logged at ERROR.
// rid is the request trace id (may be empty when invoked outside an HTTP
// request, e.g. in tests). deviceKey is middle-masked before writing.
func logPushFailure(rid, stage, deviceKey string, code int, err error) {
	maskedKey := logging.MaskMiddle(deviceKey)
	if code == 404 {
		logger.Infof("[Push] rid=%s %s not found: device_key=%s code=%d err=%v", rid, stage, maskedKey, code, err)
		return
	}
	logger.Errorf("[Push] rid=%s %s failed: device_key=%s code=%d err=%v", rid, stage, maskedKey, code, err)
}

func routeDoPush(c *fiber.Ctx) error {
	rid := ridFrom(c)
	contentType := utils.ToLower(utils.UnsafeString(c.Request().Header.ContentType()))
	contentType = utils.ParseVendorSpecificContentType(contentType)

	logger.Infof("[Push] rid=%s received: method=%s path=%s content-type=%s",
		rid, c.Method(), c.Path(), contentType)

	if strings.HasPrefix(contentType, "application/json") {
		return routeDoPushV2(c, rid)
	}
	return routeDoPushV1(c, rid)
}

func routeDoPushV1(c *fiber.Ctx, rid string) error {
	logger.Infof("[Push] rid=%s V1 request: path=%s", rid, c.Path())

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
		logger.Warnf("[Push] rid=%s V1 path parse failed: %v", rid, err)
		return c.Status(400).JSON(failed(400, "url path parse failed: %v", err))
	}
	for key, val := range pathParams {
		params[key] = val
	}

	deviceKey := ""
	if dk, ok := params["device_key"]; ok {
		deviceKey = fmt.Sprint(dk)
	}
	// Log params with sensitive/content fields masked so operators can see
	// what was submitted without leaking tokens or message body.
	if bodyBytes, mErr := jsoniter.Marshal(logging.MaskSensitiveFields(params)); mErr == nil {
		logger.Infof("[Push] rid=%s V1 params: %s", rid, string(bodyBytes))
	}

	code, err := push(rid, params)
	if err != nil {
		logPushFailure(rid, "V1", deviceKey, code, err)
		return c.Status(code).JSON(failed(code, "%s", err.Error()))
	}
	logger.Infof("[Push] rid=%s V1 success: device_key=%s code=%d", rid, logging.MaskMiddle(deviceKey), code)
	return c.JSON(success())
}

func routeDoPushV2(c *fiber.Ctx, rid string) error {
	logger.Infof("[Push] rid=%s V2 request: path=%s", rid, c.Path())

	params := make(map[string]interface{})
	if err := c.BodyParser(&params); err != nil && err != fiber.ErrUnprocessableEntity {
		logger.Warnf("[Push] rid=%s V2 body parse failed: %v", rid, err)
		return c.Status(400).JSON(failed(400, "request bind failed: %v", err))
	}
	c.Request().URI().QueryArgs().VisitAll(func(key, value []byte) {
		params[strings.ToLower(string(key))] = string(value)
	})
	pathParams, err := extractUrlPathParams(c)
	if err != nil {
		logger.Warnf("[Push] rid=%s V2 path parse failed: %v", rid, err)
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
		if bodyBytes, mErr := jsoniter.Marshal(logging.MaskSensitiveFields(params)); mErr == nil {
			logger.Infof("[Push] rid=%s V2 single params: %s", rid, string(bodyBytes))
		}
		code, err := push(rid, params)
		if err != nil {
			logPushFailure(rid, "V2", deviceKey, code, err)
			return c.Status(code).JSON(failed(code, "%s", err.Error()))
		}
		logger.Infof("[Push] rid=%s V2 single success: device_key=%s code=%d", rid, logging.MaskMiddle(deviceKey), code)
		return c.JSON(success())
	}

	logger.Infof("[Push] rid=%s V2 batch push: count=%d", rid, count)
	if count > maxBatchPushCount && maxBatchPushCount != -1 {
		logger.Warnf("[Push] rid=%s V2 batch exceeds limit: count=%d limit=%d", rid, count, maxBatchPushCount)
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

			// Captures rid and deviceKeys[i] for consistent logging even
			// though newParams["device_key"] already holds the key — we
			// still mask it the same way.
			code, err := push(rid, newParams)

			mu.Lock()
			result[i] = make(map[string]interface{})
			if err != nil {
				result[i]["message"] = err.Error()
				logPushFailure(rid, "V2 batch item", deviceKeys[i], code, err)
			} else {
				logger.Infof("[Push] rid=%s V2 batch item success: device_key=%s code=%d",
					rid, logging.MaskMiddle(deviceKeys[i]), code)
			}
			result[i]["code"] = code
			result[i]["device_key"] = deviceKeys[i]
			mu.Unlock()
		}(i, newParams)
	}
	wg.Wait()
	logger.Infof("[Push] rid=%s V2 batch completed: total=%d", rid, count)
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

// toInt coerces a string to int; used for numeric query params and
// JSON numbers (fmt.Sprint'd to string first) that represent count values
// like badge count.
func toInt(s string) (int, error) {
	var n int
	_, err := fmt.Sscanf(s, "%d", &n)
	return n, err
}

// push dispatches a single logical push request to one or more devices.
// rid is the request trace id threaded through every business log so a
// multi-platform fan-out can be correlated back to one HTTP request.
func push(rid string, params map[string]interface{}) (int, error) {
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
				// Keep the raw name for HarmonyOS: the V3 client appends
				// ".mp3" for the app /resources/rawfile lookup, while APNs
				// uses the .caf-suffixed msg.Sound below.
				msg.ExtParams["sound"] = val
				if strings.HasSuffix(val, ".caf") {
					msg.Sound = val
				} else {
					msg.Sound = val + ".caf"
				}
			case "soundduration":
				if n, err := toInt(val); err == nil {
					msg.ExtParams["soundduration"] = n
				}
			case "badge":
				if n, err := toInt(val); err == nil {
					msg.Badge = n
					msg.HasBadge = true
				}
			case "platform":
				msg.ExtParams["_platform"] = val
			case "icon":
				// Users often copy example URLs wrapped in backticks/whitespace
				// from docs; APNs image loading and Huawei image download both
				// fail silently on such URLs, so trim once here for both.
				msg.ExtParams["icon"] = strings.TrimSpace(strings.Trim(val, "`"))
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

	// Numeric badge handling for JSON numeric types (float64/int) that bypassed
	// the string case above; clean up the ExtParams side-effect too.  Any int
	// value is accepted (including zero and negatives), HasBadge distinguishes
	// "not set" from an explicit zero.
	if v, ok := msg.ExtParams["badge"]; ok && !msg.HasBadge {
		if n, err := toInt(fmt.Sprint(v)); err == nil {
			msg.Badge = n
			msg.HasBadge = true
		}
		delete(msg.ExtParams, "badge")
	}
	// Numeric soundDuration for JSON numeric types (float64/int) bypassed
	// the string switch above, and JSON keys keep their original casing
	// there (e.g. "soundDuration"). Normalize to int under the lowercase
	// key; unparseable values are dropped. The HarmonyOS V3 client later
	// clamps to [1, 60]. Query (lowercase, parsed as string) wins over
	// body on conflict, matching the Bark precedence rules.
	for _, k := range []string{"soundDuration", "soundduration"} {
		if v, ok := msg.ExtParams[k]; ok {
			delete(msg.ExtParams, k)
			if n, err := toInt(fmt.Sprint(v)); err == nil {
				msg.ExtParams["soundduration"] = n
			}
		}
	}
	if msg.DeviceKey == "" {
		logger.Errorf("[Push] rid=%s device key is empty", rid)
		return 400, fmt.Errorf("device key is empty")
	}

	if msg.IsEmptyAlert() {
		msg.Body = "Empty Message"
	}

	maskedKey := logging.MaskMiddle(msg.DeviceKey)
	logger.Infof("[Push] rid=%s device lookup: device_key=%s", rid, maskedKey)
	devices, err := db.DevicesByKey(msg.DeviceKey)
	if err != nil {
		// Scanner probes land here with well-known names; fail quietly with
		// 404 instead of the noisy 400 client-error path.
		if _, probe := wellKnownProbeKeys[msg.DeviceKey]; probe {
			logger.Infof("[Push] rid=%s probe path rejected: device_key=%s", rid, maskedKey)
			return 404, fmt.Errorf("device not found")
		}
		logger.Errorf("[Push] rid=%s device not found: device_key=%s err=%v", rid, maskedKey, err)
		return 400, fmt.Errorf("failed to get device info: %v", err)
	}

	// Explicit `platform` param narrows delivery to one platform; without it
	// we fan out to every (key, platform) record so a single device_key can
	// reach both iOS and HarmonyOS devices at once.
	explicitPlatform, _ := msg.ExtParams["_platform"].(string)
	targets := make([]*database.DeviceInfo, 0, len(devices))
	for _, di := range devices {
		if di.Token == "" {
			// Skip records whose token was cleared (e.g. after a prior
			// BadDeviceToken). The record stays so the key remains known,
			// but we don't push to a dead token.
			continue
		}
		if explicitPlatform != "" && di.Platform != explicitPlatform {
			continue
		}
		targets = append(targets, di)
	}
	if len(targets) == 0 {
		logger.Warnf("[Push] rid=%s no valid target: device_key=%s explicit=%s", rid, maskedKey, explicitPlatform)
		return 400, fmt.Errorf("no valid device token for key %s", msg.DeviceKey)
	}
	if explicitPlatform != "" {
		logger.Infof("[Push] rid=%s platform override: device_key=%s explicit=%s targets=%d",
			rid, maskedKey, explicitPlatform, len(targets))
	} else {
		logger.Infof("[Push] rid=%s platform resolved: device_key=%s targets=%d", rid, maskedKey, len(targets))
	}

	// Record the push to the monitoring stream once per logical request,
	// independent of how many platforms actually receive it.
	gotifyPublish(&msg)

	if len(targets) == 1 {
		return pushToDevice(rid, targets[0], &msg)
	}

	logger.Infof("[Push] rid=%s multi-platform fan-out: device_key=%s targets=%d", rid, maskedKey, len(targets))

	// Fan out concurrently. Succeed if any one delivery succeeds; surface
	// the last failure code only when every target failed.
	var wg sync.WaitGroup
	var mu sync.Mutex
	successCount := 0
	var lastCode int
	var lastErr error
	for _, di := range targets {
		wg.Add(1)
		go func(di *database.DeviceInfo) {
			defer wg.Done()
			code, err := pushToDevice(rid, di, &msg)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				lastCode = code
				lastErr = err
			} else {
				successCount++
			}
		}(di)
	}
	wg.Wait()
	if successCount > 0 {
		return 200, nil
	}
	if lastErr != nil {
		return lastCode, lastErr
	}
	return 500, fmt.Errorf("all pushes failed for key %s", msg.DeviceKey)
}

// pushToDevice dispatches a single push to the platform-specific channel.
// The PushMessage is shallow-copied per call so concurrent fan-out writes
// distinct DeviceToken fields without racing on the shared struct.
// rid is threaded through to the platform-specific push function for log
// correlation.
func pushToDevice(rid string, deviceInfo *database.DeviceInfo, msg *apns.PushMessage) (int, error) {
	m := *msg
	m.DeviceToken = deviceInfo.Token
	if deviceInfo.Platform == "harmony" {
		initHarmony()
		logger.Infof("[Push] rid=%s routing to HarmonyOS: device_key=%s", rid, logging.MaskMiddle(m.DeviceKey))
		return pushToHarmony(rid, deviceInfo, &m)
	}
	logger.Infof("[Push] rid=%s routing to APNs: device_key=%s", rid, logging.MaskMiddle(m.DeviceKey))
	return pushToAPNs(rid, deviceInfo, &m)
}

// pushToAPNs sends notification via APNs
func pushToAPNs(rid string, deviceInfo *database.DeviceInfo, msg *apns.PushMessage) (int, error) {
	code, err := pushAPNs(msg)

	if code == 410 || (code == 400 && strings.Contains(err.Error(), "BadDeviceToken")) {
		logger.Warnf("[Push] rid=%s APNs invalid token, clearing: device_key=%s platform=%s token=%s code=%d",
			rid, logging.MaskMiddle(msg.DeviceKey), deviceInfo.Platform, logging.MaskMiddle(msg.DeviceToken), code)
		_ = db.ClearDeviceTokenByKeyAndPlatform(msg.DeviceKey, deviceInfo.Platform)
	}
	if err != nil {
		logger.Errorf("[Push] rid=%s APNs failed: device_key=%s token=%s code=%d err=%v",
			rid, logging.MaskMiddle(msg.DeviceKey), logging.MaskMiddle(msg.DeviceToken), code, err)
		return 500, fmt.Errorf("push failed: %v", err)
	}
	logger.Infof("[Push] rid=%s APNs success: device_key=%s token=%s code=%d",
		rid, logging.MaskMiddle(msg.DeviceKey), logging.MaskMiddle(msg.DeviceToken), code)
	return 200, nil
}

// pushToHarmony sends notification via Huawei Push Kit (V3 scenario API).
//
// V3 clickAction only has actionType 0 (open app home) or 1 (open inner
// page); the legacy V1 launch/banner/page display mapping does not apply.
// The Bark `level` field is an APNs concept with no direct V3 equivalent,
// so we default to actionType 0 (open app home on click).
func pushToHarmony(rid string, deviceInfo *database.DeviceInfo, msg *apns.PushMessage) (int, error) {
	if harmonyClient == nil {
		logger.Errorf("[Push] rid=%s HarmonyOS client not initialized: device_key=%s",
			rid, logging.MaskMiddle(msg.DeviceKey))
		return 500, fmt.Errorf("harmony push client is not initialized")
	}

	actionType := 0
	// Huawei V3 requires the notification to carry both title and body;
	// empty-title messages are accepted (hmsCode=0) but silently not
	// displayed on the device. Bark pushes often carry body only (V1 path
	// style), so fall back to the body text as the title.
	title := msg.Title
	if title == "" {
		title = DEFAULT_TITLE
	}

	var dataStr string
	if customData, ok := msg.ExtParams["data"].(string); ok {
		dataStr = customData
	}

	// Bark `icon` stays in ExtParams for the APNs custom payload; Harmony
	// maps it to notification.image (large icon URL, HTTPS required).
	var iconStr string
	if icon, ok := msg.ExtParams["icon"].(string); ok {
		iconStr = icon
	}

	// Bark `sound` is the raw ringtone name (shared across platforms);
	// the V3 client appends ".mp3" for the HarmonyOS /resources/rawfile
	// lookup. soundDuration (seconds) only takes effect with a sound;
	// the client clamps it to [1, 60].
	var soundStr string
	if sound, ok := msg.ExtParams["sound"].(string); ok {
		soundStr = sound
	}
	var soundDuration int
	if v, ok := msg.ExtParams["soundduration"]; ok {
		soundDuration, _ = toInt(fmt.Sprint(v))
	}

	logger.Infof("[Push] rid=%s HarmonyOS push: device_key=%s token=%s actionType=%d hasImage=%v hasData=%v hasSound=%v",
		rid, logging.MaskMiddle(msg.DeviceKey), logging.MaskMiddle(deviceInfo.Token),
		actionType, iconStr != "", dataStr != "", soundStr != "")

	var badgeArg *int
	if msg.HasBadge {
		v := msg.Badge
		badgeArg = &v
	}
	_, hmsCode, err := pushHarmony(
		[]string{deviceInfo.Token},
		title,
		msg.Body,
		dataStr,
		iconStr,
		actionType,
		badgeArg,
		soundStr,
		soundDuration,
	)

	if err != nil {
		if hmsCode == 80200001 {
			logger.Warnf("[Push] rid=%s HarmonyOS invalid token, clearing: device_key=%s platform=%s token=%s hmsCode=%d",
				rid, logging.MaskMiddle(msg.DeviceKey), deviceInfo.Platform, logging.MaskMiddle(deviceInfo.Token), hmsCode)
			_ = db.ClearDeviceTokenByKeyAndPlatform(msg.DeviceKey, deviceInfo.Platform)
		}
		logger.Errorf("[Push] rid=%s HarmonyOS failed: device_key=%s token=%s hmsCode=%d err=%v",
			rid, logging.MaskMiddle(msg.DeviceKey), logging.MaskMiddle(deviceInfo.Token), hmsCode, err)
		return 500, fmt.Errorf("harmony push failed (code %d): %v", hmsCode, err)
	}

	logger.Infof("[Push] rid=%s HarmonyOS success: device_key=%s token=%s hmsCode=%d",
		rid, logging.MaskMiddle(msg.DeviceKey), logging.MaskMiddle(deviceInfo.Token), hmsCode)
	return 200, nil
}
