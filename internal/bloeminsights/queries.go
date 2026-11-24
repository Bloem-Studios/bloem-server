package bloeminsights

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Window is the period a route covers: the live streams, or those started in
// the last 24 hours or 7 days.
type Window string

const (
	WindowLive Window = "live"
	Window24h  Window = "24h"
	Window7d   Window = "7d"
)

// ErrInvalidQuery refuses a window, day count or cursor out of range.
var ErrInvalidQuery = errors.New("invalid stream insights query")

// ParseWindow reads a window parameter; empty is live.
func ParseWindow(raw string) (Window, error) {
	switch Window(strings.TrimSpace(raw)) {
	case "", WindowLive:
		return WindowLive, nil
	case Window24h:
		return Window24h, nil
	case Window7d:
		return Window7d, nil
	}
	return "", fmt.Errorf("%w: window must be live, 24h or 7d", ErrInvalidQuery)
}

// MaxDays is how far back the stats routes reach: the retention.
const MaxDays = 90

// ParseDays reads a days parameter, 1 to MaxDays; empty is def.
func ParseDays(raw string, def int) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def, nil
	}
	days, err := strconv.Atoi(raw)
	if err != nil || days < 1 || days > MaxDays {
		return 0, fmt.Errorf("%w: days must be between 1 and %d", ErrInvalidQuery, MaxDays)
	}
	return days, nil
}

// liveStaleAfter is how long a record with no stop stays live: a stop the
// observer missed leaves the record open until it is this old.
const liveStaleAfter = 24 * time.Hour

// Scope limits a query to one organization's members; the zero value is the
// whole platform.
type Scope struct {
	OrganizationID uuid.UUID
}

func (s Scope) arg() any {
	if s.OrganizationID == uuid.Nil {
		return nil
	}
	return s.OrganizationID
}

// memberOf is the organization filter on a user id column, with the scope
// in parameter n: the account holds an active membership of the
// organization, as the organization people routes count members.
func memberOf(column string, n int) string {
	p := "$" + strconv.Itoa(n)
	return `(` + p + `::uuid IS NULL OR EXISTS (SELECT 1 FROM organization_memberships m
		WHERE m.account_id = ` + column + ` AND m.organization_id = ` + p + `::uuid AND m.status = 'active'))`
}

func windowCondition(w Window, now time.Time) (string, time.Time) {
	switch w {
	case Window24h:
		return `started_at >= $1`, seriesOrigin(w, now)
	case Window7d:
		return `started_at >= $1`, seriesOrigin(w, now)
	}
	return `ended_at IS NULL AND updated_at >= $1`, now.Add(-liveStaleAfter)
}

// Trail is one stream's route.
type Trail struct {
	SessionID string    `json:"session_id"`
	Live      bool      `json:"live"`
	Transport Transport `json:"transport"`
	Hops      []Hop     `json:"hops"`
}

// Trails lists the streams of the window, newest first, each with the hops
// that have coordinates: viewer, Cloudflare colo, edge, server, node. It
// returns at most limit and reports whether more were left out.
func (s *Store) Trails(ctx context.Context, w Window, now time.Time, positions Positions, limit int) ([]Trail, bool, error) {
	condition, since := windowCondition(w, now)
	rows, err := s.pool.Query(ctx, `SELECT session_id, ended_at IS NULL, viewer_transport,
			viewer_lat, viewer_lon, viewer_approx, viewer_city, viewer_country, cf_colo, edge_name, edge_lat, edge_lon, node_id
		FROM bloem_stream_records WHERE `+condition+` ORDER BY started_at DESC, session_id LIMIT $2`, since, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var out []Trail
	for rows.Next() {
		var t Trail
		var transport, city, country, colo, edge string
		var viewerLat, viewerLon, edgeLat, edgeLon *float64
		var approx bool
		var nodeID *int
		if err := rows.Scan(&t.SessionID, &t.Live, &transport, &viewerLat, &viewerLon, &approx, &city, &country,
			&colo, &edge, &edgeLat, &edgeLon, &nodeID); err != nil {
			return nil, false, err
		}
		t.Transport = Transport(transport)
		t.Hops = []Hop{}
		if viewerLat != nil && viewerLon != nil {
			label := city
			if label == "" {
				label = country
			}
			t.Hops = append(t.Hops, Hop{Kind: HopViewer, Label: label, Latitude: *viewerLat, Longitude: *viewerLon, Approximate: approx})
		}
		if colo != "" {
			if coloCity, lat, lon, ok := ColoLocation(colo); ok {
				t.Hops = append(t.Hops, Hop{Kind: HopCloudflare, Label: coloCity + " (" + colo + ")", Latitude: lat, Longitude: lon})
			}
		}
		if edgeLat != nil && edgeLon != nil {
			t.Hops = append(t.Hops, Hop{Kind: HopEdge, Label: edge, Latitude: *edgeLat, Longitude: *edgeLon})
		}
		if positions.Server != nil {
			t.Hops = append(t.Hops, *positions.Server)
		}
		if nodeID != nil {
			if node, ok := positions.Nodes[*nodeID]; ok {
				t.Hops = append(t.Hops, node)
			}
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if len(out) > limit {
		return out[:limit], true, nil
	}
	return out, false, nil
}

// TransportCounts counts streams per transport class.
type TransportCounts struct {
	UDP        int64 `json:"udp"`
	TCP        int64 `json:"tcp"`
	Cloudflare int64 `json:"cloudflare"`
	Unknown    int64 `json:"unknown"`
}

func (c *TransportCounts) add(t Transport) {
	switch t {
	case TransportUDP:
		c.UDP++
	case TransportTCP:
		c.TCP++
	case TransportCloudflare:
		c.Cloudflare++
	default:
		c.Unknown++
	}
}

// TransportBucket is the streams started in one bucket, per class.
type TransportBucket struct {
	At time.Time `json:"at"`
	TransportCounts
}

// TransportSummary is how a window's streams were carried.
type TransportSummary struct {
	Counts map[Transport]int64 `json:"-"`
	Totals TransportCounts     `json:"counts"`
	Series []TransportBucket   `json:"series"`
}

func seriesStep(w Window) (time.Duration, int) {
	if w == Window7d {
		return 6 * time.Hour, 28
	}
	return time.Hour, 24
}

// seriesOrigin is the start of a window's first bucket: whole hours for
// 24h, whole six-hour blocks (UTC) for 7d.
func seriesOrigin(w Window, now time.Time) time.Time {
	step, buckets := seriesStep(w)
	return now.UTC().Truncate(step).Add(-time.Duration(buckets-1) * step)
}

// Transport counts the window's streams per class: the live streams with
// their current class, or those started in 24h or 7d, with a series of
// hourly or six-hourly buckets.
func (s *Store) Transport(ctx context.Context, w Window, now time.Time) (TransportSummary, error) {
	condition, since := windowCondition(w, now)
	rows, err := s.pool.Query(ctx, `SELECT started_at, viewer_transport FROM bloem_stream_records WHERE `+condition, since)
	if err != nil {
		return TransportSummary{}, err
	}
	defer rows.Close()
	out := TransportSummary{Counts: map[Transport]int64{}, Series: []TransportBucket{}}
	var step time.Duration
	var origin time.Time
	if w != WindowLive {
		var buckets int
		step, buckets = seriesStep(w)
		origin = seriesOrigin(w, now)
		for i := range buckets {
			out.Series = append(out.Series, TransportBucket{At: origin.Add(time.Duration(i) * step)})
		}
	}
	for rows.Next() {
		var started time.Time
		var class string
		if err := rows.Scan(&started, &class); err != nil {
			return TransportSummary{}, err
		}
		t := Transport(class)
		out.Counts[t]++
		out.Totals.add(t)
		if w != WindowLive {
			if i := int(started.Sub(origin) / step); i >= 0 && i < len(out.Series) {
				out.Series[i].add(t)
			}
		}
	}
	return out, rows.Err()
}

// Refusal is one refusal at a limit.
type Refusal struct {
	UserID    int       `json:"user_id"`
	Username  string    `json:"username"`
	Kind      string    `json:"kind"`
	Scope     string    `json:"scope"`
	RefusedAt time.Time `json:"refused_at"`
}

func refusalCursor(at time.Time, id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(at.UnixNano(), 10) + ":" + strconv.FormatInt(id, 10)))
}

func parseRefusalCursor(raw string) (time.Time, int64, error) {
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("%w: cursor", ErrInvalidQuery)
	}
	at, id, ok := strings.Cut(string(data), ":")
	nanos, err1 := strconv.ParseInt(at, 10, 64)
	rowID, err2 := strconv.ParseInt(id, 10, 64)
	if !ok || err1 != nil || err2 != nil {
		return time.Time{}, 0, fmt.Errorf("%w: cursor", ErrInvalidQuery)
	}
	return time.Unix(0, nanos), rowID, nil
}

// Refusals pages the scope's refusals of the last days, newest first. The
// returned cursor fetches the next page; empty means there is none.
func (s *Store) Refusals(ctx context.Context, scope Scope, days int, now time.Time, cursor string, limit int) ([]Refusal, string, error) {
	var afterAt any
	var afterID any
	if cursor != "" {
		at, id, err := parseRefusalCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		afterAt, afterID = at, id
	}
	rows, err := s.pool.Query(ctx, `SELECT r.id, r.user_id, COALESCE(u.username, ''), r.kind, r.scope, r.refused_at
		FROM bloem_limit_refusals r LEFT JOIN users u ON u.id = r.user_id
		WHERE r.refused_at >= $1 AND `+memberOf("r.user_id", 2)+`
			AND ($3::timestamptz IS NULL OR (r.refused_at, r.id) < ($3::timestamptz, $4::bigint))
		ORDER BY r.refused_at DESC, r.id DESC LIMIT $5`,
		now.AddDate(0, 0, -days), scope.arg(), afterAt, afterID, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []Refusal{}
	var ids []int64
	for rows.Next() {
		var r Refusal
		var id int64
		if err := rows.Scan(&id, &r.UserID, &r.Username, &r.Kind, &r.Scope, &r.RefusedAt); err != nil {
			return nil, "", err
		}
		out = append(out, r)
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	if len(out) > limit {
		last := limit - 1
		return out[:limit], refusalCursor(out[last].RefusedAt, ids[last]), nil
	}
	return out, "", nil
}

// TopPerson is how much one person watched.
type TopPerson struct {
	UserID         int     `json:"user_id"`
	Username       string  `json:"username"`
	WatchedSeconds float64 `json:"watched_seconds"`
	Plays          int64   `json:"plays"`
}

// TopPeople ranks the scope's people by watch time over the last days, from
// Silo's playback history (read only). watchTimeSQL is the history's watch
// time expression on alias h, as the admin watch summary computes it.
func (s *Store) TopPeople(ctx context.Context, scope Scope, days int, now time.Time, watchTimeSQL string) ([]TopPerson, error) {
	rows, err := s.pool.Query(ctx, `SELECT h.user_id, COALESCE(u.username, ''), COALESCE(sum(`+watchTimeSQL+`), 0)::float8, count(*)
		FROM admin_playback_history h LEFT JOIN users u ON u.id = h.user_id
		WHERE h.ended_at >= $1 AND `+memberOf("h.user_id", 2)+`
		GROUP BY h.user_id, u.username ORDER BY 3 DESC, h.user_id LIMIT 50`, now.AddDate(0, 0, -days), scope.arg())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TopPerson{}
	for rows.Next() {
		var p TopPerson
		if err := rows.Scan(&p.UserID, &p.Username, &p.WatchedSeconds, &p.Plays); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Heatmap counts the scope's plays of the last days by the local weekday
// (0 is Sunday) and hour they started in tz.
func (s *Store) Heatmap(ctx context.Context, scope Scope, days int, now time.Time, tz *time.Location) ([7][24]int64, error) {
	var out [7][24]int64
	if tz == nil {
		tz = time.UTC
	}
	rows, err := s.pool.Query(ctx, `SELECT extract(dow FROM h.started_at AT TIME ZONE $3)::int, extract(hour FROM h.started_at AT TIME ZONE $3)::int, count(*)
		FROM admin_playback_history h
		WHERE h.started_at >= $1 AND `+memberOf("h.user_id", 2)+`
		GROUP BY 1, 2`, now.AddDate(0, 0, -days), scope.arg(), tz.String())
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var day, hour int
		var plays int64
		if err := rows.Scan(&day, &hour, &plays); err != nil {
			return out, err
		}
		if day >= 0 && day < 7 && hour >= 0 && hour < 24 {
			out[day][hour] = plays
		}
	}
	return out, rows.Err()
}

// ClientStat is one client's plays and how many of them transcoded video.
type ClientStat struct {
	ClientName     string `json:"client_name"`
	DevicePlatform string `json:"device_platform"`
	Plays          int64  `json:"plays"`
	Transcodes     int64  `json:"transcodes"`
}

// Clients counts the scope's plays of the last days per client and platform.
func (s *Store) Clients(ctx context.Context, scope Scope, days int, now time.Time) ([]ClientStat, error) {
	rows, err := s.pool.Query(ctx, `SELECT COALESCE(h.client_name, ''), COALESCE(h.device_platform, ''), count(*),
			count(*) FILTER (WHERE h.video_decision = 'transcode')
		FROM admin_playback_history h
		WHERE h.started_at >= $1 AND `+memberOf("h.user_id", 2)+`
		GROUP BY 1, 2 ORDER BY 3 DESC, 1, 2 LIMIT 100`, now.AddDate(0, 0, -days), scope.arg())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ClientStat{}
	for rows.Next() {
		var c ClientStat
		if err := rows.Scan(&c.ClientName, &c.DevicePlatform, &c.Plays, &c.Transcodes); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// TranscodeCause is how often a client transcoded video for one reason.
type TranscodeCause struct {
	ClientName string `json:"client_name"`
	Reason     string `json:"reason"`
	Count      int64  `json:"count"`
}

// TranscodeCauses counts why the scope's video transcodes of the last days
// happened, per client, from the stream records' adaptation reasons.
func (s *Store) TranscodeCauses(ctx context.Context, scope Scope, days int, now time.Time) ([]TranscodeCause, error) {
	rows, err := s.pool.Query(ctx, `SELECT r.client_name, reason, count(*)
		FROM bloem_stream_records r, unnest(r.adaptation_reasons) AS reason
		WHERE r.video_decision = 'transcode' AND r.started_at >= $1 AND `+memberOf("r.user_id", 2)+`
		GROUP BY 1, 2 ORDER BY 3 DESC, 1, 2 LIMIT 100`, now.AddDate(0, 0, -days), scope.arg())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TranscodeCause{}
	for rows.Next() {
		var c TranscodeCause
		if err := rows.Scan(&c.ClientName, &c.Reason, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// RecordView is what a stream record says about a live stream's route.
type RecordView struct {
	ViewerCity, ViewerCountry, ViewerCountryCode string
	ViewerLat, ViewerLon                         *float64
	ViewerApprox                                 bool
	CFColo, EdgeName                             string
	EdgeLat, EdgeLon                             *float64
	Transport                                    Transport
	ViewerProto, ServerProto                     string
	TransportChanged                             bool
	NodeID                                       *int
}

// Records reads the stream records of the given sessions.
func (s *Store) Records(ctx context.Context, sessionIDs []string) (map[string]RecordView, error) {
	out := map[string]RecordView{}
	if len(sessionIDs) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT session_id, viewer_city, viewer_country, viewer_country_code, viewer_lat, viewer_lon, viewer_approx,
			cf_colo, edge_name, edge_lat, edge_lon, viewer_transport, viewer_proto, server_proto, transport_changed, node_id
		FROM bloem_stream_records WHERE session_id = ANY($1)`, sessionIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, transport string
		var v RecordView
		if err := rows.Scan(&id, &v.ViewerCity, &v.ViewerCountry, &v.ViewerCountryCode, &v.ViewerLat, &v.ViewerLon, &v.ViewerApprox,
			&v.CFColo, &v.EdgeName, &v.EdgeLat, &v.EdgeLon, &transport, &v.ViewerProto, &v.ServerProto, &v.TransportChanged, &v.NodeID); err != nil {
			return nil, err
		}
		v.Transport = Transport(transport)
		out[id] = v
	}
	return out, rows.Err()
}
