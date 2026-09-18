package api

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

// Only the native HLS delivery route accepts stream tokens. The shared verifier
// supplies signature, expiry and session-path binding; this adapter additionally
// requires the Live TV purpose and a profile owner. Operational routes never
// receive the stream-token authorization marker.
func bloemLiveTVStreamTokens(auth *apimw.AuthMiddleware, secret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		verified := auth.StreamTokenAuth(secret)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := apimw.StreamTokenClaims(r.Context())
			// streamtoken.Sign emits a purpose, not a JWT audience. Refuse
			// envelopes minted for another audience as well as other purposes.
			if !ok || claims.PlayMethod != handlers.BloemLiveHLSStreamPurpose || len(claims.Audience) != 0 || claims.UserID <= 0 || claims.ProfileID == "" || claims.ExpiresAt == nil {
				http.Error(w, "Valid Live TV delivery ticket required", http.StatusUnauthorized)
				return
			}
			// Playlists and registered API peers need only the delivery ticket.
			// Never propagate caller-supplied account/profile query credentials.
			clone := r.Clone(r.Context())
			clone.URL.RawQuery = url.Values{streamtoken.QueryParam: {r.URL.Query().Get(streamtoken.QueryParam)}}.Encode()
			next.ServeHTTP(w, clone)
		}))
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, NativeAPIPrefix+"/livetv/live-hls/") {
				next.ServeHTTP(w, r)
				return
			}
			if !isBloemLiveTVDelivery(r) {
				http.Error(w, "Invalid Live TV delivery path", http.StatusUnauthorized)
				return
			}
			verified.ServeHTTP(w, r)
		})
	}
}

func isBloemLiveTVDelivery(r *http.Request) bool {
	const prefix = NativeAPIPrefix + "/livetv/live-hls/"
	if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, prefix) || r.URL.EscapedPath() != r.URL.Path {
		return false
	}
	id, name, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, prefix), "/")
	return ok && id != "" && id != "." && id != ".." && !strings.ContainsAny(name, "/\\") &&
		(name == "index.m3u8" || strings.HasPrefix(name, "seg_") && strings.HasSuffix(name, ".ts"))
}

// The regular profile gate is unchanged for normal requests. A headerless HLS
// fetch instead uses the profile freshly resolved from its verified ticket.
func requireBloemLiveTVProfile(next http.Handler) http.Handler {
	regular := apimw.RequireProfile(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if apimw.IsStreamTokenAuthorized(r.Context()) {
			claims, ok := apimw.StreamTokenClaims(r.Context())
			userID, profileID, resolved := apimw.StreamTokenViewer(r)
			if !isBloemLiveTVDelivery(r) || !ok || !resolved || claims.PlayMethod != handlers.BloemLiveHLSStreamPurpose || profileID == "" || userID != claims.UserID || profileID != claims.ProfileID {
				http.Error(w, "Live TV viewer could not be resolved", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		regular.ServeHTTP(w, r)
	})
}

// Read household authority from the same account-owned profile store used by
// the acting-admin gate. An absent, foreign or child profile never gets the
// cross-viewer override; LiveTVHandler separately requires the admin role.
func bloemLiveTVPrimaryProfileChecker(stores userstore.UserStoreProvider) apimw.PrimaryProfileChecker {
	return func(ctx context.Context, accountID int, profileID string) (bool, bool, error) {
		store, err := stores.ForUser(ctx, accountID)
		if err != nil {
			return false, false, err
		}
		profile, err := store.GetProfile(ctx, profileID)
		if err != nil {
			return false, false, err
		}
		if profile == nil {
			return false, false, nil
		}
		return profile.IsPrimary && !profile.IsChild, true, nil
	}
}
