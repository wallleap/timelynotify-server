package harmony

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// sendAPIURL is the Huawei Push Kit downlink message sending endpoint.
// The project ID is interpolated at runtime since it is part of the
// credentials defined in harmony_certs.go.
const sendAPIURL = "https://push-api.cloud.huawei.com/v1/%s/messages:send"

// Message structures — subset of the Huawei Push Kit REST API. We define
// our own structs (instead of relying on an external SDK) to keep the
// package self-contained and to stay close to the wire format.

type Message struct {
	ValidateOnly bool       `json:"validate_only,omitempty"`
	Message      PushMessage `json:"message"`
}

type PushMessage struct {
	Token []string    `json:"token"`
	Data  string      `json:"data,omitempty"`
	Notify *Notification `json:"notification,omitempty"`
}

type Notification struct {
	Title       string `json:"title"`
	Body        string `json:"body"`
	ClickAction string `json:"click_action,omitempty"` // "launch", "banner", "page"
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
// It returns the Huawei HTTP status code, the server's error code (if
// any), and an error (wrapped with context). On token-expired errors it
// invalidates the local cache and retries exactly once.
func (c *Client) Send(targetTokens []string, title, body, data, clickAction string) (httpStatus int, hmsCode int, err error) {
	if len(targetTokens) == 0 {
		return 0, 0, fmt.Errorf("no target tokens provided")
	}

	notify := &Notification{
		Title:       title,
		Body:        body,
		ClickAction: clickAction,
	}

	msg := Message{
		Message: PushMessage{
			Token:  targetTokens,
			Data:   data,
			Notify: notify,
		},
	}

	return c.sendWithRetry(&msg)
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
	if jsonErr := json.Unmarshal(body, &hmsResp); jsonErr == nil {
		// Huawei returns code 80000000 for success (along with HTTP 200).
		// Non-zero codes other than 80000000 are actual errors.
		if hmsResp.Code != 0 && hmsResp.Code != 80000000 {
			return resp.StatusCode, hmsResp.Code, fmt.Errorf("huawei push API error: status=%d code=%d message=%s", resp.StatusCode, hmsResp.Code, hmsResp.Message)
		}
	}

	// If HTTP status is 200 and no business error, it's a success.
	if resp.StatusCode == http.StatusOK {
		return resp.StatusCode, 0, nil
	}

	return resp.StatusCode, 0, fmt.Errorf("huawei push API request failed: status=%d body=%s", resp.StatusCode, string(body))
}

// hmsResponse models the typical error response body returned by the
// Huawei Push Kit API.
type hmsResponse struct {
	Code    int    `json:"code"`
	Message string `json:"msg"`
}
