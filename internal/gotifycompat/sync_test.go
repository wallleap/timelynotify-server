package gotifycompat

import (
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

// TestMessagesAfterAscending drives the acceptance scenario: after=0 pages
// through the device's history in ascending id order with hasMore flags.
func TestMessagesAfterAscending(t *testing.T) {
	for _, svc := range []*Service{buildTestService(t, ""), buildMemoryFallbackService(t)} {
		publishDevice(t, svc, "k1", 3) // ids 1,2,3

		msgs, hasMore, err := svc.MessagesAfterByDevice("k1", 0, 2)
		if err != nil {
			t.Fatalf("MessagesAfterByDevice(after=0): %v", err)
		}
		if len(msgs) != 2 || msgs[0].ID != 1 || msgs[1].ID != 2 {
			t.Fatalf("page1 want [1 2], got %+v", msgs)
		}
		if !hasMore {
			t.Fatal("page1 hasMore must be true")
		}

		msgs, hasMore, err = svc.MessagesAfterByDevice("k1", 2, 2)
		if err != nil {
			t.Fatalf("MessagesAfterByDevice(after=2): %v", err)
		}
		if len(msgs) != 1 || msgs[0].ID != 3 {
			t.Fatalf("page2 want [3], got %+v", msgs)
		}
		if hasMore {
			t.Fatal("page2 hasMore must be false")
		}

		msgs, hasMore, err = svc.MessagesAfterByDevice("k1", 3, 100)
		if err != nil || len(msgs) != 0 || hasMore {
			t.Fatalf("after=last want empty/false, got len=%d hasMore=%v err=%v", len(msgs), hasMore, err)
		}
		msgs, _, err = svc.MessagesAfterByDevice("k1", 0, 0)
		if err != nil || len(msgs) != 0 {
			t.Fatalf("limit=0 must stay empty, got %d %v", len(msgs), err)
		}
	}
}

// TestMessagesAfterDeviceIsolation: a per-device ascending walk skips other
// devices' interleaved ids and hasMore counts only the requested device.
func TestMessagesAfterDeviceIsolation(t *testing.T) {
	svc := buildTestService(t, "")
	publishDevice(t, svc, "k1", 2) // ids 1,2
	publishDevice(t, svc, "k2", 2) // ids 3,4
	publishDevice(t, svc, "k1", 1) // id 5

	msgs, hasMore, err := svc.MessagesAfterByDevice("k1", 0, 10)
	if err != nil {
		t.Fatalf("MessagesAfterByDevice: %v", err)
	}
	want := []uint64{1, 2, 5}
	if len(msgs) != len(want) {
		t.Fatalf("want %d k1 rows, got %d", len(want), len(msgs))
	}
	for i, m := range msgs {
		if m.ID != want[i] || m.SourceDevice() != "k1" {
			t.Fatalf("msgs[%d] = %+v, want id %d device k1", i, m, want[i])
		}
	}
	if hasMore {
		t.Fatal("hasMore must be false when the whole device history fits")
	}

	// Gaps caused by deletions must not confuse the walk.
	if _, err := svc.DeleteMessageByDevice("k1", 2); err != nil {
		t.Fatalf("delete: %v", err)
	}
	msgs, _, err = svc.MessagesAfterByDevice("k1", 1, 10)
	if err != nil || len(msgs) != 1 || msgs[0].ID != 5 {
		t.Fatalf("after gap want [5], got %+v err=%v", msgs, err)
	}
}

// TestMessagesAfterIndexRebuiltOnReopen verifies the (device_key,id) index is
// available for an existing gotify.db that predates the feature (migration
// rebuilds the index), and keeps working across a normal reopen.
func TestMessagesAfterIndexRebuiltOnReopen(t *testing.T) {
	dir := t.TempDir()
	svc1, err := Init(Config{DataDir: dir, ClientToken: ""})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	publishDevice(t, svc1, "k1", 3)
	if err := svc1.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Simulate a pre-feature database: drop the secondary index bucket.
	db, err := bolt.Open(t.TempDir()+"/ignore.db", 0o600, nil)
	_ = db.Close()
	core, err := bolt.Open(dir+"/gotify.db", 0o600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		t.Fatalf("reopen raw: %v", err)
	}
	if err := core.Update(func(tx *bolt.Tx) error {
		return tx.DeleteBucket([]byte(bucketDeviceMessages))
	}); err != nil {
		t.Fatalf("drop index bucket: %v", err)
	}
	if err := core.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	svc2, err := Init(Config{DataDir: dir, ClientToken: ""})
	if err != nil {
		t.Fatalf("reopen Init: %v", err)
	}
	defer svc2.Close()
	msgs, _, err := svc2.MessagesAfterByDevice("k1", 0, 100)
	if err != nil {
		t.Fatalf("after reopen: %v", err)
	}
	if len(msgs) != 3 || msgs[0].ID != 1 || msgs[2].ID != 3 {
		t.Fatalf("rebuilt index want 1..3 asc, got %+v", msgs)
	}
}

// TestMessagesPageDescHasMore checks hasMore on the legacy newest-first walk:
// it reports whether older messages remain in the walk direction.
func TestMessagesPageDescHasMore(t *testing.T) {
	svc := buildTestService(t, "")
	publishDevice(t, svc, "k1", 5)

	msgs, hasMore, err := svc.MessagesPageByDevice("k1", 2, 0)
	if err != nil || len(msgs) != 2 || msgs[0].ID != 5 || msgs[1].ID != 4 {
		t.Fatalf("page1: %+v hasMore=%v err=%v", msgs, hasMore, err)
	}
	if !hasMore {
		t.Fatal("page1 must report older messages remain")
	}
	msgs, hasMore, err = svc.MessagesPageByDevice("k1", 10, 4)
	if err != nil || len(msgs) != 3 || msgs[0].ID != 3 || msgs[2].ID != 1 {
		t.Fatalf("page2: %+v err=%v", msgs, err)
	}
	if hasMore {
		t.Fatal("page2 is the last page, hasMore must be false")
	}
}

// seedAgedDeletion inserts a deletion record with an explicit created_at
// timestamp (used to exercise the 30-day retention sweep).
func seedAgedDeletion(t *testing.T, svc *Service, device string, kind DeletionKind, msgID, ceiling uint64, at time.Time) uint64 {
	t.Helper()
	rec := deletionRecord{
		DeviceKey: device,
		Kind:      kind,
		MessageID: msgID,
		Ceiling:   ceiling,
		CreatedAt: at.Unix(),
	}
	switch st := svc.store.(type) {
	case *bboltStore:
		var id uint64
		err := st.db.Update(func(tx *bolt.Tx) error {
			var err error
			id, err = addDeletionTx(tx, rec)
			return err
		})
		if err != nil {
			t.Fatalf("seed aged deletion: %v", err)
		}
		return id
	case *memoryStore:
		svc.store.(*memoryStore).mu.Lock()
		id := st.delSeq + 1
		rec.ID = id
		st.dels = append(st.dels, rec)
		st.delSeq = id
		st.mu.Unlock()
		return id
	default:
		t.Fatalf("unsupported store %T", st)
		return 0
	}
}

// TestDeletionTombstones covers the three manual埋点: single delete writes
// kind=1 only when the row existed, purge writes one kind=2 with the device's
// max id as ceiling, and events are device-isolated. The client protocol
// treats deletedSince=0 as a first-sync baseline (empty + current cursor), so
// an anchor event is created before taking the baseline cursor; only events
// newer than the baseline are observable (events at/before seeding are
// already reflected in the message snapshot).
func TestDeletionTombstones(t *testing.T) {
	for _, svc := range []*Service{buildTestService(t, ""), buildMemoryFallbackService(t)} {
		publishDevice(t, svc, "d1", 3) // ids 1,2,3
		publishDevice(t, svc, "d2", 1) // id 4

		// First-time baseline on an empty log: cursor 0, empty, no reset.
		p0, err := svc.DeletionsByDevice("d1", 0)
		if err != nil || p0.Cursor != 0 || p0.Reset || len(p0.IDs) != 0 || len(p0.Purges) != 0 {
			t.Fatalf("baseline on empty log: %+v err=%v", p0, err)
		}

		// Anchor event (the client's first-sync baseline lands here).
		if ok, err := svc.DeleteMessageByDevice("d1", 1); err != nil || !ok {
			t.Fatalf("anchor delete d1/1: ok=%v err=%v", ok, err)
		}
		base, err := svc.DeletionsByDevice("d1", 0)
		if err != nil || base.Cursor == 0 {
			t.Fatalf("baseline cursor: %+v err=%v", base, err)
		}
		if len(base.IDs) != 0 || len(base.Purges) != 0 || base.Reset || base.HasMore {
			t.Fatalf("baseline must be empty/non-reset: %+v", base)
		}

		// Events after the baseline are observable.
		ok, err := svc.DeleteMessageByDevice("d1", 2)
		if err != nil || !ok {
			t.Fatalf("delete d1/2: ok=%v err=%v", ok, err)
		}
		// Deleting a missing / foreign message must not write a tombstone.
		if ok, _ := svc.DeleteMessageByDevice("d1", 99); ok {
			t.Fatal("d1/99 must not exist")
		}
		if ok, _ := svc.DeleteMessageByDevice("d1", 4); ok {
			t.Fatal("id 4 belongs to d2")
		}

		page, err := svc.DeletionsByDevice("d1", base.Cursor)
		if err != nil {
			t.Fatalf("deletions page: %v", err)
		}
		if len(page.IDs) != 1 || page.IDs[0] != 2 || len(page.Purges) != 0 {
			t.Fatalf("want single kind=1 id [2], got ids=%v purges=%v", page.IDs, page.Purges)
		}
		if page.Reset || page.HasMore || page.Cursor <= base.Cursor {
			t.Fatalf("unexpected page flags: %+v (base cursor %d)", page, base.Cursor)
		}

		// d2 has never had an event: cursor 0 and no leak from d1.
		d2page, err := svc.DeletionsByDevice("d2", 0)
		if err != nil || d2page.Cursor != 0 || len(d2page.IDs) != 0 {
			t.Fatalf("device leak into d2: %+v err=%v", d2page, err)
		}

		// Purge d1: one kind=2 row with ceiling = max d1 message id (3).
		if err := svc.DeleteAllMessagesByDevice("d1"); err != nil {
			t.Fatalf("purge d1: %v", err)
		}
		purgePage, err := svc.DeletionsByDevice("d1", page.Cursor)
		if err != nil {
			t.Fatalf("purge page: %v", err)
		}
		if len(purgePage.Purges) != 1 || purgePage.Purges[0] != 3 || len(purgePage.IDs) != 0 {
			t.Fatalf("want purge ceiling [3], got ids=%v purges=%v", purgePage.IDs, purgePage.Purges)
		}
		// Re-purging an empty device writes nothing.
		cursorAfterPurge := purgePage.Cursor
		if err := svc.DeleteAllMessagesByDevice("d1"); err != nil {
			t.Fatalf("empty purge: %v", err)
		}
		again, _ := svc.DeletionsByDevice("d1", cursorAfterPurge)
		if again.Cursor != cursorAfterPurge || len(again.IDs) != 0 || len(again.Purges) != 0 {
			t.Fatalf("empty purge must be a no-op in the log: %+v", again)
		}
	}
}

// TestPurgeAllDevicesWritesPerDeviceCeiling: the device=="" wipe records one
// purge per affected device, using that device's own max id. A per-device
// anchor event establishes the baseline cursor first.
func TestPurgeAllDevicesWritesPerDeviceCeiling(t *testing.T) {
	svc := buildTestService(t, "")
	// Anchors: one observable event per device before the action under test.
	publishDevice(t, svc, "d1", 1) // id 1
	publishDevice(t, svc, "d2", 1) // id 2
	if ok, _ := svc.DeleteMessageByDevice("d1", 1); !ok {
		t.Fatal("anchor d1 delete failed")
	}
	if ok, _ := svc.DeleteMessageByDevice("d2", 2); !ok {
		t.Fatal("anchor d2 delete failed")
	}
	baseD1, _ := svc.DeletionsByDevice("d1", 0)
	baseD2, _ := svc.DeletionsByDevice("d2", 0)

	publishDevice(t, svc, "d1", 2) // ids 3,4
	publishDevice(t, svc, "d2", 3) // ids 5,6,7
	if err := svc.DeleteAllMessagesByDevice(""); err != nil {
		t.Fatalf("purge all: %v", err)
	}
	d1, err := svc.DeletionsByDevice("d1", baseD1.Cursor)
	if err != nil || len(d1.Purges) != 1 || d1.Purges[0] != 4 {
		t.Fatalf("d1 want ceiling 4: %+v err=%v", d1, err)
	}
	d2, err := svc.DeletionsByDevice("d2", baseD2.Cursor)
	if err != nil || len(d2.Purges) != 1 || d2.Purges[0] != 7 {
		t.Fatalf("d2 want ceiling 7: %+v err=%v", d2, err)
	}
}

// TestCapacityEvictionTombstones: --gotify-max-messages drops the oldest
// messages; each evicted id must surface as a kind=1 deletion. An anchor
// delete before the evictions gives the client a baseline cursor.
func TestCapacityEvictionTombstones(t *testing.T) {
	makeSvc := func(store Store) *Service {
		return &Service{store: store, hub: newHub(), stopCh: make(chan struct{})}
	}
	for _, svc := range []*Service{
		newSvc(t, Config{DataDir: t.TempDir(), ClientToken: "", MaxMessages: 3}),
		makeSvc(newMemoryStore(3)),
	} {
		publishDevice(t, svc, "k1", 3) // ids 1,2,3
		if ok, _ := svc.DeleteMessageByDevice("k1", 3); !ok {
			t.Fatal("anchor delete failed")
		}
		base, err := svc.DeletionsByDevice("k1", 0)
		if err != nil || base.Cursor == 0 {
			t.Fatalf("baseline: %+v err=%v", base, err)
		}
		publishDevice(t, svc, "k1", 3) // ids 4,5,6; cap 3 evicts 1 then 2
		page, err := svc.DeletionsByDevice("k1", base.Cursor)
		if err != nil || len(page.IDs) != 2 || page.IDs[0] != 1 || page.IDs[1] != 2 {
			t.Fatalf("evicted ids want [1 2], got %+v err=%v", page, err)
		}
		msgs, _ := svc.MessagesByDevice("k1", 100, 0)
		if len(msgs) != 3 || msgs[0].ID != 6 || msgs[2].ID != 4 {
			t.Fatalf("survivors want 4,5,6, got %+v", msgs)
		}
	}
}

// TestTTLExpiryWritesTombstones: messages carrying a ttl extra are removed by
// the sweep and recorded as kind=1 events; not-yet-expired messages survive;
// orphan ttl index rows are cleaned without phantom events.
func TestTTLExpiryWritesTombstones(t *testing.T) {
	svc := buildTestService(t, "")
	extras := func(ttl interface{}) map[string]interface{} {
		return map[string]interface{}{"device_key": "k1", "ttl": ttl}
	}
	// Anchor: one observable event before the baseline (id 1, deleted).
	if err := svc.Publish("anchor", "x", 0, map[string]interface{}{"device_key": "k1"}); err != nil {
		t.Fatalf("publish anchor: %v", err)
	}
	if ok, _ := svc.DeleteMessageByDevice("k1", 1); !ok {
		t.Fatal("anchor delete failed")
	}
	base, _ := svc.DeletionsByDevice("k1", 0)

	// ids 2 and 3 expire; 4 has no ttl; 5/6 carry invalid ttl and survive.
	if err := svc.Publish("a", "b", 0, extras(float64(60))); err != nil {
		t.Fatalf("publish ttl: %v", err)
	}
	if err := svc.Publish("c", "d", 0, extras("60")); err != nil {
		t.Fatalf("publish ttl string: %v", err)
	}
	if err := svc.Publish("e", "f", 0, map[string]interface{}{"device_key": "k1"}); err != nil {
		t.Fatalf("publish without ttl: %v", err)
	}
	if err := svc.Publish("g", "h", 0, extras(float64(-5))); err != nil {
		t.Fatalf("publish negative ttl: %v", err)
	}
	if err := svc.Publish("i", "j", 0, extras("soon")); err != nil {
		t.Fatalf("publish junk ttl: %v", err)
	}

	n, err := svc.ExpireMessages(time.Now().Add(30 * time.Second))
	if err != nil || n != 0 {
		t.Fatalf("nothing should be expired yet, got n=%d err=%v", n, err)
	}
	n, err = svc.ExpireMessages(time.Now().Add(61 * time.Second))
	if err != nil || n != 2 {
		t.Fatalf("want 2 expired, got n=%d err=%v", n, err)
	}
	msgs, _ := svc.MessagesByDevice("k1", 100, 0)
	if len(msgs) != 3 {
		t.Fatalf("ttl-free messages must survive (ids 4,5,6), got %d", len(msgs))
	}
	page, err := svc.DeletionsByDevice("k1", base.Cursor)
	if err != nil || len(page.IDs) != 2 || page.IDs[0] != 2 || page.IDs[1] != 3 {
		t.Fatalf("expired ids want [2 3], got %+v err=%v", page, err)
	}

	// Sweep again is idempotent (no duplicate tombstones).
	n, _ = svc.ExpireMessages(time.Now().Add(365 * 24 * time.Hour))
	if n != 0 {
		t.Fatalf("second sweep must remove nothing, got %d", n)
	}
	page, _ = svc.DeletionsByDevice("k1", page.Cursor)
	if len(page.IDs) != 0 {
		t.Fatalf("no new events expected, got %v", page.IDs)
	}
}

// TestTTLOverwriteRefreshesDeadline: re-publishing the same extras.id with a
// new ttl extends the old message's lifetime instead of expiring it.
func TestTTLOverwriteRefreshesDeadline(t *testing.T) {
	svc := buildTestService(t, "")
	pub := func(ttl int) {
		t.Helper()
		err := svc.Publish("t", "b", 0, map[string]interface{}{
			"device_key": "k1", "id": "logical-1", "ttl": ttl,
		})
		if err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	pub(60)
	pub(120)
	if n, _ := svc.ExpireMessages(time.Now().Add(61 * time.Second)); n != 0 {
		t.Fatalf("overwritten deadline must be ~120s, but sweep at 61s removed %d", n)
	}
	if n, _ := svc.ExpireMessages(time.Now().Add(121 * time.Second)); n != 1 {
		t.Fatalf("sweep at 121s must remove 1 message, got %d", n)
	}
}

// TestTTLExpireBulk1000 simulates the acceptance batch: 1000 messages expiring
// at once must land as tombstones in one sweep, pageable at 500/page without
// a reset and without pathological latency.
func TestTTLExpireBulk1000(t *testing.T) {
	svc := newSvc(t, Config{DataDir: t.TempDir(), ClientToken: "", MaxMessages: 1100})
	// Anchor event so the tested events are newer than the baseline cursor.
	if err := svc.Publish("anchor", "x", 0, map[string]interface{}{"device_key": "k1"}); err != nil {
		t.Fatalf("anchor publish: %v", err)
	}
	if ok, _ := svc.DeleteMessageByDevice("k1", 1); !ok {
		t.Fatal("anchor delete failed")
	}
	base, err := svc.DeletionsByDevice("k1", 0)
	if err != nil || base.Reset || base.Cursor == 0 {
		t.Fatalf("baseline: %+v err=%v", base, err)
	}
	const total = 1000
	for i := 0; i < total; i++ {
		if err := svc.Publish("t", "b", 0, map[string]interface{}{
			"device_key": "k1", "ttl": 60,
		}); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}
	start := time.Now()
	n, err := svc.ExpireMessages(time.Now().Add(61 * time.Second))
	elapsed := time.Since(start)
	if err != nil || n != total {
		t.Fatalf("bulk expire want %d, got %d err=%v", total, n, err)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("bulk expire took %s, too slow", elapsed)
	}
	t.Logf("bulk expire %d messages in %s", total, elapsed)

	cursor := base.Cursor
	seen := make(map[uint64]struct{}, total)
	pages := 0
	for {
		page, err := svc.DeletionsByDevice("k1", cursor)
		if err != nil {
			t.Fatalf("deletions page: %v", err)
		}
		if page.Reset {
			t.Fatal("must not reset within the retention window")
		}
		if len(page.IDs) > deletionPageLimit {
			t.Fatalf("page exceeded %d: %d", deletionPageLimit, len(page.IDs))
		}
		if page.HasMore && len(page.IDs) != deletionPageLimit {
			t.Fatalf("intermediate page must be full, got %d", len(page.IDs))
		}
		for _, id := range page.IDs {
			if _, dup := seen[id]; dup {
				t.Fatalf("duplicate event id %d across pages", id)
			}
			seen[id] = struct{}{}
		}
		pages++
		cursor = page.Cursor
		if !page.HasMore {
			break
		}
		if pages > total/deletionPageLimit+2 {
			t.Fatal("paging did not converge")
		}
	}
	if len(seen) != total {
		t.Fatalf("paged events = %d, want %d", len(seen), total)
	}
}

// TestDeletionsPagingBoundary: exactly 501 events yields a full first page
// (hasMore, continuation cursor) then a 1-row final page.
func TestDeletionsPagingBoundary(t *testing.T) {
	svc := newSvc(t, Config{DataDir: t.TempDir(), ClientToken: "", MaxMessages: 600})
	// Anchor event establishing the baseline cursor.
	if err := svc.Publish("anchor", "x", 0, map[string]interface{}{"device_key": "k1"}); err != nil {
		t.Fatalf("anchor publish: %v", err)
	}
	if ok, _ := svc.DeleteMessageByDevice("k1", 1); !ok {
		t.Fatal("anchor delete failed")
	}
	base, _ := svc.DeletionsByDevice("k1", 0)
	for i := 0; i < deletionPageLimit+1; i++ {
		if err := svc.Publish("t", "b", 0, map[string]interface{}{
			"device_key": "k1", "ttl": 60,
		}); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}
	if _, err := svc.ExpireMessages(time.Now().Add(61 * time.Second)); err != nil {
		t.Fatalf("expire: %v", err)
	}
	p1, err := svc.DeletionsByDevice("k1", base.Cursor)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(p1.IDs) != deletionPageLimit || !p1.HasMore {
		t.Fatalf("page1 want %d/hasMore, got %d/%v", deletionPageLimit, len(p1.IDs), p1.HasMore)
	}
	if p1.Cursor != p1.IDs[len(p1.IDs)-1] {
		t.Fatalf("continuation cursor must be the page's last event id %d, got %d",
			p1.IDs[len(p1.IDs)-1], p1.Cursor)
	}
	p2, err := svc.DeletionsByDevice("k1", p1.Cursor)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(p2.IDs) != 1 || p2.HasMore {
		t.Fatalf("page2 want 1/done, got %d/hasMore=%v", len(p2.IDs), p2.HasMore)
	}
	if p2.Cursor < p1.Cursor {
		t.Fatalf("final cursor %d must not move backwards (was %d)", p2.Cursor, p1.Cursor)
	}
	if p3, _ := svc.DeletionsByDevice("k1", p2.Cursor); len(p3.IDs) != 0 || p3.HasMore {
		t.Fatalf("re-reading the final cursor must be empty: %+v", p3)
	}
}

// TestRetentionResetSentinel exercises the 30-day cleanup: old rows are
// dropped, each affected device first receives a kind=3 sentinel (its new
// oldest row), and clients with a cursor inside the gap observe reset=true.
func TestRetentionResetSentinel(t *testing.T) {
	for _, svc := range []*Service{buildTestService(t, ""), buildMemoryFallbackService(t)} {
		now := time.Now()
		oldID := seedAgedDeletion(t, svc, "d1", DeletionSingle, 7, 0, now.Add(-31*24*time.Hour))
		// A recent row must survive the sweep and stay queryable.
		freshID := seedAgedDeletion(t, svc, "d1", DeletionSingle, 8, 0, now.Add(-time.Hour))

		removed, err := svc.CleanupDeletions(now)
		if err != nil || removed != 1 {
			t.Fatalf("cleanup want removed=1, got %d err=%v", removed, err)
		}

		// Client whose cursor predates the retention window observes reset:
		// empty arrays, cursor advanced to the current max, hasMore=false.
		page, err := svc.DeletionsByDevice("d1", oldID)
		if err != nil {
			t.Fatalf("reset query: %v", err)
		}
		if !page.Reset {
			t.Fatal("expected reset=true for a cursor inside the retention gap")
		}
		if len(page.IDs) != 0 || len(page.Purges) != 0 || page.HasMore {
			t.Fatalf("reset page must be empty and final: %+v", page)
		}
		if page.Cursor < freshID || page.Cursor == 0 {
			t.Fatalf("reset cursor must advance to the current max (>= %d), got %d", freshID, page.Cursor)
		}

		// Baseline after a reset never reports reset itself.
		base, _ := svc.DeletionsByDevice("d1", 0)
		if base.Reset || base.Cursor != page.Cursor {
			t.Fatalf("baseline after reset: %+v", base)
		}

		// A device that never had any rows: cursor 0, no reset.
		none, err := svc.DeletionsByDevice("ghost", 0)
		if err != nil || none.Cursor != 0 || none.Reset || len(none.IDs) != 0 {
			t.Fatalf("unknown device: %+v err=%v", none, err)
		}

		// The fresh row survives and remains readable after the sentinel.
		from := page.Cursor
		rest, err := svc.DeletionsByDevice("d1", from)
		if err != nil || len(rest.IDs) != 0 {
			t.Fatalf("nothing newer than current max: %+v err=%v", rest, err)
		}
	}
}

// TestValidateMessageListParams covers the 400 combination matrix.
func TestValidateMessageListParams(t *testing.T) {
	cases := []struct {
		name        string
		after       bool
		since       bool
		query       bool
		delSince    bool
		limit       int
		wantErr     bool
		wantMessage string
	}{
		{"plain", false, false, false, false, 100, false, ""},
		{"after", true, false, false, false, 100, false, ""},
		{"after+since", true, true, false, false, 100, true, "after and since cannot be used together"},
		{"after+query", true, false, true, false, 100, true, "after and query cannot be used together"},
		{"after+export", true, false, false, false, -1, true, "after is not allowed with limit=-1"},
		{"delSince+export", false, false, false, true, -1, true, "deletedSince is not allowed with limit=-1"},
		{"after+delSince", true, false, false, true, 100, false, ""},
		{"since+delSince", false, true, false, true, 100, false, ""},
		{"query+delSince", false, false, true, true, 100, false, ""},
		{"since+query+delSince", false, true, true, true, 100, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateMessageListParams(tc.after, tc.since, tc.query, tc.delSince, tc.limit)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error %q, got nil", tc.wantMessage)
				}
				if err.Error() != tc.wantMessage {
					t.Fatalf("want %q, got %q", tc.wantMessage, err.Error())
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// TestParseCursor covers strict cursor parsing: empty means absent; negatives
// and junk are rejected (the handler maps these to 400).
func TestParseCursor(t *testing.T) {
	if v, present, err := ParseCursor("after", ""); present || v != 0 || err != nil {
		t.Fatalf("empty: v=%d present=%v err=%v", v, present, err)
	}
	if v, present, err := ParseCursor("after", "42"); !present || v != 42 || err != nil {
		t.Fatalf("42: v=%d present=%v err=%v", v, present, err)
	}
	for _, bad := range []string{"-1", "abc", "1.5", "99999999999999999999999"} {
		if _, _, err := ParseCursor("after", bad); err == nil {
			t.Fatalf("%q must be rejected", bad)
		}
	}
}

// TestDeleteByExtraID covers explicit extras.id deletion (the pushDelete
// path): a matching message is removed with a kind=1 tombstone, a
// DeletionByExtraID record is always appended (even when no message
// matched), and the envelope projects it as extraIds (int64) coexisting
// with ids/purges. Runs against both the bbolt and the memory store.
func TestDeleteByExtraID(t *testing.T) {
	for _, svc := range []*Service{buildTestService(t, ""), buildMemoryFallbackService(t)} {
		pub := func(device, extraID string) {
			t.Helper()
			if err := svc.Publish("t", "b", 0, map[string]interface{}{"device_key": device, "id": extraID}); err != nil {
				t.Fatalf("Publish(%s, id=%s): %v", device, extraID, err)
			}
		}
		pub("d1", "7") // id 1
		pub("d1", "8") // id 2
		pub("d2", "7") // id 3 — same extras.id, another device: untouched

		// Anchor event so the client's baseline cursor is non-zero.
		publishDevice(t, svc, "d1", 1) // id 4
		if ok, err := svc.DeleteMessageByDevice("d1", 4); err != nil || !ok {
			t.Fatalf("anchor delete d1/4: ok=%v err=%v", ok, err)
		}
		base, err := svc.DeletionsByDevice("d1", 0)
		if err != nil || base.Cursor == 0 {
			t.Fatalf("baseline cursor: %+v err=%v", base, err)
		}
		if len(base.ExtraIDs) != 0 {
			t.Fatalf("baseline must not leak extraIds: %+v", base)
		}

		// Found: message removed, kind=1 tombstone + kind=4 record.
		removed, err := svc.DeleteMessageByExtraID("d1", "7")
		if err != nil || !removed {
			t.Fatalf("DeleteMessageByExtraID(d1, 7): removed=%v err=%v", removed, err)
		}
		msgs, _ := svc.MessagesByDevice("d1", 100, 0)
		if len(msgs) != 1 || msgs[0].ID != 2 {
			t.Fatalf("d1 should keep only id 2, got %+v", msgs)
		}
		page, err := svc.DeletionsByDevice("d1", base.Cursor)
		if err != nil {
			t.Fatalf("deletions page: %v", err)
		}
		if len(page.IDs) != 1 || page.IDs[0] != 1 {
			t.Fatalf("want kind=1 ids [1], got %v", page.IDs)
		}
		if len(page.ExtraIDs) != 1 || page.ExtraIDs[0] != 7 {
			t.Fatalf("want extraIds [7], got %v", page.ExtraIDs)
		}
		if len(page.Purges) != 0 || page.Reset || page.HasMore {
			t.Fatalf("unexpected page flags: %+v", page)
		}

		// Not found: no message removed, but the extras.id record still lands.
		removed, err = svc.DeleteMessageByExtraID("d1", "99")
		if err != nil || removed {
			t.Fatalf("DeleteMessageByExtraID(d1, 99): removed=%v err=%v", removed, err)
		}
		page2, err := svc.DeletionsByDevice("d1", page.Cursor)
		if err != nil {
			t.Fatalf("deletions page2: %v", err)
		}
		if len(page2.ExtraIDs) != 1 || page2.ExtraIDs[0] != 99 {
			t.Fatalf("want extraIds [99], got %v", page2.ExtraIDs)
		}
		if len(page2.IDs) != 0 {
			t.Fatalf("a missing extras.id must not write kind=1 ids, got %v", page2.IDs)
		}

		// extraIds coexist with purges on the same page.
		if err := svc.DeleteAllMessagesByDevice("d1"); err != nil {
			t.Fatalf("purge d1: %v", err)
		}
		page3, err := svc.DeletionsByDevice("d1", page2.Cursor)
		if err != nil {
			t.Fatalf("deletions page3: %v", err)
		}
		if len(page3.Purges) != 1 || page3.Purges[0] != 2 {
			t.Fatalf("want purge ceiling [2], got %v", page3.Purges)
		}
		if len(page3.ExtraIDs) != 0 {
			t.Fatalf("purge page must not replay extraIds, got %v", page3.ExtraIDs)
		}

		// d2's message with the same extras.id is untouched and its log is clean.
		d2msgs, _ := svc.MessagesByDevice("d2", 100, 0)
		if len(d2msgs) != 1 {
			t.Fatalf("d2 message must survive, got %+v", d2msgs)
		}
		d2page, err := svc.DeletionsByDevice("d2", 0)
		if err != nil || d2page.Cursor != 0 || len(d2page.ExtraIDs) != 0 {
			t.Fatalf("device leak into d2: %+v err=%v", d2page, err)
		}
	}
}

func newSvc(t *testing.T, cfg Config) *Service {
	t.Helper()
	svc, err := Init(cfg)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	return svc
}
