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
	"github.com/wallleap/timelynotify-server/internal/gotifycompat"
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
	overridePushHarmony(t, func(_ []string, title, body, _, _ string, _ int, _ *int, _ string, _ int, _ int, _ []string, _ int) (int, int, error) {
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

// TestHarmonySoundParams verifies that the Bark `sound` parameter reaches
// the HarmonyOS channel as the raw ringtone name (the V3 client appends
// the ".mp3" suffix itself; the APNs ".caf" suffixing must not leak in)
// and that soundDuration is parsed from both JSON-number and query-string
// forms.
func TestHarmonySoundParams(t *testing.T) {
	registerHarmonyUnderTestKey(t, "harmony-sound-token")
	overridePushAPNs(t, func(*apns.PushMessage) (int, error) { return 200, nil })

	var gotSound string
	var gotDuration int
	overridePushHarmony(t, func(_ []string, _, _, _, _ string, _ int, _ *int, sound string, soundDuration int, _ int, _ []string, _ int) (int, int, error) {
		gotSound, gotDuration = sound, soundDuration
		return 200, 0, nil
	})

	// JSON body: bare sound name + numeric soundDuration.
	res := doPush(t, "POST", "/push", `{"device_key":"`+key+`","body":"hi","sound":"minuet","soundDuration":30}`, true)
	if res.StatusCode != 200 {
		t.Fatalf("push with sound should succeed, got %d", res.StatusCode)
	}
	if gotSound != "minuet" {
		t.Fatalf("harmony sound should be raw name %q, got %q", "minuet", gotSound)
	}
	if gotDuration != 30 {
		t.Fatalf("harmony soundDuration should be 30, got %d", gotDuration)
	}

	// V1 query form: soundDuration as string.
	res = doPush(t, "GET", "/"+key+"/hi?sound=alarm&soundDuration=15", "", false)
	if res.StatusCode != 200 {
		t.Fatalf("push with query sound should succeed, got %d", res.StatusCode)
	}
	if gotSound != "alarm" {
		t.Fatalf("harmony sound should be %q, got %q", "alarm", gotSound)
	}
	if gotDuration != 15 {
		t.Fatalf("harmony soundDuration should be 15, got %d", gotDuration)
	}

	// No sound -> empty sound name and zero duration (fields omitted).
	res = doPush(t, "POST", "/push", `{"device_key":"`+key+`","body":"hi"}`, true)
	if res.StatusCode != 200 {
		t.Fatalf("plain push should succeed, got %d", res.StatusCode)
	}
	if gotSound != "" || gotDuration != 0 {
		t.Fatalf("expected empty sound/zero duration, got sound=%q duration=%d", gotSound, gotDuration)
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
func overridePushHarmony(t *testing.T, pushFn func(tokens []string, title, body, data, icon string, actionType int, setNum *int, sound string, soundDuration int, foregroundShow int, inboxContent []string, notifyId int) (int, int, error)) {
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
	overridePushHarmony(t, func(tokens []string, _, _, _, _ string, _ int, _ *int, _ string, _ int, _ int, _ []string, _ int) (int, int, error) {
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
	overridePushHarmony(t, func([]string, string, string, string, string, int, *int, string, int, int, []string, int) (int, int, error) {
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
	overridePushHarmony(t, func([]string, string, string, string, string, int, *int, string, int, int, []string, int) (int, int, error) {
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
	overridePushHarmony(t, func([]string, string, string, string, string, int, *int, string, int, int, []string, int) (int, int, error) {
		return 500, 80200003, errors.New("harmony error")
	})

	res := doPush(t, "POST", "/push", `{"device_key":"`+key+`","body":"hi"}`, true)
	if res.StatusCode != 500 {
		t.Fatalf("all-failed push should surface 500, got %d", res.StatusCode)
	}
}

// overrideRevokeHarmony swaps the Harmony revoke seam for one test and
// restores the original afterwards.
func overrideRevokeHarmony(t *testing.T, fn func(tokens []string, notifyId int) (int, int, error)) {
	t.Helper()
	orig := revokeHarmony
	revokeHarmony = fn
	t.Cleanup(func() { revokeHarmony = orig })
}

// TestIsTruthyFlag covers the truthy-flag parsing used by the `delete`
// entry across the shapes the V1 (query/form strings) and V2 (JSON
// bool/number) entry points produce.
func TestIsTruthyFlag(t *testing.T) {
	cases := []struct {
		name string
		in   interface{}
		want bool
	}{
		{"bool true", true, true},
		{"bool false", false, false},
		{"string 1", "1", true},
		{"string true", "true", true},
		{"string TRUE upper", "TRUE", true},
		{"string yes", "yes", true},
		{"string on", "on", true},
		{"empty string is flag presence (bare ?delete)", "", true},
		{"whitespace padded", "  true ", true},
		{"string 0", "0", false},
		{"string false", "false", false},
		{"string no", "no", false},
		{"json number 1", float64(1), true},
		{"json number 0", float64(0), false},
		{"json number 7", float64(7), true},
		{"int 5", 5, true},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTruthyFlag(tc.in); got != tc.want {
				t.Errorf("isTruthyFlag(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestHarmonyDeleteForms covers the Bark-compatible `delete` withdrawal
// across all accepted flag shapes: a truthy `delete` plus `id` (the
// original notifyId) short-circuits into pushDelete() — the revoke seam is
// called with the harmony tokens and the parsed notifyId, the iOS record on
// the same key receives the standard Bark silent push, the harmony push
// seam is never touched, and all other push params are ignored.
func TestHarmonyDeleteForms(t *testing.T) {
	const harmonyTok = "harmony-delete-token"
	registerHarmonyUnderTestKey(t, harmonyTok)

	var (
		mu          sync.Mutex
		gotTokens   []string
		gotNotifyID int
	)
	var revokeCalls int32
	overrideRevokeHarmony(t, func(tokens []string, notifyId int) (int, int, error) {
		mu.Lock()
		gotTokens = append([]string(nil), tokens...)
		gotNotifyID = notifyId
		mu.Unlock()
		atomic.AddInt32(&revokeCalls, 1)
		return 200, 80000000, nil
	})

	apnsCalls := 0
	var gotMsg *apns.PushMessage
	overridePushAPNs(t, func(msg *apns.PushMessage) (int, error) {
		m := *msg
		gotMsg = &m
		apnsCalls++
		return 200, nil
	})

	// V2 JSON: boolean flag + numeric id; title/body are irrelevant and
	// must not leak into the payload (the delete branch builds a pure
	// ContentAvailable payload without alert fields).
	res := doPush(t, "POST", "/push", `{"device_key":"`+key+`","delete":true,"id":12345,"title":"ignored","body":"ignored"}`, true)
	if res.StatusCode != 200 {
		t.Fatalf("V2 delete should succeed, got %d", res.StatusCode)
	}
	if got := atomic.LoadInt32(&revokeCalls); got != 1 {
		t.Fatalf("revoke should be called once, got %d", got)
	}
	if gotNotifyID != 12345 {
		t.Errorf("expected notifyId=12345, got %d", gotNotifyID)
	}
	if len(gotTokens) != 1 || gotTokens[0] != harmonyTok {
		t.Errorf("expected harmony token %q, got %v", harmonyTok, gotTokens)
	}
	if gotMsg == nil || !gotMsg.IsDelete() {
		t.Fatalf("APNs should receive a delete-flagged push, got %+v", gotMsg)
	}
	if v, _ := gotMsg.ExtParams["delete"].(string); v != "1" {
		t.Errorf("payload should carry the canonical delete=1, got %v", gotMsg.ExtParams["delete"])
	}

	// V1 query form: delete=1 with a string id.
	res = doPush(t, "GET", "/"+key+"?delete=1&id=678", "", false)
	if res.StatusCode != 200 {
		t.Fatalf("V1 delete should succeed, got %d", res.StatusCode)
	}
	if gotNotifyID != 678 {
		t.Errorf("expected notifyId=678, got %d", gotNotifyID)
	}

	// Flag-style presence (?delete) and "true" both count as enabled.
	res = doPush(t, "GET", "/"+key+"?delete=true&id=999", "", false)
	if res.StatusCode != 200 {
		t.Fatalf("delete=true should succeed, got %d", res.StatusCode)
	}
	if gotNotifyID != 999 {
		t.Errorf("expected notifyId=999, got %d", gotNotifyID)
	}

	res = doPush(t, "GET", "/"+key+"?delete&id=42", "", false)
	if res.StatusCode != 200 {
		t.Fatalf("bare ?delete flag should succeed, got %d", res.StatusCode)
	}
	if gotNotifyID != 42 {
		t.Errorf("expected notifyId=42, got %d", gotNotifyID)
	}

	// JSON numeric flag form.
	res = doPush(t, "POST", "/push", `{"device_key":"`+key+`","delete":1,"id":7}`, true)
	if res.StatusCode != 200 {
		t.Fatalf("numeric delete flag should succeed, got %d", res.StatusCode)
	}
	if gotNotifyID != 7 {
		t.Errorf("expected notifyId=7, got %d", gotNotifyID)
	}

	// Large numeric id (int32 max, the HMS notifyId upper bound): JSON
	// numbers decode as float64 and must NOT round-trip through
	// fmt.Sprint (which yields "2.147483647e+09" — toInt would then
	// silently parse 2). The revoke seam must see the exact integer.
	res = doPush(t, "POST", "/push", `{"device_key":"`+key+`","delete":true,"id":2147483647}`, true)
	if res.StatusCode != 200 {
		t.Fatalf("large numeric id delete should succeed, got %d: %s", res.StatusCode, decodeBody(t, res))
	}
	if gotNotifyID != 2147483647 {
		t.Errorf("expected notifyId=2147483647, got %d", gotNotifyID)
	}

	if apnsCalls != 6 {
		t.Errorf("iOS record should receive the silent-push removal for every delete request, got %d APNs calls", apnsCalls)
	}
}

// TestHarmonyDeleteMissingID covers the 400 when delete is requested
// without a usable positive numeric id; neither the revoke seam nor APNs
// may be reached (both platforms locate the notification to remove by
// `id`).
func TestHarmonyDeleteMissingID(t *testing.T) {
	registerHarmonyUnderTestKey(t, "harmony-delete-noid-token")

	var revokeCalls int32
	overrideRevokeHarmony(t, func([]string, int) (int, int, error) {
		atomic.AddInt32(&revokeCalls, 1)
		return 200, 0, nil
	})
	apnsCalls := 0
	overridePushAPNs(t, func(*apns.PushMessage) (int, error) {
		apnsCalls++
		return 200, nil
	})

	for _, body := range []string{
		`{"device_key":"` + key + `","delete":true}`,
		`{"device_key":"` + key + `","delete":true,"id":"abc"}`,
		`{"device_key":"` + key + `","delete":true,"id":-5}`,
		`{"device_key":"` + key + `","delete":true,"id":0}`,
	} {
		res := doPush(t, "POST", "/push", body, true)
		if res.StatusCode != 400 {
			t.Fatalf("body %s: expected 400, got %d", body, res.StatusCode)
		}
	}

	// V1 form without id.
	res := doPush(t, "GET", "/"+key+"?delete=1", "", false)
	if res.StatusCode != 400 {
		t.Fatalf("delete without id should be 400, got %d", res.StatusCode)
	}
	if got := atomic.LoadInt32(&revokeCalls); got != 0 {
		t.Errorf("revoke seam must not be called without a valid id, got %d", got)
	}
	if apnsCalls != 0 {
		t.Errorf("APNs must not be called without a valid id, got %d", apnsCalls)
	}
}

// TestHarmonyDeletePartialFailure verifies the any-success rule: when the
// HarmonyOS revoke fails but the iOS silent push succeeds, the overall
// request still succeeds (200).
func TestHarmonyDeletePartialFailure(t *testing.T) {
	registerHarmonyUnderTestKey(t, "harmony-delete-partial-token")

	var revokeCalls int32
	overrideRevokeHarmony(t, func([]string, int) (int, int, error) {
		atomic.AddInt32(&revokeCalls, 1)
		return 500, 80000001, errors.New("huawei internal error")
	})
	apnsCalls := 0
	overridePushAPNs(t, func(*apns.PushMessage) (int, error) {
		apnsCalls++
		return 200, nil
	})

	res := doPush(t, "POST", "/push", `{"device_key":"`+key+`","delete":1,"id":123}`, true)
	if res.StatusCode != 200 {
		t.Fatalf("delete should succeed when any channel succeeds, got %d", res.StatusCode)
	}
	if got := atomic.LoadInt32(&revokeCalls); got != 1 || apnsCalls != 1 {
		t.Errorf("both channels should be attempted once, got revoke=%d apns=%d", got, apnsCalls)
	}
}

// TestHarmonyDeleteAllFail surfaces a full failure (both the Huawei revoke
// and the iOS silent push failed) as 500.
func TestHarmonyDeleteAllFail(t *testing.T) {
	registerHarmonyUnderTestKey(t, "harmony-delete-allfail-token")
	overrideRevokeHarmony(t, func([]string, int) (int, int, error) {
		return 500, 80000001, errors.New("huawei internal error")
	})
	overridePushAPNs(t, func(*apns.PushMessage) (int, error) {
		return 502, errors.New("BadGateway")
	})

	res := doPush(t, "POST", "/push", `{"device_key":"`+key+`","delete":1,"id":123}`, true)
	if res.StatusCode != 500 {
		t.Fatalf("all-failed delete should surface 500, got %d", res.StatusCode)
	}
}

// TestHarmonyDeleteInvalidTokenClears verifies that an 80200001 (invalid
// token) response on the revoke clears the stored harmony token, mirroring
// the push path's dead-token cleanup. The iOS token is cleared so the
// target set is harmony-only and the revoke failure surfaces as 500.
func TestHarmonyDeleteInvalidTokenClears(t *testing.T) {
	registerHarmonyUnderTestKey(t, "harmony-delete-badtoken")
	if err := db.ClearDeviceTokenByKeyAndPlatform(key, "ios"); err != nil {
		t.Fatalf("clear ios token: %v", err)
	}
	t.Cleanup(func() { _, _ = db.SaveDeviceTokenByKey(key, deviceToken) })
	overrideRevokeHarmony(t, func([]string, int) (int, int, error) {
		return 500, 80200001, errors.New("invalid token")
	})

	res := doPush(t, "POST", "/push", `{"device_key":"`+key+`","delete":1,"id":123}`, true)
	if res.StatusCode != 500 {
		t.Fatalf("expected 500 for invalid token, got %d", res.StatusCode)
	}

	devices, err := db.DevicesByKey(key)
	if err != nil {
		t.Fatalf("db lookup: %v", err)
	}
	for _, di := range devices {
		if di.Platform == "harmony" && di.Token != "" {
			t.Fatalf("harmony token should be cleared after 80200001, got %q", di.Token)
		}
	}
}

// TestHarmonyDeleteFlagFalse verifies a falsey delete value (false/0) does
// NOT enter delete mode — the request fans out as a normal push.
func TestHarmonyDeleteFlagFalse(t *testing.T) {
	registerHarmonyUnderTestKey(t, "harmony-delete-false-token")

	apnsCalls := 0
	overridePushAPNs(t, func(*apns.PushMessage) (int, error) {
		apnsCalls++
		return 200, nil
	})
	var revokeCalls, harmonyCalls int32
	overrideRevokeHarmony(t, func([]string, int) (int, int, error) {
		atomic.AddInt32(&revokeCalls, 1)
		return 200, 0, nil
	})
	overridePushHarmony(t, func([]string, string, string, string, string, int, *int, string, int, int, []string, int) (int, int, error) {
		atomic.AddInt32(&harmonyCalls, 1)
		return 200, 0, nil
	})

	res := doPush(t, "POST", "/push", `{"device_key":"`+key+`","delete":false,"body":"hi","id":123}`, true)
	if res.StatusCode != 200 {
		t.Fatalf("normal push should succeed, got %d", res.StatusCode)
	}
	if atomic.LoadInt32(&revokeCalls) != 0 {
		t.Errorf("revoke must not be called when flag is false, got %d", revokeCalls)
	}
	if atomic.LoadInt32(&harmonyCalls) != 1 || apnsCalls != 1 {
		t.Errorf("falsey delete should fan out as a normal push, got harmony=%d apns=%d", harmonyCalls, apnsCalls)
	}
}

// TestHarmonyDeleteRevokes covers the Bark-compatible `delete` trigger for
// HarmonyOS withdrawal: the request short-circuits into pushDelete() which
// must call the revoke seam (never the harmony push seam) with the parsed
// notifyId, while the iOS record on the same key still receives the
// standard Bark silent-push removal.
func TestHarmonyDeleteRevokes(t *testing.T) {
	const harmonyTok = "harmony-delete-token"
	registerHarmonyUnderTestKey(t, harmonyTok)

	apnsCalls := 0
	overridePushAPNs(t, func(*apns.PushMessage) (int, error) {
		apnsCalls++
		return 200, nil
	})
	var harmonyPushCalls int32
	overridePushHarmony(t, func([]string, string, string, string, string, int, *int, string, int, int, []string, int) (int, int, error) {
		atomic.AddInt32(&harmonyPushCalls, 1)
		return 200, 80000000, nil
	})

	var (
		mu          sync.Mutex
		gotTokens   []string
		gotNotifyID int
	)
	var revokeCalls int32
	overrideRevokeHarmony(t, func(tokens []string, notifyId int) (int, int, error) {
		mu.Lock()
		gotTokens = append([]string(nil), tokens...)
		gotNotifyID = notifyId
		mu.Unlock()
		atomic.AddInt32(&revokeCalls, 1)
		return 200, 80000000, nil
	})

	// V2 JSON numeric flag + numeric id: harmony revokes, iOS keeps the
	// silent-push removal.
	res := doPush(t, "POST", "/push", `{"device_key":"`+key+`","delete":1,"id":12345}`, true)
	if res.StatusCode != 200 {
		t.Fatalf("V2 delete revoke should succeed, got %d: %s", res.StatusCode, decodeBody(t, res))
	}
	if got := atomic.LoadInt32(&revokeCalls); got != 1 {
		t.Fatalf("revoke should be called once, got %d", got)
	}
	if gotNotifyID != 12345 {
		t.Errorf("expected notifyId=12345, got %d", gotNotifyID)
	}
	if len(gotTokens) != 1 || gotTokens[0] != harmonyTok {
		t.Errorf("expected harmony token %q, got %v", harmonyTok, gotTokens)
	}
	if atomic.LoadInt32(&harmonyPushCalls) != 0 {
		t.Errorf("harmony push must not be called for delete, got %d calls", harmonyPushCalls)
	}
	if apnsCalls != 1 {
		t.Errorf("iOS record should still receive the silent-push removal, got %d APNs calls", apnsCalls)
	}

	// V1 query form.
	res = doPush(t, "GET", "/"+key+"?delete=1&id=678", "", false)
	if res.StatusCode != 200 {
		t.Fatalf("V1 delete revoke should succeed, got %d", res.StatusCode)
	}
	if gotNotifyID != 678 {
		t.Errorf("expected notifyId=678, got %d", gotNotifyID)
	}

	// JSON bool flag form (same shapes the revoke flag accepts).
	res = doPush(t, "POST", "/push", `{"device_key":"`+key+`","delete":true,"id":777}`, true)
	if res.StatusCode != 200 {
		t.Fatalf("bool delete flag should succeed, got %d", res.StatusCode)
	}
	if gotNotifyID != 777 {
		t.Errorf("expected notifyId=777, got %d", gotNotifyID)
	}
}

// TestHarmonyDeleteHarmonyOnly covers a harmony-only target set: delete=1
// withdraws via the revoke seam and never touches APNs; an unusable id
// surfaces as 400 (no other target can mask the failure) and the revoke seam
// stays cold. MemBase serves a single key, so harmony-only is simulated by
// clearing the iOS record's token (targets skip empty tokens).
func TestHarmonyDeleteHarmonyOnly(t *testing.T) {
	const harmonyTok = "harmony-delete-only-token"
	registerHarmonyUnderTestKey(t, harmonyTok)
	if err := db.ClearDeviceTokenByKeyAndPlatform(key, "ios"); err != nil {
		t.Fatalf("clear ios token: %v", err)
	}
	t.Cleanup(func() { _, _ = db.SaveDeviceTokenByKey(key, deviceToken) })

	var revokeCalls int32
	overrideRevokeHarmony(t, func([]string, int) (int, int, error) {
		atomic.AddInt32(&revokeCalls, 1)
		return 200, 80000000, nil
	})
	apnsCalls := 0
	overridePushAPNs(t, func(*apns.PushMessage) (int, error) {
		apnsCalls++
		return 200, nil
	})

	res := doPush(t, "POST", "/push", `{"device_key":"`+key+`","delete":1,"id":42}`, true)
	if res.StatusCode != 200 {
		t.Fatalf("harmony-only delete should succeed, got %d: %s", res.StatusCode, decodeBody(t, res))
	}
	if got := atomic.LoadInt32(&revokeCalls); got != 1 {
		t.Fatalf("revoke should be called once, got %d", got)
	}
	if apnsCalls != 0 {
		t.Errorf("APNs must not be called for a harmony-only key, got %d calls", apnsCalls)
	}

	for _, body := range []string{
		`{"device_key":"` + key + `","delete":1}`,
		`{"device_key":"` + key + `","delete":1,"id":"abc"}`,
		`{"device_key":"` + key + `","delete":1,"id":-5}`,
		`{"device_key":"` + key + `","delete":1,"id":0}`,
	} {
		res := doPush(t, "POST", "/push", body, true)
		if res.StatusCode != 400 {
			t.Fatalf("body %s: expected 400, got %d", body, res.StatusCode)
		}
	}
	if got := atomic.LoadInt32(&revokeCalls); got != 1 {
		t.Errorf("revoke seam must not be called without a valid id, got %d", got)
	}
}

// TestHarmonyDeleteIOSOnlyKeepsSilentPush locks the pre-existing Bark
// behavior: on a key without harmony records, delete=1 stays a silent APNs
// push and never reaches the revoke seam.
func TestHarmonyDeleteIOSOnlyKeepsSilentPush(t *testing.T) {
	var revokeCalls int32
	overrideRevokeHarmony(t, func([]string, int) (int, int, error) {
		atomic.AddInt32(&revokeCalls, 1)
		return 200, 80000000, nil
	})
	apnsCalls := 0
	overridePushAPNs(t, func(msg *apns.PushMessage) (int, error) {
		apnsCalls++
		if !msg.IsDelete() {
			t.Errorf("iOS push for delete=1 should be flagged as delete")
		}
		return 200, nil
	})

	res := doPush(t, "POST", "/push", `{"device_key":"`+key+`","delete":1,"id":99}`, true)
	if res.StatusCode != 200 {
		t.Fatalf("iOS-only delete should succeed, got %d: %s", res.StatusCode, decodeBody(t, res))
	}
	if apnsCalls != 1 {
		t.Errorf("expected exactly one APNs call, got %d", apnsCalls)
	}
	if got := atomic.LoadInt32(&revokeCalls); got != 0 {
		t.Errorf("revoke seam must not be called without harmony targets, got %d", got)
	}
}

// TestHarmonyDeleteCleansHistory verifies the server-side cleanup wired into
// pushDelete: a delete=1 request removes the stored history message carrying
// the same extras.id and appends a DeletionByExtraID record to the device's
// deletion log (surfaced as extraIds), on top of the platform fan-out.
func TestHarmonyDeleteCleansHistory(t *testing.T) {
	// Swap the global gotify service for a fresh bbolt-backed one.
	old := gotifyService
	svc, err := gotifycompat.Init(gotifycompat.Config{
		DataDir:     t.TempDir(),
		ClientToken: "delete-history-token",
	})
	if err != nil {
		t.Fatalf("gotifycompat.Init: %v", err)
	}
	gotifyService = svc
	t.Cleanup(func() {
		gotifyService = old
		_ = svc.Close()
	})

	// Seed the history message targeted by the delete (extras.id=4242).
	if err := svc.Publish("t", "b", 0, map[string]interface{}{"device_key": key, "id": "4242"}); err != nil {
		t.Fatalf("seed publish: %v", err)
	}
	// Anchor event so the deletion-log baseline cursor is non-zero.
	if err := svc.Publish("anchor", "b", 0, map[string]interface{}{"device_key": key}); err != nil {
		t.Fatalf("anchor publish: %v", err)
	}
	if ok, err := svc.DeleteMessageByDevice(key, 2); err != nil || !ok {
		t.Fatalf("anchor delete: ok=%v err=%v", ok, err)
	}
	base, err := svc.DeletionsByDevice(key, 0)
	if err != nil || base.Cursor == 0 {
		t.Fatalf("baseline cursor: %+v err=%v", base, err)
	}

	registerHarmonyUnderTestKey(t, "harmony-delete-history-token")
	overrideRevokeHarmony(t, func([]string, int) (int, int, error) {
		return 200, 80000000, nil
	})
	overridePushAPNs(t, func(*apns.PushMessage) (int, error) {
		return 200, nil
	})

	res := doPush(t, "POST", "/push", `{"device_key":"`+key+`","delete":1,"id":4242}`, true)
	if res.StatusCode != 200 {
		t.Fatalf("delete should succeed, got %d: %s", res.StatusCode, decodeBody(t, res))
	}

	// The server-side history copy must be gone.
	msgs, err := svc.MessagesByDevice(key, 100, 0)
	if err != nil {
		t.Fatalf("MessagesByDevice: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("history message must be removed by delete=1, got %+v", msgs)
	}

	// The deletion log carries both the kind=1 tombstone of the removed
	// message (id 1) and the extras.id record.
	page, err := svc.DeletionsByDevice(key, base.Cursor)
	if err != nil {
		t.Fatalf("DeletionsByDevice: %v", err)
	}
	if len(page.ExtraIDs) != 1 || page.ExtraIDs[0] != 4242 {
		t.Fatalf("want extraIds [4242], got %v", page.ExtraIDs)
	}
	if len(page.IDs) != 1 || page.IDs[0] != 1 {
		t.Fatalf("want kind=1 ids [1], got %v", page.IDs)
	}
}

// TestToInt locks down strict integer parsing. In particular a
// scientific-notation string ("2.147483647e+09", produced by
// fmt.Sprint on a large float64 JSON number) must be rejected — the
// previous fmt.Sscanf("%d") implementation silently parsed the leading
// "2" and returned no error.
func TestToInt(t *testing.T) {
	cases := []struct {
		in      string
		want    int
		wantErr bool
	}{
		{"123", 123, false},
		{"0", 0, false},
		{"2147483647", 2147483647, false},
		{" 42 ", 42, false},
		{"-5", -5, false},
		{"", 0, true},
		{"abc", 0, true},
		{"2.147483647e+09", 0, true},
		{"12.5", 0, true},
		{"12abc", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := toInt(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("toInt(%q) = %d, want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("toInt(%q) unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("toInt(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// TestNumberToString covers the JSON-number-to-string normalization used
// for id/badge/soundDuration. A large float64 (JSON numbers decode as
// float64) must render as a plain integer string, never in scientific
// notation (fmt.Sprint would produce "2.147483647e+09").
func TestNumberToString(t *testing.T) {
	cases := []struct {
		name string
		in   interface{}
		want string
	}{
		{"float64 int32 max", float64(2147483647), "2147483647"},
		{"float64 small", float64(12345), "12345"},
		{"float64 zero", float64(0), "0"},
		{"int", 7, "7"},
		{"int64", int64(9999999999), "9999999999"},
		{"string passthrough", "42", "42"},
		{"json.Number", json.Number("2147483647"), "2147483647"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := numberToString(tc.in); got != tc.want {
				t.Errorf("numberToString(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestPushLargeNumericID verifies the SEND path with a large JSON numeric
// id: the HarmonyOS seam must receive notifyId=2147483647 (not 2 from a
// scientific-notation truncation) and the APNs collapse id must be the
// plain string "2147483647".
func TestPushLargeNumericID(t *testing.T) {
	registerHarmonyUnderTestKey(t, "harmony-largeid-token")

	var (
		mu           sync.Mutex
		gotNotifyID  int
		gotApnsID    string
		harmonyCalls int32
	)
	overridePushHarmony(t, func(_ []string, _, _, _, _ string, _ int, _ *int, _ string, _, _ int, _ []string, notifyId int) (int, int, error) {
		mu.Lock()
		gotNotifyID = notifyId
		harmonyCalls++
		mu.Unlock()
		return 200, 80000000, nil
	})
	overridePushAPNs(t, func(msg *apns.PushMessage) (int, error) {
		mu.Lock()
		gotApnsID = msg.Id
		mu.Unlock()
		return 200, nil
	})

	res := doPush(t, "POST", "/push", `{"device_key":"`+key+`","id":2147483647,"body":"large id"}`, true)
	if res.StatusCode != 200 {
		t.Fatalf("push with large numeric id should succeed, got %d: %s", res.StatusCode, decodeBody(t, res))
	}
	if atomic.LoadInt32(&harmonyCalls) != 1 {
		t.Fatalf("harmony push should be called once, got %d", harmonyCalls)
	}
	if gotNotifyID != 2147483647 {
		t.Errorf("expected notifyId=2147483647, got %d", gotNotifyID)
	}
	if gotApnsID != "2147483647" {
		t.Errorf("expected APNs id %q, got %q", "2147483647", gotApnsID)
	}
}
