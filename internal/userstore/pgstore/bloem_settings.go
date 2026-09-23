package pgstore

import (
	"context"
	"fmt"

	"github.com/Silo-Server/silo-server/internal/settingscontract"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/jackc/pgx/v5"
)

// DeleteDeviceInTransaction removes one device and all of its legacy and
// canonical settings for the selected profiles in a caller-owned transaction.
func (s *PostgresUserStore) DeleteDeviceInTransaction(ctx context.Context, tx pgx.Tx, profileIDs []string, deviceID string) ([]userstore.SettingIdentity, error) {
	rows, err := tx.Query(ctx, `
		SELECT profile_id,key FROM user_setting_values
		WHERE user_id=$1 AND scope='profile_device' AND profile_id=ANY($2::text[]) AND device_id=$3
		ORDER BY profile_id,key`, s.userID, profileIDs, deviceID)
	if err != nil {
		return nil, fmt.Errorf("listing device setting changes for %q: %w", deviceID, err)
	}
	var changed []userstore.SettingIdentity
	for rows.Next() {
		var identity userstore.SettingIdentity
		identity.Scope = settingscontract.ScopeProfileDevice
		identity.DeviceID = deviceID
		if err := rows.Scan(&identity.ProfileID, &identity.Key); err != nil {
			rows.Close()
			return nil, err
		}
		changed = append(changed, identity)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for _, query := range []string{
		`DELETE FROM user_setting_values WHERE user_id=$1 AND profile_id=ANY($2::text[]) AND scope='profile_device' AND device_id=$3`,
		`DELETE FROM user_device_settings WHERE user_id=$1 AND profile_id=ANY($2::text[]) AND device_id=$3`,
		`DELETE FROM user_devices WHERE user_id=$1 AND profile_id=ANY($2::text[]) AND device_id=$3`,
	} {
		if _, err := tx.Exec(ctx, query, s.userID, profileIDs, deviceID); err != nil {
			return nil, fmt.Errorf("deleting device %q: %w", deviceID, err)
		}
	}
	return changed, nil
}
