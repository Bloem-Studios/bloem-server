package userdb

import (
	"context"
	"encoding/json"
)

func (s *SQLiteUserStore) ListWatchProgressDates(ctx context.Context, profileID string, ids []string) (map[string]string, error) {
	encoded, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT media_item_id,event_at FROM watch_progress WHERE profile_id=? AND media_item_id IN (SELECT value FROM json_each(?))`, profileID, string(encoded))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	dates := make(map[string]string)
	for rows.Next() {
		var id, date string
		if err := rows.Scan(&id, &date); err != nil {
			return nil, err
		}
		dates[id] = date
	}
	return dates, rows.Err()
}
