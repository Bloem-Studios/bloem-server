package catalog

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

// A full copy builds its side index, "<prefix>_mirrorcopy_<unix seconds>", on the mirror
// server. A failover during a copy makes that server the main, and the swap drops the copy from
// the row, so the side index stays on the new main. Neither cleanup looks there: Silo's deletes
// only "_rebuild_" names and the previous index, and the copy's runs on the mirror server only.
//
// The mirror removes these leftovers itself: right after a swap commits, and once per process
// start while both servers are as remembered (leftovers of an earlier crash). A side index is
// the mirror's own artifact, never part of the main index; apart from the identity marker,
// these deletions are the only writes the mirror makes to the main server.

// cleanMainCopyLeftovers deletes, on the main server, every index named like this prefix's
// copy side indexes ("<prefix>_mirrorcopy_…") except the main's active index, and touches no
// other name. Before each deletion it checks that the run still owns the mirror (checkOwner).
// An index already gone counts as deleted. It goes on past a failed deletion, and returns the
// failures joined; it stops at once when the run lost the mirror or ctx ended.
func (m *SearchMirror) cleanMainCopyLeftovers(ctx context.Context) error {
	if m == nil || m.main == nil {
		return nil
	}
	prefix := m.startup.MeilisearchIndex
	if strings.TrimSpace(prefix) == "" {
		return nil
	}
	main, err := NewSearchIndexEventRepository(m.pool).GetState(ctx, SearchProviderMeilisearch)
	if err != nil {
		return searchMirrorDatabaseError(ctx, fmt.Errorf("read the main search index state: %w", err))
	}
	uids, err := m.main.ListIndexUIDs(ctx)
	if err != nil {
		return fmt.Errorf("list the main search server's indexes: %w", err)
	}
	var failures []error
	for _, uid := range searchMirrorMainCopyLeftovers(uids, prefix, main.ActiveIndexUID) {
		if err := m.checkOwner(ctx); err != nil {
			return err
		}
		if err := dropSearchMirrorCopyIndex(ctx, m.main, uid); err != nil {
			if searchMirrorNotRecorded(ctx, err) {
				return err
			}
			failures = append(failures, fmt.Errorf("delete %s: %w", uid, err))
			continue
		}
		slog.InfoContext(ctx, "search mirror: removed a leftover copy index from the main search server",
			"component", "catalog", "index", uid)
	}
	return errors.Join(failures...)
}

// removeMainCopyLeftovers is cleanMainCopyLeftovers for the run's steps: a failure is only
// logged, never fails the run. A leftover costs disk, not correctness. A run that lost the mirror
// or is shutting down logs nothing.
func (m *SearchMirror) removeMainCopyLeftovers(ctx context.Context) {
	if err := m.cleanMainCopyLeftovers(ctx); err != nil && !searchMirrorNotRecorded(ctx, err) {
		slog.WarnContext(ctx, "search mirror: could not remove leftover copy indexes from the main search server",
			"component", "catalog", "err", m.errorText(err))
	}
}

// searchMirrorMainCopyLeftovers selects the main server's indexes that are this prefix's copy
// side indexes, never activeUID (the main's active index). Every other name, the marker index
// and other prefixes' indexes included, is kept.
func searchMirrorMainCopyLeftovers(uids []string, prefix, activeUID string) []string {
	if strings.TrimSpace(prefix) == "" {
		return nil
	}
	copyPrefix := prefix + searchMirrorCopySideInfix
	var leftovers []string
	for _, uid := range uids {
		if uid != activeUID && strings.HasPrefix(uid, copyPrefix) {
			leftovers = append(leftovers, uid)
		}
	}
	return leftovers
}
