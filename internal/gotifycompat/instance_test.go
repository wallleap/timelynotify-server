package gotifycompat

import (
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"
)

func TestInstanceIDPersistsWithHistory(t *testing.T) {
	dir := t.TempDir()
	first, err := Init(Config{DataDir: dir, ClientToken: "test-token"})
	if err != nil {
		t.Fatal(err)
	}
	id := first.InstanceID()
	if id == "" || !first.InstancePersistent() {
		t.Fatalf("first instance = %q, persistent = %v", id, first.InstancePersistent())
	}
	if err := first.Publish("title", "body", 1, map[string]interface{}{"device_key": "one"}); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Init(Config{DataDir: dir, ClientToken: "test-token"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	if second.InstanceID() != id {
		t.Fatalf("reopened instance = %q, want %q", second.InstanceID(), id)
	}
	if err := second.DeleteAllMessagesByDevice("one"); err != nil {
		t.Fatal(err)
	}
	if second.InstanceID() != id {
		t.Fatal("clearing history must not rotate instance ID")
	}
	third, err := Init(Config{DataDir: t.TempDir(), ClientToken: "test-token"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = third.Close() })
	if third.InstanceID() == id {
		t.Fatal("different database must have a different instance ID")
	}
}

func TestInstanceIDMigratesLegacyDatabase(t *testing.T) {
	dir := t.TempDir()
	db, err := bolt.Open(filepath.Join(dir, "gotify.db"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(bucketMeta))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	svc, err := Init(Config{DataDir: dir, ClientToken: "test-token"})
	if err != nil {
		t.Fatal(err)
	}
	if svc.InstanceID() == "" || !svc.InstancePersistent() {
		t.Fatalf("legacy migration instance = %q, persistent = %v", svc.InstanceID(), svc.InstancePersistent())
	}
	_ = svc.Close()
}

func TestMemoryInstanceIDIsTemporary(t *testing.T) {
	first, err := Init(Config{ClientToken: "test-token"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	second, err := Init(Config{ClientToken: "test-token"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	if first.InstanceID() == "" || first.InstanceID() == second.InstanceID() ||
		first.InstancePersistent() || second.InstancePersistent() {
		t.Fatalf("memory instances = %q / %q, persistent = %v / %v",
			first.InstanceID(), second.InstanceID(), first.InstancePersistent(), second.InstancePersistent())
	}
}
