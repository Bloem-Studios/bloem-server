package downloads

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
)

type nativeContainmentFiles struct {
	single *models.MediaFile
	rows   []*models.MediaFile
	bulk   map[string][]*models.MediaFile
	err    error
}

func (f *nativeContainmentFiles) GetByID(context.Context, int) (*models.MediaFile, error) {
	return f.single, f.err
}
func (f *nativeContainmentFiles) GetByContentID(context.Context, string) ([]*models.MediaFile, error) {
	return f.rows, f.err
}
func (f *nativeContainmentFiles) GetByEpisodeID(context.Context, string) ([]*models.MediaFile, error) {
	return f.rows, f.err
}
func (f *nativeContainmentFiles) ListByEpisodeIDs(context.Context, []string) (map[string][]*models.MediaFile, error) {
	return f.bulk, f.err
}
func TestNativeContainmentDownloadResolver(t *testing.T) {
	native := &models.MediaFile{ID: 1, FilePath: "bloem-storage:"}
	local := &models.MediaFile{ID: 2, FilePath: "/physical/bloem-storage:key"}
	raw := &nativeContainmentFiles{single: native, rows: []*models.MediaFile{native, local}, bulk: map[string][]*models.MediaFile{"a": {native, local}, "nil": nil, "empty": {}}}
	guarded := GuardNativeStorageFiles(raw)
	if f, e := guarded.GetByID(t.Context(), 1); f != nil || !errors.Is(e, catalog.ErrItemNotFound) {
		t.Errorf("reserved ID=%v %v", f, e)
	}
	for _, get := range []func(context.Context, string) ([]*models.MediaFile, error){guarded.GetByContentID, guarded.GetByEpisodeID} {
		rows, e := get(t.Context(), "a")
		if e != nil || len(rows) != 1 || rows[0] != local {
			t.Errorf("candidates=%v err=%v", rows, e)
		} else {
			rows[0] = nil
		}
		if raw.rows[0] != native || raw.rows[1] != local {
			t.Error("mutated shared candidates")
		}
	}
	bulk, e := guarded.ListByEpisodeIDs(t.Context(), []string{"a"})
	if e != nil || len(bulk["a"]) != 1 || bulk["a"][0] != local {
		t.Errorf("bulk=%v err=%v", bulk, e)
	} else {
		bulk["a"][0] = nil
		delete(bulk, "nil")
	}
	if len(raw.bulk["a"]) != 2 || raw.bulk["a"][0] != native || raw.bulk["a"][1] != local || raw.bulk["nil"] != nil {
		t.Error("mutated shared bulk")
	}
	if raw.single.FilePath != "bloem-storage:" {
		t.Error("mutated object")
	}
	if GuardNativeStorageFiles(nil) != nil {
		t.Error("nil resolver changed")
	}
	raw.single = nil
	raw.rows = nil
	raw.bulk = nil
	if f, e := guarded.GetByID(t.Context(), 0); f != nil || e != nil {
		t.Error("nil file changed")
	}
	if f, e := guarded.GetByContentID(t.Context(), ""); f != nil || e != nil {
		t.Error("nil slice changed")
	}
	if f, e := guarded.ListByEpisodeIDs(t.Context(), nil); f != nil || e != nil {
		t.Error("nil map changed")
	}
	boom := errors.New("db unavailable")
	raw.err = boom
	if _, e := guarded.GetByID(t.Context(), 1); !errors.Is(e, boom) {
		t.Error("ID error swallowed")
	}
	if _, e := guarded.GetByContentID(t.Context(), ""); !errors.Is(e, boom) {
		t.Error("content error swallowed")
	}
	if _, e := guarded.GetByEpisodeID(t.Context(), ""); !errors.Is(e, boom) {
		t.Error("episode error swallowed")
	}
	if _, e := guarded.ListByEpisodeIDs(t.Context(), nil); !errors.Is(e, boom) {
		t.Error("bulk error swallowed")
	}
	// Error outputs are not rewritten or interpreted as a native hit.
	raw.single = native
	raw.rows = []*models.MediaFile{native}
	raw.bulk = map[string][]*models.MediaFile{"a": {native}}
	if f, e := guarded.GetByID(t.Context(), 1); f != native || !errors.Is(e, boom) {
		t.Error("error result changed")
	}
	if rows, e := guarded.GetByContentID(t.Context(), ""); !reflect.DeepEqual(rows, raw.rows) || !errors.Is(e, boom) {
		t.Error("error slice changed")
	}
}
func TestNativeContainmentDownloadDirectCollision(t *testing.T) {
	t.Chdir(t.TempDir())
	const key = "bloem-storage:legacy"
	const body = "unrelated-download-local-bytes"
	if err := os.WriteFile(key, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, path string
		deny       bool
	}{{"native", key, true}, {"ordinary", "./" + key, false}} {
		t.Run(tc.name, func(t *testing.T) {
			raw := &nativeContainmentFiles{single: &models.MediaFile{ID: 1, ContentID: "book", FilePath: tc.path}}
			svc := serviceWithFileRepo(GuardNativeStorageFiles(raw))
			svc.itemAccess = allowDownloadItemAccess{}
			target, err := svc.ResolveDirectFile(t.Context(), 7, "", 1, FormatOriginal, catalog.AccessFilter{})
			if tc.deny {
				if target != nil || !errors.Is(err, catalog.ErrItemNotFound) {
					t.Errorf("signed target escaped: %+v %v", target, err)
				}
			} else if err != nil || target.Path != tc.path {
				t.Errorf("local target=%+v %v", target, err)
			}
			rr := httptest.NewRecorder()
			err = svc.ServeDirect(t.Context(), rr, httptest.NewRequest(http.MethodGet, "/", nil), 7, "", 1, FormatOriginal, catalog.AccessFilter{})
			if tc.deny {
				if !errors.Is(err, catalog.ErrItemNotFound) || rr.Body.Len() != 0 || rr.Header().Get("Content-Disposition") != "" {
					t.Errorf("escaped body=%q err=%v", rr.Body.String(), err)
				}
			} else if err != nil || rr.Body.String() != body {
				t.Errorf("local bytes=%q err=%v", rr.Body.String(), err)
			}
		})
	}
}
