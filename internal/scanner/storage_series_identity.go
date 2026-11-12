package scanner

import (
	"path"
	"strings"

	"github.com/Silo-Server/silo-server/internal/naming"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

// storageSeriesIdentity is Silo's series identity for one listed episode
// file: the show (title, year, provider tags, content group) and the episode
// coordinates, read from its logical path as the filesystem scanner reads a
// path. Nothing here is persisted as a path or opened.
type storageSeriesIdentity struct {
	parsePath              string
	title                  string
	year                   int
	tvdbID, tmdbID, imdbID string
	groupKey               string
	groupKeyVersion        int
	confidence             string
	ambiguous              bool
	season, episode        int
	seasonKnown            bool
	multiStart, multiEnd   int
	// episodic reports an episode number. Only numbered files are published:
	// Silo links air-date and unnumbered files by reparsing the file path,
	// which a native file's opaque catalog key cannot supply.
	episodic bool
}

// StorageSeriesParseRoots places a library's selected roots under the
// parse-only root, so Silo's naming reads folders relative to what the
// library selected rather than the whole source: an all-buckets source's
// "television" bucket is not a show. A selected root that is itself a show
// folder (a Title (Year) name or provider tags) parses from its parent, so
// its title and tags still identify the show. The parse root itself is
// always a candidate.
func StorageSeriesParseRoots(roots []storagesource.SelectedRoot) []string {
	out := []string{storageMovieParseRoot}
	for _, root := range roots {
		if !root.Enabled {
			continue
		}
		p := path.Clean(storageMovieParseRoot + "/" + root.LogicalPath)
		for strings.HasPrefix(p, storageMovieParseRoot+"/") && storageNamesTitle(path.Base(p)) {
			p = path.Dir(p)
		}
		if strings.HasPrefix(p, storageMovieParseRoot+"/") {
			out = append(out, p)
		}
	}
	return out
}

func storageNamesTitle(name string) bool {
	if _, _, trusted := naming.ParseInferFolderTitleYear(name); trusted {
		return true
	}
	return naming.ParseStructuredFolderIDs(name) != nil
}

// storageSeriesIdentities applies Silo's series root, group and episode rules
// to one listed page. A later page joins an existing show through the
// persisted content group key, which a {tvdb-} tag anchors.
func storageSeriesIdentities(entries []*storagev1.Entry, roots []string) []storageSeriesIdentity {
	if len(roots) == 0 {
		roots = []string{storageMovieParseRoot}
	}
	paths := make([]string, len(entries))
	parseable := make([]string, 0, len(entries))
	for i, entry := range entries {
		if p, ok := storageParsePath(entry); ok {
			paths[i] = p
			parseable = append(parseable, p)
		}
	}
	_, assignments := naming.InferRootAssignments(parseable, "series", 0, nil, roots...)
	identities := make([]storageSeriesIdentity, len(entries))
	for i, entry := range entries {
		p := paths[i]
		if p == "" {
			continue
		}
		assignment, ok := assignments[p]
		if !ok {
			continue
		}
		group := naming.InferGroupIdentity(p, "series", assignment)
		hints := naming.ParseFilename(p, "series", assignment.LibraryRootPath)
		id := storageSeriesIdentity{parsePath: p, title: strings.TrimSpace(group.BaseTitle), year: group.BaseYear,
			tvdbID: group.TvdbID, tmdbID: group.TmdbID, imdbID: group.ImdbID,
			groupKey: group.ContentGroupKey, groupKeyVersion: group.GroupKeyVersion, confidence: group.Confidence,
			ambiguous: group.State == "ambiguous" && group.TvdbID == "" && group.TmdbID == "" && group.ImdbID == ""}
		if id.title == "" && hints != nil {
			id.title, id.year = strings.TrimSpace(hints.Title), hints.Year
		}
		if id.title == "" {
			id.title = strings.TrimSpace(strings.TrimSuffix(entry.GetName(), path.Ext(entry.GetName())))
		}
		if hints != nil && hints.Type == "series" && hints.EpisodeNum > 0 && group.BaseType == "series" {
			id.season, id.seasonKnown, id.episode, id.episodic = hints.SeasonNum, hints.SeasonKnown, hints.EpisodeNum, true
			if variant := naming.ParseVariantHints(p, "series", assignment.LibraryRootPath); variant != nil && variant.MultiEpisodeEnd > variant.MultiEpisodeStart {
				id.multiStart, id.multiEnd = variant.MultiEpisodeStart, variant.MultiEpisodeEnd
			}
		}
		identities[i] = id
	}
	return identities
}

// StorageSeriesFileSkipped reports a listed TV-library file that is never
// probed or published as an episode: a zero-byte object (a folder marker or a
// placeholder), anything in a skipped or supplemental folder, a title-owned
// extras folder, and a sample or extra that names no episode.
func StorageSeriesFileSkipped(entry *storagev1.Entry) bool {
	if entry.GetKind() != storagev1.EntryKind_ENTRY_KIND_FILE {
		return false
	}
	if entry.GetSize() <= 0 {
		return true
	}
	p, ok := storageParsePath(entry)
	if !ok {
		return true
	}
	if storageSupplementalAncestor(p) {
		return true
	}
	_, _, _, explicit := naming.ExplicitEpisodeCoordinates(entry.GetName())
	if _, extra := naming.ParseExtraSuffix(p); extra && !explicit {
		return true
	}
	stem := normalizeScannerDirLabel(strings.TrimSuffix(path.Base(p), path.Ext(p)))
	return stem == "sample" || strings.HasPrefix(stem, "sample ") || strings.HasSuffix(stem, " sample")
}

// storageSupplementalAncestor applies the folder rules of
// StorageMovieFileSkipped to a file's ancestors.
func storageSupplementalAncestor(p string) bool {
	for dir := path.Dir(p); strings.HasPrefix(dir, storageMovieParseRoot+"/"); dir = path.Dir(dir) {
		if SkippedDirName(path.Base(dir)) || shouldSkipMovieSupplementalDir(dir) {
			return true
		}
	}
	dir := path.Dir(p)
	for depth := 0; depth < extrasDirAncestorDepth && strings.HasPrefix(dir, storageMovieParseRoot+"/"); depth++ {
		if extrasDirKinds[normalizeScannerDirLabel(path.Base(dir))] != "" {
			return storageTitleOwns(dir)
		}
		dir = path.Dir(dir)
	}
	return false
}

// StorageSeriesSidecar is a text subtitle listed beside an episode. It is
// attached to that episode's file, never published as a catalog file.
type StorageSeriesSidecar struct {
	Entry                   *storagev1.Entry
	Base                    string
	Format, Language        string
	Forced, HearingImpaired bool
}

// storageSidecarFormats are the text sidecars Silo serves; bitmap sidecars
// (.sup, VobSub) are left out as the filesystem scan leaves them out.
var storageSidecarFormats = map[string]bool{"srt": true, "vtt": true, "ass": true, "ssa": true}

// StorageSeriesSidecarFromEntry recognizes a text subtitle sidecar.
func StorageSeriesSidecarFromEntry(entry *storagev1.Entry) (StorageSeriesSidecar, bool) {
	if entry.GetKind() != storagev1.EntryKind_ENTRY_KIND_FILE || entry.GetSize() < 0 || entry.GetRevision() == "" {
		return StorageSeriesSidecar{}, false
	}
	name := entry.GetName()
	format := strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
	if !storageSidecarFormats[format] {
		return StorageSeriesSidecar{}, false
	}
	if p, ok := storageParsePath(entry); !ok || storageSupplementalAncestor(p) {
		return StorageSeriesSidecar{}, false
	}
	return StorageSeriesSidecar{Entry: entry, Base: strings.TrimSuffix(name, path.Ext(name)), Format: format}, true
}

// PairStorageSeriesSidecars assigns each sidecar to the video in its folder
// whose base name it extends, with Silo's sidecar rules. The longest
// matching base wins, so "Show - S01E01.Part2.en.srt" belongs to
// "Show - S01E01.Part2.mkv" rather than "Show - S01E01.mkv".
func PairStorageSeriesSidecars(videos []*storagev1.Entry, sidecars []StorageSeriesSidecar) map[string][]StorageSeriesSidecar {
	out := map[string][]StorageSeriesSidecar{}
	for _, sub := range sidecars {
		dir := path.Dir(path.Clean("/" + sub.Entry.GetLogicalPath()))
		best, bestLen := "", -1
		var bestSuffix string
		for _, video := range videos {
			if path.Dir(path.Clean("/"+video.GetLogicalPath())) != dir {
				continue
			}
			base := strings.TrimSuffix(video.GetName(), path.Ext(video.GetName()))
			suffix, ok := naming.SubtitleSuffix(base, sub.Base)
			if ok && len(base) > bestLen {
				best, bestLen, bestSuffix = video.GetId(), len(base), suffix
			}
		}
		if best == "" {
			continue
		}
		tags := naming.ParseSubtitleSuffix(bestSuffix)
		sub.Language, sub.Forced, sub.HearingImpaired = tags.Language, tags.Forced, tags.HearingImpaired
		out[best] = append(out[best], sub)
	}
	return out
}

// StorageSeriesEpisodic reports, by entry ID, the listed videos Silo's naming
// gives an episode number, which are the only ones published.
func StorageSeriesEpisodic(entries []*storagev1.Entry, roots []string) map[string]bool {
	out := make(map[string]bool, len(entries))
	for i, identity := range storageSeriesIdentities(entries, roots) {
		if identity.episodic {
			out[entries[i].GetId()] = true
		}
	}
	return out
}
