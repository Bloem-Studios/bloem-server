package remotesource

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// The store keeps the validators and probe of the last scan, and GetMany
// reads a page of paths in one query.
func TestRemoteSourceValidatorsDB(t *testing.T) {
	pool := remoteSourceTestPool(t)
	ctx := context.Background()
	store := NewStore(pool)
	first, second := "/media/movies/validators-db-test-a.strm", "/media/movies/validators-db-test-b.strm"
	t.Cleanup(func() {
		_ = store.Delete(context.Background(), first)
		_ = store.Delete(context.Background(), second)
	})
	when := time.Unix(1_700_000_000, 0).UTC()
	probe := json.RawMessage(`{"CodecVideo":"h264","Duration":42}`)
	if err := store.Put(ctx, Source{FilePath: first, URL: "https://media.example.net/a.mkv", RemoteSize: 10, Validators: Validators{Size: 10, ETag: `"a"`, LastModified: when}, Probe: probe}); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, Source{FilePath: second, URL: "https://media.example.net/b.mkv", SourceRevision: "v2", RemoteSize: 20}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.Get(ctx, first)
	if err != nil || !ok || got.Validators != (Validators{Size: 10, ETag: `"a"`, LastModified: when}) {
		t.Fatalf("Get = %+v, %v, %v", got, ok, err)
	}
	var decoded map[string]any
	if json.Unmarshal(got.Probe, &decoded) != nil || decoded["CodecVideo"] != "h264" {
		t.Fatalf("stored probe = %s", got.Probe)
	}
	many, err := store.GetMany(ctx, []string{first, second, "/media/movies/missing.strm"})
	if err != nil || len(many) != 2 || many[second].SourceRevision != "v2" || many[second].Probe != nil || many[second].Validators.Size != 20 || many[first].URL != "https://media.example.net/a.mkv" {
		t.Fatalf("GetMany = %+v, %v", many, err)
	}
	if empty, err := store.GetMany(ctx, nil); err != nil || len(empty) != 0 {
		t.Fatalf("GetMany(nil) = %v, %v", empty, err)
	}
}
