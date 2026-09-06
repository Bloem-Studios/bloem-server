package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type syntheticMedia struct {
	folderID, fileID int
	contentID, path  string
}

func createMedia(ctx context.Context, pool *pgxpool.Pool, dir string) (syntheticMedia, error) {
	var zero syntheticMedia
	media := syntheticMedia{contentID: "synthetic-harness-" + uuid.NewString(), path: filepath.Join(dir, "synthetic.mp4")}
	generation, done := context.WithTimeout(ctx, time.Minute)
	defer done()
	cmd := exec.CommandContext(generation, "ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=24", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "30", "-c:v", "libx264", "-profile:v", "high", "-level:v", "4.1", "-pix_fmt", "yuv420p", "-c:a", "aac", "-ac", "2", "-movflags", "+faststart", media.path)
	if output, err := cmd.CombinedOutput(); err != nil {
		return zero, fmt.Errorf("generate synthetic media: %w: %s", err, output)
	}
	info, err := os.Stat(media.path)
	if err != nil {
		return zero, err
	}
	video, err := json.Marshal([]models.VideoTrack{{Codec: "h264", Profile: "high", Level: 41, Width: 320, Height: 180, FrameRate: "24/1", BitDepth: 8, VideoRange: "SDR", VideoRangeType: "SDR"}})
	if err != nil {
		return zero, err
	}
	audio, err := json.Marshal([]models.AudioTrack{{Codec: "aac", Channels: 2, Layout: "stereo", Default: true}})
	if err != nil {
		return zero, err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return zero, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	if err = tx.QueryRow(ctx, `INSERT INTO media_folders(type,name) VALUES('movies','Synthetic playback only') RETURNING id`).Scan(&media.folderID); err != nil {
		return zero, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO media_items(content_id,type,title) VALUES($1,'movie','Synthetic playback only')`, media.contentID); err != nil {
		return zero, err
	}
	if err = tx.QueryRow(ctx, `INSERT INTO media_files(content_id,media_folder_id,file_path,file_size,container,codec_video,codec_audio,resolution,bitrate,audio_channels,duration,video_tracks,audio_tracks,probe_source,probe_updated_at) VALUES($1,$2,$3,$4,'mp4','h264','aac','1080p',8000,2,30,$5,$6,'ffprobe',now()) RETURNING id`, media.contentID, media.folderID, media.path, info.Size(), video, audio).Scan(&media.fileID); err != nil {
		return zero, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO media_item_libraries(content_id,media_folder_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, media.contentID, media.folderID); err != nil {
		return zero, err
	}
	if err = tx.Commit(ctx); err != nil {
		return zero, err
	}
	return media, nil
}
func (m syntheticMedia) remove(ctx context.Context, pool *pgxpool.Pool) error {
	_, folderErr := pool.Exec(ctx, `DELETE FROM media_folders WHERE id=$1`, m.folderID)
	_, itemErr := pool.Exec(ctx, `DELETE FROM media_items WHERE content_id=$1`, m.contentID)
	return errors.Join(folderErr, itemErr)
}
