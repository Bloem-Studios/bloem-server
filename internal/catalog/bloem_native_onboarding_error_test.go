package catalog

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestNativeOnboardingErrors(t *testing.T) {
	for _, code := range []string{"BN001", "BN002", "BN003", "55P03", "40P01", "40001", "23505"} {
		t.Run(code, func(t *testing.T) {
			cause := &pgconn.PgError{Code: code, Message: "private-identifier", Detail: "private-argument"}
			mapped := MapNativeOnboardingError(fmt.Errorf("private-sql: %w", cause))
			var got *NativeOnboardingError
			if !errors.As(mapped, &got) {
				t.Fatal("typed public error required")
			}
			want := "native_storage_unavailable"
			if code == "BN001" {
				want = "native_local_operation_unsupported"
			}
			if got.Code != want {
				t.Fatalf("public code=%s want=%s", got.Code, want)
			}
			if strings.Contains(got.Error(), "private") {
				t.Fatal("private SQL leaked")
			}
			if !errors.Is(got, cause) {
				t.Fatal("internal cause lost")
			}
		})
	}
	if MapNativeOnboardingError(nil) != nil {
		t.Fatal("nil error changed")
	}
}

func TestNativeOnboardingUnknownOutcome(t *testing.T) {
	cause := errors.New("private SQL arguments")
	got := &MutationOutcomeUnknown{Cause: cause}
	if got.Error() != "Mutation outcome requires reconciliation" {
		t.Fatal("fixed reconciliation message required")
	}
	if !errors.Is(got, cause) {
		t.Fatal("internal cause lost")
	}
}

func TestNativeOnboardingUnknownOutcomeMapping(t *testing.T) {
	original := &MutationOutcomeUnknown{Operation: "initialize", Cause: errors.New("private acknowledgement")}
	if got := MapNativeOnboardingError(fmt.Errorf("commit: %w", original)); got != original {
		t.Fatal("unknown commit outcome lost its reconciliation identity")
	}
}
