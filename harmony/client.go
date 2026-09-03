package harmony

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// sendAPIURL is the Huawei Push Kit downlink message sending endpoint.
// The project ID is interpolated at runtime since it is part of the
// credentials defined in harmony_certs.go.
//
// Use v3 per Huawei's "基于服务账号生成鉴权令牌" guide:
//
//	https://push-api.cloud.huawei.com/v3/[projectId]/messages:send
//
// V3 only supports HarmonyOS NEXT/5.x and later; V2 was for 3.x/4.x; V1 is
// the legacy form and is not recommended. This project targets HarmonyOS
// NEXT, so v3 is required.
const sendAPIURL = "https://push-api.cloud.huawei.com/v3/%s/messages:send"

// pushTypeHeader is the HTTP header name for the scenario message type.
// Per the V3 docs, the request MUST carry a "push-type" header; 0 = Alert
// (notification) message.
const (
	pushTypeHeader  = "push-type"
	pushTypeAlert   = "0"
	defaultCategory = "SUBSCRIPTION"
	defaultTTL      = 86400
	// maxSoundDuration is the V3 soundDuration upper bound in seconds.
	// Per Huawei docs the valid range is [1, 60]; the ringtone loops
	// until the duration elapses.
	maxSoundDuration = 60
)

// Message is the V3 scenario message request body.
//
// Per Huawei Push Kit V3 "推送场景化消息" / "发送通知消息" docs, the
// structure is:
//
//	{
//	  "payload": { "notification": {...} },
//	  "target":  { "token": ["..."] },
//	  "pushOptions": { "testMessage": false, "ttl": 86400 }
//	}
//
// The legacy V1/V2 shape ({message:{token,notification,data}}) is NOT
// accepted by the V3 endpoint and would silently drop messages.
type Message struct {
	Payload     Payload      `json:"payload"`
	Target      Target       `json:"target"`
	PushOptions *PushOptions `json:"pushOptions,omitempty"`
}

type Payload struct {
	Notification *Notification `json:"notification,omitempty"`
}

type Target struct {
	Token []string `json:"token"`
}

type PushOptions struct {
	TestMessage bool `json:"testMessage,omitempty"`
	TTL         int  `json:"ttl,omitempty"`
}

// Badge controls the app icon badge shown on the home screen.
// Per Huawei V3 docs addNum is the increment and setNum is the
// absolute value.  addNum is always at least 1; setNum is omitted
// when zero (via omitempty) so the server just increments the count.
type Badge struct {
	AddNum int  `json:"addNum,omitempty"`
	SetNum *int `json:"setNum,omitempty"`
}

type Notification struct {
	Category string `json:"category"` // e.g. "MARKETING"
	Title    string `json:"title"`
	Body     string `json:"body"`
	// Image is the URL of the large icon shown on the right side of the
	// notification.  Per Huawei docs it must be HTTPS and one of
	// PNG/JPG/JPEG/BMP (recommended <= 128x128 px).  Maps from Bark `icon`.
	Image          string       `json:"image,omitempty"`
	Badge          Badge        `json:"badge"`
	ClickAction    *ClickAction `json:"clickAction,omitempty"`
	ForegroundShow bool         `json:"foregroundShow"`
	// Sound is the custom notification ringtone file name resolved
	// against the app's /resources/rawfile directory (e.g. "alert.mp3").
	// Empty omits the field so the system default ringtone is used.
	// Ignored (silent) when category is MARKETING; the custom ringtone
	// right must also be granted in AGC.
	Sound string `json:"sound,omitempty"`
	// SoundDuration is the ringtone playback duration in seconds; only
	// effective together with Sound. Range [1, 60]: a shorter ringtone
	// loops until the duration elapses. Zero/omitted falls back to the
	// default 30s truncation.
	SoundDuration int `json:"soundDuration,omitempty"`
	// InboxContent is the V3 multi-line notification body. When non-empty
	// the notification renders as an inbox-style list (one row per entry)
	// and Style MUST be set to 3 — the client wires that pairing in Send.
	// See Huawei V3 "通知样式" docs.
	InboxContent []string `json:"inboxContent,omitempty"`
	// Style is the V3 notification display style. 0 = default (omitted),
	// 1 = big text, 2 = big picture, 3 = inbox. Send sets it to 3 when
	// InboxContent is non-empty; it stays 0 (omitted) otherwise.
	Style int `json:"style,omitempty"`
	// NotifyId is the V3 notification unique identifier (integer, range
	// [0, 2147483647]). Notifications sharing the same notifyId replace
	// each other; -1 or omitted lets Push Kit auto-generate one. Mapped
	// from the Bark `id` param (parsed to int; non-numeric values are
	// dropped). Zero is omitted (auto-generate) via omitempty.
	NotifyId int `json:"notifyId,omitempty"`
}

// ClickAction mirrors the V3 clickAction object. actionType 0 opens the
// app home, 1 opens an inner page (requires action or uri).
type ClickAction struct {
	ActionType int                    `json:"actionType"`
	Action     string                 `json:"action,omitempty"`
	URI        string                 `json:"uri,omitempty"`
	Data       map[string]interface{} `json:"data,omitempty"`
}

// Client is a minimal wrapper around the Huawei Push Kit HTTP API.
// It is safe for concurrent use and relies on TokenSource for auth.
type Client struct {
	ts      *TokenSource
	httpCli *http.Client
	baseURL string // Allows overriding the API endpoint (e.g. for testing)
}

// NewClient constructs a Client using the package-level projectID and
// a sensible HTTP timeout.
func NewClient(ts *TokenSource) *Client {
	return &Client{
		ts:      ts,
		httpCli: &http.Client{Timeout: 15 * time.Second},
	}
}

// NewClientWithURL is like NewClient but allows overriding the base URL.
// This is useful for local testing against a mock server.
func NewClientWithURL(ts *TokenSource, baseURL string) *Client {
	return &Client{
		ts:      ts,
		httpCli: &http.Client{Timeout: 15 * time.Second},
		baseURL: baseURL,
	}
}

// Send sends a push message to one or more target tokens.
//
// actionType: 0 = open app home on click, 1 = open inner page.
// data: optional JSON string placed under clickAction.data; if it is not
// valid JSON it is wrapped as {"data": <string>}. Pass "" to omit.
// icon: optional HTTPS URL for the notification large icon (maps to
// notification.image). Pass "" to omit.
// badgeNum: optional absolute badge value pointer controlling the V3
// badge object shape.  nil = default behaviour (sends addNum:1 to increment
// by one), non-nil = send setNum to the given value (including zero, which
// clears the badge).  addNum and setNum are NEVER both sent because V3
// semantics treat setNum as overriding addNum.
// sound: optional custom ringtone name mapped to notification.sound.
// A bare Bark sound name (shared with the iOS .caf sounds) gets ".mp3"
// appended for the HarmonyOS /resources/rawfile lookup; names already
// carrying an audio extension (.mp3/.wav/.mpeg) are kept as-is. Pass ""
// for the system default ringtone.
// soundDuration: ringtone playback duration in seconds, clamped to
// [1, 60]; only sent when sound is non-empty.
// foregroundShow: controls the V3 notification.foregroundShow field.
// 1 → true (display notifications while the app is in the foreground),
// any other value → false. The caller (route_push.go) is responsible
// for defaulting to 1 when the user does not pass the param.
// inboxContent: optional V3 notification.inboxContent multi-line body.
// When non-empty, Style is auto-set to 3 (inbox style) per the V3 docs;
// pass nil/empty to omit both fields.
// notifyId: optional V3 notification.notifyId (int, range [0, 2147483647]).
// Notifications with the same notifyId replace each other. Pass 0 to
// omit (let Push Kit auto-generate); the caller maps Bark `id` (string)
// to int — non-numeric values yield 0 (omitted).
//
// It returns the Huawei HTTP status code, the server's error code (if
// any), and an error (wrapped with context). On token-expired errors it
// invalidates the local cache and retries exactly once.
func (c *Client) Send(targetTokens []string, title, body, data, icon string, actionType int, badgeNum *int, sound string, soundDuration int, foregroundShow int, inboxContent []string, notifyId int) (httpStatus int, hmsCode int, err error) {
	if len(targetTokens) == 0 {
		return 0, 0, fmt.Errorf("no target tokens provided")
	}

	clickAction := &ClickAction{ActionType: actionType}
	if data != "" {
		clickAction.Data = parseDataField(data)
	}

	// Huawei V3 badge semantics: addNum and setNum are mutually exclusive.
	// When both are present setNum overrides addNum, so we send exactly one:
	//   - badgeNum == nil: addNum:1          (default: increment by 1)
	//   - badgeNum != nil: setNum=*badgeNum  (explicit absolute value, incl. 0)
	badge := Badge{}
	if badgeNum != nil {
		v := *badgeNum
		badge.SetNum = &v
	} else {
		badge.AddNum = 1
	}

	// Custom ringtone: normalize the Bark sound name to a rawfile file
	// name. soundDuration only takes effect alongside a sound, so drop it
	// (and clamp to [1, 60]) at the same boundary.
	sound = normalizeSoundName(sound)
	var soundDur int
	if sound != "" {
		soundDur = clampSoundDuration(soundDuration)
	}

	notification := &Notification{
		Category:       defaultCategory,
		Title:          title,
		Body:           body,
		Image:          icon,
		Badge:          badge,
		ClickAction:    clickAction,
		ForegroundShow: foregroundShow == 1,
		Sound:          sound,
		SoundDuration:  soundDur,
		NotifyId:       notifyId,
	}
	// V3 requires style=3 (inbox) whenever inboxContent is present;
	// omitting style silently degrades to the default single-line layout.
	if len(inboxContent) > 0 {
		notification.InboxContent = inboxContent
		notification.Style = 3
	}

	msg := &Message{
		Payload: Payload{
			Notification: notification,
		},
		Target: Target{Token: targetTokens},
		PushOptions: &PushOptions{
			TestMessage: false,
			TTL:         defaultTTL,
		},
	}

	return c.sendWithRetry(msg)
}

// parseDataField tries to parse data as a JSON object; on failure it wraps
// the raw string under a "data" key so custom data is never lost.
func parseDataField(data string) map[string]interface{} {
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(data), &obj); err == nil {
		return obj
	}
	return map[string]interface{}{"data": data}
}

// normalizeSoundName maps a Bark ringtone name to the HarmonyOS rawfile
// file name expected by the V3 notification.sound field. HarmonyOS
// resolves custom ringtones under the app's /resources/rawfile directory
// and requires the file extension (MP3/WAV/MPEG...). Bark sound names are
// shared across platforms but iOS uses .caf while the HarmonyOS client
// ships .mp3, so a bare name like "minuet" becomes "minuet.mp3"; a name
// already carrying an audio extension (case-insensitive) is kept as-is.
func normalizeSoundName(sound string) string {
	sound = strings.TrimSpace(sound)
	if sound == "" {
		return ""
	}
	// A .caf suffix is the iOS convention and never exists in the
	// HarmonyOS rawfile; strip it before deciding on the .mp3 suffix.
	if strings.HasSuffix(strings.ToLower(sound), ".caf") {
		sound = sound[:len(sound)-len(".caf")]
	}
	lower := strings.ToLower(sound)
	for _, ext := range []string{".mp3", ".wav", ".mpeg"} {
		if strings.HasSuffix(lower, ext) {
			return sound
		}
	}
	return sound + ".mp3"
}

// clampSoundDuration bounds the V3 soundDuration field to its documented
// range [1, 60] seconds. Non-positive values return 0 so the field is
// omitted (omitempty, default 30s truncation); values above 60 are capped.
func clampSoundDuration(seconds int) int {
	if seconds < 1 {
		return 0
	}
	if seconds > maxSoundDuration {
		return maxSoundDuration
	}
	return seconds
}

// sendWithRetry performs the actual HTTP call. It retries once on the
// specific "access token expired" (80200003) error, which is the most
// common transient failure.
func (c *Client) sendWithRetry(msg *Message) (httpStatus int, hmsCode int, err error) {
	for attempt := 0; attempt < 2; attempt++ {
		httpStatus, hmsCode, err = c.doSend(msg)
		if err != nil {
			// If the error indicates an expired token, invalidate and retry.
			if hmsCode == 80200003 && attempt == 0 {
				c.ts.ForceInvalidate()
				continue
			}
			return httpStatus, hmsCode, err
		}
		return httpStatus, hmsCode, nil
	}
	return httpStatus, hmsCode, err
}

// doSend marshals the message and performs a single HTTP POST.
func (c *Client) doSend(msg *Message) (httpStatus int, hmsCode int, err error) {
	payload, err := json.Marshal(msg)
	if err != nil {
		return 0, 0, fmt.Errorf("marshal message: %w", err)
	}

	// Get a valid JWT
	jwtToken, err := c.ts.Get()
	if err != nil {
		return 0, 0, fmt.Errorf("get auth token: %w", err)
	}

	// Use custom base URL if set, otherwise default
	apiURL := c.baseURL
	if apiURL == "" {
		apiURL = fmt.Sprintf(sendAPIURL, projectID)
	}

	req, err := http.NewRequest(http.MethodPost, apiURL, bytes.NewReader(payload))
	if err != nil {
		return 0, 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+jwtToken)
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	// V3 scenario message API requires the "push-type" header.
	// 0 = Alert (notification) message.
	req.Header.Set(pushTypeHeader, pushTypeAlert)

	resp, err := c.httpCli.Do(req)
	if err != nil {
		return 0, 0, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, 0, fmt.Errorf("read response: %w", err)
	}

	// Attempt to extract the Huawei-specific error code from the response.
	// Even if the HTTP status code is 200, the API may return a non-zero
	// business error code in the JSON body.
	var hmsResp hmsResponse
	jsonErr := json.Unmarshal(body, &hmsResp)
	var hmsRespCode int64
	if jsonErr == nil {
		hmsRespCode, _ = hmsResp.Code.Int64()
	}
	if jsonErr == nil {
		// Huawei returns code 80000000 for success (along with HTTP 200).
		// Non-zero codes other than 80000000 are actual errors.
		if hmsRespCode != 0 && hmsRespCode != 80000000 {
			return resp.StatusCode, int(hmsRespCode), fmt.Errorf("huawei push API error: status=%d code=%d message=%s", resp.StatusCode, hmsRespCode, hmsResp.Message)
		}
	} else if resp.StatusCode != http.StatusOK {
		// HTTP non-200 AND response body is not valid JSON — Huawei returned
		// a plain-text error (gateway timeouts, SSL errors, etc.). Mark this
		// distinctly so callers can tell "Huawei said code 80200001 invalid
		// token" from "Huawei returned a 502 with an HTML error page".
		return resp.StatusCode, 0, fmt.Errorf("huawei push API non-JSON error: status=%d body=%s", resp.StatusCode, string(body))
	} else {
		// HTTP 200 but body is not JSON — an edge case that shouldn't happen
		// in normal operation, but we still need to surface it rather than
		// silently pretending everything is fine (which is what the old code
		// did: hmsCode=0 with no error).
		return resp.StatusCode, 0, fmt.Errorf("huawei push API returned HTTP 200 but non-JSON body: %s", string(body))
	}

	// If HTTP status is 200 and no business error, it's a success.
	if resp.StatusCode == http.StatusOK {
		return resp.StatusCode, int(hmsRespCode), nil
	}

	return resp.StatusCode, 0, fmt.Errorf("huawei push API request failed: status=%d body=%s", resp.StatusCode, string(body))
}

// hmsResponse models the typical error response body returned by the
// Huawei Push Kit API.
type hmsResponse struct {
	Code    json.Number `json:"code"`
	Message string      `json:"msg"`
}
