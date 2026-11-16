package main

import (
	"testing"

	"github.com/wallleap/timelynotify-server/apns"
	"github.com/wallleap/timelynotify-server/internal/gotifycompat"
)

// TestInboxContentFillsEmptyBody covers the inboxContent-only push: without an
// explicit body the server must derive the notification text from the inbox
// lines instead of falling back to "Empty Message", while still forwarding the
// original array to HarmonyOS.
func TestInboxContentFillsEmptyBody(t *testing.T) {
	old := gotifyService
	svc, err := gotifycompat.Init(gotifycompat.Config{DataDir: t.TempDir(), ClientToken: "inbox-test-token"})
	if err != nil {
		t.Fatal(err)
	}
	gotifyService = svc
	t.Cleanup(func() { gotifyService = old; _ = svc.Close() })
	registerHarmonyUnderTestKey(t, "harmony-inbox-token")

	var deliveredBody string
	var deliveredInbox []string
	overridePushHarmony(t, func(_ []string, _, body, _, _ string, _ int, _ *int, _ string, _, _ int, inbox []string, _ int) (int, int, error) {
		deliveredBody = body
		deliveredInbox = inbox
		return 200, 80000000, nil
	})

	res := doPush(t, "POST", "/push",
		`{"device_key":"`+key+`","platform":"harmony","title":"标题","inboxContent":["第一行","第二行"]}`, true)
	if res.StatusCode != 200 {
		t.Fatalf("push status = %d: %s", res.StatusCode, decodeBody(t, res))
	}
	if deliveredBody != "第一行\n第二行" {
		t.Errorf("Huawei body = %q, want joined inbox lines", deliveredBody)
	}
	if len(deliveredInbox) != 2 {
		t.Errorf("Huawei inboxContent = %v, want 2 lines", deliveredInbox)
	}

	messages, err := svc.MessagesByDevice(key, 10, 0)
	if err != nil || len(messages) != 1 {
		t.Fatalf("history = %+v, err=%v", messages, err)
	}
	if messages[0].Message != "第一行\n第二行" {
		t.Errorf("history body = %q, want joined inbox lines", messages[0].Message)
	}
	if messages[0].Extras["inboxcontent"] == nil {
		t.Errorf("history extras lost inboxcontent: %+v", messages[0].Extras)
	}
}

// TestInboxContentKeepsExplicitBody verifies an explicit body is never
// replaced by the inbox lines.
func TestInboxContentKeepsExplicitBody(t *testing.T) {
	registerHarmonyUnderTestKey(t, "harmony-inbox-body-token")
	var deliveredBody string
	overridePushHarmony(t, func(_ []string, _, body, _, _ string, _ int, _ *int, _ string, _, _ int, _ []string, _ int) (int, int, error) {
		deliveredBody = body
		return 200, 80000000, nil
	})

	res := doPush(t, "POST", "/push",
		`{"device_key":"`+key+`","platform":"harmony","body":"摘要","inboxContent":["第一行","第二行"]}`, true)
	if res.StatusCode != 200 {
		t.Fatalf("push status = %d: %s", res.StatusCode, decodeBody(t, res))
	}
	if deliveredBody != "摘要" {
		t.Errorf("Huawei body = %q, want explicit body untouched", deliveredBody)
	}
}

// TestInboxContentFillsEmptyIOSBody documents the cross-platform fallback:
// inboxContent is HarmonyOS-only, but an iOS target sharing the key still gets
// the joined lines as its alert body instead of "Empty Message".
func TestInboxContentFillsEmptyIOSBody(t *testing.T) {
	overridePushAPNs(t, func(msg *apns.PushMessage) (int, error) {
		if msg.Body != "第一行\n第二行" {
			t.Errorf("APNs body = %q, want joined inbox lines", msg.Body)
		}
		return 200, nil
	})

	res := doPush(t, "POST", "/push",
		`{"device_key":"`+key+`","platform":"ios","inboxContent":["第一行","第二行"]}`, true)
	if res.StatusCode != 200 {
		t.Fatalf("push status = %d: %s", res.StatusCode, decodeBody(t, res))
	}
}
