package playback

import (
	"context"
	"errors"
	"sync/atomic"
)

// LimitKind is which limit refused a stream.
type LimitKind string

const (
	LimitStreams    LimitKind = "streams"
	LimitTranscodes LimitKind = "transcodes"
)

// LimitScope is whose limit it was: the person's own, their household's
// shared pool, or their organization's transcode pool.
type LimitScope string

const (
	ScopePerson       LimitScope = "person"
	ScopeHousehold    LimitScope = "household"
	ScopeOrganization LimitScope = "organization"
)

// LimitRefusalObserver hears every refusal at a stream or transcode limit,
// for stream insights. It runs on the refused request's path (with no
// session manager lock held) and must return at once.
type LimitRefusalObserver interface {
	LimitRefused(ctx context.Context, userID int, profileID string, kind LimitKind, scope LimitScope)
}

// SetLimitRefusalObserver installs the observer the session manager reports
// to; nil removes it. It hears fresh starts (noteStreamLimit), switches the
// household or organization pool refuses (admitReplacementLocked) and
// rebuilt sessions (the reconstruct reservation). A switch the admission
// decider refuses reaches SetAdmissionRefusalObserver's observer instead.
func (m *SessionManager) SetLimitRefusalObserver(observer LimitRefusalObserver) {
	m.mu.Lock()
	m.limitRefusalObserver = observer
	m.mu.Unlock()
}

// classifyLimitRefusal names the limit err is a refusal at. The household
// and tenant errors wrap the generic ones, so they are checked first.
func classifyLimitRefusal(err error) (LimitKind, LimitScope, bool) {
	switch {
	case err == nil:
		return "", "", false
	case errors.Is(err, ErrHouseholdStreamsExceeded):
		return LimitStreams, ScopeHousehold, true
	case errors.Is(err, ErrHouseholdTranscodesExceeded):
		return LimitTranscodes, ScopeHousehold, true
	case errors.Is(err, ErrTenantTranscodesExceeded):
		return LimitTranscodes, ScopeOrganization, true
	case errors.Is(err, ErrTooManyStreams):
		return LimitStreams, ScopePerson, true
	case errors.Is(err, ErrTooManyTranscodes):
		return LimitTranscodes, ScopePerson, true
	}
	return "", "", false
}

// noteLimitRefusal reports err to the observer when it is a limit refusal.
// Callers must not hold m.mu.
func (m *SessionManager) noteLimitRefusal(ctx context.Context, userID int, profileID string, err error) {
	kind, scope, ok := classifyLimitRefusal(err)
	if !ok {
		return
	}
	m.mu.RLock()
	observer := m.limitRefusalObserver
	m.mu.RUnlock()
	if observer != nil {
		observer.LimitRefused(ctx, userID, profileID, kind, scope)
	}
}

// freshStartKey marks the context of a fresh start, whose refusal
// noteStreamLimit reports; the admission decider then stays quiet about it.
type freshStartKey struct{}

func withFreshStart(ctx context.Context) context.Context {
	return context.WithValue(ctx, freshStartKey{}, true)
}

func isFreshStart(ctx context.Context) bool {
	marked, _ := ctx.Value(freshStartKey{}).(bool)
	return marked
}

type limitRefusalObserverBox struct{ observer LimitRefusalObserver }

var admissionRefusalObserver atomic.Pointer[limitRefusalObserverBox]

// SetAdmissionRefusalObserver installs the observer NoteAdmissionRefusal
// reports to, process-wide; nil removes it. The admission decider is built
// apart from any one session manager, so it reports here.
func SetAdmissionRefusalObserver(observer LimitRefusalObserver) {
	if observer == nil {
		admissionRefusalObserver.Store(nil)
		return
	}
	admissionRefusalObserver.Store(&limitRefusalObserverBox{observer: observer})
}

// NoteAdmissionRefusal is how an admission decider reports a denial for
// reasonCode. A fresh start's denial is left to noteStreamLimit, which
// knows the profile, so each refusal is heard once; any other (a mid-stream
// switch) is reported for the account, with no profile.
func NoteAdmissionRefusal(ctx context.Context, userID int, reasonCode string) {
	if ctx != nil && isFreshStart(ctx) {
		return
	}
	kind, scope, ok := classifyLimitRefusal(admissionDenyError(reasonCode))
	if !ok {
		return
	}
	box := admissionRefusalObserver.Load()
	if box == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	box.observer.LimitRefused(ctx, userID, "", kind, scope)
}
