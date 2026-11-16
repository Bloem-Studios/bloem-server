package orglive

import (
	"sync"
)

const (
	// DefaultMaxPending is how many distinct items may wait on one channel
	// before the mailbox gives up forwarding them one by one and asks the
	// client to re-read the list.
	DefaultMaxPending = 100
)

// CloseReason says why a mailbox was closed from outside its connection.
type CloseReason int

const (
	// ReasonNone: the mailbox is open.
	ReasonNone CloseReason = iota
	// ReasonFeedDown: the change feed went away; the connection closes 1013.
	ReasonFeedDown
	// ReasonShutdown: the server is stopping; the connection closes 1012.
	ReasonShutdown
)

// Frame is one thing a connection sends: a change, or a resync of a channel.
type Frame struct {
	Resync bool
	Change Change
	// Channel is set on both kinds, so a caller can switch on it.
	Channel Channel
}

type channelQueue struct {
	order  []string
	items  map[string]Change
	resync bool
}

// Mailbox collects the signals for one connection and coalesces them. A later
// signal for an item replaces an earlier one; too many distinct items become a
// resync. It never blocks a publisher and never drops silently: whatever it
// cannot hold becomes a resync.
type Mailbox struct {
	maxPending int

	mu     sync.Mutex
	subs   map[Channel]bool
	queues map[Channel]*channelQueue
	reason CloseReason

	wake chan struct{}
	done chan struct{}
}

// NewMailbox returns an empty mailbox. maxPending <= 0 uses DefaultMaxPending.
func NewMailbox(maxPending int) *Mailbox {
	if maxPending <= 0 {
		maxPending = DefaultMaxPending
	}
	return &Mailbox{
		maxPending: maxPending,
		subs:       map[Channel]bool{},
		queues:     map[Channel]*channelQueue{},
		wake:       make(chan struct{}, 1),
		done:       make(chan struct{}),
	}
}

// Wake receives one value whenever there may be frames to drain.
func (m *Mailbox) Wake() <-chan struct{} { return m.wake }

// Done is closed when the mailbox is closed from outside.
func (m *Mailbox) Done() <-chan struct{} { return m.done }

// Reason returns why Done closed, or ReasonNone.
func (m *Mailbox) Reason() CloseReason {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reason
}

// Close ends the mailbox. The first reason wins.
func (m *Mailbox) Close(reason CloseReason) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.reason != ReasonNone {
		return
	}
	m.reason = reason
	close(m.done)
}

func (m *Mailbox) signal() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// Subscribe adds a channel and queues a resync for it, so the client reads the
// list after the subscription is in force and no change falls between its
// first read and the subscription.
func (m *Mailbox) Subscribe(channel Channel) {
	m.mu.Lock()
	m.subs[channel] = true
	m.queueFor(channel).resync = true
	m.mu.Unlock()
	m.signal()
}

// Unsubscribe removes a channel and forgets what waited on it.
func (m *Mailbox) Unsubscribe(channel Channel) {
	m.mu.Lock()
	delete(m.subs, channel)
	delete(m.queues, channel)
	m.mu.Unlock()
}

// Subscribed reports whether the channel is subscribed.
func (m *Mailbox) Subscribed(channel Channel) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.subs[channel]
}

// SubscribedCount is how many channels are subscribed.
func (m *Mailbox) SubscribedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.subs)
}

func (m *Mailbox) queueFor(channel Channel) *channelQueue {
	q := m.queues[channel]
	if q == nil {
		q = &channelQueue{items: map[string]Change{}}
		m.queues[channel] = q
	}
	return q
}

// Push queues a change. A channel the connection did not subscribe to is
// ignored.
func (m *Mailbox) Push(change Change) {
	m.mu.Lock()
	if !m.subs[change.Channel] {
		m.mu.Unlock()
		return
	}
	q := m.queueFor(change.Channel)
	if q.resync {
		// The whole list is read again anyway.
		m.mu.Unlock()
		m.signal()
		return
	}
	if old, ok := q.items[change.ID]; ok {
		merged, keep := merge(old, change)
		if keep {
			q.items[change.ID] = merged
		} else {
			delete(q.items, change.ID)
			q.order = removeID(q.order, change.ID)
		}
	} else {
		q.items[change.ID] = change
		q.order = append(q.order, change.ID)
	}
	if len(q.items) > m.maxPending {
		q.resync = true
		q.items = map[string]Change{}
		q.order = nil
	}
	m.mu.Unlock()
	m.signal()
}

// Resync marks a subscribed channel as needing a re-read.
func (m *Mailbox) Resync(channel Channel) {
	m.mu.Lock()
	if !m.subs[channel] {
		m.mu.Unlock()
		return
	}
	q := m.queueFor(channel)
	q.resync = true
	q.items = map[string]Change{}
	q.order = nil
	m.mu.Unlock()
	m.signal()
}

// ResyncAll marks every subscribed channel as needing a re-read.
func (m *Mailbox) ResyncAll() {
	m.mu.Lock()
	for channel := range m.subs {
		q := m.queueFor(channel)
		q.resync = true
		q.items = map[string]Change{}
		q.order = nil
	}
	m.mu.Unlock()
	m.signal()
}

// Drain returns everything waiting, resyncs first, in channel order, then
// changes in the order their items first appeared. It empties the mailbox.
func (m *Mailbox) Drain() []Frame {
	m.mu.Lock()
	defer m.mu.Unlock()
	var frames []Frame
	for _, channel := range Channels {
		if q := m.queues[channel]; q != nil && q.resync {
			frames = append(frames, Frame{Resync: true, Channel: channel})
			q.resync = false
		}
	}
	for _, channel := range Channels {
		q := m.queues[channel]
		if q == nil {
			continue
		}
		for _, id := range q.order {
			frames = append(frames, Frame{Channel: channel, Change: q.items[id]})
		}
		q.items = map[string]Change{}
		q.order = nil
	}
	return frames
}

// merge combines a waiting change with a newer one for the same item. keep is
// false when the two cancel out: an item that appeared and vanished inside one
// window never existed as far as the client is concerned.
func merge(old, next Change) (merged Change, keep bool) {
	switch {
	case next.Kind == KindDeleted:
		if old.Kind == KindCreated {
			return Change{}, false
		}
		next.Kind = KindDeleted
	case old.Kind == KindCreated:
		next.Kind = KindCreated
	default:
		// updated+updated, updated+created, deleted+created, deleted+updated:
		// the item exists afterwards and the client already knows an older one.
		next.Kind = KindUpdated
	}
	return next, true
}

func removeID(order []string, id string) []string {
	for i, existing := range order {
		if existing == id {
			return append(order[:i], order[i+1:]...)
		}
	}
	return order
}
