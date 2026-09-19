package livetv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Admission and the write share one transaction/connection. The service's early
// quota check is useful feedback, but two API replicas must not both consume
// the final guide slot. No upstream calls run under this lock.
func (s *PgStore) writeBloemGuideSource(ctx context.Context, source *GuideSource, create bool) (*GuideSource, error) {
	if source.Type == GuideSourceXtream {
		source.Config = map[string]string{"tuner_id": source.Config["tuner_id"]}
	}
	cfg, err := json.Marshal(source.Config)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback is best effort on returned error
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(1481921101,2)`); err != nil {
		return nil, err
	}
	if source.Enabled {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM livetv_guide_sources WHERE enabled AND id<>$1`, source.ID).Scan(&count); err != nil {
			return nil, err
		}
		if count >= MaxGuideSources {
			return nil, fmt.Errorf("%w: at most %d enabled guide sources are allowed", ErrLimitExceeded, MaxGuideSources)
		}
	}
	if source.Type == GuideSourceXtream {
		tunerID := source.Config["tuner_id"]
		var parent string
		// Provider deletion takes the conflicting parent lock before deleting
		// linked guides. Validation then deletion cannot leave an orphan.
		if err := tx.QueryRow(ctx, `SELECT id FROM livetv_tuners WHERE id=$1 AND type='xtream' FOR KEY SHARE`, tunerID).Scan(&parent); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrNotFound
			}
			return nil, err
		}
		var duplicate bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM livetv_guide_sources WHERE type='xtream' AND config_json->>'tuner_id'=$1 AND id<>$2)`, tunerID, source.ID).Scan(&duplicate); err != nil {
			return nil, err
		}
		if duplicate {
			return nil, fmt.Errorf("%w: this Xtream provider already has a guide source", ErrInvalidArgument)
		}
	}
	var row pgx.Row
	if create {
		row = tx.QueryRow(ctx, `
			INSERT INTO livetv_guide_sources(id,type,priority,enabled,display_name,config_json,status,last_error,last_sync_at,next_sync_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			RETURNING id,type,priority,enabled,display_name,config_json,status,last_error,last_sync_at,next_sync_at`,
			source.ID, source.Type, source.Priority, source.Enabled, source.DisplayName, cfg, source.Status, source.LastError, source.LastSyncAt, source.NextSyncAt)
	} else {
		row = tx.QueryRow(ctx, `
			UPDATE livetv_guide_sources SET type=$2,priority=$3,enabled=$4,display_name=$5,config_json=$6,status=$7,last_error=$8,next_sync_at=$9,updated_at=now()
			WHERE id=$1
			RETURNING id,type,priority,enabled,display_name,config_json,status,last_error,last_sync_at,next_sync_at`,
			source.ID, source.Type, source.Priority, source.Enabled, source.DisplayName, cfg, source.Status, source.LastError, source.NextSyncAt)
	}
	out, err := scanGuideSource(row)
	if errors.Is(err, pgx.ErrNoRows) && !create {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &out, nil
}
