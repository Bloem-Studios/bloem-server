package catalog

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"
)

// The search mirror's failure handling (spec §9). Three kinds of trouble are told apart:
//   - unreachable: no try is counted and the bookmark and window stay put; past the lag cap the
//     mirror needs a full copy;
//   - key rejected: the mirror is misconfigured; no try is counted;
//   - a rejected page or a database error: the page is retried with the main index's timing,
//     and on its last try split so only what Meilisearch really refuses is dead-lettered.
//
// A shutdown (ctx.Err() != nil) never counts an error or saves a failure state.

// searchMirrorPastCapReason is the needs_full_copy reason when the mirror fell further behind
// than the lag cap.
const searchMirrorPastCapReason = "past_cap"

// searchMirrorMaxErrorText bounds the error text kept in the state row and the dead-letter table.
const searchMirrorMaxErrorText = 1024

// searchMirrorRetryDelay is how long a page that failed attempts times waits before its next
// try: base × attempts, at most max.
func searchMirrorRetryDelay(attempts int, base, max time.Duration) time.Duration {
	if attempts <= 0 || base <= 0 {
		return 0
	}
	if max > 0 && base > max/time.Duration(attempts) {
		return max
	}
	return base * time.Duration(attempts)
}

// searchMirrorFailureKind is the kind of a failed mirror step. An error tagged with its own kind
// (searchMirrorApplyError: applyItems, follow and the copy tag theirs, Postgres errors as
// database) keeps it; anything else is a Meilisearch error, sorted by classifySearchMirrorError.
// classifySearchMirrorError cannot tell a Postgres error from a rejection, so every database
// step whose error reaches here is tagged with searchMirrorDatabaseError.
func searchMirrorFailureKind(err error) searchMirrorErrorKind {
	if err == nil {
		return searchMirrorErrNone
	}
	var tagged interface{ Kind() searchMirrorErrorKind }
	if errors.As(err, &tagged) && tagged.Kind() != searchMirrorErrNone {
		return tagged.Kind()
	}
	return classifySearchMirrorError(err)
}

// searchMirrorErrorMetricKind is the errors_total kind for a failure kind: a rejected key counts
// as rejected.
func searchMirrorErrorMetricKind(kind searchMirrorErrorKind) string {
	switch kind {
	case searchMirrorErrUnreachable, searchMirrorErrDatabase:
		return string(kind)
	default:
		return string(searchMirrorErrRejected)
	}
}

// searchMirrorErrorText is err's text, cut to searchMirrorMaxErrorText bytes on a rune boundary.
func searchMirrorErrorText(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	if len(text) <= searchMirrorMaxErrorText {
		return text
	}
	cut := searchMirrorMaxErrorText
	for cut > 0 && text[cut]&0xC0 == 0x80 {
		cut--
	}
	return text[:cut] + "…"
}

// searchMirrorStateLatched reports whether the state is the same-server latch, which no later
// failure replaces: the mirror address reaches the main server.
func searchMirrorStateLatched(s *searchMirrorState) bool {
	return s.State == SearchMirrorMisconfigured && s.Reason == string(SearchMirrorProblemSameServer)
}

// noteSearchMirrorFailure counts one failure of kind and records it as the last error.
func noteSearchMirrorFailure(s *searchMirrorState, kind searchMirrorErrorKind, err error, now time.Time) {
	if s.Errors == nil {
		s.Errors = map[string]int64{}
	}
	s.Errors[searchMirrorErrorMetricKind(kind)]++
	if err != nil {
		s.LastError = searchMirrorErrorText(err)
	}
	if s.ErrorSince == nil {
		since := now
		s.ErrorSince = &since
	}
}

// setSearchMirrorFailureState moves the state to name with reason, unless the same-server latch
// holds, and logs the change once.
func setSearchMirrorFailureState(ctx context.Context, s *searchMirrorState, name SearchMirrorStateName, reason string) {
	if searchMirrorStateLatched(s) {
		return
	}
	if s.State != name || s.Reason != reason {
		slog.WarnContext(ctx, "search mirror state changed", "component", "catalog",
			"from", string(s.State), "to", string(name), "reason", reason, "err", s.LastError)
	}
	s.State = name
	s.Reason = reason
}

func (m *SearchMirror) saveFailureRow(ctx context.Context, row *searchMirrorRow) error {
	return saveSearchMirrorRow(ctx, m.pool, *row)
}

// unreachable records that the mirror could not be reached. No try is counted and the bookmark
// and window stay put. Once the snapshot the mirror holds is older than the lag cap, the mirror
// needs a full copy (needs_full_copy, past_cap) and cleanup stops holding rows for it.
func (m *SearchMirror) unreachable(ctx context.Context, row *searchMirrorRow, err error) error {
	if ctx.Err() != nil {
		return searchMirrorShutdownError(ctx, err)
	}
	now := time.Now().UTC()
	s := &row.State
	noteSearchMirrorFailure(s, searchMirrorErrUnreachable, err, now)

	// On a database error the configured cap is returned; the save below reports the database.
	maxLag, _ := searchMirrorEffectiveMaxLag(ctx, m.pool, m.cfg.MaxLag)
	if maxLag <= 0 {
		maxLag = DefaultSearchMirrorMaxLag
	}
	s.MaxLagSeconds = int64(maxLag / time.Second)

	state, reason := SearchMirrorUnreachable, ""
	if _, at, ok := s.hold(); ok && !at.IsZero() && now.Sub(at) > maxLag {
		state, reason = SearchMirrorNeedsFullCopy, searchMirrorPastCapReason
	}
	setSearchMirrorFailureState(ctx, s, state, reason)
	return m.saveFailureRow(ctx, row)
}

// keyRejected records that the mirror refused the key (401 or 403): misconfigured, key_rejected.
// No try is counted.
func (m *SearchMirror) keyRejected(ctx context.Context, row *searchMirrorRow, err error) error {
	if ctx.Err() != nil {
		return searchMirrorShutdownError(ctx, err)
	}
	s := &row.State
	noteSearchMirrorFailure(s, searchMirrorErrKeyRejected, err, time.Now().UTC())
	setSearchMirrorFailureState(ctx, s, SearchMirrorMisconfigured, string(SearchMirrorProblemKeyRejected))
	return m.saveFailureRow(ctx, row)
}

// searchMirrorShutdownError is what a failure handler returns when the run is shutting down:
// the failure itself, or the context's error when there is none.
func searchMirrorShutdownError(ctx context.Context, err error) error {
	if err != nil {
		return err
	}
	return ctx.Err()
}

// handleRunError records a failure that is not a follow page (copy batches, publish, markers):
// unreachable and a rejected key take their own paths; a rejection or a database error is
// counted and the mirror is failing. The state is saved; the returned error is the save's.
func (m *SearchMirror) handleRunError(ctx context.Context, row *searchMirrorRow, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return searchMirrorShutdownError(ctx, err)
	}
	switch kind := searchMirrorFailureKind(err); kind {
	case searchMirrorErrUnreachable:
		return m.unreachable(ctx, row, err)
	case searchMirrorErrKeyRejected:
		return m.keyRejected(ctx, row, err)
	default:
		s := &row.State
		noteSearchMirrorFailure(s, kind, err, time.Now().UTC())
		setSearchMirrorFailureState(ctx, s, SearchMirrorFailing, "")
		return m.saveFailureRow(ctx, row)
	}
}

// pageFailed handles a follow page whose items could not be applied. advance reports whether
// the caller moves the cursor past the page; when it does, the caller saves the row with the
// cursor (the state here is changed but not saved). When it does not, the state is saved here
// and err2 is the save's error.
//
//   - Unreachable and a rejected key take their own paths; no try is counted.
//   - A rejection or a database error counts a try, and the next waits
//     searchMirrorRetryDelay(tries).
//   - On the MaxAttempts-th try the page is split (splitApply). An unreachable or database error
//     during the split stops it, and that kind's path takes over. When a page of two or more
//     items has none accepted the mirror is failing: nothing is dead-lettered, the cursor stays,
//     and the next try comes after RetryMax. Otherwise (a page of one item counts here, since
//     nothing tells its refusal from a bad item) the refused items are dead-lettered and the
//     cursor moves on.
func (m *SearchMirror) pageFailed(ctx context.Context, row *searchMirrorRow, ids []string, err error) (bool, error) {
	if err == nil {
		return false, nil
	}
	if ctx.Err() != nil {
		return false, searchMirrorShutdownError(ctx, err)
	}
	kind := searchMirrorFailureKind(err)
	switch kind {
	case searchMirrorErrUnreachable:
		return false, m.unreachable(ctx, row, err)
	case searchMirrorErrKeyRejected:
		return false, m.keyRejected(ctx, row, err)
	}

	s := &row.State
	attempts := 1
	if s.Retry != nil {
		attempts = s.Retry.Attempts + 1
	}
	if attempts < m.opts.MaxAttempts {
		m.noteSearchMirrorTry(s, kind, err, attempts, time.Now().UTC())
		return false, m.saveFailureRow(ctx, row)
	}

	// The last try: split the page. Nothing is recorded until it ends, so a shutdown during the
	// split leaves the state as it was.
	accepted, refusals, abort := m.splitApplyRefusals(ctx, m.mirror, row.ActiveIndexUID, ids)
	if ctx.Err() != nil {
		return false, searchMirrorShutdownError(ctx, errors.Join(err, abort))
	}
	now := time.Now().UTC()
	m.noteSearchMirrorTry(s, kind, err, attempts, now)

	if abort != nil {
		switch abortKind := searchMirrorFailureKind(abort); abortKind {
		case searchMirrorErrUnreachable:
			return false, m.unreachable(ctx, row, abort)
		case searchMirrorErrKeyRejected:
			return false, m.keyRejected(ctx, row, abort)
		default:
			noteSearchMirrorFailure(s, abortKind, abort, now)
			s.Retry.LastError = searchMirrorErrorText(abort)
			return false, m.saveFailureRow(ctx, row)
		}
	}

	if len(accepted) == 0 && len(ids) > 1 {
		// No part of a page of several items was accepted: the mirror server itself is failing.
		if n := len(refusals); n > 0 {
			s.LastError = searchMirrorErrorText(refusals[n-1].Err)
			s.Retry.LastError = s.LastError
		}
		s.Retry.NextAt = now.Add(m.opts.RetryMax)
		setSearchMirrorFailureState(ctx, s, SearchMirrorFailing, "")
		return false, m.saveFailureRow(ctx, row)
	}

	if err := writeSearchMirrorDeadLetters(ctx, m.pool, refusals); err != nil {
		if ctx.Err() != nil {
			return false, searchMirrorShutdownError(ctx, err)
		}
		noteSearchMirrorFailure(s, searchMirrorErrDatabase, err, now)
		s.Retry.LastError = searchMirrorErrorText(err)
		return false, m.saveFailureRow(ctx, row)
	}
	if len(refusals) > 0 {
		slog.WarnContext(ctx, "search mirror dead-lettered refused items", "component", "catalog",
			"refused", len(refusals), "accepted", len(accepted), "err", searchMirrorErrorText(refusals[len(refusals)-1].Err))
	}
	searchMirrorPageSucceeded(s)
	return true, nil
}

// searchMirrorPageSucceeded records a follow page the mirror took (all of it, or all but the
// dead-lettered items): the retry timing and the last error are cleared, and a mirror that was
// unreachable, failing or had its key refused is following again.
func searchMirrorPageSucceeded(s *searchMirrorState) {
	s.Retry = nil
	s.LastError = ""
	s.ErrorSince = nil
	switch {
	case s.State == SearchMirrorUnreachable, s.State == SearchMirrorFailing,
		s.State == SearchMirrorMisconfigured && s.Reason == string(SearchMirrorProblemKeyRejected):
		s.State = SearchMirrorFollowing
		s.Reason = ""
	}
}

// noteSearchMirrorTry counts a page's try number attempts and schedules the next.
func (m *SearchMirror) noteSearchMirrorTry(s *searchMirrorState, kind searchMirrorErrorKind, err error, attempts int, now time.Time) {
	if s.Retry == nil {
		s.Retry = &searchMirrorRetry{}
	}
	s.Retry.Attempts = attempts
	s.Retry.NextAt = now.Add(searchMirrorRetryDelay(attempts, m.opts.RetryBase, m.opts.RetryMax))
	s.Retry.LastError = searchMirrorErrorText(err)
	noteSearchMirrorFailure(s, kind, err, now)
}

// searchMirrorRefusal is an item Meilisearch refused on its own, with the refusal.
type searchMirrorRefusal struct {
	ID  string
	Err error
}

// splitApply applies ids to uid on client, halving recursively down to single ids so that only
// the items Meilisearch itself refuses are left in refused. Only a Meilisearch rejection splits
// further: any other error stops the split and is returned as abort, with what was settled so
// far.
func (m *SearchMirror) splitApply(ctx context.Context, client *meilisearchClient, uid string, ids []string) (accepted, refused []string, abort error) {
	accepted, refusals, abort := m.splitApplyRefusals(ctx, client, uid, ids)
	for _, refusal := range refusals {
		refused = append(refused, refusal.ID)
	}
	return accepted, refused, abort
}

// splitApplyRefusals is splitApply, keeping each refused item's error.
func (m *SearchMirror) splitApplyRefusals(ctx context.Context, client *meilisearchClient, uid string, ids []string) (accepted []string, refused []searchMirrorRefusal, abort error) {
	var apply func(part []string) error
	apply = func(part []string) error {
		if len(part) == 0 {
			return nil
		}
		err := m.applyItems(ctx, client, uid, slices.Clone(part))
		if err == nil {
			accepted = append(accepted, part...)
			return nil
		}
		if ctx.Err() != nil || searchMirrorFailureKind(err) != searchMirrorErrRejected {
			return err
		}
		if len(part) == 1 {
			refused = append(refused, searchMirrorRefusal{ID: part[0], Err: err})
			return nil
		}
		half := len(part) / 2
		if err := apply(part[:half]); err != nil {
			return err
		}
		return apply(part[half:])
	}
	abort = apply(slices.Clone(ids))
	return accepted, refused, abort
}

// writeSearchMirrorDeadLetters records refused items: a new entry, or one more failure of an
// existing one.
func writeSearchMirrorDeadLetters(ctx context.Context, q pgxExecer, refusals []searchMirrorRefusal) error {
	if len(refusals) == 0 {
		return nil
	}
	ids := make([]string, 0, len(refusals))
	texts := make([]string, 0, len(refusals))
	seen := make(map[string]bool, len(refusals))
	for _, refusal := range refusals {
		if seen[refusal.ID] {
			continue
		}
		seen[refusal.ID] = true
		ids = append(ids, refusal.ID)
		texts = append(texts, searchMirrorErrorText(refusal.Err))
	}
	_, err := q.Exec(ctx, `
		INSERT INTO bloem_search_mirror_dead_letters (content_id, last_error)
		SELECT content_id, last_error FROM unnest($1::text[], $2::text[]) AS t(content_id, last_error)
		ON CONFLICT (content_id) DO UPDATE SET
			last_failed_at = now(),
			attempts = bloem_search_mirror_dead_letters.attempts + 1,
			last_error = EXCLUDED.last_error`, ids, texts)
	if err != nil {
		return fmt.Errorf("write search mirror dead letters: %w", err)
	}
	return nil
}

// deleteSearchMirrorDeadLetters removes ids from the dead-letter table.
func deleteSearchMirrorDeadLetters(ctx context.Context, q pgxExecer, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if _, err := q.Exec(ctx, `DELETE FROM bloem_search_mirror_dead_letters WHERE content_id = ANY($1::text[])`, ids); err != nil {
		return fmt.Errorf("delete search mirror dead letters: %w", err)
	}
	return nil
}

// searchMirrorDeadLetterIDs is a page of dead-lettered ids after after, in id order.
func searchMirrorDeadLetterIDs(ctx context.Context, q pgxQuerier, after string, limit int) ([]string, error) {
	rows, err := q.Query(ctx, `
		SELECT content_id FROM bloem_search_mirror_dead_letters
		WHERE content_id > $1
		ORDER BY content_id
		LIMIT $2`, after, limit)
	if err != nil {
		return nil, fmt.Errorf("read search mirror dead letters: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("read search mirror dead letters: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read search mirror dead letters: %w", err)
	}
	return ids, nil
}

// retryDeadLetters retries the dead-lettered items by id, at most once every
// DeadLetterRetryEvery and only while the mirror is following its published index. It takes
// them in pages of the live SyncBatchSize and splits each page: accepted items leave the table,
// refused ones count one more failure. Then DeadLetterRetryAt is set and the row saved.
//
// A failure that stops a split still sets DeadLetterRetryAt, so the next attempt is an hour
// later: unreachable and a rejected key take their own paths; a database error is counted and
// returned, and the state is left as it is.
func (m *SearchMirror) retryDeadLetters(ctx context.Context, row *searchMirrorRow) error {
	if ctx.Err() != nil {
		return nil
	}
	s := &row.State
	now := time.Now().UTC()
	if s.DeadLetterRetryAt != nil && now.Sub(*s.DeadLetterRetryAt) < m.opts.DeadLetterRetryEvery {
		return nil
	}
	if s.State != SearchMirrorFollowing || s.Copy != nil || row.ActiveIndexUID == "" {
		return nil
	}

	// A settings read that fails leaves the defaults; the dead-letter read reports the database.
	settings, _ := LoadCatalogSearchSettings(ctx, m.settings)
	size := settings.SyncBatchSize
	if size <= 0 {
		size = DefaultMeilisearchSyncBatchSize
	}

	retried, cleared := 0, 0
	var failure error
	for after := ""; ; {
		ids, err := searchMirrorDeadLetterIDs(ctx, m.pool, after, size)
		if err != nil {
			failure = searchMirrorDatabaseError(ctx, err)
			break
		}
		if len(ids) == 0 {
			break
		}
		after = ids[len(ids)-1]
		accepted, refusals, abort := m.splitApplyRefusals(ctx, m.mirror, row.ActiveIndexUID, ids)
		if ctx.Err() != nil {
			return searchMirrorShutdownError(ctx, abort)
		}
		retried += len(accepted) + len(refusals)
		if err := deleteSearchMirrorDeadLetters(ctx, m.pool, accepted); err != nil {
			failure = searchMirrorDatabaseError(ctx, err)
			break
		}
		cleared += len(accepted)
		if err := writeSearchMirrorDeadLetters(ctx, m.pool, refusals); err != nil {
			failure = searchMirrorDatabaseError(ctx, err)
			break
		}
		if abort != nil {
			failure = abort
			break
		}
		if len(ids) < size {
			break
		}
	}
	if ctx.Err() != nil {
		return searchMirrorShutdownError(ctx, failure)
	}
	if retried > 0 {
		slog.InfoContext(ctx, "search mirror retried dead letters", "component", "catalog",
			"retried", retried, "cleared", cleared)
	}

	s.DeadLetterRetryAt = &now
	if failure == nil {
		return m.saveFailureRow(ctx, row)
	}
	switch kind := searchMirrorFailureKind(failure); kind {
	case searchMirrorErrUnreachable:
		return m.unreachable(ctx, row, failure)
	case searchMirrorErrKeyRejected:
		return m.keyRejected(ctx, row, failure)
	default:
		if s.Errors == nil {
			s.Errors = map[string]int64{}
		}
		s.Errors[searchMirrorErrorMetricKind(kind)]++
		if err := m.saveFailureRow(ctx, row); err != nil {
			return errors.Join(fmt.Errorf("retry search mirror dead letters: %w", failure), err)
		}
		return fmt.Errorf("retry search mirror dead letters: %w", failure)
	}
}
