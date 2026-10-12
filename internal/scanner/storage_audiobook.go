package scanner

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"google.golang.org/protobuf/proto"
)

type StorageAudioTrackStatus string

const (
	StorageAudioTrackOK          StorageAudioTrackStatus = "ok"
	StorageAudioTrackInvalid     StorageAudioTrackStatus = "invalid"
	StorageAudioTrackUnsupported StorageAudioTrackStatus = "unsupported"
)

type StorageAudioTrack struct {
	Entry  *storagev1.Entry
	Probe  *NativeAudioProbe
	Status StorageAudioTrackStatus
}

type StorageAudioPage struct {
	Folder                *models.MediaFolder
	Location              storagesource.Location
	Lease                 storagesource.Lease
	ConfigurationRevision int64
	DirectoryID           string
	Complete              bool
	Tracks                []StorageAudioTrack
}

type StorageAudioResult struct {
	StoragePublishResult
	BooksPublished, BooksRejected int
}

func (s *Scanner) NativeAudioScanConfigured() bool {
	return s != nil && s.fileRepo != nil && s.itemRepo != nil && s.libraryRepo != nil && strings.TrimSpace(s.ffprobePath) != ""
}

func (s *Scanner) ProbeStorageAudio(ctx context.Context, entry *storagev1.Entry, file mediasource.File, cfg NativeProbeConfig) (StorageAudioTrack, error) {
	if s == nil || entry.GetKind() != storagev1.EntryKind_ENTRY_KIND_FILE {
		return StorageAudioTrack{}, ErrNativeProbeInvalid
	}
	track := StorageAudioTrack{Entry: proto.CloneOf(entry), Status: StorageAudioTrackUnsupported}
	demuxer, ok := NativeAudioDemuxer(entry.GetName())
	if !ok {
		return track, nil
	}
	cfg.Demuxer = demuxer
	probe, err := ProbeNativeAudio(ctx, s.ffprobePath, file, cfg)
	if errors.Is(err, ErrNativeProbeInvalid) {
		track.Status = StorageAudioTrackInvalid
		return track, nil
	}
	if err != nil {
		return StorageAudioTrack{}, err
	}
	track.Probe = probe
	track.Status = StorageAudioTrackOK
	return track, nil
}

func orderNativeAudioTracks(tracks []StorageAudioTrack) ([]StorageAudioTrack, error) {
	ordered := slices.Clone(tracks)
	complete := true
	seen := map[[2]uint32]bool{}
	duplicate := false
	for _, track := range ordered {
		pos := [2]uint32{track.Entry.GetAudioFile().GetDiscNumber(), track.Entry.GetAudioFile().GetTrackNumber()}
		if pos[0] == 0 || pos[1] == 0 {
			complete = false
		}
		if seen[pos] {
			duplicate = true
		}
		seen[pos] = true
	}
	if complete && duplicate {
		return nil, ErrNativeAudioTimeline
	}
	slices.SortFunc(ordered, func(a, b StorageAudioTrack) int {
		if complete {
			ap, bp := a.Entry.GetAudioFile(), b.Entry.GetAudioFile()
			if ap.GetDiscNumber() != bp.GetDiscNumber() {
				if ap.GetDiscNumber() < bp.GetDiscNumber() {
					return -1
				}
				return 1
			}
			if ap.GetTrackNumber() < bp.GetTrackNumber() {
				return -1
			}
			if ap.GetTrackNumber() > bp.GetTrackNumber() {
				return 1
			}
		}
		if naturalPathLess(a.Entry.GetLogicalPath(), b.Entry.GetLogicalPath()) {
			return -1
		}
		if naturalPathLess(b.Entry.GetLogicalPath(), a.Entry.GetLogicalPath()) {
			return 1
		}
		return strings.Compare(a.Entry.GetId(), b.Entry.GetId())
	})
	return ordered, nil
}

func nativeAudioTrackChapters(track StorageAudioTrack, part, total int) []ChapterInfo {
	if len(track.Probe.Chapters) > 0 {
		return slices.Clone(track.Probe.Chapters)
	}
	if total == 1 {
		return []ChapterInfo{}
	}
	title := strings.TrimSpace(track.Probe.Tags["title"])
	if title == "" {
		title = strings.TrimSuffix(track.Entry.GetName(), path.Ext(track.Entry.GetName()))
		if _, err := strconv.ParseUint(title, 10, 64); err == nil {
			title = ""
		}
	}
	if title == "" {
		title = fmt.Sprintf("Part %d", part)
	}
	return []ChapterInfo{{Index: 0, Title: title, StartSeconds: 0, EndSeconds: track.Probe.DurationSeconds}}
}

func validateNativeAudioBook(tracks []StorageAudioTrack) error {
	if len(tracks) == 0 || len(tracks) > 512 {
		return ErrNativeAudioTimeline
	}
	var normalized []NativeAudioTrack
	bookID := ""
	for i, track := range tracks {
		if track.Status != StorageAudioTrackOK || track.Probe == nil || track.Probe.Probe == nil || !durationIsPositiveFinite(track.Probe.DurationSeconds) || track.Probe.DurationSeconds < 1 {
			return ErrNativeAudioTimeline
		}
		for _, id := range []string{track.Entry.GetAudioFile().GetBookId(), track.Entry.GetAudiobook().GetBookId()} {
			if id != "" {
				if bookID != "" && id != bookID {
					return ErrNativeAudioTimeline
				}
				bookID = id
			}
		}
		t := NativeAudioTrack{EntryID: track.Entry.GetId(), Disc: 1, Track: uint32(i + 1), Duration: track.Probe.DurationSeconds}
		for _, ch := range nativeAudioTrackChapters(track, i+1, len(tracks)) {
			t.Chapters = append(t.Chapters, NativeAudioChapter{ID: strconv.Itoa(ch.Index), Title: ch.Title, Start: ch.StartSeconds, End: ch.EndSeconds})
		}
		normalized = append(normalized, t)
	}
	_, err := NormalizeNativeAudioTracks(normalized)
	return err
}
