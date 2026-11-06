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

// SetSignInDomainCheck makes every new session this service creates pass
// check first. Setting it again replaces the previous check.
//
// The service gets its own copy of its session repository carrying the check,
// used by Silo's lines (the sessions field) and Bloem's (bloemSessions) alike.
// The repository it was built with is shared with other services, such as
// device sign-in, and stays unchecked.
func (s *Service) SetSignInDomainCheck(check SignInDomainCheck) {
	if check == nil || s.sessions == nil {
		return
	}
	checked := *s.sessions
	checked.bloemSignInCheck = check
	if current, ok := s.bloemSessions.(*SessionRepository); ok && current == s.sessions {
		s.bloemSessions = &checked
	}
	s.sessions = &checked
}

// bloemCheckSignInDomain runs the repository's domain check for a new
// session. An impersonation session is an administrator's, not a sign-in, and
// is not checked.
func (r *SessionRepository) bloemCheckSignInDomain(ctx context.Context, session models.AuthSession) error {
	if r.bloemSignInCheck == nil || session.ImpersonatorUserID != nil {
		return nil
	}
	return r.bloemSignInCheck(ctx, session.UserID)
}
