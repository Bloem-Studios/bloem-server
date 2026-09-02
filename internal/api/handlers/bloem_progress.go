package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

// Per-item sync result statuses on POST /api/bloem/v1/sync/progress. The client
// contract allows exactly these three values, and the reason it does is that a
// client which cannot tell a landed write from a discarded one stops resending
// a position the server never stored — the one failure the sync protocol
// exists to prevent.
//
// The Silo-compatible projection at POST /api/v1/sync/progress keeps reporting
// "ok" for every non-error row. That value is what Silo clients parse, so it
// cannot change under them; the finer vocabulary is a native-API feature and
// clients detect it through the progress_sync_v1 token on
// GET /api/bloem/v1/capabilities.
const (
	// syncStatusUpdated means the row was written.
	syncStatusUpdated = "updated"
	// syncStatusIgnored means the row was accepted but not written: the
	// min-resume floor discarded it, or a newer stored event won
	// last-write-wins. Nothing is wrong and the client must not retry it as an
	// error.
	syncStatusIgnored = "ignored"
	// syncStatusError means the item was rejected; the error field carries the
	// reason.
	syncStatusError = "error"
	// syncErrMissingMediaItemID is the per-item rejection message. It is the
	// same wording POST /api/v1/sync/progress returns, because it is the same
	// rejection: a client that flushes one queue to both surfaces must not have
	// to branch on which one answered.
	syncErrMissingMediaItemID = "media_item_id is required"
)

// HandleBloemSyncProgress handles POST /api/bloem/v1/sync/progress: the same batch of
// progress updates the v1 route accepts, reported in the contract vocabulary.
//
// Everything that touches storage is shared with the v1 handler — the same
// store, the same thresholds, the same last-write-wins merge, the same profile
// refresh and event fan-out. Only the per-item reporting differs, so the two
// surfaces cannot drift into writing different rows for the same request.
func (h *ProgressHandler) HandleBloemSyncProgress(w http.ResponseWriter, r *http.Request) {
	userID := apimw.GetUserID(r.Context())
	profileID := apimw.GetProfileID(r.Context())

	var req syncProgressRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}
	if len(req.Items) == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "At least one progress item is required")
		return
	}

	store, err := h.storeProvider.ForUser(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to access user store")
		return
	}

	thresholds := h.progressThresholds(r.Context())

	results := make([]syncProgressResultItem, 0, len(req.Items))
	processedAnyItem := false

	for _, item := range req.Items {
		result := syncProgressResultItem{MediaItemID: item.MediaItemID}

		if item.MediaItemID == "" {
			result.Status = syncStatusError
			result.Error = syncErrMissingMediaItemID
			results = append(results, result)
			continue
		}

		// Resolve the min-resume floor up front so the response can name a
		// discarded row. Every write path below applies the same rule
		// internally (the stores call ResolveProgressState too); this is the
		// pure classification, not a second copy of the threshold logic, and it
		// does not change which store call runs.
		pos, completed, skip := userstore.ResolveProgressState(item.Position, item.Duration, thresholds)
		// applied stays true for the paths whose store call reports no
		// applied/not-applied signal: reaching them without an error means the
		// write landed.
		applied := true

		var updateErr error
		switch {
		case item.UpdatedAt != nil:
			// Offline-queued event: clamp the client event time and merge
			// last-write-wins on the bounded event_at. synced_seq (the cursor)
			// is stamped server-side; completion still comes from the threshold
			// logic, never the timestamp alone.
			client, parseErr := parseClientEventTime(*item.UpdatedAt)
			if parseErr != nil {
				result.Status = syncStatusError
				result.Error = "updated_at must be RFC3339"
				results = append(results, result)
				continue
			}
			now := time.Now()
			eventAt := clampEventAt(client, now)
			if !client.IsZero() && client.After(now.Add(progressClockSkew)) {
				slog.WarnContext(r.Context(), "clamped future-dated progress event time", "component", "api",
					"profile_id", profileID, "media_item_id", item.MediaItemID)
			}
			if !skip {
				// A false here is last-write-wins: a newer stored event beat
				// this queued one, so nothing was written.
				applied, updateErr = store.SetProgressIfNewer(r.Context(), profileID, item.MediaItemID, pos, item.Duration, completed, eventAt)
			}
		case item.ForceOverwrite:
			updateErr = store.SetProgress(r.Context(), profileID, item.MediaItemID, item.Position, item.Duration, thresholds)
		default:
			updateErr = store.UpdateProgress(r.Context(), profileID, item.MediaItemID, item.Position, item.Duration, thresholds)
		}

		switch {
		case updateErr != nil:
			result.Status = syncStatusError
			result.Error = "failed to update progress"
		case skip || !applied:
			result.Status = syncStatusIgnored
			// Still a processed item: the profile refresh and event fan-out
			// below keep the reach they have on v1, where every non-error row
			// reports success. Only the per-item reporting is finer here.
			processedAnyItem = true
		default:
			result.Status = syncStatusUpdated
			processedAnyItem = true
		}

		results = append(results, result)
	}

	if processedAnyItem {
		triggerProfileRefresh(r.Context(), h.profileStaler, h.profileRefreshRequester, userID, profileID)
		for _, item := range req.Items {
			if item.MediaItemID == "" {
				continue
			}
			publishUserStateEvent(
				r.Context(),
				h.EventsHub,
				userID,
				profileID,
				item.MediaItemID,
				"",
				"progress",
				userStateEventState{},
			)
		}
	}

	writeJSON(w, http.StatusOK, syncProgressResponse{Results: results})
}

// progressThresholds reads the deployment's watched and min-resume percentages.
// A missing, empty or unparsable setting leaves the zero value, which the
// userstore treats as its own default.
//
// HandleSyncProgress carries the same block inline. It is not folded into this
// helper because /api/v1 is held byte-identical to upstream Silo on this
// branch; the shared spelling starts here and the v1 copy joins it the next
// time that file is allowed to change.
func (h *ProgressHandler) progressThresholds(ctx context.Context) userstore.ProgressThresholds {
	var thresholds userstore.ProgressThresholds
	if h.SettingsRepo == nil {
		return thresholds
	}
	if v, _ := h.SettingsRepo.Get(ctx, "playback.watched_threshold"); v != "" {
		if pct, err := strconv.Atoi(v); err == nil && pct > 0 {
			thresholds.WatchedPct = pct
		}
	}
	if v, _ := h.SettingsRepo.Get(ctx, "playback.min_resume_threshold"); v != "" {
		if pct, err := strconv.Atoi(v); err == nil && pct > 0 {
			thresholds.MinResumePct = pct
		}
	}
	return thresholds
}
