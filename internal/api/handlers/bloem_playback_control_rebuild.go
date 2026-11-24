package handlers

import (
	"context"
	"log/slog"
	"net/url"
	"time"

	evt "github.com/Silo-Server/silo-server/internal/events"
	"github.com/Silo-Server/silo-server/internal/playback"
)

// Bloem (temporary, meant for a Silo PR): a control ticket for a live session
// this API process does not hold rebuilds the session at once.
//
// An API process switch (every blue/green deploy moves the players twice)
// leaves the new process's session manager empty. Silo rebuilds a live session
// from the player's next media request, and until then the ticket mint answers
// ErrPlaybackControlSocketSessionResuming. A player that buffered ahead sends
// no media request for minutes (HLS) or for the rest of a direct play, so its
// control lane, and every administrator command that needs the lane, stays
// down while the player retries every Retry-After. Production showed 1,061 of
// 1,062 such refusals in four days were for sessions another process had
// already admitted.
//
// The rebuild uses the card that media request would bring: the stream token in
// the durable attempt's current plan (the URL the player follows), or, for a
// header-authenticated attempt, the API's stored recovery card. It runs only
// after Silo's mint read the attempt and found it live and the caller's, and
// only past the checks the media paths make before they rebuild, so the session
// is the one the player's next request would have rebuilt. Cards whose rebuild
// has to start the runtime with the session (tone-mapped or keyframe-planned
// transcodes) are left to the media path.
func (h *BloemPlaybackControlSocketV2) rebuildForControl(ctx context.Context, identity evt.SocketIdentity, sessionID string) bool {
	ph := h.Playback
	if ph == nil || ph.tm == nil || ph.sessionMgr == nil || ph.PlanStoreV3 == nil || identity.UserID == 0 {
		return false
	}
	if ph.StreamDeny.Denied(ctx, sessionID) {
		return false
	}
	record, err := ph.PlanStoreV3.GetAttempt(ctx, sessionID)
	if err != nil || record == nil || record.StoppedAt != nil || record.UserID != identity.UserID ||
		record.ProfileID == "" || record.ProfileID != identity.ProfileID ||
		(!record.ExpiresAt.IsZero() && time.Now().After(record.ExpiresAt)) {
		return false
	}
	card, err := bloemControlRecoveryCard(ctx, ph, record, sessionID, identity.UserID)
	if err != nil || card == nil || card.UserID != identity.UserID || (card.ProfileID != "" && card.ProfileID != record.ProfileID) {
		return false
	}
	if card.ToneMapMode != "" || card.KeyframePlaylist ||
		nativeAPIEgressStatusV3(card.RoutingWorkload, card.RoutingExecution, card.RoutingEgress) != 0 {
		return false
	}
	ph.withRecordedRenditionTracksV3(ctx, card)
	session, status, _ := ph.tm.LoadOrReconstructSessionDetail(ctx, ph.sessionMgr.GetSession, sessionID, identity.UserID, card)
	if status != playback.SessionLoaded || session == nil {
		return false
	}
	slog.InfoContext(ctx, "rebuilt a live playback session for its control lane",
		"component", "api", "playback_session_id", sessionID)
	return true
}

// bloemControlRecoveryCard is the card the session's next media request would
// rebuild from. A header-authenticated attempt has the API's stored card; any
// other v3 attempt's current plan carries the player's signed stream token.
func bloemControlRecoveryCard(ctx context.Context, ph *PlaybackHandler, record *playback.AttemptRecordV3, sessionID string, userID int) (*playback.RecipeCard, error) {
	if playback.HasFeatureV3(record.NormalizedRequest.ClientFeatures, playback.FeatureHeaderAuthenticatedMediaV3) {
		return ph.storedSessionRecipeV3(ctx, sessionID, userID)
	}
	// A URL that does not parse carries no token, so nothing is rebuilt.
	token := ""
	if streamURL, err := url.Parse(record.CurrentPlan.Stream.URL); err == nil {
		token = streamURL.Query().Get(streamTokenParam)
	}
	card, _ := verifiedStreamCardFromToken(token, sessionID, ph.JWTSecret)
	return card, nil
}
