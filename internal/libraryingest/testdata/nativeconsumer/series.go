package main

import (
	"os"
	"path"
	"strconv"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
)

// seriesTree serves the TV layout verified on the live ClearStream node:
// television/<group>/<Show (Year) {tvdb-ID}>/Season NN/<Show (Year)> - SxxEyy [quality]-Group.mkv,
// show folders with artwork and tvshow.nfo, several subtitle sidecars per
// episode, a .downloads/ folder and zero-byte objects. Entry IDs are the
// logical paths, as an object-key-encoded provider would expose its keys.
// Directories are listed two entries per page, so sidecars and their episode
// cross page boundaries.
const (
	seriesShowA = "television/00s/Show A (2005) {tvdb-111}"
	seriesShowB = "television/10s/Show B (2012) {tvdb-222}"
	seriesA01   = seriesShowA + "/Season 01/Show A (2005) - S01E01 [WEBDL-1080p]-Grp"
	seriesA02   = seriesShowA + "/Season 01/Show A (2005) - S01E02 [WEBDL-1080p]-Grp"
	seriesA00   = seriesShowA + "/Season 00/Show A (2005) - S00E01 [WEBDL-1080p]-Grp"
	seriesB01   = seriesShowB + "/Season 01/Show B (2012) - S01E01-E02 [HDTV-720p]-Grp"
	seriesShowC = "television/10s/Show C (2015) {tvdb-333}"
	seriesC01   = seriesShowC + "/Season 01/Show C (2015) - S01E01"
	seriesC02   = seriesShowC + "/Season 01/Show C (2015) - S01E02"
)

func seriesFixtures(prefix, revision string) ([]object, error) {
	mkv, err := os.ReadFile(prefix + ".mkv")
	if err != nil {
		return nil, err
	}
	// Each sidecar's text names its file, so a read shows which one it got.
	srt := func(name string) []byte {
		return []byte("1\n00:00:00,000 --> 00:00:01,000\nhi " + path.Base(name) + "\n")
	}
	var result []object
	add := func(logical string, data []byte, directory bool) {
		entry := &storagev1.Entry{Id: logical, Name: path.Base(logical), LogicalPath: logical, Kind: storagev1.EntryKind_ENTRY_KIND_FILE,
			Size: int64(len(data)), Revision: revision, ModifiedUnixNano: 1700000000000000000}
		if directory {
			entry.Kind, entry.Revision = storagev1.EntryKind_ENTRY_KIND_DIRECTORY, "directory"
		}
		result = append(result, object{entry, data})
	}
	for _, dir := range []string{"television", "television/00s", "television/00s/.downloads", seriesShowA, seriesShowA + "/Season 00", seriesShowA + "/Season 01",
		seriesShowA + "/Featurettes", "television/10s", seriesShowB, seriesShowB + "/Season 01"} {
		add(dir, nil, true)
	}
	for _, name := range []string{"tvshow.nfo", "poster.jpg", "fanart.jpg", "banner.jpg", "clearlogo.png"} {
		add(seriesShowA+"/"+name, []byte("sidecar "+name), false)
	}
	add("television/00s/.downloads/Show A (2005) - S01E09 [WEBDL-1080p]-Grp.mkv", mkv, false)
	add(seriesShowA+"/Featurettes/Making Of.mkv", mkv, false)
	add(seriesA01+".mkv", mkv, false)
	for _, suffix := range []string{".en.srt", ".en.sdh.srt", ".nl.srt"} {
		add(seriesA01+suffix, srt(seriesA01+suffix), false)
	}
	add(seriesA02+".mkv", mkv, false)
	add(seriesA02+".en.srt", srt(seriesA02+".en.srt"), false)
	// A zero-byte placeholder of a later episode and a sample.
	add(seriesShowA+"/Season 01/Show A (2005) - S01E03 [WEBDL-1080p]-Grp.mkv", nil, false)
	add(seriesShowA+"/Season 01/sample.mkv", mkv, false)
	// Sidecars listed a page before their episode.
	add(seriesA00+".en.srt", srt(seriesA00+".en.srt"), false)
	add(seriesA00+".nl.srt", srt(seriesA00+".nl.srt"), false)
	add(seriesA00+".mkv", mkv, false)
	add(seriesB01+".mkv", mkv, false)
	add(seriesB01+".zh.srt", srt(seriesB01+".zh.srt"), false)
	if url, err := os.ReadFile(prefix + ".strm-url"); err == nil {
		// A test that serves a remote episode names its address here: one
		// `.strm` episode with a sidecar, and one whose address never answers.
		add(seriesShowC, nil, true)
		add(seriesShowC+"/Season 01", nil, true)
		add(seriesC01+".strm", url, false)
		add(seriesC01+".en.srt", srt(seriesC01+".en.srt"), false)
		add(seriesC02+".strm", []byte("https://untrusted.invalid/remote-episode\n"), false)
	}
	return result, nil
}

// listSeries lists one directory of the tree. <notify>.series-removed holds
// entry IDs that are gone, one per line. Listing an excluded folder fails, so
// a scan that lists one fails.
func (s *server) listSeries(r *storagev1.ListRequest) (*storagev1.ListResponse, error) {
	if r.SourceId != "books" || r.MaxEntries <= 0 {
		return nil, status.Error(codes.InvalidArgument, "wrong list scope")
	}
	if r.ChangesSince != "" {
		return nil, status.Error(codes.FailedPrecondition, "no change feed")
	}
	dir := r.DirectoryId
	if dir == "root" {
		dir = ""
	}
	if strings.HasSuffix(dir, "/.downloads") || strings.HasSuffix(dir, "/Featurettes") {
		return nil, status.Error(codes.FailedPrecondition, "excluded folder listed")
	}
	s.audit("list " + r.DirectoryId)
	removed := map[string]bool{}
	if b, err := os.ReadFile(s.notify + ".series-removed"); err == nil {
		for _, id := range strings.Split(string(b), "\n") {
			removed[id] = true
		}
	}
	var children []*storagev1.Entry
	for _, o := range s.objects {
		parent := path.Dir(o.entry.LogicalPath)
		if parent == "." {
			parent = ""
		}
		if parent == dir && !removed[o.entry.Id] {
			children = append(children, o.entry)
		}
	}
	offset := 0
	if r.Cursor != "" {
		var err error
		if offset, err = strconv.Atoi(r.Cursor); err != nil || offset <= 0 || offset >= len(children) {
			return nil, status.Error(codes.InvalidArgument, "cursor")
		}
	}
	end := min(offset+2, len(children))
	page := &storagev1.ListResponse{Entries: children[offset:end], Complete: end == len(children)}
	if !page.Complete {
		page.NextCursor = strconv.Itoa(end)
	}
	return page, nil
}
