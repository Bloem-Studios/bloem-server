package orglive

import (
	"errors"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
)

// MaxConnectionsPerAccount is how many sockets one account may hold open for
// one organization on one node.
const MaxConnectionsPerAccount = 5

// MaxChannelsPerConnection bounds a connection's subscriptions.
const MaxChannelsPerConnection = 3

// ErrTooManyConnections is Register's answer when an account is at its limit.
var ErrTooManyConnections = errors.New("orglive: too many connections for this account")

// ErrNoOrganization is Register's answer for a connection with no organization.
var ErrNoOrganization = errors.New("orglive: connection names no organization")

// ErrFeedDown is Register's answer while the change feed is down.
var ErrFeedDown = errors.New("orglive: the change feed is down")

type registration struct {
	account int
	mailbox *Mailbox
}

// Bus delivers signals to the connections registered under an organization.
// It is the only way a signal reaches a connection.
type Bus struct {
	mu      sync.RWMutex
	orgs    map[uuid.UUID]map[*registration]struct{}
	healthy atomic.Bool
}

// NewBus returns an empty bus. It starts unhealthy: no signal source is
// attached until Run of the listener reports one.
func NewBus() *Bus {
	return &Bus{orgs: map[uuid.UUID]map[*registration]struct{}{}}
}

// Healthy reports whether the change feed is up. While it is not, new tickets
// and sockets are refused and open sockets are closed.
func (b *Bus) Healthy() bool { return b != nil && b.healthy.Load() }

// SetHealthy records the state of the change feed. Going down closes every
// connection; coming up after a gap, which the caller signals with ResyncAll,
// is the caller's job.
func (b *Bus) SetHealthy(up bool) {
	if b == nil {
		return
	}
	was := b.healthy.Swap(up)
	if was && !up {
		b.closeAll(ReasonFeedDown)
	}
}

// Shutdown closes every connection with ReasonShutdown and marks the feed down.
func (b *Bus) Shutdown() {
	if b == nil {
		return
	}
	b.healthy.Store(false)
	b.closeAll(ReasonShutdown)
}

func (b *Bus) closeAll(reason CloseReason) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, regs := range b.orgs {
		for reg := range regs {
			reg.mailbox.Close(reason)
		}
	}
}

// Register attaches a connection to an organization. The returned function
// detaches it and is safe to call more than once.
func (b *Bus) Register(org uuid.UUID, account int, mailbox *Mailbox) (func(), error) {
	if b == nil || mailbox == nil {
		return nil, errors.New("orglive: bus or mailbox missing")
	}
	if org == uuid.Nil {
		return nil, ErrNoOrganization
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	// Checked under the lock SetHealthy's closeAll takes: a connection either
	// registers before the feed drops and is closed with it, or is refused.
	if !b.healthy.Load() {
		return nil, ErrFeedDown
	}
	regs := b.orgs[org]
	if regs == nil {
		regs = map[*registration]struct{}{}
		b.orgs[org] = regs
	}
	held := 0
	for reg := range regs {
		if reg.account == account {
			held++
		}
	}
	if held >= MaxConnectionsPerAccount {
		if len(regs) == 0 {
			delete(b.orgs, org)
		}
		return nil, ErrTooManyConnections
	}
	reg := &registration{account: account, mailbox: mailbox}
	regs[reg] = struct{}{}
	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			if regs := b.orgs[org]; regs != nil {
				delete(regs, reg)
				if len(regs) == 0 {
					delete(b.orgs, org)
				}
			}
		})
	}, nil
}

// Publish delivers a change to the connections of one organization. A change
// with no organization is dropped.
func (b *Bus) Publish(org uuid.UUID, change Change) {
	if b == nil || org == uuid.Nil {
		return
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for reg := range b.orgs[org] {
		reg.mailbox.Push(change)
	}
}

// ResyncOrganization asks the connections of one organization to re-read a
// channel.
func (b *Bus) ResyncOrganization(org uuid.UUID, channel Channel) {
	if b == nil || org == uuid.Nil {
		return
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for reg := range b.orgs[org] {
		reg.mailbox.Resync(channel)
	}
}

// ResyncAll asks every connection to re-read every channel it follows. The
// listener calls it after a gap in the change feed.
func (b *Bus) ResyncAll() {
	if b == nil {
		return
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, regs := range b.orgs {
		for reg := range regs {
			reg.mailbox.ResyncAll()
		}
	}
}

// SubscribedOrganizations lists the organizations with at least one connection
// subscribed to the channel on this node.
func (b *Bus) SubscribedOrganizations(channel Channel) []uuid.UUID {
	if b == nil {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	var orgs []uuid.UUID
	for org, regs := range b.orgs {
		for reg := range regs {
			if reg.mailbox.Subscribed(channel) {
				orgs = append(orgs, org)
				break
			}
		}
	}
	return orgs
}
