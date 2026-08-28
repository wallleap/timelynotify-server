package gotifycompat

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// buildTestService creates a bbolt-backed service in a temp dir.
func buildTestService(t *testing.T, tokenOverride string) *Service {
	t.Helper()
	dir := t.TempDir()
	svc, err := Init(Config{
		DataDir:     dir,
		ClientToken: tokenOverride,
		Version:     "1.2.3",
	})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	return svc
}

func msgOf(title, body string, priority int) Message {
	return Message{
		AppID:    1,
		Title:    title,
		Message:  body,
		Priority: priority,
		Extras:   map[string]interface{}{"device_key": "k1"},
		Date:     time.Now().Format(time.RFC3339Nano),
	}
}

func TestInitGeneratesAndPersistsToken(t *testing.T) {
	svc := buildTestService(t, "")
	if svc.ClientToken() == "" {
		t.Fatal("expected a generated token")
	}
	if !svc.ValidateToken(svc.ClientToken()) {
		t.Fatal("generated token must validate")
	}
	if svc.ValidateToken("wrong-token") {
		t.Fatal("wrong token must not validate")
	}
	if svc.TokenSource() != TokenSourceGenerated {
		t.Fatalf("want TokenSourceGenerated, got %v", svc.TokenSource())
	}
}

// TestInitWithUnusableDataDirFallsBackToMemory guards the "first boot logs
// nothing" symptom: when the data dir cannot be used, Init must still succeed
// on the in-memory store, generate a token and keep the interface usable.
func TestInitWithUnusableDataDirFallsBackToMemory(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	// MkdirAll fails because a regular file sits where a dir would be created.
	svc, err := Init(Config{DataDir: filepath.Join(blocker, "sub"), ClientToken: ""})
	if err != nil {
		t.Fatalf("Init must not fail when the store is unusable: %v", err)
	}
	if svc.ClientToken() == "" {
		t.Fatal("expected a generated token even on memory fallback")
	}
	if !svc.ValidateToken(svc.ClientToken()) {
		t.Fatal("generated token must validate")
	}
	if svc.TokenSource() != TokenSourceGenerated {
		t.Fatalf("want TokenSourceGenerated, got %v", svc.TokenSource())
	}
	if err := svc.Publish("t", "b", 0, nil); err != nil {
		t.Fatalf("Publish on memory store: %v", err)
	}
}

func TestTokenSourceAfterReopen(t *testing.T) {
	dir := t.TempDir()
	svc1, err := Init(Config{DataDir: dir, ClientToken: ""})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if svc1.TokenSource() != TokenSourceGenerated {
		t.Fatalf("want Generated on first boot, got %v", svc1.TokenSource())
	}
	if err := svc1.store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	svc2, err := Init(Config{DataDir: dir, ClientToken: ""})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if svc2.TokenSource() != TokenSourcePersisted {
		t.Fatalf("want Persisted after reopen, got %v", svc2.TokenSource())
	}
	if svc2.ClientToken() != "" {
		t.Fatal("persisted token must not be re-logged on restart")
	}
}

func TestTokenSourceOperator(t *testing.T) {
	svc := buildTestService(t, "secret")
	if svc.TokenSource() != TokenSourceOperator {
		t.Fatalf("want TokenSourceOperator, got %v", svc.TokenSource())
	}
}

func TestPublishAndMessagesOrdering(t *testing.T) {
	svc := buildTestService(t, "")
	for i := 0; i < 5; i++ {
		if err := svc.Publish("t", "body", 0, map[string]interface{}{"n": i}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}
	msgs, err := svc.MessagesByDevice("", 100, 0)
	if err != nil {
		t.Fatalf("MessagesByDevice: %v", err)
	}
	if len(msgs) != 5 {
		t.Fatalf("want 5 messages, got %d", len(msgs))
	}
	// newest first
	for i := 0; i < len(msgs)-1; i++ {
		if msgs[i].ID <= msgs[i+1].ID {
			t.Fatalf("messages not ordered desc: %d then %d", msgs[i].ID, msgs[i+1].ID)
		}
	}
	// monotonic ids
	for i, m := range msgs {
		if m.ID != uint64(5-i) {
			t.Fatalf("msg[%d].ID = %d, want %d", i, m.ID, 5-i)
		}
	}
}

func TestMessagesSinceFilter(t *testing.T) {
	svc := buildTestService(t, "")
	for i := 0; i < 10; i++ {
		if err := svc.Publish("t", "body", 0, nil); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}
	// since=7 → only ids 1..6
	msgs, err := svc.MessagesByDevice("", 100, 7)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if len(msgs) != 6 {
		t.Fatalf("want 6 messages with since=7, got %d", len(msgs))
	}
	if msgs[0].ID != 6 || msgs[5].ID != 1 {
		t.Fatalf("unexpected range: got %d..%d", msgs[0].ID, msgs[5].ID)
	}
}

func TestMessagesLimit(t *testing.T) {
	svc := buildTestService(t, "")
	for i := 0; i < 10; i++ {
		_ = svc.Publish("t", "body", 0, nil)
	}
	msgs, _ := svc.MessagesByDevice("", 3, 0)
	if len(msgs) != 3 {
		t.Fatalf("want 3 messages, got %d", len(msgs))
	}
	msgs, _ = svc.MessagesByDevice("", 0, 0)
	if len(msgs) != 0 {
		t.Fatalf("want 0 messages for limit=0, got %d", len(msgs))
	}
}

func TestPruningRetainsNewest(t *testing.T) {
	svc, err := Init(Config{DataDir: t.TempDir(), ClientToken: "", MaxMessages: 3})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	for i := 0; i < 10; i++ {
		_ = svc.Publish("t", "body", 0, nil)
	}
	msgs, _ := svc.MessagesByDevice("", 100, 0)
	if len(msgs) != 3 {
		t.Fatalf("want 3 retained, got %d", len(msgs))
	}
	if msgs[0].ID != 10 || msgs[2].ID != 8 {
		t.Fatalf("unexpected retained ids: %d,%d", msgs[0].ID, msgs[2].ID)
	}
}

func TestPersistenceAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	svc1, err := Init(Config{DataDir: dir, ClientToken: ""})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	tok := svc1.ClientToken()
	for i := 0; i < 3; i++ {
		_ = svc1.Publish("t", "body", 0, nil)
	}
	if err := svc1.store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	svc2, err := Init(Config{DataDir: dir, ClientToken: ""})
	if err != nil {
		t.Fatalf("reopen Init: %v", err)
	}
	// token stable, not re-generated
	if svc2.ClientToken() != "" {
		t.Fatal("token should not be re-generated/re-logged on restart")
	}
	if !svc2.ValidateToken(tok) {
		t.Fatal("persisted token must still validate after reopen")
	}
	msgs, _ := svc2.MessagesByDevice("", 100, 0)
	if len(msgs) != 3 {
		t.Fatalf("want 3 persisted messages, got %d", len(msgs))
	}
	if msgs[0].ID != 3 {
		t.Fatalf("id should continue monotonically, got %d", msgs[0].ID)
	}
	// ids keep increasing after reopen
	_ = svc2.Publish("t", "body", 0, nil)
	msgs, _ = svc2.MessagesByDevice("", 1, 0)
	if msgs[0].ID != 4 {
		t.Fatalf("id after reopen should be 4, got %d", msgs[0].ID)
	}
}

func TestTokenOverridePersistsHash(t *testing.T) {
	dir := t.TempDir()
	svc, err := Init(Config{DataDir: dir, ClientToken: "secret"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if !svc.ValidateToken("secret") {
		t.Fatal("override token must validate")
	}
	if svc.ClientToken() != "" {
		t.Fatal("override token must not be logged")
	}
	// raw plaintext must NOT be in the store
	raw, _ := svc.store.AutoToken()
	if raw != "" {
		t.Fatal("operator token must not be stored in plaintext")
	}

	// reopen: still validates
	svc.store.(*bboltStore).Close()
	svc2, err := Init(Config{DataDir: dir, ClientToken: ""})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if !svc2.ValidateToken("secret") {
		t.Fatal("hash must persist across restart")
	}
}

// TestOperatorOverrideClearsStaleAutoToken verifies that switching to an
// operator-supplied token wipes the plaintext of a previously auto-generated
// one from the store.
func TestOperatorOverrideClearsStaleAutoToken(t *testing.T) {
	dir := t.TempDir()
	svc1, err := Init(Config{DataDir: dir, ClientToken: ""})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if raw, _ := svc1.store.AutoToken(); raw == "" {
		t.Fatal("expected persisted plaintext autoToken after first boot")
	}
	if err := svc1.store.(*bboltStore).Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	svc2, err := Init(Config{DataDir: dir, ClientToken: "secret"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if raw, _ := svc2.store.AutoToken(); raw != "" {
		t.Fatalf("stale autoToken not cleared: %q", raw)
	}
	if !svc2.ValidateToken("secret") {
		t.Fatal("operator token must validate")
	}
}

func TestHubFanout(t *testing.T) {
	svc := buildTestService(t, "")
	ch, unsubscribe := svc.SubscribeByDevice("")
	defer unsubscribe()

	_ = svc.Publish("t", "body", 1, map[string]interface{}{"a": 1})
	select {
	case m := <-ch:
		if m.Title != "t" || m.Priority != 1 {
			t.Fatalf("unexpected message: %+v", m)
		}
		if _, ok := m.Extras["a"]; !ok {
			t.Fatal("extras lost")
		}
	case <-time.After(time.Second):
		t.Fatal("no broadcast received")
	}

	unsubscribe()
	_ = svc.Publish("t2", "body", 0, nil)
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("should not receive after unsubscribe")
		}
	case <-time.After(50 * time.Millisecond):
		t.Fatal("expected closed channel after unsubscribe")
	}
}

func TestSubscriberCount(t *testing.T) {
	svc := buildTestService(t, "")
	if n := svc.SubscriberCount(); n != 0 {
		t.Fatalf("want 0 subscribers, got %d", n)
	}
	ch1, un1 := svc.SubscribeByDevice("")
	defer un1()
	ch2, un2 := svc.SubscribeByDevice("")
	defer un2()
	if n := svc.SubscriberCount(); n != 2 {
		t.Fatalf("want 2 subscribers, got %d", n)
	}
	un1()
	if n := svc.SubscriberCount(); n != 1 {
		t.Fatalf("want 1 subscriber after unsub, got %d", n)
	}
	_ = ch1
	un2()
	if n := svc.SubscriberCount(); n != 0 {
		t.Fatalf("want 0 subscribers after all unsub, got %d", n)
	}
	_ = ch2
}

func TestMessageJSONWireShape(t *testing.T) {
	svc := buildTestService(t, "")
	_ = svc.Publish("title", "body", 2, map[string]interface{}{"device_key": "k", "subtitle": "sub"})
	msgs, _ := svc.MessagesByDevice("", 1, 0)
	b, err := json.Marshal(msgs[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, field := range []string{"id", "appid", "title", "message", "priority", "extras", "date"} {
		if _, ok := got[field]; !ok {
			t.Fatalf("missing wire field %q in %s", field, b)
		}
	}
	// date is a string (RFC3339Nano), not a Go time.Time re-marshal
	var date string
	if err := json.Unmarshal(got["date"], &date); err != nil {
		t.Fatalf("date must be a string, got %s", got["date"])
	}
	if _, err := time.Parse(time.RFC3339Nano, date); err != nil {
		t.Fatalf("date not RFC3339Nano: %v", err)
	}
	// extras always an object
	if string(got["extras"]) != "{}" && !reflect.DeepEqual(got["extras"], json.RawMessage(`{"device_key":"k","subtitle":"sub"}`)) {
		t.Fatalf("unexpected extras: %s", got["extras"])
	}
}

// buildMemoryFallbackService returns a Service backed by the degraded
// in-memory store (unusable data dir).
func buildMemoryFallbackService(t *testing.T) *Service {
	t.Helper()
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	svc, err := Init(Config{DataDir: filepath.Join(blocker, "sub"), ClientToken: ""})
	if err != nil {
		t.Fatalf("Init on unusable dir: %v", err)
	}
	return svc
}

func publishN(t *testing.T, svc *Service, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := svc.Publish("t", "b", 0, nil); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}
}

// TestDeleteMessage covers single-message deletion: existence result, what
// remains, and that the ID sequence keeps increasing afterwards.
func TestDeleteMessage(t *testing.T) {
	svc := buildTestService(t, "")
	publishN(t, svc, 3)

	ok, err := svc.DeleteMessageByDevice("", 2)
	if err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	if !ok {
		t.Fatal("message 2 should exist")
	}

	msgs, _ := svc.MessagesByDevice("", 100, 0)
	if len(msgs) != 2 {
		t.Fatalf("want 2 messages after delete, got %d", len(msgs))
	}
	if msgs[0].ID != 3 || msgs[1].ID != 1 {
		t.Fatalf("unexpected remaining messages: %+v", msgs)
	}

	ok, err = svc.DeleteMessageByDevice("", 99)
	if err != nil {
		t.Fatalf("DeleteMessage(99): %v", err)
	}
	if ok {
		t.Fatal("message 99 must not exist")
	}

	// sequence continues after delete (bbolt NextSequence never goes back)
	if err := svc.Publish("t", "b", 0, nil); err != nil {
		t.Fatalf("Publish after delete: %v", err)
	}
	msgs, _ = svc.MessagesByDevice("", 1, 0)
	if msgs[0].ID != 4 {
		t.Fatalf("id after delete should be 4, got %d", msgs[0].ID)
	}
}

// TestDeleteAllMessages covers wiping the whole history while keeping the ID
// sequence monotonic.
func TestDeleteAllMessages(t *testing.T) {
	svc := buildTestService(t, "")
	publishN(t, svc, 3)

	if err := svc.DeleteAllMessagesByDevice(""); err != nil {
		t.Fatalf("DeleteAllMessages: %v", err)
	}
	msgs, _ := svc.MessagesByDevice("", 100, 0)
	if len(msgs) != 0 {
		t.Fatalf("want 0 messages after DeleteAll, got %d", len(msgs))
	}

	if err := svc.Publish("t", "b", 0, nil); err != nil {
		t.Fatalf("Publish after DeleteAll: %v", err)
	}
	msgs, _ = svc.MessagesByDevice("", 1, 0)
	if msgs[0].ID != 4 {
		t.Fatalf("id after DeleteAll should continue from 4, got %d", msgs[0].ID)
	}
}

// msgFor publishes a message tagged to a specific device key.
func publishDevice(t *testing.T, svc *Service, device string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := svc.Publish("t", "b", 0, map[string]interface{}{"device_key": device}); err != nil {
			t.Fatalf("Publish(%s): %v", device, err)
		}
	}
}

// TestStoreRecentByDevice verifies device-scoped reads only surface that
// device's messages (bbolt + memory), and deviceKey == "" returns everything.
func TestStoreRecentByDevice(t *testing.T) {
	svc := buildTestService(t, "")
	publishDevice(t, svc, "k1", 3)
	publishDevice(t, svc, "k2", 2)

	// device-scoped read only returns that device's messages, newest first.
	for _, device := range []string{"k1", "k2"} {
		msgs, err := svc.MessagesByDevice(device, 100, 0)
		if err != nil {
			t.Fatalf("MessagesByDevice(%s): %v", device, err)
		}
		want := 3
		if device == "k2" {
			want = 2
		}
		if len(msgs) != want {
			t.Fatalf("MessagesByDevice(%s) want %d, got %d", device, want, len(msgs))
		}
		for _, m := range msgs {
			if m.SourceDevice() != device {
				t.Fatalf("got a message from device %q, want %q", m.SourceDevice(), device)
			}
		}
	}

	// "" returns everything
	all, err := svc.MessagesByDevice("", 100, 0)
	if err != nil {
		t.Fatalf("MessagesByDevice(all): %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("MessagesByDevice(all) want 5, got %d", len(all))
	}

	// since filter within a device
	msgs, _ := svc.MessagesByDevice("k1", 100, 3)
	if len(msgs) != 2 { // ids 1,2 remain (< since=3)
		t.Fatalf("MessagesByDevice(k1, since=3) want 2, got %d", len(msgs))
	}
}

// TestMessagesByDeviceLimitWithinDevice: the limit applies inside a device's
// own history, independently of other devices' messages.
func TestMessagesByDeviceLimitWithinDevice(t *testing.T) {
	svc := buildTestService(t, "")
	publishDevice(t, svc, "k1", 5) // ids 1..5
	publishDevice(t, svc, "k2", 2) // ids 6,7 (interleaved, newer)

	// limit=3 must return the 3 newest k1 messages (ids 5,4,3), newest first.
	// k2's rows sit at newer ids but the device filter must skip them and keep
	// walking back to gather k1's own three newest.
	msgs, err := svc.MessagesByDevice("k1", 3, 0)
	if err != nil {
		t.Fatalf("MessagesByDevice(k1, 3): %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("device-scoped limit: want 3, got %d", len(msgs))
	}
	want := []uint64{5, 4, 3}
	for i, m := range msgs {
		if m.ID != want[i] {
			t.Fatalf("msgs[%d].ID = %d, want %d", i, m.ID, want[i])
		}
		if m.SourceDevice() != "k1" {
			t.Fatalf("msgs[%d] belongs to %q, want k1", i, m.SourceDevice())
		}
	}
}

// TestMessagesByDeviceNewestFirstWithinDevice: device-scoped reads must order
// that device's own messages newest-first, skipping newer rows of other
// devices in the middle.
func TestMessagesByDeviceNewestFirstWithinDevice(t *testing.T) {
	svc := buildTestService(t, "")
	publishDevice(t, svc, "k1", 2) // ids 1,2
	publishDevice(t, svc, "k2", 2) // ids 3,4
	publishDevice(t, svc, "k1", 2) // ids 5,6

	msgs, err := svc.MessagesByDevice("k1", 100, 0)
	if err != nil {
		t.Fatalf("MessagesByDevice(k1): %v", err)
	}
	if len(msgs) != 4 {
		t.Fatalf("want 4 k1 messages, got %d", len(msgs))
	}
	// ids: 6,5,2,1 (newest first, k2's 3,4 skipped in the middle)
	want := []uint64{6, 5, 2, 1}
	for i, m := range msgs {
		if m.ID != want[i] {
			t.Fatalf("msgs[%d].ID = %d, want %d", i, m.ID, want[i])
		}
	}
}

// TestMessagesUnlimitedLimit: limit<0 means "no cap" (the export use case) —
// every stored message of the device is returned, newest first, on both
// stores. limit==0 keeps returning an empty list.
func TestMessagesUnlimitedLimit(t *testing.T) {
	svc := buildTestService(t, "")
	publishDevice(t, svc, "k1", 3)
	publishDevice(t, svc, "k2", 2)

	msgs, err := svc.MessagesByDevice("k1", -1, 0)
	if err != nil {
		t.Fatalf("MessagesByDevice(k1, -1): %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("bbolt limit=-1: want all 3 k1 messages, got %d", len(msgs))
	}
	all, _ := svc.MessagesByDevice("", -1, 0)
	if len(all) != 5 {
		t.Fatalf("bbolt limit=-1 (all devices): want 5, got %d", len(all))
	}

	mem := buildMemoryFallbackService(t)
	publishDevice(t, mem, "d1", 3)
	memMsgs, err := mem.MessagesByDevice("d1", -1, 0)
	if err != nil {
		t.Fatalf("memory MessagesByDevice(d1, -1): %v", err)
	}
	if len(memMsgs) != 3 {
		t.Fatalf("memory limit=-1: want all 3, got %d", len(memMsgs))
	}
	empty, _ := svc.MessagesByDevice("k1", 0, 0)
	if len(empty) != 0 {
		t.Fatalf("limit=0 must stay empty, got %d", len(empty))
	}
}

// TestSearchMessagesByDevice: keyword search is a case-insensitive substring
// match over title+body, scoped to one device, with `total` counting every
// match of the device regardless of limit. Both stores must behave the same.
func TestSearchMessagesByDevice(t *testing.T) {
	svc := buildTestService(t, "")
	pub := func(device, title, body string) {
		t.Helper()
		if err := svc.Publish(title, body, 0, map[string]interface{}{"device_key": device}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}
	pub("k1", "Alert", "disk almost full")
	pub("k1", "提醒", "服务器 CPU 高")
	pub("k1", "alert banner", "nothing to see")
	pub("k2", "ALERT", "other device")

	// Case-insensitive title match, device-scoped (k2 excluded).
	msgs, total, err := svc.SearchMessagesByDevice("k1", "alert", -1, 0)
	if err != nil {
		t.Fatalf("SearchMessagesByDevice(alert): %v", err)
	}
	if total != 2 || len(msgs) != 2 {
		t.Fatalf("title match: want 2/2, got total=%d len=%d", total, len(msgs))
	}

	// Body match with non-ASCII text.
	msgs, total, err = svc.SearchMessagesByDevice("k1", "cpu", -1, 0)
	if err != nil {
		t.Fatalf("SearchMessagesByDevice(cpu): %v", err)
	}
	if total != 1 || len(msgs) != 1 || msgs[0].Title != "提醒" {
		t.Fatalf("body match: want the CPU message, got total=%d len=%d msgs=%v", total, len(msgs), msgs)
	}

	// limit caps the list but not total.
	pub("k1", "m", "match")
	pub("k1", "m", "match")
	msgs, total, err = svc.SearchMessagesByDevice("k1", "match", 2, 0)
	if err != nil {
		t.Fatalf("SearchMessagesByDevice(match, limit=2): %v", err)
	}
	if len(msgs) != 2 || total != 2 {
		t.Fatalf("limited search: want len=2 total=2, got len=%d total=%d", len(msgs), total)
	}
	pub("k1", "m", "match")
	msgs, total, err = svc.SearchMessagesByDevice("k1", "match", 2, 0)
	if err != nil {
		t.Fatalf("SearchMessagesByDevice(match, limit=2): %v", err)
	}
	if len(msgs) != 2 || total != 3 {
		t.Fatalf("limited search: want len=2 total=3, got len=%d total=%d", len(msgs), total)
	}

	// since excludes newer ids from both list and total (ID < since).
	since := msgs[0].ID
	_, total, err = svc.SearchMessagesByDevice("k1", "match", -1, since)
	if err != nil {
		t.Fatalf("SearchMessagesByDevice(since): %v", err)
	}
	if total != 2 {
		t.Fatalf("search with since: want total=2, got %d", total)
	}

	// No match → zero, empty, no error.
	msgs, total, err = svc.SearchMessagesByDevice("k1", "no-such-keyword", -1, 0)
	if err != nil || len(msgs) != 0 || total != 0 {
		t.Fatalf("no match: want 0/0, got len=%d total=%d err=%v", len(msgs), total, err)
	}

	// Memory store parity.
	mem := buildMemoryFallbackService(t)
	if err := mem.Publish("Alert", "x", 0, map[string]interface{}{"device_key": "d1"}); err != nil {
		t.Fatalf("mem Publish: %v", err)
	}
	if err := mem.Publish("other", "y", 0, map[string]interface{}{"device_key": "d1"}); err != nil {
		t.Fatalf("mem Publish: %v", err)
	}
	memMsgs, memTotal, err := mem.SearchMessagesByDevice("d1", "ALERT", -1, 0)
	if err != nil {
		t.Fatalf("memory SearchMessagesByDevice: %v", err)
	}
	if memTotal != 1 || len(memMsgs) != 1 || memMsgs[0].Title != "Alert" {
		t.Fatalf("memory search: want 1 match, got total=%d len=%d", memTotal, len(memMsgs))
	}
}

// TestForEachByDevice: the streaming walk visits a device's matching messages
// newest-first exactly once per match (device + keyword + since filtered),
// reports the total match count independently of callback aborts, and
// propagates a callback error to stop the walk. Both stores must behave the
// same.
func TestForEachByDevice(t *testing.T) {
	pub := func(svc *Service, device, title, body string) {
		t.Helper()
		if err := svc.Publish(title, body, 0, map[string]interface{}{"device_key": device}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}
	for _, svc := range []*Service{buildTestService(t, ""), buildMemoryFallbackService(t)} {
		pub(svc, "k1", "Alert", "disk full")
		pub(svc, "k1", "note", "hello")
		pub(svc, "k1", "alert two", "again")
		pub(svc, "k2", "ALERT", "other device")

		// Full walk: k1 newest-first.
		var ids []uint64
		total, err := svc.ForEachMessageByDevice("k1", "", 0, func(m Message) error {
			ids = append(ids, m.ID)
			return nil
		})
		if err != nil {
			t.Fatalf("ForEachMessageByDevice: %v", err)
		}
		if total != 3 || len(ids) != 3 {
			t.Fatalf("full walk: want total=3 len=3, got total=%d len=%d", total, len(ids))
		}
		if ids[0] != 3 || ids[1] != 2 || ids[2] != 1 {
			t.Fatalf("walk not newest-first within device: %v", ids)
		}

		// Keyword + since filtering with total.
		total, err = svc.ForEachMessageByDevice("k1", "alert", 3, func(Message) error { return nil })
		if err != nil {
			t.Fatalf("ForEachMessageByDevice(alert, since=3): %v", err)
		}
		if total != 1 { // id 3 excluded by since, k2's ALERT excluded by device
			t.Fatalf("filtered walk: want total=1, got %d", total)
		}

		// Callback abort stops the walk and propagates the error.
		sentinel := errors.New("stop")
		count := 0
		total, err = svc.ForEachMessageByDevice("k1", "", 0, func(Message) error {
			count++
			return sentinel
		})
		if !errors.Is(err, sentinel) {
			t.Fatalf("callback error not propagated: %v", err)
		}
		if count != 1 || total != 1 {
			t.Fatalf("abort: want count=total=1, got count=%d total=%d", count, total)
		}
	}
}

// TestSourceDeviceFieldTakesPrecedence: SourceDevice must read the stored
// DeviceKey field first and only fall back to extras when that field is empty
// (guards the bbolt path, where DeviceKey is dropped by json:"-" and data comes
// back through the extras fallback).
func TestSourceDeviceFieldPrecedesExtras(t *testing.T) {
	m := Message{
		DeviceKey: "field",
		Extras:    map[string]interface{}{"device_key": "extra"},
	}
	if got := m.SourceDevice(); got != "field" {
		t.Fatalf("SourceDevice with populated field = %q, want %q", got, "field")
	}

	m = Message{
		DeviceKey: "",
		Extras:    map[string]interface{}{"device_key": "extra"},
	}
	if got := m.SourceDevice(); got != "extra" {
		t.Fatalf("SourceDevice fallback to extras = %q, want %q", got, "extra")
	}

	m = Message{}
	if got := m.SourceDevice(); got != "" {
		t.Fatalf("SourceDevice on empty message = %q, want empty", got)
	}
}

// TestMessagesByDeviceOnMemoryStore: the degraded in-memory store must apply
// the same device isolation.
func TestMessagesByDeviceOnMemoryStore(t *testing.T) {
	svc := buildMemoryFallbackService(t)
	publishDevice(t, svc, "d1", 2)
	publishDevice(t, svc, "d2", 1)
	msgs, _ := svc.MessagesByDevice("d1", 100, 0)
	if len(msgs) != 2 {
		t.Fatalf("memory store: MessagesByDevice(d1) want 2, got %d", len(msgs))
	}
	all, _ := svc.MessagesByDevice("", 100, 0)
	if len(all) != 3 {
		t.Fatalf("memory store: all want 3, got %d", len(all))
	}
}

// TestDeleteByDevice: wiping one device's history leaves the other intact, and
// deleting a message ID that belongs to another device reports 404 (false).
func TestDeleteByDevice(t *testing.T) {
	svc := buildTestService(t, "")
	publishDevice(t, svc, "d1", 2) // ids 1,2
	publishDevice(t, svc, "d2", 2) // ids 3,4

	// deleting d2's id from d1's scope must report not-existed
	ok, err := svc.DeleteMessageByDevice("d1", 3)
	if err != nil {
		t.Fatalf("DeleteMessageByDevice: %v", err)
	}
	if ok {
		t.Fatal("message 3 belongs to d2, must not be deletable from d1 scope")
	}

	// single delete within d1's own scope must work
	ok, err = svc.DeleteMessageByDevice("d1", 2)
	if err != nil {
		t.Fatalf("DeleteMessageByDevice(d1,2): %v", err)
	}
	if !ok {
		t.Fatal("message 2 belongs to d1 and should be deletable")
	}
	d1, _ := svc.MessagesByDevice("d1", 100, 0)
	if len(d1) != 1 || d1[0].ID != 1 {
		t.Fatalf("d1 after single delete: want only id 1, got %+v", d1)
	}

	// bulk-delete d1
	if err := svc.DeleteAllMessagesByDevice("d1"); err != nil {
		t.Fatalf("DeleteAllMessagesByDevice(d1): %v", err)
	}
	d1, _ = svc.MessagesByDevice("d1", 100, 0)
	if len(d1) != 0 {
		t.Fatalf("d1 messages should be empty, got %d", len(d1))
	}
	d2, _ := svc.MessagesByDevice("d2", 100, 0)
	if len(d2) != 2 {
		t.Fatalf("d2 messages should be untouched (2), got %d", len(d2))
	}
}

// TestSubscribeByDevice: a device-scoped subscriber only hears its own device's
// messages, never another device's, while a wildcard subscriber hears all.
func TestSubscribeByDevice(t *testing.T) {
	svc := buildTestService(t, "")
	chD1, unD1 := svc.SubscribeByDevice("k1")
	defer unD1()
	chAll, unAll := svc.SubscribeByDevice("")
	defer unAll()

	_ = svc.Publish("t", "b", 0, map[string]interface{}{"device_key": "k1"})
	if m := <-chD1; m.SourceDevice() != "k1" {
		t.Fatalf("k1 subscriber got %q", m.SourceDevice())
	}
	if m := <-chAll; m.SourceDevice() != "k1" {
		t.Fatalf("wildcard subscriber got %q", m.SourceDevice())
	}

	// a k2 message must NOT reach the k1 subscriber
	_ = svc.Publish("t", "b", 0, map[string]interface{}{"device_key": "k2"})
	if m := <-chAll; m.SourceDevice() != "k2" {
		t.Fatalf("wildcard subscriber should get k2, got %q", m.SourceDevice())
	}
	select {
	case m := <-chD1:
		t.Fatalf("k1 subscriber must NOT receive k2 message, got %q", m.SourceDevice())
	case <-time.After(50 * time.Millisecond):
	}
}

// TestDeleteOnMemoryStore covers single + bulk deletion on the degraded
// in-memory store.
func TestDeleteOnMemoryStore(t *testing.T) {
	svc := buildMemoryFallbackService(t)
	publishN(t, svc, 3)

	ok, err := svc.DeleteMessageByDevice("", 2)
	if err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	if !ok {
		t.Fatal("message 2 should exist")
	}
	msgs, _ := svc.MessagesByDevice("", 100, 0)
	if len(msgs) != 2 || msgs[0].ID != 3 || msgs[1].ID != 1 {
		t.Fatalf("unexpected remaining after single delete: %+v", msgs)
	}

	if err := svc.DeleteAllMessagesByDevice(""); err != nil {
		t.Fatalf("DeleteAllMessages: %v", err)
	}
	msgs, _ = svc.MessagesByDevice("", 100, 0)
	if len(msgs) != 0 {
		t.Fatalf("want 0 after DeleteAll on memory store, got %d", len(msgs))
	}
}

// TestDeleteByDeviceOnMemoryStore verifies the degraded in-memory store applies
// the same device isolation as bbolt for single and bulk deletion.
func TestDeleteByDeviceOnMemoryStore(t *testing.T) {
	svc := buildMemoryFallbackService(t)
	publishDevice(t, svc, "d1", 2) // ids 1,2
	publishDevice(t, svc, "d2", 2) // ids 3,4

	// deleting d2's id from d1's scope must report not-existed
	ok, err := svc.DeleteMessageByDevice("d1", 3)
	if err != nil {
		t.Fatalf("DeleteMessageByDevice: %v", err)
	}
	if ok {
		t.Fatal("message 3 belongs to d2, must not be deletable from d1 scope")
	}

	// single delete within d1's own scope must work
	ok, err = svc.DeleteMessageByDevice("d1", 2)
	if err != nil {
		t.Fatalf("DeleteMessageByDevice(d1,2): %v", err)
	}
	if !ok {
		t.Fatal("message 2 belongs to d1 and should be deletable")
	}
	d1, _ := svc.MessagesByDevice("d1", 100, 0)
	if len(d1) != 1 || d1[0].ID != 1 {
		t.Fatalf("d1 after single delete: want only id 1, got %+v", d1)
	}

	// bulk-delete d1, keep d2 intact
	if err := svc.DeleteAllMessagesByDevice("d1"); err != nil {
		t.Fatalf("DeleteAllMessagesByDevice(d1): %v", err)
	}
	d1, _ = svc.MessagesByDevice("d1", 100, 0)
	if len(d1) != 0 {
		t.Fatalf("d1 messages should be empty, got %d", len(d1))
	}
	d2, _ := svc.MessagesByDevice("d2", 100, 0)
	if len(d2) != 2 {
		t.Fatalf("d2 messages should be untouched (2), got %d", len(d2))
	}
}

// TestTokenCompareEmpty guards the constant-time comparison shortcut: a zero
// or empty digest must never validate.
func TestTokenCompareEmpty(t *testing.T) {
	if tokensEqual(nil, []byte{1, 2}) {
		t.Fatal("nil digest must not match")
	}
	if tokensEqual([]byte{1, 2}, nil) {
		t.Fatal("nil digest must not match")
	}
	if tokensEqual(nil, nil) {
		t.Fatal("two nil digests must not match")
	}
	if !tokensEqual(hashToken("x"), hashToken("x")) {
		t.Fatal("equal hashes must validate")
	}
	if tokensEqual(hashToken("x"), hashToken("y")) {
		t.Fatal("different hashes must not validate")
	}
}

// TestPublishPersistenceError covers the error path where persisting the
// message fails: the caller must receive the error and nothing may be
// published downstream (no partial fan-out).
func TestPublishPersistenceError(t *testing.T) {
	svc := buildTestService(t, "")
	if err := svc.store.(*bboltStore).Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	ch, unsubscribe := svc.SubscribeByDevice("")
	defer unsubscribe()
	if err := svc.Publish("t", "b", 0, nil); err == nil {
		t.Fatal("Publish on a closed store must return an error")
	}
	select {
	case m := <-ch:
		t.Fatalf("no message should reach subscribers on persist error, got %+v", m)
	default:
	}
}

// TestMessagesZeroLimit covers the boundary that limit==0 short-circuits to
// an empty result on both stores. Negative limit now means "no cap" and is
// covered by TestMessagesUnlimitedLimit.
func TestMessagesZeroLimit(t *testing.T) {
	for _, svc := range []*Service{
		buildTestService(t, ""),
		buildMemoryFallbackService(t),
	} {
		publishN(t, svc, 3)
		msgs, err := svc.MessagesByDevice("", 0, 0)
		if err != nil {
			t.Fatalf("Messages(0): %v", err)
		}
		if len(msgs) != 0 {
			t.Fatalf("want 0 messages for zero limit, got %d", len(msgs))
		}
	}
}

// TestDeleteAllWhenEmpty verifies the bulk delete is idempotent and the ID
// sequence is untouched by wiping an already-empty history.
func TestDeleteAllWhenEmpty(t *testing.T) {
	svc := buildTestService(t, "")
	if err := svc.DeleteAllMessagesByDevice(""); err != nil {
		t.Fatalf("DeleteAllMessages on empty store: %v", err)
	}
	if err := svc.Publish("t", "b", 0, nil); err != nil {
		t.Fatalf("Publish after empty DeleteAll: %v", err)
	}
	msgs, _ := svc.MessagesByDevice("", 1, 0)
	if len(msgs) != 1 || msgs[0].ID != 1 {
		t.Fatalf("first id after empty DeleteAll should be 1, got %+v", msgs)
	}
}
