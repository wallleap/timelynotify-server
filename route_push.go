package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
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

var pushHarmony = func(targetTokens []string, title, body, data, icon string, actionType int, setNum *int, sound string, soundDuration int, foregroundShow int, inboxContent []string, notifyId int) (int, int, error) {
	if harmonyClient == nil {
		return 0, 0, fmt.Errorf("harmony client not initialized")
	}
	return harmonyClient.Send(targetTokens, title, body, data, icon, actionType, setNum, sound, soundDuration, foregroundShow, inboxContent, notifyId)
}

// revokeHarmony is the seam used by pushRevoke() to withdraw a HarmonyOS
// notification. Tests override it to run the pipeline offline.
var revokeHarmony = func(targetTokens []string, notifyId int) (int, int, error) {
	if harmonyClient == nil {
		return 0, 0, fmt.Errorf("harmony client not initialized")
	}
	return harmonyClient.Revoke(targetTokens, notifyId)
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

// toInt coerces a string to int with STRICT integer parsing. Used for
// numeric query params and normalized JSON numbers (see numberToString).
// strconv.Atoi rejects partial/non-integer input ("2.147e+09", "12abc")
// outright — the previous fmt.Sscanf("%d") implementation silently
// consumed the leading digits of a scientific-notation string and
// returned no error (e.g. "2.147483647e+09" parsed as 2).
func toInt(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("parse int %q: %w", s, err)
	}
	return n, nil
}

// numberToString renders a request parameter value as a plain, integer
// string. JSON numbers decode as float64, and fmt.Sprint renders large
// ones in scientific notation (float64(2147483647) →
// "2.147483647e+09"), which then breaks id/notifyId/badge parsing.
// Integral float64 values render via int64 (no exponent); non-integral
// values use fixed-point notation; strings and json.Number pass through.
// All integer ids in this codebase stay well within float64 exactness
// (HMS notifyId max is 2^31-1 ≪ 2^53).
func numberToString(v interface{}) string {
	switch n := v.(type) {
	case nil:
		return ""
	case string:
		return n
	case json.Number:
		return n.String()
	case float64:
		if n == float64(int64(n)) {
			return strconv.FormatInt(int64(n), 10)
		}
		return strconv.FormatFloat(n, 'f', -1, 64)
	case int:
		return strconv.Itoa(n)
	case int64:
		return strconv.FormatInt(n, 10)
	default:
		return fmt.Sprint(v)
	}
}

// isTruthyFlag parses a boolean-ish request parameter across the shapes
// the V1 (query/form strings) and V2 (JSON bool/number) entry points
// produce: bools pass through; numbers are truthy when non-zero; strings
// accept "1"/"true"/"yes"/"on" (case-insensitive, whitespace trimmed); an
// empty string means bare flag presence (?revoke) and is truthy; anything
// else (e.g. "0"/"false"/"no") is false.
func isTruthyFlag(v interface{}) bool {
	switch val := v.(type) {
	case bool:
		return val
	case nil:
		return false
	case string:
		switch strings.ToLower(strings.TrimSpace(val)) {
		case "":
			return true
		case "1", "true", "yes", "on":
			return true
		default:
			return false
		}
	default:
		// JSON numbers (float64/int) arrive here; non-zero is truthy.
		if n, err := toInt(numberToString(v)); err == nil {
			return n != 0
		}
		return true
	}
}

// toInboxStrings coerces a V2 JSON array ([]interface{}), a V1 JSON-encoded
// string (`'["a","b"]'`), or a plain V1 string into the []string form the
// HarmonyOS V3 client expects for notification.inboxContent. Empty arrays and
// empty strings yield nil so the field is omitted entirely.
func toInboxStrings(v interface{}) []string {
	switch val := v.(type) {
	case []string:
		out := make([]string, 0, len(val))
		for _, s := range val {
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	case []interface{}:
		out := make([]string, 0, len(val))
		for _, item := range val {
			s := fmt.Sprint(item)
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		// V1 query/form carries a single string; try JSON array first so
		// `?inboxContent=["a","b"]` works, then fall back to a single-line
		// array for bare strings.
		var arr []string
		if err := jsoniter.Unmarshal([]byte(val), &arr); err == nil {
			out := make([]string, 0, len(arr))
			for _, s := range arr {
				if s != "" {
					out = append(out, s)
				}
			}
			return out
		}
		var raw []interface{}
		if err := jsoniter.Unmarshal([]byte(val), &raw); err == nil {
			out := make([]string, 0, len(raw))
			for _, item := range raw {
				s := fmt.Sprint(item)
				if s != "" {
					out = append(out, s)
				}
			}
			return out
		}
		if val != "" {
			return []string{val}
		}
		return nil
	default:
		s := fmt.Sprint(val)
		if s != "" {
			return []string{s}
		}
		return nil
	}
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
			case "foregroundshow":
				// HarmonyOS V3 foregroundShow: "1" displays notifications while
				// the app is in the foreground; any other value (including
				// "0"/"false"/non-numeric) suppresses foreground display.
				// Store the raw string here; the normalization block below
				// converts it (and JSON numeric variants) to int.
				msg.ExtParams["foregroundShow"] = val
			case "badge":
				if n, err := toInt(val); err == nil {
					msg.Badge = n
					msg.HasBadge = true
				}
			case "platform":
				msg.ExtParams["_platform"] = val
			case "revoke":
				// HarmonyOS-only notification withdrawal. A truthy flag
				// plus `id` (the original notifyId) short-circuits the push
				// pipeline into pushRevoke(); every other push parameter
				// (title/body/sound/...) is ignored. V2 JSON bool/number
				// forms bypass the string case and are normalized below.
				if isTruthyFlag(val) {
					msg.ExtParams["_revoke"] = true
				}
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
		if n, err := toInt(numberToString(v)); err == nil {
			msg.Badge = n
			msg.HasBadge = true
		}
		delete(msg.ExtParams, "badge")
	}
	// id normalization: V2 JSON numeric ids arrive as float64 and bypass the
	// string case above (which sets msg.Id). Convert any type to string so
	// HarmonyOS notifyId parsing (toInt(msg.Id)) and APNs collapse-id both
	// work; also normalize the ExtParams value to string for consistent
	// downstream matching (gotify overwrite uses fmt.Sprint on extras["id"]).
	// numberToString (NOT fmt.Sprint) keeps large integers intact instead
	// of rendering them in scientific notation.
	if v, ok := msg.ExtParams["id"]; ok {
		s := numberToString(v)
		if msg.Id == "" {
			msg.Id = s
		}
		msg.ExtParams["id"] = s
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
			if n, err := toInt(numberToString(v)); err == nil {
				msg.ExtParams["soundduration"] = n
			}
		}
	}
	// Numeric foregroundShow for JSON numeric types (float64/int) bypassed
	// the string switch above, and JSON keys keep their original casing
	// there (e.g. "foregroundShow"). Normalize to int under the canonical
	// key; non-numeric values map to 0 (false). V3 semantics: 1 → true,
	// any other value → false. pushToHarmony defaults to 1 when absent.
	for _, k := range []string{"foregroundShow", "foregroundshow"} {
		if v, ok := msg.ExtParams[k]; ok {
			delete(msg.ExtParams, k)
			if n, err := toInt(numberToString(v)); err == nil {
				msg.ExtParams["foregroundShow"] = n
			} else {
				msg.ExtParams["foregroundShow"] = 0
			}
		}
	}
	// InboxContent normalization: V2 JSON arrays arrive as []interface{}
	// under the original-cased key; V1 string values may be JSON arrays or
	// plain strings under the lowercase key. Convert to []string under the
	// canonical lowercase key; empty arrays are dropped so the V3 client
	// omits both inboxContent and style. HarmonyOS-only field (APNs just
	// ignores it in the custom payload).
	for _, k := range []string{"inboxContent", "inboxcontent"} {
		if v, ok := msg.ExtParams[k]; ok {
			delete(msg.ExtParams, k)
			if s := toInboxStrings(v); len(s) > 0 {
				msg.ExtParams["inboxcontent"] = s
			}
		}
	}
	// Revoke flag normalization: V1 query/form values land under the
	// lowercase "revoke" key (handled in the string switch above); V2 JSON
	// keeps the original casing and may use a bool, number or string.
	// Collapse any casing into the internal "_revoke" marker (truthy only,
	// so a falsey flag never leaves a stray key in the APNs payload) and
	// drop the raw key so it is never forwarded as a custom payload field.
	for k, v := range msg.ExtParams {
		if strings.ToLower(k) != "revoke" {
			continue
		}
		delete(msg.ExtParams, k)
		if isTruthyFlag(v) {
			msg.ExtParams["_revoke"] = true
		}
	}
	if msg.DeviceKey == "" {
		logger.Errorf("[Push] rid=%s device key is empty", rid)
		return 400, fmt.Errorf("device key is empty")
	}

	// HarmonyOS notification withdrawal: a truthy `revoke` flag reroutes
	// the whole request into pushRevoke() (v1 messages:revoke). Only
	// device_key + id (notifyId) are meaningful there; every other push
	// parameter is ignored by design. Revoke is HarmonyOS-only — APNs has
	// no remote notification withdrawal API — and does not publish to the
	// monitoring stream since nothing is delivered.
	if _, revoke := msg.ExtParams["_revoke"]; revoke {
		return pushRevoke(rid, &msg)
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
		soundDuration, _ = toInt(numberToString(v))
	}

	// Bark `foregroundShow` controls the V3 notification.foregroundShow
	// field: 1 → true (display notifications while the app is in the
	// foreground), any other value → false. Default 1 when absent; the
	// push() normalization block stores an int under the canonical key.
	foregroundShow := 1
	if v, ok := msg.ExtParams["foregroundShow"]; ok {
		if n, err := toInt(numberToString(v)); err == nil {
			foregroundShow = n
		} else {
			foregroundShow = 0
		}
	}

	// Bark `inboxContent` is the V3 multi-line body; push() normalizes V2
	// JSON arrays and V1 JSON-encoded strings to []string under the
	// lowercase key. When non-empty the V3 client auto-sets style=3.
	var inboxContent []string
	if v, ok := msg.ExtParams["inboxcontent"]; ok {
		inboxContent = toInboxStrings(v)
	}

	// Bark `id` is the notification folding ID on iOS (apns-collapse-id).
	// On HarmonyOS V3 it maps to notification.notifyId (int); a non-numeric
	// or empty id yields 0 (omitted — Push Kit auto-generates one).
	var notifyId int
	if msg.Id != "" {
		if n, err := toInt(msg.Id); err == nil {
			notifyId = n
		}
	}

	logger.Infof("[Push] rid=%s HarmonyOS push: device_key=%s token=%s actionType=%d hasImage=%v hasData=%v hasSound=%v foregroundShow=%d inboxLines=%d notifyId=%d",
		rid, logging.MaskMiddle(msg.DeviceKey), logging.MaskMiddle(deviceInfo.Token),
		actionType, iconStr != "", dataStr != "", soundStr != "", foregroundShow, len(inboxContent), notifyId)

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
		foregroundShow,
		inboxContent,
		notifyId,
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

// pushRevoke withdraws a previously delivered HarmonyOS notification.
//
// Reached from push() when the request carries a truthy `revoke` flag;
// the Bark `id` param is used as notifyId and must have been sent with
// the original push (an auto-generated id cannot be referenced later).
// Revoke is HarmonyOS-only — APNs provides no remote notification
// withdrawal API — so only harmony records under the device_key are
// targeted (an explicit platform=ios narrows to none → 400). Every
// other push parameter (title/body/sound/...) is ignored, and no
// monitoring-stream entry is published since nothing is delivered.
func pushRevoke(rid string, msg *apns.PushMessage) (int, error) {
	maskedKey := logging.MaskMiddle(msg.DeviceKey)

	devices, err := db.DevicesByKey(msg.DeviceKey)
	if err != nil {
		if _, probe := wellKnownProbeKeys[msg.DeviceKey]; probe {
			logger.Infof("[Push] rid=%s revoke probe path rejected: device_key=%s", rid, maskedKey)
			return 404, fmt.Errorf("device not found")
		}
		logger.Errorf("[Push] rid=%s revoke device not found: device_key=%s err=%v", rid, maskedKey, err)
		return 400, fmt.Errorf("failed to get device info: %v", err)
	}

	// Revoke only targets harmony tokens; APNs has no remote withdrawal.
	// platform=ios explicitly narrows to nothing → 400 below.
	explicitPlatform, _ := msg.ExtParams["_platform"].(string)
	tokens := make([]string, 0, len(devices))
	for _, di := range devices {
		if di.Platform != "harmony" || di.Token == "" {
			continue
		}
		if explicitPlatform != "" && di.Platform != explicitPlatform {
			continue
		}
		tokens = append(tokens, di.Token)
	}
	if len(tokens) == 0 {
		logger.Warnf("[Push] rid=%s revoke no harmony target: device_key=%s explicit=%s", rid, maskedKey, explicitPlatform)
		return 400, fmt.Errorf("no valid harmony device token for key %s", msg.DeviceKey)
	}

	// notifyId is required and must be the positive numeric id carried by
	// the original push (Bark `id` → V3 notification.notifyId).
	notifyId, err := toInt(msg.Id)
	if err != nil || notifyId <= 0 {
		logger.Warnf("[Push] rid=%s revoke requires a numeric positive id: device_key=%s id=%q", rid, maskedKey, msg.Id)
		return 400, fmt.Errorf("revoke requires a numeric positive id")
	}

	initHarmony()
	if harmonyClient == nil {
		logger.Errorf("[Push] rid=%s HarmonyOS client not initialized for revoke: device_key=%s", rid, maskedKey)
		return 500, fmt.Errorf("harmony push client is not initialized")
	}

	logger.Infof("[Push] rid=%s HarmonyOS revoke: device_key=%s notifyId=%d targets=%d",
		rid, maskedKey, notifyId, len(tokens))

	_, hmsCode, err := revokeHarmony(tokens, notifyId)
	if err != nil {
		if hmsCode == 80200001 {
			logger.Warnf("[Push] rid=%s HarmonyOS revoke invalid token, clearing: device_key=%s platform=harmony targets=%d hmsCode=%d",
				rid, maskedKey, len(tokens), hmsCode)
			_ = db.ClearDeviceTokenByKeyAndPlatform(msg.DeviceKey, "harmony")
		}
		logger.Errorf("[Push] rid=%s HarmonyOS revoke failed: device_key=%s notifyId=%d hmsCode=%d err=%v",
			rid, maskedKey, notifyId, hmsCode, err)
		return 500, fmt.Errorf("harmony revoke failed (code %d): %v", hmsCode, err)
	}

	logger.Infof("[Push] rid=%s HarmonyOS revoke success: device_key=%s notifyId=%d hmsCode=%d targets=%d",
		rid, maskedKey, notifyId, hmsCode, len(tokens))
	return 200, nil
}
