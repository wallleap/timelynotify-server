package harmony

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestClient_Send_EmptyTokens verifies that sending with no tokens fails early.
func TestClient_Send_EmptyTokens(t *testing.T) {
	ts, _ := NewTokenSource()
	client := NewClient(ts)
	status, hmsCode, err := client.Send(nil, "title", "body", "", "", 0, nil, "", 0, 0, nil, 0)
	if err == nil {
		t.Fatal("expected error for empty tokens")
	}
	if status != 0 || hmsCode != 0 {
		t.Errorf("expected zero codes, got status=%d hmsCode=%d", status, hmsCode)
	}
}

// TestClient_Send_JSONPayload verifies the exact V3 JSON structure sent to
// the Huawei API. Per the V3 docs the body MUST be:
//
//	{
//	  "payload": { "notification": { category, title, body, clickAction, foregroundShow } },
//	  "target":  { "token": ["..."] },
//	  "pushOptions": { "testMessage": false, "ttl": 86400 }
//	}
//
// The legacy {message:{token,notification}} shape is silently rejected by V3.
func TestClient_Send_JSONPayload(t *testing.T) {
	var capturedBody []byte
	var capturedAuth string
	var capturedPushType string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		capturedPushType = r.Header.Get(pushTypeHeader)
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		capturedBody = buf

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	ts, _ := NewTokenSource()
	client := NewClient(ts)
	projectID = "test-project"

	client.httpCli = &http.Client{
		Transport: &rewriteTransport{target: server.URL},
	}

	_, _, err := client.Send([]string{"token1"}, "Hello", "World", "", "", 0, nil, "", 0, 1, nil, 0)
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	// Authorization header
	if !strings.HasPrefix(capturedAuth, "Bearer ") {
		t.Errorf("expected Authorization header to start with 'Bearer ', got %q", capturedAuth)
	}

	// push-type header is REQUIRED by V3
	if capturedPushType != "0" {
		t.Errorf("expected push-type header '0', got %q", capturedPushType)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(capturedBody, &payload); err != nil {
		t.Fatalf("failed to unmarshal request body: %v", err)
	}

	// V3 top-level keys: payload, target, pushOptions (NOT "message")
	target, ok := payload["target"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected 'target' object in V3 payload, got: %v", payload)
	}
	tokens, ok := target["token"].([]interface{})
	if !ok || len(tokens) != 1 || tokens[0].(string) != "token1" {
		t.Errorf("expected target.token=[\"token1\"], got %v", target["token"])
	}

	payloadObj, ok := payload["payload"].(map[string]interface{})
	if !ok {
		t.Fatal("expected 'payload' object in V3 body")
	}
	notify, ok := payloadObj["notification"].(map[string]interface{})
	if !ok {
		t.Fatal("expected 'payload.notification' object")
	}
	if notify["title"] != "Hello" {
		t.Errorf("expected title='Hello', got %v", notify["title"])
	}
	if notify["body"] != "World" {
		t.Errorf("expected body='World', got %v", notify["body"])
	}
	if notify["category"] != "SUBSCRIPTION" {
		t.Errorf("expected category='SUBSCRIPTION', got %v", notify["category"])
	}
	if notify["foregroundShow"] != true {
		t.Errorf("expected foregroundShow=true, got %v", notify["foregroundShow"])
	}

	// clickAction must be an object with actionType (V3), NOT a string
	clickAction, ok := notify["clickAction"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected clickAction object, got %T: %v", notify["clickAction"], notify["clickAction"])
	}
	if clickAction["actionType"] != float64(0) {
		t.Errorf("expected clickAction.actionType=0, got %v", clickAction["actionType"])
	}

	// pushOptions
	pushOpts, ok := payload["pushOptions"].(map[string]interface{})
	if !ok {
		t.Fatal("expected 'pushOptions' object in V3 body")
	}
	if pushOpts["ttl"] != float64(86400) {
		t.Errorf("expected pushOptions.ttl=86400, got %v", pushOpts["ttl"])
	}
}

// TestClient_Send_DataJSON verifies that the data string is parsed as JSON
// and placed under clickAction.data.
func TestClient_Send_DataJSON(t *testing.T) {
	var capturedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		capturedBody = buf
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	ts, _ := NewTokenSource()
	client := NewClientWithURL(ts, server.URL)
	client.httpCli = &http.Client{
		Transport: &rewriteTransport{target: server.URL},
	}

	_, _, _ = client.Send([]string{"t1"}, "title", "body", `{"key":"value"}`, "", 0, nil, "", 0, 0, nil, 0)
	var payload map[string]interface{}
	_ = json.Unmarshal(capturedBody, &payload)
	notify := payload["payload"].(map[string]interface{})["notification"].(map[string]interface{})
	clickAction := notify["clickAction"].(map[string]interface{})
	data, ok := clickAction["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected clickAction.data to be a JSON object, got %T", clickAction["data"])
	}
	if data["key"] != "value" {
		t.Errorf("expected data.key='value', got %v", data["key"])
	}
}

// TestClient_Send_DataNonJSON verifies that a non-JSON data string is wrapped
// under a "data" key rather than dropped.
func TestClient_Send_DataNonJSON(t *testing.T) {
	var capturedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		capturedBody = buf
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	ts, _ := NewTokenSource()
	client := NewClientWithURL(ts, server.URL)
	client.httpCli = &http.Client{
		Transport: &rewriteTransport{target: server.URL},
	}

	_, _, _ = client.Send([]string{"t1"}, "title", "body", "plain-string", "", 0, nil, "", 0, 0, nil, 0)
	var payload map[string]interface{}
	_ = json.Unmarshal(capturedBody, &payload)
	notify := payload["payload"].(map[string]interface{})["notification"].(map[string]interface{})
	clickAction := notify["clickAction"].(map[string]interface{})
	data, ok := clickAction["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected clickAction.data wrapped object, got %T", clickAction["data"])
	}
	if data["data"] != "plain-string" {
		t.Errorf("expected data.data='plain-string', got %v", data["data"])
	}
}

// TestClient_Send_RetryOnTokenExpired verifies that the client retries
// once when the server returns error code 80200003.
func TestClient_Send_RetryOnTokenExpired(t *testing.T) {
	var callCount int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.Header().Set("Content-Type", "application/json")

		if atomic.LoadInt32(&callCount) == 1 {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(hmsResponse{Code: json.Number("80200003"), Message: "access token expired"})
		} else {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(struct{}{})
		}
	}))
	defer server.Close()

	ts, _ := NewTokenSource()
	client := NewClient(ts)
	projectID = "test-project-retry"
	client.httpCli = &http.Client{
		Transport: &rewriteTransport{target: server.URL},
	}

	status, hmsCode, err := client.Send([]string{"token1"}, "title", "body", "", "", 0, nil, "", 0, 0, nil, 0)
	if err != nil {
		t.Fatalf("Send should succeed after retry, got err: %v", err)
	}
	if status != http.StatusOK {
		t.Errorf("expected status 200, got %d", status)
	}
	if hmsCode != 0 {
		t.Errorf("expected hmsCode 0 after successful retry, got %d", hmsCode)
	}
	if count := atomic.LoadInt32(&callCount); count != 2 {
		t.Errorf("expected 2 calls due to retry, got %d", count)
	}
}

// TestClient_Send_DoNotRetryOnOtherError verifies that the client does
// NOT retry on non-token-expired errors.
func TestClient_Send_DoNotRetryOnOtherError(t *testing.T) {
	var callCount int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(hmsResponse{Code: json.Number("80200001"), Message: "invalid token"})
	}))
	defer server.Close()

	ts, _ := NewTokenSource()
	client := NewClient(ts)
	projectID = "test-project-no-retry"
	client.httpCli = &http.Client{
		Transport: &rewriteTransport{target: server.URL},
	}

	_, _, _ = client.Send([]string{"bad_token"}, "title", "body", "", "", 0, nil, "", 0, 0, nil, 0)
	if count := atomic.LoadInt32(&callCount); count != 1 {
		t.Errorf("expected only 1 call for non-retryable error, got %d", count)
	}
}

// rewriteTransport is an http.RoundTripper that redirects a specific
// Huawei API URL to a local test server.
// intPtr is a tiny test helper that returns a pointer to an int literal,
// making Send() calls with explicit absolute badge values (including zero)
// readable without extra local variables.
func intPtr(i int) *int { return &i }

type rewriteTransport struct {
	target string
}

func (rt *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(rt.target, "http://")
	return http.DefaultTransport.RoundTrip(req)
}

// TestClient_Send_SuccessCode80000000 verifies that the client correctly
// treats Huawei's success code 80000000 as a successful response, not
// an error. Huawei returns HTTP 200 with code=80000000 for successful
// push delivery.
func TestClient_Send_SuccessCode80000000(t *testing.T) {
	var callCount int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(hmsResponse{Code: json.Number("80000000"), Message: "Success"})
	}))
	defer server.Close()

	ts, _ := NewTokenSource()
	client := NewClientWithURL(ts, server.URL)
	projectID = "test-project-success"
	client.httpCli = &http.Client{
		Transport: &rewriteTransport{target: server.URL},
	}

	status, hmsCode, err := client.Send([]string{"token1"}, "Hello", "World", "", "", 0, nil, "", 0, 0, nil, 0)
	if err != nil {
		t.Fatalf("Send should succeed with code 80000000, got error: %v", err)
	}
	if status != http.StatusOK {
		t.Errorf("expected status 200, got %d", status)
	}
	if hmsCode != 80000000 {
		t.Errorf("expected hmsCode 80000000 (real Huawei code), got %d", hmsCode)
	}
	if count := atomic.LoadInt32(&callCount); count != 1 {
		t.Errorf("expected 1 call, got %d", count)
	}
}

// TestClient_Send_StringCodeInvalidToken verifies error detection against
// the REAL Huawei response shape, where "code" is a JSON string
// ({"code":"80200001","msg":"invalid token"}) rather than a number.
// Business errors arrive with HTTP 200, so a failure to parse the code
// silently turns errors into successes.
func TestClient_Send_StringCodeInvalidToken(t *testing.T) {
	var callCount int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"code":"80200001","msg":"invalid token","requestId":"req-1"}`))
	}))
	defer server.Close()

	ts, _ := NewTokenSource()
	client := NewClientWithURL(ts, server.URL)
	projectID = "test-project-strcode-err"
	client.httpCli = &http.Client{
		Transport: &rewriteTransport{target: server.URL},
	}

	_, hmsCode, err := client.Send([]string{"bad_token"}, "Hello", "World", "", "", 0, nil, "", 0, 0, nil, 0)
	if err == nil {
		t.Fatal("expected error for string-coded invalid token, got nil")
	}
	if hmsCode != 80200001 {
		t.Errorf("expected hmsCode 80200001, got %d", hmsCode)
	}
	if count := atomic.LoadInt32(&callCount); count != 1 {
		t.Errorf("expected only 1 call (no retry), got %d", count)
	}
}

// TestClient_Send_StringCodeSuccess verifies that a string-coded success
// response (the real API shape: {"code":"80000000","msg":"Success"}) is
// treated as success and the returned hmsCode is 80000000 so that server
// logs correspond to what Huawei actually returned.
func TestClient_Send_StringCodeSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"code":"80000000","msg":"Success","requestId":"req-2"}`))
	}))
	defer server.Close()

	ts, _ := NewTokenSource()
	client := NewClientWithURL(ts, server.URL)
	projectID = "test-project-strcode-ok"
	client.httpCli = &http.Client{
		Transport: &rewriteTransport{target: server.URL},
	}

	status, hmsCode, err := client.Send([]string{"token1"}, "Hello", "World", "", "", 0, nil, "", 0, 0, nil, 0)
	if err != nil {
		t.Fatalf("Send should succeed with string code 80000000, got error: %v", err)
	}
	if status != http.StatusOK {
		t.Errorf("expected status 200, got %d", status)
	}
	if hmsCode != 80000000 {
		t.Errorf("expected hmsCode 80000000 (real Huawei code), got %d", hmsCode)
	}
}

// TestClient_Send_HttpError verifies that non-200 HTTP responses are
// treated as errors.
func TestClient_Send_HttpError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("Bad Request"))
	}))
	defer server.Close()

	ts, _ := NewTokenSource()
	client := NewClientWithURL(ts, server.URL)
	projectID = "test-project-httperr"
	client.httpCli = &http.Client{
		Transport: &rewriteTransport{target: server.URL},
	}

	status, _, err := client.Send([]string{"token1"}, "Hello", "World", "", "", 0, nil, "", 0, 0, nil, 0)
	if err == nil {
		t.Fatal("expected error for HTTP 400, got nil")
	}
	if status != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", status)
	}
}

// TestClient_Send_NetworkError verifies that network-level errors are
// returned as errors.
func TestClient_Send_NetworkError(t *testing.T) {
	ts, _ := NewTokenSource()
	client := NewClientWithURL(ts, "http://localhost:19999")

	_, _, err := client.Send([]string{"token1"}, "Hello", "World", "", "", 0, nil, "", 0, 0, nil, 0)
	if err == nil {
		t.Fatal("expected error for network failure, got nil")
	}
}

// TestClient_Send_InvalidToken verifies that the client handles the
// Huawei invalid token error code (80200001) correctly without retrying.
func TestClient_Send_InvalidToken(t *testing.T) {
	var callCount int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(hmsResponse{Code: json.Number("80200001"), Message: "invalid token"})
	}))
	defer server.Close()

	ts, _ := NewTokenSource()
	client := NewClientWithURL(ts, server.URL)
	projectID = "test-project-invalid"
	client.httpCli = &http.Client{
		Transport: &rewriteTransport{target: server.URL},
	}

	_, hmsCode, err := client.Send([]string{"bad_token"}, "Hello", "World", "", "", 0, nil, "", 0, 0, nil, 0)
	if err == nil {
		t.Fatal("expected error for invalid token, got nil")
	}
	if hmsCode != 80200001 {
		t.Errorf("expected hmsCode 80200001, got %d", hmsCode)
	}
	if count := atomic.LoadInt32(&callCount); count != 1 {
		t.Errorf("expected only 1 call (no retry), got %d", count)
	}
}

// TestClient_Send_ActionType verifies that the actionType (0 or 1) is
// correctly forwarded as clickAction.actionType in the V3 body.
func TestClient_Send_ActionType(t *testing.T) {
	var capturedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		capturedBody = buf
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	ts, _ := NewTokenSource()
	client := NewClientWithURL(ts, server.URL)
	client.httpCli = &http.Client{
		Transport: &rewriteTransport{target: server.URL},
	}

	client.Send([]string{"token1"}, "Hello", "World", "", "", 1, nil, "", 0, 0, nil, 0)
	var payload map[string]interface{}
	if err := json.Unmarshal(capturedBody, &payload); err != nil {
		t.Fatalf("failed to unmarshal request: %v", err)
	}

	notify := payload["payload"].(map[string]interface{})["notification"].(map[string]interface{})
	clickAction := notify["clickAction"].(map[string]interface{})

	if clickAction["actionType"] != float64(1) {
		t.Errorf("expected clickAction.actionType=1, got %v", clickAction["actionType"])
	}
}

// TestClient_Send_PushTypeHeader verifies that the mandatory "push-type: 0"
// header is sent on every request (V3 requirement).
func TestClient_Send_PushTypeHeader(t *testing.T) {
	var capturedPushType string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPushType = r.Header.Get(pushTypeHeader)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	ts, _ := NewTokenSource()
	client := NewClientWithURL(ts, server.URL)
	client.httpCli = &http.Client{
		Transport: &rewriteTransport{target: server.URL},
	}

	_, _, _ = client.Send([]string{"token1"}, "Hello", "World", "", "", 0, nil, "", 0, 0, nil, 0)
	if capturedPushType != "0" {
		t.Errorf("expected push-type header '0', got %q", capturedPushType)
	}
}

// TestNewClientWithURL verifies that NewClientWithURL correctly sets the
// base URL and that NewClient uses the default URL.
func TestNewClientWithURL(t *testing.T) {
	ts, _ := NewTokenSource()

	client1 := NewClient(ts)
	if client1.baseURL != "" {
		t.Errorf("expected empty baseURL for NewClient, got %q", client1.baseURL)
	}

	client2 := NewClientWithURL(ts, "http://localhost:9999")
	if client2.baseURL != "http://localhost:9999" {
		t.Errorf("expected baseURL='http://localhost:9999', got %q", client2.baseURL)
	}
}

// TestClient_Send_BadgeDefault verifies that when setNum <= 0 the badge
// object is still included with addNum:1 (the default per project rules),
// but setNum is omitted because its zero value uses omitempty.
//
// Per the Huawei V3 docs the badge shape is:
//
//	"badge": { "addNum": 1 }                    // default, no setNum
//	"badge": { "addNum": 1, "setNum": 99 }      // explicit badge value
//
// TestClient_Send_BadgeSetNumZero verifies that passing an explicit
// zero via *int (not nil) serializes setNum:0 (badge cleared). This is
// distinct from the default (nil = addNum:1 increment) because 0 is a
// valid absolute badge value in Huawei V3 semantics.
// TestClient_Send_Image verifies the Bark `icon` parameter maps to the
// Huawei V3 notification.image field (right-side large icon URL) and is
// omitted when empty.
func TestClient_Send_Image(t *testing.T) {
	var capturedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		capturedBody = buf
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	ts, _ := NewTokenSource()
	client := NewClientWithURL(ts, server.URL)
	client.httpCli = &http.Client{
		Transport: &rewriteTransport{target: server.URL},
	}

	_, _, _ = client.Send([]string{"t1"}, "title", "body", "", "https://example.com/icon.png", 0, nil, "", 0, 0, nil, 0)

	var payload map[string]interface{}
	_ = json.Unmarshal(capturedBody, &payload)
	notify := payload["payload"].(map[string]interface{})["notification"].(map[string]interface{})

	if notify["image"] != "https://example.com/icon.png" {
		t.Errorf("expected notification.image=https://example.com/icon.png, got %v", notify["image"])
	}

	// Empty icon must omit the image key entirely (omitempty).
	_, _, _ = client.Send([]string{"t1"}, "title", "body", "", "", 0, nil, "", 0, 0, nil, 0)
	_ = json.Unmarshal(capturedBody, &payload)
	notify = payload["payload"].(map[string]interface{})["notification"].(map[string]interface{})
	if _, ok := notify["image"]; ok {
		t.Errorf("expected notification.image omitted when icon empty, got %v", notify["image"])
	}
}

func TestClient_Send_BadgeSetNumZero(t *testing.T) {
	var capturedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		capturedBody = buf
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	ts, _ := NewTokenSource()
	client := NewClientWithURL(ts, server.URL)
	client.httpCli = &http.Client{
		Transport: &rewriteTransport{target: server.URL},
	}

	_, _, _ = client.Send([]string{"t1"}, "title", "body", "", "", 0, intPtr(0), "", 0, 0, nil, 0)

	var payload map[string]interface{}
	_ = json.Unmarshal(capturedBody, &payload)
	notify := payload["payload"].(map[string]interface{})["notification"].(map[string]interface{})

	badge, ok := notify["badge"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected badge object in notification, got %T", notify["badge"])
	}
	// Explicit *int=0 → absolute setNum:0 must be sent (addNum must NOT appear)
	if _, hasAddNum := badge["addNum"]; hasAddNum {
		t.Errorf("expected badge.addNum omitted for explicit setNum=0, got addNum=%v", badge["addNum"])
	}
	if _, hasSetNum := badge["setNum"]; !hasSetNum {
		t.Fatalf("expected badge.setNum key present (even when 0) for explicit *int=0")
	}
	if badge["setNum"] != float64(0) {
		t.Errorf("expected badge.setNum=0, got %v", badge["setNum"])
	}
}

func TestClient_Send_BadgeDefault(t *testing.T) {
	var capturedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		capturedBody = buf
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	ts, _ := NewTokenSource()
	client := NewClientWithURL(ts, server.URL)
	client.httpCli = &http.Client{
		Transport: &rewriteTransport{target: server.URL},
	}

	_, _, _ = client.Send([]string{"t1"}, "title", "body", "", "", 0, nil, "", 0, 0, nil, 0)

	var payload map[string]interface{}
	_ = json.Unmarshal(capturedBody, &payload)
	notify := payload["payload"].(map[string]interface{})["notification"].(map[string]interface{})

	badge, ok := notify["badge"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected badge object in notification (always on with addNum default), got %T", notify["badge"])
	}
	if badge["addNum"] != float64(1) {
		t.Errorf("expected badge.addNum=1 (default), got %v", badge["addNum"])
	}
	if _, hasSetNum := badge["setNum"]; hasSetNum {
		t.Errorf("expected badge.setNum to be omitted when setNum=0, got %v", badge["setNum"])
	}
}

// TestClient_Send_BadgeWithSetNum verifies that when a badge value is
// provided (setNum > 0) both addNum:1 and setNum=<value> are present in
// the serialized badge object.
func TestClient_Send_BadgeWithSetNum(t *testing.T) {
	var capturedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		capturedBody = buf
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	ts, _ := NewTokenSource()
	client := NewClientWithURL(ts, server.URL)
	client.httpCli = &http.Client{
		Transport: &rewriteTransport{target: server.URL},
	}

	_, _, _ = client.Send([]string{"t1"}, "title", "body", "", "", 0, intPtr(99), "", 0, 0, nil, 0)

	var payload map[string]interface{}
	_ = json.Unmarshal(capturedBody, &payload)
	notify := payload["payload"].(map[string]interface{})["notification"].(map[string]interface{})

	badge, ok := notify["badge"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected badge object, got %T", notify["badge"])
	}
	// When an explicit setNum is provided Huawei treats setNum as overriding
	// addNum, so we must send ONLY setNum without addNum to avoid ambiguity.
	if _, hasAddNum := badge["addNum"]; hasAddNum {
		t.Errorf("expected badge.addNum to be omitted when setNum=99 is set, got addNum=%v", badge["addNum"])
	}
	if badge["setNum"] != float64(99) {
		t.Errorf("expected badge.setNum=99, got %v", badge["setNum"])
	}
}

// TestNormalizeSoundName covers the Bark sound name -> HarmonyOS rawfile
// file name mapping: bare names gain ".mp3" (HarmonyOS ships ringtones as
// mp3 under /resources/rawfile, vs .caf on iOS), names already carrying an
// audio extension (.mp3/.wav/.mpeg, case-insensitive) are kept, and the
// iOS ".caf" suffix is remapped to ".mp3".
func TestNormalizeSoundName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"bare name gains mp3", "minuet", "minuet.mp3"},
		{"whitespace trimmed then suffixed", "  alarm  ", "alarm.mp3"},
		{"mp3 kept", "bell.mp3", "bell.mp3"},
		{"wav case-insensitive kept", "alert.WAV", "alert.WAV"},
		{"mpeg case-insensitive kept", "chime.MpEg", "chime.MpEg"},
		{"caf remapped to mp3", "minuet.caf", "minuet.mp3"},
		{"uppercase caf remapped", "minuet.CAF", "minuet.mp3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeSoundName(tc.in); got != tc.want {
				t.Errorf("normalizeSoundName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestClampSoundDuration covers the V3 soundDuration range [1, 60]:
// non-positive values become 0 (field omitted via omitempty), values
// above 60 are capped.
func TestClampSoundDuration(t *testing.T) {
	cases := []struct {
		in, want int
	}{
		{-5, 0},
		{0, 0},
		{1, 1},
		{30, 30},
		{60, 60},
		{61, 60},
		{999, 60},
	}
	for _, tc := range cases {
		if got := clampSoundDuration(tc.in); got != tc.want {
			t.Errorf("clampSoundDuration(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// TestClient_Send_Sound verifies that the Bark `sound` parameter maps to
// the V3 notification.sound field (bare name auto-suffixed with .mp3) and
// soundDuration maps to notification.soundDuration in seconds.
func TestClient_Send_Sound(t *testing.T) {
	var capturedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		capturedBody = buf
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	ts, _ := NewTokenSource()
	client := NewClientWithURL(ts, server.URL)
	client.httpCli = &http.Client{
		Transport: &rewriteTransport{target: server.URL},
	}

	_, _, _ = client.Send([]string{"t1"}, "title", "body", "", "", 0, nil, "minuet", 30, 0, nil, 0)

	var payload map[string]interface{}
	if err := json.Unmarshal(capturedBody, &payload); err != nil {
		t.Fatalf("failed to unmarshal request: %v", err)
	}
	notify := payload["payload"].(map[string]interface{})["notification"].(map[string]interface{})

	if notify["sound"] != "minuet.mp3" {
		t.Errorf("expected notification.sound=minuet.mp3 (auto .mp3 suffix), got %v", notify["sound"])
	}
	if notify["soundDuration"] != float64(30) {
		t.Errorf("expected notification.soundDuration=30, got %v", notify["soundDuration"])
	}

	// An explicit .wav name must be kept unchanged (no double suffix).
	_, _, _ = client.Send([]string{"t1"}, "title", "body", "", "", 0, nil, "alert.WAV", 10, 0, nil, 0)
	_ = json.Unmarshal(capturedBody, &payload)
	notify = payload["payload"].(map[string]interface{})["notification"].(map[string]interface{})
	if notify["sound"] != "alert.WAV" {
		t.Errorf("expected notification.sound=alert.WAV kept as-is, got %v", notify["sound"])
	}
	if notify["soundDuration"] != float64(10) {
		t.Errorf("expected notification.soundDuration=10, got %v", notify["soundDuration"])
	}
}

// TestClient_Send_SoundOmitted verifies that without an explicit sound
// neither sound nor soundDuration is serialized — soundDuration only
// takes effect together with sound, and the default ringtone is used
// when sound is absent.
func TestClient_Send_SoundOmitted(t *testing.T) {
	var capturedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		capturedBody = buf
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	ts, _ := NewTokenSource()
	client := NewClientWithURL(ts, server.URL)
	client.httpCli = &http.Client{
		Transport: &rewriteTransport{target: server.URL},
	}

	// Duration without sound must be dropped entirely.
	_, _, _ = client.Send([]string{"t1"}, "title", "body", "", "", 0, nil, "", 30, 0, nil, 0)

	var payload map[string]interface{}
	_ = json.Unmarshal(capturedBody, &payload)
	notify := payload["payload"].(map[string]interface{})["notification"].(map[string]interface{})

	if _, ok := notify["sound"]; ok {
		t.Errorf("expected notification.sound omitted when empty, got %v", notify["sound"])
	}
	if _, ok := notify["soundDuration"]; ok {
		t.Errorf("expected notification.soundDuration omitted without sound, got %v", notify["soundDuration"])
	}
}

// TestClient_Send_SoundDurationClamped verifies that out-of-range
// soundDuration values are clamped to the documented [1, 60] range
// instead of being rejected or sent verbatim.
func TestClient_Send_SoundDurationClamped(t *testing.T) {
	var capturedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		capturedBody = buf
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	ts, _ := NewTokenSource()
	client := NewClientWithURL(ts, server.URL)
	client.httpCli = &http.Client{
		Transport: &rewriteTransport{target: server.URL},
	}

	_, _, _ = client.Send([]string{"t1"}, "title", "body", "", "", 0, nil, "minuet", 999, 0, nil, 0)

	var payload map[string]interface{}
	_ = json.Unmarshal(capturedBody, &payload)
	notify := payload["payload"].(map[string]interface{})["notification"].(map[string]interface{})

	if notify["sound"] != "minuet.mp3" {
		t.Errorf("expected notification.sound=minuet.mp3, got %v", notify["sound"])
	}
	if notify["soundDuration"] != float64(60) {
		t.Errorf("expected notification.soundDuration clamped to 60, got %v", notify["soundDuration"])
	}

	// Zero duration with sound: field omitted (default 30s truncation).
	_, _, _ = client.Send([]string{"t1"}, "title", "body", "", "", 0, nil, "minuet", 0, 0, nil, 0)
	_ = json.Unmarshal(capturedBody, &payload)
	notify = payload["payload"].(map[string]interface{})["notification"].(map[string]interface{})
	if notify["sound"] != "minuet.mp3" {
		t.Errorf("expected notification.sound=minuet.mp3 kept, got %v", notify["sound"])
	}
	if _, ok := notify["soundDuration"]; ok {
		t.Errorf("expected notification.soundDuration omitted for 0, got %v", notify["soundDuration"])
	}
}

// TestClient_Send_ForegroundShow verifies the foregroundShow parameter
// mapping per the V3 docs: 1 → true (display notifications while the app
// is in the foreground), any other value → false. The caller (route_push.go)
// is responsible for defaulting to 1 when the user does not pass the param.
func TestClient_Send_ForegroundShow(t *testing.T) {
	var capturedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		capturedBody = buf
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	ts, _ := NewTokenSource()
	client := NewClientWithURL(ts, server.URL)
	client.httpCli = &http.Client{
		Transport: &rewriteTransport{target: server.URL},
	}

	cases := []struct {
		name string
		in   int
		want bool
	}{
		{"one is true", 1, true},
		{"zero is false", 0, false},
		{"negative is false", -1, false},
		{"two is false", 2, false},
		{"large is false", 999, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _ = client.Send([]string{"t1"}, "title", "body", "", "", 0, nil, "", 0, tc.in, nil, 0)
			var payload map[string]interface{}
			if err := json.Unmarshal(capturedBody, &payload); err != nil {
				t.Fatalf("failed to unmarshal request: %v", err)
			}
			notify := payload["payload"].(map[string]interface{})["notification"].(map[string]interface{})
			if got, ok := notify["foregroundShow"].(bool); !ok || got != tc.want {
				t.Errorf("foregroundShow=%d: expected %v, got %v (type %T)", tc.in, tc.want, notify["foregroundShow"], notify["foregroundShow"])
			}
		})
	}
}

// TestClient_Send_InboxContent verifies that when inboxContent is non-empty
// the V3 notification carries both the inboxContent array and style=3
// (Huawei's inbox display style). Per the V3 docs style MUST be 3 when
// inboxContent is present, otherwise the system silently falls back to the
// default single-line layout.
func TestClient_Send_InboxContent(t *testing.T) {
	var capturedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		capturedBody = buf
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	ts, _ := NewTokenSource()
	client := NewClientWithURL(ts, server.URL)
	client.httpCli = &http.Client{
		Transport: &rewriteTransport{target: server.URL},
	}

	lines := []string{"1. 通知栏消息样式", "2. 通知栏消息提醒方式和展示方式", "3. 通知栏消息语言本地化"}
	_, _, _ = client.Send([]string{"t1"}, "title", "body", "", "", 0, nil, "", 0, 1, lines, 0)

	var payload map[string]interface{}
	if err := json.Unmarshal(capturedBody, &payload); err != nil {
		t.Fatalf("failed to unmarshal request: %v", err)
	}
	notify := payload["payload"].(map[string]interface{})["notification"].(map[string]interface{})

	got, ok := notify["inboxContent"].([]interface{})
	if !ok {
		t.Fatalf("expected inboxContent array, got %T: %v", notify["inboxContent"], notify["inboxContent"])
	}
	if len(got) != len(lines) {
		t.Fatalf("expected %d lines, got %d", len(lines), len(got))
	}
	for i, want := range lines {
		if got[i] != want {
			t.Errorf("inboxContent[%d]=%v, want %v", i, got[i], want)
		}
	}
	if style, ok := notify["style"].(float64); !ok || int(style) != 3 {
		t.Errorf("expected style=3 when inboxContent present, got %v", notify["style"])
	}
}

// TestClient_Send_InboxContentOmitted verifies that without inboxContent
// neither inboxContent nor style is serialized — style=3 is only sent as
// a companion to inboxContent per the V3 docs.
func TestClient_Send_InboxContentOmitted(t *testing.T) {
	var capturedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		capturedBody = buf
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	ts, _ := NewTokenSource()
	client := NewClientWithURL(ts, server.URL)
	client.httpCli = &http.Client{
		Transport: &rewriteTransport{target: server.URL},
	}

	_, _, _ = client.Send([]string{"t1"}, "title", "body", "", "", 0, nil, "", 0, 1, nil, 0)

	var payload map[string]interface{}
	_ = json.Unmarshal(capturedBody, &payload)
	notify := payload["payload"].(map[string]interface{})["notification"].(map[string]interface{})

	if _, ok := notify["inboxContent"]; ok {
		t.Errorf("expected inboxContent omitted when nil, got %v", notify["inboxContent"])
	}
	if _, ok := notify["style"]; ok {
		t.Errorf("expected style omitted when inboxContent absent, got %v", notify["style"])
	}
}

// TestClient_Send_InboxContentSingleLine verifies a single-element array is
// still serialized as inboxContent + style=3 (not collapsed to a scalar).
func TestClient_Send_InboxContentSingleLine(t *testing.T) {
	var capturedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		capturedBody = buf
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	ts, _ := NewTokenSource()
	client := NewClientWithURL(ts, server.URL)
	client.httpCli = &http.Client{
		Transport: &rewriteTransport{target: server.URL},
	}

	_, _, _ = client.Send([]string{"t1"}, "title", "body", "", "", 0, nil, "", 0, 1, []string{"only line"}, 0)

	var payload map[string]interface{}
	_ = json.Unmarshal(capturedBody, &payload)
	notify := payload["payload"].(map[string]interface{})["notification"].(map[string]interface{})

	got, ok := notify["inboxContent"].([]interface{})
	if !ok || len(got) != 1 || got[0] != "only line" {
		t.Errorf("expected inboxContent=[\"only line\"], got %v", notify["inboxContent"])
	}
	if style, ok := notify["style"].(float64); !ok || int(style) != 3 {
		t.Errorf("expected style=3 for single-line inbox, got %v", notify["style"])
	}
}

// TestClient_Send_InboxContentEmptyArray verifies an empty (non-nil) array is
// treated as absent — neither inboxContent nor style is sent — so callers can
// safely pass an empty slice without triggering the inbox style.
func TestClient_Send_InboxContentEmptyArray(t *testing.T) {
	var capturedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		capturedBody = buf
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	ts, _ := NewTokenSource()
	client := NewClientWithURL(ts, server.URL)
	client.httpCli = &http.Client{
		Transport: &rewriteTransport{target: server.URL},
	}

	_, _, _ = client.Send([]string{"t1"}, "title", "body", "", "", 0, nil, "", 0, 1, []string{}, 0)

	var payload map[string]interface{}
	_ = json.Unmarshal(capturedBody, &payload)
	notify := payload["payload"].(map[string]interface{})["notification"].(map[string]interface{})

	if _, ok := notify["inboxContent"]; ok {
		t.Errorf("expected inboxContent omitted for empty array, got %v", notify["inboxContent"])
	}
	if _, ok := notify["style"]; ok {
		t.Errorf("expected style omitted for empty inbox, got %v", notify["style"])
	}
}

// TestClient_Send_NotifyId verifies that a non-zero notifyId is serialized
// as notification.notifyId in the V3 body — notifications sharing the same
// notifyId replace each other (Huawei's notification grouping mechanism).
func TestClient_Send_NotifyId(t *testing.T) {
	var capturedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		capturedBody = buf
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	ts, _ := NewTokenSource()
	client := NewClientWithURL(ts, server.URL)
	client.httpCli = &http.Client{
		Transport: &rewriteTransport{target: server.URL},
	}

	_, _, _ = client.Send([]string{"t1"}, "title", "body", "", "", 0, nil, "", 0, 1, nil, 12345)

	var payload map[string]interface{}
	if err := json.Unmarshal(capturedBody, &payload); err != nil {
		t.Fatalf("failed to unmarshal request: %v", err)
	}
	notify := payload["payload"].(map[string]interface{})["notification"].(map[string]interface{})

	if got, ok := notify["notifyId"].(float64); !ok || int(got) != 12345 {
		t.Errorf("expected notifyId=12345, got %v", notify["notifyId"])
	}
}

// TestClient_Send_NotifyIdOmitted verifies that notifyId=0 is omitted via
// omitempty so Push Kit auto-generates a unique identifier (the default
// behavior when the field is absent).
func TestClient_Send_NotifyIdOmitted(t *testing.T) {
	var capturedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		capturedBody = buf
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	ts, _ := NewTokenSource()
	client := NewClientWithURL(ts, server.URL)
	client.httpCli = &http.Client{
		Transport: &rewriteTransport{target: server.URL},
	}

	_, _, _ = client.Send([]string{"t1"}, "title", "body", "", "", 0, nil, "", 0, 1, nil, 0)

	var payload map[string]interface{}
	_ = json.Unmarshal(capturedBody, &payload)
	notify := payload["payload"].(map[string]interface{})["notification"].(map[string]interface{})

	if _, ok := notify["notifyId"]; ok {
		t.Errorf("expected notifyId omitted when 0, got %v", notify["notifyId"])
	}
}

// TestClient_Send_NotifyIdWithInbox verifies that notifyId and inboxContent
// coexist in the same notification — a non-zero notifyId does not interfere
// with the inbox style=3 pairing.
func TestClient_Send_NotifyIdWithInbox(t *testing.T) {
	var capturedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		capturedBody = buf
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	ts, _ := NewTokenSource()
	client := NewClientWithURL(ts, server.URL)
	client.httpCli = &http.Client{
		Transport: &rewriteTransport{target: server.URL},
	}

	lines := []string{"line1", "line2"}
	_, _, _ = client.Send([]string{"t1"}, "title", "body", "", "", 0, nil, "", 0, 1, lines, 99)

	var payload map[string]interface{}
	_ = json.Unmarshal(capturedBody, &payload)
	notify := payload["payload"].(map[string]interface{})["notification"].(map[string]interface{})

	if got, ok := notify["notifyId"].(float64); !ok || int(got) != 99 {
		t.Errorf("expected notifyId=99, got %v", notify["notifyId"])
	}
	if style, ok := notify["style"].(float64); !ok || int(style) != 3 {
		t.Errorf("expected style=3 with inboxContent, got %v", notify["style"])
	}
}
