package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"io"

	"github.com/gofiber/fiber/v2"
	jsoniter "github.com/json-iterator/go"
	"github.com/wallleap/timelynotify-server/apns"
	"github.com/wallleap/timelynotify-server/database"
)

// Before running the tests, a valid deviceToken must be set. Otherwise, the tests will fail.
const (
	// A syntactically valid device token (64 hex chars). Delivery to APNs is
	// stubbed in TestMain via pushAPNs, so this never needs to be a real token.
	deviceToken = "580e322f0470c3eca5df5246d1c251e00ac3ad776664c80d44535f9a73bb0b40"
	key         = "MemoryBaseKey"
)

var app *fiber.App

// recordedPush captures the payload a push request handed to the (stubbed)
// APNs layer, plus the response it is expected to produce.
type recordedPush struct {
	msg *apns.PushMessage
	mu  sync.Mutex
	// pushFn returns the (code, err) a real APNs call would have returned.
	pushFn func(*apns.PushMessage) (int, error)
}

func (r *recordedPush) push(msg *apns.PushMessage) (int, error) {
	if r.pushFn != nil {
		return r.pushFn(msg)
	}
	r.mu.Lock()
	r.msg = msg
	r.mu.Unlock()
	return 200, nil
}

var stub *recordedPush

func TestMain(m *testing.M) {
	if deviceToken == "" {
		panic("deviceToken is not set")
	}
	db = database.NewMemBase()
	db.SaveDeviceTokenByKey(key, deviceToken)
	stub = &recordedPush{}
	pushAPNs = stub.push
	app = NewServer()
	m.Run()
}

// lastPush returns the most recent message passed to the stubbed APNs layer.
func lastPush() *apns.PushMessage {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	return stub.msg
}

func TestRegister(t *testing.T) {
	Endpoint(t, []APITestCase{
		{
			Name:           "Normal registration",
			Method:         "GET",
			URL:            "/register?devicetoken=" + deviceToken,
			Body:           "",
			IsJson:         false,
			WantStatusCode: 200,
		},
		{
			Name:           "Registration with key",
			Method:         "GET",
			URL:            "/register?key=" + key + "&devicetoken=" + deviceToken,
			Body:           "",
			IsJson:         false,
			WantStatusCode: 200,
		},
		{
			Name:           "Registration with wrong key",
			Method:         "GET",
			URL:            "/register?key=" + "wrongKey" + "&devicetoken=" + deviceToken,
			Body:           "",
			IsJson:         false,
			WantStatusCode: 500,
		},
		{
			Name:           "Registration without devicetoken",
			Method:         "GET",
			URL:            "/register?key=" + key,
			Body:           "",
			IsJson:         false,
			WantStatusCode: 400,
		},
	})
}

func TestPushTitleAndBody(t *testing.T) {
	// Correct push
	Endpoint(t, []APITestCase{
		{
			Name:           "GET push body",
			Method:         "GET",
			URL:            "/" + key + "/body",
			Body:           "",
			IsJson:         false,
			WantStatusCode: 200,
		},
		{
			Name:           "GET push title body",
			Method:         "GET",
			URL:            "/" + key + "/title/body",
			Body:           "",
			IsJson:         false,
			WantStatusCode: 200,
		},
		{
			Name:           "GET push title subtitle body",
			Method:         "GET",
			URL:            "/" + key + "/title/subtitle/body",
			Body:           "",
			IsJson:         false,
			WantStatusCode: 200,
		},
		{
			Name:           "POST push body",
			Method:         "POST",
			URL:            "/" + key,
			Body:           "body=body",
			IsJson:         false,
			WantStatusCode: 200,
		},
		{
			Name:           "POST push title body",
			Method:         "POST",
			URL:            "/" + key,
			Body:           "title=title&body=body",
			IsJson:         false,
			WantStatusCode: 200,
		},
		{
			Name:           "POST push title subtitle body",
			Method:         "POST",
			URL:            "/" + key,
			Body:           "title=title&subtitle=subtitle&body=body",
			IsJson:         false,
			WantStatusCode: 200,
		},
		{
			Name:           "GET title subtitle body URL parameters",
			Method:         "GET",
			URL:            "/" + key + "?title=title&subtitle=subtitle&body=body",
			Body:           "",
			IsJson:         false,
			WantStatusCode: 200,
		},
		{
			Name:           "POST title subtitle body POST parameters",
			Method:         "GET",
			URL:            "/" + key,
			Body:           "title=title&subtitle=subtitle&body=body",
			IsJson:         false,
			WantStatusCode: 200,
		},
		{
			Name:           "POST title subtitle body JSON parameters",
			Method:         "POST",
			URL:            "/" + key,
			Body:           "{\"title\":\"title\",\"subtitle\":\"subtitle\",\"body\":\"body\"}",
			IsJson:         true,
			WantStatusCode: 200,
		},
		{
			Name:           "POST V2 title subtitle body",
			Method:         "POST",
			URL:            "/push",
			Body:           "device_key=" + key + "&title=title&subtitle=subtitle&body=body",
			IsJson:         false,
			WantStatusCode: 200,
		},
		{
			Name:           "POST title subtitle body JSON parameters V2",
			Method:         "POST",
			URL:            "/push",
			Body:           "{\"title\":\"title\",\"subtitle\":\"subtitle\",\"body\":\"body\",\"device_key\":\"" + key + "\"}",
			IsJson:         true,
			WantStatusCode: 200,
		},
	})

	// Incorrect push
	Endpoint(t, []APITestCase{
		{
			Name:           "GET push without key",
			Method:         "GET",
			URL:            "/body",
			Body:           "",
			IsJson:         false,
			WantStatusCode: 400,
		},
		{
			Name:           "POST push without key",
			Method:         "POST",
			URL:            "/push",
			Body:           "title=title&subtitle=subtitle&body=body",
			IsJson:         false,
			WantStatusCode: 400,
		},
		{
			Name:           "POST JSON push without key",
			Method:         "POST",
			URL:            "/push",
			Body:           "body=body",
			IsJson:         true,
			WantStatusCode: 400,
		},
		{
			Name:           "GET push with too many parameters",
			Method:         "GET",
			URL:            "/" + key + "/title/subtitle/body/extra",
			Body:           "",
			IsJson:         false,
			WantStatusCode: 404,
		},
	})
}

func TestCiphertext(t *testing.T) {
	Endpoint(t, []APITestCase{
		{
			Name:           "Send encrypted push",
			Method:         "GET",
			URL:            "/" + key + "/body?ciphertext=text&iv=01234567890123456",
			Body:           "",
			IsJson:         false,
			WantStatusCode: 200,
		},
		{
			Name:           "Send encrypted push, omit body",
			Method:         "GET",
			URL:            "/" + key + "?ciphertext=text",
			Body:           "",
			IsJson:         false,
			WantStatusCode: 200,
		},
		{
			Name:           "POST send encrypted push",
			Method:         "POST",
			URL:            "/" + key,
			Body:           "ciphertext=text",
			IsJson:         false,
			WantStatusCode: 200,
		},
		{
			Name:           "POST send encrypted push V2",
			Method:         "POST",
			URL:            "/push",
			Body:           "{\"device_key\":\"" + key + "\",\"ciphertext\":\"text\"}",
			IsJson:         true,
			WantStatusCode: 200,
		},
	})
}

func TestBatchPush(t *testing.T) {
	Endpoint(t, []APITestCase{
		{
			Name:           "Batch Push",
			Method:         "POST",
			URL:            "/" + key,
			Body:           "{\"title\":\"title\",\"subtitle\":\"subtitle\",\"body\":\"body\",\"device_keys\":[\"" + key + "\",\"" + key + "\",\"" + key + "\"]}",
			IsJson:         true,
			WantStatusCode: 200,
		},
		{
			Name:           "Batch Push",
			Method:         "POST",
			URL:            "/push",
			Body:           "{\"title\":\"title\",\"subtitle\":\"subtitle\",\"body\":\"body\",\"device_keys\":[\"" + key + "\",\"" + key + "\",\"" + key + "\"]}",
			IsJson:         true,
			WantStatusCode: 200,
		},
		{
			Name:           "Batch Push",
			Method:         "POST",
			URL:            "/push",
			Body:           "{\"title\":\"title\",\"subtitle\":\"subtitle\",\"body\":\"body\",\"device_keys\": \"" + key + "," + key + "," + key + "\"}",
			IsJson:         true,
			WantStatusCode: 200,
		},
	})
}

type APITestCase struct {
	Name           string
	Method         string
	URL            string
	Body           string
	IsJson         bool
	WantStatusCode int
}

func NewServer() *fiber.App {
	fiberApp := fiber.New(fiber.Config{
		JSONEncoder: jsoniter.Marshal,
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			if e, ok := err.(*fiber.Error); ok {
				code = e.Code
			}
			return c.Status(code).JSON(CommonResp{
				Code:      code,
				Message:   err.Error(),
				Timestamp: time.Now().Unix(),
			})
		},
	})

	routerSetup(fiberApp)
	return fiberApp
}

func Endpoint(t *testing.T, tc []APITestCase) {
	EndpointDo(t, tc)
}

func EndpointDo(t *testing.T, tc []APITestCase) {
	for _, tt := range tc {
		t.Run(tt.Name, func(t *testing.T) {
			req, _ := http.NewRequest(tt.Method, tt.URL, bytes.NewBufferString(tt.Body))
			req.Host = "localhost"
			if tt.IsJson {
				req.Header.Set("Content-Type", "application/json")
			} else {
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}
			res, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()

			if res.StatusCode != tt.WantStatusCode {
				body, _ := io.ReadAll(io.Reader(res.Body))
				t.Fatalf("want %d, got %d, res: %s", tt.WantStatusCode, res.StatusCode, string(body))
			}
		})
		// Prevent rate limiting by sending requests too quickly
		time.Sleep(100 * time.Millisecond)
	}
}

// decodeBody unmarshals a JSON response body into a generic map for asserting
// on CommonResp fields (code/message/data).
func decodeBody(t *testing.T, r *http.Response) map[string]interface{} {
	t.Helper()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal body %q: %v", string(b), err)
	}
	return m
}

// overridePushAPNs swaps out the APNs seek for one test and restores the
// stub afterwards, so failure injections never leak into sibling tests.
func overridePushAPNs(t *testing.T, pushFn func(*apns.PushMessage) (int, error)) {
	t.Helper()
	orig := pushAPNs
	pushAPNs = func(msg *apns.PushMessage) (int, error) {
		return pushFn(msg)
	}
	t.Cleanup(func() { pushAPNs = orig })
}

// doPush req executes a single HTTP call against the test app and captures the
// message handed to the APNs stub (or the error it was told to return).
func doPush(t *testing.T, method, url, body string, isJSON bool) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, url, bytes.NewBufferString(body))
	req.Host = "localhost"
	if isJSON {
		req.Header.Set("Content-Type", "application/json")
	} else {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	res, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = res.Body.Close() })
	return res
}

// TestSoundCAFSuffix covers the .caf normalization: a bare name gets the
// suffix appended, an explicit one is kept as-is.
func TestSoundCAFSuffix(t *testing.T) {
	res := doPush(t, "GET", "/"+key+"?sound=minuet", "", false)
	if res.StatusCode != 200 {
		t.Fatalf("push with sound should succeed, got %d", res.StatusCode)
	}
	if m := lastPush(); m == nil {
		t.Fatal("expected a captured APNs push")
	} else if m.Sound != "minuet.caf" {
		t.Fatalf("sound %q should gain .caf, got %q", m.Sound, m.Sound)
	}

	res = doPush(t, "GET", "/"+key+"?sound=minuet.caf", "", false)
	if res.StatusCode != 200 {
		t.Fatalf("push with .caf sound should succeed, got %d", res.StatusCode)
	}
	if m := lastPush(); m == nil {
		t.Fatal("expected a captured APNs push")
	} else if m.Sound != "minuet.caf" {
		t.Fatalf("sound %q should be left untouched", m.Sound)
	}
}

// TestEmptyAlertFallback covers the "Empty Message" body substitution for an
// alert-less push (no title/body/subtitle), which APNs would otherwise drop.
func TestEmptyAlertFallback(t *testing.T) {
	res := doPush(t, "POST", "/push", "{\"device_key\":\""+key+"\"}", true)
	if res.StatusCode != 200 {
		t.Fatalf("alert-less push should succeed, got %d", res.StatusCode)
	}
	if m := lastPush(); m == nil || m.Body != "Empty Message" {
		t.Fatalf("empty alert should fall back to 'Empty Message', got %q", m.Body)
	}
}

// TestBatchPushLimitExceeded covers the guard that caps the number of
// device_keys in a single request.
func TestBatchPushLimitExceeded(t *testing.T) {
	orig := maxBatchPushCount
	maxBatchPushCount = 2
	t.Cleanup(func() { maxBatchPushCount = orig })

	body := "{\"device_keys\":[\"" + key + "\",\"" + key + "\",\"" + key + "\"]}"
	res := doPush(t, "POST", "/push", body, true)
	if res.StatusCode != 400 {
		t.Fatalf("batch over limit should be rejected, got %d", res.StatusCode)
	}
	got := decodeBody(t, res)
	if code, _ := got["code"].(float64); int(code) != 400 {
		t.Fatalf("body code want 400, got %v", got["code"])
	}
}

// TestBatchPushEmptyArray covers the boundary where device_keys is present
// but empty: it degrades to a single-push attempt (missing device_key → 400).
func TestBatchPushEmptyDeviceKeys(t *testing.T) {
	res := doPush(t, "POST", "/push", "{\"device_keys\":[]}", true)
	if res.StatusCode != 400 {
		t.Fatalf("empty device_keys should fall back to a keyless single push, got %d", res.StatusCode)
	}
}

// TestDeviceKeysInvalidType covers the v2 rejection of a non-string,
// non-array device_keys payload.
func TestDeviceKeysInvalidType(t *testing.T) {
	for _, payload := range []string{
		`{"device_keys":123}`,
		`{"device_keys":{"k":"v"}}`,
	} {
		res := doPush(t, "POST", "/push", payload, true)
		if res.StatusCode != 400 {
			t.Fatalf("device_keys type %q want 400, got %d", payload, res.StatusCode)
		}
	}
}

// TestPushAPNsFailure covers the pipeline when APNs rejects the message: the
// HTTP response surfaces the failure and the push is not reported as success.
func TestPushAPNsFailure(t *testing.T) {
	overridePushAPNs(t, func(*apns.PushMessage) (int, error) {
		return 502, errors.New("BadGateway")
	})
	res := doPush(t, "POST", "/push", "{\"device_key\":\""+key+"\"}", true)
	if res.StatusCode != 500 {
		t.Fatalf("APNs failure should surface as 500, got %d", res.StatusCode)
	}
	got := decodeBody(t, res)
	if code, _ := got["code"].(float64); int(code) != 500 {
		t.Fatalf("body code want 500, got %v", got["code"])
	}
}

// TestWellKnownProbeShortCircuit covers the scanner-noise guard: well-known
// probe paths (favicon.ico, /sse, /api/*, ...) falling into the /:device_key
// catch-all get a quiet 404 instead of the 400 lookup failure, and never
// reach APNs or the monitoring stream — unless such a key is genuinely
// registered, in which case it must still push normally.
func TestWellKnownProbeShortCircuit(t *testing.T) {
	apnsCalls := 0
	overridePushAPNs(t, func(*apns.PushMessage) (int, error) {
		apnsCalls++
		return 200, nil
	})

	for _, probe := range []string{"favicon.ico", "robots.txt", "sse", "api/mcp"} {
		res := doPush(t, "GET", "/"+probe, "", false)
		if res.StatusCode != 404 {
			t.Fatalf("probe %q should short-circuit 404, got %d", probe, res.StatusCode)
		}
	}

	res := doPush(t, "POST", "/push", `{"device_key":"favicon.ico"}`, true)
	if res.StatusCode != 404 {
		t.Fatalf("V2 probe push should short-circuit 404, got %d", res.StatusCode)
	}

	if apnsCalls != 0 {
		t.Fatalf("APNs must not be reached for probe paths, got %d calls", apnsCalls)
	}

	// A probe-named key that IS registered must still be pushable. MemBase
	// only serves its single test key, so swap in a stub that answers
	// DevicesByKey("sse") with a real record.
	origDB := db
	db = probeNamedDB{Database: origDB}
	t.Cleanup(func() { db = origDB })

	res = doPush(t, "GET", "/sse?body=hi", "", false)
	if res.StatusCode != 200 {
		t.Fatalf("registered probe-named key should push normally, got %d", res.StatusCode)
	}
	if apnsCalls != 1 {
		t.Fatalf("registered probe-named key should go through APNs once, got %d calls", apnsCalls)
	}
}

// probeNamedDB delegates to the real Database but pretends a device with a
// probe-named key ("sse") is registered, to verify the probe short-circuit
// never breaks genuinely registered keys.
type probeNamedDB struct {
	database.Database
}

func (p probeNamedDB) DevicesByKey(key string) ([]*database.DeviceInfo, error) {
	if key == "sse" {
		return []*database.DeviceInfo{{Key: key, Token: deviceToken, Platform: "ios"}}, nil
	}
	return p.Database.DevicesByKey(key)
}

// TestHarmonyEmptyTitleFallback covers the Huawei V3 title requirement: the
// notification must carry a non-empty title, or the message is accepted
// (hmsCode=0) but silently not displayed. Bark pushes often carry body only
// (V1 path style), so an empty title falls back to the generic DEFAULT_TITLE;
// an explicit title is preserved untouched.
func TestHarmonyEmptyTitleFallback(t *testing.T) {
	registerHarmonyUnderTestKey(t, "harmony-title-fallback-token")

	overridePushAPNs(t, func(*apns.PushMessage) (int, error) { return 200, nil })

	var gotTitle, gotBody string
	overridePushHarmony(t, func(_ []string, title, body, _ string, _ int) (int, int, error) {
		gotTitle, gotBody = title, body
		return 200, 0, nil
	})

	// V1 path-style push carries body only — no title.
	res := doPush(t, "GET", "/"+key+"/qqqqq", "", false)
	if res.StatusCode != 200 {
		t.Fatalf("body-only push should succeed, got %d", res.StatusCode)
	}
	if gotTitle != DEFAULT_TITLE {
		t.Fatalf("empty title should fall back to %q, got %q", DEFAULT_TITLE, gotTitle)
	}
	if gotBody != "qqqqq" {
		t.Fatalf("body should stay %q, got %q", "qqqqq", gotBody)
	}

	// An explicit title is preserved.
	res = doPush(t, "GET", "/"+key+"/qqqqq?title=T", "", false)
	if res.StatusCode != 200 {
		t.Fatalf("titled push should succeed, got %d", res.StatusCode)
	}
	if gotTitle != "T" {
		t.Fatalf("explicit title should be kept, got %q", gotTitle)
	}
}

// TestPushUnregisteredDevice covers the unknown-device path: when the
// device_key is not registered the push must be rejected before APNs.
func TestPushUnregisteredDevice(t *testing.T) {
	orig := pushAPNs
	called := false
	pushAPNs = func(*apns.PushMessage) (int, error) {
		called = true
		return 200, nil
	}
	defer func() { pushAPNs = orig }()

	res := doPush(t, "POST", "/push", "{\"device_key\":\"no-such-key\"}", true)
	if res.StatusCode != 400 {
		t.Fatalf("unknown device should be rejected before APNs, got %d", res.StatusCode)
	}
	if called {
		t.Fatal("APNs must not be reached for an unknown device key")
	}
}

// overridePushHarmony swaps the Harmony push seam for one test and restores
// the original afterwards so multi-platform tests don't leak state into siblings.
func overridePushHarmony(t *testing.T, pushFn func(tokens []string, title, body, data string, actionType int) (int, int, error)) {
	t.Helper()
	orig := pushHarmony
	pushHarmony = pushFn
	t.Cleanup(func() { pushHarmony = orig })
}

// registerHarmonyUnderTestKey adds a harmony record under the test key so the
// multi-platform fan-out tests have a second target alongside the iOS record
// that TestMain installs. The harmony token is cleared on cleanup so
// subsequent tests see the original iOS-only state.
func registerHarmonyUnderTestKey(t *testing.T, token string) {
	t.Helper()
	if _, err := db.SaveDeviceInfo(&database.DeviceInfo{
		Key:      key,
		Token:    token,
		Platform: "harmony",
	}); err != nil {
		t.Fatalf("register harmony failed: %v", err)
	}
	t.Cleanup(func() {
		_ = db.ClearDeviceTokenByKeyAndPlatform(key, "harmony")
	})
}

// TestPushMultiPlatformFanOut covers the core multi-platform invariant: when
// a device_key has both ios and harmony records, a single push fans out to
// both channels. The HTTP response is 200 if any delivery succeeds.
func TestPushMultiPlatformFanOut(t *testing.T) {
	const harmonyTok = "harmony-fanout-token"
	registerHarmonyUnderTestKey(t, harmonyTok)

	apnsCalls := 0
	overridePushAPNs(t, func(*apns.PushMessage) (int, error) {
		apnsCalls++
		return 200, nil
	})

	var harmonyCalls int32
	var harmonyTokens []string
	var tokensMu sync.Mutex
	overridePushHarmony(t, func(tokens []string, _, _, _ string, _ int) (int, int, error) {
		atomic.AddInt32(&harmonyCalls, 1)
		tokensMu.Lock()
		harmonyTokens = append(harmonyTokens, tokens...)
		tokensMu.Unlock()
		return 200, 0, nil
	})

	res := doPush(t, "POST", "/push", `{"device_key":"`+key+`","body":"hi"}`, true)
	if res.StatusCode != 200 {
		t.Fatalf("multi-platform push should succeed, got %d", res.StatusCode)
	}
	if apnsCalls != 1 {
		t.Errorf("APNs should be called once, got %d", apnsCalls)
	}
	if got := atomic.LoadInt32(&harmonyCalls); got != 1 {
		t.Errorf("Harmony should be called once, got %d", got)
	}
	tokensMu.Lock()
	if len(harmonyTokens) != 1 || harmonyTokens[0] != harmonyTok {
		t.Errorf("harmony token mismatch, got %v", harmonyTokens)
	}
	tokensMu.Unlock()
}

// TestPushMultiPlatformPlatformOverride covers explicit platform targeting:
// when the request body specifies platform=harmony, only the harmony record is
// pushed even if an iOS record exists for the same key.
func TestPushMultiPlatformPlatformOverride(t *testing.T) {
	registerHarmonyUnderTestKey(t, "harmony-override-token")

	apnsCalls := 0
	overridePushAPNs(t, func(*apns.PushMessage) (int, error) {
		apnsCalls++
		return 200, nil
	})

	var harmonyCalls int32
	overridePushHarmony(t, func([]string, string, string, string, int) (int, int, error) {
		atomic.AddInt32(&harmonyCalls, 1)
		return 200, 0, nil
	})

	body := `{"device_key":"` + key + `","body":"hi","platform":"harmony"}`
	res := doPush(t, "POST", "/push", body, true)
	if res.StatusCode != 200 {
		t.Fatalf("platform-targeted push should succeed, got %d", res.StatusCode)
	}
	if apnsCalls != 0 {
		t.Errorf("APNs must NOT be called when platform=harmony, got %d", apnsCalls)
	}
	if got := atomic.LoadInt32(&harmonyCalls); got != 1 {
		t.Errorf("Harmony should be called once, got %d", got)
	}
}

// TestPushMultiPlatformSkipClearedToken covers the dead-token skip: when a
// record's token was cleared (e.g. after a prior BadDeviceToken), it is not
// pushed, so only the still-valid platform receives the message.
func TestPushMultiPlatformSkipClearedToken(t *testing.T) {
	registerHarmonyUnderTestKey(t, "harmony-skip-token")

	if err := db.ClearDeviceTokenByKeyAndPlatform(key, "ios"); err != nil {
		t.Fatalf("clear ios token failed: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.SaveDeviceInfo(&database.DeviceInfo{
			Key:      key,
			Token:    deviceToken,
			Platform: "ios",
		})
	})

	apnsCalls := 0
	overridePushAPNs(t, func(*apns.PushMessage) (int, error) {
		apnsCalls++
		return 200, nil
	})

	var harmonyCalls int32
	overridePushHarmony(t, func([]string, string, string, string, int) (int, int, error) {
		atomic.AddInt32(&harmonyCalls, 1)
		return 200, 0, nil
	})

	res := doPush(t, "POST", "/push", `{"device_key":"`+key+`","body":"hi"}`, true)
	if res.StatusCode != 200 {
		t.Fatalf("push to the surviving platform should succeed, got %d", res.StatusCode)
	}
	if apnsCalls != 0 {
		t.Errorf("APNs must NOT be called for cleared iOS token, got %d", apnsCalls)
	}
	if got := atomic.LoadInt32(&harmonyCalls); got != 1 {
		t.Errorf("Harmony should still be called once, got %d", got)
	}
}

// TestPushMultiPlatformAllFail covers the all-failed case: when every target
// delivery fails, the HTTP response surfaces the failure code (500).
func TestPushMultiPlatformAllFail(t *testing.T) {
	registerHarmonyUnderTestKey(t, "harmony-allfail-token")

	overridePushAPNs(t, func(*apns.PushMessage) (int, error) {
		return 502, errors.New("BadGateway")
	})
	overridePushHarmony(t, func([]string, string, string, string, int) (int, int, error) {
		return 500, 80200003, errors.New("harmony error")
	})

	res := doPush(t, "POST", "/push", `{"device_key":"`+key+`","body":"hi"}`, true)
	if res.StatusCode != 500 {
		t.Fatalf("all-failed push should surface 500, got %d", res.StatusCode)
	}
}
