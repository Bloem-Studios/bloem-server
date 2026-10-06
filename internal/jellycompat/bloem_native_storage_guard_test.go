package jellycompat

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/streamtelemetry"
)

func TestNativeContainmentJellyfinOriginalCollision(t *testing.T) {
	t.Chdir(t.TempDir())
	const key = "bloem-storage:malformed"
	const body = "unrelated-jellyfin-local-bytes"
	if err := os.WriteFile(key, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFromDB(map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	codec := NewResourceIDCodec()
	store := NewSessionStore(time.Hour, nil)
	if err := store.Put(Session{Token: compatSocketToken, StreamAppUserID: 1, ProfileID: "profile-1"}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, path string
		status     int
	}{{"native", key, 404}, {"ordinary", "./" + key, 200}} {
		for _, method := range []string{"GET", "HEAD"} {
			t.Run(tc.name+"/"+method, func(t *testing.T) {
				detail := &upstreamItemDetail{ContentID: "book", Type: "ebook", Title: "Book", Versions: []catalog.FileVersion{{FileID: 42, FilePath: tc.path, Container: "epub"}}}
				telemetryCfg := streamtelemetry.DefaultConfig("i1-jellyfin")
				telemetryCfg.Enabled = true
				telemetry := streamtelemetry.NewRegistry(telemetryCfg, streamtelemetry.NewLocalStore(), nil)
				t.Cleanup(func() { _ = telemetry.Stop(t.Context()) })
				server := NewServerWithDependencies(Dependencies{StreamTelemetry: telemetry, Config: cfg, SessionStore: store, IDCodec: codec, ContentService: &stubContentService{detail: detail}, FileResolver: GuardNativeStorageFiles(testCompatFileResolver{file: &models.MediaFile{ID: 42, FilePath: tc.path}}), SessionMgr: &testCompatSessionManager{}})
				path := "/Items/" + codec.EncodeStringID(EncodedIDItem, "book") + "/Download?api_key=" + compatSocketToken
				rr := httptest.NewRecorder()
				server.Handler().ServeHTTP(rr, httptest.NewRequest(method, path, nil))
				if rr.Code != tc.status {
					t.Errorf("status=%d want=%d body=%s", rr.Code, tc.status, rr.Body.String())
				}
				if tc.status == 200 && method == "GET" && rr.Body.String() != body {
					t.Error("local control bytes differ")
				}
				if tc.status == 404 && len(telemetry.Snapshot().Transfers) != 0 {
					t.Error("native transfer attached")
				}
				if tc.status == 404 && (rr.Body.String() == body || rr.Header().Get("Content-Disposition") != "") {
					t.Error("native attachment escaped")
				}
				unauth := httptest.NewRecorder()
				server.Handler().ServeHTTP(unauth, httptest.NewRequest(method, "/Items/"+codec.EncodeStringID(EncodedIDItem, "book")+"/Download", nil))
				if unauth.Code != 401 {
					t.Errorf("unauth=%d", unauth.Code)
				}
			})
		}
	}
}

type nativeContainmentResolver struct {
	file *models.MediaFile
	err  error
}

func (r nativeContainmentResolver) GetByID(context.Context, int) (*models.MediaFile, error) {
	return r.file, r.err
}
func TestNativeContainmentJellyfinResolverSemantics(t *testing.T) {
	if GuardNativeStorageFiles(nil) != nil {
		t.Error("nil resolver changed")
	}
	boom := errors.New("lookup unavailable")
	for _, tc := range []struct {
		file *models.MediaFile
		err  error
	}{
		{nil, nil}, {nil, boom}, {&models.MediaFile{FilePath: "bloem-storage:"}, boom}, {&models.MediaFile{FilePath: "/books/bloem-storage:key"}, nil},
	} {
		f, e := GuardNativeStorageFiles(nativeContainmentResolver{tc.file, tc.err}).GetByID(t.Context(), 1)
		if f != tc.file || !errors.Is(e, tc.err) {
			t.Errorf("file/error changed %v %v", f, e)
		}
	}
	f, e := GuardNativeStorageFiles(nativeContainmentResolver{file: &models.MediaFile{FilePath: "bloem-storage:"}}).GetByID(t.Context(), 1)
	if f != nil || !errors.Is(e, scanner.ErrFileNotFound) {
		t.Error("reserved malformed key not hidden")
	}
}
