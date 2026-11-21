package notifications

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// Sending admin Live Activity events (docs/architecture/bloem-admin-push.md).
//
// The privacy rule: the relay never sees a title, a name or a hostname. A content
// state and push-start attributes are built only from the typed structs below, whose
// strings are enumerated words and digit ids, and validate refuses anything else
// before a request is built. The relay refuses text by shape as a backstop.

const (
	// The phase words of a job's content state.
	LivePhaseRunning  = "running"
	LivePhaseFinished = "finished"
	LivePhaseFailed   = "failed"

	// The events of the relay's live_activity mode.
	LiveEventStart  = "start"
	LiveEventUpdate = "update"
	LiveEventEnd    = "end"

	relayModeLiveActivity = "live_activity"
	// adminJobAttributesType is the ActivityKit attributes type a push start names.
	adminJobAttributesType = "AdminJobAttributes"

	// adminLiveStaleAfter marks an activity stale two minutes after an update.
	adminLiveStaleAfter = 2 * time.Minute
	// adminLiveDismissAfter keeps an ended activity on the Lock Screen for 15 minutes.
	adminLiveDismissAfter = 15 * time.Minute
	// adminLiveModeOff is how long sending stops after the relay says it has no live mode.
	adminLiveModeOff = time.Hour
	// adminLiveRateHold is how long the relay's live rate limit holds sends without a Retry-After.
	adminLiveRateHold = time.Minute
	// adminLiveRetry is the shortest wait before resending after a transient failure.
	adminLiveRetry = 30 * time.Second

	relayCodeLiveRateLimited = "live_activity_rate_limited"
	liveBatch                = 50
	// maxUnixSeconds is the relay's ceiling for a time: whole Unix seconds below 2^32.
	maxUnixSeconds = 1<<32 - 1
)

// liveDeviceIDPattern is a device id as the server mints it (a ULID): letters and digits.
var liveDeviceIDPattern = regexp.MustCompile(`^[0-9A-Za-z]{1,64}$`)

// adminJobState is the content state of an admin job activity. Its strings are a phase
// word and a library id: never a name.
type adminJobState struct {
	Phase     string   `json:"phase"`
	Progress  *float64 `json:"progress,omitempty"`
	Processed *int     `json:"processed,omitempty"`
	Total     *int     `json:"total,omitempty"`
	LibraryID string   `json:"libraryID,omitempty"`
	UpdatedAt int64    `json:"updatedAt"`
}

// adminJobAttributes are the attributes of a push-started job activity: a kind word,
// the id the app knows this server's pushes by (its device id), and the start time.
type adminJobAttributes struct {
	Kind         string `json:"kind"`
	PushServerID string `json:"pushServerID"`
	StartedAt    int64  `json:"startedAt"`
}

func validUnix(at int64) bool { return at >= 1 && at <= maxUnixSeconds }

// validate refuses a state that could carry anything but ids, counts, a phase word and
// times: a phase outside its three words, a library id that is not digits, a fraction
// outside 0..1, a negative count, or a time that is not whole Unix seconds.
func (st adminJobState) validate() error {
	switch st.Phase {
	case LivePhaseRunning, LivePhaseFinished, LivePhaseFailed:
	default:
		return errors.New("live state: phase must be running, finished or failed")
	}
	if st.LibraryID != "" && !libraryIDPattern.MatchString(st.LibraryID) {
		return errors.New("live state: libraryID must be a library id")
	}
	if st.Progress != nil && (math.IsNaN(*st.Progress) || *st.Progress < 0 || *st.Progress > 1) {
		return errors.New("live state: progress must be a fraction from 0 to 1")
	}
	for _, count := range []*int{st.Processed, st.Total} {
		if count != nil && *count < 0 {
			return errors.New("live state: counts must not be negative")
		}
	}
	if !validUnix(st.UpdatedAt) {
		return errors.New("live state: updatedAt must be whole Unix seconds")
	}
	return nil
}

func (a adminJobAttributes) validate() error {
	if a.Kind != LiveKindScan && a.Kind != LiveKindBackup {
		return errors.New("live attributes: kind must be scan or backup")
	}
	if !liveDeviceIDPattern.MatchString(a.PushServerID) {
		return errors.New("live attributes: pushServerID must be the device id")
	}
	if !validUnix(a.StartedAt) {
		return errors.New("live attributes: startedAt must be whole Unix seconds")
	}
	return nil
}

// liveUpdate is a job's newest state for the activities that show it.
type liveUpdate struct {
	Kind      string
	SubjectID string
	Event     string
	State     adminJobState
	// CreatedBefore, when set, limits the update to activities registered before it.
	CreatedBefore time.Time
}

// adminRelayLive is the relay's live_activity request. It has no field for text.
type adminRelayLive struct {
	Token          string              `json:"token"`
	Environment    string              `json:"environment"`
	Topic          string              `json:"topic"`
	Mode           string              `json:"mode"`
	ServerDeviceID string              `json:"server_device_id"`
	DeliveryID     string              `json:"delivery_id"`
	Event          string              `json:"event"`
	Timestamp      int64               `json:"timestamp"`
	ContentState   adminJobState       `json:"content_state"`
	AttributesType string              `json:"attributes_type,omitempty"`
	Attributes     *adminJobAttributes `json:"attributes,omitempty"`
	StaleDate      int64               `json:"stale_date,omitempty"`
	DismissalDate  int64               `json:"dismissal_date,omitempty"`
	Priority       string              `json:"priority"`
}

// liveDeliveryID is what a live send carries as its delivery id: the device id the app
// knows the server by, the activity, and the send's timestamp.
func liveDeliveryID(serverDeviceID, activityID string, ts int64) string {
	return serverDeviceID + ".live-" + activityID + "-" + strconv.FormatInt(ts, 10)
}

func livePriority(high bool) string {
	if high {
		return "high"
	}
	return "low"
}

// adminLiveUpdatePayload builds an update or end for an activity's own token.
func adminLiveUpdatePayload(device *AdminPushDevice, c liveClaim, token string) (adminRelayLive, error) {
	if err := c.State.validate(); err != nil {
		return adminRelayLive{}, err
	}
	if !validUnix(c.Timestamp) {
		return adminRelayLive{}, errors.New("live request: timestamp must be whole Unix seconds")
	}
	out := adminRelayLive{
		Token: token, Environment: c.Environment, Topic: device.APNsTopic, Mode: relayModeLiveActivity,
		ServerDeviceID: device.ServerDeviceID, DeliveryID: liveDeliveryID(device.ServerDeviceID, c.ActivityID, c.Timestamp),
		Event: LiveEventUpdate, Timestamp: c.Timestamp, ContentState: c.State, Priority: livePriority(c.High),
	}
	if c.Event == LiveEventEnd {
		out.Event, out.Priority = LiveEventEnd, livePriority(true)
		out.DismissalDate = c.Timestamp + int64(adminLiveDismissAfter/time.Second)
	} else {
		out.StaleDate = c.Timestamp + int64(adminLiveStaleAfter/time.Second)
	}
	return out, nil
}

// adminLiveStartPayload builds a push start for a device's push-to-start token.
func adminLiveStartPayload(device *AdminPushDevice, token, kind string, state adminJobState, startedAt time.Time, ts int64) (string, adminRelayLive, error) {
	attributes := adminJobAttributes{Kind: kind, PushServerID: device.ServerDeviceID, StartedAt: startedAt.Unix()}
	if err := attributes.validate(); err != nil {
		return "", adminRelayLive{}, err
	}
	if err := state.validate(); err != nil {
		return "", adminRelayLive{}, err
	}
	if !validUnix(ts) {
		return "", adminRelayLive{}, errors.New("live request: timestamp must be whole Unix seconds")
	}
	return relayAppleSendPath, adminRelayLive{
		Token: token, Environment: device.APNsEnvironment, Topic: device.APNsTopic, Mode: relayModeLiveActivity,
		ServerDeviceID: device.ServerDeviceID, DeliveryID: device.ServerDeviceID + ".live-start-" + kind + "-" + strconv.FormatInt(ts, 10),
		Event: LiveEventStart, Timestamp: ts, ContentState: state, AttributesType: adminJobAttributesType, Attributes: &attributes,
		StaleDate: ts + int64(adminLiveStaleAfter/time.Second), Priority: livePriority(true),
	}, nil
}

// liveClaim is one activity's send, claimed in the database.
type liveClaim struct {
	ActivityID      string
	DeviceID        string
	Kind            string
	SubjectID       string
	Platform        string
	TokenCiphertext string
	Environment     string
	Event           string
	State           adminJobState
	PreviousPhase   string
	Timestamp       int64
	High            bool
}

// queueLive stores a job's newest state on every live activity that shows it, for the
// sender to claim: a newer state replaces one still waiting. An end marks the activity
// ended in the same statement, so nothing queued later reaches it.
func (s *AdminPushService) queueLive(ctx context.Context, u liveUpdate) error {
	if err := u.State.validate(); err != nil {
		return err
	}
	if u.Event != LiveEventUpdate && u.Event != LiveEventEnd {
		return errors.New("live update: event must be update or end")
	}
	if u.Kind == LiveKindScan && !libraryIDPattern.MatchString(u.SubjectID) {
		return errors.New("live update: a scan's subject must be a library id")
	}
	state, err := json.Marshal(u.State)
	if err != nil {
		return err
	}
	var createdBefore *time.Time
	if !u.CreatedBefore.IsZero() {
		createdBefore = &u.CreatedBefore
	}
	n, err := s.store.queueLive(ctx, u.Kind, u.SubjectID, u.Event, state, s.now(), createdBefore)
	if err != nil {
		return err
	}
	if n > 0 {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
	return nil
}

// liveModeAvailable reports whether live requests may go to the relay at now.
func (s *AdminPushService) liveModeAvailable(now time.Time) bool {
	s.liveMu.Lock()
	defer s.liveMu.Unlock()
	return !now.Before(s.liveOffUntil)
}

// liveModeMissing stops live requests for an hour from now and logs it once for that hour.
func (s *AdminPushService) liveModeMissing(ctx context.Context, code string, now time.Time) {
	s.liveMu.Lock()
	first := !now.Before(s.liveOffUntil)
	if first {
		s.liveOffUntil = now.Add(adminLiveModeOff)
	}
	s.liveMu.Unlock()
	if first {
		s.logger.WarnContext(ctx, "the push relay has no live_activity mode; admin Live Activity sends stop for an hour",
			"code", code, "retry_after", adminLiveModeOff.String())
	}
}

// relayLacksLiveMode is the answer of a relay without the live_activity mode: the
// current relay says invalid_mode, an older one unexpected_field for the first live
// field it does not know.
func relayLacksLiveMode(result pushSendResult) bool {
	return result.HTTPStatus == http.StatusBadRequest && (result.UpstreamReason == "invalid_mode" || result.UpstreamReason == "unexpected_field")
}

// liveTokenGone reports a refusal that means the activity or start token is gone. An
// activity token is cheap to lose: the app registers a new one when it has it.
func liveTokenGone(result pushSendResult, detail relayErrorDetail) bool {
	if result.HTTPStatus != http.StatusUnprocessableEntity || result.UpstreamReason != "apns_rejected" {
		return result.TerminalDevice
	}
	switch detail.TokenState {
	case "dead", "suspect":
		return true
	}
	switch detail.ProviderReason {
	case "BadDeviceToken", "Unregistered", "ExpiredToken", "DeviceTokenNotForTopic":
		return true
	}
	return result.TerminalDevice
}

// FlushLive sends the live activity states that are due and returns how many it sent
// or tried. Every replica may call it: each send is claimed once, in the database.
func (s *AdminPushService) FlushLive(ctx context.Context) int {
	if !s.LiveActivitiesEnabled(ctx) {
		return 0
	}
	now := s.now()
	claims, err := s.store.claimDueLive(ctx, now, liveBatch, s.liveModeAvailable(now))
	if err != nil {
		if ctx.Err() == nil {
			s.logger.WarnContext(ctx, "claim admin live activity sends failed", "error", err)
		}
		return 0
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 5)
	for _, claim := range claims {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			s.deliverLive(ctx, claim, now)
		}()
	}
	wg.Wait()
	return len(claims)
}

// deliverLive sends one claimed state and acts on the relay's answer.
func (s *AdminPushService) deliverLive(ctx context.Context, c liveClaim, now time.Time) {
	device, live, err := s.store.deviceByID(ctx, c.DeviceID)
	if err != nil {
		s.store.requeueLive(ctx, c, now.Add(adminLiveRetry))
		return
	}
	if device == nil {
		return
	}
	if !device.Enabled || !live {
		// The login that registered the device ended, or the account is no longer an
		// admin: its activities go.
		s.store.deleteLive(ctx, c.ActivityID)
		return
	}
	var path, key string
	var payload any
	switch c.Platform {
	case LivePlatformIOS:
		if !s.settings.ApplePushDeliveryEnabled(ctx) {
			return
		}
		token, err := s.cipher.Decrypt(c.TokenCiphertext, adminLiveTokenAAD(c.ActivityID))
		if err != nil {
			s.store.deleteLive(ctx, c.ActivityID)
			return
		}
		body, err := adminLiveUpdatePayload(device, c, token)
		if err != nil {
			s.logger.WarnContext(ctx, "admin live activity state refused", "activity_id", c.ActivityID, "error", err)
			return
		}
		path, payload = relayAppleSendPath, body
	default:
		if !s.settings.AndroidPushDeliveryEnabled(ctx) {
			return
		}
		token, err := s.cipher.Decrypt(device.TokenCiphertext, adminPushTokenAAD(device.ID))
		if err != nil {
			return
		}
		// The app treats a live delivery id as "re-read the job and redraw"; it fetches no
		// content and posts no alert, so the relay carries nothing but the id.
		path, payload = relayFcmSendPath, adminRelayFCM{
			Token: token, Mode: relayModePrivateAlert, ServerDeviceID: device.ServerDeviceID,
			DeliveryID: liveDeliveryID(device.ServerDeviceID, c.ActivityID, c.Timestamp),
		}
	}
	key = "live-" + c.ActivityID + "-" + strconv.FormatInt(c.Timestamp, 10)
	result, detail := s.relaySend(ctx, path, payload, key, device.Platform)
	switch {
	case result.OK:
	case c.Platform == LivePlatformIOS && relayLacksLiveMode(result):
		s.liveModeMissing(ctx, result.UpstreamReason, now)
		s.store.requeueLive(ctx, c, now)
	case result.HTTPStatus == http.StatusTooManyRequests && result.UpstreamReason == relayCodeLiveRateLimited:
		hold := result.RetryAfter
		if hold <= 0 {
			hold = adminLiveRateHold
		}
		s.store.requeueLive(ctx, c, now.Add(hold))
	case c.Platform == LivePlatformIOS && liveTokenGone(result, detail):
		s.store.deleteLive(ctx, c.ActivityID)
	case c.Platform == LivePlatformAndroid && result.TerminalDevice:
		s.store.recordFailure(ctx, device.ID, result.UpstreamReason, true)
		s.store.deleteLive(ctx, c.ActivityID)
	case result.HTTPStatus == 0 || retryableHTTPStatus(result.HTTPStatus):
		s.store.requeueLive(ctx, c, now.Add(max(result.RetryAfter, adminLiveRetry)))
	default:
		s.logger.WarnContext(ctx, "admin live activity send refused", "activity_id", c.ActivityID, "status", result.HTTPStatus, "code", result.UpstreamReason)
	}
}

// sendLiveStart push-starts an activity on a device through its push-to-start token, as
// of now. It reports whether the relay accepted it. A gone token is cleared with its kinds.
func (s *AdminPushService) sendLiveStart(ctx context.Context, device *AdminPushDevice, kind string, state adminJobState, startedAt, now time.Time) bool {
	if device == nil || device.Platform != PushPlatformApple || !s.liveModeAvailable(now) || !s.settings.ApplePushDeliveryEnabled(ctx) {
		return false
	}
	sealed, err := s.store.liveStartToken(ctx, device.ID)
	if err != nil || sealed == "" {
		return false
	}
	token, err := s.cipher.Decrypt(sealed, adminLiveStartTokenAAD(device.ID))
	if err != nil {
		return false
	}
	ts := now.Unix()
	path, payload, err := adminLiveStartPayload(device, token, kind, state, startedAt, ts)
	if err != nil {
		s.logger.WarnContext(ctx, "admin live activity start refused", "device_id", device.ServerDeviceID, "error", err)
		return false
	}
	sum := sha256.Sum256([]byte(kind + "\x00" + strconv.FormatInt(startedAt.Unix(), 10)))
	key := "live-start-" + device.ID + "-" + hex.EncodeToString(sum[:8])
	result, detail := s.relaySend(ctx, path, payload, key, device.Platform)
	switch {
	case result.OK:
		return true
	case relayLacksLiveMode(result):
		s.liveModeMissing(ctx, result.UpstreamReason, now)
	case liveTokenGone(result, detail):
		s.store.clearLiveStart(ctx, device.ID)
	}
	return false
}

// ---- store -----------------------------------------------------------------

// queueLive writes the newest state of a job on the live activities that show it. A
// state equal to the one the activity was last sent (updatedAt aside) is not sent: it
// clears a waiting one instead, as the activity already shows it. An end is always
// queued. An activity past its lifetime gets nothing.
func (s *adminPushStore) queueLive(ctx context.Context, kind, subject, event string, state []byte, now time.Time, createdBefore *time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE bloem_admin_live_activities SET
			pending_state = CASE WHEN $4 <> 'end' AND last_state_hash IS NOT DISTINCT FROM md5(($3::jsonb - 'updatedAt')::text) THEN NULL ELSE $3::jsonb END,
			pending_event = CASE WHEN $4 <> 'end' AND last_state_hash IS NOT DISTINCT FROM md5(($3::jsonb - 'updatedAt')::text) THEN NULL ELSE $4 END,
			ended_at = CASE WHEN $4 = 'end' THEN $5 ELSE ended_at END
		WHERE kind = $1 AND subject_id IS NOT DISTINCT FROM NULLIF($2, '') AND ended_at IS NULL AND expires_at > $5
			AND ($6::timestamptz IS NULL OR created_at <= $6)
			AND ($4 = 'end' OR pending_state IS NOT NULL OR last_state_hash IS DISTINCT FROM md5(($3::jsonb - 'updatedAt')::text))`,
		kind, subject, string(state), event, now, createdBefore)
	return tag.RowsAffected(), err
}

// claimDueLive takes the states that may go out now and stamps each with the next
// timestamp, larger than any the activity was sent, so replicas and retries never send
// out of order. A state is due once the activity's 30 second cadence has run, at once
// when it ends the activity or changes its phase, never during a relay hold, and never
// past the activity's lifetime. appleOK false leaves the iOS activities waiting.
func (s *adminPushStore) claimDueLive(ctx context.Context, now time.Time, limit int, appleOK bool) ([]liveClaim, error) {
	rows, err := s.pool.Query(ctx, `
		WITH due AS (
			SELECT activity_id, pending_state AS state, pending_event AS event, COALESCE(last_phase, '') AS previous_phase
			FROM bloem_admin_live_activities
			WHERE pending_state IS NOT NULL AND (hold_until IS NULL OR hold_until <= $1) AND ($3 OR platform = 'android')
				AND expires_at > $1
				AND (pending_event = 'end' OR (pending_state->>'phase') IS DISTINCT FROM last_phase OR next_send_at IS NULL OR next_send_at <= $1)
			ORDER BY activity_id LIMIT $2 FOR UPDATE SKIP LOCKED)
		UPDATE bloem_admin_live_activities AS a SET
			last_timestamp = GREATEST(floor(extract(epoch FROM $1::timestamptz))::bigint, a.last_timestamp + 1),
			last_state_hash = md5((due.state - 'updatedAt')::text), last_phase = due.state->>'phase', last_sent_at = $1,
			pending_state = NULL, pending_event = NULL, hold_until = NULL, next_send_at = $1::timestamptz + $4::interval
		FROM due WHERE a.activity_id = due.activity_id
		RETURNING a.activity_id::text, a.device_id, a.kind, COALESCE(a.subject_id, ''), a.platform, COALESCE(a.token_ciphertext, ''),
			COALESCE(a.environment, ''), due.event, due.state, due.previous_phase, a.last_timestamp`, now, limit, appleOK, adminLiveCadence)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []liveClaim
	for rows.Next() {
		var c liveClaim
		var state []byte
		if err := rows.Scan(&c.ActivityID, &c.DeviceID, &c.Kind, &c.SubjectID, &c.Platform, &c.TokenCiphertext,
			&c.Environment, &c.Event, &state, &c.PreviousPhase, &c.Timestamp); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(state, &c.State); err != nil {
			return nil, err
		}
		c.High = c.Event == LiveEventEnd || c.State.Phase != c.PreviousPhase
		out = append(out, c)
	}
	return out, rows.Err()
}

// requeueLive puts a state that could not be sent back, to go at until, unless a newer
// send has gone since (the newer state already reached the activity) or a newer state
// is waiting (it replaces this one).
func (s *adminPushStore) requeueLive(ctx context.Context, c liveClaim, until time.Time) {
	state, err := json.Marshal(c.State)
	if err != nil {
		return
	}
	_, _ = s.pool.Exec(ctx, `UPDATE bloem_admin_live_activities SET
			pending_state = COALESCE(pending_state, $3::jsonb), pending_event = COALESCE(pending_event, $4),
			last_state_hash = NULL, last_phase = NULLIF($5, ''), hold_until = $6
		WHERE activity_id = $1 AND last_timestamp = $2`, c.ActivityID, c.Timestamp, string(state), c.Event, c.PreviousPhase, until)
}

func (s *adminPushStore) deleteLive(ctx context.Context, activityID string) {
	_, _ = s.pool.Exec(ctx, `DELETE FROM bloem_admin_live_activities WHERE activity_id = $1`, activityID)
}

func (s *adminPushStore) liveStartToken(ctx context.Context, deviceID string) (string, error) {
	var sealed *string
	err := s.pool.QueryRow(ctx, `SELECT live_start_token_ciphertext FROM bloem_admin_push_devices WHERE id = $1`, deviceID).Scan(&sealed)
	if errors.Is(err, pgx.ErrNoRows) || sealed == nil {
		return "", nil
	}
	return *sealed, err
}

func (s *adminPushStore) clearLiveStart(ctx context.Context, deviceID string) {
	_, _ = s.pool.Exec(ctx, `UPDATE bloem_admin_push_devices SET live_start_token_ciphertext = NULL, live_start_kinds = '{}' WHERE id = $1`, deviceID)
}
