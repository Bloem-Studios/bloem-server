package main

import (
	"encoding/json"
	"os"
	"slices"
	"strconv"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// catalogState is a source a test rewrites between scans without changing the
// provider's configuration. The "catalog" mode reads it from <notify>.catalog
// on every List call. Books carry provider metadata, so nothing is read.
type catalogState struct {
	// Directories are the root's subdirectories, listed in this order.
	Directories []catalogDirectory `json:"directories"`
	// Token, when set, is the change token every complete directory listing
	// issues. An incremental listing must present it.
	Token string `json:"token,omitempty"`
	// Refuse lists directories whose change token is refused, as a provider
	// does when its own full-listing interval expires.
	Refuse []string `json:"refuse,omitempty"`
	// Fail lists directories whose listing fails.
	Fail []string `json:"fail,omitempty"`
}

type catalogDirectory struct {
	ID    string        `json:"id"`
	Files []catalogFile `json:"files"`
	// Changed and Removed answer an incremental listing: the file IDs it
	// lists as added or changed, and the file IDs it reports removed.
	Changed []string `json:"changed,omitempty"`
	Removed []string `json:"removed,omitempty"`
}

type catalogFile struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Revision string `json:"revision"`
}

const catalogPageSize = 2

func (s *server) listCatalog(r *storagev1.ListRequest) (*storagev1.ListResponse, error) {
	raw, err := os.ReadFile(s.notify + ".catalog")
	if err != nil {
		return nil, status.Error(codes.Unavailable, "catalog unavailable")
	}
	var state catalogState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, status.Error(codes.Unavailable, "catalog invalid")
	}
	if r.SourceId != "books" || r.MaxEntries <= 0 {
		return nil, status.Error(codes.InvalidArgument, "wrong list scope")
	}
	var entries []*storagev1.Entry
	var removed []string
	token := state.Token
	if r.DirectoryId == "root" {
		if r.ChangesSince != "" {
			return nil, status.Error(codes.FailedPrecondition, "root has no change feed")
		}
		for _, d := range state.Directories {
			entries = append(entries, &storagev1.Entry{Id: d.ID, Name: d.ID, LogicalPath: d.ID, Kind: storagev1.EntryKind_ENTRY_KIND_DIRECTORY, Revision: "directory"})
		}
		token = ""
	} else {
		i := slices.IndexFunc(state.Directories, func(d catalogDirectory) bool { return d.ID == r.DirectoryId })
		if i < 0 {
			return nil, status.Error(codes.NotFound, "unknown directory")
		}
		dir := state.Directories[i]
		if slices.Contains(state.Fail, dir.ID) {
			return nil, status.Error(codes.Unavailable, "synthetic listing failure")
		}
		files := dir.Files
		if r.ChangesSince != "" {
			if r.ChangesSince != state.Token || slices.Contains(state.Refuse, dir.ID) {
				return nil, status.Error(codes.FailedPrecondition, "change token not recognized; list fully")
			}
			files = slices.DeleteFunc(slices.Clone(files), func(f catalogFile) bool { return !slices.Contains(dir.Changed, f.ID) })
			for _, id := range dir.Removed {
				removed = append(removed, dir.ID+"/"+id)
			}
		}
		for _, f := range files {
			name := f.ID + "." + dir.ID
			entries = append(entries, &storagev1.Entry{Id: dir.ID + "/" + f.ID, Name: name, LogicalPath: dir.ID + "/" + name,
				Kind: storagev1.EntryKind_ENTRY_KIND_FILE, Size: 1024, ModifiedUnixNano: 1700000000000000000, Revision: f.Revision,
				Ebook: &storagev1.EbookMetadata{Title: f.Title, Authors: []string{"Catalog Writer"}}})
		}
	}
	offset := 0
	if r.Cursor != "" {
		if offset, err = strconv.Atoi(r.Cursor); err != nil || offset <= 0 || offset > len(entries) {
			return nil, status.Error(codes.InvalidArgument, "cursor")
		}
	}
	end := min(offset+catalogPageSize, len(entries))
	page := &storagev1.ListResponse{Entries: entries[offset:end], Complete: end == len(entries)}
	if page.Complete {
		page.ChangeToken, page.RemovedEntryIds = token, removed
	} else {
		page.NextCursor = strconv.Itoa(end)
	}
	return page, nil
}
