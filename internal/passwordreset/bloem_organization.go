package passwordreset

// Bloem-owned. An organization administrator's reset link for one member of
// their own organization.
//
// Silo's Issue cannot serve: it opens its own transaction, cannot see
// organization roles (an organization owner or administrator is users.role
// "user", so the Owner rules do not protect them), and its audit row goes to
// activity_log, which does not record a request under /api/bloem/v2. This file
// judges the shared authority rules (adminpeople.LockAuthority) under the same
// row locks as the write and records the audit event in the same transaction.
// Completing the link is Silo's public route, unchanged.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/adminpeople"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/mail"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// Lifetimes of an organization administrator's links. A link a person carries
// by hand is more exposed than one that goes to the account's own mailbox.
const (
	OrganizationLinkTTL  = 4 * time.Hour
	OrganizationEmailTTL = DefaultTTL
)

// Delivery statuses reported to the administrator.
const (
	DeliveryStatusNotRequested     = "not_requested"
	DeliveryStatusSent             = "sent"
	DeliveryStatusFailedOrUnknown  = "failed_or_unknown"
	organizationResetAction        = "people.password_reset_issued"
	organizationResetRefusedAction = "people.password_reset_refused"
)

// Refusals specific to a reset. The shared ones (a protected target, a shared
// account, a stale revision) are adminpeople's.
var (
	ErrMembershipNotActive = adminpeople.ErrMembershipNotActive
	// ErrDeliveryUnavailable reports a missing mail server or public URL; the
	// wrapped error says which.
	ErrDeliveryUnavailable = errors.New("reset link delivery is not configured")
)

// OrganizationIssueInput is an organization administrator's request.
type OrganizationIssueInput struct {
	OrganizationID   uuid.UUID
	Actor            adminpeople.MutationActor
	TargetAccountID  int
	ExpectedRevision int64
	Delivery         Delivery
}

// OrganizationIssueResult reports a stored link. It may accompany a delivery
// error only through DeliveryStatus: a stored link is never an error.
type OrganizationIssueResult struct {
	Delivery       Delivery
	DeliveryStatus string
	// URL is set for DeliveryLink only. It embeds the raw token and is shown
	// once, to the issuing administrator, in this response.
	URL       string
	ExpiresAt time.Time
}

// organizationRepository is what IssueForOrganization needs from storage.
type organizationRepository interface {
	issueForOrganization(ctx context.Context, in organizationIssue) error
}

type organizationIssue struct {
	OrganizationIssueInput
	TokenHash string
	ExpiresAt time.Time
	// Prepare runs under the locks, after the account is judged eligible and
	// before the link is stored. An error refuses the request, audited, and
	// leaves the account's current link in place.
	Prepare func(target organizationTarget) error
	// Done receives the facts the notice and the email need.
	Done func(target organizationTarget)
}

type organizationTarget struct {
	AccountID        int
	Email            string
	Username         string
	OrganizationName string
}

func (r *Repository) issueForOrganization(ctx context.Context, in organizationIssue) error {
	_, err := adminpeople.RunSupportTx(ctx, r.pool.Begin, func(tx pgx.Tx) (struct{}, error) {
		return struct{}{}, r.issueForOrganizationTx(ctx, tx, in)
	})
	return err
}

func (r *Repository) issueForOrganizationTx(ctx context.Context, tx pgx.Tx, in organizationIssue) error {
	// Serialize this organization's reset budget with the writes it counts.
	if err := adminpeople.LockBudget(ctx, tx, adminpeople.BudgetDisclose, in.OrganizationID); err != nil {
		return err
	}
	if err := adminpeople.CheckBudget(ctx, tx, adminpeople.BudgetDisclose, in.OrganizationID, in.Actor.AccountID, fmt.Sprint(in.TargetAccountID)); err != nil {
		return err
	}
	event := adminpeople.AuditEvent{
		Actor: in.Actor, Action: organizationResetRefusedAction, TargetType: "membership",
		OrganizationID: in.OrganizationID, SubjectAccountID: in.TargetAccountID,
	}
	refuse := func(reason string, cause error, locked adminpeople.LockedAuthority) error {
		event.TargetID = locked.Target.MembershipID.String()
		event.BeforeRevision, event.AfterRevision = locked.Target.SecurityRevision, locked.Target.SecurityRevision
		return adminpeople.RefuseAudited(ctx, tx, event, reason, cause)
	}

	locked, err := adminpeople.LockAuthority(ctx, tx, in.OrganizationID, in.Actor, in.TargetAccountID)
	if err != nil {
		var protected *adminpeople.TargetProtectedError
		if errors.As(err, &protected) {
			return refuse(string(protected.Reason), err, locked)
		}
		return err
	}
	if err := locked.RequireUnshared(); err != nil {
		return refuse("account_shared", err, locked)
	}
	if locked.Target.SecurityRevision != in.ExpectedRevision {
		return adminpeople.ErrAuthorizationStateChanged
	}
	if locked.Target.Status != tenancy.MembershipActive {
		return refuse("membership_not_active", ErrMembershipNotActive, locked)
	}

	target := organizationTarget{AccountID: in.TargetAccountID}
	var enabled, localPassword, hasPassword bool
	if err := tx.QueryRow(ctx, `SELECT u.enabled, u.local_password_login_enabled, u.password_hash <> '', COALESCE(u.email::text, ''), COALESCE(u.username, '') FROM users u WHERE u.id = $1`, in.TargetAccountID).
		Scan(&enabled, &localPassword, &hasPassword, &target.Email, &target.Username); err != nil {
		return fmt.Errorf("read reset target: %w", err)
	}
	switch {
	case !enabled:
		return refuse("account_disabled", ErrAccountDisabled, locked)
	case !localPassword || !hasPassword:
		return refuse("password_login_disabled", auth.ErrPasswordLoginDisabled, locked)
	}
	if err := tx.QueryRow(ctx, `SELECT name FROM organizations WHERE id = $1`, in.OrganizationID).Scan(&target.OrganizationName); err != nil {
		return fmt.Errorf("read organization name: %w", err)
	}
	// Refuse before minting: storing a link replaces the account's live one,
	// even when nothing can deliver the new one.
	if in.Prepare != nil {
		if err := in.Prepare(target); err != nil {
			return refuse(prepareReason(err), err, locked)
		}
	}

	tag, err := tx.Exec(ctx, `
		INSERT INTO password_reset_tokens (user_id, token_hash, password_fingerprint, issued_by, expires_at)
		SELECT u.id, $2, `+passwordFingerprint+`, $3, $4 FROM users u
		WHERE u.id = $1 AND `+eligibleAccount+`
		ON CONFLICT (user_id) DO UPDATE SET
			token_hash = EXCLUDED.token_hash,
			password_fingerprint = EXCLUDED.password_fingerprint,
			issued_by = EXCLUDED.issued_by,
			expires_at = EXCLUDED.expires_at,
			created_at = now()`,
		in.TargetAccountID, in.TokenHash, in.Actor.AccountID, in.ExpiresAt)
	if err != nil {
		return fmt.Errorf("issuing organization password reset link: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return refuse("password_login_disabled", ErrNotEligible, locked)
	}
	// The event names the delivery and expiry only: never the link, its
	// digest, the address or the account's password state.
	status := DeliveryStatusNotRequested
	if in.Delivery == DeliveryEmail {
		status = "requested"
	}
	if err := adminpeople.RecordAudit(ctx, tx, adminpeople.AuditEvent{
		Actor: in.Actor, Action: organizationResetAction, TargetType: "membership", TargetID: locked.Target.MembershipID.String(),
		OrganizationID: in.OrganizationID, SubjectAccountID: in.TargetAccountID,
		BeforeRevision: locked.Target.SecurityRevision, AfterRevision: locked.Target.SecurityRevision,
		Outcome: "success",
		After:   map[string]any{"delivery": string(in.Delivery), "delivery_status": status, "expires_at": in.ExpiresAt.UTC().Format(time.RFC3339)},
	}); err != nil {
		return err
	}
	if in.Done != nil {
		in.Done(target)
	}
	return nil
}

func prepareReason(err error) string {
	switch {
	case errors.Is(err, ErrNoEmail):
		return "email_unavailable"
	case errors.Is(err, ErrDeliveryUnavailable):
		return "capability_not_configured"
	default:
		return "refused"
	}
}

// IssueForOrganization stores a single-use reset link for a member of the
// administrator's own organization and delivers it. The administrator never
// types or sees a password; with DeliveryLink the link is returned once.
//
// ctx must carry the target organization for the link host and branding (the
// settings overlay follows the organization in the context).
func (s *Service) IssueForOrganization(ctx context.Context, in OrganizationIssueInput) (*OrganizationIssueResult, error) {
	if in.Delivery != DeliveryEmail && in.Delivery != DeliveryLink {
		return nil, ErrUnknownDelivery
	}
	orgRepo, ok := s.repo.(organizationRepository)
	if !ok {
		return nil, errors.New("password reset storage does not support organization links")
	}
	token, tokenHash, err := auth.NewLinkToken()
	if err != nil {
		return nil, fmt.Errorf("generate password reset token: %w", err)
	}
	ttl := OrganizationLinkTTL
	if in.Delivery == DeliveryEmail {
		ttl = OrganizationEmailTTL
	}
	expiresAt := s.now().Add(ttl)
	linkBase := s.linkBase(ctx)

	var target organizationTarget
	err = orgRepo.issueForOrganization(ctx, organizationIssue{
		OrganizationIssueInput: in,
		TokenHash:              tokenHash,
		ExpiresAt:              expiresAt,
		Prepare: func(target organizationTarget) error {
			if linkBase == "" {
				return fmt.Errorf("%w: %w", ErrDeliveryUnavailable, ErrNoLinkBase)
			}
			if in.Delivery != DeliveryEmail {
				return nil
			}
			if _, err := auth.ValidateEmail(target.Email); err != nil {
				return ErrNoEmail
			}
			if s.mail == nil || !s.mail.Enabled(ctx) {
				return fmt.Errorf("%w: %w", ErrDeliveryUnavailable, mail.ErrNotConfigured)
			}
			return nil
		},
		Done: func(found organizationTarget) { target = found },
	})
	if err != nil {
		return nil, err
	}

	resetURL := linkBase + "/reset-password/" + token
	result := &OrganizationIssueResult{Delivery: in.Delivery, ExpiresAt: expiresAt}
	if in.Delivery == DeliveryLink {
		result.URL = resetURL
		result.DeliveryStatus = DeliveryStatusNotRequested
		s.sendResetNotice(ctx, target)
		return result, nil
	}
	// The link is live from here on. A failed or uncertain send is reported,
	// not returned as an error: retrying replaces the link.
	result.DeliveryStatus = DeliveryStatusFailedOrUnknown
	brand := s.brand.Load(ctx)
	content := composeResetEmail(brand, false, target.Username, s.serverName(ctx), resetURL, expiresAt, s.now())
	if err := s.mail.Send(ctx, mail.Message{
		To:       []string{target.Email},
		Subject:  content.Subject,
		TextBody: content.Text,
		HTMLBody: content.HTML,
		Inline:   brand.InlineImages(),
	}); err != nil {
		slog.WarnContext(ctx, "organization password reset email failed or is uncertain", "component", "passwordreset", "error", err)
		return result, nil
	}
	result.DeliveryStatus = DeliveryStatusSent
	return result, nil
}

// sendResetNotice tells the account holder, in the background, that an
// administrator of their organization issued a reset for their account. It
// carries no link. A failure is logged and never fails the request.
func (s *Service) sendResetNotice(ctx context.Context, target organizationTarget) {
	if s.mail == nil || target.Email == "" {
		return
	}
	if _, err := auth.ValidateEmail(target.Email); err != nil {
		return
	}
	select {
	case s.pending <- struct{}{}:
	default:
		slog.WarnContext(ctx, "password reset notice dropped: too many in flight", "component", "passwordreset")
		return
	}
	s.async(func() {
		defer func() { <-s.pending }()
		defer func() {
			if r := recover(); r != nil {
				slog.ErrorContext(ctx, "password reset notice panicked", "component", "passwordreset", "panic", r)
			}
		}()
		bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), selfServiceTimeout)
		defer cancel()
		if !s.mail.Enabled(bg) {
			return
		}
		brand := s.brand.Load(bg)
		content := composeResetNoticeEmail(brand, target.Username, target.OrganizationName, s.serverName(bg))
		if err := s.mail.Send(bg, mail.Message{
			To:       []string{target.Email},
			Subject:  content.Subject,
			TextBody: content.Text,
			HTMLBody: content.HTML,
			Inline:   brand.InlineImages(),
		}); err != nil {
			slog.WarnContext(bg, "password reset notice email failed", "component", "passwordreset", "error", err)
		}
	})
}
