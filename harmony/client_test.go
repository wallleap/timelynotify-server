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
	status, hmsCode, err := client.Send(nil, "title", "body", "", 0)
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

	_, _, err := client.Send([]string{"token1"}, "Hello", "World", "", 0)
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
	if notify["category"] != "MARKETING" {
		t.Errorf("expected category='MARKETING', got %v", notify["category"])
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

	_, _, _ = client.Send([]string{"t1"}, "title", "body", `{"key":"value"}`, 0)

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

	_, _, _ = client.Send([]string{"t1"}, "title", "body", "plain-string", 0)

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
			json.NewEncoder(w).Encode(hmsResponse{Code: 80200003, Message: "access token expired"})
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

	status, hmsCode, err := client.Send([]string{"token1"}, "title", "body", "", 0)
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
		json.NewEncoder(w).Encode(hmsResponse{Code: 80200001, Message: "invalid token"})
	}))
	defer server.Close()

	ts, _ := NewTokenSource()
	client := NewClient(ts)
	projectID = "test-project-no-retry"
	client.httpCli = &http.Client{
		Transport: &rewriteTransport{target: server.URL},
	}

	_, _, _ = client.Send([]string{"bad_token"}, "title", "body", "", 0)

	if count := atomic.LoadInt32(&callCount); count != 1 {
		t.Errorf("expected only 1 call for non-retryable error, got %d", count)
	}
}

// rewriteTransport is an http.RoundTripper that redirects a specific
// Huawei API URL to a local test server.
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
		json.NewEncoder(w).Encode(hmsResponse{Code: 80000000, Message: "Success"})
	}))
	defer server.Close()

	ts, _ := NewTokenSource()
	client := NewClientWithURL(ts, server.URL)
	projectID = "test-project-success"
	client.httpCli = &http.Client{
		Transport: &rewriteTransport{target: server.URL},
	}

	status, hmsCode, err := client.Send([]string{"token1"}, "Hello", "World", "", 0)
	if err != nil {
		t.Fatalf("Send should succeed with code 80000000, got error: %v", err)
	}
	if status != http.StatusOK {
		t.Errorf("expected status 200, got %d", status)
	}
	if hmsCode != 0 {
		t.Errorf("expected hmsCode 0 for success, got %d", hmsCode)
	}
	if count := atomic.LoadInt32(&callCount); count != 1 {
		t.Errorf("expected 1 call, got %d", count)
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

	status, _, err := client.Send([]string{"token1"}, "Hello", "World", "", 0)
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

	_, _, err := client.Send([]string{"token1"}, "Hello", "World", "", 0)
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
		json.NewEncoder(w).Encode(hmsResponse{Code: 80200001, Message: "invalid token"})
	}))
	defer server.Close()

	ts, _ := NewTokenSource()
	client := NewClientWithURL(ts, server.URL)
	projectID = "test-project-invalid"
	client.httpCli = &http.Client{
		Transport: &rewriteTransport{target: server.URL},
	}

	_, hmsCode, err := client.Send([]string{"bad_token"}, "Hello", "World", "", 0)
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

	client.Send([]string{"token1"}, "Hello", "World", "", 1)

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

	_, _, _ = client.Send([]string{"token1"}, "Hello", "World", "", 0)

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
