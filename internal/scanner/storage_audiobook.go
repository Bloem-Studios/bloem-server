package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"

	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

type StorageAudioTrackStatus string

const (
	StorageAudioTrackOK          StorageAudioTrackStatus = "ok"
	StorageAudioTrackInvalid     StorageAudioTrackStatus = "invalid"
	StorageAudioTrackUnsupported StorageAudioTrackStatus = "unsupported"
	// StorageAudioTrackUnavailable is a track whose bytes could not be read
	// this scan; see StorageAudioProbeFailure. It rejects its book for the run
	// only, and the next scan reads the track again.
	StorageAudioTrackUnavailable StorageAudioTrackStatus = "unavailable"
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

// movFormatNames is the format name ffprobe gives an MP4 family file.
const movFormatNames = "mov,mp4,m4a,3gp,3g2,mj2"

// nativeAudioFormatNames are the ffprobe format names of each native audio
// demuxer, so a track built from provider metadata names its container as a
// probe of the same file would.
var nativeAudioFormatNames = map[NativeProbeDemuxer]string{
	NativeProbeMOV:  movFormatNames,
	NativeProbeMP3:  "mp3",
	NativeProbeFLAC: "flac",
	NativeProbeOgg:  "ogg",
	NativeProbeWAV:  "wav",
}

// StorageAudioTrackFromEntry builds a track from the audio metadata its
// provider listed, as StorageEbookFromEntry does for books, so the track is
// published without being opened and probed. The provider must give the
// codec and a duration of at least a second; the container follows from the
// file's demuxer. The page's entry metadata was validated when it was listed.
func StorageAudioTrackFromEntry(entry *storagev1.Entry) (StorageAudioTrack, bool) {
	meta := entry.GetAudioFile()
	demuxer, ok := NativeAudioDemuxer(entry.GetName())
	codec := strings.ToLower(strings.TrimSpace(meta.GetCodec()))
	duration := meta.GetDurationSeconds()
	if !ok || meta == nil || entry.GetKind() != storagev1.EntryKind_ENTRY_KIND_FILE || codec == "" ||
		!durationIsPositiveFinite(duration) || duration < 1 || duration > maxReasonableAudioDurationSeconds {
		return StorageAudioTrack{}, false
	}
	kbps := int(meta.GetBitrate() / 1000)
	probe := &ProbeData{
		Container:   containerForFile(nativeAudioFormatNames[demuxer], entry.GetName()),
		CodecAudio:  codec,
		Duration:    int(duration),
		Bitrate:     kbps,
		AudioTracks: []AudioTrackInfo{{Codec: codec, Channels: int(meta.GetChannels()), Bitrate: kbps, SampleRate: int(meta.GetSampleRate()), Default: true}},
	}
	chapters := make([]ChapterInfo, 0, len(meta.GetChapters()))
	for i, chapter := range meta.GetChapters() {
		chapters = append(chapters, ChapterInfo{Index: i, Title: chapter.GetTitle(), StartSeconds: chapter.GetStartSeconds(), EndSeconds: chapter.GetEndSeconds(), Source: "embedded"})
	}
	return StorageAudioTrack{Entry: proto.CloneOf(entry), Status: StorageAudioTrackOK, Probe: &NativeAudioProbe{Probe: probe, DurationSeconds: duration, Chapters: chapters}}, true
}

// CachedStorageAudioTrack rebuilds a track from the probe an earlier scan
// kept for this entry's exact revision, without opening it.
func CachedStorageAudioTrack(entry *storagev1.Entry, status string, probe []byte) (StorageAudioTrack, error) {
	track := StorageAudioTrack{Entry: proto.CloneOf(entry), Status: StorageAudioTrackStatus(status)}
	switch track.Status {
	case StorageAudioTrackOK:
		var p nativeAudioStagedProbe
		if err := json.Unmarshal(probe, &p); err != nil {
			return StorageAudioTrack{}, err
		}
		track.Probe = &NativeAudioProbe{Probe: p.Probe, DurationSeconds: p.DurationSeconds, Chapters: p.Chapters, Tags: p.Tags}
	case StorageAudioTrackInvalid:
	default:
		return StorageAudioTrack{}, ErrNativeProbeInvalid
	}
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
