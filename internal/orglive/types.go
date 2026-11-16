// Package orglive carries change signals for an organization's admin pages:
// which live streams, people and activity entries changed. A signal names a
// channel, an item id, a revision and a kind; the client re-reads the item
// through REST, which stays the truth (docs/design/bloem-org-live.md).
//
// Isolation does not rest on a filter. A Bus keeps connections registered
// under the organization their pass names, and Publish reaches only those. A
// change with no organization reaches nobody.
package orglive

import (
	"encoding/json"
	"strings"

	"github.com/google/uuid"
)

// Channel names a stream of change signals.
type Channel string

const (
	// ChannelStreams is the organization's live playback sessions starting,
	// stopping, pausing or resuming.
	ChannelStreams Channel = "org.streams"
	// ChannelPeople is members joining, leaving, or changing status or role.
	ChannelPeople Channel = "org.people"
	// ChannelActivity is new entries in the organization's activity list.
	ChannelActivity Channel = "org.activity"
)

// Channels is every channel this package carries, in the order the ticket
// route lists them.
var Channels = []Channel{ChannelStreams, ChannelPeople, ChannelActivity}

// ParseChannel returns the channel a client named, if there is one.
func ParseChannel(name string) (Channel, bool) {
	for _, channel := range Channels {
		if string(channel) == name {
			return channel, true
		}
	}
	return "", false
}

// Kind is what happened to an item.
type Kind string

const (
	KindCreated Kind = "created"
	KindUpdated Kind = "updated"
	KindDeleted Kind = "deleted"
)

func (k Kind) valid() bool { return k == KindCreated || k == KindUpdated || k == KindDeleted }

// Change is one change signal. ID is the item's identifier in the matching
// REST list, and Rev an opaque revision a client compares for inequality.
type Change struct {
	Channel Channel
	ID      string
	Rev     string
	Kind    Kind
}

// MaxIDLength bounds an item id and a revision, so a payload from the database
// can never grow a frame without limit.
const MaxIDLength = 128

// NotifyChannel is the Postgres channel the triggers notify on.
const NotifyChannel = "bloem_org_live"

// notification is the JSON the triggers send with pg_notify.
type notification struct {
	Channel string `json:"ch"`
	Org     string `json:"org"`
	ID      string `json:"id"`
	Rev     string `json:"rev"`
	Kind    string `json:"kind"`
}

// ParseNotification decodes a trigger's payload. It reports false for anything
// that is not a well-formed signal for a named organization: such a payload is
// dropped, never delivered to a guess.
func ParseNotification(payload string) (uuid.UUID, Change, bool) {
	var n notification
	if err := json.Unmarshal([]byte(payload), &n); err != nil {
		return uuid.Nil, Change{}, false
	}
	org, err := uuid.Parse(strings.TrimSpace(n.Org))
	if err != nil || org == uuid.Nil {
		return uuid.Nil, Change{}, false
	}
	channel, ok := ParseChannel(n.Channel)
	if !ok || channel == ChannelStreams {
		// Streams signals come from the playback lifecycle, not the database.
		return uuid.Nil, Change{}, false
	}
	change := Change{Channel: channel, ID: n.ID, Rev: n.Rev, Kind: Kind(n.Kind)}
	if change.ID == "" || len(change.ID) > MaxIDLength || len(change.Rev) > MaxIDLength || !change.Kind.valid() {
		return uuid.Nil, Change{}, false
	}
	return org, change, true
}
