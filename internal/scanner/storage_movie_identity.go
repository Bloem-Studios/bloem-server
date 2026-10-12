package scanner

import (
	"path"
	"strconv"
	"strings"

	"github.com/Silo-Server/silo-server/internal/librarykind"
	"github.com/Silo-Server/silo-server/internal/naming"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
)

// storageMovieParseRoot places a provider's logical path below a library root
// so Silo's naming rules can read its folders. It is parse-only: never
// persisted, opened, or compared with a host path.
const storageMovieParseRoot = "/bloem-storage"

// storageParsePath returns the entry's logical path below
// storageMovieParseRoot. Logical paths are provider display metadata: dot
// segments resolve inside the source, and a path that escapes it or does not
// end in the entry's name falls back to the name alone. False means the entry
// has no usable name.
func storageParsePath(entry *storagev1.Entry) (string, bool) {
	name := entry.GetName()
	logical := path.Clean(storageMovieParseRoot + "/" + entry.GetLogicalPath())
	if strings.HasPrefix(logical, storageMovieParseRoot+"/") && path.Base(logical) == name {
		return logical, true
	}
	fallback := path.Clean(storageMovieParseRoot + "/" + path.Base("/"+name))
	if !strings.HasPrefix(fallback, storageMovieParseRoot+"/") {
		return "", false
	}
	return fallback, true
}

// StorageMovieTitle is one title/year hypothesis for provider search.
type StorageMovieTitle struct {
	Title, Source string
	Year          int
}

// storageMovieIdentity is Silo's filename identity for one listed movie file,
// read from its logical path the way the filesystem scanner reads a path.
type storageMovieIdentity struct {
	parsePath       string
	title           string
	year            int
	tmdbID, imdbID  string
	groupKey        string
	groupKeyVersion int
	confidence      string
	ambiguous       bool
	variant         *naming.VariantHints
	alternates      []StorageMovieTitle
}

// storageMovieIdentities applies Silo's movie root, group and edition rules to
// one listed page. The page is the native listing's unit of consistency: a
// title folder's files normally arrive together, but a later page still joins
// an existing item through the persisted content group key.
func storageMovieIdentities(entries []*storagev1.Entry) []storageMovieIdentity {
	paths := make([]string, len(entries))
	for i, entry := range entries {
		paths[i], _ = storageParsePath(entry)
	}
	parseable := make([]string, 0, len(paths))
	for _, p := range paths {
		if p != "" {
			parseable = append(parseable, p)
		}
	}
	_, assignments := naming.InferRootAssignments(parseable, "movies", 0, nil, storageMovieParseRoot)
	identities := make([]storageMovieIdentity, len(entries))
	for i, entry := range entries {
		p := paths[i]
		stem := strings.TrimSuffix(entry.GetName(), path.Ext(entry.GetName()))
		if p == "" {
			identities[i] = storageMovieIdentity{title: strings.TrimSpace(stem)}
			continue
		}
		assignment, ok := assignments[p]
		if !ok {
			assignment = naming.RootAssignment{FilePath: p, RootPath: path.Dir(p), LibraryRootPath: storageMovieParseRoot, InferredType: "movie"}
		}
		group := naming.InferGroupIdentity(p, "movies", assignment)
		hints := naming.ParseFilename(p, "movies", storageMovieParseRoot)
		id := storageMovieIdentity{parsePath: p, title: strings.TrimSpace(group.BaseTitle), year: group.BaseYear,
			tmdbID: group.TmdbID, imdbID: group.ImdbID, groupKey: group.ContentGroupKey, groupKeyVersion: group.GroupKeyVersion,
			confidence: group.Confidence, ambiguous: group.State == "ambiguous" && group.TmdbID == "" && group.ImdbID == "",
			variant: naming.ParseVariantHints(p, "movies", storageMovieParseRoot)}
		if id.title == "" {
			id.title, id.year = strings.TrimSpace(hints.Title), hints.Year
		}
		if id.title == "" {
			id.title = strings.TrimSpace(stem)
		}
		id.alternates = storageMovieAlternates(id, hints, stem, group.ObservedRootPath)
		identities[i] = id
	}
	return identities
}

// storageMovieAlternates mirrors the queued movie matcher's fallback
// identities: the parsed path, the file name and its release folder.
func storageMovieAlternates(id storageMovieIdentity, hints *naming.FilenameHints, stem, observedRoot string) []StorageMovieTitle {
	seen := map[string]bool{storageTitleKey(id.title, id.year): true}
	var out []StorageMovieTitle
	add := func(title string, year int, source string) {
		title = strings.TrimSpace(naming.StripComparisonSafeEditionSuffix(title))
		key := storageTitleKey(title, year)
		if title == "" || seen[key] || len(out) == 3 {
			return
		}
		seen[key] = true
		out = append(out, StorageMovieTitle{Title: title, Year: year, Source: source})
	}
	if hints != nil {
		add(hints.Title, hints.Year, "current_path")
	}
	parsed := naming.ParseInferMovieStem(stem, id.title, id.year)
	add(parsed.Title, parsed.Year, "filename")
	if observedRoot != "" && observedRoot != storageMovieParseRoot {
		folder := naming.ParseInferMovieStem(path.Base(observedRoot), "", 0)
		add(folder.Title, folder.Year, "release_folder")
	}
	return out
}

func storageTitleKey(title string, year int) string {
	return strings.ToLower(strings.Join(strings.Fields(title), " ")) + "\x00" + strconv.Itoa(year)
}

// StorageMovieFileSkipped reports a listed movie-library file that Silo's
// filesystem walk would not publish as a movie: one inside an ignored or
// sample/subtitle folder, a sample, or a local extra. Extras are classified by
// Silo's folder and suffix conventions. A native listing cannot prove that a
// title folder holds media of its own, so an extras folder counts as owned
// when its owner is named like a title (Title (Year) or provider tags).
func StorageMovieFileSkipped(entry *storagev1.Entry) bool {
	if entry.GetKind() != storagev1.EntryKind_ENTRY_KIND_FILE {
		return false
	}
	p, ok := storageParsePath(entry)
	if !ok {
		return false
	}
	for dir := path.Dir(p); strings.HasPrefix(dir, storageMovieParseRoot+"/"); dir = path.Dir(dir) {
		if isIgnoredDirectoryPath(dir) || shouldSkipMovieSupplementalDir(dir) {
			return true
		}
	}
	dir := path.Dir(p)
	for depth := 0; depth < extrasDirAncestorDepth && strings.HasPrefix(dir, storageMovieParseRoot+"/"); depth++ {
		if extrasDirKinds[normalizeScannerDirLabel(path.Base(dir))] != "" {
			if storageTitleOwns(dir) {
				return true
			}
			break
		}
		dir = path.Dir(dir)
	}
	if _, extra := naming.ParseExtraSuffix(p); extra {
		return true
	}
	return shouldSkipMovieSupplementalFile(p)
}

// StorageDirectorySkipped reports a listed directory the native scan never
// lists, as Silo's walk never enters it: an ignored directory name for every
// library kind, and for movie libraries a sample/subtitle folder or an extras
// folder owned by a title. Its entry is still recorded; its contents cost no
// provider requests.
func StorageDirectorySkipped(libraryType string, entry *storagev1.Entry) bool {
	if entry.GetKind() != storagev1.EntryKind_ENTRY_KIND_DIRECTORY {
		return false
	}
	p, ok := storageParsePath(entry)
	if !ok {
		return false
	}
	movie := librarykind.IsMovie(libraryType)
	for dir := p; strings.HasPrefix(dir, storageMovieParseRoot+"/"); dir = path.Dir(dir) {
		if isIgnoredDirectoryPath(dir) {
			return true
		}
		if movie && (shouldSkipMovieSupplementalDir(dir) ||
			(extrasDirKinds[normalizeScannerDirLabel(path.Base(dir))] != "" && storageTitleOwns(dir))) {
			return true
		}
	}
	return false
}

// storageTitleOwns reports that a convention-named supplemental directory
// belongs to a title folder rather than naming a content scope such as
// movies/shorts/ or 4K/Other/.
func storageTitleOwns(supplementalDir string) bool {
	owner := firstNonSupplementalAncestor(supplementalDir)
	if !strings.HasPrefix(owner, storageMovieParseRoot+"/") {
		return false
	}
	name := path.Base(owner)
	if _, _, trusted := naming.ParseInferFolderTitleYear(name); trusted {
		return true
	}
	return naming.ParseStructuredFolderIDs(name) != nil
}
