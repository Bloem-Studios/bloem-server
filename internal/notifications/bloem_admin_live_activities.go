package notifications

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/secret"
)

// Live Activities for the Bloem admin apps (docs/architecture/bloem-admin-push.md).
//
// An app registers each scan or backup Live Activity it runs, with the activity's
// own push token on iOS and none on Android, and may give the server a push-to-start
// token. The server follows the job the activity shows and keeps it current. All of
// it is off until the operator turns config.AdminLiveActivitiesSettingKey on, which
// they do once the relay has the live_activity mode.

const (
	// AdminLiveActivitiesCapabilityToken tells an app this server keeps its scan and
	// backup Live Activities current. It is advertised with AdminPushCapabilityToken,
	// and only while the operator's switch is on.
	AdminLiveActivitiesCapabilityToken = "admin_live_activities_v1"

	// LiveKindScan and LiveKindBackup are the jobs an admin Live Activity shows.
	LiveKindScan   = "scan"
	LiveKindBackup = "backup"
	// LivePlatformIOS and LivePlatformAndroid are where the activity runs.
	LivePlatformIOS     = "ios"
	LivePlatformAndroid = "android"

	// adminLiveLifetime is how long iOS keeps an activity; the server sends nothing after it.
	adminLiveLifetime = 8 * time.Hour
	// adminLiveMaxPerDevice is how many activities a device keeps; a sixth replaces the oldest.
	adminLiveMaxPerDevice = 5
)

var (
	// adminLiveKinds are the kinds in the order the server stores them.
	adminLiveKinds = []string{LiveKindScan, LiveKindBackup}
	// liveTokenPattern is the relay's rule for an APNs token: 64 to 200 hex characters.
	liveTokenPattern = regexp.MustCompile(`^[0-9a-f]{64,200}$`)
	// libraryIDPattern is a library id: digits only, so no library name can pass for one.
	libraryIDPattern = regexp.MustCompile(`^[0-9]{1,18}$`)
)

// LiveActivityRegistration is one activity an app runs, after transport decoding.
type LiveActivityRegistration struct {
	ActivityID string
	Kind       string
	// SubjectID is the library id of a scan; empty for a backup.
	SubjectID string
	Platform  string
	// PushToken and Environment are the activity's APNs push token and its environment
	// (iOS only).
	PushToken   string
	Environment string
}

// LiveActivitiesEnabled reports whether the operator has Live Activities on. It is off
// by default, and stays off until the relay has the live_activity mode.
func (s *AdminPushService) LiveActivitiesEnabled(ctx context.Context) bool {
	return s.Available() && s.settings != nil &&
		s.settings.boolSettingFailClosed(ctx, config.AdminLiveActivitiesSettingKey, false)
}

func adminLiveTokenAAD(activityID string) string {
	return secret.RowAAD("bloem_admin_live_activities", "token", activityID)
}

func adminLiveStartTokenAAD(deviceID string) string {
	return secret.RowAAD("bloem_admin_push_devices", "live_start_token", deviceID)
}

func canonicalActivityID(id string) (string, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(id))
	if err != nil || len(strings.TrimSpace(id)) != 36 {
		return "", fmt.Errorf("%w: activity_id must be a UUID", ErrAdminPushInvalid)
	}
	return parsed.String(), nil
}

func validLiveToken(token string) (string, error) {
	token = strings.ToLower(strings.TrimSpace(token))
	if !liveTokenPattern.MatchString(token) {
		return "", fmt.Errorf("%w: the token must be 64 to 200 hex characters", ErrAdminPushInvalid)
	}
	return token, nil
}

// validLiveRegistration checks a registration against the device it is for.
func validLiveRegistration(in LiveActivityRegistration, device *AdminPushDevice) (LiveActivityRegistration, error) {
	out := LiveActivityRegistration{Kind: strings.TrimSpace(in.Kind), SubjectID: strings.TrimSpace(in.SubjectID), Platform: strings.TrimSpace(in.Platform)}
	id, err := canonicalActivityID(in.ActivityID)
	if err != nil {
		return out, err
	}
	out.ActivityID = id
	switch out.Kind {
	case LiveKindScan:
		if !libraryIDPattern.MatchString(out.SubjectID) {
			return out, fmt.Errorf("%w: a scan's subject_id must be its library id", ErrAdminPushInvalid)
		}
	case LiveKindBackup:
		if out.SubjectID != "" {
			return out, fmt.Errorf("%w: a backup has no subject_id", ErrAdminPushInvalid)
		}
	default:
		return out, fmt.Errorf("%w: kind must be scan or backup", ErrAdminPushInvalid)
	}
	switch out.Platform {
	case LivePlatformIOS:
		if device.Platform != PushPlatformApple {
			return out, fmt.Errorf("%w: an ios activity needs an Apple device", ErrAdminPushInvalid)
		}
		if out.PushToken, err = validLiveToken(in.PushToken); err != nil {
			return out, err
		}
		out.Environment = strings.ToLower(strings.TrimSpace(in.Environment))
		if out.Environment != APNsEnvironmentProd && out.Environment != APNsEnvironmentSandbox {
			return out, fmt.Errorf("%w: environment must be production or sandbox", ErrAdminPushInvalid)
		}
	case LivePlatformAndroid:
		if device.Platform != PushPlatformAndroid {
			return out, fmt.Errorf("%w: an android activity needs an Android device", ErrAdminPushInvalid)
		}
		if strings.TrimSpace(in.PushToken) != "" || strings.TrimSpace(in.Environment) != "" {
			return out, fmt.Errorf("%w: an android activity has no push_token or environment", ErrAdminPushInvalid)
		}
	default:
		return out, fmt.Errorf("%w: platform must be ios or android", ErrAdminPushInvalid)
	}
	return out, nil
}

// RegisterLiveActivity records an activity on one of the caller's devices. Registering
// the same activity id again replaces its token (the token rotated) and keeps its
// lifetime. A device keeps at most five; a sixth replaces the oldest.
func (s *AdminPushService) RegisterLiveActivity(ctx context.Context, userID int, deviceID string, in LiveActivityRegistration) error {
	if !s.LiveActivitiesEnabled(ctx) {
		return ErrAdminPushUnavailable
	}
	return s.store.withOwnedDevice(ctx, userID, deviceID, func(tx pgx.Tx, device *AdminPushDevice) error {
		reg, err := validLiveRegistration(in, device)
		if err != nil {
			return err
		}
		var ciphertext *string
		if reg.Platform == LivePlatformIOS {
			sealed, err := s.cipher.Encrypt(reg.PushToken, adminLiveTokenAAD(reg.ActivityID))
			if err != nil {
				return fmt.Errorf("encrypt activity token: %w", err)
			}
			ciphertext = &sealed
		}
		now := s.now()
		tag, err := tx.Exec(ctx, `INSERT INTO bloem_admin_live_activities AS a
				(activity_id, device_id, kind, subject_id, platform, token_ciphertext, environment, created_at, expires_at)
			VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, NULLIF($7, ''), $8, $9)
			ON CONFLICT (activity_id) DO UPDATE SET kind = EXCLUDED.kind, subject_id = EXCLUDED.subject_id,
				platform = EXCLUDED.platform, token_ciphertext = EXCLUDED.token_ciphertext, environment = EXCLUDED.environment,
				last_state_hash = NULL, last_phase = NULL
			WHERE a.device_id = EXCLUDED.device_id AND a.ended_at IS NULL`,
			reg.ActivityID, device.ID, reg.Kind, reg.SubjectID, reg.Platform, ciphertext, reg.Environment, now, now.Add(adminLiveLifetime))
		if err != nil {
			return fmt.Errorf("store activity: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("%w: activity_id is already in use", ErrAdminPushInvalid)
		}
		// Keep the newest five; the device row lock makes concurrent registrations queue.
		_, err = tx.Exec(ctx, `DELETE FROM bloem_admin_live_activities WHERE device_id = $1 AND activity_id IN (
			SELECT activity_id FROM bloem_admin_live_activities WHERE device_id = $1
			ORDER BY created_at DESC, activity_id DESC OFFSET $2)`, device.ID, adminLiveMaxPerDevice)
		return err
	})
}

// EndLiveActivity deletes an activity of one of the caller's devices, with its token.
// Ending one the server does not have is not an error.
func (s *AdminPushService) EndLiveActivity(ctx context.Context, userID int, deviceID, activityID string) error {
	if !s.Available() {
		return ErrAdminPushUnavailable
	}
	id, err := canonicalActivityID(activityID)
	if err != nil {
		return err
	}
	return s.store.withOwnedDevice(ctx, userID, deviceID, func(tx pgx.Tx, device *AdminPushDevice) error {
		_, err := tx.Exec(ctx, `DELETE FROM bloem_admin_live_activities WHERE activity_id = $1 AND device_id = $2`, id, device.ID)
		return err
	})
}

// SetLiveStart stores, or with a nil token clears, the push-to-start token of one of the
// caller's Apple devices and the kinds the person allowed the server to start. Clearing
// works with the switch off, so an app can always withdraw its consent.
func (s *AdminPushService) SetLiveStart(ctx context.Context, userID int, deviceID string, token *string, kinds []string) error {
	if !s.Available() {
		return ErrAdminPushUnavailable
	}
	if token != nil && !s.LiveActivitiesEnabled(ctx) {
		return ErrAdminPushUnavailable
	}
	clean := []string{}
	for _, kind := range adminLiveKinds {
		if slices.Contains(kinds, kind) {
			clean = append(clean, kind)
		}
	}
	for _, kind := range kinds {
		if !slices.Contains(adminLiveKinds, kind) {
			return fmt.Errorf("%w: kinds may name only scan and backup", ErrAdminPushInvalid)
		}
	}
	if token == nil && len(clean) > 0 {
		return fmt.Errorf("%w: kinds need a token", ErrAdminPushInvalid)
	}
	return s.store.withOwnedDevice(ctx, userID, deviceID, func(tx pgx.Tx, device *AdminPushDevice) error {
		var ciphertext *string
		if token != nil {
			if device.Platform != PushPlatformApple {
				return fmt.Errorf("%w: only an Apple device has a push-to-start token", ErrAdminPushInvalid)
			}
			plain, err := validLiveToken(*token)
			if err != nil {
				return err
			}
			sealed, err := s.cipher.Encrypt(plain, adminLiveStartTokenAAD(device.ID))
			if err != nil {
				return fmt.Errorf("encrypt start token: %w", err)
			}
			ciphertext = &sealed
		}
		_, err := tx.Exec(ctx, `UPDATE bloem_admin_push_devices SET live_start_token_ciphertext = $2, live_start_kinds = $3, updated_at = now()
			WHERE id = $1`, device.ID, ciphertext, clean)
		return err
	})
}

// withOwnedDevice runs fn in a transaction holding the lock on one of the account's
// devices, found by the device id the app knows. Another account's device is not found.
func (s *adminPushStore) withOwnedDevice(ctx context.Context, userID int, serverDeviceID string, fn func(pgx.Tx, *AdminPushDevice) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	device, err := scanAdminDevice(tx.QueryRow(ctx,
		`SELECT `+adminDeviceColumns+` FROM bloem_admin_push_devices d WHERE d.server_device_id = $1 AND d.user_id = $2 FOR UPDATE`,
		serverDeviceID, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAdminPushNotFound
	}
	if err != nil {
		return err
	}
	if err := fn(tx, device); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
