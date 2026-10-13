package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/managedtracking"
	calendar "github.com/Silo-Server/silo-server/internal/managedtracking/calendarwire"
	"github.com/Silo-Server/silo-server/internal/policy"
	"github.com/Silo-Server/silo-server/internal/recommendations"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

type managedCalendarScopeResolver interface {
	Resolve(context.Context, access.ResolveInput) (access.Scope, error)
}
type managedCalendarSource struct {
	stores          userstore.UserStoreProvider
	resolver        managedCalendarScopeResolver
	prepare         func(context.Context, managedtracking.Profile) (context.Context, error)
	pool            *pgxpool.Pool
	repo            *catalog.CalendarRepository
	handler         *handlers.CalendarHandler
	recommendations *recommendations.Reader
}

// ConfigureManagedCalendar composes an immutable adapter once all host policy
// dependencies exist. Earlier plugin calls fail closed until the atomic publish.
func ConfigureManagedCalendar(deps Dependencies, service *managedtracking.Service) {
	if service == nil || deps.DB == nil || deps.UserStoreProvider == nil || deps.PolicySystem == nil {
		return
	}
	users := auth.NewUserRepository(deps.DB)
	groups := access.NewTenantGroupStore(deps.DB)
	settings := catalog.NewEncryptedSettingsRepo(catalog.NewServerSettingsRepo(deps.DB), deps.SecretCipher)
	resolver := policy.NewViewerResolver(users, deps.UserStoreProvider, nil, deps.PolicySystem.PDP(), groups).WithUnratedContentPolicy(config.NewUnratedContentPolicy(settings)).WithBloemTenancy(resourcetenancy.NewStore(deps.DB))
	repo := catalog.NewCalendarRepository(deps.DB)
	source := &managedCalendarSource{stores: deps.UserStoreProvider, resolver: resolver, pool: deps.DB, repo: repo, handler: handlers.NewCalendarHandler(repo, nil, nil, nil)}
	source.recommendations = recommendations.NewReader(recommendations.NewRepo(deps.DB), catalog.NewRatingsRepo(deps.DB), deps.RecWorker, deps.UserStoreProvider)
	source.prepare = func(ctx context.Context, p managedtracking.Profile) (context.Context, error) {
		account, err := strconv.Atoi(p.AccountID)
		if err != nil {
			return nil, managedtracking.ErrAuthority
		}
		user, err := users.GetByID(ctx, account)
		if err != nil {
			return nil, err
		}
		if user == nil || !user.Enabled {
			return nil, managedtracking.ErrAuthority
		}
		store, err := deps.UserStoreProvider.ForUser(ctx, account)
		if err != nil {
			return nil, err
		}
		profile, err := store.GetProfile(ctx, p.ProfileID)
		if err != nil {
			return nil, err
		}
		if profile == nil || profile.PINHash != "" {
			return nil, managedtracking.ErrAuthority
		}
		organization, err := uuid.Parse(profile.OrganizationID)
		if err != nil {
			return nil, managedtracking.ErrAuthority
		}
		tenant, err := tenancy.NewResolver(tenancy.NewStore(deps.DB)).ResolveProfile(ctx, account, organization)
		if err != nil {
			return nil, err
		}
		return tenancy.WithContext(ctx, tenant), nil
	}
	service.SetCalendarSource(source)
}
func (s *managedCalendarSource) scope(ctx context.Context, p managedtracking.Profile) (access.Scope, error) {
	if s == nil || s.resolver == nil || !p.AccountActive || !p.Active {
		return access.Scope{}, managedtracking.ErrAuthority
	}
	account, err := strconv.Atoi(p.AccountID)
	if err != nil || account < 1 {
		return access.Scope{}, managedtracking.ErrAuthority
	}
	if s.prepare != nil {
		ctx, err = s.prepare(ctx, p)
		if err != nil {
			return access.Scope{}, err
		}
	}
	scope, err := s.resolver.Resolve(ctx, access.ResolveInput{UserID: account, ProfileID: p.ProfileID})
	if err != nil {
		return access.Scope{}, err
	}
	if scope.UserID != account || scope.ProfileID != p.ProfileID || !scope.ProfileVerified || scope.PINVerificationSkipped || scope.PreferencesDegraded {
		return access.Scope{}, managedtracking.ErrAuthority
	}
	return scope, nil
}
func (s *managedCalendarSource) Authority(ctx context.Context, p managedtracking.Profile) (string, error) {
	scope, err := s.scope(ctx, p)
	if err != nil {
		return "", err
	}
	// Nil and empty allowlists have different meanings; preserve that distinction.
	if scope.AllowedLibraryIDs != nil {
		scope.AllowedLibraryIDs = append([]int{}, scope.AllowedLibraryIDs...)
		sort.Ints(scope.AllowedLibraryIDs)
	}
	if scope.DisabledLibraryIDs != nil {
		scope.DisabledLibraryIDs = append([]int{}, scope.DisabledLibraryIDs...)
		sort.Ints(scope.DisabledLibraryIDs)
	}
	var libraries json.RawMessage
	if s.pool != nil {
		err = s.pool.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_array(id,enabled) ORDER BY id),'[]'::jsonb) FROM media_folders WHERE $1::integer[] IS NULL OR id=ANY($1)`, scope.AllowedLibraryIDs).Scan(&libraries)
		if err != nil {
			return "", err
		}
	}
	var personal [][]string
	if s.repo != nil {
		for _, read := range []func(context.Context, int, string) ([]string, error){s.repo.ListFollowedItemIDs, s.repo.ListFavoriteItemIDs, s.repo.ListWatchlistItemIDs} {
			ids, readErr := read(ctx, scope.UserID, scope.ProfileID)
			if readErr != nil {
				return "", readErr
			}
			sort.Strings(ids)
			personal = append(personal, ids)
		}
	}
	raw, err := json.Marshal(struct {
		Scope                            access.Scope
		Personal                         [][]string
		Preset                           string
		Libraries                        json.RawMessage
		AccountRevision, ProfileRevision uint64
	}{scope, personal, "following", libraries, p.AccountRevision, p.Revision})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

// managedCalendarArtworkID retains asset identifiers and omits remote URLs.
// The destination accepts metadata references, never remote image sources.
func managedCalendarArtworkID(value string) string {
	if strings.Contains(value, "://") || strings.HasPrefix(value, "//") {
		return ""
	}
	return value
}
func calendarIDSet(ids []string) map[string]bool {
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out
}
func optionalString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
func optionalNumber(v *int) *int32 {
	if v == nil {
		return nil
	}
	n := int32(*v)
	return &n
}
func (s *managedCalendarSource) Events(ctx context.Context, p managedtracking.Profile, start, end time.Time) ([]*calendar.CalendarEvent, error) {
	scope, err := s.scope(ctx, p)
	if err != nil {
		return nil, err
	}
	if s.handler == nil || s.repo == nil {
		return nil, managedtracking.ErrAuthority
	}
	filter := catalog.AccessFilter{UserID: scope.UserID, ProfileID: scope.ProfileID, AllowedLibraryIDs: scope.AllowedLibraryIDs, DisabledLibraryIDs: scope.DisabledLibraryIDs, MaturityLimits: scope.MaturityLimits, MaxPlaybackQuality: scope.MaxPlaybackQuality}
	view, err := s.handler.Calendar(ctx, handlers.CalendarQuery{Start: start, End: end, Filter: "following", Location: time.UTC}, filter)
	if err != nil {
		return nil, err
	}
	followedIDs, err := s.repo.ListFollowedItemIDs(ctx, scope.UserID, scope.ProfileID)
	if err != nil {
		return nil, err
	}
	favoriteIDs, err := s.repo.ListFavoriteItemIDs(ctx, scope.UserID, scope.ProfileID)
	if err != nil {
		return nil, err
	}
	watchlistIDs, err := s.repo.ListWatchlistItemIDs(ctx, scope.UserID, scope.ProfileID)
	if err != nil {
		return nil, err
	}
	followed, favorite, watchlist := calendarIDSet(followedIDs), calendarIDSet(favoriteIDs), calendarIDSet(watchlistIDs)
	out := []*calendar.CalendarEvent{}
	for _, day := range view.Events {
		for _, event := range day.Items {
			series := optionalString(event.SeriesID)
			//nolint:misspell // The generated wire field retains the approved protocol spelling.
			e := &calendar.CalendarEvent{EventId: event.ContentID + ":" + event.Type + ":" + event.AirDate, ContentId: event.ContentID, Kind: event.Type, Title: event.Title, EpisodeTitle: optionalString(event.EpisodeTitle), SeriesId: series, SeasonNumber: optionalNumber(event.SeasonNumber), EpisodeNumber: optionalNumber(event.EpisodeNumber), Date: event.AirDate, AirTimezone: optionalString(event.AirTimezone), Watched: event.Watched, Followed: followed[event.ContentID] || followed[series], Favourite: favorite[event.ContentID] || favorite[series], Watchlist: watchlist[event.ContentID] || watchlist[series], Badges: event.Badges}
			if event.AirAt != nil {
				at, err := time.Parse(time.RFC3339, *event.AirAt)
				if err != nil {
					return nil, err
				}
				e.AirAt = timestamppb.New(at)
			}
			out = append(out, e)
		}
	}
	if len(out) > 10000 {
		return nil, managedtracking.ErrSnapshot
	}
	if s.pool != nil && len(out) > 0 {
		ids := make([]string, 0, len(out)*2)
		for _, e := range out {
			ids = append(ids, e.ContentId)
			if e.SeriesId != "" {
				ids = append(ids, e.SeriesId)
			}
		}
		rows, err := s.pool.Query(ctx, `SELECT content_id,COALESCE(tmdb_id,''),COALESCE(imdb_id,''),COALESCE(tvdb_id,''),COALESCE(poster_path,'') FROM media_items WHERE content_id=ANY($1)`, ids)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		identifiers := map[string]map[string]string{}
		artwork := map[string]string{}
		for rows.Next() {
			var id, tmdb, imdb, tvdb, poster string
			if err = rows.Scan(&id, &tmdb, &imdb, &tvdb, &poster); err != nil {
				return nil, err
			}
			m := map[string]string{}
			if tmdb != "" {
				m["tmdb"] = tmdb
			}
			if imdb != "" {
				m["imdb"] = imdb
			}
			if tvdb != "" {
				m["tvdb"] = tvdb
			}
			identifiers[id] = m
			artwork[id] = poster
		}
		if err = rows.Err(); err != nil {
			return nil, err
		}
		for _, e := range out {
			e.ExternalIds = identifiers[e.ContentId]
			e.SeriesExternalIds = identifiers[e.SeriesId]
			e.ArtworkId = managedCalendarArtworkID(artwork[e.ContentId])
			if e.ArtworkId == "" {
				e.ArtworkId = managedCalendarArtworkID(artwork[e.SeriesId])
			}
		}
	}
	return out, nil
}
