package jellycompat

// Bloem-owned tests for this package. Kept out of Silo's own test files so
// upstream merges do not conflict here; see contracts/seams.txt.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
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
