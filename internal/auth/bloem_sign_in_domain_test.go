package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Silo-Server/silo-server/internal/models"
)

// countingExec stands in for the database a session is inserted into and
// counts the inserts it was asked to run.
type countingExec struct{ inserts int }

func (c *countingExec) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	c.inserts++
	return pgconn.CommandTag{}, nil
}

// domainCheckedService is a service built the way NewService builds one, over
// shared, the repository other services (device sign-in) also hold.
func domainCheckedService(shared *SessionRepository, check SignInDomainCheck) *Service {
	svc := &Service{sessions: shared}
	svc.bloemUseRepositories(shared, nil)
	svc.SetSignInDomainCheck(check)
	return svc
}

func TestSignInDomainCheckRefusesBeforeCreating(t *testing.T) {
	var checked []int
	svc := domainCheckedService(&SessionRepository{}, func(_ context.Context, accountID int) error {
		checked = append(checked, accountID)
		return &OrganizationDomainError{SignInURL: "https://main.domain.com"}
	})
	ctx := context.Background()
	db := &countingExec{}
	// Silo's lines create sessions through the concrete sessions field (password
	// and provider sign-in, identity sessions); Bloem's lines (direct-profile
	// sign-in, setup) through bloemSessions. Every path ends in createWithQuerier.
	for name, create := range map[string]func() error{
		"Silo's sessions.Create":                  func() error { return svc.sessions.Create(ctx, models.AuthSession{UserID: 5}) },
		"Silo's sessions.createWithQuerier":       func() error { return svc.sessions.createWithQuerier(ctx, db, models.AuthSession{UserID: 5}) },
		"Bloem's bloemSessions.createWithQuerier": func() error { return svc.bloemSessions.createWithQuerier(ctx, db, models.AuthSession{UserID: 5}) },
	} {
		var domainErr *OrganizationDomainError
		if err := create(); !errors.As(err, &domainErr) || domainErr.SignInURL != "https://main.domain.com" {
			t.Errorf("%s: %v, want the organization domain refusal", name, err)
		}
	}
	if db.inserts != 0 {
		t.Fatalf("inserted %d sessions, want none", db.inserts)
	}
	if len(checked) != 3 || checked[0] != 5 {
		t.Fatalf("checked %v, want account 5 three times", checked)
	}
}

func TestSignInDomainCheckLeavesTheSharedRepositoryUnchecked(t *testing.T) {
	shared := &SessionRepository{}
	domainCheckedService(shared, func(context.Context, int) error {
		t.Fatal("a session created through the shared repository was checked")
		return nil
	})
	db := &countingExec{}
	if err := shared.createWithQuerier(context.Background(), db, models.AuthSession{UserID: 5}); err != nil || db.inserts != 1 {
		t.Fatalf("err=%v inserts=%d, want the device sign-in session created", err, db.inserts)
	}
}

func TestSignInDomainCheckAllowsWhenTheCheckPasses(t *testing.T) {
	svc := domainCheckedService(&SessionRepository{}, func(context.Context, int) error { return nil })
	db := &countingExec{}
	if err := svc.sessions.createWithQuerier(context.Background(), db, models.AuthSession{UserID: 5}); err != nil || db.inserts != 1 {
		t.Fatalf("err=%v inserts=%d, want the session created", err, db.inserts)
	}
}

func TestSignInDomainCheckSkipsImpersonation(t *testing.T) {
	svc := domainCheckedService(&SessionRepository{}, func(context.Context, int) error {
		t.Fatal("an impersonation session was checked")
		return nil
	})
	admin := 1
	db := &countingExec{}
	if err := svc.sessions.createWithQuerier(context.Background(), db, models.AuthSession{UserID: 5, ImpersonatorUserID: &admin}); err != nil || db.inserts != 1 {
		t.Fatalf("err=%v inserts=%d", err, db.inserts)
	}
}

func TestSetSignInDomainCheckDoesNotStack(t *testing.T) {
	calls := 0
	check := func(context.Context, int) error { calls++; return nil }
	svc := domainCheckedService(&SessionRepository{}, check)
	svc.SetSignInDomainCheck(check)
	if err := svc.sessions.createWithQuerier(context.Background(), &countingExec{}, models.AuthSession{UserID: 5}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("check ran %d times, want once", calls)
	}
}

func TestOrganizationDomainErrorMessage(t *testing.T) {
	if got := (&OrganizationDomainError{SignInURL: "https://main.domain.com"}).Error(); got != "account signs in at https://main.domain.com" {
		t.Fatalf("Error() = %q", got)
	}
}
