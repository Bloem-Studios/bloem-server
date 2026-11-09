package watchsync

// Bloem-owned. Watched removals from watch-sync plugins: when a person removes
// an item's watches at the provider (Pastime deletes an item from a profile's
// library), the connection's profile loses that item's watch history here too.
// The contract is `removed` on WatchSyncRemoteWatchedState, added in Bloem's
// plugin SDK v0.28.0 and proposed to Silo; until Silo carries it, the hooks in
// plugin_provider_state.go, service.go and types.go are temporary seams.

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"time"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/protobuf/encoding/protowire"

	"github.com/Silo-Server/silo-server/internal/userstore"
)

// watchedRemovedField is WatchSyncRemoteWatchedState.removed. The host builds
// against Silo's SDK, whose generated type does not have the field yet, so its
// value is read from the message's unknown fields.
const watchedRemovedField protowire.Number = 3

// watchedRemoved reports whether a plugin marked this watched state as a removal.
func watchedRemoved(watched *pluginv1.WatchSyncRemoteWatchedState) bool {
	if watched == nil {
		return false
	}
	removed := false
	b := watched.ProtoReflect().GetUnknown()
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return false
		}
		b = b[n:]
		if num == watchedRemovedField && typ == protowire.VarintType {
			v, m := protowire.ConsumeVarint(b)
			if m < 0 {
				return false
			}
			removed = v != 0
			b = b[m:]
			continue
		}
		m := protowire.ConsumeFieldValue(num, typ, b)
		if m < 0 {
			return false
		}
		b = b[m:]
	}
	return removed
}

// remoteWatchRemovalFromProto decodes a watched removal. removal is false when
// the state is an ordinary watched state, which remoteWatchFromProto decodes.
func remoteWatchRemovalFromProto(provider string, state *pluginv1.WatchSyncRemoteState) (row RemoteWatch, removal bool, err error) {
	watched := state.GetWatched()
	if !watchedRemoved(watched) {
		return RemoteWatch{}, false, nil
	}
	if watched.GetPlayCount() != 0 || watched.GetLastWatchedAt() == nil {
		return RemoteWatch{}, true, errors.New("watch sync plugin returned a watched removal without its time or with plays")
	}
	identity, err := remoteIdentityFromProto(state)
	if err != nil {
		return RemoteWatch{}, true, err
	}
	removedAt := watched.GetLastWatchedAt().AsTime().UTC()
	return RemoteWatch{
		Provider: provider, ProviderItemKey: state.GetProviderItemKey(),
		Kind: identity.kind, Title: identity.title, Year: identity.year,
		IMDbID: identity.imdbID, TMDBID: identity.tmdbID, TVDBID: identity.tvdbID,
		SeriesTitle: identity.seriesTitle, SeriesYear: identity.seriesYear,
		SeriesIMDbID: identity.seriesIMDbID, SeriesTMDBID: identity.seriesTMDBID, SeriesTVDBID: identity.seriesTVDBID,
		SeasonNumber: identity.season, EpisodeNumber: identity.episode,
		LastWatchedAt: &removedAt, Removed: true,
	}, true, nil
}

// applyWatchedRemovals removes the connection profile's watches of each removed
// item: a movie or episode, or every episode of a series. Only watches at or
// before the removal time go. An item watched or resumed here after the
// removal keeps everything (the newer state wins), and an item with nothing
// left to remove is skipped, so a removal repeated by every traversal writes
// nothing after its first application. A series removal reaches only the
// episodes the profile may open, as Silo's whole-series marker does.
func (s *Service) applyWatchedRemovals(ctx context.Context, conn Connection, profile ProfileAccess, rows []RemoteWatch, result *ImportWatchedResult) error {
	if len(rows) == 0 {
		return nil
	}
	if s.storeProvider == nil {
		return errors.New("watch provider user store is not configured")
	}
	// A removal also drops the watches Silo kept for a title not in a library
	// yet (pending_watches.go), so a later sync cannot import one the person
	// removed. ImportWatched marks removed rows fresh, so this sync skips them.
	if pending, ok := s.repo.(pendingWatchStore); ok {
		if err := pending.DeletePendingWatches(ctx, conn.ID, conn.ProviderAccountID, rows); err != nil {
			return err
		}
	}
	cutoffs := make(map[string]time.Time)
	for _, row := range rows {
		if row.LastWatchedAt == nil {
			continue
		}
		matches, reason, err := s.matchWatchedLeaves(ctx, row, profile)
		if err != nil {
			return err
		}
		if len(matches) == 0 {
			result.Unmatched++
			if reason != "" {
				result.Warnings = append(result.Warnings, reason)
			}
			continue
		}
		// Whole seconds: the store reports watch times to the second.
		cutoff := row.LastWatchedAt.UTC().Truncate(time.Second)
		for _, match := range matches {
			if existing, ok := cutoffs[match.MediaItemID]; !ok || cutoff.After(existing) {
				cutoffs[match.MediaItemID] = cutoff
			}
		}
	}
	if len(cutoffs) == 0 {
		return nil
	}
	ids := make([]string, 0, len(cutoffs))
	for id := range cutoffs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	store, err := s.storeProvider.ForUser(ctx, conn.UserID)
	if err != nil {
		return err
	}
	latest, err := latestLocalWatchState(ctx, store, conn.ProfileID, ids)
	if err != nil {
		return err
	}
	byCutoff := make(map[time.Time][]string)
	for _, id := range ids {
		last, ok := latest[id]
		if !ok || last.After(cutoffs[id]) {
			continue
		}
		byCutoff[cutoffs[id]] = append(byCutoff[cutoffs[id]], id)
	}
	times := make([]time.Time, 0, len(byCutoff))
	for at := range byCutoff {
		times = append(times, at)
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
	for _, at := range times {
		group := byCutoff[at]
		if err := store.RemoveHistoryItems(ctx, conn.ProfileID, group, at); err != nil {
			return err
		}
		result.Removed += len(group)
	}
	if result.Removed > 0 {
		slog.InfoContext(ctx, "watch sync removed watch history", "provider", conn.Provider, "connection_id", conn.ID, "items", result.Removed)
	}
	return nil
}

// latestLocalWatchState returns, per item, the newest of its last completed
// watch and its watch progress for the profile. Items with neither are absent.
func latestLocalWatchState(ctx context.Context, store userstore.UserStore, profileID string, ids []string) (map[string]time.Time, error) {
	latest := make(map[string]time.Time, len(ids))
	keep := func(id, value string) {
		at, ok := parseStoreTime(value)
		if !ok {
			// An unreadable time cannot prove the item is older than the
			// removal; treat it as newer so nothing is removed by mistake.
			at = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
		}
		if existing, found := latest[id]; !found || at.After(existing) {
			latest[id] = at
		}
	}
	history, err := store.ListCompletedHistoryItems(ctx, userstore.CompletedHistoryItemQuery{ProfileID: profileID, MediaItemIDs: ids})
	if err != nil {
		return nil, err
	}
	for _, item := range history {
		keep(item.MediaItemID, item.WatchedAt)
	}
	progress, err := store.ListProgressByMediaItems(ctx, profileID, ids)
	if err != nil {
		return nil, err
	}
	for id, row := range progress {
		keep(id, row.UpdatedAt)
	}
	return latest, nil
}

func parseStoreTime(value string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05"} {
		if at, err := time.Parse(layout, value); err == nil {
			return at.UTC(), true
		}
	}
	return time.Time{}, false
}
