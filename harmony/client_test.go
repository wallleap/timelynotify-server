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
	status, hmsCode, err := client.Send(nil, "title", "body", "", "")
	if err == nil {
		t.Fatal("expected error for empty tokens")
	}
	if status != 0 || hmsCode != 0 {
		t.Errorf("expected zero codes, got status=%d hmsCode=%d", status, hmsCode)
	}
}

// TestClient_Send_JSONPayload verifies the exact JSON structure sent to
// the Huawei API via a local httptest server.
func TestClient_Send_JSONPayload(t *testing.T) {
	var capturedBody []byte
	var capturedAuth string
	
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		// We read the body
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		capturedBody = buf
		
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	// Build a client that points to our test server. We can't easily
	// override the URL, so we verify the payload construction logic
	// independently (it's pure). The actual HTTP path is verified in the
	// retry / integration test.
	// Instead, we construct a client using a custom URL by replacing the
	// constant at runtime (possible because we use fmt.Sprintf).
	// For this specific test we just verify payload building and
	// authorization header construction via a helper.
	
	// We can't easily override the URL constant since it's in the same file.
	// Let's instead use the raw doSend function with a modified transport
	// that redirects the URL. But since doSend is unexported, we need
	// to test via the public Send method.
	// 
	// A simpler approach: we create a Client with an httptest server
	// using a custom RoundTripper.
	
	ts, _ := NewTokenSource()
	client := NewClient(ts)
	
	// Override the projectID to make the URL predictable
	projectID = "test-project"
	
	// Create a custom HTTP client that redirects our API URL to the test server
	client.httpCli = &http.Client{
		Transport: &rewriteTransport{target: server.URL},
	}
	
	_, _, err := client.Send([]string{"token1"}, "Hello", "World", "data", "launch")
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	
	if !strings.HasPrefix(capturedAuth, "Bearer ") {
		t.Errorf("expected Authorization header to start with 'Bearer ', got %q", capturedAuth)
	}
	
	var payload map[string]interface{}
	if err := json.Unmarshal(capturedBody, &payload); err != nil {
		t.Fatalf("failed to unmarshal request body: %v", err)
	}
	
	// Verify structure
	msg, ok := payload["message"].(map[string]interface{})
	if !ok {
		t.Fatal("expected 'message' field in payload")
	}
	
	tokens, ok := msg["token"].([]interface{})
	if !ok || len(tokens) != 1 || tokens[0].(string) != "token1" {
		t.Errorf("expected token list [\"token1\"], got %v", msg["token"])
	}
	
	notify, ok := msg["notification"].(map[string]interface{})
	if !ok {
		t.Fatal("expected 'notification' field in message")
	}
	if notify["title"] != "Hello" {
		t.Errorf("expected title='Hello', got %v", notify["title"])
	}
	if notify["body"] != "World" {
		t.Errorf("expected body='World', got %v", notify["body"])
	}
	if notify["click_action"] != "launch" {
		t.Errorf("expected click_action='launch', got %v", notify["click_action"])
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
			// First call: return token expired
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(hmsResponse{Code: 80200003, Message: "access token expired"})
		} else {
			// Second call: return success
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

	status, hmsCode, err := client.Send([]string{"token1"}, "title", "body", "", "")
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
		// Return a permanent error (bad token)
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

	_, _, _ = client.Send([]string{"bad_token"}, "title", "body", "", "")
	
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
	// Modify the request URL to point to the test server
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
		// Return Huawei's success code 80000000
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

	status, hmsCode, err := client.Send([]string{"token1"}, "Hello", "World", "", "")
	if err != nil {
		t.Fatalf("Send should succeed with code 80000000, got error: %v", err)
	}
	if status != http.StatusOK {
		t.Errorf("expected status 200, got %d", status)
	}
	// hmsCode should be 0 because 80000000 is normalized to success
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

	status, _, err := client.Send([]string{"token1"}, "Hello", "World", "", "")
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
	// Point to a non-existent server
	ts, _ := NewTokenSource()
	client := NewClientWithURL(ts, "http://localhost:19999")

	_, _, err := client.Send([]string{"token1"}, "Hello", "World", "", "")
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

	_, hmsCode, err := client.Send([]string{"bad_token"}, "Hello", "World", "", "")
	if err == nil {
		t.Fatal("expected error for invalid token, got nil")
	}
	if hmsCode != 80200001 {
		t.Errorf("expected hmsCode 80200001, got %d", hmsCode)
	}
	// Should NOT retry because invalid token is not a retryable error
	if count := atomic.LoadInt32(&callCount); count != 1 {
		t.Errorf("expected only 1 call (no retry), got %d", count)
	}
}

// TestClient_Send_DefaultClickAction verifies that when no level is
// specified, the click_action parameter is correctly forwarded to the API.
func TestClient_Send_DefaultClickAction(t *testing.T) {
	var capturedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		r.Body.Read(buf)
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

	client.Send([]string{"token1"}, "Hello", "World", "", "page")

	var payload map[string]interface{}
	if err := json.Unmarshal(capturedBody, &payload); err != nil {
		t.Fatalf("failed to unmarshal request: %v", err)
	}

	msg := payload["message"].(map[string]interface{})
	notify := msg["notification"].(map[string]interface{})

	if notify["click_action"] != "page" {
		t.Errorf("expected click_action='page', got %q", notify["click_action"])
	}
}

// TestNewClientWithURL verifies that NewClientWithURL correctly sets the
// base URL and that NewClient uses the default URL.
func TestNewClientWithURL(t *testing.T) {
	ts, _ := NewTokenSource()

	// Test NewClient (no custom URL)
	client1 := NewClient(ts)
	if client1.baseURL != "" {
		t.Errorf("expected empty baseURL for NewClient, got %q", client1.baseURL)
	}

	// Test NewClientWithURL
	client2 := NewClientWithURL(ts, "http://localhost:9999")
	if client2.baseURL != "http://localhost:9999" {
		t.Errorf("expected baseURL='http://localhost:9999', got %q", client2.baseURL)
	}
}
