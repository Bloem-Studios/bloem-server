package livetv

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// A bounded refresh owns the source row's MVCC version. Configuration edits,
// removal and a later refresh invalidate both publication and status updates.
// No database connection is held while downloading or parsing the guide.
func (s *Service) runXtreamGuideSync(ctx context.Context, source *GuideSource) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	store, err := s.xtreamStore()
	if err != nil {
		return err
	}
	version, err := store.claimXtreamGuideSync(ctx, source)
	if err != nil {
		return err
	}
	if err = s.syncXtreamGuide(ctx, source, version); err != nil {
		cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer done()
		// Only this attempt's unchanged row can be marked failed. Publication
		// commits the ready status and guide rows together instead.
		_, _ = store.db.Exec(cleanup, `UPDATE livetv_guide_sources
			SET status='error',last_error=$3,next_sync_at=clock_timestamp()+interval '6 hours',updated_at=clock_timestamp()
			WHERE id=$1 AND xmin::text=$2`, source.ID, version, err.Error())
	}
	return err
}

func (s *PgStore) claimXtreamGuideSync(ctx context.Context, source *GuideSource) (string, error) {
	config, err := json.Marshal(source.Config)
	if err != nil {
		return "", errors.New("invalid Xtream guide configuration")
	}
	var version string
	err = s.db.QueryRow(ctx, `UPDATE livetv_guide_sources
		SET status='syncing',last_error='',updated_at=clock_timestamp(),next_sync_at=clock_timestamp()+interval '3 minutes'
		WHERE id=$1 AND type='xtream' AND enabled AND config_json=$2::jsonb
		AND (status<>'syncing' OR updated_at < clock_timestamp()-interval '3 minutes')
		RETURNING xmin::text`, source.ID, config).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errors.New("Xtream guide source changed or a synchronization is already running")
	}
	return version, err
}
