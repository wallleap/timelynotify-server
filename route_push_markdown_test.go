package main

import (
	"net/url"
	"testing"

	"github.com/wallleap/timelynotify-server/apns"
	"github.com/wallleap/timelynotify-server/internal/gotifycompat"
)

func TestFormMarkdownPreservesLineBreaks(t *testing.T) {
	markdown := "# Markdown 测试通知\n这是一条 **加粗** 测试。\n- 第一项"
	overridePushAPNs(t, func(msg *apns.PushMessage) (int, error) {
		if got := msg.ExtParams["markdown"]; got != markdown {
			t.Errorf("APNs markdown = %q, want %q", got, markdown)
		}
		return 200, nil
	})
	form := url.Values{"device_key": {key}, "platform": {"ios"}, "markdown": {markdown}}
	res := doPush(t, "POST", "/push", form.Encode(), false)
	if res.StatusCode != 200 {
		t.Fatalf("form push status = %d: %s", res.StatusCode, decodeBody(t, res))
	}
}

func TestMarkdownHarmonyPushAndHistory(t *testing.T) {
	old := gotifyService
	svc, err := gotifycompat.Init(gotifycompat.Config{DataDir: t.TempDir(), ClientToken: "markdown-test-token"})
	if err != nil {
		t.Fatal(err)
	}
	gotifyService = svc
	t.Cleanup(func() { gotifyService = old; _ = svc.Close() })
	registerHarmonyUnderTestKey(t, "harmony-markdown-token")

	var delivered string
	overridePushHarmony(t, func(_ []string, _, body, _, _ string, _ int, _ *int, _ string, _, _ int, _ []string, _ int) (int, int, error) {
		delivered = body
		return 200, 80000000, nil
	})
	res := doPush(t, "POST", "/push", `{"device_key":"`+key+`","platform":"harmony","title":"标题","markdown":"**新正文**"}`, true)
	if res.StatusCode != 200 {
		t.Fatalf("push status = %d: %s", res.StatusCode, decodeBody(t, res))
	}
	if delivered != "新正文" {
		t.Errorf("Huawei body = %q, want 新正文", delivered)
	}
	messages, err := svc.MessagesByDevice(key, 10, 0)
	if err != nil || len(messages) != 1 {
		t.Fatalf("history = %+v, err=%v", messages, err)
	}
	if messages[0].Message != "新正文" || messages[0].Extras["markdown"] != "**新正文**" {
		t.Errorf("history = %+v, want plain body and original markdown", messages[0])
	}
}

func TestReadableBodyFallback(t *testing.T) {
	tests := []struct {
		name, body string
		extras     map[string]interface{}
		want       string
	}{
		{"markdown overrides body", "旧正文", map[string]interface{}{"markdown": "**新正文**"}, "新正文"},
		{"no markdown", "旧正文", nil, "旧正文"},
		{"empty markdown result", "旧正文", map[string]interface{}{"markdown": "   "}, "旧正文"},
		{"encrypted content", "密文占位", map[string]interface{}{"markdown": "**不应展示**", "ciphertext": "secret"}, "密文占位"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := readableBody(&apns.PushMessage{Body: tt.body, ExtParams: tt.extras})
			if got != tt.want {
				t.Errorf("readableBody = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMarkdownPreservesIOSPayload(t *testing.T) {
	overridePushAPNs(t, func(msg *apns.PushMessage) (int, error) {
		if msg.Body != "旧正文" || msg.ExtParams["markdown"] != "**新正文**" {
			t.Errorf("APNs payload = %+v, want original body and markdown", msg)
		}
		return 200, nil
	})
	res := doPush(t, "POST", "/push", `{"device_key":"`+key+`","platform":"ios","body":"旧正文","markdown":"**新正文**"}`, true)
	if res.StatusCode != 200 {
		t.Fatalf("push status = %d: %s", res.StatusCode, decodeBody(t, res))
	}
}
