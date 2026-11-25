package bloeminsights

import (
	"cmp"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/playback"
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

// SiloHistory is what the viewing stats borrow from Silo's playback Insights
// (internal/api/handlers, admin_insights_plays.go), so that an organization
// administrator's numbers are the ones a platform administrator reads in
// Silo's Insights for the same people. The handlers package, which owns that
// SQL, hands it in.
type SiloHistory struct {
	// Viewings returns Silo's viewing grouping of admin_playback_history for
	// the sessions that match scopeCond (on alias h) around [from, to): a WITH
	// clause ending in the CTE vr (with vr.user_id, vr.started_at and
	// vr.watch_sec), the predicate that keeps vr's sessions started in the
	// window, and the predicate true on the one session of a viewing that
	// counts as a play. Its parameters are appended to args.
	Viewings func(scopeCond string, from, to time.Time, args *[]any) (with, counted, isPlay string)
	// Direct and Transcode are Silo's play-method predicates over a
	// play_method column.
	Direct, Transcode func(column string) string
}

// clientKeySQL and clientNameSQL group and name a client as Silo's Devices
// tab does: names that differ only in case and spacing are one client.
func clientKeySQL(column string) string {
	return "LOWER(COALESCE(NULLIF(BTRIM(" + column + "), ''), 'unknown'))"
}

func clientNameSQL(column string) string {
	return "COALESCE(MIN(NULLIF(BTRIM(" + column + "), '')), 'Unknown')"
}

// TopPerson is how much one person watched.
type TopPerson struct {
	UserID         int     `json:"user_id"`
	Username       string  `json:"username"`
	WatchedSeconds float64 `json:"watched_seconds"`
	Plays          int64   `json:"plays"`
}

// TopPeople ranks the scope's people by watch time over the last days, as
// Silo's Insights counts it: watch time sums the sessions that started in the
// window, and plays are Silo's viewings.
func (s *Store) TopPeople(ctx context.Context, scope Scope, days int, now time.Time, silo SiloHistory) ([]TopPerson, error) {
	args := []any{scope.arg()}
	with, counted, isPlay := silo.Viewings(memberOf("h.user_id", 1), now.AddDate(0, 0, -days), now, &args)
	rows, err := s.pool.Query(ctx, with+`
		SELECT vr.user_id, COALESCE(u.username, ''), COALESCE(sum(vr.watch_sec), 0)::float8, count(*) FILTER (WHERE `+isPlay+`)
		FROM vr LEFT JOIN users u ON u.id = vr.user_id
		WHERE `+counted+`
		GROUP BY vr.user_id, u.username ORDER BY 3 DESC, vr.user_id LIMIT 50`, args...)
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

// Heatmap counts the scope's plays of the last days by the local weekday (0 is
// Monday, as in Silo's Insights) and hour they started in tz.
func (s *Store) Heatmap(ctx context.Context, scope Scope, days int, now time.Time, tz *time.Location, silo SiloHistory) ([7][24]int64, error) {
	var out [7][24]int64
	if tz == nil {
		tz = time.UTC
	}
	args := []any{scope.arg()}
	with, counted, isPlay := silo.Viewings(memberOf("h.user_id", 1), now.AddDate(0, 0, -days), now, &args)
	args = append(args, tz.String())
	zone := "$" + strconv.Itoa(len(args))
	rows, err := s.pool.Query(ctx, with+`
		SELECT (EXTRACT(ISODOW FROM (vr.started_at AT TIME ZONE `+zone+`))::int - 1), EXTRACT(HOUR FROM (vr.started_at AT TIME ZONE `+zone+`))::int, count(*)
		FROM vr WHERE `+counted+` AND `+isPlay+`
		GROUP BY 1, 2`, args...)
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

// ClientStat is one client's sessions and how many of them were transcoded.
type ClientStat struct {
	ClientName        string `json:"client_name"`
	DevicePlatform    string `json:"device_platform"`
	Sessions          int64  `json:"sessions"`
	TranscodeSessions int64  `json:"transcode_sessions"`
}

// Clients counts the scope's sessions that started in the last days per
// client and platform, as Silo's Devices tab does (per session, names merged
// regardless of case and spacing).
func (s *Store) Clients(ctx context.Context, scope Scope, days int, now time.Time, silo SiloHistory) ([]ClientStat, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+clientNameSQL("h.client_name")+`, `+clientNameSQL("h.device_platform")+`, count(*),
			count(*) FILTER (WHERE `+silo.Transcode("h.play_method")+`)
		FROM admin_playback_history h
		WHERE h.started_at >= $2 AND h.started_at < $3 AND `+memberOf("h.user_id", 1)+`
		GROUP BY `+clientKeySQL("h.client_name")+`, `+clientKeySQL("h.device_platform")+`
		ORDER BY 3 DESC, 1, 2 LIMIT 100`, scope.arg(), now.AddDate(0, 0, -days), now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ClientStat{}
	for rows.Next() {
		var c ClientStat
		if err := rows.Scan(&c.ClientName, &c.DevicePlatform, &c.Sessions, &c.TranscodeSessions); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// TranscodeCause is how many of a client's sessions were adapted for one reason.
type TranscodeCause struct {
	ClientName string `json:"client_name"`
	Reason     string `json:"reason"`
	Sessions   int64  `json:"sessions"`
}

// TranscodeCauses counts why the scope's sessions of the last days were not
// played directly, per client and adaptation reason, from the reasons Silo's
// playback history records (as Silo's Devices tab does).
func (s *Store) TranscodeCauses(ctx context.Context, scope Scope, days int, now time.Time, silo SiloHistory) ([]TranscodeCause, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+clientNameSQL("h.client_name")+`, reason, count(*)
		FROM admin_playback_history h CROSS JOIN LATERAL unnest(h.adaptation_reasons) AS reason
		WHERE h.started_at >= $2 AND h.started_at < $3 AND `+memberOf("h.user_id", 1)+`
			AND NOT (`+silo.Direct("h.play_method")+`)
		GROUP BY `+clientKeySQL("h.client_name")+`, reason`, scope.arg(), now.AddDate(0, 0, -days), now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type key struct{ client, reason string }
	counts := map[key]int64{}
	for rows.Next() {
		var client, reason string
		var n int64
		if err := rows.Scan(&client, &reason, &n); err != nil {
			return nil, err
		}
		if norm := playback.NormalizeAdaptationReasons([]string{reason}); len(norm) > 0 {
			counts[key{client, norm[0]}] += n
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]TranscodeCause, 0, len(counts))
	for k, n := range counts {
		out = append(out, TranscodeCause{ClientName: k.client, Reason: k.reason, Sessions: n})
	}
	slices.SortFunc(out, func(a, b TranscodeCause) int {
		if c := cmp.Compare(b.Sessions, a.Sessions); c != 0 {
			return c
		}
		if c := cmp.Compare(a.ClientName, b.ClientName); c != 0 {
			return c
		}
		return cmp.Compare(a.Reason, b.Reason)
	})
	if len(out) > 100 {
		out = out[:100]
	}
	return out, nil
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
