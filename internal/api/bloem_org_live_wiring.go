package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/config"
	evt "github.com/Silo-Server/silo-server/internal/events"
	"github.com/Silo-Server/silo-server/internal/orglive"
)

// newBloemOrganizationLive builds the organization live ticket route and
// socket (docs/design/bloem-org-live.md) and the loops that feed it: one
// Postgres LISTEN connection for people and activity, and one reader of the
// playback lifecycle for streams. The loops start on first use. It returns nil, and the routes stay
// unmounted and the capability unadvertised, without a database, a context to
// bound the loops, or the administrative context middleware to revalidate with.
func newBloemOrganizationLive(deps Dependencies, adminMW *apimw.AdminContextMiddleware, admin *handlers.AdminHandler) *handlers.BloemOrganizationLive {
	if deps.DB == nil || adminMW == nil || deps.AppContext == nil {
		return nil
	}
	bus := orglive.NewBus()
	channels := []orglive.Channel{orglive.ChannelPeople, orglive.ChannelActivity}

	var sessions handlers.SupportStreamSessions
	if admin != nil && admin.AdminPlaybackSessionsAvailable() && deps.EventsHub != nil {
		sessions = admin
		channels = []orglive.Channel{orglive.ChannelStreams, orglive.ChannelPeople, orglive.ChannelActivity}
	}

	logger := slog.Default()
	listener := orglive.NewListener(bus, deps.DB.Config().ConnConfig, logger)
	var bridge *orglive.StreamsBridge
	if sessions != nil {
		bridge = orglive.NewStreamsBridge(deps.EventsHub, bus, orgLiveStreamOwners{pool: deps.DB}, orgLiveStreamFingerprints{sessions: sessions}, logger)
	}

	live := handlers.NewBloemOrganizationLive(bus, evt.NewSocketTicketStore(deps.RedisClient, handlers.SocketAudienceOrgLive), adminMW, channels, deps.PublicURL)
	// The loops start on first use, not with the router: a node nobody opens an
	// organization page on never holds the LISTEN connection, and a router built
	// for a test never opens one. Both stop with the application context.
	live.Start = func() {
		listener.Start(deps.AppContext)
		if bridge != nil {
			bridge.Start(deps.AppContext)
		}
	}
	if deps.OnConfigChange != nil {
		deps.OnConfigChange(func(_, updated *config.Config) { live.SetPublicOrigin(updated.Server.PublicURL) })
	}
	if overlay := deps.overlayOrigins(); overlay != nil {
		live.SetOverlayOrigins(overlay)
	}
	return live
}

// orgLiveStreamOwners maps a live session to the organization whose stream
// list holds it, by the filter ReadAdminPlaybackSessions applies in SQL
// (PlaybackSessionsQuery.OrganizationID): the account is a member, and the
// session's profile, if it names one, belongs to that organization.
type orgLiveStreamOwners struct{ pool *pgxpool.Pool }

func (o orgLiveStreamOwners) SessionOrganization(ctx context.Context, accountID int, profileID string) (uuid.UUID, error) {
	var organization uuid.UUID
	err := o.pool.QueryRow(ctx, `SELECT om.organization_id
FROM organization_memberships om
WHERE om.account_id = $1
  AND (COALESCE($2::text, '') = '' OR EXISTS (
        SELECT 1 FROM user_profiles up
        WHERE up.user_id = om.account_id AND up.id = $2::text AND up.organization_id = om.organization_id))
LIMIT 1`, accountID, profileID).Scan(&organization)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, nil
	}
	return organization, err
}

// orgLiveStreamFingerprints summarizes an organization's stream set from the
// same read the REST stream list uses, so a difference is a difference the
// administrator would see.
type orgLiveStreamFingerprints struct {
	sessions handlers.SupportStreamSessions
}

// orgLiveFingerprintLimit bounds the sessions one comparison reads.
const orgLiveFingerprintLimit = 1000

func (f orgLiveStreamFingerprints) StreamsFingerprint(ctx context.Context, organization uuid.UUID) (string, error) {
	rows, err := f.sessions.ReadAdminPlaybackSessions(ctx, handlers.PlaybackSessionsQuery{OrganizationID: organization}, "", orgLiveFingerprintLimit)
	if err != nil {
		return "", err
	}
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, row.SessionID+":"+strconv.FormatBool(row.IsPaused))
	}
	sort.Strings(lines)
	lines = slices.Compact(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:]), nil
}
