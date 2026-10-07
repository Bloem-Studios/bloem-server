package catalog

import (
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	nativeStorageUnavailableCode        = "native_storage_unavailable"
	nativeLocalOperationUnsupportedCode = "native_local_operation_unsupported"
)

// NativeOnboardingError carries a fixed public code and an internal cause.
// Authorized command handlers alone may expose the current revisions.
type NativeOnboardingError struct {
	Code                                          string
	CurrentSourceRevision, CurrentLibraryRevision *int64
	Cause                                         error
}

func (e *NativeOnboardingError) Error() string { return "Native storage admission unavailable" }
func (e *NativeOnboardingError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}
func MapNativeOnboardingError(err error) error {
	if err == nil {
		return nil
	}
	var unknown *MutationOutcomeUnknown
	if errors.As(err, &unknown) {
		return unknown
	}
	var existing *NativeOnboardingError
	if errors.As(err, &existing) {
		return existing
	}
	code := nativeStorageUnavailableCode
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) {
		switch pgerr.Code {
		case "BN001":
			code = nativeLocalOperationUnsupportedCode
		case "BN002", "BN003", "55P03", "40P01", "40001":
			code = nativeStorageUnavailableCode
		}
	}
	return &NativeOnboardingError{Code: code, Cause: err}
}

// MutationOutcomeUnknown records identifiers allocated before a commit attempt.
// An acknowledgement failure is reconciled through the original operation;
// callback completion alone is never proof of a successful commit.
type MutationOutcomeUnknown struct {
	OperationID uuid.UUID  `json:"operation_id"`
	CreationKey uuid.UUID  `json:"creation_key,omitempty"`
	LibraryID   int        `json:"library_id,omitempty"`
	SourceKey   *uuid.UUID `json:"source_key,omitempty"`
	ScanRunID   *string    `json:"scan_run_id,omitempty"`
	Operation   string     `json:"operation"`
	Cause       error      `json:"-"`
}

func (e *MutationOutcomeUnknown) Error() string { return "Mutation outcome requires reconciliation" }
func (e *MutationOutcomeUnknown) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}
