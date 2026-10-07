package apiv2

import (
	"bytes"
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/artworkkey"
	"github.com/Silo-Server/silo-server/internal/artworkurl"
	"github.com/Silo-Server/silo-server/internal/blobstore"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type coverFile struct {
	*bytes.Reader
	size int64
}

func (f coverFile) Info() mediasource.Info { return mediasource.Info{Size: f.size} }
func (coverFile) Close() error             { return nil }

type storageCoverStub struct {
	data  []byte
	err   error
	opens []string
}

func (s *storageCoverStub) OpenCover(_ context.Context, contentID, revision string) (mediasource.File, error) {
	s.opens = append(s.opens, contentID+"/"+revision)
	if s.err != nil {
		return nil, s.err
	}
	return coverFile{Reader: bytes.NewReader(s.data), size: int64(len(s.data))}, nil
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
	return NewHandler(Dependencies{ArtworkStore: store, ArtworkSigner: signer, StorageCovers: covers}), signer
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
	if len(covers.opens) != 1 || covers.opens[0] != "146532612416483348/"+revision {
		t.Fatalf("opens = %v", covers.opens)
	}
	if etag := got.Header().Get("ETag"); etag != `"`+revision+`"` {
		t.Fatalf("etag = %q", etag)
	}
	// A revalidation is answered from the key alone, without the plugin.
	cached := do(t, h, http.MethodGet, u, "", map[string]string{"If-None-Match": got.Header().Get("ETag")})
	if cached.Code != 304 || len(covers.opens) != 1 {
		t.Fatalf("conditional: %d, opens = %d", cached.Code, len(covers.opens))
	}
	// Unsigned or tampered URLs never reach the source.
	if got := do(t, h, http.MethodGet, u+"x", "", nil); got.Code != 404 || len(covers.opens) != 1 {
		t.Fatalf("tampered: %d, opens = %d", got.Code, len(covers.opens))
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
