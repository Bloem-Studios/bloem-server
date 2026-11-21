package playback

import (
	"context"
	"errors"
	"time"
)

var (
	ErrReservationGenerationMismatch = errors.New("playback reservation generation mismatch")
	ErrReservationInvalid            = errors.New("playback reservation request is invalid")
)

// ReservationRequest is the complete shared-capacity fact set frozen for one
// playback admission attempt. Zero limits mean unlimited.
type ReservationRequest struct {
	SessionID         string
	AccountID         int
	ProfileID         string
	TenantID          string
	IsTranscode       bool
	AccountStreams    int
	AccountTranscodes int
	TenantTranscodes  int
	// The household's shared pool (bloem_household_reservation.go):
	// HouseholdID 0 is no pooled household.
	HouseholdID         int
	HouseholdStreams    int
	HouseholdTranscodes int
	LeaseUntil          time.Time
}

// Reservation is the fenced ownership token for one shared capacity slot.
type Reservation struct {
	SessionID  string
	Generation int64
	LeaseUntil time.Time
}

// ReservationStore is the fleet-wide playback admission authority. Release
// and renewal only affect the exact generation returned by Acquire.
type ReservationStore interface {
	Acquire(context.Context, ReservationRequest) (Reservation, error)
	Renew(context.Context, string, int64, time.Time) (Reservation, error)
	Release(context.Context, string, int64) error
}

func (request ReservationRequest) valid(now time.Time) bool {
	// A household session reserves even without a profile, so nothing plays
	// outside the household's pool.
	return request.SessionID != "" && request.AccountID > 0 && (request.ProfileID != "" || request.HouseholdID > 0) &&
		request.AccountStreams >= 0 && request.AccountTranscodes >= 0 && request.TenantTranscodes >= 0 &&
		request.HouseholdID >= 0 && request.HouseholdStreams >= 0 && request.HouseholdTranscodes >= 0 &&
		request.LeaseUntil.After(now)
}
