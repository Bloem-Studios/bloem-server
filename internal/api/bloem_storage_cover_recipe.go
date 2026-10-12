package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/artworkkey"
	"github.com/Silo-Server/silo-server/internal/artworkurl"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

const coverRecipePurpose = "bloem/storage-cover-viewer/v1"
const coverRecipeTTL = 15 * time.Minute
const maxCoverRecipeBytes = 4096

// nativeCoverReferences lists a cover's current publications for issuance and
// redeems one exact file/root publication, never switching to another.
type nativeCoverReferences interface {
	CoverReferences(context.Context, string, string) ([]storagesource.CoverReference, error)
	CoverReferenceForFile(context.Context, string, string, int, uuid.UUID) (storagesource.CoverReference, error)
}

// Actor metadata reuses the host's credential checks without storing tokens or
// invoking video playback admission. The account incarnation also fences reused IDs.
type coverActor struct {
	UserID                                                                  int
	Incarnation                                                             uuid.UUID
	ProfileID, TokenType, LoginSessionID, AuthMethod                        string
	APIKeyID, APIKeyRevision, ProfilePINRevision, ProfileCredentialRevision int64
	Tenant                                                                  tenancy.Context
	ExpiresAt                                                               time.Time
}

func (a coverActor) credential() handlers.NativeVideoAuthority {
	return handlers.NativeVideoAuthority{UserID: a.UserID, ProfileID: a.ProfileID, TokenType: a.TokenType, LoginSessionID: a.LoginSessionID, AuthMethod: a.AuthMethod, APIKeyID: a.APIKeyID, APIKeyRevision: a.APIKeyRevision, ProfilePINRevision: a.ProfilePINRevision, ProfileCredentialRevision: a.ProfileCredentialRevision, Tenant: a.Tenant, ExpiresAt: a.ExpiresAt}
}

type coverRecipe struct {
	Version                              int
	Actor                                coverActor
	FileID, FolderID                     int
	LocationID, RootID                   uuid.UUID
	ContentID, Revision, ReferenceDigest string
	Generation                           uint64
	Checksum                             string
}

type nativeStorageArtwork struct {
	next          artworkurl.Resolver
	signer        *artworkurl.Signer
	cipher        *secret.Cipher
	actors        nativeVideoPolicy
	references    nativeCoverReferences
	registry      nativestorage.Registry
	authorizeFile func(context.Context, storagesource.CoverReference) error
	now           func() time.Time
}

func (s *nativeStorageArtwork) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}
func nativeCoverKey(key string) bool {
	return strings.HasPrefix(key, "storage-covers/") || strings.HasPrefix(key, artworkkey.StorageCoverScheme+":")
}
func (s *nativeStorageArtwork) ResolveURLs(ctx context.Context, keys []string) map[string]catalog.ResolvedImageURL {
	out := make(map[string]catalog.ResolvedImageURL, len(keys))
	ordinary := make([]string, 0, len(keys))
	for _, key := range keys {
		if nativeCoverKey(key) {
			if resolved, ok := s.ResolveURLFor(ctx, key, 0); ok {
				out[key] = resolved
			}
		} else {
			ordinary = append(ordinary, key)
		}
	}
	if s.next != nil {
		for key, value := range s.next.ResolveURLs(ctx, ordinary) {
			out[key] = value
		}
	}
	return out
}
func (s *nativeStorageArtwork) ResolveURLFor(ctx context.Context, key string, ttl time.Duration) (catalog.ResolvedImageURL, bool) {
	if !nativeCoverKey(key) {
		return artworkurl.ResolveURLFor(ctx, s.next, key, ttl)
	}
	empty := catalog.ResolvedImageURL{}
	key = strings.TrimPrefix(key, artworkkey.StorageCoverScheme+"://")
	contentID, revision, ok := artworkkey.ParseStorageCoverKey(key)
	if !ok || s.signer == nil || s.cipher == nil || s.references == nil || s.registry == nil || s.authorizeFile == nil {
		return empty, false
	}
	actor, err := s.captureActor(ctx)
	if err != nil {
		return empty, false
	}
	if ttl > 0 && ttl < coverRecipeTTL {
		actor.ExpiresAt = minCoverExpiry(actor.ExpiresAt, s.clock().Add(ttl))
	}
	recipe, ok := s.issueRecipe(ctx, actor, contentID, revision)
	if !ok {
		return empty, false
	}
	// Repeat original credential and PIN verification after sealing metadata:
	// a PIN rotated during issuance must never create a new proof for the old PIN.
	if _, err = freshNativeStorageViewer(ctx, s.actors); err != nil {
		return empty, false
	}
	data, err := json.Marshal(recipe)
	if err != nil {
		return empty, false
	}
	sealed, err := s.cipher.Encrypt(string(data), coverRecipePurpose+":"+key)
	if err != nil || len(sealed) > maxCoverRecipeBytes {
		return empty, false
	}
	raw, _ := s.signer.SignFor(key, s.clock(), recipe.Actor.ExpiresAt.Sub(s.clock()))
	exp := recipe.Actor.ExpiresAt
	return catalog.ResolvedImageURL{URL: raw + "&recipe=" + url.QueryEscape(sealed), ExpiresAt: &exp}, true
}

// issueRecipe pins the first current publication, in file/root order, that the
// issuing viewer may read. The same source and cover can be published into
// several libraries; a viewer limited to one of them still gets its cover.
// Redemption remains strict to the chosen file and root.
func (s *nativeStorageArtwork) issueRecipe(ctx context.Context, actor coverActor, contentID, revision string) (coverRecipe, bool) {
	candidates, err := s.references.CoverReferences(ctx, contentID, revision)
	if err != nil {
		return coverRecipe{}, false
	}
	snapshots := make(map[uuid.UUID]*plugins.NativeStorageSnapshot)
	for _, ref := range candidates {
		snapshot, seen := snapshots[ref.Source.Key]
		if !seen {
			snapshot, err = s.registry.Snapshot(ctx, ref.Source.Key, ref.Source.OwnerID)
			if err != nil {
				snapshot = nil
			}
			snapshots[ref.Source.Key] = snapshot
		}
		if snapshot == nil || snapshot.Installation == nil || !nativestorage.SameSource(ref.Source, snapshot.Source) {
			continue
		}
		recipe := coverRecipe{Version: 1, Actor: actor, FileID: ref.MediaFileID, FolderID: ref.FolderID, LocationID: ref.Location.ID, RootID: ref.RootID, ContentID: contentID, Revision: revision, ReferenceDigest: coverReferenceDigest(ref), Generation: snapshot.Generation, Checksum: snapshot.ArtifactChecksum}
		if _, err = s.authorize(ctx, recipe); err == nil {
			return recipe, true
		}
		if ctx.Err() != nil {
			return coverRecipe{}, false
		}
	}
	return coverRecipe{}, false
}

func minCoverExpiry(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
func coverReferenceDigest(ref storagesource.CoverReference) string {
	data, _ := json.Marshal(ref)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// freshNativeStorageViewer repeats the owning credential, tenant and PIN policy
// from request memory. It grants artwork/ebook access without video admission.
func freshNativeStorageViewer(ctx context.Context, p nativeVideoPolicy) (context.Context, error) {
	request, ok := ctx.Value(nativeVideoRequestKey{}).(nativeVideoRequest)
	claims := middleware.GetClaims(ctx)
	scope, hasScope := access.GetScope(ctx)
	if !ok || claims == nil || !hasScope || scope.UserID <= 0 || scope.UserID != claims.UserID || p.credentials == nil || p.viewer == nil || p.tenants == nil {
		return nil, middleware.ErrCurrentCredentialInvalid
	}
	if claims.TokenType != auth.TokenTypeAccess && claims.TokenType != auth.TokenTypeAPIKey {
		return nil, middleware.ErrCurrentCredentialForbidden
	}
	if claims.ProfileID != "" && claims.ProfileID != scope.ProfileID {
		return nil, middleware.ErrCurrentCredentialInvalid
	}
	if err := p.credentials.RevalidateCurrent(ctx, request.authorization, claims, request.method, request.path); err != nil {
		return nil, err
	}
	tenant, err := p.tenants.ResolveSubjectTenant(ctx, claims.UserID, scope.ProfileID)
	if err != nil {
		return nil, err
	}
	captured, ok := tenancy.FromContext(ctx)
	if !ok {
		return nil, middleware.ErrCurrentCredentialInvalid
	}
	tenant.Legacy = captured.Legacy
	if tenant != captured {
		return nil, middleware.ErrCurrentCredentialInvalid
	}
	ctx = tenancy.WithContext(ctx, tenant)
	skip := claims.TokenType == auth.TokenTypeAPIKey || claims.AuthMethod == auth.AuthMethodDirectProfile
	fresh, err := p.viewer.ResolveCurrent(ctx, access.ResolveInput{UserID: claims.UserID, ProfileID: scope.ProfileID, SessionID: claims.SessionID, ProfileToken: request.profileToken, SkipPINVerification: skip})
	if err != nil {
		return nil, err
	}
	if fresh.UserID != scope.UserID || fresh.ProfileID != scope.ProfileID || scope.ProfileID != "" && !fresh.ProfileVerified {
		return nil, middleware.ErrCurrentCredentialInvalid
	}
	ctx = access.SetScope(ctx, fresh)
	ctx = middleware.SetProfileID(ctx, fresh.ProfileID)
	return ctx, ctx.Err()
}
func (s *nativeStorageArtwork) captureActor(ctx context.Context) (coverActor, error) {
	empty := coverActor{}
	fresh, err := freshNativeStorageViewer(ctx, s.actors)
	if err != nil {
		return empty, err
	}
	claims := middleware.GetClaims(fresh)
	scope, _ := access.GetScope(fresh)
	tenant, _ := tenancy.FromContext(fresh)
	if s.actors.loadUser == nil {
		return empty, middleware.ErrCurrentCredentialInvalid
	}
	user, err := s.actors.loadUser(fresh, claims.UserID)
	if err != nil || user == nil || !user.Enabled || user.AccountIncarnationID == uuid.Nil {
		return empty, middleware.ErrCurrentCredentialInvalid
	}
	if claims.AccountIncarnationID != "" && claims.AccountIncarnationID != user.AccountIncarnationID.String() {
		return empty, middleware.ErrCurrentCredentialInvalid
	}
	a := coverActor{UserID: claims.UserID, Incarnation: user.AccountIncarnationID, ProfileID: scope.ProfileID, TokenType: claims.TokenType, LoginSessionID: claims.SessionID, AuthMethod: claims.AuthMethod, Tenant: tenant, ExpiresAt: s.clock().Add(coverRecipeTTL)}
	if claims.TokenType == auth.TokenTypeAccess {
		if claims.ExpiresAt == nil || s.actors.loadLogin == nil {
			return empty, middleware.ErrCurrentCredentialInvalid
		}
		a.ExpiresAt = minCoverExpiry(a.ExpiresAt, claims.ExpiresAt.Time)
		login, err := s.actors.loadLogin(fresh, claims.SessionID)
		if err != nil || login == nil {
			return empty, middleware.ErrCurrentCredentialInvalid
		}
		a.ExpiresAt = minCoverExpiry(a.ExpiresAt, login.ExpiresAt)
	} else {
		if s.actors.loadAPIKeyMetadata == nil {
			return empty, middleware.ErrCurrentCredentialInvalid
		}
		key, err := s.actors.loadAPIKeyMetadata(fresh, claims.APIKeyID)
		if err != nil || key == nil {
			return empty, middleware.ErrCurrentCredentialInvalid
		}
		a.APIKeyID = key.ID
		a.APIKeyRevision = key.Revision
	}
	if a.ProfileID != "" {
		if s.actors.loadProfile == nil {
			return empty, middleware.ErrCurrentCredentialInvalid
		}
		profile, err := s.actors.loadProfile(fresh, a.UserID, a.ProfileID)
		if err != nil || profile == nil {
			return empty, middleware.ErrCurrentCredentialInvalid
		}
		a.ProfilePINRevision = profile.PINRevision
		a.ProfileCredentialRevision = profile.CredentialRevision
	}
	return a, nil
}

// authorize redeems a recipe against current evidence in two passes. The first
// resolves the actor, then checks publication, library, source and runtime. The
// final pass repeats them in wait order: catalog and resource lookups first,
// then the runtime registry, then the actor, its access filter and the recipe
// expiry. A session, credential, profile, library-scope, runtime or expiry
// change committed during any final catalog, resource or registry wait is
// therefore seen before a 304 or bytes; publication evidence is current as of
// the final catalog read.
func (s *nativeStorageArtwork) authorize(ctx context.Context, r coverRecipe) (storagesource.CoverReference, error) {
	empty := storagesource.CoverReference{}
	if r.Version != 1 || r.FileID <= 0 || r.FolderID <= 0 || r.LocationID == uuid.Nil || r.RootID == uuid.Nil || r.Actor.Incarnation == uuid.Nil || !s.clock().Before(r.Actor.ExpiresAt) || s.actors.loadUser == nil || s.actors.viewer == nil || s.actors.tenants == nil || s.actors.authorizeSource == nil || s.references == nil || s.registry == nil || s.authorizeFile == nil {
		return empty, storagesource.ErrReferenceConflict
	}
	viewerCtx, err := s.currentCoverViewer(ctx, r.Actor)
	if err != nil {
		return empty, err
	}
	ref, err := s.authorizePublication(viewerCtx, r)
	if err != nil {
		return empty, err
	}
	if err = s.checkRecipeRuntime(viewerCtx, ref, r); err != nil {
		return empty, err
	}

	final, err := s.authorizePublication(viewerCtx, r)
	if err != nil {
		return empty, err
	}
	if !nativestorage.SameCoverReference(ref, final) {
		return empty, storagesource.ErrReferenceConflict
	}
	if err = s.checkRecipeRuntime(viewerCtx, final, r); err != nil {
		return empty, err
	}
	finalCtx, err := s.currentCoverViewer(ctx, r.Actor)
	if err != nil {
		return empty, err
	}
	// The file decision above used the first scope; it stands only while the
	// viewer's current access filter is unchanged.
	if !reflect.DeepEqual(handlers.AccessFilterFromContext(viewerCtx, ""), handlers.AccessFilterFromContext(finalCtx, "")) || !s.clock().Before(r.Actor.ExpiresAt) {
		return empty, storagesource.ErrReferenceConflict
	}
	return final, finalCtx.Err()
}

// authorizePublication reads the exact pinned publication and checks the
// viewer's library and shared-source authority for it.
func (s *nativeStorageArtwork) authorizePublication(ctx context.Context, r coverRecipe) (storagesource.CoverReference, error) {
	empty := storagesource.CoverReference{}
	ref, err := s.references.CoverReferenceForFile(ctx, r.ContentID, r.Revision, r.FileID, r.RootID)
	if err != nil {
		return empty, err
	}
	if ref.FolderID != r.FolderID || ref.Location.ID != r.LocationID || coverReferenceDigest(ref) != r.ReferenceDigest || artworkkey.StorageCoverRevision(ref.Cover.EntryID, ref.Cover.Revision) != r.Revision {
		return empty, storagesource.ErrReferenceConflict
	}
	if err = s.authorizeFile(ctx, ref); err != nil {
		return empty, err
	}
	if err = s.actors.authorizeSource(ctx, &models.MediaFile{ID: ref.MediaFileID, ContentID: ref.ContentID, MediaFolderID: ref.FolderID, FilePath: ref.FilePath}, ref.Source); err != nil {
		return empty, err
	}
	return ref, nil
}

// checkRecipeRuntime requires the approved runtime generation and artifact the
// recipe was issued against.
func (s *nativeStorageArtwork) checkRecipeRuntime(ctx context.Context, ref storagesource.CoverReference, r coverRecipe) error {
	snapshot, err := s.registry.Snapshot(ctx, ref.Source.Key, ref.Source.OwnerID)
	if err != nil {
		return err
	}
	if snapshot == nil || snapshot.Installation == nil || !snapshot.Installation.Enabled || !nativestorage.SameSource(ref.Source, snapshot.Source) || snapshot.Generation != r.Generation || snapshot.ArtifactChecksum != r.Checksum {
		return storagesource.ErrReferenceConflict
	}
	return nil
}

// currentCoverViewer re-resolves an attested actor, including incarnation,
// session/profile revisions and current tenant/viewer policy, without tokens.
func (s *nativeStorageArtwork) currentCoverViewer(ctx context.Context, actor coverActor) (context.Context, error) {
	if err := verifyNativeVideoActor(ctx, actor.credential(), s.actors); err != nil {
		return nil, err
	}
	user, err := s.actors.loadUser(ctx, actor.UserID)
	if err != nil || user == nil || user.AccountIncarnationID != actor.Incarnation {
		return nil, storagesource.ErrReferenceConflict
	}
	tenant, err := s.actors.tenants.ResolveSubjectTenant(ctx, actor.UserID, actor.ProfileID)
	if err != nil {
		return nil, err
	}
	tenant.Legacy = actor.Tenant.Legacy
	if tenant != actor.Tenant {
		return nil, storagesource.ErrReferenceConflict
	}
	ctx = tenancy.WithContext(ctx, tenant)
	ctx = middleware.SetClaims(ctx, &auth.Claims{UserID: actor.UserID, ProfileID: actor.ProfileID})
	scope, err := s.actors.viewer.ResolveCurrent(ctx, access.ResolveInput{UserID: actor.UserID, ProfileID: actor.ProfileID, SessionID: actor.LoginSessionID, SkipPINVerification: true})
	if err != nil {
		return nil, err
	}
	if scope.UserID != actor.UserID || scope.ProfileID != actor.ProfileID {
		return nil, storagesource.ErrReferenceConflict
	}
	ctx = middleware.SetProfileID(access.SetScope(ctx, scope), scope.ProfileID)
	return ctx, ctx.Err()
}

func (s *nativeStorageArtwork) authorizeRequest(ctx context.Context, request *http.Request, contentID, revision string) (context.Context, error) {
	if s == nil || s.cipher == nil || request == nil {
		return nil, storagesource.ErrReferenceConflict
	}
	values := request.URL.Query()["recipe"]
	if len(values) != 1 || len(values[0]) == 0 || len(values[0]) > maxCoverRecipeBytes {
		return nil, storagesource.ErrReferenceConflict
	}
	key := "storage-covers/" + contentID + "/original." + revision + ".img"
	data, err := s.cipher.Decrypt(values[0], coverRecipePurpose+":"+key)
	if err != nil {
		return nil, storagesource.ErrReferenceConflict
	}
	var recipe coverRecipe
	if json.Unmarshal([]byte(data), &recipe) != nil || recipe.ContentID != contentID || recipe.Revision != revision {
		return nil, storagesource.ErrReferenceConflict
	}
	ref, err := s.authorize(ctx, recipe)
	if err != nil {
		return nil, storagesource.ErrReferenceConflict
	}
	resolve := func(ctx context.Context) (storagesource.CoverReference, error) {
		ref, err := s.authorize(ctx, recipe)
		if err != nil {
			return storagesource.CoverReference{}, storagesource.ErrReferenceConflict
		}
		return ref, nil
	}
	return nativestorage.WithCoverReadAuthority(ctx, ref, resolve), nil
}
