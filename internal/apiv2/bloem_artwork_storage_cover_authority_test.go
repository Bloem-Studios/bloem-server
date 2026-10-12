package apiv2

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/artworkkey"
	"github.com/Silo-Server/silo-server/internal/artworkurl"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

func TestMalformedNativeCoverNeverReadsLocalArtwork(t *testing.T) {
	signer := artworkurl.NewSigner("test-secret", time.Hour)
	store := &artworkReadFailure{}
	h := NewBloemArtworkHandler(store, signer, nil, &authorizedCoverStub{})
	for _, key := range []string{"storage-covers/book/original.bad.img", "storage-covers/book/malformed"} {
		url, _ := signer.Sign(key, time.Now())
		got := do(t, h, http.MethodGet, url, "", nil)
		if got.Code != http.StatusNotFound || store.calls != 0 {
			t.Fatalf("malformed native key fell back to local: status=%d reads=%d", got.Code, store.calls)
		}
	}
}

type authorizedCoverStub struct {
	storageCoverStub
	checks int
	denyAt int
}

type anonymousCoverStub struct{}

func (*anonymousCoverStub) ReadCover(context.Context, string, string) ([]byte, error) {
	return jpegCover, nil
}

func (s *authorizedCoverStub) AuthorizeCoverRequest(_ context.Context, _ *http.Request, _, _ string) error {
	s.checks++
	if s.denyAt > 0 && s.checks >= s.denyAt {
		return storagesource.ErrReferenceConflict
	}
	return nil
}

func TestStorageCoverAuthorizationBeforeBytesAndNotModified(t *testing.T) {
	for _, conditional := range []bool{false, true} {
		t.Run(map[bool]string{false: "bytes", true: "not modified"}[conditional], func(t *testing.T) {
			covers := &authorizedCoverStub{storageCoverStub: storageCoverStub{data: jpegCover}, denyAt: 1}
			h, signer := storageCoverHandler(t, covers)
			key := artworkkey.StorageCoverKey("146532612416483348", "cover/book-1", "cover:abc")
			u, _ := signer.Sign(key, time.Now())
			headers := map[string]string{}
			if conditional {
				headers["If-None-Match"] = `"` + artworkkey.StorageCoverRevision("cover/book-1", "cover:abc") + `"`
			}
			got := do(t, h, http.MethodGet, u, "", headers)
			if got.Code != http.StatusNotFound || len(covers.reads) != 0 || got.Header().Get("ETag") != "" {
				t.Fatalf("revoked cover: status=%d reads=%v etag=%q", got.Code, covers.reads, got.Header().Get("ETag"))
			}
		})
	}
}

func TestStorageCoverRechecksAuthorityAfterRead(t *testing.T) {
	covers := &authorizedCoverStub{storageCoverStub: storageCoverStub{data: jpegCover}, denyAt: 2}
	h, signer := storageCoverHandler(t, covers)
	key := artworkkey.StorageCoverKey("146532612416483348", "cover/book-1", "cover:abc")
	u, _ := signer.Sign(key, time.Now())
	got := do(t, h, http.MethodGet, u, "", nil)
	if got.Code != http.StatusNotFound || covers.checks != 2 || strings.Contains(got.Body.String(), "covercover") || got.Header().Get("ETag") != "" {
		t.Fatalf("revoked after read: status=%d checks=%d etag=%q", got.Code, covers.checks, got.Header().Get("ETag"))
	}
}

func TestStorageCoverRequiresAuthorizationServiceAndRevalidation(t *testing.T) {
	key := artworkkey.StorageCoverKey("146532612416483348", "cover/book-1", "cover:abc")
	legacy := &anonymousCoverStub{}
	h, signer := storageCoverHandler(t, legacy)
	u, _ := signer.Sign(key, time.Now())
	if got := do(t, h, http.MethodGet, u, "", nil); got.Code != http.StatusNotFound {
		t.Fatalf("anonymous signature granted storage cover access: %d", got.Code)
	}
	covers := &authorizedCoverStub{storageCoverStub: storageCoverStub{data: jpegCover}}
	h, signer = storageCoverHandler(t, covers)
	u, _ = signer.Sign(key, time.Now())
	got := do(t, h, http.MethodGet, u, "", nil)
	if got.Code != http.StatusOK || strings.Contains(got.Header().Get("Cache-Control"), "immutable") || !strings.Contains(got.Header().Get("Cache-Control"), "no-cache") {
		t.Fatalf("storage cover must revalidate: status=%d cache-control=%q", got.Code, got.Header().Get("Cache-Control"))
	}
}
