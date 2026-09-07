package playback

import (
	"context"
	"errors"
	"time"
)

var ErrStaleAttemptAuthorityV3 = errors.New("stale playback attempt authority")

type AttemptAuthorityStateV3 string

const (
	AttemptPreparingV3 AttemptAuthorityStateV3 = "preparing"
	AttemptActiveV3    AttemptAuthorityStateV3 = "active"
	AttemptTerminalV3  AttemptAuthorityStateV3 = "terminal"
	AttemptStoppedV3   AttemptAuthorityStateV3 = "stopped"
	AttemptDrainingV3  AttemptAuthorityStateV3 = "draining"
)

// AttemptAuthorityV3 fences one reservation generation. OwnerID identifies a
// process boot, not a stable node name. This is not a delivery authorization.
type AttemptAuthorityV3 struct {
	PlaybackAttemptID string
	OwnerID           string
	// Incarnation fences delayed calls after cleanup and same-boot attempt reuse.
	Incarnation    string
	Epoch          int64
	State          AttemptAuthorityStateV3
	LeaseExpiresAt time.Time
}

type AttemptReservationRequestV3 struct {
	// ExpectedAdmissionID is captured before reservation by the initial caller.
	// Empty identifies a legacy/unbound allocation, refused after first admission.
	ExpectedAdmissionID  string
	PlaybackAttemptID    string
	UserID               int
	ProfileID            string
	RequestedMediaFileID int
	// RequestDigest is the existing exact-body/normalized-device fingerprint.
	// The store compares it; it must never recompute it from NormalizedRequest.
	RequestDigest     string
	NormalizedRequest StartRequestV3
	OwnerID           string
	LeaseDuration     time.Duration
	Retention         time.Duration
}

type AttemptReservationV3 struct {
	// Owned is true only for the winning insert or expired preparing takeover.
	// Even the same owner's concurrent retry must not allocate another transport.
	Owned     bool
	Authority AttemptAuthorityV3
	Record    *AttemptRecordV3
}

// AuthoritativePlanStoreV3 extends the existing durable attempt store without
// making the legacy in-memory store claim distributed fencing. No lifecycle
// route uses this extension until delivery and personal-state fencing are ready.
// Transactions end before callers perform any transport work. Active-owner
// takeover and replan activation are deliberately outside this checkpoint.
type AuthoritativePlanStoreV3 interface {
	PlanStoreV3
	ReserveAttempt(context.Context, AttemptReservationRequestV3) (AttemptReservationV3, error)
	RenewAttempt(context.Context, AttemptAuthorityV3, time.Duration) (AttemptAuthorityV3, error)
	PublishAttempt(context.Context, AttemptAuthorityV3, AttemptRecordV3) error
	StopAttempt(context.Context, AttemptAuthorityV3) error
}
