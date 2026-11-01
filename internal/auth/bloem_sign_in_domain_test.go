package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
)

// recordingSessions counts the sessions it was asked to create; every other
// method of the embedded repository is left unimplemented.
type recordingSessions struct {
	serviceSessionRepository
	created int
}

func (r *recordingSessions) Create(context.Context, models.AuthSession) error {
	r.created++
	return nil
}

func (r *recordingSessions) CreateProfileSessionIfCurrent(context.Context, models.AuthSession, SessionSubject) error {
	r.created++
	return nil
}

func (r *recordingSessions) createWithQuerier(context.Context, sessionExecQuerier, models.AuthSession) error {
	r.created++
	return nil
}

func TestDomainCheckedSessionsRefusesBeforeCreating(t *testing.T) {
	inner := &recordingSessions{}
	svc := &Service{sessions: inner}
	var checked []int
	svc.SetSignInDomainCheck(func(_ context.Context, accountID int) error {
		checked = append(checked, accountID)
		return &OrganizationDomainError{SignInURL: "https://main.domain.com"}
	})
	ctx := context.Background()
	for name, create := range map[string]func() error{
		"Create": func() error { return svc.sessions.Create(ctx, models.AuthSession{UserID: 5}) },
		"CreateProfileSessionIfCurrent": func() error {
			return svc.sessions.CreateProfileSessionIfCurrent(ctx, models.AuthSession{UserID: 5}, SessionSubject{})
		},
		"createWithQuerier": func() error { return svc.sessions.createWithQuerier(ctx, nil, models.AuthSession{UserID: 5}) },
	} {
		var domainErr *OrganizationDomainError
		if err := create(); !errors.As(err, &domainErr) || domainErr.SignInURL != "https://main.domain.com" {
			t.Errorf("%s: %v, want the organization domain refusal", name, err)
		}
	}
	if inner.created != 0 {
		t.Fatalf("created %d sessions, want none", inner.created)
	}
	if len(checked) != 3 || checked[0] != 5 {
		t.Fatalf("checked %v, want account 5 three times", checked)
	}
}

func TestDomainCheckedSessionsAllowsWhenTheCheckPasses(t *testing.T) {
	inner := &recordingSessions{}
	svc := &Service{sessions: inner}
	svc.SetSignInDomainCheck(func(context.Context, int) error { return nil })
	if err := svc.sessions.Create(context.Background(), models.AuthSession{UserID: 5}); err != nil || inner.created != 1 {
		t.Fatalf("err=%v created=%d, want the session created", err, inner.created)
	}
}

func TestDomainCheckedSessionsSkipsImpersonation(t *testing.T) {
	inner := &recordingSessions{}
	svc := &Service{sessions: inner}
	svc.SetSignInDomainCheck(func(context.Context, int) error {
		t.Fatal("an impersonation session was checked")
		return nil
	})
	admin := 1
	if err := svc.sessions.Create(context.Background(), models.AuthSession{UserID: 5, ImpersonatorUserID: &admin}); err != nil || inner.created != 1 {
		t.Fatalf("err=%v created=%d", err, inner.created)
	}
}

func TestSetSignInDomainCheckDoesNotStack(t *testing.T) {
	inner := &recordingSessions{}
	svc := &Service{sessions: inner}
	calls := 0
	check := func(context.Context, int) error { calls++; return nil }
	svc.SetSignInDomainCheck(check)
	svc.SetSignInDomainCheck(check)
	_ = svc.sessions.Create(context.Background(), models.AuthSession{UserID: 5})
	if calls != 1 {
		t.Fatalf("check ran %d times, want once", calls)
	}
}

func TestOrganizationDomainErrorMessage(t *testing.T) {
	if got := (&OrganizationDomainError{SignInURL: "https://main.domain.com"}).Error(); got != "account signs in at https://main.domain.com" {
		t.Fatalf("Error() = %q", got)
	}
}
