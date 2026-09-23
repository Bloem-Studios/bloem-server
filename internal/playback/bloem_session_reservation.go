package playback

// Bloem fleet reservation, tenant admission, and subject-context seams for
// SessionManager. Kept out of session.go so the upstream Silo file stays as
// close to upstream/main as possible.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// SessionContextProvider validates a playback subject and enriches the
// request context for every downstream admission check.
type SessionContextProvider func(ctx context.Context, userID int, profileID string) (context.Context, error)

// SetReservationStore installs the shared fleet admission authority. A nil
// store keeps the in-memory single-node behavior used by isolated tests.
func (m *SessionManager) SetReservationStore(store ReservationStore, lease time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reservationStore = store
	if lease > 0 {
		m.reservationLease = lease
	}
}

// SetContextProvider installs subject validation shared by limit lookup and
// policy admission. The returned context is used for both operations.
func (m *SessionManager) SetContextProvider(provider SessionContextProvider) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.contextProvider = provider
}

// reservationStoreTimeout bounds every fleet reservation store call. A stalled
// database or a contended advisory lock must degrade into an admission error,
// never into a playback manager that waits forever.
const reservationStoreTimeout = 5 * time.Second

func reservationStoreContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, reservationStoreTimeout)
}

type sessionReservationLock struct {
	mu sync.Mutex
	// holders counts owners and waiters so the entry outlives every waiter.
	holders int
}

// lockSessionReservation serializes fleet reservation changes for one session
// ID and returns the unlock function. Callers must not hold m.mu.
func (m *SessionManager) lockSessionReservation(sessionID string) func() {
	m.reservationLocksMu.Lock()
	if m.reservationLocks == nil {
		m.reservationLocks = make(map[string]*sessionReservationLock)
	}
	lock := m.reservationLocks[sessionID]
	if lock == nil {
		lock = &sessionReservationLock{}
		m.reservationLocks[sessionID] = lock
	}
	lock.holders++
	m.reservationLocksMu.Unlock()

	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		m.reservationLocksMu.Lock()
		lock.holders--
		if lock.holders == 0 {
			delete(m.reservationLocks, sessionID)
		}
		m.reservationLocksMu.Unlock()
	}
}

// applyFleetReservationLocked publishes a store result only when it is newer
// than what the session already holds. Generations come from one database
// sequence, so the highest generation is the row the store currently keeps.
// Callers hold m.mu.
func applyFleetReservationLocked(session *Session, reservation Reservation, request ReservationRequest) {
	if session == nil || reservation.Generation < session.reservationGeneration {
		return
	}
	request.LeaseUntil = reservation.LeaseUntil
	session.reservationGeneration = reservation.Generation
	session.reservationLeaseUntil = reservation.LeaseUntil
	session.reservationRequest = request
}

func (m *SessionManager) acquireFleetReservation(ctx context.Context, session *Session, limits SessionLimits) error {
	m.mu.RLock()
	store := m.reservationStore
	lease := m.reservationLeaseLocked()
	m.mu.RUnlock()
	if store == nil {
		return nil
	}
	// Upstream Silo has no fleet reservation: it admits on the in-memory caps
	// alone. Keep exactly that path when a reservation has nothing to protect.
	// A client that authenticated without an active profile cannot even be
	// expressed as a ReservationRequest -- valid() requires a profile -- so
	// gating on it turns "no profile" into ErrReservationInvalid and refuses
	// playback that Silo serves without complaint.
	//
	// A tenant session still reserves: the shared tenant transcode pool is the
	// thing the reservation exists to defend, and it must never go unmetered.
	if session.ProfileID == "" && limits.TenantID == "" {
		return nil
	}
	request := ReservationRequest{
		SessionID:         session.ID,
		AccountID:         session.UserID,
		ProfileID:         session.ProfileID,
		TenantID:          limits.TenantID,
		IsTranscode:       session.PlayMethod == PlayTranscode,
		AccountStreams:    limits.MaxStreams,
		AccountTranscodes: limits.MaxTranscodes,
		TenantTranscodes:  limits.TenantMaxTranscodes,
		LeaseUntil:        time.Now().Add(lease),
	}
	storeCtx, cancel := reservationStoreContext(ctx)
	defer cancel()
	reservation, err := store.Acquire(storeCtx, request)
	if err != nil {
		return err
	}
	applyFleetReservationLocked(session, reservation, request)
	return nil
}

func (m *SessionManager) releaseFleetReservation(session *Session) {
	if session == nil || session.reservationGeneration <= 0 {
		return
	}
	m.releaseFleetReservationGeneration(session.ID, session.reservationGeneration)
}

func (m *SessionManager) releaseFleetReservationGeneration(sessionID string, generation int64) {
	if sessionID == "" || generation <= 0 {
		return
	}
	m.mu.RLock()
	store := m.reservationStore
	m.mu.RUnlock()
	if store == nil {
		return
	}
	ctx, cancel := reservationStoreContext(context.Background())
	defer cancel()
	if err := store.Release(ctx, sessionID, generation); err != nil && !errors.Is(err, ErrReservationGenerationMismatch) {
		slog.Warn("failed to release playback reservation", "component", "playback", "session", sessionID, "generation", generation, "error", err)
	}
}

// renewFleetReservation keeps a live session's shared lease current. session
// is a snapshot taken by the caller; the reservation actually renewed is the
// one the manager holds now, read under the session reservation lock, so
// concurrent transport and progress requests renew or reacquire once instead
// of each minting a generation the manager then loses track of.
//
// Renewal is driven only by client traffic (progress, activity, transport);
// nothing renews on a timer, and CleanInactive neither renews nor releases a
// session still inside its grace. A session that sends no traffic for longer
// than the lease -- for example a paused client that stops reporting progress
// -- stops counting toward fleet capacity once its row expires, while the
// in-memory paused grace still keeps the session. Its next request reacquires
// the reservation. The session is stopped when that reacquire is refused for
// stream, transcode, or tenant capacity, or fails for any reason once the
// lease the manager held has already lapsed.
func (m *SessionManager) renewFleetReservation(session *Session) error {
	if session == nil || session.reservationGeneration <= 0 {
		return nil
	}
	m.mu.RLock()
	store := m.reservationStore
	lease := m.reservationLeaseLocked()
	m.mu.RUnlock()
	if store == nil {
		return nil
	}
	if session.reservationLeaseUntil.After(time.Now().Add(lease / 2)) {
		return nil
	}

	unlock := m.lockSessionReservation(session.ID)
	remotelyStopped := false
	defer func() {
		unlock()
		// Callbacks may touch this session's reservation state, so invoke
		// them after releasing the per-session lock as well as m.mu.
		if remotelyStopped {
			m.retireStoppedSessions([]string{session.ID})
		}
	}()

	m.mu.RLock()
	current := m.sessions[session.ID]
	if current == nil || current.reservationGeneration <= 0 {
		m.mu.RUnlock()
		return nil
	}
	generation := current.reservationGeneration
	heldUntil := current.reservationLeaseUntil
	request := current.reservationRequest
	m.mu.RUnlock()
	// Another request renewed while this one waited for the lock.
	if heldUntil.After(time.Now().Add(lease / 2)) {
		return nil
	}

	leaseUntil := time.Now().Add(lease)
	ctx, cancel := reservationStoreContext(context.Background())
	defer cancel()
	reservation, err := store.Renew(ctx, session.ID, generation, leaseUntil)
	if errors.Is(err, ErrReservationGenerationMismatch) {
		request.LeaseUntil = leaseUntil
		reservation, err = store.Acquire(ctx, request)
	}
	if err != nil {
		remotelyStopped = errors.Is(err, ErrAttemptStoppedV3)
		slog.Warn("failed to renew playback reservation", "component", "playback", "session", session.ID, "generation", generation, "error", err)
		if errors.Is(err, ErrAttemptStoppedV3) || !heldUntil.After(time.Now()) || errors.Is(err, ErrTooManyStreams) || errors.Is(err, ErrTooManyTranscodes) || errors.Is(err, ErrTenantTranscodesExceeded) {
			return err
		}
		return nil
	}

	m.mu.Lock()
	current = m.sessions[session.ID]
	if current == nil {
		m.mu.Unlock()
		// The session stopped while the store call was in flight. Its stop
		// released the generation it knew about; release what renewal just
		// wrote so the row does not hold capacity until the lease expires.
		m.releaseFleetReservationGeneration(session.ID, reservation.Generation)
		return nil
	}
	applyFleetReservationLocked(current, reservation, request)
	m.mu.Unlock()
	return nil
}

// replacementReservationRequestLocked reports whether admitting method for
// session must move its fleet reservation, and the request to acquire. It
// follows acquireFleetReservation's rule: a profileless, tenantless session
// never reserved, so switching its method has nothing to move. Callers hold
// m.mu.
func (m *SessionManager) replacementReservationRequestLocked(session *Session, method PlayMethod, limits SessionLimits) (ReservationRequest, bool) {
	if m.reservationStore == nil || session == nil {
		return ReservationRequest{}, false
	}
	requestedTranscode := method == PlayTranscode
	if session.reservationGeneration > 0 && session.reservationRequest.IsTranscode == requestedTranscode {
		return ReservationRequest{}, false
	}
	if session.reservationGeneration <= 0 && session.ProfileID == "" && limits.TenantID == "" {
		return ReservationRequest{}, false
	}
	return ReservationRequest{
		SessionID:         session.ID,
		AccountID:         session.UserID,
		ProfileID:         session.ProfileID,
		TenantID:          limits.TenantID,
		IsTranscode:       requestedTranscode,
		AccountStreams:    limits.MaxStreams,
		AccountTranscodes: limits.MaxTranscodes,
		TenantTranscodes:  limits.TenantMaxTranscodes,
		LeaseUntil:        time.Now().Add(m.reservationLeaseLocked()),
	}, true
}

// admitReplacementLocked publishes an admitted replacement method and moves
// the fleet reservation to match. Callers hold the session reservation lock
// and m.mu, and have just checked admission under m.mu; it returns with m.mu
// released and never holds m.mu across the store call.
//
// The method is published provisionally before the store call. Local
// admission counts replacementPlayMethod, so from that moment no other start
// or replacement on this node can take the slot while the reservation is in
// flight. On a store error the provisional method is withdrawn. If the
// session stopped meanwhile, the generation this call wrote is released and
// the replacement reports ErrSessionNotFound.
func (m *SessionManager) admitReplacementLocked(ctx context.Context, session *Session, method PlayMethod, limits SessionLimits) error {
	request, needed := m.replacementReservationRequestLocked(session, method, limits)
	store := m.reservationStore
	priorMethod := session.replacementPlayMethod
	hadReservation := session.reservationGeneration > 0
	previous := session.reservationRequest
	session.replacementPlayMethod = method
	m.mu.Unlock()
	if !needed {
		return nil
	}

	storeCtx, cancel := reservationStoreContext(ctx)
	reservation, err := store.Acquire(storeCtx, request)
	cancel()

	m.mu.Lock()
	current := m.sessions[session.ID]
	if current != session {
		m.mu.Unlock()
		if err == nil {
			m.releaseFleetReservationGeneration(session.ID, reservation.Generation)
		}
		return ErrSessionNotFound
	}
	// Only this function and RollbackReplacement set replacementPlayMethod,
	// and both hold the session reservation lock. A different value here
	// means a stream-state commit consumed the replacement while the store
	// call ran; that commit already cleared the displaced reservation too.
	provisional := current.replacementPlayMethod == method
	if err != nil {
		if provisional {
			current.replacementPlayMethod = priorMethod
		}
		m.mu.Unlock()
		return err
	}
	applyFleetReservationLocked(current, reservation, request)
	if provisional && hadReservation {
		copy := previous
		current.replacementReservationPrevious = &copy
	}
	m.mu.Unlock()
	return nil
}

// restoreFleetReplacement reacquires the reservation a canceled or rolled back
// replacement displaced. Callers hold the session reservation lock and must
// NOT hold m.mu: the store call runs unlocked, so a slow database stalls only
// this session's reservation changes instead of every session on the node.
// If the previous reservation cannot be restored, the session is removed and
// whatever the store still holds for it is released.
func (m *SessionManager) restoreFleetReplacement(sessionID string, previous *ReservationRequest) error {
	if previous == nil {
		return nil
	}
	m.mu.RLock()
	store := m.reservationStore
	lease := m.reservationLeaseLocked()
	m.mu.RUnlock()
	if store == nil {
		return nil
	}
	request := *previous
	request.LeaseUntil = time.Now().Add(lease)
	ctx, cancel := reservationStoreContext(context.Background())
	reservation, err := store.Acquire(ctx, request)
	cancel()

	m.mu.Lock()
	current := m.sessions[sessionID]
	if err != nil {
		if current == nil {
			m.mu.Unlock()
			return err
		}
		copy := *current
		delete(m.sessions, sessionID)
		m.mu.Unlock()
		m.releaseFleetReservation(&copy)
		return err
	}
	if current == nil {
		m.mu.Unlock()
		m.releaseFleetReservationGeneration(sessionID, reservation.Generation)
		return nil
	}
	applyFleetReservationLocked(current, reservation, request)
	m.mu.Unlock()
	return nil
}

// tenantTranscodeCountLocked counts live transcoding sessions across every
// account of a park tenant organization — the shared pool the tenant's plan
// reserved. Callers hold m.mu.
func (m *SessionManager) tenantTranscodeCountLocked(tenantID string) int {
	if tenantID == "" {
		return 0
	}
	now := time.Now()
	count := 0
	for _, s := range m.sessions {
		if s.TenantID == tenantID &&
			(s.PlayMethod == PlayTranscode || s.replacementPlayMethod == PlayTranscode) &&
			m.countsTowardLimitsLocked(s, now) {
			count++
		}
	}
	return count
}

// tenantAdmissionErrorLocked is the park tenant gate, applied
// UNCONDITIONALLY — the inline path and the policy-decider path alike,
// because the tenant organization's transcode pool and frozen flag are the
// operator's sold entitlement, not a per-account policy the engine may
// overrule. Callers hold m.mu.
func (m *SessionManager) tenantAdmissionErrorLocked(method PlayMethod, limits SessionLimits) error {
	if limits.TenantID == "" {
		return nil
	}
	if limits.TenantFrozen {
		return ErrTenantFrozen
	}
	if method == PlayTranscode && limits.TenantMaxTranscodes > 0 &&
		m.tenantTranscodeCountLocked(limits.TenantID) >= limits.TenantMaxTranscodes {
		return ErrTenantTranscodesExceeded
	}
	return nil
}

// SetDeviceID records the device a session's client identified with. Remote
// control (S-5a) resolves the device's advertised command list through it.
func (m *SessionManager) SetDeviceID(sessionID, deviceID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[sessionID]
	if !ok {
		return ErrSessionNotFound
	}
	s.DeviceID = deviceID
	return nil
}

// Bloem admission reason codes, kept out of upstream's const block.
const (
	AdmissionReasonTenantTranscodesExceeded = "tenant_transcodes_exceeded"
	AdmissionReasonTenantFrozen             = "tenant_frozen"
)

// defaultReservationLease is the fleet reservation lease used until
// SetReservationStore installs a positive override.
const defaultReservationLease = 2 * time.Minute

// bloemSessionReservation is the fleet reservation a Session holds. Embedded
// in Session so upstream's field list stays untouched.
type bloemSessionReservation struct {
	reservationGeneration          int64
	reservationLeaseUntil          time.Time
	reservationRequest             ReservationRequest
	replacementReservationPrevious *ReservationRequest
}

// bloemSessionManagerState is the Bloem state SessionManager carries for
// subject context, remote stops, and fleet reservations. Embedded in
// SessionManager so upstream's field list stays untouched.
type bloemSessionManagerState struct {
	contextProvider      SessionContextProvider
	remoteStopHooks      []func(*Session)
	reservationStore     ReservationStore
	reservationLease     time.Duration
	reservationLifecycle sync.Mutex
	// reservationLocks serializes fleet reservation mutations per session ID
	// without holding mu across a database round trip. Lock order is
	// reservationLifecycle, then a session reservation lock, then mu.
	reservationLocksMu sync.Mutex
	reservationLocks   map[string]*sessionReservationLock
}

// reservationLeaseLocked returns the configured fleet reservation lease.
// Callers hold m.mu (read or write).
func (m *SessionManager) reservationLeaseLocked() time.Duration {
	if m.reservationLease <= 0 {
		return defaultReservationLease
	}
	return m.reservationLease
}

// bloemAdmissionDenyError maps Bloem's tenant reason codes; any other
// unknown code keeps upstream's ErrPlaybackNotAllowed.
func bloemAdmissionDenyError(reasonCode string) error {
	switch reasonCode {
	case AdmissionReasonTenantTranscodesExceeded:
		return ErrTenantTranscodesExceeded
	case AdmissionReasonTenantFrozen:
		return ErrTenantFrozen
	default:
		return ErrPlaybackNotAllowed
	}
}

// bloemSessionSnapshot captures a copy of a session under m.mu so a deferred
// fleet reservation call can use it after m.mu is released.
type bloemSessionSnapshot struct {
	session *Session
}

// capture copies s. Callers hold m.mu. A nil s captures nothing.
func (b *bloemSessionSnapshot) capture(s *Session) {
	if s == nil {
		return
	}
	copy := *s
	b.session = &copy
}

// renewTouchedReservation renews the fleet reservation of a session that
// UpdateProgress, TouchActivity, or BeginTransport just touched. It runs
// deferred, after m.mu is released. A refused renewal stops the session and
// replaces the caller's nil error.
func (m *SessionManager) renewTouchedReservation(sessionID string, touched *bloemSessionSnapshot, errp *error) {
	if *errp != nil || touched.session == nil {
		return
	}
	if err := m.renewFleetReservation(touched.session); err != nil {
		_ = m.StopSession(sessionID)
		*errp = err
	}
}

// releaseStoppedReservation releases the fleet reservation of a session
// StopSession removed. It runs deferred, after m.mu is released.
func (m *SessionManager) releaseStoppedReservation(stopped *bloemSessionSnapshot) {
	if stopped.session == nil {
		return
	}
	m.releaseFleetReservation(stopped.session)
}

// bloemReplacementRestore holds a session reservation lock across a replacement
// cancel or rollback and, once m.mu is released, reacquires the fleet
// reservation the replacement displaced.
type bloemReplacementRestore struct {
	m         *SessionManager
	sessionID string
	unlock    func()
	previous  *ReservationRequest
}

// beginReplacementRestore takes the session reservation lock. Callers must
// not hold m.mu.
func (m *SessionManager) beginReplacementRestore(sessionID string) *bloemReplacementRestore {
	return &bloemReplacementRestore{m: m, sessionID: sessionID, unlock: m.lockSessionReservation(sessionID)}
}

// takeLocked moves the displaced reservation out of session. Callers hold m.mu.
func (r *bloemReplacementRestore) takeLocked(session *Session) {
	r.previous = session.replacementReservationPrevious
	session.replacementReservationPrevious = nil
}

// finishCancel restores what takeLocked moved out, then releases the session
// reservation lock.
func (r *bloemReplacementRestore) finishCancel() {
	defer r.unlock()
	if err := r.m.restoreFleetReplacement(r.sessionID, r.previous); err != nil {
		slog.Warn("failed to restore playback reservation after canceled replacement; session removed", "component", "playback", "session", r.sessionID, "error", err)
	}
}

// finishRollback restores previous after a successful in-memory rollback,
// then releases the session reservation lock.
//
// The in-memory rollback commits first under the same CAS as before; the
// fleet restore follows outside m.mu, bounded by reservationStoreTimeout,
// with the session reservation lock still held so no other reservation
// change for this session interleaves. Until it lands the store may still
// describe the replacement's play method.
func (r *bloemReplacementRestore) finishRollback(previous *ReservationRequest, errp *error) {
	defer r.unlock()
	if *errp != nil {
		return
	}
	if err := r.m.restoreFleetReplacement(r.sessionID, previous); err != nil {
		*errp = fmt.Errorf("restore playback reservation during rollback: %w", err)
	}
}

// startPreflight runs the Bloem subject-context provider, the limit lookup,
// and the playback/tenant-frozen gates ahead of StartSessionWithFilesContext's
// admission loop, and builds the candidate session the loop admits. The
// candidate is built once so its fleet reservation keeps one session ID
// across admission retries.
func (m *SessionManager) startPreflight(
	ctx context.Context,
	userID int,
	profileID string,
	effectiveFileID int,
	requestedFileID int,
	method PlayMethod,
	transcodeAudio bool,
) (context.Context, SessionLimits, *Session, error) {
	m.mu.RLock()
	contextProvider := m.contextProvider
	m.mu.RUnlock()
	if contextProvider != nil {
		var err error
		ctx, err = contextProvider(ctx, userID, profileID)
		if err != nil {
			return ctx, SessionLimits{}, nil, err
		}
		if ctx == nil {
			return ctx, SessionLimits{}, nil, errors.New("playback context provider returned nil context")
		}
	}
	limits, err := m.limitsForUser(ctx, userID, profileID)
	if err != nil {
		return ctx, SessionLimits{}, nil, err
	}
	if limits.PlaybackDisabled {
		return ctx, SessionLimits{}, nil, ErrPlaybackNotAllowed
	}
	if limits.TenantID != "" && limits.TenantFrozen {
		return ctx, SessionLimits{}, nil, ErrTenantFrozen
	}
	candidate := newSession(ctx, userID, profileID, effectiveFileID, requestedFileID, method, transcodeAudio)
	candidate.TenantID = limits.TenantID
	return ctx, limits, candidate, nil
}

// admitInlineCandidate finishes the inline (no policy decider) admission
// path: it reserves fleet capacity without holding m.mu, rechecks the inline
// limits, and publishes the candidate. Callers must not hold m.mu.
func (m *SessionManager) admitInlineCandidate(ctx context.Context, candidate *Session, limits SessionLimits, userID int, method PlayMethod, transcodeAudio bool) (*Session, error) {
	if err := m.acquireFleetReservation(ctx, candidate, limits); err != nil {
		return nil, err
	}
	m.mu.Lock()
	if err := m.inlineAdmissionErrorLocked(userID, method, transcodeAudio, limits); err != nil {
		m.mu.Unlock()
		m.releaseFleetReservation(candidate)
		return nil, err
	}
	m.sessions[candidate.ID] = candidate
	m.mu.Unlock()
	return candidate, nil
}

// commitDecidedCandidateLocked publishes a candidate the policy decider
// admitted. Callers hold m.mu; it returns with m.mu released.
//
// The tenant gate runs even on the decider path: the shared pool and the
// frozen flag are sold entitlements, not per-account policy.
func (m *SessionManager) commitDecidedCandidateLocked(candidate *Session, method PlayMethod, limits SessionLimits) (*Session, error) {
	if err := m.tenantAdmissionErrorLocked(method, limits); err != nil {
		m.mu.Unlock()
		m.releaseFleetReservation(candidate)
		return nil, err
	}
	m.sessions[candidate.ID] = candidate
	m.mu.Unlock()
	return candidate, nil
}

// bloemReconstructReservation holds the reservation lifecycle lock and the
// session reservation lock across RegisterReconstructedWithLimits, and the
// fleet reservation the reconstructed session acquired.
type bloemReconstructReservation struct {
	m        *SessionManager
	session  *Session
	acquired bool
	unlock   func()
}

// reserveReconstructed runs the Bloem first admission pass for a
// reconstructed session and acquires its fleet reservation without holding
// m.mu. It returns an already-registered session instead when one exists.
// The returned reservation must be finished (deferred) by the caller, which
// then repeats admission under m.mu before publishing the session.
func (m *SessionManager) reserveReconstructed(ctx context.Context, s *Session, limits SessionLimits) (*bloemReconstructReservation, *Session, error) {
	m.reservationLifecycle.Lock()
	unlockReservation := m.lockSessionReservation(s.ID)
	r := &bloemReconstructReservation{m: m, session: s, unlock: func() {
		unlockReservation()
		m.reservationLifecycle.Unlock()
	}}

	m.mu.Lock()
	if existing, ok := m.sessions[s.ID]; ok {
		m.mu.Unlock()
		return r, existing, nil
	}
	if limits.PlaybackDisabled {
		m.mu.Unlock()
		return r, nil, ErrPlaybackNotAllowed
	}
	// The session being reconstructed is not yet in the map, so the live counts
	// reflect the user's *other* sessions; admitting one more must stay within cap.
	if err := m.inlineAdmissionErrorLocked(s.UserID, s.PlayMethod, s.TranscodeAudio, limits); err != nil {
		m.mu.Unlock()
		return r, nil, err
	}
	s.TenantID = limits.TenantID
	m.mu.Unlock()

	if err := m.acquireFleetReservation(ctx, s, limits); err != nil {
		return r, nil, err
	}
	r.acquired = true
	return r, nil, nil
}

// adoptLocked moves the reservation just acquired onto a session another
// caller registered meanwhile. Callers hold m.mu.
func (r *bloemReconstructReservation) adoptLocked(existing *Session) {
	applyFleetReservationLocked(existing, Reservation{
		SessionID:  r.session.ID,
		Generation: r.session.reservationGeneration,
		LeaseUntil: r.session.reservationLeaseUntil,
	}, r.session.reservationRequest)
}

// finish releases the acquired reservation when admission failed, then
// releases the locks. It runs deferred, after m.mu is released.
func (r *bloemReconstructReservation) finish(errp *error) {
	defer r.unlock()
	if *errp != nil && r.acquired {
		r.m.releaseFleetReservation(r.session)
	}
}
