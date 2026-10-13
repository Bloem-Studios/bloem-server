package pgstore

import (
	"context"
	"time"
)

func (s *PostgresUserStore) ListWatchProgressDates(ctx context.Context, profileID string, ids []string) (map[string]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT media_item_id, event_at FROM user_watch_progress WHERE user_id=$1 AND profile_id=$2 AND media_item_id=ANY($3)`, s.userID, profileID, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	dates := make(map[string]string)
	for rows.Next() {
		var id string
		var date time.Time
		if err := rows.Scan(&id, &date); err != nil {
			return nil, err
		}
		dates[id] = date.UTC().Format(time.RFC3339Nano)
	}
	return dates, rows.Err()
}
