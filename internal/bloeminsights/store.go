package bloeminsights

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/bloemgeo"
)

// Store keeps stream records and limit refusals.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns a store on pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// recordColumns is one stream record as a lifecycle event describes it.
type recordColumns struct {
	userID                              int
	profileID                           string
	startedAt                           time.Time
	endedAt                             *time.Time
	clientName, clientVersion, platform string
	playMethod, effectiveMethod         string
	videoDecision, audioDecision        string
	adaptationReasons                   []string
	nodeID                              *int
	nodeName                            string
}

func strValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func nodeIDValue(p *string) *int {
	if p == nil {
		return nil
	}
	id, err := strconv.Atoi(*p)
	if err != nil || id <= 0 {
		return nil
	}
	return &id
}

func columnsFromEvent(ev LifecycleEvent) (recordColumns, bool) {
	switch {
	case ev.Session != nil:
		s := ev.Session
		userID, err := strconv.Atoi(s.UserID)
		if err != nil || userID <= 0 {
			return recordColumns{}, false
		}
		c := recordColumns{userID: userID, profileID: s.ProfileID, startedAt: s.StartedAt,
			clientName: s.ClientName, clientVersion: s.ClientVersion, platform: s.DevicePlatform,
			playMethod: s.PlayMethod, effectiveMethod: s.EffectivePlayMethod,
			videoDecision: s.VideoDecision, audioDecision: s.AudioDecision, adaptationReasons: s.AdaptationReasons}
		if c.nodeID, c.nodeName = nodeIDValue(s.RoutingExecutionNodeID), s.RoutingExecutionNodeName; c.nodeID == nil && c.nodeName == "" {
			c.nodeID, c.nodeName = nodeIDValue(s.RoutingEgressNodeID), s.RoutingEgressNodeName
		}
		if c.startedAt.IsZero() {
			c.startedAt = ev.At
		}
		if ev.State == "stopped" {
			at := ev.At
			c.endedAt = &at
		}
		return c, true
	case ev.History != nil:
		h := ev.History
		userID, err := strconv.Atoi(h.UserID)
		if err != nil || userID <= 0 {
			return recordColumns{}, false
		}
		ended := h.EndedAt
		if ended.IsZero() {
			ended = ev.At
		}
		c := recordColumns{userID: userID, profileID: h.ProfileID, startedAt: h.StartedAt, endedAt: &ended,
			clientName: strValue(h.ClientName), clientVersion: strValue(h.ClientVersion), platform: strValue(h.DevicePlatform),
			playMethod: h.PlayMethod, videoDecision: strValue(h.VideoDecision), audioDecision: strValue(h.AudioDecision)}
		if c.startedAt.IsZero() {
			c.startedAt = ended
		}
		return c, true
	}
	return recordColumns{}, false
}

// transportColumns are the connection columns a facts summary gives. A
// live stream shows its latest request's class; a stopped one the class
// that carried most of its requests.
type transportColumns struct {
	class                    Transport
	viewerProto, serverProto string
	changed                  bool
	edgeName, cfColo         string
	edgeLat, edgeLon         *float64
}

func transportFromFacts(f *FactsSummary, stopped bool) transportColumns {
	t := transportColumns{class: ClassifyViewerLeg(f.Latest), serverProto: f.Latest.Proto, viewerProto: f.Latest.Proto,
		changed: f.Changed, cfColo: f.Latest.CFColo}
	if stopped {
		t.class = f.Majority()
	}
	if edge := f.Latest.Edge; edge != nil {
		t.edgeName = edge.Name
		lat, lon := edge.Latitude, edge.Longitude
		t.edgeLat, t.edgeLon = &lat, &lon
		if edge.ViewerProto != "" {
			t.viewerProto = edge.ViewerProto
		}
	}
	return t
}

// ApplyLifecycle writes one lifecycle event to its stream record. An event
// whose seq is not newer than the record's is ignored, so a repeated or
// late event never overwrites a newer one. geo (nil when the address has no
// location) and facts (nil when no media request was seen) fill the viewer
// and transport columns; nil leaves what the record already holds. The
// viewer's address itself is never stored.
func (s *Store) ApplyLifecycle(ctx context.Context, ev LifecycleEvent, geo *bloemgeo.Location, facts *FactsSummary) error {
	if s == nil || s.pool == nil || ev.SessionID == "" {
		return nil
	}
	at := ev.At
	if at.IsZero() {
		at = time.Now()
	}
	c, ok := columnsFromEvent(ev)
	if !ok {
		if ev.State != "stopped" {
			return nil
		}
		_, err := s.pool.Exec(ctx, `UPDATE bloem_stream_records SET ended_at = $2, updated_at = $2, last_seq = $3
			WHERE session_id = $1 AND last_seq < $3`, ev.SessionID, at, ev.Seq)
		return err
	}
	var city, country, countryCode string
	var viewerLat, viewerLon *float64
	var approx bool
	if geo != nil {
		city, country, countryCode, approx = geo.City, geo.Country, geo.CountryCode, geo.Approximate
		lat, lon := geo.Latitude, geo.Longitude
		viewerLat, viewerLon = &lat, &lon
	}
	transport := transportColumns{class: TransportUnknown}
	if facts != nil {
		transport = transportFromFacts(facts, c.endedAt != nil)
	}
	reasons := c.adaptationReasons
	if reasons == nil {
		reasons = []string{}
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO bloem_stream_records AS r (
			session_id, user_id, profile_id, started_at, ended_at, updated_at, last_seq,
			client_name, client_version, device_platform, play_method, effective_play_method,
			video_decision, audio_decision, adaptation_reasons, node_id, node_name,
			viewer_city, viewer_country, viewer_country_code, viewer_lat, viewer_lon, viewer_approx,
			cf_colo, edge_name, edge_lat, edge_lon, viewer_transport, viewer_proto, server_proto, transport_changed)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17,
			$18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29, $30, $31)
		ON CONFLICT (session_id) DO UPDATE SET
			user_id = EXCLUDED.user_id,
			profile_id = COALESCE(NULLIF(EXCLUDED.profile_id, ''), r.profile_id),
			ended_at = COALESCE(EXCLUDED.ended_at, r.ended_at),
			updated_at = EXCLUDED.updated_at,
			last_seq = EXCLUDED.last_seq,
			client_name = COALESCE(NULLIF(EXCLUDED.client_name, ''), r.client_name),
			client_version = COALESCE(NULLIF(EXCLUDED.client_version, ''), r.client_version),
			device_platform = COALESCE(NULLIF(EXCLUDED.device_platform, ''), r.device_platform),
			play_method = COALESCE(NULLIF(EXCLUDED.play_method, ''), r.play_method),
			effective_play_method = COALESCE(NULLIF(EXCLUDED.effective_play_method, ''), r.effective_play_method),
			video_decision = COALESCE(NULLIF(EXCLUDED.video_decision, ''), r.video_decision),
			audio_decision = COALESCE(NULLIF(EXCLUDED.audio_decision, ''), r.audio_decision),
			adaptation_reasons = CASE WHEN cardinality(EXCLUDED.adaptation_reasons) > 0 THEN EXCLUDED.adaptation_reasons ELSE r.adaptation_reasons END,
			node_id = COALESCE(EXCLUDED.node_id, r.node_id),
			node_name = COALESCE(NULLIF(EXCLUDED.node_name, ''), r.node_name),
			viewer_city = CASE WHEN $32 THEN EXCLUDED.viewer_city ELSE r.viewer_city END,
			viewer_country = CASE WHEN $32 THEN EXCLUDED.viewer_country ELSE r.viewer_country END,
			viewer_country_code = CASE WHEN $32 THEN EXCLUDED.viewer_country_code ELSE r.viewer_country_code END,
			viewer_lat = CASE WHEN $32 THEN EXCLUDED.viewer_lat ELSE r.viewer_lat END,
			viewer_lon = CASE WHEN $32 THEN EXCLUDED.viewer_lon ELSE r.viewer_lon END,
			viewer_approx = CASE WHEN $32 THEN EXCLUDED.viewer_approx ELSE r.viewer_approx END,
			cf_colo = CASE WHEN $33 THEN EXCLUDED.cf_colo ELSE r.cf_colo END,
			edge_name = CASE WHEN $33 THEN EXCLUDED.edge_name ELSE r.edge_name END,
			edge_lat = CASE WHEN $33 THEN EXCLUDED.edge_lat ELSE r.edge_lat END,
			edge_lon = CASE WHEN $33 THEN EXCLUDED.edge_lon ELSE r.edge_lon END,
			viewer_transport = CASE WHEN $33 THEN EXCLUDED.viewer_transport ELSE r.viewer_transport END,
			viewer_proto = CASE WHEN $33 THEN EXCLUDED.viewer_proto ELSE r.viewer_proto END,
			server_proto = CASE WHEN $33 THEN EXCLUDED.server_proto ELSE r.server_proto END,
			transport_changed = CASE WHEN $33 THEN EXCLUDED.transport_changed ELSE r.transport_changed END
		WHERE r.last_seq < EXCLUDED.last_seq`,
		ev.SessionID, c.userID, c.profileID, c.startedAt, c.endedAt, at, ev.Seq,
		c.clientName, c.clientVersion, c.platform, c.playMethod, c.effectiveMethod,
		c.videoDecision, c.audioDecision, reasons, c.nodeID, c.nodeName,
		city, country, countryCode, viewerLat, viewerLon, approx,
		transport.cfColo, transport.edgeName, transport.edgeLat, transport.edgeLon, string(transport.class),
		transport.viewerProto, transport.serverProto, transport.changed,
		geo != nil, facts != nil)
	if err != nil {
		return fmt.Errorf("apply lifecycle event for %s: %w", ev.SessionID, err)
	}
	return nil
}

// Prune deletes stream records that ended before cutoff, live records with
// no event since cutoff (their stop was missed), and refusals before cutoff.
// It returns how many rows it deleted.
func (s *Store) Prune(ctx context.Context, cutoff time.Time) (int64, error) {
	if s == nil || s.pool == nil {
		return 0, errors.New("stream insights store has no database")
	}
	records, err := s.pool.Exec(ctx, `DELETE FROM bloem_stream_records
		WHERE ended_at < $1 OR (ended_at IS NULL AND updated_at < $1)`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("prune stream records: %w", err)
	}
	refusals, err := s.pool.Exec(ctx, `DELETE FROM bloem_limit_refusals WHERE refused_at < $1`, cutoff)
	if err != nil {
		return records.RowsAffected(), fmt.Errorf("prune limit refusals: %w", err)
	}
	return records.RowsAffected() + refusals.RowsAffected(), nil
}
