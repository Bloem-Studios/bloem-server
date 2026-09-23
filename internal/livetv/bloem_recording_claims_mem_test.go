package livetv

import (
	"context"
	"time"
)

// memoryStore mirrors PgStore's recording-claim CAS semantics.

func (s *memoryStore) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *memoryStore) ClaimRecording(_ context.Context, id, status, token, nodeID string, lease time.Duration) (*Recording, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.recordings[id]
	now := s.clock()
	if !ok || rec.Status != status || (rec.LeaseUntil != nil && !rec.LeaseUntil.Before(now)) {
		return nil, nil
	}
	until := now.Add(lease)
	rec.ClaimToken, rec.LeaseUntil = token, &until
	s.recordings[id] = rec
	out := rec
	return &out, nil
}

func (s *memoryStore) claimedLocked(id, token string, statuses ...string) (Recording, bool) {
	rec, ok := s.recordings[id]
	if !ok || rec.ClaimToken != token {
		return rec, false
	}
	for _, status := range statuses {
		if rec.Status == status {
			return rec, true
		}
	}
	return rec, false
}

func (s *memoryStore) MarkRecordingStarted(_ context.Context, id, token, path, tunerSessionID string, lease time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.claimedLocked(id, token, "scheduled", "recording")
	if !ok {
		return false, nil
	}
	until := s.clock().Add(lease)
	rec.Status, rec.Path, rec.TunerSessionID, rec.LastError, rec.LeaseUntil = "recording", path, tunerSessionID, "", &until
	rec.Interrupted = rec.Interrupted || rec.Segments > 0
	rec.Segments++
	s.recordings[id] = rec
	return true, nil
}

func (s *memoryStore) RenewRecordingLease(_ context.Context, id, token string, lease time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.claimedLocked(id, token, "recording")
	if !ok {
		return false, nil
	}
	until := s.clock().Add(lease)
	rec.LeaseUntil = &until
	s.recordings[id] = rec
	return true, nil
}

func (s *memoryStore) ReleaseRecordingClaim(_ context.Context, id, token, lastError string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.claimedLocked(id, token, "scheduled", "recording")
	if !ok {
		return false, nil
	}
	rec.ClaimToken, rec.LeaseUntil, rec.TunerSessionID, rec.LastError = "", nil, "", lastError
	rec.StartAttempts++
	s.recordings[id] = rec
	return true, nil
}

func (s *memoryStore) FinishRecordingClaim(_ context.Context, id, token, status, path, lastError string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.claimedLocked(id, token, "scheduled", "recording")
	if !ok {
		return false, nil
	}
	rec.Status, rec.LastError = status, lastError
	if path != "" {
		rec.Path = path
	}
	rec.ClaimToken, rec.LeaseUntil, rec.TunerSessionID = "", nil, ""
	s.recordings[id] = rec
	return true, nil
}
