package apiv2

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/artworkkey"
	"github.com/Silo-Server/silo-server/internal/artworkurl"
	"github.com/Silo-Server/silo-server/internal/blobstore"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type storageCoverStub struct {
	data  []byte
	err   error
	reads []string
}

func (s *storageCoverStub) ReadCover(_ context.Context, contentID, revision string) ([]byte, error) {
	s.reads = append(s.reads, contentID+"/"+revision)
	return s.data, s.err
}

func (*storageCoverStub) AuthorizeCoverRequest(context.Context, *http.Request, string, string) error {
	return nil
}

// A JPEG signature is enough for content sniffing.
var jpegCover = append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, bytes.Repeat([]byte("cover"), 100)...)

func storageCoverHandler(t *testing.T, covers StorageCoverService) (http.Handler, *artworkurl.Signer) {
	t.Helper()
	store, err := blobstore.NewFilesystem(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	signer := artworkurl.NewSigner("test-secret", time.Hour)
	deps := Dependencies{ArtworkStore: store, ArtworkSigner: signer}
	deps.StorageCovers = covers
	return NewHandler(deps), signer
}

func TestArtworkServesStorageCoversThroughTheirSource(t *testing.T) {
	covers := &storageCoverStub{data: jpegCover}
	h, signer := storageCoverHandler(t, covers)
	key := artworkkey.StorageCoverKey("146532612416483348", "cover/book-1", "cover:abc")
	u, _ := signer.Sign(key, time.Now())
	got := do(t, h, http.MethodGet, u, "", nil)
	if got.Code != 200 || !bytes.Equal(got.Body.Bytes(), jpegCover) || got.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("GET: %d %q", got.Code, got.Header().Get("Content-Type"))
	}
	revision := artworkkey.StorageCoverRevision("cover/book-1", "cover:abc")
	if len(covers.reads) != 1 || covers.reads[0] != "146532612416483348/"+revision {
		t.Fatalf("reads = %v", covers.reads)
	}
	if etag := got.Header().Get("ETag"); etag != `"`+revision+`"` {
		t.Fatalf("etag = %q", etag)
	}
	// An authorized revalidation does not read the plugin's cover bytes.
	cached := do(t, h, http.MethodGet, u, "", map[string]string{"If-None-Match": got.Header().Get("ETag")})
	if cached.Code != 304 || len(covers.reads) != 1 {
		t.Fatalf("conditional: %d, reads = %d", cached.Code, len(covers.reads))
	}
	// Unsigned or tampered URLs never reach the source.
	if got := do(t, h, http.MethodGet, u+"x", "", nil); got.Code != 404 || len(covers.reads) != 1 {
		t.Fatalf("tampered: %d, reads = %d", got.Code, len(covers.reads))
	}
}

func TestArtworkStorageCoverFailures(t *testing.T) {
	key := artworkkey.StorageCoverKey("146532612416483348", "cover/book-1", "cover:abc")
	for name, tc := range map[string]struct {
		covers *storageCoverStub
		status int
	}{
		"changed cover":     {&storageCoverStub{err: storagesource.ErrReferenceConflict}, 404},
		"disabled source":   {&storageCoverStub{err: storagesource.ErrSourceUnavailable}, 404},
		"removed by plugin": {&storageCoverStub{err: status.Error(codes.NotFound, "entry absent")}, 404},
		"provider down":     {&storageCoverStub{err: status.Error(codes.Unavailable, "down")}, 503},
		"not an image":      {&storageCoverStub{data: []byte("<html>")}, 404},
	} {
		t.Run(name, func(t *testing.T) {
			h, signer := storageCoverHandler(t, tc.covers)
			u, _ := signer.Sign(key, time.Now())
			got := do(t, h, http.MethodGet, u, "", nil)
			if got.Code != tc.status || got.Header().Get("ETag") != "" {
				t.Fatalf("status = %d, etag = %q", got.Code, got.Header().Get("ETag"))
			}
		})
	}
	// Without a storage runtime the cover is absent.
	h, signer := storageCoverHandler(t, nil)
	u, _ := signer.Sign(key, time.Now())
	if got := do(t, h, http.MethodGet, u, "", nil); got.Code != 404 {
		t.Fatalf("no runtime: %d", got.Code)
	}
}

// A cover read once is kept in artwork storage and served from there: its
// signed URL is the authority, and the source is not read or asked again.
func TestArtworkKeepsAStorageCoverAfterItsFirstRead(t *testing.T) {
	covers := &authorizedCoverStub{storageCoverStub: storageCoverStub{data: jpegCover}}
	h, signer := storageCoverHandler(t, covers)
	key := artworkkey.StorageCoverKey("146532612416483348", "cover/book-1", "cover:abc")
	u, _ := signer.Sign(key, time.Now())
	first := do(t, h, http.MethodGet, u, "", nil)
	if first.Code != 200 || len(covers.reads) != 1 || covers.checks != 2 {
		t.Fatalf("first: %d reads=%d checks=%d", first.Code, len(covers.reads), covers.checks)
	}
	second := do(t, h, http.MethodGet, u, "", nil)
	if second.Code != 200 || !bytes.Equal(second.Body.Bytes(), jpegCover) || second.Header().Get("Content-Type") != "image/jpeg" ||
		!strings.Contains(second.Header().Get("Cache-Control"), "immutable") || second.Header().Get("ETag") != first.Header().Get("ETag") {
		t.Fatalf("kept: %d %q %q", second.Code, second.Header().Get("Content-Type"), second.Header().Get("Cache-Control"))
	}
	if len(covers.reads) != 1 || covers.checks != 2 {
		t.Fatalf("kept cover read the source again: reads=%d checks=%d", len(covers.reads), covers.checks)
	}
	if got := do(t, h, http.MethodGet, u, "", map[string]string{"If-None-Match": first.Header().Get("ETag")}); got.Code != 304 {
		t.Fatalf("conditional: %d", got.Code)
	}
	// A tampered URL is refused before artwork storage is read.
	if got := do(t, h, http.MethodGet, u+"x", "", nil); got.Code != 404 {
		t.Fatalf("tampered: %d", got.Code)
	}
}

// A cover the viewer may not see is neither served nor kept.
func TestArtworkDoesNotKeepADeniedStorageCover(t *testing.T) {
	for _, denyAt := range []int{1, 2} {
		covers := &authorizedCoverStub{storageCoverStub: storageCoverStub{data: jpegCover}, denyAt: denyAt}
		h, signer := storageCoverHandler(t, covers)
		key := artworkkey.StorageCoverKey("146532612416483348", "cover/book-1", "cover:abc")
		u, _ := signer.Sign(key, time.Now())
		if got := do(t, h, http.MethodGet, u, "", nil); got.Code != 404 {
			t.Fatalf("denyAt %d: %d", denyAt, got.Code)
		}
		covers.denyAt = 0
		before := len(covers.reads)
		if got := do(t, h, http.MethodGet, u, "", nil); got.Code != 200 || len(covers.reads) != before+1 {
			t.Fatalf("denyAt %d: cover was kept after a denial: %d reads=%d", denyAt, got.Code, len(covers.reads)-before)
		}
	}
}
