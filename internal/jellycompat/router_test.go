package jellycompat

import (
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/compatcontract"
	"github.com/Silo-Server/silo-server/internal/config"
)

const (
	fixtureOrdinaryProfile = "ordinary-profile-001"
	fixtureAdultProfile    = "adult-profile-002"
	fixtureOrdinaryItem    = "ordinary-item-001"
	fixtureAdultItem       = "adult-item-001"
)

type fixturePolicyContent struct{ access AccessFilterResolver }

func (c fixturePolicyContent) visible(session *Session, libraryID int) bool {
	filter := c.access(context.Background(), session.StreamAppUserID, session.ProfileID)
	for _, id := range filter.AllowedLibraryIDs {
		if id == libraryID {
			return true
		}
	}
	return false
}
func (c fixturePolicyContent) ListUserLibraries(_ context.Context, s *Session) ([]upstreamUserLibrary, error) {
	libs := []upstreamUserLibrary{{ID: 1, Name: "Ordinary", Type: "movies"}, {ID: 2, Name: "Adult", Type: "movies"}}
	out := []upstreamUserLibrary{}
	for _, lib := range libs {
		if c.visible(s, lib.ID) {
			out = append(out, lib)
		}
	}
	return out, nil
}
func (c fixturePolicyContent) BrowseItems(_ context.Context, s *Session, _ url.Values) (*upstreamBrowseResponse, error) {
	items := []upstreamListItem{{ContentID: fixtureOrdinaryItem, Type: "movie", Title: "Ordinary title"}}
	if c.visible(s, 2) {
		items = append(items, upstreamListItem{ContentID: fixtureAdultItem, Type: "movie", Title: "Adult title"})
	}
	return &upstreamBrowseResponse{Items: items, Total: len(items)}, nil
}
func (c fixturePolicyContent) SearchItems(ctx context.Context, s *Session, _ SearchItemsOptions) (*upstreamBrowseResponse, error) {
	return c.BrowseItems(ctx, s, nil)
}
func (c fixturePolicyContent) GetItemDetail(_ context.Context, s *Session, id string, _ *int) (*upstreamItemDetail, error) {
	if id == fixtureAdultItem && !c.visible(s, 2) {
		return nil, &HTTPError{StatusCode: http.StatusNotFound, Message: "not found"}
	}
	if id != fixtureOrdinaryItem && id != fixtureAdultItem {
		return nil, &HTTPError{StatusCode: http.StatusNotFound, Message: "not found"}
	}
	title := "Ordinary title"
	if id == fixtureAdultItem {
		title = "Adult title"
	}
	return &upstreamItemDetail{ContentID: id, Type: "movie", Title: title}, nil
}
func (c fixturePolicyContent) GetItemDetailsByIDs(ctx context.Context, s *Session, ids []string, l *int) (map[string]*upstreamItemDetail, error) {
	out := map[string]*upstreamItemDetail{}
	for _, id := range ids {
		if item, err := c.GetItemDetail(ctx, s, id, l); err == nil {
			out[id] = item
		}
	}
	return out, nil
}
func (fixturePolicyContent) ListSeasons(context.Context, *Session, string, *int) ([]upstreamSeason, error) {
	return []upstreamSeason{}, nil
}
func (fixturePolicyContent) GetSeason(context.Context, *Session, string, int, *int) (*upstreamSeason, error) {
	return nil, errors.New("not found")
}
func (fixturePolicyContent) ListEpisodes(context.Context, *Session, string, int, *int) ([]upstreamEpisode, error) {
	return []upstreamEpisode{}, nil
}
func (fixturePolicyContent) ListEpisodesBySeasonID(context.Context, *Session, string, *int) ([]upstreamEpisode, error) {
	return []upstreamEpisode{}, nil
}
func (fixturePolicyContent) ListItemFilters(context.Context, *Session, url.Values) (*upstreamItemFiltersResponse, error) {
	return &upstreamItemFiltersResponse{}, nil
}
func (fixturePolicyContent) EnrichSeriesUserData(context.Context, *Session, []upstreamListItem) {}

func fixtureAccessFilter(_ context.Context, _ int, profileID string) catalog.AccessFilter {
	if profileID == fixtureAdultProfile {
		return catalog.AccessFilter{AllowedLibraryIDs: []int{1, 2}}
	}
	return catalog.AccessFilter{AllowedLibraryIDs: []int{1}}
}

func TestEmbeddedJellyfinAdultPolicyContract(t *testing.T) {
	cfg, err := config.LoadFromDB(map[string]string{})
	if err != nil {
		t.Fatalf("LoadFromDB: %v", err)
	}
	codec := NewResourceIDCodec()
	store := NewSessionStore(time.Hour, time.Now)
	for _, profile := range []string{fixtureOrdinaryProfile, fixtureAdultProfile} {
		token := "fixture-" + profile
		if err := store.Put(Session{Token: token, StreamAppUserID: 7, ProfileID: profile, PseudoUserID: PseudoUserID(7, profile)}); err != nil {
			t.Fatalf("put %s session: %v", profile, err)
		}
	}
	content := fixturePolicyContent{access: fixtureAccessFilter}
	server := httptest.NewServer(NewRouter(Dependencies{Config: cfg, IDCodec: codec, SessionStore: store, Authenticator: NewAuthenticator(store, nil), ContentService: content, UserDataService: &mockUserDataService{}, AccessFilterFn: fixtureAccessFilter}))
	defer server.Close()
	run := func(name, profile string, suite compatcontract.Suite) {
		t.Helper()
		itemID := codec.EncodeStringID(EncodedIDItem, fixtureAdultItem)
		userID := PseudoUserID(7, profile).String()
		for i := range suite.Cases {
			suite.Cases[i].Path = strings.ReplaceAll(suite.Cases[i].Path, "adult-item-001", itemID)
			suite.Cases[i].Path = strings.ReplaceAll(suite.Cases[i].Path, "random-missing-002", codec.EncodeStringID(EncodedIDItem, "random-missing-002"))
			suite.Cases[i].Path = strings.ReplaceAll(suite.Cases[i].Path, "/Users/fixture/", "/Users/"+userID+"/")
			if profile == fixtureOrdinaryProfile {
				suite.Cases[i].AbsentStrings = append(suite.Cases[i].AbsentStrings, itemID)
			}
		}
		report, err := compatcontract.Run(context.Background(), compatcontract.Target{BaseURL: server.URL, Client: server.Client(), Credentials: compatcontract.CredentialFunc(func(req *http.Request) error { req.Header.Set("X-Emby-Token", "fixture-"+profile); return nil })}, suite)
		if err != nil {
			t.Fatalf("%s: %v; report=%s", name, err, report.JSON())
		}
	}
	run("ordinary", fixtureOrdinaryProfile, compatcontract.JellyfinOrdinaryAdultPolicy())
	run("adult", fixtureAdultProfile, compatcontract.JellyfinAuthorizedAdultPolicy())
}

func TestEmbeddedJellyfinCompatibilityContract(t *testing.T) {
	cfg, err := config.LoadFromDB(map[string]string{})
	if err != nil {
		t.Fatalf("LoadFromDB: %v", err)
	}
	server := httptest.NewServer(NewRouter(Dependencies{
		Config:        cfg,
		Authenticator: NewAuthenticator(NewSessionStore(time.Hour, time.Now), nil),
	}))
	defer server.Close()

	report, err := compatcontract.Run(context.Background(), compatcontract.Target{
		BaseURL: server.URL,
		Client:  server.Client(),
	}, compatcontract.JellyfinBaseline())
	if err != nil {
		t.Fatalf("embedded Jellyfin compatibility contract: %v; report=%s", err, report.JSON())
	}
}

func TestRouterCompressesJSONResponses(t *testing.T) {
	cfg, err := config.LoadFromDB(map[string]string{})
	if err != nil {
		t.Fatalf("LoadFromDB: %v", err)
	}
	router := NewRouter(Dependencies{Config: cfg})

	req := httptest.NewRequest(http.MethodGet, "/System/Info/Public", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}

	reader, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer reader.Close()
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read compressed body: %v", err)
	}
	if !strings.Contains(string(body), `"ProductName":"Jellyfin Server"`) {
		t.Fatalf("unexpected response body %q", string(body))
	}
}

func TestRouterServesCompatWebAssetsCreatedAfterStartup(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.LoadFromDB(map[string]string{
		"jellyfin_compat.enabled":         "true",
		"jellyfin_compat.web_install_dir": root,
		"jellyfin_compat.web_version":     "10.11.6",
	})
	if err != nil {
		t.Fatalf("LoadFromDB: %v", err)
	}
	router := NewRouter(Dependencies{Config: cfg})

	missingReq := httptest.NewRequest(http.MethodGet, "/web/", nil)
	missingRec := httptest.NewRecorder()
	router.ServeHTTP(missingRec, missingReq)
	if missingRec.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d, want %d", missingRec.Code, http.StatusNotFound)
	}

	release := filepath.Join(root, "10.11.6")
	writeValidWebRelease(t, release, "10.11.6")
	if err := os.WriteFile(filepath.Join(release, "index.html"), []byte("<!doctype html>ready"), 0o644); err != nil {
		t.Fatalf("write ready index: %v", err)
	}
	if err := os.Symlink("10.11.6", filepath.Join(root, "current")); err != nil {
		t.Fatalf("symlink current: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/web/", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), "ready") {
		t.Fatalf("unexpected response body %q", rec.Body.String())
	}
}

func TestRouterReportsDisabledCompatWebAssets(t *testing.T) {
	for _, tt := range []struct {
		name     string
		settings map[string]string
		wantBody string
	}{
		{
			name: "proxy disabled",
			settings: map[string]string{
				"jellyfin_compat.enabled":     "false",
				"jellyfin_compat.web_enabled": "true",
			},
			wantBody: "Jellyfin Web UI is disabled because the Jellyfin compatibility proxy is disabled",
		},
		{
			name: "web disabled",
			settings: map[string]string{
				"jellyfin_compat.enabled":     "true",
				"jellyfin_compat.web_enabled": "false",
			},
			wantBody: "Jellyfin Web UI is disabled in Silo settings",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			release := filepath.Join(root, "10.11.6")
			writeValidWebRelease(t, release, "10.11.6")
			if err := os.WriteFile(filepath.Join(release, "index.html"), []byte("<!doctype html>ready"), 0o644); err != nil {
				t.Fatalf("write ready index: %v", err)
			}
			if err := os.Symlink("10.11.6", filepath.Join(root, "current")); err != nil {
				t.Fatalf("symlink current: %v", err)
			}
			settings := map[string]string{
				"jellyfin_compat.web_install_dir": root,
				"jellyfin_compat.web_version":     "10.11.6",
			}
			for key, value := range tt.settings {
				settings[key] = value
			}
			cfg, err := config.LoadFromDB(settings)
			if err != nil {
				t.Fatalf("LoadFromDB: %v", err)
			}
			router := NewRouter(Dependencies{Config: cfg})

			req := httptest.NewRequest(http.MethodGet, "/web/", nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
			}
			if !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Fatalf("response body = %q, want %q", rec.Body.String(), tt.wantBody)
			}
			if strings.Contains(rec.Body.String(), "assets are not installed") {
				t.Fatalf("response body = %q, should not report installed assets as missing", rec.Body.String())
			}
		})
	}
}

func TestRouterRejectsArbitraryCompatWebDirectory(t *testing.T) {
	root := t.TempDir()
	arbitrary := t.TempDir()
	if err := os.WriteFile(filepath.Join(arbitrary, "index.html"), []byte("<!doctype html>secret"), 0o644); err != nil {
		t.Fatalf("write arbitrary index: %v", err)
	}
	cfg, err := config.LoadFromDB(map[string]string{
		"jellyfin_compat.enabled":         "true",
		"jellyfin_compat.web_install_dir": root,
		"jellyfin_compat.web_dir":         arbitrary,
		"jellyfin_compat.web_version":     "10.11.6",
	})
	if err != nil {
		t.Fatalf("LoadFromDB: %v", err)
	}
	router := NewRouter(Dependencies{Config: cfg})

	req := httptest.NewRequest(http.MethodGet, "/web/", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("arbitrary web_dir content was served: %q", rec.Body.String())
	}
}
