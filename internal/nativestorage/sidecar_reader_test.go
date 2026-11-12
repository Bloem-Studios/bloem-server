package nativestorage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

type sidecarRefsStub struct {
	*refsStub
	sidecar storagesource.Sidecar
	calls   int
}

func (s *sidecarRefsStub) FileSidecar(_ context.Context, fileID int, location uuid.UUID, entry string) (storagesource.Sidecar, error) {
	s.calls++
	if fileID != s.sidecar.MediaFileID || location != s.sidecar.LocationID || entry != s.sidecar.EntryID {
		return storagesource.Sidecar{}, storagesource.ErrReferenceConflict
	}
	return s.sidecar, nil
}

func sidecarFixture(t *testing.T) (*Coordinator, *sidecarRefsStub, *playbackSourceFixture, *models.MediaFile, PlaybackAuthority, storagesource.PersistedRef) {
	t.Helper()
	c, refs, _, _, source, file, authority := playbackFixture(t)
	pinned := storagesource.PersistedRef{LocationID: refs.ref.LocationID, EntryID: "movie.en.srt", Revision: "subtitle-revision", LogicalPath: "movie.en.srt"}
	sidecars := &sidecarRefsStub{refsStub: refs, sidecar: storagesource.Sidecar{MediaFileID: file.ID, PersistedRef: pinned, Name: "movie.en.srt", Format: "srt", Language: "en"}}
	c.References = sidecars
	source.data, source.path = []byte("1\n00:00:00,000 --> 00:00:01,000\nhi\n"), pinned.LogicalPath
	return c, sidecars, source, file, authority, pinned
}

func TestSidecarReadsPinnedEntryWithVideoAuthority(t *testing.T) {
	c, refs, source, file, authority, pinned := sidecarFixture(t)
	var authorized int
	authorizeFile := authority.AuthorizeFile
	authority.AuthorizeFile = func(ctx context.Context, f *models.MediaFile) (*models.MediaFile, error) {
		authorized++
		return authorizeFile(ctx, f)
	}
	opened, err := c.OpenAuthorizedSidecar(t.Context(), file, authority, pinned)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	identity := opened.Identity()
	if identity.FileID != file.ID || identity.Reference != pinned || identity.ProviderSourceID != refs.source.ProviderSourceID || identity.SourceKey != refs.source.Key {
		t.Fatalf("identity %+v", identity)
	}
	data, err := io.ReadAll(opened)
	if err != nil || !bytes.Equal(data, source.data) {
		t.Fatalf("read %q %v", data, err)
	}
	if authorized < 2 || refs.calls < 2 {
		t.Fatalf("authority checked %d times, sidecar %d times", authorized, refs.calls)
	}
}

func TestSidecarRefusesWithoutCurrentReference(t *testing.T) {
	for _, change := range []string{"no sidecar store", "revision", "other location", "logical path", "video policy"} {
		t.Run(change, func(t *testing.T) {
			c, refs, source, file, authority, pinned := sidecarFixture(t)
			switch change {
			case "no sidecar store":
				c.References = refs.refsStub
			case "revision":
				refs.sidecar.Revision = "newer-revision"
			case "other location":
				other := uuid.New()
				pinned.LocationID, refs.sidecar.LocationID = other, other
			case "logical path":
				source.path = "elsewhere.srt"
			case "video policy":
				authority.AuthorizeSource = func(context.Context, *models.MediaFile, storagesource.SourceConfig) error {
					return errors.New("policy revoked")
				}
			}
			stats := 0
			source.statHook = func() { stats++ }
			opened, err := c.OpenAuthorizedSidecar(t.Context(), file, authority, pinned)
			if err == nil {
				opened.Close()
				t.Fatal("sidecar admitted")
			}
			if change != "logical path" && stats != 0 {
				t.Fatalf("provider touched before refusal: %d", stats)
			}
		})
	}
}

func TestSidecarRechecksAroundProviderReads(t *testing.T) {
	for _, change := range []string{"sidecar revision", "sidecar detached", "video revision", "video policy"} {
		t.Run(change, func(t *testing.T) {
			c, refs, source, file, authority, pinned := sidecarFixture(t)
			revoked := false
			authorizeSource := authority.AuthorizeSource
			authority.AuthorizeSource = func(ctx context.Context, f *models.MediaFile, s storagesource.SourceConfig) error {
				if revoked {
					return errors.New("policy revoked")
				}
				return authorizeSource(ctx, f, s)
			}
			source.rangeHook = func(context.Context, int) {
				switch change {
				case "sidecar revision":
					refs.sidecar.Revision = "replaced"
				case "sidecar detached":
					refs.sidecar.MediaFileID = file.ID + 1
				case "video revision":
					refs.ref.Revision = "replaced"
				case "video policy":
					revoked = true
				}
			}
			opened, err := c.OpenAuthorizedSidecar(t.Context(), file, authority, pinned)
			if err != nil {
				t.Fatal(err)
			}
			defer opened.Close()
			data := bytes.Repeat([]byte{'?'}, len(source.data))
			n, err := opened.ReadAt(data, 0)
			if n != 0 || err == nil || !bytes.Equal(data, make([]byte, len(data))) {
				t.Fatalf("exposed bytes after %s: n=%d err=%v", change, n, err)
			}
		})
	}
}
