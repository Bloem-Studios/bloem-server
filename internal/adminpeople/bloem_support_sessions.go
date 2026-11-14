package adminpeople

// Bloem-owned. Signing a member out of sessions and devices on behalf of the
// member's organization, and profile edits that need no Silo profile
// lifecycle (rename, clear a forgotten PIN).
//
// "Sign out everywhere" raises the member's security revision, which is what
// ends membership-bound tokens and older account-login sessions. A single
// session or device sign-out does not: it would sign the member out of
// everything. Nothing here shows an IP address, and an impersonation session
// of a platform administrator is neither listed nor individually revocable.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const maxListedSessions = 50

// SessionView is one live session of a member as support sees it.
type SessionView struct {
	ID             string     `json:"id"`
	DeviceName     string     `json:"device_name"`
	DeviceID       string     `json:"device_id"`
	DevicePlatform string     `json:"device_platform"`
	AuthMethod     string     `json:"auth_method"`
	ProfileID      *string    `json:"profile_id"`
	ProfileName    *string    `json:"profile_name"`
	CreatedAt      time.Time  `json:"created_at"`
	LastSeenAt     *time.Time `json:"last_seen_at"`
	ExpiresAt      time.Time  `json:"expires_at"`
}

// DeviceProfileView is a profile of the organization that used a device.
type DeviceProfileView struct {
	ProfileID   string `json:"profile_id"`
	ProfileName string `json:"profile_name"`
}

// DeviceView is one device a member's profiles have used.
type DeviceView struct {
	DeviceID   string              `json:"device_id"`
	Name       string              `json:"name"`
	Platform   string              `json:"platform"`
	LastSeenAt time.Time           `json:"last_seen_at"`
	Profiles   []DeviceProfileView `json:"profiles"`
}

// RevokedSessions is the result of signing a member out everywhere.
type RevokedSessions struct {
	Person   PersonSummary
	Sessions int
}

// ListSessions lists the member's live sessions, newest first, up to 50.
func (s *Service) ListSessions(ctx context.Context, organizationID uuid.UUID, actor MutationActor, accountID int) ([]SessionView, error) {
	locked, err := ReadAuthority(ctx, s.pool, organizationID, actor, accountID)
	if err != nil {
		return nil, err
	}
	if err := locked.RequireUnshared(); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT a.id, COALESCE(a.device_name,''), COALESCE(a.device_id,''), COALESCE(a.device_platform,''), a.auth_method,
		       a.profile_id, p.name, a.created_at, a.last_seen_at, a.expires_at
		FROM auth_sessions a
		LEFT JOIN user_profiles p ON p.user_id = a.user_id AND p.id = a.profile_id
		WHERE a.user_id = $1 AND a.revoked_at IS NULL AND a.expires_at > now()
		  AND a.impersonator_user_id IS NULL AND a.purpose = 'account' AND a.auth_method <> 'preview_guest'
		ORDER BY a.created_at DESC, a.id DESC LIMIT $2`, accountID, maxListedSessions)
	if err != nil {
		return nil, fmt.Errorf("list member sessions: %w", err)
	}
	defer rows.Close()
	sessions := []SessionView{}
	for rows.Next() {
		var view SessionView
		if err := rows.Scan(&view.ID, &view.DeviceName, &view.DeviceID, &view.DevicePlatform, &view.AuthMethod, &view.ProfileID, &view.ProfileName, &view.CreatedAt, &view.LastSeenAt, &view.ExpiresAt); err != nil {
			return nil, err
		}
		sessions = append(sessions, view)
	}
	return sessions, rows.Err()
}

// ListDevices lists the devices the member's profiles in this organization
// used, most recently seen first.
func (s *Service) ListDevices(ctx context.Context, organizationID uuid.UUID, actor MutationActor, accountID int) ([]DeviceView, error) {
	locked, err := ReadAuthority(ctx, s.pool, organizationID, actor, accountID)
	if err != nil {
		return nil, err
	}
	if err := locked.RequireUnshared(); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT d.device_id, d.device_name, d.device_platform, d.last_seen_at, d.profile_id, p.name
		FROM user_devices d
		JOIN user_profiles p ON p.user_id = d.user_id AND p.id = d.profile_id AND p.organization_id = $2
		WHERE d.user_id = $1
		ORDER BY d.last_seen_at DESC, d.device_id, p.name`, accountID, organizationID)
	if err != nil {
		return nil, fmt.Errorf("list member devices: %w", err)
	}
	defer rows.Close()
	devices := []DeviceView{}
	index := map[string]int{}
	for rows.Next() {
		var deviceID, name, platform, profileID, profileName string
		var lastSeen time.Time
		if err := rows.Scan(&deviceID, &name, &platform, &lastSeen, &profileID, &profileName); err != nil {
			return nil, err
		}
		position, ok := index[deviceID]
		if !ok {
			position = len(devices)
			index[deviceID] = position
			devices = append(devices, DeviceView{DeviceID: deviceID, Name: name, Platform: platform, LastSeenAt: lastSeen, Profiles: []DeviceProfileView{}})
		}
		devices[position].Profiles = append(devices[position].Profiles, DeviceProfileView{ProfileID: profileID, ProfileName: profileName})
	}
	return devices, rows.Err()
}

// supportEvent starts an audit event for a support write about one member.
func supportEvent(actor MutationActor, organizationID uuid.UUID, accountID int, action, targetType, targetID string, locked LockedAuthority) AuditEvent {
	return AuditEvent{
		Actor: actor, Action: action, TargetType: targetType, TargetID: targetID, OrganizationID: organizationID,
		SubjectAccountID: accountID, BeforeRevision: locked.Target.SecurityRevision, AfterRevision: locked.Target.SecurityRevision,
		Outcome: "success",
	}
}

// supportAuthority locks and judges the member for a support write, auditing a
// refusal as "<refusedAction>". The caller has taken the rate-limit budget.
// Every write also needs an active membership and an account that no other
// organization shares.
func supportAuthority(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, actor MutationActor, accountID int, refusedAction, targetType, targetID string) (LockedAuthority, error) {
	locked, err := LockAuthority(ctx, tx, organizationID, actor, accountID)
	refusal := func(reason string, cause error) error {
		if targetID == "" {
			targetID = locked.Target.MembershipID.String()
		}
		event := supportEvent(actor, organizationID, accountID, refusedAction, targetType, targetID, locked)
		return RefuseAudited(ctx, tx, event, reason, cause)
	}
	if err != nil {
		var protected *TargetProtectedError
		if errors.As(err, &protected) {
			return locked, refusal(string(protected.Reason), err)
		}
		return locked, err
	}
	if err := locked.RequireUnshared(); err != nil {
		return locked, refusal("account_shared", err)
	}
	if err := locked.RequireActive(); err != nil {
		return locked, refusal("membership_not_active", err)
	}
	return locked, nil
}

// RevokeSessions signs the member out everywhere: it raises their security
// revision (which stales membership-bound tokens and, through the revocation
// trigger, older account-login sessions) and revokes every session of the
// account, impersonation sessions included. The caller owes an account-wide
// compatibility-session invalidation after commit.
func (s *Service) RevokeSessions(ctx context.Context, organizationID uuid.UUID, actor MutationActor, accountID int, expectedRevision int64) (RevokedSessions, error) {
	return RunSupportTx(ctx, s.pool.Begin, func(tx pgx.Tx) (RevokedSessions, error) {
		var zero RevokedSessions
		if err := LockBudget(ctx, tx, BudgetMutate, organizationID); err != nil {
			return zero, err
		}
		if err := CheckBudget(ctx, tx, BudgetMutate, organizationID, actor.AccountID, strconv.Itoa(accountID)); err != nil {
			return zero, err
		}
		locked, err := supportAuthority(ctx, tx, organizationID, actor, accountID, "people.sessions_revoke_refused", "membership", "")
		if err != nil {
			return zero, err
		}
		if locked.Target.SecurityRevision != expectedRevision {
			return zero, ErrAuthorizationStateChanged
		}
		if _, err := tx.Exec(ctx, `UPDATE organization_memberships SET security_revision = security_revision + 1, updated_at = now() WHERE id = $1`, locked.Target.MembershipID); err != nil {
			return zero, fmt.Errorf("raise security revision: %w", err)
		}
		tag, err := tx.Exec(ctx, `UPDATE auth_sessions SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`, accountID)
		if err != nil {
			return zero, fmt.Errorf("revoke account sessions: %w", err)
		}
		event := supportEvent(actor, organizationID, accountID, "people.sessions_revoked", "membership", locked.Target.MembershipID.String(), locked)
		event.AfterRevision = locked.Target.SecurityRevision + 1
		event.After = map[string]any{"sessions_revoked": tag.RowsAffected()}
		if err := RecordAudit(ctx, tx, event); err != nil {
			return zero, err
		}
		person, err := getPersonInTransaction(ctx, tx, organizationID, accountID)
		if err != nil {
			return zero, err
		}
		return RevokedSessions{Person: person, Sessions: int(tag.RowsAffected())}, nil
	})
}

// RevokedSession reports one session sign-out.
type RevokedSession struct {
	// Changed is false when the session had already ended.
	Changed   bool
	ProfileID *string
}

// RevokeSession ends one session of the member. It does not raise the
// security revision. An id that is not the member's, or that is an
// impersonation session, is ErrNotFound; a session that already ended succeeds.
func (s *Service) RevokeSession(ctx context.Context, organizationID uuid.UUID, actor MutationActor, accountID int, sessionID string) (RevokedSession, error) {
	return RunSupportTx(ctx, s.pool.Begin, func(tx pgx.Tx) (RevokedSession, error) {
		var zero RevokedSession
		if err := LockBudget(ctx, tx, BudgetMutate, organizationID); err != nil {
			return zero, err
		}
		if err := CheckBudget(ctx, tx, BudgetMutate, organizationID, actor.AccountID, strconv.Itoa(accountID)); err != nil {
			return zero, err
		}
		locked, err := supportAuthority(ctx, tx, organizationID, actor, accountID, "people.session_revoke_refused", "session", sessionID)
		if err != nil {
			return zero, err
		}
		var deviceID *string
		var profileID *string
		var revokedAt *time.Time
		var impersonation bool
		var purpose string
		err = tx.QueryRow(ctx, `SELECT device_id, profile_id, revoked_at, impersonator_user_id IS NOT NULL, purpose FROM auth_sessions WHERE id = $1 AND user_id = $2 FOR UPDATE`, sessionID, accountID).
			Scan(&deviceID, &profileID, &revokedAt, &impersonation, &purpose)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && (impersonation || purpose != "account")) {
			return zero, ErrNotFound
		}
		if err != nil {
			return zero, err
		}
		if revokedAt != nil {
			return RevokedSession{Changed: false, ProfileID: profileID}, nil
		}
		if _, err := tx.Exec(ctx, `UPDATE auth_sessions SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, sessionID); err != nil {
			return zero, fmt.Errorf("revoke session: %w", err)
		}
		event := supportEvent(actor, organizationID, accountID, "people.session_revoked", "session", sessionID, locked)
		event.After = map[string]any{"device_id": derefString(deviceID)}
		if err := RecordAudit(ctx, tx, event); err != nil {
			return zero, err
		}
		return RevokedSession{Changed: true, ProfileID: profileID}, nil
	})
}

// SignedOutDevice reports a device sign-out.
type SignedOutDevice struct {
	Sessions   int
	ProfileIDs []string
}

// SignOutDevice ends the member's sessions opened from one device and forgets
// the device registration and per-device settings of their profiles in this
// organization. It does not raise the security revision and does not stop a
// stream that is playing. A device the member does not have is ErrNotFound,
// whether it belongs to someone else or to nobody.
func (s *Service) SignOutDevice(ctx context.Context, organizationID uuid.UUID, actor MutationActor, accountID int, deviceID string) (SignedOutDevice, error) {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return SignedOutDevice{}, ErrNotFound
	}
	return RunSupportTx(ctx, s.pool.Begin, func(tx pgx.Tx) (SignedOutDevice, error) {
		var zero SignedOutDevice
		if err := LockBudget(ctx, tx, BudgetMutate, organizationID); err != nil {
			return zero, err
		}
		if err := CheckBudget(ctx, tx, BudgetMutate, organizationID, actor.AccountID, strconv.Itoa(accountID)); err != nil {
			return zero, err
		}
		locked, err := supportAuthority(ctx, tx, organizationID, actor, accountID, "people.device_sign_out_refused", "device", deviceID)
		if err != nil {
			return zero, err
		}
		// The organization's profiles that used the device, from the registry,
		// per-device settings and the sessions opened from it.
		rows, err := tx.Query(ctx, `
			SELECT DISTINCT p.id FROM user_profiles p
			WHERE p.user_id = $1 AND p.organization_id = $2 AND (
			      EXISTS (SELECT 1 FROM user_devices d WHERE d.user_id = p.user_id AND d.profile_id = p.id AND d.device_id = $3)
			   OR EXISTS (SELECT 1 FROM user_device_settings d WHERE d.user_id = p.user_id AND d.profile_id = p.id AND d.device_id = $3)
			   OR EXISTS (SELECT 1 FROM auth_sessions a WHERE a.user_id = p.user_id AND a.profile_id = p.id AND a.device_id = $3))`,
			accountID, organizationID, deviceID)
		if err != nil {
			return zero, fmt.Errorf("find device profiles: %w", err)
		}
		profileIDs := []string{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return zero, err
			}
			profileIDs = append(profileIDs, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return zero, err
		}
		var accountLogin bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM auth_sessions WHERE user_id = $1 AND device_id = $2 AND profile_id IS NULL AND impersonator_user_id IS NULL AND purpose = 'account')`, accountID, deviceID).Scan(&accountLogin); err != nil {
			return zero, err
		}
		if len(profileIDs) == 0 && !accountLogin {
			// Nothing of this person's uses the device. A device that belongs to
			// someone else, or to nobody, is one answer: not found.
			return zero, ErrNotFound
		}
		tag, err := tx.Exec(ctx, `
			UPDATE auth_sessions SET revoked_at = now()
			WHERE user_id = $1 AND device_id = $2 AND revoked_at IS NULL AND impersonator_user_id IS NULL AND purpose = 'account'
			  AND (profile_id IS NULL OR profile_id = ANY($3))`, accountID, deviceID, profileIDs)
		if err != nil {
			return zero, fmt.Errorf("revoke device sessions: %w", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM user_setting_values WHERE user_id = $1 AND scope = 'profile_device' AND device_id = $2 AND profile_id = ANY($3)`, accountID, deviceID, profileIDs); err != nil {
			return zero, fmt.Errorf("forget device setting values: %w", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM user_device_settings WHERE user_id = $1 AND device_id = $2 AND profile_id = ANY($3)`, accountID, deviceID, profileIDs); err != nil {
			return zero, fmt.Errorf("forget device settings: %w", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM user_devices WHERE user_id = $1 AND device_id = $2 AND profile_id = ANY($3)`, accountID, deviceID, profileIDs); err != nil {
			return zero, fmt.Errorf("forget device: %w", err)
		}
		event := supportEvent(actor, organizationID, accountID, "people.device_signed_out", "device", deviceID, locked)
		event.After = map[string]any{"sessions_revoked": tag.RowsAffected()}
		if err := RecordAudit(ctx, tx, event); err != nil {
			return zero, err
		}
		return SignedOutDevice{Sessions: int(tag.RowsAffected()), ProfileIDs: profileIDs}, nil
	})
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// TakeBudget serializes and checks a support rate-limit budget in tx. target
// is the account id as text, or the stream session id for BudgetStream.
func TakeBudget(ctx context.Context, tx pgx.Tx, budget Budget, organizationID uuid.UUID, actorID int, target string) error {
	if err := LockBudget(ctx, tx, budget, organizationID); err != nil {
		return err
	}
	return CheckBudget(ctx, tx, budget, organizationID, actorID, target)
}

// LockSupportTarget locks and judges a member for a support write: the shared
// authority rules, the shared-account guard and the active-membership rule. A
// refusal is audited as refusedAction and marked so RunSupportTx commits it.
func LockSupportTarget(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, actor MutationActor, accountID int, refusedAction, targetType, targetID string) (LockedAuthority, error) {
	return supportAuthority(ctx, tx, organizationID, actor, accountID, refusedAction, targetType, targetID)
}

// SupportEvent starts a success audit event about a member.
func SupportEvent(actor MutationActor, organizationID uuid.UUID, accountID int, action, targetType, targetID string, locked LockedAuthority) AuditEvent {
	return supportEvent(actor, organizationID, accountID, action, targetType, targetID, locked)
}

// PersonInTransaction reads the person summary inside tx.
func PersonInTransaction(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, accountID int) (PersonSummary, error) {
	return getPersonInTransaction(ctx, tx, organizationID, accountID)
}
