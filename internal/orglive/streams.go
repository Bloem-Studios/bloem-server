package orglive

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/events"
)

// The playback lifecycle events on the hub's sessions channel. They are
// spelled out here because the handlers package imports this one; a test in
// that package pins them to the constants that publish them.
const (
	EventSessionStarted  = "session.started"
	EventSessionPaused   = "session.paused"
	EventSessionResumed  = "session.resumed"
	EventSessionStopped  = "session.stopped"
	EventSessionsChanged = "sessions.replaced"
)

const eventsSessions = events.ChannelSessions

// fingerprintDelay collects a burst of unattributed session changes into one
// look at the organizations' stream sets.
const fingerprintDelay = time.Second

// StreamOrganizations says which organization a live session counts toward.
type StreamOrganizations interface {
	// SessionOrganization returns the organization whose REST stream list holds
	// a session of this account and profile: the account is a member of it and
	// the profile, if the session names one, belongs to it. It returns
	// uuid.Nil, nil when there is none.
	SessionOrganization(ctx context.Context, accountID int, profileID string) (uuid.UUID, error)
}

// StreamFingerprints summarizes an organization's live stream set.
type StreamFingerprints interface {
	// StreamsFingerprint returns a string that differs whenever a stream
	// appears or disappears or changes between playing and paused, as the REST
	// stream list reports it.
	StreamsFingerprint(ctx context.Context, organization uuid.UUID) (string, error)
}

// StreamsBridge turns the playback lifecycle events on the shared hub into
// org.streams signals for the organization that owns the session, and only
// that one. Every node runs one: each sees every lifecycle event (its own and
// the ones relayed from other nodes) and signals the connections it holds.
type StreamsBridge struct {
	hub          *events.Hub
	bus          *Bus
	orgs         StreamOrganizations
	fingerprints StreamFingerprints
	logger       *slog.Logger
	delay        time.Duration

	last    map[uuid.UUID]string
	dropped uint64
	ready   chan struct{}
	once    sync.Once
}

// NewStreamsBridge returns a bridge. fingerprints may be nil, in which case a
// change that names no session is ignored.
func NewStreamsBridge(hub *events.Hub, bus *Bus, orgs StreamOrganizations, fingerprints StreamFingerprints, logger *slog.Logger) *StreamsBridge {
	if logger == nil {
		logger = slog.Default()
	}
	return &StreamsBridge{hub: hub, bus: bus, orgs: orgs, fingerprints: fingerprints, logger: logger, delay: fingerprintDelay, last: map[uuid.UUID]string{}, ready: make(chan struct{})}
}

// Start runs the bridge in the background the first time it is called, and does
// nothing after.
func (b *StreamsBridge) Start(ctx context.Context) {
	b.once.Do(func() { go b.Run(ctx) })
}

// Ready is closed once Run is reading the hub.
func (b *StreamsBridge) Ready() <-chan struct{} { return b.ready }

// lifecycle is the part of a lifecycle event's data the bridge reads. The
// session and history objects each carry the owning account and profile.
type lifecycle struct {
	SessionID string        `json:"session_id"`
	Seq       int64         `json:"seq"`
	Session   *sessionOwner `json:"session"`
	History   *sessionOwner `json:"history"`
}

type sessionOwner struct {
	UserID    json.RawMessage `json:"user_id"`
	ProfileID string          `json:"profile_id"`
}

func (o *sessionOwner) account() (int, bool) {
	if o == nil {
		return 0, false
	}
	raw := bytes.Trim(bytes.TrimSpace(o.UserID), `"`)
	id, err := strconv.Atoi(string(raw))
	return id, err == nil && id > 0
}

// Run reads the hub until ctx ends.
func (b *StreamsBridge) Run(ctx context.Context) {
	if b == nil || b.hub == nil || b.bus == nil {
		return
	}
	feed, unsubscribe := b.hub.Subscribe()
	defer unsubscribe()
	b.dropped = b.hub.Dropped(eventsSessions)
	close(b.ready)

	var timer *time.Timer
	var timerC <-chan time.Time
	arm := func() {
		if timer == nil {
			timer = time.NewTimer(b.delay)
			timerC = timer.C
		}
	}
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case env, ok := <-feed:
			if !ok {
				return
			}
			if env.Channel != eventsSessions {
				continue
			}
			if b.handle(ctx, env) {
				arm()
			}
			// The hub drops an envelope when this reader falls behind. A drop
			// is a lost signal, so the streams are compared instead.
			if dropped := b.hub.Dropped(eventsSessions); dropped != b.dropped {
				b.dropped = dropped
				arm()
			}
		case <-timerC:
			timer, timerC = nil, nil
			b.compare(ctx)
		}
	}
}

// handle signals the organization of a lifecycle event. It reports whether the
// event could not be attributed and the stream sets must be compared instead.
func (b *StreamsBridge) handle(ctx context.Context, env events.Envelope) (compare bool) {
	var kind Kind
	switch env.Event {
	case EventSessionStarted:
		kind = KindCreated
	case EventSessionPaused, EventSessionResumed:
		kind = KindUpdated
	case EventSessionStopped:
		kind = KindDeleted
	case EventSessionsChanged:
		return len(b.bus.SubscribedOrganizations(ChannelStreams)) > 0
	default:
		return false
	}
	if len(b.bus.SubscribedOrganizations(ChannelStreams)) == 0 {
		return false
	}
	var event lifecycle
	if err := json.Unmarshal(env.Data, &event); err != nil || event.SessionID == "" {
		return true
	}
	owner := event.Session
	if _, ok := owner.account(); !ok {
		owner = event.History
	}
	account, ok := owner.account()
	if !ok || b.orgs == nil {
		return true
	}
	lookup, cancel := context.WithTimeout(ctx, 2*time.Second)
	org, err := b.orgs.SessionOrganization(lookup, account, owner.ProfileID)
	cancel()
	if err != nil {
		b.logger.WarnContext(ctx, "organization live stream owner lookup failed", "component", "orglive", "error", err)
		return true
	}
	if org == uuid.Nil {
		// Not in any organization's stream list: nobody to tell.
		return false
	}
	b.bus.Publish(org, Change{Channel: ChannelStreams, ID: event.SessionID, Rev: strconv.FormatInt(event.Seq, 10), Kind: kind})
	return false
}

// compare looks at the stream set of every organization with a subscriber on
// this node and asks the ones whose set changed since the last look to re-read.
// It is the path for changes that name no session or no owner. The first look
// at an organization has nothing to compare with, so it asks for a re-read.
func (b *StreamsBridge) compare(ctx context.Context) {
	if b.fingerprints == nil {
		return
	}
	following := b.bus.SubscribedOrganizations(ChannelStreams)
	seen := make(map[uuid.UUID]bool, len(following))
	for _, org := range following {
		seen[org] = true
		lookup, cancel := context.WithTimeout(ctx, 5*time.Second)
		fingerprint, err := b.fingerprints.StreamsFingerprint(lookup, org)
		cancel()
		if err != nil {
			b.logger.WarnContext(ctx, "organization live stream comparison failed", "component", "orglive", "error", err)
			// Unknown is not unchanged.
			delete(b.last, org)
			b.bus.ResyncOrganization(org, ChannelStreams)
			continue
		}
		if previous, known := b.last[org]; !known || previous != fingerprint {
			b.last[org] = fingerprint
			b.bus.ResyncOrganization(org, ChannelStreams)
		}
	}
	for org := range b.last {
		if !seen[org] {
			delete(b.last, org)
		}
	}
}
