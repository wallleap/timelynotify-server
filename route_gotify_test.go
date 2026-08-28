package main

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/wallleap/timelynotify-server/internal/gotifycompat"
)

// streamExportTestService swaps the global gotifyService for a fresh bbolt
// service and publishes a fixed corpus to device key "stream-dev": 5 messages,
// 2 of which mention "cpu". The original service is restored on cleanup.
func streamExportTestService(t *testing.T) *gotifycompat.Service {
	t.Helper()
	old := gotifyService
	svc, err := gotifycompat.Init(gotifycompat.Config{
		DataDir:     t.TempDir(),
		ClientToken: "stream-test-token",
	})
	if err != nil {
		t.Fatalf("gotifycompat.Init: %v", err)
	}
	gotifyService = svc
	t.Cleanup(func() { gotifyService = old })

	pub := func(title, body string) {
		t.Helper()
		if err := svc.Publish(title, body, 0, map[string]interface{}{"device_key": "stream-dev"}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}
	pub("Alert", "cpu high")
	pub("note", "hello")
	pub("alert two", "cpu again")
	pub("misc", "nothing")
	pub("misc", "quiet")
	return svc
}

func doStreamGet(t *testing.T, url string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	req.Host = "localhost"
	res, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, body
}

type streamPaging struct {
	Size  int    `json:"size"`
	Limit int    `json:"limit"`
	Since uint64 `json:"since"`
	Total int    `json:"total"`
	Error string `json:"error"`
}

type streamEnvelope struct {
	Messages []gotifycompat.Message `json:"messages"`
	Paging   streamPaging           `json:"paging"`
}

// TestExportStreamLimitNegative: ?limit=-1 streams the full device history as
// one valid JSON envelope — messages array plus trailing paging with size and
// total equal to the device's message count.
func TestExportStreamLimitNegative(t *testing.T) {
	streamExportTestService(t)

	code, body := doStreamGet(t, "/stream-dev/message?limit=-1&token=stream-test-token")
	if code != 200 {
		t.Fatalf("limit=-1 export should succeed, got %d", code)
	}
	var env streamEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("streamed body is not valid JSON: %v\nbody: %s", err, body)
	}
	if len(env.Messages) != 5 {
		t.Fatalf("want 5 streamed messages, got %d", len(env.Messages))
	}
	if env.Paging.Size != 5 || env.Paging.Total != 5 {
		t.Fatalf("paging size/total: want 5/5, got %d/%d", env.Paging.Size, env.Paging.Total)
	}
	if env.Paging.Limit != -1 {
		t.Fatalf("paging limit: want -1, got %d", env.Paging.Limit)
	}
	if env.Messages[0].ID != 5 {
		t.Fatalf("stream must be newest-first, first id = %d", env.Messages[0].ID)
	}
}

// TestExportStreamWithQuery: query + limit=-1 streams every keyword match and
// reports the match count in paging.
func TestExportStreamWithQuery(t *testing.T) {
	streamExportTestService(t)

	code, body := doStreamGet(t, "/stream-dev/message?query=cpu&limit=-1&token=stream-test-token")
	if code != 200 {
		t.Fatalf("query export should succeed, got %d", code)
	}
	var env streamEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("streamed body is not valid JSON: %v\nbody: %s", err, body)
	}
	if len(env.Messages) != 2 || env.Paging.Total != 2 {
		t.Fatalf("query export: want 2 messages total=2, got len=%d total=%d", len(env.Messages), env.Paging.Total)
	}
}

// TestExportStreamUnauthorized: the token check must happen before any
// streaming begins, so an anonymous caller gets a clean 401.
func TestExportStreamUnauthorized(t *testing.T) {
	streamExportTestService(t)

	code, body := doStreamGet(t, "/stream-dev/message?limit=-1&token=wrong")
	if code != 401 {
		t.Fatalf("want 401 before streaming, got %d", code)
	}
	if len(body) == 0 {
		t.Fatal("401 should carry an error body")
	}
}

// TestMessageQueryZeroLimit: limit=0 is the safe-failure value — an empty
// result set, never the full history (that is -1's job).
func TestMessageQueryZeroLimit(t *testing.T) {
	streamExportTestService(t)

	code, body := doStreamGet(t, "/stream-dev/message?limit=0&token=stream-test-token")
	if code != 200 {
		t.Fatalf("want 200, got %d", code)
	}
	var env streamEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(env.Messages) != 0 {
		t.Fatalf("limit=0 must return an empty set, got %d messages", len(env.Messages))
	}
}

// TestExportStreamIsolation: the streamed export only contains the path
// device's messages, never other devices'.
func TestExportStreamIsolation(t *testing.T) {
	svc := streamExportTestService(t)
	if err := svc.Publish("other", "msg", 0, map[string]interface{}{"device_key": "other-dev"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	_, body := doStreamGet(t, "/stream-dev/message?limit=-1&token=stream-test-token")
	var env streamEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	for _, m := range env.Messages {
		if m.SourceDevice() != "stream-dev" {
			t.Fatalf("leaked message from %q", m.SourceDevice())
		}
	}
}
