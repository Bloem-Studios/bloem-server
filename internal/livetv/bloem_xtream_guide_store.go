package livetv

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Stable programme IDs and in-place updates retain DVR links across guide
// refreshes. Build/validate the complete bounded feed before opening this
// transaction; a malformed or partial download must not erase a working guide.
func (s *PgStore) replaceXtreamPrograms(ctx context.Context, source *GuideSource, version string, channels []Channel, programs []Program, from, to time.Time) error {
	if version == "" || len(programs) == 0 {
		return errors.New("Xtream guide publication requires a claimed source and matching programmes")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback is best effort on returned error
	config, err := json.Marshal(source.Config)
	if err != nil {
		return err
	}
	var id string
	// A source retargeted/disabled/deleted during the fetch cannot publish its
	// old programme payload under the new configuration.
	if err := tx.QueryRow(ctx, `SELECT id FROM livetv_guide_sources WHERE id=$1 AND type='xtream' AND enabled AND config_json=$2::jsonb AND xmin::text=$3 FOR UPDATE`, source.ID, config, version).Scan(&id); err != nil {
		return errors.New("Xtream guide source changed during synchronization")
	}
	if err := lockXtreamGuideChannels(ctx, tx, source, channels, programs); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `CREATE TEMP TABLE bloem_xtream_program_stage (LIKE livetv_programs INCLUDING DEFAULTS) ON COMMIT DROP`); err != nil {
		return err
	}
	columns := []string{"id", "channel_id", "source_id", "series_id", "external_id", "start_at", "stop_at", "title", "subtitle", "description", "season", "episode", "genres", "image_url", "is_new", "is_live"}
	_, err = tx.CopyFrom(ctx, pgx.Identifier{"bloem_xtream_program_stage"}, columns, pgx.CopyFromSlice(len(programs), func(i int) ([]any, error) {
		p := programs[i]
		return []any{p.ID, p.ChannelID, source.ID, p.SeriesID, p.ExternalID, p.Start, p.Stop, p.Title, p.Subtitle, p.Description, p.Season, p.Episode, nonNilStringSlice(p.Genres), "", p.IsNew, p.IsLive}, nil
	}))
	if err != nil {
		return err
	}
	// Avoid one database round trip per programme. Only rows from this exact
	// source may conflict; the source-scoped deterministic IDs never adopt an
	// unrelated programme (including its recording associations).
	tag, err := tx.Exec(ctx, `
		INSERT INTO livetv_programs (id,channel_id,source_id,series_id,external_id,start_at,stop_at,title,subtitle,description,season,episode,genres,image_url,is_new,is_live)
		SELECT id,channel_id,source_id,series_id,external_id,start_at,stop_at,title,subtitle,description,season,episode,genres,image_url,is_new,is_live
		FROM bloem_xtream_program_stage n
		WHERE EXISTS(SELECT 1 FROM livetv_channels c WHERE c.id=n.channel_id AND c.tuner_id=$1 AND c.enabled)
		ON CONFLICT(id) DO UPDATE SET
			stop_at=EXCLUDED.stop_at,title=EXCLUDED.title,subtitle=EXCLUDED.subtitle,
			description=EXCLUDED.description,season=EXCLUDED.season,episode=EXCLUDED.episode,
			genres=EXCLUDED.genres,is_new=EXCLUDED.is_new,is_live=EXCLUDED.is_live,updated_at=now()
		WHERE livetv_programs.source_id=EXCLUDED.source_id AND livetv_programs.channel_id=EXCLUDED.channel_id AND livetv_programs.start_at=EXCLUDED.start_at`, source.Config["tuner_id"])
	if err != nil {
		return err
	}
	if tag.RowsAffected() != int64(len(programs)) {
		return errors.New("Xtream guide source or programme identity changed during synchronization")
	}
	// Retain historical programme rows linked to a recording. Unreferenced
	// history is bounded; removed future entries leave the captured recording
	// schedule intact through the existing ON DELETE SET NULL relationship.
	_, err = tx.Exec(ctx, `DELETE FROM livetv_programs p WHERE p.source_id=$1 AND (
		(p.stop_at<=$2 AND NOT EXISTS(SELECT 1 FROM livetv_recordings r WHERE r.program_id=p.id))
		OR (p.start_at<$3 AND p.stop_at>$2 AND NOT EXISTS(SELECT 1 FROM bloem_xtream_program_stage n WHERE n.id=p.id)))`, source.ID, from, to)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE livetv_guide_sources SET status='ready',last_error='',
		last_sync_at=clock_timestamp(),next_sync_at=clock_timestamp()+interval '6 hours',updated_at=clock_timestamp()
		WHERE id=$1`, source.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Channel mappings may change while a large feed is fetched. Recheck them
// under row locks, so neither a rescan nor a mapping edit can race publication.
func lockXtreamGuideChannels(ctx context.Context, tx pgx.Tx, source *GuideSource, channels []Channel, programs []Program) error {
	stations := make(map[string]string, len(channels))
	for _, channel := range channels {
		if !channel.Enabled {
			continue
		}
		station := channel.GuideStationID
		if station == "" {
			station = channel.Number
		}
		stations[channel.ID] = station
	}
	ids, expected := []string{}, []string{}
	seen := map[string]bool{}
	for _, program := range programs {
		station, ok := stations[program.ChannelID]
		if !ok || station == "" || program.SourceID != source.ID {
			return errors.New("Xtream programme does not match the captured channel mapping")
		}
		if !seen[program.ChannelID] {
			seen[program.ChannelID] = true
			ids, expected = append(ids, program.ChannelID), append(expected, station)
		}
	}
	rows, err := tx.Query(ctx, `SELECT coalesce(nullif(c.guide_station_id,''),c.number)=e.station
		FROM unnest($2::text[],$3::text[]) AS e(id,station)
		JOIN livetv_channels c ON c.id=e.id
		WHERE c.tuner_id=$1 AND c.enabled ORDER BY c.id FOR SHARE OF c`, source.Config["tuner_id"], ids, expected)
	if err != nil {
		return err
	}
	matches, err := pgx.CollectRows(rows, pgx.RowTo[bool])
	if err != nil {
		return err
	}
	if len(matches) != len(ids) {
		return errors.New("Xtream channel availability changed during synchronization")
	}
	for _, match := range matches {
		if !match {
			return errors.New("Xtream channel mapping changed during synchronization")
		}
	}
	return nil
}
