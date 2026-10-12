package nativestorage

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/Silo-Server/silo-server/internal/artworkkey"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/storageplugin"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
)

type revokingReadSource struct {
	*sourceStub
	after func()
}

func (s *revokingReadSource) ReadRange(ctx context.Context, r mediasource.Ref, offset, length int64, w io.Writer) error {
	err := s.sourceStub.ReadRange(ctx, r, offset, length, w)
	if s.after != nil {
		s.after()
	}
	return err
}

type readAuthorityRuntime struct{ lease Lease }

func (r readAuthorityRuntime) Borrow(context.Context, storageplugin.Snapshot) (Lease, error) {
	return r.lease, nil
}
func (readAuthorityRuntime) Disable(int) {}

func TestEbookAndCoverDiscardBytesAfterRootRevocation(t *testing.T) {
	for _, surface := range []string{"ebook", "cover"} {
		t.Run(surface, func(t *testing.T) {
			c, refs, _, run, file := fixture(t)
			source := &revokingReadSource{sourceStub: run.source, after: func() {
				refs.err = storagesource.ErrReferenceConflict
				refs.coverErr = storagesource.ErrReferenceConflict
			}}
			c.Runtime = readAuthorityRuntime{Lease{Context: run.ctx, Source: source, Description: run.description}}
			var opened mediasource.File
			var err error
			if surface == "ebook" {
				opened, err = c.OpenAuthorizedEbook(t.Context(), file)
			} else {
				opened, err = c.OpenCover(t.Context(), "book", artworkkey.StorageCoverRevision("cover/book", "cover:1"))
			}
			if err != nil {
				t.Fatal(err)
			}
			defer opened.Close()
			data := []byte("xxxx")
			n, err := opened.ReadAt(data, 0)
			if n != 0 || !errors.Is(err, storagesource.ErrReferenceConflict) || string(data) != "\x00\x00\x00\x00" {
				t.Fatalf("revoked %s bytes escaped: n=%d data=%q err=%v", surface, n, data, err)
			}
		})
	}
}

func TestCoverCacheCannotAuthorizeAnonymousRequest(t *testing.T) {
	c, _, _, _, _ := fixture(t)
	h := &Host{}
	h.SetCoverReader(c)
	h.SetCoverCache(&coverCacheStub{data: map[string][]byte{"bloem:storage-cover:book:cover-rev": []byte("book")}})
	data, err := h.ReadCover(t.Context(), "book", artworkkey.StorageCoverRevision("cover/book", "cover:1"))
	if data != nil || !errors.Is(err, storagesource.ErrReferenceConflict) {
		t.Fatalf("anonymous cache authority: data=%q err=%v", data, err)
	}
}

func TestCoverCacheHitRechecksPublication(t *testing.T) {
	c, refs, _, _, _ := fixture(t)
	h := &Host{}
	h.SetCoverReader(c)
	h.SetCoverCache(&coverCacheStub{data: map[string][]byte{}})
	ctx := WithCoverReadAuthority(t.Context(), refs.cover, func(ctx context.Context) (storagesource.CoverReference, error) {
		return refs.CoverReference(ctx, "book", artworkkey.StorageCoverRevision("cover/book", "cover:1"))
	})
	if data, err := h.ReadCover(ctx, "book", artworkkey.StorageCoverRevision("cover/book", "cover:1")); err != nil || string(data) != "book" {
		t.Fatalf("prime: %q %v", data, err)
	}
	refs.coverErr = storagesource.ErrReferenceConflict
	if data, err := h.ReadCover(ctx, "book", artworkkey.StorageCoverRevision("cover/book", "cover:1")); data != nil || !errors.Is(err, storagesource.ErrReferenceConflict) {
		t.Fatalf("revoked cache hit: %q %v", data, err)
	}
}

// lateRegistry runs a mutation after the nth registry lookup completes, so a
// test can commit a revocation during the final registry wait of one read.
type lateRegistry struct {
	snapshot *plugins.NativeStorageSnapshot
	calls    int
	at       int
	after    func()
}

func (s *lateRegistry) Snapshot(context.Context, uuid.UUID, uuid.UUID) (*plugins.NativeStorageSnapshot, error) {
	s.calls++
	result := clonePlaybackSnapshot(s.snapshot)
	if s.calls == s.at && s.after != nil {
		s.after()
	}
	return result, nil
}

// TestReviewTask8EbookRejectsRevocationDuringFinalRegistryWait keeps the Task8
// review proof: the last read check must finish its registry lookup before it
// repeats file, membership and viewer authority.
func TestReviewTask8EbookRejectsRevocationDuringFinalRegistryWait(t *testing.T) {
	for _, mutation := range []string{"root", "session"} {
		t.Run(mutation, func(t *testing.T) {
			c, refs, reg, _, file := fixture(t)
			revoked := false
			c.AuthorizeFile = func(context.Context, *models.MediaFile) (*models.MediaFile, error) {
				if revoked {
					return nil, storagesource.ErrReferenceConflict
				}
				return file, nil
			}
			late := &lateRegistry{snapshot: reg.snapshot, at: 4}
			c.Registry = late
			opened, err := c.OpenAuthorizedEbook(t.Context(), file)
			if err != nil {
				t.Fatal(err)
			}
			defer opened.Close()
			late.calls = 0
			late.after = func() {
				if mutation == "root" {
					refs.err = storagesource.ErrReferenceConflict
				} else {
					revoked = true
				}
			}
			data := []byte("xxxx")
			n, err := opened.ReadAt(data, 0)
			if late.calls != 4 {
				t.Fatalf("fixture did not reach final registry wait: calls=%d", late.calls)
			}
			if n != 0 || err == nil || string(data) != "\x00\x00\x00\x00" {
				t.Fatalf("revocation during final registry wait leaked ebook bytes: n=%d data=%q err=%v mutation=%s", n, data, err, mutation)
			}
		})
	}
}

// Covers follow the same rule: the final reader check and every cache-hit
// check finish the registry lookup before repeating library-source,
// publication and viewer authority.
func TestCoverRejectsRevocationDuringFinalRegistryWait(t *testing.T) {
	rev := artworkkey.StorageCoverRevision("cover/book", "cover:1")
	for _, mutation := range []string{"root", "library-source", "viewer"} {
		revoke := func(c *Coordinator, refs *refsStub, viewer *bool) {
			switch mutation {
			case "root":
				refs.coverErr = storagesource.ErrReferenceConflict
			case "library-source":
				c.AuthorizeCover = func(context.Context, storagesource.Location, storagesource.SourceConfig) error {
					return storagesource.ErrSourceUnavailable
				}
			case "viewer":
				*viewer = true
			}
		}
		authority := func(refs *refsStub, viewer *bool) context.Context {
			return WithCoverReadAuthority(t.Context(), refs.cover, func(ctx context.Context) (storagesource.CoverReference, error) {
				if *viewer {
					return storagesource.CoverReference{}, storagesource.ErrReferenceConflict
				}
				return refs.CoverReference(ctx, "book", rev)
			})
		}
		t.Run("reader/"+mutation, func(t *testing.T) {
			c, refs, reg, _, _ := fixture(t)
			late := &lateRegistry{snapshot: reg.snapshot, at: 4}
			c.Registry = late
			viewer := false
			opened, err := c.OpenCover(authority(refs, &viewer), "book", rev)
			if err != nil {
				t.Fatal(err)
			}
			defer opened.Close()
			late.calls = 0
			late.after = func() { revoke(c, refs, &viewer) }
			data := []byte("xxxx")
			n, err := opened.ReadAt(data, 0)
			if late.calls != 4 {
				t.Fatalf("fixture did not reach final registry wait: calls=%d", late.calls)
			}
			if n != 0 || err == nil || string(data) != "\x00\x00\x00\x00" {
				t.Fatalf("revocation during final registry wait leaked cover bytes: n=%d data=%q err=%v", n, data, err)
			}
		})
		t.Run("cache-hit/"+mutation, func(t *testing.T) {
			c, refs, reg, _, _ := fixture(t)
			late := &lateRegistry{snapshot: reg.snapshot}
			c.Registry = late
			h := &Host{}
			h.SetCoverReader(c)
			h.SetCoverCache(&coverCacheStub{data: map[string][]byte{}})
			viewer := false
			ctx := authority(refs, &viewer)
			if data, err := h.ReadCover(ctx, "book", rev); err != nil || string(data) != "book" {
				t.Fatalf("prime: %q %v", data, err)
			}
			// The cache-hit read resolves once, then rechecks: revoke during
			// that final registry lookup.
			late.calls, late.at = 0, 2
			late.after = func() { revoke(c, refs, &viewer) }
			data, err := h.ReadCover(ctx, "book", rev)
			if late.calls != 2 {
				t.Fatalf("fixture did not reach final registry wait: calls=%d", late.calls)
			}
			if data != nil || err == nil {
				t.Fatalf("revocation during cache-hit registry wait returned bytes: %q %v", data, err)
			}
		})
	}
}
