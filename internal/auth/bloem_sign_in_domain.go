package auth

// Bloem-owned. Refusing a sign-in on another organization's domain: every
// sign-in ends by creating a session, so the check runs there, before the
// session exists. See docs/architecture/bloem-organization-domains.md.

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/models"
)

// OrganizationDomainError refuses a sign-in that arrived on another
// organization's domain. SignInURL is the account's own address, empty when
// none is known.
type OrganizationDomainError struct {
	SignInURL string
}

func (e *OrganizationDomainError) Error() string {
	if e.SignInURL == "" {
		return "account signs in at its organization's own address"
	}
	return "account signs in at " + e.SignInURL
}

// SignInDomainCheck decides whether accountID may sign in on the domain ctx's
// request arrived on: nil allows, *OrganizationDomainError refuses, any other
// error fails the sign-in.
type SignInDomainCheck func(ctx context.Context, accountID int) error

// SetSignInDomainCheck makes every new session pass check first. Setting it
// again replaces the previous check.
func (s *Service) SetSignInDomainCheck(check SignInDomainCheck) {
	if check == nil {
		return
	}
	inner := s.sessions
	if wrapped, ok := inner.(domainCheckedSessions); ok {
		inner = wrapped.serviceSessionRepository
	}
	s.sessions = domainCheckedSessions{serviceSessionRepository: inner, check: check}
}

// domainCheckedSessions is the session repository with the domain check in
// front of every way a session is created. An impersonation session is an
// administrator's, not a sign-in, and is not checked.
type domainCheckedSessions struct {
	serviceSessionRepository
	check SignInDomainCheck
}

func (d domainCheckedSessions) allowed(ctx context.Context, session models.AuthSession) error {
	if session.ImpersonatorUserID != nil {
		return nil
	}
	return d.check(ctx, session.UserID)
}

func (d domainCheckedSessions) Create(ctx context.Context, session models.AuthSession) error {
	if err := d.allowed(ctx, session); err != nil {
		return err
	}
	return d.serviceSessionRepository.Create(ctx, session)
}

func (d domainCheckedSessions) CreateProfileSessionIfCurrent(ctx context.Context, session models.AuthSession, subject SessionSubject) error {
	if err := d.allowed(ctx, session); err != nil {
		return err
	}
	return d.serviceSessionRepository.CreateProfileSessionIfCurrent(ctx, session, subject)
}

func (d domainCheckedSessions) createWithQuerier(ctx context.Context, q sessionExecQuerier, session models.AuthSession) error {
	if err := d.allowed(ctx, session); err != nil {
		return err
	}
	return d.serviceSessionRepository.createWithQuerier(ctx, q, session)
}
