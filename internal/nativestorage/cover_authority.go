package nativestorage

import (
	"context"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/artworkkey"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

// CoverRequestAuthorizer redeems a viewer-bound recipe using the host's current
// identity, profile, library and resource policy. It returns a read context;
// an artwork signature alone must never create one.
type CoverRequestAuthorizer func(context.Context, *http.Request, string, string) (context.Context, error)

type coverAuthorityKey struct{}
type coverReadAuthority struct {
	reference storagesource.CoverReference
	resolve   func(context.Context) (storagesource.CoverReference, error)
}

// WithCoverReadAuthority retains a pinned publication and a fresh authorizer.
// The callback is request memory only; neither credentials nor access decisions
// belong in the bytes cache.
func WithCoverReadAuthority(ctx context.Context, ref storagesource.CoverReference, resolve func(context.Context) (storagesource.CoverReference, error)) context.Context {
	ref.Source = clonePlaybackSource(ref.Source)
	return context.WithValue(ctx, coverAuthorityKey{}, coverReadAuthority{reference: ref, resolve: resolve})
}

func SameCoverReference(a, b storagesource.CoverReference) bool {
	return a.MediaFileID == b.MediaFileID && a.FolderID == b.FolderID && a.ContentID == b.ContentID && a.FilePath == b.FilePath && a.Location == b.Location && a.Cover == b.Cover && a.File == b.File && a.RootID == b.RootID && SameSource(a.Source, b.Source)
}

func resolveCoverRead(ctx context.Context, refs References, contentID, revision string) (storagesource.CoverReference, error) {
	if authority, ok := ctx.Value(coverAuthorityKey{}).(coverReadAuthority); ok {
		if authority.resolve == nil || authority.reference.ContentID != contentID || artworkkey.StorageCoverRevision(authority.reference.Cover.EntryID, authority.reference.Cover.Revision) != revision {
			return storagesource.CoverReference{}, storagesource.ErrReferenceConflict
		}
		fresh, err := authority.resolve(ctx)
		if err != nil {
			return storagesource.CoverReference{}, err
		}
		if !SameCoverReference(authority.reference, fresh) {
			return storagesource.CoverReference{}, storagesource.ErrReferenceConflict
		}
		return fresh, nil
	}
	return refs.CoverReference(ctx, contentID, revision)
}
